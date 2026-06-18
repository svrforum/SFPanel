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
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/composex"
)

const migrationShaHeader = "X-SFPanel-Migration-Sha256"

// healthGateTimeout bounds how long the target waits for the imported stack to
// become healthy before treating the import as failed (and rolling back). It is
// longer than a bare container start because, when a service declares a Docker
// health-check, we wait for it to report healthy rather than merely "running".
const healthGateTimeout = 120 * time.Second

// maxMigrationBundleBytes caps an incoming migration bundle. The bundle now
// carries volume/bind data + saved images, so the cap is a large safety bound
// against a runaway/hostile transfer from a compromised cluster member, not a
// tight limit. A var so tests can lower it.
var maxMigrationBundleBytes int64 = 64 << 30 // 64 GiB

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
	m, files, staged, err := receiveBundle(body, expectedSha, staging)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput("bundle: "+err.Error()))
	}
	if !validStackID.MatchString(m.StackID) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid stack id in bundle")
	}

	// Serialize imports of the same stack so two concurrent pushes can't race on
	// the same dir/project (one's failure cleanup wiping the other's healthy stack).
	if !h.tryAcquireMigration(m.StackID) {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "an import for this stack is already in progress")
	}
	defer h.releaseMigration(m.StackID)

	composeData, ok := files["compose/"+m.ComposeFile]
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, "bundle missing compose file")
	}
	if err := composex.ValidateAdvancedCompose(string(composeData)); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	// Overwrite safety: if a stack with this id already exists, refuse unless the
	// source acked overwrite. On overwrite, move the prior tenant aside (backup)
	// so a failed import can restore it AND its named volumes are NOT removed on
	// cleanup — a failed import must never destroy pre-existing target data.
	stackDir := filepath.Join(h.ComposePath, m.StackID)
	backup := ""
	if _, statErr := os.Stat(stackDir); statErr == nil {
		if !m.Overwrite {
			return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "a stack with this id already exists on the target")
		}
		backup = stackDir + ".migbak"
		_ = os.RemoveAll(backup)
		if rerr := os.Rename(stackDir, backup); rerr != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("overwrite backup failed: "+rerr.Error()))
		}
	}

	if err := restoreDefinition(h.ComposePath, m, files); err != nil {
		// Partial write, never upped — just remove the new dir and restore any backup.
		_ = os.RemoveAll(stackDir)
		if backup != "" {
			_ = os.Rename(backup, stackDir)
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	// Serialize restores that touch the SAME docker volume. The stack lock above
	// does NOT cover this: two distinct stacks can share a named/external volume,
	// hold different stack locks, and race clearVolume + tar-extract on the one
	// shared volume — corrupting it and (under a source `delete` disposition)
	// destroying the source over a corrupted target. Acquire all the volumes this
	// import will restore, all-or-nothing; refuse with 409 if any is mid-restore.
	var volNames []string
	for _, v := range m.Volumes {
		if v.Copy && v.Archive != "" {
			volNames = append(volNames, v.Docker)
		}
	}
	acquiredVols, contendedVol := h.tryAcquireVolumes(volNames)
	if contendedVol != "" {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "another migration is restoring shared volume: "+contendedVol)
	}
	defer h.releaseVolumes(acquiredVols)

	// Detach from the request context: a client/proxy disconnect must not abort
	// the data restore + compose up + health poll (which would clean up a stack
	// that is in fact coming up healthy). Bound it independently instead. 2h
	// covers docker load + volume extraction of a large stack.
	opCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	// Re-validate the RESOLVED compose (after .env interpolation). The raw-text
	// ValidateAdvancedCompose above can be bypassed by a hostile/edited .env that
	// injects privileged/host-mode/device directives via ${VAR} substitution, and
	// the target's `up` re-resolves with that .env. Best-effort: if the config
	// can't be resolved here, `up` would surface the same error, so fall back to
	// the raw check rather than failing on a transient resolve error.
	if resolved, rerr := h.Compose.GetResolvedConfigYAML(opCtx, m.StackID); rerr == nil {
		if verr := composex.ValidateAdvancedCompose(resolved); verr != nil {
			_ = os.RemoveAll(stackDir)
			if backup != "" {
				if err := os.Rename(backup, stackDir); err != nil {
					slog.Error("resolved-compose reject: restoring prior definition from backup failed", "component", "compose", "stack", m.StackID, "error", err)
				}
			}
			return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput("resolved compose rejected: "+verr.Error()))
		}
	}

	// Restore images + volume/bind data BEFORE up. Each archive was sha256-verified
	// on receipt, so a clean return means the target holds intact data — which is
	// what makes the source `delete` disposition safe.
	// staging holds the received bundle; reuse it as the prebak dir so a pre-existing
	// volume can be archived aside before an overwrite clearVolume wipes it.
	createdVols, prebaks, warns, derr := restoreData(opCtx, m, staged, h.ComposePath, staging)
	for _, w := range warns {
		slog.Warn("migrate import: "+w, "component", "compose", "stack", m.StackID)
	}
	if derr != nil {
		h.failedImportData(m.StackID, backup, createdVols, prebaks)
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("restore data: "+derr.Error()))
	}
	if _, err := h.Compose.Up(opCtx, m.StackID); err != nil {
		h.failedImportData(m.StackID, backup, createdVols, prebaks)
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("up: "+err.Error()))
	}
	if err := h.waitHealthy(opCtx, m.StackID, healthGateTimeout); err != nil {
		h.failedImportData(m.StackID, backup, createdVols, prebaks)
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput("healthcheck: "+err.Error()))
	}
	if backup != "" {
		_ = os.RemoveAll(backup) // success: the prior definition is intentionally replaced
	}
	return response.OK(c, map[string]string{"status": "ok", "stackId": m.StackID})
}

// failedImportCleanup tears down a failed import after `up`. For a fresh import
// it removes the stack with its volumes. For an OVERWRITE (backup != "") it must
// NOT remove volumes (they may belong to the prior tenant) and restores the
// prior definition from the backup, so a failed import never destroys pre-existing data.
func (h *Handler) failedImportCleanup(stackID, backup string) {
	overwrite := backup != ""
	// Never `down -v`: a blanket volume removal would delete a pre-existing
	// volume whose name collides with the imported stack's (a different tenant's
	// data). Volumes we FRESHLY created are removed by failedImportData via the
	// createdVols list; pre-existing ones are left intact.
	h.migrateCleanup(stackID, false)
	if overwrite {
		dir := filepath.Join(h.ComposePath, stackID)
		if err := os.RemoveAll(dir); err != nil {
			slog.Error("failed import: removing partial stack dir before backup restore failed", "component", "compose", "stack", stackID, "error", err)
		}
		// Restoring the prior definition is the whole point of the .migbak backup;
		// a silent failure here leaves the prior tenant's definition gone with no trace.
		if err := os.Rename(backup, dir); err != nil {
			slog.Error("failed import: restoring prior stack definition from backup failed — prior definition may be lost", "component", "compose", "stack", stackID, "backup", backup, "error", err)
		}
	}
}

// failedImportData cleans up after a data-restore/up/health failure: the stack
// teardown (failedImportCleanup), removal of any volumes restoreData FRESHLY
// created, and restoration of any pre-existing volumes it cleared on overwrite.
// Freshly-created volumes did not exist before this import, so removing them is
// safe even on an overwrite (a pre-existing tenant volume is never in createdVols);
// pre-existing volumes cleared on overwrite are restored from their prebak archive
// so a failed import never destroys the prior tenant's volume data.
func (h *Handler) failedImportData(stackID, backup string, createdVols []string, prebaks []volPrebak) {
	h.failedImportCleanup(stackID, backup)
	// Cleanup must run even if the import op deadline already fired (a timeout is a
	// common failure cause), so use a fresh bounded context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, v := range createdVols {
		removeVolume(ctx, v)
	}
	// Restore pre-existing volumes we wiped on overwrite, from their prebak. The
	// prebak is our OWN archive of the target's prior data, so it extracts via the
	// trusted path (no hostile-input validation that could reject legit content).
	for _, pb := range prebaks {
		if err := clearVolume(ctx, pb.Docker); err != nil {
			slog.Error("failed import: clearing volume before prebak restore failed", "component", "compose", "volume", pb.Docker, "error", err)
			continue
		}
		if err := extractArchiveToVolume(ctx, pb.Docker, pb.Archive); err != nil {
			slog.Error("failed import: restoring pre-existing volume from prebak failed — prior data may be lost", "component", "compose", "volume", pb.Docker, "error", err)
		}
	}
}

// migrateCleanup tears down an imported stack (down [-v] + rm dir). removeVolumes
// is false on an overwrite so the prior tenant's named volumes survive.
func (h *Handler) migrateCleanup(stackID string, removeVolumes bool) {
	if err := h.Compose.DeleteProject(context.Background(), stackID, false, removeVolumes); err != nil {
		slog.Warn("migrate import cleanup failed", "component", "compose", "stack", stackID, "error", err, "removeVolumes", removeVolumes)
	}
}

// waitHealthy polls until every service is "running" or the timeout elapses.
func (h *Handler) waitHealthy(ctx context.Context, stackID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		svcs, err := h.Compose.GetProjectServices(ctx, stackID)
		reason := ""
		allUp := err == nil && len(svcs) > 0
		anyUnhealthy := false
		stillStarting := false
		switch {
		case err != nil:
			reason = "service status unavailable: " + err.Error()
		case len(svcs) == 0:
			reason = "no services found"
		default:
			for _, s := range svcs {
				// A container must be running — not restarting (crash loop),
				// exited, or created. This is what catches a stack that came up
				// and immediately fell over, which "running"-at-some-instant alone
				// would miss.
				if s.State != "running" {
					allUp = false
					reason = s.Name + " is " + s.State
					break
				}
				// When the service declares a Docker health-check, gate on it:
				// "(unhealthy)" is a real failure; "health: starting" is not yet a
				// failure (keep waiting). Without a health-check, running is all
				// Docker knows, so running == ready.
				if s.HasHealthcheck && strings.Contains(s.Status, "(unhealthy)") {
					anyUnhealthy = true
					reason = s.Name + " is unhealthy"
				} else if s.HasHealthcheck && strings.Contains(s.Status, "health: starting") {
					stillStarting = true
					if reason == "" {
						reason = s.Name + " health-check is still starting"
					}
				}
			}
		}
		if allUp && !anyUnhealthy && !stillStarting {
			return nil
		}
		if time.Now().After(deadline) {
			// A slow-warming health-check must not false-fail a stack that is
			// otherwise running and not explicitly unhealthy — accept it with a
			// note rather than rolling back a stack that is in fact coming up.
			if allUp && !anyUnhealthy && stillStarting {
				slog.Warn("migrate import: health gate elapsed while a health-check is still warming up; accepting running stack", "component", "compose", "stack", stackID)
				return nil
			}
			return fmt.Errorf("services did not become healthy within %s (%s)", timeout, reason)
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
		HasAbsBind:        facts.HasAbsBind,
		HasDevice:         facts.HasDevice,
		OverwriteAcked:    overwriteAcked,
		Disposition:       DispositionRetain, // preflight is disposition-agnostic for blocking; clone relaxation handled at migrate time
		// Real estimate: resolved config + actual volume/bind/image data sizes +
		// headroom, so the insufficient-disk block reflects the true transfer.
		EstimatedBytes: int64(len(cfgJSON)) + estimateTransferBytes(ctx, facts) + (16 << 20),
	}

	// Target facts via the cross-node info endpoint. A 200 with an unparseable
	// body (version skew, truncation) must NOT masquerade as a clean pass — it
	// gets the same target-unreachable warning so the operator isn't shown a
	// falsely-green report with arch/disk/port/overwrite checks silently skipped.
	path := "/api/v1/docker/compose/migrate/target-info?stackId=" + url.QueryEscape(project)
	status, body, perr := mgr.ProxyToNode(ctx, targetNodeID, http.MethodGet, path, nil, username)
	var jerr error
	if perr == nil && status == http.StatusOK {
		var wrapper struct {
			Data migrateTargetInfo `json:"data"`
		}
		if jerr = json.Unmarshal(body, &wrapper); jerr == nil {
			in.TargetArch = wrapper.Data.Arch
			in.TargetFreeBytes = wrapper.Data.FreeBytes
			in.TargetPortsInUse = wrapper.Data.PortsInUse
			in.TargetStackExists = wrapper.Data.StackExists
		}
	}
	report := BuildPreflightReport(in)
	if perr != nil || status != http.StatusOK || jerr != nil {
		report.Warnings = append(report.Warnings, PreflightFinding{Code: "target-unreachable", Message: "could not query the target node; disk/port/arch/overwrite checks were skipped"})
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

	// Serialize migrations of the same stack — a second concurrent run could race
	// the first's quiesce/package/disposition destructively.
	if !h.tryAcquireMigration(project) {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "a migration for this stack is already in progress")
	}
	defer h.releaseMigration(project)

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

	// Detach the orchestration from the request context so a client/proxy
	// disconnect (or the SSE relay timeout) cannot cancel the
	// quiesce/transfer/ROLLBACK/finalize docker operations mid-flight — the
	// rollback that restores the source MUST run even when the operator is gone.
	// The request context governs only SSE writes (no-ops once disconnected).
	// 2h bounds a large data+image transfer (volume tar + docker save/load).
	opCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	username, _ := c.Get("username").(string)

	// 1. preflight — abort on any block.
	send(PhasePreflight, "Running pre-flight checks...", false)
	report, perr := h.gatherPreflight(opCtx, project, req.TargetNodeID, username, req.OverwriteAcked)
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
	if _, err := h.Compose.Stop(opCtx, project); err != nil {
		h.reportRollback(send, project, "quiesce failed", err.Error())
		return nil
	}

	// 3. package — archive definition + volume/bind data + images into a bundle.
	send(PhasePackage, "Packaging stack (data + images)...", false)
	yamlPath, _ := h.Compose.ResolveComposeFile(opCtx, project)
	composeFile := filepath.Base(yamlPath)
	hasEnv := false
	if _, err := os.Stat(filepath.Join(h.ComposePath, project, ".env")); err == nil {
		hasEnv = true
	}
	manifest := buildDefinitionManifest(project, composeFile, hasEnv, mgr.LocalNodeID(), runtime.GOARCH, req.TargetNodeID, disp, req.OverwriteAcked)
	// Populate data sections from the resolved config. A parse failure falls back
	// to a definition-only migration rather than aborting the run.
	if cfgJSON, cerr := h.Compose.GetResolvedConfig(opCtx, project); cerr == nil {
		if facts, ferr := parseStackConfig(cfgJSON, filepath.Join(h.ComposePath, project)); ferr == nil {
			manifest.Volumes = facts.Volumes
			manifest.Binds = facts.Binds
			manifest.Images = facts.Images
			manifest.Devices = facts.Devices
			manifest.Ports = facts.HostPorts
		}
	}
	tmpDir, terr0 := os.MkdirTemp(h.ComposePath, ".mig-pkg-*")
	if terr0 != nil {
		h.reportRollback(send, project, "packaging failed", terr0.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	bundleFile := filepath.Join(tmpDir, "bundle.tar")
	sha, pkgErr := packageFullBundle(opCtx, bundleFile, &manifest, filepath.Join(h.ComposePath, project), tmpDir)
	if pkgErr != nil {
		h.reportRollback(send, project, "packaging failed", pkgErr.Error())
		return nil
	}

	// 4. transfer — stream the bundle to the target; it restores + ups + healthchecks.
	send(PhaseTransfer, "Transferring to target...", false)
	status, body, terr := h.pushBundleFileToTarget(opCtx, req.TargetNodeID, username, bundleFile, sha)
	if terr != nil || status != http.StatusOK {
		cause := ""
		if terr != nil {
			// The target may have received the bundle and come up anyway (it
			// detaches its up from the connection); flag a possible orphan.
			cause = terr.Error() + " (verify the target for an orphaned copy)"
		} else {
			cause = "target status " + http.StatusText(status) + ": " + string(body)
		}
		h.reportRollback(send, project, "transfer/restore failed", cause)
		return nil
	}

	// 5. finalize — disposition (target is healthy now). A finalize failure does
	// NOT undo the successful migration, so it terminates as DONE with a warning
	// rather than masquerading as a failed migration.
	send(PhaseFinalize, "Applying source disposition ("+string(disp)+")...", false)
	if err := h.migrateFinalize(opCtx, project, disp); err != nil {
		send(PhaseDone, response.SanitizeOutput("migrated to target (running); source cleanup needs attention: "+err.Error()), true)
		return nil
	}
	send(PhaseDone, "Migration complete.", true)
	return nil
}

// reportRollback restarts the source after a pre-finalize failure and emits an
// honest terminal event — it claims "source restarted" only if the restart
// actually succeeded, otherwise a PhaseError telling the operator to start it.
// It runs on a FRESH bounded context (not the op context) so the source restart
// still happens when the failure was the op deadline itself expiring.
func (h *Handler) reportRollback(send func(string, string, bool), project, what, cause string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if rerr := h.migrateRollback(ctx, project); rerr != nil {
		send(PhaseError, response.SanitizeOutput(what+"; SOURCE FAILED TO RESTART — start it manually: "+cause), true)
		return
	}
	send(PhaseRollback, response.SanitizeOutput(what+"; source restarted: "+cause), true)
}

// migrateRollback restarts the source after a pre-finalize failure. Returns the
// restart error so the caller reports honestly instead of always claiming success.
func (h *Handler) migrateRollback(ctx context.Context, project string) error {
	if _, err := h.Compose.Start(ctx, project); err != nil {
		slog.Error("migration rollback: failed to restart source", "component", "compose", "project", project, "error", err)
		return err
	}
	return nil
}

// migrateFinalize applies the source disposition AFTER the target is healthy.
func (h *Handler) migrateFinalize(ctx context.Context, project string, d Disposition) error {
	switch d {
	case DispositionRetain:
		return nil // source already stopped
	case DispositionDelete:
		// Down (with volumes) the source, CHECKING the error: swallowing a down
		// failure would delete the definition while containers keep running.
		if _, err := h.Compose.DownWithVolumes(ctx, project); err != nil {
			return fmt.Errorf("source teardown failed (still running): %w", err)
		}
		return os.RemoveAll(filepath.Join(h.ComposePath, project))
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
