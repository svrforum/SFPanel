# Contributing to SFPanel

Thanks for your interest in improving SFPanel! This guide covers how to build, test, and submit changes.

> **Security issues:** please do **not** open a public issue. See [SECURITY.md](SECURITY.md) for private disclosure.

## Architecture at a glance

SFPanel is a Go backend with an embedded React/TypeScript SPA, shipped as a single binary (`go:embed all:web/dist`).

```
cmd/sfpanel/        # Entry point + CLI subcommands
internal/
  api/router.go     # The single route-registration point
  api/response/      # OK/Fail helpers + error codes + SanitizeOutput
  common/exec/       # Commander interface (+ MockCommander for tests)
  db/                # SQLite open + version-tracked migrations
  cluster/           # Raft FSM, gRPC, mTLS
  feature/<name>/    # ~22 self-contained feature modules (the bulk of the code)
web/                # React SPA (embedded into the binary at build)
desktop/            # Tauri 2 wrapper
```

A feature module is self-contained (`handler.go` + siblings, injected dependencies) and registered **once** in `internal/api/router.go`. When in doubt, mirror the pattern of an existing module.

## Build & run

```bash
make build      # Frontend (npm) + backend (go build)
make test       # Go tests (same set CI runs)
make lint       # golangci-lint + eslint
make ci         # lint + test + build
make dev-api    # API only, :3628
make dev-web    # Vite dev server :5173 (proxies /api and /ws to :3628)
```

Requires Go (version per `go.mod`) and Node (version per `.nvmrc`).

## Code conventions

- **Command execution** goes through `exec.Commander` (5-minute timeout, stderr capture, test substitutability). The documented exceptions are live SSE/WS streaming, PTY sessions, and `tail -F` — add a one-line comment explaining why when you need one.
- **HTTP responses:** `response.OK(c, data)` / `response.Fail(c, status, code, msg)`. `code` is a constant from `internal/api/response/errors.go` — reuse or add one; don't invent ad-hoc strings. Never put raw command stderr in a response without `response.SanitizeOutput`.
- **Logging:** `log/slog` only, structured fields (`slog.Info("...", "key", val)`), never `fmt.Println` for observability.
- **Frontend:** TypeScript + React, Tailwind with semantic design tokens (not hardcoded hex), i18n via `react-i18next` (keep `en.json`/`ko.json` in parity).

## Testing expectations

Tests are required for things that fail silently or have strong contracts: security-sensitive parsing/validation (auth, path traversal, token handling), HTTP response-contract changes, cluster logic, and complex text parsers. Straight-through command wrappers and small UI tweaks don't need a test. Run `make test` before submitting.

## Submitting a change

1. Fork and create a topic branch off `main`.
2. Keep changes focused; match the surrounding style.
3. Ensure `make ci` passes.
4. Open a PR with a clear description of **what** and **why**. Link any related issue.
5. Use clear, declarative commit messages (see `git log --oneline` for the style).

By contributing, you agree your contributions are licensed under the project's [AGPL-3.0](LICENSE).
