# T5 — Specialized I/O

**Reviewed at:** v0.13.15 commit `f5d38a4` · 2026-05-18
**Scope:** `process` · `terminal` · `appstore` · `websocket`

---

## Findings count

| Module | P0 | P1 | P2 | P3 | Total |
|---|---|---|---|---|---|
| process | **1** | 2 | 3 | 3 | 9 |
| terminal | **2** | 4 | 5 | 4 | 15 |
| appstore | **2** | 6 | 5 | 3 | 16 |
| websocket | **2** | 3 | 3 | 3 | 11 |
| **Total** | **7** | **15** | **16** | **13** | **51** |

**Running total (T1+T2+T3+T4+T5): 25 P0, 77 P1, 93 P2, 101 P3 = 296 findings**

---

## P0 — must fix

### P0-16. `process.KillProcess` writes NO audit row
**File:** `process/handler.go:113-173`

A privileged "SIGKILL to PID X" produces zero audit trail. CLAUDE.md and project conventions require audit for state-changing operations; this is one of the loudest blind spots.

**Fix sketch:** explicit audit log call before returning OK with `pid`, `signal`, `username`, `node_id`. Reference pattern in auth security_events.

### P0-17. `terminal` slow-client head-of-line blocking
**File:** `terminal/handler.go:104-117`

Broadcast holds `readersMu` and synchronously writes to every connected WS with a 10s deadline. One slow/dead client stalls the PTY reader for up to 10s per other reader. With `maxTerminalSessions=20` and shared sessions per reconnect, one hung TCP can freeze interactive shells.

**Fix sketch:** per-reader bounded send channel (e.g. 64 frames) + non-blocking enqueue + drop reader on overflow.

### P0-18. `terminal` `started` flag is racy
**File:** `terminal/handler.go:139-142`

`startReader` checks `s.started` without holding any mutex. Outer `sessionsMu` serializes creation but the reconnect-on-dead-session path can produce different race shapes. Use `sync.Once` or move flag under `s.mu`.

### P0-19. `appstore` Advanced YAML — 2026-04-19 P0 **NOT closed**
**File:** `composex/safety.go:38-50` (shared between `compose` and `appstore`)

Three documented bypasses are STILL OPEN — `compose_safety.go` in appstore is a 1-line delegating alias for `composex.ValidateAdvancedCompose`, so any gap is shared between BOTH `compose` and `appstore`:

| Attack | Status | Evidence |
|---|---|---|
| `privileged: true` | CLOSED | safety.go:35-37 |
| `pid: "host"` / `network: "host"` / `ipc: "host"` (short form) | CLOSED | safety.go:38-41 |
| **`pid_mode: host` / `network_mode: host` / `ipc_mode: host`** (long form) | **OPEN** | not in the key list at safety.go:38 |
| `/:/hostfs` and `/etc`/`/root`/`/proc`/etc bind mounts | CLOSED | safety.go:107-124 |
| `cap_add: [SYS_ADMIN/SYS_MODULE/SYS_PTRACE/ALL]` | CLOSED | safety.go:47 |
| **`cap_add: [CAP_SYS_ADMIN]`** (canonical Docker form) | **OPEN** | ToUpper doesn't strip CAP_ prefix |
| **`group_add: [docker]`** | **OPEN** | `group_add` not inspected anywhere |
| Docker socket bind mount (`/var/run/docker.sock`) | CLOSED | safety.go:120-123 |

**Fix sketch (single PR closes both surfaces):**
1. Add `pid_mode`/`network_mode`/`ipc_mode` to the long-form key list.
2. Strip optional `CAP_` prefix before comparing cap names.
3. Add `group_add` check rejecting `docker` (+ other security-sensitive groups: `disk`, `sudo`, `wheel`).

### P0-20. `appstore` Advanced mode has no privileged re-auth
**File:** `appstore/handler.go:490-491, 583-598`

Stolen JWT → POST `advanced=true` → any YAML the validator misses → RCE. Combined with P0-19 validator gaps, this stacks risk.

**Fix sketch:** require a fresh password/2FA confirmation in the last 60s for any `advanced=true` install. Mirror SetupAdmin's pattern.

### P0-21. `websocket.MetricsWS` missing keepalive
**File:** `websocket/handler.go:113-147`

No `startWSKeepalive`, no `SetReadDeadline`, no `SetPongHandler`. Per the file's own comment block at lines 50-58, half-open connections (laptop lid, NAT timeout) leave read goroutines parked on `ReadMessage` until OS-level TCP keepalive fires (often hours).

**Fix sketch:** add `startWSKeepalive(ctx, ws, writer)` + initial `SetReadDeadline` — pattern already exists in the same file for other WS handlers.

### P0-22. `websocket` permissive `CheckOrigin` + legacy `?token=` path live
**Files:** `websocket/handler.go:23-25`, `internal/auth/wsauth.go:25-33`

`CheckOrigin` returns `true` unconditionally. The legacy `?token=` JWT path is still accepted (not just ticket-based). A non-panel page running in the user's browser can open `wss://panel/...?token=<exfiltrated JWT from localStorage XSS>` and the upgrade succeeds.

**Fix sketch:** restrict `CheckOrigin` to the panel's configured `server.url` / same-origin, OR drop the legacy `?token=` path and require ticket-only.

---

## Per-module sections

### process

**What it does:** Lists running processes via gopsutil (3s TTL cache); sends signals (TERM/KILL/HUP/INT) to a PID.

**Findings:**

- **P0-16** — no audit log on KillProcess (see top).
- **P1 — `Cmdline` returned without `SanitizeOutput`** (`handler.go:201, 219`). Argv frequently contains credentials (`mysqld --password=`, container env). Wrap + truncate.
- **P1 — Unbounded result set** (`handler.go:99-109`). 1500 processes = ~400 KB JSON per poll. Cap + paginate + sort.
- **P2** ×3 — context not propagated to refresh, per-call slice copy on cache hit (sort moved to refresh fixes both), error swallowed without slog.
- **P3** ×3 — cache invalidation post-kill races with concurrent refresh (benign), signal allowlist narrow (no USR1/USR2), `pid<=2` rejection message slightly misleading.
- **Test gaps** — **no `*_test.go` file**. PID parse, signal allowlist, self-kill guard, cache TTL all uncovered.

**Note from agent:** Module spec mentioned nice/ionice + Commander injection + audit calls — none exist in current code; router only registers TopProcesses/ListProcesses/KillProcess.

---

### terminal

**What it does:** Bridges a `creack/pty` shell to a WebSocket; supports reconnect (scrollback replay), resize, multi-reader fan-out, idle-timeout cleanup.

**Findings:**

- **P0-17** — slow-client HOL blocking (see top).
- **P0-18** — `started` flag racy (see top).
- **P1 — Idle-only timeout misclassifies active output** (`handler.go:331-340`). `lastUse` bumped only on WS input; user watching `top`/`tail -f` gets killed mid-command. Bump from `broadcast` too OR separate "connected" vs "input idle" timeouts.
- **P1 — No total-session timeout** (only idle).
- **P1 — Stale session detection weak** (`handler.go:232`). Only `cmd.ProcessState != nil`; ProcessState set by Wait which reader calls only on Read error. If child died but Read still pending in kernel queue, returns "exists" and serves dead session.
- **P1 — No audit row for session start/stop** with node_id. Audit middleware logs the WS request but no explicit session_started/session_ended event.
- **P2 — Server shutdown leaks child shells** (`handler.go:276, 349`). Cleanup goroutine respects ctx.Done but doesn't drain active sessions. Shells orphan to PID 1 until parent exits.
- **P2 — Create branch doesn't replay scrollback** (intentional; just noting).
- **P2 — `pty.Setsize` errors swallowed** (`:322`).
- **P2 — Raw error string back to client** (`:289`). "Failed to start shell: …" exposes filesystem/PAM details.
- **P2 — Root-shell privilege not documented** in handler comment.
- **P3** ×4 — ringBuffer.Write byte-loop (replace with 2× copy), shared `buf` aliasing in broadcast (safe but undocumented), `Upgrader` mutable + package-level, `writeMu` naming.
- **Optimization** — single-flight for `startReader` (`sync.Once`), per-reader bounded channel for broadcast (fixes P0-17).
- **Test gaps (7)** — no PTY lifecycle, no idle-timeout, no max-sessions cap, no reconnect/replay, no race-targeted, no never-expire timeout, no resize-message Setsize-invoked.

---

### appstore

**What it does:** Lists curated compose templates from GitHub raw, installs as compose stacks via SSE-streamed `docker compose pull/up -d`. "Advanced" mode lets operators submit arbitrary YAML.

**Findings:**

- **P0-19** — Advanced YAML carry-forward partial OPEN (see top).
- **P0-20** — Advanced mode no privileged re-auth (see top).
- **P1 — Subprocess detached from request context** (`handler.go:656`). 10-min timeout on `context.Background()`. Client cancel during `docker compose pull` of 5 GB image doesn't observe. SSE writes silently fail (`:324-330` ignores error).
- **P1 — SSE subprocess output not sanitized** (`handler.go:707`). Raw `scanner.Text()` → `sendSSE`. Docker stderr containing registry auth errors, ANSI, stack traces reach operator browser raw.
- **P1 — No template signature verification** (`handler.go:193-209, 519`). Repo compromise of `svrforum/SFPanel-appstore` = one-step RCE on every panel browsing the store. Pin commit SHA or sign the index.
- **P1 — `GetApp` goroutine ghost work after caller disconnect.** `httpGet` doesn't accept context; goroutines run for full HTTP timeout (~30 s) regardless of caller cancellation.
- **P1 — No body size limit on `InstallApp`** (`handler.go:486-494`). Echo `c.Bind` reads full body; `Compose` field arbitrary string. 1 GB JSON body OOMs panel.
- **P1 — N+1 install probe in ListApps** (`handler.go:359-368`). `isInstalled` per app = N SQLite reads + N stat calls.
- **P1 — `findFreePort` fork bomb** (`handler.go:763-774`). Up to 100 `ss` invocations per GetApp call. Replace with single `ss -tlnH` parsed once.
- **P2** ×5 — `refreshCache` holds `mu.Lock` while launching `go h.persistCache()`, regex-based container_name conflict check, lazy isInstalled cleanup race (benign), error messages leak internal text, `appStoreBaseURL` hardcoded (no air-gapped override).
- **P3** ×3 — `generatePassword` discards `rand.Read` error, `sseEvent.Success` semantics inverted, README probe blocks 90 s on 3-branch serial probe.
- **Test gaps** — **no `*_test.go` files in module** (zero tests).

**Critical relationship to T2 compose finding:** `appstore/compose_safety.go` is a 10-line file containing only `func validateAdvancedCompose(content string) error { return composex.ValidateAdvancedCompose(content) }`. The compose module and appstore use the **SAME** validator. Every gap in `composex/safety.go` applies identically in both surfaces. **Fix the shared validator once; both surfaces close together.**

---

### websocket

**What it does:** Generic WS transport helpers (auth ticket/JWT, upgrade, safe writer, keepalive) + 4 concrete handlers: MetricsWS, ContainerLogsWS, ComposeLogsWS, ContainerExecWS.

**Findings:**

- **P0-21** — MetricsWS missing keepalive (see top).
- **P0-22** — Open CheckOrigin + legacy `?token=` path (see top).
- **P1 — Raw docker error strings leaked to client** (`handler.go:182, 278, 326`). Violates CLAUDE.md §2.
- **P1 — `ContainerLogsWS` accepts unvalidated `tail`** (`handler.go:159-169`). `tail=all` against multi-GB log streams everything. Cap to 10k, mirror `ComposeLogsWS` integer parse.
- **P1 — `ContainerExecWS` write goroutine not joined** (`handler.go:359-389`). Works in practice but no `WaitGroup`; future regressions can leak.
- **P2** ×3 — `bufio.Scanner` default 64 KB token cap (single multiplexed docker log frame >64 KB silently kills stream), bare `Upgrader.Upgrade` error returns 500 with raw text, `safeWSWriter` redundant SetWriteDeadline.
- **P3** ×3 — subprotocol allowlist not declared, hand-rolled `parseInt`, MetricsWS 2 s polling fixed.
- **Test gaps** — **no `*_test.go` files in module**. No keepalive test, no `?token=` vs ticket precedence, no origin allow-list (once added), no `tail`/`since` validation.

---

## Final pattern list (T1–T5)

Adding T5 patterns:

### Pattern T — Streaming SSE/WS handlers without context propagation

Affects: appstore (install), terminal (PTY reader detached), websocket (ContainerExecWS goroutines), monitor (StartUpdateChecker), logs (tail/grep).
**Single fix:** extends Pattern F — handlers passing `c.Request().Context()` into the streaming subprocess/SSE/WS path so client cancel propagates.

### Pattern U — Slow-client head-of-line blocking in fan-out

Affects: terminal (broadcast to N readers), cluster ws_relay (T1), possibly logs.
**Single fix:** per-reader bounded send channel + non-blocking enqueue + drop-on-overflow policy.

### Pattern V — High-risk handlers without audit row

Affects: process (KillProcess), terminal (session start/stop), settings (UpdateSettings — T3), websocket (session open).
**Single fix:** audit pattern adoption — for every state-changing handler, ensure middleware OR explicit audit row stamps node_id + actor + target.

### Pattern W — Shared sensitive validator is duplicated/delegated and gaps repeat

Affects: composex/safety.go used by both compose AND appstore via 1-line alias.
**Single fix:** harden the shared validator. Single PR closes both attack surfaces.

---

## Exit criteria for T5

- [x] All 4 modules reviewed
- [x] 7 P0 findings flagged
- [x] Cross-tier patterns extended T-W
- [x] 2026-04-19 P0 carry-forward (advanced YAML) **verified PARTIAL/OPEN** — needs P0-19 fix
- [ ] R-final synthesis
