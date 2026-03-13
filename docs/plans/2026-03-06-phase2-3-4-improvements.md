# SFPanel Phase 2/3/4 개선 구현 계획

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** SFPanel의 안정성/성능 최적화, 코드 아키텍처 정리, 기능 완성을 3단계로 구현

**Architecture:** 백엔드(Go/Echo)는 기존 핸들러 패턴 유지하면서 파일 분할/공통화, 프론트엔드(React/TS)는 기존 api.ts 패턴 유지하면서 배치 엔드포인트 활용

**Tech Stack:** Go 1.24, Echo v4, Docker Go SDK, React 19, TypeScript, Vite 7

---

## Phase 2: 안정성 + 성능 (Task 1-6)

### Task 1: Docker N+1 쿼리 해소 — ListContainers 1회 통합

**Files:**
- Modify: `internal/docker/client.go:325-466`

**배경:** `ListImagesWithUsage`, `ListVolumesWithUsage`, `ListNetworksWithUsage`가 각각 `ListContainers(ctx)`를 호출하여 총 3회 중복. 1회로 통합.

**Step 1: 컨테이너 목록을 받는 내부 헬퍼 함수 추가**

`client.go`의 기존 3개 함수를 컨테이너를 인자로 받는 버전으로 변경:

```go
// client.go — 기존 함수 위에 추가
func (c *Client) listImagesWithContainers(ctx context.Context, containers []types.Container) ([]ImageWithUsage, error) {
	images, err := c.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	// ... 기존 ListImagesWithUsage 로직에서 containers 파라미터 사용
}

func (c *Client) listVolumesWithContainers(ctx context.Context, containers []types.Container) ([]VolumeWithUsage, error) {
	volumes, err := c.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	// ... 기존 ListVolumesWithUsage 로직에서 containers 파라미터 사용
}

func (c *Client) listNetworksWithContainers(ctx context.Context, containers []types.Container) ([]NetworkWithUsage, error) {
	networks, err := c.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	// ... 기존 ListNetworksWithUsage 로직에서 containers 파라미터 사용
}
```

**Step 2: 기존 public 함수를 래퍼로 변경**

기존 `ListImagesWithUsage` 등은 내부적으로 containers를 한 번 조회한 뒤 내부 함수 호출:

```go
func (c *Client) ListImagesWithUsage(ctx context.Context) ([]ImageWithUsage, error) {
	containers, err := c.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	return c.listImagesWithContainers(ctx, containers)
}
// ListVolumesWithUsage, ListNetworksWithUsage도 동일 패턴
```

**Step 3: 핸들러에서 일괄 호출하는 곳이 있으면 containers 공유**

`handlers/docker.go`에서 images/volumes/networks를 동시에 조회하는 곳이 없으므로 (각각 별도 엔드포인트), 현재는 Step 2만으로 충분. 향후 대시보드 통합 엔드포인트에서 containers 1회 조회 후 3개 함수에 전달.

**Step 4: 빌드 검증**

```bash
cd /opt/stacks/SFPanel && go build ./...
```

**Step 5: 커밋**

```bash
git add internal/docker/client.go
git commit -m "perf: Docker N+1 쿼리 해소 — ListContainers 1회 통합"
```

---

### Task 2: 컨테이너 Stats 배치 엔드포인트

**Files:**
- Modify: `internal/docker/client.go` (새 메서드 추가)
- Modify: `internal/api/handlers/docker.go` (새 핸들러 추가)
- Modify: `internal/api/router.go` (라우트 등록)
- Modify: `web/src/lib/api.ts` (새 API 메서드)
- Modify: `web/src/types/api.ts` (배치 응답 타입)
- Modify: `web/src/pages/docker/DockerContainers.tsx` (배치 호출로 전환)

**배경:** 현재 `ContainerStatsCell` 컴포넌트가 컨테이너별로 5초마다 개별 API 호출. 20개 컨테이너 = 초당 4 API 호출. 배치 엔드포인트로 1회 호출로 통합.

**Step 1: Docker client에 배치 stats 메서드 추가**

```go
// internal/docker/client.go
type ContainerStatsResult struct {
	ID         string  `json:"id"`
	CPUPercent float64 `json:"cpu_percent"`
	MemPercent float64 `json:"mem_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
}

func (c *Client) ContainerStatsBatch(ctx context.Context) ([]ContainerStatsResult, error) {
	containers, err := c.ListContainers(ctx)
	if err != nil {
		return nil, err
	}

	var results []ContainerStatsResult
	for _, ct := range containers {
		if ct.State != "running" {
			continue
		}
		stats, err := c.ContainerStats(ctx, ct.ID)
		if err != nil {
			continue // skip failed containers
		}
		results = append(results, ContainerStatsResult{
			ID:         ct.ID,
			CPUPercent: stats.CPUPercent,
			MemPercent: stats.MemPercent,
			MemUsage:   stats.MemUsage,
			MemLimit:   stats.MemLimit,
		})
	}
	return results, nil
}
```

**Step 2: 핸들러 추가**

```go
// internal/api/handlers/docker.go
func (h *DockerHandler) ContainerStatsBatch(c echo.Context) error {
	stats, err := h.Docker.ContainerStatsBatch(c.Request().Context())
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, stats)
}
```

**Step 3: 라우터에 엔드포인트 등록**

`router.go`의 Docker containers 그룹에 추가:
```go
containers.GET("/stats/batch", dockerHandler.ContainerStatsBatch)
```

**Step 4: 프론트엔드 API 메서드 + 타입 추가**

```typescript
// web/src/types/api.ts
export interface ContainerStatsResult {
  id: string
  cpu_percent: number
  mem_percent: number
  mem_usage: number
  mem_limit: number
}

// web/src/lib/api.ts
async containerStatsBatch(): Promise<ContainerStatsResult[]> {
  return this.request('/docker/containers/stats/batch')
}
```

**Step 5: DockerContainers.tsx에서 배치 호출로 전환**

기존 `ContainerStatsCell`의 개별 폴링을 제거하고, 부모 컴포넌트에서 배치 조회:

```tsx
// DockerContainers.tsx — 부모에서 배치 조회
const [statsMap, setStatsMap] = useState<Record<string, ContainerStatsResult>>({})

useEffect(() => {
  const fetchStats = async () => {
    try {
      const stats = await api.containerStatsBatch()
      const map: Record<string, ContainerStatsResult> = {}
      stats.forEach(s => { map[s.id] = s })
      setStatsMap(map)
    } catch { /* ignore */ }
  }
  fetchStats()
  const interval = setInterval(fetchStats, 5000)
  return () => clearInterval(interval)
}, [])

// ContainerStatsCell을 인라인 표시로 변경
// stats={statsMap[container.id]} prop으로 전달
```

**Step 6: 빌드 검증**

```bash
cd /opt/stacks/SFPanel && go build ./... && cd web && npx tsc --noEmit
```

**Step 7: 커밋**

```bash
git add internal/docker/client.go internal/api/handlers/docker.go internal/api/router.go web/src/lib/api.ts web/src/types/api.ts web/src/pages/docker/DockerContainers.tsx
git commit -m "perf: 컨테이너 Stats 배치 엔드포인트 추가 — N개 개별 호출 → 1회 통합"
```

---

### Task 3: 파일 쓰기 백업 + 원자적 쓰기

**Files:**
- Modify: `internal/api/handlers/files.go:181-213`

**배경:** 현재 `WriteFile`이 직접 `os.WriteFile`로 덮어씀. 시스템 파일 편집 시 실수하면 복구 불가. 기존 파일 백업 + 임시 파일 → rename 패턴 적용.

**Step 1: WriteFile 핸들러에 백업 + 원자적 쓰기 적용**

```go
// internal/api/handlers/files.go — WriteFile 핸들러 내부
// 기존 파일이 있으면 .bak 백업 생성
if _, err := os.Stat(req.Path); err == nil {
	backupPath := req.Path + ".bak"
	_ = os.Remove(backupPath) // 이전 백업 제거
	if err := os.Rename(req.Path, backupPath); err != nil {
		// rename 실패 시 (cross-device 등) copy fallback
		data, readErr := os.ReadFile(req.Path)
		if readErr == nil {
			_ = os.WriteFile(backupPath, data, 0644)
		}
	}
}

// 임시 파일에 쓰고 rename (원자적)
tmpPath := req.Path + ".sfpanel.tmp"
if err := os.WriteFile(tmpPath, []byte(req.Content), 0644); err != nil {
	// 에러 처리...
}
if err := os.Rename(tmpPath, req.Path); err != nil {
	os.Remove(tmpPath)
	// 에러 처리...
}
```

**Step 2: 업로드 핸들러도 동일 패턴 적용**

`UploadFile` 핸들러(~line 433)에서도 임시 파일 → rename 패턴 적용.

**Step 3: 빌드 검증**

```bash
go build ./...
```

**Step 4: 커밋**

```bash
git add internal/api/handlers/files.go
git commit -m "fix: 파일 쓰기 시 .bak 백업 + 원자적 쓰기(tmp→rename) 적용"
```

---

### Task 4: 로그 스트리밍 tail -F 전환 (로그 rotate 지원)

**Files:**
- Modify: `internal/api/handlers/logs.go:263` (및 필터 케이스)

**배경:** 현재 `tail -f` 사용. logrotate가 파일을 rename 하면 구 inode를 계속 추적하여 새 로그를 놓침. `tail -F`(대문자)는 파일 이름을 추적하여 rotate 후에도 새 파일을 자동으로 follow.

**Step 1: tail -f → tail -F 변경**

```go
// logs.go — LogStreamWS 내부
// Before: exec.Command("tail", "-f", info.Path)
// After:
tailCmd := exec.Command("tail", "-F", info.Path)
```

두 곳 모두 변경 (필터 있는 경우 + 없는 경우).

**Step 2: 빌드 검증**

```bash
go build ./...
```

**Step 3: 커밋**

```bash
git add internal/api/handlers/logs.go
git commit -m "fix: 로그 스트리밍 tail -f → tail -F 전환 (logrotate 지원)"
```

---

### Task 5: Graceful Shutdown 구현

**Files:**
- Modify: `cmd/sfpanel/main.go:105-114`

**배경:** 현재 `e.Start(addr)` 블로킹 후 종료. SIGTERM 수신 시 진행 중 요청 완료 없이 즉시 종료됨. systemd stop 시 클린 셧다운 필요.

**Step 1: 시그널 핸들러 + context 기반 shutdown 추가**

```go
// cmd/sfpanel/main.go — 서버 시작 부분 교체
addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
log.Printf("SFPanel %s starting on %s", version, addr)

// Graceful shutdown
go func() {
	if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}()

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

log.Println("Shutting down server...")
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := e.Shutdown(ctx); err != nil {
	log.Fatalf("Server forced shutdown: %v", err)
}
log.Println("Server stopped")
```

**Step 2: 필요한 import 추가**

```go
import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)
```

**Step 3: 빌드 검증**

```bash
go build ./...
```

**Step 4: 커밋**

```bash
git add cmd/sfpanel/main.go
git commit -m "feat: Graceful shutdown 구현 — SIGTERM 시 10초 대기 후 종료"
```

---

### Task 6: 이미지 Pull SSE 스트리밍

**Files:**
- Modify: `internal/api/handlers/docker.go` (PullImage 핸들러 리팩토링)
- Modify: `web/src/lib/api.ts` (SSE 클라이언트 추가)
- Modify: `web/src/pages/docker/DockerImages.tsx` (진행률 UI)

**배경:** 현재 `PullImage`가 `io.Copy(io.Discard, reader)`로 전체 다운로드 완료까지 HTTP 블로킹. 대형 이미지 시 타임아웃. packages.go의 Docker 설치에서 이미 SSE 패턴 사용 중이므로 동일 패턴 적용.

**Step 1: PullImage를 SSE 스트리밍으로 변경**

```go
// internal/api/handlers/docker.go — PullImage 교체
func (h *DockerHandler) PullImage(c echo.Context) error {
	var req struct {
		Image string `json:"image"`
	}
	if err := c.Bind(&req); err != nil || req.Image == "" {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "image required")
	}

	reader, err := h.Docker.PullImage(c.Request().Context(), req.Image)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	defer reader.Close()

	// SSE 스트리밍
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	decoder := json.NewDecoder(reader)
	flusher := c.Response()
	for {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			break
		}
		data, _ := json.Marshal(event)
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	fmt.Fprintf(flusher, "data: {\"status\":\"complete\",\"image\":\"%s\"}\n\n", req.Image)
	flusher.Flush()
	return nil
}
```

**Step 2: 프론트엔드 SSE 클라이언트**

```typescript
// web/src/lib/api.ts
async pullImageStream(
  imageName: string,
  onProgress: (event: { status: string; progress?: string }) => void
): Promise<void> {
  const res = await fetch(`${API_BASE}/docker/images/pull`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${getToken()}` },
    body: JSON.stringify({ image: imageName }),
  })
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
        try { onProgress(JSON.parse(line.slice(6))) } catch {}
      }
    }
  }
}
```

**Step 3: DockerImages.tsx에서 진행률 표시 UI 추가**

기존 `handlePullImage` 함수를 `pullImageStream` 사용으로 변경하고, 진행 상태를 Dialog 또는 toast로 표시.

**Step 4: 빌드 검증**

```bash
go build ./... && cd web && npx tsc --noEmit
```

**Step 5: 커밋**

```bash
git add internal/api/handlers/docker.go web/src/lib/api.ts web/src/pages/docker/DockerImages.tsx
git commit -m "feat: 이미지 Pull SSE 스트리밍 — 실시간 진행률 표시"
```

---

## Phase 3: 아키텍처 정리 (Task 7-10)

### Task 7: disk.go 6개 파일로 분할

**Files:**
- Modify: `internal/api/handlers/disk.go` → 6개 파일로 분할
- 기존 라우터 등록은 변경 불필요 (같은 패키지, 같은 struct)

**배경:** 2,957줄 단일 파일 → 기능별 분할. `DiskHandler` struct는 그대로 유지, 메서드만 파일별 분리.

**Step 1: 타입 정의 + 검증 함수 → disk.go (기존 파일 축소)**

기존 `disk.go`에 `DiskHandler` struct 정의, 타입 정의, 검증 함수만 남김 (~350줄).

**Step 2: 블록 디바이스 + SMART → disk_blocks.go**

`ListDisks`, `GetSmartInfo`, `CheckSmartmontools`, `InstallSmartmontools` 핸들러 이동.

**Step 3: 파티션 → disk_partitions.go**

`ListPartitions`, `CreatePartition`, `DeletePartition` 핸들러 이동.

**Step 4: 파일시스템 → disk_filesystems.go**

`ListFilesystems`, `FormatPartition`, `MountFilesystem`, `UnmountFilesystem`, `ResizeFilesystem`, `CheckExpandable`, `ExpandFilesystem` + 관련 헬퍼 함수 이동.

**Step 5: LVM → disk_lvm.go**

LVM 관련 모든 핸들러 + 헬퍼 이동.

**Step 6: RAID → disk_raid.go**

RAID 관련 모든 핸들러 이동.

**Step 7: Swap + I/O + Usage → disk_swap.go**

`GetSwapInfo`, `CreateSwap`, `RemoveSwap`, `SetSwappiness`, swap resize + `GetIOStats`, `GetDiskUsage` 이동.

**Step 8: 빌드 검증**

```bash
go build ./...
```

**Step 9: 커밋**

```bash
git add internal/api/handlers/disk*.go
git commit -m "refactor: disk.go 2957줄 → 7개 파일로 분할 (blocks/partitions/fs/lvm/raid/swap)"
```

---

### Task 8: firewall.go 4개 파일로 분할

**Files:**
- Modify: `internal/api/handlers/firewall.go` → 4개 파일로 분할

**배경:** 1,667줄 단일 파일 → 기능별 분할. 같은 패턴으로 `FirewallHandler` struct 유지.

**Step 1: 타입 + 검증 → firewall.go (축소)**

`FirewallHandler` struct, 타입 정의, 검증 정규식만 남김.

**Step 2: UFW → firewall_ufw.go**

`GetUFWStatus`, `EnableUFW`, `DisableUFW`, `ListRules`, `AddRule`, `DeleteRule`, `ListPorts` 이동.

**Step 3: Fail2ban → firewall_fail2ban.go**

모든 Fail2ban 핸들러 이동.

**Step 4: Docker 방화벽 → firewall_docker.go**

Docker 방화벽 핸들러 이동.

**Step 5: 빌드 검증 + 커밋**

```bash
go build ./...
git add internal/api/handlers/firewall*.go
git commit -m "refactor: firewall.go 1667줄 → 4개 파일로 분할 (ufw/fail2ban/docker)"
```

---

### Task 9: 공통 exec 헬퍼 추출

**Files:**
- Create: `internal/api/handlers/exec.go`
- Modify: `internal/api/handlers/packages.go` (로컬 runCommand 제거)
- Modify: `internal/api/handlers/wireguard.go` (로컬 runCommand 제거)
- Modify: `internal/api/handlers/tailscale.go` (로컬 runCommand 제거)
- Modify: `internal/api/handlers/services.go` (필요 시)

**배경:** `runCommand`, `runCommandEnv`, `commandExists` 등이 여러 핸들러에 중복 정의. 공통 패키지로 추출.

**Step 1: exec.go 생성**

```go
// internal/api/handlers/exec.go
package handlers

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

const defaultCommandTimeout = 5 * time.Minute

func runCommand(name string, args ...string) (string, error) {
	return runCommandWithTimeout(defaultCommandTimeout, name, args...)
}

func runCommandWithTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w", name, err)
	}
	return string(output), nil
}

func runCommandEnv(env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w", name, err)
	}
	return string(output), nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
```

**Step 2: 각 핸들러에서 로컬 중복 함수 제거**

`packages.go`, `wireguard.go`, `tailscale.go` 등에서 로컬 `runCommand` 정의를 삭제. 같은 패키지이므로 import 불필요.

**Step 3: 빌드 검증 + 커밋**

```bash
go build ./...
git add internal/api/handlers/exec.go internal/api/handlers/packages.go internal/api/handlers/wireguard.go internal/api/handlers/tailscale.go
git commit -m "refactor: 공통 exec 헬퍼 추출 — runCommand/commandExists 중복 제거"
```

---

### Task 10: 에러 코드 표준화

**Files:**
- Create: `internal/api/response/errors.go`
- Modify: 주요 핸들러들 (에러 코드 상수 적용)

**배경:** 핸들러별로 임의의 에러 코드 문자열 사용 ("DOCKER_ERROR", "INVALID_REQUEST" 등). 표준 상수로 정의.

**Step 1: 에러 코드 상수 정의**

```go
// internal/api/response/errors.go
package response

// Common error codes
const (
	ErrInvalidRequest  = "INVALID_REQUEST"
	ErrMissingFields   = "MISSING_FIELDS"
	ErrNotFound        = "NOT_FOUND"
	ErrPermissionDenied = "PERMISSION_DENIED"
	ErrInternalError   = "INTERNAL_ERROR"

	// Auth
	ErrInvalidCredentials = "INVALID_CREDENTIALS"
	ErrInvalidToken       = "INVALID_TOKEN"
	ErrTOTPRequired       = "TOTP_REQUIRED"

	// Docker
	ErrDockerError     = "DOCKER_ERROR"
	ErrDockerNotAvail  = "DOCKER_NOT_AVAILABLE"

	// File
	ErrFileError   = "FILE_ERROR"
	ErrInvalidPath = "INVALID_PATH"

	// System
	ErrCommandFailed = "COMMAND_FAILED"
	ErrServiceError  = "SERVICE_ERROR"

	// Firewall
	ErrFirewallError  = "FIREWALL_ERROR"
	ErrFail2banError  = "FAIL2BAN_ERROR"

	// Disk
	ErrDiskError = "DISK_ERROR"
	ErrLVMError  = "LVM_ERROR"
	ErrRAIDError = "RAID_ERROR"
	ErrSwapError = "SWAP_ERROR"

	// Network
	ErrNetworkError   = "NETWORK_ERROR"
	ErrWireGuardError = "WIREGUARD_ERROR"
	ErrTailscaleError = "TAILSCALE_ERROR"
)
```

**Step 2: 주요 핸들러에서 문자열 → 상수로 교체**

각 핸들러에서 `response.Fail(c, status, "DOCKER_ERROR", msg)` → `response.Fail(c, status, response.ErrDockerError, msg)` 로 변경. 한 파일씩 순차적으로 적용.

**Step 3: 빌드 검증 + 커밋**

```bash
go build ./...
git add internal/api/response/errors.go internal/api/handlers/*.go
git commit -m "refactor: 에러 코드 표준화 — 문자열 리터럴 → 상수 정의"
```

---

## Phase 4: 기능 완성 (Task 11-14)

### Task 11: 대시보드 통합 엔드포인트

**Files:**
- Modify: `internal/api/handlers/dashboard.go` (새 핸들러 추가)
- Modify: `internal/api/router.go` (라우트 등록)
- Modify: `web/src/lib/api.ts` (API 메서드)
- Modify: `web/src/types/api.ts` (응답 타입)
- Modify: `web/src/pages/Dashboard.tsx` (단일 호출로 변경)

**배경:** 대시보드 로드 시 4+ API 호출 → 단일 엔드포인트로 통합. 초기 렌더링 속도 개선.

**Step 1: 통합 응답 타입 + 핸들러 추가**

```go
// internal/api/handlers/dashboard.go
type DashboardOverview struct {
	SystemInfo     interface{} `json:"system_info"`
	MetricsHistory interface{} `json:"metrics_history"`
	TopProcesses   interface{} `json:"top_processes"`
	RecentLogs     interface{} `json:"recent_logs"`
	Containers     interface{} `json:"containers,omitempty"`
}

func (h *DashboardHandler) GetOverview(c echo.Context) error {
	// 병렬로 데이터 수집
	// ... systemInfo, metricsHistory, topProcesses, recentLogs
	return response.OK(c, overview)
}
```

**Step 2: 라우터 등록**

```go
// router.go — system 그룹에 추가
system.GET("/overview", dashboardHandler.GetOverview)
```

**Step 3: 프론트엔드 API + Dashboard.tsx 업데이트**

```typescript
// api.ts
async getDashboardOverview(): Promise<DashboardOverview> {
  return this.request('/system/overview')
}
```

Dashboard.tsx에서 기존 4개 `useEffect` → 단일 `getDashboardOverview()` 호출로 통합.

**Step 4: 빌드 검증 + 커밋**

```bash
go build ./... && cd web && npx tsc --noEmit
git add internal/api/handlers/dashboard.go internal/api/router.go web/src/lib/api.ts web/src/types/api.ts web/src/pages/Dashboard.tsx
git commit -m "feat: 대시보드 통합 엔드포인트 — 4개 API 호출 → 1개로 통합"
```

---

### Task 12: 네트워크 통합 엔드포인트

**Files:**
- Modify: `internal/api/handlers/network.go` (새 핸들러)
- Modify: `internal/api/router.go`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/network/NetworkInterfaces.tsx`

**배경:** NetworkInterfaces 페이지에서 interfaces+routes+dns+bonds 4개 API 동시 호출 → 단일 엔드포인트.

**Step 1: 통합 핸들러**

```go
// network.go
func (h *NetworkHandler) GetNetworkStatus(c echo.Context) error {
	// interfaces, routes, dns, bonds를 한번에 수집
	return response.OK(c, map[string]interface{}{
		"interfaces": interfaces,
		"routes":     routes,
		"dns":        dnsConfig,
		"bonds":      bonds,
	})
}
```

**Step 2: 라우터 + 프론트엔드 연동**

```go
network.GET("/status", networkHandler.GetNetworkStatus)
```

**Step 3: NetworkInterfaces.tsx에서 단일 호출로 변경**

기존 `Promise.all([4개])` → `api.getNetworkStatus()` 1개로 변경.

**Step 4: 빌드 검증 + 커밋**

```bash
go build ./... && cd web && npx tsc --noEmit
git add internal/api/handlers/network.go internal/api/router.go web/src/lib/api.ts web/src/pages/network/NetworkInterfaces.tsx
git commit -m "feat: 네트워크 통합 엔드포인트 — 4개 API → 1개 통합"
```

---

### Task 13: Fail2ban ignoreip UI

**Files:**
- Modify: `internal/api/handlers/firewall.go` (또는 firewall_fail2ban.go 분할 후)
- Modify: `web/src/pages/firewall/FirewallFail2ban.tsx`
- Modify: `web/src/i18n/locales/ko.json`
- Modify: `web/src/i18n/locales/en.json`

**배경:** Fail2ban jail 생성/수정 시 `ignoreip` 필드 없음. 관리자 자기 IP를 화이트리스트에 추가해야 셀프 잠김 방지 가능.

**Step 1: 백엔드 — jail 설정에 ignoreip 필드 추가**

기존 `CreateJail` / `UpdateJailConfig` 요청 구조체에 `IgnoreIP string` 필드 추가. jail.local 파일 생성 시 `ignoreip = <value>` 줄 포함.

**Step 2: 프론트엔드 — jail 생성/수정 Dialog에 ignoreip 입력 추가**

FirewallFail2ban.tsx의 jail 생성 Dialog에 `ignoreip` 텍스트 입력 추가 (placeholder: "127.0.0.1/8 ::1 your.ip").

**Step 3: i18n 번역 키 추가**

```json
// ko.json
"firewall.fail2ban.ignoreIp": "화이트리스트 IP",
"firewall.fail2ban.ignoreIpHelp": "차단에서 제외할 IP (공백으로 구분)"
// en.json
"firewall.fail2ban.ignoreIp": "Whitelist IPs",
"firewall.fail2ban.ignoreIpHelp": "IPs to exclude from banning (space-separated)"
```

**Step 4: 빌드 검증 + 커밋**

```bash
go build ./... && cd web && npx tsc --noEmit
git add internal/api/handlers/firewall*.go web/src/pages/firewall/FirewallFail2ban.tsx web/src/i18n/locales/*.json
git commit -m "feat: Fail2ban ignoreip UI 추가 — 관리자 IP 화이트리스트"
```

---

### Task 14: Systemd 서비스 의존성 표시

**Files:**
- Modify: `internal/api/handlers/services.go`
- Modify: `web/src/types/api.ts`
- Modify: `web/src/pages/Services.tsx`

**배경:** 서비스 재시작 시 연쇄 영향 파악 불가. `systemctl show` 출력에서 `Requires`, `RequiredBy`, `WantedBy` 정보 파싱.

**Step 1: ServiceInfo 구조체에 의존성 필드 추가**

```go
// services.go — ServiceInfo struct
type ServiceInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	LoadState   string   `json:"load_state"`
	ActiveState string   `json:"active_state"`
	SubState    string   `json:"sub_state"`
	Requires    []string `json:"requires,omitempty"`
	RequiredBy  []string `json:"required_by,omitempty"`
	WantedBy    []string `json:"wanted_by,omitempty"`
}
```

**Step 2: 서비스 상세 조회 시 의존성 파싱**

```go
// systemctl show 출력에서 파싱
output, _ := runCommand("systemctl", "show", name,
	"--property=Requires,RequiredBy,WantedBy")
// Requires=network-online.target\nRequiredBy=docker.service\n...
```

**Step 3: 프론트엔드에서 의존성 표시**

서비스 재시작 확인 Dialog에 "이 서비스에 의존하는 서비스" 목록 표시:

```tsx
{service.required_by?.length > 0 && (
  <div className="text-[13px] text-muted-foreground mt-2">
    {t('services.dependents')}: {service.required_by.join(', ')}
  </div>
)}
```

**Step 4: 빌드 검증 + 커밋**

```bash
go build ./... && cd web && npx tsc --noEmit
git add internal/api/handlers/services.go web/src/types/api.ts web/src/pages/Services.tsx web/src/i18n/locales/*.json
git commit -m "feat: Systemd 서비스 의존성(Requires/RequiredBy) 표시"
```

---

## 태스크 요약

| Phase | Task | 설명 | 예상 영향 |
|-------|------|------|----------|
| **2** | 1 | Docker N+1 쿼리 해소 | Docker 페이지 40% 빠름 |
| **2** | 2 | 컨테이너 Stats 배치 | API 호출 95% 감소 (20→1) |
| **2** | 3 | 파일 백업 + 원자적 쓰기 | 데이터 손실 방지 |
| **2** | 4 | tail -F 전환 | 로그 rotate 시 스트림 유지 |
| **2** | 5 | Graceful shutdown | 클린 종료, 요청 완료 보장 |
| **2** | 6 | 이미지 Pull SSE | 대형 이미지 타임아웃 해소 |
| **3** | 7 | disk.go 분할 | 유지보수성 대폭 향상 |
| **3** | 8 | firewall.go 분할 | 유지보수성 향상 |
| **3** | 9 | exec 헬퍼 통합 | 코드 중복 제거 |
| **3** | 10 | 에러 코드 표준화 | 프론트엔드 에러 처리 개선 |
| **4** | 11 | 대시보드 통합 엔드포인트 | 초기 로드 50% 빠름 |
| **4** | 12 | 네트워크 통합 엔드포인트 | 네트워크 페이지 75% 빠름 |
| **4** | 13 | Fail2ban ignoreip UI | 셀프 잠김 방지 |
| **4** | 14 | 서비스 의존성 표시 | 안전한 서비스 관리 |
