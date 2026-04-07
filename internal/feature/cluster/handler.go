package featurecluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
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
	case errors.Is(err, cluster.ErrInvalidToken), errors.Is(err, cluster.ErrTokenUsed):
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidRequest, err.Error())
	default:
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}
}

type Handler struct {
	Manager    *cluster.Manager
	Config     *config.Config
	ConfigPath string
}

func (h *Handler) InitCluster(c echo.Context) error {
	if h.Manager != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster already initialized")
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
		advertise = cluster.DetectOutboundIP()
	}

	h.Config.Cluster.AdvertiseAddress = advertise
	h.Config.Cluster.APIPort = h.Config.Server.Port

	mgr := cluster.NewManager(&h.Config.Cluster)
	if err := mgr.Init(clusterName); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Cluster init failed: %v", err))
	}

	h.Config.Cluster = *mgr.GetConfig()

	data, err := yaml.Marshal(h.Config)
	if err != nil {
		mgr.Shutdown()
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to marshal config: %v", err))
	}
	if err := os.WriteFile(h.ConfigPath, data, 0644); err != nil {
		mgr.Shutdown()
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
	}

	mgr.Shutdown()

	log.Printf("[cluster] Cluster '%s' initialized via UI. Restarting...", clusterName)

	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Println("[cluster] Exiting for systemd restart...")
		os.Exit(1)
	}()

	return response.OK(c, map[string]interface{}{
		"message": "Cluster initialized. Service restarting...",
		"name":    clusterName,
		"node_id": h.Config.Cluster.NodeID,
		"restart": true,
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
		return response.OK(c, map[string]interface{}{"interfaces": []interface{}{}})
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

	return response.OK(c, map[string]interface{}{
		"interfaces": result,
	})
}

func (h *Handler) GetOverview(c echo.Context) error {
	if h.Manager == nil {
		return response.OK(c, map[string]interface{}{
			"name": "", "node_count": 0, "leader_id": "",
			"nodes": []interface{}{}, "metrics": []interface{}{},
		})
	}

	if !h.Manager.IsLeader() {
		if resp, err := h.proxyToLeader(c); err == nil {
			return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
		}
	}

	overview := h.Manager.GetOverview()
	if overview == nil {
		return response.OK(c, map[string]interface{}{
			"name": "", "node_count": 0, "leader_id": "",
			"nodes": []interface{}{}, "metrics": []interface{}{},
		})
	}
	return response.OK(c, overview)
}

func (h *Handler) GetNodes(c echo.Context) error {
	if h.Manager == nil {
		return response.OK(c, map[string]interface{}{
			"nodes": []interface{}{}, "local_id": "", "is_leader": false,
		})
	}

	if !h.Manager.IsLeader() {
		if resp, err := h.proxyToLeader(c); err == nil {
			return h.returnWithLocalID(c, resp)
		}
	}

	nodes := h.Manager.GetNodes()
	return response.OK(c, map[string]interface{}{
		"nodes":     nodes,
		"local_id":  h.Manager.LocalNodeID(),
		"is_leader": h.Manager.IsLeader(),
	})
}

func (h *Handler) GetStatus(c echo.Context) error {
	if h.Manager == nil {
		return response.OK(c, map[string]interface{}{
			"enabled": false,
		})
	}

	if !h.Manager.IsLeader() {
		if resp, err := h.proxyToLeader(c); err == nil {
			return h.returnWithLocalID(c, resp)
		}
	}

	overview := h.Manager.GetOverview()
	return response.OK(c, map[string]interface{}{
		"enabled":    true,
		"name":       overview.Name,
		"node_count": overview.NodeCount,
		"leader_id":  overview.LeaderID,
		"local_id":   h.Manager.LocalNodeID(),
		"is_leader":  h.Manager.IsLeader(),
	})
}

func (h *Handler) returnWithLocalID(c echo.Context, resp *pb.APIResponse) error {
	var envelope map[string]interface{}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
	}
	if data, ok := envelope["data"].(map[string]interface{}); ok {
		data["local_id"] = h.Manager.LocalNodeID()
		data["is_leader"] = h.Manager.IsLeader()
	}
	patched, err := json.Marshal(envelope)
	if err != nil {
		return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
	}
	return c.Blob(int(resp.StatusCode), "application/json", patched)
}

func (h *Handler) CreateToken(c echo.Context) error {
	if h.Manager == nil {
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

	token, err := h.Manager.CreateJoinToken(ttl)
	if err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]interface{}{
		"token":      token.Token,
		"expires_at": token.ExpiresAt,
	})
}

func (h *Handler) RemoveNode(c echo.Context) error {
	if h.Manager == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node ID required")
	}

	if err := h.Manager.RemoveNode(nodeID); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{"removed": nodeID})
}

func (h *Handler) GetEvents(c echo.Context) error {
	if h.Manager == nil {
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
		events = h.Manager.GetEvents().Since(afterID)
	} else {
		events = h.Manager.GetEvents().Recent(limit)
	}
	if events == nil {
		events = []cluster.ClusterEvent{}
	}

	return response.OK(c, map[string]interface{}{
		"events": events,
	})
}

func (h *Handler) UpdateNodeLabels(c echo.Context) error {
	if h.Manager == nil {
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

	if err := h.Manager.UpdateNodeLabels(nodeID, body.Labels); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]interface{}{
		"node_id": nodeID,
		"labels":  body.Labels,
	})
}

func (h *Handler) UpdateNodeAddress(c echo.Context) error {
	if h.Manager == nil {
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

	if err := h.Manager.UpdateNodeAddress(nodeID, body.APIAddress, body.GRPCAddress); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{
		"node_id":      nodeID,
		"api_address":  body.APIAddress,
		"grpc_address": body.GRPCAddress,
	})
}

func (h *Handler) TransferLeadership(c echo.Context) error {
	if h.Manager == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}

	var body struct {
		TargetNodeID string `json:"target_node_id"`
	}
	if err := c.Bind(&body); err != nil || body.TargetNodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "target_node_id required")
	}

	if err := h.Manager.TransferLeadership(body.TargetNodeID); err != nil {
		return clusterErrResponse(c, err)
	}

	return response.OK(c, map[string]string{
		"message":        "Leadership transfer initiated",
		"target_node_id": body.TargetNodeID,
	})
}

func (h *Handler) proxyToLeader(c echo.Context) (*pb.APIResponse, error) {
	pool := h.Manager.GetConnPool()
	leaderAddr := h.Manager.GetLeaderGRPCAddress()
	if leaderAddr == "" {
		return nil, fmt.Errorf("no leader")
	}

	client, err := pool.Get(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("dial leader: %w", err)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()

	proxySecret := h.Manager.ProxySecret()
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

func (h *Handler) DisbandCluster(c echo.Context) error {
	if h.Manager == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}

	h.Manager.Shutdown()

	h.Config.Cluster.Enabled = false
	data, err := yaml.Marshal(h.Config)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to marshal config: %v", err))
	}
	if err := os.WriteFile(h.ConfigPath, data, 0644); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
	}

	log.Println("[cluster] Cluster disbanded via UI. Restarting...")

	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(1)
	}()

	return response.OK(c, map[string]string{
		"message": "Cluster disbanded. Service restarting...",
	})
}

func (h *Handler) ClusterUpdate(c echo.Context) error {
	if h.Manager == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if !h.Manager.IsLeader() {
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

	sendSSE := func(data map[string]interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(flusher, "data: %s\n\n", jsonData)
		flusher.Flush()
	}

	state := h.Manager.GetRaft().GetFSM().GetState()
	health := h.Manager.GetHeartbeat().CheckHealth()
	metricsSlice := h.Manager.GetHeartbeat().GetAllMetrics()
	metricsMap := make(map[string]*cluster.NodeMetrics, len(metricsSlice))
	for _, m := range metricsSlice {
		metricsMap[m.NodeID] = m
	}

	localID := h.Manager.LocalNodeID()
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

		pool := h.Manager.GetConnPool()
		client, err := pool.Get(node.GRPCAddress)
		if err != nil {
			sendSSE(map[string]interface{}{"node_id": ni.ID, "node_name": ni.Name, "step": "error", "message": "Connection failed: " + err.Error()})
			return false
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		proxyHeaders := make(map[string]string)
		if secret := h.Manager.ProxySecret(); secret != "" {
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
			time.Sleep(5 * time.Second)
			h2 := h.Manager.GetHeartbeat().CheckHealth()
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
				failed++
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
			h2 := h.Manager.GetHeartbeat().CheckHealth()
			if s, ok := h2[f.ID]; ok && s == cluster.StatusOnline {
				sendSSE(map[string]interface{}{"node_id": leader.ID, "node_name": leader.Name, "step": "transfer", "message": "Transferring leadership to " + f.Name})
				_ = h.Manager.GetRaft().TransferLeadership(f.ID)
				time.Sleep(2 * time.Second)
				break
			}
		}
		updated++
	}

	sendSSE(map[string]interface{}{"overall": "complete", "updated": updated, "failed": failed})

	if leader.ID != "" {
		go func() {
			time.Sleep(1 * time.Second)
			h.Manager.Shutdown()
			client := &http.Client{Timeout: 5 * time.Minute}
			req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/v1/system/update", h.Config.Server.Port), nil)
			if secret := h.Manager.ProxySecret(); secret != "" {
				req.Header.Set("X-SFPanel-Internal-Proxy", secret)
			}
			client.Do(req)
		}()
	}

	return nil
}
