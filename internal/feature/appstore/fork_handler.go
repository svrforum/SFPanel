package appstore

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

const forkApplyTimeout = 5 * time.Second

type createForkRequest struct {
	StackName   string `json:"stack_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type updateForkRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Category    *string `json:"category,omitempty"`
}

// ListForks returns all forks across the cluster. Reads from the local FSM
// (replicated, sub-second lag).
func (h *Handler) ListForks(c echo.Context) error {
	if h.ClusterMgr == nil {
		return response.OK(c, []*cluster.ForkRecord{})
	}
	return response.OK(c, h.ClusterMgr.ListForks())
}

// GetFork returns the fork by id (cluster-wide, read from local FSM).
func (h *Handler) GetFork(c echo.Context) error {
	id := c.Param("id")
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "cluster not initialized")
	}
	rec := h.ClusterMgr.GetFork(id)
	if rec == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "fork not found")
	}
	return response.OK(c, rec)
}

// CreateFork extracts compose + env from a running stack and creates a
// fork record via Raft.
func (h *Handler) CreateFork(c echo.Context) error {
	var req createForkRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.StackName = strings.TrimSpace(req.StackName)
	if req.StackName == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "stack_name required")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "name required")
	}
	if len(req.Name) > 100 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "name too long (max 100)")
	}
	if h.ClusterMgr == nil || h.Compose == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster or compose not configured")
	}

	ctx := c.Request().Context()
	composeYAML, _, err := h.Compose.GetProjectYAML(ctx, req.StackName)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, response.SanitizeOutput(err.Error()))
	}
	envContent, err := h.Compose.GetProjectEnv(ctx, req.StackName)
	if err != nil {
		// Missing .env is fine — fork without runtime values.
		envContent = ""
	}
	envValues := parseEnvFile(envContent)

	meta, compose := ExtractForkMeta(req.StackName, composeYAML, envValues, UserForkInput{
		Name: req.Name, Description: req.Description, Category: req.Category,
	})
	metaJSON, _ := json.Marshal(meta)
	rec := &cluster.ForkRecord{
		ID:          meta.ID,
		Name:        req.Name,
		Description: req.Description,
		Category:    meta.Category,
		Compose:     compose,
		Meta:        metaJSON,
		CreatedAt:   time.Now().UnixMilli(),
		CreatedBy:   getUsernameFromContext(c),
	}
	if err := h.ClusterMgr.CreateFork(rec, forkApplyTimeout); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"id": rec.ID})
}

// UpdateFork patches metadata (name/description/category). YAML immutable.
func (h *Handler) UpdateFork(c echo.Context) error {
	id := c.Param("id")
	var req updateForkRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster not initialized")
	}
	existing := h.ClusterMgr.GetFork(id)
	if existing == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "fork not found")
	}
	patch := *existing
	if req.Name != nil {
		patch.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		patch.Description = *req.Description
	}
	if req.Category != nil {
		patch.Category = *req.Category
	}
	if err := h.ClusterMgr.UpdateFork(id, &patch, forkApplyTimeout); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"id": id})
}

// DeleteFork removes a fork.
func (h *Handler) DeleteFork(c echo.Context) error {
	id := c.Param("id")
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster not initialized")
	}
	if err := h.ClusterMgr.DeleteFork(id, forkApplyTimeout); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"id": id})
}

// parseEnvFile parses KEY=VALUE lines into a map.
func parseEnvFile(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		out[strings.TrimSpace(line[:eq])] = strings.TrimSpace(line[eq+1:])
	}
	return out
}

// getUsernameFromContext extracts the authenticated user from the JWT
// middleware (which sets c.Set("username", <string>) — see
// internal/api/middleware/auth.go). Returns "" if no value is present
// (e.g. tests without the auth middleware in front).
func getUsernameFromContext(c echo.Context) string {
	if u, ok := c.Get("username").(string); ok {
		return u
	}
	return ""
}
