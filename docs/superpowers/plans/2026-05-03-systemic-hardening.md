# Systemic Hardening Plan

**Goal:** Address the four follow-up areas surfaced after the deployment-hardening pass: DB migration safety, complex-parser test coverage, HTTP API + shell-out security, and cluster split-brain fencing.

**Architecture:** Direct edits in main branch, four area-scoped commit groups. TDD on parsers (table-driven) and on security-sensitive validators. Each area's fixes can land independently — sequenced by ROI (smallest blast radius first) so a regression discovered mid-plan doesn't block the rest.

**Sequencing:** Area B (DB) → Area C (parser tests) → Area A (security) → Area D (cluster). DB first because it's the smallest blast radius and a migration crash today bricks a node. Parser tests next — pure additions, no behavior change, builds confidence in shared parsers before security fixes touch them. Security third — the fixes are localized but numerous. Cluster last — invasive, depends on stable foundations.

**Out of scope (explicitly):**
- Repo-wide adoption of structured request validation library — current pattern (per-handler regex + allowlist) is fine.
- Full Raft fuzz / chaos tests — would need a multi-process test harness; the work here adds unit-level fences and a documented partition runbook.
- Replacing SQLite with a different store.

---

## Area B — DB migration safety

**Files:**
- Modify: `internal/db/migrations.go` (transaction wrapping + schema_version + better idempotency)
- Modify: `internal/db/sqlite.go` (drop unused `sessions` DDL, expose CheckpointWAL helper)
- Modify: `internal/feature/system/handler.go` (force WAL checkpoint before DB .bak; rotation of stale .bak)
- Modify: `cmd/sfpanel/watchdog.go` (DB rollback alongside binary rollback)
- Modify: `internal/api/middleware/audit.go` (drop reliance on lastCleanup CAS-only; tick on background goroutine)
- New: `internal/feature/alert/retention.go` (alert_history pruner, mirrors audit.go)
- Modify: `internal/monitor/history.go` (periodic prune, not just at boot)
- Test: `internal/db/migrations_test.go` (idempotency + transaction rollback + schema_version)

### B.1 Add `schema_version` table + transaction wrapping
- Wrap each migration in `db.BeginTx`. New `schema_version` row tracks last applied; skip applied steps on re-run. Failure: rollback + `os.Exit(1)`.

### B.2 Force WAL checkpoint before DB .bak
- `internal/feature/system/handler.go` line 315: before `os.ReadFile(h.DBPath)`, run `PRAGMA wal_checkpoint(TRUNCATE)` so the .bak is a complete snapshot.

### B.3 DB rollback in watchdog
- `cmd/sfpanel/watchdog.go`: when binary rollback fires, also `cp sfpanel.db.bak sfpanel.db` (under SQLite-safe sequence: stop service → rename → start). Otherwise rolled-back old binary boots against new schema.

### B.4 Periodic retention pruners
- `audit_logs`: middleware/audit.go currently relies on incoming requests to trigger CAS-based prune. Replace with a single goroutine ticking every 5 min — runs even when idle.
- `alert_history`: new pruner keeps last 30 days OR 50 000 rows.
- `metrics_history`: history.go prune-at-boot only; add hourly tick.

### B.5 Drop dead `sessions` table
- DDL declared, no callers. Remove from migrations.go; document in commit so an existing DB's `sessions` is left alone (idempotent skip via schema_version).

### B.6 Tests
- `migrations_test.go`: in-memory SQLite (`:memory:`); run migrations twice, verify schema unchanged + schema_version row reflects all applied steps. Inject a deliberately-failing migration; verify it rolls back and earlier steps remain.

---

## Area C — Complex-parser test coverage

**Files (new test files only; no source changes):**
- Create: `internal/feature/firewall/firewall_ufw_test.go`
- Create: `internal/feature/firewall/firewall_fail2ban_test.go`
- Create: `internal/feature/firewall/firewall_docker_test.go`
- Create: `internal/feature/disk/disk_blocks_test.go`
- Create: `internal/feature/disk/disk_raid_test.go`
- Create: `internal/feature/disk/disk_swap_test.go`
- Create: `internal/feature/network/wireguard_test.go`
- Create: `internal/feature/cron/handler_test.go`
- Modify: `internal/release/release_test.go` (cover ParseExpectedSHA256 — currently untested)

### C.1 Test fixtures
- One real captured output per parser, kept short (10–40 lines). Tests are table-driven with `name / input / want / wantErr` rows.
- Edge cases per parser: empty input, malformed line (extra/missing fields), localized output, presence of comments/disabled entries, IPv6 + brackets, sentinel values (`(none)`, `--`, `n/a`).

### C.2 Per-parser tests
Coverage list (from inventory, lines reflect current code):
1. `parseUFWRules` (firewall_ufw.go:134) — IPv4 / IPv6 / numbered / disabled rules
2. `parseSSOutput` (firewall_ufw.go:370) — IPv6 brackets, multi-process `users:((...)`
3. `getDockerPublishedPorts` (firewall_docker.go:89) + `lookupDNATMapping` (178) + `buildReverseDNATMap` (208) — share regex; cover one fixture exercising all three
4. `parseFail2banJailStatus` (firewall_fail2ban.go:170) — banned IP list parsing, tree characters
5. `parseSmartctlJSON` (disk_blocks.go:277) + `computeSmartStatus` (262) — passing / pre-fail / failing
6. `parseLsblkJSON` + `convertLsblkDevice` (disk_blocks.go:113/164) — polymorphic size (number vs string) and rota/ro (bool vs "0"/"1")
7. `parseMdstat` (disk_raid.go:76) — clean / degraded / recovering / faulty
8. `parseDiskStats` (disk_swap.go:468) — sector-to-bytes math, 14- vs 18-column variants
9. `parseWGDump` (wireguard.go:88) — multi-peer, `(none)` sentinel, latest-handshake epoch
10. `parseCronLine` (cron/handler.go:241) + `extractScheduleAndCommand` (338) — env / comment / disabled / 5-field / @daily; round-trip safety
11. `ParseExpectedSHA256` (release/release.go:26) — gnu format with `*<file>`, plain format, missing entry, leading/trailing whitespace

### C.3 Verification
- `make test` green for all new files.
- Each test catches at least one mutated-input regression (e.g., flip a regex group index) — the goal is to actually exercise the failure mode the parser was written to handle.

---

## Area A — HTTP API + shell-out security

**Files:**
- Modify: `internal/feature/firewall/firewall_fail2ban.go` (add regex validation for `BanTime`/`FindTime`)
- Modify: `internal/feature/files/handler.go` (extension blocklist for upload; size cap on WriteFile body)
- Modify: `internal/feature/compose/handler.go` (apply `validateAdvancedCompose` to plain-compose CreateProject/UpdateProject too, gated by config flag for back-compat)
- Modify: `internal/config/config.go` (raise JWT secret minimum to 32 chars; add `auth.trusted_proxies` for X-Forwarded-For)
- Modify: `internal/api/middleware/auth.go` (only trust X-Forwarded-For when remote IP is in trusted_proxies)
- Modify: `internal/feature/auth/handler.go` (length-bound Username and TOTPCode; rate-limit ChangePassword)
- New: `internal/feature/auth/refresh.go` + handler additions (refresh-token endpoint with rotation)
- Modify: `internal/api/router.go` (wire refresh endpoint)
- Test: `internal/feature/firewall/firewall_fail2ban_test.go` (extend with bantime validation tests)
- Test: `internal/feature/auth/refresh_test.go`
- Test: `internal/feature/files/handler_test.go` (upload size + extension)

### A.1 Fail2ban time-field validation
- `BanTime`/`FindTime` regex: `^\d+[smhd]?$`. Reject `; rm -rf /` and similar. Tests cover malicious shapes.

### A.2 File upload restrictions
- Size cap from settings (already there) + new MIME/extension blocklist for binary/script types in restricted dirs (`.sh`, `.exe`, `.bat`, `.php`, `.cgi` outside designated webserver roots). Default policy permissive; operator can tighten via config.

### A.3 Compose YAML validation parity
- `compose/handler.go` CreateProject / UpdateProject currently accept raw YAML; `appstore` already validates via `validateAdvancedCompose`. Apply the same gate. New config flag `compose.validate_yaml: true` (default true) so the strict-mode landing doesn't break existing operator scripts that use `:/var/run/docker.sock` deliberately — operator can flip off for sysadmin-trusted environments.

### A.4 JWT secret minimum + auto-rotation note
- Raise minimum from 16 to 32 chars. Document in install.sh + config.example.yaml. Auto-generated secret already 64-hex (handled).

### A.5 Trusted proxies for X-Forwarded-For
- New config: `server.trusted_proxies: ["127.0.0.1", "10.0.0.0/8"]`. Echo's `IPExtractor` to honor X-Forwarded-For only from those sources. Otherwise `c.RealIP()` returns socket addr — login rate-limiting becomes spoof-resistant.

### A.6 Refresh token endpoint
- POST `/api/v1/auth/refresh` — short-lived access (1h) + opaque refresh stored per-user. Refresh rotates on each use. Revocation by deleting refresh row.

### A.7 ChangePassword rate-limit
- Same per-IP limiter as login (5/60s).

### A.8 Auth length bounds
- Username ≤64, TOTPCode regex `^\d{6}$`, password length 8..256.

---

## Area D — Cluster split-brain fencing

**Files:**
- Modify: `internal/cluster/raft.go` (BarrierUntilLeader helper; expose ConsistentRead helper that proves leadership before responding)
- Modify: `internal/cluster/manager.go` (re-check IsLeader after each multi-step Apply; check Apply error in `onNodeStatusChange` and ClusterUpdate-post hooks)
- Modify: `internal/feature/cluster/handler.go` (Disband: refuse local fallback when Apply timed out due to lost majority; ClusterUpdate: re-check leader before TransferLeadership)
- Modify: `internal/cluster/grpc_server.go` (rate-limit PromoteOnHeartbeatIfPending per peer)
- Modify: `internal/cluster/manager.go` GetNodes/GetStatus — proxy reads to leader by default in cluster mode (operator opt-out for "stale-OK")
- New: `docs/specs/cluster-partition-runbook.md` (operator playbook for split-brain detection + recovery)

### D.1 Barrier-before-write helper
- Add `RaftNode.Barrier(timeout)` wrapping `raft.Barrier()`. Call it before sensitive Apply chains (HandleJoin → AddNonvoter → CmdAddNode) so a lost-leader scenario fails fast.

### D.2 Leader-confirmed reads in critical paths
- `GetStatus`/`GetNodes` on a follower: proxy to leader; on leader: run `raft.VerifyLeader()` first. Falls back to local with explicit `stale=true` flag in response when leader unreachable. UI can show "stale data" badge.

### D.3 Discarded Apply errors
- 5 callsites in manager.go discard Apply errors (`_ = m.raft.Apply(...)`). Convert to logged failures with `slog.Error(..., "stale_leader_suspect", true)`.

### D.4 Disband fallback gate
- handler.go:779 fires `performDisband` locally even when Apply timed out. Add explicit check: if Apply error is `raft.ErrLeadershipLost` or `raft.ErrNotLeader`, refuse the fallback and require manual confirmation via a `force=true` query param.

### D.5 Promote rate-limit
- Per-peer (NodeId) rate-limiter on `PromoteOnHeartbeatIfPending` — a malicious or buggy peer flooding heartbeats can't churn Raft config.

### D.6 ProxyRequest replay defense
- Today the cluster-proxy secret has no nonce. Add a per-request nonce + timestamp ≤30s skew, signed alongside the secret. Replay-able requests rejected.

### D.7 Partition runbook
- New `docs/specs/cluster-partition-runbook.md` covering: detection (status + node count discrepancy), majority-side procedure, minority-side procedure, recovery checklist.

---

## Self-Review

| Spec item | Plan task | Status |
|---|---|---|
| DB no-tx → atomic | B.1 | ✓ |
| DB no schema_version | B.1 | ✓ |
| WAL not checkpointed before .bak | B.2 | ✓ |
| Watchdog binary-only rollback | B.3 | ✓ |
| audit_logs / alert_history / metrics_history retention | B.4 | ✓ |
| Dead sessions table | B.5 | ✓ |
| Parser test gaps (10) + ParseExpectedSHA256 | C.2 | ✓ |
| Fail2ban BanTime/FindTime unvalidated | A.1 | ✓ |
| File upload no extension blocklist | A.2 | ✓ |
| compose plain CreateProject not validated | A.3 | ✓ |
| JWT secret 16-char min | A.4 | ✓ |
| X-Forwarded-For spoof | A.5 | ✓ |
| No refresh endpoint | A.6 | ✓ |
| ChangePassword no rate-limit | A.7 | ✓ |
| Username/TOTPCode unbounded | A.8 | ✓ |
| Discarded Apply errors | D.3 | ✓ |
| Disband fallback split-brain | D.4 | ✓ |
| ClusterUpdate TransferLeadership no recheck | D.3 | ✓ |
| Promote unrate-limited | D.5 | ✓ |
| Stale FSM read | D.2 | ✓ |
| ProxyRequest replay-able | D.6 | ✓ |

Items deferred (documented out of scope at top of plan): repo-wide validation lib, multi-process Raft fuzz harness, SQLite replacement.

---

## Commit plan

1. `area-b: db migration safety + retention` — B.1–B.6 in one commit (small, contained)
2. `area-c-1: parser tests — firewall + disk` — C.2 items 1–8
3. `area-c-2: parser tests — wg + cron + checksums` — C.2 items 9–11
4. `area-a-1: input validation + auth bounds` — A.1, A.2, A.3, A.7, A.8
5. `area-a-2: jwt secret + trusted proxies + refresh` — A.4, A.5, A.6
6. `area-d-1: leader fencing + barrier + discarded errors` — D.1, D.3, D.5
7. `area-d-2: leader-confirmed reads + disband fence + replay defense` — D.2, D.4, D.6, D.7
