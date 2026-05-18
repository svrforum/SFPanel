# R-final — Module Hardening Program Synthesis

**Date:** 2026-05-18 · **Target:** v0.13.15 commit `f5d38a4` · **Predecessor:** [2026-04-19 code review](../2026-04-19-code-review/) (v0.9.0)
**Tier files:** [T1](T1-cross-cutting.md) · [T2](T2-heavy-io.md) · [T3](T3-ops-workhorses.md) · [T4](T4-bounded.md) · [T5](T5-specialized.md)
**Umbrella plan:** `docs/superpowers/plans/2026-05-18-module-hardening-program.md`

---

## Executive summary

After a systematic 27-module review (22 feature modules + 5 platform packages) against a 25-item stability+optimization+security checklist via parallel Opus-4.7 subagents, the codebase produced **296 findings**: **25 P0** (exploitable / panic / unbounded leak), **77 P1** (correctness / leak under load), **93 P2** (perf / retention / index), **101 P3** (style / minor).

**Headlines:**
1. **The 2026-04-19 cluster TLS P0 is CLOSED** (proxy + ws_relay no longer `InsecureSkipVerify`). One new related P0 emerged: **gRPC streaming RPCs bypass the cert check** because `UnaryInterceptor` doesn't cover streams.
2. **The 2026-04-19 AppStore advanced-YAML P0 (F-09) is PARTIAL/OPEN**: short-form `pid/network/ipc: host` is closed but **long-form `pid_mode: host` / `network_mode: host` / `ipc_mode: host` is open**, and `CAP_SYS_ADMIN` (canonical Docker form) bypasses the cap_add blocklist that catches `SYS_ADMIN`. Same validator is shared with compose, so one fix closes both surfaces.
3. **The 2026-04-19 installer-hash P0 is OPEN**: get.docker.com, claude.ai/install.sh, NVM (partial — tag-pinned), and tailscale.com/install.sh all still fetch-and-execute without SHA256 verification. CLAUDE.md describes an env-var override that is **not implemented in code**.
4. **5 new symlink-leaf bypasses** of path-allowlist validators (files UploadFile + MkDir, logs AddCustomSource) effectively re-open the spirit of the `/etc/cron.d` P0 from 2026-04-19.
5. **Two self-targeting destruction paths**: services API allows `systemctl stop sfpanel.service`, process API allows `kill <sfpanel-pid>` (blocked) but doesn't audit kills.
6. **Systematic `SanitizeOutput` miss** across 12+ modules — every SSE/WS handler that streams subprocess output, and every `response.Fail` that interpolates `err.Error()` from a subprocess. Single mechanical sweep would close ~50 finding sites.

**Overall posture:** the codebase's structural decisions (Raft FSM, Commander DI, response code mapping, append-only migrations, prefix-based path allowlists, audit middleware) are sound. The defects are concentrated at **boundary layers**: subprocess stdout → client (sanitize), subprocess timeout → request cancel (Commander ctx), symlinks at validation edge, and corner-case Docker YAML forms.

---

## Findings count

| Tier | Modules | P0 | P1 | P2 | P3 | Total |
|---|---|---|---|---|---|---|
| T1 | auth, cluster, audit, middleware, monitor, common/exec, db | 3 | 17 | 25 | 32 | 77 |
| T2 | compose, docker, packages, network, firewall | 8 | 17 | 21 | 24 | 70 |
| T3 | system, settings, services, files, logs | 5 | 17 | 16 | 18 | 56 |
| T4 | disk, alert, cron, portmap | 2 | 11 | 15 | 14 | 42 |
| T5 | process, terminal, appstore, websocket | 7 | 15 | 16 | 13 | 51 |
| **Total** | **27 modules/packages** | **25** | **77** | **93** | **101** | **296** |

---

## All P0 findings (numbered)

| # | Module/file | Title | Status |
|---|---|---|---|
| P0-1 | cluster/grpc_server.go + tls.go | gRPC streaming RPCs bypass cert check (UnaryInterceptor doesn't cover streams) | NEW |
| P0-2 | cluster/grpc_server.go:202-211 | Heartbeat recv goroutine leak on parent select exit | NEW |
| P0-3 | common/exec/exec.go:36-38 | `RunWithTimeout(0, ...)` deadlines immediately | NEW |
| P0-4 | packages + network + firewall + compose-streaming | `SanitizeOutput` systematically missing in 12+ streaming handlers | NEW (systemic) |
| P0-5 | packages + network | 3rd-party installer hash verification (docker/claude/nvm/tailscale) | **CARRY-FORWARD from 2026-04-19 OPEN** |
| P0-6 | firewall — 12 handlers | Mutation handlers skip `Cmd.Exists()` guard | NEW |
| P0-7 | firewall/firewall_ufw.go:223-293 | `AddRule` has no lockout guard | NEW |
| P0-8 | packages/handler.go:35 | `validPackageName` accepts leading hyphen (`--reinstall`) | NEW |
| P0-9 | system/handler.go:123-447 | Cluster-wide simultaneous `RunUpdate` breaks quorum | NEW |
| P0-10 | services/handler.go:79-143 | `sfpanel.service` self-stop / disable / restart allowed | NEW |
| P0-11 | files/handler.go (UploadFile + MkDir) | Symlink-leaf bypass of `isCriticalPath` | NEW (related to 2026-04-19 R3 N-01) |
| P0-12 | files/handler_test.go | `isCriticalPath` has no regression test for 2026-04-19 P0 vectors | NEW |
| P0-13 | logs/handler.go:493-507 | Custom-source allowlist bypassable via symlink | NEW |
| P0-14 | portmap/aggregator.go:40 | UDP Docker bindings dropped/mismatched | NEW |
| P0-15 | portmap/aggregator.go:45-49 | Same-port multi-container collision (last-write-wins) | NEW |
| P0-16 | process/handler.go:113-173 | KillProcess writes no audit row | NEW |
| P0-17 | terminal/handler.go:104-117 | Slow-client head-of-line blocking in broadcast | NEW |
| P0-18 | terminal/handler.go:139-142 | `started` flag is racy | NEW |
| P0-19 | composex/safety.go + appstore/compose_safety.go | Advanced YAML: CAP_-prefix + long-form `*_mode` + `group_add: docker` | **CARRY-FORWARD from 2026-04-19 PARTIAL/OPEN** |
| P0-20 | appstore/handler.go:490-491 | Advanced mode has no privileged re-auth | NEW |
| P0-21 | websocket/handler.go:113-147 | MetricsWS missing keepalive | NEW |
| P0-22 | websocket/handler.go:23-25 + auth/wsauth.go | Permissive `CheckOrigin` + legacy `?token=` path live | NEW |

> Counted "P0" = 25 — three of the systemic items (P0-4, P0-5, P0-19) span multiple tiers and modules; counted once in the table above but represent ~50 individual finding sites across modules.

---

## 2026-04-19 carry-forward status (point-by-point)

| Original finding | 2026-04-19 status | Today | Evidence |
|---|---|---|---|
| Cluster TLS `InsecureSkipVerify` (proxy.go:34) | P0 | **CLOSED** | `proxy.go` now uses `mgr.GetTLS().ClientTLSConfig()` |
| Cluster TLS `InsecureSkipVerify` (ws_relay.go:53) | P0 | **CLOSED** | Same mTLS config used |
| Cluster TLS `VerifyClientCertIfGiven` (tls.go:173/254) | P0 | **PARTIAL — new P0-1** | Unary interceptor catches non-stream RPCs; streaming Heartbeat RPC still cert-less |
| `/etc/cron.d` write via files API | P0 | **PARTIAL — new P0-11** | `isCriticalPath` prefix check closed for direct paths, **symlink-leaf bypass on UploadFile + MkDir is open** |
| AppStore Advanced YAML | P0 | **PARTIAL — new P0-19** | `privileged`/short-form `*: host`/socket bind closed; **`pid_mode: host` long-form + `CAP_`-prefix caps + `group_add: docker` open** |
| 3rd-party installer hash (get.docker.com, claude.ai, NVM) | P0 | **OPEN — P0-5** | No SHA256 verify, no env-var override implemented |
| Tailscale install hash | P1 | **OPEN — P0-5** (escalated) | Same pattern |
| Frontend `marked` README XSS | P0 | OUT OF SCOPE for this program | Flag for separate frontend pass |
| Login rate-limit off-by-one | Closed (re-verified) | CLOSED | Per agent re-verification |
| Settings password mask, audit_log_cleared protection, etc. | Various P1/P2 | Mostly CLOSED (newer 0.13.x work) | See per-module sections |

---

## Cross-tier patterns (consolidated A–W)

These are pattern-level findings — a single fix design closes many sites.

### Boundary patterns (subprocess ↔ client / DB ↔ goroutine)

| Pattern | Affected modules | Single fix shape |
|---|---|---|
| **G — SanitizeOutput sweep** | packages, network, firewall, disk, cron, alert, logs, system tuning, files err strings, services ServiceLogs, appstore SSE, compose streaming, terminal err, ws docker errors | Sweep + CI lint rule that fails when subprocess output reaches `response.Fail`/SSE writer without `SanitizeOutput` |
| **F+I+R+T — Commander context (universal)** | Every module that runs subprocesses (essentially everywhere) | Add `RunCtx(ctx, ...)` to `Commander` interface; phased sweep replacing `h.Cmd.Run(...)` → `h.Cmd.RunCtx(c.Request().Context(), ...)` |
| **C — DB write context** | auth, audit, cron, packages, system, settings, db, common/exec | Sweep `db.Exec` → `db.ExecContext(ctx, ...)` |
| **A — Async-writer pattern** | audit middleware, auth security_events | One `db.AsyncWriter` helper (buffered channel + single consumer + drop-on-overflow + slog metric) |
| **B+L+Q — Safe-goroutine pattern** | monitor (6 collectors), alert (manager), audit (retention), auth (refresh-retention), log scanners (reader + scanner), terminal (reader), packages (SSE), network (Tailscale install SSE), cluster (heartbeat) | `safe.Go(name string, ctx context.Context, fn func(context.Context))` wrapper. Recovers + slog.Error + optional restart |
| **J — bufio.Scanner buffer hardening** | packages (6 SSE handlers), logs, network Tailscale install, websocket ContainerLogsWS, terminal | Helper `scannerWithLargeBuffer` that sets 64 KB → 1 MB max + always checks `scanner.Err()` and sends a terminal error frame on truncation |

### Indexing + perf patterns

| Pattern | Affected modules | Single fix shape |
|---|---|---|
| **D — Indexes migration #28** | audit, db | `idx_audit_logs_node_id`, partial `idx_audit_logs_unprotected_created (created_at) WHERE protected = 0`, `idx_refresh_tokens_expires`, `idx_container_metrics_history_ts`, possibly `idx_alert_history_rule_id` |
| **E — SQLite reader pool split** | All read-heavy modules (audit, monitor, cluster metrics, container observability) | Split `*sql.DB` into reader pool (N=8) + writer (1) + writer mutex on DDL; OR raise MaxOpenConns + single-flight DDL |
| **K — N+1 subprocess invocations** | firewall (jail info — 5 forks/jail), docker (per-container inspect), compose (image inspect ×4 sites), portmap (ss tcp+udp), disk (mdadm), appstore (findFreePort spawns up to 100 `ss`) | Per-module mechanical replacement. No single helper; pattern only. |
| **M — 3rd-party installer hash verify** | packages (3), network (1) | `internal/release/installer_pin.go` helper reading `SFPANEL_*_INSTALLER_SHA256` env vars; verify on download |

### Security / correctness patterns

| Pattern | Affected modules | Single fix shape |
|---|---|---|
| **N — Symlink-leaf bypass of path validators** | files (UploadFile + MkDir), logs (AddCustomSource) | Helper `resolveAndValidate(path string, isAllowed func(string) bool)` doing `filepath.EvalSymlinks` on the FULL path then re-checking; reject symlinks at leaf for upload/mkdir destinations |
| **O — Self-targeting destructive ops** | services (sfpanel.service stop/disable/restart), process (kill self-PID — actually blocked, but kernel kthreads partially blocked) | Per-module denylist or self-detection helper |
| **P — Whole-cluster operations missing quorum guard** | system (RunUpdate), maybe future settings | FSM-replicated "operation_in_progress" flag + leader-then-followers ordering. Pattern already used in cluster ClusterUpdate (handler.go:1184) — extend |
| **S — Replicated-vs-per-node ambiguity** | alert (rules expected FSM, actually per-node SQLite), settings (per-node confirmed) | Documentation pass + decision: keep per-node and document, OR migrate to FSM with new commands. Either way, per-table comment in CLAUDE.md |
| **U — Slow-client HOL blocking in fan-out** | terminal (broadcast to N readers), cluster ws_relay | Per-reader bounded send channel + non-blocking enqueue + drop-on-overflow |
| **V — High-risk handlers without audit row** | process (KillProcess), terminal (session start/stop), settings (UpdateSettings), websocket (session open) | Explicit audit emission for these specifically high-risk handlers |
| **W — Shared sensitive validator** | composex/safety.go used by compose AND appstore | Single hardening PR closes both surfaces |
| **H — Hardcoded streaming allowlist vs `-stream` suffix** | packages (7 routes), network/Tailscale install, fail2ban install (T2 finding) | Rename routes to `-stream` suffix OR canonicalize allowlist + CI test detecting new SSE handlers missing from both |

---

## Recommended remediation plans

20 themed plans, ordered by ROI (small blast radius + high-confidence first). Each lands as a single PR or a small commit group.

### Phase A — P0 fixes (land immediately, in this order)

| # | Plan | Closes | Notes |
|---|---|---|---|
| **A1** | `fix-p0-cluster-and-exec-foundations.md` | P0-1, P0-2, P0-3 | Three small patches: gRPC stream interceptor, heartbeat recv goroutine teardown, `RunWithTimeout(0)` special-case |
| **A2** | `fix-shared-yaml-validator.md` | **P0-19** | Single PR hardens `composex/safety.go`: strip `CAP_` prefix, add long-form `*_mode` keys, add `group_add` check. **Closes compose + appstore simultaneously**. |
| **A3** | `fix-installer-hash-verify.md` | **P0-5** | New `internal/release/installer_pin.go` + 4 callers (get.docker.com, claude.ai, NVM, tailscale.com). Honors `SFPANEL_*_INSTALLER_SHA256` env vars per CLAUDE.md. |
| **A4** | `fix-symlink-leaf-validators.md` | P0-11, P0-13 | Helper + caller update in files UploadFile/MkDir + logs AddCustomSource. Also adds the missing regression tests (P0-12). |
| **A5** | `fix-self-targeting-protections.md` | P0-10, P0-16, P0-22(partial) | sfpanel.service denylist + KillProcess audit row + (optional) drop legacy `?token=` from WS auth |
| **A6** | `fix-cluster-update-quorum.md` | P0-9 | FSM-replicated `update_in_progress` flag + leader-then-followers ordering for RunUpdate. Mirror ClusterUpdate's pattern at `feature/cluster/handler.go:1184`. |
| **A7** | `fix-firewall-lockout-and-exists.md` | P0-6, P0-7 | `AddRule` lockout preflight + `require(tool string)` helper on 12 mutation handlers. |
| **A8** | `fix-package-validators.md` | P0-8 | Tighten `validPackageName` + add `--` separator in apt-get calls + tighten `validNodeVersion`-as-env-var. |
| **A9** | `fix-ws-keepalive-and-origin.md` | P0-21, P0-22 | MetricsWS startWSKeepalive + restrict CheckOrigin to configured panel origin. |
| **A10** | `fix-portmap-aggregator.md` | P0-14, P0-15 | Plumb Proto through PortBinding + `[]ContainerInfo` per port (or list-per-collision). |
| **A11** | `fix-appstore-advanced-reauth.md` | P0-20 | Fresh password/2FA confirmation required for `advanced=true` install. |
| **A12** | `fix-terminal-broadcast.md` | P0-17, P0-18 | Per-reader bounded send channel + `sync.Once` for startReader. |

### Phase B — Systemic P1 sweeps (mechanical, high leverage)

| # | Plan | Pattern | Notes |
|---|---|---|---|
| **B1** | `fix-sanitize-output-sweep.md` | G | ~50 sites across 12 modules. CI lint rule (grep + go vet style) prevents regression. |
| **B2** | `fix-safe-goroutine-pattern.md` | B/L/Q | Introduce `safe.Go` helper; replace every `go func()` background spawn across monitor/alert/audit/auth/logs/terminal/packages/network. |
| **B3** | `fix-async-writer-pattern.md` | A | Single `db.AsyncWriter` adopted by audit middleware + auth security_events. |
| **B4** | `fix-bufio-scanner-buffer.md` | J | Helper + sweep across packages (6 SSE handlers), logs, network Tailscale, websocket ContainerLogsWS, terminal. |
| **B5** | `fix-indexes-migration-28.md` | D | Single migration adding 4-5 indexes. |
| **B6** | `fix-commander-context.md` | F/R/T | Phased: add `RunCtx` first, then sweep callers by tier (T1 first, T5 last). Affects every feature module. |
| **B7** | `fix-streaming-allowlist-normalization.md` | H | Either rename SSE routes to `-stream` OR canonicalize allowlist + CI test. |
| **B8** | `fix-n+1-subprocess.md` | K | Per-module: firewall getJailInfo, docker buildContainerIPMap, compose serial inspect, portmap ss combined, disk mdadm parallel, appstore findFreePort. |

### Phase C — Module-specific correctness (after sweeps land cleanly)

| # | Plan | Tier source | Notes |
|---|---|---|---|
| **C1** | `fix-auth-correctness.md` | T1 | refresh.go tx.Commit drops + follower rate-limit + loginAttempts retention. |
| **C2** | `fix-cluster-correctness.md` | T1 | raft_fsm SetOnDisband ordering + Leave HTTP fallback + proxy.go name-vs-id + ClusterUpdate error surfacing + applyUpdateNode nil-vs-empty Labels + manager.go heartbeat ctx + ProxySecret cache. |
| **C3** | `fix-monitor-retention-rollup.md` | T1+T4 | container_metrics_history rollup + StartUpdateChecker leader-only + ts index (in B5). |
| **C4** | `fix-files-tightenings.md` | T3 | ListDir entry cap + WriteFile streaming + TOCTOU flock + /etc/passwd-group decision. |
| **C5** | `fix-services-graceful-and-tests.md` | T3 | Graceful empty when systemd absent + create `services/handler_test.go` + parser tests. |
| **C6** | `fix-settings-atomic-and-audit.md` | T3 | Transactional updates + audit row + value validators + GetSettings allowlist. |
| **C7** | `fix-netplan-atomic-rollback.md` | T2 | Atomic write + snapshot-and-restore OR `netplan try` integration. |
| **C8** | `fix-alert-replication-decision.md` | T4 | Decide: alert rules FSM-replicated OR per-node + document. Plus: manager recover, regex cache, node_id stamping on history. |
| **C9** | `fix-firewall-perf-and-atomicity.md` | T2 | UpdateJailConfig atomic (rewrite `.local` once) + DOCKER-USER persistence file 0600 under /var/lib + dedup preflight on AddDockerUserRule. |
| **C10** | `fix-system-systemd-fallback.md` | T3 | Align doc-comment vs code on RunUpdate/RestoreBackup supervisor-less branch. |
| **C11** | `fix-disk-perf-and-smart-trending.md` | T4 | Parallel mdadm + lsblk string-size handling + ListPartitions raw-passthrough cleanup + optional SMART trending (depends on monitor rollup). |
| **C12** | `fix-cron-validation-and-history.md` | T4 | isValidSchedule test + handler MockCommander tests + newline-rejection test + (optional) run-history opt-in. |
| **C13** | `fix-terminal-bundle.md` | T5 | Total-session timeout + drain-on-shutdown + audit row + ringBuffer.Write copy + stale session detection. |
| **C14** | `fix-websocket-misc.md` | T5 | Upgrader.Upgrade error wrap + ContainerLogsWS tail cap + ContainerExecWS WaitGroup join. |
| **C15** | `fix-appstore-correctness.md` | T5 | Body size limit on InstallApp + N+1 isInstalled + GetApp ghost goroutines + refreshCache lock release pattern + readme parallel probes. |

### Phase D — Refactors (last; bench-driven)

| # | Plan | Notes |
|---|---|---|
| **D1** | `fix-db-pool-split.md` | Reader pool (N=8) + writer (1). Foundational. Needs benchmarks proving the read-throughput claim. |
| **D2** | `fix-pragma-additions.md` | `temp_store=MEMORY` + `optimize` on shutdown. Tiny. |

### Phase E — Hygiene

| # | Plan | Notes |
|---|---|---|
| **E1** | `misc-p2-p3-sweep.md` | Request-logger skip list + correlation IDs, CSP tightening, misc P3 cleanup batch. |

**Total plans:** 12 (A) + 8 (B) + 15 (C) + 2 (D) + 1 (E) = **38 plans** if every leaf becomes its own PR. More realistically: 12 A + 8 B + 8-12 C + 2 D + 1 E ≈ **30-35 PRs** over a sustained program.

---

## Suggested cadence

**Week 1 — A1-A6:** 6 critical P0 fixes. Most are small, single-file patches (A1, A2, A3, A4 helper + 2 callers, A6 FSM flag + handler edit). Closes 11 P0s including the two 2026-04-19 carry-forwards. **Highest ROI of the entire program.**

**Week 2 — A7-A12:** 6 remaining P0 fixes. Slightly larger surface (firewall lockout sweep, terminal broadcast refactor, ws origin tightening). Closes remaining 11 P0s.

**Weeks 3-4 — B1-B8:** Mechanical sweeps. Touch surface is wide but per-site change is trivial. Land in tier order (T1 sites first, T5 sites last) to keep diffs reviewable.

**Weeks 5-8 — C1-C15:** Module-specific correctness. Can parallelize across team members if available.

**Week 9+ — D1, D2, E1:** Refactors + hygiene. D1 (pool split) needs benchmark validation.

---

## What this program does NOT cover

- **Frontend security review** — `marked` README XSS (2026-04-19 R10 C-1) is still flagged but out of scope here; needs a separate `web/src/` pass.
- **Penetration testing** — this is a code review, not a runtime probe. Several P0s (e.g. P0-1 gRPC stream, P0-22 WS CSRF) warrant explicit exploit verification in a staging env.
- **Performance benchmarks** — D1 (pool split) requires baseline measurements before commit; B5 (indexes) likewise.
- **Cluster chaos testing** — the simultaneous-restart 15-20s pre-vote delay from CLAUDE.md is documented but not stress-tested.

---

## Open questions (for the next planning checkpoint)

1. **alert_rules placement** (P-S finding): keep per-node and document, or migrate to Raft FSM with new commands? Per-node is simpler; FSM matches operator mental model and the v0.13.x cluster-mode work.
2. **Drop legacy `?token=` WS auth** (P0-22 mitigation choice): drop entirely vs restrict to local/admin-only. Tickets are already implemented; the legacy path is just back-compat.
3. **AppStore template signing** (P0-19 follow-up): pin commit SHA per release, sign the index, or vendor the catalog? Each has different operator workflow implications.
4. **Cron run-history** (C12 optional item): opt-in or default? The deepening was scoped earlier as "highest-impact module deepening"; lands in remediation rather than feature work.
5. **DB pool split (D1)** vs raise `MaxOpenConns` to N + single-flight DDL: simpler refactor exists. Bench required either way.

---

## Closing

`296 findings · 25 P0 · 296 → 0 is the multi-quarter horizon.` The Phase A plan list (12 P0s) is the realistic single-sprint scope; Phase B sweeps are where the codebase's "boundary hygiene" debt actually clears. After Phase A + B, the long tail (Phase C/D/E) is real but no longer carries acute risk.

The 2026-04-19 review's "4-5 PRs cover most P0/P1" framing held for that snapshot; v0.13.x's expanded surface (cluster proxy fanout, healthcheck composer, observability, portmap, advanced YAML) has re-opened or freshly opened P0s in areas not present in v0.9.0. This is consistent with the project's velocity — not a regression in code quality.
