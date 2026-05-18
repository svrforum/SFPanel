# T3 — Operations workhorses

**Reviewed at:** v0.13.15 commit `f5d38a4` · 2026-05-18
**Scope:** `system` · `settings` · `services` · `files` · `logs`
**Method:** 5 parallel read-only subagent reviews (Opus 4.7)

---

## Findings count

| Module | P0 | P1 | P2 | P3 | Total |
|---|---|---|---|---|---|
| system | **1** | 2 | 3 | 5 | 11 |
| settings | 0 | 2 | 2 | 2 | 6 |
| services | **1** | 2 | 3 | 3 | 9 |
| files | **2** | 6 | 5 | 4 | 17 |
| logs | **1** | 5 | 3 | 4 | 13 |
| **Total** | **5** | **17** | **16** | **18** | **56** |

**Running total (T1+T2+T3): 16 P0, 51 P1, 62 P2, 74 P3 = 203 findings**

---

## P0 — must fix

### P0-9. Cluster-wide simultaneous `RunUpdate` breaks quorum
**File:** `system/handler.go:123-447`

`POST /system/update?node=A` and `?node=B` issued in parallel both `systemctl restart` simultaneously. CLAUDE.md "Cluster operational notes" calls out the 15-20 s pre-vote delay; two voters down within that window = no leader.

**Fix sketch:** FSM-replicated `update_in_progress` flag with leader-then-followers ordering; reject second concurrent update with HTTP 409 when another voter is mid-update.

### P0-10. `services` allows `sfpanel.service` self-stop / disable / restart
**Files:** `services/handler.go:79-92, 96-109, 130-143`

`validServiceName` regex `^[a-zA-Z0-9@._:-]+\.service$` accepts `sfpanel.service`. `POST /system/services/sfpanel.service/stop` kills the panel mid-response. `disable` would also prevent systemd auto-restart of the panel. CLAUDE.md says only documented handlers may exit the process — this is an undocumented exit path.

**Fix sketch:** denylist `sfpanel.service` (and arguably `dbus`, `systemd-*`, `ssh`/`sshd`) at the regex level OR refuse with `ErrForbidden` if the unit name matches the panel's own service.

### P0-11. `files` UploadFile + MkDir symlink-leaf bypass of `isCriticalPath`
**Files:** `files/handler.go:587-625` (UploadFile) and `:396-418` (MkDir)

`validatePathForWrite` resolves the *parent* of the supplied path but treats the supplied path itself as the leaf. When the supplied path is the destination *directory* (UploadFile's `destDir`, MkDir's `req.Path`), a leaf-level symlink (`/tmp/sneaky -> /etc/cron.d`) is never resolved, so `MkdirAll`/`os.Create` follow it into a protected tree.

**Attack:** create `/tmp/sneaky` as a symlink to `/etc/cron.d/`, then upload `backdoor` to `/tmp/sneaky` — file lands as `/etc/cron.d/backdoor`. WriteFile (full path) is safe because `EvalSymlinks(parentDir)` catches it; UploadFile/MkDir validate the dir as the leaf and skip resolution.

**Fix sketch:** call `filepath.EvalSymlinks` on the full supplied path (not just parent) and re-run `isCriticalPath` on that resolution; OR `os.Lstat` the leaf and reject symlinks for upload/mkdir destinations entirely.

### P0-12. `files` `isCriticalPath` has no regression test for 2026-04-19 P0 vectors
**File:** `files/handler_test.go`

The 2026-04-19 R3 N-01 fix (prefix-based isCriticalPath) has **zero regression guard**. A future refactor (e.g., switching back to exact-match "for performance") would silently re-introduce the original P0.

**Fix sketch:** add explicit assertions for every entry in `criticalPaths` + a few representative path traversals (`/etc/cron.d/backdoor`, `/etc/sudoers.d/foo`, `/etc/systemd/system/x.service`, `/usr/local/bin/sfpanel`, `/etc/init.d/x`, `/etc/profile.d/x.sh`), plus negatives.

### P0-13. `logs` custom-source allowlist bypassable via symlink
**File:** `logs/handler.go:493-507`

Allowlist requires `/var/log/` or `/opt/` prefix. No `EvalSymlinks`. Operator creates `/var/log/sneak` as a symlink to `/etc/shadow`, calls `AddCustomSource(/var/log/sneak)`, then `ReadLog` happily tails `/etc/shadow`.

**Fix sketch:** `filepath.EvalSymlinks(path)` and re-check resolved path against allowlist (same fix shape as P0-11). Plus tighten `/opt/` (too permissive — see P1 below).

---

## Per-module sections

### system

**What it does:** GitHub-driven self-update (download → cosign+sha256 verify → atomic binary swap → watchdog rollback → systemd-or-self-exit), tar.gz backup/restore, sysctl tuning with 60s auto-rollback.

**Findings:**

- **P0-9** — cluster simultaneous update (see top).
- **P1 — systemd-fallback semantics don't match the comment.** `handler.go:435-446, 660-676`. Code unconditionally schedules `exitProcess()` whenever the systemctl branch wasn't taken, including the "systemd is up but unit is inactive" sub-case. Test at `handler_test.go:686` enforces this behavior — so either the doc claim is stale or the code is. Clarify intent.
- **P1 — Tuning rollback holds `rollbackMu` during N `sysctl -w` invocations.** `tuning.go:285-298`. Default config has ~50 params × 5-min commandTimeout. A hung sysctl call blocks every other tuning RPC.
- **P2 — No `req.WithContext(ctx)` on outbound HTTP.** `handler.go:94, 131, 189, 202, 453`. Client disconnect during 5-min archive download doesn't cancel.
- **P2 — Raw subprocess stderr in `response.Fail`.** `tuning.go:252, 256, 267, 362`. Bypasses SanitizeOutput.
- **P2 — Gzip-bomb risk on `io.ReadAll(tr)`** for in-archive binary at `handler.go:317`. cosign+sha256 chain covers it today but cheap defense-in-depth.
- **P3** ×5 — Tmp dir leak on SIGKILL mid-update, binary .bak read into RAM (50 MB unnecessary), sysctl conf parsing duplicated, deadline negative-overflow clamp, etc.
- **Test gaps (7)** — no cosign verify path tests integrated into RunUpdate, no watchdog spawn path test, no systemd-active happy-path RunUpdate test, no tuning rollback timer firing test, no `ApplyTuning` mid-confirmation rejection test, no `RestoreBackup` DB rename failure test, no ctx-cancel-during-download regression.

---

### settings

**What it does:** Per-node KV store for two user-settable settings (terminal_timeout, max_upload_size) + GetSetting helper.

**Findings:**

- **P1 — `UpdateSettings` is not transactional** despite a doc-comment promising atomic batch. `handler.go:90-98` — loop of `INSERT … ON CONFLICT` outside a `BeginTx`. Concurrent batches can interleave; mid-loop failure leaves partial writes.
- **P1 — No audit row for settings mutations.** Settings is exactly the admin-visible state change auditors expect to see; today it's invisible.
- **P2 — Value validation is length-only.** `handler.go:84`. Doc cites "terminal_timeout=99999999 (DoS)" as the reason for allowlist, but the validator still permits exactly that. Need per-key validators.
- **P2 — `GetSettings` leaks every row** including keys other modules write (`appstore_cache`, etc.). Filter to a known-readable allowlist.
- **P3** ×2 — no `ctx` plumbing on DB calls; `GetSetting` helper returns `""` for unknown keys (no distinguishable miss-vs-blank).
- **Test gaps (6)** — no length-cap rejection test, no per-key validator tests, no GetSettings default-overlay test, no upsert semantics test, no concurrency atomicity test, no leakage-from-other-modules test.

---

### services

**What it does:** Wraps `systemctl` (and `journalctl`) for list/start/stop/restart/enable/disable/logs/deps, with a 3s in-memory cache for the list view.

**Findings:**

- **P0-10** — self-restart not blocked (see top).
- **P1 — No context propagation on any `h.Cmd.Run` call** (`handler.go:68, 85, 102, 119, 136, 163, 180, 268, 314`). Client-cancel doesn't abort `systemctl`/`journalctl`. `journalctl -u <name> -n 500` on a chatty unit can stall.
- **P1 — `ServiceLogs` returns `journalctl` output verbatim** (`handler.go:169`). No SanitizeOutput; service logs frequently contain tokens/secrets. Wrap + consider role-gating.
- **P2 — Non-graceful failure when systemd absent** (`handler.go:50-52`). Container nodes return 500 instead of empty `services:[]` with a flag. Breaks cluster aggregation UI.
- **P2 — `getEnabledStates` silently swallows error** (`handler.go:316`). Produces `enabled="unknown"` for everything with no log.
- **P2 — `list-units --all` has no upper bound.** Hosts with >2000 unit files yield megabytes of JSON.
- **P3** ×3 — cache copies on every ListServices request (unnecessary make+copy), parser fragile to systemd column changes (use `--output=json`), module-level singleton cache breaks if Handler is ever per-tenant.
- **Test gaps** — **no `services/handler_test.go` exists at all**. Need: validServiceName regex, fetchAllServices parser, filterDeps, self-restart denylist (after P0 fix), graceful-empty path.

---

### files

**What it does:** REST file browser for the panel — list/read/write/mkdir/rename/delete/upload/download as root, with critical-path + read-protect lists.

**Findings:**

- **P0-11 — UploadFile + MkDir symlink-leaf bypass** (see top).
- **P0-12 — `isCriticalPath` has no tests** (see top).
- **P1 — Raw error strings leak through `response.Fail`** at 17 sites (handler.go:237, 300, 317, 380, 386, 414, 445, 452, 497, 506, 513, 548, 599, 609, 651, 658, 667). `err.Error()` exposes absolute paths and Linux-error text.
- **P1 — `ListDir` has no entry cap** (`handler.go:229`). `?path=/proc` or a Docker overlay dir builds an unbounded slice. Cap + paginate.
- **P1 — `WriteFile` holds full body twice in RAM** (`handler.go:375`). With `maxWriteSize = 10 MB` × N concurrent writers, 20·N MB resident. Stream instead.
- **P1 — `UploadFile` `io.Copy` ignores request context** (`handler.go:654`). Client cancel mid-upload doesn't stop. With 1 GB defaults this matters.
- **P1 — Cluster proxy: `/files/download` and `/files/upload` need HTTP relay** verification. If not in proxy allowlist, `?node=` against remote buffers the whole body across gRPC unary.
- **P1 — No download rate limit / concurrency cap.** Single client can saturate upstream + Echo workers.
- **P1 — `MkDir` doesn't re-call `isCriticalPath` on resolved path** post-EvalSymlinks (`handler.go:404-410`). Even ignoring the symlink-leaf issue, MkDir trusts `validatePathForWrite` without defense-in-depth.
- **P2 — TOCTOU on backup-then-write** (`handler.go:358-388`). `Stat` → `Rename` to `.bak` → write `.tmp` → `Rename` has 3 windows. `flock(2)` or per-path mutex.
- **P2 — `.bak` file not in read-protected list.** Writing `/etc/sfpanel/config.yaml` leaves `/etc/sfpanel/config.yaml.bak` readable. Theoretical (write itself blocked) but worth a comment.
- **P2** ×3 — extension-only upload blocklist (`shell.php.txt` bypass), `isWebServedPath` micro-allocation, backup overwrite race.
- **P3** ×4 — `criticalPaths` should be `map[string]struct{}`, FileEntry.Mode could also return octal, validatePathForWrite error wrap leaks absolute path.
- **Test gaps (8)** — see "Status of 2026-04-19 P0 carry-forward" section below.

**Status of 2026-04-19 P0 carry-forward (`/etc/cron.d` write):**
- isCriticalPath prefix check: **CLOSED** (handler.go:127-141 walks every entry).
- Symlink bypass: **OPEN (P0-11)** — UploadFile + MkDir bypassable.
- `/etc/sudoers.d`, `/usr/local/bin`, `/etc/systemd/system`, `/etc/init.d`, `/etc/profile.d`: closed for direct paths, **OPEN for symlink-leaf upload/mkdir**.
- `/etc/passwd`, `/etc/shadow`, `/etc/group`, `/root/.ssh/authorized_keys`: write-blocked via /etc/root prefix. `/etc/passwd` and `/etc/group` are NOT in `readProtectedPaths` and ARE readable via `/files/read` — conventionally OK on Unix but worth flagging.

---

### logs

**What it does:** Built-in + custom log sources, paginated `tail -n` reads (`ReadLog`), `tail -F` WebSocket streaming (`LogStreamWS`), custom-source CRUD with absolute-path allowlist.

**Findings:**

- **P0-13 — Symlink-based path-traversal bypass** (see top).
- **P1 — No SanitizeOutput on log content** (`handler.go:300-304, 442`). Log lines frequently contain ANSI escapes (journalctl) and may contain pasted secrets.
- **P1 — No `recover()` in scanner / reader goroutines** (`handler.go:422, 435`). Panic crashes the whole panel.
- **P1 — `/opt/` allowlist too permissive.** `/opt/` houses arbitrary third-party installs; `/opt/some-app/data.db.sql` is readable as a "log source". Tighten to documented log subdirs or require existing-text-file check at add time.
- **P1 — `exec.Command` not `exec.CommandContext` for streaming tail/grep** (`handler.go:350, 357`). Mitigated by explicit `Kill` on disconnect, but pre-Kill panic (see recover gap) leaks subprocess.
- **P1 — Raw error text leaks into client response** (`handler.go:279, 531, 565`). Violates CLAUDE.md.
- **P2 — `countFileLines` re-runs `wc -l`/`grep -c` on every ReadLog.** Full file scan on each paginated render of `/var/log/syslog`. Cache or drop `total_lines`.
- **P2 — WS scanner has no backpressure.** Scanner reads from pipe as fast as possible; slow client hits 10s write deadline per line and scanner spins. Add buffered channel + drop policy.
- **P2 — Tests don't exercise WS handler.** Only the kill-pipe invariant captured.
- **P3** ×4 — LogStreamWS returns JSON before WS upgrade on missing source (client expects WS frames), allSources called twice per ReadLog, strings.Split allocation 2x, source-ID regex stripping can collide.
- **Test gaps (6)** — no ReadLog parameter tests, no AddCustomSource rejection tests, no symlink-traversal test (the P0), no DeleteCustomSource 404 test, no WS handler integration test, no filter-pipeline cleanup test.

---

## New cross-tier patterns (T3 additions)

### Pattern N — Symlink-leaf bypass of path-allowlist validators

Affects: `files` (UploadFile, MkDir), `logs` (AddCustomSource).
**Single fix:** helper `func resolveAndValidate(path string, isAllowed func(string) bool) (resolved string, err error)` that does `filepath.EvalSymlinks` on the full path and re-runs the predicate. Use everywhere a user-supplied path leaf could be a symlink.

### Pattern O — Self-targeting destructive operations not denylisted

Affects: `services` (sfpanel.service stop/disable), `system` (rollback to old binary), maybe `cluster` (Disband during your own active session).
**Single fix:** small helper `protectSelf(unitName string) error` (and analogs) used at handler entry.

### Pattern P — Cluster orchestration missing for whole-cluster operations

Affects: `system` (RunUpdate quorum guard missing).
**Single fix:** FSM flag for "operation-in-progress" + rolling vs simultaneous mode + quorum check. Already exists for ClusterUpdate at `feature/cluster/handler.go:1184` — extend pattern to RunUpdate.

---

## Updated remediation grouping (T1 + T2 + T3)

Now ~18 plans expected. Ordering refined:

1. **`fix-p0-stream-auth-and-leaks`** (T1) — gRPC stream auth + heartbeat goroutine + RunWithTimeout(0)
2. **`fix-symlink-leaf-validators`** (NEW T3) — Pattern N — files UploadFile + MkDir + logs AddCustomSource
3. **`fix-self-targeting-protections`** (NEW T3) — Pattern O — sfpanel.service denylist
4. **`fix-cluster-update-quorum`** (NEW T3) — Pattern P — RunUpdate FSM flag
5. **`fix-sanitize-output-sweep`** (T2, extended) — Pattern G across packages/network/firewall/compose-streaming + system tuning + files err strings + logs + services ServiceLogs
6. **`fix-installer-hash-verify`** (T2) — Pattern M
7. **`fix-firewall-lockout-and-exists`** (T2)
8. **`fix-package-validators`** (T2)
9. **`fix-netplan-atomic-and-rollback`** (T2)
10. **`fix-async-writer-pattern`** (T1)
11. **`fix-safe-goroutine-pattern`** (T1, extended) — collectors + SSE handlers + log scanners
12. **`fix-indexes-migration-28`** (T1, extended)
13. **`fix-cluster-correctness-and-streaming-allowlist`** (T2)
14. **`fix-auth-cluster-correctness`** (T1)
15. **`fix-monitor-rollup-and-retention`** (T1)
16. **`fix-firewall-perf-and-atomicity`** (T2)
17. **`fix-files-tightenings`** (NEW T3) — entry cap, body streaming, TOCTOU lock, /etc/passwd/group decision
18. **`fix-services-graceful-and-tests`** (NEW T3) — graceful empty + create `services/handler_test.go` + parser tests
19. **`fix-settings-atomic-and-audit`** (NEW T3) — transactional updates + audit row + value validators
20. **`fix-system-systemd-fallback`** (NEW T3) — clarify and align comment vs code
21. **`fix-commander-context`** (T1, extended)
22. **`fix-db-pool-split`** (T1)
23. **`misc-p2-p3-sweep`** (T1, extended)

Sequencing intent unchanged: P0 fixes first (1-4), then P1 sweeps with high leverage (5-12), then domain-specific correctness (13-20), then refactors (21-22), then cleanup (23).

---

## Exit criteria for T3

- [x] All 5 modules reviewed
- [x] 5 P0 findings flagged
- [x] Cross-tier patterns extended N-P
- [x] Remediation grouping updated to ~23 plans
- [ ] Continue to T4
