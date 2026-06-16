package compose

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/composex"
)

const migrationShaHeader = "X-SFPanel-Migration-Sha256"

// MigrateImport (POST /docker/compose/migrate-import) receives a migration
// bundle from a source node over the authenticated cluster channel, verifies
// its checksum, runs the compose safety validator, restores the stack
// definition, brings it up, and waits for health. Any failure after files are
// written tears the partial stack back down. Reachable only node-to-node
// (internal-proxy auth) — never by an external client.
func (h *Handler) MigrateImport(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "migrate-import is cluster-internal only")
	}
	expectedSha := c.Request().Header.Get(migrationShaHeader)
	if expectedSha == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "missing bundle checksum header")
	}

	staging, err := os.MkdirTemp(h.ComposePath, ".migrate-stage-*")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "failed to create staging dir")
	}
	defer func() { _ = os.RemoveAll(staging) }()

	m, files, err := receiveBundle(c.Request().Body, expectedSha, staging)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput("bundle: "+err.Error()))
	}

	composeData, ok := files["compose/"+m.ComposeFile]
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, "bundle missing compose file")
	}
	if err := composex.ValidateAdvancedCompose(string(composeData)); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	if err := restoreDefinition(h.ComposePath, m, files); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	ctx := c.Request().Context()
	if _, err := h.Compose.Up(ctx, m.StackID); err != nil {
		h.migrateCleanup(m.StackID)
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("up: "+err.Error()))
	}
	if err := h.waitHealthy(ctx, m.StackID, 60*time.Second); err != nil {
		h.migrateCleanup(m.StackID)
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("healthcheck: "+err.Error()))
	}
	return response.OK(c, map[string]string{"status": "ok", "stackId": m.StackID})
}

// migrateCleanup tears down a partially-imported stack (down -v + rm dir).
func (h *Handler) migrateCleanup(stackID string) {
	if err := h.Compose.DeleteProject(context.Background(), stackID, false, true); err != nil {
		slog.Warn("migrate import cleanup failed", "component", "compose", "stack", stackID, "error", err)
	}
}

// waitHealthy polls until every service is "running" or the timeout elapses.
func (h *Handler) waitHealthy(ctx context.Context, stackID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		svcs, err := h.Compose.GetProjectServices(ctx, stackID)
		if err == nil && len(svcs) > 0 {
			allRunning := true
			for _, s := range svcs {
				if s.State != "running" {
					allRunning = false
					break
				}
			}
			if allRunning {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("services did not become healthy within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
