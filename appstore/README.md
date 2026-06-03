# SFPanel App Store — Contributor Guide / 기여 가이드

This is the catalog behind SFPanel's one-click app installs. Adding an app is
mostly: scaffold two files, fill them in, add the id to `index.json`, and run
the validation test. No backend changes needed.

이 디렉터리는 SFPanel 원클릭 설치에 쓰이는 앱 카탈로그입니다. 새 앱 추가는
파일 2개 생성 → 작성 → `index.json` 등록 → 검증 테스트 실행 순서로 끝납니다.

---

## Directory layout / 디렉터리 구조

```
appstore/
├── index.json          # Array of app ids. An app is invisible until listed here.
├── categories.json     # Category definitions (id + bilingual name + lucide icon)
├── README.md           # This file
└── apps/
    └── <app-id>/
        ├── metadata.json       # Required — app metadata (schema below)
        ├── docker-compose.yml  # Required — the stack to deploy
        └── icon.svg            # Optional — used if metadata.json has no "icon" URL
```

**App id rules:** lowercase letters, digits, `-`, `_`; max 50 chars; must start
with a letter or digit. Regex: `^[a-z0-9][a-z0-9_-]{0,49}$`. The id MUST equal
the folder name and the `"id"` in `metadata.json`.

---

## metadata.json schema

| Field | Type | Req | Notes |
|-------|------|-----|-------|
| `id` | string | yes | Equals folder name; matches the id-regex above. |
| `name` | string | yes | Display name. |
| `description` | `{ko, en}` | yes | Both `ko` and `en` required. |
| `category` | string | yes | Must be an `id` in `categories.json`. |
| `version` | string | yes | App Store package version (bump on edits). The *app's* version is auto-detected from `source`. |
| `website` | string | yes | Official site. |
| `source` | string | yes | GitHub repo. Used for version auto-detect + README rendering. |
| `icon` | string | no | Icon URL (svg/png). Omit to fall back to `apps/<id>/icon.svg`. |
| `ports` | `int[]` | yes | Fixed host ports the app exposes; each 1–65535. |
| `featured` | bool | no | `true` renders the app first with a "Featured" badge. |
| `screenshots` | `string[]` | no | Image URLs for the detail-page gallery. |
| `features` | object[] | no | Feature cards (≤4 recommended). |
| `env` | object[] | yes | Variables written into the stack's `.env`. |

**`features[]` item:**

| Field | Type | Notes |
|-------|------|-------|
| `title` | `{ko, en}` | Card title. |
| `description` | `{ko, en}` | Card body. |
| `icon` | string | Emoji (e.g. `🚀`). Optional. |

**`env[]` item:**

| Field | Type | Notes |
|-------|------|-------|
| `key` | string | Env var name (used as `${KEY}` in compose). |
| `label` | `{ko, en}` | Form field label. |
| `type` | string | One of `text`, `port`, `password`, `select`, `path` (empty = `text`). |
| `default` | string | Default value (pre-filled in the form). |
| `required` | bool | Whether the field must be filled. |
| `generate` | bool | `true` → a 32-char random value is generated at install time (use for passwords). |
| `options` | `string[]` | **Required when `type` is `select`** — the dropdown choices. |

**env `type` → UI:**

| type | UI |
|------|----|
| `text` | text input |
| `port` | numeric input + port-conflict check |
| `password` | masked input with reveal toggle |
| `select` | dropdown driven by `options` |
| `path` | text input for a host filesystem path |

---

## Add an app / 새 앱 추가

### 1. Scaffold

```bash
scripts/new-appstore-app.sh <app-id>
```

Creates `appstore/apps/<app-id>/metadata.json` and `docker-compose.yml` from a
commented template. It refuses to overwrite an existing folder.

### 2. Edit the files

- **metadata.json** — strip the placeholder `"// ..."` comment keys, then fill in
  every field per the schema above.
- **docker-compose.yml** — base it on the **project's official compose file**.
  Writing one from scratch tends to drop healthchecks / `depends_on` ordering and
  break installs. Minimal edits only:
  - Make the user-facing port an env var: `"${PORT:-8080}:80"`.
  - Wire `generate: true` passwords through as `${THE_KEY}`.
  - Convert host bind mounts (`./data:/data`) to named volumes.
  - Drop optional/profile services (SSO, office suites) to keep it simple.
  - **Keep** the original `healthcheck`, `depends_on`, `command`, and required
    `environment` defaults — don't "tidy" these away.
  - Use an app-specific `container_name` prefix to avoid clashes (e.g. `npg-db`).

### 3. Register in index.json

Add `<app-id>` to `appstore/index.json` (alphabetical order preferred). **An app
not listed here is never shown.**

### 4. Validate

```bash
go test ./internal/feature/appstore/
```

`TestCatalogValid` checks: every indexed app has a folder with `metadata.json` +
`docker-compose.yml`; metadata parses and its `id` matches the folder; `name`
non-empty; `description` has `ko` + `en`; `category` exists; `version` non-empty;
ports in range; env types valid and `select` has `options`; no orphan folders;
`index.json` / `categories.json` parse. CI runs this on every catalog change.

### 5. Open a PR

Reference any related app-request issue.

> Not comfortable with a PR? Open a request via **Issues → App request**
> (`.github/ISSUE_TEMPLATE/app-request.yml`) and a maintainer can package it.

---

## Categories / 카테고리

Current categories (from `categories.json`):

| id | 한글 | English |
|----|------|---------|
| `media` | 미디어 | Media |
| `cloud` | 클라우드 | Cloud |
| `security` | 보안 | Security |
| `monitoring` | 모니터링 | Monitoring |
| `dev` | 개발 | Development |
| `productivity` | 생산성 | Productivity |
| `automation` | 자동화 | Automation |
| `communication` | 커뮤니케이션 | Communication |
| `database` | 데이터베이스 | Database |
| `network` | 네트워크 | Network |

Need a new one? Add it to `categories.json` (`icon` is a
[lucide](https://lucide.dev) icon name):

```json
{ "id": "new-category", "name": { "ko": "새 카테고리", "en": "New Category" }, "icon": "Folder" }
```

---

## Reference apps / 참고할 만한 기존 앱

- **Simple, few env vars:** `apps/uptime-kuma/`
- **Auto-generated password:** `apps/vaultwarden/` (`generate: true`)
- **Multi-service + DB:** `apps/nextcloud/`, `apps/immich/`
- **Multiple ports:** `apps/nginx-proxy-manager/`

---

## How SFPanel consumes this catalog / 동작 방식

1. **Cache** — `index.json` + each `metadata.json` are fetched via raw URL
   (1-hour cache, no GitHub API). A catalog-only commit to `main` reaches every
   panel within the TTL — no release needed.
2. **Version detect** — the latest tag is read from `source`'s
   `/releases/latest` redirect.
3. **README** — the `source` repo's `README.md` is rendered on the detail page
   (tries `main` → `master` → `develop`).
4. **Pre-install checks** — port conflicts, container-name conflicts, and an
   existing stack directory are checked before install.
5. **Install** — `docker-compose.yml` is downloaded to `/opt/stacks/<app-id>/`,
   `.env` is generated from the `env` defs, then `docker compose pull` + `up -d`
   stream over SSE.
