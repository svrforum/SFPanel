# Docker observability (Theme F) — design spec

> **Roadmap context.** Wave 1 of the Docker management roadmap
> (`docs/superpowers/roadmaps/2026-05-03-docker-management-roadmap.md`).
> This is the spec for theme F. Theme D follows separately. Themes B / C / E
> get their own specs after Wave 1 ships.
>
> **v1 scope locked.** Sub-features 1 + 2 + 4 from the roadmap (metrics
> history, restart timeline, Docker events → existing alert pipeline).
> Healthcheck UI column (3) and log-based alerts (5) are deferred to v1.5.

**Goal.** Give a self-hosting operator the three signals that matter when
something goes wrong with a containerised service: a 24-hour CPU/memory
trail, a precise event timeline (start / stop / die / OOM / health
transitions), and proactive alerts when a container is down, OOM-killed,
or restart-looping. No external dependencies. No Prometheus. The whole
feature lives inside the existing Go binary + SQLite store.

**Non-goal.** Prometheus / Grafana parity. Long-retention capacity
planning (max 72h). Network or disk I/O history. Cross-host aggregated
dashboards (deferred to a separate roadmap).

---

## Scope

### In v1

1. **Container metrics history** — CPU% + Memory% (+ memory bytes) at
   30 s granularity, 24 h rolling window default. Sparkline per row in
   the container list, line chart in the detail drawer with 1h / 6h /
   24h range selector.
2. **Restart / lifecycle timeline** — events log per container
   (start / stop / die / oom / kill / restart / healthy / unhealthy)
   stored with millisecond precision. Vertical timeline UI in the
   detail drawer.
3. **Docker events → existing alert manager** — three new alert rule
   types (`container_down`, `container_oom`, `container_restart_loop`)
   that plug into the existing `alert_rules` / `alert_channels`
   infrastructure.

### Explicitly NOT in v1

- Healthcheck status column on the stack list (v1.5 — events are still
  captured, only the column UI is deferred).
- Log regex matching for alerts.
- Network / Disk I/O / PIDs metrics (v1.5 if requested).
- Multi-host aggregated event feed (separate roadmap).
- Chart zoom / brush / drag-to-select (uplot defaults only).
- Prometheus / OpenMetrics export. Self-hosting operators wanting
  external observability run cAdvisor in Docker; this feature is the
  built-in alternative, not a stepping stone.
- Long retention (>72h). Storage growth + rollup complexity rules it out.

---

## Architecture

Two background goroutines per node (started from `cmd/sfpanel/main.go`
on `bgCtx`, stopped on SIGTERM):

### Collector (polling)

```
ticker every 30s:
    for each running container:
        ContainerStatsOneShot(ctx with 5s per-call timeout)
        compute CPU% (cgroups v1+v2 cache subtraction — already in client.go:calcMemUsage)
        compute Mem% + Mem bytes
        INSERT INTO container_metrics_history(...)
```

Reuses the polling shape of `monitor.StartHistoryCollector`. New file
`internal/monitor/docker_history.go`.

### Events listener (long-lived stream)

```
loop:
    open docker events stream with filters
        type=container, event in {start, stop, die, oom, kill,
        restart, health_status:healthy, health_status:unhealthy}
    for each event:
        decode, normalize event_type, extract exit_code/detail
        INSERT INTO container_events(...)
        evaluate matching alert_rules → push to existing alert manager
    on EOF/error:
        exponential backoff 1s → 5min cap, reconnect
```

New file `internal/monitor/docker_events.go`. Reuses the reconnect
pattern from `cluster/manager.go:StartLocalMetrics` (same backoff
scheme, same goroutine-leak safeguards via ctx propagation).

### Pruners

Two new tickers wired alongside existing audit / alert retention
goroutines (`cmd/sfpanel/main.go` after the existing
`middleware.StartAuditRetention` line):

- `monitor.StartDockerMetricsRetention(bgCtx, db)` — hourly tick,
  prunes `container_metrics_history` older than configured retention.
- `monitor.StartDockerEventsRetention(bgCtx, db)` — hourly tick,
  enforces 30d age cap + 5k rows-per-container cap.

### Disabled state

`docker.observability.enabled: false` in `config.yaml` skips starting
all three goroutines. New endpoints still register (so cluster proxy
routing works) but return `{"observability_disabled": true, "data": []}`.

---

## Data model

Two new tables, both per-node SQLite (NOT replicated through Raft —
matches per-node retention model already used by `audit_logs` and
`metrics_history`).

### `container_metrics_history`

```sql
CREATE TABLE container_metrics_history (
    container_id   TEXT    NOT NULL,
    container_name TEXT    NOT NULL,
    ts             INTEGER NOT NULL,  -- unix millis
    cpu_percent    REAL    NOT NULL,
    mem_percent    REAL    NOT NULL,
    mem_bytes      INTEGER NOT NULL,
    PRIMARY KEY (container_id, ts)
);
```

Container name is denormalised so a deleted container's row still
renders a name in the chart. PK gives both natural time ordering and
fast per-container range scan.

Volume estimate (30s × 24h × 20 containers): ~57,600 rows × ~80 bytes
≈ **4.6 MB / day** at peak. Daily prune brings it back to that ceiling.

### `container_events`

```sql
CREATE TABLE container_events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id   TEXT    NOT NULL,
    container_name TEXT    NOT NULL,
    ts             INTEGER NOT NULL,  -- unix millis
    event_type     TEXT    NOT NULL,  -- 8 enum values
    exit_code      INTEGER,           -- nullable
    detail         TEXT               -- nullable JSON
);

CREATE INDEX idx_container_events_container_ts
    ON container_events(container_id, ts DESC);

CREATE INDEX idx_container_events_ts
    ON container_events(ts DESC);
```

`event_type` ∈ `{start, stop, die, oom, kill, restart, healthy, unhealthy}`.
Unknown event types from the Docker daemon (e.g. future additions) are
silently dropped — no panic on schema drift.

`detail` is a JSON blob for type-specific extras: kill signal, last
healthcheck output line, etc. Kept under 1 KB per row.

Volume estimate: 10–50 events/day × 30 days × 20 containers ≈
6,000–30,000 rows ≈ **1–5 MB**.

### Migration IDs

Append three new entries to `internal/db/migrations.go` under the
`{ID, Up}` tuple slice introduced in the schema_version work:

- ID 16 — `CREATE TABLE container_metrics_history (...)`
- ID 17 — `CREATE TABLE container_events (...)`
- ID 18 — `CREATE INDEX idx_container_events_container_ts`
- ID 19 — `CREATE INDEX idx_container_events_ts`

(IDs are reserved against the current head of the migration list. The
plan task will lock exact IDs against the live tree.)

---

## API surface

Three new read endpoints, registered on the existing `authorized.GET`
group in `internal/api/router.go`. All inherit JWT auth + cluster
proxy (`?node=<id>`) for free.

### `GET /api/v1/docker/containers/:id/metrics?range=1h|6h|24h`

```jsonc
// response.data
[
  {"ts": 1714742400000, "cpu_percent": 8.4, "mem_percent": 34.3, "mem_bytes": 1471123456},
  ...
]
```

- `range` defaults to `1h`. Other accepted values: `6h`, `24h`. Invalid
  → 400 with `INVALID_RANGE`.
- Empty result is a 200 OK with `[]` (UI renders empty state, not error).

### `GET /api/v1/docker/containers/:id/events?limit=50&before=<ts>`

```jsonc
// response.data
[
  {"ts": 1714742432120, "event_type": "oom", "exit_code": 137, "detail": null},
  {"ts": 1714742435001, "event_type": "start", "exit_code": null, "detail": null},
  {"ts": 1714742480200, "event_type": "unhealthy", "exit_code": null,
   "detail": "{\"failing_streak\":3,\"output\":\"Connection refused\"}"},
  ...
]
```

- Newest-first. `limit` capped at 200. `before=<ts>` is the cursor
  (next page = `before` set to the oldest ts of the current page).

### `GET /api/v1/docker/events/recent?limit=50`

Same shape as above but across all containers on this node. Used for
the future global widget; the v1 UI does not consume this yet but the
endpoint is registered now to avoid a second migration later.

### Modified `GET /api/v1/docker/containers`

Existing endpoint gains two computed fields per row:

```jsonc
{
  ...existing fields...,
  "cpu_avg_1h": 8.4,    // null if observability disabled or no data
  "mem_avg_1h": 34.3
}
```

Single trip to the API populates the row's sparkline values; the
sparkline component fetches its 60-point series via the metrics
endpoint when the row enters the viewport.

### Configuration (config.yaml only — no runtime API)

```yaml
docker:
  observability:
    enabled: true             # default true
    metrics_retention: 24h    # 6h | 24h | 72h
    events_retention: 30d     # 7d | 30d | 90d
```

Validation in `internal/config/config.go` rejects unsupported values
with a clear error. Changes require panel restart.

### Alert rule type extensions

Three new `type` values for the existing `alert_rules` table:

| type | trigger | condition JSON |
|---|---|---|
| `container_down` | `die` or `oom` event matches pattern | `{"container_pattern": "*"}` |
| `container_oom` | `oom` event only — auto-marks rule severity `critical` | `{"container_pattern": "*"}` |
| `container_restart_loop` | ≥ N restart events in T seconds | `{"container_pattern": "*", "threshold_count": 3, "window_seconds": 300}` |

`container_pattern` accepts shell-style wildcards (`*`, `?`); regex is
NOT supported (catastrophic-backtracking risk on operator input).
Pattern is matched against `container_name` at evaluation time.

Existing `alert_rules.channel_ids`, `severity`, `cooldown`, `node_scope`,
`enabled` semantics unchanged.

---

## UI changes

### Container list row — CPU + memory sparkline

Existing row currently:
```
nginx-app   running   CPU 8.4%   MEM 34.3%
```

Becomes:
```
nginx-app   running   CPU 8.4% ▁▂▄▆█▆▄▂   MEM 34.3% ▁▁▂▃▃▃▂▂
```

- 60 points × 1 h. Rendered with `uplot` (already a frontend dep).
- Click on row → opens existing detail drawer **on the new History
  tab** (today it opens on Overview).

New component: `web/src/components/ContainerSparkline.tsx`.

### Container detail drawer — new "History" tab

Tab list before: `Overview / Stats / Logs / Inspect / Exec`.
After: `Overview / Stats / **History** / Logs / Inspect / Exec`.

Tab body (top → bottom):

1. **Range selector**: `[ 1h ] [ 6h ] [ 24h ]`. Default 1h.
2. **CPU + Memory line chart**: two lines on dual y-axes (CPU% on left,
   Memory% on right). uplot. Sets `mem_bytes` on hover tooltip.
3. **Events timeline**: vertical list, newest first. Each row:
   `<icon> <time> <event_type> <exit_code/detail summary>`. Icons:
   - ⚠ OOM
   - ✗ die (non-zero exit)
   - ↻ start / restart
   - ⏹ stop
   - ✓ healthy
   - ⚡ unhealthy
   - 🔪 kill
4. **"더 보기"** button at the bottom for cursor pagination (50 events
   per fetch).

New components:
- `web/src/components/ContainerHistoryTab.tsx` — composes the chart +
  timeline.
- `web/src/components/EventTimelineRow.tsx` — single event row.

### Alert rule create/edit dialog — extended type selector

Existing dialog at `/alerts/rules/create` (and edit) gains three new
options in the `type` dropdown. Selecting one of them dynamically
swaps in:

- `container_down` / `container_oom`: a single text input
  `container_pattern` (default `*`).
- `container_restart_loop`: `container_pattern` + `threshold_count`
  (number, default 3) + `window_seconds` (number, default 300).

Existing channel selector / severity / cooldown / node_scope inputs
unchanged.

### NOT changed in v1

- Stack list `Health` column — deferred to v1.5.
- Dashboard "recent events" widget — deferred.
- Chart zoom / brush — uplot defaults only.

---

## Cluster behavior

Strict per-node model. The two new tables are NOT Raft-replicated. Each
node runs its own collector + listener.

### Cross-node access

All three new endpoints are `?node=<id>` aware via the existing
`ClusterProxyMiddleware` in `internal/api/middleware/proxy.go`. An
operator on node A can open the detail drawer for a container running
on node B and the cluster proxy forwards transparently. No new
streaming endpoints, so the existing gRPC unary path is sufficient
(no entry needed in `isStreamingEndpoint`).

### Alert delivery

Alert rules in `alert_rules` are already replicated through Raft FSM —
this stays. **Each node delivers alerts for events it observed locally**
(no leader-only fan-in). Alert payloads include a `[Node: <name>]`
prefix in the message body so the operator knows which node raised it.

Trade-off: a node with broken outbound network loses its alerts.
Accepted because:

- Leader-only delivery would force every node's events to fan in
  through Raft, multiplying the events-per-second load on the leader.
- A node with broken outbound network has bigger problems anyway, and
  the panel would surface those separately via existing host alerts.

### Cluster-mode parity for non-cluster operators

Single-node panels (cluster disabled) get all features identically. No
cluster-mode-only behavior in v1.

---

## Error handling

### Collector goroutine

| Scenario | Behavior |
|---|---|
| Docker daemon unreachable | Log warn, exponential backoff 1s → 30s cap, infinite retry |
| Per-container `ContainerStatsOneShot` hangs | 5s context timeout per call, skip that container, continue tick |
| Container deleted between list + stats | 404 silently skipped |
| DB write failure | `slog.Warn` + continue, do not crash collector |
| Panel shutdown (`bgCtx.Done()`) | Drain ticker, return cleanly within 100ms |

### Events listener

| Scenario | Behavior |
|---|---|
| Stream EOF / connection closed | Reconnect with exponential backoff 1s → 5min cap |
| Unknown `event_type` from Docker | Silent skip (forward-compat with future Docker versions) |
| JSON parse error | Log warn + skip event |
| `bgCtx.Done()` | Close stream + return |

### Pruners

| Scenario | Behavior |
|---|---|
| DELETE locks > 1s | Existing audit/metrics pattern — already indexed, has not been a problem |
| Pruner panic | Recovered by goroutine wrapper, next tick retries |

### API handlers

| Scenario | HTTP | Error code |
|---|---|---|
| Invalid `range` value | 400 | `INVALID_RANGE` |
| Container ID not in DB (no metrics yet) | 200 | `[]` (empty data) |
| Internal DB error | 500 | `DB_ERROR` (sanitized via `response.SanitizeOutput`) |
| Observability disabled | 200 | `{"observability_disabled": true, "data": []}` |

---

## Testing strategy

CLAUDE.md classifies all of the below as "Required tests" — security-
sensitive parsing, response contracts, complex parsers, shared infra.

### Unit tests

1. **`monitor/docker_history_test.go`** — collector with a mocked
   Docker client interface (small interface introduced over `*Client`
   to enable mocking, similar shape to `commonExec.Commander`).
   Verifies tick cadence, row shape, container-removed-mid-tick
   tolerance, daemon-down backoff.
2. **`monitor/docker_events_test.go`** — table-driven tests for each
   `event_type` JSON shape from Docker. Verifies parsing produces the
   right row + reconnect counter increments on injected EOF.
3. **`feature/alert/container_rules_test.go`** — most important tests.
   `container_restart_loop` window logic: 3 events in 5min → trigger,
   2 in 5min → no trigger, 3 spread over 6min → no trigger.
   `container_pattern` matching: `*`, `nginx-*`, exact, characters
   that would be regex-special pass through as literals.
4. **`feature/docker/metrics_handler_test.go`** — endpoint integration
   with in-memory SQLite + seeded data. `range=1h` cutoff math
   correct, missing container ID returns `200 + []`.

### Integration

5. **Migrations idempotent** — already covered by existing
   `db/migrations_test.go` framework. New entries automatically
   exercised under `TestRunMigrations_Idempotent` and
   `TestRunMigrations_PartialFailureRollsBack`.

### Manual smoke test (post-deploy)

- Collector running for ≥ 1h on the live cluster, verify chart
  populates and is bounded under 5MB/day in DB growth.
- Trigger a container OOM artificially (`docker run --memory=10m
  busybox sh -c "yes | head -c 100M"`) and verify the OOM event +
  alert firing within 10 s.

---

## Success metrics

Verified within 1 week post-deploy:

- Host CPU overhead < 1% average (peak spikes during 30 s tick OK).
- DB growth stable after 24h (retention pruner works).
- Resident memory < 50 MB additional vs. pre-feature panel.
- Alert delivery latency < 10s from event to channel.

---

## Migration / rollout

- **Defaults**: `enabled: true`, `metrics_retention: 24h`,
  `events_retention: 30d`. Existing operators upgrading get the
  feature on by default.
- **No data migration**: tables start empty, populate from first tick.
- **Rollback**: a follow-up release can disable via `enabled: false`
  in config.yaml — no schema removal needed (tables stay, retain
  whatever they have, auto-prune to retention; collector goroutines
  do not start).
- **Forward compatibility**: future themes (B / C / D) read from
  these tables; this spec locks the schema only enough for v1 — column
  additions are allowed via new migrations (CC-3).

---

## Open questions resolved (from roadmap)

- **Q-F1** Container metrics retention configurable per-node in v1?
  → Yes, via `config.yaml` only. 6h / 24h / 72h. No runtime API.
- **Q-F2** Filter Docker events to sfpanel-managed containers only?
  → No. Filter by event type (8 types). Capture all containers on host.

---

## Next step

After this spec is approved, the `writing-plans` skill produces an
implementation plan in
`docs/superpowers/plans/2026-05-03-docker-observability-plan.md` that
breaks the work into bite-sized TDD tasks. Implementation follows.
