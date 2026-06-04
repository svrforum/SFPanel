# Appstore Logic Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the App Store resilient when the upstream catalog is slow/unreachable, fix the no-op refresh, surface an update-available badge (acted on in the Docker menu), allow keep-data uninstall, show current→target image digests when updating a stack, and add a post-install health signal.

**Architecture:** Catalog source-of-truth stays per-app (`appstore/apps/<id>/metadata.json`); a `make` target bundles it into one deterministic `appstore/catalog.json` that the panel fetches in a single GET (legacy per-app walk kept as a 404 fallback). The fetch path gains serve-stale-on-error + a force flag. Install/uninstall gain a health poll and a keep-data option. The existing Docker `CheckImageUpdate` already computes the remote digest but discards it — we surface it. The App Store reuses the existing `POST /docker/compose/:project/check-updates` for its badge (an installed app *is* a compose stack at the shared `StacksPath`).

**Tech Stack:** Go (echo, database/sql, log/slog, `internal/common/exec.Commander`), React/TypeScript (shadcn/ui, i18next), Docker SDK.

**Spec:** `docs/superpowers/specs/2026-06-04-appstore-logic-hardening-design.md`

**Conventions (this repo):**
- Commit author/committer must be `svrforum`: prefix every commit with `GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com`. No AI references in messages.
- Run `go build ./...` and `go test ./...` from `/opt/stacks/SFPanel`.
- Frontend: `cd web && npx tsc --noEmit` to type-check; new i18n keys go in **both** `web/src/i18n/locales/en.json` and `ko.json`.

---

## Phase A — Catalog resilience

### Task A1: Catalog bundle builder + generator + Makefile target

**Files:**
- Create: `internal/feature/appstore/bundle.go`
- Create: `cmd/appstore-catalog/main.go`
- Modify: `Makefile` (after the `test:` target, ~line 40)
- Generate: `appstore/catalog.json`

- [ ] **Step 1: Write the failing test for the builder**

Create `internal/feature/appstore/bundle_test.go`:

```go
package appstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildCatalogBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "categories.json"),
		[]byte(`[{"id":"media","name":{"en":"Media"},"icon":"m"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`["b","a"]`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		appDir := filepath.Join(dir, "apps", id)
		if err := os.MkdirAll(appDir, 0755); err != nil {
			t.Fatal(err)
		}
		meta := `{"id":"` + id + `","name":"` + id + `","category":"media","version":"1.0.0","ports":[80]}`
		if err := os.WriteFile(filepath.Join(appDir, "metadata.json"), []byte(meta), 0644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := BuildCatalogBundle(dir)
	if err != nil {
		t.Fatalf("BuildCatalogBundle: %v", err)
	}
	var bundle struct {
		Categories []json.RawMessage `json:"categories"`
		Apps       []json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(out, &bundle); err != nil {
		t.Fatalf("bundle not valid JSON: %v", err)
	}
	if len(bundle.Categories) != 1 || len(bundle.Apps) != 2 {
		t.Fatalf("got %d cats %d apps, want 1/2", len(bundle.Categories), len(bundle.Apps))
	}
	// Apps must be sorted by id (a before b) regardless of index order.
	var first struct{ ID string `json:"id"` }
	_ = json.Unmarshal(bundle.Apps[0], &first)
	if first.ID != "a" {
		t.Fatalf("apps not sorted: first=%s want a", first.ID)
	}
	// Determinism: a second build is byte-identical.
	out2, _ := BuildCatalogBundle(dir)
	if string(out) != string(out2) {
		t.Fatal("BuildCatalogBundle is not deterministic")
	}
}
```

- [ ] **Step 2: Run it — fails (undefined: BuildCatalogBundle)**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestBuildCatalogBundle`
Expected: compile error `undefined: BuildCatalogBundle`.

- [ ] **Step 3: Implement the builder**

Create `internal/feature/appstore/bundle.go`:

```go
package appstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BuildCatalogBundle reads the per-app catalog under root (categories.json,
// index.json, apps/<id>/metadata.json) and returns the deterministic bundled
// catalog.json bytes: {"categories":[...],"apps":[...]} with apps sorted by id.
// Per-app metadata is embedded verbatim (json.RawMessage) so the source files
// stay the single source of truth. Used by `make appstore-catalog` and the
// TestCatalogBundleUpToDate guard.
func BuildCatalogBundle(root string) ([]byte, error) {
	catsRaw, err := os.ReadFile(filepath.Join(root, "categories.json"))
	if err != nil {
		return nil, fmt.Errorf("read categories.json: %w", err)
	}
	var categories []json.RawMessage
	if err := json.Unmarshal(catsRaw, &categories); err != nil {
		return nil, fmt.Errorf("parse categories.json: %w", err)
	}

	idxRaw, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("read index.json: %w", err)
	}
	var ids []string
	if err := json.Unmarshal(idxRaw, &ids); err != nil {
		return nil, fmt.Errorf("parse index.json: %w", err)
	}
	sort.Strings(ids)

	apps := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		mRaw, err := os.ReadFile(filepath.Join(root, "apps", id, "metadata.json"))
		if err != nil {
			return nil, fmt.Errorf("read %s metadata.json: %w", id, err)
		}
		if !json.Valid(mRaw) {
			return nil, fmt.Errorf("%s/metadata.json is not valid JSON", id)
		}
		// Compact to a canonical form so whitespace/indent differences in the
		// source files don't make the bundle non-deterministic.
		var canon bytes.Buffer
		if err := json.Compact(&canon, mRaw); err != nil {
			return nil, fmt.Errorf("compact %s metadata.json: %w", id, err)
		}
		apps = append(apps, json.RawMessage(canon.Bytes()))
	}

	bundle := struct {
		Categories []json.RawMessage `json:"categories"`
		Apps       []json.RawMessage `json:"apps"`
	}{categories, apps}

	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
```

- [ ] **Step 4: Run it — passes**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestBuildCatalogBundle`
Expected: PASS.

- [ ] **Step 5: Create the generator command**

Create `cmd/appstore-catalog/main.go`:

```go
// Command appstore-catalog regenerates appstore/catalog.json from the per-app
// catalog files. Run via `make appstore-catalog` after editing any app.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/svrforum/SFPanel/internal/feature/appstore"
)

func main() {
	root := "appstore"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	data, err := appstore.BuildCatalogBundle(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "appstore-catalog:", err)
		os.Exit(1)
	}
	out := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(out, data, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "appstore-catalog:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes)\n", out, len(data))
}
```

- [ ] **Step 6: Add the Makefile target**

In `Makefile`, add after the `test:` target block:

```makefile
appstore-catalog:
	go run ./cmd/appstore-catalog
```

- [ ] **Step 7: Generate the bundle and verify build**

Run:
```bash
cd /opt/stacks/SFPanel && make appstore-catalog && go build ./...
```
Expected: `wrote appstore/catalog.json (...)`, build clean. Confirm `appstore/catalog.json` exists and `python3 -c "import json;d=json.load(open('appstore/catalog.json'));print(len(d['apps']),'apps',len(d['categories']),'cats')"` prints `89 apps 10 cats`.

- [ ] **Step 8: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/bundle.go internal/feature/appstore/bundle_test.go cmd/appstore-catalog/main.go Makefile appstore/catalog.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: add bundled catalog.json generator + make target"
```

---

### Task A2: Stale-bundle CI guard

**Files:**
- Modify: `internal/feature/appstore/catalog_test.go`

- [ ] **Step 1: Add the guard test**

Append to `internal/feature/appstore/catalog_test.go` (it already imports `os` and `path/filepath`; add `bytes` to the import block):

```go
// TestCatalogBundleUpToDate fails if appstore/catalog.json is stale relative
// to the per-app source files. A contributor who edits an app must run
// `make appstore-catalog` and commit the regenerated bundle.
func TestCatalogBundleUpToDate(t *testing.T) {
	if _, err := os.Stat(catalogDir); os.IsNotExist(err) {
		t.Skipf("catalog directory %s not present; skipping", catalogDir)
	}
	want, err := os.ReadFile(filepath.Join(catalogDir, "catalog.json"))
	if err != nil {
		t.Fatalf("appstore/catalog.json missing or unreadable — run `make appstore-catalog` and commit: %v", err)
	}
	got, err := BuildCatalogBundle(catalogDir)
	if err != nil {
		t.Fatalf("BuildCatalogBundle: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("appstore/catalog.json is stale — run `make appstore-catalog` and commit the result")
	}
}
```

- [ ] **Step 2: Run it — passes (bundle just generated in A1)**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestCatalogBundleUpToDate`
Expected: PASS.

- [ ] **Step 3: Prove the guard bites**

Run:
```bash
cd /opt/stacks/SFPanel
python3 -c "import json;d=json.load(open('appstore/catalog.json'));d['apps']=d['apps'][:-1];json.dump(d,open('appstore/catalog.json','w'),indent=2)"
go test ./internal/feature/appstore/ -run TestCatalogBundleUpToDate; echo "exit=$?"
make appstore-catalog   # restore
```
Expected: FAIL ("catalog.json is stale"), then restored.

- [ ] **Step 4: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/catalog_test.go appstore/catalog.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: guard catalog.json freshness in CI"
```

---

### Task A3: Fetch bundle with legacy fallback

**Files:**
- Modify: `internal/feature/appstore/handler.go` (constants ~131-138; `refreshCache` 191-268; callers at `ensureCache` 188 and `RefreshCache` 1069)

- [ ] **Step 1: Add the bundle constant**

In the `const (...)` block (after `appStoreBaseURL`):

```go
	appStoreBundleFile = "catalog.json"
```

- [ ] **Step 2: Split the existing per-app walk into a named fallback**

Rename the body of the current `refreshCache` (the part that fetches categories.json + index.json + per-app metadata, lines ~202-259, ending just before the `h.mu.Lock()` that stores results) into a new method that **returns** the data instead of storing it:

```go
// fetchCatalogLegacy is the pre-bundle fetch path: categories.json + index.json
// + one metadata.json per app (concurrency-limited). Kept as a fallback for a
// `main` that doesn't yet carry catalog.json.
func (h *Handler) fetchCatalogLegacy() ([]AppStoreCategory, []AppStoreMeta, error) {
	catData, err := h.httpGet(appStoreBaseURL + "categories.json")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch categories: %w", err)
	}
	cats := make([]AppStoreCategory, 0)
	if err := json.Unmarshal(catData, &cats); err != nil {
		return nil, nil, fmt.Errorf("parse categories: %w", err)
	}

	indexData, err := h.httpGet(appStoreBaseURL + "index.json")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch index: %w", err)
	}
	var appIDs []string
	if err := json.Unmarshal(indexData, &appIDs); err != nil {
		return nil, nil, fmt.Errorf("parse index: %w", err)
	}

	type metaResult struct {
		meta AppStoreMeta
		ok   bool
	}
	results := make([]metaResult, len(appIDs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i, appID := range appIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			metaData, err := h.httpGet(appStoreBaseURL + "apps/" + id + "/metadata.json")
			if err != nil {
				slog.Warn("skip app: fetch error", "component", "appstore", "app_id", id, "error", err)
				return
			}
			var meta AppStoreMeta
			if err := json.Unmarshal(metaData, &meta); err != nil {
				slog.Warn("skip app: parse error", "component", "appstore", "app_id", id, "error", err)
				return
			}
			if meta.ID == "" {
				meta.ID = id
			}
			results[idx] = metaResult{meta: meta, ok: true}
		}(i, appID)
	}
	wg.Wait()

	apps := make([]AppStoreMeta, 0)
	for _, r := range results {
		if r.ok {
			apps = append(apps, r.meta)
		}
	}
	return cats, apps, nil
}
```

- [ ] **Step 3: Add the bundle fetch**

```go
// fetchCatalogBundle fetches the single bundled catalog.json. When force is set
// a per-minute cache-bust query sidesteps the ~5-min raw.githubusercontent CDN
// window.
func (h *Handler) fetchCatalogBundle(force bool) ([]AppStoreCategory, []AppStoreMeta, error) {
	url := appStoreBaseURL + appStoreBundleFile
	if force {
		url += fmt.Sprintf("?v=%d", time.Now().Unix()/60)
	}
	data, err := h.httpGet(url)
	if err != nil {
		return nil, nil, err
	}
	var bundle struct {
		Categories []AppStoreCategory `json:"categories"`
		Apps       []AppStoreMeta     `json:"apps"`
	}
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, nil, fmt.Errorf("parse catalog bundle: %w", err)
	}
	return bundle.Categories, bundle.Apps, nil
}
```

- [ ] **Step 4: Rewrite `refreshCache` to take `force` and prefer the bundle (serve-stale lands in A4 — for now keep returning the error)**

Replace the `refreshCache` signature and head:

```go
func (h *Handler) refreshCache(force bool) error {
	h.refreshing.Lock()
	defer h.refreshing.Unlock()

	if !force {
		h.mu.RLock()
		valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
		h.mu.RUnlock()
		if valid {
			return nil
		}
	}

	cats, apps, err := h.fetchCatalogBundle(force)
	if err != nil {
		slog.Warn("appstore: bundle fetch failed, falling back to per-app walk",
			"component", "appstore", "error", err)
		cats, apps, err = h.fetchCatalogLegacy()
	}
	if err != nil {
		return err
	}

	h.mu.Lock()
	h.categories = cats
	h.apps = apps
	h.cachedAt = time.Now()
	go h.persistCache()
	h.mu.Unlock()
	return nil
}
```

- [ ] **Step 5: Update the two callers**

`ensureCache` (line ~188): `return h.refreshCache()` → `return h.refreshCache(false)`.
`RefreshCache` handler (line ~1069): `if err := h.refreshCache(); err != nil {` → `if err := h.refreshCache(true); err != nil {`.

- [ ] **Step 6: Build + existing tests**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/feature/appstore/`
Expected: build clean, tests PASS.

- [ ] **Step 7: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/handler.go
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: fetch bundled catalog.json with per-app fallback + force refresh"
```

---

### Task A4: Serve-stale-on-error + `stale` flag + `/appstore/status`

**Files:**
- Modify: `internal/feature/appstore/handler.go` (`Handler` struct ~143-153; `loadCacheFromDB` 289-310; `refreshCache` from A3; add `GetStatus`)
- Modify: `internal/api/router.go` (after line 224)

- [ ] **Step 1: Add the `stale` field to the Handler struct**

In `type Handler struct {`, under `cachedAt time.Time`:

```go
	stale      bool
```

- [ ] **Step 2: Make `loadCacheFromDB` a last-resort loader (accept any age, flag stale)**

Replace the tail of `loadCacheFromDB` (the `if time.Since(...) < cacheTTL { ... }` block):

```go
	h.mu.Lock()
	defer h.mu.Unlock()
	// Load the DB cache when in-mem is empty or the DB copy is newer. Caches
	// older than the TTL are accepted as a last resort (offline survivability)
	// and flagged stale rather than discarded.
	if h.cachedAt.IsZero() || cacheData.CachedAt.After(h.cachedAt) {
		h.categories = cacheData.Categories
		h.apps = cacheData.Apps
		h.cachedAt = cacheData.CachedAt
		h.stale = time.Since(cacheData.CachedAt) >= cacheTTL
	}
```

- [ ] **Step 3: Add serve-stale to `refreshCache`**

In `refreshCache` (A3), replace the `if err != nil { return err }` after the fallback with:

```go
	if err != nil {
		// Serve-stale: if we already have a catalog (in-mem or DB), keep it and
		// flag it stale instead of failing the whole store offline.
		h.mu.RLock()
		haveCache := len(h.apps) > 0
		h.mu.RUnlock()
		if haveCache {
			h.mu.Lock()
			h.stale = true
			h.mu.Unlock()
			slog.Warn("appstore: refresh failed; serving stale cache",
				"component", "appstore", "error", err)
			return nil
		}
		return err
	}
```

And on the success path (the `h.mu.Lock()` store block), add `h.stale = false` next to `h.cachedAt = time.Now()`.

- [ ] **Step 4: Add the status endpoint**

Add near `RefreshCache`:

```go
type appStoreStatus struct {
	Stale    bool      `json:"stale"`
	CachedAt time.Time `json:"cached_at"`
	Apps     int       `json:"apps"`
}

// GetStatus reports whether the served catalog is stale (last refresh failed,
// serving a cached copy) so the UI can show an offline banner. Never errors —
// a brand-new panel with no cache simply reports stale=false, apps=0.
func (h *Handler) GetStatus(c echo.Context) error {
	_ = h.ensureCache()
	h.mu.RLock()
	st := appStoreStatus{Stale: h.stale, CachedAt: h.cachedAt, Apps: len(h.apps)}
	h.mu.RUnlock()
	return response.OK(c, st)
}
```

- [ ] **Step 5: Register the route**

In `internal/api/router.go` after `appStore.POST("/refresh", appStoreHandler.RefreshCache)`:

```go
	appStore.GET("/status", appStoreHandler.GetStatus)
```

- [ ] **Step 6: Build + test**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/feature/appstore/`
Expected: build clean, PASS.

- [ ] **Step 7: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/handler.go internal/api/router.go
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: serve stale catalog on fetch failure + status endpoint"
```

---

### Task A5: Frontend offline/stale banner

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/AppStore.tsx`
- Modify: `web/src/i18n/locales/en.json`, `web/src/i18n/locales/ko.json`

- [ ] **Step 1: Add the type + api method**

`web/src/types/api.ts`:
```ts
export interface AppStoreStatus {
  stale: boolean
  cached_at: string
  apps: number
}
```
`web/src/lib/api.ts` (next to `refreshAppStore`):
```ts
  getAppStoreStatus() {
    return this.request<import('@/types/api').AppStoreStatus>('/appstore/status')
  }
```

- [ ] **Step 2: Fetch status + render banner in AppStore.tsx**

Add state + effect near the existing `refreshing` state:
```tsx
  const [stale, setStale] = useState(false)
```
In the page's load effect (where apps/categories are fetched), also:
```tsx
    api.getAppStoreStatus().then((s) => setStale(s.stale)).catch(() => {})
```
After `handleRefresh` succeeds, add `setStale(false)`.
Render, just above the controls row (the row containing the refresh button):
```tsx
      {stale && (
        <div className="flex items-center gap-2 rounded-xl bg-amber-500/10 px-3 py-2 text-[13px] text-amber-600 ring-1 ring-amber-500/20">
          <AlertCircle className="h-4 w-4 shrink-0" />
          <span>{t('appStore.staleCatalog')}</span>
        </div>
      )}
```
(`AlertCircle` is already imported in AppStore.tsx.)

- [ ] **Step 3: Add i18n strings (both files)**

`en.json` under `appStore`:
```json
"staleCatalog": "Offline — showing the last cached catalog. Press refresh to retry.",
```
`ko.json` under `appStore`:
```json
"staleCatalog": "오프라인 — 캐시된 카탈로그를 표시 중입니다. 새로고침을 눌러 다시 시도하세요.",
```

- [ ] **Step 4: Type-check + commit**

Run: `cd /opt/stacks/SFPanel/web && npx tsc --noEmit`
Expected: no errors.
```bash
cd /opt/stacks/SFPanel
git add web/src/types/api.ts web/src/lib/api.ts web/src/pages/AppStore.tsx web/src/i18n/locales/en.json web/src/i18n/locales/ko.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: show offline banner when catalog is stale"
```

---

## Phase B — Lifecycle depth

### Task B1: Surface current/remote digest in `ImageUpdateStatus`

**Files:**
- Modify: `internal/docker/client.go` (`ImageUpdateStatus` 559-565; `CheckImageUpdate` 567-600)
- Create: `internal/docker/imageupdate_test.go`

- [ ] **Step 1: Write failing tests for the parse helpers**

Create `internal/docker/imageupdate_test.go`:
```go
package docker

import "testing"

func TestParseImageTag(t *testing.T) {
	cases := map[string]string{
		"nginx:latest":                       "latest",
		"nginx":                              "latest",
		"lscr.io/linuxserver/sonarr:develop": "develop",
		"ghcr.io/foo/bar":                    "latest",
		"registry:5000/img:1.2.3":            "1.2.3",
	}
	for in, want := range cases {
		if got := parseImageTag(in); got != want {
			t.Errorf("parseImageTag(%q)=%q want %q", in, got, want)
		}
	}
}

func TestShortSha(t *testing.T) {
	if got := shortSha("sha256:abcdef0123456789"); got != "abcdef012345" {
		t.Errorf("shortSha=%q", got)
	}
	if got := shortSha("abc"); got != "abc" {
		t.Errorf("shortSha short=%q", got)
	}
}

func TestShortRepoDigest(t *testing.T) {
	rd := []string{"nginx@sha256:abcdef0123456789aaaa"}
	if got := shortRepoDigest(rd); got != "abcdef012345" {
		t.Errorf("shortRepoDigest=%q", got)
	}
	if got := shortRepoDigest(nil); got != "" {
		t.Errorf("shortRepoDigest empty=%q", got)
	}
}
```

- [ ] **Step 2: Run — fails (undefined helpers)**

Run: `cd /opt/stacks/SFPanel && go test ./internal/docker/ -run 'TestParseImageTag|TestShortSha|TestShortRepoDigest'`
Expected: compile error (undefined: parseImageTag, shortSha, shortRepoDigest).

- [ ] **Step 3: Extend the struct + add helpers + populate**

In `internal/docker/client.go`, replace the `ImageUpdateStatus` struct:
```go
// ImageUpdateStatus holds update check result for a single image.
type ImageUpdateStatus struct {
	Image          string `json:"image"`
	Tag            string `json:"tag,omitempty"`
	CurrentID      string `json:"current_id"`
	CurrentDigest  string `json:"current_digest,omitempty"`
	RemoteDigest   string `json:"remote_digest,omitempty"`
	CurrentCreated string `json:"current_created,omitempty"`
	HasUpdate      bool   `json:"has_update"`
	Error          string `json:"error,omitempty"`
}
```
Add helpers (above `CheckImageUpdate`):
```go
func parseImageTag(ref string) string {
	name := ref
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return "latest"
}

func shortSha(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func shortRepoDigest(repoDigests []string) string {
	for _, rd := range repoDigests {
		if i := strings.Index(rd, "@sha256:"); i >= 0 {
			return shortSha(rd[i+1:])
		}
	}
	return ""
}
```
In `CheckImageUpdate`, after `status.CurrentID = id`:
```go
	status.Tag = parseImageTag(imageRef)
	status.CurrentDigest = shortRepoDigest(localInspect.RepoDigests)
	status.CurrentCreated = localInspect.Created
```
And after `remoteDigest := string(distInspect.Descriptor.Digest)`:
```go
	status.RemoteDigest = shortSha(remoteDigest)
```

- [ ] **Step 4: Run tests + build**

Run: `cd /opt/stacks/SFPanel && go test ./internal/docker/ -run 'TestParseImageTag|TestShortSha|TestShortRepoDigest' && go build ./...`
Expected: PASS, build clean.

- [ ] **Step 5: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/docker/client.go internal/docker/imageupdate_test.go
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "docker: surface current/remote image digest in update check"
```

---

### Task B2: Render current → target in the Docker update dialog

**Files:**
- Modify: `web/src/types/api.ts` (`ImageUpdateStatus` 616-623)
- Modify: `web/src/pages/docker/DockerStacks.tsx` (per-image rows ~761-785)
- Modify: `web/src/i18n/locales/en.json`, `ko.json`

- [ ] **Step 1: Extend the TS type**

`web/src/types/api.ts`:
```ts
export interface ImageUpdateStatus {
  image: string
  tag?: string
  current_id: string
  current_digest?: string
  remote_digest?: string
  current_created?: string
  has_update: boolean
  error?: string
}
```

- [ ] **Step 2: Render the digest delta under the "update available" row**

In `DockerStacks.tsx`, inside `updateCheck.images.map(img => ...)`, the `img.has_update` branch currently renders icon + image + an "update available" badge in a single flex row. Wrap that row so a second line can show the delta. Replace the `) : img.has_update ? (` branch's `<>...</>` with:
```tsx
                        <div className="flex flex-col gap-0.5 min-w-0 flex-1">
                          <div className="flex items-center gap-2 min-w-0">
                            <span className="inline-block w-2 h-2 rounded-full bg-[#3182f6] shrink-0" />
                            <span className="font-mono text-[12px] truncate min-w-0 flex-1" title={img.image}>{img.image}</span>
                            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6] shrink-0">
                              {t('docker.stacks.updateAvailable')}
                            </span>
                          </div>
                          {img.current_digest && img.remote_digest && (
                            <span className="pl-4 font-mono text-[11px] text-muted-foreground truncate">
                              {img.current_digest} → {img.remote_digest}
                            </span>
                          )}
                        </div>
```
Note: the original branch rendered the icon/image/badge as direct flex children of the row `div` (`className="flex items-center gap-2 ..."`). Because this branch now returns a single `<div>` child instead of a fragment of three, it sits correctly inside that row. Leave the `img.error` and up-to-date branches unchanged.

- [ ] **Step 3: (No new i18n needed — reuses `docker.stacks.updateAvailable`.) Type-check**

Run: `cd /opt/stacks/SFPanel/web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /opt/stacks/SFPanel
git add web/src/types/api.ts web/src/pages/docker/DockerStacks.tsx
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "docker: show current to target digest in stack update check"
```

---

### Task B3: Keep-data uninstall (backend)

**Files:**
- Modify: `internal/feature/appstore/handler.go` (`UninstallApp` 842-874)
- Create: `internal/feature/appstore/uninstall_test.go`

- [ ] **Step 1: Write the failing test for the args helper**

Create `internal/feature/appstore/uninstall_test.go`:
```go
package appstore

import (
	"strings"
	"testing"
)

func TestComposeDownArgs(t *testing.T) {
	withVol := strings.Join(composeDownArgs("/x/docker-compose.yml", false), " ")
	if !strings.Contains(withVol, " -v") {
		t.Errorf("default uninstall must remove volumes: %q", withVol)
	}
	keep := strings.Join(composeDownArgs("/x/docker-compose.yml", true), " ")
	if strings.Contains(keep, " -v") {
		t.Errorf("keep_data uninstall must NOT remove volumes: %q", keep)
	}
	if !strings.Contains(keep, "--remove-orphans") {
		t.Errorf("uninstall must always remove orphans: %q", keep)
	}
}
```

- [ ] **Step 2: Run — fails (undefined: composeDownArgs)**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestComposeDownArgs`
Expected: compile error.

- [ ] **Step 3: Add the helper + use it in UninstallApp**

Add near `UninstallApp`:
```go
// composeDownArgs builds the `docker compose ... down` argument list. Volumes
// (and thus app data) are removed only when keepData is false.
func composeDownArgs(composePath string, keepData bool) []string {
	args := []string{"compose", "-f", composePath, "down", "--remove-orphans"}
	if !keepData {
		args = append(args, "-v")
	}
	return args
}
```
In `UninstallApp`, after the `validAppID` check add:
```go
	keepData := c.QueryParam("keep_data") == "true"
```
Replace the teardown call:
```go
	out, err := h.Cmd.RunCtx(c.Request().Context(), "docker", composeDownArgs(composePath, keepData)...)
```

- [ ] **Step 4: Run test + build**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestComposeDownArgs && go build ./...`
Expected: PASS, build clean.

- [ ] **Step 5: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/handler.go internal/feature/appstore/uninstall_test.go
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: support keep-data uninstall (down without -v)"
```

---

### Task B4: Keep-data checkbox (frontend)

**Files:**
- Modify: `web/src/lib/api.ts` (`uninstallApp` 1753)
- Modify: `web/src/pages/AppStoreDetail.tsx` (`handleUninstall` 215-233)
- Modify: `web/src/i18n/locales/en.json`, `ko.json`

- [ ] **Step 1: Extend the api method**

```ts
  uninstallApp(id: string, keepData = false) {
    const q = keepData ? '?keep_data=true' : ''
    return this.request<{ message: string }>(`/appstore/apps/${id}${q}`, { method: 'DELETE' })
  }
```

- [ ] **Step 2: Add the checkbox to the confirm flow**

`useConfirm` in this repo returns a boolean; it does not host a checkbox. Use a local state + render a small checkbox in the uninstall section and pass it through. Add near the other uninstall state (`const [uninstalling, ...]`):
```tsx
  const [keepData, setKeepData] = useState(false)
```
In `handleUninstall`, change the call:
```tsx
      await api.uninstallApp(detail.app.id, keepData)
```
and extend the confirm description to make the choice explicit:
```tsx
      description: keepData
        ? t('appStore.uninstallConfirmKeep', { name: detail.app.name })
        : t('appStore.uninstallConfirm', { name: detail.app.name }),
```
Render the checkbox just above the uninstall button (find the uninstall `<Button>` rendered in the installed state and place this before it):
```tsx
              <label className="flex items-center gap-2 text-[13px] text-muted-foreground mb-2 cursor-pointer">
                <input type="checkbox" checked={keepData} onChange={(e) => setKeepData(e.target.checked)} />
                {t('appStore.keepData')}
              </label>
```

- [ ] **Step 3: i18n (both files)**

`en.json` under `appStore`:
```json
"keepData": "Keep data volumes (don't delete app data)",
"uninstallConfirmKeep": "Remove {{name}} but keep its data volumes?",
```
`ko.json` under `appStore`:
```json
"keepData": "데이터 볼륨 유지 (앱 데이터를 삭제하지 않음)",
"uninstallConfirmKeep": "{{name}}을(를) 제거하되 데이터 볼륨은 유지할까요?",
```

- [ ] **Step 4: Type-check + commit**

Run: `cd /opt/stacks/SFPanel/web && npx tsc --noEmit`
```bash
cd /opt/stacks/SFPanel
git add web/src/lib/api.ts web/src/pages/AppStoreDetail.tsx web/src/i18n/locales/en.json web/src/i18n/locales/ko.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: keep-data option in uninstall dialog"
```

---

### Task B5: Post-install health poll (backend)

**Files:**
- Modify: `internal/feature/appstore/handler.go` (`sseEvent` 101-106; `InstallApp` success branch ~818-833)
- Create: `internal/feature/appstore/health_test.go`

- [ ] **Step 1: Write failing tests for the classifier**

Create `internal/feature/appstore/health_test.go`:
```go
package appstore

import "testing"

func TestClassifyComposeHealth(t *testing.T) {
	// docker compose ps --format json: newline-delimited objects (common) ...
	ndjson := `{"Service":"app","State":"running","Health":"healthy"}
{"Service":"db","State":"running","Health":""}`
	if got := classifyComposeHealth(ndjson); got != "healthy" {
		t.Errorf("ndjson all-running=%q want healthy", got)
	}
	// ... or a JSON array (newer compose)
	arr := `[{"Service":"app","State":"running","Health":"starting"}]`
	if got := classifyComposeHealth(arr); got != "starting" {
		t.Errorf("array starting=%q want starting", got)
	}
	if got := classifyComposeHealth(`[{"Service":"app","State":"restarting","Health":""}]`); got != "starting" {
		t.Errorf("restarting=%q want starting", got)
	}
	if got := classifyComposeHealth(""); got != "unknown" {
		t.Errorf("empty=%q want unknown", got)
	}
	if got := classifyComposeHealth(`[{"Service":"app","State":"exited","Health":""}]`); got != "starting" {
		t.Errorf("exited=%q want starting", got)
	}
}
```

- [ ] **Step 2: Run — fails (undefined: classifyComposeHealth)**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestClassifyComposeHealth`
Expected: compile error.

- [ ] **Step 3: Implement classifier + poller + a Health field on sseEvent**

Add to the `sseEvent` struct:
```go
	Health  string `json:"health,omitempty"`
```
Add (e.g. near `streamCommand`):
```go
type composePS struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

func parseComposePS(s string) []composePS {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var arr []composePS
	if json.Unmarshal([]byte(s), &arr) == nil {
		return arr
	}
	out := make([]composePS, 0)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p composePS
		if json.Unmarshal([]byte(line), &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// classifyComposeHealth maps `docker compose ps --format json` to a coarse
// signal: "healthy" (all services running, none mid-startup/unhealthy),
// "starting" (something still coming up), or "unknown" (no parseable rows).
func classifyComposeHealth(psJSON string) string {
	svcs := parseComposePS(psJSON)
	if len(svcs) == 0 {
		return "unknown"
	}
	allHealthy := true
	for _, s := range svcs {
		state := strings.ToLower(s.State)
		health := strings.ToLower(s.Health)
		if health == "starting" || state == "restarting" || state == "created" {
			return "starting"
		}
		if state != "running" || (health != "" && health != "healthy") {
			allHealthy = false
		}
	}
	if allHealthy {
		return "healthy"
	}
	return "starting"
}

// pollHealth samples compose ps for up to ~15s, returning as soon as the stack
// is healthy. Never blocks install success — worst case returns the last seen
// classification ("starting"/"unknown").
func (h *Handler) pollHealth(ctx context.Context, composePath string) string {
	last := "unknown"
	for i := 0; i < 5; i++ {
		out, err := h.Cmd.RunCtx(ctx, "docker", "compose", "-f", composePath, "ps", "--format", "json")
		if err == nil {
			last = classifyComposeHealth(out)
			if last == "healthy" {
				return "healthy"
			}
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(3 * time.Second):
		}
	}
	return last
}
```

- [ ] **Step 4: Emit health on the final done event**

In `InstallApp`, replace the success tail (the install-record write + `send("done", ...)`). Keep the record write, then:
```go
	send("health", "Checking health...", false, true)
	health := h.pollHealth(installCtx, composePath)
	sendSSE(w, flusher, sseEvent{Stage: "done", Message: "App installed successfully", Done: true, Success: true, Health: health})
	return nil
```
(Replaces the old `send("done", "App installed successfully", true, true); return nil`.)

- [ ] **Step 5: Run tests + build**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ && go build ./...`
Expected: PASS, build clean.

- [ ] **Step 6: Commit**

```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/handler.go internal/feature/appstore/health_test.go
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: poll stack health after install and report it"
```

---

### Task B6: Reflect health on the install-success screen (frontend)

**Files:**
- Modify: `web/src/pages/AppStoreDetail.tsx` (SSE done handling ~411-430)
- Modify: `web/src/i18n/locales/en.json`, `ko.json`

- [ ] **Step 1: Capture health from the done event**

Add state near `progressDone`:
```tsx
  const [installHealth, setInstallHealth] = useState<string>('')
```
In the SSE read loop, where `event.done` is handled (`if (event.done) { setProgressDone(true) ... }`), also:
```tsx
              if (event.health) setInstallHealth(event.health)
```
(The parsed `event` already comes from `JSON.parse`; add `health?: string` to its local type if one is declared, otherwise it's untyped JSON.)

- [ ] **Step 2: Show health in the success UI**

Where the post-install success block renders (gated by `progressDone` + success), add above the "Open app" action:
```tsx
                {installHealth === 'healthy' ? (
                  <p className="text-[13px] text-[#00c471]">{t('appStore.healthHealthy')}</p>
                ) : installHealth === 'starting' ? (
                  <p className="text-[13px] text-amber-600">{t('appStore.healthStarting')}</p>
                ) : null}
```

- [ ] **Step 3: i18n (both files)**

`en.json` under `appStore`:
```json
"healthHealthy": "Running and healthy — open it below.",
"healthStarting": "Still starting up — give it a moment, then open it.",
```
`ko.json` under `appStore`:
```json
"healthHealthy": "정상 실행 중입니다 — 아래에서 열어보세요.",
"healthStarting": "아직 시작하는 중입니다 — 잠시 후 열어보세요.",
```

- [ ] **Step 4: Type-check + commit**

Run: `cd /opt/stacks/SFPanel/web && npx tsc --noEmit`
```bash
cd /opt/stacks/SFPanel
git add web/src/pages/AppStoreDetail.tsx web/src/i18n/locales/en.json web/src/i18n/locales/ko.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: reflect post-install health on success screen"
```

---

### Task B7: "Update available" badge in the App Store (frontend, reuses compose check)

**Files:**
- Modify: `web/src/pages/AppStore.tsx`
- Modify: `web/src/i18n/locales/en.json`, `ko.json`

- [ ] **Step 1: Lazily check updates for installed apps**

In `AppStore.tsx`, add state:
```tsx
  const [updates, setUpdates] = useState<Record<string, boolean>>({})
```
Add an effect that runs after apps load, checking only installed apps (best-effort, never throws):
```tsx
  useEffect(() => {
    const installed = apps.filter((a) => a.installed)
    let cancelled = false
    ;(async () => {
      for (const a of installed) {
        try {
          const r = await api.checkStackUpdates(a.id)
          if (!cancelled && r.has_updates) setUpdates((m) => ({ ...m, [a.id]: true }))
        } catch {
          /* docker absent / not scanned — no badge */
        }
      }
    })()
    return () => { cancelled = true }
  }, [apps])
```
(`apps` is the existing loaded list state; adjust the name if it differs in the file.)

- [ ] **Step 2: Render the badge on installed cards, deep-linking to the Docker stack**

In the app card markup, where the "설치됨/installed" marker renders, add when `updates[app.id]`:
```tsx
                  {app.installed && updates[app.id] && (
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); navigate(`/docker/stacks/${app.id}`) }}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]"
                    >
                      {t('appStore.updateAvailable')}
                    </button>
                  )}
```
(`navigate` from `useNavigate()` — add the import/hook if not already present in the file.)

- [ ] **Step 3: i18n (both files)**

`en.json` under `appStore`:
```json
"updateAvailable": "Update available",
```
`ko.json` under `appStore`:
```json
"updateAvailable": "업데이트 있음",
```

- [ ] **Step 4: Type-check + commit**

Run: `cd /opt/stacks/SFPanel/web && npx tsc --noEmit`
```bash
cd /opt/stacks/SFPanel
git add web/src/pages/AppStore.tsx web/src/i18n/locales/en.json web/src/i18n/locales/ko.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: update-available badge linking to the Docker stack"
```

---

## Phase C — Accuracy / polish

### Task C1: Drop the placeholder version; show install date

**Files:**
- Modify: `internal/feature/appstore/handler.go` (`appStoreAppDetail` 84-91; `GetApp` detail build ~576-583)
- Modify: `web/src/types/api.ts` (`AppStoreAppDetail` / detail type)
- Modify: `web/src/pages/AppStore.tsx` (card `v{app.version}` line 332)
- Modify: `web/src/pages/AppStoreDetail.tsx` (`v{detail.app.version}` line 517)
- Modify: `web/src/i18n/locales/en.json`, `ko.json`

- [ ] **Step 1: Add `installed_at` to the detail response (backend)**

In `appStoreAppDetail` struct add:
```go
	InstalledAt string `json:"installed_at,omitempty"`
```
In `GetApp`, after computing `Installed: h.isInstalled(id)`, look up the record date:
```go
	installedAt := ""
	if h.isInstalled(id) {
		var value string
		if err := h.DB.QueryRow("SELECT value FROM settings WHERE key = ?", "appstore_installed_"+id).Scan(&value); err == nil {
			var rec appStoreInstallRecord
			if json.Unmarshal([]byte(value), &rec) == nil {
				installedAt = rec.InstalledAt
			}
		}
	}
```
and set `InstalledAt: installedAt` in the `appStoreAppDetail{...}` literal.

- [ ] **Step 2: Build (backend)**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/feature/appstore/`
Expected: clean, PASS.

- [ ] **Step 3: Remove `v{version}` on the card, keep it informative**

`web/src/pages/AppStore.tsx` line 332 — delete the `<span ...>v{app.version}</span>` (the catalog version is a constant placeholder and misleading).

- [ ] **Step 4: Detail page — replace version with install date when installed**

`web/src/types/api.ts`: add to the detail interface:
```ts
  installed_at?: string
```
`web/src/pages/AppStoreDetail.tsx` line ~517 — replace `v{detail.app.version}` with:
```tsx
                    {detail.installed && detail.installed_at
                      ? t('appStore.installedOn', { date: new Date(detail.installed_at).toLocaleDateString() })
                      : null}
```

- [ ] **Step 5: i18n (both files)**

`en.json` under `appStore`: `"installedOn": "Installed {{date}}",`
`ko.json` under `appStore`: `"installedOn": "설치일 {{date}}",`

- [ ] **Step 6: Type-check + commit**

Run: `cd /opt/stacks/SFPanel/web && npx tsc --noEmit`
```bash
cd /opt/stacks/SFPanel
git add internal/feature/appstore/handler.go web/src/types/api.ts web/src/pages/AppStore.tsx web/src/pages/AppStoreDetail.tsx web/src/i18n/locales/en.json web/src/i18n/locales/ko.json
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: drop placeholder version, show install date instead"
```

---

### Task C2: Document CDN propagation + scaffold reminder

**Files:**
- Modify: `appstore/README.md`
- Modify: `scripts/new-appstore-app.sh` (closing echo)

- [ ] **Step 1: Add a propagation note to the README**

Append a short section to `appstore/README.md`:
```markdown
## Catalog propagation & caching

Catalog edits on `main` are served from `raw.githubusercontent.com` and can take
up to ~5 minutes to clear the GitHub CDN. The panel additionally caches the
catalog for 1 hour. To pull changes immediately, use the **Refresh** button in
the App Store (it forces a re-fetch and appends a cache-bust query).

After editing any app, regenerate the bundle and commit it:

    make appstore-catalog
```

- [ ] **Step 2: Remind contributors in the scaffold script**

At the end of `scripts/new-appstore-app.sh`, add to the closing message:
```sh
echo "Next: edit metadata.json + docker-compose.yml, add the id to appstore/index.json, then run 'make appstore-catalog' and 'go test ./internal/feature/appstore/'."
```

- [ ] **Step 3: Commit**

```bash
cd /opt/stacks/SFPanel
git add appstore/README.md scripts/new-appstore-app.sh
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
  git commit -m "appstore: document CDN propagation + catalog regen step"
```

---

## Final verification (after all tasks)

- [ ] **Full build + tests + lint + type-check**

```bash
cd /opt/stacks/SFPanel && go build ./... && go test ./... && make lint
cd /opt/stacks/SFPanel/web && npx tsc --noEmit
```
Expected: all green.

- [ ] **Bundle freshness sanity**

```bash
cd /opt/stacks/SFPanel && make appstore-catalog && git diff --quiet appstore/catalog.json && echo "bundle up to date"
```
Expected: `bundle up to date` (regenerating produces no diff).

- [ ] **Deploy + smoke (node 203)**

Build, swap binary, restart, then push the catalog so `catalog.json` is live:
```bash
cd /opt/stacks/SFPanel && make build && sudo systemctl stop sfpanel && sudo cp ./sfpanel /usr/local/bin/sfpanel && sudo systemctl start sfpanel
git push origin main
```
Smoke: log in, open the App Store (89 apps, no stale banner online), force refresh (returns fresh), install a small app (e.g. gotify) and confirm the health line on success, then uninstall with "keep data" checked and confirm the volume survives (`docker volume ls`). In the Docker menu, open a stack → check updates → confirm the `current → target` digest line renders.

---

## Notes for the implementer

- **Order matters within a phase** but phases A/B/C are largely independent; B1/B2 (Docker digests) don't depend on A. If parallelizing across subagents, keep one subagent per task and re-run `go build ./...` after each backend task.
- **i18n parity** is enforced by review, not a test — always edit `en.json` and `ko.json` together.
- **Do not** modify `UpdateStack`/`RollbackStack` — rollback already works; we only enrich the *check* output.
- **`StacksPath` is shared** between appstore and the compose manager; that's why B7 can reuse `checkStackUpdates(appID)` with the app id as the project name.
