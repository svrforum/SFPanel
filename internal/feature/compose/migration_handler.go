package compose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/composex"
)

const migrationShaHeader = "X-SFPanel-Migration-Sha256"

// maxMigrationBundleBytes caps an incoming migration bundle. M1 is
// definition-only (compose + .env + small config files); the cap bounds memory
// against a malformed/hostile bundle from a compromised cluster member. A var so
// tests can lower it.
var maxMigrationBundleBytes int64 = 64 << 20 // 64 MiB

// validProjectID reports whether a :project / stackId path or query param is
// safe for filesystem use — same rule as restoreDefinition's stack id (leading
// alnum rejects "."/"..", plus an explicit ".." guard for defense in depth).
func validProjectID(id string) bool {
	return validStackID.MatchString(id) && !strings.Contains(id, "..")
}

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

	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxMigrationBundleBytes)
	m, files, err := receiveBundle(body, expectedSha, staging)
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
		// Partial write — the stack was never brought up, so just remove the
		// (validated, traversal-safe) stack dir. m.StackID was validated by
		// restoreDefinition's own validStackID check before any write, but it
		// only returns the error AFTER MkdirAll, so the dir may exist.
		if validStackID.MatchString(m.StackID) {
			_ = os.RemoveAll(filepath.Join(h.ComposePath, m.StackID))
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	// Detach from the request context: a client/proxy disconnect must not abort
	// the compose up + health poll (which would clean up a stack that is in fact
	// coming up healthy). Bound it independently instead.
	opCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := h.Compose.Up(opCtx, m.StackID); err != nil {
		h.migrateCleanup(m.StackID)
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("up: "+err.Error()))
	}
	if err := h.waitHealthy(opCtx, m.StackID, 60*time.Second); err != nil {
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
	if stackID != "" && !validProjectID(stackID) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid stackId")
	}
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

// gatherPreflight runs the source-side dry-run: it resolves the stack's compose
// config for local facts, queries the target node over the cluster channel for
// its facts, and folds both into a PreflightReport. Shared by MigratePreflight
// (returns it as JSON) and Migrate (consumes it directly). A target that can't
// be reached is non-fatal — the report carries a target-unreachable warning and
// the local-only checks still run.
func (h *Handler) gatherPreflight(ctx context.Context, project, targetNodeID, username string, overwriteAcked bool) (PreflightReport, error) {
	mgr := h.clusterManager()
	if mgr == nil {
		return PreflightReport{}, fmt.Errorf("cluster is not enabled")
	}
	cfgJSON, err := h.Compose.GetResolvedConfig(ctx, project)
	if err != nil {
		return PreflightReport{}, err
	}
	facts, err := parseStackConfig(cfgJSON, filepath.Join(h.ComposePath, project))
	if err != nil {
		return PreflightReport{}, err
	}

	in := PreflightInput{
		SourceNodeID:      mgr.LocalNodeID(),
		TargetNodeID:      targetNodeID,
		SourceArch:        runtime.GOARCH,
		StackPorts:        facts.HostPorts,
		HasSystemBind:     facts.HasSystemBind,
		HasExternalVolume: facts.HasExternalVolume,
		HasDevice:         facts.HasDevice,
		OverwriteAcked:    overwriteAcked,
		Disposition:       DispositionRetain, // preflight is disposition-agnostic for blocking; clone relaxation handled at migrate time
	}

	// Target facts via the cross-node info endpoint.
	path := "/api/v1/docker/compose/migrate/target-info?stackId=" + url.QueryEscape(project)
	status, body, perr := mgr.ProxyToNode(ctx, targetNodeID, http.MethodGet, path, nil, username)
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
	return report, nil
}

// MigratePreflight (POST /docker/compose/:project/migrate/preflight) runs on the
// source node and returns a dry-run report. Body: {"targetNodeId","overwriteAcked"}.
func (h *Handler) MigratePreflight(c echo.Context) error {
	project := c.Param("project")
	if !validProjectID(project) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid project id")
	}
	if h.clusterManager() == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInternalError, "cluster is not enabled")
	}
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

	username, _ := c.Get("username").(string)
	report, err := h.gatherPreflight(c.Request().Context(), project, req.TargetNodeID, username, req.OverwriteAcked)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, report)
}

// Migrate (POST /docker/compose/:project/migrate, SSE) cold-migrates a stack to
// targetNodeId. Body: {"targetNodeId","disposition","overwriteAcked"}. Streams
// phase events. The source is restored to running on any failure before
// finalize; the source is only destroyed (delete disposition) AFTER the target
// reports healthy.
func (h *Handler) Migrate(c echo.Context) error {
	project := c.Param("project")
	if !validProjectID(project) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid project id")
	}
	var req struct {
		TargetNodeID   string `json:"targetNodeId"`
		Disposition    string `json:"disposition"`
		OverwriteAcked bool   `json:"overwriteAcked"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	disp := Disposition(req.Disposition)
	mgr := h.clusterManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInternalError, "cluster is not enabled")
	}
	if req.TargetNodeID == "" || !disp.Valid() {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "targetNodeId and a valid disposition are required")
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	flusher := c.Response()
	send := func(phase, msg string, done bool) {
		data, _ := json.Marshal(map[string]any{"phase": phase, "message": msg, "done": done})
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	ctx := c.Request().Context()
	username, _ := c.Get("username").(string)

	// 1. preflight — abort on any block.
	send(PhasePreflight, "Running pre-flight checks...", false)
	report, perr := h.gatherPreflight(ctx, project, req.TargetNodeID, username, req.OverwriteAcked)
	if perr != nil {
		send(PhaseError, response.SanitizeOutput("pre-flight failed: "+perr.Error()), true)
		return nil
	}
	if len(report.Blocks) > 0 {
		send(PhaseError, "pre-flight blocked: "+blocksSummary(report.Blocks), true)
		return nil
	}

	// 2. quiesce — stop the source (cold) so the snapshot is consistent.
	send(PhaseQuiesce, "Stopping source stack...", false)
	if _, err := h.Compose.Stop(ctx, project); err != nil {
		send(PhaseError, response.SanitizeOutput("quiesce failed: "+err.Error()), true)
		return nil
	}

	// 3. package — build the definition bundle + checksum.
	send(PhasePackage, "Packaging stack...", false)
	yamlPath, _ := h.Compose.ResolveComposeFile(ctx, project)
	composeFile := filepath.Base(yamlPath)
	hasEnv := false
	if _, err := os.Stat(filepath.Join(h.ComposePath, project, ".env")); err == nil {
		hasEnv = true
	}
	manifest := buildDefinitionManifest(project, composeFile, hasEnv, mgr.LocalNodeID(), runtime.GOARCH, req.TargetNodeID, disp)
	var buf bytes.Buffer
	if err := packageDefinitionBundle(&buf, manifest, filepath.Join(h.ComposePath, project)); err != nil {
		h.migrateRollback(ctx, project)
		send(PhaseRollback, response.SanitizeOutput("packaging failed; source restarted: "+err.Error()), true)
		return nil
	}
	sum := sha256.Sum256(buf.Bytes())
	sha := hex.EncodeToString(sum[:])

	// 4. transfer — push to the target; it restores + ups + healthchecks.
	send(PhaseTransfer, "Transferring to target...", false)
	status, body, terr := h.pushBundleToTarget(ctx, req.TargetNodeID, username, buf.Bytes(), sha)
	if terr != nil || status != http.StatusOK {
		h.migrateRollback(ctx, project)
		msg := "transfer/restore failed; source restarted"
		if terr != nil {
			msg += ": " + terr.Error()
		} else {
			msg += " (target status " + http.StatusText(status) + ": " + string(body) + ")"
		}
		send(PhaseRollback, response.SanitizeOutput(msg), true)
		return nil
	}

	// 5. finalize — disposition (target is healthy now).
	send(PhaseFinalize, "Applying source disposition ("+string(disp)+")...", false)
	if err := h.migrateFinalize(ctx, project, disp); err != nil {
		send(PhaseError, response.SanitizeOutput("finalize warning: "+err.Error()), true)
		return nil
	}
	send(PhaseDone, "Migration complete.", true)
	return nil
}

// migrateRollback restores the source to running after a pre-finalize failure.
func (h *Handler) migrateRollback(ctx context.Context, project string) {
	if _, err := h.Compose.Start(ctx, project); err != nil {
		slog.Error("migration rollback: failed to restart source", "component", "compose", "project", project, "error", err)
	}
}

// migrateFinalize applies the source disposition AFTER the target is healthy.
func (h *Handler) migrateFinalize(ctx context.Context, project string, d Disposition) error {
	switch d {
	case DispositionRetain:
		return nil // source already stopped
	case DispositionDelete:
		return h.Compose.DeleteProject(ctx, project, false, true)
	case DispositionClone:
		_, err := h.Compose.Start(ctx, project)
		return err
	}
	return fmt.Errorf("unknown disposition %q", d)
}

func blocksSummary(b []PreflightFinding) string {
	parts := make([]string, 0, len(b))
	for _, f := range b {
		parts = append(parts, f.Code)
	}
	return strings.Join(parts, ", ")
}
