# SFPanel 기술 스택 & 기능 스펙

> 마지막 전체 동기화: 2026-04-19 · 기능 워크스루 보강: 2026-06-03 (캠페인 v0.19.0–v0.40.0 반영) · 기준 버전: v0.40.0 · 근거: `docs/superpowers/research/2026-04-19-docs-overhaul/features-inventory.md`, `CHANGELOG.md`
>
> 경량 서버 관리 웹 패널. 개인 서버 관리자 및 DevOps 팀을 위한 Docker 중심 관리 도구.
> 올인원 Go 바이너리 아키텍처 — React SPA를 `go:embed`로 포함하여 단일 실행 파일로 배포.
>
> 아래 **기능 목록**(§1–§22)은 v0.40.0까지의 모듈 심화 캠페인을 반영합니다. 단, 본 문서 하단의 표 섹션(데이터베이스 스키마, API/WS/SSE 엔드포인트 목록, 프론트엔드 페이지 등)은 아직 v0.9.0 시점 기준이며 캠페인에서 추가된 라우트/테이블/페이지를 일부 누락합니다. 변경 요약은 `CHANGELOG.md`를, 설계 의도는 `docs/superpowers/specs/`의 테마별 디자인 문서를 참조하세요.

---

## 기술 스택

### 백엔드

| 기술 | 버전 | 용도 |
|------|------|------|
| Go | 1.24.0 | 서버 런타임, 올인원 바이너리 빌드 |
| Echo | v4.15.1 (`labstack/echo/v4`) | HTTP 웹 프레임워크 (라우팅, 미들웨어, CORS) |
| SQLite | v1.46.1 (`modernc.org/sqlite`) | 설정/세션/Compose 프로젝트 저장 (CGO-free 순수 Go 구현) |
| Docker Go SDK | v27.5.1 (`docker/docker`) | Docker 소켓 직접 통신 (컨테이너/이미지/볼륨/네트워크 관리) |
| golang-jwt/jwt | v5.3.1 | JWT 토큰 생성 및 검증 (HS256) |
| pquerna/otp | v1.5.0 | TOTP 2단계 인증 시크릿 생성 및 코드 검증 |
| golang.org/x/crypto | v0.47.0 | bcrypt 패스워드 해싱 |
| gorilla/websocket | v1.5.3 | WebSocket 연결 (실시간 메트릭, 로그 스트리밍, 터미널, Docker exec) |
| gopsutil | v4.26.1 (`shirou/gopsutil/v4`) | 시스템 메트릭 수집 (CPU, 메모리, 디스크, 네트워크, 호스트 정보, 프로세스) |
| gopkg.in/yaml.v3 | v3.0.1 | YAML 설정 파일 파싱 |
| creack/pty | v1.1.24 | 서버 터미널 PTY (pseudo-terminal) 세션 생성 |
| hashicorp/raft | v1.7.3 | Raft 합의 알고리즘 (클러스터 리더 선출, 로그 복제) |
| raft-boltdb | v2.3.1 | Raft 로그/스냅샷 저장 (BoltDB 기반, 임베디드) |
| google.golang.org/grpc | v1.79.2 | 노드 간 gRPC 통신 (클러스터 제어 채널) |
| google.golang.org/protobuf | v1.36.11 | Protocol Buffers 직렬화 (gRPC 메시지 정의) |

### 프론트엔드

| 기술 | 버전 | 용도 |
|------|------|------|
| React | ^19.2.0 | UI 라이브러리 (함수형 컴포넌트 + hooks) |
| TypeScript | ~5.9.3 | 타입 안전성 |
| Vite | ^7.3.1 | 빌드 도구 및 개발 서버 (HMR) |
| Tailwind CSS | ^4.2.1 | 유틸리티-퍼스트 CSS 프레임워크 |
| shadcn/ui | ^3.8.5 (dev) | Radix UI 기반 재사용 컴포넌트 (Dialog, Table, Tabs, Button, Input 등) |
| Radix UI | ^1.4.3 | 접근성 기반 헤드리스 UI 프리미티브 |
| React Router DOM | ^7.13.1 | 클라이언트 사이드 라우팅 (SPA) |
| uplot | ^1.6.32 | 시스템 메트릭 시계열 차트 (CPU/메모리 24시간 히스토리) |
| xterm.js | ^6.0.0 (`@xterm/xterm`) | 웹 기반 터미널 에뮬레이터 |
| xterm Addons | fit ^0.11.0, search ^0.16.0, web-links ^0.12.0 | 터미널 자동 크기 조절, 검색, 링크 감지 |
| Monaco Editor | ^4.7.0 (`@monaco-editor/react`) | 파일 편집기 / Compose YAML 편집 (구문 강조, 자동 완성) |
| i18next | ^25.8.13 | 다국어 지원 프레임워크 |
| react-i18next | ^16.5.4 | React용 i18n 바인딩 |
| i18next-browser-languagedetector | ^8.2.1 | 브라우저 언어 자동 감지 |
| Lucide React | ^0.575.0 | 아이콘 라이브러리 |
| Sonner | ^2.0.7 | 토스트 알림 |
| class-variance-authority | ^0.7.1 | 컴포넌트 변형(variant) 관리 |
| clsx / tailwind-merge | ^2.1.1 / ^3.5.0 | 조건부 클래스명 결합 |

### 인프라 / 배포

| 기술 | 용도 |
|------|------|
| `go:embed` | React SPA (`web/dist`)를 Go 바이너리에 임베딩 — 단일 실행 파일 배포 |
| GoReleaser v2 | 크로스 컴파일 빌드 및 GitHub 릴리즈 자동화 (linux/amd64, linux/arm64) |
| GitHub Actions | CI/CD — 태그 푸시 시 자동 릴리즈 워크플로우 |
| systemd | 프로덕션 서비스 관리 (자동 시작, 재시작, 보안 하드닝) |
| Bash 설치 스크립트 | 원클릭 설치/업그레이드/삭제 (`scripts/install.sh`) |

### 프론트엔드 최적화

| 기법 | 구현 |
|------|------|
| **코드 스플리팅** | `React.lazy()` + `Suspense` — 모든 페이지 컴포넌트(41개)를 지연 로딩. 초기 번들 크기 감소 및 라우트별 온디맨드 로딩 |
| **공유 유틸리티** | `formatBytes()` 함수를 `web/src/lib/utils.ts`에 추출하여 Dashboard, Docker, Files, Network 등에서 재사용 |
| **SetupGuard 캐싱** | 초기 설정 상태 확인 API(`/auth/setup-status`)를 모듈 레벨 변수로 캐싱 — 매 라우트 전환마다 API 호출하지 않고 한 번만 확인 |
| **버전 정보 서버 제공** | `DashboardHandler.Version` 필드로 빌드 시 주입된 버전을 `/api/v1/system/info` 응답에 포함 (`version` 키) |

---

## 기능 목록

### 1. 시스템 대시보드

- **설명**: 노드 단위 서버 상태를 한눈에 파악하는 실시간 모니터링 대시보드. 클러스터 모드에서는 좌측 트리에서 선택한 노드 범위로 렌더되고, `?node=` 프록시를 통해 원격 노드 메트릭도 동일 UI로 표시된다.
- **데이터 로딩**: 초기 진입 시 단일 통합 엔드포인트 `GET /api/v1/system/overview`로 호스트 정보 + 현재 메트릭 + 히스토리 + 버전 + 업데이트 정보를 한 번에 수신(`DashboardOverview`). 서버는 host/metrics와 history를 goroutine 2개로 병렬 수집하고, 한 소스가 실패해도 500 대신 부분 데이터 + null 필드로 응답한다(대시보드가 장애의 첫 단서이므로 빈 화면보다 부분 표시 우선; UI가 null을 가드).
- **주요 기능**:
  - 실시간 CPU/메모리/디스크/네트워크 메트릭 카드 (`/ws/metrics` WebSocket, 2초 간격; 우상단 연결 상태 Live/Disconnected 배지)
  - CPU/메모리 사용률 시계열 차트 (**1/4/12/24시간** 범위 토글 — `range` 쿼리로 서버에서 윈도우 조회)
  - 호스트 정보 (호스트명, OS, 플랫폼+버전, 커널, 업타임, CPU 코어 수, **주 IP 주소** — `getNetworkInterfaces()`로 기본 게이트웨이 인터페이스 IP)
  - 메모리 카드에 Swap 사용량 (스왑 비활성 시 "비활성" 처리)
  - 네트워크 I/O 실시간 송/수신 속도 + 누적 전송량
  - Docker 컨테이너 요약 (실행 중/중지/전체 카운트 + 최근 컨테이너 상태; "전체 보기" → `/docker`)
  - Top 프로세스 (CPU 사용률 기준, 10초 간격 자동 갱신)
  - 최근 시스템 로그 — **syslog / 방화벽 로그 탭 토글** (각 8 / 50줄; 클릭 시 해당 로그 페이지로 이동)
  - **업데이트 배너**: `update_info.latest_version`가 현재 버전보다 높으면 상단 배너 표시 → `/settings?scope=node&tab=system`로 이동
  - 빠른 액션 바로가기 (파일, Docker, 패키지, Cron, 로그)
- **관련 엔드포인트**: `GET /system/overview`(통합), `GET /system/info`·`GET /system/metrics-history`(개별, 하위호환 유지), top 프로세스/컨테이너/로그 조회, `/ws/metrics`(클러스터 래핑 WS)
- **관련 기술**: gopsutil v4, gorilla/websocket, uPlot, Echo WebSocket, Raft 클러스터 프록시(`?node=`)

### 2. Docker 관리

- **설명**: Docker 리소스의 전체 생명주기를 웹 UI에서 관리. 5개 탭(스택/컨테이너/이미지/볼륨/네트워크) + 리소스 정리.
- **주요 기능**:
  - **컨테이너**: 목록 조회 (전체/실행 중/중지)와 목록 내 **CPU/메모리 스파크라인**(ContainerSparkline), 상세 검사 (포트/환경변수/마운트/네트워크), 시작/중지/재시작/삭제, 실시간 CPU/메모리 통계, **컨테이너 관측성 탭**(ContainerHistoryTab) — CPU/메모리 **히스토리 차트**(1h/6h/24h, `GET /containers/:id/metrics`)와 **수명주기 이벤트 타임라인**(die/oom/restart/health 등, `GET /containers/:id/events`), 실시간 로그 스트리밍 (WebSocket), 컨테이너 내부 셸 접속 (WebSocket + exec, TTY 리사이즈), **컨테이너 생성** (이미지/이름/포트/볼륨/환경변수/네트워크/명령어/재시작 정책), **헬스체크 컴포저**(HealthcheckComposerDialog로 healthcheck 설정을 생성·삽입)
  - **이미지**: 로컬 이미지 목록, 이미지 풀(SSE 진행률 스트리밍), 삭제(강제), **Docker Hub 검색**(설명/스타/공식 여부), **이미지 업데이트 확인**(`checkImageUpdates` — 실행 중 컨테이너가 쓰는 이미지의 레지스트리 최신 여부를 병렬 조회, 이미지별 타임아웃)
  - **볼륨**: 목록, 생성, 삭제(강제), **볼륨 사용량 카드**(DockerVolumeUsageCard — 볼륨별 디스크 점유; 디스크 페이지에서도 노출)
  - **네트워크**: 목록, 생성(드라이버 선택: bridge 기본), 삭제, 상세 검사
  - **Docker Compose (Stacks)**: 설정 가능한 스택 루트(`server.stacks_path`, 기본 `/opt/stacks`) 디스크 스캔 기반 프로젝트 관리, Monaco 에디터 YAML 편집, `.env` 편집, `up -d`/`down`(이미지·볼륨 동시 삭제 옵션), 프로젝트 상태, **서비스별 제어**(시작/중지/재시작)와 로그, **스택 업데이트 확인·적용**(`POST /compose/:project/check-updates` → `…/update`/`…/update-stream` SSE)
    - **노드 간 스택 마이그레이션**(클러스터 전용): 대상 노드(`targetNodeId`)로의 콜드 마이그레이션 + 원본 처분(`disposition`: retain/delete/clone). 정의·`.env`·볼륨·바인드 데이터·이미지(save/load)를 콜드 패키징해 이전. **사전 점검**(arch/디스크/포트/덮어쓰기)과 **헬스 게이트**를 통과해야 적용하며, 덮어쓰기 시 정의/볼륨을 백업하고 실패 시 원복. 전송 진행률은 SSE로 스트리밍, **전송 속도 제한**(v0.51.0) 지원
  - **리소스 정리(Prune)**: 컨테이너/이미지/볼륨/네트워크 개별 + 전체 일괄
- **관련 기술**: Docker Go SDK, gorilla/websocket, xterm.js, Monaco Editor, uPlot(컨테이너 히스토리), SQLite `container_metrics_history`/`container_events`(마이그레이션 16·17)

### 3. 웹 터미널

- **설명**: 브라우저에서 서버 셸에 직접 접속하는 완전한 터미널 에뮬레이터
- **주요 기능**:
  - PTY (pseudo-terminal) 기반 실제 셸 세션 (/bin/bash 또는 /bin/sh)
  - 다중 탭 지원 (생성/닫기/이름 변경, localStorage 지속)
  - 세션 지속성 — 탭 전환/재연결 시 스크롤백 버퍼 재생 (256KB 링 버퍼)
  - **보존 세션 재연결(reattach)**: 서버는 연결 끊김 후에도 PTY 세션과 스크롤백을 유지하므로, `GET /terminal/sessions`로 살아 있는 세션 목록을 조회해 피커에서 기존 세션 id에 다시 붙을 수 있음 (이전에는 프론트엔드가 항상 새 세션 id를 생성해 기존 세션에 도달 불가)
  - 터미널 리사이즈 (PTY 동기화)
  - 폰트 크기 조절 (10~24px)
  - 터미널 내 텍스트 검색 (SearchAddon)
  - 웹 링크 자동 감지 및 클릭 (WebLinksAddon), Unicode11 폭 처리 (CJK)
  - Tokyo Night 컬러 테마, 클리어 버튼 (Ctrl-L 전송)
  - 모바일 특수키 바 (Esc/Tab/Ctrl/Alt 토글, 방향키, Ctrl+C/D/Z)
  - 유휴 세션 자동 정리 (설정 가능한 타임아웃, 기본 30분, 0=무제한) + **빈-리더 세션 5분 강제 회수** (탭 종료 후 PTY 누수 방지)
  - 최대 20 동시 세션, WS keepalive (30초 ping / 70초 read deadline), 사용자별 세션 키 바인딩, HOME 동적 해석(비-root 유닛 지원)
  - 키보드 단축키 (Ctrl+F 검색)
  - WS 인증: 단발성 ws-ticket(`POST /auth/ws-ticket`) 우선, 레거시 `?token=` fallback
- **관련 기술**: creack/pty, gorilla/websocket, xterm.js v6, xterm addons (Search/WebLinks/Unicode11)

### 4. 파일 관리자

- **설명**: 서버 파일시스템을 웹 UI에서 탐색하고 편집하는 파일 매니저
- **주요 기능**:
  - 디렉토리 탐색 (브레드크럼 네비게이션, 직접 경로 입력)
  - 파일 읽기 (최대 5MB, Monaco 에디터; 초과 시 다운로드로 안내), 쓰기 최대 10MB, 다운로드 최대 2GB
  - 파일 생성/쓰기/저장 (`.bak` 백업은 스트리밍 복사)
  - 디렉토리 생성 (중첩 경로 자동 생성)
  - 파일/디렉토리 삭제 (시스템 중요 경로 17개 보호: /, /etc, /usr, /bin, /sbin, /var, /boot, /proc, /sys, /dev, /home, /root, /lib, /lib64, /opt, /run, /srv)
  - **읽기 차단 경로(read-protected)**: /root/.ssh, 사용자 홈의 SSH 키/authorized_keys/known_hosts 등 → 403
  - **업로드 정책**: 웹 서빙 디렉토리(/var/www 등)에는 실행 확장자(.php/.sh/.cgi/.jsp 등) 및 .htaccess/.htpasswd/web.config 업로드 차단
  - 파일/디렉토리 이름 변경 (이동), 다운로드
  - **재귀 이름 검색** (`GET /files/search`): 현재 디렉토리 하위를 이름으로 재귀 탐색, 결과 수 상한 + 벽시계 데드라인으로 제한 (잘림 여부를 응답에 플래그)
  - **복사** (`POST /files/copy`): 파일/디렉토리 트리 복사 — 기존 파일 덮어쓰기·자기 자신으로의 복사·시스템 중요 경로 쓰기를 거부하고, 비정규 파일(심볼릭 링크 등)을 건너뛰어 심볼릭 링크를 통한 우회 차단
  - **다중 선택 삭제**: 행별 체크박스 + 일괄 삭제 (항목별 성공/실패 결과 요약)
  - 파일 업로드 (multipart, 상한은 설정값 `max_upload_size`)
  - 경로 유효성 검증 (절대 경로 필수, `filepath.Clean` 동등성으로 트래버설 차단, 심볼릭 링크 해석 재검증)
  - 우클릭 컨텍스트 메뉴 (행 + 빈 영역), 목록 정렬(디렉토리 우선), 타입별 아이콘, 권한/크기/수정시간 표시
- **관련 기술**: Go os 패키지, Monaco Editor, 30+ 프로그래밍 언어 구문 강조

### 5. 로그 뷰어

- **설명**: 시스템 및 애플리케이션 로그를 웹에서 조회하고 실시간 스트리밍하는 뷰어
- **주요 기능**:
  - 7개 사전 정의 로그 소스: System Log, Auth Log, Kernel Log, SFPanel, Package Manager (dpkg), Firewall (UFW/DOCKER-USER — kern.log grep 필터), Fail2ban
  - **커스텀 로그 소스 추가/삭제** (이름 + 파일 경로 지정, SQLite `custom_log_sources` 테이블에 저장)
  - **구조화된 파싱 뷰** (Firewall/Auth/Fail2ban/SFPanel — 원시/파싱 토글, 컬럼 테이블)
  - 줄 수 선택 (100, 500, 1000, 5000줄)
  - 실시간 스트리밍 모드 (WebSocket + `tail -F` — logrotate 자동 추적, Live 토글)
  - 로그 레벨 감지 및 색상 구분 (ERROR/FATAL=빨강, WARN=노랑, INFO=파랑, DEBUG=회색)
  - 로그 내 텍스트 검색 (하이라이트, 이전/다음 매치, Ctrl+F), 자동 스크롤 토글, 가상 스크롤
  - 로그 다운로드 (클라이언트 Blob), 줄 번호 표시
  - 커스텀 소스 경로 허용 목록: **`/var/log/` · `/opt/` 만** (세그먼트 경계 매칭 + 심볼릭 링크 해석 재검증; `/home`·`/tmp` 제외)
- **관련 기술**: tail 명령어, gorilla/websocket, WebSocket 기반 실시간 스트리밍

### 6. 프로세스 관리

- **설명**: 서버에서 실행 중인 모든 프로세스를 모니터링하고 제어
- **주요 기능**:
  - 전체 프로세스 목록 (PID, **PPID(부모 PID)**, 이름, 사용자, CPU%, 메모리%, **절대 RSS(상주 메모리)**, **nice 값**, 상태, 커맨드라인)
  - **프로세스 트리 뷰**: PPID 기반 부모→자식 트리 렌더 (사이클 가드 포함), 평면 목록과 토글
  - 프로세스 검색 (이름, 명령어, 사용자, PID로 필터링)
  - 정렬 (CPU, 메모리, PID, 이름)
  - 프로세스 종료 (SIGTERM, SIGKILL, SIGHUP, SIGINT 시그널 선택) + **작업 제어 시그널 STOP(일시정지)/CONT(재개)**
  - **renice**: 우선순위(nice) 변경 (−20..19로 클램프)
  - **sysguard 자기-보호**: 보호 PID(init/kthreadd/sfpanel) 및 패널이 직접 띄운 자식 프로세스(apt/docker compose/터미널 PTY 등, pgid 기준)에 대한 시그널 전송·renice 거부 (403, kill과 동일 가드)
  - 15초 간격 자동 갱신 (탭 비활성 시 일시정지), 대용량 목록 가상 스크롤
  - 실시간 시스템 리소스 요약 (CPU/메모리/Swap 사용률 바, `/ws/metrics`)
  - Top 프로세스 (대시보드용 별도 API `/system/processes`)
- **관련 기술**: gopsutil/v4 process, syscall 시그널, common/sysguard

### 7. Cron 작업 관리

- **설명**: 시스템 crontab을 웹 UI에서 관리
- **주요 기능**:
  - crontab 목록 조회 (작업, 환경변수, 주석 구분)
  - Cron 작업 생성 (스케줄 + 명령어)
  - Cron 작업 수정 (스케줄/명령어 변경)
  - Cron 작업 삭제
  - **지금 실행(run-now)** (`POST /cron/:id/run`): 스케줄과 무관하게 해당 작업의 명령어를 `sh -c`로 즉시 실행하고 출력을 캡처해 반환 (5분 타임아웃). 패널 권한(root)으로 cron이 사용할 동일 컨텍스트에서 실행되므로 새 권한 상승은 없고 테스트용 즉시 실행만 제공
  - 작업 활성화/비활성화 토글 (주석 처리 방식)
  - 스케줄 프리셋 (매분, 매시, 매일, 매주, 매월)
  - 스케줄 설명 자동 생성 ("Every 5 minutes", "@reboot" 등)
  - 스케줄 유효성 검증 (5-필드 형식 + @키워드, 클라이언트 사전 검증 + 개행 문자 거부)
  - 전체 타입 표시 모드 (env, comment 포함)
  - **낙관적 동시성 가드(`expected_raw`)**: 라인 인덱스 시프트로 다른 작업이 수정/삭제되는 것을 방지, 불일치 시 409 (`CRON_CONFLICT`)
  - crontab 미설치 노드: 목록은 빈 배열, 생성/수정/삭제는 503. `crontabMu`로 read-modify-write 직렬화
- **관련 기술**: crontab CLI (`crontab -l`, `crontab -`), 정규식 파싱

### 8. 패키지 관리

- **설명**: 시스템 패키지(apt) 업데이트 및 Docker 설치를 웹에서 관리
- **주요 기능**:
  - **Docker 상태 확인**: 설치 여부, 버전, 실행 상태, Docker Compose 사용 가능 여부
  - **Docker 원클릭 설치**: get.docker.com 스크립트 실행, SSE(Server-Sent Events)로 실시간 출력 스트리밍
  - **개발 도구 설치**: Node.js(NVM/LTS — 설치·버전 전환·삭제, LTS 원격 목록), Claude Code(claude.ai), Codex(`@openai/codex`), Gemini CLI(`@google/gemini-cli`) — 전부 SSE 스트리밍, Node 미설치 시 Codex/Gemini 버튼 비활성화
  - **시스템 업데이트 확인**: `apt list --upgradable` 파싱 (패키지명, 현재/신규 버전, 아키텍처)
  - **패키지 업그레이드**: 전체 또는 선택적 업그레이드 (`apt-get upgrade`, **SSE 스트리밍**)
  - **패키지 설치/제거**: 이름으로 설치/제거 (`apt-get install`/`remove`)
  - **패키지 검색**: `apt-cache search` 결과 표시 (최대 50건, 설치 상태 표시)
  - 패키지 이름 유효성 검증 (인젝션 방지), **dpkg 프런트엔드 락 사전 점검 → 점유 시 즉시 409**
  - 업스트림 설치 스크립트 SHA-256 핀(옵션, `SFPANEL_*_INSTALLER_SHA256` env; 기본 track-latest)
- **관련 기술**: apt/apt-get/apt-cache CLI, nvm/npm, SSE 스트리밍, 5분 명령 타임아웃

### 9. 네트워크 / VPN 관리

- **설명**: 서버 네트워크 인터페이스, DNS, 라우팅, 본딩 및 VPN 클라이언트(WireGuard, Tailscale)를 웹 UI에서 관리
- **주요 기능**:
  - **인터페이스**: 네트워크 인터페이스 목록 (이름/상태/IP/MAC/속도/MTU), 인터페이스 상세 정보, DHCP/Static 설정 변경, **물리/가상/루프백/Docker 인터페이스 분류** (Docker 그룹 접이식), 단일 집계 조회 `getNetworkStatus()`(interfaces+routes+dns)
  - **DNS**: DNS 서버 및 검색 도메인 조회
  - **라우팅**: 시스템 라우팅 테이블 조회
  - **본딩**: 네트워크 본드 목록, 생성 (모드/슬레이브/프라이머리 설정), 삭제
  - **Netplan**: 네트워크 설정 적용 (`netplan apply`)
  - **WireGuard VPN**: 설치 상태 확인 및 원클릭 설치, 인터페이스 목록/상세 (피어 정보 포함), 인터페이스 활성화/비활성화 (`wg-quick up/down`), 설정 파일 CRUD (생성/조회/수정/삭제), `.conf` 파일 업로드 지원, PrivateKey 마스킹
    - **피어 관리**: 원시 config 직접 편집 없이 UI에서 피어 추가/삭제. **키페어 생성**(`POST /network/wireguard/keypair`), **피어 추가**(`POST .../configs/:name/peers` — 검증된 `[Peer]` 블록을 config에 append, 인터페이스가 up이면 `wg set`으로 라이브 적용), **피어 삭제**(`DELETE .../configs/:name/peers?public_key=…` — base64 키에 `/`·`+`가 포함되므로 경로 세그먼트 대신 쿼리로 전달), **부팅 자동시작 토글**(`wg-quick@<name>` enable/disable). public key / preshared key / CIDR / endpoint 입력은 서버 측 검증, 중복 피어는 409 거부
    - **클라이언트 config + QR**: 피어 추가 시 클라이언트 키페어를 생성하고 클라이언트 config를 **브라우저 측에서 조립** — **서버는 클라이언트 PrivateKey를 절대 저장하지 않음**(표준 WireGuard 온보딩). 복사 가능한 텍스트 + 모바일 임포트용 **QR 코드**로 렌더. 서버 PublicKey/리슨 포트는 인터페이스가 down이어도 config의 PrivateKey/포트에서 파생하므로 터널 시작 전에도 유효한 클라이언트 config가 생성됨 (v0.39.0–v0.40.0)
  - **Tailscale VPN**: 설치 상태 확인 및 **SSE 스트리밍 설치 출력** (공식 install.sh), 연결/해제/로그아웃, Auth Key 입력 또는 브라우저 인증 URL 자동 오픈, 자기 노드 정보 (Hostname/IPv4·IPv6/OS/Tailnet/MagicDNS), 피어 목록 (호스트명/IP/OS/온라인/트래픽), Exit Node 선택/해제, **Accept Routes / Advertise Exit Node 토글**, 버전 확인 및 업데이트 체크
- **관련 기술**: Netplan CLI, ip/networkctl, wg/wg-quick, tailscale CLI

### 10. 디스크 관리

- **설명**: 서버 디스크, 파티션, 파일시스템, LVM, RAID, Swap을 웹 UI에서 관리
- **주요 기능**:
  - **디스크 개요**: 디스크 목록 (이름/크기/모델/시리얼), I/O 통계, 디스크 사용량 분석 (경로/깊이별)
  - **SMART 모니터링**: smartmontools 설치 상태 확인 및 원클릭 설치, 디스크별 SMART 정보 조회. **SMART 자기 검사 트리거**(`POST /disks/:device/smart/test`, type=`short`/`long` — smartctl이 ETA를 즉시 반환하고 검사는 드라이브에서 백그라운드 수행) + **자기 검사 로그 파싱·표시**(검사 종류/상태/통과·실패/실행 시점의 power-on 시간) — 이전 결과와 방금 완료한 결과를 함께 노출
  - **파티션**: 디스크별 파티션 목록, 파티션 생성 (시작/끝/파일시스템 타입), 파티션 삭제
  - **파일시스템**: 마운트된 파일시스템 목록, 파티션 포맷 (ext4/xfs/btrfs 등), 마운트/언마운트, 파일시스템 리사이즈, 확장 가능 여부 사전 검사 및 확장(expand-check/expand; LVM `/dev/mapper` 타깃 지원)
  - **LVM**: PV/VG/LV 목록 및 생성/삭제, LV 리사이즈
  - **RAID**: mdadm RAID 배열 목록 및 상세 정보, RAID 생성 (레벨/디바이스 선택), RAID 삭제, 디스크 추가/제거
  - **Swap**: 스왑 정보 조회, 스왑 파일/파티션 생성/삭제, swappiness 설정, 스왑 리사이즈 (안전성 사전 검사)
- **관련 기술**: lsblk, parted, mkfs, mount, lvm2, mdadm, smartmontools CLI

### 11. 방화벽 관리

- **설명**: UFW 방화벽, Fail2ban 침입 방지 시스템, Docker 방화벽을 웹 UI에서 관리
- **주요 기능**:
  - **UFW 방화벽**: 활성화/비활성화 토글, 규칙 목록 조회 (번호/대상/동작/소스/코멘트/IPv6 구분), 규칙 추가 (action/port/protocol/from/to/comment), 규칙 삭제
  - **리스닝 포트**: `ss` 명령어로 TCP/UDP 리스닝 포트 조회 (프로토콜/주소/포트/PID/프로세스), 포트에서 직접 UFW 규칙 추가 지원
  - **포트 맵**: 호스트 포트별로 UFW 규칙 + Docker DNAT 컨테이너 + 호스트 프로세스를 단일 표로 통합 조회 (`GET /system/portmap`), 상태(listening/bound) 및 외부 노출 위험 하이라이트
  - **Fail2ban**: 설치 상태 확인 및 원클릭 설치, **jail 템플릿** (SSH 등 사전 정의), jail 생성/삭제(설정은 원자적 temp+rename 기록)/목록·상태/활성화·비활성화/설정 변경 (maxretry/bantime/findtime)/차단 IP 해제 (unban)
  - **Docker 방화벽**: DOCKER-USER 체인 규칙 관리 (iptables; 동시 편집 직렬화), Docker 발행 포트 표 + DNAT 매핑
  - **방화벽 로그**: UFW + Docker-USER, Fail2ban 로그 뷰어 (공유 로그 소스)
  - **잠금 방지 가드**: UFW 활성화/규칙 추가/규칙 삭제 시 SSH(22) 또는 패널 포트 접근이 차단되는 변경을 사전 감지하여 HTTP 409로 거부 (`?force=true`로 우회; 비활성 상태에선 `ufw show added`로 스테이징 규칙까지 검사)
  - 입력값 검증: 포트 번호/범위, IP/CIDR 주소, 프로토콜, action, jail 이름 등 서버 측 정규식 검증
- **관련 기술**: UFW CLI (`ufw`), Fail2ban CLI (`fail2ban-client`), iptables, ss 명령어

### 12. Systemd 서비스 관리

- **설명**: 시스템 서비스(systemd unit)를 웹 UI에서 모니터링하고 제어
- **주요 기능**:
  - 서비스 목록 조회 (이름/active_state·sub_state/enabled(enabled·disabled·static·masked)/설명), 검색 + 상태 필터(all/running/failed/inactive)
  - 서비스 시작/중지/재시작, 활성화/비활성화 (부팅 시 자동 시작)
  - 서비스 로그 조회 (journalctl) + 의존성 정보 (required_by/requires/wanted_by) 동시 표시
  - **보호 유닛 가드**: sfpanel/dbus/systemd-journald 등 보호 유닛의 중지·재시작·비활성화 거부 (403, sysguard)
  - 15초 간격 자동 갱신 (탭 비활성 시 일시정지)
- **관련 기술**: systemctl CLI, journalctl, common/sysguard

### 14. 앱스토어 (App Store)

- **설명**: GitHub 레포 기반 원클릭 Docker Compose 앱 설치
- **주요 기능**:
  - 앱 카테고리별 탐색 (모니터링, 보안, 미디어, 클라우드, 개발, 인프라 등)
  - 앱 검색 (이름, 설명 기반)
  - 원클릭 설치: Compose YAML 자동 생성 + 환경변수 폼 + `docker compose up -d`
  - 동적 환경변수 설정 (포트, 비밀번호 등 앱별 커스텀)
  - 자동 비밀번호 생성 (`crypto/rand`, 32바이트 hex)
  - 캐시 영속화: SQLite `settings.appstore_cache` (1시간 TTL, 5개 동시 HTTP 요청 갱신) — 재시작 후 GitHub 재조회 없이 복원
  - 설치된 앱은 Docker Compose Stacks에서 관리 가능. 설치 경로 `<stacks_path>/<app-id>/`
  - 설치 모드: 심플 모드 (환경변수 폼) / **고급 모드** (docker-compose.yml + .env 직접 편집)
  - **고급 모드 보안**: 설치 시 비밀번호 재인증(bcrypt) + 위험 compose(privileged/pid:host/hostfs/docker.sock) 차단 (도난 JWT만으로 호스트 루트 탈취 방지), 1MB 요청 바디 캡
  - 앱 상세: README 렌더링(브랜치 자동 탐색) + 포트 사용 현황/대체 포트 제안
  - 포트/컨테이너 이름 충돌 자동 감지(409) 및 대체 포트 제안, 설치 레이스 가드(atomic mkdir) + 실패 시 자동 롤백(compose down -v)
  - SSE 기반 설치 진행률 스트리밍 (prepare/fetch → pull → start → done)
- **관련 기술**: GitHub Raw API, Docker Compose, crypto/rand, bcrypt, net/http, SSE 스트리밍

### 15. 클러스터 관리

- **설명**: Proxmox 스타일 대칭 클러스터. 2~32대 노드를 하나의 클러스터로 통합 관리
- **주요 기능**:
  - **Raft 합의 엔진**: `hashicorp/raft` + BoltDB 스토어 (임베디드, 외부 의존 없음). FSM이 JWT 시크릿, 클러스터 이름, 노드 목록(역할/주소/상태), 어드민 계정을 복제
  - **gRPC + mTLS**: 노드 간 제어 채널 (포트 3629), TLS 1.3 상호 인증, ECDSA P-256 자체 CA, 노드 인증서 자동 발급
  - **참가 토큰**: HMAC-SHA256 서명, 1회용, 시간제한 (기본 24시간, 메모리 저장으로 리더 재시작 시 소실). **대기 중 토큰 목록·취소**(`GET/DELETE /cluster/tokens`) — 목록은 마스킹된 값 + 짧은 핑거프린트로 리댁트(전체 토큰 미반환)하여, 잘못 발급된 초대를 사용 전에 무효화 가능
  - **하트비트 모니터링**: 2초 간격, 3단계 상태 판정 (online → suspect → offline)
  - **JoinEngine 파이프라인** (`internal/cluster/join.go`): 리더→조인노드 파이프라인을 `PreFlight` / `Execute` 두 단계로 분리
    - PreFlight: TCP 연결 → `TokenManager.Peek()`(소비 없이 검증) → 포트 확인 → IP 자동 감지 → 예상 실패 사유 사전 반환
    - Execute: 6단계 원자적 조인 (Join RPC → CA/노드 인증서 저장 → Config 업데이트 → Config 원자 저장 → DB 어드민 동기화 → `LiveActivate` 콜백으로 Manager+gRPC 서버 시작). 각 단계 실패 시 롤백 경로 명시
  - **Zero-Restart 라이프사이클**: `LiveActivate` 콜백 (`main.go`에서 주입)으로 Raft/gRPC를 프로세스 재시작 없이 활성화. 기존 `existingMgr` 파라미터로 Raft 셧다운/재시작 레이스 회피. 탈퇴/해산만 바이너리 재시작 필요
  - **IP 자동 감지** (`internal/cluster/detect.go`): Tailscale(100.64.0.0/10), 동일 서브넷 매칭, TCP 다이얼 기반 감지, 리더 주소 기반 라우팅 힌트
  - **클러스터 업데이트**: 롤링/동시 모드로 전체 클러스터 SFPanel 업데이트 오케스트레이션 (SSE 진행률 스트리밍, 노드별 step+status 이벤트). UI는 평면 로그 대신 **노드별 스테퍼 + 전체 진행 바**(완료/총 노드, 실패 카운트)로 렌더 — 각 노드의 지역화된 단계 상태(업데이트 중/재시작 대기/리더십 이전/온라인 복귀/느린 재시작/건너뜀/실패)를 백엔드가 이미 내보내던 구조화 SSE에서 도출
  - **노드 주소 편집·탈퇴 (인라인)**: 노드 행에서 광고 주소(advertise address) 인라인 편집(API + gRPC), 로컬 노드 행에서 클러스터 탈퇴 — 정족수 손실 시 force 오버라이드 지원
  - **CLI 명령어**: `sfpanel cluster init/join/leave/status/token/remove`
  - **웹 UI API**: 클러스터 초기화/참여/탈퇴/해산/업데이트 REST API (~15개 엔드포인트)
- **패키지 레이아웃**: `internal/cluster/` — `manager.go`, `raft_fsm.go`, `grpc_server.go`, `join.go`, `detect.go`, `tls.go`, `token.go`, `ws_relay.go` 외 다수. proto는 `proto/cluster.proto`, 생성물 `proto/cluster.pb.go` / `proto/cluster_grpc.pb.go`
- **설정 확장**: `config.yaml`에 `cluster` 섹션 (enabled, name, node_id, node_name, grpc_port, data_dir, cert_dir, advertise_address, raft_tls)
- **REST 프록시 미들웨어**: `ClusterProxyMiddleware` — `?node=X` 쿼리 파라미터로 원격 노드에 요청 투명 전달. 일반 요청은 gRPC `ProxyRequest`(30초 타임아웃), 스트리밍(SSE/`-stream` 접미사 경로)은 HTTP 직접 릴레이(5분 타임아웃)
- **WebSocket 릴레이** (`internal/cluster/ws_relay.go`): `WrapEchoWSHandler`로 터미널/로그/메트릭/Docker exec 등 모든 WS를 원격 노드로 양방향 포워딩. 메시지 타입(바이너리/텍스트) 보존, 한쪽 종료 시 전파
- **내부 프록시 인증**: CA 인증서 SHA-256 해시 기반, `X-SFPanel-Internal-Proxy` 헤더 상수시간 비교 (JWT 비의존). `X-SFPanel-Original-User` 헤더로 원본 사용자 전파
- **동시성 보호**: `Manager.joiningMu`(Init/Join 중복 방지), `Config.configMu`(Cluster 필드 보호), `Handler.mu` RWMutex(Manager 포인터 동기화), `Handler.OnManagerActivated` 콜백(다른 핸들러가 Manager 활성화 시 갱신)
- **클러스터 UI (좌측 트리)**: 클러스터 모드에서 표준 사이드바를 2단 트리로 대체 (`ClusterSidebar` = `TreePanel` + `ContextMenu`). TreePanel은 데이터센터(클러스터) 루트 + 노드 목록(상태 점/리더 왕관/local 태그, 로컬 우선 정렬)을, ContextMenu는 선택 범위에 따라 메뉴를 렌더 — **데이터센터 범위**: 개요/노드/토큰/설정, **노드 범위**: 해당 노드의 전체 모듈 메뉴. 선택·접힘 상태 localStorage 지속. 노드 선택 시 `api.setCurrentNode`로 `?node=` 전역 주입, 데이터센터 선택 시 해제. (기존 `NodeSelector`는 비클러스터 폴백 전용)
- **데이터센터 개요** (`/cluster/overview`): 노드별 메트릭 집계 (평균 CPU/메모리/디스크, 총 컨테이너, online/total), 팔로워 stale 응답 시 경고 배너, 최근 이벤트, 리더 전용 롤링/동시 업데이트 버튼(SSE 진행률). 미초기화 시 `ClusterInitForm`(init/join) 렌더
  - **WebSocket 푸시** (`/ws/cluster/overview`, v0.31.0): 세 HTTP 엔드포인트를 15초마다 폴링하는 대신, **상태 + 개요 + 최근 이벤트** 스냅샷을 소켓으로 수신. 노드당 **공유 샘플러 1개**가 로컬(Raft 복제) FSM + 이벤트 버스에서 5초마다 스냅샷을 재구성해 열려 있는 모든 대시보드로 팬아웃 — 탭별 리더 RPC 없음. 팔로워는 복제된 뷰를 `stale` 플래그로 제공(UI가 배너 렌더). 페이지는 즉시 첫 페인트용 마운트 시점 fetch 1회만 유지하고 이후 소켓으로 라이브 업데이트
- **클러스터 업데이트 가드**: 동시 모드는 정족수를 깨는 경우(투표자 ≥2) 사전 거부, 롤링은 첫 실패 시 중단. 리더는 자기 업데이트 전에 리더십을 온라인 팔로워로 이전
- **Graceful 누락 처리**: 원격 노드에 ufw/crontab/rsyslog 미설치 시 500 대신 빈 결과 반환
- **알려진 제약**:
  - 토큰은 메모리에만 저장 (리더 재시작 시 기존 토큰 소실)
  - 비클러스터 → 클러스터 마이그레이션 경로 없음 (Init/Join 신규만 지원)
  - TLS: CA 10년 / 노드 인증서 5년 TTL, 만료 자동 감시는 없음. 노드 인증서는 `sfpanel cluster reissue-cert`로 무중단 재발급 가능, CA 회전은 전 노드 동시 재시작 필요
  - 네트워크 분할 시 Raft 안전성만 보장 (분할 뇌 자체는 막지 않음)
- **설계 문서**: `docs/superpowers/specs/2026-04-13-cluster-join-redesign.md` (조인 재설계), `docs/superpowers/research/2026-04-19-docs-overhaul/cluster-inventory.md` (인벤토리)
- **관련 기술**: hashicorp/raft, raft-boltdb, gRPC, protobuf, crypto/x509

### 16. 시스템 튜닝

- **설명**: 커널 매개변수(sysctl)를 웹 UI에서 조회하고 권장값으로 적용하는 시스템 성능 최적화 도구
- **주요 기능**:
  - **튜닝 상태 조회**: 네트워크/메모리/파일시스템/보안 4개 기본 카테고리 + conntrack 모듈 탑재 시 conntrack 카테고리 추가, 각 sysctl 매개변수의 현재값 vs 동적 권장값 비교
  - **동적 권장값 계산**: CPU 코어 수, RAM 용량에 따라 버퍼 크기/백로그/swappiness 등 자동 조정
  - **카테고리별 적용**: 네트워크(BBR, TCP 버퍼, 커넥션 백로그), 메모리(swappiness, dirty ratio, cache pressure), 파일시스템(file-max, inotify), 보안(SYN 쿠키, RP 필터, ICMP 제한) 선택 적용
  - **안전한 적용/확인/롤백 워크플로우**: 적용 후 60초 내 확인하지 않으면 자동 롤백 (이전 sysctl 값 + 설정 파일 복원)
  - **설정 초기화**: SFPanel 튜닝 설정 파일 제거 및 시스템 기본값 복원 (`sysctl --system`)
  - 설정 파일: `/etc/sysctl.d/99-sfpanel-tuning.conf`
- **관련 기술**: sysctl CLI, gopsutil (시스템 정보), os 파일 I/O

### 17. 감사 로그

- **설명**: 모든 상태 변경 API 요청(POST, PUT, DELETE)을 자동으로 기록하는 감사 추적 시스템
- **주요 기능**:
  - **자동 기록**: AuditMiddleware가 모든 상태 변경 요청을 비동기로 `audit_logs` 테이블에 기록
  - **기록 항목**: 사용자명, HTTP 메서드, 경로, 응답 상태 코드, 클라이언트 IP, 노드 ID, **보호 플래그(protected)**, 생성 시각
  - **클러스터 지원**: `?node=X` 원격 요청 시 노드 ID 자동 추적
  - **감사 로그 조회**: 페이지네이션 (page/limit, 기본 50건, 최대 100건)
  - **범위 삭제 + Tombstone**: `?days=N`(N일 이전) 또는 `?before=ISO8601|YYYY-MM-DD` 선택 삭제(둘 다 지정 시 400). 삭제 행위를 먼저 `protected=1` 행으로 기록(수행자/IP/노드/건수, 단일 트랜잭션) → `DELETE … WHERE protected=0`으로 보호·tombstone 행은 면역 (공격자가 흔적 제거 불가). tombstone node_id는 로컬 노드 ID 스탬프
  - **로그인 실패 기록**: 비밀번호 오류뿐 아니라 존재하지 않는 계정 시도(unknown_user)도 기록(자격증명 스프레이 추적). 비밀번호는 절대 미기록
  - **보안 예외**: 로그인/셋업 엔드포인트 본문은 기록 제외
- **관련 기술**: Echo 미들웨어, SQLite, 비동기 AsyncWriter(큐 full 시 드롭+경고)

### 18. 시스템 백업/복원

- **설명**: SFPanel 설정 데이터를 백업하고 복원하는 재해 복구 기능
- **주요 기능**:
  - **백업 구성**: `sfpanel.db` + `config.yaml` + 스택 루트(`stacks_path`, 기본 `/opt/stacks`) 하위 각 프로젝트의 compose 파일(docker-compose.yml/compose.yaml/.env)을 `.tar.gz`로 다운로드. **컨테이너 데이터/볼륨은 미포함**
  - **예약 로컬 백업**(v0.26.0): 반복 로컬 백업 활성화 (간격 단위 시간, 보존 개수) — 타임스탬프가 찍힌 `tar.gz` 아카이브(DB + config + compose 파일)를 DB 옆 `backups/` 디렉토리에 기록하고 보존 한도까지 자동 정리(prune). 백그라운드 러너(`StartBackupScheduler`)가 **10분마다** 점검해 도래 시 실행하며 마지막 실행 시각/상태/오류를 기록(`backup_schedule` 테이블, 마이그레이션 33–34). 엔드포인트: `GET/PUT /system/backup/schedule`, `POST /system/backup/schedule/run`(지금 실행), 개별 아카이브 다운로드/삭제(이름 패턴 검증으로 트래버설 차단). 유지보수 탭에 스케줄 폼·run-now·아카이브 목록(다운로드/삭제) 노출
  - **백업 파일 복원**: tar.gz 업로드 → DB/설정/Compose 파일 복원, 기존 파일 자동 .bak 백업
  - **필수 파일 검증**: sfpanel.db 미포함 시 복원 거부
  - **서비스 자동 재시작**: 복원 완료 후 재시작을 `Commander`로 동기 실행, 실패 시 self-exit하여 supervisor가 새 DB로 재기동 (stale DB 핸들로 계속 서빙 방지)
  - **클러스터 경고**: 클러스터 모드에서 백업/복원은 FSM 복제 상태(admin·jwt_secret·cluster_node)와 desync 위험 → UI에서 추가 확인 후 진행
- **관련 기술**: archive/tar, compress/gzip, multipart 업로드, systemctl

### 19. AI 도구 설치

- **설명**: AI 코딩 어시스턴트 CLI 도구 설치 상태 확인 및 원클릭 설치
- **주요 기능**:
  - **Claude CLI**: 설치 상태/버전 확인, 공식 install.sh로 원클릭 설치 (SSE 실시간 출력)
  - **Codex CLI**: 설치 상태/버전 확인, npm 글로벌 설치 (`@openai/codex`, SSE 스트리밍)
  - **Gemini CLI**: 설치 상태/버전 확인, npm 글로벌 설치 (`@google/gemini-cli`, SSE 스트리밍)
  - **Node.js 버전 관리**: NVM 기반 설치된 버전 목록, 버전 전환, 신규 버전 설치, 버전 삭제
  - **원격 LTS 조회**: NVM을 통해 사용 가능한 LTS 버전 목록 제공
- **관련 기술**: NVM, npm, curl, SSE 스트리밍, exec.Command

### 20. Tauri 데스크톱 클라이언트

- **설명**: 크로스플랫폼 데스크톱 앱으로 원격 SFPanel 서버에 접속하여 관리
- **주요 기능**:
  - **서버 접속**: URL 입력 → `GET /api/v1/health`(5초) 검증 → `sfpanel_server_url` 저장 후 `/login` 이동
  - **연결 진단**: 환경(Tauri/Web)/대상/요청·응답·본문을 로깅하고 CORS·버전·타임아웃·주소 힌트를 분류 출력
  - **언어 선택**: Connect 페이지에서 한국어/영어 전환
  - **TauriGuard**: 서버 URL 미설정 시 자동으로 /connect 페이지로 리다이렉트
  - **크로스플랫폼**: macOS, Windows, Linux 지원 (Tauri v2)
- **관련 기술**: Tauri v2, tauri-plugin-http, Rust, WebView

### 21. 알림 시스템

- **설명**: 시스템 리소스 임계치 기반 자동 알림 발송. `internal/feature/alert/` 하위 `handler.go`(REST CRUD) + `manager.go`(백그라운드 평가) + `channels/discord.go`, `channels/telegram.go`(채널 구현).
- **주요 기능**:
  - **규칙 타입**: 호스트 임계치형 `cpu`/`memory`/`disk` (threshold %) + 컨테이너형 `container_down`/`container_oom`/`container_restart_loop`/`container_unhealthy` (JSON condition: 패턴·count·window 초) + 이벤트형 `service`/`login`/`package`
  - **알림 규칙** (`alert_rules` 테이블): 호스트형 조건 JSON `{"operator":">","threshold":90}` (연산자 `>`/`>=`/`<`/`<=`), 쿨다운(기본 300초, 동일 규칙 재발송 억제 — 원자적 예약으로 동시 중복 발송 차단)
  - **알림 채널** (`alert_channels` 테이블): Discord (`{"webhook_url":"..."}`), Telegram (`{"bot_token":"...","chat_id":"..."}`), **Webhook**(`{"webhook_url":"..."}`) — Slack/Mattermost incoming webhook 호환 `text` 필드 + 구조화 알림 필드(title/message/severity/timestamp)를 JSON 본문으로 POST하므로 Slack incoming webhook이나 임의의 커스텀 수신기와 동작. 홈랩 수신기가 LAN에 있는 설계 특성상 임의의 http(s) 대상을 허용(URL 형식만 검증), 전송은 공유 10초 클라이언트로 바운드. 채널 `enabled` 플래그로 토글
  - **클러스터 지원**: `node_scope` = `all` 또는 `specific`, `node_scope=specific` 시 `node_ids` JSON 배열로 대상 노드 지정
  - **심각도 수준**: info, warning, critical (`severity` 컬럼)
  - **알림 이력** (`alert_history` 테이블): 발송 시 INSERT (실제 전송 성공 채널 ID 배열만 `sent_channels`에 저장). 규칙 삭제 후에도 보존 (`rule_name` 스냅샷). 자동 정리 없음, 관리자가 UI에서 수동 삭제
  - **평가 주기**: `manager.go`가 60초 ticker로 `WHERE enabled=1` 규칙 평가 + 컨테이너 이벤트(`internal/monitor/docker_events.go`) 구독. 발송은 **bounded 비동기 워커 큐**로 분리 — 느린 webhook이 평가 ticker나 docker 이벤트 리스너를 막지 않음
  - **채널 테스트**: 채널 생성/편집 후 테스트 알림 발송 엔드포인트 제공
- **관련 기술**: net/http (Discord/Telegram/Webhook 전송), database/sql, 고루틴 (백그라운드 평가)
- **설계 문서**: `docs/superpowers/specs/2026-04-07-alert-system-design.md`

### 22. 설정

- **설명**: 패널/계정/시스템 설정을 묶은 **6-탭 허브** (일반/보안/시스템/튜닝/알림/감사). 단일 노드는 전체 탭, 클러스터에서는 스코프로 분기.
- **주요 기능**:
  - **일반**: 영어/한국어 전환 (i18next, 클라이언트 전용; Tauri는 연결 서버 해제 블록)
  - **보안**: 비밀번호 변경(최소 8자, 확인 일치); 2FA(TOTP) — 설정 시작 → **QR(qrcode.react 클라이언트 렌더링)** + 시크릿 → 6자리 검증 → 활성화, **비활성화(비밀번호 + 현재 TOTP 코드 재확인, `DELETE /auth/2fa`)** — 세션만 탈취한 공격자가 2FA를 다운그레이드하지 못하도록 현재 TOTP 코드를 요구하며, 비활성화 시 복구 코드도 함께 폐기(2FA 없이는 무의미하므로 재활성화 시 새 출발)
    - **2FA 복구 코드**(v0.34.0): 인증기를 분실해도 로그인할 수 있는 1회용 코드 세트 생성(`POST /auth/2fa/recovery`, 2FA 활성 상태 필수; 평문은 생성 시 1회만 표시). 로그인 화면에서 "복구 코드로 로그인" 시 TOTP 필드가 복구 코드 필드로 전환되고 유효 코드는 사용 시 소비됨. 코드는 **SHA-256 해시**로 저장(고엔트로피라 bcrypt 불필요, 로그인 경로 빠른 비교)되고, 클러스터 어드민의 경우 **Raft FSM으로 복제**(마이그레이션 35 + `CmdSetRecoveryCodes`) — 계정 레코드와 분리되어 비밀번호/TOTP 변경이 복구 코드를 지우지 않음. 로컬 계정은 `admin.recovery_codes` 컬럼에 저장. 클러스터 어드민 코드 소비는 리더 쓰기이며, 팔로워에서의 복구 로그인은 재사용 가능 코드를 위험에 빠뜨리지 않도록 "리더 노드 사용" 힌트와 함께 거부
  - **시스템**: 패널 업데이트(체크 + SSE 스트리밍 실행), 백업 다운로드/복원·**예약 백업**(§18), 시스템 정보(버전/호스트/OS/커널/업타임)
    - **업데이트 무결성 검증**: 릴리즈의 `checksums.txt`를 **Cosign keyless(Sigstore) 서명**으로 먼저 검증(인증서가 정식 레포의 `release.yml` 워크플로우/태그에 핀)한 뒤, 거기서 파싱한 **SHA-256** 해시로 다운로드 아카이브를 대조. `SignatureRequiredSince` 컷오프 이후 릴리즈는 `.sig`/`.pem` 자산이 없으면 업데이트를 거부(공격자가 서명 자산을 삭제해 SHA-only로 다운그레이드하는 공급망 공격 방지). 컷오프 이전 구버전은 1회성 업그레이드 경로를 위해 SHA-256 단독 검증으로 폴백
  - **튜닝**: 터미널 유휴 타임아웃(분), 최대 업로드 크기(MB) — per-node `settings`; sysctl 튜닝(적용/확인/리셋, 60초 자동 롤백)
  - **알림**: 채널(Discord/Telegram/Webhook) CRUD + 테스트, 규칙 CRUD, 이력 조회/삭제
  - **감사**: 감사 로그 조회/삭제 (§17)
  - **클러스터 스코프 분기**: `scope=node` 시 per-node 탭(시스템·튜닝·감사)만, 그 외 클러스터 전역 탭(일반·보안·알림)만 노출. 보안(비밀번호/2FA)은 FSM 복제 admin 행에 반영
  - 키-값 설정 저장 (SQLite `settings` 테이블, UPSERT)
- **관련 기술**: i18next, bcrypt, TOTP (pquerna/otp), SQLite

### 23. 공통 UX (cross-cutting)

캠페인(v0.30.0–v0.37.0)에서 여러 모듈에 걸쳐 일관되게 적용된 UX 개선. 특정 기능이 아니라 패널 전반의 상호작용 품질에 영향.

- **스타일 확인/입력 다이얼로그**: 모든 네이티브 `window.confirm()`을 온브랜드 확인 다이얼로그로 교체(`useConfirm()` 훅 + 앱 레벨 `ConfirmProvider`, 클러스터·디스크·도커·파일·알림·보안·백업·튜닝·감사·WireGuard·compose 헬스체크 컴포저 등 ~22 호출부; 파괴적 액션은 빨간 확인 버튼). 남아 있던 `window.prompt()`(2FA 비활성화 비밀번호, 앱스토어 고급 설치 재인증)도 마스킹 비밀번호 필드를 갖춘 `usePrompt()` 다이얼로그 + `PromptProvider`로 교체
- **type-to-confirm 게이팅**: 비가역 작업은 파괴 버튼 활성화 전에 정확한 디바이스/배열/클러스터 이름을 직접 타이핑해야 함 — **디스크 포맷**, **파티션 삭제**, **RAID 배열 삭제**, **클러스터 해산(disband)**에 적용. 오클릭으로 데이터 손실 불가
- **로딩 스켈레톤 + 명시적 에러 상태**: 컨테이너/서비스/프로세스/cron/방화벽 규칙/도커 이미지·볼륨·네트워크 목록 페이지가 **로딩**(스켈레톤 플레이스홀더), **로드 실패**(메시지 + Retry 버튼의 인라인 에러 블록), **실제 빈 상태**(빈 상태)를 구분 — 이전에는 삼켜진 fetch 오류가 빈 목록과 동일하게 보였음. 스켈레톤은 최초 로드에만 표시(백그라운드 갱신 시 기존 행 위로 깜빡이지 않음)
- **모바일 카드 폴백**: 넓은 데이터 테이블(**감사 로그**, **알림 이력**, **방화벽 포트맵**)이 작은 화면에서 가로 오버플로 대신 라벨/값 **카드 목록**으로 접힘(`hidden md:*`; 데스크톱 테이블은 동일한 필터/페이지네이션 행을 동일 배지·링크·액션으로 렌더)

---

## 인증 & 보안

| 항목 | 구현 |
|------|------|
| **인증 방식** | JWT 토큰 기반 (HS256, `Authorization: Bearer <token>` 헤더) |
| **토큰 생성** | `golang-jwt/jwt/v5` — username, 발급/만료 시간 클레임 포함 |
| **토큰 만료** | 설정 가능 (기본 24시간, `config.yaml`의 `token_expiry`) |
| **비밀번호 해싱** | bcrypt (golang.org/x/crypto, DefaultCost) |
| **2단계 인증** | TOTP (pquerna/otp) — Google Authenticator 등 호환, QR 코드 지원. **복구 코드**(SHA-256 해시 1회용, 클러스터는 Raft FSM 복제) 로그인 대체 경로 |
| **WebSocket 인증** | 쿼리 파라미터 `?token=<JWT>` 방식 (HTTP 헤더 불가능한 환경 대응) |
| **JWT 미들웨어** | Echo 미들웨어로 보호 라우트 그룹 인증 처리 |
| **초기 설정** | 셋업 위저드 — admin 계정 미존재 시 공개 엔드포인트로 최초 계정 생성 |
| **비밀번호 정책** | 최소 8자 이상 필수 |
| **파일 시스템 보호** | 절대 경로 필수, `..` 트래버설 차단, 시스템 핵심 경로 삭제 금지 |
| **패키지 이름 검증** | 정규식으로 안전한 문자만 허용 (`a-zA-Z0-9._+-`) |
| **로그 접근 제어** | 허용 목록(allowlist) 기반 — 사전 정의된 로그 소스 및 커스텀 소스만 읽기 가능 |
| **서비스 하드닝** | systemd: `ProtectSystem=full`, `NoNewPrivileges`, `LimitNOFILE=65536` |
| **설정 파일 보안** | 설치 시 `chmod 600` 적용 (config.yaml) |

---

## 설정

### 설정 파일

- **형식**: YAML
- **경로**: `config.yaml` (기본) 또는 명령줄 인수로 지정 (예: `/etc/sfpanel/config.yaml`)
- **동작**: 설정 파일이 없으면 기본값으로 실행

### 주요 설정 항목

| 섹션 | 키 | 기본값 | 설명 |
|------|-----|--------|------|
| `server.host` | host | `0.0.0.0` | 바인딩 호스트 주소 |
| `server.port` | port | `3628` | 서버 포트 |
| `database.path` | path | `./sfpanel.db` | SQLite 데이터베이스 파일 경로 |
| `auth.jwt_secret` | jwt_secret | (없음) | JWT 서명 시크릿 (반드시 변경 필요) |
| `auth.token_expiry` | token_expiry | `24h` | JWT 토큰 만료 시간 (Go duration 형식) |
| `docker.socket` | socket | `unix:///var/run/docker.sock` | Docker 소켓 경로 |
| `log.level` | level | `info` | 로그 레벨 (debug, info, warn, error) |
| `log.file` | file | (없음) | 로그 파일 경로 |
| `cluster.enabled` | enabled | `false` | 클러스터 모드 활성화 |
| `cluster.name` | name | (없음) | 클러스터 이름 |
| `cluster.node_id` | node_id | (없음) | 노드 UUID (자동 생성) |
| `cluster.node_name` | node_name | (없음) | 노드 표시 이름 |
| `cluster.grpc_port` | grpc_port | `3629` | gRPC 통신 포트 |
| `cluster.data_dir` | data_dir | `/var/lib/sfpanel/cluster` | Raft 데이터 저장 경로 |
| `cluster.cert_dir` | cert_dir | `/etc/sfpanel/cluster` | mTLS 인증서 저장 경로 |
| `cluster.advertise_address` | advertise_address | (없음) | 다른 노드가 접근할 IP |
| `cluster.raft_tls` | raft_tls | `false` | Raft 전송 계층 TLS 암호화 (초기화 시 설정) |

### 런타임 설정 (SQLite 저장)

| 키 | 기본값 | 설명 |
|-----|--------|------|
| `terminal_timeout` | `30` | 터미널 유휴 세션 타임아웃 (분, 0=무제한) |

---

## 데이터베이스 스키마

SQLite (WAL 모드, busy_timeout 5000ms, `SetMaxOpenConns(1)`, 추가 프래그마: `synchronous(NORMAL)`, `mmap_size=256MB`, `cache_size=8MB`, `foreign_keys(on)`) — 자동 마이그레이션 **10개 테이블**. 상세 스키마는 `docs/specs/db-schema.md` 참조.

| 테이블 | 용도 |
|--------|------|
| `admin` | 관리자 계정 (username, password hash, TOTP secret 평문 저장) |
| `sessions` | 세션 토큰 해시 (현재 미사용, 향후 블랙리스트/리프레시용 예약) |
| `compose_projects` | Docker Compose 프로젝트 메타 (name, yaml_path, status) |
| `settings` | 키-값 설정 (terminal_timeout, max_upload_size, appstore_cache, `appstore_installed_*` 동적 키) |
| `custom_log_sources` | 커스텀 로그 소스 (source_id, name, path) |
| `metrics_history` | CPU/메모리 시계열 (60초 간격, 24시간 롤링, ms 단위 time PK) |
| `audit_logs` | API 감사 로그 (method/path/status/ip/node_id, 최대 50,000행, 5분 주기 정리) |
| `alert_channels` | 알림 채널 (discord/telegram, config JSON) |
| `alert_rules` | 알림 규칙 (type/condition JSON/channel_ids/severity/cooldown/node_scope) |
| `alert_history` | 알림 이력 (자동 정리 없음, 수동 삭제만) |

---

## API 엔드포인트

### 공개 (인증 불필요)

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/api/v1/health` | 헬스 체크 |
| POST | `/api/v1/auth/login` | 로그인 (JWT 토큰 발급) |
| GET | `/api/v1/auth/setup-status` | 초기 설정 필요 여부 확인 |
| POST | `/api/v1/auth/setup` | 최초 관리자 계정 생성 |

### 인증 필요 (Bearer JWT)

| 메서드 | 경로 | 설명 |
|--------|------|------|
| POST | `/api/v1/auth/2fa/setup` | 2FA 시크릿 생성 |
| POST | `/api/v1/auth/2fa/verify` | 2FA 코드 검증 및 활성화 |
| POST | `/api/v1/auth/change-password` | 비밀번호 변경 |
| GET | `/api/v1/settings` | 설정 조회 |
| PUT | `/api/v1/settings` | 설정 업데이트 |
| GET | `/api/v1/system/info` | 시스템 정보 + 현재 메트릭 + 버전 |
| GET | `/api/v1/system/metrics-history` | 24시간 메트릭 히스토리 |
| GET | `/api/v1/system/processes` | Top 10 프로세스 |
| GET | `/api/v1/system/processes/list` | 전체 프로세스 목록 (검색/정렬) |
| POST | `/api/v1/system/processes/:pid/kill` | 프로세스 시그널 전송 |
| GET | `/api/v1/system/services` | Systemd 서비스 목록 |
| GET | `/api/v1/system/services/:name/logs` | 서비스 로그 조회 |
| POST | `/api/v1/system/services/:name/start` | 서비스 시작 |
| POST | `/api/v1/system/services/:name/stop` | 서비스 중지 |
| POST | `/api/v1/system/services/:name/restart` | 서비스 재시작 |
| POST | `/api/v1/system/services/:name/enable` | 서비스 활성화 |
| POST | `/api/v1/system/services/:name/disable` | 서비스 비활성화 |
| GET | `/api/v1/files` | 디렉토리 목록 |
| GET | `/api/v1/files/read` | 파일 읽기 |
| POST | `/api/v1/files/write` | 파일 쓰기 |
| POST | `/api/v1/files/mkdir` | 디렉토리 생성 |
| DELETE | `/api/v1/files` | 파일/디렉토리 삭제 |
| POST | `/api/v1/files/rename` | 이름 변경/이동 |
| GET | `/api/v1/files/download` | 파일 다운로드 |
| POST | `/api/v1/files/upload` | 파일 업로드 (multipart) |
| GET | `/api/v1/cron` | Cron 작업 목록 |
| POST | `/api/v1/cron` | Cron 작업 생성 |
| PUT | `/api/v1/cron/:id` | Cron 작업 수정 |
| DELETE | `/api/v1/cron/:id` | Cron 작업 삭제 |
| GET | `/api/v1/logs/sources` | 로그 소스 목록 |
| GET | `/api/v1/logs/read` | 로그 읽기 (tail) |
| POST | `/api/v1/logs/custom-sources` | 커스텀 로그 소스 추가 |
| DELETE | `/api/v1/logs/custom-sources/:id` | 커스텀 로그 소스 삭제 |
| GET | `/api/v1/network/interfaces` | 네트워크 인터페이스 목록 |
| GET | `/api/v1/network/interfaces/:name` | 네트워크 인터페이스 상세 |
| PUT | `/api/v1/network/interfaces/:name` | 네트워크 인터페이스 설정 변경 |
| POST | `/api/v1/network/apply` | Netplan 설정 적용 |
| GET | `/api/v1/network/dns` | DNS 설정 조회 |
| GET | `/api/v1/network/routes` | 라우팅 테이블 조회 |
| GET | `/api/v1/network/bonds` | 본드 목록 |
| POST | `/api/v1/network/bonds` | 본드 생성 |
| DELETE | `/api/v1/network/bonds/:name` | 본드 삭제 |
| GET | `/api/v1/disks/overview` | 디스크 목록 및 개요 |
| GET | `/api/v1/disks/iostat` | 디스크 I/O 통계 |
| POST | `/api/v1/disks/usage` | 디스크 사용량 분석 |
| GET | `/api/v1/disks/smartmontools-status` | smartmontools 설치 상태 |
| POST | `/api/v1/disks/install-smartmontools` | smartmontools 설치 |
| GET | `/api/v1/disks/:device/smart` | 디스크 SMART 정보 |
| GET | `/api/v1/disks/:device/partitions` | 파티션 목록 |
| POST | `/api/v1/disks/:device/partitions` | 파티션 생성 |
| DELETE | `/api/v1/disks/:device/partitions/:number` | 파티션 삭제 |
| GET | `/api/v1/filesystems` | 파일시스템 목록 |
| POST | `/api/v1/filesystems/format` | 파티션 포맷 |
| POST | `/api/v1/filesystems/mount` | 파일시스템 마운트 |
| POST | `/api/v1/filesystems/unmount` | 파일시스템 언마운트 |
| POST | `/api/v1/filesystems/resize` | 파일시스템 리사이즈 |
| GET | `/api/v1/filesystems/expand-check` | 파일시스템 확장 가능 여부 확인 |
| POST | `/api/v1/filesystems/expand` | 파일시스템 확장 |
| GET | `/api/v1/lvm/pvs` | LVM PV 목록 |
| GET | `/api/v1/lvm/vgs` | LVM VG 목록 |
| GET | `/api/v1/lvm/lvs` | LVM LV 목록 |
| POST | `/api/v1/lvm/pvs` | PV 생성 |
| POST | `/api/v1/lvm/vgs` | VG 생성 |
| POST | `/api/v1/lvm/lvs` | LV 생성 |
| DELETE | `/api/v1/lvm/pvs/:name` | PV 삭제 |
| DELETE | `/api/v1/lvm/vgs/:name` | VG 삭제 |
| DELETE | `/api/v1/lvm/lvs/:vg/:name` | LV 삭제 |
| POST | `/api/v1/lvm/lvs/resize` | LV 리사이즈 |
| GET | `/api/v1/raid` | RAID 배열 목록 |
| GET | `/api/v1/raid/:name` | RAID 배열 상세 |
| POST | `/api/v1/raid` | RAID 생성 |
| DELETE | `/api/v1/raid/:name` | RAID 삭제 |
| POST | `/api/v1/raid/:name/add` | RAID 디스크 추가 |
| POST | `/api/v1/raid/:name/remove` | RAID 디스크 제거 |
| GET | `/api/v1/swap` | 스왑 정보 |
| POST | `/api/v1/swap` | 스왑 생성 |
| DELETE | `/api/v1/swap` | 스왑 삭제 |
| PUT | `/api/v1/swap/swappiness` | swappiness 설정 |
| GET | `/api/v1/swap/resize-check` | 스왑 리사이즈 안전성 확인 |
| GET | `/api/v1/swap/resize-check` | 스왑 리사이즈 안전성 확인 |
| PUT | `/api/v1/swap/resize` | 스왑 리사이즈 |
| GET | `/api/v1/packages/updates` | 업데이트 가능 패키지 조회 |
| POST | `/api/v1/packages/upgrade` | 패키지 업그레이드 |
| POST | `/api/v1/packages/install` | 패키지 설치 |
| POST | `/api/v1/packages/remove` | 패키지 제거 |
| GET | `/api/v1/packages/search` | 패키지 검색 |
| GET | `/api/v1/packages/docker-status` | Docker 설치 상태 확인 |
| POST | `/api/v1/packages/install-docker` | Docker 설치 (SSE 스트리밍) |
| GET | `/api/v1/appstore/categories` | 앱스토어 카테고리 목록 |
| GET | `/api/v1/appstore/apps` | 앱 목록 (카테고리 필터) |
| GET | `/api/v1/appstore/apps/:id` | 앱 상세 정보 + Compose YAML |
| POST | `/api/v1/appstore/apps/:id/install` | 앱 설치 |
| GET | `/api/v1/appstore/installed` | 설치된 앱 목록 |
| POST | `/api/v1/appstore/refresh` | 앱스토어 캐시 갱신 |
| GET | `/api/v1/firewall/status` | UFW 상태 조회 |
| POST | `/api/v1/firewall/enable` | UFW 활성화 |
| POST | `/api/v1/firewall/disable` | UFW 비활성화 |
| GET | `/api/v1/firewall/rules` | UFW 규칙 목록 |
| POST | `/api/v1/firewall/rules` | UFW 규칙 추가 |
| DELETE | `/api/v1/firewall/rules/:number` | UFW 규칙 삭제 |
| GET | `/api/v1/firewall/ports` | 리스닝 포트 목록 (ss) |
| GET | `/api/v1/firewall/docker` | Docker 방화벽 규칙 목록 |
| POST | `/api/v1/firewall/docker/rules` | Docker 방화벽 규칙 추가 |
| DELETE | `/api/v1/firewall/docker/rules/:number` | Docker 방화벽 규칙 삭제 |
| GET | `/api/v1/fail2ban/status` | Fail2ban 설치/실행 상태 |
| POST | `/api/v1/fail2ban/install` | Fail2ban 설치 |
| GET | `/api/v1/fail2ban/templates` | Fail2ban jail 템플릿 목록 |
| GET | `/api/v1/fail2ban/jails` | Fail2ban jail 목록 |
| POST | `/api/v1/fail2ban/jails` | Fail2ban jail 생성 |
| DELETE | `/api/v1/fail2ban/jails/:name` | Fail2ban jail 삭제 |
| GET | `/api/v1/fail2ban/jails/:name` | Fail2ban jail 상세 |
| POST | `/api/v1/fail2ban/jails/:name/enable` | Fail2ban jail 활성화 |
| POST | `/api/v1/fail2ban/jails/:name/disable` | Fail2ban jail 비활성화 |
| PUT | `/api/v1/fail2ban/jails/:name/config` | Fail2ban jail 설정 변경 |
| POST | `/api/v1/fail2ban/jails/:name/unban` | Fail2ban IP 차단 해제 |

### Docker 전용 (Docker 사용 가능 시에만 등록)

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/api/v1/docker/containers` | 컨테이너 목록 |
| POST | `/api/v1/docker/containers` | 컨테이너 생성 |
| GET | `/api/v1/docker/containers/:id/inspect` | 컨테이너 상세 정보 |
| GET | `/api/v1/docker/containers/:id/stats` | 컨테이너 CPU/메모리 통계 |
| POST | `/api/v1/docker/containers/:id/start` | 컨테이너 시작 |
| POST | `/api/v1/docker/containers/:id/stop` | 컨테이너 중지 |
| POST | `/api/v1/docker/containers/:id/restart` | 컨테이너 재시작 |
| DELETE | `/api/v1/docker/containers/:id` | 컨테이너 삭제 |
| GET | `/api/v1/docker/images` | 이미지 목록 |
| GET | `/api/v1/docker/images/search` | Docker Hub 이미지 검색 |
| POST | `/api/v1/docker/images/pull` | 이미지 풀 |
| DELETE | `/api/v1/docker/images/:id` | 이미지 삭제 |
| GET | `/api/v1/docker/volumes` | 볼륨 목록 |
| POST | `/api/v1/docker/volumes` | 볼륨 생성 |
| DELETE | `/api/v1/docker/volumes/:name` | 볼륨 삭제 |
| GET | `/api/v1/docker/networks` | 네트워크 목록 |
| POST | `/api/v1/docker/networks` | 네트워크 생성 |
| DELETE | `/api/v1/docker/networks/:id` | 네트워크 삭제 |
| POST | `/api/v1/docker/prune/containers` | 중지된 컨테이너 정리 |
| POST | `/api/v1/docker/prune/images` | 미사용 이미지 정리 |
| POST | `/api/v1/docker/prune/volumes` | 미사용 볼륨 정리 |
| POST | `/api/v1/docker/prune/networks` | 미사용 네트워크 정리 |
| POST | `/api/v1/docker/prune/all` | 전체 리소스 일괄 정리 |
| GET | `/api/v1/docker/compose` | Compose 프로젝트 목록 (상태 포함) |
| POST | `/api/v1/docker/compose` | Compose 프로젝트 생성 |
| GET | `/api/v1/docker/compose/:project` | Compose 프로젝트 조회 (YAML 포함) |
| PUT | `/api/v1/docker/compose/:project` | Compose YAML 업데이트 |
| DELETE | `/api/v1/docker/compose/:project` | Compose 프로젝트 삭제 |
| POST | `/api/v1/docker/compose/:project/up` | Compose Up (detached) |
| POST | `/api/v1/docker/compose/:project/down` | Compose Down |
| GET | `/api/v1/docker/compose/:project/env` | Compose 환경변수(.env) 조회 |
| PUT | `/api/v1/docker/compose/:project/env` | Compose 환경변수(.env) 업데이트 |
| GET | `/api/v1/docker/compose/:project/services` | Compose 서비스 목록 |
| POST | `/api/v1/docker/compose/:project/services/:service/restart` | Compose 서비스 재시작 |
| POST | `/api/v1/docker/compose/:project/services/:service/stop` | Compose 서비스 중지 |
| POST | `/api/v1/docker/compose/:project/services/:service/start` | Compose 서비스 시작 |
| GET | `/api/v1/docker/compose/:project/services/:service/logs` | Compose 서비스 로그 |
| POST | `/api/v1/docker/compose/:project/migrate/preflight` | 노드 간 마이그레이션 사전 점검 |
| POST | `/api/v1/docker/compose/:project/migrate` | 노드 간 마이그레이션 (SSE) |
| GET | `/api/v1/docker/compose/migrate/target-info` | 대상 노드 사실 조회 (내부 전용) |
| POST | `/api/v1/docker/compose/migrate-import` | 마이그레이션 번들 수신 (내부 전용) |

### WebSocket 엔드포인트 (쿼리 파라미터 토큰 인증)

총 6개. 모두 `?node=X`로 클러스터 원격 릴레이 가능 (`internal/cluster/ws_relay.go`).

| 경로 | 설명 |
|------|------|
| `/ws/metrics` | 실시간 시스템 메트릭 (약 3초 주기 JSON) |
| `/ws/logs` | 실시간 로그 스트리밍 (`tail -f`, 쿼리 `source=syslog/auth/kern/sfpanel/dpkg/firewall/fail2ban/custom_*`) |
| `/ws/terminal` | 서버 PTY 세션 (영속, 256KB 스크롤백, 최대 20 세션, idle 타임아웃 DB `terminal_timeout`) |
| `/ws/docker/containers/:id/logs` | 컨테이너 로그 (`tail`/`timestamps`/`stream`/`since` 쿼리) |
| `/ws/docker/containers/:id/exec` | 컨테이너 셸 exec (TextMessage 양방향 + resize JSON) |
| `/ws/docker/compose/:project/logs` | Compose 프로젝트 로그 (`service` 필터 가능) |

### SSE 스트리밍 엔드포인트 (`Content-Type: text/event-stream`)

총 9개. JWT 미들웨어 적용. 장시간 실행 작업의 실시간 진행률 스트리밍용. 클러스터 프록시 시 HTTP 직접 릴레이(5분 타임아웃).

| 경로 | 용도 | 이벤트 형식 |
|------|------|-----------|
| `POST /api/v1/system/update` | SFPanel 자체 업데이트 | JSON `{step, message}` (downloading/verifying/extracting/replacing/restarting/complete) |
| `POST /api/v1/docker/images/pull` | Docker 이미지 풀 | JSON (Docker API 이벤트 그대로) |
| `POST /api/v1/docker/compose/:project/up-stream` | Compose 프로젝트 시작 | JSON `{phase, line}` |
| `POST /api/v1/docker/compose/:project/update-stream` | Compose 스택 풀+재생성 | JSON `{phase, line}` |
| `POST /api/v1/docker/compose/:project/migrate` | 노드 간 스택 마이그레이션 | JSON `{phase, message, done}` (preflight/quiesce/package/transfer/finalize/done) |
| `POST /api/v1/packages/install-docker` | Docker 엔진 설치 (get.docker.com) | 평문 라인 + `[DONE]` |
| `POST /api/v1/packages/install-node` | Node.js/NVM 설치 | 평문 라인 + `[DONE]` |
| `POST /api/v1/network/tailscale/install` | Tailscale 설치 | 평문 라인 + `[DONE]` |
| `POST /api/v1/cluster/update` | 멀티노드 업데이트 오케스트레이션 | JSON `{node_id, node_name, step, status, message}` |

### API 응답 형식

모든 REST API 응답은 통일된 JSON 형식:

```json
// 성공
{"success": true, "data": {...}}

// 실패
{"success": false, "error": {"code": "ERROR_CODE", "message": "사람이 읽을 수 있는 메시지"}}
```

---

## 프론트엔드 페이지

모든 페이지 컴포넌트는 `React.lazy()`로 지연 로딩되며, `<Suspense>` 폴백으로 스피너를 표시합니다.

| 페이지 | 파일 | 설명 |
|--------|------|------|
| Login | `web/src/pages/Login.tsx` | 로그인 (username + password + TOTP) |
| Setup | `web/src/pages/Setup.tsx` | 최초 관리자 계정 생성 위저드 |
| Dashboard | `web/src/pages/Dashboard.tsx` | 시스템 대시보드 |
| AppStore | `web/src/pages/AppStore.tsx` | 앱스토어 (원클릭 Docker 앱 설치) |
| Docker | `web/src/pages/Docker.tsx` | Docker 관리 (탭: Stacks, Containers, Images, Volumes, Networks) |
| DockerStacks | `web/src/pages/docker/DockerStacks.tsx` | Docker Compose 스택 관리 |
| DockerContainers | `web/src/pages/docker/DockerContainers.tsx` | 컨테이너 관리 |
| DockerContainerCreate | `web/src/pages/docker/DockerContainerCreate.tsx` | 컨테이너 생성 폼 |
| DockerImages | `web/src/pages/docker/DockerImages.tsx` | 이미지 관리 |
| DockerVolumes | `web/src/pages/docker/DockerVolumes.tsx` | 볼륨 관리 |
| DockerNetworks | `web/src/pages/docker/DockerNetworks.tsx` | 네트워크 관리 |
| Terminal | `web/src/pages/Terminal.tsx` | 웹 터미널 (다중 탭) |
| Files | `web/src/pages/Files.tsx` | 파일 관리자 |
| Logs | `web/src/pages/Logs.tsx` | 로그 뷰어 |
| CronJobs | `web/src/pages/CronJobs.tsx` | Cron 작업 관리 |
| Processes | `web/src/pages/Processes.tsx` | 프로세스 관리 |
| Network | `web/src/pages/Network.tsx` | 네트워크 관리 |
| Disk | `web/src/pages/Disk.tsx` | 디스크 관리 (탭: Overview, Partitions, Filesystems, LVM, RAID, Swap) |
| Services | `web/src/pages/Services.tsx` | Systemd 서비스 관리 |
| Firewall | `web/src/pages/Firewall.tsx` | 방화벽 관리 (탭: Rules, Ports, Fail2ban, Docker, Logs) |
| Packages | `web/src/pages/Packages.tsx` | 패키지 관리 + Docker 설치 |
| Settings | `web/src/pages/Settings.tsx` | 설정 (언어, 터미널, 비밀번호, 2FA, 시스템 정보) |

### 다국어 지원

- **지원 언어**: 영어 (`en.json`), 한국어 (`ko.json`)
- **감지 방식**: 브라우저 언어 자동 감지 (`i18next-browser-languagedetector`)
- **전환**: Settings 페이지에서 수동 전환 가능

---

## 빌드 & 배포

### 빌드 프로세스

```bash
# 전체 빌드 (Makefile)
make build
# 1. cd web && npm install && npm run build  → web/dist/ 생성
# 2. go build -ldflags="-s -w" -trimpath -o sfpanel ./cmd/sfpanel  → 바이너리 생성 (~16MB)

# 개발 모드
make dev-api   # Go 백엔드 (:3628)
make dev-web   # Vite 프론트엔드 (:5173, API 프록시 → :3628)

# 린트
make lint      # golangci-lint + eslint
```

### CI/CD 파이프라인

- **트리거**: `v*` 태그 푸시 (예: `v0.3.0`)
- **워크플로우**: `.github/workflows/release.yml`
  1. Checkout (full history)
  2. Go 설정 (go.mod에서 버전 자동 감지)
  3. Node.js 20 설정 (npm 캐시)
  4. GoReleaser v2 실행 (`release --clean`)

### GoReleaser 설정

- **Before Hook**: `cd web && npm ci && npm run build` (프론트엔드 빌드)
- **빌드 타깃**: linux/amd64, linux/arm64 (CGO_ENABLED=0)
- **ldflags**: `-s -w` (디버그 심볼 제거) + version/commit/date 주입
- **아카이브**: `sfpanel_{version}_{os}_{arch}.tar.gz` (config.example.yaml 포함)
- **체크섬**: `checksums.txt`
- **변경 로그**: 자동 생성 (docs/test/ci/chore 제외)
- **릴리즈**: GitHub Releases (드래프트 아님, 프리릴리즈 자동 감지)

### 설치 스크립트

```bash
# 설치
curl -fsSL https://raw.githubusercontent.com/sfpanel/sfpanel/main/scripts/install.sh | bash

# 삭제
curl -fsSL https://raw.githubusercontent.com/sfpanel/sfpanel/main/scripts/install.sh | bash -s uninstall
```

**설치 과정**:
1. Root 권한, Linux OS, 아키텍처(amd64/arm64) 확인
2. GitHub API에서 최신 버전 조회
3. 바이너리 다운로드 및 설치 (`/usr/local/bin/sfpanel`)
4. 디렉토리 생성 (`/etc/sfpanel`, `/var/lib/sfpanel`, `/var/log/sfpanel`)
5. JWT 시크릿 자동 생성 포함 `config.yaml` 생성 (chmod 600)
6. systemd 서비스 등록, 활성화, 시작
7. 기존 설치 감지 시 서비스 중지 후 업그레이드

### 프로덕션 디렉토리 구조

```
/usr/local/bin/sfpanel           # 바이너리
/etc/sfpanel/config.yaml         # 설정 파일 (600 권한)
/var/lib/sfpanel/sfpanel.db      # SQLite 데이터베이스
/var/lib/sfpanel/compose/        # Docker Compose 프로젝트 YAML 저장
/var/log/sfpanel/sfpanel.log     # 로그 파일
/etc/systemd/system/sfpanel.service  # systemd 서비스
```

---

## 아키텍처 특징

- **Docker 비의존**: Docker가 없어도 패널 자체는 정상 동작 (Docker 기능만 비활성화, 26개 `/docker/*` 라우트 미등록)
- **자동 마이그레이션**: 첫 실행 시 SQLite 스키마 자동 생성 (10개 테이블, 멱등)
- **백그라운드 수집**: 60초 간격 메트릭 히스토리 수집 (SQLite 저장, 24시간 롤링)
- **세션 정리**: 1분 간격 유휴 터미널 세션 자동 정리
- **SPA 라우팅**: 모든 경로에 대해 `index.html` 폴백 제공 (API/WS 경로 제외)
- **CORS**: 개발 모드용 localhost:5173 허용
- **코드 스플리팅**: 모든 페이지 컴포넌트를 `React.lazy()`로 지연 로딩하여 초기 번들 크기 최소화
- **SetupGuard 캐싱**: 초기 설정 확인 API를 모듈 레벨 변수로 캐싱하여 불필요한 반복 호출 방지
- **버전 API 제공**: 서버 빌드 시 주입된 버전 정보를 `/api/v1/system/info` 응답에 포함 (`DashboardHandler.Version`)
- **Compose 디렉토리 스캔**: `/opt/stacks` 디렉토리를 스캔하여 기존 Compose 프로젝트 자동 발견

---

## CLI 커맨드

SFPanel 바이너리는 서버 실행 외에도 관리 명령을 지원합니다.

| 커맨드 | 설명 |
|--------|------|
| `sfpanel [config.yaml]` | 패널 서버 시작 (기본 설정: `config.yaml`) |
| `sfpanel version` | 버전 정보 출력 (버전, 커밋 해시, 빌드 날짜) |
| `sfpanel update` | GitHub Releases에서 최신 버전 다운로드 및 자동 업데이트. 현재 아키텍처(amd64/arm64) 자동 감지. systemd 서비스 실행 중이면 자동 재시작. |
| `sfpanel reset` | 데이터베이스 삭제 및 초기화 (셋업 위저드로 복귀). 확인 프롬프트(y/N) 표시. |
| `sfpanel cluster init [--name]` | 새 클러스터 초기화 (CA 생성, Raft 부트스트랩) |
| `sfpanel cluster join ADDR TOKEN` | 기존 클러스터 참가 |
| `sfpanel cluster leave` | 클러스터 탈퇴 (단독 모드 복귀) |
| `sfpanel cluster status` | 클러스터 상태 확인 |
| `sfpanel cluster token [--ttl]` | 참가 토큰 생성 |
| `sfpanel cluster remove NODE_ID` | 노드 제거 |
| `sfpanel help` | 사용법 도움말 출력 |

**update 동작 과정:**
1. GitHub API에서 최신 릴리즈 버전 조회
2. 현재 버전과 비교 (동일하면 "Already up to date" 출력)
3. 사용자 확인(y/N) 후 바이너리 다운로드 (tar.gz)
4. 현재 바이너리 경로에 atomic replace (`.new` 임시 파일 → rename)
5. systemd 서비스 활성 상태이면 `systemctl restart sfpanel` 자동 실행
