# SFPanel Development Guidelines

## Project Overview

SFPanel is a server management panel built with Go (backend) and React/TypeScript (frontend). It manages Docker, system services, files, firewall, network, disk, packages, and more.

## Architecture

```
internal/
  common/
    exec/           # Commander interface for system commands (with MockCommander for tests)
    logging/        # slog-based structured logging setup
  api/
    router.go       # Route registration only
    middleware/      # JWT auth, audit, request logging, cluster proxy
    response/       # Standardized API responses, error codes, output sanitizer
  feature/          # 20 independent feature modules
    auth/           # Authentication, 2FA, password management
    docker/         # Container, image, volume, network management
    compose/        # Docker Compose stack management
    firewall/       # UFW, Fail2ban, Docker firewall
    disk/           # Partitions, filesystems, LVM, RAID, swap
    network/        # Interfaces, WireGuard, Tailscale
    packages/       # APT packages, Docker, Node.js, Claude, Codex, Gemini
    services/       # systemd service management
    files/          # File browser and editor
    cron/           # Cron job management
    logs/           # Log viewer
    process/        # Process manager
    monitor/        # Dashboard metrics
    settings/       # Panel settings
    audit/          # Audit log
    system/         # Updates, backup/restore, tuning
    cluster/        # Multi-node cluster
    appstore/       # App store
    terminal/       # Web terminal (xterm.js)
    websocket/      # WebSocket real-time data
```

## Key Rules

### Command Execution
- NEVER use `os/exec.Command` directly in handlers
- Always use the `Commander` interface via `h.Cmd.Run()`, `h.Cmd.RunWithTimeout()`, `h.Cmd.RunWithEnv()`, `h.Cmd.RunWithInput()`
- Commander is injected via Handler struct field `Cmd exec.Commander` for testability
- All commands have a 5-minute default timeout
- Exception: streaming commands (SSE, tail -F) that need live stdout pipes must use `exec.CommandContext` directly with a comment explaining why

### Error Handling
- Always use `response.OK()` / `response.Fail()` for HTTP responses
- Never return raw command stderr to clients - use `response.SanitizeOutput()` first
- Use defined error codes from `response/errors.go` (150+ codes available)
- Never use `fmt.Errorf()` for client-facing error messages

### Logging
- Use `log/slog` exclusively (never `log` or `fmt.Print` for logging)
- Include structured fields: `slog.Info("msg", "key", value)`
- Use component attribute for subsystem logs: `"component", "cluster"`
- Request logging is handled by middleware automatically

### Testing
- All new code must have unit tests
- Use `exec.MockCommander` for testing command execution
- Use `testutil.NewTestContext()` for handler tests
- Run: `make test`

### Adding a New Feature Module
1. Create `internal/feature/<name>/handler.go`
2. Define `type Handler struct` with `Cmd exec.Commander` (if executing commands) and other deps
3. Register routes in `internal/api/router.go`
4. Inject `cmd` (shared Commander) from router's `NewRouter` function
5. Write tests in `handler_test.go`
6. Use `response.OK()` / `response.Fail()` for all responses

## Build & Test

```bash
make build          # Build frontend + backend
make test           # Run Go tests
make test-coverage  # Tests with coverage report
make lint           # Go + frontend linting
make ci             # Full CI pipeline locally (lint + test + build)
make dev-api        # Run API in dev mode
make dev-web        # Run frontend with hot reload
```

## Configuration

Config file: `config.yaml` (YAML format)

Environment variable overrides:
- `SFPANEL_PORT` - Server port
- `SFPANEL_JWT_SECRET` - JWT signing secret
- `SFPANEL_DB_PATH` - SQLite database path
- `SFPANEL_LOG_LEVEL` - Log level (debug, info, warn, error)

## Tech Stack

- Backend: Go 1.23, Echo v4, SQLite (CGO-free), slog
- Frontend: React 19, TypeScript 5.9, Vite 7, Tailwind CSS 4, shadcn/ui
- Desktop: Tauri (Rust)
- CI: GitHub Actions, golangci-lint
