# Theme D Phase 2.1 — Healthcheck Composer Convenience Polish (Design)

> **Status:** approved (2026-05-06). Builds on Theme D Phase 2 (commits
> `874e772..d3a14ad`, shipped same day). Driving feedback from operator:
> *"초심자도 편하게 잘 쓸수있어야해요"* — beginners must be able to use this
> comfortably. The polish round targets adoption, not feature breadth.

## Goal

Five additions on top of the Phase 2 healthcheck composer that turn it
from "operator-must-know-yaml" to "operator-just-clicks-a-preset" while
preserving all five Phase 2 stability guarantees.

## Why these five

| # | Item | Beginner pain it removes |
|---|---|---|
| 1 | Preset library | "What test command should I write for postgres?" |
| 2 | Test now button | "Will this command actually work in the container?" |
| 3 | Remove healthcheck | "I added a wrong one — how do I undo?" |
| 4 | Services-row indicator | "Does my stack already have healthchecks?" |
| 5 | Backup retention | (operator hygiene — backup files no longer pile up) |

## Non-goals

- Bulk multi-service apply (rare in homeserver workloads).
- Auto-detection of port from `services.X.ports` for the preset URL — `PORT` placeholder is intentional.
- Showing recent healthcheck output history (Phase 3 / observability territory).
- Customizing default interval/timeout per preset (presets only set the test command + type).

## Architecture

```
┌─ HealthcheckComposerDialog ──────────────────────────┐
│  프리셋: [Custom ▼]   ← (1) preset dropdown         │
│  Test 명령어: ○ CMD-SHELL ⊙ CMD ○ NONE              │
│  [test value input]                                  │
│  [4 duration/retries inputs]                         │
│  [지금 테스트]  ← (2) inline result                  │
│  ✓ exit 0 (15ms): pong                               │
│                                                      │
│  ┌── footer ──────────────────────────────────────┐ │
│  │ [Healthcheck 제거] (3)        [취소] [적용]    │ │
│  └────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘

DockerStacks services row:
  ❤️ green = healthcheck present     ← (4)
  🤍 grey  = none

Backend handler chain (write side):
  ApplyHealthcheck / RemoveHealthcheck → backup → retention prune (5)
```

## (1) Preset library

Five hard-coded presets, frontend-only (no backend coupling). Selecting
a preset overwrites `test_type` + `test_value`; durations/retries fall
back to current defaults.

| Label | TestType | TestValue |
|---|---|---|
| HTTP GET /health | CMD-SHELL | `curl -f http://localhost:PORT/health \|\| exit 1` |
| PostgreSQL (pg_isready) | CMD | `pg_isready\|-U\|postgres` |
| Redis (PING) | CMD-SHELL | `redis-cli ping \| grep PONG` |
| MySQL (ping) | CMD-SHELL | `mysqladmin ping -h localhost \|\| exit 1` |
| Custom | (no change) | (no change) |

`PORT` literal is intentional. Auto-resolving from `services.X.ports` adds
edge cases (multiple ports / declared-but-unused / mapping translation)
without commensurate beginner value. The form input lets them replace
it in two seconds.

A preset selector lives in the dialog body, ABOVE the test-type radio,
because operators decide pattern first, command shape second.

## (2) "Test now" button

A button in the dialog footer (next to 적용). Sends the current spec to
a new endpoint that runs the command inside the live container and
returns exit code, stdout, stderr, and duration.

### Endpoint

`POST /api/v1/docker/compose/:project/healthcheck/:service/test`

Body: `HealthcheckSpec` (JSON; same shape as PUT body, no `replace` /
`base_yaml_sha256`).

Response: `{ exit_code: int, stdout: string, stderr: string, duration_ms: int }`

### Backend behavior

- Validate the spec (`HealthcheckSpec.validate`). NONE returns 400 — testing NONE has no semantic.
- Look up the running container ID for `services.<service>` of the project. If the service is not running, return 503 with a clear message.
- Execute via the existing docker SDK `ContainerExecCreate` + `ContainerExecAttach` (no shell-out). For CMD-SHELL: `["sh", "-c", testValue]`. For CMD: `[testValue.split("|")...]`.
- Apply a 30-second timeout matching cosign's verify timeout — long-hanging healthchecks are themselves bugs.
- Sanitize stdout/stderr through `response.SanitizeOutput`.
- The container is unmodified. Read-only operation.

### Frontend behavior

Result renders inline below the Test now button:
- ✓ green: `exit 0 (15ms): <first line of stdout>`
- ✗ red: `exit 1: <first line of stderr or "no output">`
- ⏳ during run

Button is disabled when test_type=NONE (testing has no semantic) or when
test_value is empty.

## (3) "Healthcheck 제거" button

Visible in dialog footer (left side) ONLY when `hasExisting === true`.
Confirmation prompt before sending DELETE. Uses native `confirm()` (matches
existing pattern in ForkList).

### Endpoint

`DELETE /api/v1/docker/compose/:project/healthcheck/:service`

Body: `{ base_yaml_sha256: string }` (concurrent-edit precondition same as PUT).

Response: `{ yaml: string, backup_path: string }`.

### Backend implementation

New pure function `RemoveHealthcheck(yamlContent, service)` in
`internal/feature/compose/healthcheck.go`. Removes the healthcheck key
+ value from the service's MappingNode `Content` slice (key/value pair).
Returns `ErrServiceNotFound` if service missing; returns `(yaml, nil)`
unchanged if no healthcheck present (idempotent).

The handler reuses ALL five stability guarantees from PUT: validate
sha256 precondition → call `RemoveHealthcheck` → pre-flight re-parse →
backup before write → atomic tmp+rename. Same code paths, same disk
discipline.

## (4) Services-row visual indicator

The `HeartPulse` icon already on each service row gains a color
indicating healthcheck presence:

- `text-[#00c471]` (existing brand green) — healthcheck present in YAML
- `text-muted-foreground` — no healthcheck

### Backend

Extend `docker.ComposeService` (the runtime status struct returned by
`GetProjectServices`) with `HasHealthcheck bool`. Populate in the same
function that already parses compose YAML for service listing — single
extra `ParseHealthcheck` call per service, negligible cost (services
typically 1-5 per stack).

### Frontend

`web/src/types/api.ts`'s `ComposeService` gains `has_healthcheck?:
boolean`. The icon className branches on it.

## (5) Backup retention

After a successful write (PUT or DELETE handler), prune older
`<dir>/<file>.bak.healthcheck.*` files keeping only the N most recent
(by mtime). Default `N = 5`.

### Implementation

```go
func pruneHealthcheckBackups(dir, prefix string, keep int) {
    entries, _ := filepath.Glob(filepath.Join(dir, prefix+".bak.healthcheck.*"))
    if len(entries) <= keep {
        return
    }
    sort.Slice(entries, func(i, j int) bool {
        infoI, _ := os.Stat(entries[i])
        infoJ, _ := os.Stat(entries[j])
        return infoI.ModTime().After(infoJ.ModTime())
    })
    for _, p := range entries[keep:] {
        _ = os.Remove(p)
    }
}
```

Best-effort: prune errors are logged but never fail the request. The
backup the user just created is the FIRST entry sorted by mtime, so it's
always preserved.

## Stability commitments (preserved + added)

- **All Phase 2 guarantees still hold** — backup, re-parse, sha256, atomic write, no auto-deploy.
- **Test now is read-only** — never writes to disk, never modifies the container.
- **Remove is symmetric to Apply** — same five guarantees, same disk discipline.
- **Retention preserves the new backup** — operator's most recent revert path is always intact. Worst case: oldest .bak files lost, never the latest 5.

## Edge cases

| Scenario | Behavior |
|---|---|
| Preset selected on existing healthcheck | Overwrites test_type/test_value but leaves durations/retries from existing. Operator can still adjust. |
| Test now while service stopped | 503 + "service not running — start it first to test" |
| Test now with NONE | Button disabled; tooltip "NONE has no command to test" |
| DELETE on service without healthcheck | Idempotent — returns 200 + unchanged YAML |
| DELETE with bad sha256 | 409 — same as PUT precondition |
| Test now command exits non-zero (legitimate failure) | Returns 200 with `exit_code: 1` (not an HTTP error). UI surfaces as red ✗. |
| Backup prune fails (e.g. permission) | Logged warn, request still 200 — non-fatal |
| Service has healthcheck but YAML has comments inside the block | Round-trip preservation already covers this (Phase 2 test) |

## Cluster

Per-node like the rest of compose. `?node=` proxying via existing
ClusterProxyMiddleware. No new infra.

## Testing

**Required:**
- `RemoveHealthcheck` table tests (present → removed; absent → no-op idempotent; service missing → error).
- DELETE handler tests for sha256 mismatch + non-existent service.
- Test handler tests for stopped service + NONE type rejection.
- Retention helper unit test (creates 7 .bak files with staggered mtime, asserts oldest 2 deleted, newest 5 kept).
- Round-trip preservation test for `RemoveHealthcheck` (comments survive removal of the healthcheck block).

**Skipped:**
- Frontend Playwright drive — covered by manual smoke (Task 9 of plan).

## Implementation footprint

Estimated 8 tasks, ~500-700 LOC:

1. `RemoveHealthcheck` pure function + tests (TDD).
2. Backup retention helper + unit test.
3. DELETE handler + handler test (sha256 + concurrent-edit + retention call).
4. Test-now handler (POST /test) + handler test.
5. `ComposeService.has_healthcheck` field + populate in `GetProjectServices`.
6. Frontend types + 2 new API methods (`removeHealthcheck`, `testHealthcheck`).
7. Dialog rework: preset dropdown + Test now button + Remove button.
8. DockerStacks: HeartPulse color branching on `has_healthcheck`.
9. Manual smoke test (build, deploy, all five paths verified).
