# SFPanel 셀프 관리 기능 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Settings 페이지에서 패널 업데이트(SSE 스트리밍), 설정 백업/복원, 업데이트 알림(대시보드 배너+사이드바 배지) 기능을 추가한다.

**Architecture:** 백엔드에 4개 API 엔드포인트 추가 (update-check, update SSE, backup, restore). `DashboardOverview`에 `update_available` 필드 추가하여 알림 제공. 기존 `updatePanel()` CLI 로직을 웹 핸들러로 포팅. 프론트엔드 Settings 페이지에 업데이트/백업 카드 추가, Layout에 업데이트 배지, Dashboard에 업데이트 배너.

**Tech Stack:** Go (Echo v4, archive/tar, compress/gzip, net/http, SSE), React (fetch ReadableStream), i18n (ko/en)

**Design doc:** `docs/plans/2026-03-06-self-management-design.md`

---

### Task 1: Backend — 업데이트 체크 API + 업데이트 SSE 핸들러

**Files:**
- Create: `internal/api/handlers/system.go`
- Modify: `internal/api/router.go:75-77`
- Modify: `internal/api/response/errors.go` (새 에러 코드 추가)

**Context:**
- GitHub API URL: `https://api.github.com/repos/sfpanel/sfpanel/releases/latest`
- 기존 CLI `updatePanel()` 로직은 `cmd/sfpanel/main.go:175-284` 참고
- SSE 패턴은 `internal/api/handlers/docker.go:236-256` (PullImage) 참고
- `DashboardHandler`는 `Version` 필드를 가지고 있음 (`dashboard.go:12-14`)

**Step 1: `internal/api/handlers/system.go` 생성**

```go
package handlers

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

type SystemHandler struct {
	Version string
}

type GitHubRelease struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

type UpdateCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseNotes    string `json:"release_notes"`
	PublishedAt     string `json:"published_at"`
}

// CheckUpdate queries GitHub releases API and returns version comparison.
func (h *SystemHandler) CheckUpdate(c echo.Context) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sfpanel/sfpanel/releases/latest")
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateCheckFailed, "Failed to check for updates")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateCheckFailed,
			fmt.Sprintf("GitHub API returned %d", resp.StatusCode))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUpdateCheckFailed, "Failed to parse release info")
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	return response.OK(c, UpdateCheckResponse{
		CurrentVersion:  h.Version,
		LatestVersion:   latest,
		UpdateAvailable: latest != h.Version,
		ReleaseNotes:    release.Body,
		PublishedAt:     release.PublishedAt,
	})
}

// RunUpdate downloads the latest release and replaces the current binary, streaming progress via SSE.
func (h *SystemHandler) RunUpdate(c echo.Context) error {
	// 1. Check latest version
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sfpanel/sfpanel/releases/latest")
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed, "Failed to check for updates")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return response.Fail(c, http.StatusBadGateway, response.ErrUpdateFailed, "GitHub API error")
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUpdateFailed, "Failed to parse release")
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == h.Version {
		return response.OK(c, map[string]string{"status": "up_to_date"})
	}

	// SSE setup
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	flusher := c.Response()

	sendEvent := func(step, message string) {
		data, _ := json.Marshal(map[string]string{"step": step, "message": message})
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	// 2. Download
	arch := runtime.GOARCH
	url := fmt.Sprintf("https://github.com/sfpanel/sfpanel/releases/download/v%s/sfpanel_%s_linux_%s.tar.gz", latest, latest, arch)
	sendEvent("downloading", fmt.Sprintf("Downloading v%s (%s)...", latest, arch))

	dlClient := &http.Client{Timeout: 5 * time.Minute}
	dlResp, err := dlClient.Get(url)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Download failed: %v", err))
		return nil
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		sendEvent("error", fmt.Sprintf("Download failed (HTTP %d)", dlResp.StatusCode))
		return nil
	}

	// 3. Extract
	sendEvent("extracting", "Extracting binary...")
	gzr, err := gzip.NewReader(dlResp.Body)
	if err != nil {
		sendEvent("error", fmt.Sprintf("Decompression failed: %v", err))
		return nil
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var binaryData []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			sendEvent("error", fmt.Sprintf("Archive read failed: %v", err))
			return nil
		}
		if hdr.Name == "sfpanel" || strings.HasSuffix(hdr.Name, "/sfpanel") {
			binaryData, err = io.ReadAll(tr)
			if err != nil {
				sendEvent("error", fmt.Sprintf("Binary read failed: %v", err))
				return nil
			}
			break
		}
	}
	if binaryData == nil {
		sendEvent("error", "Binary not found in archive")
		return nil
	}

	// 4. Replace binary
	sendEvent("replacing", "Replacing binary...")
	execPath, err := os.Executable()
	if err != nil {
		sendEvent("error", fmt.Sprintf("Cannot find binary path: %v", err))
		return nil
	}

	// Backup current binary
	backupPath := execPath + ".bak"
	if data, err := os.ReadFile(execPath); err == nil {
		os.WriteFile(backupPath, data, 0755)
	}

	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, binaryData, 0755); err != nil {
		sendEvent("error", fmt.Sprintf("Write failed: %v", err))
		return nil
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		sendEvent("error", fmt.Sprintf("Replace failed: %v", err))
		return nil
	}

	// 5. Restart
	sendEvent("restarting", "Restarting service...")
	if err := exec.Command("systemctl", "is-active", "--quiet", "sfpanel").Run(); err == nil {
		exec.Command("systemctl", "restart", "sfpanel").Start()
	}

	sendEvent("complete", fmt.Sprintf("Updated to v%s. Restarting...", latest))
	return nil
}
```

**Step 2: 에러 코드 추가 — `internal/api/response/errors.go`에 추가**

파일 끝에 다음 상수를 추가:
```go
ErrUpdateCheckFailed = "UPDATE_CHECK_FAILED"
ErrUpdateFailed      = "UPDATE_FAILED"
ErrBackupFailed      = "BACKUP_FAILED"
ErrRestoreFailed     = "RESTORE_FAILED"
```

**Step 3: 라우터 등록 — `internal/api/router.go`**

`dashboardHandler` 생성 근처 (현재 ~line 48)에 `SystemHandler` 생성 추가:
```go
systemHandler := &handlers.SystemHandler{Version: version}
```

`authorized` 그룹의 system 라우트 섹션 (line 77 근처, `authorized.GET("/system/overview", ...)` 다음)에 추가:
```go
authorized.GET("/system/update-check", systemHandler.CheckUpdate)
authorized.POST("/system/update", systemHandler.RunUpdate)
authorized.POST("/system/backup", systemHandler.CreateBackup)
authorized.POST("/system/restore", systemHandler.RestoreBackup)
```

**Step 4: 빌드 확인**

```bash
cd /opt/stacks/SFPanel && go build ./cmd/sfpanel
```

**Step 5: 커밋**

```bash
git add internal/api/handlers/system.go internal/api/router.go internal/api/response/errors.go
git commit -m "feat: 업데이트 체크 + SSE 업데이트 API 추가"
```

---

### Task 2: Backend — 설정 백업/복원 API

**Files:**
- Modify: `internal/api/handlers/system.go` (Task 1에서 생성한 파일에 메서드 추가)

**Context:**
- 백업 대상: `sfpanel.db` (DB), `config.yaml` (설정 파일)
- DB 경로: `config.yaml`의 `database.path` (기본값 `./sfpanel.db`)
- 설정 경로: CLI 인자 또는 기본 `config.yaml`
- 파일 다운로드는 `Content-Type: application/gzip` + `Content-Disposition: attachment`
- 복원 시 multipart/form-data로 tar.gz 업로드

**Step 1: `SystemHandler` 구조체에 설정 경로 필드 추가**

`system.go`의 `SystemHandler` 구조체를 수정:
```go
type SystemHandler struct {
	Version    string
	DBPath     string
	ConfigPath string
}
```

**Step 2: `CreateBackup` 메서드 추가**

```go
// CreateBackup creates a tar.gz archive of DB + config and sends it as download.
func (h *SystemHandler) CreateBackup(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "application/gzip")
	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=sfpanel-backup-%s.tar.gz", time.Now().Format("20060102-150405")))
	c.Response().WriteHeader(http.StatusOK)

	gw := gzip.NewWriter(c.Response())
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Add DB file
	if err := addFileToTar(tw, h.DBPath, "sfpanel.db"); err != nil {
		return err
	}

	// Add config file
	if err := addFileToTar(tw, h.ConfigPath, "config.yaml"); err != nil {
		return err
	}

	return nil
}

func addFileToTar(tw *tar.Writer, filePath, nameInArchive string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", filePath, err)
	}

	hdr := &tar.Header{
		Name: nameInArchive,
		Size: info.Size(),
		Mode: int64(info.Mode()),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
```

**Step 3: `RestoreBackup` 메서드 추가**

```go
// RestoreBackup receives a tar.gz upload, validates contents, and restores DB + config.
func (h *SystemHandler) RestoreBackup(c echo.Context) error {
	file, err := c.FormFile("backup")
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "No backup file provided")
	}

	src, err := file.Open()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to open uploaded file")
	}
	defer src.Close()

	gzr, err := gzip.NewReader(src)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Invalid gzip file")
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	files := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Invalid tar archive")
		}
		if hdr.Name == "sfpanel.db" || hdr.Name == "config.yaml" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to read archive entry")
			}
			files[hdr.Name] = data
		}
	}

	if _, ok := files["sfpanel.db"]; !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrRestoreFailed, "Backup must contain sfpanel.db")
	}

	// Backup current files
	if data, err := os.ReadFile(h.DBPath); err == nil {
		os.WriteFile(h.DBPath+".bak", data, 0644)
	}
	if data, err := os.ReadFile(h.ConfigPath); err == nil {
		os.WriteFile(h.ConfigPath+".bak", data, 0644)
	}

	// Write restored files
	if dbData, ok := files["sfpanel.db"]; ok {
		if err := os.WriteFile(h.DBPath, dbData, 0644); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to restore database")
		}
	}
	if cfgData, ok := files["config.yaml"]; ok {
		if err := os.WriteFile(h.ConfigPath, cfgData, 0644); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrRestoreFailed, "Failed to restore config")
		}
	}

	// Restart service
	if err := exec.Command("systemctl", "is-active", "--quiet", "sfpanel").Run(); err == nil {
		exec.Command("systemctl", "restart", "sfpanel").Start()
	}

	return response.OK(c, map[string]string{"message": "Backup restored. Service restarting..."})
}
```

**Step 4: 라우터에서 `SystemHandler` 초기화 업데이트**

`router.go`에서 `SystemHandler` 생성을 수정하여 경로 전달:
```go
systemHandler := &handlers.SystemHandler{
	Version:    version,
	DBPath:     cfg.Database.Path,
	ConfigPath: cfgPath,
}
```

이를 위해 `NewRouter` 함수 시그니처에 `cfgPath string` 파라미터 추가 필요.
`router.go`의 `NewRouter` 시그니처: `func NewRouter(database *sql.DB, cfg *config.Config, webFS embed.FS, version string) *echo.Echo`
→ `func NewRouter(database *sql.DB, cfg *config.Config, webFS embed.FS, version, cfgPath string) *echo.Echo`

`main.go`의 호출도 업데이트:
```go
e := api.NewRouter(database, cfg, sfpanel.WebDistFS, version, cfgPath)
```

**Step 5: 빌드 확인**

```bash
cd /opt/stacks/SFPanel && go build ./cmd/sfpanel
```

**Step 6: 커밋**

```bash
git add internal/api/handlers/system.go internal/api/router.go cmd/sfpanel/main.go
git commit -m "feat: 설정 백업/복원 API 추가"
```

---

### Task 3: Backend — DashboardOverview에 업데이트 알림 필드 추가

**Files:**
- Modify: `internal/api/handlers/dashboard.go:41-46`
- Modify: `internal/monitor/collector.go` 또는 새 파일 `internal/monitor/update.go`

**Context:**
- `DashboardOverview` 구조체에 `UpdateAvailable` 필드 추가
- 백그라운드에서 1시간 간격으로 GitHub API 폴링, 결과 캐시
- `GetOverview`에서 캐시된 값 반환

**Step 1: `internal/monitor/update.go` 생성**

```go
package monitor

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	updateMu        sync.RWMutex
	cachedLatest    string
	cachedNotes     string
	cachedPublished string
	lastCheck       time.Time
)

// StartUpdateChecker polls GitHub releases every hour in background.
func StartUpdateChecker(currentVersion string) {
	go func() {
		checkUpdate(currentVersion) // immediate first check
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			checkUpdate(currentVersion)
		}
	}()
}

func checkUpdate(currentVersion string) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sfpanel/sfpanel/releases/latest")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}

	var release struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	updateMu.Lock()
	cachedLatest = latest
	cachedNotes = release.Body
	cachedPublished = release.PublishedAt
	lastCheck = time.Now()
	updateMu.Unlock()
}

type UpdateInfo struct {
	UpdateAvailable bool   `json:"update_available"`
	LatestVersion   string `json:"latest_version,omitempty"`
}

// GetUpdateInfo returns cached update status.
func GetUpdateInfo(currentVersion string) UpdateInfo {
	updateMu.RLock()
	defer updateMu.RUnlock()
	if cachedLatest == "" {
		return UpdateInfo{}
	}
	return UpdateInfo{
		UpdateAvailable: cachedLatest != currentVersion,
		LatestVersion:   cachedLatest,
	}
}
```

**Step 2: `dashboard.go` — DashboardOverview 구조체 + GetOverview 수정**

`DashboardOverview` 구조체에 필드 추가:
```go
type DashboardOverview struct {
	Host           *monitor.HostInfo      `json:"host"`
	Metrics        *monitor.Metrics       `json:"metrics"`
	Version        string                 `json:"version"`
	MetricsHistory []monitor.MetricsPoint `json:"metrics_history"`
	UpdateInfo     *monitor.UpdateInfo    `json:"update_info,omitempty"`
}
```

`GetOverview` 메서드 끝 (response.OK 호출 직전)에 업데이트 정보 추가:
```go
updateInfo := monitor.GetUpdateInfo(h.Version)
```

반환값에 `UpdateInfo: &updateInfo` 추가.

**Step 3: `main.go`에서 업데이트 체커 시작**

`monitor.StartHistoryCollector(database)` 다음에:
```go
monitor.StartUpdateChecker(version)
```

**Step 4: 빌드 확인**

```bash
cd /opt/stacks/SFPanel && go build ./cmd/sfpanel
```

**Step 5: 커밋**

```bash
git add internal/monitor/update.go internal/api/handlers/dashboard.go cmd/sfpanel/main.go
git commit -m "feat: 백그라운드 업데이트 체커 + DashboardOverview 알림 필드"
```

---

### Task 4: Frontend — TypeScript 타입 + API 클라이언트 메서드

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

**Context:**
- `DashboardOverview` 타입에 `update_info` 필드 추가
- 새 API 메서드: `checkUpdate()`, `runUpdateStream()`, `createBackup()`, `restoreBackup()`
- SSE 스트리밍은 `pullImageStream()` 패턴 참고 (`api.ts:233-263`)
- 파일 다운로드는 `downloadFile()` 패턴 참고 (`api.ts:477-490`)

**Step 1: `types/api.ts`에 타입 추가**

```typescript
export interface UpdateCheckResult {
  current_version: string
  latest_version: string
  update_available: boolean
  release_notes: string
  published_at: string
}

export interface UpdateInfo {
  update_available: boolean
  latest_version?: string
}
```

`DashboardOverview` 인터페이스에 필드 추가:
```typescript
export interface DashboardOverview {
  host: HostInfo
  metrics: Metrics
  version: string
  metrics_history: MetricsPoint[]
  update_info?: UpdateInfo
}
```

**Step 2: `api.ts`에 메서드 추가**

import 목록에 `UpdateCheckResult` 추가.

```typescript
// System update
checkUpdate() {
  return this.request<UpdateCheckResult>('/system/update-check')
}

async runUpdateStream(
  onProgress: (event: { step: string; message: string }) => void
): Promise<void> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (this.token) {
    headers['Authorization'] = `Bearer ${this.token}`
  }

  const res = await fetch(`${API_BASE}/system/update`, {
    method: 'POST',
    headers,
  })
  if (!res.ok) throw new Error('Update failed')
  const reader = res.body?.getReader()
  if (!reader) throw new Error('No stream')
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''
    for (const line of lines) {
      if (line.startsWith('data: ')) {
        try { onProgress(JSON.parse(line.slice(6))) } catch { /* skip */ }
      }
    }
  }
}

// System backup/restore
async downloadBackup(): Promise<Blob> {
  const headers: Record<string, string> = {}
  if (this.token) {
    headers['Authorization'] = `Bearer ${this.token}`
  }
  const res = await fetch(`${API_BASE}/system/backup`, {
    method: 'POST',
    headers,
  })
  if (!res.ok) throw new Error(`Backup failed (${res.status})`)
  return res.blob()
}

async restoreBackup(file: File): Promise<void> {
  const headers: Record<string, string> = {}
  if (this.token) {
    headers['Authorization'] = `Bearer ${this.token}`
  }
  const formData = new FormData()
  formData.append('backup', file)
  const res = await fetch(`${API_BASE}/system/restore`, {
    method: 'POST',
    headers,
    body: formData,
  })
  const json = await res.json()
  if (!json.success) throw new Error(json.error?.message || 'Restore failed')
}
```

**Step 3: 프론트엔드 빌드 확인**

```bash
cd /opt/stacks/SFPanel/web && npx tsc --noEmit
```

**Step 4: 커밋**

```bash
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "feat: 업데이트/백업 API 클라이언트 메서드 추가"
```

---

### Task 5: Frontend — i18n 번역 키 추가 (ko + en)

**Files:**
- Modify: `web/src/i18n/locales/ko.json`
- Modify: `web/src/i18n/locales/en.json`

**Context:**
- Settings 페이지의 업데이트/백업 섹션, Dashboard 배너, Layout 배지에 사용할 번역 키
- 기존 `settings` 네임스페이스 하위에 추가

**Step 1: `en.json`의 `settings` 객체에 다음 키 추가**

```json
"update": "Panel Update",
"updateDescription": "Check for updates and install the latest version",
"currentVersion": "Current Version",
"latestVersion": "Latest Version",
"checkForUpdates": "Check for Updates",
"checking": "Checking...",
"upToDate": "You're running the latest version",
"updateAvailable": "Update available: v{{version}}",
"updateNow": "Update Now",
"updating": "Updating...",
"updateStep": {
  "downloading": "Downloading...",
  "extracting": "Extracting...",
  "replacing": "Replacing binary...",
  "restarting": "Restarting service...",
  "complete": "Update complete!",
  "error": "Update failed"
},
"releaseNotes": "Release Notes",
"backup": "Settings Backup",
"backupDescription": "Download a backup of your database and configuration",
"downloadBackup": "Download Backup",
"downloadingBackup": "Downloading...",
"restore": "Restore from Backup",
"restoreDescription": "Upload a backup file to restore database and configuration",
"restoreUpload": "Select Backup File",
"restoring": "Restoring...",
"restoreConfirm": "This will replace your current database and configuration. The service will restart. Continue?",
"restoreSuccess": "Backup restored. Service is restarting...",
"backupFailed": "Backup download failed",
"restoreFailed": "Restore failed"
```

**Step 2: `ko.json`의 `settings` 객체에 동일 키 한국어 추가**

```json
"update": "패널 업데이트",
"updateDescription": "최신 버전 확인 및 업데이트 설치",
"currentVersion": "현재 버전",
"latestVersion": "최신 버전",
"checkForUpdates": "업데이트 확인",
"checking": "확인 중...",
"upToDate": "최신 버전을 사용 중입니다",
"updateAvailable": "업데이트 가능: v{{version}}",
"updateNow": "지금 업데이트",
"updating": "업데이트 중...",
"updateStep": {
  "downloading": "다운로드 중...",
  "extracting": "압축 해제 중...",
  "replacing": "바이너리 교체 중...",
  "restarting": "서비스 재시작 중...",
  "complete": "업데이트 완료!",
  "error": "업데이트 실패"
},
"releaseNotes": "릴리즈 노트",
"backup": "설정 백업",
"backupDescription": "데이터베이스와 설정 파일을 백업으로 다운로드합니다",
"downloadBackup": "백업 다운로드",
"downloadingBackup": "다운로드 중...",
"restore": "백업에서 복원",
"restoreDescription": "백업 파일을 업로드하여 데이터베이스와 설정을 복원합니다",
"restoreUpload": "백업 파일 선택",
"restoring": "복원 중...",
"restoreConfirm": "현재 데이터베이스와 설정이 교체됩니다. 서비스가 재시작됩니다. 계속하시겠습니까?",
"restoreSuccess": "백업이 복원되었습니다. 서비스를 재시작합니다...",
"backupFailed": "백업 다운로드 실패",
"restoreFailed": "복원 실패"
```

Dashboard 배너용 키도 추가 (en.json `dashboard` 객체):
```json
"updateBanner": "A new version (v{{version}}) is available.",
"updateBannerAction": "Go to Settings"
```

ko.json `dashboard` 객체:
```json
"updateBanner": "새 버전 (v{{version}})이 사용 가능합니다.",
"updateBannerAction": "설정으로 이동"
```

**Step 3: 커밋**

```bash
git add web/src/i18n/locales/ko.json web/src/i18n/locales/en.json
git commit -m "feat: 업데이트/백업 관련 i18n 번역 키 추가"
```

---

### Task 6: Frontend — Settings 페이지에 업데이트 + 백업 카드 추가

**Files:**
- Modify: `web/src/pages/Settings.tsx`

**Context:**
- 기존 Settings 페이지 구조: `web/src/pages/Settings.tsx` (415줄)
- 시스템 정보 카드 위에 2개 카드 삽입: (1) 패널 업데이트, (2) 설정 백업/복원
- 카드 디자인: `<div className="bg-card rounded-2xl p-6 card-shadow">`
- SSE 스트리밍 UI: 진행 단계 표시 (downloading → extracting → replacing → restarting → complete)
- 업데이트 버튼 클릭 시 `window.confirm()` 확인 후 진행

**Step 1: import 추가 + 새 state 변수**

파일 상단 import에 추가:
```typescript
import { Download, Upload, RefreshCw, CheckCircle, AlertCircle, ArrowRight } from 'lucide-react'
```

기존 state 아래에 업데이트/백업 state 추가:
```typescript
// Update state
const [updateChecking, setUpdateChecking] = useState(false)
const [updateInfo, setUpdateInfo] = useState<{ latest_version: string; update_available: boolean; release_notes: string } | null>(null)
const [updating, setUpdating] = useState(false)
const [updateStep, setUpdateStep] = useState('')
const [updateError, setUpdateError] = useState('')

// Backup state
const [backupLoading, setBackupLoading] = useState(false)
const [restoreLoading, setRestoreLoading] = useState(false)
```

**Step 2: 업데이트 확인 함수**

```typescript
async function handleCheckUpdate() {
  setUpdateChecking(true)
  setUpdateError('')
  try {
    const data = await api.checkUpdate()
    setUpdateInfo(data)
    if (!data.update_available) {
      toast.success(t('settings.upToDate'))
    }
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : 'Failed'
    toast.error(message)
  } finally {
    setUpdateChecking(false)
  }
}

async function handleRunUpdate() {
  if (!window.confirm(t('settings.restoreConfirm').replace('데이터베이스와 설정이 교체됩니다', '패널이 업데이트됩니다'))) return
  setUpdating(true)
  setUpdateStep('')
  setUpdateError('')
  try {
    await api.runUpdateStream((event) => {
      setUpdateStep(event.step)
      if (event.step === 'error') {
        setUpdateError(event.message)
        setUpdating(false)
      }
      if (event.step === 'complete') {
        // Wait for restart, then reload
        setTimeout(() => {
          const check = setInterval(() => {
            fetch('/api/v1/auth/setup-status')
              .then(() => { clearInterval(check); window.location.reload() })
              .catch(() => {})
          }, 2000)
        }, 3000)
      }
    })
  } catch {
    setUpdating(false)
    setUpdateError('Connection lost')
  }
}
```

**Step 3: 백업/복원 함수**

```typescript
async function handleDownloadBackup() {
  setBackupLoading(true)
  try {
    const blob = await api.downloadBackup()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `sfpanel-backup-${new Date().toISOString().slice(0,10)}.tar.gz`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : t('settings.backupFailed')
    toast.error(message)
  } finally {
    setBackupLoading(false)
  }
}

async function handleRestoreBackup(e: React.ChangeEvent<HTMLInputElement>) {
  const file = e.target.files?.[0]
  if (!file) return
  if (!window.confirm(t('settings.restoreConfirm'))) {
    e.target.value = ''
    return
  }
  setRestoreLoading(true)
  try {
    await api.restoreBackup(file)
    toast.success(t('settings.restoreSuccess'))
    // Wait for restart, then reload
    setTimeout(() => {
      const check = setInterval(() => {
        fetch('/api/v1/auth/setup-status')
          .then(() => { clearInterval(check); window.location.reload() })
          .catch(() => {})
      }, 2000)
    }, 3000)
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : t('settings.restoreFailed')
    toast.error(message)
  } finally {
    setRestoreLoading(false)
    e.target.value = ''
  }
}
```

**Step 4: JSX — 시스템 정보 카드(`{/* System Info */}`) 위에 2개 카드 삽입**

업데이트 카드:
```tsx
{/* Panel Update */}
<div className="bg-card rounded-2xl p-6 card-shadow">
  <h3 className="text-[15px] font-semibold">{t('settings.update')}</h3>
  <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.updateDescription')}</p>

  <div className="flex items-center gap-4 mb-4">
    <div className="space-y-1">
      <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.currentVersion')}</p>
      <p className="text-[13px] font-medium">{panelVersion}</p>
    </div>
    {updateInfo?.update_available && (
      <div className="space-y-1">
        <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.latestVersion')}</p>
        <p className="text-[13px] font-medium text-[#3182f6]">v{updateInfo.latest_version}</p>
      </div>
    )}
  </div>

  {updating ? (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <RefreshCw className="h-4 w-4 animate-spin text-[#3182f6]" />
        <span className="text-[13px]">
          {updateStep && t(`settings.updateStep.${updateStep}`, { defaultValue: updateStep })}
        </span>
      </div>
      {updateError && (
        <div className="flex items-center gap-2 text-[#f04452]">
          <AlertCircle className="h-4 w-4" />
          <span className="text-[13px]">{updateError}</span>
        </div>
      )}
    </div>
  ) : (
    <div className="flex gap-2">
      <Button onClick={handleCheckUpdate} disabled={updateChecking} className="rounded-xl" variant="outline">
        {updateChecking ? t('settings.checking') : t('settings.checkForUpdates')}
      </Button>
      {updateInfo?.update_available && (
        <Button onClick={handleRunUpdate} className="rounded-xl">
          {t('settings.updateNow')}
        </Button>
      )}
    </div>
  )}

  {updateInfo?.update_available && updateInfo.release_notes && (
    <details className="mt-4">
      <summary className="text-[13px] font-medium cursor-pointer">{t('settings.releaseNotes')}</summary>
      <pre className="mt-2 text-[12px] text-muted-foreground whitespace-pre-wrap bg-secondary/50 rounded-xl p-3 max-h-48 overflow-auto">
        {updateInfo.release_notes}
      </pre>
    </details>
  )}
</div>
```

백업/복원 카드:
```tsx
{/* Settings Backup */}
<div className="bg-card rounded-2xl p-6 card-shadow">
  <h3 className="text-[15px] font-semibold">{t('settings.backup')}</h3>
  <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.backupDescription')}</p>

  <div className="flex flex-wrap gap-3">
    <Button onClick={handleDownloadBackup} disabled={backupLoading} variant="outline" className="rounded-xl">
      <Download className="h-4 w-4 mr-2" />
      {backupLoading ? t('settings.downloadingBackup') : t('settings.downloadBackup')}
    </Button>

    <label>
      <Button asChild variant="outline" className="rounded-xl cursor-pointer" disabled={restoreLoading}>
        <span>
          <Upload className="h-4 w-4 mr-2" />
          {restoreLoading ? t('settings.restoring') : t('settings.restoreUpload')}
        </span>
      </Button>
      <input
        type="file"
        accept=".tar.gz,.tgz"
        onChange={handleRestoreBackup}
        className="hidden"
        disabled={restoreLoading}
      />
    </label>
  </div>
</div>
```

**Step 5: 프론트엔드 빌드 확인**

```bash
cd /opt/stacks/SFPanel/web && npx tsc --noEmit && npm run build
```

**Step 6: 커밋**

```bash
git add web/src/pages/Settings.tsx
git commit -m "feat: Settings 페이지에 업데이트/백업 UI 추가"
```

---

### Task 7: Frontend — Dashboard 업데이트 배너 + Layout 사이드바 배지

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/components/Layout.tsx`

**Context:**
- Dashboard는 이미 `api.getOverview()`로 `DashboardOverview` 데이터를 로드 (`Dashboard.tsx`)
- `DashboardOverview.update_info.update_available`이 true면 배너 표시
- Layout 사이드바: Settings NavLink에 파란 점 배지
- Layout에서 업데이트 상태 확인: `api.checkUpdate()`를 호출하거나, 간단히 `api.getSystemInfo()`의 overview를 사용

**Step 1: Dashboard — 업데이트 배너 추가**

Dashboard.tsx에서 overview 데이터를 이미 로드하는 부분 찾기. `update_info` 데이터를 사용하여 페이지 상단에 배너 삽입.

import에 `ArrowRight` 추가 (이미 있으면 skip), `useNavigate` 사용.

overview 데이터 로드 후 JSX의 페이지 헤더 바로 아래에:
```tsx
{overview?.update_info?.update_available && (
  <div className="bg-[#3182f6]/10 border border-[#3182f6]/20 rounded-2xl px-5 py-3 flex items-center justify-between">
    <span className="text-[13px] font-medium text-[#3182f6]">
      {t('dashboard.updateBanner', { version: overview.update_info.latest_version })}
    </span>
    <button
      onClick={() => navigate('/settings')}
      className="text-[13px] font-medium text-[#3182f6] hover:underline flex items-center gap-1"
    >
      {t('dashboard.updateBannerAction')}
      <ArrowRight className="h-3.5 w-3.5" />
    </button>
  </div>
)}
```

**Step 2: Layout — 사이드바 업데이트 배지**

`Layout.tsx`에서:

1. import 추가: `import type { UpdateInfo } from '@/types/api'`
2. state 추가: `const [updateAvailable, setUpdateAvailable] = useState(false)`
3. useEffect에서 업데이트 확인:
```typescript
useEffect(() => {
  api.checkUpdate()
    .then((data) => setUpdateAvailable(data.update_available))
    .catch(() => {})
}, [])
```

4. Settings NavLink 렌더링에서 배지 추가. `navItems.map` 내부에서 Settings 아이템에 조건부 파란 점:
```tsx
{navItems.map((item) => (
  <NavLink key={item.to} to={item.to} ...>
    <item.icon className="h-[18px] w-[18px] shrink-0" />
    {!collapsed && t(item.labelKey)}
    {item.to === '/settings' && updateAvailable && (
      <span className="ml-auto h-2 w-2 rounded-full bg-[#3182f6]" />
    )}
  </NavLink>
))}
```

**Step 3: 프론트엔드 빌드 확인**

```bash
cd /opt/stacks/SFPanel/web && npx tsc --noEmit && npm run build
```

**Step 4: 커밋**

```bash
git add web/src/pages/Dashboard.tsx web/src/components/Layout.tsx
git commit -m "feat: Dashboard 업데이트 배너 + 사이드바 업데이트 배지"
```

---

### Task 8: 빌드 + 배포 + 스펙 문서 업데이트

**Files:**
- Modify: `docs/specs/api-spec.md` (4개 엔드포인트 추가)
- Full build + deploy

**Step 1: 전체 빌드**

```bash
cd /opt/stacks/SFPanel/web && npm run build
cd /opt/stacks/SFPanel && go build -ldflags="-s -w" -trimpath -o sfpanel ./cmd/sfpanel
```

**Step 2: 배포**

```bash
sudo systemctl stop sfpanel
sudo cp /opt/stacks/SFPanel/sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
```

**Step 3: `docs/specs/api-spec.md`에 새 엔드포인트 문서 추가**

시스템 관리 섹션에 4개 엔드포인트 추가:
- `GET /api/v1/system/update-check` — 업데이트 확인
- `POST /api/v1/system/update` — SSE 스트리밍 업데이트 실행
- `POST /api/v1/system/backup` — 설정 백업 다운로드
- `POST /api/v1/system/restore` — 설정 복원 (multipart/form-data)

**Step 4: 커밋**

```bash
git add docs/specs/api-spec.md
git commit -m "docs: 셀프 관리 API 엔드포인트 문서 추가"
```

---

### Task 9: Playwright 기능 테스트

**Step 1:** 브라우저에서 `http://localhost:8443` 접속 후 로그인

**Step 2:** Settings 페이지로 이동하여 다음 확인:
- 업데이트 카드가 표시되는지
- "업데이트 확인" 버튼이 동작하는지 (GitHub API 호출)
- 백업 다운로드 버튼이 동작하는지
- 복원 파일 선택 UI가 표시되는지

**Step 3:** Dashboard에서 업데이트 배너 확인 (1시간 캐시이므로 즉시 표시되지 않을 수 있음)

**Step 4:** 사이드바 Settings 링크에 업데이트 배지 확인
