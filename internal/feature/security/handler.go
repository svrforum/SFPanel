package security

import (
	"database/sql"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/security"
)

// Handler is the REST surface for the security feature. Wired in router.go.
type Handler struct {
	DB        *sql.DB
	Cluster   *cluster.Manager
	Verifier  *security.Verifier
	Installer *security.Installer
}

// GetPolicy returns the cluster-wide policy. Same answer on every node.
func (h *Handler) GetPolicy(c echo.Context) error {
	p, err := security.LoadPolicy(h.Cluster)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, p)
}

// PutPolicy validates and persists via Raft Apply (leader-only).
func (h *Handler) PutPolicy(c echo.Context) error {
	var p security.Policy
	if err := c.Bind(&p); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if err := p.Validate(); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPolicy, err.Error())
	}
	if h.Cluster == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster not initialized")
	}
	if err := security.SavePolicy(h.Cluster, p); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"status": "saved"})
}

// CosignStatus reports the binary state on the local node.
func (h *Handler) CosignStatus(c echo.Context) error {
	path := security.DefaultCosignPath
	installed := false
	version := ""
	if _, err := os.Stat(path); err == nil {
		installed = true
		if h.Installer != nil && h.Installer.Cmd != nil {
			if out, err := h.Installer.Cmd.RunWithTimeout(5*time.Second, path, "version"); err == nil {
				version = out
			}
		}
	}
	return response.OK(c, map[string]any{
		"installed": installed,
		"version":   version,
		"path":      path,
	})
}

// VerifyImage forces re-verification of a single ref, ignoring cache.
func (h *Handler) VerifyImage(c echo.Context) error {
	var req struct {
		Ref       string `json:"ref"`
		SkipCache bool   `json:"skip_cache"`
	}
	if err := c.Bind(&req); err != nil || req.Ref == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "ref required")
	}
	if h.Verifier == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "verifier not configured")
	}
	if req.SkipCache && h.DB != nil {
		_, _ = h.DB.Exec(`DELETE FROM image_signatures WHERE ref = ?`, req.Ref)
	}
	if err := h.Verifier.VerifyImage(c.Request().Context(), req.Ref); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrPolicyViolation,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"status": "verified"})
}
