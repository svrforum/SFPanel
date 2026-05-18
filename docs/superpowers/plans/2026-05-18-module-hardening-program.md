# Module Hardening & Optimization Program — 2026-05-18

> **Companion to 2026-04-19 code-review** (`docs/superpowers/research/2026-04-19-code-review/`) — that pass targeted v0.9.0 and was security-heavy. This program targets **v0.13.15** (~6 minor releases later) and gives equal weight to **stability** (race conditions, leaks, panic paths, error handling) and **optimization** (N+1 queries, unbounded scans, redundant parsing, missing indexes, command timeouts).
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to drive the per-tier reviews and remediation plans. The umbrella tasks here are *programs of work*, not TDD micro-steps — each tier produces its own research artefact and (if needed) its own remediation plan with bite-sized TDD steps.

**Goal:** Systematic stability + optimization review of all 22 feature modules and 5 supporting platform packages, with prioritized remediation landed as scoped commits.

**Architecture:** Read-only research → synthesis → remediation plans → execute. Each tier is a complete cycle; tiers are sequenced by blast radius (cross-cutting infra first) so foundational fixes ship before invasive ones.

**Out of scope (explicit):**
- New module surface or feature categories (per user direction — deepen, don't expand).
- External integrations not already present (Prometheus/OIDC/Trivy/Terraform).
- Rewrites of working code. Surgical patches only; refactors only when a defect can't be fixed otherwise.
- Re-doing the v0.9.0 security review — only re-verify items the 2026-04-19 plan listed as "open" or that touch code since rewritten.

---

## Tiering (22 modules + 5 platform packages)

Ordering is by **(criticality of dependency × probability of latent defect)**. T1 modules are upstream of everything; T5 are leaves.

| Tier | Modules / packages | Why this tier |
|---|---|---|
| **T1 — Cross-cutting infra** | `auth` · `cluster` · `audit` · `monitor` · `middleware` · `common/exec` · `db` | Every other module sits on top of these. A race or panic here propagates everywhere. Touched heavily in 0.13.x. |
| **T2 — Large surface, heavy I/O** | `compose` · `docker` · `packages` · `network` · `firewall` | Largest LOC (compose 30KB, packages 40KB, network 38KB). Streaming + subprocess + parsing density highest. |
| **T3 — Operations workhorses** | `system` · `settings` · `services` · `files` · `logs` | Daily-use admin surface, recent settings split + systemd fallback work (0.13.11) deserves a fresh pass. |
| **T4 — Bounded but high-trust** | `disk` · `alert` · `cron` · `portmap` | Smaller modules, but `disk`/`portmap` parse system state and `alert` fires real notifications. |
| **T5 — Specialized I/O** | `process` · `terminal` · `appstore` · `websocket` (transport) | PTY, supervised installs, WS lifecycle. Less change since 0.9.0 review — re-verify findings still apply. |

Total: 5 tiers × ~5 modules each ≈ 22 modules + 5 platform packages.

---

## Review checklist (applied to every module)

Each module review answers **every** item in this list. Items left unanswered are themselves findings ("unverified").

### Stability

1. **Goroutine leaks** — every `go func()` has a path that exits when its context cancels.
2. **Context propagation** — every external call (subprocess, HTTP, DB query inside a request) carries `ctx.Request().Context()` or a derived context with explicit timeout.
3. **Subprocess hygiene** — `os/exec` callers (the 6 documented exceptions) attach `Cmd.Process.Kill()` on context done; `Commander.RunWithTimeout` always has a non-zero timeout.
4. **File-handle / resource leaks** — every `Open`/`Create`/`bufio.NewScanner` paired with `defer Close()`; SSE/WS writers `defer flush + close`.
5. **Panic paths** — `recover()` only at goroutine entry boundaries; no `panic` in request paths.
6. **Error wrapping** — `fmt.Errorf("...: %w", err)` not `errors.New(err.Error())`; no swallowed errors (no `_ = ...` on error returns unless explicitly justified).
7. **Race conditions** — shared maps/slices touched from a goroutine must hold a `sync.Mutex` or use channels; `sync.Map` for read-mostly. `go test -race` clean.
8. **Cluster awareness** — handlers degrade gracefully when remote tools are absent (return empty, not 500). `?node=` reachable streaming paths use `-stream` suffix or are in the proxy allowlist.
9. **Migration safety** — any DDL referenced runs at v23-level idempotency; no `ALTER TABLE` on populated rows without a backfill plan.

### Optimization

10. **N+1 queries** — list endpoints don't loop calling per-row queries.
11. **Unbounded result sets** — list endpoints have `LIMIT/OFFSET` or pagination; full-table reads are explicit and bounded.
12. **Missing indexes** — columns used in `WHERE` / `ORDER BY` / `JOIN` have an index (check `migrations.go`).
13. **Redundant parsing** — same command output reparsed across handlers; consider cache or shared parser.
14. **Subprocess overhead** — when a single invocation returns multiple datapoints, callers don't shell out N times.
15. **Hot-path allocations** — large `json.Marshal` on huge payloads, slice grows in tight loops; replace with streaming or `make` with capacity hint.
16. **Stream backpressure** — SSE/WS writers don't grow an unbounded internal buffer when the client is slow.
17. **Retention / rollup** — long-lived tables (`audit_logs`, `alert_history`, `container_metrics_history`, `metrics_history`, future `cron_runs` / `smart_history`) have a pruner; raw rows aren't kept past usefulness.

### Security regressions (lightweight pass, not a re-do of 2026-04-19)

18. `SanitizeOutput()` wraps every command output reaching the user.
19. No path traversal on user-supplied paths (`filepath.Clean` + `strings.HasPrefix(absRoot)`).
20. No command injection — every variable in `Cmd.Run(...)` is a separate arg, never `sh -c "cmd " + userInput`.
21. SQL — parameter binding only, no string concat.

### Cluster + concurrency angles

22. Audit row `node_id` set correctly (added 0.13.15) for any write paths.
23. FSM-write endpoints have follower auto-forward coverage (0.13.13–0.13.14) — list any missed ones.
24. `ClusterProxyMiddleware` doesn't double-wrap or strip headers needed downstream.

### Test coverage gap

25. Each module's complex parser / validator / cluster-aware branch has a test; missing tests are listed as deliverables.

---

## Deliverable per tier

For each tier (`Tn`), produce **one research file** under
`docs/superpowers/research/2026-05-18-module-hardening/Tn-<area>.md` with this structure:

```markdown
# Tn — <module names>

**Reviewed at:** v0.13.15 commit f5d38a4 (date)

## Per-module findings

### <module>
- **What it does (1 line)**
- **Checklist results** — table of 25 items: PASS / FAIL / N/A + brief note
- **Findings** — P0 / P1 / P2 / P3 with file:line + 1-paragraph description + suggested fix sketch
- **Optimization candidates** — separate sub-list (perf items rarely fit P0/P1 severity but matter)
- **Test gaps** — explicit list

## Cross-module patterns
- Patterns observed across multiple modules in this tier (e.g. "all three use the same firewall parser; consolidate")

## Recommended remediation grouping
- Which findings can land as one commit. Ordered by ROI.
```

After **all 5 tier files** exist, write
`docs/superpowers/research/2026-05-18-module-hardening/R-final.md`
to synthesize:
- Total finding count by severity
- Cross-tier patterns (single fixes that touch many modules)
- Recommended remediation plan files (1 per fix theme)

Remediation lives in `docs/superpowers/plans/2026-05-18-*-fix-<theme>.md` files **created at remediation time**, not pre-baked here. Those follow the standard TDD micro-step format from `writing-plans`.

---

## Severity definition

- **P0** — exploitable security, data corruption, panic in a normal flow, deadlock or goroutine leak that grows over time.
- **P1** — correctness bug that produces wrong output, missing/wrong cluster behaviour, resource leak under load, missing context cancellation.
- **P2** — perf bottleneck visible to user (>100ms unnecessary latency, N+1 visible at scale), missing retention, missing index that hurts at scale.
- **P3** — code smell, minor inefficiency, test gap not tied to a defect.

---

## Sequencing

```
Phase 0  (this doc + tasks)                  — ✅ done when this file lands
Phase 1  T1 review → T1.md                   — cross-cutting infra
Phase 2  T2 review → T2.md
Phase 3  T3 review → T3.md
Phase 4  T4 review → T4.md
Phase 5  T5 review → T5.md
Phase 6  R-final synthesis
Phase 7+ Per-theme remediation plans (created from R-final) + execution
```

**Checkpoint after each phase:** user reviews findings file, approves before next tier starts. Remediation happens only after Phase 6 — no in-flight patches during research, so we don't change ground while still measuring it.

**Exception:** if a tier surfaces a **P0** that's actively dangerous (running in production right now), flag it inline and land a one-commit fix before the next tier. Document the exception in the tier's findings file.

---

## Execution mode

**Recommended:** subagent-driven for the research phases.
- Per tier, dispatch one `Explore` (read-only) subagent per module with a self-contained brief: "Run the 25-item checklist on module X. Return Markdown findings using the deliverable schema above. Do not modify code."
- Main thread synthesizes the per-module returns into the tier's `Tn-*.md`.
- For remediation phases, switch to `feature-dev:code-architect` + main-thread coding (or per-theme subagents) following the standard TDD plan format.

**Why subagents for research:** 22 modules of read-only analysis dwarfs the main context budget. Each subagent reads ~5–20 files and returns a structured 1–2 KB summary. Main context stays clear for synthesis.

**Why main thread for remediation:** edits need cross-module awareness and the user wants a checkpoint after each commit.

---

## Out-of-band items already known

Carry forward from earlier reviews / audits — do not re-research, just verify status during the relevant tier:

| Item | First raised | Status to verify in |
|---|---|---|
| `ClusterProxyMiddleware` `InsecureSkipVerify` (2026-04-19 P0-A) | 2026-04-19 | T1 (cluster + middleware) |
| `/etc/cron.d` path write (2026-04-19 P0 R3 N-01) | 2026-04-19 | T3 (files) |
| AppStore Advanced YAML execution (2026-04-19 P0 F-09) | 2026-04-19 | T5 (appstore) |
| 3rd-party install script hash verify (2026-04-19 P0 R1 P1-1) | 2026-04-19 | T2 (packages) |
| `marked` README XSS (2026-04-19 P0 R10 C-1) | 2026-04-19 | (frontend pass — out of scope for this program; flag if still present) |
| `image_signatures` dead schema | 0.13.0 | T1 (db) — confirm still unused, do not repurpose |

---

## Self-review

- [x] Spec coverage — checklist covers stability + optimization + security regression + cluster + concurrency + tests, as user requested.
- [x] No placeholders — all phases have explicit deliverable shapes.
- [x] Type/name consistency — tier names (T1–T5) consistent throughout; deliverable path schema consistent.
- [x] User direction honored — no new module surface proposed; remediation deferred to per-theme plans.
