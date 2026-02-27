# SFPanel 프론트엔드 스펙

## 개요

- **프레임워크**: React 18 + TypeScript + Vite
- **스타일**: Tailwind CSS v4 + shadcn/ui (일부 컴포넌트) + Toss 디자인 시스템 영향 (컬러, 라운딩, 그림자)
- **상태 관리**: React hooks (useState, useEffect, useCallback, useRef)
- **라우팅**: React Router v6 (BrowserRouter)
- **국제화**: react-i18next + i18next-browser-languagedetector (한국어/영어)
- **토스트 알림**: Sonner (via shadcn/ui 래퍼)
- **코드 에디터**: Monaco Editor (@monaco-editor/react)
- **터미널**: xterm.js (@xterm/xterm + fit/web-links/search 애드온)
- **차트**: Recharts (LineChart)
- **아이콘**: Lucide React
- **엔트리포인트**: `web/src/main.tsx` -> `<App />`
- **CSS**: `web/src/index.css` (Tailwind 설정)

---

## 라우팅

`App.tsx`에서 정의. `SetupGuard`가 최상위에서 초기 셋업 여부를 체크하고, `ProtectedRoute`가 JWT 토큰 기반 인증을 검증한다.

| 경로 | 컴포넌트 | 인증 필요 | 레이아웃 | 설명 |
|------|----------|-----------|----------|------|
| `/login` | Login | X | 없음 (독립) | 관리자 로그인 |
| `/setup` | Setup | X | 없음 (독립) | 초기 관리자 계정 생성 (첫 실행 시) |
| `/` | - | O | Layout | `/dashboard`로 리다이렉트 |
| `/dashboard` | Dashboard | O | Layout | 시스템 대시보드 (실시간 메트릭) |
| `/docker` | Docker | O | Layout | Docker 관리 (탭 구조: 컨테이너/이미지/볼륨/네트워크/Compose) |
| `/files` | Files | O | Layout | 파일 관리자 |
| `/cron` | CronJobs | O | Layout | 크론 작업 관리 |
| `/logs` | Logs | O | Layout | 시스템 로그 뷰어 |
| `/processes` | Processes | O | Layout | 프로세스 관리자 |
| `/packages` | Packages | O | Layout | 시스템 패키지 관리 + Docker 설치 |
| `/firewall` | Firewall | O | Layout | 방화벽 관리 (UFW + Fail2ban) |
| `/terminal` | Terminal | O | Layout | 웹 터미널 (멀티 탭) |
| `/settings` | Settings | O | Layout | 계정/시스템 설정 |

### 라우트 가드

- **SetupGuard**: 모든 라우트를 감싸고, `/setup` 경로가 아닌 경우 `api.getSetupStatus()`를 호출하여 `setup_required === true`이면 `/setup`으로 리다이렉트
- **ProtectedRoute**: `api.isAuthenticated()` (localStorage 토큰 존재 여부)를 체크하여, 미인증 시 `/login`으로 리다이렉트

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
- **로컬 함수**: formatUptime(), formatBytes()

### Docker
- **파일**: `web/src/pages/Docker.tsx`
- **기능**: Docker 관리 탭 컨테이너. shadcn/ui Tabs를 사용하여 5개 서브페이지를 탭으로 구성.
- **탭 구조**:
  - containers (기본값) -> DockerContainers
  - images -> DockerImages
  - volumes -> DockerVolumes
  - networks -> DockerNetworks
  - compose -> DockerCompose
- **사용 컴포넌트**: Tabs, TabsList, TabsTrigger, TabsContent (shadcn/ui)

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
- **로컬 함수**: formatPorts(), formatContainerName(), timeAgo(), formatBytes(), statusBadge()

### Docker > DockerImages
- **파일**: `web/src/pages/docker/DockerImages.tsx`
- **기능**: Docker 이미지 목록 관리
  - 이미지 수 표시
  - 이미지 테이블: RepoTag, ID(짧은), 크기, 생성일
  - 이미지 풀 다이얼로그 (기본값 "nginx:latest")
  - 이미지 삭제 확인 다이얼로그
- **사용 API**: `api.getImages()`, `api.pullImage()`, `api.removeImage()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)
- **로컬 함수**: formatSize(), shortId(), formatDate()

### Docker > DockerVolumes
- **파일**: `web/src/pages/docker/DockerVolumes.tsx`
- **기능**: Docker 볼륨 관리
  - 볼륨 수 표시
  - 볼륨 테이블: 이름, 드라이버, 마운트포인트, 생성일
  - 볼륨 생성 다이얼로그
  - 볼륨 삭제 확인 다이얼로그
- **사용 API**: `api.getVolumes()`, `api.createVolume()`, `api.removeVolume()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)

### Docker > DockerNetworks
- **파일**: `web/src/pages/docker/DockerNetworks.tsx`
- **기능**: Docker 네트워크 관리
  - 네트워크 수 표시
  - 네트워크 테이블: 이름, ID(짧은), 드라이버, 범위
  - 기본 네트워크(bridge/host/none) 삭제 방지
  - 네트워크 생성 다이얼로그 (이름 + 드라이버 선택: bridge/overlay/host)
  - 네트워크 삭제 확인 다이얼로그
- **사용 API**: `api.getNetworks()`, `api.createNetwork()`, `api.removeNetwork()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label (shadcn/ui)

### Docker > DockerCompose
- **파일**: `web/src/pages/docker/DockerCompose.tsx`
- **기능**: Docker Compose 프로젝트 관리
  - 프로젝트 수 표시
  - 프로젝트 테이블: 이름, 상태 배지, 생성일
  - 프로젝트별 액션: Up, Down, 편집, 삭제
  - 프로젝트 생성 다이얼로그 (이름 + Monaco YAML 에디터)
  - 프로젝트 편집 다이얼로그 (Monaco YAML 에디터)
  - 프로젝트 삭제 확인 다이얼로그
  - 기본 YAML 템플릿 제공 (nginx:latest 예시)
- **사용 API**: `api.getComposeProjects()`, `api.createComposeProject()`, `api.getComposeProject()`, `api.updateComposeProject()`, `api.deleteComposeProject()`, `api.composeUp()`, `api.composeDown()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label, ComposeEditor (shadcn/ui + 커스텀)

### Files
- **파일**: `web/src/pages/Files.tsx`
- **기능**: 서버 파일 관리자
  - 브레드크럼 경로 네비게이션 (클릭 시 경로 직접 입력 가능)
  - 파일/폴더 테이블: 이름(아이콘 구분), 크기, 수정일, 권한
  - 디렉토리 우선 정렬 (알파벳순)
  - 파일 클릭 시 Monaco 에디터로 편집 (언어 자동 감지: 30+ 확장자 지원)
  - 새 파일 생성, 새 폴더 생성, 파일 업로드 (FormData), 다운로드
  - 이름 변경, 삭제 확인 다이얼로그
  - 도구 모음: 새로고침, 새 파일, 새 폴더, 업로드
- **사용 API**: `api.listFiles()`, `api.readFile()`, `api.writeFile()`, `api.createDir()`, `api.deletePath()`, `api.renamePath()`, `api.uploadFile()`
- **사용 컴포넌트**: Table, Dialog, Button, Input, Label, Monaco Editor (shadcn/ui + @monaco-editor/react)
- **로컬 함수**: formatFileSize(), formatDate(), getLanguageFromFilename(), joinPath()
- **로컬 인터페이스**: FileEntry

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
  - 좌측 사이드바: 로그 소스 목록 (이름, 경로, 크기, 존재 여부)
  - 줄 수 선택: 100 / 500 / 1000 / 5000
  - 실시간 스트리밍 모드 (WebSocket 직접 관리, useWebSocket 훅 미사용)
  - 자동 스크롤 토글
  - 로그 검색: Ctrl+F, 매치 하이라이팅, 이전/다음 매치 네비게이션
  - 로그 레벨별 색상 구분 (error/warn/info/debug - 좌측 테두리 + 텍스트 색상)
  - 줄 번호 표시
  - 로그 다운로드 (Blob -> 링크 클릭)
  - 로그 지우기
  - 연결 상태 표시 (실시간 모드)
- **사용 API**: `api.getLogSources()`, `api.readLog(source, lines)`, `api.getToken()`
- **WebSocket**: 직접 관리 (`/ws/logs?source={source}&token={token}`)
- **사용 컴포넌트**: Button, Input (shadcn/ui)
- **로컬 함수**: formatFileSize(), highlightText(), getLogLevel()
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
- **로컬 함수**: formatBytes(), getStatusStyle(), statusLabel()
- **로컬 인터페이스**: ProcessInfo

### Packages
- **파일**: `web/src/pages/Packages.tsx`
- **기능**: 시스템 패키지 관리
  - Docker 상태 카드: 설치 여부, 버전, 실행 상태, Compose 가용성 표시. 미설치 시 Docker 설치 버튼 (SSE 스트리밍 출력)
  - 시스템 업데이트: 업데이트 확인, 전체/선택 업그레이드, 패키지 체크박스 선택
  - 패키지 검색/설치: 검색 결과에서 설치/제거 가능
  - 작업 출력 다이얼로그: 설치/업그레이드/제거 진행 상황 실시간 표시
- **사용 API**: `api.getDockerStatus()`, `api.installDocker()` (SSE 스트리밍), `api.checkUpdates()`, `api.upgradePackages()`, `api.installPackage()`, `api.removePackage()`, `api.searchPackages()`
- **사용 컴포넌트**: Table, Dialog, Button, Input (shadcn/ui)
- **로컬 인터페이스**: PackageInfo, SearchResult, DockerStatus, LoadingState, OutputDialog

### Firewall
- **파일**: `web/src/pages/Firewall.tsx`
- **기능**: 방화벽(UFW) 및 Fail2ban 침입 방지 시스템 관리
  - 탭 구조: UFW Rules, Open Ports, Fail2ban
  - **UFW Rules 탭** (`FirewallRules`): UFW 활성화/비활성화 토글, 규칙 목록 테이블 (번호/대상/동작/소스/코멘트/IPv6), 규칙 추가 다이얼로그 (action/port/protocol/from/to/comment), 규칙 삭제 확인 다이얼로그
  - **Open Ports 탭** (`FirewallPorts`): 리스닝 TCP/UDP 포트 목록 테이블 (프로토콜/주소/포트/PID/프로세스), 선택한 포트로 UFW 규칙 직접 추가 기능
  - **Fail2ban 탭** (`FirewallFail2ban`): Fail2ban 설치 상태 확인 및 원클릭 설치, jail 목록 테이블 (이름/활성/차단수/총차단수), jail 상세 (설정값, 차단 IP 목록), jail 활성화/비활성화, IP 차단 해제
- **탭 구성**:
  - rules (기본값) -> FirewallRules
  - ports -> FirewallPorts
  - fail2ban -> FirewallFail2ban
- **사용 API**: `api.getFirewallStatus()`, `api.enableFirewall()`, `api.disableFirewall()`, `api.getFirewallRules()`, `api.addFirewallRule()`, `api.deleteFirewallRule()`, `api.getListeningPorts()`, `api.getFail2banStatus()`, `api.installFail2ban()`, `api.getFail2banJails()`, `api.getFail2banJailDetail()`, `api.enableFail2banJail()`, `api.disableFail2banJail()`, `api.unbanFail2banIP()`
- **사용 컴포넌트**: Tabs, TabsList, TabsTrigger, TabsContent, Table, Dialog, Button, Input, Select (shadcn/ui)
- **서브 컴포넌트 파일**:
  - `web/src/pages/firewall/FirewallRules.tsx` — UFW 규칙 관리
  - `web/src/pages/firewall/FirewallPorts.tsx` — 리스닝 포트 조회
  - `web/src/pages/firewall/FirewallFail2ban.tsx` — Fail2ban 관리
- **네비게이션**: Shield 아이콘, Disk와 Packages 사이에 배치

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
- **로컬 함수**: generateTabId(), loadTabs(), saveTabs(), loadActiveTab(), saveActiveTab(), loadFontSize(), saveFontSize()
- **로컬 인터페이스**: Tab

### Settings
- **파일**: `web/src/pages/Settings.tsx`
- **기능**: 계정 및 시스템 설정
  - 언어 변경 (English / 한국어) - i18n.changeLanguage()
  - 터미널 타임아웃 설정 (분 단위, 0 = 무제한)
  - 비밀번호 변경 (현재 비밀번호 + 새 비밀번호 + 확인)
  - 2FA 관리: 설정 시작 -> QR 코드(외부 API로 생성) + 시크릿 키 표시 -> 6자리 코드 인증
  - 시스템 정보 표시 (버전, 호스트명, OS, 커널, 가동시간)
- **사용 API**: `api.getSettings()`, `api.updateSettings()`, `api.changePassword()`, `api.setup2FA()`, `api.verify2FA()`, `api.getSystemInfo()`
- **사용 컴포넌트**: Button, Input, Label (shadcn/ui)

---

## 공용 컴포넌트

| 컴포넌트 | 파일 | 용도 |
|----------|------|------|
| Layout | `web/src/components/Layout.tsx` | 인증된 페이지의 공통 레이아웃. 좌측 사이드바(네비게이션 12항목 + 로그아웃) + 우측 메인 콘텐츠(Outlet). NavLink로 활성 상태 표시. |
| MetricsCard | `web/src/components/MetricsCard.tsx` | 메트릭 표시 카드. 아이콘 + 제목 + 값 + 프로그레스 바(80% 초과 빨강, 60% 초과 노랑, 그 외 파랑). |
| MetricsChart | `web/src/components/MetricsChart.tsx` | CPU/메모리 시계열 차트. Recharts LineChart 사용. CPU(파랑) + Memory(초록) 이중 라인. Y축 0-100%, 애니메이션 비활성화. |
| ContainerShell | `web/src/components/ContainerShell.tsx` | Docker 컨테이너 셸 접속. xterm.js + WebSocket(`/ws/docker/containers/{id}/exec`). 키 입력 전송, 리사이즈 이벤트 전송. |
| ContainerLogs | `web/src/components/ContainerLogs.tsx` | Docker 컨테이너 로그 스트리밍. xterm.js(읽기 전용) + WebSocket(`/ws/docker/containers/{id}/logs`). 검색(SearchAddon), 로그 다운로드 기능. |
| ComposeEditor | `web/src/components/ComposeEditor.tsx` | Docker Compose YAML 에디터. Monaco Editor 래퍼. 높이 400px, YAML 언어, vs-dark 테마, 미니맵 비활성화. |

---

## shadcn/ui 컴포넌트

| 컴포넌트 | 파일 | 용도 |
|----------|------|------|
| Button | `web/src/components/ui/button.tsx` | 범용 버튼 (variant: default/outline/ghost/destructive, size: default/sm/icon-xs/icon-sm/xs/lg) |
| Card | `web/src/components/ui/card.tsx` | 카드 컨테이너 (현재 직접 사용하지 않고 Tailwind 클래스로 카드 스타일 구현) |
| Input | `web/src/components/ui/input.tsx` | 텍스트 입력 필드 |
| Label | `web/src/components/ui/label.tsx` | 폼 라벨 |
| Table | `web/src/components/ui/table.tsx` | 데이터 테이블 (Table, TableHeader, TableBody, TableRow, TableHead, TableCell) |
| Badge | `web/src/components/ui/badge.tsx` | 배지 (현재 인라인 스타일로 배지 구현하여 직접 사용은 제한적) |
| Dialog | `web/src/components/ui/dialog.tsx` | 모달 다이얼로그 (Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter) |
| DropdownMenu | `web/src/components/ui/dropdown-menu.tsx` | 드롭다운 메뉴 (등록되어 있으나 현재 페이지에서 미사용) |
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
| `getSystemInfo()` | GET | `/system/info` | `{ host: any; metrics: any }` | 호스트 정보 + 메트릭 |
| `getTopProcesses()` | GET | `/system/processes` | `Array<ProcessInfo>` | 상위 프로세스 목록 |
| `getMetricsHistory()` | GET | `/system/metrics-history` | `Array<{ time, cpu, mem_percent }>` | 24시간 메트릭 히스토리 |
| `listProcesses(query?, sort?)` | GET | `/system/processes/list` | `{ processes: ProcessInfo[]; total: number }` | 프로세스 목록 (검색/정렬) |
| `killProcess(pid, signal?)` | POST | `/system/processes/{pid}/kill` | - | 프로세스 종료 |

### Docker 컨테이너
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getContainers()` | GET | `/docker/containers` | `any[]` | 컨테이너 목록 |
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

### Docker Compose
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getComposeProjects()` | GET | `/docker/compose` | `any[]` | 프로젝트 목록 |
| `createComposeProject(name, yaml)` | POST | `/docker/compose` | - | 프로젝트 생성 |
| `getComposeProject(project)` | GET | `/docker/compose/{project}` | `{ project: any; yaml: string }` | 프로젝트 상세 |
| `updateComposeProject(project, yaml)` | PUT | `/docker/compose/{project}` | - | 프로젝트 수정 |
| `deleteComposeProject(project)` | DELETE | `/docker/compose/{project}` | - | 프로젝트 삭제 |
| `composeUp(project)` | POST | `/docker/compose/{project}/up` | - | 프로젝트 시작 |
| `composeDown(project)` | POST | `/docker/compose/{project}/down` | - | 프로젝트 중지 |

### 파일 관리자
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `listFiles(path)` | GET | `/files?path=` | `any[]` | 파일/폴더 목록 |
| `readFile(path)` | GET | `/files/read?path=` | `{ content: string; size: number }` | 파일 읽기 |
| `writeFile(path, content)` | POST | `/files/write` | - | 파일 쓰기 |
| `createDir(path)` | POST | `/files/mkdir` | - | 디렉토리 생성 |
| `deletePath(path)` | DELETE | `/files?path=` | - | 파일/폴더 삭제 |
| `renamePath(oldPath, newPath)` | POST | `/files/rename` | - | 이름 변경 |
| `uploadFile(destPath, file)` | POST | `/files/upload` | - | 파일 업로드 (FormData, Content-Type 미설정) |

### 로그
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getLogSources()` | GET | `/logs/sources` | `any[]` | 로그 소스 목록 |
| `readLog(source, lines?)` | GET | `/logs/read?source=&lines=` | `{ source, lines[], total_lines }` | 로그 읽기 |

### 크론 작업
| 메서드 | HTTP | 경로 | 반환 타입 | 설명 |
|--------|------|------|-----------|------|
| `getCronJobs()` | GET | `/cron` | `any[]` | 크론 작업 목록 |
| `createCronJob(schedule, command)` | POST | `/cron` | - | 작업 생성 |
| `updateCronJob(id, schedule, command, enabled)` | PUT | `/cron/{id}` | - | 작업 수정 |
| `deleteCronJob(id)` | DELETE | `/cron/{id}` | - | 작업 삭제 |

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
| `getFail2banJails()` | GET | `/fail2ban/jails` | `{ jails: Fail2banJail[], total }` | jail 목록 |
| `getFail2banJailDetail(name)` | GET | `/fail2ban/jails/{name}` | `Fail2banJail` | jail 상세 정보 |
| `enableFail2banJail(name)` | POST | `/fail2ban/jails/{name}/enable` | `{ message }` | jail 활성화 |
| `disableFail2banJail(name)` | POST | `/fail2ban/jails/{name}/disable` | `{ message }` | jail 비활성화 |
| `unbanFail2banIP(jail, ip)` | POST | `/fail2ban/jails/{jail}/unban` | `{ message }` | IP 차단 해제 |

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
  Status: string; Ports: ContainerPort[]; Created: number
}
interface ContainerPort { PrivatePort: number; PublicPort: number; Type: string }
interface DockerImage { Id: string; RepoTags: string[]; Size: number; Created: number }
interface DockerVolume { Name: string; Driver: string; Mountpoint: string; CreatedAt: string }
interface DockerNetwork { Id: string; Name: string; Driver: string; Scope: string }
interface ComposeProject { id: number; name: string; yaml_path: string; status: string; created_at: string }
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

---

## 유틸리티

**파일**: `web/src/lib/utils.ts`

| 함수 | 용도 |
|------|------|
| `cn(...inputs: ClassValue[]): string` | Tailwind CSS 클래스 병합 유틸리티 (clsx + tailwind-merge) |

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
| `layout` | 레이아웃/네비게이션 | brand, tagline, nav.dashboard/docker/files/cron/logs/processes/packages/sites/terminal/settings, logout |
| `login` | 로그인 페이지 | title, subtitle, username, password, totpCode, signIn, signingIn, totpRequired |
| `setup` | 초기 셋업 | subtitle, username, password, confirmPassword, createAdmin, passwordMinLength, passwordMismatch |
| `dashboard` | 대시보드 | title, subtitle, live, disconnected, hostInfo, hostname, os, platform, kernel, uptime, cpuCores, cpuUsage, memory, disk, network, chartTitle, topProcesses, dockerSummary, recentLogs, quickActions, sent, received |
| `processes` | 프로세스 관리 | title, subtitle, total, searchPlaceholder, sortBy, sort_cpu/memory/pid/name, kill, killTitle, killConfirm, killDescription, cpuUsage, memUsage, swapUsage, running/sleeping/zombie/stopped/idle |
| `docker` | Docker 메인 | title, tabs.containers/images/volumes/networks/compose |
| `docker.containers` | 컨테이너 관리 | count, total, running, stopped, name, image, status, ports, resources, terminal, stop, start, restart, logs, shell, inspect, memory, generalInfo, command, workingDir, hostname, portBindings, volumes, networkInfo, envVars, searchPlaceholder, stopTitle/restartTitle/deleteTitle + Confirm |
| `docker.images` | 이미지 관리 | count, repoTag, imageId, size, pullImage, pullDescription, imageReference, pulling, pull, deleteTitle/Confirm |
| `docker.volumes` | 볼륨 관리 | count, driver, mountpoint, createVolume, createDescription, volumeName, deleteTitle/Confirm |
| `docker.networks` | 네트워크 관리 | count, id, driver, scope, createNetwork, createDescription, networkName, cannotDeletePredefined, deleteTitle/Confirm |
| `docker.compose` | Compose 관리 | count, newProject, createTitle, createDescription, projectName, composeFile, up, down, editTitle, editDescription, deleteTitle/Confirm |
| `sites` | 사이트 관리 | title, count, domain, docRoot, php, ssl, newSite, createTitle, enableSSL, sslDescription, editConfig |
| `settings` | 설정 페이지 | title, subtitle, language, languageDescription, changePassword, currentPassword, newPassword, confirmNewPassword, twoFA, twoFAEnabled/NotConfigured, enable2FA, scanQR, secretKey, verificationCode, systemInfo, version, terminal, terminalTimeout |
| `siteConfig` | 사이트 설정 에디터 | loadingConfig, configSaved, fetchFailed, saveFailed, saveConfig |
| `terminal` | 터미널 | connectingLogs, connectingShell, connected, wsError, connectionClosed, notAuthenticated, newTab, noTabs, fontSmaller, fontLarger, search, searchPlaceholder, prev, next |
| `files` | 파일 관리자 | title, count, name, size, modified, permissions, empty, loading, newFile, newFolder, upload, edit, editFile, download, rename, renameTitle, deleteTitle, deleteConfirm |
| `cron` | 크론 작업 | title, count, showAll, newJob, tableTitle, schedule, command, type, presets, presetEveryMinute/Hour/Daily/Weekly/Monthly, createTitle, editTitle, deleteTitle |
| `logs` | 로그 뷰어 | title, subtitle, sources, lines, live, autoScroll, refresh, clear, search, searchPlaceholder, totalLines, linesShown, connected, disconnected, download |
| `packages` | 패키지 관리 | title, subtitle, dockerStatus, dockerDescription, installDocker, systemUpdates, checkForUpdates, upgradeAll, searchAndInstall, search, install, remove, operationComplete, operationRunning |
| `firewall` | 방화벽 관리 | title, tabs.rules/ports/fail2ban, status, enable, disable, rules, addRule, deleteRule, ports, action, port, protocol, from, to, comment, listeningPorts |
| `firewall.fail2ban` | Fail2ban | status, install, jails, enable, disable, unban, bannedIPs, maxRetry, banTime, findTime |

---

## WebSocket 엔드포인트 정리

| 경로 | 용도 | 사용 페이지 |
|------|------|------------|
| `/ws/metrics` | 시스템 메트릭 실시간 스트리밍 (Metrics JSON) | Dashboard, Processes |
| `/ws/logs?source={source}` | 로그 실시간 스트리밍 | Logs |
| `/ws/terminal?session_id={id}` | 서버 셸 세션 (바이너리 + JSON resize) | Terminal |
| `/ws/docker/containers/{id}/exec` | 컨테이너 셸 접속 | DockerContainers (ContainerShell) |
| `/ws/docker/containers/{id}/logs` | 컨테이너 로그 스트리밍 | DockerContainers (ContainerLogs) |

모든 WebSocket 연결은 `?token={JWT}` 쿼리 파라미터로 인증.

---

## 디자인 패턴 요약

- **카드 스타일**: `bg-card rounded-2xl p-5/p-6 card-shadow` (shadcn/ui Card 미사용, 직접 Tailwind 클래스)
- **배지 스타일**: 인라인 `span` + `px-2 py-0.5 rounded-full text-[11px] font-medium` + 상태별 색상
- **색상 체계**: Primary blue(`#3182f6`), Green(`#00c471`), Red(`#f04452`), Yellow(`#f59e0b`), Purple(`#8b5cf6`)
- **폰트 크기**: 11px(보조), 13px(본문), 15px(서브타이틀), 22px(페이지 제목)
- **다이얼로그 패턴**: 확인 다이얼로그는 항상 취소/확인 버튼, 위험 작업은 `variant="destructive"`
- **에러 처리**: try/catch + toast.error, 에러 메시지는 err.message 또는 i18n 번역 키
- **로딩 상태**: 개별 상태 변수 관리, 버튼에 `disabled={loading}` + 스피너 아이콘
