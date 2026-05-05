# OS × Docker Integrated Views (Theme B — Phase 1) Design

**Status:** approved 2026-05-05
**Owner:** svrforum
**Roadmap entry:** `docs/superpowers/roadmaps/2026-05-03-docker-management-roadmap.md` § B

## Goal

Two read-only integrations that exploit SFPanel's unusual combination of
parsers (UFW, Docker SDK, `ss`, `du`) — features no other Docker panel can
do because they lack the OS modules.

1. **Unified Port Map.** One table that shows every port the host listens on
   alongside its UFW rule (allow / deny / no rule), the Docker DNAT binding
   (which container forwards it), and the bare process listening on the
   socket. Operators see "port 5432 → npg-db container, ALLOW from
   192.168.1.0/24, docker-proxy listening" in one row, plus jump links to
   every contributing page.
2. **Volume × Disk integration.** A 5-minute background `du -sb` measurement
   on every Docker volume, cached per-node. The Volumes page gets a Size
   column (sortable). The Disk page gets a "Docker 볼륨 사용량" card showing
   top 10 volumes + total + a link back to the Volumes page.

This is **Phase 1 of Theme B**. Per-container egress firewall (declarative
DOCKER-USER iptables) is a Phase 2 feature, deferred for safety review.
systemd × Docker side-by-side dashboard is a stretch goal, also deferred.

## Scope

### In scope (Phase 1)

- **Port Map**: read-only table aggregating UFW × Docker DNAT × `ss` listening,
  one row per `(port, proto)`. Read-only with jump links — no port-close /
  rule-add / process-kill actions in this view (those live on their own
  pages already).
- **Volume Usage cache**: per-node SQLite table populated by a 5-minute
  background goroutine. Sequential `du -sb` (one volume at a time) so a
  fleet of 50 volumes doesn't pin disk I/O.
- **Volumes page Size column**: sortable, with a "측정 시각" tooltip.
- **Disk page card**: top 10 + total bytes + link to Volumes page.

### Out of scope (Phase 2 / deferred)

- **Per-container egress firewall UI** — destructive, requires DOCKER-USER
  iptables manipulation; needs separate safety design.
- **systemd × Docker dashboard** — stretch goal; defer.
- **Real-time port map streaming** — page-load + manual refresh is enough
  (snapshots change at minute granularity, not seconds).
- **Action buttons in Port Map** — port close / rule add / process kill.
  Existing pages already do these; jump links suffice.
- **Volume size on remote storage drivers** — design assumes the default
  local driver at `/var/lib/docker/volumes/<name>/_data`. Cluster nodes
  with non-local drivers will report null size, no error.

### Design principles

1. **Read-only by default.** Both features inform; users act on the page
   that owns the action (Firewall / Containers / Volumes).
2. **Graceful degradation.** Any single data source failing (UFW disabled,
   `ss` missing) returns empty for that column, not an error.
3. **Per-node, never replicated.** Same convention as observability —
   port map is a snapshot of THIS host, volume size is THIS node's local
   filesystem. Cluster `?node=` proxy works automatically.

## Architecture

```
┌─ Frontend ──────────────────────────────────────────────┐
│ /firewall page → shadcn Tabs                            │
│   ├─ "방화벽 룰" (existing UFW UI)                      │
│   └─ "포트 맵" (new) → PortMapTable                     │
│ /system/disk page (mod) → DockerVolumeUsageCard (new)   │
│ /docker/volumes page (mod) → Size column                │
└─────────────────────────────────────────────────────────┘
                ↓
┌─ Backend ───────────────────────────────────────────────┐
│ feature/portmap (new)                                   │
│   ├─ aggregator.go — merge 3 sources by (port, proto)   │
│   ├─ ss_parser.go  — parse `ss -tlnp -H` output         │
│   └─ handler.go    — GET /api/v1/system/portmap         │
│ feature/docker     (mod)                                │
│   └─ ListVolumes augmented with size_bytes from cache   │
│ monitor (mod)                                           │
│   └─ docker_volume_usage.go — 5min collector goroutine  │
│ db/migrations.go (mod)                                  │
│   └─ migration 20: docker_volume_usage table            │
└─────────────────────────────────────────────────────────┘
                ↓
┌─ DB (per-node SQLite, NOT replicated) ──────────────────┐
│ docker_volume_usage(volume_name PK, size_bytes,         │
│                     measured_at)                         │
└─────────────────────────────────────────────────────────┘
```

## Components

### `internal/feature/portmap/aggregator.go` (new)

```go
type PortMapRow struct {
    Port      int             `json:"port"`
    Proto     string          `json:"proto"`     // "tcp" | "udp"
    State     string          `json:"state"`     // "listening" | "bound"
    Firewall  *FirewallInfo   `json:"firewall"`
    Container *ContainerInfo  `json:"container"`
    Process   *ProcessInfo    `json:"process"`
}

type FirewallInfo struct {
    Action   string `json:"action"`     // "ALLOW" | "DENY" | "REJECT"
    Scope    string `json:"scope"`      // "any" | "192.168.1.0/24" | …
    RuleID   int    `json:"rule_id"`    // index into UFW user.rules
    Source   string `json:"source"`     // "ufw" | (future: "iptables-direct")
}

type ContainerInfo struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Stack string `json:"stack"`         // empty if not part of a compose stack
}

type ProcessInfo struct {
    PID  int    `json:"pid"`
    Name string `json:"name"`
}

// Aggregate merges three source slices into one sorted []PortMapRow.
// Pure function. Merge key is (Port, Proto).
func Aggregate(ufw []firewall.Rule, dnat []docker.PortBinding, listeners []SsEntry) []PortMapRow
```

### `internal/feature/portmap/ss_parser.go` (new)

```go
type SsEntry struct {
    Port    int
    Proto   string
    PID     int
    Name    string
}

// ParseSs parses the `ss -tlnp -H` machine-readable output:
//   tcp  LISTEN  0  128  *:8444  *:*  users:(("sfpanel",pid=1410507,fd=10))
// Returns one SsEntry per listener. Multi-process listeners (multiple
// `users:(...)`) emit one entry each.
func ParseSs(out string) []SsEntry
```

Edge cases the parser must handle:
- IPv4 + IPv6 (`*:8444` vs `[::]:8444`).
- Multiple processes on one socket (rare but possible).
- Lines without a `users:` clause (running without root → no PID info).
- UDP variant (`ss -ulnp -H`).

### `internal/feature/portmap/handler.go` (new)

```go
func (h *Handler) GetPortMap(c echo.Context) error
```

Internal flow:

1. `errgroup.Group` with 3 fan-out goroutines:
   - UFW: call existing `firewall.ParseUserRules()` (if present in repo)
     or fallback to `h.Cmd.Run("ufw", "status", "numbered")`.
   - Docker: `h.Docker.ListContainers(ctx)` + per-container Inspect
     for `NetworkSettings.Ports`.
   - `ss`: `h.Cmd.Run("ss", "-tlnp", "-H")` + `h.Cmd.Run("ss", "-ulnp", "-H")`,
     concatenate, parse.
2. Each goroutine logs `slog.Warn` + returns empty slice on error so
   `Aggregate` still produces partial rows.
3. `Aggregate(...)` → JSON response.

Single endpoint, no streaming. Cluster proxy works automatically.

### `internal/feature/docker/handler.go` (mod)

`ListContainers` already augmented with `cpu_avg_1h`/`mem_avg_1h` (from
observability). Same pattern for volumes:

```go
type volumeWithUsage struct {
    *volume.Volume
    SizeBytes      *int64 `json:"size_bytes"`       // nil if no measurement
    SizeMeasuredAt *int64 `json:"size_measured_at"` // unix millis, nil if none
}
```

After fetching from Docker SDK, single SQL query:
```sql
SELECT volume_name, size_bytes, measured_at FROM docker_volume_usage
```
Build a map, decorate. (Same per-node table convention as observability —
this is the SAME node's local volume sizes, no cluster join.)

### `internal/monitor/docker_volume_usage.go` (new)

```go
func StartVolumeUsageCollector(ctx context.Context, db *sql.DB, dockerCli *docker.Client)
```

Goroutine that:
- Initial 30-second delay (let panel finish booting).
- Tick every 5 minutes.
- Each tick:
  1. `dockerCli.ListVolumes(ctx)` — fail-soft.
  2. For each volume, sequentially:
     - `volPath := /var/lib/docker/volumes/<name>/_data`
     - `Cmd.RunWithTimeout(30s, "du", "-sb", volPath)`
     - Parse first column (bytes) from stdout.
     - `INSERT OR REPLACE INTO docker_volume_usage VALUES (?, ?, ?)`
  3. Errors per volume → log warn, skip, continue.
- Sequential is deliberate: parallel `du` on 50 volumes pins disk for
  minutes. Sequential staggers I/O over the tick.

### `internal/db/migrations.go` (mod) — migration 20

```go
{ID: 20, Up: `CREATE TABLE IF NOT EXISTS docker_volume_usage (
    volume_name TEXT PRIMARY KEY,
    size_bytes  INTEGER NOT NULL,
    measured_at INTEGER NOT NULL
)`},
```

### Frontend — `web/src/components/portmap/PortMapTable.tsx` (new)

shadcn `Table` with sticky header. Columns:

| 컬럼 | 너비 | 내용 |
|---|---|---|
| 포트 | 80px | `5432` (font-mono) |
| 프로토콜 | 60px | `TCP` / `UDP` (small badge) |
| 상태 | 80px | `LISTENING` (green) / `BOUND` (gray) |
| 방화벽 | 200px | `ALLOW LAN` 등 + `<ExternalLink>` icon → /firewall?ruleId=12 |
| 컨테이너 | flex | `npg-db` (npg) — link to /docker/stacks/npg/containers/<id> |
| 프로세스 | 200px | `docker-proxy (1024)` — link to /system/processes?pid=1024 |

빈 셀 (해당 source 데이터 없음): `text-muted-foreground` + dash (`—`).

색상 시그널:
- 외부 노출 의심 (firewall=ALLOW any + container 있음): row left-border `border-l-2 border-amber-500`.
- 룰 없는 listener (firewall=null + container=null + process는 있음 — bare host process exposed): `border-l-2 border-destructive`.

### Frontend — `web/src/pages/Firewall.tsx` (mod)

Existing UFW UI는 `<TabsContent value="rules">`로 감쌈. New tab:

```tsx
<TabsList>
  <TabsTrigger value="rules">방화벽 룰</TabsTrigger>
  <TabsTrigger value="portmap">포트 맵</TabsTrigger>
</TabsList>
<TabsContent value="rules">{/* existing */}</TabsContent>
<TabsContent value="portmap"><PortMapTable /></TabsContent>
```

### Frontend — `web/src/components/disk/DockerVolumeUsageCard.tsx` (new)

```
┌─ 카드 ──────────────────────────────────────────────┐
│ 🐳 Docker 볼륨 사용량                  [전체 보기 →]│
├─────────────────────────────────────────────────────┤
│ npg_pg_data        45.2 GB   ████████░░ 62%         │
│ immich_thumbs      12.1 GB   ██░░░░░░░░ 16%         │
│ jellyfin_metadata   3.8 GB   █░░░░░░░░░  5%         │
│ … 7 more                                            │
├─────────────────────────────────────────────────────┤
│ 합계: 73.4 GB · 12개 볼륨 · 5분 전 측정             │
└─────────────────────────────────────────────────────┘
```

각 row: name + bytes (humanBytes) + bar (percent of host disk, optional).

### Frontend — `web/src/pages/docker/DockerVolumes.tsx` (mod)

새 컬럼 "크기" — sortable. cache 없으면 `측정 중…` placeholder + spinner.

## API contracts

### `GET /api/v1/system/portmap`

Response 200:
```json
{
  "success": true,
  "data": [
    {
      "port": 22,
      "proto": "tcp",
      "state": "listening",
      "firewall": null,
      "container": null,
      "process": { "pid": 1024, "name": "sshd" }
    },
    {
      "port": 5432,
      "proto": "tcp",
      "state": "listening",
      "firewall": { "action": "ALLOW", "scope": "192.168.1.0/24", "rule_id": 12, "source": "ufw" },
      "container": { "id": "abc...", "name": "npg-db", "stack": "npg" },
      "process": { "pid": 9912, "name": "docker-proxy" }
    }
  ]
}
```

빈 source는 `null`. 절대 빈 string / 빈 object가 아님.

오류:
- `500 INTERNAL_ERROR` — 모든 3-source가 fail (희박). 단일 fail은 200 with partial data.

### `GET /api/v1/docker/volumes` (augmented)

기존 응답에 두 필드 추가 per row: `size_bytes` (number | null), `size_measured_at` (number | null, unix millis).

## Cluster awareness

Both endpoints are unary JSON under `authorized` group. `?node=<id>` automatically proxies via existing `ClusterProxyMiddleware` (gRPC unary).

Volume usage collector runs on every node independently — `bgCtx` lifecycle, started in `cmd/sfpanel/main.go` next to the observability collectors.

Disk page card shows ONLY the local node's volume sizes. Operators viewing
another node's disk see that node's data via `?node=` (handler is local-only,
proxy fans out the request).

## Error handling

| Path | Error | Behavior |
|---|---|---|
| Port Map | UFW not installed | log warn (debug-level — UFW absence is normal). All rows: firewall=null. |
| Port Map | UFW present but parse error | log warn. firewall=null per row. |
| Port Map | Docker socket unreachable | log warn. container=null per row. |
| Port Map | `ss` missing (rare) | log warn. process=null per row. |
| Port Map | All 3 fail | return 500 INTERNAL_ERROR with sanitized message. |
| Volume usage collector | `du` timeout | log warn, skip. Cache stays stale until next tick. |
| Volume usage collector | volume removed mid-tick | `du` returns ENOENT, skip without log spam. |
| Volume usage collector | docker socket gone at tick start | log warn, skip entire tick. Resume next tick. |
| ListVolumes | DB join fails | return Docker data without size fields (graceful). |

`response.SanitizeOutput` applied to every error message that surfaces text
from underlying tools — `ss`, `ufw`, `du` may include hostnames or paths.

## Testing

| Required | Why |
|---|---|
| `ParseSs` table tests (IPv4, IPv6, multi-process, no users:, UDP) | Real-world `ss` output edges |
| `Aggregate` table tests (matrix: source overlap presence/absence) | Pure function, locks the merge contract |
| `StartVolumeUsageCollector` tick test (mocked `Cmd`, mocked Docker) | du output parsing + INSERT OR REPLACE semantics |
| `GetPortMap` handler test (3 sources mocked, single-source-fail) | Concurrent fetch + graceful degradation |
| `DockerListVolumes` join test (cache present + cache absent) | size augmentation contract |

| Not required | Why |
|---|---|
| UFW parser tests | Reuse existing `internal/feature/firewall/` tests |
| `du` output parsing | trivial line split |
| Frontend unit tests | UI glue — manual smoke at task end |

## Future-proofing notes

- `PortMapRow` schema deliberately leaves `firewall.source` open
  (`"ufw"`, future `"iptables-direct"` / `"nftables"`).
- `docker_volume_usage` table can be reused as the source for a future
  alerting rule type `volume_size_exceeds`.
- Phase 2 (per-container egress) can read the same Port Map data to
  show the operator "this container currently talks to X" before they
  declare a rule.

## Approval

Approved by user 2026-05-05. Proceed to writing-plans.
