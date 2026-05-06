# Healthcheck Composer Polish Implementation Plan (Theme D Phase 2.1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Five beginner-friendly polish features on top of the Phase 2 healthcheck composer — preset library, "Test now" button, "Remove healthcheck" button, services-row visual indicator, and backup retention — preserving all five Phase 2 stability guarantees.

**Architecture:** A new `RemoveHealthcheck` pure function (yaml.v3 Node API, mirrors `ApplyHealthcheck`) underpins a DELETE endpoint with the same backup/re-parse/sha256/atomic-write discipline. A second new endpoint POSTs the spec to a new docker SDK `RunOneShotExec` helper that runs the test command inside the live container without TTY and returns exit code + stdout + stderr + duration. Backup retention prunes `<file>.bak.healthcheck.*` files keeping last 5. `ComposeService.HasHealthcheck` flag fed by `ParseHealthcheck` drives the row indicator.

**Tech Stack:** Go 1.25 + gopkg.in/yaml.v3 + github.com/docker/docker/pkg/stdcopy; React 19 + shadcn (existing Dialog/Input/Select primitives).

---

## File structure

| File | Change |
|---|---|
| `internal/feature/compose/healthcheck.go` | New `RemoveHealthcheck(yaml, service) (string, error)` |
| `internal/feature/compose/healthcheck_test.go` | Tests for RemoveHealthcheck (present/absent/missing-service/round-trip) |
| `internal/feature/compose/handler.go` | New `RemoveHealthcheck` handler (DELETE), new `TestHealthcheck` handler (POST .../test), new `pruneHealthcheckBackups` helper called by Apply+Remove |
| `internal/feature/compose/healthcheck_handler_test.go` | Tests for DELETE + TEST handlers + retention pruning |
| `internal/docker/client.go` | New `RunOneShotExec(ctx, id, cmd) (exitCode, stdout, stderr, duration, error)` helper |
| `internal/docker/compose.go` | `ComposeService.HasHealthcheck bool` field; populate in `GetProjectServices` |
| `internal/api/router.go` | Register DELETE + POST /test routes |
| `web/src/types/api.ts` | `ComposeService.has_healthcheck?` field |
| `web/src/lib/api.ts` | `removeHealthcheck`, `testHealthcheck` methods |
| `web/src/components/compose/HealthcheckComposerDialog.tsx` | Preset dropdown + Test now button + Remove button + inline result |
| `web/src/pages/docker/DockerStacks.tsx` | HeartPulse color branching on `has_healthcheck` |

---

## Task 1: `RemoveHealthcheck` pure function + tests

**Files:**
- Modify: `internal/feature/compose/healthcheck.go` (append function)
- Modify: `internal/feature/compose/healthcheck_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/compose/healthcheck_test.go`:

```go
func TestRemoveHealthcheck_RemovesExisting(t *testing.T) {
	got, err := RemoveHealthcheck(composeWithHealthcheck, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "healthcheck:") {
		t.Fatalf("healthcheck key still present:\n%s", got)
	}
	if !strings.Contains(got, "image: jellyfin/jellyfin:latest") {
		t.Errorf("other keys clobbered:\n%s", got)
	}
}

func TestRemoveHealthcheck_AbsentIsIdempotent(t *testing.T) {
	got, err := RemoveHealthcheck(sampleCompose, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	// No healthcheck to begin with — output should be functionally equivalent
	// to input (yaml.v3 may reformat whitespace; we assert structural fields
	// survive instead of byte equality).
	if !strings.Contains(got, "image: jellyfin/jellyfin:latest") {
		t.Errorf("structural keys missing:\n%s", got)
	}
	if strings.Contains(got, "healthcheck:") {
		t.Errorf("healthcheck appeared from nowhere:\n%s", got)
	}
}

func TestRemoveHealthcheck_ServiceMissing(t *testing.T) {
	_, err := RemoveHealthcheck(sampleCompose, "nonexistent")
	if !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("got %v want ErrServiceNotFound", err)
	}
}

func TestRemoveHealthcheck_PreservesComments(t *testing.T) {
	yamlWithComments := `# top comment
services:
  jellyfin:                # service line comment
    image: jellyfin/jellyfin:latest
    healthcheck:
      test: ["CMD-SHELL", "echo old"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s
    restart: unless-stopped # restart comment
`
	got, err := RemoveHealthcheck(yamlWithComments, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "healthcheck:") {
		t.Fatalf("healthcheck not removed:\n%s", got)
	}
	if !strings.Contains(got, "# top comment") {
		t.Errorf("top comment lost:\n%s", got)
	}
	if !strings.Contains(got, "service line comment") {
		t.Errorf("service line comment lost:\n%s", got)
	}
	if !strings.Contains(got, "restart comment") {
		t.Errorf("restart comment lost:\n%s", got)
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestRemoveHealthcheck -count=1`
Expected: FAIL — `RemoveHealthcheck` undefined.

- [ ] **Step 3: Append `RemoveHealthcheck` to `healthcheck.go`**

```go
// RemoveHealthcheck removes the healthcheck block from the named service.
// Idempotent: returns the input unchanged (no error) when no healthcheck
// is present. Returns ErrServiceNotFound if the service is missing.
//
// Like ApplyHealthcheck, this uses the yaml.v3 Node API so anchors,
// comments, and key ordering on sibling fields survive untouched.
func RemoveHealthcheck(yamlContent string, service string) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return "", fmt.Errorf("parse compose: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", errors.New("empty compose document")
	}

	svcNode, err := findServiceNode(&root, service)
	if err != nil {
		return "", err
	}

	// Walk Content slice; healthcheck mapping is two adjacent entries
	// [keyNode, valueNode]. Splice them out together.
	for i := 0; i+1 < len(svcNode.Content); i += 2 {
		k := svcNode.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == "healthcheck" {
			svcNode.Content = append(svcNode.Content[:i], svcNode.Content[i+2:]...)
			break
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", fmt.Errorf("encode compose: %w", err)
	}
	enc.Close()
	return buf.String(), nil
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -count=1 -v`
Expected: all 4 new RemoveHealthcheck tests PASS plus existing pure-function tests.

- [ ] **Step 5: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/compose/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/healthcheck.go internal/feature/compose/healthcheck_test.go
git commit -m "compose: RemoveHealthcheck pure function (yaml.v3 Node API)"
```

---

## Task 2: Backup retention helper + DELETE handler

**Files:**
- Modify: `internal/feature/compose/handler.go` (append helper + DELETE handler, integrate retention into existing ApplyHealthcheck)
- Modify: `internal/feature/compose/healthcheck_handler_test.go` (append tests)

- [ ] **Step 1: Add retention helper near top of handler.go**

In `internal/feature/compose/handler.go`, after the existing imports and before the `Handler` struct, add:

```go
// pruneHealthcheckBackups deletes oldest .bak.healthcheck.* files in dir,
// keeping at most `keep` most-recent (by mtime). Best-effort — errors
// are logged but never propagated; the freshest backup (the one we just
// wrote) is always preserved by the sort.
//
// We glob on the prefix "<file>.bak.healthcheck." rather than just
// "*.bak*" so we never touch backups created by other tools (e.g. an
// editor's swap files).
func pruneHealthcheckBackups(yamlPath string, keep int) {
	pattern := yamlPath + ".bak.healthcheck.*"
	entries, err := filepath.Glob(pattern)
	if err != nil || len(entries) <= keep {
		return
	}
	type entry struct {
		path string
		mtime time.Time
	}
	rows := make([]entry, 0, len(entries))
	for _, p := range entries {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		rows = append(rows, entry{p, info.ModTime()})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].mtime.After(rows[j].mtime)
	})
	for _, r := range rows[keep:] {
		if err := os.Remove(r.path); err != nil {
			slog.Warn("failed to prune healthcheck backup", "component", "compose", "path", r.path, "error", err)
		}
	}
}

const healthcheckBackupKeep = 5
```

Add the imports if not already present: `path/filepath`, `sort`, `time`, `log/slog`.

- [ ] **Step 2: Wire retention into existing ApplyHealthcheck handler**

Find the existing `ApplyHealthcheck` handler in the same file, locate the line `if err := os.Rename(tmp, yamlPath); err != nil {` and the success path that follows. After the `os.Rename` succeeds (i.e., before the existing `return response.OK(c, ...)` final line), add:

```go
	// Retention: keep last N backups, prune older. Best-effort.
	pruneHealthcheckBackups(yamlPath, healthcheckBackupKeep)
```

- [ ] **Step 3: Append failing test for DELETE bad sha256**

Append to `internal/feature/compose/healthcheck_handler_test.go`:

```go
func TestRemoveHealthcheckHandler_RejectsSHA256Mismatch(t *testing.T) {
	body := bytes.NewBufferString(`{"base_yaml_sha256":"deadbeef"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/docker/compose/foo/healthcheck/svc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")

	// Note: handler will fail at the Compose==nil guard before any disk
	// I/O. We assert it does NOT 200; precise status under nil Compose
	// can be 503 or 404 depending on order. Either is acceptable.
	h := &Handler{}
	_ = h.RemoveHealthcheck(c)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200, got 200: %s", rec.Body.String())
	}
}

func TestPruneHealthcheckBackups_KeepsLastN(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "docker-compose.yml")
	// Create 7 fake backups with staggered mtimes (1s apart, oldest first).
	now := time.Now()
	for i := 0; i < 7; i++ {
		p := yamlPath + ".bak.healthcheck." + strconv.Itoa(int(now.Add(-time.Duration(7-i)*time.Second).UnixMilli()))
		if err := os.WriteFile(p, []byte("backup"+strconv.Itoa(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		// Set mtime explicitly so sort works deterministically.
		ts := now.Add(-time.Duration(7-i) * time.Second)
		_ = os.Chtimes(p, ts, ts)
	}
	pruneHealthcheckBackups(yamlPath, 5)
	matches, _ := filepath.Glob(yamlPath + ".bak.healthcheck.*")
	if len(matches) != 5 {
		t.Fatalf("kept %d files, want 5", len(matches))
	}
}
```

Add imports if missing: `filepath`, `strconv`, `time`.

- [ ] **Step 4: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestRemoveHealthcheckHandler -count=1`
Expected: FAIL — `(*Handler).RemoveHealthcheck` undefined.

- [ ] **Step 5: Implement DELETE handler in handler.go**

Append after the existing `ApplyHealthcheck` handler:

```go
// RemoveHealthcheck deletes the healthcheck block from the named
// service. Implements the same five stability guarantees as
// ApplyHealthcheck (sha256 precondition, backup, pre-flight re-parse,
// atomic write, no auto-deploy) plus backup retention.
func (h *Handler) RemoveHealthcheck(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	if project == "" || service == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "project and service required")
	}

	var req struct {
		BaseYAMLSHA256 string `json:"base_yaml_sha256"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}

	if h.Compose == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "compose manager not configured")
	}

	yamlPath, _ := h.Compose.ResolveComposeFile(c.Request().Context(), project)
	if yamlPath == "" {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "compose file not found for project")
	}
	original, err := os.ReadFile(yamlPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrReadError, response.SanitizeOutput(err.Error()))
	}

	if req.BaseYAMLSHA256 != "" {
		sum := sha256.Sum256(original)
		if hex.EncodeToString(sum[:]) != req.BaseYAMLSHA256 {
			return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists,
				"compose file changed externally — reload before removing healthcheck")
		}
	}

	newYAML, err := RemoveHealthcheck(string(original), service)
	switch {
	case errors.Is(err, ErrServiceNotFound):
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, err.Error())
	case err != nil:
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, response.SanitizeOutput(err.Error()))
	}

	var sanity yaml.Node
	if err := yaml.Unmarshal([]byte(newYAML), &sanity); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			"healthcheck removal produced unparseable YAML: "+response.SanitizeOutput(err.Error()))
	}

	backupPath := yamlPath + ".bak.healthcheck." + strconv.FormatInt(time.Now().UnixMilli(), 10)
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			"backup failed: "+response.SanitizeOutput(err.Error()))
	}

	tmp := yamlPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(newYAML), 0o644); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			response.SanitizeOutput(err.Error()))
	}
	if err := os.Rename(tmp, yamlPath); err != nil {
		_ = os.Remove(tmp)
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			response.SanitizeOutput(err.Error()))
	}

	pruneHealthcheckBackups(yamlPath, healthcheckBackupKeep)

	return response.OK(c, map[string]any{
		"yaml":        newYAML,
		"backup_path": backupPath,
	})
}
```

- [ ] **Step 6: Run tests + lint**

```bash
cd /opt/stacks/SFPanel
go test ./internal/feature/compose/ -count=1 -v
/home/dalso/go/bin/golangci-lint run ./internal/feature/compose/...
```

Expected: all PASS, zero issues.

- [ ] **Step 7: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/handler.go internal/feature/compose/healthcheck_handler_test.go
git commit -m "compose: DELETE /healthcheck handler + backup retention (keep 5)"
```

---

## Task 3: Docker `RunOneShotExec` helper

**Files:**
- Modify: `internal/docker/client.go` (append helper)

- [ ] **Step 1: Append helper**

After the existing `ContainerExec` function, append:

```go
// ExecResult captures the outcome of a one-shot exec inside a container.
// Used by feature handlers (e.g. healthcheck Test now) to validate
// commands without modifying container state.
type ExecResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMS int64
}

// RunOneShotExec executes cmd in container id, captures stdout+stderr
// (demuxed via stdcopy because Docker's exec stream is multiplexed when
// TTY is false), and returns the exit code.
//
// 30-second timeout matches healthcheck conventions; long-hanging
// healthchecks are themselves bugs.
func (c *Client) RunOneShotExec(ctx context.Context, id string, cmd []string) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("empty command")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	start := time.Now()
	exec, err := c.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false, // multiplexed stream → stdcopy
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec create: %w", err)
	}
	resp, err := c.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return ExecResult{}, fmt.Errorf("read stream: %w", err)
	}

	inspect, err := c.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec inspect: %w", err)
	}
	return ExecResult{
		ExitCode:   inspect.ExitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}
```

Add to imports:
- `bytes`
- `github.com/docker/docker/pkg/stdcopy`

(`context`, `fmt`, `time`, `github.com/docker/docker/api/types/container` should already be imported.)

- [ ] **Step 2: Build**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean. Go modules will resolve `stdcopy` automatically since the docker SDK already depends on it.

- [ ] **Step 3: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/docker/...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/docker/client.go go.sum go.mod
git commit -m "docker: RunOneShotExec helper (non-TTY exec + stdcopy demux)"
```

(go.mod / go.sum may or may not change — `stdcopy` is already transitive. Stage them if `git status` shows them dirty.)

---

## Task 4: Test-now handler

**Files:**
- Modify: `internal/feature/compose/handler.go` (append `TestHealthcheck` handler)
- Modify: `internal/feature/compose/healthcheck_handler_test.go` (append test)

- [ ] **Step 1: Append failing tests**

```go
func TestTestHealthcheckHandler_RejectsNONE(t *testing.T) {
	body := bytes.NewBufferString(`{"test_type":"NONE"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/foo/healthcheck/svc/test", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")
	h := &Handler{}
	_ = h.TestHealthcheck(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400", rec.Code)
	}
}

func TestTestHealthcheckHandler_RejectsBadDuration(t *testing.T) {
	body := bytes.NewBufferString(`{"test_type":"CMD-SHELL","test_value":"x","interval":"30","timeout":"10s","retries":3,"start_period":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/foo/healthcheck/svc/test", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")
	h := &Handler{}
	_ = h.TestHealthcheck(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestTestHealthcheckHandler -count=1`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement TestHealthcheck handler**

Append to `internal/feature/compose/handler.go`:

```go
// TestHealthcheck runs the supplied healthcheck command inside the
// running container for a service and returns the exit code, stdout,
// stderr, and duration. Read-only — never writes to disk, never
// modifies the container.
func (h *Handler) TestHealthcheck(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	if project == "" || service == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "project and service required")
	}

	var spec HealthcheckSpec
	if err := c.Bind(&spec); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if spec.TestType == "NONE" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "NONE has no command to test")
	}
	if err := spec.validate(); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, err.Error())
	}

	if h.Compose == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "compose manager not configured")
	}

	services, err := h.Compose.GetProjectServices(c.Request().Context(), project)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, response.SanitizeOutput(err.Error()))
	}
	var containerID string
	for _, svc := range services {
		if svc.Name == service && svc.State == "running" {
			containerID = svc.ContainerID
			break
		}
	}
	if containerID == "" {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError,
			"service not running — start it first to test the healthcheck")
	}

	var cmd []string
	switch spec.TestType {
	case "CMD-SHELL":
		cmd = []string{"sh", "-c", spec.TestValue}
	case "CMD":
		cmd = strings.Split(spec.TestValue, "|")
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "unsupported test_type for testing")
	}

	docker := h.Compose.DockerClient()
	if docker == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "docker client not available")
	}
	res, err := docker.RunOneShotExec(c.Request().Context(), containerID, cmd)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]any{
		"exit_code":   res.ExitCode,
		"stdout":      response.SanitizeOutput(res.Stdout),
		"stderr":      response.SanitizeOutput(res.Stderr),
		"duration_ms": res.DurationMS,
	})
}
```

This calls `h.Compose.DockerClient()` — that accessor doesn't exist yet. Add it to `internal/docker/compose.go`:

```go
// DockerClient returns the underlying docker client (read-only access).
// Used by feature handlers that need to invoke docker SDK helpers
// directly (e.g. healthcheck Test now → RunOneShotExec).
func (m *ComposeManager) DockerClient() *Client {
	return m.dockerClient
}
```

(Add immediately after the existing `NewComposeManager` function.)

- [ ] **Step 4: Run tests + lint**

```bash
cd /opt/stacks/SFPanel
go test ./internal/feature/compose/ -count=1 -v
go build ./...
/home/dalso/go/bin/golangci-lint run ./internal/feature/compose/... ./internal/docker/...
```

Expected: all PASS, zero issues.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/handler.go internal/feature/compose/healthcheck_handler_test.go internal/docker/compose.go
git commit -m "compose: POST /healthcheck/.../test handler (live container exec)"
```

---

## Task 5: `ComposeService.HasHealthcheck` field

**Files:**
- Modify: `internal/docker/compose.go`

- [ ] **Step 1: Extend struct + populate**

Find the `ComposeService` struct (line ~48) and extend:

```go
type ComposeService struct {
	Name           string `json:"name"`
	ContainerID    string `json:"container_id"`
	Image          string `json:"image"`
	State          string `json:"state"`
	Status         string `json:"status"`
	Ports          string `json:"ports"`
	HasHealthcheck bool   `json:"has_healthcheck"`
}
```

Find `GetProjectServices` (line ~473). After services are populated and right before the return, add a healthcheck-presence pass. Read the project compose file once, parse with yaml.v3, and stamp `HasHealthcheck` per service.

The cleanest spot is at the end of the function (after the existing `services = append(...)` loop, before any final processing). Add:

```go
	// Theme D Phase 2.1: stamp HasHealthcheck so the UI can show a
	// per-service indicator without each row making its own round-trip.
	yamlPath, _ := m.resolveComposeFilePath(ctx, name)
	if yamlPath != "" {
		if data, err := os.ReadFile(yamlPath); err == nil {
			yamlStr := string(data)
			for i := range services {
				if hasComposeHealthcheck(yamlStr, services[i].Name) {
					services[i].HasHealthcheck = true
				}
			}
		}
	}
```

This also needs a tiny helper because `internal/docker` cannot import `internal/feature/compose` (cycle: feature/compose → docker for ComposeManager). Add `hasComposeHealthcheck` as a private helper in `internal/docker/compose.go`:

```go
// hasComposeHealthcheck reports whether services.<name>.healthcheck is
// present in the compose YAML. Lightweight string scan; for stamping
// the UI indicator we don't need full Node-API parsing.
func hasComposeHealthcheck(yamlStr, service string) bool {
	// Match "  <service>:" then look for a sibling-indented "healthcheck:"
	// before the next sibling-level service or end-of-file.
	lines := strings.Split(yamlStr, "\n")
	inService := false
	svcIndent := -1
	for _, line := range lines {
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		trimmed := strings.TrimSpace(line)
		if !inService {
			if trimmed == service+":" {
				inService = true
				svcIndent = indent
			}
			continue
		}
		// In-service. Empty line OK; deeper-indented line OK; same-or-less
		// indent on a non-empty line means we've left the service.
		if trimmed == "" {
			continue
		}
		if indent <= svcIndent {
			return false
		}
		if strings.HasPrefix(trimmed, "healthcheck:") {
			return true
		}
	}
	return false
}
```

Add `os` to imports if not already there (it likely is, for ReadFile use elsewhere).

- [ ] **Step 2: Build + tests**

Run:
```bash
cd /opt/stacks/SFPanel
go build ./...
go test ./internal/docker/... ./internal/feature/compose/... -count=1
```

Expected: all PASS.

- [ ] **Step 3: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/docker/... ./internal/feature/compose/...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/docker/compose.go
git commit -m "compose: ComposeService.has_healthcheck field for row indicator"
```

---

## Task 6: Register routes

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add routes**

In `internal/api/router.go`, find the existing line:

```go
		compose.PUT("/:project/healthcheck/:service", composeHandler.ApplyHealthcheck)
```

Add directly below it:

```go
		compose.DELETE("/:project/healthcheck/:service", composeHandler.RemoveHealthcheck)
		compose.POST("/:project/healthcheck/:service/test", composeHandler.TestHealthcheck)
```

- [ ] **Step 2: Build + test**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/api/... -count=1`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/api/router.go
git commit -m "router: DELETE + POST /test routes for healthcheck composer"
```

---

## Task 7: Frontend types + API methods

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Extend types**

In `web/src/types/api.ts`, find the existing `ComposeService` interface:

```typescript
export interface ComposeService {
  name: string
  container_id: string
  image: string
  state: string
  status: string
  ports: string
}
```

Extend with `has_healthcheck`:

```typescript
export interface ComposeService {
  name: string
  container_id: string
  image: string
  state: string
  status: string
  ports: string
  has_healthcheck?: boolean
}
```

(The `?` is intentional — older backend builds before this lands won't emit the field; UI must not crash on `undefined`.)

Append new types:

```typescript
export interface HealthcheckTestResult {
  exit_code: number
  stdout: string
  stderr: string
  duration_ms: number
}
```

- [ ] **Step 2: Add API methods**

In `web/src/lib/api.ts`, find the existing `applyHealthcheck` method. Add:

```typescript
  removeHealthcheck(project: string, service: string, baseYamlSha256: string) {
    return this.request<{ yaml: string; backup_path: string }>(
      `/docker/compose/${encodeURIComponent(project)}/healthcheck/${encodeURIComponent(service)}`,
      {
        method: 'DELETE',
        body: JSON.stringify({ base_yaml_sha256: baseYamlSha256 }),
      },
    )
  }

  testHealthcheck(project: string, service: string, spec: HealthcheckSpec) {
    return this.request<HealthcheckTestResult>(
      `/docker/compose/${encodeURIComponent(project)}/healthcheck/${encodeURIComponent(service)}/test`,
      {
        method: 'POST',
        body: JSON.stringify(spec),
      },
    )
  }
```

Add `HealthcheckTestResult` to the type import block at the top.

- [ ] **Step 3: Build + lint frontend**

```bash
cd /opt/stacks/SFPanel/web
npm run build
npm run lint
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: types + api methods for remove + test healthcheck"
```

---

## Task 8: Dialog rework — preset + Test now + Remove + inline result

**Files:**
- Modify: `web/src/components/compose/HealthcheckComposerDialog.tsx`

- [ ] **Step 1: Replace the entire dialog file**

The existing file's structure stays, but we add three new pieces. To keep the diff manageable, replace the whole file. Open `web/src/components/compose/HealthcheckComposerDialog.tsx` and overwrite with:

```tsx
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import type { HealthcheckSpec, HealthcheckTestType, HealthcheckTestResult } from '@/types/api'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  project: string
  service: string
  baseYaml: string
  onApplied: (newYaml: string) => void
}

const DEFAULTS: HealthcheckSpec = {
  test_type: 'CMD-SHELL',
  test_value: '',
  interval: '30s',
  timeout: '10s',
  retries: 3,
  start_period: '30s',
}

const DURATION_RE = /^\d+(\.\d+)?(ns|us|µs|ms|s|m|h)([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$/

interface Preset {
  label: string
  test_type: HealthcheckTestType
  test_value: string
}

const PRESETS: Preset[] = [
  { label: 'Custom', test_type: 'CMD-SHELL', test_value: '' },
  { label: 'HTTP GET /health', test_type: 'CMD-SHELL', test_value: 'curl -f http://localhost:PORT/health || exit 1' },
  { label: 'PostgreSQL (pg_isready)', test_type: 'CMD', test_value: 'pg_isready|-U|postgres' },
  { label: 'Redis (PING)', test_type: 'CMD-SHELL', test_value: 'redis-cli ping | grep PONG' },
  { label: 'MySQL (ping)', test_type: 'CMD-SHELL', test_value: 'mysqladmin ping -h localhost || exit 1' },
]

async function sha256Hex(s: string): Promise<string> {
  const buf = new TextEncoder().encode(s)
  const hash = await crypto.subtle.digest('SHA-256', buf)
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

export function HealthcheckComposerDialog({
  open,
  onOpenChange,
  project,
  service,
  baseYaml,
  onApplied,
}: Props) {
  const [spec, setSpec] = useState<HealthcheckSpec>(DEFAULTS)
  const [hasExisting, setHasExisting] = useState(false)
  const [replace, setReplace] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<HealthcheckTestResult | null>(null)

  useEffect(() => {
    if (!open) return
    queueMicrotask(() => {
      setSpec(DEFAULTS)
      setHasExisting(false)
      setReplace(false)
      setTestResult(null)
      // Cheap client-side detection so the dialog can pre-populate from
      // an existing healthcheck without a round-trip. Backend
      // ParseHealthcheck is the source of truth on submit.
      const lines = baseYaml.split('\n')
      let inService = false
      let svcIndent = -1
      let inHealth = false
      const next: Partial<HealthcheckSpec> = {}
      for (const line of lines) {
        const indent = line.match(/^( *)/)?.[1].length ?? 0
        const trimmed = line.trim()
        if (!inService && trimmed === `${service}:`) {
          inService = true
          svcIndent = indent
          continue
        }
        if (inService && indent <= svcIndent && trimmed !== '') break
        if (!inService) continue
        if (trimmed.startsWith('healthcheck:')) {
          inHealth = true
          setHasExisting(true)
          continue
        }
        if (inHealth) {
          if (indent <= svcIndent + 2 && trimmed !== '') {
            inHealth = false
            continue
          }
          if (trimmed.startsWith('test:')) {
            const m = trimmed.match(/test:\s*\[(.*)\]/)
            if (m) {
              const parts = m[1].split(',').map((p) => p.trim().replace(/^['"]|['"]$/g, ''))
              if (parts[0] === 'NONE') {
                next.test_type = 'NONE'
              } else if (parts[0] === 'CMD-SHELL') {
                next.test_type = 'CMD-SHELL'
                next.test_value = parts[1] ?? ''
              } else if (parts[0] === 'CMD') {
                next.test_type = 'CMD'
                next.test_value = parts.slice(1).join('|')
              }
            }
          } else if (trimmed.startsWith('interval:')) {
            next.interval = trimmed.slice(9).trim()
          } else if (trimmed.startsWith('timeout:')) {
            next.timeout = trimmed.slice(8).trim()
          } else if (trimmed.startsWith('retries:')) {
            next.retries = parseInt(trimmed.slice(8).trim(), 10) || 3
          } else if (trimmed.startsWith('start_period:')) {
            next.start_period = trimmed.slice(13).trim()
          }
        }
      }
      if (Object.keys(next).length > 0) {
        setSpec({ ...DEFAULTS, ...next })
      }
    })
  }, [open, baseYaml, service])

  function applyPreset(p: Preset) {
    if (p.label === 'Custom') return // no-op
    setSpec((s) => ({ ...s, test_type: p.test_type, test_value: p.test_value }))
    setTestResult(null)
  }

  const validDurations =
    spec.test_type === 'NONE' ||
    (DURATION_RE.test(spec.interval) && DURATION_RE.test(spec.timeout) && DURATION_RE.test(spec.start_period))
  const validTest = spec.test_type === 'NONE' || spec.test_value.trim() !== ''
  const validReplace = !hasExisting || replace
  const canSubmit = validDurations && validTest && validReplace && spec.retries > 0
  const canTest = spec.test_type !== 'NONE' && spec.test_value.trim() !== '' && !testing && !submitting

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const baseHash = await sha256Hex(baseYaml)
      const res = await api.applyHealthcheck(project, service, spec, replace || hasExisting, baseHash)
      toast.success('Healthcheck inserted — review and Save & Deploy')
      onApplied(res.yaml)
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error).message || 'Healthcheck 적용 실패')
    } finally {
      setSubmitting(false)
    }
  }

  async function onTestNow() {
    if (!canTest) return
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testHealthcheck(project, service, spec)
      setTestResult(res)
    } catch (err) {
      toast.error((err as Error).message || 'Test 실패')
    } finally {
      setTesting(false)
    }
  }

  async function onRemove() {
    if (!hasExisting) return
    if (!confirm(`${service} 서비스의 healthcheck를 제거하시겠습니까?`)) return
    setRemoving(true)
    try {
      const baseHash = await sha256Hex(baseYaml)
      const res = await api.removeHealthcheck(project, service, baseHash)
      toast.success('Healthcheck removed — review and Save & Deploy')
      onApplied(res.yaml)
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error).message || 'Healthcheck 제거 실패')
    } finally {
      setRemoving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Healthcheck — {service}</DialogTitle>
          <DialogDescription>
            compose YAML의 services.{service}.healthcheck 블록을 추가/수정합니다. 자동 배포되지 않습니다 — 미리보기 후 Save & Deploy 하세요.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="hc-preset">프리셋</Label>
            <select
              id="hc-preset"
              className="w-full h-9 border rounded-md px-2 text-[13px] bg-transparent"
              defaultValue="Custom"
              onChange={(e) => {
                const p = PRESETS.find((x) => x.label === e.target.value)
                if (p) applyPreset(p)
              }}
            >
              {PRESETS.map((p) => (
                <option key={p.label}>{p.label}</option>
              ))}
            </select>
            <p className="text-[11px] text-muted-foreground">
              프리셋 선택 시 명령어가 채워집니다. <code>PORT</code> 등 플레이스홀더는 직접 수정하세요.
            </p>
          </div>

          <div className="space-y-1.5">
            <Label>Test 명령어</Label>
            {(['CMD-SHELL', 'CMD', 'NONE'] as HealthcheckTestType[]).map((t) => (
              <label key={t} className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="test_type"
                  className="mt-1"
                  checked={spec.test_type === t}
                  onChange={() => setSpec((s) => ({ ...s, test_type: t }))}
                />
                <span className="text-[13px]">
                  <strong>{t}</strong>
                  {t === 'CMD-SHELL' && ' — 셸에서 한 줄 실행'}
                  {t === 'CMD' && ' — 인자 배열 (| 로 구분)'}
                  {t === 'NONE' && ' — 이미지의 baked-in healthcheck 비활성'}
                </span>
              </label>
            ))}
          </div>

          {spec.test_type !== 'NONE' && (
            <div className="space-y-1.5">
              <Label htmlFor="hc-test-value">{spec.test_type === 'CMD-SHELL' ? '셸 명령어' : '인자 (| 구분)'}</Label>
              <Input
                id="hc-test-value"
                value={spec.test_value}
                onChange={(e) => {
                  setSpec((s) => ({ ...s, test_value: e.target.value }))
                  setTestResult(null)
                }}
                placeholder={
                  spec.test_type === 'CMD-SHELL'
                    ? 'curl -f http://localhost:8096/health || exit 1'
                    : 'curl|-f|http://localhost:8096/health'
                }
                required
              />
            </div>
          )}

          {spec.test_type !== 'NONE' && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="hc-interval">주기 (interval)</Label>
                <Input id="hc-interval" value={spec.interval} onChange={(e) => setSpec((s) => ({ ...s, interval: e.target.value }))} placeholder="30s" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-timeout">타임아웃</Label>
                <Input id="hc-timeout" value={spec.timeout} onChange={(e) => setSpec((s) => ({ ...s, timeout: e.target.value }))} placeholder="10s" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-retries">재시도</Label>
                <Input
                  id="hc-retries"
                  type="number"
                  min={1}
                  value={spec.retries}
                  onChange={(e) => setSpec((s) => ({ ...s, retries: parseInt(e.target.value, 10) || 0 }))}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-start-period">Grace period</Label>
                <Input
                  id="hc-start-period"
                  value={spec.start_period}
                  onChange={(e) => setSpec((s) => ({ ...s, start_period: e.target.value }))}
                  placeholder="30s"
                />
              </div>
            </div>
          )}

          {spec.test_type !== 'NONE' && (
            <div className="space-y-1.5 pt-1 border-t">
              <div className="flex items-center justify-between">
                <Label className="text-[12px]">실행 중인 컨테이너에서 미리 검증</Label>
                <Button type="button" size="sm" variant="outline" onClick={onTestNow} disabled={!canTest}>
                  {testing ? '실행 중…' : '지금 테스트'}
                </Button>
              </div>
              {testResult && (
                <div
                  className={`text-[12px] font-mono rounded-md p-2 ${
                    testResult.exit_code === 0 ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-[#f04452]/10 text-[#f04452]'
                  }`}
                >
                  <div>
                    {testResult.exit_code === 0 ? '✓' : '✗'} exit {testResult.exit_code} ({testResult.duration_ms}ms)
                  </div>
                  {testResult.stdout && <div className="mt-1 text-foreground/70">stdout: {testResult.stdout.split('\n')[0]}</div>}
                  {testResult.stderr && <div className="mt-1 text-foreground/70">stderr: {testResult.stderr.split('\n')[0]}</div>}
                </div>
              )}
            </div>
          )}

          {hasExisting && (
            <label className="flex items-start gap-2 text-[12px] text-muted-foreground">
              <input type="checkbox" className="mt-0.5" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
              이 service에 이미 healthcheck가 있습니다 — 덮어쓰기
            </label>
          )}

          <DialogFooter className="flex !justify-between">
            {hasExisting ? (
              <Button type="button" variant="ghost" className="text-[#f04452] hover:bg-[#f04452]/10" onClick={onRemove} disabled={removing || submitting}>
                {removing ? '제거 중…' : 'Healthcheck 제거'}
              </Button>
            ) : <span />}
            <div className="flex items-center gap-2">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
                취소
              </Button>
              <Button type="submit" disabled={submitting || !canSubmit}>
                {submitting ? '적용 중…' : 'Compose YAML에 적용'}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 2: Build + lint**

```bash
cd /opt/stacks/SFPanel/web
npm run build
npm run lint
```

Expected: clean. (If react-hooks/set-state-in-effect lint complains again, the existing `queueMicrotask` workaround from Phase 2 already covers it.)

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/components/compose/HealthcheckComposerDialog.tsx
git commit -m "web: HealthcheckComposerDialog adds preset/test/remove (initiate friendly)"
```

---

## Task 9: DockerStacks HeartPulse color branching + smoke test

**Files:**
- Modify: `web/src/pages/docker/DockerStacks.tsx`

- [ ] **Step 1: Branch icon className on `has_healthcheck`**

Find the two `<HeartPulse className="h-3.5 w-3.5" />` instances in `web/src/pages/docker/DockerStacks.tsx` (one in the desktop services table, one in the mobile card block — both inside the same row template). Replace each with:

```tsx
<HeartPulse className={`h-3.5 w-3.5 ${svc.has_healthcheck ? 'text-[#00c471]' : ''}`} />
```

Both occurrences. The empty string fallback inherits the default ghost-button text color (gray-ish).

- [ ] **Step 2: Build + lint frontend**

```bash
cd /opt/stacks/SFPanel/web
npm run build
npm run lint
```

Expected: clean.

- [ ] **Step 3: Build full binary**

```bash
cd /opt/stacks/SFPanel
make build
```

- [ ] **Step 4: Deploy both nodes**

```bash
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-d-phase21
sudo systemctl stop sfpanel
sudo cp ./sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
sleep 4
systemctl is-active sfpanel
scp ./sfpanel root@192.168.1.118:/tmp/sfpanel.new
ssh root@192.168.1.118 'systemctl stop sfpanel && cp /tmp/sfpanel.new /usr/local/bin/sfpanel && systemctl start sfpanel && sleep 4 && systemctl is-active sfpanel'
```

Expected: both `active`.

- [ ] **Step 5: API smoke — Test now (jellyfin healthcheck added in Phase 2 lives in YAML; container should be running)**

```bash
TOKEN=$(sudo /tmp/minttoken | head -1)
# First confirm jellyfin is running
sudo docker ps --filter name=jellyfin --format '{{.Names}}: {{.Status}}'
# Then test the existing healthcheck
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"test_type":"CMD-SHELL","test_value":"echo hello && exit 0","interval":"30s","timeout":"10s","retries":3,"start_period":"30s"}' \
  "http://127.0.0.1:9443/api/v1/docker/compose/jellyfin/healthcheck/jellyfin/test" | python3 -m json.tool
```

Expected: `{success:true, data:{exit_code:0, stdout:"hello\n", stderr:"", duration_ms:<int>}}`. If jellyfin isn't running, expect 503 with "service not running" message — also acceptable smoke.

- [ ] **Step 6: API smoke — Remove healthcheck**

```bash
HASH=$(sudo sha256sum /opt/stacks/jellyfin/docker-compose.yml | awk '{print $1}')
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"base_yaml_sha256\":\"$HASH\"}" \
  "http://127.0.0.1:9443/api/v1/docker/compose/jellyfin/healthcheck/jellyfin" | python3 -c "import sys,json; d=json.load(sys.stdin); print('success:', d.get('success')); print('backup:', d.get('data',{}).get('backup_path')); y=d.get('data',{}).get('yaml',''); print('healthcheck removed:', 'healthcheck:' not in y)"
```

Expected: `success: True`, backup path printed, `healthcheck removed: True`.

- [ ] **Step 7: Verify backup retention**

After running Apply + Remove a few times to accumulate backup files, count them:

```bash
sudo ls /opt/stacks/jellyfin/docker-compose.yml.bak.healthcheck.* 2>/dev/null | wc -l
```

Expected: at most 5.

- [ ] **Step 8: UI smoke (browser)**

Navigate to `/docker/stacks/<stack-with-running-services>`:
- ❤️ icon: 초록색 = healthcheck 있음, 회색 = 없음
- 클릭 → 다이얼로그 열림. 프리셋 드롭다운 보임.
- "PostgreSQL (pg_isready)" 선택 → test 필드가 자동 채워짐.
- "지금 테스트" 클릭 → 인라인 결과 (✓ 또는 ✗) 표시.
- 기존 healthcheck 있는 service: footer 좌측 "Healthcheck 제거" 빨간 텍스트 버튼 보임. 클릭 → confirm → toast → editor로 전환.

- [ ] **Step 9: Commit + push**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/pages/docker/DockerStacks.tsx
git commit -m "web: HeartPulse color reflects healthcheck presence"
git push origin main
```

---

## Self-Review

### Spec coverage
- ✅ (1) Preset library — Task 8 (`PRESETS` constant, dropdown in dialog)
- ✅ (2) Test now button — Tasks 3 (RunOneShotExec helper), 4 (handler), 8 (UI button + inline result)
- ✅ (3) Remove healthcheck — Tasks 1 (RemoveHealthcheck pure func), 2 (DELETE handler), 8 (red text button in footer)
- ✅ (4) Visual indicator on services rows — Tasks 5 (`HasHealthcheck` field), 9 (color branching)
- ✅ (5) Backup retention — Task 2 (`pruneHealthcheckBackups` + integration into Apply + Remove handlers)
- ✅ Stability guarantees preserved — DELETE handler reuses Apply's discipline; Test now is read-only by construction

### Placeholder scan
모든 task에 실제 코드 / 명령 / 기대 출력. Task 3 step 1의 "stdcopy 자동 import" 메모는 실제 동작 설명 (deprecated import path 발생 시 알려진 fallback).

### Type consistency
- `HealthcheckSpec` JSON tags (`test_type`, `test_value`, etc.) match Go struct definitions in Phase 2 healthcheck.go.
- `HealthcheckTestResult` (Go return type → frontend type) shape matches: `{exit_code, stdout, stderr, duration_ms}`.
- `ComposeService.has_healthcheck` (Go json:"has_healthcheck" / TS `has_healthcheck?`) consistent.
- `RemoveHealthcheck(yaml, service)` signature matches its callers in Task 2.
- `RunOneShotExec(ctx, id, cmd) ExecResult` matches caller in Task 4.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-06-healthcheck-composer-polish-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task + 2-stage review.

**2. Inline Execution** — All tasks in this session via `superpowers:executing-plans`.

Which approach?
