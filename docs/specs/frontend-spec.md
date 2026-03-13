# SFPanel 프론트엔드 스펙

## 개요

- **프레임워크**: React 19 + TypeScript + Vite 7
- **스타일**: Tailwind CSS v4 + shadcn/ui (일부 컴포넌트) + Toss 디자인 시스템 영향 (컬러, 라운딩, 그림자)
- **상태 관리**: React hooks (useState, useEffect, useCallback, useRef, useMemo)
- **라우팅**: React Router v7 (BrowserRouter)
- **국제화**: react-i18next + i18next-browser-languagedetector (한국어/영어)
- **토스트 알림**: Sonner (via shadcn/ui 래퍼)
- **코드 에디터**: Monaco Editor (@monaco-editor/react)
- **터미널**: xterm.js (@xterm/xterm + fit/web-links/search 애드온)
- **차트**: Recharts v3 (LineChart)
- **아이콘**: Lucide React
- **엔트리포인트**: `web/src/main.tsx` -> `<App />`
- **CSS**: `web/src/index.css` (Tailwind 설정)
- **코드 분할**: `React.lazy()` + `<Suspense>`로 모든 페이지를 lazy loading

---

## 라우팅

`App.tsx`에서 정의. `SetupGuard`가 최상위에서 초기 셋업 여부를 체크하고, `ProtectedRoute`가 JWT 토큰 기반 인증을 검증한다. 모든 페이지 컴포넌트는 `React.lazy()`로 동적 임포트되며, `<Suspense fallback={<PageLoader />}>`로 감싸져 코드 분할을 구현한다.

| 경로 | 컴포넌트 | 인증 필요 | 레이아웃 | 설명 |
|------|----------|-----------|----------|------|
| `/login` | Login | X | 없음 (독립) | 관리자 로그인 |
| `/setup` | Setup | X | 없음 (독립) | 초기 관리자 계정 생성 (첫 실행 시) |
| `/` | - | O | Layout | `/dashboard`로 리다이렉트 |
| `/dashboard` | Dashboard | O | Layout | 시스템 대시보드 (실시간 메트릭) |
| `/appstore` | AppStore | O | Layout | 앱스토어 (원클릭 Docker 앱 설치) |
| `/docker` | Docker | O | Layout | Docker 관리 (사이드 탭 + Outlet 구조) |
| `/docker/stacks` | DockerStacks | O | Docker | Docker Compose 스택 목록 (기본 서브라우트) |
| `/docker/stacks/:name` | DockerStacks | O | Docker | 스택 상세 (서비스 목록, YAML 편집, 로그, 셸) |
| `/docker/containers` | DockerContainers | O | Docker | 컨테이너 관리 |
| `/docker/containers/create` | DockerContainerCreate | O | Docker | 컨테이너 생성 폼 |
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
| `/firewall/fail2ban` | FirewallFail2ban | O | Firewall | Fail2ban jail 관리 |
| `/firewall/docker` | FirewallDocker | O | Firewall | Docker 방화벽 (DOCKER-USER 체인) |
| `/firewall/logs` | FirewallLogs | O | Firewall | 방화벽 로그 뷰어 |
| `/packages` | Packages | O | Layout | 시스템 패키지 관리 + Docker 설치 |
| `/terminal` | Terminal | O | Layout | 웹 터미널 (멀티 탭) |
| `/settings` | Settings | O | Layout | 계정/시스템 설정 |

### 라우트 가드

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

### Login
- **파일**: `web/src/pages/Login.tsx`
- **기능**: 관리자 로그인 폼. 사용자명/비밀번호 입력 후 JWT 토큰 수령. 서버에서 2FA 요구 시 TOTP 코드 입력 필드가 동적으로 표시됨.
- **사용 API**: `api.login(username, password, totpCode?)`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)
- **상태**: username, password, totpCode, showTotp, error, loading

### Setup
- **파일**: `web/src/pages/Setup.tsx`
- **기능**: 첫 실행 시 관리자 계정 생성 위저드. 사용자명(기본값 "admin"), 비밀번호, 비밀번호 확인 입력. 최소 8자 검증.
- **사용 API**: `api.setupAdmin(username, password)`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)
- **상태**: username, password, confirmPassword, error, loading

### Dashboard
- **파일**: `web/src/pages/Dashboard.tsx`
- **기능**: 시스템 전체 현황 대시보드
  - 호스트 정보 표시 (hostname, OS, platform, kernel, uptime, CPU 코어)
  - 실시간 메트릭 카드 4개 (CPU, 메모리, 디스크, 네트워크)
  - CPU/메모리 24시간 히스토리 차트 (최대 2880 포인트)
  - Docker 컨테이너 요약 (실행/중지/전체 카운트 + 상위 5개 컨테이너 목록)
  - 네트워크 I/O 실시간 송수신 속도 및 누적량
  - 프로세스 현황 테이블 (CPU 사용률 상위, 10초마다 갱신)
  - 최근 시스템 로그 (syslog 최신 8줄)
  - 빠른 실행 바로가기 5개 (파일, Docker, 패키지, 크론, 로그)
- **사용 API**: `api.getSystemInfo()`, `api.getMetricsHistory()`, `api.getTopProcesses()`, `api.getContainers()`, `api.readLog('syslog', 8)`
- **WebSocket**: `useWebSocket({ url: '/ws/metrics' })` - 실시간 시스템 메트릭 수신 (Metrics 타입)
- **사용 컴포넌트**: MetricsCard, MetricsChart, Table (shadcn/ui)

### Docker
- **파일**: `web/src/pages/Docker.tsx`
- **기능**: Docker 관리 컨테이너. NavLink 기반 사이드 탭으로 5개 서브페이지를 네비게이션하고, `<Outlet />`으로 서브라우트 콘텐츠를 렌더링. Prune 기능 포함.
- **탭 구조** (NavLink, `<Outlet />` 패턴):
  - `/docker/stacks` (기본값) -> DockerStacks
  - `/docker/containers` -> DockerContainers
  - `/docker/containers/create` -> DockerContainerCreate
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
- **사용 API**: `api.getComposeProjects()`, `api.createComposeProject()`, `api.getComposeProject()`, `api.updateComposeProject()`, `api.deleteComposeProject()`, `api.composeUp()`, `api.composeDown()`, `api.getComposeServices()`, `api.restartComposeService()`, `api.stopComposeService()`, `api.startComposeService()`, `api.getComposeServiceLogs()`, `api.getComposeEnv()`, `api.updateComposeEnv()`
- **사용 컴포넌트**: Table, Dialog, Tabs, Button, Input, Label, ComposeEditor, ContainerLogs, ContainerShell (shadcn/ui + 커스텀)

### Docker > DockerContainers
- **파일**: `web/src/pages/docker/DockerContainers.tsx`
- **기능**: 컨테이너 목록 및 관리
  - 요약 카드 3개 (전체/실행 중/중지됨) - 클릭 시 필터링
  - 검색 (이름/이미지 기준)
  - 컨테이너 테이블: 이름, 이미지, 상태 배지, 리소스(CPU/MEM 실시간), 포트, 생성일
  - 컨테이너별 액션: 상세정보(Inspect), 터미널(Shell), 시작/중지/재시작, 삭제
  - 상세정보 다이얼로그: Inspect(자원 사용량, 일반정보, 포트, 볼륨, 네트워크, 환경변수) / Logs / Shell 탭
  - 중지/재시작/삭제 확인 다이얼로그
- **사용 API**: `api.getContainers()`, `api.startContainer()`, `api.stopContainer()`, `api.restartContainer()`, `api.removeContainer()`, `api.inspectContainer()`, `api.containerStats()`
- **사용 컴포넌트**: Table, Dialog, Tabs, Button, Input, ContainerLogs, ContainerShell (shadcn/ui + 커스텀)
- **내부 서브컴포넌트**:
  - `ContainerStatsCell`: 개별 컨테이너 CPU/MEM 실시간 표시 (5초 주기 폴링)
  - `ContainerInspect`: 컨테이너 상세정보 패널 (리소스 게이지, 일반정보, 포트, 볼륨, 네트워크, 환경변수)

### Docker > DockerContainerCreate
- **파일**: `web/src/pages/docker/DockerContainerCreate.tsx`
- **기능**: Docker 컨테이너 생성 폼
  - Docker Hub 이미지 검색 (DockerHubSearch 자동완성 컴포넌트)
  - 컨테이너 이름 입력
  - 포트 매핑 (host/container/protocol) 동적 추가/삭제
  - 볼륨 매핑 (host/container) 동적 추가/삭제
  - 환경 변수 (key/value) 동적 추가/삭제
  - 고급 옵션 토글: 재시작 정책, 메모리 제한, 네트워크 모드, 호스트네임, 명령어
  - 생성 + 자동 시작 옵션
- **사용 API**: `api.createContainer(config)`
- **사용 컴포넌트**: Button, Input, Label, DockerHubSearch (shadcn/ui + 커스텀)

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
- **사용 API**: `api.getAppStoreCategories()`, `api.getAppStoreApps()`, `api.getAppStoreAppDetail()`, `api.installAppStoreApp()`, `api.getInstalledApps()`, `api.refreshAppStore()`
- **사용 컴포넌트**: Dialog, Button, Input, Label (shadcn/ui)
- **사이드바 위치**: Docker 다음, 아이콘: `Store` (lucide-react)

### Files
- **파일**: `web/src/pages/Files.tsx`
- **기능**: 서버 파일 관리자
  - 브레드크럼 경로 네비게이션 (클릭 시 경로 직접 입력 가능)
  - 파일/폴더 테이블: 이름(아이콘 구분), 크기, 수정일, 권한
  - 디렉토리 우선 정렬 (알파벳순)
  - 파일 클릭 시 Monaco 에디터로 편집 (언어 자동 감지: 30+ 확장자 지원)
  - 새 파일 생성, 새 폴더 생성, 파일 업로드 (XHR + FormData, 진행률 표시), 다운로드
  - 이름 변경, 삭제 확인 다이얼로그
  - 도구 모음: 새로고침, 새 파일, 새 폴더, 업로드
- **사용 API**: `api.listFiles()`, `api.readFile()`, `api.writeFile()`, `api.createDir()`, `api.deletePath()`, `api.renamePath()`, `api.uploadFile()`, `api.downloadFile()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label, Monaco Editor (shadcn/ui + @monaco-editor/react)

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
- **사용 API**: `api.getCronJobs()`, `api.createCronJob()`, `api.updateCronJob()`, `api.deleteCronJob()`
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
  - 프로세스 종료 다이얼로그: SIGTERM(정상) / SIGKILL(강제) 선택
  - 5초 자동 갱신
- **사용 API**: `api.listProcesses(query, sort)`, `api.killProcess(pid, signal)`
- **WebSocket**: `useWebSocket({ url: '/ws/metrics' })` - 시스템 메트릭 수신 (리소스 요약 카드용)
- **사용 컴포넌트**: Table, Dialog, Button, Input (shadcn/ui)

### Network
- **파일**: `web/src/pages/Network.tsx`
- **기능**: 네트워크 인터페이스 관리
  - 인터페이스 카드 그리드: 이름, 상태(up/down), IP 주소(IPv4/IPv6), MAC, 속도, 트래픽(TX/RX), 에러 수
  - 인터페이스 타입별 아이콘 구분 (ethernet/wireless/loopback/bond)
  - 기본 게이트웨이 인터페이스 표시 (ring 강조)
  - 인터페이스 설정 다이얼로그: DHCP/Static 토글, IP 주소, 게이트웨이(IPv4/IPv6), DNS, MTU
  - DNS 서버 설정 (인라인 편집, 쉼표 구분)
  - 라우팅 테이블: destination, gateway, interface, metric, protocol
  - 본딩 관리: 본드 생성(이름/모드/슬레이브 선택), 본드 삭제, 본드 모드(7종) 지원
  - 설정 변경 시 플로팅 "적용" 버튼 + 경고 다이얼로그
- **사용 API**: `api.getNetworkInterfaces()`, `api.configureInterface()`, `api.applyNetworkConfig()`, `api.getDNSConfig()`, `api.configureDNS()`, `api.getRoutes()`, `api.getBonds()`, `api.createBond()`, `api.deleteBond()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)
- **사용 유틸리티**: `formatBytes()` (`web/src/lib/utils.ts`)
- **로컬 인터페이스**: NetworkAddress, BondInfo, NetworkInterface, InterfaceConfig, Route, DNSConfig

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
  - `web/src/pages/disk/DiskOverview.tsx` — 디스크 개요 (블록 디바이스, SMART, I/O 통계, 디스크 사용량)
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
  - 탭 구조: UFW Rules, Open Ports, Fail2ban
  - **UFW Rules 탭** (`FirewallRules`): UFW 활성화/비활성화 토글, 규칙 목록 테이블 (번호/대상/동작/소스/코멘트/IPv6), 규칙 추가 다이얼로그 (action/port/protocol/from/to/comment), 규칙 삭제 확인 다이얼로그
  - **Open Ports 탭** (`FirewallPorts`): 리스닝 TCP/UDP 포트 목록 테이블 (프로토콜/주소/포트/PID/프로세스), 선택한 포트로 UFW 규칙 직접 추가 기능
  - **Fail2ban 탭** (`FirewallFail2ban`): Fail2ban 설치 상태 확인 및 원클릭 설치, jail 템플릿에서 생성, jail 목록 테이블 (이름/활성/차단수/총차단수), jail 상세 (설정값, 차단 IP 목록), jail 활성화/비활성화, jail 설정 편집, jail 삭제, IP 차단 해제
- **탭 구성**:
  - rules (기본값) -> FirewallRules
  - ports -> FirewallPorts
  - fail2ban -> FirewallFail2ban
- **사용 API**: `api.getFirewallStatus()`, `api.enableFirewall()`, `api.disableFirewall()`, `api.getFirewallRules()`, `api.addFirewallRule()`, `api.deleteFirewallRule()`, `api.getListeningPorts()`, `api.getFail2banStatus()`, `api.installFail2ban()`, `api.getFail2banTemplates()`, `api.createFail2banJail()`, `api.deleteFail2banJail()`, `api.getFail2banJails()`, `api.getFail2banJailDetail()`, `api.updateFail2banJailConfig()`, `api.enableFail2banJail()`, `api.disableFail2banJail()`, `api.unbanFail2banIP()`
- **사용 컴포넌트**: Tabs, TabsList, TabsTrigger, TabsContent, Table, Dialog, Button, Input, Select (shadcn/ui)
- **서브 컴포넌트 파일**:
  - `web/src/pages/firewall/FirewallRules.tsx` — UFW 규칙 관리
  - `web/src/pages/firewall/FirewallPorts.tsx` — 리스닝 포트 조회
  - `web/src/pages/firewall/FirewallFail2ban.tsx` — Fail2ban 관리

### Packages
- **파일**: `web/src/pages/Packages.tsx`
- **기능**: 시스템 패키지 관리
  - Docker 상태 카드: 설치 여부, 버전, 실행 상태, Compose 가용성 표시. 미설치 시 Docker 설치 버튼 (SSE 스트리밍 출력)
  - 시스템 업데이트: 업데이트 확인, 전체/선택 업그레이드, 패키지 체크박스 선택
  - 패키지 검색/설치: 검색 결과에서 설치/제거 가능
  - 작업 출력 다이얼로그: 설치/업그레이드/제거 진행 상황 실시간 표시
- **사용 API**: `api.getDockerStatus()`, `api.installDocker()` (SSE 스트리밍), `api.checkUpdates()`, `api.upgradePackages()`, `api.installPackage()`, `api.removePackage()`, `api.searchPackages()`
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
- **사용 API**: `api.getToken()` (WebSocket 인증)
- **WebSocket**: 직접 관리 (`/ws/terminal?token={token}&session_id={id}`)
- **사용 컴포넌트**: Button, Input (shadcn/ui)
- **내부 서브컴포넌트**: `TerminalSession` - 개별 터미널 세션 관리

### Settings
- **파일**: `web/src/pages/Settings.tsx`
- **기능**: 계정 및 시스템 설정
  - 언어 변경 (English / 한국어) - i18n.changeLanguage()
  - 터미널 타임아웃 설정 (분 단위, 0 = 무제한)
  - 파일 업로드 최대 크기 설정 (MB 단위)
  - 비밀번호 변경 (현재 비밀번호 + 새 비밀번호 + 확인)
  - 2FA 관리: 설정 시작 -> QR 코드(외부 API로 생성) + 시크릿 키 표시 -> 6자리 코드 인증
  - 시스템 정보 표시 (버전, 호스트명, OS, 커널, 가동시간)
  - **버전 표시**: `api.getSystemInfo()`의 `data.version` 필드에서 가져옴 (`v${data.version}` 형식)
- **사용 API**: `api.getSettings()`, `api.updateSettings()`, `api.changePassword()`, `api.setup2FA()`, `api.verify2FA()`, `api.getSystemInfo()`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)

---

## 공용 컴포넌트

| 컴포넌트 | 파일 | 용도 |
|----------|------|------|
| Layout | `web/src/components/Layout.tsx` | 인증된 페이지의 공통 레이아웃. 좌측 사이드바(네비게이션 12항목 + 접기/펼치기 + 로그아웃) + 우측 메인 콘텐츠(Outlet). NavLink로 활성 상태 표시. 사이드바 접기 상태 localStorage 영속화. |
| MetricsCard | `web/src/components/MetricsCard.tsx` | 메트릭 표시 카드. 아이콘 + 제목 + 값 + 프로그레스 바(80% 초과 빨강, 60% 초과 노랑, 그 외 파랑). |
| MetricsChart | `web/src/components/MetricsChart.tsx` | CPU/메모리 시계열 차트. Recharts LineChart 사용. CPU(파랑) + Memory(초록) 이중 라인. Y축 0-100%, 애니메이션 비활성화. |
| ContainerShell | `web/src/components/ContainerShell.tsx` | Docker 컨테이너 셸 접속. xterm.js + WebSocket(`/ws/docker/containers/{id}/exec`). 키 입력 전송, 리사이즈 이벤트 전송. |
| ContainerLogs | `web/src/components/ContainerLogs.tsx` | Docker 컨테이너 로그 스트리밍. xterm.js(읽기 전용) + WebSocket(`/ws/docker/containers/{id}/logs`). 검색(SearchAddon), 로그 다운로드 기능. |
| ComposeEditor | `web/src/components/ComposeEditor.tsx` | YAML/텍스트 에디터. Monaco Editor 래퍼. 높이 400px, vs-dark 테마, 미니맵 비활성화, 자동 레이아웃. Props: `value`, `onChange`, `language`(기본값 'yaml'). |
| DockerHubSearch | `web/src/components/DockerHubSearch.tsx` | Docker Hub 이미지 검색 자동완성. 디바운싱된 검색으로 드롭다운에 결과 표시 (이름, 설명, 별점, 공식 여부). Props: `value`, `onChange`, `placeholder`. |
| DockerPrune | `web/src/components/DockerPrune.tsx` | Docker 리소스 정리 다이얼로그. 컨테이너/이미지/볼륨/네트워크 선택적 또는 전체 정리. 정리 결과(삭제 수, 회수 용량) 토스트 표시. Props: `open`, `onOpenChange`. |

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

### 디스크 관리
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `checkSmartmontools()` | GET | `/disks/smartmontools-status` | `{ installed }` | smartmontools 설치 확인 |
| `installSmartmontools()` | POST | `/disks/install-smartmontools` | `{ message, output }` | smartmontools 설치 |
| `getDiskOverview()` | GET | `/disks/overview` | `any` | 디스크 개요 |
| `getDiskSmart(device)` | GET | `/disks/{device}/smart` | `SmartInfo` | SMART 정보 |
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
| `getAppStoreAppDetail(id)` | GET | `/appstore/apps/{id}` | `{ app: AppStoreApp; compose: string; installed: boolean }` | 앱 상세 + Compose YAML |
| `installAppStoreApp(id, env)` | POST | `/appstore/apps/{id}/install` | `{ message: string; id: string; output: string }` | 앱 설치 |
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

모든 WebSocket 연결은 `?token={JWT}` 쿼리 파라미터로 인증.

---

## 디자인 패턴 요약

- **코드 분할**: 모든 페이지는 `React.lazy()` + `<Suspense>`로 동적 임포트. `PageLoader` 컴포넌트가 로딩 폴백.
- **카드 스타일**: `bg-card rounded-2xl p-5/p-6 card-shadow` (shadcn/ui Card 미사용, 직접 Tailwind 클래스)
- **배지 스타일**: 인라인 `span` + `px-2 py-0.5 rounded-full text-[11px] font-medium` + 상태별 색상
- **색상 체계**: Primary blue(`#3182f6`), Green(`#00c471`), Red(`#f04452`), Yellow(`#f59e0b`), Purple(`#8b5cf6`)
- **폰트 크기**: 11px(보조), 13px(본문), 15px(서브타이틀), 22px(페이지 제목)
- **다이얼로그 패턴**: 확인 다이얼로그는 항상 취소/확인 버튼, 위험 작업은 `variant="destructive"`
- **에러 처리**: try/catch + toast.error, 에러 메시지는 err.message 또는 i18n 번역 키
- **로딩 상태**: 개별 상태 변수 관리, 버튼에 `disabled={loading}` + 스피너 아이콘
- **공유 유틸리티**: `formatBytes()`는 `web/src/lib/utils.ts`에서 공유 (각 페이지에서 중복 정의하지 않음)
- **Docker 탭 네비게이션**: shadcn/ui Tabs 대신 React Router NavLink + `<Outlet />` 패턴으로 URL 기반 서브라우트
- **사이드바**: 접기/펼치기 토글 지원, 상태 localStorage 영속화
