package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

type ClusterHandler struct {
	Manager *cluster.Manager
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
