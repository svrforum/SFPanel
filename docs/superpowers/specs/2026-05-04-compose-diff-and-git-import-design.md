# Compose Diff Preview + Git Import (Theme D — Phase 1) Design

**Status:** approved 2026-05-04
**Owner:** svrforum
**Roadmap entry:** `docs/superpowers/roadmaps/2026-05-03-docker-management-roadmap.md` § D. Compose UX

## Goal

Two safety-and-onboarding features for SFPanel's Docker Compose workflow:

1. **Diff before apply.** When the operator edits a stack's `docker-compose.yml`,
   show a categorized preview of what will change before they hit "apply" —
   image upgrades, port openings, volume rebinds, env flips, restart policy
   changes, healthcheck edits. Eliminates "I clicked apply and now my stack
   is broken" moments.
2. **One-shot Git import.** When creating a new stack, give the operator a
   "from git" option in the "새 stack 추가" dialog. SFPanel clones the repo,
   reads the compose file, and registers the stack. After import, the stack is
   a normal SFPanel-managed stack — no continuous git binding.

This is **Phase 1 of Theme D**. Continuous git sync (polling, auto-deploy,
git-as-source-of-truth) is deferred to Phase 2.

## Scope

### In scope (Phase 1)

- **Mode 1 — Manual stack with diff preview.** Operator edits compose YAML in
  Monaco, clicks "변경사항 미리보기", sees semantic diff vs. deployed YAML.
- **Mode 2 — One-shot git import.** Operator picks "git에서 가져오기" in the
  new-stack dialog, supplies repo URL + (optional) PAT + branch + compose
  file path. SFPanel clones, reads, registers as a normal stack. Token is
  used once and not persisted.

### Out of scope (Phase 2 / future waves)

- **Mode 3 — Continuous git sync.** Polling, "deploy from git" button,
  git-linked stacks with read-only UI editor.
- **Webhook trigger.** Receiving GitHub push events to auto-deploy.
- **Git push.** UI edits committing back to git.
- **Multi-provider.** GitLab/Gitea/Forgejo support — Phase 1 is GitHub-only,
  but the underlying `go-git` library handles them so a future wave can
  expand provider coverage with mostly UI work.
- **Healthcheck composer (D feature 3).** Form-driven healthcheck block
  generator. Independent of Diff/Import; defer.
- **Environment variants (D feature 4).** Override file selector
  (dev/staging/prod). Defer.

### Self-hosting context

Most SFPanel operators run stacks fully locally — no git involvement. The
diff preview is a daily-use feature for everyone. Git import is occasional
("I want to try this open-source compose file"). The design isolates git
into a single optional path so non-git users see only the diff button.

## Architecture

```
┌─ Frontend ──────────────────────────────────────────────┐
│ ComposeEditor (existing Monaco editor)                  │
│   ├─ "변경사항 미리보기" button (NEW)                   │
│   └─ DiffPanel (semantic categories + raw text tab)     │
│ "새 stack 추가" dialog                                  │
│   └─ "git에서 가져오기" radio + form (NEW)              │
└─────────────────────────────────────────────────────────┘
                        │ HTTPS / JWT
                        ▼
┌─ Backend (internal/feature/compose) ────────────────────┐
│ diff.go        — pure-function YAML semantic diff       │
│ git_import.go  — one-shot clone + compose.yml read      │
│ handler.go     — endpoints (modified)                   │
└─────────────────────────────────────────────────────────┘
                        │
                        ▼
┌─ DB ────────────────────────────────────────────────────┐
│ NO schema changes.                                      │
│ Imported stacks land in the existing `stacks` table     │
│ as ordinary entries.                                    │
└─────────────────────────────────────────────────────────┘
```

**Per-node, NOT replicated.** Both endpoints are stateless reads/writes
against the local stack table — same convention as the rest of compose
features. Cluster `?node=` proxy works automatically.

## Components

### `internal/feature/compose/diff.go` (new)

Pure function. Takes two YAML strings (deployed + proposed), returns a
`DiffResult` struct with categorized changes.

```go
type DiffResult struct {
    Summary    DiffSummary           `json:"summary"`
    ByCategory map[string]any        `json:"by_category"`
    RawDiff    string                `json:"raw_diff"`
}
type DiffSummary struct {
    Added    int `json:"added"`
    Modified int `json:"modified"`
    Removed  int `json:"removed"`
}

func ComputeDiff(deployedYAML, proposedYAML string) (*DiffResult, error)
```

Internal flow:

1. Parse both YAML strings to `map[string]any` via `gopkg.in/yaml.v3`.
2. Walk `services.<name>.{image, ports, volumes, environment,
   restart, healthcheck}` for each service. Collect added/modified/removed
   per category.
3. Compute text-level raw diff (`go-diff` or simple line-based) for the
   "raw" tab.
4. Return populated `DiffResult`.

Six categories tracked:

| Category | Field path | Detection |
|---|---|---|
| `image` | `services.<svc>.image` | string change |
| `ports` | `services.<svc>.ports` | array set diff |
| `volumes` | `services.<svc>.volumes` | array set diff |
| `env` | `services.<svc>.environment` | map diff (handles list + map forms) |
| `restart` | `services.<svc>.restart` | string change |
| `healthcheck` | `services.<svc>.healthcheck` | block existence + sub-field diff |

A new service triggers `summary.added`. A removed service triggers
`summary.removed`. A modified service (any of 6 categories changed)
triggers `summary.modified`.

### `internal/feature/compose/git_import.go` (new)

```go
type ImportRequest struct {
    URL    string `json:"url"`     // e.g. "https://github.com/user/repo.git"
    Branch string `json:"branch"`  // default "main"
    Path   string `json:"path"`    // default "docker-compose.yml"
    Token  string `json:"token"`   // optional PAT, used once
    Name   string `json:"name"`    // resulting stack name
}

func (h *Handler) ImportFromGit(c echo.Context) error
```

Internal flow:

1. Validate request (URL is GitHub HTTPS only, name is non-empty + unique
   stack-name regex).
2. Clone to a temp dir using `go-git/v5`. With token: HTTP basic-auth
   `username=x-access-token, password=<PAT>`. Without token: anonymous.
   Shallow clone (`Depth: 1`).
3. Read `<tempdir>/<path>` as YAML; reject if invalid YAML or missing
   `services` key.
4. Create the stack directory under `cfg.Server.StacksPath/<name>/` and
   copy the compose file there.
5. Insert into `stacks` table via existing `compose.CreateStack` helper
   (so we get audit log, validation, and cluster broadcast for free).
6. Wipe the temp dir. Token is never persisted; it lives only in the
   request struct on the goroutine stack.

### `internal/feature/compose/handler.go` (modified)

Two new endpoints registered in `internal/api/router.go`:

```
POST /api/v1/compose/:project/diff      — Handler.DiffStack
POST /api/v1/compose/import             — Handler.ImportFromGit
```

`DiffStack` reads the existing deployed YAML from disk
(`cfg.Server.StacksPath/<project>/docker-compose.yml`), takes the
proposed YAML from the request body, calls `ComputeDiff`, and returns
the result.

### Frontend — files

```
web/src/components/compose/
  DiffSheet.tsx          — Sheet (slide-over) wrapping the diff view
  DiffSummaryHeader.tsx  — top bar: 추가/변경/삭제 counters
  DiffCategoryList.tsx   — accordion list of 6 categories
  DiffServiceRow.tsx     — single service row inside a category
  GitImportForm.tsx      — fields for git import tab

web/src/pages/docker/
  DockerStacks.tsx       — modified: add "변경사항 미리보기" button to
                           stack drawer's "editor" tab; add "git에서
                           가져오기" tab to "새 스택" dialog

web/src/types/api.ts     — DiffResult, DiffSummary, ImportRequest types
web/src/lib/api.ts       — diffStack(), importFromGit() methods
```

### UI/UX — design principles

The whole flow follows three rules drawn from the existing SFPanel UX:

1. **No new top-level pages.** Diff lives inside the existing stack
   detail drawer (Tabs pattern at `DockerStacks.tsx:761`). Import lives
   inside the existing "새 스택" dialog as a tab. Nothing the user has
   to discover via a new menu item.
2. **Reuse the design system.** shadcn `Sheet`, `Tabs`, `Button`,
   `Dialog`, `Input`, `Label`, `Accordion` — every primitive we need
   already exists in `web/src/components/ui/`. Color/spacing tokens
   come from existing Tailwind classes (`text-muted-foreground`,
   `text-destructive`, `text-emerald-600`, `bg-secondary/50`,
   `rounded-xl`, `text-[13px]` for body, `text-[12px]` for meta).
3. **Korean labels first.** Match existing convention: `상세정보`
   `로그` `쉘` style for tab names; full Korean prose in dialogs and
   tooltips. English only for technical terms inside code-style
   monospace cells (image tags, ports).

### UI/UX — Mode 1 (Diff preview) flow

Stage-by-stage, every visible state is specified.

#### Stage 1 — entry point inside the stack drawer

The existing drawer's "에디터" tab gets a **right-aligned** "변경사항
미리보기" button next to the existing "저장" button. Pre-existing
"저장" stays as primary action; "미리보기" is the secondary button
(outline variant). Both `size="sm"`.

```
┌─ Stack: nginx-stack ───────────────────────────────────┐
│ [상세정보] [에디터] [로그]                              │
├─────────────────────────────────────────────────────────┤
│ ┌─────────────────────────────────────────────────────┐ │
│ │ services:                                           │ │
│ │   web:                                              │ │
│ │     image: nginx:1.25                               │ │  ← Monaco
│ │     ports:                                          │ │     (existing)
│ │       - "8080:80"                                   │ │
│ │ ...                                                 │ │
│ └─────────────────────────────────────────────────────┘ │
│                            [변경사항 미리보기] [저장]   │
└─────────────────────────────────────────────────────────┘
```

The "미리보기" button is **disabled when the editor value equals the
last-saved value** (no diff to show). Tooltip on disabled state:
"변경사항이 없습니다".

#### Stage 2 — DiffSheet (slide-over from right)

Clicking "미리보기" opens a shadcn `Sheet` (right-side, 640px wide on
desktop, full-width on mobile). Inside the Sheet:

```
┌─ Sheet: 변경사항 미리보기 ──────────────────────[ × ]─┐
│                                                       │
│ ┌─ DiffSummaryHeader ──────────────────────────────┐  │
│ │  + 추가 0    ~ 변경 1    − 삭제 0                │  │
│ │   (가로 3-counter, color-coded:                  │  │
│ │    추가=emerald, 변경=blue, 삭제=destructive)    │  │
│ └──────────────────────────────────────────────────┘  │
│                                                       │
│ ┌─ Tabs ───────────────────────────────────────────┐  │
│ │ [카테고리]  [원본 텍스트]                        │  │
│ ├──────────────────────────────────────────────────┤  │
│ │ 카테고리 탭 (default):                           │  │
│ │                                                  │  │
│ │ 🐳 이미지        1 변경 ▾  ← Accordion 펼침/접기 │  │
│ │   ┌────────────────────────────────────────┐    │  │
│ │   │ web                                    │    │  │
│ │   │   nginx:1.24  →  nginx:1.25            │    │  │
│ │   └────────────────────────────────────────┘    │  │
│ │                                                  │  │
│ │ 🔌 포트          0 변경                          │  │ ← 0개면 회색 비활성
│ │ 📦 볼륨          0 변경                          │  │
│ │ 🔧 환경변수      0 변경                          │  │
│ │ ↻  restart       0 변경                          │  │
│ │ ❤  healthcheck   0 변경                          │  │
│ └──────────────────────────────────────────────────┘  │
│                                                       │
│                       [닫기]    [이대로 적용]          │
└───────────────────────────────────────────────────────┘
```

**Behavior detail:**

- 변경 0개인 카테고리는 행이 흐리게 (`text-muted-foreground`),
  Accordion 헤더 클릭해도 빈 상태 메시지 ("변경 사항 없음").
- 변경 ≥ 1개인 카테고리는 default expanded (사용자가 무엇이 바뀌는지
  바로 보이게). 0개인 카테고리는 default collapsed.
- 카테고리별 아이콘은 lucide-react: `Container, Network, HardDrive,
  Settings, RotateCw, Heart`.
- "이대로 적용" 버튼은 primary, 클릭 시 Sheet 닫고 기존 UpdateStack
  flow로 진행 (Sheet에서 적용 안 함 — 사용자가 변경 의도를 한 번 더
  확인하는 흐름).

#### Stage 3 — service row visual

Per-service change rendering inside a category. Examples:

| Category | Change type | Visual |
|---|---|---|
| image | tag bump | `web    nginx:1.24 → nginx:1.25` (mono font, "→" arrow gray) |
| ports | added | `api    + "8081:80"` (emerald `+`) |
| ports | removed | `db    − "5432:5432"` (destructive `−`) |
| ports | mixed | service shows both `+` and `−` rows stacked |
| env | added | `db    + LOG_LEVEL=debug` |
| env | removed | `db    − DEBUG=true` |
| restart | changed | `web    no → unless-stopped` |
| healthcheck | added | `web    + healthcheck (interval 30s, retries 3)` (block summarized) |
| service added | n/a | inside summary banner: `+ 새 서비스 추가: cache` |
| service removed | n/a | `− 서비스 삭제: legacy-api` |

Every change row uses **one-line layout**:

- Service name (12 chars max, truncate with ellipsis): `text-[12px] font-medium`, fixed left padding.
- Change content: `font-mono text-[12px]`, color = role-based (added=emerald, removed=destructive, modified=foreground).

#### Stage 4 — raw text tab

`[원본 텍스트]` 탭 = Monaco **DiffEditor** (`@monaco-editor/react`'s
`DiffEditor`, already part of the dependency). 400px height,
read-only, side-by-side. Leverages existing Monaco theme so it matches
the editor next door.

#### Stage 5 — empty / loading / error states

| State | Visual |
|---|---|
| Loading (request in flight) | Sheet content area shows `<Skeleton>` rows × 3 (existing shadcn `Skeleton`). 200ms minimum so it doesn't flash. |
| Diff = no changes (edge case: user clicked but YAMLs match exactly) | Sheet body shows centered: 🟢 아이콘 + `변경 사항이 없습니다` + close button only. No tabs. |
| Backend error (network / 500) | Sheet shows red banner with `미리보기를 불러올 수 없습니다` + 한 줄 사유 (sanitized) + `다시 시도` button. |
| Invalid YAML in editor | Sheet auto-closes; Monaco gets red squiggle at the line/column from the API response (existing marker pattern). Toast notification: `YAML 구문 오류 (3행 12열)`. |

### UI/UX — Mode 2 (Git import) flow

#### Stage 1 — entry point in "새 스택" dialog

The existing "+ 새 스택" button opens a shadcn `Dialog`. Dialog content
gets a top-level shadcn `Tabs` with two values:

```
┌─ Dialog: 새 스택 추가 ──────────────────────[ × ]─┐
│                                                   │
│ [수동 작성] [git에서 가져오기]                     │
├───────────────────────────────────────────────────┤
│ (수동 작성 탭은 기존 폼 그대로 — 변경 없음)       │
└───────────────────────────────────────────────────┘
```

Default tab = `수동 작성` (existing flow, unchanged). The new
`git에서 가져오기` tab is to the right.

#### Stage 2 — git import form

When `git에서 가져오기` tab is selected:

```
┌─ git에서 가져오기 ─────────────────────────────────┐
│                                                    │
│ GitHub repo URL  *                                 │
│ ┌────────────────────────────────────────────────┐ │
│ │ https://github.com/user/repo.git               │ │
│ └────────────────────────────────────────────────┘ │
│   GitHub HTTPS URL만 지원 (.git 확장자 권장)       │
│                                                    │
│ branch                              path           │
│ ┌──────────────────┐    ┌─────────────────────┐   │
│ │ main             │    │ docker-compose.yml  │   │
│ └──────────────────┘    └─────────────────────┘   │
│                                                    │
│ Personal Access Token (private repo만 필요)        │
│ ┌────────────────────────────────────────────────┐ │
│ │ ghp_...                                  [👁]  │ │  ← password input
│ └────────────────────────────────────────────────┘ │     show/hide toggle
│   토큰은 한 번만 사용되고 저장되지 않습니다 ⓘ      │
│                                                    │
│ stack 이름 *                                       │
│ ┌────────────────────────────────────────────────┐ │
│ │ my-stack                                       │ │
│ └────────────────────────────────────────────────┘ │
│   소문자/숫자/하이픈만, 1-50자                      │
│                                                    │
│                                  [취소]  [가져오기]│
└────────────────────────────────────────────────────┘
```

**Behavior:**

- URL field: real-time regex validation (`^https://github\.com/.+/.+(\.git)?$`).
  Invalid → 빨간 helper text 즉시 표시, "가져오기" 버튼 disabled.
- Stack 이름: real-time uniqueness check via `GET /compose/stacks`
  (debounced 300ms). 충돌 시 빨간 helper.
- "가져오기" 버튼: 클릭 시 spinner + 버튼 텍스트 → `가져오는 중…`,
  버튼 disabled 상태로 변경.
- Token field: shadcn `Input` with `type="password"` + 우측 toggle
  아이콘으로 평문 표시 옵션 (one-shot 입력 보조). 빈 값 허용.

#### Stage 3 — import progress / result

Single button click → backend clone + read + create. Latency expected
< 5s for shallow clone of typical compose repos. UI states:

| Phase | Visual |
|---|---|
| Submitting (POST in flight) | 버튼 spinner + 폼 disabled |
| Success | Dialog 닫기 → toast `'<name>' 스택을 가져왔습니다`. URL 라우팅: 새로 생긴 stack의 detail drawer 자동 열기 (`?selected=<name>`). |
| Error: `GIT_AUTH_FAILED` | 폼 유지, Token 필드 위에 빨간 banner: `인증 실패. PAT가 필요한 private 저장소입니다.` |
| Error: `GIT_REPO_NOT_FOUND` | URL 필드 아래 빨간 banner: `저장소를 찾을 수 없습니다.` |
| Error: `GIT_PATH_NOT_FOUND` | path 필드 아래 빨간 banner: `해당 경로의 파일이 없습니다.` |
| Error: `INVALID_YAML` | path 필드 아래: `compose YAML 형식이 올바르지 않습니다.` |
| Error: `STACK_ALREADY_EXISTS` | 이름 필드 아래: `이미 존재하는 스택 이름입니다.` |
| Error: 기타 (5xx / timeout) | 폼 하단 빨간 banner with sanitized reason. 한 줄. |

### UI/UX — keyboard / accessibility

- Sheet에서 `Esc` = 닫기 (shadcn 기본 동작).
- Dialog에서 `Esc` = 취소.
- "변경사항 미리보기" 버튼은 `aria-label="변경사항 미리보기"`.
- DiffSummaryHeader 카운터는 `<span role="status">`로 보조기 사용자가
  변경 규모 즉시 파악 가능.
- 모든 색상 차이는 텍스트 prefix (`+`, `−`, `→`)로도 표현 — 색맹
  사용자 안전.

### UI/UX — what we are NOT doing

(Document the YAGNI cuts so reviewers don't ask.)

- ❌ Inline diff inside Monaco itself (replacing current editor with a
  diff view). 사용자가 편집 중인 코드 위에 diff overlay는 정신없음 — Sheet 분리가 깔끔.
- ❌ Auto-diff (typing 후 debounce). 매번 backend round-trip은 noise. 명시적 버튼 클릭 트리거가 의도 명확.
- ❌ Diff 내에서 직접 cherry-pick 적용 (한 카테고리만 적용). complexity vs. 가치 = 낮음. 전체 적용 / 취소만.
- ❌ Diff 결과 저장 / 공유. 1회성 검토 도구.
- ❌ Import 후 git 정보 stack에 표시. Mode 2는 import 시점에 git 끊김 (Mode 3 영역).

## Data flow

### Mode 1: Diff preview

```
Operator edits Monaco → clicks "변경사항 미리보기"
   → frontend: api.diffStack(project, currentEditorValue)
      → POST /compose/:project/diff
         → handler reads stacks/<project>/docker-compose.yml
         → ComputeDiff(deployed, proposed)
         → JSON response
   → DiffPanel renders categorized + raw diff
Operator reviews → clicks "적용" → existing UpdateStack flow
   (no change to existing apply path)
```

### Mode 2: Git import

```
Operator: 새 stack 추가 → "git에서 가져오기" 탭
   → fills url + (optional) token + branch + path + name
   → frontend: api.importFromGit({url, branch, path, token, name})
      → POST /compose/import
         → validate
         → temp clone via go-git (shallow, depth=1)
         → read compose YAML from temp
         → create <stacks_path>/<name>/docker-compose.yml
         → CreateStack via existing helper → audit/cluster broadcast
         → wipe temp
         → return {project_name: <name>}
   → frontend redirects to stack detail page (regular stack now)
```

## API contracts

### POST /api/v1/compose/:project/diff

Request:
```json
{ "yaml": "version: '3.8'\nservices:\n  web:\n    image: nginx:1.25\n..." }
```

Response 200:
```json
{
  "success": true,
  "data": {
    "summary": { "added": 0, "modified": 1, "removed": 0 },
    "by_category": {
      "image":   [{ "service": "web", "from": "nginx:1.24", "to": "nginx:1.25" }],
      "ports":   [],
      "volumes": [],
      "env":     [],
      "restart": [],
      "healthcheck": []
    },
    "raw_diff": "--- deployed\n+++ proposed\n@@ -3,1 +3,1 @@\n-    image: nginx:1.24\n+    image: nginx:1.25\n"
  }
}
```

Errors:
- `400 INVALID_YAML` — proposed YAML doesn't parse. Body includes `line`,
  `column` for Monaco squiggle.
- `404 STACK_NOT_FOUND` — `:project` doesn't exist on this node.

### POST /api/v1/compose/import

Request:
```json
{
  "url": "https://github.com/grafana/grafana-docker.git",
  "branch": "main",
  "path": "docker-compose.yml",
  "token": "ghp_xxx",
  "name": "grafana"
}
```

Response 200:
```json
{ "success": true, "data": { "project_name": "grafana" } }
```

Errors:
- `400 INVALID_REQUEST` — URL not GitHub HTTPS, name invalid, etc.
- `401 GIT_AUTH_FAILED` — clone returned auth error (private repo +
  missing/wrong token).
- `404 GIT_REPO_NOT_FOUND` — clone returned 404.
- `404 GIT_PATH_NOT_FOUND` — repo cloned OK but `<path>` doesn't exist.
- `400 INVALID_YAML` — file at `<path>` not valid compose YAML.
- `409 STACK_ALREADY_EXISTS` — `<name>` already taken on this node.
- `500 GIT_CLONE_FAILED` — clone failed for other reasons (network
  timeout, SSL, etc.). Error message sanitized via
  `response.SanitizeOutput` so token doesn't leak.

## Error handling

**Diff:**

- Invalid YAML → return 400 with `line`/`column`. Frontend feeds to
  Monaco's marker API (the existing flow already does this for compose
  validation).
- One service has a malformed sub-block (e.g., `ports` is a string
  instead of array): degrade to "raw text only" for that service,
  return 200 (don't kill the entire diff).

**Import:**

- Network/clone errors are bounded: `git_import.go` uses
  `context.WithTimeout(ctx, 30*time.Second)` for the clone.
- Temp dir is `os.MkdirTemp("", "sfpanel-git-import-*")`, always
  removed via `defer os.RemoveAll`. Even on panic.
- Token never logged. The `slog.Info("git import: clone start", ...)`
  log line includes URL but masks token. Confirm by `grep -n "token"`
  on the implementation before merging.
- Stack name collision: 409 with the existing project's display name,
  not the disk path (which might leak structure).

## Cluster awareness

Both endpoints land under `authorized` group. `ClusterProxyMiddleware`
handles `?node=<id>` automatically — single-shot JSON responses, no
streaming, no `-stream` suffix needed.

The diff endpoint operates on per-node disk state (the deployed YAML
lives where the stack lives). Import creates files on the local node.
This is the same per-node model as existing compose features.

## Testing

| Required | Why |
|---|---|
| `ComputeDiff` table tests covering all 6 categories | Pure function, easy. Locks the contract for the frontend. |
| `ComputeDiff` malformed input (non-YAML, missing services key) | Defines error behavior. |
| `git_import.go` happy path with a local bare repo (`memfs` git fixture) | Covers the clone + read + write path without external network. |
| `git_import.go` invalid URL / invalid YAML / collision | Defines all error paths. |
| Handler tests for `DiffStack` and `ImportFromGit` (mock filesystem) | Locks API contract. |

| Not required | Why |
|---|---|
| End-to-end real GitHub integration test | Requires live network, secret PAT, brittle. Manual smoke at Task 22 covers it. |
| DiffPanel unit tests | UI-glue; manual smoke is enough. |

## Future-proofing notes

- The `git_import.go` clone flow is structured so a future Phase 2 can
  reuse it: change `Depth: 1` to `Depth: 0`, add a stored token lookup,
  and the same code becomes the polling fetcher.
- The `DiffResult` shape includes `raw_diff` from day one so a future
  "always show raw" preference doesn't require a contract change.
- Six categories are explicit in code (no reflection / generic walker)
  so adding a 7th (e.g., `networks`) is a localized change.

## Approval

Approved by user 2026-05-04. Proceed to writing-plans.
