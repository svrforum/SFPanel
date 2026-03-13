# App Store Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Docker Compose 기반 원클릭 앱 설치 시스템 — GitHub 레포에서 앱 카탈로그를 가져와 SFPanel UI에서 설치/관리

**Architecture:** 별도 GitHub 레포(`svrforum/SFPanel-appstore`)에 앱별 metadata.json + docker-compose.yml 관리. SFPanel 백엔드가 raw.githubusercontent.com에서 fetch + 메모리 캐시. 프론트엔드는 전용 앱스토어 페이지에서 카테고리 필터/검색/설치 폼 제공. 설치된 앱은 기존 Docker Stacks(`/opt/stacks/`)로 관리.

**Tech Stack:** Go (Echo v4, net/http), React 19, TypeScript, Tailwind CSS, shadcn/ui Dialog, Lucide icons

**Design doc:** `docs/plans/2026-03-10-appstore-design.md`

---

## Task 1: GitHub 앱스토어 레포 초기 데이터 구성

앱스토어 레포(`svrforum/SFPanel-appstore`)에 categories.json과 2개 샘플 앱(uptime-kuma, vaultwarden)을 생성한다. 나머지 앱은 프레임워크 완성 후 추가.

**Files (in svrforum/SFPanel-appstore repo):**
- Create: `categories.json`
- Create: `apps/uptime-kuma/metadata.json`
- Create: `apps/uptime-kuma/docker-compose.yml`
- Create: `apps/vaultwarden/metadata.json`
- Create: `apps/vaultwarden/docker-compose.yml`

**Step 1: categories.json 생성**

```json
[
  { "id": "media", "name": { "ko": "미디어", "en": "Media" }, "icon": "Film" },
  { "id": "cloud", "name": { "ko": "클라우드", "en": "Cloud" }, "icon": "Cloud" },
  { "id": "security", "name": { "ko": "보안", "en": "Security" }, "icon": "Shield" },
  { "id": "monitoring", "name": { "ko": "모니터링", "en": "Monitoring" }, "icon": "Activity" },
  { "id": "dev", "name": { "ko": "개발", "en": "Development" }, "icon": "Code" }
]
```

**Step 2: uptime-kuma 앱 생성**

`apps/uptime-kuma/metadata.json`:
```json
{
  "id": "uptime-kuma",
  "name": "Uptime Kuma",
  "description": {
    "ko": "서비스 모니터링 및 알림 도구",
    "en": "Service monitoring and alerting tool"
  },
  "category": "monitoring",
  "version": "1.0.0",
  "website": "https://uptime.kuma.pet",
  "source": "https://github.com/louislam/uptime-kuma",
  "ports": [3001],
  "env": [
    {
      "key": "PORT",
      "label": { "ko": "외부 포트", "en": "External Port" },
      "type": "port",
      "default": "3001"
    }
  ]
}
```

`apps/uptime-kuma/docker-compose.yml`:
```yaml
services:
  uptime-kuma:
    image: louislam/uptime-kuma:1
    container_name: uptime-kuma
    restart: unless-stopped
    ports:
      - "${PORT:-3001}:3001"
    volumes:
      - uptime-kuma-data:/app/data

volumes:
  uptime-kuma-data:
```

**Step 3: vaultwarden 앱 생성**

`apps/vaultwarden/metadata.json`:
```json
{
  "id": "vaultwarden",
  "name": "Vaultwarden",
  "description": {
    "ko": "셀프 호스팅 비밀번호 관리자 (Bitwarden 호환)",
    "en": "Self-hosted password manager (Bitwarden compatible)"
  },
  "category": "security",
  "version": "1.0.0",
  "website": "https://github.com/dani-garcia/vaultwarden",
  "source": "https://github.com/dani-garcia/vaultwarden",
  "ports": [8080],
  "env": [
    {
      "key": "PORT",
      "label": { "ko": "외부 포트", "en": "External Port" },
      "type": "port",
      "default": "8080"
    },
    {
      "key": "ADMIN_TOKEN",
      "label": { "ko": "관리자 토큰", "en": "Admin Token" },
      "type": "password",
      "required": true,
      "generate": true
    }
  ]
}
```

`apps/vaultwarden/docker-compose.yml`:
```yaml
services:
  vaultwarden:
    image: vaultwarden/server:latest
    container_name: vaultwarden
    restart: unless-stopped
    ports:
      - "${PORT:-8080}:80"
    environment:
      - ADMIN_TOKEN=${ADMIN_TOKEN}
    volumes:
      - vaultwarden-data:/data

volumes:
  vaultwarden-data:
```

**Step 4: 레포에 push하고 raw URL로 접근 확인**

```bash
# 로컬에서 확인
curl -s https://raw.githubusercontent.com/svrforum/SFPanel-appstore/main/categories.json
curl -s https://raw.githubusercontent.com/svrforum/SFPanel-appstore/main/apps/uptime-kuma/metadata.json
```

**Step 5: Commit**

```bash
git add .
git commit -m "feat: 앱스토어 초기 카탈로그 — categories + uptime-kuma, vaultwarden"
git push origin main
```

---

## Task 2: 백엔드 — AppStoreHandler 구현

Go 백엔드에 앱스토어 핸들러를 생성한다. GitHub Raw URL에서 카탈로그를 fetch하고 메모리 캐시하는 구조.

**Files:**
- Create: `internal/api/handlers/appstore.go`
- Modify: `internal/api/response/errors.go` — `ErrAppStoreError` 추가
- Modify: `internal/api/router.go` — 앱스토어 라우트 등록

**Step 1: 에러 코드 추가**

`internal/api/response/errors.go`에 추가:
```go
ErrAppStoreError = "APPSTORE_ERROR"
```

**Step 2: appstore.go 작성**

`internal/api/handlers/appstore.go` — 전체 핸들러 구현:

```go
package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

const (
	appStoreRepoOwner = "svrforum"
	appStoreRepoName  = "SFPanel-appstore"
	appStoreBranch    = "main"
	appStoreCacheTTL  = 1 * time.Hour
)

// AppStoreHandler handles app store catalog browsing and installation.
type AppStoreHandler struct {
	DB          *sql.DB
	ComposePath string // "/opt/stacks"

	mu         sync.RWMutex
	categories []AppStoreCategory
	apps       []AppStoreMeta
	cachedAt   time.Time
}

// --- Data types matching GitHub repo JSON ---

type AppStoreCategory struct {
	ID   string            `json:"id"`
	Name map[string]string `json:"name"`
	Icon string            `json:"icon"`
}

type AppStoreEnvDef struct {
	Key      string            `json:"key"`
	Label    map[string]string `json:"label"`
	Type     string            `json:"type"` // string, password, port, path, select
	Default  string            `json:"default,omitempty"`
	Required bool              `json:"required,omitempty"`
	Generate bool              `json:"generate,omitempty"`
	Options  []string          `json:"options,omitempty"` // for select type
}

type AppStoreMeta struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description map[string]string `json:"description"`
	Category    string            `json:"category"`
	Version     string            `json:"version"`
	Website     string            `json:"website"`
	Source      string            `json:"source"`
	Ports       []int             `json:"ports"`
	Env         []AppStoreEnvDef  `json:"env"`
}

// --- Helpers ---

func rawURL(path string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		appStoreRepoOwner, appStoreRepoName, appStoreBranch, path)
}

func fetchJSON(url string, target interface{}) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func fetchText(url string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func generatePassword(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

// refreshCache fetches categories + app list from GitHub and caches them.
func (h *AppStoreHandler) refreshCache() error {
	// 1. Fetch categories
	var categories []AppStoreCategory
	if err := fetchJSON(rawURL("categories.json"), &categories); err != nil {
		return fmt.Errorf("fetch categories: %w", err)
	}

	// 2. Fetch app directory listing via GitHub API (Contents API)
	type ghContent struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var contents []ghContent
	contentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/apps?ref=%s",
		appStoreRepoOwner, appStoreRepoName, appStoreBranch)
	if err := fetchJSON(contentsURL, &contents); err != nil {
		return fmt.Errorf("fetch app list: %w", err)
	}

	// 3. Fetch each app's metadata.json
	var apps []AppStoreMeta
	for _, c := range contents {
		if c.Type != "dir" {
			continue
		}
		var meta AppStoreMeta
		if err := fetchJSON(rawURL("apps/"+c.Name+"/metadata.json"), &meta); err != nil {
			continue // skip broken apps
		}
		apps = append(apps, meta)
	}

	h.mu.Lock()
	h.categories = categories
	h.apps = apps
	h.cachedAt = time.Now()
	h.mu.Unlock()

	return nil
}

func (h *AppStoreHandler) ensureCache() error {
	h.mu.RLock()
	valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < appStoreCacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}
	return h.refreshCache()
}

// --- Handlers ---

// GetCategories returns the app store category list.
// GET /appstore/categories
func (h *AppStoreHandler) GetCategories(c echo.Context) error {
	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrAppStoreError,
			"Failed to fetch app store: "+err.Error())
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return response.OK(c, h.categories)
}

// ListApps returns all apps, optionally filtered by category.
// GET /appstore/apps?category=media
func (h *AppStoreHandler) ListApps(c echo.Context) error {
	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrAppStoreError,
			"Failed to fetch app store: "+err.Error())
	}

	category := c.QueryParam("category")

	// Get installed app IDs from settings DB
	installed := h.getInstalledIDs()

	h.mu.RLock()
	defer h.mu.RUnlock()

	type appListItem struct {
		AppStoreMeta
		Installed bool `json:"installed"`
	}

	var result []appListItem
	for _, app := range h.apps {
		if category != "" && app.Category != category {
			continue
		}
		result = append(result, appListItem{
			AppStoreMeta: app,
			Installed:    installed[app.ID],
		})
	}
	if result == nil {
		result = []appListItem{}
	}
	return response.OK(c, result)
}

// GetApp returns detailed info for a single app including the compose YAML.
// GET /appstore/apps/:id
func (h *AppStoreHandler) GetApp(c echo.Context) error {
	id := c.Param("id")
	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrAppStoreError,
			"Failed to fetch app store: "+err.Error())
	}

	h.mu.RLock()
	var meta *AppStoreMeta
	for _, app := range h.apps {
		if app.ID == id {
			a := app
			meta = &a
			break
		}
	}
	h.mu.RUnlock()

	if meta == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "App not found: "+id)
	}

	// Fetch compose YAML
	composeYAML, err := fetchText(rawURL("apps/" + id + "/docker-compose.yml"))
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrAppStoreError,
			"Failed to fetch compose file: "+err.Error())
	}

	installed := h.getInstalledIDs()

	return response.OK(c, map[string]interface{}{
		"app":       meta,
		"compose":   composeYAML,
		"installed": installed[id],
	})
}

// InstallApp installs an app by creating a compose project in /opt/stacks/{id}/.
// POST /appstore/apps/:id/install
// Body: { "env": { "PORT": "3001", "DB_PASSWORD": "..." } }
func (h *AppStoreHandler) InstallApp(c echo.Context) error {
	id := c.Param("id")

	var req struct {
		Env map[string]string `json:"env"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Env == nil {
		req.Env = make(map[string]string)
	}

	// Validate app ID format
	if !isValidAppID(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid app ID")
	}

	// Check not already installed
	projectDir := filepath.Join(h.ComposePath, id)
	if _, err := os.Stat(projectDir); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists,
			"App already installed at "+projectDir)
	}

	// Fetch compose YAML from GitHub
	composeYAML, err := fetchText(rawURL("apps/" + id + "/docker-compose.yml"))
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrAppStoreError,
			"Failed to fetch compose file: "+err.Error())
	}

	// Create project directory
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError,
			"Failed to create directory: "+err.Error())
	}

	// Write docker-compose.yml
	composePath := filepath.Join(projectDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(composeYAML), 0644); err != nil {
		_ = os.RemoveAll(projectDir)
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError,
			"Failed to write compose file: "+err.Error())
	}

	// Build .env file
	var envLines []string
	for key, val := range req.Env {
		envLines = append(envLines, fmt.Sprintf("%s=%s", key, val))
	}
	if len(envLines) > 0 {
		envPath := filepath.Join(projectDir, ".env")
		if err := os.WriteFile(envPath, []byte(strings.Join(envLines, "\n")+"\n"), 0644); err != nil {
			_ = os.RemoveAll(projectDir)
			return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError,
				"Failed to write .env file: "+err.Error())
		}
	}

	// Run docker compose up -d
	output, err := runCommand("docker", "compose", "-f", composePath, "up", "-d")
	if err != nil {
		// Cleanup on failure
		_, _ = runCommand("docker", "compose", "-f", composePath, "down")
		_ = os.RemoveAll(projectDir)
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError,
			"Failed to start app: "+err.Error())
	}

	// Save install record to settings DB
	installInfo, _ := json.Marshal(map[string]string{
		"version":      "1.0.0",
		"installed_at": time.Now().Format(time.RFC3339),
	})
	_, _ = h.DB.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		"appstore_installed_"+id, string(installInfo),
	)

	return response.OK(c, map[string]interface{}{
		"message": "App installed successfully",
		"output":  output,
	})
}

// GetInstalled returns the list of apps installed via the app store.
// GET /appstore/installed
func (h *AppStoreHandler) GetInstalled(c echo.Context) error {
	rows, err := h.DB.Query("SELECT key, value FROM settings WHERE key LIKE 'appstore_installed_%'")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, err.Error())
	}
	defer rows.Close()

	type installedApp struct {
		ID          string `json:"id"`
		Version     string `json:"version"`
		InstalledAt string `json:"installed_at"`
	}

	var result []installedApp
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		appID := strings.TrimPrefix(key, "appstore_installed_")
		var info map[string]string
		if err := json.Unmarshal([]byte(value), &info); err != nil {
			continue
		}
		result = append(result, installedApp{
			ID:          appID,
			Version:     info["version"],
			InstalledAt: info["installed_at"],
		})
	}
	if result == nil {
		result = []installedApp{}
	}
	return response.OK(c, result)
}

// RefreshCache forces a cache refresh.
// POST /appstore/refresh
func (h *AppStoreHandler) RefreshCache(c echo.Context) error {
	if err := h.refreshCache(); err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrAppStoreError,
			"Failed to refresh: "+err.Error())
	}
	return response.OK(c, map[string]string{"message": "Cache refreshed"})
}

// --- Internal helpers ---

func (h *AppStoreHandler) getInstalledIDs() map[string]bool {
	result := make(map[string]bool)
	rows, err := h.DB.Query("SELECT key FROM settings WHERE key LIKE 'appstore_installed_%'")
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err == nil {
			result[strings.TrimPrefix(key, "appstore_installed_")] = true
		}
	}
	return result
}

func isValidAppID(id string) bool {
	if len(id) == 0 || len(id) > 50 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
```

**Step 3: 라우트 등록**

`internal/api/router.go`에서 settingsHandler 초기화 근처에 AppStoreHandler 초기화 추가:

```go
appStoreHandler := &handlers.AppStoreHandler{
	DB:          database,
	ComposePath: "/opt/stacks",
}
```

authorized 그룹 내에 라우트 추가:

```go
// App Store
appStore := authorized.Group("/appstore")
appStore.GET("/categories", appStoreHandler.GetCategories)
appStore.GET("/apps", appStoreHandler.ListApps)
appStore.GET("/apps/:id", appStoreHandler.GetApp)
appStore.POST("/apps/:id/install", appStoreHandler.InstallApp)
appStore.GET("/installed", appStoreHandler.GetInstalled)
appStore.POST("/refresh", appStoreHandler.RefreshCache)
```

**Step 4: 빌드 확인**

```bash
cd /opt/stacks/SFPanel && go build ./cmd/sfpanel
```

**Step 5: Commit**

```bash
git add internal/api/handlers/appstore.go internal/api/response/errors.go internal/api/router.go
git commit -m "feat: 앱스토어 백엔드 — GitHub 레포 fetch, 캐시, 설치 API"
```

---

## Task 3: 프론트엔드 — TypeScript 타입 + API 클라이언트

프론트엔드 타입 정의와 API 메서드를 추가한다.

**Files:**
- Modify: `web/src/types/api.ts` — 앱스토어 타입 추가
- Modify: `web/src/lib/api.ts` — 앱스토어 API 메서드 추가

**Step 1: TypeScript 타입 추가**

`web/src/types/api.ts` 파일 끝에 추가:

```typescript
// App Store
export interface AppStoreCategory {
  id: string
  name: Record<string, string>
  icon: string
}

export interface AppStoreEnvDef {
  key: string
  label: Record<string, string>
  type: string
  default?: string
  required?: boolean
  generate?: boolean
  options?: string[]
}

export interface AppStoreMeta {
  id: string
  name: string
  description: Record<string, string>
  category: string
  version: string
  website: string
  source: string
  ports: number[]
  env: AppStoreEnvDef[]
}

export interface AppStoreApp extends AppStoreMeta {
  installed: boolean
}

export interface AppStoreAppDetail {
  app: AppStoreMeta
  compose: string
  installed: boolean
}

export interface AppStoreInstalledApp {
  id: string
  version: string
  installed_at: string
}
```

**Step 2: API 메서드 추가**

`web/src/lib/api.ts`에 import 추가 후 클래스 내 메서드 추가:

```typescript
// App Store
getAppStoreCategories() {
  return this.request<AppStoreCategory[]>('/appstore/categories')
}

getAppStoreApps(category?: string) {
  const query = category ? `?category=${category}` : ''
  return this.request<AppStoreApp[]>(`/appstore/apps${query}`)
}

getAppStoreApp(id: string) {
  return this.request<AppStoreAppDetail>(`/appstore/apps/${id}`)
}

installApp(id: string, env: Record<string, string>) {
  return this.request<{ message: string; output: string }>(`/appstore/apps/${id}/install`, {
    method: 'POST',
    body: JSON.stringify({ env }),
  })
}

getInstalledApps() {
  return this.request<AppStoreInstalledApp[]>('/appstore/installed')
}

refreshAppStore() {
  return this.request<{ message: string }>('/appstore/refresh', { method: 'POST' })
}
```

**Step 3: Commit**

```bash
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "feat: 앱스토어 프론트엔드 타입 + API 클라이언트"
```

---

## Task 4: 프론트엔드 — AppStore.tsx 페이지

앱스토어 메인 페이지를 구현한다. 카테고리 필터, 검색, 앱 그리드 카드, 설치 다이얼로그 포함.

**Files:**
- Create: `web/src/pages/AppStore.tsx`

**Step 1: AppStore.tsx 작성**

전체 페이지 구현. 주요 구조:

```typescript
// 상태
const [categories, setCategories] = useState<AppStoreCategory[]>([])
const [apps, setApps] = useState<AppStoreApp[]>([])
const [loading, setLoading] = useState(true)
const [selectedCategory, setSelectedCategory] = useState<string>('')
const [search, setSearch] = useState('')
const [selectedApp, setSelectedApp] = useState<AppStoreAppDetail | null>(null)
const [dialogOpen, setDialogOpen] = useState(false)
const [envValues, setEnvValues] = useState<Record<string, string>>({})
const [installing, setInstalling] = useState(false)

// useEffect: load categories + apps on mount
// 카테고리 변경 시 앱 목록 reload (query param)

// filteredApps: search 필터링 (name, description)

// handleInstallClick: app detail fetch → dialog open → env 기본값/generate 채움
// handleInstall: api.installApp() → toast success → reload apps
```

UI 구조 (Toss 디자인 시스템):
- 페이지 헤더: 제목 + 설명 + 새로고침 버튼
- 카테고리 필터: pill 버튼 (전체 + 각 카테고리)
- 검색 인풋: `pl-9 h-9 rounded-xl bg-secondary/50 border-0`
- 앱 그리드: `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4`
- 앱 카드: `bg-card rounded-2xl p-5 card-shadow` + 아이콘 + 이름 + 설명 + 설치 버튼/상태
- 설치 다이얼로그: shadcn Dialog + 동적 폼 (env 타입별 인풋)

아이콘: 앱 아이콘은 GitHub Raw URL에서 `<img src={iconUrl}>` 로드. 로드 실패 시 카테고리 아이콘 fallback.

**Step 2: Commit**

```bash
git add web/src/pages/AppStore.tsx
git commit -m "feat: 앱스토어 페이지 UI — 카테고리 필터, 검색, 설치 다이얼로그"
```

---

## Task 5: 라우팅 + 사이드바 + i18n 통합

앱스토어 페이지를 라우터에 등록하고 사이드바에 메뉴를 추가한다. i18n 번역 키도 추가.

**Files:**
- Modify: `web/src/App.tsx` — lazy import + Route 추가
- Modify: `web/src/components/Layout.tsx` — navItems에 앱스토어 추가
- Modify: `web/src/i18n/locales/ko.json` — 앱스토어 번역 키
- Modify: `web/src/i18n/locales/en.json` — 앱스토어 번역 키

**Step 1: App.tsx에 lazy import + Route 추가**

```tsx
const AppStore = lazy(() => import('@/pages/AppStore'))
```

Route 추가 (ProtectedRoute 내부):
```tsx
<Route path="/appstore" element={<ProtectedRoute><Suspense fallback={...}><AppStore /></Suspense></ProtectedRoute>} />
```

**Step 2: Layout.tsx 사이드바에 메뉴 추가**

navItems 배열에서 Docker 항목 다음에 추가:

```tsx
{ to: '/appstore', labelKey: 'layout.nav.appstore', icon: Store },
```

`Store`는 `lucide-react`에서 import.

**Step 3: i18n 번역 키 추가 (ko.json)**

```json
{
  "layout": {
    "nav": {
      "appstore": "앱스토어"
    }
  },
  "appStore": {
    "title": "앱스토어",
    "subtitle": "원클릭으로 셀프호스팅 앱을 설치하세요",
    "refresh": "새로고침",
    "refreshing": "새로고침 중...",
    "refreshSuccess": "카탈로그를 새로고침했습니다",
    "all": "전체",
    "searchPlaceholder": "앱 검색...",
    "noApps": "앱이 없습니다",
    "installed": "설치됨",
    "install": "설치",
    "installing": "설치 중...",
    "installSuccess": "{{name}} 설치가 완료되었습니다",
    "installFailed": "설치에 실패했습니다",
    "installTitle": "{{name}} 설치",
    "settings": "설정",
    "website": "웹사이트",
    "source": "소스코드",
    "port": "포트",
    "cancel": "취소",
    "confirmInstall": "설치하기",
    "alreadyInstalled": "이미 설치되어 있습니다",
    "manageInStacks": "Docker Stacks에서 관리",
    "loadFailed": "앱스토어를 불러올 수 없습니다",
    "generatePassword": "비밀번호 생성"
  }
}
```

**Step 4: i18n 번역 키 추가 (en.json)**

```json
{
  "layout": {
    "nav": {
      "appstore": "App Store"
    }
  },
  "appStore": {
    "title": "App Store",
    "subtitle": "Install self-hosted apps with one click",
    "refresh": "Refresh",
    "refreshing": "Refreshing...",
    "refreshSuccess": "Catalog refreshed",
    "all": "All",
    "searchPlaceholder": "Search apps...",
    "noApps": "No apps found",
    "installed": "Installed",
    "install": "Install",
    "installing": "Installing...",
    "installSuccess": "{{name}} installed successfully",
    "installFailed": "Installation failed",
    "installTitle": "Install {{name}}",
    "settings": "Settings",
    "website": "Website",
    "source": "Source Code",
    "port": "Port",
    "cancel": "Cancel",
    "confirmInstall": "Install",
    "alreadyInstalled": "Already installed",
    "manageInStacks": "Manage in Docker Stacks",
    "loadFailed": "Failed to load app store",
    "generatePassword": "Generate Password"
  }
}
```

**Step 5: Commit**

```bash
git add web/src/App.tsx web/src/components/Layout.tsx web/src/i18n/locales/ko.json web/src/i18n/locales/en.json
git commit -m "feat: 앱스토어 라우팅, 사이드바, i18n 번역"
```

---

## Task 6: 빌드 + 배포 + 테스트

전체 빌드하고 배포 후 Playwright로 UI 테스트한다.

**Step 1: 프론트엔드 빌드**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```

**Step 2: Go 바이너리 빌드**

```bash
cd /opt/stacks/SFPanel && go build -ldflags="-s -w" -trimpath -o sfpanel ./cmd/sfpanel
```

**Step 3: 배포**

```bash
sudo systemctl stop sfpanel
sudo cp /opt/stacks/SFPanel/sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
```

**Step 4: Playwright 테스트**

1. 사이드바에 "앱스토어" 메뉴 표시되는지 확인
2. 앱스토어 페이지 접근 → 카테고리 필터 + 앱 카드 표시
3. 앱 카드 클릭 → 설치 다이얼로그 오픈
4. 환경변수 입력 → "설치하기" 클릭 → 성공 토스트
5. Docker Stacks 페이지에서 설치된 앱 확인

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: 앱스토어 v1 — 원클릭 앱 설치"
```

---

## Task 7: 나머지 앱 추가 (appstore 레포)

초기 8개 앱 중 남은 6개를 앱스토어 레포에 추가한다.

**Apps to add:**
- `apps/immich/` — 사진/동영상 백업
- `apps/jellyfin/` — 미디어 서버
- `apps/nextcloud/` — 파일 동기화
- `apps/gitea/` — Git 호스팅
- `apps/portainer/` — Docker 관리 UI
- `apps/nginx-proxy-manager/` — 리버스 프록시

각 앱에 대해 metadata.json + docker-compose.yml 작성. 공식 문서의 권장 compose 설정을 기반으로 하되, 환경변수는 `.env`에서 주입하도록 조정.

**Step 1: 각 앱의 metadata.json + docker-compose.yml 작성**

**Step 2: 레포에 push**

```bash
git add apps/
git commit -m "feat: 앱 6개 추가 — immich, jellyfin, nextcloud, gitea, portainer, npm"
git push origin main
```

**Step 3: SFPanel에서 새로고침 → 8개 앱 모두 표시 확인**

---

## Task 8: 스펙 문서 업데이트

API 스펙, 프론트엔드 스펙 문서를 업데이트한다.

**Files:**
- Modify: `docs/specs/api-spec.md` — 앱스토어 6개 엔드포인트 추가
- Modify: `docs/specs/frontend-spec.md` — AppStore 페이지 추가
