# SFPanel Contributor Guide

Server management panel — Go backend + React/TypeScript SPA embedded into a single Go binary (`go:embed all:web/dist`). Target: Ubuntu/Debian hosts with optional Docker. Docker is optional; if the socket isn't reachable, the 26 `/api/v1/docker/*` routes simply aren't registered.

This file is the working contract for anyone (human or AI) modifying this repo. It reflects **how the code actually is**, not a wish list. When in doubt, prefer the pattern used by existing modules and check the pointers at the bottom.

## Architecture at a glance

```
cmd/sfpanel/             # Entry point + CLI subcommands (cluster init/join/…)
internal/
  api/
    router.go            # Single route registration point
    middleware/          # JWT, audit, request logging, ClusterProxy (?node=…)
    response/            # OK/Fail helpers, 150+ error codes, SanitizeOutput
  common/
    exec/                # Commander interface + SystemCommander + MockCommander
    lifecycle/           # systemd helpers, process restart coordination
    logging/             # slog setup
  db/                    # SQLite open + 13-step idempotent migration list
  cluster/               # Raft FSM, gRPC, mTLS, JoinEngine, WS relay
  feature/               # 21 independent modules — the bulk of the codebase
    auth/ docker/ compose/ firewall/ disk/ network/ packages/
    services/ files/ cron/ logs/ process/ monitor/ settings/
    audit/ system/ cluster/ appstore/ terminal/ websocket/ alert/
  monitor/               # Background CPU/mem history collector (60s)
proto/                   # Cluster gRPC .proto + generated .pb.go
web/                     # React SPA (embedded into binary at build)
desktop/                 # Tauri 2 wrapper (Windows/macOS/Linux)
```

A feature module is self-contained: `handler.go` + siblings, injected dependencies, registered once in `internal/api/router.go`. There is **no per-module router registration** — keep `router.go` as the single wiring point.

## Code conventions

### 1. Command execution — use `Commander`, with documented exceptions

Batch-style commands go through `exec.Commander` (interface in `internal/common/exec/exec.go`):

```go
type Handler struct {
    Cmd exec.Commander   // injected in router.NewRouter
}

out, err := h.Cmd.Run("ufw", "status")
out, err := h.Cmd.RunWithTimeout(30*time.Second, "apt-get", "update")
out, err := h.Cmd.RunWithEnv([]string{"LANG=C"}, "lsblk", "-J")
out, err := h.Cmd.RunWithInput(yaml, "docker", "compose", "-f", "-", "up", "-d")
```

This gives you a 5-minute default timeout, stderr capture, consistent error wrapping, and — critically — test substitutability via `exec.MockCommander`.

**Exceptions where `os/exec` directly is correct** (see existing callers):

- **Live stdout/stderr streaming to SSE/WS clients** — `packages/`, `network/tailscale.go`, `system/` update flow, `compose/` up-stream/update-stream, `appstore/` install.
- **PTY sessions** — `terminal/` (via `creack/pty`).
- **Long-running `tail -F`** — `logs/`.
- **Interactive stdin piping** with progress feedback — `internal/docker/compose.go`.

When you need an exception: write a one-line comment explaining *why* the Commander interface doesn't fit (usually "streaming output to client"), propagate `ctx.Request().Context()` so the subprocess dies with the request, and use `response.SanitizeOutput` on anything that might reach the user.

New handlers should default to `Commander`. Migrating existing exception handlers isn't required unless you're already rewriting them.

### 2. HTTP responses

- `response.OK(ctx, data)` for success, `response.Fail(ctx, code, message)` for errors.
- `code` is a string constant from `internal/api/response/errors.go`. If the failure mode you're returning doesn't have a code yet, add one — don't invent ad-hoc strings.
- Never put raw command stderr into a client response. Wrap with `response.SanitizeOutput()` which strips ANSI and known sensitive patterns.
- Never `fmt.Errorf` into a user-facing message. Use error codes + translated UI text on the frontend.

### 3. Logging

- `log/slog` only. Never `log.*` or `fmt.Println` for observability.
- Structured fields, not interpolation: `slog.Info("service restarted", "name", name, "duration_ms", ms)`.
- Subsystem logs carry a `component` attribute: `slog.With("component", "cluster")`.
- Request-level access logs are injected by middleware; don't log per-request in handlers unless something unusual happened.

### 4. Cluster awareness

When the server is in cluster mode, **any protected route can be invoked with `?node=<nodeID>`** to target a different node. Three things follow:

- **Handlers stay local-only.** They don't know about cluster proxying — `ClusterProxyMiddleware` (`internal/api/middleware/proxy.go`) short-circuits the request and forwards to the target node via gRPC (30s) or HTTP relay (5m for SSE, WS upgrade for WebSocket) before your handler runs.
- **Streaming endpoints need an `-stream` path suffix** (or a match in the proxy's explicit allowlist — `/system/update`, `/appstore/.../install`, etc.) so the middleware uses HTTP relay instead of gRPC unary.
- **Don't assume local filesystem or service presence.** Graceful empty results are the norm for remote nodes lacking `ufw` / `crontab` / `rsyslog`. Return empty collections, not 500s.

Replicated state (JWT secret, cluster config, admin account) flows through the Raft FSM in `internal/cluster/raft_fsm.go`. Per-node state (metrics history, audit log rows, cron, docker) is local.

### 5. Error codes

- `internal/api/response/errors.go` is authoritative. Search before adding a duplicate.
- HTTP status is determined by the error code — see `StatusCode()` in the same file. Adding a code without a status mapping defaults to 500.
- User-facing messages are Korean/English via i18next on the frontend; the Go message is a fallback, keep it terse and actionable.

## Testing expectations

We do **not** require a test for every new handler. We do require tests for things that fail silently or have strong contracts:

| Required | Not required |
|----------|--------------|
| Security-sensitive parsing/validation (auth, path traversal, token verification) | Straight-through command wrappers whose output is JSON-serialized and returned |
| HTTP/response contract changes (new error code, status mapping) | Small UI-adjacent field renames |
| Cluster logic (Raft FSM, proxy routing, join/detect) | Frontend glue |
| Complex text parsers (firewall rules, lsblk, smartctl output) | |
| Shared infra (Commander, lifecycle, response helpers) | |

Use `exec.MockCommander` to substitute `Cmd` when asserting command-argument construction. Run `make test`; it's the same set CI runs. Coverage of the feature modules today is low — don't use that as a precedent, but do expand tests in the module you're already touching rather than opening a sweeping coverage push.

## Adding a feature module

1. `internal/feature/<name>/handler.go` — define `type Handler struct { DB *sql.DB; Cmd exec.Commander; ... }`. Only include what you actually use.
2. Register routes exactly once in `internal/api/router.go` — inside `NewRouter` use the `authorized` group unless the route is genuinely public (health, login, setup).
3. If you add DB state, append DDL to `internal/db/migrations.go` (use `CREATE TABLE IF NOT EXISTS`; migrations are re-run on every boot). Document the table in `docs/specs/db-schema.md`.
4. If you add WS or SSE endpoints, document them in `docs/specs/websocket-spec.md`.
5. Add tests per the Testing table above.
6. If the feature is cluster-relevant, decide: does it replicate (FSM), or is it per-node? If per-node, does `?node=` routing make sense? Document in the handler.

## Build & run

```bash
make build          # Frontend (npm) + backend (go build)
make test           # Go tests
make test-coverage
make lint           # golangci-lint + eslint
make ci             # lint + test + build
make dev-api        # API only, :8443 (default config)
make dev-web        # Vite dev server :5173, proxies /api and /ws to :8443
```

Version is injected at build via ldflags in the Makefile and exposed through `/api/v1/system/info` and `sfpanel version`.

## Configuration

File: `config.yaml` (path given as CLI arg; production install uses `/etc/sfpanel/config.yaml`).

Environment overrides:

- `SFPANEL_PORT`, `SFPANEL_JWT_SECRET`, `SFPANEL_DB_PATH`, `SFPANEL_LOG_LEVEL`

Schema reference: `docs/specs/tech-features.md` § Configuration + `internal/config/config.go`.

## Runtime layout (production install)

`scripts/install.sh` lays this out — don't hard-code other paths in code:

```
/usr/local/bin/sfpanel                  # binary
/etc/sfpanel/config.yaml                # 0600, JWT secret
/etc/sfpanel/cluster/                   # mTLS CA + node certs (cluster mode)
/var/lib/sfpanel/sfpanel.db             # SQLite + WAL
/var/lib/sfpanel/cluster/               # Raft logs, snapshots, BoltDB
/var/lib/sfpanel/compose/               # Per-stack compose files (appstore installs)
/var/log/sfpanel/sfpanel.log            # logrotate via /etc/logrotate.d/sfpanel
/etc/systemd/system/sfpanel.service     # Restart=always (see cluster note)
```

**`Restart=always`, not `on-failure`, is intentional**: several HTTP handlers (`/system/update`, `/cluster/leave`, `/cluster/disband`) exit the process after responding so the supervisor picks up a new binary or new cluster config. `on-failure` would treat those exits as done and leave the panel offline. If you add a handler that calls `os.Exit`, document it and confirm it's picked up by systemd.

## Ports

| Port | Purpose |
|------|---------|
| `server.port` (default 8443) | HTTP (plain, TLS is expected from a reverse proxy) |
| 9444 | Cluster gRPC (mTLS) — only bound when cluster enabled |
| 5173 | Vite dev server (dev only) |

## Deep-dive references

Start with these before making non-trivial changes — they reflect actual code at `2026-04-19`:

- `docs/specs/api-spec.md` — every REST/SSE route with request/response shapes
- `docs/specs/websocket-spec.md` — WS endpoints + SSE streaming catalog + cluster relay
- `docs/specs/db-schema.md` — all 10 tables, retention policies, WAL pragmas
- `docs/specs/frontend-spec.md` — pages, API client, state, i18n, build
- `docs/specs/tech-features.md` — feature-by-feature walkthrough
- `docs/superpowers/specs/2026-04-13-cluster-join-redesign.md` — cluster join redesign spec
- `docs/superpowers/research/2026-04-19-docs-overhaul/` — per-area ground-truth inventories used to produce the specs above

## Repo hygiene

- Commit author/committer must be `svrforum <svrforum.com@gmail.com>` (pass via `GIT_AUTHOR_*` / `GIT_COMMITTER_*` env vars; do not modify `.git/config`).
- Commit messages do not reference AI tooling. File paths containing `CLAUDE` are fine; trailers like `Co-Authored-By: Claude …` or `🤖 Generated with …` are not.
- Don't skip hooks (`--no-verify`) or bypass signing unless explicitly asked.
- Keep the existing short, declarative commit style visible in `git log --oneline`.
