# Appstore Logic Hardening — Design

**Date:** 2026-06-04
**Status:** Approved (brainstorm), pending implementation plan
**Scope:** Harden the App Store catalog/cache layer, deepen install lifecycle, and surface version info in the Docker update flow.

## Goal

Make the App Store resilient when the upstream catalog (GitHub raw) is slow/unreachable, fix the no-op refresh button, give installed apps an update-available signal (acted on from the Docker menu), let operators uninstall without destroying data, and show real version deltas when updating a stack.

## Background (how it works today)

- Catalog lives in the main repo under `/appstore` and is fetched at runtime from
  `https://raw.githubusercontent.com/svrforum/SFPanel/main/appstore/`.
- `internal/feature/appstore/handler.go`:
  - `ensureCache()` → in-mem (1h TTL) → `loadCacheFromDB()` → `refreshCache()`.
  - `refreshCache()` does `1 + 1 + N` HTTP GETs (categories.json, index.json, then one metadata.json per app — **89 apps** as of this change). Per-app fetch failures are logged at warn and **silently dropped**, and the shrunken result is persisted over the good DB cache.
  - `loadCacheFromDB()` **discards** any cached catalog older than the 1h TTL — so an offline panel whose last fetch was >1h ago serves an empty store (500).
  - `RefreshCache()` → `refreshCache()` early-returns if the cache is still <1h fresh, so the manual "refresh" button is a **no-op** unless the cache is already expired.
- Install (`InstallApp`, SSE) is already well-hardened: body cap, newline-injection reject, atomic writes, `os.Mkdir` EEXIST race guard, single-`ss` port snapshot, container-name conflict, advanced re-auth + `validateAdvancedCompose`, cleanup-on-failure, detached 10-min context. **No changes needed here except B7/C8 below.**
- Uninstall (`UninstallApp`) always runs `down -v --remove-orphans` (destroys volumes) then `RemoveAll`.
- Docker/compose update infra already exists:
  - `internal/docker/compose.go`: `CheckStackUpdates` (per-image), `UpdateStack` (rollback-save → `pull` → `up -d --force-recreate`), `RollbackStack`.
  - `internal/docker/client.go:CheckImageUpdate` computes the **remote digest** via `DistributionInspect` (line ~590) and compares against local `RepoDigests`, but only returns a `HasUpdate` bool — the remote digest and tag are discarded.
- App Store installs and the compose manager share the **same** stack root: both use `cfg.Server.StacksPath` (`router.go:105` appstore `ComposePath`, `router.go:128` `NewComposeManager`). An installed app at `<StacksPath>/<id>/` is therefore a normal compose project visible/managed in the Docker menu.

## Non-goals

- No separate "update" action inside the App Store (the Docker menu owns updates; the store only shows a badge).
- No custom/arbitrary app install from the store UI (Docker → Compose already covers user-supplied stacks).
- No stored per-app image digest (update detection uses the live compose check).
- A digest-diff "update available" badge in the Docker stack **list** is out of scope here (the per-stack check already exists); we only enrich the update **dialog**.

---

## A. Catalog resilience

### A1. Single bundled `catalog.json`

**Source of truth stays per-app** (`appstore/apps/<id>/metadata.json` + `categories.json`). A build step concatenates them into one `appstore/catalog.json`:

```json
{
  "generated_at": "<RFC3339, stamped by the generator>",
  "categories": [ ... ],
  "apps": [ <full AppStoreMeta>, ... ]
}
```

- **Generator:** `make appstore-catalog` runs a small Go program (`cmd/appstore-catalog/` or a `go:generate`-able helper) that reads `index.json` + every app's `metadata.json` + `categories.json`, sorts apps by id, and writes `catalog.json`. `generated_at` is the only volatile field.
- **Honesty guard:** `catalog_test.go` gains `TestCatalogBundleUpToDate` — it regenerates the bundle in memory (ignoring `generated_at`) and fails if it differs from the committed `catalog.json`. CI then rejects any app edit that forgot `make appstore-catalog`.
- **Panel fetch:** `refreshCache()` fetches `catalog.json` (one GET). On HTTP 404 (legacy `main` without the bundle), it falls back to the current categories+index+per-app walk. Compose YAML and README stay fetched on-demand in `GetApp`/`InstallApp` (unchanged).
- **Atomicity:** one file → the catalog is all-or-nothing; the partial-catalog-overwrites-good-cache failure mode disappears.

### A2. Serve-stale-on-error

- `loadCacheFromDB()`: split into "fresh load" (used by `ensureCache` fast path, keeps the TTL gate) and "last-resort load" (ignores age). Add a `stale bool` to the in-mem cache state set true when the loaded cache is past TTL.
- `refreshCache()`: if the network fetch fails **and** a cache exists (in-mem or DB), log a warn, keep the existing cache, set `stale=true`, and return `nil` (no 500). Only return an error when there is **no** cache at all to serve.
- API: `ListApps`/`GetCategories` responses gain a top-level `stale` flag (and `cached_at`). When `stale`, the frontend shows a quiet inline banner ("오프라인 — 캐시된 카탈로그 표시 중") and the refresh button stays enabled.

### A3. Force refresh

- `refreshCache(force bool)`: when `force`, skip the "cache still valid" early-returns and re-fetch unconditionally.
- `RefreshCache` handler calls `refreshCache(true)`.
- Force fetch appends a cache-bust query (`?v=<unix-minute>`) to the `catalog.json` URL to sidestep the ~5-min raw.githubusercontent CDN window.

---

## B. Lifecycle depth

### B4. App Store "update available" badge

- **Reuse the existing endpoint** — `POST /compose/:project/check-updates` (`composeHandler.CheckStackUpdates`, `router.go:562`) already returns `StackUpdateCheck { has_updates, images }`. The store frontend calls it with `project = appID` (an installed app *is* a compose stack at `<StacksPath>/<id>`). **No new appstore backend endpoint.** This keeps the store handler unchanged for B4 and inherits the `?node=` proxy + Docker-absent graceful handling already in the compose path.
- Frontend: on the App Store detail and the installed-apps view, installed apps fire `check-updates` (lazily, debounced, best-effort) and render an "업데이트 있음" badge when `has_updates`. The badge's action **deep-links** to the Docker stack page (`/docker/stacks/<id>`) — no update is performed in the store.
- If the call errors (Docker unavailable, stack not yet scanned), the frontend simply shows no badge — never a blocking error.

### B5. Current → target version in the Docker update flow

- Extend `ImageUpdateStatus` (`internal/docker/client.go`):

  ```go
  type ImageUpdateStatus struct {
      Image         string `json:"image"`
      Tag           string `json:"tag,omitempty"`            // parsed from Image ref ("latest" if none)
      CurrentID     string `json:"current_id"`               // existing: local image ID short
      CurrentDigest string `json:"current_digest,omitempty"` // local RepoDigest short
      RemoteDigest  string `json:"remote_digest,omitempty"`  // DistributionInspect digest short (was discarded)
      CurrentCreated string `json:"current_created,omitempty"` // local image Created (RFC3339)
      HasUpdate     bool   `json:"has_update"`
      Error         string `json:"error,omitempty"`
  }
  ```

  `CheckImageUpdate` already computes `remoteDigest` and `localInspect.RepoDigests` — populate the new fields from data it already has (no extra registry calls). `Tag` parsed from the image ref; bare refs default to `latest`. `CurrentCreated` from `localInspect.Created`.
- Frontend: the existing update/check dialog renders, per image: `repo:tag` and `current_digest → remote_digest` (short shas), highlighting rows where `has_update`. When `current_digest`/`remote_digest` are unavailable (private registry, inspect error) it falls back to the existing `has_update`/error display. No change to `UpdateStack` or rollback.

### B6. Keep-data uninstall

- `UninstallApp` reads `keep_data := c.QueryParam("keep_data") == "true"`. Compose teardown becomes `down --remove-orphans` plus `-v` **only when not** `keep_data`.
- Frontend uninstall confirm dialog gains a "데이터 볼륨 유지" checkbox (default **unchecked** → current `-v` behavior preserved). Copy makes clear that unchecking deletes all app data.

### B7. Post-install health poll

- After the `up -d` success branch in `InstallApp`, before sending the final `done`, run a bounded poll (≤ ~15s, `installCtx`):
  - `docker compose -f <compose> ps --format json` → if every service is `running`/`healthy`, emit SSE `health: healthy`.
  - If any service has a `health` of `starting` or is `restarting`, emit `health: starting` ("아직 시작 중") and stop polling at the cap.
- The final `done` event carries a `health` field (`healthy` | `starting` | `unknown`). The install-success screen reflects it: "정상 실행 중 — 열기" vs "시작하는 중일 수 있어요 — 잠시 후 확인". Health failure never fails the install (the stack is up); it only annotates the result.

---

## C. Accuracy / polish

### C8. Drop the placeholder version display

- `appStoreInstallRecord.Version` is `metadata.version` (always `"1.0.0"`, a catalog placeholder). Stop surfacing it as an "app version". The installed-apps UI shows "설치일 `<InstalledAt 날짜>`" instead. The stored record field stays (back-compat) but is no longer presented as a version. Update-available now comes from B4's live check.

### C9. Document CDN propagation

- Add a short note to `appstore/README.md`: catalog edits on `main` take up to ~5 minutes to propagate through the raw.githubusercontent CDN, and the panel additionally caches for 1h (use the forced refresh button to pull immediately).

---

## Components touched

| File | Change |
|------|--------|
| `cmd/appstore-catalog/main.go` (new) | Bundle generator |
| `Makefile` | `appstore-catalog` target |
| `appstore/catalog.json` (new, generated) | Bundled catalog |
| `internal/feature/appstore/catalog_test.go` | `TestCatalogBundleUpToDate` guard |
| `internal/feature/appstore/handler.go` | catalog.json fetch + fallback, serve-stale + `stale` flag, `refreshCache(force)`, `keep_data` uninstall, post-install health poll, drop version display |
| `internal/api/router.go` | no new route (B4 reuses `POST /compose/:project/check-updates`) |
| `internal/docker/client.go` | extend `ImageUpdateStatus`, populate new fields in `CheckImageUpdate` |
| `web/src/pages/AppStore.tsx`, `AppStoreDetail.tsx` | stale banner, update-available badge + deep-link, keep-data checkbox, "설치일" copy |
| `web/src/lib/api.ts`, `web/src/types/api.ts` | `update-check`, `keep_data`, new `ImageUpdateStatus` fields, `stale` |
| Docker stack update dialog (compose UI) | render current → target digests |
| `appstore/README.md` | CDN propagation note |
| `web/src/i18n/locales/{en,ko}.json` | new strings (parity) |

## Testing

- `TestCatalogBundleUpToDate` — bundle matches per-app sources (ignoring `generated_at`).
- Existing `TestCatalogValid` — unchanged, still validates per-app files.
- `refreshCache` serve-stale: with `MockCommander`/injected HTTP failure, assert the prior cache + `stale=true` survive a failed refresh and no error is returned when a cache exists.
- `refreshCache(force=true)` re-fetches even when the cache is fresh.
- `CheckImageUpdate` populates `CurrentDigest`/`RemoteDigest`/`Tag` (table-driven with a fake docker client).
- `UninstallApp` keep-data: assert `down` args include `-v` only when `keep_data` is unset (MockCommander argument assertion).
- Post-install health: parse `compose ps --format json` → health classification (unit-test the classifier, not the live docker call).

## Risks / rollout

- **Bundle staleness blocking CI:** mitigated by the `make appstore-catalog` target + a clear failure message from the guard test telling the contributor to run it. The `new-appstore-app.sh` scaffold script gets a closing reminder to run the target.
- **Legacy panels / pre-bundle `main`:** the 404 fallback keeps old panels working until the bundle lands; new panels prefer the bundle.
- **Private-registry images** (`DistributionInspect` fails): B5 fields are best-effort — UI falls back to the existing has-update/error path.
- Deploy is build + binary swap on node 203 (per the standard flow); the catalog bundle ships via the normal `main` push.
