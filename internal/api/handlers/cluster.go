package handlers

import (
	"net/http"
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	overview := h.Manager.GetOverview()
	if overview == nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Failed to get cluster overview")
	}
	return response.OK(c, overview)
}

// GetNodes returns the list of cluster nodes.
// GET /api/v1/cluster/nodes
func (h *ClusterHandler) GetNodes(c echo.Context) error {
	if h.Manager == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
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
