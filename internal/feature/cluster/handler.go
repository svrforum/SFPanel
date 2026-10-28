package featurecluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/paneltls"
	"github.com/svrforum/SFPanel/internal/release"
	"gopkg.in/yaml.v3"
)

// clusterErrResponse maps cluster errors to appropriate HTTP status codes.
func clusterErrResponse(c echo.Context, err error) error {
	switch {
	case errors.Is(err, cluster.ErrNotLeader):
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "This node is not the cluster leader")
	case errors.Is(err, cluster.ErrNodeNotFound):
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Node not found")
	case errors.Is(err, cluster.ErrSelfRemove):
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cannot remove self from cluster")
	case errors.Is(err, cluster.ErrNodeAlreadyExists):
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "Node already exists")
	case errors.Is(err, cluster.ErrMaxNodesReached):
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Maximum node count reached")
	case errors.Is(err, cluster.ErrMaxAgentsReached):
		return response.Fail(c, http.StatusBadRequest, response.ErrClusterMaxAgents, err.Error())
	case errors.Is(err, cluster.ErrTokenKind):
		return response.Fail(c, http.StatusBadRequest, response.ErrClusterTokenKind, err.Error())
	case errors.Is(err, cluster.ErrTokenNotFound), errors.Is(err, cluster.ErrTokenExpired), errors.Is(err, cluster.ErrTokenUsed):
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, err.Error())
	default:
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}
}

// Leader-proxy budget for the read endpoints the UI polls on a timer (status,
// nodes, overview) — the only callers of this handler's own leader proxy. A
// healthy LAN peer answers in single-digit milliseconds, so the budget only
// ever matters when the leader is unreachable, and it used to be the 10s an
// FSM write would want, paid in full by every 15s poll cycle.
//
// FSM writes do not come through here: they use middleware.ProxyToLeader.
const (
	leaderReadTimeout = 5 * time.Second
	// leaderReadCooldown suppresses repeat dials after a read-path failure so
	// a burst of sidebar polls costs one dial, not one per request. Short
	// enough that a leader coming back is noticed within a poll interval.
	leaderReadCooldown = 5 * time.Second
)

type Handler struct {
	mu           sync.RWMutex
	joiningMu    sync.Mutex // prevents concurrent Init/Join
	configMu     sync.Mutex // protects h.Config.Cluster field writes
	disbandOnce  sync.Once  // guards performDisband so replicated CmdDisband can't fire twice
	Manager      *cluster.Manager
	Config       *config.Config
	ConfigPath   string
	DB           *sql.DB
	LiveActivate cluster.LiveActivateFunc
	// Version is this node's own version: a cluster update knows its own,
	// whatever its heartbeats say.
	Version string
	// OnManagerActivated is called after a manager is set (init/join).
	// Used to propagate the manager to other handlers (e.g. auth).
	OnManagerActivated func(*cluster.Manager)

	// Read-path leader-proxy breaker. See proxyToLeaderRead.
	leaderReadMu   sync.Mutex
	leaderReadAddr string
	leaderReadFail time.Time

	// updateRunning admits one cluster update at a time. See ClusterUpdate.
	updateRunning atomic.Bool
}

func (h *Handler) getManager() *cluster.Manager {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Manager
}

// GetManager is the exported accessor used by the API router's middleware
// layer so it can resolve the live cluster manager on every request instead
// of capturing a (possibly nil) pointer at startup.
func (h *Handler) GetManager() *cluster.Manager {
	return h.getManager()
}

func (h *Handler) setManager(m *cluster.Manager) {
	h.mu.Lock()
	h.Manager = m
	cb := h.OnManagerActivated
	h.mu.Unlock()
	if cb != nil {
		cb(m)
	}
	if m != nil {
		// L-04: Register the disband callback so this node self-cleans when
		// any leader broadcasts CmdDisband through the Raft log. sync.Once
		// inside performDisband guards against duplicate invocation in the
		// unlikely event of log replay firing twice.
		m.SetOnDisband(h.performDisband)
	}
}

// ActivateBootManager wires the manager-dependent callbacks (currently the
// disband self-clean) for a Manager that was injected at construction time via
// the boot path. The runtime init/join paths register these through
// setManager; the boot path builds the Handler with a struct literal
// (router.go) and so must call this explicitly. Without it, a node that boots
// straight into an existing cluster never registers performDisband, so a
// replicated CmdDisband silently no-ops and "cluster disband" appears to do
// nothing after any restart. Nil-safe and idempotent.
func (h *Handler) ActivateBootManager() {
	if m := h.getManager(); m != nil {
		m.SetOnDisband(h.performDisband)
	}
}

// performDisband is the node-local cleanup fired by CmdDisband replication.
// Runs on both the leader (who initiated) and every follower. Wipes cluster
// material, flips config.Enabled=false, and exits so the supervisor restarts
// the node in standalone mode. Guarded by sync.Once — safe to invoke from
// multiple replication paths.
func (h *Handler) performDisband(fromNodeID string) {
	h.disbandOnce.Do(func() {
		slog.Info("performing cluster disband from replicated CmdDisband", "component", "cluster", "initiator", fromNodeID)
		h.configMu.Lock()
		dataDir := h.Config.Cluster.DataDir
		certDir := h.Config.Cluster.CertDir
		h.configMu.Unlock()

		if mgr := h.getManager(); mgr != nil {
			// Sync the cluster admin down to the local DB BEFORE shutting the
			// manager (which abandons the FSM) — otherwise the standalone restart
			// falls back to a stale pre-cluster local account.
			h.syncClusterAdminToLocalDB(mgr)
			mgr.Shutdown()
		}

		if dataDir != "" {
			if rmErr := os.RemoveAll(dataDir); rmErr != nil {
				slog.Warn("disband: failed to remove data dir", "path", dataDir, "error", rmErr)
			}
		}
		if certDir != "" {
			if rmErr := os.RemoveAll(certDir); rmErr != nil {
				slog.Warn("disband: failed to remove cert dir", "path", certDir, "error", rmErr)
			}
		}

		h.configMu.Lock()
		h.Config.Cluster.Enabled = false
		data, err := yaml.Marshal(h.Config)
		h.configMu.Unlock()
		if err == nil && h.ConfigPath != "" {
			if wErr := config.AtomicWriteFile(h.ConfigPath, data, 0600); wErr != nil {
				slog.Error("disband: failed to persist standalone config", "error", wErr)
			}
		}

		// Give the HTTP response (if any) time to flush, then exit so
		// systemd restarts us in standalone mode. Exit 1 per the project
		// convention — Restart=always needs a non-zero code path.
		time.Sleep(2 * time.Second)
		slog.Info("disband: exiting to restart in standalone mode", "component", "cluster")
		os.Exit(1)
	})
}

// syncClusterAdminToLocalDB writes the cluster (FSM) admin account down into the
// local SQLite admin table. Account state replicates one-way at init (local DB →
// FSM via SyncAccountFromDB) but, while clustered, all auth changes (password,
// TOTP, recovery codes) go only to the FSM. When a node drops back to standalone
// (disband / leave) it then authenticates against the local DB again — which
// still holds the stale pre-cluster row. Calling this just before the FSM is
// abandoned closes that gap so the credentials actually in use cluster-wide
// survive the transition. Single-admin app: the one local row is overwritten to
// match the cluster admin (the username may differ, e.g. a local "admin2" vs
// cluster "admin"). Best-effort: a failure is logged, not fatal.
func (h *Handler) syncClusterAdminToLocalDB(mgr *cluster.Manager) {
	if mgr == nil || h.DB == nil {
		return
	}
	_, user, passHash, totp := mgr.GetJWTAndAdminFull()
	if user == "" || passHash == "" {
		return // no cluster admin to sync
	}
	var totpVal interface{}
	if totp != "" {
		totpVal = totp
	}
	var recoveryVal interface{}
	if codes := mgr.GetRecoveryCodes(user); len(codes) > 0 {
		if b, mErr := json.Marshal(codes); mErr == nil {
			recoveryVal = string(b)
		}
	}
	res, err := h.DB.Exec(
		`UPDATE admin SET username=?, password=?, totp_secret=?, recovery_codes=? WHERE id=(SELECT id FROM admin ORDER BY id LIMIT 1)`,
		user, passHash, totpVal, recoveryVal)
	if err != nil {
		slog.Error("cluster→local admin sync failed before standalone transition", "component", "cluster", "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// No local row (a node that only ever knew the cluster admin) — insert one.
		if _, iErr := h.DB.Exec(
			`INSERT INTO admin (username, password, totp_secret, recovery_codes) VALUES (?,?,?,?)`,
			user, passHash, totpVal, recoveryVal); iErr != nil {
			slog.Error("cluster→local admin insert failed before standalone transition", "component", "cluster", "error", iErr)
			return
		}
	}
	slog.Info("synced cluster admin to local DB before standalone transition", "component", "cluster", "username", user)
}

// rollbackInit cleans up a partially-initialised cluster after a post-Init
// failure: shuts the manager down, removes the on-disk Raft/TLS material,
// forces config back to Enabled=false (in memory + on disk, best-effort), and
// returns a 500. Keeping config Enabled=false means the supervisor restarts
// the node cleanly in standalone mode rather than as a half-formed member.
//
// mgr may be nil — Shutdown is invoked only when it isn't (Manager.Shutdown
// dereferences struct fields without a receiver-nil guard).
func (h *Handler) rollbackInit(c echo.Context, mgr *cluster.Manager, err error) error {
	slog.Error("cluster init rolled back", "component", "cluster", "error", err)

	if mgr != nil {
		mgr.Shutdown()
	}

	h.configMu.Lock()
	dataDir := h.Config.Cluster.DataDir
	certDir := h.Config.Cluster.CertDir
	h.configMu.Unlock()

	// Removing the Raft/TLS material matters for a clean retry: a leftover
	// DataDir trips ErrCantBootstrap on the next init (see L-01). Log on
	// failure rather than swallow, mirroring performDisband.
	if dataDir != "" {
		if rmErr := os.RemoveAll(dataDir); rmErr != nil {
			slog.Warn("cluster init rollback: failed to remove data dir", "component", "cluster", "path", dataDir, "error", rmErr)
		}
	}
	if certDir != "" {
		if rmErr := os.RemoveAll(certDir); rmErr != nil {
			slog.Warn("cluster init rollback: failed to remove cert dir", "component", "cluster", "path", certDir, "error", rmErr)
		}
	}

	// Force config back to standalone (in memory + on disk, best-effort).
	h.configMu.Lock()
	h.Config.Cluster.Enabled = false
	data, marshalErr := yaml.Marshal(h.Config)
	h.configMu.Unlock()
	if marshalErr != nil {
		slog.Warn("cluster init rollback: failed to marshal standalone config", "component", "cluster", "error", marshalErr)
	} else if h.ConfigPath != "" {
		if wErr := config.AtomicWriteFile(h.ConfigPath, data, 0600); wErr != nil {
			slog.Warn("cluster init rollback: failed to persist standalone config", "component", "cluster", "error", wErr)
		}
	}

	return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Cluster init failed: "+err.Error())
}

func (h *Handler) InitCluster(c echo.Context) error {
	// Prevent concurrent init/join operations
	if !h.joiningMu.TryLock() {
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, "Another cluster operation is in progress")
	}
	defer h.joiningMu.Unlock()

	if h.getManager() != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Already part of a cluster")
	}
	if h.memberNotRunning() {
		return response.Fail(c, http.StatusConflict, response.ErrClusterNotRunning, memberNotRunningMsg)
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}

	var body struct {
		Name             string `json:"name"`
		AdvertiseAddress string `json:"advertise_address"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	clusterName := body.Name
	if clusterName == "" {
		clusterName = "sfpanel"
	}

	advertise := body.AdvertiseAddress
	if advertise == "" {
		advertise = cluster.DetectFallbackIP()
	}
	if advertise == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cannot detect advertise address. Please provide one.")
	}

	h.configMu.Lock()
	h.Config.Cluster.AdvertiseAddress = advertise

	grpcPort := h.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = h.Config.Server.Port + 1
		h.Config.Cluster.GRPCPort = grpcPort
	}

	h.Config.Cluster.APIPort = h.Config.Server.Port
	// Hand NewManager a *copy* of the cluster config so Manager can mutate
	// its own state without racing other handlers (GetStatus, etc.) that
	// read h.Config.Cluster under configMu.
	cfgCopy := h.Config.Cluster
	h.configMu.Unlock()
	mgr := cluster.NewManager(&cfgCopy)
	if err := mgr.Init(clusterName); err != nil {
		// L-01: Init may have written CA, node cert, or Raft data to disk
		// before failing. Clean those up so a retry starts from a clean slate;
		// otherwise NewRaftNode hits ErrCantBootstrap on existing BoltDB and
		// the CA ends up orphaned in /etc/sfpanel/cluster.
		mgr.Shutdown()
		os.RemoveAll(cfgCopy.DataDir)
		os.RemoveAll(cfgCopy.CertDir)
		// Reset in-memory cluster config so a retry picks a fresh NodeID.
		// Without this, the handler retains cfgCopy.NodeID assigned by
		// mgr.Init (uuid.New on first call) and the next retry reuses it,
		// which can collide with stale Raft state and keep failing.
		h.configMu.Lock()
		h.Config.Cluster = config.ClusterConfig{
			GRPCPort: cfgCopy.GRPCPort,
			DataDir:  cfgCopy.DataDir,
			CertDir:  cfgCopy.CertDir,
		}
		h.configMu.Unlock()
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Init failed: %v", err))
	}

	h.configMu.Lock()
	h.Config.Cluster = *mgr.GetConfig()
	h.configMu.Unlock()

	// L-02: Write jwt_secret + admin to FSM *before* persisting config.
	// If the process dies between the config file write and these Raft
	// applies, the node reboots with Enabled=true but an empty FSM,
	// breaking auth. Doing them first makes the Raft log durable in the
	// happy path; a crash before the config write simply means Enabled=false
	// on reboot (clean state).
	if h.Config.Auth.JWTSecret != "" {
		if err := mgr.SetConfig("jwt_secret", h.Config.Auth.JWTSecret); err != nil {
			return h.rollbackInit(c, mgr, fmt.Errorf("persist jwt secret: %w", err))
		}
	}
	if err := mgr.SetConfig("raft_tls", "true"); err != nil {
		return h.rollbackInit(c, mgr, fmt.Errorf("persist raft_tls flag: %w", err))
	}
	if h.DB != nil {
		// Replicate the admin, with its 2FA recovery codes, to the FSM. A
		// failure here leaves the cluster with a jwt_secret but no admin
		// account — every node would trust the JWT secret with nothing to
		// authenticate against — so roll back rather than persist a
		// half-initialized cluster.
		if _, err := mgr.SeedAdminFromDB(h.DB); err != nil {
			return h.rollbackInit(c, mgr, fmt.Errorf("sync admin account to FSM: %w", err))
		}
	}

	// Save config. rollbackInit resets Enabled=false in memory AND on disk and
	// tears down the manager — the previous hand-rolled cleanup left
	// Enabled=true in memory with no running manager (inconsistent state).
	data, err := yaml.Marshal(h.Config)
	if err != nil {
		return h.rollbackInit(c, mgr, fmt.Errorf("marshal cluster config: %w", err))
	}
	if err := config.AtomicWriteFile(h.ConfigPath, data, 0600); err != nil {
		return h.rollbackInit(c, mgr, fmt.Errorf("save cluster config: %w", err))
	}

	// Live activate — pass existing manager to avoid Raft shutdown/reopen race
	if h.LiveActivate != nil {
		newMgr, err := h.LiveActivate(h.Config, h.ConfigPath, mgr)
		if err != nil {
			mgr.Shutdown()
			slog.Error("live activation failed after init", "error", err)
			return response.OK(c, map[string]interface{}{
				"message": "Cluster initialized but live activation failed. Restart required.",
				"name":    clusterName,
				"node_id": h.Config.Cluster.NodeID,
				"live":    false,
			})
		}
		h.setManager(newMgr)
	} else {
		mgr.Shutdown()
	}

	slog.Info("cluster initialized via UI", "component", "cluster", "name", clusterName)

	return response.OK(c, map[string]interface{}{
		"message": "Cluster initialized successfully",
		"name":    clusterName,
		"node_id": h.Config.Cluster.NodeID,
		"live":    h.getManager() != nil,
	})
}

func (h *Handler) JoinCluster(c echo.Context) error {
	// Prevent concurrent init/join operations
	if !h.joiningMu.TryLock() {
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, "Another cluster operation is in progress")
	}
	defer h.joiningMu.Unlock()

	if h.getManager() != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Already part of a cluster")
	}
	if h.memberNotRunning() {
		return response.Fail(c, http.StatusConflict, response.ErrClusterNotRunning, memberNotRunningMsg)
	}

	var body struct {
		LeaderAddress    string `json:"leader_address"`
		Token            string `json:"token"`
		CAPin            string `json:"ca_pin"`
		AdvertiseAddress string `json:"advertise_address"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if body.LeaderAddress == "" || body.Token == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "leader_address and token are required")
	}
	pin, err := cluster.NormalizeCAPin(body.CAPin)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			"ca_pin is required: copy the join command from the leader's token page ("+err.Error()+")")
	}

	engine := &cluster.JoinEngine{
		ConfigPath: h.ConfigPath,
		Config:     h.Config,
		DB:         h.DB,
		OnActivate: h.LiveActivate,
	}

	// Pre-flight check
	pfResult, err := engine.PreFlight(body.LeaderAddress, body.Token, pin)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, err.Error())
	}

	advertise := body.AdvertiseAddress
	if advertise == "" {
		advertise = pfResult.RecommendedIP
	}

	// Execute join
	result, err := engine.Execute(body.LeaderAddress, body.Token, advertise, pin)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	// Update handler's Manager pointer for subsequent requests
	if result.Manager != nil {
		h.setManager(result.Manager)
	}

	return response.OK(c, map[string]interface{}{
		"message":      "Joined cluster successfully",
		"cluster_name": result.ClusterName,
		"node_id":      result.NodeID,
		"live":         result.Manager != nil,
	})
}

func (h *Handler) GetNetworkInterfaces(c echo.Context) error {
	type ifaceInfo struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	var result []ifaceInfo
	ifaces, err := net.Interfaces()
	if err != nil {
		return response.OK(c, map[string]interface{}{"interfaces": []interface{}{}, "recommended": "", "reason": ""})
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			result = append(result, ifaceInfo{
				Name:    iface.Name,
				Address: ip.String(),
			})
		}
	}

	recommended := ""
	reason := ""
	leaderAddr := c.QueryParam("leader_addr")
	if leaderAddr != "" {
		if ip, err := cluster.DetectAdvertiseAddress(leaderAddr); err == nil {
			recommended = ip
			leaderHost, _, _ := net.SplitHostPort(leaderAddr)
			if cluster.IsTailscaleIP(net.ParseIP(leaderHost)) {
				reason = "Tailscale network matches leader"
			} else {
				reason = "same network as leader"
			}
		}
	}

	return response.OK(c, map[string]interface{}{
		"interfaces":  result,
		"recommended": recommended,
		"reason":      reason,
	})
}

func (h *Handler) GetOverview(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.OK(c, map[string]interface{}{
			"name": "", "node_count": 0, "leader_id": "",
			"nodes": []interface{}{}, "metrics": []interface{}{},
		})
	}

	if !mgr.IsLeader() {
		// L-06: only the leader applies CmdUpdateNode, so follower-local
		// FSM status can be stale by many heartbeat ticks. Prefer an
		// explicit 503 over silently serving stale data — the frontend can
		// retry or surface a "leader unreachable" banner.
		resp, err := h.proxyToLeaderRead(c)
		if err != nil {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, fmt.Sprintf("leader unreachable: %v", err))
		}
		return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
	}

	// Confirm leadership of quorum before serving — without VerifyLeader a
	// partitioned-but-still-thinks-it's-leader node cheerfully returns
	// stale data with no indication. Same pattern as GetStatus.
	stale := false
	if err := mgr.GetRaft().VerifyLeader(2 * time.Second); err != nil {
		stale = true
	}

	overview := mgr.GetOverview()
	if overview == nil {
		return response.OK(c, map[string]interface{}{
			"name": "", "node_count": 0, "leader_id": "",
			"nodes": []interface{}{}, "metrics": []interface{}{},
			"stale": stale,
		})
	}
	return response.OK(c, map[string]interface{}{
		"name":       overview.Name,
		"node_count": overview.NodeCount,
		"leader_id":  overview.LeaderID,
		"nodes":      overview.Nodes,
		"metrics":    overview.Metrics,
		"stale":      stale,
	})
}

func (h *Handler) GetNodes(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.OK(c, map[string]interface{}{
			"nodes": []interface{}{}, "local_id": "", "is_leader": false,
		})
	}

	if !mgr.IsLeader() {
		// L-06: see GetOverview — follower-local status is stale; refuse
		// to answer without leader confirmation.
		resp, err := h.proxyToLeaderRead(c)
		if err != nil {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, fmt.Sprintf("leader unreachable: %v", err))
		}
		return h.returnWithLocalID(c, resp)
	}

	stale := false
	if err := mgr.GetRaft().VerifyLeader(2 * time.Second); err != nil {
		stale = true
	}
	nodes := mgr.GetNodes()
	return response.OK(c, map[string]interface{}{
		"nodes":     nodes,
		"local_id":  mgr.LocalNodeID(),
		"is_leader": mgr.IsLeader(),
		"stale":     stale,
	})
}

// memberNotRunningMsg is the fallback text for ErrClusterNotRunning.
const memberNotRunningMsg = "This node is a cluster member but its cluster service did not start. Restart the panel and check its log."

// memberNotRunning reports a node whose config says it belongs to a cluster
// while no manager is running: the cluster start failed at boot and the panel
// came up standalone. Init and join must refuse there — Init's failure path
// deleted the Raft and certificate folders the node still depends on, and a
// second click then founded a new cluster over the old membership.
func (h *Handler) memberNotRunning() bool {
	if h.getManager() != nil {
		return false
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	return h.Config.Cluster.Enabled
}

func (h *Handler) GetStatus(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		// configured: a member whose cluster did not start, which the UI
		// must not present as "clustering is not enabled" with Init/Join.
		return response.OK(c, map[string]interface{}{
			"enabled":    false,
			"configured": h.memberNotRunning(),
		})
	}

	stale := false
	if !mgr.IsLeader() {
		// Follower: try to proxy to leader. On failure fall back to local
		// FSM but flag the response as stale — the UI renders a banner so
		// operators know they're looking at potentially-out-of-date data
		// rather than confidently trusting what they see.
		if resp, err := h.proxyToLeaderRead(c); err == nil {
			return h.returnWithLocalID(c, resp)
		}
		stale = true
	} else {
		// Leader: confirm we're still leader-of-quorum *now*. Without this,
		// a stale leader on a minority partition cheerfully serves its own
		// FSM as authoritative until its lease eventually times out.
		if err := mgr.GetRaft().VerifyLeader(2 * time.Second); err != nil {
			stale = true
		}
	}

	overview := mgr.GetOverview()
	if overview == nil {
		return response.OK(c, map[string]interface{}{
			"enabled":   true,
			"stale":     true,
			"local_id":  mgr.LocalNodeID(),
			"is_leader": mgr.IsLeader(),
		})
	}
	return response.OK(c, map[string]interface{}{
		"enabled":    true,
		"name":       overview.Name,
		"node_count": overview.NodeCount,
		"leader_id":  overview.LeaderID,
		"local_id":   mgr.LocalNodeID(),
		"is_leader":  mgr.IsLeader(),
		"stale":      stale,
	})
}

func (h *Handler) returnWithLocalID(c echo.Context, resp *pb.APIResponse) error {
	mgr := h.getManager()
	var envelope map[string]interface{}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
	}
	if data, ok := envelope["data"].(map[string]interface{}); ok {
		data["local_id"] = mgr.LocalNodeID()
		data["is_leader"] = mgr.IsLeader()
	}
	patched, err := json.Marshal(envelope)
	if err != nil {
		return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
	}
	return c.Blob(int(resp.StatusCode), "application/json", patched)
}

// tokenRequest is the body of POST /cluster/token. Both fields may be left
// out: the CLI and the token page from before agents send only ttl, and a
// body without kind asks for a manager token, the only kind there was.
type tokenRequest struct {
	TTL  string `json:"ttl"`
	Kind string `json:"kind"`
}

// parseTokenRequest reads a token request. A ttl that does not parse is the
// default, as it always was; one that parses must be positive and at most
// MaxTokenTTL, so a typo (8760h = 1y, 99999h = ~11y) or a careless operator
// can't mint long-lived bearer credentials. A kind other than manager or
// agent is refused rather than defaulted: a manager token hands the server
// that joins with it the cluster's JWT key, admin account and CA key.
func parseTokenRequest(body tokenRequest) (ttl time.Duration, kind cluster.NodeKind, err error) {
	ttl = cluster.DefaultTokenTTL
	if body.TTL != "" {
		if d, err := time.ParseDuration(body.TTL); err == nil {
			ttl = d
		}
	}
	if ttl > cluster.MaxTokenTTL {
		return 0, "", fmt.Errorf("ttl exceeds maximum of %s", cluster.MaxTokenTTL)
	}
	if ttl <= 0 {
		return 0, "", errors.New("ttl must be positive")
	}
	switch cluster.NodeKind(body.Kind) {
	case "", cluster.KindManager:
		return ttl, cluster.KindManager, nil
	case cluster.KindAgent:
		return ttl, cluster.KindAgent, nil
	}
	return 0, "", fmt.Errorf("kind must be %q or %q", cluster.KindManager, cluster.KindAgent)
}

func (h *Handler) CreateToken(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	// FSM-write — only leader can mint tokens. Followers auto-forward.
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}

	// A body that cannot be read is refused, not taken for the default: it
	// may have asked for an agent token, and the default is a manager token.
	var body tokenRequest
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	ttl, kind, err := parseTokenRequest(body)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	token, err := mgr.CreateJoinToken(ttl, kind)
	if err != nil {
		return clusterErrResponse(c, err)
	}

	caKey, err := mgr.CAPublicKey()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "cluster CA unavailable")
	}

	// Include the cluster's gRPC port + advertise address so the UI can
	// render an exact-paste join command without hardcoding the legacy
	// 9443 port that no longer matches the default install (3629), and the
	// CA fingerprint the joining node verifies the leader against.
	cfg := mgr.GetConfig()
	return response.OK(c, map[string]interface{}{
		"token":             token.Token,
		"kind":              token.EffectiveKind(),
		"expires_at":        token.ExpiresAt,
		"grpc_port":         cfg.GRPCPort,
		"advertise_address": cfg.AdvertiseAddress,
		"ca_pin":            cluster.CAPin(caKey),
	})
}

func (h *Handler) ListTokens(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	// Tokens live on the leader's disk — followers forward so the operator
	// sees the same list from whichever node they're logged into.
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	tokens, err := mgr.ListJoinTokens()
	if err != nil {
		return clusterErrResponse(c, err)
	}
	return response.OK(c, map[string]interface{}{"tokens": tokens})
}

func (h *Handler) RevokeToken(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	id := c.Param("id")
	if id == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Token ID required")
	}
	if err := mgr.RevokeJoinToken(id); err != nil {
		return clusterErrResponse(c, err)
	}
	return response.OK(c, map[string]string{"revoked": id})
}

func (h *Handler) RemoveNode(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	// raft.RemoveServer is leader-only. Followers auto-forward so the
	// operator can remove a node from whichever node they're logged into.
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node ID required")
	}

	// Quorum guard: refuse to drop the cluster below quorum unless the
	// caller explicitly asks for it via ?force=true. Same pattern as
	// disband — a single fat-fingered click on a 2-voter cluster's "remove"
	// button would otherwise leave a 1-voter cluster with no fault
	// tolerance, and from there the next click bricks the whole thing.
	if c.QueryParam("force") != "true" {
		if msg, blocked := wouldDropBelowQuorum(mgr, nodeID); blocked {
			return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, msg+
				" — pass ?force=true to confirm")
		}
	}

	if err := mgr.RemoveNode(nodeID); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{"removed": nodeID})
}

// wouldDropBelowQuorum returns a human-readable reason + true when removing
// the named voter would leave the cluster with fewer voters than the
// current Raft quorum (N/2 + 1). Non-voter removal is always safe.
func wouldDropBelowQuorum(mgr *cluster.Manager, nodeID string) (string, bool) {
	target := mgr.GetNode(nodeID)
	if target == nil || target.Role != cluster.RoleVoter {
		return "", false
	}
	voters := 0
	for _, n := range mgr.GetNodes() {
		if n.Role == cluster.RoleVoter {
			voters++
		}
	}
	return checkQuorumAfterRemoval(nodeID, voters)
}

// validateLabels enforces a small bounded schema on user-supplied node
// labels — without this, the FSM Apply path persisted whatever the caller
// sent (including multi-MB strings) into the Raft log forever. Following
// k8s conventions: 63 chars max per key/value, key starts with alnum,
// allowed chars [A-Za-z0-9._-]. Caps total label count at 32 per node.
func validateLabels(labels map[string]string) error {
	if len(labels) > 32 {
		return fmt.Errorf("too many labels (max 32)")
	}
	for k, v := range labels {
		if len(k) == 0 || len(k) > 63 {
			return fmt.Errorf("label key %q must be 1..63 chars", k)
		}
		if !labelKeyRE.MatchString(k) {
			return fmt.Errorf("label key %q has invalid characters (allowed: alnum + .-_)", k)
		}
		if len(v) > 63 {
			return fmt.Errorf("label value for %q exceeds 63 chars", k)
		}
		if !labelValueRE.MatchString(v) {
			return fmt.Errorf("label value for %q has invalid characters", k)
		}
	}
	return nil
}

var (
	labelKeyRE   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	labelValueRE = regexp.MustCompile(`^[a-zA-Z0-9._-]*$`)
)

// checkQuorumAfterRemoval is the pure math behind the guard, split out so
// the boundary cases (1, 2, 3, 5 voters) are testable without standing up
// a real Manager+Raft. Returns true when removing one voter from a cluster
// of `voters` total would leave fewer than N/2+1 voters remaining.
func checkQuorumAfterRemoval(targetID string, voters int) (string, bool) {
	if voters <= 0 {
		return "", false
	}
	postRemoval := voters - 1
	quorum := voters/2 + 1
	if postRemoval < quorum {
		return fmt.Sprintf(
			"removing voter %q would drop the cluster below quorum (%d/%d voters; quorum %d)",
			targetID, postRemoval, voters, quorum), true
	}
	return "", false
}

func (h *Handler) GetEvents(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.OK(c, map[string]interface{}{
			"events": []interface{}{},
		})
	}

	limit := 50
	if l, err := strconv.Atoi(c.QueryParam("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	afterID := 0
	if id, err := strconv.Atoi(c.QueryParam("after")); err == nil && id > 0 {
		afterID = id
	}

	var events []cluster.ClusterEvent
	if afterID > 0 {
		events = mgr.GetEvents().Since(afterID)
	} else {
		events = mgr.GetEvents().Recent(limit)
	}
	if events == nil {
		events = []cluster.ClusterEvent{}
	}

	return response.OK(c, map[string]interface{}{
		"events": events,
	})
}

// RenameCluster changes the name every node shows. The name is FSM config,
// so a follower's request is forwarded to the leader, which owns the write.
func (h *Handler) RenameCluster(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	name, err := validateClusterName(body.Name)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Cluster name must be 1-64 characters with no control characters")
	}
	if err := mgr.RenameCluster(name); err != nil {
		return clusterErrResponse(c, err)
	}
	slog.Info("cluster renamed", "component", "cluster", "name", name)
	return response.OK(c, map[string]string{"name": name})
}

func (h *Handler) UpdateNodeLabels(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	// Labels live in the FSM cluster_node table — leader-only Apply.
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node ID required")
	}

	var body struct {
		Labels map[string]string `json:"labels"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if err := validateLabels(body.Labels); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	if err := mgr.UpdateNodeLabels(nodeID, body.Labels); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]interface{}{
		"node_id": nodeID,
		"labels":  body.Labels,
	})
}

func (h *Handler) UpdateNodeAddress(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	// Per cluster/CLAUDE.md "verifySelfAddress is leader-only", this PATCH
	// is the load-bearing path for changing a node's advertised address
	// after boot. Auto-forwarding lets operators run it from any node.
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node ID required")
	}

	var body struct {
		APIAddress  string `json:"api_address"`
		GRPCAddress string `json:"grpc_address"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if body.APIAddress == "" || body.GRPCAddress == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "api_address and grpc_address required")
	}

	if err := mgr.UpdateNodeAddress(nodeID, body.APIAddress, body.GRPCAddress); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{
		"node_id":      nodeID,
		"api_address":  body.APIAddress,
		"grpc_address": body.GRPCAddress,
	})
}

func (h *Handler) TransferLeadership(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	// raft.TransferLeadership runs on the current leader and hands off to
	// the target. Forwarding from a follower means "ask the current leader
	// to hand off to this target" — exactly the operator's intent.
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}

	var body struct {
		TargetNodeID string `json:"target_node_id"`
	}
	if err := c.Bind(&body); err != nil || body.TargetNodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "target_node_id required")
	}

	if err := mgr.TransferLeadership(body.TargetNodeID); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{
		"message":        "Leadership transfer initiated",
		"target_node_id": body.TargetNodeID,
	})
}

// proxyToLeaderRead forwards one of the UI-polled read endpoints to the
// leader. It spends leaderReadTimeout and short-circuits while a recent
// attempt against the same leader address failed, so an unreachable leader
// costs one dial per leaderReadCooldown instead of one per request.
//
// Read-only on purpose: the breaker reports a cached failure without probing,
// which is fine for a poll that repeats seconds later but would be wrong for
// a write. FSM writes use middleware.ProxyToLeader and are unaffected.
func (h *Handler) proxyToLeaderRead(c echo.Context) (*pb.APIResponse, error) {
	mgr := h.getManager()
	if mgr == nil {
		return nil, fmt.Errorf("cluster not configured")
	}
	leaderAddr := mgr.GetLeaderGRPCAddress()
	if leaderAddr == "" {
		return nil, fmt.Errorf("no leader")
	}
	if h.leaderReadBlocked(leaderAddr, time.Now()) {
		return nil, fmt.Errorf("leader unreachable (recent attempt failed)")
	}
	resp, err := h.proxyToLeaderTimeout(c, leaderReadTimeout)
	h.recordLeaderRead(leaderAddr, err, time.Now())
	return resp, err
}

// leaderReadBlocked reports whether a read-path proxy to addr should be
// skipped because a previous attempt failed within the cooldown. A change of
// leader address clears the block — the new leader deserves its own probe.
func (h *Handler) leaderReadBlocked(addr string, now time.Time) bool {
	h.leaderReadMu.Lock()
	defer h.leaderReadMu.Unlock()
	if h.leaderReadAddr != addr || h.leaderReadFail.IsZero() {
		return false
	}
	return now.Sub(h.leaderReadFail) < leaderReadCooldown
}

// recordLeaderRead opens the breaker on failure and clears it on success.
func (h *Handler) recordLeaderRead(addr string, err error, now time.Time) {
	h.leaderReadMu.Lock()
	defer h.leaderReadMu.Unlock()
	if err == nil {
		h.leaderReadAddr = ""
		h.leaderReadFail = time.Time{}
		return
	}
	h.leaderReadAddr = addr
	h.leaderReadFail = now
}

func (h *Handler) proxyToLeaderTimeout(c echo.Context, timeout time.Duration) (*pb.APIResponse, error) {
	mgr := h.getManager()
	pool := mgr.GetConnPool()
	leaderAddr := mgr.GetLeaderGRPCAddress()
	if leaderAddr == "" {
		return nil, fmt.Errorf("no leader")
	}

	client, err := pool.Get(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("dial leader: %w", err)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), timeout)
	defer cancel()

	var bodyBytes []byte
	if c.Request().Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request().Body)
	}

	proxyPath := c.Request().URL.Path
	if rawQuery := c.Request().URL.RawQuery; rawQuery != "" {
		proxyPath += "?" + rawQuery
	}

	// No internal-proxy header: the receiving gRPC server drops one and signs
	// its own loopback hop (grpc_server.go ProxyRequest).
	apiReq := &pb.APIRequest{
		Method: c.Request().Method,
		Path:   proxyPath,
		Body:   bodyBytes,
	}
	resp, err := client.ProxyRequest(ctx, apiReq)
	if err != nil {
		// First attempt may have hit a stale pooled connection (peer restart,
		// idle-timeout, ephemeral RST). Mirror the proxyToNodeGRPC retry path:
		// drop the dead conn, reconnect, retry once. Without this the cluster
		// status poll every 60s alternates 503/200 whenever the pool's cached
		// connection died between calls — the UI showed the cluster as
		// "leader unreachable" repeatedly even on a perfectly healthy peer.
		slog.Warn("cluster proxy: first attempt failed, retrying",
			"component", "cluster", "addr", leaderAddr,
			"path", proxyPath, "error", err)
		pool.Remove(leaderAddr)
		client, dialErr := pool.Get(leaderAddr)
		if dialErr != nil {
			slog.Error("cluster proxy: reconnect failed", "component", "cluster",
				"addr", leaderAddr, "path", proxyPath, "error", dialErr)
			return nil, fmt.Errorf("proxy reconnect: %w", dialErr)
		}
		resp, err = client.ProxyRequest(ctx, apiReq)
		if err != nil {
			slog.Error("cluster proxy: retry failed", "component", "cluster",
				"addr", leaderAddr, "path", proxyPath, "error", err)
			pool.Remove(leaderAddr)
			return nil, fmt.Errorf("proxy: %w", err)
		}
	}
	return resp, nil
}

// LeaveCluster gracefully removes this node from the cluster.
// Unlike Disband, it notifies the leader of departure before cleaning up.
// exitProcess ends the process after a leave, so that systemd starts it again
// standalone. A test replaces it.
var exitProcess = os.Exit

func (h *Handler) LeaveCluster(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}

	// Quorum guard: refuse to leave when the surviving voters could not form
	// quorum afterwards, based on *live* heartbeat health (not stale FSM
	// status). The orphan case this catches: a 2-voter cluster whose peer is
	// already offline — leaving strands a lone voter that can neither elect
	// nor commit. ?force=true bypasses for an emergency drain.
	if c.QueryParam("force") != "true" {
		if reason, blocked := mgr.WouldDropBelowQuorumOnLeave(); blocked {
			return response.Fail(c, http.StatusConflict, response.ErrClusterQuorum,
				"Leave refused: "+reason+"; pass ?force=true to override")
		}
	}

	dataDir := h.Config.Cluster.DataDir
	certDir := h.Config.Cluster.CertDir

	// Sync the cluster admin down to the local DB before Leave() abandons the
	// FSM, so this node keeps the cluster-wide credentials once standalone.
	h.syncClusterAdminToLocalDB(mgr)

	// Notify leader of departure (best-effort).
	// Leave() internally shuts down Raft, so no separate Shutdown() call needed.
	// A leave no leader took goes ahead, as the CLI's does, but its answer
	// says so: the UI and the CLI with the service running show only that.
	untaken := ""
	nodeID := mgr.LocalNodeID()
	if err := mgr.Leave(); err != nil {
		slog.Warn("could not notify cluster of departure", "component", "cluster", "error", err)
		if errors.Is(err, cluster.ErrLeaveUnacknowledged) {
			untaken = fmt.Sprintf(" Warning: %v. This node's ID is %s.", err, nodeID)
		}
	}

	// L-03: Wipe Raft data + TLS material *before* flipping config.Enabled=false.
	// A crash between the config write and RemoveAll would leave stale
	// cluster material on a "standalone" node, which confuses future joins
	// and accumulates junk in /var/lib/sfpanel/cluster. Config write last
	// means the node reboots as the old cluster member (Enabled=true) and
	// the operator can retry cleanly on a transient removal failure.
	if dataDir != "" {
		if rmErr := os.RemoveAll(dataDir); rmErr != nil {
			slog.Warn("failed to remove cluster data dir", "path", dataDir, "error", rmErr)
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to remove cluster data: %v", rmErr))
		}
	}
	if certDir != "" {
		if rmErr := os.RemoveAll(certDir); rmErr != nil {
			slog.Warn("failed to remove cluster cert dir", "path", certDir, "error", rmErr)
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to remove cluster certs: %v", rmErr))
		}
	}

	h.Config.Cluster.Enabled = false
	data, err := yaml.Marshal(h.Config)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to marshal config: %v", err))
	}
	if err := config.AtomicWriteFile(h.ConfigPath, data, 0600); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
	}

	slog.Info("node left cluster via API, restarting", "component", "cluster")

	exit := exitProcess
	go func() {
		time.Sleep(2 * time.Second)
		exit(1)
	}()

	return response.OK(c, map[string]string{
		"message": "Left cluster. Service restarting in standalone mode..." + untaken,
	})
}

func (h *Handler) DisbandCluster(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	force := c.QueryParam("force") == "true"
	// Only the leader can disband: a follower's broadcast cannot commit, and
	// forcing it here wiped this node while Raft kept it as a dead voter.
	// A forced disband that finds no leader at all stays here: it is the way
	// out of a cluster whose quorum is gone, and there is no one to forward
	// it to.
	leaderless := force && !mgr.IsLeader() && mgr.LeaderNode() == nil
	if !leaderless {
		if handled, err := middleware.ProxyToLeader(c, mgr); handled {
			return err
		}
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}
	var notTold []string
	if leaderless {
		// No leader can take the agents out, so none of them is told.
		notTold = agentNames(mgr)
	} else {
		var err error
		if notTold, err = mgr.RemoveAgentsForDisband(15 * time.Second); err != nil {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "Could not remove the agents before disbanding: "+err.Error())
		}
	}
	untold := ""
	if len(notTold) > 0 {
		untold = fmt.Sprintf(" These agents were not reached and need 'sudo sfpanel agent reset --yes' on each: %s", strings.Join(notTold, ", "))
	}

	// L-04: Broadcast disband to all nodes through the Raft FSM. Apply blocks
	// until CmdDisband is replicated to a majority. Each node's FSM.Apply
	// fires performDisband (in its own goroutine), which wipes local state
	// and exits — including on this leader. Single, unified cleanup path.
	if err := mgr.Disband(10 * time.Second); err != nil {
		// Fallback gate: if the originator lost leadership / quorum mid-Apply,
		// firing a local-only performDisband splits the cluster — this node
		// destroys itself while the surviving majority elects a new leader
		// and continues. Refuse the fallback unless the operator explicitly
		// passes ?force=true, accepting the split.
		if !force {
			slog.Warn("cluster-wide Disband broadcast failed; refusing local-only fallback (set ?force=true to proceed)",
				"component", "cluster", "error", err)
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError,
				"Disband broadcast failed (likely lost majority). Pass ?force=true to disband this node only — surviving nodes will keep running and require manual cleanup.")
		}
		slog.Warn("forced local-only disband (operator-initiated split)",
			"component", "cluster", "broadcast_error", err)
		go h.performDisband(mgr.LocalNodeID())
		return response.OK(c, map[string]string{
			"message": "Forced local-only disband. Follower nodes (if any) require manual 'cluster leave'." + untold,
		})
	}

	slog.Info("cluster disbanded via UI, nodes will self-clean from CmdDisband", "component", "cluster")
	return response.OK(c, map[string]string{
		"message": "Cluster disbanded. All nodes are restarting in standalone mode..." + untold,
	})
}

// agentNames lists the agents in this node's copy of the cluster state, by
// name where they have one: the ones a disband without a leader cannot tell.
// Once this node has wiped its cluster state, the answer is the only list.
func agentNames(mgr *cluster.Manager) []string {
	var names []string
	for _, n := range mgr.GetNodes() {
		if n.IsManager() {
			continue
		}
		if n.Name != "" {
			names = append(names, n.Name)
		} else {
			names = append(names, n.ID)
		}
	}
	sort.Strings(names)
	return names
}

func (h *Handler) ClusterUpdate(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if !mgr.IsLeader() {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "Only the leader can orchestrate cluster updates")
	}

	var req struct {
		Mode     string `json:"mode"`
		WaveSize int    `json:"wave_size"`
	}
	if err := c.Bind(&req); err != nil {
		req.Mode = "rolling"
	}
	if req.Mode != "rolling" && req.Mode != "simultaneous" {
		req.Mode = "rolling"
	}

	// One at a time. The orchestration outlives the request that started it,
	// so closing the tab no longer ends it — and a second click would
	// otherwise start a second one alongside.
	if !h.updateRunning.CompareAndSwap(false, true) {
		return response.Fail(c, http.StatusConflict, response.ErrUpdateInProgress, "A cluster update is already running")
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	flusher := c.Response()

	feed := newUpdateFeed()
	go func() {
		defer h.updateRunning.Store(false)
		defer feed.finish()
		h.runClusterUpdate(mgr, req.Mode, req.WaveSize, feed.emit)
	}()
	feed.forward(c.Request().Context().Done(), func(ev map[string]interface{}) {
		jsonData, _ := json.Marshal(ev)
		fmt.Fprintf(flusher, "data: %s\n\n", jsonData)
		flusher.Flush()
	})
	return nil
}

// runClusterUpdate is the orchestration behind ClusterUpdate. It runs on its
// own goroutine and reports through emit, which never blocks it: whether
// anyone is still watching has no bearing on whether the update finishes.
func (h *Handler) runClusterUpdate(mgr *cluster.Manager, mode string, waveSize int, emit func(map[string]interface{})) {
	rel, err := release.FetchLatest(context.Background())
	if err != nil {
		slog.Warn("cluster update: could not look up the latest release", "component", "cluster", "error", err)
		emit(map[string]interface{}{"overall": "error", "message": "Could not look up the latest release: " + err.Error()})
		return
	}
	state := mgr.GetRaft().GetFSM().GetState()
	versions := map[string]string{}
	for _, m := range mgr.GetHeartbeat().GetAllMetrics() {
		versions[m.NodeID] = m.Version
	}
	if h.Version != "" {
		versions[mgr.LocalNodeID()] = h.Version
	}
	if mode == "simultaneous" {
		waveSize = 0
		for _, n := range state.Nodes {
			if !n.IsManager() {
				waveSize++
			}
		}
	}
	plan := planClusterUpdate(state, mgr.GetHeartbeat().CheckHealth(), versions, mgr.LocalNodeID(), rel.TagName, waveSize)
	env := clusterUpdateEnv{mgr: mgr, self: paneltls.Self{
		TLSEnabled: h.Config.Server.TLS.Enabled,
		Dir:        h.Config.Server.TLS.Dir,
		CertFile:   h.Config.Server.TLS.CertFile,
		CAFile:     h.Config.Server.TLS.CAFile,
		Port:       h.Config.Server.Port,
	}}
	// Nobody may be watching (see updateFeed); the line below is then the
	// only record of how the run ended, so it carries the run's last word:
	// the counts alone read the same for a run that finished and one that
	// stopped when leadership moved.
	var outcome, reason string
	updated, failed := runUpdatePlan(env, plan, rel, mode, func(ev map[string]interface{}) {
		if o, ok := ev["overall"].(string); ok && o != "started" {
			outcome = o
			reason, _ = ev["message"].(string)
		}
		emit(ev)
	})
	slog.Info("cluster update finished", "component", "cluster", "mode", mode, "target", rel.TagName,
		"outcome", outcome, "reason", reason, "updated", updated, "failed", failed)
}

// orchestratedUpdatePath is how the cluster update asks a node — a follower,
// or the leader itself — to update. force=true skips system/update's quorum
// guard, which exists to stop an operator updating nodes one by one outside
// the orchestrator. The orchestrator has already decided the order and the
// quorum question; letting the guard veto it stranded the old leader of every
// two-node cluster, where one node down is always "below quorum". The query is
// part of the signed request URI, so it must be signed as written here.
const orchestratedUpdatePath = "/api/v1/system/update?force=true"

// PublishPanelCA records a node's panel certificate authority in replicated
// state, on behalf of that node.
//
// It exists because every FSM write is leader-only while panel CAs are
// per-node: a follower can generate its own authority but cannot publish it,
// and a node that is never elected would therefore never appear as an HTTPS
// peer. Followers call this on the leader over the mTLS-authenticated gRPC
// proxy, exactly as PATCH /cluster/nodes/:id/address already does for the one
// other piece of per-node state a follower cannot write itself.
//
// Authorisation is the cluster's existing model, not a new one. The caller is
// an authenticated cluster member — the internal proxy header is HMAC-signed
// with the shared proxy secret, and members already share the JWT secret, so a
// member can assert any identity it likes by minting a token directly. The
// header this reads is attribution, not a credential; see
// internal/cluster/CLAUDE.md § "Header copy is the trust boundary". What IS
// enforced is the shape of the material: SetPanelCA parses it and refuses
// anything that is not a CA certificate, so a leaf, a private key, or garbage
// cannot become a cluster-wide trust anchor.
func (h *Handler) PublishPanelCA(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster is not enabled")
	}
	var body cluster.PanelCAPublishRequest
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if body.NodeID == "" || body.CACert == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "node_id and ca_cert are required")
	}
	if mgr.GetNode(body.NodeID) == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Node not found")
	}
	if err := mgr.SetPanelCA(body.NodeID, body.CACert); err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			// The caller reached the wrong node; it retries against the
			// current leader on its next tick. clusterErrResponse maps this
			// to the same 503 every other leader-only route returns.
			return clusterErrResponse(c, err)
		}
		// Anything else here is a rejected certificate, which is a bad request
		// rather than a server fault.
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{"node_id": body.NodeID})
}
