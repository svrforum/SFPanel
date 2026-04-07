# SFPanel Code Quality Improvement Design

## Overview

Comprehensive code quality improvement for SFPanel after modular architecture migration (20 feature modules). Covers backend (Go), frontend (React/TypeScript), and CI/CD.

**Approach**: Layer-by-layer horizontal improvement — apply the same type of change across all modules before moving to the next concern. Each Phase verified by build + test before proceeding.

**Branch**: `improve/code-quality`

---

## Phase 1: Command Execution Unification

### Problem

- `common/exec/exec.go` defines `Commander` interface but is **never imported anywhere**
- `packages/exec.go` and `network/exec.go` are identical duplicates (60 lines each)
- `services`, `system` handlers call `exec.Command` directly **without timeout**
- `disk/exec.go` only has `commandExists()`, `firewall/exec.go` has its own wrappers

### Changes

1. **Extend `common/exec` package**
   - Add `AptEnv()` helper (currently duplicated)
   - Ensure `Commander` interface covers all use cases

2. **Add `Cmd` field to Handler structs**
   - Each handler that executes system commands gets `Cmd exec.Commander`
   - Enables mock injection for testing

3. **Update `router.go`**
   - Create shared `exec.NewCommander()` instance
   - Inject into all handlers that need it

4. **Delete per-module exec files**
   - `internal/feature/packages/exec.go` -> deleted
   - `internal/feature/network/exec.go` -> deleted
   - `internal/feature/disk/exec.go` -> deleted
   - `internal/feature/firewall/exec.go` -> deleted

5. **Replace all raw `exec.Command` calls**
   - ~12 modules affected: services, system, firewall, network, disk, packages, cron, logs, process, compose, cluster, appstore
   - All calls go through `h.Cmd.Run()` or `h.Cmd.RunWithTimeout()`

### Verification

- `go build ./...` after each module migration
- E2E test on running instance

---

## Phase 2: Structured Logging (slog)

### Problem

- 40+ log statements using standard `log` package
- No log levels, no structured fields
- Command execution failures often not logged
- Manual prefixes like `[WARN]`, `[cluster]`

### Changes

1. **Create `common/logging` package**
   - Initialize `slog.Logger` with JSON handler
   - Log levels: Debug, Info, Warn, Error
   - Configurable via `config.yaml` log level setting
   - Helper to create child logger with context (request_id, user)

2. **Add request logging middleware**
   - Log every request: method, path, status, duration
   - Add request_id to context for tracing
   - Skip health endpoint to reduce noise

3. **Add command execution logging to `Commander`**
   - Log command name, args, duration, success/failure at Debug level
   - Log failures at Warn level with output snippet

4. **Replace all `log.Printf` calls**
   - 40+ locations across codebase
   - Map existing prefixes to appropriate log levels:
     - `[WARN]` -> `slog.Warn()`
     - `[cluster]` -> `slog.Info()` with `"component"="cluster"`
     - `log.Fatalf` -> `slog.Error()` + `os.Exit(1)`

5. **Update `main.go`**
   - Initialize logger before server start
   - Pass logger to router/middleware

### Verification

- `go build ./...`
- Check log output format on running instance

---

## Phase 3: Error Handling Standardization

### Problem

- stderr output sent directly to client (leaks system paths)
- No timeout-specific error codes
- Docker errors all return single `ErrDockerError`
- `setup` endpoint has no rate limiting

### Changes

1. **Add error codes to `response/errors.go`**
   - `ErrCommandTimeout` - command exceeded timeout
   - `ErrPermissionDenied` - insufficient permissions
   - `ErrToolNotFound` - required system tool not installed (unify existing patterns)

2. **Create `response/sanitize.go`**
   - `SanitizeOutput(output string) string`
   - Strip absolute paths (`/home/xxx`, `/opt/xxx`)
   - Strip usernames from command output
   - Configurable: full output in development mode

3. **Standardize error returns across modules**
   - All handlers use `response.Fail()` exclusively
   - No raw `fmt.Errorf()` returned to client
   - Command output passed through `SanitizeOutput()` before client response

4. **Add rate limiting to setup endpoint**
   - Max 5 attempts per IP per minute
   - Use in-memory rate limiter (same pattern as login)

### Verification

- `go build ./...`
- Test error responses don't contain system paths
- Test setup rate limiting

---

## Phase 4: Unit Tests

### Problem

- Zero `*_test.go` files in entire Go codebase
- No Makefile test target
- Commander interface ready for mocking but unused

### Changes

1. **Test infrastructure**
   - `common/exec/mock.go` - `MockCommander` with configurable responses
   - `internal/testutil/` - shared test helpers (mock DB, mock Echo context, assertion helpers)
   - Makefile: `test` and `test-coverage` targets

2. **Module tests (priority order)**

   | Module | Test Focus | Estimated Tests |
   |--------|-----------|-----------------|
   | auth | login, password change, 2FA, setup, token validation | 15+ |
   | files | path validation, critical path blocking, CRUD | 10+ |
   | firewall | UFW output parsing, rule management | 8+ |
   | services | service list parsing, start/stop/restart | 8+ |
   | cron | CRUD operations, validation | 6+ |
   | packages | search parsing, update check | 6+ |
   | network | interface parsing, DNS config | 6+ |
   | docker | container list, inspect, stats | 6+ |
   | disk | partition parsing, filesystem list | 6+ |
   | logs | source listing, log reading | 4+ |
   | process | process list parsing | 4+ |
   | settings | get/update settings | 3+ |

3. **Coverage targets**
   - Parsing/transform functions: 90%+
   - Business logic (auth, files, firewall): 70%+
   - HTTP handler response codes: verified

### Verification

- `make test` passes
- `make test-coverage` shows targets met

---

## Phase 5: Frontend Improvements

### Problem

- API client has no request timeout (fetch can hang forever)
- No React Error Boundary (runtime errors show blank page)
- JWT token in localStorage (XSS vector)
- No error code to user message mapping

### Changes

1. **API client hardening (`web/src/lib/api.ts`)**
   - Add `AbortController` with 30-second timeout on all requests
   - Streaming requests: 5-minute timeout
   - Single retry on network error (500ms delay)
   - Offline detection: `navigator.onLine` check before requests

2. **Error Boundary component**
   - `web/src/components/ErrorBoundary.tsx`
   - Wraps entire app in `App.tsx`
   - Fallback UI with error message and refresh button
   - Logs error details to console

3. **Token management improvement**
   - Full httpOnly cookie migration deferred (requires backend auth flow changes)
   - This phase: validate token format before storing, clear on any parse error
   - Add token expiry check before requests

4. **Error UX**
   - Map server error codes to Korean user-friendly messages
   - Network offline banner component
   - Toast notification for transient errors

### Verification

- `npm run build` succeeds
- `npm run lint` passes
- Manual test: kill server -> verify timeout + retry + error UI

---

## Phase 6: CI/CD Enhancement

### Problem

- Only release workflow exists; no quality gates on PR/push
- No test or lint in CI
- Config has no validation; random JWT secret in production
- No environment variable override

### Changes

1. **CI workflow (`.github/workflows/ci.yml`)**
   - Trigger: push to main, all PRs
   - Jobs:
     - `go-test`: `go test ./...`
     - `go-lint`: `golangci-lint run`
     - `go-build`: `go build ./cmd/sfpanel`
     - `frontend-lint`: `npm run lint`
     - `frontend-build`: `npm run build`
   - Cache: Go modules + npm modules

2. **Lint configuration**
   - `.golangci.yml` with rules:
     - errcheck (unchecked errors)
     - govet (suspicious constructs)
     - staticcheck (static analysis)
     - unused (unused code)
     - gosimple (simplifications)

3. **Config validation (`internal/config/config.go`)**
   - `Validate()` method on Config struct
   - Required fields: port > 0, database path set
   - JWT secret: warn if auto-generated, error if empty in production
   - Environment variable overrides:
     - `SFPANEL_PORT`
     - `SFPANEL_JWT_SECRET`
     - `SFPANEL_DB_PATH`
     - `SFPANEL_LOG_LEVEL`

4. **Makefile updates**
   - `make test` - run Go tests
   - `make test-coverage` - tests with coverage report
   - `make lint` - run both Go and frontend linters
   - `make ci` - run full CI pipeline locally

### Verification

- Push to branch triggers CI
- All CI jobs pass
- Config validation catches invalid settings

---

## Execution Order and Dependencies

```
Phase 1 (exec unification)
    |
Phase 2 (structured logging) - depends on Phase 1 for Commander logging
    |
Phase 3 (error handling) - depends on Phase 2 for error logging
    |
Phase 4 (unit tests) - depends on Phase 1 for MockCommander, Phase 3 for error codes
    |
Phase 5 (frontend) - independent, but benefits from Phase 3 error codes
    |
Phase 6 (CI/CD) - depends on Phase 4 for test targets
```

## Risk Mitigation

- Each Phase: `go build` verification after every module change
- Each Phase ends with E2E smoke test on running instance
- Feature branch with atomic commits per Phase
- No API endpoint changes - all improvements are internal
- No database schema changes
