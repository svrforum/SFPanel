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
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// clusterErrResponse maps cluster errors to appropriate HTTP status codes.
func clusterErrResponse(c echo.Context, err error) error {
	switch {
	case errors.Is(err, cluster.ErrNotLeader):
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "This node is not the cluster leader")
	case errors.Is(err, cluster.ErrNodeNotFound):
		return response.Fail(c, http.StatusNotFound, response.ErrInternalError, "Node not found")
	case errors.Is(err, cluster.ErrSelfRemove):
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cannot remove self from cluster")
	case errors.Is(err, cluster.ErrNodeAlreadyExists):
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, "Node already exists")
	case errors.Is(err, cluster.ErrMaxNodesReached):
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Maximum node count reached")
	case errors.Is(err, cluster.ErrTokenNotFound), errors.Is(err, cluster.ErrTokenExpired), errors.Is(err, cluster.ErrTokenUsed):
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidRequest, err.Error())
	default:
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}
}

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
	// OnManagerActivated is called after a manager is set (init/join).
	// Used to propagate the manager to other handlers (e.g. auth).
	OnManagerActivated func(*cluster.Manager)
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

func (h *Handler) InitCluster(c echo.Context) error {
	// Prevent concurrent init/join operations
	if !h.joiningMu.TryLock() {
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, "Another cluster operation is in progress")
	}
	defer h.joiningMu.Unlock()

	if h.getManager() != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Already part of a cluster")
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
		mgr.SetConfig("jwt_secret", h.Config.Auth.JWTSecret)
	}
	mgr.SetConfig("raft_tls", "true")
	if h.DB != nil {
		var username, passwordHash string
		var totpSecret sql.NullString
		if err := h.DB.QueryRow("SELECT username, password, totp_secret FROM admin LIMIT 1").Scan(&username, &passwordHash, &totpSecret); err == nil {
			totp := ""
			if totpSecret.Valid {
				totp = totpSecret.String
			}
			mgr.SyncAccountFromDB(username, passwordHash, totp)
		}
	}

	// Save config
	data, err := yaml.Marshal(h.Config)
	if err != nil {
		mgr.Shutdown()
		os.RemoveAll(h.Config.Cluster.DataDir)
		os.RemoveAll(h.Config.Cluster.CertDir)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
	}
	if err := config.AtomicWriteFile(h.ConfigPath, data, 0600); err != nil {
		mgr.Shutdown()
		os.RemoveAll(h.Config.Cluster.DataDir)
		os.RemoveAll(h.Config.Cluster.CertDir)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
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

	var body struct {
		LeaderAddress    string `json:"leader_address"`
		Token            string `json:"token"`
		AdvertiseAddress string `json:"advertise_address"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if body.LeaderAddress == "" || body.Token == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "leader_address and token are required")
	}

	engine := &cluster.JoinEngine{
		ConfigPath: h.ConfigPath,
		Config:     h.Config,
		DB:         h.DB,
		OnActivate: h.LiveActivate,
	}

	// Pre-flight check
	pfResult, err := engine.PreFlight(body.LeaderAddress, body.Token)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, err.Error())
	}

	advertise := body.AdvertiseAddress
	if advertise == "" {
		advertise = pfResult.RecommendedIP
	}

	// Execute join
	result, err := engine.Execute(body.LeaderAddress, body.Token, advertise)
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
		resp, err := h.proxyToLeader(c)
		if err != nil {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, fmt.Sprintf("leader unreachable: %v", err))
		}
		return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
	}

	overview := mgr.GetOverview()
	if overview == nil {
		return response.OK(c, map[string]interface{}{
			"name": "", "node_count": 0, "leader_id": "",
			"nodes": []interface{}{}, "metrics": []interface{}{},
		})
	}
	return response.OK(c, overview)
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
		resp, err := h.proxyToLeader(c)
		if err != nil {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, fmt.Sprintf("leader unreachable: %v", err))
		}
		return h.returnWithLocalID(c, resp)
	}

	nodes := mgr.GetNodes()
	return response.OK(c, map[string]interface{}{
		"nodes":     nodes,
		"local_id":  mgr.LocalNodeID(),
		"is_leader": mgr.IsLeader(),
	})
}

func (h *Handler) GetStatus(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.OK(c, map[string]interface{}{
			"enabled": false,
		})
	}

	if !mgr.IsLeader() {
		if resp, err := h.proxyToLeader(c); err == nil {
			return h.returnWithLocalID(c, resp)
		}
	}

	overview := mgr.GetOverview()
	return response.OK(c, map[string]interface{}{
		"enabled":    true,
		"name":       overview.Name,
		"node_count": overview.NodeCount,
		"leader_id":  overview.LeaderID,
		"local_id":   mgr.LocalNodeID(),
		"is_leader":  mgr.IsLeader(),
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

func (h *Handler) CreateToken(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}

	ttl := cluster.DefaultTokenTTL

	var body struct {
		TTL string `json:"ttl"`
	}
	if err := c.Bind(&body); err == nil && body.TTL != "" {
		if d, err := time.ParseDuration(body.TTL); err == nil {
			ttl = d
		}
	}

	token, err := mgr.CreateJoinToken(ttl)
	if err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]interface{}{
		"token":      token.Token,
		"expires_at": token.ExpiresAt,
	})
}

func (h *Handler) RemoveNode(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node ID required")
	}

	if err := mgr.RemoveNode(nodeID); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{"removed": nodeID})
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

func (h *Handler) UpdateNodeLabels(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
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

func (h *Handler) proxyToLeader(c echo.Context) (*pb.APIResponse, error) {
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

	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()

	proxySecret := mgr.ProxySecret()
	headers := make(map[string]string)
	if proxySecret != "" {
		headers["X-SFPanel-Internal-Proxy"] = proxySecret
	}

	var bodyBytes []byte
	if c.Request().Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request().Body)
	}

	proxyPath := c.Request().URL.Path
	if rawQuery := c.Request().URL.RawQuery; rawQuery != "" {
		proxyPath += "?" + rawQuery
	}

	resp, err := client.ProxyRequest(ctx, &pb.APIRequest{
		Method:  c.Request().Method,
		Path:    proxyPath,
		Headers: headers,
		Body:    bodyBytes,
	})
	if err != nil {
		pool.Remove(leaderAddr)
		return nil, fmt.Errorf("proxy: %w", err)
	}
	return resp, nil
}

// LeaveCluster gracefully removes this node from the cluster.
// Unlike Disband, it notifies the leader of departure before cleaning up.
func (h *Handler) LeaveCluster(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}

	dataDir := h.Config.Cluster.DataDir
	certDir := h.Config.Cluster.CertDir

	// Notify leader of departure (best-effort).
	// Leave() internally shuts down Raft, so no separate Shutdown() call needed.
	if err := mgr.Leave(); err != nil {
		slog.Warn("could not notify cluster of departure", "component", "cluster", "error", err)
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

	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(1)
	}()

	return response.OK(c, map[string]string{
		"message": "Left cluster. Service restarting in standalone mode...",
	})
}

func (h *Handler) DisbandCluster(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}

	// L-04: Broadcast disband to all nodes through the Raft FSM. Apply blocks
	// until CmdDisband is replicated to a majority. Each node's FSM.Apply
	// fires performDisband (in its own goroutine), which wipes local state
	// and exits — including on this leader. Single, unified cleanup path.
	if err := mgr.Disband(10 * time.Second); err != nil {
		slog.Warn("cluster-wide Disband broadcast failed, falling back to local-only cleanup", "component", "cluster", "error", err)
		// Fallback: fire performDisband directly for this node so the
		// operator isn't left with a half-disbanded leader. Followers (if
		// any) will go Offline via heartbeats and need manual leave.
		go h.performDisband(mgr.LocalNodeID())
		return response.OK(c, map[string]string{
			"message": "Disband broadcast failed; leader is cleaning up locally. Follower nodes require manual 'cluster leave'.",
		})
	}

	slog.Info("cluster disbanded via UI, nodes will self-clean from CmdDisband", "component", "cluster")
	return response.OK(c, map[string]string{
		"message": "Cluster disbanded. All nodes are restarting in standalone mode...",
	})
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
		Mode string `json:"mode"`
	}
	if err := c.Bind(&req); err != nil {
		req.Mode = "rolling"
	}
	if req.Mode != "rolling" && req.Mode != "simultaneous" {
		req.Mode = "rolling"
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	flusher := c.Response()

	var sseMu sync.Mutex
	sendSSE := func(data map[string]interface{}) {
		sseMu.Lock()
		defer sseMu.Unlock()
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(flusher, "data: %s\n\n", jsonData)
		flusher.Flush()
	}

	state := mgr.GetRaft().GetFSM().GetState()
	health := mgr.GetHeartbeat().CheckHealth()
	metricsSlice := mgr.GetHeartbeat().GetAllMetrics()
	metricsMap := make(map[string]*cluster.NodeMetrics, len(metricsSlice))
	for _, m := range metricsSlice {
		metricsMap[m.NodeID] = m
	}

	localID := mgr.LocalNodeID()
	type nodeInfo struct {
		ID      string
		Name    string
		Version string
		IsLocal bool
	}

	var followers []nodeInfo
	var leader nodeInfo
	for id, node := range state.Nodes {
		if s, ok := health[id]; !ok || s != cluster.StatusOnline {
			sendSSE(map[string]interface{}{"node_id": id, "node_name": node.Name, "step": "skipped", "message": "Node is offline"})
			continue
		}
		ver := ""
		if m, ok := metricsMap[id]; ok {
			ver = m.Version
		}
		ni := nodeInfo{ID: id, Name: node.Name, Version: ver, IsLocal: id == localID}
		if id == localID {
			leader = ni
		} else {
			followers = append(followers, ni)
		}
	}

	sendSSE(map[string]interface{}{"overall": "started", "mode": req.Mode, "total_nodes": len(followers) + 1})

	updateNode := func(ni nodeInfo) bool {
		sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "updating", "message": "Starting update..."})

		if ni.IsLocal {
			sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "updating", "message": "Triggering local update..."})
			return true
		}

		node, ok := state.Nodes[ni.ID]
		if !ok {
			sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "error", "message": "Node not found"})
			return false
		}

		pool := mgr.GetConnPool()
		client, err := pool.Get(node.GRPCAddress)
		if err != nil {
			sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "error", "message": "Connection failed: " + err.Error()})
			return false
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		proxyHeaders := make(map[string]string)
		if secret := mgr.ProxySecret(); secret != "" {
			proxyHeaders["X-SFPanel-Internal-Proxy"] = secret
		}

		resp, err := client.ProxyRequest(ctx, &pb.APIRequest{
			Method:  "POST",
			Path:    "/api/v1/system/update",
			Headers: proxyHeaders,
		})
		if err != nil {
			sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "error", "message": "Proxy failed: " + err.Error()})
			return false
		}

		if resp.StatusCode >= 400 {
			sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "error", "message": fmt.Sprintf("Update failed (HTTP %d)", resp.StatusCode)})
			return false
		}

		sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "complete", "message": "Update triggered, node restarting..."})

		sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "waiting", "message": "Waiting for node to restart..."})
		for attempt := 0; attempt < 12; attempt++ {
			// Honour client disconnect: if the SSE consumer has gone away,
			// bail out instead of sitting in a 60s sleep loop that keeps the
			// handler goroutine alive.
			select {
			case <-c.Request().Context().Done():
				return false
			case <-time.After(5 * time.Second):
			}
			h2 := mgr.GetHeartbeat().CheckHealth()
			if s, ok := h2[ni.ID]; ok && s == cluster.StatusOnline {
				sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "online", "message": "Node back online"})
				return true
			}
		}
		sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "warning", "message": "Node did not come back within 60s"})
		return true
	}

	updated := 0
	failed := 0

	if req.Mode == "rolling" {
		for _, f := range followers {
			if updateNode(f) {
				updated++
			} else {
				sendSSE(map[string]interface{}{"overall": "error", "message": fmt.Sprintf("Rolling update stopped: %s failed", f.Name)})
				return nil
			}
		}
	} else {
		type result struct {
			ok   bool
			name string
		}
		ch := make(chan result, len(followers))
		for _, f := range followers {
			go func(ni nodeInfo) {
				ch <- result{ok: updateNode(ni), name: ni.Name}
			}(f)
		}
		for range followers {
			r := <-ch
			if r.ok {
				updated++
			} else {
				failed++
			}
		}
	}

	if leader.ID != "" {
		sendSSE(map[string]interface{}{"node_id": leader.ID, "node_name": leader.Name, "step": "updating", "message": "Updating leader (this node)..."})
		for _, f := range followers {
			h2 := mgr.GetHeartbeat().CheckHealth()
			if s, ok := h2[f.ID]; ok && s == cluster.StatusOnline {
				sendSSE(map[string]interface{}{"node_id": leader.ID, "node_name": leader.Name, "step": "transfer", "message": "Transferring leadership to " + f.Name})
				_ = mgr.GetRaft().TransferLeadership(f.ID)
				time.Sleep(2 * time.Second)
				break
			}
		}
		updated++
	}

	sendSSE(map[string]interface{}{"overall": "complete", "updated": updated, "failed": failed})

	if leader.ID != "" {
		// Snapshot the proxy secret *before* Shutdown. Shutdown currently
		// leaves the TLSManager intact so ProxySecret() still works, but
		// relying on that is fragile — if a future change cleans up the
		// TLS state during shutdown, this call would start returning "".
		proxySecret := mgr.ProxySecret()
		go func() {
			time.Sleep(1 * time.Second)
			mgr.Shutdown()
			client := &http.Client{Timeout: 5 * time.Minute}
			req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/v1/system/update", h.Config.Server.Port), nil)
			if secret := proxySecret; secret != "" {
				req.Header.Set("X-SFPanel-Internal-Proxy", secret)
			}
			client.Do(req)
		}()
	}

	return nil
}
