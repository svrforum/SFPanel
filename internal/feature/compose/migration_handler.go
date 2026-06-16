package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v4/disk"
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

// migrateTargetInfo is the target node's answer to a pre-flight query.
type migrateTargetInfo struct {
	Arch        string `json:"arch"`
	FreeBytes   int64  `json:"freeBytes"`
	PortsInUse  []int  `json:"portsInUse"`
	StackExists bool   `json:"stackExists"`
}

// MigrateTargetInfo (GET /docker/compose/migrate/target-info?stackId=X) returns
// this node's pre-flight facts. Cluster-internal only.
func (h *Handler) MigrateTargetInfo(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "cluster-internal only")
	}
	stackID := c.QueryParam("stackId")
	info := migrateTargetInfo{Arch: runtime.GOARCH}
	if u, err := disk.Usage(h.ComposePath); err == nil {
		info.FreeBytes = int64(u.Free)
	}
	if stackID != "" {
		if _, err := os.Stat(filepath.Join(h.ComposePath, stackID)); err == nil {
			info.StackExists = true
		}
	}
	info.PortsInUse = h.usedHostPorts(c.Request().Context())
	return response.OK(c, info)
}

// usedHostPorts returns the distinct published host ports of containers running
// on this node, best-effort. Used by the port-conflict pre-flight check. Errors
// (docker unreachable, etc.) collapse to an empty slice — a no-op check is
// acceptable rather than failing the whole pre-flight.
func (h *Handler) usedHostPorts(ctx context.Context) []int {
	if h.Compose == nil {
		return nil
	}
	dc := h.Compose.DockerClient()
	if dc == nil {
		return nil
	}
	containers, err := dc.ListContainersCached(ctx)
	if err != nil {
		slog.Warn("preflight: list containers failed", "component", "compose", "error", err)
		return nil
	}
	seen := map[int]bool{}
	var ports []int
	for _, ct := range containers {
		for _, p := range ct.Ports {
			if p.PublicPort == 0 {
				continue // not published to the host
			}
			hp := int(p.PublicPort)
			if seen[hp] {
				continue
			}
			seen[hp] = true
			ports = append(ports, hp)
		}
	}
	return ports
}

// MigratePreflight (POST /docker/compose/:project/migrate/preflight) runs on the
// source node and returns a dry-run report. Body: {"targetNodeId","overwriteAcked"}.
func (h *Handler) MigratePreflight(c echo.Context) error {
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInternalError, "cluster is not enabled")
	}
	project := c.Param("project")
	var req struct {
		TargetNodeID   string `json:"targetNodeId"`
		OverwriteAcked bool   `json:"overwriteAcked"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if req.TargetNodeID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "targetNodeId is required")
	}

	ctx := c.Request().Context()
	// Local facts from the resolved compose config.
	cfgJSON, err := h.Compose.GetResolvedConfig(ctx, project)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	facts, err := parseStackConfig(cfgJSON, filepath.Join(h.ComposePath, project))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	in := PreflightInput{
		SourceNodeID:      h.ClusterMgr.LocalNodeID(),
		TargetNodeID:      req.TargetNodeID,
		SourceArch:        runtime.GOARCH,
		StackPorts:        facts.HostPorts,
		HasSystemBind:     facts.HasSystemBind,
		HasExternalVolume: facts.HasExternalVolume,
		HasDevice:         facts.HasDevice,
		OverwriteAcked:    req.OverwriteAcked,
		Disposition:       DispositionRetain, // preflight is disposition-agnostic for blocking; clone relaxation handled at migrate time
	}

	// Target facts via the cross-node info endpoint.
	username, _ := c.Get("username").(string)
	path := "/api/v1/docker/compose/migrate/target-info?stackId=" + url.QueryEscape(project)
	status, body, perr := h.ClusterMgr.ProxyToNode(ctx, req.TargetNodeID, http.MethodGet, path, nil, username)
	if perr == nil && status == http.StatusOK {
		var wrapper struct {
			Data migrateTargetInfo `json:"data"`
		}
		if jerr := json.Unmarshal(body, &wrapper); jerr == nil {
			in.TargetArch = wrapper.Data.Arch
			in.TargetFreeBytes = wrapper.Data.FreeBytes
			in.TargetPortsInUse = wrapper.Data.PortsInUse
			in.TargetStackExists = wrapper.Data.StackExists
		}
	}
	// If the target query failed, BuildPreflightReport still returns the local-only
	// checks; surface a warning so the operator knows target checks were skipped.
	report := BuildPreflightReport(in)
	if perr != nil || status != http.StatusOK {
		report.Warnings = append(report.Warnings, PreflightFinding{Code: "target-unreachable", Message: "could not query the target node; disk/port/arch checks were skipped"})
	}
	return response.OK(c, report)
}
