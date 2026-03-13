package handlers

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

type ClusterHandler struct {
	Manager    *cluster.Manager
	Config     *config.Config
	ConfigPath string
}

// InitCluster initializes a new cluster from the UI.
// POST /api/v1/cluster/init
func (h *ClusterHandler) InitCluster(c echo.Context) error {
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
		advertise = detectDefaultIP()
	}

	h.Config.Cluster.AdvertiseAddress = advertise

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

	// Schedule restart after response is sent (exit 1 so systemd Restart=on-failure triggers)
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Println("[cluster] Exiting for systemd restart...")
		os.Exit(1)
	}()

	return response.OK(c, map[string]interface{}{
		"message":  "Cluster initialized. Service restarting...",
		"name":     clusterName,
		"node_id":  h.Config.Cluster.NodeID,
		"restart":  true,
	})
}

// GetNetworkInterfaces returns available network interfaces for advertise address selection.
// GET /api/v1/cluster/interfaces
func (h *ClusterHandler) GetNetworkInterfaces(c echo.Context) error {
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

func detectDefaultIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// GetOverview returns cluster overview with all nodes and metrics.
// GET /api/v1/cluster/overview
func (h *ClusterHandler) GetOverview(c echo.Context) error {
	if h.Manager == nil {
		return response.OK(c, map[string]interface{}{
			"name": "", "node_count": 0, "leader_id": "",
			"nodes": []interface{}{}, "metrics": []interface{}{},
		})
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

// GetNodes returns the list of cluster nodes.
// GET /api/v1/cluster/nodes
func (h *ClusterHandler) GetNodes(c echo.Context) error {
	if h.Manager == nil {
		return response.OK(c, map[string]interface{}{
			"nodes": []interface{}{}, "local_id": "", "is_leader": false,
		})
	}
	nodes := h.Manager.GetNodes()
	return response.OK(c, map[string]interface{}{
		"nodes":     nodes,
		"local_id":  h.Manager.LocalNodeID(),
		"is_leader": h.Manager.IsLeader(),
	})
}

// GetStatus returns basic cluster status info.
// GET /api/v1/cluster/status
func (h *ClusterHandler) GetStatus(c echo.Context) error {
	if h.Manager == nil {
		return response.OK(c, map[string]interface{}{
			"enabled": false,
		})
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

// CreateToken generates a join token. Leader-only.
// POST /api/v1/cluster/token
func (h *ClusterHandler) CreateToken(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"token":      token.Token,
		"expires_at": token.ExpiresAt,
	})
}

// RemoveNode removes a node from the cluster. Leader-only.
// DELETE /api/v1/cluster/nodes/:id
func (h *ClusterHandler) RemoveNode(c echo.Context) error {
	if h.Manager == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node ID required")
	}

	if err := h.Manager.RemoveNode(nodeID); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	return response.OK(c, map[string]string{"removed": nodeID})
}

// GetEvents returns recent cluster events.
// GET /api/v1/cluster/events
func (h *ClusterHandler) GetEvents(c echo.Context) error {
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

// UpdateNodeLabels updates labels for a node. Leader-only.
// PATCH /api/v1/cluster/nodes/:id/labels
func (h *ClusterHandler) UpdateNodeLabels(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"node_id": nodeID,
		"labels":  body.Labels,
	})
}

// UpdateNodeAddress updates the API and gRPC addresses of a node. Leader-only.
// PATCH /api/v1/cluster/nodes/:id/address
func (h *ClusterHandler) UpdateNodeAddress(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	return response.OK(c, map[string]string{
		"node_id":      nodeID,
		"api_address":  body.APIAddress,
		"grpc_address": body.GRPCAddress,
	})
}

// TransferLeadership transfers Raft leadership to the specified node. Leader-only.
// POST /api/v1/cluster/leader-transfer
func (h *ClusterHandler) TransferLeadership(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	return response.OK(c, map[string]string{
		"message":        "Leadership transfer initiated",
		"target_node_id": body.TargetNodeID,
	})
}

// DisbandCluster disables cluster mode and restarts the service.
// POST /api/v1/cluster/disband
func (h *ClusterHandler) DisbandCluster(c echo.Context) error {
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
