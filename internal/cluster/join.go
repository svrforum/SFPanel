package cluster

import (
	"context"
	"database/sql"
	"fmt"
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
type LiveActivateFunc func(cfg *config.Config, cfgPath string) (*Manager, error)

// JoinEngine handles the full cluster join pipeline for both CLI and Web UI.
type JoinEngine struct {
	ConfigPath string
	Config     *config.Config
	DB         *sql.DB          // for updating admin credentials
	OnActivate LiveActivateFunc // nil = config-only mode (CLI without running server)
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
func (e *JoinEngine) PreFlight(leaderAddr, token string) (*PreFlightResult, error) {
	// Step 1: TCP connection test
	conn, err := net.DialTimeout("tcp", leaderAddr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot reach leader at %s: %w", leaderAddr, err)
	}
	conn.Close()

	// Step 2: gRPC PreFlight RPC
	client, err := DialNodeInsecure(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("leader is not responding to cluster requests at %s: %w", leaderAddr, err)
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
	if ln, lnErr := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort)); lnErr != nil {
		return nil, fmt.Errorf("gRPC port %d is already in use locally", grpcPort)
	} else {
		ln.Close()
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

// Execute runs the full join pipeline: gRPC join -> certs -> config -> live activate.
func (e *JoinEngine) Execute(leaderAddr, token, advertiseAddr string) (*JoinResult, error) {
	if advertiseAddr == "" {
		detected, err := DetectAdvertiseAddress(leaderAddr)
		if err != nil {
			return nil, fmt.Errorf("cannot detect advertise address: %w", err)
		}
		advertiseAddr = detected
	}

	nodeID := uuid.New().String()
	hostname, _ := os.Hostname()

	grpcPort := e.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = e.Config.Server.Port + 1
	}
	apiAddr := fmt.Sprintf("%s:%d", advertiseAddr, e.Config.Server.Port)
	grpcAddr := fmt.Sprintf("%s:%d", advertiseAddr, grpcPort)

	slog.Info("joining cluster", "component", "cluster", "leader", leaderAddr, "advertise", advertiseAddr)

	// Step 1: gRPC Join RPC
	client, err := DialNodeInsecure(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to leader: %w", err)
	}
	defer client.Close()

	resp, err := client.Join(context.Background(), &pb.JoinRequest{
		Token:       token,
		NodeId:      nodeID,
		NodeName:    hostname,
		ApiAddress:  apiAddr,
		GrpcAddress: grpcAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("join request failed: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("join rejected: %s", resp.Error)
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
		os.RemoveAll(certDir)
		return nil, fmt.Errorf("failed to save node cert: %w", err)
	}

	// Step 3: Update config
	e.Config.Cluster.Enabled = true
	e.Config.Cluster.Name = resp.ClusterName
	e.Config.Cluster.NodeID = nodeID
	e.Config.Cluster.NodeName = hostname
	e.Config.Cluster.AdvertiseAddress = advertiseAddr
	e.Config.Cluster.GRPCPort = grpcPort

	if resp.JwtSecret != "" {
		e.Config.Auth.JWTSecret = resp.JwtSecret
	}

	// Step 4: Save config atomically
	if e.ConfigPath != "" {
		data, err := yaml.Marshal(e.Config)
		if err != nil {
			e.rollbackJoin(certDir, originalConfig)
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		if err := config.AtomicWriteFile(e.ConfigPath, data, 0600); err != nil {
			e.rollbackJoin(certDir, originalConfig)
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	// Step 5: Update admin credentials from leader
	if e.DB != nil && resp.AdminUsername != "" && resp.AdminPasswordHash != "" {
		_, err := e.DB.Exec(
			"UPDATE admin SET password = ? WHERE username = ?",
			resp.AdminPasswordHash, resp.AdminUsername,
		)
		if err != nil {
			slog.Warn("failed to sync admin credentials from leader", "error", err)
		}
	}

	// Step 6: Live activate (if callback provided)
	var mgr *Manager
	if e.OnActivate != nil {
		e.Config.Cluster.APIPort = e.Config.Server.Port
		mgr, err = e.OnActivate(e.Config, e.ConfigPath)
		if err != nil {
			e.rollbackJoin(certDir, originalConfig)
			return nil, fmt.Errorf("live activation failed: %w", err)
		}
	}

	slog.Info("cluster join successful", "component", "cluster",
		"cluster_name", resp.ClusterName, "node_id", nodeID, "live", mgr != nil)

	return &JoinResult{
		ClusterName: resp.ClusterName,
		NodeID:      nodeID,
		NodeName:    hostname,
		Manager:     mgr,
	}, nil
}

// rollbackJoin cleans up certs and restores original config on failure.
func (e *JoinEngine) rollbackJoin(certDir string, originalConfig []byte) {
	os.RemoveAll(certDir)
	if e.ConfigPath != "" && originalConfig != nil {
		os.WriteFile(e.ConfigPath, originalConfig, 0600)
	}
	e.Config.Cluster.Enabled = false
	slog.Warn("join rolled back", "component", "cluster")
}

// preFlightErrorMessage maps internal error strings to user-friendly messages.
func preFlightErrorMessage(errStr string) string {
	switch errStr {
	case ErrTokenNotFound.Error():
		return "token does not exist — check for typos"
	case ErrTokenExpired.Error():
		return "token has expired — create a new one on the leader"
	case ErrTokenUsed.Error():
		return "token has already been used"
	case ErrNotLeader.Error():
		return "node is not the cluster leader"
	case ErrMaxNodesReached.Error():
		return "cluster has reached maximum node count"
	default:
		return errStr
	}
}
