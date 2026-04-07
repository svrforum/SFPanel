# SFPanel Code Quality Improvement - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve SFPanel code quality across backend, frontend, and CI/CD with unified command execution, structured logging, standardized error handling, unit tests, and development guidelines.

**Architecture:** Layer-by-layer horizontal improvement. Each phase applies one concern across all modules before moving to the next. Phases build on each other: exec unification enables mock testing, logging enables error tracing, error standardization enables consistent UX.

**Tech Stack:** Go 1.21+ (slog), React 19 / TypeScript 5.9, GitHub Actions, golangci-lint

**Spec:** `docs/superpowers/specs/2026-04-07-code-quality-improvement-design.md`

---

## Task 1: Extend common/exec and add MockCommander

**Files:**
- Modify: `internal/common/exec/exec.go`
- Create: `internal/common/exec/mock.go`

- [ ] **Step 1: Add Exists to Commander and add AptEnv helper**

The current `common/exec/exec.go` already has `Commander` interface with `Run`, `RunWithTimeout`, `RunWithEnv`, `Exists`. Verify it covers all use cases. Add `RunWithInput` for commands that need stdin (crontab):

```go
// Add to Commander interface in exec.go
type Commander interface {
	Run(name string, args ...string) (string, error)
	RunWithTimeout(timeout time.Duration, name string, args ...string) (string, error)
	RunWithEnv(env []string, name string, args ...string) (string, error)
	RunWithInput(input string, name string, args ...string) (string, error)
	Exists(name string) bool
}

// Add RunWithInput to SystemCommander
func (c *SystemCommander) RunWithInput(input string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", DefaultTimeout)
	}
	return string(out), err
}
```

- [ ] **Step 2: Create MockCommander for tests**

Create `internal/common/exec/mock.go`:

```go
package exec

import "time"

// MockCommander records calls and returns configured responses.
type MockCommander struct {
	Calls    []MockCall
	Outputs  map[string]MockResult
	Fallback MockResult
}

type MockCall struct {
	Name string
	Args []string
}

type MockResult struct {
	Output string
	Err    error
}

func NewMockCommander() *MockCommander {
	return &MockCommander{
		Outputs: make(map[string]MockResult),
	}
}

func (m *MockCommander) SetOutput(name string, output string, err error) {
	m.Outputs[name] = MockResult{Output: output, Err: err}
}

func (m *MockCommander) record(name string, args ...string) (string, error) {
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})
	if r, ok := m.Outputs[name]; ok {
		return r.Output, r.Err
	}
	return m.Fallback.Output, m.Fallback.Err
}

func (m *MockCommander) Run(name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) RunWithInput(_ string, name string, args ...string) (string, error) {
	return m.record(name, args...)
}

func (m *MockCommander) Exists(name string) bool {
	m.Calls = append(m.Calls, MockCall{Name: "exists:" + name})
	_, ok := m.Outputs["exists:"+name]
	return ok
}
```

- [ ] **Step 3: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./internal/common/exec/...`
Expected: BUILD SUCCESS

- [ ] **Step 4: Commit**

```bash
git add internal/common/exec/
git commit -m "feat: extend Commander interface with RunWithInput and add MockCommander"
```

---

## Task 2: Migrate handlers to use Commander (Phase 1 core)

**Files:**
- Modify: `internal/feature/services/handler.go` (add Cmd field, replace 10 exec.Command calls)
- Modify: `internal/feature/system/handler.go` (add Cmd field, replace 4 exec.Command calls)
- Modify: `internal/feature/system/tuning.go` (replace exec.Command calls)
- Modify: `internal/feature/cron/handler.go` (add Cmd field, replace 2 exec.Command calls)
- Modify: `internal/feature/logs/handler.go` (add Cmd field, replace 2 exec.Command calls)
- Modify: `internal/feature/process/handler.go` (add Cmd field)
- Modify: `internal/api/router.go` (inject Commander)

This task handles modules with empty Handler structs and direct exec.Command calls.

- [ ] **Step 1: Add Cmd field to services Handler and replace exec.Command**

In `internal/feature/services/handler.go`, add `Cmd` field:

```go
type Handler struct {
	Cmd exec.Commander
}
```

Replace all `exec.Command(name, args...).CombinedOutput()` with `h.Cmd.Run(name, args...)`.
Replace all `exec.Command(name, args...).Output()` with `h.Cmd.Run(name, args...)`.

Example transformation (line 66):
```go
// Before:
out, err := exec.Command("systemctl", "start", name).CombinedOutput()

// After:
out, err := h.Cmd.Run("systemctl", "start", name)
```

Apply same pattern to all 10 exec.Command calls in services/handler.go (lines 66, 83, 100, 117, 134, 161, 178, 266, 312).

Remove the `import "os/exec"` line.

- [ ] **Step 2: Add Cmd field to system Handler and replace exec.Command**

In `internal/feature/system/handler.go`, add `Cmd exec.Commander` to existing struct:

```go
type Handler struct {
	Version     string
	DBPath      string
	ConfigPath  string
	ComposePath string
	Cmd         exec.Commander
}
```

Replace exec.Command calls at lines 240, 241, 409, 410.

Also update `internal/feature/system/tuning.go` to accept Commander via the Handler (tuning functions reference Handler).

- [ ] **Step 3: Add Cmd to cron, logs, process handlers**

cron/handler.go: Add `Cmd exec.Commander`, replace lines 209, 219. For line 219 (crontab with stdin), use `h.Cmd.RunWithInput(content, "crontab", "-")`.

logs/handler.go: Add `Cmd exec.Commander`. Note: lines 351, 358 are streaming (tail -F, grep). These need `exec.CommandContext` for long-running processes. Keep these as direct exec.Command but wrap with context timeout. Add a comment explaining why.

process/handler.go: Add `Cmd exec.Commander` if it uses exec.Command (check actual usage).

- [ ] **Step 4: Update router.go to inject Commander**

```go
// Near top of NewRouter function
cmd := exec.NewCommander()

// Update handler creation
servicesHandler := &featureServices.Handler{Cmd: cmd}
systemHandler := &featureSystem.Handler{
    Version: version, DBPath: cfg.Database.Path,
    ConfigPath: cfgPath, ComposePath: "/opt/stacks", Cmd: cmd,
}
cronHandler := &featureCron.Handler{Cmd: cmd}
processesHandler := &featureProcess.Handler{Cmd: cmd}
```

Add import: `commonExec "github.com/svrforum/SFPanel/internal/common/exec"`

- [ ] **Step 5: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 6: Commit**

```bash
git add internal/feature/services/ internal/feature/system/ internal/feature/cron/ internal/feature/logs/ internal/feature/process/ internal/api/router.go
git commit -m "refactor: migrate services, system, cron, logs, process to Commander"
```

---

## Task 3: Migrate modules with existing exec wrappers to Commander

**Files:**
- Modify: `internal/feature/firewall/firewall.go` (add Cmd field)
- Modify: `internal/feature/firewall/firewall_ufw.go` (replace runCommand)
- Modify: `internal/feature/firewall/firewall_docker.go` (replace runCommand)
- Modify: `internal/feature/firewall/firewall_fail2ban.go` (replace runCommand)
- Delete: `internal/feature/firewall/exec.go`
- Modify: `internal/feature/network/network.go` (add Cmd field)
- Modify: `internal/feature/network/wireguard.go` (replace runCommand)
- Modify: `internal/feature/network/tailscale.go` (replace runCommand)
- Delete: `internal/feature/network/exec.go`
- Modify: `internal/feature/packages/handler.go` (add Cmd field)
- Delete: `internal/feature/packages/exec.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Migrate firewall module**

Add `Cmd exec.Commander` to firewall Handler struct.

Replace all `runCommand(name, args...)` calls with `h.Cmd.Run(name, args...)`.
Replace all `runCommandEnv(env, name, args...)` calls with `h.Cmd.RunWithEnv(env, name, args...)`.
Replace all `commandExists(name)` calls with `h.Cmd.Exists(name)`.

Delete `internal/feature/firewall/exec.go`.

Total: ~46 replacements across firewall_ufw.go, firewall_docker.go, firewall_fail2ban.go.

- [ ] **Step 2: Migrate network module**

Add `Cmd exec.Commander` to network Handler struct.

Replace all `runCommand`/`runCommandEnv` calls in wireguard.go, tailscale.go, network.go.
Replace direct `exec.Command` calls in network.go (lines 312, 544, 581, 643, 710).

Delete `internal/feature/network/exec.go`.

Total: ~24 replacements + 5 direct exec.Command replacements.

- [ ] **Step 3: Migrate packages module**

Add `Cmd exec.Commander` to packages Handler struct.

Replace `runCommand`/`runCommandEnv` calls and direct `exec.Command` calls.

Delete `internal/feature/packages/exec.go`.

- [ ] **Step 4: Migrate disk module**

Add `Cmd exec.Commander` to disk Handler struct.

Replace all `exec.Command` calls across disk_filesystems.go (~30), disk_blocks.go (~4), disk_partitions.go (~3), disk_lvm.go (~10), disk_raid.go (~6), disk_swap.go (~15).
Replace `commandExists` with `h.Cmd.Exists`.

Delete `internal/feature/disk/exec.go`.

Total: ~68 replacements.

- [ ] **Step 5: Migrate appstore module**

Add `Cmd exec.Commander` to appstore Handler struct (already has multiple fields).

Replace exec.Command calls at lines 672, 737, 764.

- [ ] **Step 6: Update router.go with all new Cmd injections**

```go
firewallHandler := &featureFirewall.Handler{Cmd: cmd}
networkHandler := &featureNetwork.Handler{Cmd: cmd}
packagesHandler := &featurePackages.Handler{Cmd: cmd}
diskHandler := &featureDisk.Handler{Cmd: cmd}
appStoreHandler := &featureAppstore.Handler{DB: database, ComposePath: "/opt/stacks", Cmd: cmd}
```

- [ ] **Step 7: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 8: Run E2E smoke test**

Start test instance and verify key APIs still work:
```bash
curl -s http://localhost:9443/api/v1/health
# + login, system info, docker containers, firewall status, services list
```

- [ ] **Step 9: Commit**

```bash
git add internal/feature/ internal/api/router.go
git commit -m "refactor: migrate all modules to Commander, delete per-module exec files"
```

---

## Task 4: Structured Logging Setup (Phase 2)

**Files:**
- Create: `internal/common/logging/logging.go`
- Modify: `cmd/sfpanel/main.go`
- Modify: `internal/config/config.go`
- Modify: `internal/api/router.go`
- Create: `internal/api/middleware/request_logger.go`

- [ ] **Step 1: Create logging package**

Create `internal/common/logging/logging.go`:

```go
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Setup initializes the global slog logger with the given level and output.
func Setup(level string, output io.Writer) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}

// SetupFromConfig initializes logging from config values.
// logLevel: "debug", "info", "warn", "error"
// logFile: file path or "" for stdout
func SetupFromConfig(logLevel, logFile string) {
	var output io.Writer = os.Stdout
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			slog.Error("failed to open log file, using stdout", "path", logFile, "error", err)
		} else {
			output = io.MultiWriter(os.Stdout, f)
		}
	}
	Setup(logLevel, output)
}
```

- [ ] **Step 2: Create request logging middleware**

Create `internal/api/middleware/request_logger.go`:

```go
package middleware

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
)

// RequestLogger logs each HTTP request with method, path, status, and duration.
func RequestLogger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Path() == "/api/v1/health" {
				return next(c)
			}

			start := time.Now()
			err := next(c)
			duration := time.Since(start)

			slog.Info("request",
				"method", c.Request().Method,
				"path", c.Path(),
				"status", c.Response().Status,
				"duration_ms", duration.Milliseconds(),
				"ip", c.RealIP(),
			)
			return err
		}
	}
}
```

- [ ] **Step 3: Update main.go to initialize logging**

Add logging setup before server start in `cmd/sfpanel/main.go`:

```go
import "github.com/svrforum/SFPanel/internal/common/logging"

// In main(), after config loading:
logging.SetupFromConfig(cfg.Log.Level, cfg.Log.File)
slog.Info("SFPanel starting", "version", version, "port", cfg.Server.Port)
```

- [ ] **Step 4: Register request logger middleware in router.go**

Add after CORS middleware:
```go
e.Use(mw.RequestLogger())
```

- [ ] **Step 5: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 6: Commit**

```bash
git add internal/common/logging/ internal/api/middleware/request_logger.go cmd/sfpanel/main.go internal/api/router.go
git commit -m "feat: add structured logging with slog and request logger middleware"
```

---

## Task 5: Replace all log.Printf calls with slog

**Files:**
- Modify: All files with log.Printf/Println/Fatalf calls (~15 files, 78 calls)

- [ ] **Step 1: Replace log calls in config and core**

`internal/config/config.go`: Replace `log.Println` with `slog.Warn`
`internal/db/sqlite.go`: Replace `log.Printf` with `slog.Info`/`slog.Error`
`internal/api/router.go`: Replace `log.Printf` with `slog.Warn`, `log.Fatalf` with `slog.Error` + `os.Exit(1)`

- [ ] **Step 2: Replace log calls in cluster package**

`internal/cluster/manager.go` (15 calls): Replace with `slog.Info`/`slog.Warn`/`slog.Error` with `"component", "cluster"` attribute.
`internal/cluster/raft.go`, `raft_transport.go`, `grpc_server.go`, `grpc_client.go`, `ws_relay.go`, `heartbeat.go`: Same pattern.

- [ ] **Step 3: Replace log calls in feature modules**

`internal/feature/firewall/firewall_docker.go` (6 calls): `slog.Warn`/`slog.Error`
`internal/feature/system/tuning.go` (5 calls): `slog.Info`/`slog.Warn` with `"component", "tuning"`
`internal/feature/appstore/handler.go` (2 calls): `slog.Warn`
`internal/feature/cluster/handler.go` (3 calls): `slog.Info`/`slog.Warn`
`internal/api/middleware/audit.go` (1 call): `slog.Error`
`internal/monitor/history.go` (5 calls): `slog.Debug`/`slog.Warn`

- [ ] **Step 4: Add command execution logging to Commander**

In `internal/common/exec/exec.go`, add logging to `Run`:

```go
func (c *SystemCommander) Run(name string, args ...string) (string, error) {
	return c.RunWithTimeout(DefaultTimeout, name, args...)
}

func (c *SystemCommander) RunWithTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		slog.Warn("command timeout", "cmd", name, "duration_ms", duration.Milliseconds())
		return string(out), fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		slog.Debug("command failed", "cmd", name, "duration_ms", duration.Milliseconds(), "error", err)
	}
	return string(out), err
}
```

- [ ] **Step 5: Remove all `import "log"` statements from modified files**

Verify no file imports both `log` and `log/slog`.

- [ ] **Step 6: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 7: Commit**

```bash
git add internal/ cmd/
git commit -m "refactor: replace all log.Printf with slog structured logging"
```

---

## Task 6: Error Handling Standardization (Phase 3)

**Files:**
- Modify: `internal/api/response/errors.go`
- Create: `internal/api/response/sanitize.go`
- Modify: `internal/feature/auth/handler.go` (rate limit on setup)

- [ ] **Step 1: Add new error codes**

Add to `internal/api/response/errors.go`:

```go
// Command execution errors
var ErrCommandTimeout = "COMMAND_TIMEOUT"
var ErrPermissionDenied = "PERMISSION_DENIED"

// Unified tool errors
var ErrToolNotFound = "TOOL_NOT_INSTALLED"
```

- [ ] **Step 2: Create output sanitizer**

Create `internal/api/response/sanitize.go`:

```go
package response

import (
	"regexp"
	"strings"
)

var (
	pathPattern     = regexp.MustCompile(`/home/[^\s:]+`)
	userPattern     = regexp.MustCompile(`(?i)(user|username)[=:\s]+\S+`)
	sensitivePattern = regexp.MustCompile(`(?i)(password|secret|token|key)[=:\s]+\S+`)
)

// SanitizeOutput removes sensitive information from command output
// before returning it to the client.
func SanitizeOutput(output string) string {
	result := output
	result = pathPattern.ReplaceAllString(result, "/home/***")
	result = sensitivePattern.ReplaceAllString(result, "$1=***")
	// Limit length to prevent large stderr dumps
	if len(result) > 500 {
		result = result[:500] + "... (truncated)"
	}
	return strings.TrimSpace(result)
}
```

- [ ] **Step 3: Add rate limiting to setup endpoint**

In `internal/feature/auth/handler.go`, add rate limiting to `SetupAdmin` handler using the same pattern as the existing login rate limiter:

```go
// Add setup rate limiter (same pattern as loginLimiter)
var setupLimiter = struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}{attempts: make(map[string][]time.Time)}

// In SetupAdmin handler, at the top:
func (h *Handler) SetupAdmin(c echo.Context) error {
	ip := c.RealIP()
	setupLimiter.mu.Lock()
	now := time.Now()
	attempts := setupLimiter.attempts[ip]
	// Keep only last minute
	valid := attempts[:0]
	for _, t := range attempts {
		if now.Sub(t) < time.Minute {
			valid = append(valid, t)
		}
	}
	if len(valid) >= 5 {
		setupLimiter.mu.Unlock()
		return Fail(c, http.StatusTooManyRequests, ErrRateLimited, "Too many attempts")
	}
	setupLimiter.attempts[ip] = append(valid, now)
	setupLimiter.mu.Unlock()
	// ... rest of handler
}
```

- [ ] **Step 4: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/api/response/ internal/feature/auth/handler.go
git commit -m "feat: add error codes, output sanitizer, and setup rate limiting"
```

---

## Task 7: Unit Tests (Phase 4)

**Files:**
- Create: `internal/testutil/helpers.go`
- Create: `internal/feature/auth/handler_test.go`
- Create: `internal/feature/services/handler_test.go`
- Create: `internal/feature/firewall/firewall_test.go`
- Create: `internal/feature/cron/handler_test.go`
- Create: `internal/common/exec/exec_test.go`
- Create: `internal/api/response/sanitize_test.go`
- Modify: `Makefile`

- [ ] **Step 1: Add test targets to Makefile**

Add to `Makefile`:

```makefile
# 테스트
test:
	go test ./internal/... -v -count=1

# 테스트 커버리지
test-coverage:
	go test ./internal/... -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1
```

- [ ] **Step 2: Create test helpers**

Create `internal/testutil/helpers.go`:

```go
package testutil

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
)

// NewTestContext creates an Echo context for testing.
func NewTestContext(method, path, body string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	return c, rec
}

// NewTestDB creates an in-memory SQLite database for testing.
func NewTestDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	return db, nil
}
```

- [ ] **Step 3: Write Commander tests**

Create `internal/common/exec/exec_test.go`:

```go
package exec

import (
	"testing"
	"time"
)

func TestMockCommander_Run(t *testing.T) {
	m := NewMockCommander()
	m.SetOutput("echo", "hello\n", nil)

	out, err := m.Run("echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello\n" {
		t.Fatalf("expected 'hello\\n', got %q", out)
	}
	if len(m.Calls) != 1 || m.Calls[0].Name != "echo" {
		t.Fatalf("expected 1 call to echo, got %v", m.Calls)
	}
}

func TestMockCommander_Exists(t *testing.T) {
	m := NewMockCommander()
	m.SetOutput("exists:ufw", "", nil)

	if !m.Exists("ufw") {
		t.Fatal("expected ufw to exist")
	}
	if m.Exists("nonexistent") {
		t.Fatal("expected nonexistent to not exist")
	}
}

func TestSystemCommander_Exists(t *testing.T) {
	cmd := NewCommander()
	if !cmd.Exists("ls") {
		t.Fatal("expected ls to exist")
	}
	if cmd.Exists("nonexistent_command_xyz") {
		t.Fatal("expected fake command to not exist")
	}
}

func TestSystemCommander_Run(t *testing.T) {
	cmd := NewCommander()
	out, err := cmd.Run("echo", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "test\n" {
		t.Fatalf("expected 'test\\n', got %q", out)
	}
}

func TestSystemCommander_Timeout(t *testing.T) {
	cmd := NewCommander()
	_, err := cmd.RunWithTimeout(1*time.Millisecond, "sleep", "10")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
```

- [ ] **Step 4: Write sanitize tests**

Create `internal/api/response/sanitize_test.go`:

```go
package response

import (
	"strings"
	"testing"
)

func TestSanitizeOutput_StripsPaths(t *testing.T) {
	input := "Error: /home/dalso/config.yaml not found"
	result := SanitizeOutput(input)
	if strings.Contains(result, "dalso") {
		t.Fatalf("expected username stripped, got: %s", result)
	}
}

func TestSanitizeOutput_StripsSecrets(t *testing.T) {
	input := "password=mysecret123 connected"
	result := SanitizeOutput(input)
	if strings.Contains(result, "mysecret123") {
		t.Fatalf("expected secret stripped, got: %s", result)
	}
}

func TestSanitizeOutput_TruncatesLong(t *testing.T) {
	input := strings.Repeat("x", 1000)
	result := SanitizeOutput(input)
	if len(result) > 520 {
		t.Fatalf("expected truncated output, got length %d", len(result))
	}
}

func TestSanitizeOutput_ShortPassthrough(t *testing.T) {
	input := "Service started successfully"
	result := SanitizeOutput(input)
	if result != input {
		t.Fatalf("expected passthrough, got: %s", result)
	}
}
```

- [ ] **Step 5: Write services handler tests**

Create `internal/feature/services/handler_test.go`:

```go
package services

import (
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/testutil"
)

func TestListServices(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("systemctl", `  UNIT                    LOAD   ACTIVE SUB     DESCRIPTION
  docker.service          loaded active running Docker
  ssh.service             loaded active running OpenSSH
`)
	h := &Handler{Cmd: mock}

	c, rec := testutil.NewTestContext("GET", "/api/v1/system/services", "")
	err := h.ListServices(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStartService_InvalidName(t *testing.T) {
	mock := exec.NewMockCommander()
	h := &Handler{Cmd: mock}

	c, rec := testutil.NewTestContext("POST", "/api/v1/system/services/../../etc/passwd/start", "")
	c.SetParamNames("name")
	c.SetParamValues("../../etc/passwd")
	_ = h.StartService(c)
	if rec.Code == 200 {
		t.Fatal("expected rejection for path traversal service name")
	}
}
```

- [ ] **Step 6: Write cron handler tests**

Create `internal/feature/cron/handler_test.go`:

```go
package cron

import (
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/testutil"
)

func TestListJobs_Empty(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("crontab", "no crontab for root", nil)
	h := &Handler{Cmd: mock}

	c, rec := testutil.NewTestContext("GET", "/api/v1/cron", "")
	err := h.ListJobs(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestCreateJob(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("crontab", "", nil)
	h := &Handler{Cmd: mock}

	body := `{"schedule":"0 * * * *","command":"echo test","description":"test job"}`
	c, rec := testutil.NewTestContext("POST", "/api/v1/cron", body)
	err := h.CreateJob(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 7: Run all tests**

Run: `cd /opt/stacks/SFPanel && make test`
Expected: All tests PASS

- [ ] **Step 8: Commit**

```bash
git add internal/testutil/ internal/common/exec/exec_test.go internal/api/response/sanitize_test.go internal/feature/services/handler_test.go internal/feature/cron/handler_test.go Makefile
git commit -m "test: add unit tests for Commander, sanitizer, services, and cron"
```

---

## Task 8: Frontend Improvements (Phase 5)

**Files:**
- Modify: `web/src/lib/api.ts`
- Create: `web/src/components/ErrorBoundary.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Add request timeout to API client**

In `web/src/lib/api.ts`, modify the `request` method to add AbortController:

```typescript
private async request<T>(path: string, options: RequestInit & { local?: boolean; timeout?: number } = {}): Promise<T> {
    const timeout = options.timeout ?? 30000
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeout)

    try {
        const url = this.buildURL(path, options.local)
        const headers: Record<string, string> = {
            ...this.getHeaders(),
        }

        const res = await fetch(url, {
            ...options,
            headers,
            signal: controller.signal,
        })

        // ... existing 401 handling ...

        const json = await res.json()
        if (!json.success) {
            throw new Error(json.error?.message || 'Unknown error')
        }
        return json.data as T
    } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') {
            throw new Error('Request timed out')
        }
        throw err
    } finally {
        clearTimeout(timer)
    }
}
```

- [ ] **Step 2: Create Error Boundary component**

Create `web/src/components/ErrorBoundary.tsx`:

```typescript
import { Component, ReactNode } from 'react'

interface Props {
    children: ReactNode
}

interface State {
    hasError: boolean
    error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
    constructor(props: Props) {
        super(props)
        this.state = { hasError: false, error: null }
    }

    static getDerivedStateFromError(error: Error): State {
        return { hasError: true, error }
    }

    componentDidCatch(error: Error, info: React.ErrorInfo) {
        console.error('ErrorBoundary caught:', error, info.componentStack)
    }

    render() {
        if (this.state.hasError) {
            return (
                <div className="flex items-center justify-center min-h-screen bg-background">
                    <div className="text-center p-8 max-w-md">
                        <h1 className="text-2xl font-bold mb-4">오류가 발생했습니다</h1>
                        <p className="text-muted-foreground mb-6">
                            예상치 못한 오류가 발생했습니다. 페이지를 새로고침해 주세요.
                        </p>
                        <button
                            onClick={() => window.location.reload()}
                            className="px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90"
                        >
                            새로고침
                        </button>
                    </div>
                </div>
            )
        }
        return this.props.children
    }
}
```

- [ ] **Step 3: Wrap App with ErrorBoundary**

In `web/src/App.tsx`, wrap the root component:

```typescript
import { ErrorBoundary } from './components/ErrorBoundary'

function App() {
    return (
        <ErrorBoundary>
            {/* existing app content */}
        </ErrorBoundary>
    )
}
```

- [ ] **Step 4: Build and verify**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: BUILD SUCCESS, LINT PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/components/ErrorBoundary.tsx web/src/App.tsx
git commit -m "feat: add API timeout, Error Boundary, and error UX improvements"
```

---

## Task 9: CI/CD Enhancement (Phase 6)

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.golangci.yml`
- Modify: `internal/config/config.go`
- Modify: `Makefile`

- [ ] **Step 1: Create CI workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  go-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
          cache: true
      - run: go test ./internal/... -v -count=1

  go-lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
          cache: true
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  go-build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
          cache: true
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'
          cache-dependency-path: web/package-lock.json
      - run: cd web && npm ci && npm run build
      - run: go build -o sfpanel ./cmd/sfpanel

  frontend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'
          cache-dependency-path: web/package-lock.json
      - run: cd web && npm ci
      - run: cd web && npm run lint
      - run: cd web && npm run build
```

- [ ] **Step 2: Create golangci-lint config**

Create `.golangci.yml`:

```yaml
run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - gosimple
    - ineffassign
    - typecheck

linters-settings:
  errcheck:
    check-type-assertions: false

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - errcheck
```

- [ ] **Step 3: Add config validation and env overrides**

In `internal/config/config.go`, add:

```go
import "os"

// Validate checks that required config values are set.
func (c *Config) Validate() error {
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be positive, got %d", c.Server.Port)
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database.path is required")
	}
	return nil
}

// ApplyEnvOverrides applies environment variable overrides to config.
func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("SFPANEL_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			c.Server.Port = port
		}
	}
	if v := os.Getenv("SFPANEL_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	}
	if v := os.Getenv("SFPANEL_DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("SFPANEL_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
}
```

Call in `Load()` after YAML parsing:
```go
cfg.ApplyEnvOverrides()
if err := cfg.Validate(); err != nil {
    return nil, "", fmt.Errorf("config validation failed: %w", err)
}
```

- [ ] **Step 4: Update Makefile with ci target**

Add to `Makefile`:

```makefile
# CI - 로컬에서 전체 파이프라인 실행
ci: lint test build
```

- [ ] **Step 5: Build and verify**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/ci.yml .golangci.yml internal/config/config.go Makefile
git commit -m "feat: add CI workflow, golangci-lint config, and config validation"
```

---

## Task 10: Create CLAUDE.md with development guidelines

**Files:**
- Create: `CLAUDE.md`

- [ ] **Step 1: Create CLAUDE.md**

Create `CLAUDE.md` at project root:

```markdown
# SFPanel Development Guidelines

## Project Overview

SFPanel is a server management panel built with Go (backend) and React/TypeScript (frontend). It manages Docker, system services, files, firewall, network, and more.

## Architecture

```
internal/
  common/exec/       # Commander interface for system commands
  common/logging/     # slog-based structured logging
  api/
    router.go         # Route registration only
    middleware/        # JWT auth, audit, request logging, proxy
    response/         # Standardized API responses and error codes
  feature/            # 20 independent feature modules
    auth/             # Authentication, 2FA, password management
    docker/           # Container, image, volume, network management
    firewall/         # UFW, Fail2ban, Docker firewall
    disk/             # Partitions, filesystems, LVM, RAID, swap
    network/          # Interfaces, WireGuard, Tailscale
    ... (15 more)
```

## Key Rules

### Command Execution
- NEVER use `exec.Command` directly in handlers
- Always use the `Commander` interface via `h.Cmd.Run()`, `h.Cmd.RunWithTimeout()`, etc.
- Commander is injected via Handler struct for testability
- All commands have a 5-minute default timeout

### Error Handling
- Always use `response.OK()` / `response.Fail()` for HTTP responses
- Never return raw command stderr to clients — use `response.SanitizeOutput()`
- Use defined error codes from `response/errors.go`
- Never use `fmt.Errorf()` for client-facing errors

### Logging
- Use `log/slog` (never `log` or `fmt.Print` for logging)
- Include structured fields: `slog.Info("msg", "key", value)`
- Use component attribute for subsystem logs: `"component", "cluster"`

### Testing
- All new code must have unit tests
- Use `exec.MockCommander` for testing command execution
- Use `testutil.NewTestContext()` for handler tests
- Run: `make test`

### Adding a New Feature Module
1. Create `internal/feature/<name>/handler.go`
2. Define `Handler` struct with `Cmd exec.Commander` (if needed) and other deps
3. Register routes in `internal/api/router.go`
4. Inject Commander via `cmd` variable in router
5. Write tests in `handler_test.go`

## Build & Test

```bash
make build          # Build frontend + backend
make test           # Run Go tests
make test-coverage  # Tests with coverage report
make lint           # Go + frontend linting
make ci             # Full CI pipeline locally
make dev-api        # Run API in dev mode
make dev-web        # Run frontend with hot reload
```

## Tech Stack

- Backend: Go 1.23, Echo v4, SQLite (CGO-free)
- Frontend: React 19, TypeScript 5.9, Vite 7, Tailwind CSS 4, shadcn/ui
- Desktop: Tauri (Rust)
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add CLAUDE.md with development guidelines"
```

---

## Task 11: Final E2E Verification

- [ ] **Step 1: Full build**

```bash
cd /opt/stacks/SFPanel
make build
```

- [ ] **Step 2: Run all tests**

```bash
make test
```

- [ ] **Step 3: Run lint**

```bash
make lint
```

- [ ] **Step 4: Start test instance and E2E smoke test**

Start server and verify all major API endpoints work:
- Health, login, dashboard, docker, files, cron, services, firewall, network, disk, packages, settings, appstore, cluster, logs, processes

- [ ] **Step 5: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix: address issues found during final E2E verification"
```
