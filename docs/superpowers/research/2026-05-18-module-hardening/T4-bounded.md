# T4 — Bounded but high-trust

**Reviewed at:** v0.13.15 commit `f5d38a4` · 2026-05-18
**Scope:** `disk` · `alert` · `cron` · `portmap`

---

## Findings count

| Module | P0 | P1 | P2 | P3 | Total |
|---|---|---|---|---|---|
| disk | 0 | 3 | 5 | 5 | 13 |
| alert | 0 | 3 | 4 | 4 | 11 |
| cron | 0 | 3 | 3 | 3 | 9 |
| portmap | **2** | 2 | 3 | 2 | 9 |
| **Total** | **2** | **11** | **15** | **14** | **42** |

**Running total (T1+T2+T3+T4): 18 P0, 62 P1, 77 P2, 88 P3 = 245 findings**

---

## P0 — must fix

### P0-14. `portmap` aggregator drops UDP Docker bindings
**File:** `portmap/aggregator.go:40`

`PortBinding` is keyed with hardcoded `Proto: "tcp"`. A container publishing `53/udp` is either silently dropped OR — if some unrelated tcp service listens on 53 — emits a row that falsely shows the UDP container as a TCP DNAT.

**Fix sketch:** carry `Proto` on `PortBinding` from `collectDockerBindings` (Docker API exposes `p.Type`), key DNAT pass on `(port, proto)`.

### P0-15. `portmap` aggregator same-port multi-container collision
**File:** `portmap/aggregator.go:45-49`

Last-write-wins when two containers publish the same host port (legitimate during blue/green deploys, accidental during misconfig). Portmap exists to surface exactly this kind of conflict and is hiding it.

**Fix sketch:** keep `[]ContainerInfo` per port, OR emit one row per `(host_port, container_id)` and let UI render the conflict.

---

## Per-module sections

### disk

**What it does:** Pure-read disk inventory — lsblk, SMART, df, LVM, MD RAID, swap, partitions, /proc/diskstats.

**Findings:**

- **P1 — Raw stderr in client responses (SanitizeOutput gap)** across ~30 sites in `disk_filesystems.go`, `disk_lvm.go`, `disk_raid.go`, `disk_swap.go`, `disk_partitions.go`, `disk_blocks.go`. Same pattern as T2 Pattern G.
- **P1 — Polymorphic lsblk size silently zeros on legacy util-linux** (`disk_blocks.go:170-177`). JSON decoder handles only `float64`/`json.Number`; util-linux <2.37 sends `"size": "8589934592"` as a string → falls through to `Size=0`. Test at `disk_blocks_test.go:46-48` *documents* the bug as "confirms current behavior" but never asserts the right value. Add `case string:` branch with `ParseInt`, or use `json.Decoder` + `UseNumber()`.
- **P1 — Context not propagated to subprocesses.** All ~50 `Cmd.Run` calls use background ctx + 5-min wall-clock timeout. `GetDiskUsage` over `/` doesn't kill `du` when client disconnects. (Pattern F again.)
- **P2 — `ListPartitions` raw-JSON passthrough** (`disk_partitions.go:32-40`) — validates then writes back raw lsblk output. If parser semantics change, endpoint diverges.
- **P2 — `parseAllRAIDArrays` serial mdadm fan-out** (`disk_raid.go:49-69`). N×fork on a 4-array host; parallelize with errgroup.
- **P2 — `parseSwapEntries` keeps the swapon header line as a fake entry** (test documents the bug).
- **P2 — `getPVDeviceForVG` returns first PV only** (`disk_filesystems.go:351-369`). Striped/concatenated VGs lose other PVs as expand candidates.
- **P2 — `DeletePartition` does not verify partition is unmounted** (`disk_partitions.go:93`). parted rm on mounted partition corrupts userspace until reboot.
- **P3** ×5 — `inDeviceSection` flag awkward in mdstat parser, `getParentDisk` nvme heuristic, duplicate `formatBytesGo`, dead json.Number branch (until decoder switches), `maxSwapSizeMB` unit comment.
- **Optimization candidates** — **SMART trending** (currently snapshot only), background daily SMART sweep, parallel mdadm/lvm enumeration, LVM cache, I/O delta endpoint, `GetDiskUsage` entry cap.
- **Test gaps (9)** — lsblk string-size assertion, parseDfOutput, parsePVsJSON/parseVGsJSON/parseLVsJSON, parseDuOutput, getParentDisk, getMdadmDetail, parseSwapEntries header filter, computeSmartStatus boundary, CheckExpandable end-to-end with MockCommander.

---

### alert

**What it does:** CPU/mem/disk threshold rules + container-event rules fan out to Discord/Telegram with cooldown gating + per-node scope + masked-secret listing + row-cap/age retention.

**Findings:**

- **P1 — `manager.go:55-69` no `recover()` on the ticker goroutine.** Panic kills the alert system silently for process lifetime. (Extends T1 Pattern B.)
- **P1 — `container_rules.go:25-43` regex compiled per event per rule.** Busy docker host = `regexp.Compile` allocates and parses on every event. Cache compiled regex keyed by rule.id + pattern.
- **P1 — `handler.go:271, 282` raw upstream error returned to client.** `err.Error()` from `channels.SendDiscord`/`SendTelegram` may include API body fragments. Sanitize.
- **P2 — `channels/telegram.go:42` no host validation.** Unlike Discord which has `isDiscordWebhook`, the botToken interpolates into `https://api.telegram.org/bot%s/sendMessage`. An admin setting `botToken = "x@169.254.169.254/path?"` hits cloud-metadata. Admin is already privileged so impact low, but the same SSRF bar Discord meets isn't met.
- **P2 — `channels/{discord,telegram}.go` no ctx on Post.** Fire accepts ctx but discards it (`manager.go:176`). In-flight webhooks block up to 10s past `Stop()`.
- **P2 — `manager.go:237` `alert_history.node_id` stamped as empty string.** Defeats per-node history filterability the schema (mig 12) anticipates. Plumb `identity.LocalNodeID()`.
- **P2 — Alert rules NOT FSM-replicated** (`raft_fsm.go` CmdAddNode…CmdDisband only; alert_rules in local SQLite, migration 11). The review premise expected FSM-replicated rules. Operator creating a rule on node A doesn't see it on node B's evaluator. Either document "rules are per-node" or wire alert_rules through the FSM with new commands.
- **P3** ×4 — channel-type whitelist hardcoded in 3 places, empty-string vs omit ambiguity in CreateRule, total-count `Scan` error swallowed, `LIMIT -1 OFFSET ?` SQLite-ism not commented.
- **Test gaps (6)** — no `SendDiscord`/`SendTelegram` HTTP round-trip tests with httptest.Server, no `pruneAlertHistory` test (the retention contract!), no `Manager.Fire` cooldown/history test, no `evaluate` metrics-error skip test, no `TestChannel` handler test, no node_id-stamping regression test.

---

### cron

**What it does:** CRUD over root's user crontab via `crontab -l` / `crontab -` (stdin). 5-field + `@-keyword` schedule parsing. In-process mutex serializes RMW.

**Findings:**

- **P1 — Raw stderr leaks into client responses** at 7 sites (`handler.go:61, 105, 118, 158, 183, 200, 216`). Pattern G again.
- **P1 — No request-context propagation** (`handler.go:223, 232`). `crontab` hang under disk pressure / NFS spool can't be cancelled.
- **P1 — In-process-only mutex** (`handler.go:20`). Doesn't protect against concurrent edits from another sfpanel instance OR human running `crontab -e` on the box. Document limitation OR add post-write read-back consistency check.
- **P2 — No command allowlist.** Cron entries run as root. Operator already has root so no privilege escalation, but stolen JWT/CSRF → persistent root code execution. Worth a P2 hardening note.
- **P2 — Fragile error classification by substring** (`handler.go:58, 102`). `strings.Contains(err.Error(), "no crontab for")` misclassifies on non-English locales. Use `LANG=C` env or exit status check.
- **P2 — `extractScheduleAndCommand` allocates 5× per line** (`:365-367`). Cron is CRUD-only so fine, but noted.
- **P3** ×3 — `isCronField` regex compiled per call, `UpdateJob` disable-comment overwrites user comments, duplicate `len(fields)>=6` loop.
- **Optimization candidates** — **Run history / failure detection** (the major deepening candidate noted earlier — still absent). Live next-N-run preview using `robfig/cron/v3`. Pre-compiled regex. Read-after-write verify.
- **Test gaps (6)** — no `isValidSchedule` test (the public mutation validator!), no handler-level CreateJob/UpdateJob/DeleteJob round-trip with MockCommander, no newline-rejection test, no "no crontab installed" path test (substring classifier), no concurrent-edit mutex test, `isCronField` cron-special-char realistic context (`1L`, `15W`, `2#1`).
- **Files module owns `/etc/cron.d`** — cron module only touches root's user crontab. 2026-04-19 R3 N-01 P0 is files module's surface, not cron's. CLOSED here.

---

### portmap

**What it does:** Aggregates `ss -tlnp/-ulnp` listeners + UFW rules + Docker DNAT into unified per-port view.

**Findings:**

- **P0-14 — UDP Docker bindings dropped** (see top).
- **P0-15 — Same-port multi-container collision hidden** (see top).
- **P1 — Context not propagated to subprocesses** (`handler.go:46, 53, 67`). Client cancel still spawns/keeps subprocesses up to 5s each.
- **P1 — `ProcessInfo.Name` not sanitized** (`handler.go:97`). Process names from `ss` argv0; hostile local user can run binary with ANSI/control bytes and have it rendered in panel UI. Apply `response.SanitizeOutput`.
- **P2 — Serial ss tcp/udp calls** (`handler.go:46, 53`). Parallelizable, OR use `ss -tulnpH` for combined.
- **P2 — Stale comment on docker-proxy ordering** (`aggregator.go:18-19`). Promises invariant code doesn't enforce.
- **P2 — UFW parser duplicated** between portmap and firewall. Share or document why.
- **P3** ×2 — no row cap, `parseUFWForPortMap` untested.
- **Test gaps (6)** — no `parseUFWForPortMap` direct test, no UDP Docker binding test (P0-14), no same-port collision test (P0-15), no partial-failure matrix test, no ANSI-name sanitization test, no `splitPortProto` direct test.

---

## Additions to cross-tier patterns

### Pattern Q — Manager-style background goroutines without `recover()`

Already part of Pattern B (T1) — alert.Manager extends the surface. Same `safe.Go` helper covers it.

### Pattern R — Subprocess via `Commander` without request context

Universal across T1–T4. This is just Pattern F restated — every subprocess call site needs `ctx` plumbing once Commander supports it.

### Pattern S — Replicated-vs-per-node ambiguity

Affects: `alert` (rules expected FSM but actually per-node). Possibly affects: settings (clarified during T3 — confirmed per-node).
**Single fix:** for each "shared state" table, document in CLAUDE.md whether it is FSM-replicated, per-node, or per-cluster-via-leader-only. Operators currently can't tell which is which.

---

## Updated remediation grouping (T1+T2+T3+T4)

Adding two more plans:

24. **`fix-portmap-aggregator`** (NEW T4) — P0-14 UDP + P0-15 multi-container collision + Proto plumbing
25. **`fix-alert-replication-and-recover`** (NEW T4) — recover() on manager, decide FSM vs per-node and document, regex cache, node_id stamping, secret sanitization on send errors
26. **`fix-disk-perf-and-trending`** (NEW T4) — parallel mdadm, lsblk string-size, ListPartitions raw-passthrough, opt-in SMART trending (depends on monitor rollup work)
27. **`fix-cron-validation-and-history`** (NEW T4) — handler tests, isValidSchedule test, optional run-history opt-in (the major deepening item)

Full plan list now ~27. Sequencing intent unchanged: P0 first, then mechanical sweeps, then domain correctness, then refactors, then cleanup.

---

## Exit criteria for T4

- [x] All 4 modules reviewed
- [x] 2 P0 findings flagged
- [x] Cross-tier patterns extended Q-S
- [x] Remediation list expanded to ~27 plans
- [ ] Continue to T5
