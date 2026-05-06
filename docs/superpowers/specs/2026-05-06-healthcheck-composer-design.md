# Theme D Phase 2 — Healthcheck Composer (Design)

> **Status:** approved (2026-05-06). Roadmap entry:
> `docs/superpowers/roadmaps/2026-05-03-docker-management-roadmap.md` §D.
> Phase 2 is deliberately scoped to the healthcheck composer alone;
> environment variants (the other §D Phase 2 item) is shelved on
> home-server-value grounds.

## Goal

Make adding a Docker healthcheck a 30-second click instead of a 10-minute
"figure out the YAML syntax" task. Operator clicks a ❤️ icon on a
service row, fills 5 fields in a dialog, and the backend writes a
healthcheck block into the stack's `docker-compose.yml`.

## Why this matters

We just shipped `container_unhealthy` alerts (Theme F Phase 2). Without
a low-friction way to add healthchecks to community images that ship
without one (jellyfin, plex, nextcloud, vaultwarden, …), the alert
never fires. The composer connects the alert investment to actual
runtime signal.

## Non-goals

- Environment variants (dev/staging/prod overlays). Home-server adoption
  too low to justify; revisit on real demand.
- Multi-service batch composer (one service per click).
- Healthcheck preset library.
- Real-time healthcheck log viewer.

## Architecture

```
User clicks ❤️ on services table row
   │
   ▼
HealthcheckComposerDialog
   - GET current compose YAML (already cached in DockerStacks state)
   - Parse via yaml.v3 → detect existing healthcheck on this service
   - Auto-populate fields if found; show "이미 존재 — 덮어쓰기" checkbox
   │
   ▼ user edits + clicks Apply
POST /api/v1/compose/:project/healthcheck/:service
   body: { test_type, test_value, interval, timeout, retries, start_period, replace }
   │
   ▼
internal/feature/compose/healthcheck.go
   - ApplyHealthcheck(yaml, service, spec, replace) (newYAML, error)
   - Pure function. Reads via yaml.v3 Node tree, modifies services.<svc>.healthcheck,
     marshals back. Preserves comments + anchors via Node API.
   │
   ▼
Response { yaml, diff_summary }
   │
   ▼
DockerStacks: editYaml = response.yaml; switch to Editor tab;
toast "Healthcheck inserted — review and Save & Deploy"
```

The composer **never deploys directly**. It only patches the YAML and
hands it back to the editor. The operator reviews the diff (Phase 1
diff modal works as-is) and clicks Save & Deploy themselves. This
preserves the existing change-control flow.

## Components

### `internal/feature/compose/healthcheck.go` (pure function)

```go
type HealthcheckSpec struct {
    TestType    string // "CMD-SHELL" | "CMD" | "NONE"
    TestValue   string // shell command (CMD-SHELL) or pipe-separated argv (CMD); ignored for NONE
    Interval    string // duration: "30s", "1m30s"
    Timeout     string
    Retries     int
    StartPeriod string
}

// ApplyHealthcheck inserts or replaces the healthcheck block on the
// named service in the compose YAML. Returns the new YAML or an error.
//
// Rules:
//   - service must exist in services.<name> — else error.
//   - If healthcheck already present and replace=false → error.
//   - Test type NONE writes only `test: [NONE]` and ignores other fields.
//   - Durations validated against time.ParseDuration.
//   - Comments + anchors preserved via yaml.v3 Node API.
func ApplyHealthcheck(yamlContent string, service string, spec HealthcheckSpec, replace bool) (string, error)

// ParseHealthcheck reads the existing healthcheck (if any) so the UI
// can populate the dialog. Returns (zero-value, false, nil) if absent.
func ParseHealthcheck(yamlContent string, service string) (HealthcheckSpec, bool, error)
```

### `internal/feature/compose/handler.go` (1 new endpoint)

`PUT /api/v1/compose/:project/healthcheck/:service`
- Body: HealthcheckSpec + `replace bool`
- Reads `<stacksPath>/<project>/docker-compose.yml`, calls `ApplyHealthcheck`,
  writes back, returns `{ yaml }`.
- Validation errors → 400 with descriptive message.
- Service not found → 404.
- Unparseable YAML → 500 with sanitized error.

### `web/src/components/compose/HealthcheckComposerDialog.tsx`

Dialog with:
- Service name shown in title (read-only, set by parent).
- Test type radio (CMD-SHELL / CMD / NONE).
- Test value input — single line. Placeholder per type:
  - CMD-SHELL: `curl -f http://localhost:8096/health || exit 1`
  - CMD: `curl|-f|http://localhost:8096/health` (pipe separator).
  - NONE: hidden.
- Four duration / number inputs with validation.
- "이미 healthcheck 있음 — 덮어쓰기" checkbox (visible only when one exists).
- 취소 / 적용 buttons.
- On apply: call API → on success, parent receives new YAML and switches
  to Editor tab.

### `web/src/pages/docker/DockerStacks.tsx`

- New ❤️ icon on each service row in the services table (lucide
  `HeartPulse`). Same row position pattern as the existing
  start/stop/restart/logs/shell icons.
- State: `healthcheckTarget: ComposeService | null`.
- Mounting `<HealthcheckComposerDialog open={!!healthcheckTarget} ...
  onApplied={(newYaml) => { setEditYaml(newYaml); setEditorTab('compose'); ... }} />`

### `web/src/lib/api.ts` (1 new method)

```typescript
applyHealthcheck(project: string, service: string, spec: HealthcheckSpec, replace: boolean) {
    return this.request<{ yaml: string }>(
        `/compose/${encodeURIComponent(project)}/healthcheck/${encodeURIComponent(service)}`,
        { method: 'PUT', body: JSON.stringify({ ...spec, replace }) }
    )
}
```

## Defaults

- Test type: `CMD-SHELL` (most common operator-written pattern).
- interval: `30s`
- timeout: `10s`
- retries: `3`
- start_period: `30s` (most home-stack services are usable within 30s).

## Stability commitments

The composer touches the operator's running stack definition. Any
regression here breaks production. Five hard guarantees:

1. **Round-trip preservation.** yaml.v3 Node API (not the high-level
   `Marshal/Unmarshal`) is used so anchors, aliases, merge keys,
   comments, and key order survive untouched. Round-trip is asserted by
   a test that takes a representative compose, runs `ApplyHealthcheck`,
   and diffs everything except the healthcheck block — it must be
   byte-identical.

2. **Backup before write.** Before overwriting `docker-compose.yml`,
   the handler saves `docker-compose.yml.bak.healthcheck.<unix-millis>`
   in the same directory. Operator can revert with `mv` if anything
   surprises them.

3. **Pre-flight re-parse.** After `ApplyHealthcheck` builds the new
   YAML string, the handler re-parses it via the same yaml.v3 to
   confirm structural validity. If parsing fails, the original file
   is untouched and the response is 500 with a clear error — the
   composer NEVER ships a broken YAML to disk.

4. **Concurrent edit detection.** Request body includes a
   `base_yaml_sha256` field (frontend computes this from the YAML it
   loaded into the dialog). Backend rejects with 409 if the on-disk
   YAML's hash differs — operator likely edited externally; UI prompts
   them to reload.

5. **No automatic deploy.** Composer ONLY edits the YAML and returns
   it. The operator must explicitly click Save & Deploy in the editor.
   The running stack is not touched until that explicit second action.

These five guarantees combine so the worst-case failure mode is "the
operator sees an unexpected diff in the editor" — never "the running
stack stopped because a healthcheck composer wrote bad YAML to disk."

## Edge cases

| Scenario | Behavior |
|---|---|
| service has existing healthcheck, replace=false | Backend returns 409 with message; UI surfaces "이미 존재 — 덮어쓰기 체크박스 사용". |
| Test type = NONE | Writes `test: [NONE]` only. Disables image-baked healthcheck. Other fields ignored. |
| YAML uses anchors / merge keys | yaml.v3 Node API preserves these; round-trip safe for the common case. Operator sees diff before save (Phase 1 diff modal). |
| Service name missing | 404 + clear error. |
| Bad duration (e.g. `30`) | 400 + "interval must be a Go duration like 30s". Frontend pre-validates. |
| Empty test value (CMD-SHELL or CMD) | 400 + "test value required". |

## Cluster

Stacks are per-node. `?node=` proxies the new endpoint correctly via the
existing ClusterProxyMiddleware. No new infrastructure.

## Testing

**Required:**
- Table tests for `ApplyHealthcheck`:
  - Add CMD-SHELL on service with no existing block.
  - Add CMD with pipe-separated argv.
  - Add NONE.
  - Replace existing.
  - replace=false on existing → error.
  - Service missing → error.
  - **Round-trip preservation**: a real-world compose with anchors +
    comments + key ordering is fed through `ApplyHealthcheck`; the
    diff (excluding the healthcheck block itself) must be empty.
- Round-trip property test: take 5 different real compose files
  (jellyfin, postgres+redis stack, multi-service with anchors), apply
  identity transformations (replace=true with the same spec), expect
  yaml = original.
- Pre-flight re-parse test: feed `ApplyHealthcheck` something that
  would produce malformed YAML if a future bug landed; assert the
  handler returns 500 without writing.
- `base_yaml_sha256` mismatch test: handler returns 409.
- Table tests for `ParseHealthcheck`:
  - Existing CMD-SHELL → populated.
  - Existing CMD → populated.
  - Absent → (zero, false, nil).
- Handler validation tests for bad inputs.

**Skipped:**
- UI Playwright drive in Phase 13 manual smoke.

## Implementation footprint

Estimated 5-7 tasks, ~300-400 LOC:

1. `ApplyHealthcheck` + `ParseHealthcheck` + tests (TDD red→green).
2. REST handler + handler test.
3. Router wiring.
4. Frontend types + API method.
5. `HealthcheckComposerDialog` component.
6. DockerStacks integration (icon + dialog mount).
7. Manual smoke test (build + deploy + UI walkthrough).
