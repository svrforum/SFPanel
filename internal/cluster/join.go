package cluster

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"github.com/svrforum/SFPanel/internal/auth"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// LiveActivateFunc starts Manager + gRPC server in-process after a successful join/init.
// If existingMgr is non-nil, it reuses that Manager instead of creating a new one
// (used by Init flow to avoid Raft shutdown/reopen race).
type LiveActivateFunc func(cfg *config.Config, cfgPath string, existingMgr *Manager) (*Manager, error)

// JoinEngine handles the full cluster join pipeline for both CLI and Web UI.
type JoinEngine struct {
	ConfigPath string
	Config     *config.Config
	DB         *sql.DB          // for updating admin credentials
	OnActivate LiveActivateFunc // nil = config-only mode (CLI without running server)
	// NodeID is the ID to join under: an agent promoted to a manager keeps
	// its own, with a token the leader made for it. Empty, a new one.
	NodeID string
	// Name is the node name to join under; empty, the host name. An agent
	// promoted to a manager keeps the one the cluster knows it by, as the
	// leader's record of it does.
	Name string
	// SkipPortCheck skips PreFlight's check that the cluster gRPC port is
	// free. On an agent being promoted, the agent's gRPC server holds it on
	// the advertise address; the manager binds it after the restart that
	// ends that server.
	SkipPortCheck bool
}

// adoptClusterAdmin makes the cluster admin this node's local admin account.
// It overwrites the single admin row by id, username included — the way the
// feature handler's syncClusterAdminToLocalDB does on leave and disband — and
// inserts one when the node has none.
//
// Matching by username, as this used to, left a local admin under any other
// name in place: a working login on this node whose tokens, now signed with
// the cluster key, were accepted on every node, outside the cluster's password
// and 2FA. Its recovery codes are cleared for the same reason; the cluster's
// codes live in the FSM.
func adoptClusterAdmin(db *sql.DB, username, passwordHash, totpSecret string) error {
	var totp interface{}
	if totpSecret != "" {
		totp = totpSecret // NULL keeps "2FA disabled" explicit
	}
	res, err := db.Exec(
		`UPDATE admin SET username = ?, password = ?, totp_secret = ?, recovery_codes = NULL WHERE id = (SELECT id FROM admin ORDER BY id LIMIT 1)`,
		username, passwordHash, totp,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = db.Exec(`INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, ?)`, username, passwordHash, totp)
	return err
}

// PreFlightResult contains the result of a pre-flight check.
type PreFlightResult struct {
	ClusterName   string
	NodeCount     int
	MaxNodes      int
	RecommendedIP string
	IPReason      string
}

// JoinResult contains the result of a successful join.
type JoinResult struct {
	ClusterName string
	NodeID      string
	NodeName    string
	Manager     *Manager // non-nil only if OnActivate was provided and succeeded
}

// PreFlight validates that a join can succeed before committing any changes.
func (e *JoinEngine) PreFlight(leaderAddr, token, pin string) (*PreFlightResult, error) {
	pin, err := NormalizeCAPin(pin)
	if err != nil {
		return nil, err
	}
	// Step 1: TCP connection test
	conn, err := net.DialTimeout("tcp", leaderAddr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot reach leader at %s: %w", leaderAddr, err)
	}
	conn.Close()

	// Step 2: gRPC PreFlight RPC, over a connection verified against the pin
	client, err := dialPinnedLeader(leaderAddr, pin)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	resp, err := client.PreFlight(context.Background(), token)
	if err != nil {
		return nil, fmt.Errorf("pre-flight request failed: %w", err)
	}
	if !resp.Valid {
		return nil, fmt.Errorf("%s", preFlightErrorMessage(resp.Error))
	}

	// Step 3: Local gRPC port check
	grpcPort := e.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = e.Config.Server.Port + 1
	}
	if !e.SkipPortCheck {
		if ln, lnErr := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort)); lnErr != nil {
			return nil, fmt.Errorf("gRPC port %d is already in use locally", grpcPort)
		} else {
			ln.Close()
		}
	}

	// Step 4: Detect advertise address
	recommendedIP, ipErr := DetectAdvertiseAddress(leaderAddr)
	ipReason := "detected from connection to leader"
	if ipErr != nil {
		recommendedIP = ""
		ipReason = fmt.Sprintf("auto-detection failed: %v", ipErr)
	} else {
		leaderHost, _, _ := net.SplitHostPort(leaderAddr)
		if IsTailscaleIP(net.ParseIP(leaderHost)) && IsTailscaleIP(net.ParseIP(recommendedIP)) {
			ipReason = "Tailscale network matches leader"
		} else if leaderHost != "" {
			ipReason = "same network as leader"
		}
	}

	return &PreFlightResult{
		ClusterName:   resp.ClusterName,
		NodeCount:     int(resp.NodeCount),
		MaxNodes:      int(resp.MaxNodes),
		RecommendedIP: recommendedIP,
		IPReason:      ipReason,
	}, nil
}

// dialPinnedLeader connects to the leader over a connection that verifies it
// against the pinned CA key. The key is learned first over an unverified
// connection — there is nothing to verify with yet — and asked for with no
// token, so the token travels only over the verified connection: a server in
// the middle that relays the real key never sees it.
func dialPinnedLeader(leaderAddr, pin string) (*GRPCClient, error) {
	insecure, err := DialNodeInsecure(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("leader is not responding to cluster requests at %s: %w", leaderAddr, err)
	}
	probe, err := insecure.PreFlight(context.Background(), "")
	insecure.Close()
	if err != nil {
		return nil, fmt.Errorf("pre-flight request failed: %w", err)
	}
	if err := checkPreFlightPin(probe, pin, leaderAddr); err != nil {
		return nil, err
	}
	client, err := DialNodePinned(leaderAddr, probe.CaPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to leader: %w", err)
	}
	return client, nil
}

// Execute runs the full join pipeline: gRPC join -> certs -> config -> live activate.
func (e *JoinEngine) Execute(leaderAddr, token, advertiseAddr, pin string) (*JoinResult, error) {
	pin, err := NormalizeCAPin(pin)
	if err != nil {
		return nil, err
	}
	if advertiseAddr == "" {
		detected, err := DetectAdvertiseAddress(leaderAddr)
		if err != nil {
			return nil, fmt.Errorf("cannot detect advertise address: %w", err)
		}
		advertiseAddr = detected
	}

	nodeID := e.NodeID
	if nodeID == "" {
		nodeID = uuid.New().String()
	}
	name := e.Name
	if name == "" {
		name, _ = os.Hostname()
	}

	grpcPort := e.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = e.Config.Server.Port + 1
	}
	apiAddr := fmt.Sprintf("%s:%d", advertiseAddr, e.Config.Server.Port)
	grpcAddr := fmt.Sprintf("%s:%d", advertiseAddr, grpcPort)

	slog.Info("joining cluster", "component", "cluster", "leader", leaderAddr, "advertise", advertiseAddr)

	// Step 1: reach the leader over a connection verified against the pin;
	// the token goes nowhere else.
	client, err := dialPinnedLeader(leaderAddr, pin)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	pf, err := client.PreFlight(context.Background(), token)
	if err != nil {
		return nil, fmt.Errorf("pre-flight request failed: %w", err)
	}
	if !pf.Valid {
		return nil, fmt.Errorf("%s", preFlightErrorMessage(pf.Error))
	}

	resp, err := client.Join(context.Background(), &pb.JoinRequest{
		Token:        token,
		NodeId:       nodeID,
		NodeName:     name,
		ApiAddress:   apiAddr,
		GrpcAddress:  grpcAddr,
		Capabilities: []string{CapAgents},
	})
	if err != nil {
		return nil, fmt.Errorf("join request failed: %w", err)
	}
	if !resp.Success {
		// An agent token passes PreFlight, which cannot tell which kind of
		// join it checks for, and is refused only here.
		return nil, fmt.Errorf("join rejected: %s", preFlightErrorMessage(resp.Error))
	}
	if block, _ := pem.Decode(resp.CaCert); block == nil {
		return nil, fmt.Errorf("join response carried no CA certificate")
	} else if ca, perr := x509.ParseCertificate(block.Bytes); perr != nil || CAPin(ca.RawSubjectPublicKeyInfo) != pin {
		return nil, fmt.Errorf("the CA in the join response is not the pinned one")
	}

	// Backup original config for rollback
	var originalConfig []byte
	if e.ConfigPath != "" {
		originalConfig, _ = os.ReadFile(e.ConfigPath)
	}

	certDir := e.Config.Cluster.CertDir
	if certDir == "" {
		certDir = DefaultCertDir
	}

	// Step 2: Save certs
	tlsMgr := NewTLSManager(certDir)
	if err := tlsMgr.SaveCACert(resp.CaCert); err != nil {
		return nil, fmt.Errorf("failed to save CA cert: %w", err)
	}
	if err := tlsMgr.SaveNodeCert(resp.NodeCert, resp.NodeKey); err != nil {
		_ = os.RemoveAll(certDir) // best-effort cleanup; the save error below is authoritative
		return nil, fmt.Errorf("failed to save node cert: %w", err)
	}

	// Step 3: Update config
	e.Config.Cluster.Enabled = true
	e.Config.Cluster.Name = resp.ClusterName
	e.Config.Cluster.NodeID = nodeID
	e.Config.Cluster.NodeName = name
	e.Config.Cluster.AdvertiseAddress = advertiseAddr
	e.Config.Cluster.GRPCPort = grpcPort
	e.Config.Cluster.RaftTLS = resp.RaftTls
	// A manager's config: an agent's role and managers list, kept, would boot
	// it as an agent again (IsAgent reads the role alone).
	e.Config.Cluster.Role = ""
	e.Config.Cluster.Managers = nil

	// The config carries the cluster's key from here so the file saved below
	// has it; the running panel switches to it only once the join commits.
	// Its generation comes with it: one kept from an earlier cluster would
	// stand above every rotation of this one. A leader older than the field
	// sends 0.
	priorAuth := e.Config.Auth
	if resp.JwtSecret != "" {
		e.Config.Auth.JWTSecret = resp.JwtSecret
		e.Config.Auth.JWTGeneration = resp.JwtGeneration
	}

	// Step 4: Save config atomically
	if e.ConfigPath != "" {
		data, err := yaml.Marshal(e.Config)
		if err != nil {
			e.rollbackJoin(certDir, originalConfig, priorAuth)
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		if err := config.AtomicWriteFile(e.ConfigPath, data, 0600); err != nil {
			e.rollbackJoin(certDir, originalConfig, priorAuth)
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	// Step 5: Live activate (if callback provided)
	var mgr *Manager
	if e.OnActivate != nil {
		e.Config.Cluster.APIPort = e.Config.Server.Port
		mgr, err = e.OnActivate(e.Config, e.ConfigPath, nil)
		if err != nil {
			e.rollbackJoin(certDir, originalConfig, priorAuth)
			return nil, fmt.Errorf("live activation failed: %w", err)
		}
	}

	// The join has committed: sign and check tokens with the cluster's key from
	// now on. Published here and not where the config field changes, so a join
	// that fails above leaves every signer and verifier on the key it had.
	if resp.JwtSecret != "" {
		auth.SetJWTSecret(resp.JwtSecret)
	}

	// Step 6: Update admin credentials from leader (after LiveActivate so rollback is clean).
	// Also sync the TOTP secret so the follower's local-DB login fallback
	// can enforce 2FA before Raft replication lands the admin account.
	if e.DB != nil && resp.AdminUsername != "" && resp.AdminPasswordHash != "" {
		if err := adoptClusterAdmin(e.DB, resp.AdminUsername, resp.AdminPasswordHash, resp.AdminTotpSecret); err != nil {
			slog.Warn("failed to sync admin credentials from leader", "error", err)
		}
	}

	slog.Info("cluster join successful", "component", "cluster",
		"cluster_name", resp.ClusterName, "node_id", nodeID, "live", mgr != nil)

	return &JoinResult{
		ClusterName: resp.ClusterName,
		NodeID:      nodeID,
		NodeName:    name,
		Manager:     mgr,
	}, nil
}

// rollbackJoin cleans up certs and restores original config on failure —
// on disk, and the token key and its generation in memory, which the join had
// already replaced with the cluster's and which cluster init would otherwise
// hand to a new cluster as this node's own.
func (e *JoinEngine) rollbackJoin(certDir string, originalConfig []byte, prior config.AuthConfig) {
	if err := os.RemoveAll(certDir); err != nil {
		slog.Warn("join rollback: failed to remove cert dir",
			"component", "cluster", "dir", certDir, "error", err)
	}
	if e.ConfigPath != "" && originalConfig != nil {
		// Restoring the original config is the load-bearing step: a silent
		// failure here leaves a half-written cluster config that would make
		// the node try to join on next boot. Surface it loudly.
		if err := os.WriteFile(e.ConfigPath, originalConfig, 0600); err != nil {
			slog.Error("join rollback: FAILED to restore original config — manual recovery may be required",
				"component", "cluster", "path", e.ConfigPath, "error", err)
		}
	}
	e.Config.Cluster.Enabled = false
	e.Config.Auth = prior
	slog.Warn("join rolled back", "component", "cluster")
}

// preFlightErrorMessage maps internal error strings to user-friendly messages.
// It words the leader's refusals of a manager join — PreFlight's and Join's —
// so ErrTokenKind here means the token is an agent's.
func preFlightErrorMessage(errStr string) string {
	switch errStr {
	case ErrTokenNotFound.Error():
		// Tokens are kept by the node that created them, not replicated, so
		// after a leader change a correctly pasted token is also "not found".
		return "token does not exist on this leader — check for typos, or create a new one if the leader has changed"
	case ErrTokenExpired.Error():
		return "token has expired — create a new one on the leader"
	case ErrTokenUsed.Error():
		return "token has already been used"
	case ErrNotLeader.Error():
		return "node is not the cluster leader"
	case ErrMaxNodesReached.Error():
		return "cluster has reached maximum node count"
	case ErrTokenKind.Error():
		return "this token is for an agent — run 'sfpanel agent join' with it, or create a manager token"
	default:
		return errStr
	}
}
