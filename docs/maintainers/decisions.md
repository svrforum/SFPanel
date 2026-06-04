# Decisions

Design decisions and *why*, so they don't get "fixed" by accident. _Updated 2026-06-04._

## Product scope: deepen modules, don't add new ones
Improvement work matures the existing ~22 feature modules rather than adding new scope (no RBAC/SSO/Prometheus/Trivy-style expansion). The modules stay small because nobody adds scaffolding "for later." Push back on requests that imply a second product inside the panel. (This is why a full "LLM Wiki" feature was declined in favor of *this* in-repo maintainer wiki — see the conversation that created `docs/maintainers/`.)

## App Store catalog: runtime-fetched + bundled, in the main repo
The catalog was migrated from a separate `SFPanel-appstore` repo into `main/appstore/` (v0.42.0). It's still fetched at runtime (a catalog-only commit updates every panel within the cache TTL — no release needed), but now also bundled to one `catalog.json` for a single atomic fetch. Per-app `metadata.json` stays the contributor source of truth; CI guards bundle freshness. See [gotchas.md](gotchas.md).

## App Store stays a curated catalog, not custom-install
The store installs vetted catalog apps only. Arbitrary user-supplied stacks are intentionally **not** added there — the Docker → Compose menu already covers custom stacks. Appstore "advanced install" exists but is gated behind step-up re-auth (a stolen JWT alone can't push a host-root compose).

## `Restart=always` (not `on-failure`) in the systemd unit
Several handlers (`/system/update`, `/cluster/leave`, `/cluster/disband`) call `os.Exit` after responding, so the supervisor picks up a new binary / new cluster config. `on-failure` would treat those clean exits as "done" and leave the panel offline. Any new handler that exits the process must keep this assumption.

## Cluster ports moved to 3628–3630 (from the old 9xxx block)
HTTP 3628, gRPC 3629, Raft 3630. The old 19443/9444/9445 defaults collided with common homeserver workloads (Jupyter, Plausible). Existing installs keep whatever is in `config.yaml`; only the fallback defaults changed.

## Replicated vs per-node state
Replicated via the Raft FSM (`internal/cluster/raft_fsm.go`): JWT secret, cluster config, admin account, 2FA recovery codes. Per-node (local): metrics history, audit rows, cron, docker, appstore installs. The `CommandType` iota in the FSM is **append-only** — never reorder/insert.

## Terminal sizing is tied to `visualViewport`, deliberately
The terminal page shrinks the app shell to `visualViewport.height` (`--app-h`) so the input line / keys aren't pushed under the soft keyboard. The alternative (fixed height + keyboard overlay) was considered and rejected because the cursor/prompt would sit under the keyboard. The cost of this choice is the keyboard-resize fragility documented in [gotchas.md](gotchas.md) and [known-issues.md](known-issues.md).

## Auto-reload on new deploy (added on request)
`web/src/main.tsx` reloads the tab when a new service worker takes control. This is intentional so operators don't keep seeing a stale shell after an upgrade. The trade-off: during rapid back-to-back deploys, an open tab reloads repeatedly — that's expected, not a bug.

## Commit & repo hygiene
- Commit author/committer must be `svrforum <svrforum.com@gmail.com>` (via `GIT_AUTHOR_*`/`GIT_COMMITTER_*` env, not `.git/config`).
- Commit messages never reference AI tooling (no `Co-Authored-By: Claude`, no "Generated with…").
- `**/CLAUDE.md` is intentionally gitignored — edits to it never appear in diffs; don't flag the missing commit.
