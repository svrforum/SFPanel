# AppStore — Template Forks (Theme E — Phase 1) Design

**Status:** approved 2026-05-05
**Owner:** svrforum
**Roadmap entry:** `docs/superpowers/roadmaps/2026-05-03-docker-management-roadmap.md` § E

## Goal

Two read-and-write extensions of the existing AppStore that turn it from a
read-only marketplace of pre-curated apps into a personal template library.

1. **Fork from running stack.** Operator clicks "Template으로 저장" on a
   running stack's detail drawer. SFPanel extracts the deployed
   `docker-compose.yml` plus the current environment variable values, lets
   the operator add a name + description + category, and saves the result
   as a personal template (a "fork"). Forks are usable everywhere the
   official AppStore catalog is — they slot into the existing install
   path because they conform to the same `AppStoreMeta` shape.
2. **3-tab AppStore UI.** The AppStore page splits into three tabs:
   "Marketplace" (existing official catalog), "내 Templates" (forks created
   by this cluster's operators), and "설치됨" (apps + forks currently
   running on this node). Search and category filter scope to the current
   tab.

This is **Phase 1 of Theme E**. Version pinning, screenshot/changelog
metadata, and external marketplace contribution are deferred.

## Scope

### In scope (Phase 1)

- **Fork creation** — backend endpoint + FSM command + UI dialog.
- **Fork listing** — read endpoint + "내 Templates" tab.
- **Fork install** — existing AppStore install handler reused; the only
  change is `GetApp` falling back to the fork store when the ID isn't
  found in the official cache.
- **Fork metadata edit** — name / description / category only. YAML is
  immutable; changing the YAML means creating a new fork.
- **Fork delete** — from the "내 Templates" tab card or fork detail page.
- **3-tab UI** — AppStore page top-level shadcn `Tabs`.
- **Search per tab** — existing search box scoped to the active tab.

### Out of scope (Phase 2 / future waves)

- **Version pinning** — recording which fork version a running stack came
  from + upgrade-confirmation flow.
- **Screenshot / icon upload** — fork visual assets.
- **Changelog** — per-fork commit-style history.
- **Fork export / import** — moving a fork between clusters.
- **GitHub PR contribution** — forks → official catalog upstream.
- **Per-user permissions** — single-admin model for now.

## Architecture

```
┌─ Frontend ──────────────────────────────────────────────┐
│ /appstore page (mod) — 3-tab Tabs                       │
│   ├─ Marketplace      (existing list, official cache)   │
│   ├─ 내 Templates     (NEW — fork list, edit/delete)    │
│   └─ 설치됨           (NEW — installed apps + forks)    │
│ /docker/stacks page (mod) — "Template으로 저장" 버튼     │
│   └─ ForkCreateDialog — name/desc/category form         │
│ /appstore/fork/:id (NEW page) — metadata edit           │
└─────────────────────────────────────────────────────────┘
                ↓
┌─ Backend ───────────────────────────────────────────────┐
│ feature/appstore (mod)                                  │
│   ├─ fork.go         — fork-specific helpers + types    │
│   ├─ fork_extract.go — running stack → AppStoreMeta     │
│   ├─ handler.go (mod) — GetApp falls back to fork store │
│   └─ fork_handler.go — CRUD endpoints                    │
│ cluster/raft_fsm.go (mod)                               │
│   ├─ Op kinds: ForkCreate / ForkUpdate / ForkDelete     │
│   └─ FSM state field: forks map[string]*ForkRecord      │
└─────────────────────────────────────────────────────────┘
                ↓
┌─ Cluster FSM (replicated, snapshot-persisted) ──────────┐
│ forks: id → {Meta (AppStoreMeta), CreatedAt, CreatedBy} │
└─────────────────────────────────────────────────────────┘
```

**Storage placement:** Cluster FSM, NOT per-node SQLite. Same convention
as `alert_rules` and `alert_channels` — operator-defined assets that
should be visible from any node. FSM state is in-memory map + Raft log +
snapshot. Single-node clusters use the same path (Raft is bootstrapped
with one voter; FSM commands still flow through it).

## Components

### Backend

#### `internal/feature/appstore/fork.go` (new)

```go
type ForkRecord struct {
    Meta      AppStoreMeta // existing struct, reused as-is
    CreatedAt int64        // unix millis
    CreatedBy string       // username from JWT, "" if unknown
}
```

`AppStoreMeta` (already defined at `handler.go:53`) has all needed fields:
ID, Name, Description, Category, IconURL, EnvDefs, Features, Version,
Compose. Fork records reuse this struct verbatim — install handler doesn't
need to know whether the source is official or fork.

ID format: `fork-<short-uuid>` (e.g. `fork-a1b2c3d4`). Prefix prevents
collision with official IDs (which are slugs like `nextcloud`, `grafana`).

#### `internal/feature/appstore/fork_extract.go` (new)

```go
// ExtractForkMeta builds an AppStoreMeta from a running stack.
// stackName: project name. composeYAML: deployed compose content.
// envValues: current env var values (key → value).
// userMeta: name/description/category supplied via the fork dialog.
func ExtractForkMeta(stackName, composeYAML string, envValues map[string]string, userMeta UserForkInput) AppStoreMeta
```

Internal flow:

1. Parse compose YAML into a tree (yaml.v3).
2. Walk `services.<svc>.environment`. For each env key:
   - Build `AppStoreEnvDef{Key, Default: envValues[key], Type: "string", Label: key}`.
3. Set `Meta.Compose = composeYAML` (verbatim).
4. Set `Meta.ID = "fork-" + shortUUID()`, `Meta.Name`, `Meta.Description`,
   `Meta.Category` from `userMeta`.
5. Set `Meta.Version = "1.0.0"` (Phase 1 default; pinning comes later).
6. `Meta.IconURL = ""` (Phase 2 feature).

```go
type UserForkInput struct {
    Name        string
    Description string
    Category    string // default "내 Templates" if empty
}
```

Pure function — easy to unit test without docker socket or FSM.

#### `internal/feature/appstore/fork_handler.go` (new)

```go
func (h *Handler) ListForks(c echo.Context) error
func (h *Handler) GetFork(c echo.Context) error
func (h *Handler) CreateFork(c echo.Context) error
func (h *Handler) UpdateFork(c echo.Context) error // metadata only
func (h *Handler) DeleteFork(c echo.Context) error
```

`CreateFork` flow:

1. Parse body: `{stack_name, name, description, category}`.
2. Look up `stack_name` via existing `Compose.GetProjectYAML` → composeYAML.
3. Look up env via `Compose.GetProjectEnv` → key=value lines → map.
4. `ExtractForkMeta(...)` → `AppStoreMeta`.
5. Submit FSM command `ForkCreate{ID, Record}` via `clusterMgr.Apply(...)`.
6. Return new fork ID.

`UpdateFork` flow:

1. Body: `{name, description, category}` only.
2. FSM `ForkUpdate{ID, Patch}`.

`DeleteFork`:

1. FSM `ForkDelete{ID}`.

#### `internal/feature/appstore/handler.go` (mod, GetApp)

`GetApp` currently looks up `id` in the official cache. Fall back to fork
store if not found:

```go
// Pseudo-diff inside GetApp:
meta, ok := h.cache[id]
if !ok {
    // Fork lookup
    if forkRecord := h.lookupFork(id); forkRecord != nil {
        meta = forkRecord.Meta
        ok = true
    }
}
if !ok { return 404 }
```

`lookupFork(id)` reads from FSM state via `clusterMgr.GetState().Forks[id]`.
This makes `InstallApp` (existing) work transparently for forks — same
endpoint, same body shape, same SSE streaming flow.

#### `internal/cluster/raft_fsm.go` (mod)

Add three new Op constants:
```go
const (
    OpForkCreate = "fork_create"
    OpForkUpdate = "fork_update"
    OpForkDelete = "fork_delete"
)
```

FSM state struct gets a new field:
```go
type State struct {
    // ... existing fields ...
    Forks map[string]*appstore.ForkRecord `json:"forks"`
}
```

Apply handlers parse the command payload + mutate `s.Forks`. Snapshot
serializer already encodes the full `State` struct via JSON — `Forks`
goes along for free.

### Frontend

#### `web/src/pages/AppStore.tsx` (mod) — 3-tab structure

```tsx
<Tabs defaultValue="marketplace">
  <TabsList>
    <TabsTrigger value="marketplace">Marketplace</TabsTrigger>
    <TabsTrigger value="forks">내 Templates</TabsTrigger>
    <TabsTrigger value="installed">설치됨</TabsTrigger>
  </TabsList>
  <TabsContent value="marketplace">{/* existing list */}</TabsContent>
  <TabsContent value="forks"><ForkList /></TabsContent>
  <TabsContent value="installed"><InstalledList /></TabsContent>
</Tabs>
```

Search box + category filter currently scope to the entire app list.
After the change they scope to the current tab's data source — `forks`
tab filters fork list, etc.

#### `web/src/components/appstore/ForkList.tsx` (new)

Card grid of forks. Each card:

```
┌─ Card ──────────────────────────────────────┐
│ 📦 Name                          [...] menu │
│ category badge                              │
│ Description (truncate 2 lines)              │
│                                             │
│ [설치]  [편집]                              │
└─────────────────────────────────────────────┘
```

`[...]` menu: 편집 / 삭제. Confirmation dialog for delete.

#### `web/src/components/appstore/InstalledList.tsx` (new)

Reads from existing `GET /appstore/installed` (returns app IDs currently
installed). Joins with marketplace cache + fork list to display each entry
with its source (official / fork). Acts as a quick "what's running" view.

#### `web/src/pages/docker/DockerStacks.tsx` (mod) — "Template으로 저장" 버튼

Stack detail drawer footer (next to existing 시작/중지/재시작) gets a new
button:

```tsx
<Button variant="outline" size="sm" onClick={() => setForkDialogOpen(true)}>
  <Save className="h-3.5 w-3.5 mr-1" />
  Template으로 저장
</Button>
```

Click → `ForkCreateDialog`:

```
┌─ Dialog: Template으로 저장 ────────────────┐
│ 이름 *      [my-template          ]        │
│ 설명        [짧은 한 줄 설명…    ]        │
│ 카테고리    [내 Templates ▼     ]        │
│                                            │
│ ⓘ 현재 stack의 compose YAML과 환경 변수가 │
│    자동으로 포함됩니다.                    │
│                                            │
│             [취소]  [저장]                  │
└────────────────────────────────────────────┘
```

Submit → `POST /appstore/forks` → toast "<name> 템플릿이 저장되었습니다" + 다이얼로그 닫힘. AppStore 페이지 "내 Templates" 탭으로 이동 link in toast.

#### `web/src/pages/AppStoreForkDetail.tsx` (new)

Fork detail with editable metadata form:
- Name, Description, Category fields (editable).
- YAML preview (read-only Monaco).
- Env defs table (read-only — derived from fork creation).
- "저장" button → PATCH metadata.
- "삭제" button → confirmation → DELETE.
- "설치" button → `/appstore/<fork-id>` (existing AppStoreDetail page handles install via the `GetApp` fallback).

Route: `/appstore/forks/:id`.

## API contracts

### Fork CRUD

```
GET    /api/v1/appstore/forks                    list (cluster-wide)
GET    /api/v1/appstore/forks/:id                detail
POST   /api/v1/appstore/forks                    create
PATCH  /api/v1/appstore/forks/:id                edit metadata
DELETE /api/v1/appstore/forks/:id                delete
```

`POST /forks` request:
```json
{
  "stack_name": "my-stack",
  "name": "My Web Stack",
  "description": "Nginx + PostgreSQL bundle I use for testing",
  "category": "내 Templates"
}
```

Response:
```json
{ "success": true, "data": { "id": "fork-a1b2c3d4" } }
```

`PATCH /forks/:id` request (any subset):
```json
{ "name": "...", "description": "...", "category": "..." }
```

### Existing GetApp / Install reused

`GET /appstore/apps/:id` — works for fork IDs (`fork-...`) via the fallback
described in the GetApp mod.

`POST /appstore/apps/:id/install` — same. Existing SSE streaming flow,
existing port-conflict + container-name-conflict checks.

## Cluster awareness

All fork mutations go through Raft (`clusterMgr.Apply(...)`). Reads can
happen on any node (FSM state is replicated). Single-node clusters use the
same path — Raft has 1 voter, Apply commits immediately.

`ClusterProxyMiddleware` proxies write endpoints to the leader
automatically (existing behavior). Read endpoints (`GET /forks`,
`GET /forks/:id`) work on followers — read directly from local FSM state
(stale by ≤ replication lag, which is sub-second for healthy clusters).

## Error handling

| Path | Error | Behavior |
|---|---|---|
| CreateFork | `stack_name` doesn't exist | 404 NOT_FOUND |
| CreateFork | YAML parse fails (corrupted on disk) | 500 INTERNAL_ERROR (sanitized) |
| CreateFork | name length > 100 / contains `/` | 400 INVALID_REQUEST |
| CreateFork | FSM apply fails (cluster split) | 503 CLUSTER_UNAVAILABLE |
| GetFork | id not found in FSM | 404 NOT_FOUND |
| UpdateFork | id not found | 404 NOT_FOUND |
| UpdateFork | yaml mutation attempted (extra field) | 400 INVALID_REQUEST |
| DeleteFork | id not found | 404 (idempotent — ok to swallow) |
| Install of fork-id | env_def missing required field | existing AppStore behavior — 400 |

## Testing

| Required | Why |
|---|---|
| `ExtractForkMeta` table tests | Pure function. env_def derivation correctness across compose env shapes (map vs list, with/without values). |
| FSM Apply tests for ForkCreate/Update/Delete | Locks the replicated state contract. |
| Snapshot round-trip with forks | Ensures Raft snapshot serializes/deserializes forks correctly. |
| Handler test: CreateFork happy path (mocked Compose + Cluster) | API contract. |
| Handler test: GetApp fallback to fork store | Verifies fork install reuse path. |

| Not required | Why |
|---|---|
| Frontend unit tests | UI glue — manual smoke at task end |
| Existing AppStore install path | Already covered |

## Future-proofing notes

- `ForkRecord` carries `CreatedAt` + `CreatedBy` from day one — Phase 2's
  version pinning can record source-fork-id + created-from-version on the
  installed stack rows without schema migration.
- AppStore's existing install streaming (SSE) handles forks transparently.
  Phase 2 upgrade flow ("install over existing fork instance") fits the
  same SSE pattern.
- `Meta.Version = "1.0.0"` default leaves room for Phase 2 to bump on
  metadata edits ("update fork version → instances see upgrade-available
  badge").

## Approval

Approved by user 2026-05-05. Proceed to writing-plans.
