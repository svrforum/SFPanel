# Module Hardening Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement
> this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each fix that
> touches security-sensitive parsing/validation, an HTTP/response contract, cluster logic, or
> a complex text parser MUST get a test per CLAUDE.md's testing table (TDD: reproduce → fix → pass).

**Goal:** Remediate the 49 stability/optimization/security findings from the 2026-06-02 module
review (`docs/superpowers/research/2026-06-02-module-review/findings.md`), sequentially, by severity.

**Architecture:** Work on branch `fix/module-hardening-2026-06`. One commit per logical fix (or
per tight cluster of identical-pattern fixes). Commit author/committer = `svrforum`, no AI refs.
Run `make test` after each phase; run the targeted package test after each fix.

**Tech Stack:** Go 1.25 (echo v4, database/sql + SQLite WAL, hashicorp/raft, gorilla/websocket,
creack/pty), `exec.Commander`/`MockCommander`, `response.OK/Fail/SanitizeOutput`.

**Conventions reminder:** `response.Fail(ctx, code, msg)` with codes from
`internal/api/response/errors.go`; `log/slog` only; `exec.Commander` unless a documented
streaming/PTY exception; sysguard deny-lists for any kill/stop path.

---

## Phase 0: Baseline

- [ ] **Step 1:** Confirm clean working tree on branch `fix/module-hardening-2026-06`.
- [ ] **Step 2:** Run `make test` and record it green BEFORE any change. If red, stop and report
  the pre-existing failures — do not start fixes on a red baseline.

---

## Phase 1: Critical (C1–C4)

Each item: read the actual code first (line numbers below are from review time and may drift),
write a failing test where the table requires, apply the minimal fix, verify, commit.

### Task C1: logs path validation — drop `..` substring + boundary-less prefix
**Files:** Modify `internal/feature/logs/handler.go` (~`validateCustomSourcePath`, line ~45);
Test: `internal/feature/logs/handler_test.go` (create if absent).
- [ ] Write failing test: `/var/log-evil/x.log` and `/opt-backup/x` must be REJECTED; `/var/log/app..log`
  must be ACCEPTED; `/var/log/syslog` accepted; `/etc/passwd` rejected.
- [ ] Replace substring `..` check with `filepath.Clean` + segment-boundary match
  (`clean == prefix || strings.HasPrefix(clean, prefix+"/")`, prefixes stored without trailing slash).
- [ ] `go test ./internal/feature/logs/...` green. Commit: `logs: tighten custom source path validation to segment boundaries`.

### Task C2/C3: firewall lockout guards — account for `From` scope
**Files:** Modify `internal/feature/firewall/firewall_ufw.go` (`hasAccessRule`/`ruleAllowsPort`, ~88-94)
and `internal/feature/firewall/firewall_lockout.go` (`wouldLockOutOnAdd`, ~77);
Test: `internal/feature/firewall/firewall_lockout_test.go` (extend existing).
- [ ] Write failing tests reproducing both lockout scenarios:
  (a) `EnableUFW` with only `allow from 10.0.0.0/8 to any port 22` must NOT be judged "safe" (must
      warn / require force) when the admin source is outside 10/8;
  (b) `AddRule` of a source-scoped `deny from X to any port 22` above the panel allow must be caught.
- [ ] Treat a rule whose `From` is non-`Anywhere` as NOT a general access rule in `hasAccessRule`;
  extend `wouldLockOutOnAdd` to consider `From`-shadowing deny/reject.
- [ ] Tests green. Commit: `firewall: make lockout guards aware of source (From) scope`.

### Task C4: alert dispatch — async, non-blocking on the docker event loop
**Files:** Modify `internal/feature/alert/manager.go` (`Fire`, ~176); verify call sites
`internal/feature/alert/container_rules.go:191`, `internal/monitor/docker_events.go:156,170`;
Test: `internal/feature/alert/manager_test.go`.
- [ ] Write failing test: a channel whose `Send` blocks for N seconds must NOT block a second
  `Fire`/the caller (assert the event-loop call returns promptly; use a fake slow channel).
- [ ] Introduce a bounded worker queue (buffered chan + small fixed worker pool started once) that
  `Fire` enqueues onto; drop-with-`slog.Warn` when the queue is full. Keep cooldown reservation
  (see H13) atomic so async dispatch can't double-fire.
- [ ] Tests green; `make test`. Commit: `alert: dispatch notifications on a bounded async worker queue`.

---

## Phase 2: High (H1–H17)

Grouped so identical patterns commit together.

### Task H1: auth refresh-token rotation — serialize, no deadlock
**Files:** `internal/feature/auth/refresh.go` (~110-227); Test: `internal/feature/auth/refresh_test.go`.
- [ ] Failing test: two concurrent `Refresh` with the same valid token → exactly one succeeds,
  the other gets a clean "already used"/invalid result (NOT a 500/SQLITE_BUSY).
- [ ] Replace read-then-write with a guarded single statement
  `UPDATE refresh_tokens SET consumed_at=? WHERE token_hash=? AND consumed_at IS NULL`, treat
  `RowsAffected()==0` as already-consumed (trigger family revocation as today).
- [ ] Tests green. Commit: `auth: make refresh-token rotation atomic to avoid concurrent-refresh deadlock`.

### Task H4 + H15-scan + M15 + M16 + L1: unchecked DB row errors (one sweep)
**Files:** `internal/feature/docker/observability.go:153`, `internal/feature/alert/container_rules.go:217-219`,
`internal/feature/alert/handler.go:491`, `internal/feature/audit/handler.go:81-89`,
`internal/feature/settings/handler.go:87-93`.
- [ ] For each: check the `rows.Scan` return and add `rows.Err()` after the loop (or capture
  `QueryRow().Scan` error for the COUNT). Match the sibling `GetEvents` pattern.
- [ ] `make test`. Commit: `db: check rows.Scan/rows.Err across docker/alert/audit/settings reads`.

### Task H5 + H6: caches holding write-lock across slow exec/syscall
**Files:** `internal/feature/process/handler.go:58-77` (200ms /proc scan under lock);
`internal/feature/services/handler.go:279-298` (systemctl×2 under lock) + move `serviceCache`
onto the `Handler` (mirror process module's per-Handler cache).
- [ ] Refactor both: gather into a local outside the lock, take the write lock only to swap
  `data`/`updatedAt`. Move `serviceCache` to `Handler`.
- [ ] `make test`. Commit: `process,services: collect cache data without holding the write lock`.

### Task H7: disk findMountPoint empty-string false match
**Files:** `internal/feature/disk/disk_filesystems.go:847-854`; Test: extend disk tests.
- [ ] Failing test: a nonexistent device must NOT resolve to `/proc`/`/sys` mountpoint.
- [ ] Skip the resolved-compare branch when `resolvedDev == ""` or `resolvedMountDev == ""`.
- [ ] Tests green. Commit: `disk: don't match empty resolved device path in findMountPoint`.

### Task H8: smartctl bounded timeout
**Files:** `internal/feature/disk/disk_blocks.go:240-269`.
- [ ] Wrap the `smartctl -j -a` call in `context.WithTimeout(reqCtx, 45*time.Second)`.
- [ ] `make test`. Commit: `disk: bound smartctl with a 45s timeout to survive failing disks`.

### Task H2: appstore streamCommand — cancel on client disconnect
**Files:** `internal/feature/appstore/handler.go:814-840`.
- [ ] In the scan loop, detect SSE write failure / poll `c.Request().Context().Done()` and cancel
  `installCtx` so the install aborts when the operator navigates away. Keep the detached-context
  intent documented.
- [ ] `make test`. Commit: `appstore: abort detached install when the SSE client disconnects`.

### Task H3: docker PullImage — unblock Decode on cancel
**Files:** `internal/feature/docker/handler.go:355-371`.
- [ ] Run `decoder.Decode` via a goroutine feeding a channel; `select` on `{event, ctx.Done()}`.
  Confirm the SDK reader closes on ctx cancel.
- [ ] `make test`. Commit: `docker: make PullImage SSE responsive to client cancellation`.

### Task H9: terminal WS read deadline + keepalive
**Files:** `internal/feature/terminal/handler.go:499-529` (reuse `websocket.startWSKeepalive` pattern).
- [ ] Arm `SetReadDeadline` + `SetPongHandler` re-arm + ping ticker on the direct terminal WS.
- [ ] `make test`. Commit: `terminal: add read deadline and keepalive to detect dead WS clients`.

### Task H10: cluster gRPC proxy — guard oversized response
**Files:** `internal/cluster/grpc_server.go:295-368`; Test: `internal/cluster/...` if feasible.
- [ ] When `len(respBody)` approaches the gRPC recv cap, return a clear error code instead of
  letting gRPC truncate; add a one-line doc note that non-allowlisted proxied routes are capped at
  the 30s `localHTTPClient` timeout.
- [ ] `make test`. Commit: `cluster: return a clear error when a proxied response exceeds the gRPC cap`.

### Task H11 + H12 + H15 + H17: firewall/portmap iptables parsing & safety
**Files:** `internal/feature/firewall/firewall_docker.go` (184-209 proto, 248/358/384 batching,
412-427 delete), `internal/feature/portmap/handler.go:118`; Test: `firewall_docker_test.go`,
`portmap` parser test.
- [ ] H12: derive protocol from the regex capture group, normalize, compare to `req.Protocol`.
- [ ] H11: serialize DOCKER-USER mutations behind a per-handler mutex (or match by `iptables -S` spec).
- [ ] H17: split portmap comment on `" # "` not `LastIndex('#')` (mirror firewall_ufw.go).
- [ ] H15: batch `docker inspect` over all IDs in one call; parse the NAT DOCKER chain once per request.
- [ ] Tests green; `make test`. Commit: `firewall,portmap: fix proto matching, serialize rule deletes, batch inspects`.

### Task H13 + H14: alert cooldown race + cursor-held dispatch
**Files:** `internal/feature/alert/manager.go:161,178-234` (builds on C4's queue).
- [ ] H13: make cooldown check-and-reserve atomic under a single `Lock` (tentatively set `lastSent`
  before enqueueing the send).
- [ ] H14: in `evaluate`, collect `AlertFire` payloads, close `rows`, then enqueue dispatch.
- [ ] `make test`. Commit: `alert: reserve cooldown atomically and dispatch after closing the cursor`.

### Task H16: image-update checks — parallel + per-image timeout
**Files:** `internal/feature/docker/handler.go:390-417`, `internal/docker/compose.go:761-796`.
- [ ] Replace serial loop with a bounded worker pool (mirror `ContainerStatsBatch`'s `sem`); give
  each `CheckImageUpdate` its own short `context.WithTimeout`.
- [ ] `make test`. Commit: `docker: parallelize image-update checks with per-image timeouts`.

---

## Phase 3: Medium (M1–M19)

One commit per related cluster; tests where the table requires (M2/M3 audit, M10 cron, M17/M19 cluster).

- [ ] **M1** `compose/handler.go:348-409` — wrap SSE error payload in `response.SanitizeOutput`.
- [ ] **M2** `audit/handler.go:116-176` — wrap count+tombstone+delete in one `tx`. (test)
- [ ] **M3** `auth/handler.go:157-159` — record `unknown_user` audit event on `sql.ErrNoRows`. (test)
- [ ] **M4** `system/handler.go:988` — route restore restart through `h.Cmd.Run` + `exitProcess()` on error.
- [ ] **M5** `internal/monitor/history.go:123-126` — use `safe.Go` (or fold into the retention tick).
- [ ] **M6** `disk/disk_filesystems.go:217-220` — special-case `/dev/mapper/` before `validateDeviceName`. (test)
- [ ] **M7** `disk/disk_filesystems.go:391-417` — anchor nvme/loop split with regex `(nvme\d+n\d+|loop\d+)p(\d+)$`. (test)
- [ ] **M8** `logs/handler.go:373-379` — upgrade WS first, then send unavailable as WS frame.
- [ ] **M9** `files/handler.go:417-422` — stat existing file, stream/skip `.bak` over a size cap.
- [ ] **M10** `cron/handler.go:198,235` — address jobs by stable id/`expected_raw` verify, not line index. (test)
- [ ] **M11** `network/tailscale.go:491`,`wireguard.go:240,418,458,484` — `SanitizeOutput` on returned errors.
- [ ] **M12** `firewall/firewall_fail2ban.go:773` — atomic temp+rename for jail configs.
- [ ] **M13** `firewall/firewall_fail2ban.go:616,671` — constrain `validLogPath` to an allowlist/canonical `/var/log`.
- [ ] **M14** `network/tailscale.go:413` — restrict auth-URL extraction to the control-server host.
- [ ] **M17** `internal/cluster/ws_relay.go:114-163` — close the opposite conn on one-sided failure. (test if feasible)
- [ ] **M18** `internal/cluster/grpc_client.go:131-154` — expire pooled conns on idle, not creation age.
- [ ] **M19** `internal/feature/cluster/handler.go:1099-1164` — re-fetch node from live FSM in `updateNode`.
- [ ] `make test` after the phase. Commit per cluster as above.

---

## Phase 4: Low (L1–L9)

L1 folded into Phase 2 sweep. Remaining:
- [ ] **L2** `auth/handler.go:312-326` — bound the audit fallback (small worker / drop-with-log).
- [ ] **L3** `logs/handler.go:167-188,306-323` — derive line count from already-read data.
- [ ] **L4** `disk/disk_blocks.go:32-82` — move `diskCache` onto the `Handler`.
- [ ] **L5** `portmap/ss_parser.go:31` — reuse the colon-presence field fallback from firewall_ufw.go.
- [ ] **L6** `firewall/firewall_fail2ban.go:643-646` — copy loop var / index `&jailTemplates[i]`.
- [ ] **L7** `internal/cluster/manager.go:1042-1145`,`heartbeat.go:94-119` — wrap in `safe.Go`.
- [ ] **L8** `internal/cluster/manager.go:265-267` — select on shutdown alongside the timer.
- [ ] **L9** `internal/cluster/grpc_client.go:45-60` — tag the insecure client join-only.
- [ ] `make test`. Commit: `chore: low-severity hardening (safe.Go, per-handler caches, parser fallbacks)`.

---

## Phase 5: Final verification
- [ ] `make ci` (lint + test + build) green.
- [ ] Re-read each Critical/High diff: every changed line traces to a finding (surgical-changes rule).
- [ ] Summarize what changed, what was deferred, and any finding downgraded after closer reading.

## Self-review notes
- Findings that may be partly false positive — verify by reproduction before asserting a fix:
  C2/C3 (lockout reasoning is abstract), H10 (documented sharp edge, may be doc-only), M19 (operator-driven window).
- If reproduction shows a finding is not real, mark it in the findings doc as "not reproduced" and skip — do not invent a fix.
