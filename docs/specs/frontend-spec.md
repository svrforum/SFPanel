# SFPanel 프론트엔드 스펙

> 마지막 전체 동기화: 2026-04-19 · 기준 버전: v0.9.0 · 근거: `docs/superpowers/research/2026-04-19-docs-overhaul/frontend-inventory.md`
>
> v0.10.0 이후 추가된 페이지/컴포넌트는 본 문서에 미반영입니다. 권한 있는 출처는 `web/src/`이며, 변경 요약은 `CHANGELOG.md`를 참조하세요. 본 문서가 코드와 어긋날 경우 코드를 우선시합니다.
>
> **부분 갱신: 2026-06-03 · v0.40.0** — v0.19.0~v0.40.0 개선 캠페인에서 추가/변경된 프론트엔드 표면(신규 공용 컴포넌트, 횡단 UI 패턴, 신규 페이지/플로우, API 클라이언트 메서드)을 반영했습니다. 변경 항목에는 `(v0.NN.0)` 표기를 달았습니다. 캠페인 이전 본문은 v0.9.0 기준이며, 일부 페이지(특히 Settings)는 그동안 구조가 바뀌어 해당 섹션에 갱신 노트를 덧붙였습니다.

## 개요

- **프레임워크**: React 19 + TypeScript + Vite 7
- **스타일**: Tailwind CSS v4 + shadcn/ui (일부 컴포넌트) + Toss 디자인 시스템 영향 (컬러, 라운딩, 그림자)
- **상태 관리**: React hooks (useState, useEffect, useCallback, useRef, useMemo)
- **라우팅**: React Router v7 (BrowserRouter)
- **국제화**: react-i18next + i18next-browser-languagedetector (한국어/영어)
- **토스트 알림**: Sonner (via shadcn/ui 래퍼)
- **코드 에디터**: Monaco Editor (@monaco-editor/react)
- **터미널**: xterm.js (@xterm/xterm + fit/web-links/search 애드온)
- **차트**: uPlot v1.6 (시계열 차트)
- **아이콘**: Lucide React
- **QR 코드**: qrcode.react (`QRCodeSVG`) — Settings 보안 탭 2FA QR + WireGuard 피어 클라이언트 config QR
- **엔트리포인트**: `web/src/main.tsx` -> `<App />`
- **CSS**: `web/src/index.css` (Tailwind 설정)
- **코드 분할**: `React.lazy()` + `<Suspense>`로 모든 페이지를 lazy loading

---

## 라우팅

`App.tsx`에서 정의. `SetupGuard`가 최상위에서 초기 셋업 여부를 체크하고, `ProtectedRoute`가 JWT 토큰 기반 인증을 검증한다. 모든 페이지 컴포넌트는 `React.lazy()`로 동적 임포트되며, `<Suspense fallback={<PageLoader />}>`로 감싸져 코드 분할을 구현한다.

| 경로 | 컴포넌트 | 인증 필요 | 레이아웃 | 설명 |
|------|----------|-----------|----------|------|
| `/connect` | Connect | X | 없음 (독립) | Tauri 데스크톱 서버 접속 (URL 입력, health check, 진단, 언어 선택) |
| `/login` | Login | X | 없음 (독립) | 관리자 로그인 |
| `/setup` | Setup | X | 없음 (독립) | 초기 관리자 계정 생성 (첫 실행 시) |
| `/` | - | O | Layout | `/dashboard`로 리다이렉트 |
| `/dashboard` | Dashboard | O | Layout | 시스템 대시보드 (실시간 메트릭) |
| `/appstore` | AppStore | O | Layout | 앱스토어 (원클릭 Docker 앱 설치) |
| `/appstore` (모달) | AppStoreDetailModal | O | AppStore 내장 | 앱 상세 + 설치 모달 (SSE 설치 진행률) |
| `/cluster` | Cluster | O | Layout | 클러스터 관리 (사이드 탭 + Outlet 구조) |
| `/cluster/overview` | ClusterOverview | O | Cluster | 클러스터 개요 + 초기화 + **disband(TypeToConfirm, v0.30.0)**. **(v0.31.0)** 마운트 시 1회 fetch 후 `/ws/cluster/overview` WebSocket으로 status+overview+recent events 통합 스냅샷 수신(기존 15s 3회 폴링 대체, follower는 `stale` 플래그+배너). **(v0.22.0)** 클러스터 업데이트 뷰는 **per-node 스테퍼 + 전체 진행률 바**(완료/전체, 실패 수; 구조화 SSE 기반 — `api.clusterUpdateStream`) |
| `/cluster/nodes` | ClusterNodes | O | Cluster | 노드 목록 + 제거/리더 이전/라벨 편집. **(v0.19.0)** 노드 **advertised 주소 인라인 편집**(`api.updateClusterNodeAddress`) + 로컬 노드 행에서 **클러스터 떠나기**(quorum-loss force 오버라이드, `api.leaveCluster(force?)`) |
| `/cluster/tokens` | ClusterTokens | O | Cluster | 참가 토큰 생성 + join 명령어 표시. **(v0.19.0)** 발급된 토큰 **목록/취소**(마스킹된 값 + 짧은 지문만 노출; `api.listClusterTokens`, `api.revokeClusterToken`) |
| `/docker` | Docker | O | Layout | Docker 관리 (사이드 탭 + Outlet 구조) |
| `/docker/stacks` | DockerStacks | O | Docker | Docker Compose 스택 목록 (기본 서브라우트) |
| `/docker/stacks/:name` | DockerStacks | O | Docker | 스택 상세 (서비스 목록, YAML 편집, 로그, 셸) |
| `/docker/containers` | DockerContainers | O | Docker | 컨테이너 관리 |
| `/docker/images` | DockerImages | O | Docker | 이미지 관리 |
| `/docker/volumes` | DockerVolumes | O | Docker | 볼륨 관리 |
| `/docker/networks` | DockerNetworks | O | Docker | 네트워크 관리 |
| `/files` | Files | O | Layout | 파일 관리자 |
| `/cron` | CronJobs | O | Layout | 크론 작업 관리 |
| `/logs` | Logs | O | Layout | 시스템 로그 뷰어 |
| `/processes` | Processes | O | Layout | 프로세스 관리자 |
| `/services` | Services | O | Layout | Systemd 서비스 관리 |
| `/network` | Network | O | Layout | 네트워크/VPN 관리 (사이드 탭 + Outlet 구조) |
| `/network/interfaces` | NetworkInterfaces | O | Network | 네트워크 인터페이스 관리 (기본 서브라우트) |
| `/network/wireguard` | NetworkWireGuard | O | Network | WireGuard VPN 클라이언트 관리 |
| `/network/tailscale` | NetworkTailscale | O | Network | Tailscale VPN 클라이언트 관리 |
| `/disk` | Disk | O | Layout | 디스크/스토리지 관리 (사이드 탭 + Outlet 구조) |
| `/disk/overview` | DiskOverview | O | Disk | 디스크 개요 + S.M.A.R.T. + I/O 통계 (기본 서브라우트) |
| `/disk/partitions` | DiskPartitions | O | Disk | 파티션 관리 |
| `/disk/filesystems` | DiskFilesystems | O | Disk | 파일시스템 관리 |
| `/disk/lvm` | DiskLVM | O | Disk | LVM PV/VG/LV 관리 |
| `/disk/raid` | DiskRAID | O | Disk | RAID 배열 관리 |
| `/disk/swap` | DiskSwap | O | Disk | 스왑 관리 |
| `/firewall` | Firewall | O | Layout | 방화벽 관리 (사이드 탭 + Outlet 구조) |
| `/firewall/rules` | FirewallRules | O | Firewall | UFW 규칙 관리 (기본 서브라우트) |
| `/firewall/ports` | FirewallPorts | O | Firewall | 리스닝 포트 조회 |
| `/firewall/portmap` | FirewallPortmap | O | Firewall | 포트 맵 (UFW+Docker+프로세스 통합) |
| `/firewall/fail2ban` | FirewallFail2ban | O | Firewall | Fail2ban jail 관리 |
| `/firewall/docker` | FirewallDocker | O | Firewall | Docker 방화벽 (DOCKER-USER 체인) |
| `/firewall/logs` | FirewallLogs | O | Firewall | 방화벽 로그 뷰어 |
| `/packages` | Packages | O | Layout | 시스템 패키지 관리 + Docker 설치 |
| `/terminal` | Terminal | O | Layout | 웹 터미널 (멀티 탭) |
| `/settings` | Settings | O | Layout | 계정/시스템 설정 |
| `/settings` (내장) | SettingsTuning | O | Settings 내장 | 시스템 커널 튜닝 (sysctl 최적화, 4 카테고리, 롤백) |
| `/settings` (내장) | AlertSettings | O | Settings 내장 | 알림 채널/규칙 관리 (Discord, Telegram) |

### 라우트 가드

- **TauriGuard**: Tauri 데스크톱 환경에서만 활성화. 서버 URL이 설정되지 않은 경우 `/connect` 페이지로 리다이렉트. 웹 브라우저 환경에서는 패스스루.
- **SetupGuard**: 모든 라우트를 감싸고, `/setup` 경로가 아닌 경우 `api.getSetupStatus()`를 호출하여 `setup_required === true`이면 `/setup`으로 리다이렉트. **모듈 레벨 `setupChecked` 변수**로 결과를 캐싱하여 한 번 체크 후에는 재호출하지 않음.
- **ProtectedRoute**: `api.isAuthenticated()` (localStorage 토큰 존재 여부)를 체크하여, 미인증 시 `/login`으로 리다이렉트

### 코드 분할 (Code Splitting)

모든 페이지 컴포넌트는 `React.lazy()`로 동적 임포트:

```tsx
const Login = lazy(() => import('@/pages/Login'))
const Dashboard = lazy(() => import('@/pages/Dashboard'))
const DockerStacks = lazy(() => import('@/pages/docker/DockerStacks'))
// ... 모든 페이지 동일 패턴
```

`<Suspense>`는 전체 `<Routes>`를 감싸며, 로딩 중에는 `PageLoader` 컴포넌트 (스피너 애니메이션)를 표시.

---

## 페이지 컴포넌트

### Connect (Tauri 전용)
- **파일**: `web/src/pages/Connect.tsx`
- **기능**: Tauri 데스크톱 클라이언트에서 서버 접속 페이지. 서버 URL 입력, health check API 호출로 연결 가능 여부 확인, 연결 실패 시 진단 기능 (포트/방화벽/DNS 등), 언어 선택 (한국어/영어). 웹 브라우저에서는 접근 불가 (TauriGuard가 리다이렉트).
- **사용 API**: `api.healthCheck()`
- **사용 컴포넌트**: Button, Input (shadcn/ui)
- **상태**: serverUrl, connectionStatus, diagnostics, language

### Login
- **파일**: `web/src/pages/Login.tsx`
- **기능**: 관리자 로그인 폼. 사용자명/비밀번호 입력 후 JWT 토큰 수령. 서버에서 2FA 요구 시 TOTP 코드 입력 필드가 동적으로 표시됨. **(v0.34.0)** 2FA 화면에서 "복구 코드로 로그인" 토글 시 TOTP 필드가 복구 코드 필드로 교체되고, 유효한 코드는 사용 시 소진됨.
- **사용 API**: `api.login(username, password, totpCode?, recoveryCode?)`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)
- **상태**: username, password, totpCode, showTotp, useRecovery, recoveryCode, error, loading

### Setup
- **파일**: `web/src/pages/Setup.tsx`
- **기능**: 첫 실행 시 관리자 계정 생성 위저드. 사용자명(기본값 "admin"), 비밀번호, 비밀번호 확인 입력. 최소 8자 검증.
- **사용 API**: `api.setupAdmin(username, password)`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)
- **상태**: username, password, confirmPassword, error, loading

### Dashboard
- **파일**: `web/src/pages/Dashboard.tsx` (~600줄)
- **기능**: 노드 단위 시스템 현황 대시보드 (클러스터 Layout 내에서 선택 노드 범위로 렌더)
  - 호스트 정보 (hostname, OS, platform+version, kernel, uptime, CPU 코어, **주 IP**)
  - 실시간 메트릭 카드 4개 (CPU / 메모리+Swap / 디스크 / 네트워크)
  - CPU/메모리 히스토리 차트 (**1/4/12/24h** 범위 토글)
  - Docker 컨테이너 요약 (실행/중지/전체 카운트 + 최근 컨테이너 목록, "전체 보기" → /docker)
  - 네트워크 I/O 실시간 송수신 속도 및 누적량
  - Top 프로세스 테이블 (CPU 사용률 상위, 10초마다 갱신)
  - 최근 로그 — **syslog / 방화벽 탭 토글** (각 8 / 50줄; 클릭 시 해당 로그 페이지로)
  - **업데이트 배너** (`update_info` 기반) → `/settings?scope=node&tab=system`
  - 빠른 실행 바로가기 5개 (파일, Docker, 패키지, 크론, 로그)
  - WebSocket 연결 상태 배지 (Live / Disconnected)
- **사용 API**: `api.getDashboardOverview()` (host+metrics+history+version+update_info **통합**), `api.getNetworkInterfaces()`, `api.getTopProcesses()`, `api.getContainers()`, `api.readLog('syslog', 8)`, `api.readLog('firewall', 50)`
- **WebSocket**: `useWebSocket({ url: '/ws/metrics' })` — 실시간 메트릭(2초), 클러스터 래핑(노드 타겟 가능)
- **사용 컴포넌트**: MetricsCard, MetricsChart, Table (shadcn/ui)
- **변경 이력**: 기존 `getSystemInfo()`+`getMetricsHistory()` 2회 호출 → `getDashboardOverview()` 단일 호출로 통합(초기 로드 요청 수 감소). 호스트 정보에 IP, 로그 패널에 방화벽 탭, 상단 업데이트 배너 추가.

### Docker
- **파일**: `web/src/pages/Docker.tsx`
- **기능**: Docker 관리 컨테이너. NavLink 기반 사이드 탭으로 5개 서브페이지를 네비게이션하고, `<Outlet />`으로 서브라우트 콘텐츠를 렌더링. Prune 기능 포함.
- **탭 구조** (NavLink, `<Outlet />` 패턴):
  - `/docker/stacks` (기본값) -> DockerStacks
  - `/docker/containers` -> DockerContainers
  - `/docker/images` -> DockerImages
  - `/docker/volumes` -> DockerVolumes
  - `/docker/networks` -> DockerNetworks
- **사용 컴포넌트**: NavLink (react-router-dom), DockerPrune (커스텀), Lucide 아이콘

### Docker > DockerStacks
- **파일**: `web/src/pages/docker/DockerStacks.tsx`
- **기능**: Docker Compose 스택 관리 (목록 + 상세를 하나의 컴포넌트에서 처리)
  - 스택 목록 테이블: 이름, 상태 아이콘 (running/partial/stopped), 서비스 수, 실행 중 서비스 수
  - URL 파라미터 `name`으로 스택 선택 시 상세 보기
  - 스택 상세: 서비스 목록 테이블 (이름, 이미지, 상태 배지, 포트), 서비스별 시작/중지/재시작
  - YAML 편집 (ComposeEditor, Monaco Editor)
  - .env 파일 편집 (ComposeEditor)
  - 서비스별 로그 보기 (ContainerLogs) / 셸 접속 (ContainerShell) 다이얼로그
  - 스택 생성/삭제 다이얼로그
  - 스택 Up/Down/Restart 액션
  - 스택 마이그레이션 (MigrateStackDialog, v0.50.0 콜드 데이터 이전 / v0.51.0 전송 속도 제한): 클러스터 모드에서 다른 온라인 노드로 스택 이전 — 대상 노드 선택, disposition(retain/delete/clone), 덮어쓰기 ack, 전송 속도 제한, 사전 점검 게이트, SSE 진행 로그
- **사용 API**: `api.getComposeProjects()`, `api.createComposeProject()`, `api.getComposeProject()`, `api.updateComposeProject()`, `api.deleteComposeProject()`, `api.composeUp()`, `api.composeDown()`, `api.getComposeServices()`, `api.restartComposeService()`, `api.stopComposeService()`, `api.startComposeService()`, `api.getComposeServiceLogs()`, `api.getComposeEnv()`, `api.updateComposeEnv()`, `api.migratePreflight()`, `api.migrateStream()`(SSE), `api.getMigrateTargetInfo()`
- **사용 컴포넌트**: Table, Dialog, Tabs, Button, Input, Label, ComposeEditor, ContainerLogs, ContainerShell, MigrateStackDialog (shadcn/ui + 커스텀)

### Docker > DockerContainers
- **파일**: `web/src/pages/docker/DockerContainers.tsx`
- **기능**: 컨테이너 목록 및 관리
  - 요약 카드 3개 (전체/실행 중/중지됨) - 클릭 시 필터링
  - 검색 (이름/이미지 기준)
  - 컨테이너 테이블: 이름, 이미지, 상태 배지, 리소스(CPU/MEM 실시간 + **스파크라인** ContainerSparkline), 포트, 생성일
  - **(v0.23.0) 독립형 컨테이너 생성 폼**(CreateContainerDialog): compose 없이 이미지(미존재 시 pull)/이름/발행 포트(host/container/proto)/환경변수/볼륨 바인드(읽기전용)/재시작 정책/네트워크/명령어/자동 시작을 입력하는 검증된 폼 (`api.createContainer(spec)`, `CreateContainerSpec`/`PortBindingSpec` 타입)
  - 컨테이너별 액션: 상세정보(Inspect), 터미널(Shell), 시작/중지/재시작, 삭제
  - 상세정보 다이얼로그: Inspect(자원 사용량, 일반정보, 포트, 볼륨, 네트워크, 환경변수) / **관측성(History)** / Logs / Shell 탭
  - **관측성 탭**(ContainerHistoryTab): CPU/메모리 히스토리 차트(1h/6h/24h) + 수명주기 이벤트 타임라인
  - **헬스체크 컴포저**(HealthcheckComposerDialog): healthcheck 설정 생성/삽입
  - 중지/재시작/삭제 확인 다이얼로그
- **사용 API**: `api.getContainers()`, `api.startContainer()`, `api.stopContainer()`, `api.restartContainer()`, `api.removeContainer()`, `api.inspectContainer()`, `api.containerStats()`, `api.getContainerMetrics(id, range)`, `api.getContainerEvents(id, opts)`
- **사용 컴포넌트**: Table, Dialog, Tabs, Button, Input, ContainerLogs, ContainerShell, ContainerHistoryTab, ContainerSparkline, HealthcheckComposerDialog (shadcn/ui + 커스텀)
- **내부 서브컴포넌트**:
  - `ContainerStatsCell`: 개별 컨테이너 CPU/MEM 실시간 표시 (5초 주기 폴링)
  - `ContainerInspect`: 컨테이너 상세정보 패널 (리소스 게이지, 일반정보, 포트, 볼륨, 네트워크, 환경변수)

### Docker > DockerImages
- **파일**: `web/src/pages/docker/DockerImages.tsx`
- **기능**: Docker 이미지 목록 관리
  - 이미지 수 표시
  - 이미지 테이블: RepoTag, ID(짧은), 크기, 생성일, 사용 상태(in_use/used_by)
  - 이미지 풀 다이얼로그
  - 이미지 삭제 확인 다이얼로그
- **사용 API**: `api.getImages()`, `api.pullImage()`, `api.removeImage()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)

### Docker > DockerVolumes
- **파일**: `web/src/pages/docker/DockerVolumes.tsx`
- **기능**: Docker 볼륨 관리
  - 볼륨 수 표시
  - 볼륨 테이블: 이름, 드라이버, 마운트포인트, 생성일, 사용 상태(in_use/used_by)
  - 볼륨 생성 다이얼로그
  - 볼륨 삭제 확인 다이얼로그
- **사용 API**: `api.getVolumes()`, `api.createVolume()`, `api.removeVolume()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)

### Docker > DockerNetworks
- **파일**: `web/src/pages/docker/DockerNetworks.tsx`
- **기능**: Docker 네트워크 관리
  - 네트워크 수 표시
  - 네트워크 테이블: 이름, ID(짧은), 드라이버, 범위, 사용 상태(in_use/used_by)
  - 기본 네트워크(bridge/host/none) 삭제 방지
  - 네트워크 생성 다이얼로그 (이름 + 드라이버 선택: bridge/overlay/host)
  - 네트워크 삭제 확인 다이얼로그
- **사용 API**: `api.getNetworks()`, `api.createNetwork()`, `api.removeNetwork()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)

### AppStore
- **파일**: `web/src/pages/AppStore.tsx`
- **기능**: 앱스토어 (원클릭 Docker Compose 앱 설치)
  - 카테고리 필터 필(pill) — 전체/모니터링/보안/미디어/클라우드/개발/인프라 등
  - 앱 검색 (이름, 설명 기반)
  - 앱 그리드 (3열) — 앱 이름, 설명, 포트, 설치 상태 표시
  - 설치 다이얼로그: 동적 환경변수 폼 (앱별 env 설정), generate 타입은 자동 생성된 비밀번호 표시
  - 설치된 앱은 "설치됨" 상태 필 표시 + Stacks 바로가기 링크
  - 캐시 갱신 버튼
- **사용 API**: `api.getAppStoreCategories()`, `api.getAppStoreApps()`, `api.getAppStoreApp(id)`, `api.getInstalledApps()`, `api.refreshAppStore()`. 설치는 래퍼 없이 직접 `fetch`(SSE 스트림). 고급 모드는 `window.prompt`로 비밀번호 재입력 후 `{advanced,compose,env_raw,password}` 전송
- **사용 컴포넌트**: Dialog, Button, Input, Label (shadcn/ui)
- **사이드바 위치**: Docker 다음, 아이콘: `Store` (lucide-react)
- **내부 서브컴포넌트**: `AppStoreDetailModal` (`web/src/pages/AppStoreDetail.tsx`)

### AppStore > AppStoreDetailModal
- **파일**: `web/src/pages/AppStoreDetail.tsx`
- **기능**: 앱 상세 정보 + 설치 모달 (AppStore 페이지 내에서 호출되는 모달 컴포넌트)
  - 앱 아이콘, 이름, 설명(다국어), 버전, 카테고리, 포트 표시
  - 웹사이트/소스 코드 링크
  - 설치 상태 표시 (설치됨/미설치)
  - 설치 폼: 심플 모드 (환경변수 폼) / 고급 모드 (docker-compose.yml + .env 직접 편집) 탭 전환
  - 환경변수 타입별 입력: text, password (비밀번호 표시/숨기기 토글 + 자동 생성), port (숫자), select (드롭다운)
  - SSE 기반 설치 진행률 표시: 단계 인디케이터 (fetch → prepare → pull → start → done) + 실시간 로그 출력
  - 설치 에러 처리: 포트 충돌, 컨테이너 이름 충돌, 이미 설치됨 등 에러 코드별 메시지
  - Docker Compose YAML 미리보기 (접기/펼치기)
  - 앱 기능(features) 그리드 표시
  - README 마크다운 렌더링 (GitHub Alert 문법 지원, 이미지/링크 base URL 변환)
  - ESC 키로 닫기, 배경 클릭으로 닫기 (설치 중에는 비활성화)
- **Props**: `appId`, `open`, `onClose`, `onInstalled`
- **사용 API**: `api.getAppStoreApp(id)` (상세 로드), `POST /appstore/apps/{id}/install` (SSE 스트리밍 설치, fetch API 직접 사용)
- **사용 컴포넌트**: Button, Input (shadcn/ui), Marked (마크다운 렌더링)
- **내부 서브컴포넌트**: `RenderedReadme` (마크다운 → HTML 변환 + GitHub Alert 처리)

### Files
- **파일**: `web/src/pages/Files.tsx`
- **기능**: 서버 파일 관리자
  - 브레드크럼 경로 네비게이션 (클릭 시 경로 직접 입력 가능)
  - 파일/폴더 테이블: 이름(아이콘 구분), 크기, 수정일, 권한
  - 디렉토리 우선 정렬 (알파벳순)
  - 파일 클릭 시 Monaco 에디터로 편집 (언어 자동 감지: 30+ 확장자 지원)
  - 새 파일 생성, 새 폴더 생성, 파일 업로드 (XHR + FormData, 진행률 표시), 다운로드
  - 이름 변경, 삭제 확인 다이얼로그
  - **우클릭 컨텍스트 메뉴**(행 + 배경 빈 영역): 열기/편집/다운로드/이름변경/삭제, 업로드/새파일/새폴더/새로고침
  - 5MB 초과 파일은 편집기 대신 다운로드로 안내(confirm)
  - 도구 모음: 새로고침, 새 파일, 새 폴더, 업로드
  - **(v0.21.0) 검색/복사/다중 삭제**: 현재 디렉토리 하위 **재귀 이름 검색**(결과 캡 + 데드라인, 잘림 플래그), 파일/디렉토리 트리 **복사**, **다중 선택 삭제**(행별 체크박스 + 일괄 삭제 + 항목별 결과 요약).
- **사용 API**: `api.listFiles()`, `api.readFile()`, `api.writeFile()`, `api.createDir()`, `api.deletePath()`, `api.renamePath()`, `api.uploadFile()`, `api.downloadFile()`, `api.searchFiles(path, q, limit?)`, `api.copyPath(src, dst)`
- **사용 컴포넌트**: Table, Dialog, ContextMenu, Button, Input, Label, Monaco Editor (shadcn/ui + @monaco-editor/react)

### CronJobs
- **파일**: `web/src/pages/CronJobs.tsx`
- **기능**: 시스템 크론탭 관리
  - 작업 수 표시 (job 타입만 카운트)
  - "모든 항목 보기" 체크박스 (env, comment 타입도 표시)
  - 작업 테이블: 상태(활성/비활성 토글), 스케줄(코드 + 설명), 명령어, 유형(선택적), 액션
  - 작업 활성화/비활성화 토글
  - 작업 생성/편집 다이얼로그 (스케줄 입력 + 프리셋 5개 + 명령어 입력 + 실행 주기 미리보기)
  - 작업 삭제 확인 다이얼로그
  - 프리셋: 매분, 매시간, 매일, 매주, 매월
  - **(v0.21.0 영역) 즉시 실행(Run now)**: 행 액션에서 작업을 즉시 실행하고 출력/성공 여부를 다이얼로그로 표시 (`api.runCronJob(id)`)
  - **(v0.37.0)** 로딩 스켈레톤 / 인라인 에러 + Retry / 빈 상태 3분기 적용
- **사용 API**: `api.getCronJobs()`, `api.createCronJob()`, `api.updateCronJob()`, `api.deleteCronJob()`, `api.runCronJob(id)`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)
- **로컬 함수**: describeSchedule()
- **로컬 인터페이스**: CronJob, SchedulePreset

### Logs
- **파일**: `web/src/pages/Logs.tsx`
- **기능**: 시스템/앱 로그 뷰어
  - 좌측 사이드바: 로그 소스 목록 (이름, 경로, 크기, 존재 여부, 커스텀 여부)
  - 커스텀 로그 소스 추가/삭제 기능 (다이얼로그)
  - 줄 수 선택: 100 / 500 / 1000 / 5000
  - 실시간 스트리밍 모드 (WebSocket 직접 관리, useWebSocket 훅 미사용)
  - 자동 스크롤 토글
  - 로그 검색: Ctrl+F, 매치 하이라이팅, 이전/다음 매치 네비게이션
  - **구조화된 로그 파싱 (logParsers)**: auth.log, ufw.log, sfpanel.log에 대해 구조화된 컬럼 뷰 제공. 원시/파싱 뷰 토글.
  - 로그 레벨별 색상 구분 (error/warn/info/debug - 좌측 테두리 + 텍스트 색상)
  - 줄 번호 표시
  - 로그 다운로드 (Blob -> 링크 클릭)
  - 로그 지우기
  - 연결 상태 표시 (실시간 모드)
- **사용 API**: `api.getLogSources()`, `api.readLog(source, lines)`, `api.getToken()`, `api.addCustomLogSource()`, `api.deleteCustomLogSource()`
- **WebSocket**: 직접 관리 (`/ws/logs?source={source}&token={token}`)
- **사용 컴포넌트**: Button, Input, Dialog (shadcn/ui)
- **사용 유틸리티**: `hasParsedView()`, `getParser()`, `parseLogLines()` (`web/src/lib/logParsers.ts`)
- **로컬 함수**: formatFileSize(), highlightText()
- **로컬 인터페이스**: LogSource, LogResponse

### Processes
- **파일**: `web/src/pages/Processes.tsx`
- **기능**: 시스템 프로세스 관리자
  - 리소스 요약 카드 3개: CPU, 메모리, 스왑 (실시간 게이지)
  - 프로세스 검색 (이름/PID/사용자/명령어)
  - 정렬 선택: CPU / 메모리 / PID / 이름
  - 프로세스 테이블: PID, 이름(+명령어), 사용자, CPU%, MEM%, 상태 배지, 종료 버튼
  - 프로세스 종료 다이얼로그: SIGTERM(정상) / SIGKILL(강제) 선택. 보호 PID/패널 자식 프로세스는 서버가 403 거부(sysguard)
  - 15초 자동 갱신 (탭 비활성 시 일시정지), 대용량 목록 가상 스크롤(@tanstack/react-virtual)
  - **(v0.20.0) 트리 뷰 / renice / job-control 시그널**: 뷰 모드 토글(`list`/`tree` — PPID 기반 부모→자식 평탄화 DFS, 사이클 가드), 행에 parent PID·절대 RSS·nice 값 표시, **renice 컨트롤**(−20..19 클램프, 보호 PID 가드), 시그널 메뉴에 **STOP(일시정지)·CONT(재개)** 추가. 보호 PID/패널 자식 프로세스는 renice·신규 시그널 모두 서버가 거부.
- **사용 API**: `api.listProcesses()` (인자 없음 — 필터/정렬은 클라이언트), `api.killProcess(pid, signal)`, `api.reniceProcess(pid, nice)`
- **WebSocket**: `useWebSocket({ url: '/ws/metrics' })` - 시스템 메트릭 수신 (리소스 요약 카드용)
- **사용 컴포넌트**: Table, Dialog, Button, Input (shadcn/ui)

### Services
- **파일**: `web/src/pages/Services.tsx`
- **기능**: Systemd 서비스 관리
  - 서비스 수 표시, 검색(이름/설명), 상태 필터 탭 (all/running/failed/inactive)
  - 테이블(데스크톱)/카드(모바일): 이름, 설명, 상태(sub_state/active_state), 부팅(enabled/disabled/static/masked), 액션 드롭다운
  - 액션: 시작/중지/재시작, 활성화/비활성화, 로그 보기. 보호 유닛 중지·재시작·비활성화는 서버 403(sysguard)
  - 로그 다이얼로그: journalctl 출력 + 의존성(required_by/requires/wanted_by) 패널
  - 15초 자동 갱신 (탭 비활성 시 일시정지)
- **사용 API**: `api.listServices()`, `api.startService()`, `api.stopService()`, `api.restartService()`, `api.enableService()`, `api.disableService()`, `api.getServiceLogs(name, lines)`, `api.getServiceDeps(name)`
- **사용 컴포넌트**: Table, Dialog, DropdownMenu, Button, Input (shadcn/ui)

### Network
- **파일**: `web/src/pages/Network.tsx` — 사이드 탭 셸(interfaces/wireguard/tailscale) + `<Outlet/>`
- **탭 구조**: `/network/interfaces`(기본) → NetworkInterfaces, `/network/wireguard` → NetworkWireGuard, `/network/tailscale` → NetworkTailscale

#### Network > NetworkInterfaces (`web/src/pages/network/NetworkInterfaces.tsx`)
- 인터페이스 카드: 물리/가상/루프백/Docker(접이식) 분류, 상태, IPv4/IPv6, MAC, 속도, 트래픽, 기본 게이트웨이 강조
- 설정 다이얼로그(DHCP/Static, IP, gateway4/6, DNS, MTU), DNS 인라인 편집, 라우팅 테이블, 본딩(생성 7모드/삭제), 플로팅 "적용"(netplan apply) + 경고
- **사용 API**: `api.getNetworkStatus()`(interfaces+routes+dns 집계), `api.configureInterface()`, `api.configureDNS()`, `api.applyNetworkConfig()`, `api.createBond()`, `api.deleteBond()`

#### Network > NetworkWireGuard (`web/src/pages/network/NetworkWireGuard.tsx`)
- 미설치 시 원클릭 설치 게이트; 인터페이스 카드(주소/DNS/공개키 복사/listen_port/피어), up/down 토글, 설정 CRUD + `.conf` 업로드, 키 마스킹(`********`) 저장 가드
- **(v0.28.0) 피어 관리**: 키페어 생성, **피어 추가**(주소/엔드포인트/DNS/keepalive 입력 → 클라이언트 키페어 생성, 클라이언트 config를 브라우저에서 조립 — 서버는 클라이언트 개인키 미저장), 피어 제거, 부팅 자동시작 토글. 추가 플로우는 클라이언트 config를 복사 가능한 텍스트 + **QR 코드**(`QRCodeSVG` from `qrcode.react`)로 렌더링하여 모바일 가져오기 지원.
- **사용 API**: `getWireGuardStatus/Interfaces`, `wireGuardInterfaceUp/Down`, `create/get/update/deleteWireGuardConfig`, `generateWireGuardKeypair()`, `addWireGuardPeer(name, peer)`, `removeWireGuardPeer(name, publicKey)`, `setWireGuardAutostart`

#### Network > NetworkTailscale (`web/src/pages/network/NetworkTailscale.tsx`)
- SSE 스트리밍 설치(게이트), 상태(NotInstalled/NeedsLogin/Running), Auth Key 또는 브라우저 인증 URL 자동 오픈, 자기 정보, Accept Routes/Advertise Exit Node 토글, Exit Node 선택, 버전+업데이트 체크, 피어 표, 연결/해제/로그아웃
- **사용 API**: `getTailscaleStatus`, `tailscaleUp/Down/Logout`, `getTailscalePeers`, `setTailscalePreferences`, `checkTailscaleUpdate`
- **사용 유틸리티**: `formatBytes()`

### Disk
- **파일**: `web/src/pages/Disk.tsx`
- **기능**: 디스크 및 스토리지 관리 (탭 구조)
  - 탭 구조: Overview, Partitions, Filesystems, LVM, RAID, Swap
- **탭 구성**:
  - overview (기본값) -> DiskOverview
  - partitions -> DiskPartitions
  - filesystems -> DiskFilesystems
  - lvm -> DiskLVM
  - raid -> DiskRAID
  - swap -> DiskSwap
- **사용 컴포넌트**: Tabs, TabsList, TabsTrigger, TabsContent (shadcn/ui)
- **서브 컴포넌트 파일**:
  - `web/src/pages/disk/DiskOverview.tsx` — 디스크 개요 (블록 디바이스, SMART, I/O 통계, 디스크 사용량). **(v0.27.0)** SMART **셀프 테스트 실행 UI**(short/long, smartctl ETA 토스트) + 드라이브 **셀프 테스트 로그**(유형/상태/통과·실패/실행 시 power-on hours) 표시.
  - `web/src/pages/disk/DiskPartitions.tsx` — 파티션 관리 (생성/삭제)
  - `web/src/pages/disk/DiskFilesystems.tsx` — 파일시스템 관리 (포맷/마운트/언마운트/리사이즈)
  - `web/src/pages/disk/DiskLVM.tsx` — LVM 관리 (PV/VG/LV 생성/삭제/리사이즈)
  - `web/src/pages/disk/DiskRAID.tsx` — RAID 관리 (생성/삭제/디스크 추가·제거)
  - `web/src/pages/disk/DiskSwap.tsx` — Swap 관리 (생성/삭제/리사이즈/스왑피니스 설정)
- **사용 API**:
  - Overview: `api.getDiskOverview()`, `api.getDiskSmart()`, `api.getDiskIOStats()`, `api.getDiskUsage()`, `api.checkSmartmontools()`, `api.installSmartmontools()`
  - Partitions: `api.getPartitions()`, `api.createPartition()`, `api.deletePartition()`
  - Filesystems: `api.getFilesystems()`, `api.formatPartition()`, `api.mountFilesystem()`, `api.unmountFilesystem()`, `api.resizeFilesystem()`
  - LVM: `api.getLVMPVs()`, `api.getLVMVGs()`, `api.getLVMLVs()`, `api.createPV()`, `api.createVG()`, `api.createLV()`, `api.removePV()`, `api.removeVG()`, `api.removeLV()`, `api.resizeLV()`
  - RAID: `api.getRAIDArrays()`, `api.getRAIDDetail()`, `api.createRAID()`, `api.deleteRAID()`, `api.addRAIDDisk()`, `api.removeRAIDDisk()`
  - Swap: `api.getSwapInfo()`, `api.createSwap()`, `api.removeSwap()`, `api.setSwappiness()`, `api.checkSwapResize()`, `api.resizeSwap()`

### Firewall
- **파일**: `web/src/pages/Firewall.tsx`
- **기능**: 방화벽(UFW) 및 Fail2ban 침입 방지 시스템 관리
  - 탭 구조: UFW Rules, Open Ports, Fail2ban, Docker, Logs
  - **UFW Rules 탭** (`FirewallRules`): UFW 활성화/비활성화 토글, 규칙 목록 테이블 (번호/대상/동작/소스/코멘트/IPv6), 규칙 추가 다이얼로그 (action/port/protocol/from/to/comment), 규칙 삭제 확인 다이얼로그
  - **Open Ports 탭** (`FirewallPorts`): 리스닝 TCP/UDP 포트 목록 테이블 (프로토콜/주소/포트/PID/프로세스), 선택한 포트로 UFW 규칙 직접 추가 기능
  - **Fail2ban 탭** (`FirewallFail2ban`): Fail2ban 설치 상태 확인 및 원클릭 설치, jail 템플릿에서 생성, jail 목록 테이블 (이름/활성/차단수/총차단수), jail 상세 (설정값, 차단 IP 목록), jail 활성화/비활성화, jail 설정 편집, jail 삭제, IP 차단 해제
- **탭 구성**:
  - rules (기본값) -> FirewallRules
  - ports -> FirewallPorts
  - fail2ban -> FirewallFail2ban
  - docker -> FirewallDocker
  - logs -> FirewallLogs
- **사용 API**: `api.getFirewallStatus()`, `api.enableFirewall()`, `api.disableFirewall()`, `api.getFirewallRules()`, `api.addFirewallRule()`, `api.deleteFirewallRule()`, `api.getListeningPorts()`, `api.getFail2banStatus()`, `api.installFail2ban()`, `api.getFail2banTemplates()`, `api.createFail2banJail()`, `api.deleteFail2banJail()`, `api.getFail2banJails()`, `api.getFail2banJailDetail()`, `api.updateFail2banJailConfig()`, `api.enableFail2banJail()`, `api.disableFail2banJail()`, `api.unbanFail2banIP()`
- **사용 컴포넌트**: Tabs, TabsList, TabsTrigger, TabsContent, Table, Dialog, Button, Input, Select (shadcn/ui)
- **서브 컴포넌트 파일**:
  - `web/src/pages/firewall/FirewallRules.tsx` — UFW 규칙 관리
  - `web/src/pages/firewall/FirewallPorts.tsx` — 리스닝 포트 조회
  - `web/src/pages/firewall/FirewallFail2ban.tsx` — Fail2ban 관리
  - `web/src/pages/firewall/FirewallDocker.tsx` — Docker 방화벽 (DOCKER-USER 체인) 관리
  - `web/src/pages/firewall/FirewallLogs.tsx` — 방화벽 로그 뷰어

### Packages
- **파일**: `web/src/pages/Packages.tsx`
- **기능**: 시스템 패키지 관리
  - Docker 상태 카드: 설치 여부, 버전, 실행 상태, Compose 가용성 표시. 미설치 시 Docker 설치 버튼 (SSE 스트리밍 출력)
  - **개발 도구 카드**: Node.js(버전 관리 다이얼로그 — 설치/전환/삭제, LTS), Claude Code, Codex, Gemini CLI (각 SSE 설치; Node 미설치 시 Codex/Gemini 비활성화)
  - 시스템 업데이트: 업데이트 확인, 전체/선택 업그레이드(SSE), 패키지 체크박스 선택
  - 패키지 검색/설치: 검색 결과에서 설치/제거 (설치 상태 표시)
  - 작업 출력 다이얼로그: 설치/업그레이드/제거 진행 상황 실시간 표시
- **사용 API**: `api.getDockerStatus()`, `api.installDocker()`(SSE), `api.checkUpdates()`, `api.upgradePackages()`(SSE), `api.installPackage()`, `api.removePackage()`, `api.searchPackages()`, `getNodeStatus/getNodeVersions/switchNodeVersion/uninstallNodeVersion`, `getClaudeStatus/getCodexStatus/getGeminiStatus`, `install-node/claude/codex/gemini`(fetch SSE)
- **사용 컴포넌트**: Table, Dialog, Button, Input (shadcn/ui)

### Terminal
- **파일**: `web/src/pages/Terminal.tsx`
- **기능**: 웹 기반 서버 터미널 (멀티 탭)
  - 탭 관리: 추가, 닫기, 이름 변경(더블클릭), 탭 전환
  - 탭 상태 localStorage 영속화 (탭 목록, 활성 탭, 글꼴 크기)
  - 글꼴 크기 조절 (10~24px, 기본 14px)
  - 터미널 검색 (SearchAddon, Ctrl+F)
  - xterm.js 테마: Tokyo Night 스타일
  - WebSocket으로 서버 셸 세션 연결 (`/ws/terminal?token=&session_id=`)
  - 바이너리 데이터(ArrayBuffer) 지원
  - 윈도우 리사이즈 시 자동 피팅
  - 리사이즈 이벤트 서버 전송 (JSON: `{type: "resize", cols, rows}`)
- **사용 API**: `api.buildWsUrl('/ws/terminal', {session_id})` — 단발성 ws-ticket(`POST /auth/ws-ticket`) 발급 후 URL 구성, 레거시 `?token=` fallback. 추가로 클리어(Ctrl-L), 모바일 키 바, Unicode11Addon 적용
- **WebSocket**: 직접 관리 (`/ws/terminal?token={token}&session_id={id}`)
- **사용 컴포넌트**: Button, Input (shadcn/ui)
- **내부 서브컴포넌트**: `TerminalSession` - 개별 터미널 세션 관리

> **갱신 노트 (v0.40.0)**: Settings는 더 이상 단일 페이지가 아니라 **탭형 셸 + 코드 분할 탭 패널**로 재구성되었다. `Settings.tsx`는 shadcn/ui `Tabs`로 6개 탭(general / security / system / tuning / alerts / audit)을 렌더링하며 각 탭 패널은 `React.lazy()`로 별도 분할된다. `?tab=` 쿼리로 활성 탭을 동기화한다. **클러스터 모드에서는 `?scope`에 따라 탭이 필터링**된다: `?scope=node`면 per-node SQLite를 건드리는 탭(`system`/`tuning`/`audit`), 그 외에는 FSM 복제 대상(`general`/`security`/`alerts`)만 노출(단일 노드 배포는 전체 표시). 탭→패널 매핑: general → `settings/General.tsx`, security → `settings/Security.tsx`, system → `settings/Maintenance.tsx`, tuning → `settings/Performance.tsx`(`SettingsTuning` 래핑), alerts → `settings/AlertSettings.tsx`, audit → `settings/Audit.tsx`. 아래 v0.9.0 기준 단일 페이지 설명은 일부 기능이 이 탭들로 분산된 것으로 읽을 것.
>
> - **General** (`settings/General.tsx`): 언어 변경(i18n.changeLanguage), 기타 일반 설정.
> - **Security** (`settings/Security.tsx`): 비밀번호 변경, 2FA 설정(QR `QRCodeSVG` from `qrcode.react`)·검증·비활성화(비활성화 시 `usePrompt`로 **비밀번호 + 현재 TOTP 코드** 입력 — `api.disable2FA(password, totpCode)`), **2FA 복구 코드** 생성/재생성·잔여 개수 표시(`api.get2FARecoveryStatus()`, `api.regenerate2FARecoveryCodes()`). (v0.34.0/v0.36.0/v0.38.0)
> - **System (Maintenance)** (`settings/Maintenance.tsx`): 시스템 정보, 패널 업데이트(SSE), 백업 다운로드/복원, **예약 백업 스케줄 폼 + 즉시 실행 + 아카이브 목록(다운로드/삭제)** (v0.26.0). `clusterEnabled` prop 수신.
> - **Performance (Tuning)** (`settings/Performance.tsx`): 터미널 타임아웃·업로드 한도 설정 + `SettingsTuning` 커널 튜닝 컴포넌트 래핑.
> - **Alerts** (`settings/AlertSettings.tsx`): 알림 채널/규칙/히스토리 (아래 별도 섹션).
> - **Audit** (`settings/Audit.tsx`): 감사 로그 뷰어(데스크톱 테이블 + **모바일 카드 폴백**, v0.35.0).

### Settings
- **파일**: `web/src/pages/Settings.tsx`
- **기능**: 계정 및 시스템 설정
  - 언어 변경 (English / 한국어) - i18n.changeLanguage()
  - 터미널 타임아웃 설정 (분 단위, 0 = 무제한)
  - 파일 업로드 최대 크기 설정 (MB 단위)
  - 비밀번호 변경 (현재 비밀번호 + 새 비밀번호 + 확인)
  - 2FA 관리: 설정 시작 -> QR 코드(qrcode.react 클라이언트 렌더링) + 시크릿 키 표시 -> 6자리 코드 인증 -> 활성화/비활성화(비밀번호 재확인)
  - 시스템 정보 표시 (버전, 호스트명, OS, 커널, 가동시간)
  - **버전 표시**: `api.getSystemInfo()`의 `data.version` 필드에서 가져옴 (`v${data.version}` 형식)
  - 시스템 튜닝 섹션 (SettingsTuning 컴포넌트 내장)
  - 알림 설정 섹션 (AlertSettings 컴포넌트 내장)
- **사용 API**: `api.getSettings()`, `api.updateSettings()`, `api.changePassword()`, `api.setup2FA()`, `api.verify2FA()`, `api.getSystemInfo()`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)
- **내부 서브컴포넌트**: `SettingsTuning` (`web/src/pages/SettingsTuning.tsx`), `AlertSettings` (`web/src/pages/settings/AlertSettings.tsx`)

### Settings > SettingsTuning
- **파일**: `web/src/pages/SettingsTuning.tsx`
- **기능**: 시스템 커널 파라미터 최적화 관리 (Settings 페이지 내에 포함된 컴포넌트)
  - 시스템 스펙 표시 (CPU 코어 수, RAM 용량, 커널 버전)
  - 전체 최적화 진행률 바 + 적용 카운트 (applied / total_params)
  - 전체 적용 / 기본값 복원 버튼
  - 카테고리별 최적화 카드 (network, memory, filesystem, security)
    - 카테고리 헤더: 아이콘, 이름, 설명, 적용 상태 (optimized 필/카운트), 개별 적용 버튼
    - 카테고리 펼치기: 혜택(benefit) + 주의사항(caution) 안내, 파라미터 목록 (key, 현재값 → 추천값, 적용 상태)
  - 안전 롤백 메커니즘: 적용 후 자동 카운트다운 (60초), 카운트다운 내 "유지" 확인 필요, 미확인 시 서버에서 자동 롤백
  - 롤백 카운트다운 배너 (진행률 바 + 확인 버튼)
- **사용 API**: `api.getTuningStatus()`, `api.applyTuning(categories?)`, `api.confirmTuning()`, `api.resetTuning()`
- **사용 컴포넌트**: Button (shadcn/ui)
- **사용 유틸리티**: `formatBytes()` (`web/src/lib/utils.ts`)
- **타입**: `TuningStatus`, `TuningCategory` (`web/src/types/api.ts`)

### Settings > AlertSettings
- **파일**: `web/src/pages/settings/AlertSettings.tsx`
- **기능**: 알림 채널 및 규칙 관리 (Settings 페이지 내에 포함된 컴포넌트)
  - 알림 채널 관리: Discord (Webhook URL), Telegram (Bot Token + Chat ID), **(v0.25.0) webhook (임의 http(s) URL — Slack/Mattermost 호환 `text` 페이로드)** 채널 추가/삭제/활성화 토글/테스트 전송
  - **(v0.35.0)** 알림 히스토리는 데스크톱 테이블 + 모바일 카드 폴백
  - 알림 규칙 관리: 규칙 생성/삭제/활성화 토글
    - 규칙 타입: CPU, 메모리, 디스크, 컨테이너, 서비스, 로그인, 패키지
    - 심각도: Info, Warning, Critical (색상별 배지)
    - 임계값 + 쿨다운(초) 설정
    - 채널 선택 (다중 선택)
    - 노드 범위: 전체(all) / 특정 노드 선택
  - 알림 히스토리 뷰어: 페이지네이션 (20건 단위), 히스토리 전체 삭제
- **사용 API**: `/alerts/channels` (GET/POST), `/alerts/channels/{id}` (PUT/DELETE), `/alerts/channels/{id}/test` (POST), `/alerts/rules` (GET/POST), `/alerts/rules/{id}` (PUT/DELETE), `/alerts/history` (GET/DELETE)
- **사용 컴포넌트**: Button, Input, Label, Table, Select, Checkbox (shadcn/ui)
- **타입**: `AlertChannel`, `AlertRule`, `AlertHistoryEntry` (컴포넌트 내부 정의)

---

## 공용 컴포넌트

| 컴포넌트 | 파일 | 용도 |
|----------|------|------|
| Layout | `web/src/components/Layout.tsx` | 인증된 페이지의 공통 레이아웃. 좌측 사이드바(네비게이션 12항목 + 접기/펼치기 + 로그아웃) + 우측 메인 콘텐츠(Outlet). NavLink로 활성 상태 표시. 사이드바 접기 상태 localStorage 영속화. |
| MetricsCard | `web/src/components/MetricsCard.tsx` | 메트릭 표시 카드. 아이콘 + 제목 + 값 + 프로그레스 바(80% 초과 빨강, 60% 초과 노랑, 그 외 파랑). |
| MetricsChart | `web/src/components/MetricsChart.tsx` | CPU/메모리 시계열 차트. uPlot 사용. CPU(파랑) + Memory(초록) 이중 라인. Y축 0-100%. |
| ClusterSidebar | `web/src/components/cluster/ClusterSidebar.tsx` | 클러스터 모드 좌측 2단 트리(TreePanel+ContextMenu). 선택 시 `api.setCurrentNode` + 라우팅. 표준 사이드바를 대체. |
| TreePanel | `web/src/components/cluster/TreePanel.tsx` | 데이터센터 루트 + 노드 목록(상태/리더/local, 로컬 우선 정렬), 접이식 52px 레일. |
| ContextMenu | `web/src/components/cluster/ContextMenu.tsx` | 선택 범위별 메뉴(데이터센터: 개요/노드/토큰/설정 · 노드: 모듈 메뉴). |
| NodeSelector | `web/src/components/NodeSelector.tsx` | (레거시) 비클러스터 표준 사이드바 전용 드롭다운. 클러스터 모드에선 미사용. |
| ContainerShell | `web/src/components/ContainerShell.tsx` | Docker 컨테이너 셸 접속. xterm.js + WebSocket(`/ws/docker/containers/{id}/exec`). 키 입력 전송, 리사이즈 이벤트 전송. |
| ContainerLogs | `web/src/components/ContainerLogs.tsx` | Docker 컨테이너 로그 스트리밍. xterm.js(읽기 전용) + WebSocket(`/ws/docker/containers/{id}/logs`). 검색(SearchAddon), 로그 다운로드 기능. |
| ComposeEditor | `web/src/components/ComposeEditor.tsx` | YAML/텍스트 에디터. Monaco Editor 래퍼. 높이 400px, vs-dark 테마, 미니맵 비활성화, 자동 레이아웃. Props: `value`, `onChange`, `language`(기본값 'yaml'). |
| DockerHubSearch | `web/src/components/DockerHubSearch.tsx` | Docker Hub 이미지 검색 자동완성. 디바운싱된 검색으로 드롭다운에 결과 표시 (이름, 설명, 별점, 공식 여부). Props: `value`, `onChange`, `placeholder`. |
| DockerPrune | `web/src/components/DockerPrune.tsx` | Docker 리소스 정리 다이얼로그. 컨테이너/이미지/볼륨/네트워크 선택적 또는 전체 정리. 정리 결과(삭제 수, 회수 용량) 토스트 표시. Props: `open`, `onOpenChange`. |
| ConfirmDialog | `web/src/components/ConfirmDialog.tsx` | (v0.32.0) 네이티브 `window.confirm` 대체. `ConfirmProvider`(앱 루트 `App.tsx`에 1개 마운트) + `useConfirm()` 훅이 `confirm(opts) => Promise<boolean>`를 반환. `ConfirmOptions`: `title`, `description?`, `confirmLabel?`, `cancelLabel?`, `danger?`(빨강 확인 버튼). ~22개 호출 지점(cluster/disk/docker/files/alerts/security/backup/tuning/audit/WireGuard/compose healthcheck)에서 사용. |
| PromptDialog | `web/src/components/PromptDialog.tsx` | (v0.36.0) 네이티브 `window.prompt` 대체. `PromptProvider`(`App.tsx` 마운트) + `usePrompt()` 훅이 `prompt(opts) => Promise<string \| null>`를 반환. `PromptOptions`: `title`, `description?`, `placeholder?`, `defaultValue?`, `password?`(마스킹 입력), `confirmLabel?`, `cancelLabel?`. disable-2FA 재인증·appstore 고급 설치 재인증 등 파괴/인증 플로우에서 사용. |
| TypeToConfirmDialog | `web/src/components/TypeToConfirmDialog.tsx` | (v0.30.0) 비가역 작업 게이트. 정확한 이름(디바이스/배열/클러스터명)을 입력해야 파괴 버튼이 활성화. Props: `open`, `onOpenChange`, `title`, `description?`, `confirmPhrase`(입력해야 할 정확한 문자열), `confirmLabel`, `loading?`, `onConfirm`. 적용: 디스크 포맷, 파티션 삭제, RAID 배열 삭제, 클러스터 disband. |
| Skeleton | `web/src/components/ui/skeleton.tsx` | (v0.33.0) 로딩 스켈레톤 플레이스홀더. `bg-accent animate-pulse rounded-md` div 래퍼. `className` props로 형태 지정. 목록 페이지 첫 로드 시에만 표시(백그라운드 갱신 시 미표시). |

> **횡단 UI 패턴 (v0.33.0~v0.37.0)**
> - **로딩/에러/빈 상태 3분기**: 목록 페이지가 **로딩**(Skeleton), **로드 실패**(인라인 빨강 에러 블록 + 메시지 + Retry 버튼), **실제 빈 상태**(empty state)를 구분한다. 이전엔 삼켜진 fetch 에러가 빈 목록과 동일하게 보였다. 적용 페이지: docker 컨테이너/이미지/볼륨/네트워크, services, processes, cron, firewall rules. Skeleton은 첫 로드에만 표시.
> - **모바일 카드 폴백 (v0.35.0)**: 넓은 데이터 테이블이 작은 화면에서 가로 오버플로 대신 라벨/값 스택 카드로 접힌다. 데스크톱 테이블은 `hidden md:table`(또는 `hidden md:block`), 카드 목록은 `md:hidden`으로 동일한 필터/페이지네이션 행을 동일 배지·링크·액션과 함께 렌더링한다. 적용: **감사 로그**(`settings/Audit.tsx`), **알림 히스토리**(`settings/AlertSettings.tsx`), **방화벽 포트맵**(`components/portmap/PortMapTable.tsx`). 새 i18n 문자열 없이 기존 컬럼 헤더 키 재사용.

---

## shadcn/ui 컴포넌트

| 컴포넌트 | 파일 | 용도 |
|----------|------|------|
| Button | `web/src/components/ui/button.tsx` | 범용 버튼 (variant: default/outline/ghost/destructive, size: default/sm/icon-xs/icon-sm/xs/lg) |
| Card | `web/src/components/ui/card.tsx` | 카드 컨테이너 (현재 직접 사용하지 않고 Tailwind 클래스로 카드 스타일 구현) |
| Checkbox | `web/src/components/ui/checkbox.tsx` | 체크박스 |
| Input | `web/src/components/ui/input.tsx` | 텍스트 입력 필드 |
| Label | `web/src/components/ui/label.tsx` | 폼 라벨 |
| Table | `web/src/components/ui/table.tsx` | 데이터 테이블 (Table, TableHeader, TableBody, TableRow, TableHead, TableCell) |
| Badge | `web/src/components/ui/badge.tsx` | 배지 (현재 인라인 스타일로 배지 구현하여 직접 사용은 제한적) |
| Dialog | `web/src/components/ui/dialog.tsx` | 모달 다이얼로그 (Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter) |
| DropdownMenu | `web/src/components/ui/dropdown-menu.tsx` | 드롭다운 메뉴 |
| ContextMenu | `web/src/components/ui/context-menu.tsx` | 우클릭 컨텍스트 메뉴 |
| Select | `web/src/components/ui/select.tsx` | 드롭다운 셀렉트 |
| Slider | `web/src/components/ui/slider.tsx` | 슬라이더 |
| Tabs | `web/src/components/ui/tabs.tsx` | 탭 인터페이스 (Tabs, TabsList, TabsTrigger, TabsContent) |
| Sonner (Toaster) | `web/src/components/ui/sonner.tsx` | 토스트 알림 래퍼 (sonner 라이브러리 커스텀 래퍼, lucide 아이콘 사용) |

---

## 커스텀 훅

| 훅 | 파일 | 용도 |
|----|------|------|
| useWebSocket | `web/src/hooks/useWebSocket.ts` | WebSocket 연결 관리 훅. JWT 토큰 자동 포함, 자동 재연결(기본 3초), JSON 메시지 자동 파싱. 반환값: `{ connected, send, ws }`. Dashboard와 Processes 페이지에서 사용. |

### useWebSocket 옵션

```typescript
interface UseWebSocketOptions {
  url: string                    // WebSocket 경로 (예: '/ws/metrics')
  onMessage?: (data: any) => void // 메시지 수신 콜백
  autoReconnect?: boolean        // 자동 재연결 (기본 true)
  reconnectInterval?: number     // 재연결 간격 ms (기본 3000)
}
```

---

## API 클라이언트 메서드

**파일**: `web/src/lib/api.ts`

싱글턴 클래스 `ApiClient` (export: `api`). localStorage에 JWT 토큰 저장. 모든 요청에 `Authorization: Bearer <token>` 헤더 자동 포함. 응답은 `{ success, data, error }` 형식으로 래핑되며, `success === false` 시 Error throw.

### 토큰 관리
| 메서드 | 설명 |
|--------|------|
| `setToken(token: string)` | 토큰 설정 + localStorage 저장 |
| `clearToken()` | 토큰 제거 + localStorage 삭제 |
| `getToken(): string \| null` | 현재 토큰 반환 |
| `isAuthenticated(): boolean` | 토큰 존재 여부 |

### 인증 (Auth)
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `login(username, password, totpCode?)` | POST | `/auth/login` | `{ token: string }` | 로그인 |
| `getSetupStatus()` | GET | `/auth/setup-status` | `{ setup_required: boolean }` | 셋업 필요 여부 |
| `setupAdmin(username, password)` | POST | `/auth/setup` | `{ token: string }` | 초기 관리자 생성 |
| `changePassword(currentPassword, newPassword)` | POST | `/auth/change-password` | - | 비밀번호 변경 |
| `setup2FA()` | POST | `/auth/2fa/setup` | `{ secret: string; url: string }` | 2FA 설정 시작 |
| `verify2FA(secret, code)` | POST | `/auth/2fa/verify` | - | 2FA 코드 검증 |
| `disable2FA(password, totpCode?)` | DELETE | `/auth/2fa` | `{ message }` | (v0.38.0) 2FA 비활성화. **비밀번호 + 현재 TOTP 코드** 필요 |
| `get2FARecoveryStatus()` | GET | `/auth/2fa/recovery/status` | `{ generated, remaining }` | (v0.34.0) 복구 코드 생성 여부/잔여 개수 |
| `regenerate2FARecoveryCodes()` | POST | `/auth/2fa/recovery` | `{ codes: string[] }` | (v0.34.0) 복구 코드 생성/재생성 (평문은 이때만 반환) |

> **(v0.34.0)** `login()`은 4번째 인자 `recoveryCode?`를 받아 `{ username, password, totp_code, recovery_code }`를 전송한다.
>
> **(v0.19.0) `streamHeaders(base?)`** — `request()`를 우회하는 raw fetch/XHR 스트리밍 호출(SSE, 바이너리 blob, multipart 업로드)용 헤더 빌더. **Bearer 토큰 + CSRF double-submit 토큰**(`X-CSRF-Token`, `sfpanel_csrf` 쿠키)을 함께 실어 `CSRFProtect`의 403을 방지한다. system 업데이트·backup/restore·image pull·compose deploy·각종 install·파일 업로드 등 모든 스트리밍 POST가 이 헬퍼를 경유.

### 설정 (Settings)
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getSettings()` | GET | `/settings` | `Record<string, string>` | 설정 조회 |
| `updateSettings(settings)` | PUT | `/settings` | - | 설정 업데이트 |

### 시스템 (System)
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getSystemInfo()` | GET | `/system/info` | `{ host: any; metrics: any; version?: string }` | 호스트 정보 + 메트릭 + 패널 버전 |
| `getTopProcesses()` | GET | `/system/processes` | `Array<ProcessInfo>` | 상위 프로세스 목록 |
| `getMetricsHistory()` | GET | `/system/metrics-history` | `Array<{ time, cpu, mem_percent }>` | 24시간 메트릭 히스토리 |
| `listProcesses(query?, sort?)` | GET | `/system/processes/list` | `{ processes: ProcessInfo[]; total: number }` | 프로세스 목록 (검색/정렬) |
| `killProcess(pid, signal?)` | POST | `/system/processes/{pid}/kill` | - | 프로세스 종료 |
| `reniceProcess(pid, nice)` | POST | `/system/processes/{pid}/renice` | `{ message, pid, nice }` | (v0.20.0) nice 값 변경 (−20..19) |
| `getBackupSchedule()` | GET | `/system/backup/schedule` | `{ schedule: BackupScheduleConfig; archives: BackupFile[] }` | (v0.26.0) 예약 백업 설정 + 아카이브 목록 |
| `updateBackupSchedule(cfg)` | PUT | `/system/backup/schedule` | `{ message }` | (v0.26.0) `{ enabled, interval_hours, retention }` 저장 |
| `runBackupNow()` | POST | `/system/backup/schedule/run` | `{ message, name }` | (v0.26.0) 백업 즉시 실행 |

> **(v0.20.0)** `ProcessInfo`에 `ppid`(부모 PID), `rss`(절대 RSS), `nice` 필드가 추가됨 (`web/src/types/api.ts`).

### Docker 컨테이너
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getContainers()` | GET | `/docker/containers` | `any[]` | 컨테이너 목록 |
| `createContainer(config)` | POST | `/docker/containers` | `{ id, message }` | 컨테이너 생성 |
| `startContainer(id)` | POST | `/docker/containers/{id}/start` | - | 컨테이너 시작 |
| `stopContainer(id)` | POST | `/docker/containers/{id}/stop` | - | 컨테이너 중지 |
| `restartContainer(id)` | POST | `/docker/containers/{id}/restart` | - | 컨테이너 재시작 |
| `inspectContainer(id)` | GET | `/docker/containers/{id}/inspect` | ContainerInspectData | 컨테이너 상세정보 |
| `containerStats(id)` | GET | `/docker/containers/{id}/stats` | `{ cpu_percent, mem_usage, mem_limit, mem_percent }` | 컨테이너 리소스 통계 |
| `removeContainer(id)` | DELETE | `/docker/containers/{id}` | - | 컨테이너 삭제 |

### Docker 이미지
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getImages()` | GET | `/docker/images` | `any[]` | 이미지 목록 |
| `pullImage(image)` | POST | `/docker/images/pull` | - | 이미지 풀 |
| `removeImage(id)` | DELETE | `/docker/images/{id}` | - | 이미지 삭제 |
| `searchDockerHub(query, limit?)` | GET | `/docker/images/search` | `DockerHubSearchResult[]` | Docker Hub 이미지 검색 |

### Docker 볼륨
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getVolumes()` | GET | `/docker/volumes` | `any[]` | 볼륨 목록 |
| `createVolume(name)` | POST | `/docker/volumes` | - | 볼륨 생성 |
| `removeVolume(name)` | DELETE | `/docker/volumes/{name}` | - | 볼륨 삭제 |

### Docker 네트워크
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getNetworks()` | GET | `/docker/networks` | `any[]` | 네트워크 목록 |
| `createNetwork(name, driver?)` | POST | `/docker/networks` | - | 네트워크 생성 |
| `removeNetwork(id)` | DELETE | `/docker/networks/{id}` | - | 네트워크 삭제 |

### Docker Prune
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `pruneContainers()` | POST | `/docker/prune/containers` | `PruneReport` | 미사용 컨테이너 정리 |
| `pruneImages()` | POST | `/docker/prune/images` | `PruneReport` | 미사용 이미지 정리 |
| `pruneVolumes()` | POST | `/docker/prune/volumes` | `PruneReport` | 미사용 볼륨 정리 |
| `pruneNetworks()` | POST | `/docker/prune/networks` | `PruneReport` | 미사용 네트워크 정리 |
| `pruneAll()` | POST | `/docker/prune/all` | `PruneAllReport` | 전체 리소스 정리 |

### Docker Compose
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getComposeProjects()` | GET | `/docker/compose` | `ComposeProjectWithStatus[]` | 프로젝트 목록 (상태 포함) |
| `createComposeProject(name, yaml)` | POST | `/docker/compose` | - | 프로젝트 생성 |
| `getComposeProject(project)` | GET | `/docker/compose/{project}` | `{ project: ComposeProject; yaml: string }` | 프로젝트 상세 |
| `updateComposeProject(project, yaml)` | PUT | `/docker/compose/{project}` | - | 프로젝트 수정 |
| `deleteComposeProject(project)` | DELETE | `/docker/compose/{project}` | - | 프로젝트 삭제 |
| `composeUp(project)` | POST | `/docker/compose/{project}/up` | - | 프로젝트 시작 |
| `composeDown(project)` | POST | `/docker/compose/{project}/down` | - | 프로젝트 중지 |
| `getComposeServices(project)` | GET | `/docker/compose/{project}/services` | `ComposeService[]` | 서비스 목록 |
| `restartComposeService(project, service)` | POST | `/docker/compose/{project}/services/{service}/restart` | - | 서비스 재시작 |
| `stopComposeService(project, service)` | POST | `/docker/compose/{project}/services/{service}/stop` | - | 서비스 중지 |
| `startComposeService(project, service)` | POST | `/docker/compose/{project}/services/{service}/start` | - | 서비스 시작 |
| `getComposeServiceLogs(project, service, tail?)` | GET | `/docker/compose/{project}/services/{service}/logs` | `{ logs: string }` | 서비스 로그 |
| `getComposeEnv(project)` | GET | `/docker/compose/{project}/env` | `{ content: string }` | .env 파일 읽기 |
| `updateComposeEnv(project, content)` | PUT | `/docker/compose/{project}/env` | - | .env 파일 수정 |
| `migratePreflight` | POST | `/docker/compose/{project}/migrate/preflight` | - | 사전 점검 |
| `migrateStream` | POST(SSE) | `/docker/compose/{project}/migrate` | - | 노드 간 마이그레이션 |

### 파일 관리자
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `listFiles(path)` | GET | `/files?path=` | `any[]` | 파일/폴더 목록 |
| `readFile(path)` | GET | `/files/read?path=` | `{ content: string; size: number }` | 파일 읽기 |
| `writeFile(path, content)` | POST | `/files/write` | - | 파일 쓰기 |
| `createDir(path)` | POST | `/files/mkdir` | - | 디렉토리 생성 |
| `deletePath(path)` | DELETE | `/files?path=` | - | 파일/폴더 삭제 |
| `renamePath(oldPath, newPath)` | POST | `/files/rename` | - | 이름 변경 |
| `uploadFile(destPath, file, onProgress?)` | POST | `/files/upload` | - | 파일 업로드 (XHR, FormData, 진행률 콜백) |
| `downloadFile(path)` | GET | `/files/download?path=` | `Blob` | 파일 다운로드 |
| `searchFiles(path, q, limit?)` | GET | `/files/search` | (검색 결과 + 잘림 플래그) | (v0.21.0) 재귀 이름 검색 |
| `copyPath(src, dst)` | POST | `/files/copy` | - | (v0.21.0) 파일/디렉토리 복사 |

### 로그
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getLogSources()` | GET | `/logs/sources` | `LogSource[]` | 로그 소스 목록 (커스텀 포함) |
| `readLog(source, lines?)` | GET | `/logs/read?source=&lines=` | `{ source, lines[], total_lines }` | 로그 읽기 |
| `addCustomLogSource(name, path)` | POST | `/logs/custom-sources` | `{ id, source }` | 커스텀 로그 소스 추가 |
| `deleteCustomLogSource(id)` | DELETE | `/logs/custom-sources/{id}` | `{ message }` | 커스텀 로그 소스 삭제 |

### 크론 작업
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getCronJobs()` | GET | `/cron` | `any[]` | 크론 작업 목록 |
| `createCronJob(schedule, command)` | POST | `/cron` | - | 작업 생성 |
| `updateCronJob(id, schedule, command, enabled)` | PUT | `/cron/{id}` | - | 작업 수정 |
| `deleteCronJob(id)` | DELETE | `/cron/{id}` | - | 작업 삭제 |
| `runCronJob(id)` | POST | `/cron/{id}/run` | `{ output, success, error? }` | (v0.21.0 영역) 작업 즉시 실행 |

### 네트워크 관리
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getNetworkInterfaces()` | GET | `/network/interfaces` | `any[]` | 네트워크 인터페이스 목록 |
| `getNetworkInterface(name)` | GET | `/network/interfaces/{name}` | `any` | 인터페이스 상세 |
| `configureInterface(name, config)` | PUT | `/network/interfaces/{name}` | - | 인터페이스 설정 |
| `applyNetworkConfig()` | POST | `/network/apply` | `{ message }` | 네트워크 설정 적용 |
| `getDNSConfig()` | GET | `/network/dns` | `{ servers[], search[] }` | DNS 설정 조회 |
| `configureDNS(config)` | PUT | `/network/dns` | - | DNS 설정 변경 |
| `getRoutes()` | GET | `/network/routes` | `any[]` | 라우팅 테이블 |
| `getBonds()` | GET | `/network/bonds` | `any[]` | 본드 인터페이스 목록 |
| `createBond(data)` | POST | `/network/bonds` | - | 본드 생성 |
| `deleteBond(name)` | DELETE | `/network/bonds/{name}` | - | 본드 삭제 |

> 위 표는 v0.9.0 기준이며 WireGuard/Tailscale 메서드는 본문 페이지 섹션(Network > NetworkWireGuard / NetworkTailscale)에 정리되어 있다. v0.28.0 WireGuard 피어 관리 신규 메서드: `generateWireGuardKeypair()`(POST `/network/wireguard/keypair`), `addWireGuardPeer(name, peer)`(POST `.../configs/:name/peers`), `removeWireGuardPeer(name, publicKey)`(DELETE `.../configs/:name/peers?public_key=`), `setWireGuardAutostart(name, enabled)`(POST `.../configs/:name/autostart`).

### WireGuard 알림 (Alert) — webhook 채널
> **(v0.25.0)** 알림 채널 타입에 **webhook**이 Discord/Telegram에 추가되었다. AlertSettings 폼에서 채널 타입 `webhook` 선택 시 `webhook_url`을 입력하며, 서버가 Slack/Mattermost 호환 `text` + 구조화 필드를 POST한다. (전용 api.ts 메서드 없이 `/alerts/channels` POST의 `config.webhook_url`로 전송 — AlertSettings 섹션 참조.)

### 클러스터 (Cluster) — 캠페인 신규 메서드
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `listClusterTokens()` | GET | `/cluster/tokens` | `{ tokens: ClusterTokenInfo[] }` | (v0.19.0) 발급 토큰 목록(마스킹) |
| `revokeClusterToken(id)` | DELETE | `/cluster/tokens/{id}` | `{ revoked }` | (v0.19.0) 토큰 취소 |
| `updateClusterNodeAddress(nodeId, apiAddr, grpcAddr)` | PATCH | `/cluster/nodes/{nodeId}/address` | `{ node_id, api_address, grpc_address }` | (v0.19.0) 노드 광고 주소 편집 |
| `leaveCluster(force?)` | POST | `/cluster/leave` | `{ message }` | (v0.19.0) 로컬 노드 클러스터 탈퇴 (`?force=true`) |
| `clusterUpdateStream(mode, onEvent)` | POST | `/cluster/update` | (SSE) | (v0.22.0) 롤링/동시 업데이트 SSE 스트림 (per-node 스테퍼 구동) |

### 디스크 관리
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `checkSmartmontools()` | GET | `/disks/smartmontools-status` | `{ installed }` | smartmontools 설치 확인 |
| `installSmartmontools()` | POST | `/disks/install-smartmontools` | `{ message, output }` | smartmontools 설치 |
| `getDiskOverview()` | GET | `/disks/overview` | `any` | 디스크 개요 |
| `getDiskSmart(device)` | GET | `/disks/{device}/smart` | `SmartInfo` | SMART 정보 (셀프 테스트 로그 `self_tests` 포함) |
| `runSmartTest(device, type)` | POST | `/disks/{device}/smart/test` | `{ message, output }` | (v0.27.0) SMART 셀프 테스트 실행 (`'short' \| 'long'`) |
| `getDiskIOStats()` | GET | `/disks/iostat` | `IOStat[]` | I/O 통계 |
| `getDiskUsage(path, depth?)` | POST | `/disks/usage` | `DiskUsageEntry` | 디스크 사용량 |
| `getPartitions(device)` | GET | `/disks/{device}/partitions` | `any` | 파티션 목록 |
| `createPartition(device, data)` | POST | `/disks/{device}/partitions` | - | 파티션 생성 |
| `deletePartition(device, partition)` | DELETE | `/disks/{device}/partitions/{partition}` | - | 파티션 삭제 |
| `getFilesystems()` | GET | `/filesystems` | `Filesystem[]` | 파일시스템 목록 |
| `formatPartition(data)` | POST | `/filesystems/format` | - | 파티션 포맷 |
| `mountFilesystem(data)` | POST | `/filesystems/mount` | - | 마운트 |
| `unmountFilesystem(mountPoint)` | POST | `/filesystems/unmount` | - | 언마운트 |
| `resizeFilesystem(data)` | POST | `/filesystems/resize` | - | 파일시스템 리사이즈 |
| `getLVMPVs()` | GET | `/lvm/pvs` | `PhysicalVolume[]` | PV 목록 |
| `getLVMVGs()` | GET | `/lvm/vgs` | `VolumeGroup[]` | VG 목록 |
| `getLVMLVs()` | GET | `/lvm/lvs` | `LogicalVolume[]` | LV 목록 |
| `createPV(device)` | POST | `/lvm/pvs` | - | PV 생성 |
| `createVG(name, pvs)` | POST | `/lvm/vgs` | - | VG 생성 |
| `createLV(name, vg, size)` | POST | `/lvm/lvs` | - | LV 생성 |
| `removePV(name)` | DELETE | `/lvm/pvs/{name}` | - | PV 삭제 |
| `removeVG(name)` | DELETE | `/lvm/vgs/{name}` | - | VG 삭제 |
| `removeLV(vg, name)` | DELETE | `/lvm/lvs/{vg}/{name}` | - | LV 삭제 |
| `resizeLV(data)` | POST | `/lvm/lvs/resize` | - | LV 리사이즈 |
| `getRAIDArrays()` | GET | `/raid` | `RAIDArray[]` | RAID 배열 목록 |
| `getRAIDDetail(name)` | GET | `/raid/{name}` | `RAIDArray` | RAID 상세 |
| `createRAID(data)` | POST | `/raid` | - | RAID 생성 |
| `deleteRAID(name)` | DELETE | `/raid/{name}` | - | RAID 삭제 |
| `addRAIDDisk(name, device)` | POST | `/raid/{name}/add` | - | RAID 디스크 추가 |
| `removeRAIDDisk(name, device)` | POST | `/raid/{name}/remove` | - | RAID 디스크 제거 |
| `getSwapInfo()` | GET | `/swap` | `SwapInfo` | 스왑 정보 |
| `createSwap(data)` | POST | `/swap` | - | 스왑 생성 |
| `removeSwap(path)` | DELETE | `/swap` | - | 스왑 삭제 |
| `setSwappiness(value)` | PUT | `/swap/swappiness` | - | 스왑피니스 설정 |
| `checkSwapResize(path)` | GET | `/swap/resize-check` | `SwapResizeInfo` | 스왑 리사이즈 가능 여부 |
| `resizeSwap(data)` | PUT | `/swap/resize` | `{ steps[], message? }` | 스왑 리사이즈 |

### 패키지 관리
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `checkUpdates()` | GET | `/packages/updates` | `{ updates[], total, last_checked }` | 업데이트 확인 |
| `upgradePackages(packages?)` | POST | `/packages/upgrade` | - | 패키지 업그레이드 |
| `installPackage(name)` | POST | `/packages/install` | - | 패키지 설치 |
| `removePackage(name)` | POST | `/packages/remove` | - | 패키지 제거 |
| `searchPackages(query)` | GET | `/packages/search?q=` | `{ packages[], total, query }` | 패키지 검색 |
| `getDockerStatus()` | GET | `/packages/docker-status` | `{ installed, version, running, compose_available }` | Docker 상태 |
| `installDocker()` | POST | `/packages/install-docker` | - | Docker 설치 (SSE 스트리밍) |

### 방화벽 (UFW)
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getFirewallStatus()` | GET | `/firewall/status` | `{ active, default_incoming, default_outgoing }` | UFW 상태 조회 |
| `enableFirewall()` | POST | `/firewall/enable` | `{ message }` | UFW 활성화 |
| `disableFirewall()` | POST | `/firewall/disable` | `{ message }` | UFW 비활성화 |
| `getFirewallRules()` | GET | `/firewall/rules` | `{ rules: UFWRule[], total }` | UFW 규칙 목록 |
| `addFirewallRule(data)` | POST | `/firewall/rules` | `{ message, output }` | UFW 규칙 추가 |
| `deleteFirewallRule(number)` | DELETE | `/firewall/rules/{number}` | `{ message }` | UFW 규칙 삭제 |
| `getListeningPorts()` | GET | `/firewall/ports` | `{ ports: ListeningPort[], total }` | 리스닝 포트 목록 |

### Fail2ban
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getFail2banStatus()` | GET | `/fail2ban/status` | `{ installed, running, version }` | Fail2ban 상태 확인 |
| `installFail2ban()` | POST | `/fail2ban/install` | `{ message }` | Fail2ban 설치 |
| `getFail2banTemplates()` | GET | `/fail2ban/templates` | `{ templates[] }` | jail 템플릿 목록 |
| `createFail2banJail(data)` | POST | `/fail2ban/jails` | `{ message }` | jail 생성 |
| `deleteFail2banJail(name)` | DELETE | `/fail2ban/jails/{name}` | `{ message }` | jail 삭제 |
| `getFail2banJails()` | GET | `/fail2ban/jails` | `{ jails: Fail2banJail[], total }` | jail 목록 |
| `getFail2banJailDetail(name)` | GET | `/fail2ban/jails/{name}` | `Fail2banJail` | jail 상세 정보 |
| `updateFail2banJailConfig(name, config)` | PUT | `/fail2ban/jails/{name}/config` | `{ message }` | jail 설정 변경 |
| `enableFail2banJail(name)` | POST | `/fail2ban/jails/{name}/enable` | `{ message }` | jail 활성화 |
| `disableFail2banJail(name)` | POST | `/fail2ban/jails/{name}/disable` | `{ message }` | jail 비활성화 |
| `unbanFail2banIP(jail, ip)` | POST | `/fail2ban/jails/{jail}/unban` | `{ message }` | IP 차단 해제 |

### 앱스토어 (App Store)
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getAppStoreCategories()` | GET | `/appstore/categories` | `AppStoreCategory[]` | 카테고리 목록 |
| `getAppStoreApps(category?)` | GET | `/appstore/apps` | `AppStoreApp[]` | 앱 목록 (카테고리 필터) |
| `getAppStoreApp(id)` | GET | `/appstore/apps/{id}` | `{ app, compose, readme, readme_base_url, port_status[] }` | 앱 상세 + Compose + README + 포트 상태 |
| (직접 `fetch`) | POST | `/appstore/apps/{id}/install` | **SSE** `{stage,message,done,success}` | 앱 설치 (스트리밍; 래퍼 메서드 없음) |
| `getInstalledApps()` | GET | `/appstore/installed` | `InstalledApp[]` | 설치된 앱 목록 |
| `refreshAppStore()` | POST | `/appstore/refresh` | `{ message: string; apps: number; categories: number }` | 캐시 갱신 |

---

## 타입 정의

**파일**: `web/src/types/api.ts`

### ApiResponse<T>
```typescript
interface ApiResponse<T> {
  success: boolean
  data?: T
  error?: { code: string; message: string }
}
```

### 인증
```typescript
interface LoginRequest { username: string; password: string; totp_code?: string }
interface LoginResponse { token: string }
```

### 시스템
```typescript
interface HostInfo {
  hostname: string; os: string; platform: string;
  kernel: string; uptime: number; num_cpu: number
}
interface Metrics {
  cpu: number; mem_total: number; mem_used: number; mem_percent: number;
  swap_total: number; swap_used: number; swap_percent: number;
  disk_total: number; disk_used: number; disk_percent: number;
  net_bytes_sent: number; net_bytes_recv: number; timestamp: number
}
interface SystemInfo { host: HostInfo; metrics: Metrics }
```

### Docker
```typescript
interface Container {
  Id: string; Names: string[]; Image: string; State: string;
  Status: string; Ports: ContainerPort[]; Created: number;
  Labels: Record<string, string>
}
interface ContainerPort { PrivatePort: number; PublicPort: number; Type: string }
interface DockerImage {
  Id: string; RepoTags: string[]; Size: number; Created: number;
  in_use: boolean; used_by: string[]
}
interface DockerVolume {
  Name: string; Driver: string; Mountpoint: string; CreatedAt: string;
  in_use: boolean; used_by: string[]
}
interface DockerNetwork {
  Id: string; Name: string; Driver: string; Scope: string;
  in_use: boolean; used_by: string[]
}
interface ComposeProject {
  name: string; compose_file: string; has_env: boolean; path: string
}
interface ComposeProjectWithStatus extends ComposeProject {
  service_count: number; running_count: number; real_status: string
}
interface ComposeService {
  name: string; container_id: string; image: string;
  state: string; status: string; ports: string
}
interface ContainerCreateConfig {
  name: string; image: string; cmd?: string[]; env?: string[];
  ports?: Record<string, string>; volumes?: Record<string, string>;
  restart_policy?: string; memory_limit?: number; cpu_quota?: number;
  network_mode?: string; hostname?: string; labels?: Record<string, string>;
  auto_start?: boolean
}
interface DockerHubSearchResult {
  name: string; description: string; star_count: number; is_official: boolean
}
interface PruneReport { deleted: number; space_reclaimed?: number }
interface PruneAllReport {
  containers: PruneReport; images: PruneReport;
  volumes: PruneReport; networks: PruneReport
}
type MigrateDisposition = 'retain' | 'delete' | 'clone'
interface MigratePreflightFinding { code: string; message: string }
interface MigratePreflightReport {
  blocks: MigratePreflightFinding[]; warnings: MigratePreflightFinding[]
}
interface MigratePhaseEvent {
  phase: string; message: string; done: boolean
}
```

### 네트워크
```typescript
interface NetworkInterfaceInfo {
  name: string; type: string; state: string; mac_address: string;
  mtu: number; speed: number; addresses: NetworkAddress[];
  is_default: boolean; driver: string;
  tx_bytes: number; rx_bytes: number; tx_packets: number; rx_packets: number;
  tx_errors: number; rx_errors: number; bond_info?: BondInfo
}
interface NetworkAddress { address: string; prefix: number; family: string }
interface BondInfo { mode: string; slaves: string[]; primary: string }
interface InterfaceConfig {
  dhcp4: boolean; dhcp6: boolean; addresses: string[];
  gateway4: string; gateway6: string; dns: string[]
}
interface InterfaceDetail extends NetworkInterfaceInfo { config: InterfaceConfig | null }
interface DNSConfig { servers: string[]; search: string[] }
interface NetworkRoute {
  destination: string; gateway: string; interface: string;
  metric: number; protocol: string; scope: string
}
```

### 디스크 관리
```typescript
interface BlockDevice {
  name: string; size: number; type: string; fstype: string;
  mountpoint: string; model: string; serial: string;
  rotational: boolean; readonly: boolean; transport: string;
  state: string; vendor: string; children?: BlockDevice[]
}
interface SmartInfo {
  device_path: string; model_name: string; serial_number: string;
  firmware_version: string; healthy: boolean; temperature: number;
  power_on_hours: number; attributes: SmartAttr[]
}
interface SmartAttr {
  id: number; name: string; value: number; worst: number;
  threshold: number; raw_value: string
}
interface Filesystem {
  source: string; fstype: string; size: number; used: number;
  available: number; use_percent: number; mount_point: string
}
interface PhysicalVolume { name: string; vg_name: string; size: string; free: string; attr: string }
interface VolumeGroup { name: string; size: string; free: string; pv_count: number; lv_count: number; attr: string }
interface LogicalVolume {
  name: string; vg_name: string; size: string; attr: string;
  path: string; pool_lv: string; data_percent: string
}
interface RAIDArray {
  name: string; level: string; state: string; size: number;
  devices: RAIDDisk[]; active: number; total: number; failed: number; spare: number
}
interface RAIDDisk { device: string; state: string; index: number }
interface SwapEntry { name: string; type: string; size: number; used: number; priority: number }
interface SwapInfo { total: number; used: number; free: number; swappiness: number; entries: SwapEntry[] }
interface IOStat {
  device: string; read_ops: number; write_ops: number;
  read_bytes: number; write_bytes: number; io_time: number
}
interface DiskUsageEntry { path: string; size: number; children?: DiskUsageEntry[] }
```

### 크론 작업
```typescript
interface CronJob {
  id: number; schedule: string; command: string;
  enabled: boolean; raw: string; type: 'job' | 'env' | 'comment'
}
```

### 방화벽 (UFW)
```typescript
interface UFWStatus {
  active: boolean; default_incoming: string; default_outgoing: string
}
interface UFWRule {
  number: number; to: string; action: string;
  from: string; comment: string; v6: boolean
}
interface AddRuleRequest {
  action: string; port: string; protocol: string;
  from: string; to: string; comment: string
}
interface ListeningPort {
  protocol: string; address: string; port: number;
  pid: number; process: string
}
```

### Fail2ban
```typescript
interface Fail2banStatus {
  installed: boolean; running: boolean; version: string
}
interface Fail2banJail {
  name: string; enabled: boolean; filter: string;
  banned_count: number; total_banned: number; banned_ips: string[];
  max_retry: number; ban_time: string; find_time: string
}
```

### 앱스토어 (App Store)
```typescript
interface AppStoreCategory {
  id: string; name: { ko: string; en: string }; icon: string
}
interface AppStoreEnvVar {
  key: string; label: { ko: string; en: string }; type: string;
  default: string; required: boolean; generate: string
}
interface AppStoreApp {
  id: string; name: string;
  description: { ko: string; en: string };
  category: string; version: string;
  website: string; source: string;
  ports: string[]; env: AppStoreEnvVar[];
  installed: boolean
}
interface InstalledApp {
  id: string;
  details: { version: string; installed_at: string }
}
```

---

## 유틸리티

### web/src/lib/utils.ts

| 함수 | 용도 |
|------|------|
| `cn(...inputs: ClassValue[]): string` | Tailwind CSS 클래스 병합 유틸리티 (clsx + tailwind-merge) |
| `formatBytes(bytes: number): string` | 바이트를 사람이 읽기 좋은 형식으로 변환 (B/KB/MB/GB/TB/PB). 여러 페이지에서 공유 사용. |

### web/src/lib/logParsers.ts

로그 파서 유틸리티. 원시 로그 라인을 구조화된 엔트리로 파싱하여 컬럼 뷰를 제공.

| 함수/타입 | 용도 |
|-----------|------|
| `hasParsedView(sourceId)` | 해당 로그 소스에 구조화된 파서가 있는지 확인 |
| `getParser(sourceId)` | 소스 ID에 해당하는 파서 반환 |
| `parseLogLines(sourceId, lines)` | 로그 라인 배열을 파싱된 엔트리 배열로 변환 |
| `LogParser<T>` | 파서 인터페이스 (parse 함수 + columns 정의) |
| `ColumnDef<T>` | 컬럼 정의 (key, i18nKey, width, render) |
| `ParsedLogEntry` / `RawLogEntry` / `LogEntry` | 로그 엔트리 타입 |
| `AuthLogEntry` | auth.log 파싱 결과 (service, pid, event, sourceIP, user, details) |
| `UFWLogEntry` | ufw.log 파싱 결과 (action, sourceIP, destPort, protocol, iface) |
| `SFPanelLogEntry` | sfpanel.log 파싱 결과 (method, uri, status, latency, remoteIP) |

지원하는 로그 소스:
- `auth`: auth.log (SSH 로그인, sudo, 세션 이벤트)
- `ufw`: ufw.log (방화벽 BLOCK/ALLOW/AUDIT/LIMIT)
- `sfpanel`: sfpanel.log (Echo HTTP 요청 JSON + Go 로그)

---

## i18n 키 구조

**설정 파일**: `web/src/i18n/index.ts`
- 라이브러리: i18next + react-i18next + i18next-browser-languagedetector
- 지원 언어: `en` (English), `ko` (한국어)
- 감지 순서: localStorage (`sfpanel_language`) -> 브라우저 언어
- fallback: `en`

**로케일 파일**: `web/src/i18n/locales/ko.json`, `web/src/i18n/locales/en.json`

| 네임스페이스 | 설명 | 주요 키 |
|-------------|------|---------|
| `common` | 공통 UI 텍스트 | refresh, cancel, delete, create, save, loading, name, status, actions, created, edit, saving, creating |
| `layout` | 레이아웃/네비게이션 | brand, tagline, nav.dashboard/docker/appstore/files/cron/logs/processes/network/disk/firewall/packages/terminal/settings, logout, collapse, expand |
| `login` | 로그인 페이지 | title, subtitle, username, password, totpCode, signIn, signingIn, totpRequired |
| `setup` | 초기 셋업 | subtitle, username, password, confirmPassword, createAdmin, passwordMinLength, passwordMismatch |
| `dashboard` | 대시보드 | title, subtitle, live, disconnected, hostInfo, hostname, os, platform, kernel, uptime, cpuCores, cpuUsage, memory, disk, network, chartTitle, topProcesses, dockerSummary, recentLogs, quickActions, sent, received |
| `processes` | 프로세스 관리 | title, subtitle, total, searchPlaceholder, sortBy, sort_cpu/memory/pid/name, kill, killTitle, killConfirm, killDescription, cpuUsage, memUsage, swapUsage, running/sleeping/zombie/stopped/idle |
| `docker` | Docker 메인 | title, sidebar.stacks/containers/images/volumes/networks/prune |
| `docker.containers` | 컨테이너 관리 | count, total, running, stopped, name, image, status, ports, resources, terminal, stop, start, restart, logs, shell, inspect, memory, generalInfo, command, workingDir, hostname, portBindings, volumes, networkInfo, envVars, searchPlaceholder, stopTitle/restartTitle/deleteTitle + Confirm |
| `docker.containerCreate` | 컨테이너 생성 | title, subtitle, imagePlaceholder, containerName 등 |
| `docker.images` | 이미지 관리 | count, repoTag, imageId, size, pullImage, pullDescription, imageReference, pulling, pull, deleteTitle/Confirm |
| `docker.volumes` | 볼륨 관리 | count, driver, mountpoint, createVolume, createDescription, volumeName, deleteTitle/Confirm |
| `docker.networks` | 네트워크 관리 | count, id, driver, scope, createNetwork, createDescription, networkName, cannotDeletePredefined, deleteTitle/Confirm |
| `docker.compose` | Compose 관리 | count, newProject, createTitle, createDescription, projectName, composeFile, up, down, editTitle, editDescription, deleteTitle/Confirm |
| `docker.prune` | Docker 정리 | title, containers, images, volumes, networks, success 등 |
| `network` | 네트워크 관리 | title, subtitle, interfaces, dnsServers, routes, bonding, configure, up, down, speed, bondMode 등 |
| `disk` | 디스크 관리 | title, tabs.overview/partitions/filesystems/lvm/raid/swap 등 |
| `settings` | 설정 페이지 | title, subtitle, language, languageDescription, changePassword, currentPassword, newPassword, confirmNewPassword, twoFA, twoFAEnabled/NotConfigured, enable2FA, scanQR, secretKey, verificationCode, systemInfo, version, terminal, terminalTimeout, fileUpload, maxUploadSize |
| `terminal` | 터미널 | connectingLogs, connectingShell, connected, wsError, connectionClosed, notAuthenticated, newTab, noTabs, fontSmaller, fontLarger, search, searchPlaceholder, prev, next |
| `files` | 파일 관리자 | title, count, name, size, modified, permissions, empty, loading, newFile, newFolder, upload, edit, editFile, download, rename, renameTitle, deleteTitle, deleteConfirm |
| `cron` | 크론 작업 | title, count, showAll, newJob, tableTitle, schedule, command, type, presets, presetEveryMinute/Hour/Daily/Weekly/Monthly, createTitle, editTitle, deleteTitle |
| `logs` | 로그 뷰어 | title, subtitle, sources, lines, live, autoScroll, refresh, clear, search, searchPlaceholder, totalLines, linesShown, connected, disconnected, download, col.timestamp/service/event/sourceIP/user/details/action/destPort/protocol/interface/method/status/latency |
| `packages` | 패키지 관리 | title, subtitle, dockerStatus, dockerDescription, installDocker, systemUpdates, checkForUpdates, upgradeAll, searchAndInstall, search, install, remove, operationComplete, operationRunning |
| `firewall` | 방화벽 관리 | title, tabs.rules/ports/fail2ban, status, enable, disable, rules, addRule, deleteRule, ports, action, port, protocol, from, to, comment, listeningPorts |
| `firewall.fail2ban` | Fail2ban | status, install, jails, enable, disable, unban, bannedIPs, maxRetry, banTime, findTime |
| `appstore` | 앱스토어 | title, subtitle, searchPlaceholder, allCategories, install, installed, installing, installTitle, installDescription, envVars, refresh, refreshing, noApps, port, website, viewStack, generated |

---

## WebSocket 엔드포인트 정리

| 경로 | 용도 | 사용 페이지 |
|------|------|------------|
| `/ws/metrics` | 시스템 메트릭 실시간 스트리밍 (Metrics JSON) | Dashboard, Processes |
| `/ws/logs?source={source}` | 로그 실시간 스트리밍 | Logs |
| `/ws/terminal?session_id={id}` | 서버 셸 세션 (바이너리 + JSON resize) | Terminal |
| `/ws/docker/containers/{id}/exec` | 컨테이너 셸 접속 | DockerContainers (ContainerShell), DockerStacks (ContainerShell) |
| `/ws/docker/containers/{id}/logs` | 컨테이너 로그 스트리밍 | DockerContainers (ContainerLogs), DockerStacks (ContainerLogs) |
| `/ws/cluster/overview` | (v0.31.0) status + overview + recent events 통합 스냅샷 푸시(5s 샘플러, follower는 `stale`) | ClusterOverview (`useWebSocket`) |

모든 WebSocket 연결은 `?token={JWT}` 쿼리 파라미터로 인증.

---

## 디자인 패턴 요약

- **코드 분할**: 모든 페이지는 `React.lazy()` + `<Suspense>`로 동적 임포트. `PageLoader` 컴포넌트가 로딩 폴백.
- **카드 스타일**: `bg-card rounded-2xl p-5/p-6 card-shadow` (shadcn/ui Card 미사용, 직접 Tailwind 클래스)
- **배지 스타일**: 인라인 `span` + `px-2 py-0.5 rounded-full text-[11px] font-medium` + 상태별 색상
- **색상 체계**: Primary blue(`#3182f6`), Green(`#00c471`), Red(`#f04452`), Yellow(`#f59e0b`), Purple(`#8b5cf6`)
- **폰트 크기**: 11px(보조), 13px(본문), 15px(서브타이틀), 22px(페이지 제목)
- **다이얼로그 패턴**: 확인 다이얼로그는 항상 취소/확인 버튼, 위험 작업은 `variant="destructive"`
- **(v0.30.0~v0.36.0) 확인/입력 다이얼로그 통일**: 네이티브 `window.confirm`/`window.prompt`는 앱 루트(`App.tsx`)의 `ConfirmProvider`/`PromptProvider` + `useConfirm()`/`usePrompt()` 훅으로 대체. 데이터 손실 가능 작업(디스크 포맷, 파티션/RAID 삭제, 클러스터 disband)은 `TypeToConfirmDialog`로 정확한 이름 입력 게이트.
- **(v0.33.0) 로딩/에러/빈 상태 3분기**: 목록 페이지는 Skeleton(첫 로드) / 인라인 빨강 에러 + Retry / empty state를 구분.
- **(v0.35.0) 반응형 테이블**: 넓은 테이블은 `hidden md:*` 데스크톱 테이블 + `md:hidden` 모바일 카드 목록 폴백.
- **에러 처리**: try/catch + toast.error, 에러 메시지는 err.message 또는 i18n 번역 키
- **로딩 상태**: 개별 상태 변수 관리, 버튼에 `disabled={loading}` + 스피너 아이콘
- **공유 유틸리티**: `formatBytes()`는 `web/src/lib/utils.ts`에서 공유 (각 페이지에서 중복 정의하지 않음)
- **Docker 탭 네비게이션**: shadcn/ui Tabs 대신 React Router NavLink + `<Outlet />` 패턴으로 URL 기반 서브라우트
- **사이드바**: 접기/펼치기 토글 지원, 상태 localStorage 영속화

---

## 상태 관리 현황

전역 상태 라이브러리는 **사용하지 않음** — Zustand / Context API / TanStack Query / SWR 어느 것도 도입되지 않았음. 모든 상태는 다음 중 하나:

- 페이지 내부 `useState` / `useEffect`
- `localStorage` (영속화)
- `ApiClient` 싱글턴 인스턴스의 private 필드 (토큰, 현재 노드 ID, Tauri 모드 플래그 등)

### localStorage 영속화 키

| 키 | 용도 | 관리 위치 |
|----|------|----------|
| `token` | JWT 액세스 토큰 | `ApiClient` (login/logout) |
| `sfpanel_server_url` | Tauri 모드에서 원격 서버 URL | `Connect.tsx`, `ApiClient` |
| `sfpanel_current_node` | 클러스터 원격 노드 ID (`?node=` 자동 주입) | `ClusterSidebar`(`api.setCurrentNode`), `ApiClient` |
| `sfpanel_language` | i18next 선택 언어 | `Settings.tsx`, i18next detector |
| `sfpanel-sidebar-collapsed` | 사이드바 접기 상태 | `Layout.tsx` |
| `sfpanel_terminal_tabs` | 터미널 탭 상태 (이름, 순서) | `Terminal.tsx` |
| `sfpanel_file_path` | 파일 관리자 마지막 경로 | `Files.tsx` |

---

## API 클라이언트 (`lib/api.ts`)

- **싱글턴 패턴**: 파일 하단에서 `export const api = new ApiClient()`로 단일 인스턴스만 공개
- **80+ 메서드** — auth / system / docker / files / network / disk / firewall / logs / packages / cluster 영역별 그룹화
- **자동 주입**: 모든 요청에 `Authorization: Bearer <token>` 헤더와 (클러스터 모드인 경우) `?node=<id>` 쿼리 파라미터 자동 삽입
- **타임아웃**: 30초 기본 (`AbortController`), 메서드별 재정의 가능
- **실패 처리**: `{success:false, error:{code,message}}` 응답은 Error로 throw — 각 페이지가 toast로 표시
- **SSE 헬퍼**: `readSSEStream(url, body, onEvent)` — `fetch`로 POST 후 `ReadableStream`을 라인 단위 파싱, 종료 마커(`[DONE]` 또는 `step:complete`)에서 resolve
- **WebSocket URL 빌더**: `buildWsUrl(path)` — `ws:`/`wss:` 자동 결정 + 토큰 + 노드 ID 병합
- **파일 다운로드/업로드**: `downloadBackup()`(Blob), `restoreBackup()`(FormData)

---

## 빌드 & 번들링

### Vite 설정 (`web/vite.config.ts`)

- 플러그인: `@vitejs/plugin-react`, `@tailwindcss/vite` (Tailwind v4 JIT), `vite-plugin-pwa`
- **수동 청크** (6개):
  - `react-vendor` — react, react-dom, react-router-dom
  - `ui-vendor` — cva, clsx, tailwind-merge
  - `xterm` — xterm + 애드온
  - `i18n` — i18next 계열
  - `uplot` — 시계열 차트
  - `monaco` — Monaco Editor
- 경고 한계: 1000KB
- **PWA**: 서비스워커 자동 생성. 캐시 포함 — `*.css/html/ico/png/svg/woff2`, `assets/*.js`. 캐시 제외 — Monaco/xterm/TypeScript 워커 파일 (바이트 큰 WASM/Worker는 네트워크에서만 로드)

### 빌드 출력

- `web/dist/` — 약 **16 MB** (정적 자산 포함, Monaco+xterm 번들 영향)
- Go 바이너리: `web.go`의 `//go:embed all:web/dist`로 전체 통합 → 단일 실행 파일

### Dev 서버 포트

- Vite: `:5173` (HMR)
- 백엔드 API/WS: `:3628` (config 기본값, 프로덕션 `install.sh`도 동일)

### 린트/타입체크/테스트

- ESLint 9 flat config + `typescript-eslint` + `eslint-plugin-react-hooks` + `eslint-plugin-react-refresh`
- 타입 체크: `tsc -b` (빌드 전)
- **자동 테스트 없음** — Vitest / Playwright 미설정. 변경 사항 검증은 수동 브라우저 테스트 또는 백엔드 `make test`에 의존.

---

## Desktop (Tauri 2)

`desktop/` 디렉토리 (`com.sfpanel.desktop`). Tauri 2 래퍼로 Windows/macOS/Linux 네이티브 앱 제공.

- **프론트엔드 주입**: `../../web/dist` (빌드 산출물)
- **dev URL**: `http://localhost:5173`
- **빌드 스크립트**:
  - `beforeDevCommand`: `cd ../web && npm run dev`
  - `beforeBuildCommand`: `cd ../web && npm run build`
- **윈도우**: 1280×800 기본, 최소 900×600, 중앙 정렬
- **CSP**: `connect-src 'self' http: https: ws: wss:`, `script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net`
- **플랫폼 산출물**: Windows `.exe`, macOS `.dmg` + `.app`, Linux `.deb` + `AppImage`
- **Tauri 감지**: 프론트엔드가 `window.__TAURI_INTERNALS__`를 검사하여 `api.isTauri` 플래그 설정. `TauriGuard`가 서버 URL 미설정 시 `/connect`로 리다이렉트
