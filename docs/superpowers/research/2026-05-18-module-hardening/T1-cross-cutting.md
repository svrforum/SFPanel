# T1 — Cross-cutting infrastructure

**Reviewed at:** v0.13.15 commit `f5d38a4` · 2026-05-18
**Scope:** `auth` · `cluster` · `audit` · `middleware` (audit/proxy/csrf/auth/request_logger/security_headers) · `monitor` · `common/exec` + `common/lifecycle` · `db`
**Method:** 5 parallel read-only subagent reviews (Opus 4.7), 25-item checklist each
**Companion umbrella:** `docs/superpowers/plans/2026-05-18-module-hardening-program.md`

---

## Findings count

| Module | P0 | P1 | P2 | P3 | Total |
|---|---|---|---|---|---|
| auth | 0 | 3 | 2 | 5 | 10 |
| cluster | **2** | 5 | 5 | 7 | 19 |
| audit | 0 | 3 | 3 | 4 | 10 |
| middleware-misc | 0 | 0 | 4 | 4 | 8 |
| monitor | 0 | 2 | 3 | 4 | 9 |
| common/exec | **1** | 2 | 3 | 4 | 10 |
| db | 0 | 2 | 5 | 4 | 11 |
| **Total** | **3** | **17** | **25** | **32** | **77** |

**Verified from 2026-04-19 review:**
- ✅ `proxy.go:34` `InsecureSkipVerify:true` — **CLOSED** (mTLS now enforced via `ClientTLSConfig()`)
- ✅ `ws_relay.go:53` `InsecureSkipVerify:true` — **CLOSED**
- ⚠️ `tls.go:254` `VerifyClientCertIfGiven` — **STILL OPEN** but partially mitigated (unary interceptor); **streaming RPCs bypass cert check → new P0** (see below)

---

## P0 — must fix before next tier (umbrella exception clause)

### P0-1. gRPC streaming RPCs accept cert-less callers
**Files:** `internal/cluster/tls.go:254`, `internal/cluster/grpc_server.go:79-82`, `internal/cluster/grpc_server.go:193-259`

`tls.VerifyClientCertIfGiven` (not `RequireAndVerifyClientCert`) accepts handshakes without a client cert. The `requireClientCertInterceptor` (`grpc_server.go:55-68`) plugs the gap for **unary** RPCs only — `grpc.UnaryInterceptor` does not cover streaming. The `Heartbeat` streaming RPC (`grpc_server.go:193-259`) therefore accepts cert-less callers; an attacker who can reach the gRPC port (default 3629) can:
- Open a `Heartbeat` stream supplying any `NodeId` already present in the FSM,
- Trigger `PromoteOnHeartbeatIfPending` (`grpc_server.go:245`), or simply keep a flapping peer's "online" status sticky from the attacker side, masking a real outage in operator UI.

**Fix sketch:** add a `grpc.StreamInterceptor(requireClientCertStreamInterceptor)` mirroring the unary one, OR switch ClientAuth to `RequireAndVerifyClientCert` and route PreFlight/Join through a separate listener (since those legitimately have no client cert pre-join).

### P0-2. Heartbeat receive goroutine leak
**File:** `internal/cluster/grpc_server.go:202-211`

The recv goroutine writes into `recvCh` (buffer 1) in `for { stream.Recv(); recvCh <- result{} }`. The parent select can exit via `<-stream.Context().Done()` or idle timeout while the channel send is blocked because the buffer is full and there is no reader. Goroutine leaks once per stream teardown. On a flapping cluster restarting heartbeats many times this accumulates.

**Fix sketch:** make the inner loop also `select` on `stream.Context().Done()` before the channel send.

### P0-3. `RunWithTimeout(0, …)` deadlines immediately
**File:** `internal/common/exec/exec.go:36-38`

Passing `time.Duration(0)` (zero-value, easy default) results in `context.WithTimeout(ctx, 0)` which deadlines synchronously — the subprocess never starts, caller sees `context.DeadlineExceeded`. Silent footgun any time a config field carrying a timeout defaults to zero.

**Fix sketch:** at start of `RunWithTimeout`, `if timeout <= 0 { timeout = DefaultTimeout }`.

**P0 mitigation status today:** none of the three are actively bleeding in current production deployments (P0-1 requires reachable gRPC port — typically firewalled; P0-2 is a slow drip; P0-3 fires only if a caller passes 0). They are P0 by *severity*, not *active incident*. Safe to defer to a single P0-fix commit after the full T1–T5 picture, OR fix immediately. **Recommendation: fix immediately as one small commit before Phase 2.**

---

## Per-module sections

> Full checklists and the long-form findings live in each subagent's return — captured verbatim below.

### auth (+ auth/csrf middleware)

**What it does:** Local + cluster-FSM-replicated admin auth — bcrypt login with per-IP rate limit, TOTP 2FA enable/verify/disable, password change, sha256-hashed refresh-token family rotation with OWASP reuse detection, single-use 60s WS tickets, httpOnly refresh + double-submit CSRF cookies, security-event audit rows stamped with origin node.

**Findings:**

- **P1 — `handler.go:28` `loginAttempts` map has no time-based eviction.** Entries removed only on successful login / disable-2FA / change-password (`:177,464,526`). An IP-rotating attacker grows the map unbounded for the process lifetime. Fix: 5-min retention goroutine; delete entries where `firstAt < now - (rateLimitWindow + rateLimitBlockDuration)`.
- **P1 — `refresh.go:118-122,131,144-145` `_ = tx.Commit()` silently drops commit errors** on the family-revoke, expired-token-delete, and orphan-user-delete paths. On family-revoke the deletion may fail to persist while caller sees "Session revoked" — observability bug + potential security hole on the steal-detection path.
- **P1 — `handler.go:343-345,405-407,474-476` follower auto-forward inside `Verify2FA`/`Disable2FA`/`ChangePassword`** runs before `c.RealIP()` is read for rate-limit on follower side. Distributed brute-force across followers can multiply the leader's rate limit budget. Either tick `preRecordLoginAttempt` on follower pre-forward, or document that the leader's limiter (using X-Forwarded-For) is authoritative.
- **P2 — `refresh.go:269` pruner WHERE on `expires_at`/`consumed_at` lacks supporting index.** Full table scan every hour. Add `CREATE INDEX idx_refresh_tokens_expires ON refresh_tokens(expires_at)`.
- **P2 — Unbounded per-request goroutines vs `MaxOpenConns=1`.** Both audit middleware (`audit.go:60-67`) and auth security_events spawn fire-and-forget INSERT goroutines that contend on a single connection. Replace with buffered channel + single writer goroutine.
- **P3** ×5 — `new(int)` allocation in Login scan, SetupAdmin global mutex on GC iteration, refresh cookie max-age hard-coded vs `refreshTokenLifetime`, SetupAdmin GC-before-validate ordering, redundant index citation.
- **Test gaps (9)** — no Login test for any branch, no `preRecordLoginAttempt` concurrency test, no `Refresh` OWASP-reuse path test (the highest-value security branch — uncovered), no `family_id==""` backfill test, no Logout revoke test, no MintWSTicket wrapper test, no SetupAdmin test, no JWTMiddleware query-token allowlist test, no CSRF v2-path bypass test.

---

### cluster

**What it does:** Multi-node clustering with hashicorp/raft (mTLS gRPC + boltdb), FSM replicating accounts/config/nodes, request proxying via gRPC unary / HTTP relay / WS relay, heartbeat-driven health.

**Findings:**

- **P0-1 — gRPC stream auth bypass** (see top). `tls.go:254` + `grpc_server.go:79-82`.
- **P0-2 — Heartbeat recv goroutine leak** (see top). `grpc_server.go:202-211`.
- **P1 — `raft_fsm.go:98-127` `applyUpdateNode` nil-vs-empty Labels ambiguity.** Sending `{"labels": null}` silently does nothing (nil map). Treat nil as clear OR reject.
- **P1 — `manager.go:1066` heartbeat stream uses `context.Background()`.** Manager shutdown can't cancel an in-flight `stream.Send` on a slow peer. Use a context tied to manager lifetime + `client.Close()` first in `closeStream`.
- **P1 — `raft_fsm.go:69-73` `SetOnDisband` set AFTER Raft replay.** A node that crashed mid-disband won't re-disband on reboot because the replayed CmdDisband finds nil callback. Plumb callback registration into `NewRaftNode` wiring.
- **P1 — `proxy.go:421` node lookup falls back to `Name` match,** allowing duplicate hostnames to land on either of two `db-01` nodes nondeterministically. Match by ID only; reject duplicate Names at HandleJoin.
- **P1 — `manager.go:618-633` Leave silently drops Leave notification** if gRPC conn unavailable. Leader FSM never learns this node left → appears offline forever. Add HTTP relay fallback.
- **P1 — `handler.go:1184-1195` ClusterUpdate ignores TransferLeadership error** (`_ = ...`). Operator sees "complete" while transfer actually failed. Surface error in SSE.
- **P2 — `manager.go:808-818` ProxySecret re-reads CA cert on every call** (`proxy.go:128,279`; `handler.go:831,1206`; `ws_relay.go:204`). Hot-path disk I/O. Cache on first compute (CA is non-rotatable without coordinated restart anyway).
- **P2 — `grpc_server.go:27` `localHTTPClient.Timeout=30s` defeats** the 5min cluster-relay timeout for `/docker/compose` requests. Drop the client-level Timeout, inherit from request ctx.
- **P2 — `handler.go:1199-1239` leader self-update goroutine is untracked** and races `mgr.Shutdown()`. Only a 1s `time.Sleep` orders them. Process kill cleans up but logs are chaotic.
- **P2 — `manager.go:599-635` Leave can stall for tens of seconds** if pool TLS handshake to leader is slow. Bound the entire Leave flow with one ctx.
- **P2 — `handler.go:728-744` no max-key-count early-exit** for labels (regex runs per key); bounded at 32 already.
- **P3** ×7 — duplicate "merge live health" loops between GetNodes/GetOverview, GetState's `*v` dereference panics on nil corruption, nondeterministic peers order, hardcoded `:3628` API port in ws_relay, isServerRunning TCP-only liveness detection, VerifyLeader future goroutine leak under partition, `raft.go:182-196`.
- **Test gaps (10)** — no test for gRPC stream auth (the P0), no ProxyToLeader stale-retry test, no Heartbeat teardown test, no ws_relay tests at all, no `applyUpdateNode` nil-Labels behavior test, no malformed peers.json test, no CmdDisband-during-replay test, no ClusterUpdate quorum-guard test, no leader-flap-mid-call test, no TokenTTL clamp test.

---

### audit

**What it does:** Records every authenticated state-changing request to `audit_logs` with `node_id` stamp; paginated list; scoped/full clears always leave `protected=1` tombstone; background pruner caps at 50k rows.

**Findings:**

- **P1 — `middleware/audit.go:60-67` Unbounded per-request goroutine fan-out.** Every state-changing request spawns a goroutine that holds a DB write. With `MaxOpenConns=1`, goroutines serialize on the connection pool; under burst (cluster proxy fanout, bulk admin script) they accumulate. SIGTERM mid-burst loses in-flight writes. Replace with buffered channel + single consumer.
- **P1 — `middleware/audit.go:61` `db.Exec` not `ExecContext`** — bgCtx can't cancel writes at shutdown.
- **P1 — `middleware/audit.go:99-117` `pruneAuditLogs` same issue.** Non-context Exec; stuck WAL holds the prune.
- **P2 — `migrations.go:71` only `created_at` indexed.** Future filters (the v0.13.x trend) by `node_id`/`username`/`protected` will scan. Add `idx_audit_logs_node_id`, partial `idx_audit_logs_protected_created (created_at) WHERE protected = 0` for the prune query.
- **P2 — `handler.go:55-98` `ListAuditLogs` exposes no filters at all.** Users page through everything. Either add filters (with the indexes) or document the gap.
- **P2 — `handler.go:67` cluster-wide COUNT shows per-node count.** No aggregated view — document or aggregate.
- **P3** ×4 — strings.HasPrefix chain on hot path, encoding wipe scope into `path` column via `#` fragment is brittle, silent row.Scan failures swallow diagnostics, protected INT-to-bool dance is unnecessary.
- **Test gaps (6)** — no `AuditMiddleware` unit test (skip list, node_id stamping, status capture), no `pruneAuditLogs` test, no `StartAuditRetention` ctx-cancel test, no boundary parsing test (negative page, limit>100), no concurrency-burst test, no `LocalNodeIDFn`-nil vs `LocalNodeIDFn`-empty test.

---

### middleware-misc (request_logger + security_headers)

**Findings:**

- **P2 — `request_logger.go:13` only `/api/v1/health` exempt.** Heartbeats, `/system/overview` polling, `/system/metrics-history` (5s poll), `/cluster/status` — all dominate `sfpanel.log` in a quiet cluster. Extend skip list or gate by debug level.
- **P2 — `request_logger.go:18-24` no request_id / correlation field.** Cannot correlate log line to audit row or downstream handler error. Add `request_id` from echo.
- **P2 — `security_headers.go:42` CSP `'unsafe-inline'` on `style-src`** (for Pretendard). Replace with nonce/hash to drop unsafe-inline.
- **P2 — `security_headers.go:32` `img-src … https:`** allows any HTTPS origin. Tighten to `'self' data: blob:`.
- **P3** ×4 — `c.Path()` route template vs concrete URL not documented, err captured but not in log fields, header `if h.Get(...) == ""` guard inconsistent across headers, `connect-src https:` too broad.
- **Test gaps (3)** — no RequestLogger skip-list test, no SecurityHeaders CSP/header-presence test, no HSTS-absent regression test.

---

### monitor

**What it does:** 60s host metrics collector, hourly metrics retention pruner, docker events listener, 5min volume usage collector, hourly GitHub release update checker.

**Findings:**

- **P1 — Every collector goroutine lacks top-level `recover()`** (`history.go:44`, `docker_history.go:21`, `docker_retention.go:15` & `:33`, `docker_volume_usage.go:34`, `update.go:18`). One panic kills history collection forever (no respawn). One-line per goroutine entry: `defer func(){ if r := recover(); r != nil { slog.Error("collector panicked", "err", r) } }()`.
- **P1 — No downsampling/rollup for `container_metrics_history`.** At 30s × 72h × N containers, table grows linearly with N. 100 containers × 72h ≈ 864k rows; missing `(ts)` index makes time-range reads full-scan. Add `idx_cmh_ts`; consider 5-min rollup table for ranges > 6h.
- **P2 — `update.go:18-25` `StartUpdateChecker` ignores ctx** AND runs on every cluster node independently — three GitHub fetches/hour across a 3-node cluster; banners can disagree during rolling upgrade. Run on leader only or share through FSM.
- **P2 — `docker_events.go:154` synchronous SQLite write inside select loop.** Burst during retention sweep can stall the Docker SDK's event channel and drop messages. Buffered channel + writer goroutine.
- **P2 — `docker_volume_usage.go:71` path-traversal vector.** `"/var/lib/docker/volumes/" + v.Name + "/_data"` — Docker daemon validates names so practical risk low, but defense in depth: `filepath.Clean` + prefix check.
- **P3** ×4 — `loadHistoryFromDB` no LIMIT, `pruneHistory` only DB-side (in-memory implicit), `collectPoint` re-slice vs ring, redundant post-load DB cleanup at `history.go:120-123`.
- **Optimization** — replace per-tick `du -sb` (potentially 25 min worst case at 50 volumes) with single `docker system df -v --format json`.
- **Test gaps (7)** — no ctx-cancel-exits test for any collector, no `StartUpdateChecker` lifecycle test, no `pruneHistory` test, no clock-skew slice test, no events-backpressure test, no panic-in-collector recovery test, no large-dataset retention test.

---

### common/exec (+ common/lifecycle)

**Findings:**

- **P0-3 — `RunWithTimeout(0, …)` deadlines immediately** (see top). `exec.go:36-38`.
- **P1 — `Commander` interface has no context parameter.** Default 5-min timeout decoupled from request lifetime; SSE/long-poll client disconnect leaves subprocess running for up to 5 min. Add `RunCtx(ctx, …)`.
- **P1 — `MaxOpenConns=1` on `*sql.DB`** defeats WAL's concurrent-read benefit. Single writer is correct; single reader is not. Switch to reader pool + writer pool split.
- **P2 — `temp_store=MEMORY` pragma not set.** Temp B-trees for sorts spill to disk. One-line DSN addition.
- **P2 — Missing indexes** `audit_logs(node_id)`, partial `audit_logs(protected)`.
- **P2 — `RunWithTimeout` error wrapping drops underlying `err` on deadline.** Only timeout duration surfaces — exit signal info lost.
- **P3** ×4 — MockCommander.Calls exposed without lock (convention only), unconditional SIGKILL no SIGTERM-grace, RunWithEnv leaks parent env, `MigrateRestartPolicy` non-atomic write.
- **Test gaps** — no `RunWithTimeout(0)` test (would catch P0), no actual-SIGKILL-after-timeout test, no stderr-on-non-zero test, no parent-ctx-cancel test (blocked by P1), no MockCommander race test, no FK/table-missing migration negative case, no `CheckpointWAL` test.

---

### db

**Findings:**

- **P1 — `MaxOpenConns=1`** (same as common/exec finding, from the DB angle).
- **P1 — Migrations use bare `Exec`/`Query`** (not Context). Low impact today (Open called once from main) but easy fix.
- **P2** ×5 — `temp_store=MEMORY` missing, audit_logs missing `node_id`+`protected` indexes, `container_metrics_history` cross-container range queries scan, `Open()` doesn't `MkdirAll` parent dir, no integrity_check before snapshot.
- **P3** ×4 — `backfillAppliedFromSchema` runs every boot (cheap but free win), dead `image_signatures` schema, PRAGMA per-connection concern when pool widens, no backup verification path.

---

## Cross-tier patterns (single fixes that touch many modules)

### Pattern A — Fire-and-forget DB-write goroutines vs `MaxOpenConns=1`

Affects: `auth` (security_events insert), `audit` (middleware insert).
Root cause: every request spawns a goroutine to write to a DB with one connection. Goroutines accumulate, shutdown loses writes.
**Single fix:** common helper `db.AsyncWriter` — buffered channel + single consumer goroutine + `select { default: drop+log }` overflow policy. Adopted by both modules.

### Pattern B — Background goroutines missing `recover()`

Affects: `monitor` (6 collectors), `cluster` (heartbeat + retention pumps), `audit` (retention), `auth` (refresh-token retention).
**Single fix:** small helper `safe.Go(name string, ctx context.Context, fn func(context.Context))` that wraps the goroutine entry with `defer recover() + slog.Error + maybe-restart`. Replace every `go func()` background spawn.

### Pattern C — No context propagation to DB writes

Affects: `auth`, `audit`, `db/migrations`, `common/exec`.
**Single fix:** sweep replacing `db.Exec` → `db.ExecContext(ctx, ...)` for non-init paths. Tedious but mechanical.

### Pattern D — Missing audit_logs indexes for v0.13.15 use-cases

Affects: `audit`, `db` (same finding, different angles).
**Single fix:** one migration #28: `idx_audit_logs_node_id`, partial `idx_audit_logs_unprotected_created`, `idx_refresh_tokens_expires`, `idx_container_metrics_history_ts`.

### Pattern E — SQLite read-concurrency throttled by `MaxOpenConns=1`

Affects: every read endpoint (audit list, metrics history, container metrics, cluster status).
**Single fix:** split `*sql.DB` into reader pool (8) + writer (1) with internal mutex around DDL. Foundational; benefits every module.

### Pattern F — Commander has no context

Affects: every feature module that runs subprocesses.
**Single fix:** add `RunCtx`/`RunCtxWithEnv`/`RunCtxWithInput` to the `Commander` interface, pass `c.Request().Context()` from handlers. Mechanical sweep; can phase by tier.

---

## Recommended remediation grouping

Suggested plan filenames + scope (created in Phase 7 after R-final):

1. **`2026-05-18-fix-p0-stream-auth-and-leaks.md`** — P0-1 (gRPC stream interceptor or RequireAndVerifyClientCert + separate Join listener) + P0-2 (Heartbeat recv goroutine teardown) + P0-3 (RunWithTimeout(0) special case). One commit, three small patches. **Land first.**
2. **`2026-05-18-fix-async-writer-pattern.md`** — Pattern A (audit + security_events). New `db.AsyncWriter` helper + migrations.
3. **`2026-05-18-fix-safe-goroutine-pattern.md`** — Pattern B sweep across monitor/cluster/audit/auth.
4. **`2026-05-18-fix-indexes-migration-28.md`** — Pattern D. Single migration step.
5. **`2026-05-18-fix-commander-context.md`** — Pattern F. Big touch surface; phase by tier.
6. **`2026-05-18-fix-db-pool-split.md`** — Pattern E. Foundational refactor; needs care.
7. **`2026-05-18-fix-auth-cluster-correctness.md`** — auth P1 cluster (refresh.go tx.Commit drops, follower auto-forward rate-limit) + cluster P1 list (raft_fsm SetOnDisband ordering, Leave fallback, proxy.go name-vs-id, ClusterUpdate error surfacing).
8. **`2026-05-18-fix-monitor-rollup-and-retention.md`** — monitor P1 (rollup + ts index) + update checker leader-only.
9. **`2026-05-18-misc-p2-p3-sweep.md`** — middleware-misc CSP tightening, request_logger skip list + correlation, p3 cleanup batch.

Sequencing: **1 → 4 → 2 → 3 → 8 → 7 → 6 → 5 → 9**.
Rationale: 1 first (the P0 fixes shouldn't wait); 4 next (single migration, unblocks subsequent perf work); 2+3 mechanical sweeps with high leverage; 8 covers a defined area cleanly; 7 cleans correctness in the most-touched modules; 6 (pool split) is the biggest refactor and needs benchmark validation; 5 (Commander context) sweeps everywhere — last to avoid colliding with concurrent T2-T5 work; 9 absorbs leftovers.

---

## Exit criteria for T1

- [x] All 7 modules in T1 scope reviewed
- [x] 3 P0 findings flagged for immediate fix
- [x] Cross-cutting patterns identified (A–F)
- [x] Remediation plan list drafted
- [ ] User checkpoint — proceed to T2 vs land P0 fixes first
