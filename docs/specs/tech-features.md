# SFPanel 기술 스택 & 기능 스펙

> 마지막 동기화: 2026-04-19 · 기준 버전: v0.9.0 · 근거: `docs/superpowers/research/2026-04-19-docs-overhaul/features-inventory.md`
>
> 경량 서버 관리 웹 패널. 개인 서버 관리자 및 DevOps 팀을 위한 Docker 중심 관리 도구.
> 올인원 Go 바이너리 아키텍처 — React SPA를 `go:embed`로 포함하여 단일 실행 파일로 배포.

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

- **설명**: 서버의 전체 상태를 한눈에 파악하는 실시간 모니터링 대시보드
- **주요 기능**:
  - 실시간 CPU/메모리/디스크/네트워크 메트릭 카드 (WebSocket으로 2초 간격 업데이트)
  - CPU/메모리 사용률 시계열 차트 (최대 24시간, 60초 간격 ~1,440 포인트)
  - 호스트 정보 표시 (호스트명, OS, 플랫폼, 커널, 업타임, CPU 코어 수)
  - 네트워크 I/O 속도 (송/수신 bytes/sec) 및 누적 전송량
  - Swap 메모리 사용량
  - Docker 컨테이너 요약 (실행 중/중지/전체 개수, 최근 5개 컨테이너 상태)
  - Top 10 프로세스 (CPU 사용률 기준, 10초 간격 자동 갱신)
  - 최근 시스템 로그 (syslog 마지막 8줄)
  - 빠른 액션 바로가기 (파일, Docker, 패키지, Cron, 로그)
  - WebSocket 연결 상태 표시 (Live/Disconnected)
- **관련 기술**: gopsutil v4, gorilla/websocket, uPlot, Echo WebSocket

### 2. Docker 관리

- **설명**: Docker 리소스의 전체 생명주기를 웹 UI에서 관리
- **주요 기능**:
  - **컨테이너**: 목록 조회 (전체/실행 중/중지), 상세 검사 (포트/환경변수/마운트/네트워크), 시작/중지/재시작/삭제, CPU/메모리 통계, 실시간 로그 스트리밍 (WebSocket), 컨테이너 내부 셸 접속 (WebSocket + exec, TTY 리사이즈 지원), **컨테이너 생성** (이미지/이름/포트/볼륨/환경변수/네트워크/명령어/재시작 정책 설정)
  - **이미지**: 로컬 이미지 목록, 이미지 풀 (레지스트리에서 다운로드), 이미지 삭제 (강제), **Docker Hub 검색** (이미지 이름으로 검색, 설명/스타/공식 여부 표시)
  - **볼륨**: 볼륨 목록, 생성, 삭제 (강제)
  - **네트워크**: 네트워크 목록, 생성 (드라이버 선택: bridge 기본), 삭제
  - **Docker Compose (Stacks)**: `/opt/stacks` 디렉토리 기반 프로젝트 관리 (디스크 스캔), Monaco 에디터로 YAML 편집, `.env` 파일 편집, `docker compose up -d` / `docker compose down` 실행, 프로젝트 상태 관리, **서비스별 제어** (시작/중지/재시작), 서비스별 로그 조회
  - **리소스 정리 (Prune)**: 컨테이너/이미지/볼륨/네트워크 개별 정리 및 전체 일괄 정리
- **관련 기술**: Docker Go SDK v27.5.1, gorilla/websocket, xterm.js, Monaco Editor

### 3. 웹 터미널

- **설명**: 브라우저에서 서버 셸에 직접 접속하는 완전한 터미널 에뮬레이터
- **주요 기능**:
  - PTY (pseudo-terminal) 기반 실제 셸 세션 (/bin/bash 또는 /bin/sh)
  - 다중 탭 지원 (생성/닫기/이름 변경, localStorage 지속)
  - 세션 지속성 — 탭 전환/재연결 시 스크롤백 버퍼 재생 (256KB 링 버퍼)
  - 터미널 리사이즈 (PTY 동기화)
  - 폰트 크기 조절 (10~24px)
  - 터미널 내 텍스트 검색 (SearchAddon)
  - 웹 링크 자동 감지 및 클릭 (WebLinksAddon)
  - Tokyo Night 컬러 테마
  - 유휴 세션 자동 정리 (설정 가능한 타임아웃, 기본 30분, 0=무제한)
  - 키보드 단축키 (Ctrl+F 검색)
- **관련 기술**: creack/pty, gorilla/websocket, xterm.js v6, xterm addons

### 4. 파일 관리자

- **설명**: 서버 파일시스템을 웹 UI에서 탐색하고 편집하는 파일 매니저
- **주요 기능**:
  - 디렉토리 탐색 (브레드크럼 네비게이션, 직접 경로 입력)
  - 파일 읽기 (최대 5MB, Monaco 에디터로 구문 강조 표시)
  - 파일 생성/쓰기/저장
  - 디렉토리 생성 (중첩 경로 자동 생성)
  - 파일/디렉토리 삭제 (시스템 중요 경로 보호: /, /etc, /usr, /bin, /sbin, /var, /boot, /proc, /sys, /dev)
  - 파일/디렉토리 이름 변경 (이동)
  - 파일 다운로드
  - 파일 업로드 (multipart, 최대 100MB)
  - 경로 유효성 검증 (절대 경로 필수, 디렉토리 트래버설 차단)
  - 파일 목록 정렬 (디렉토리 우선, 알파벳순)
  - 파일 타입별 아이콘 표시
  - 권한(mode), 크기, 수정 시간 표시
- **관련 기술**: Go os 패키지, Monaco Editor, 30+ 프로그래밍 언어 구문 강조

### 5. 로그 뷰어

- **설명**: 시스템 및 애플리케이션 로그를 웹에서 조회하고 실시간 스트리밍하는 뷰어
- **주요 기능**:
  - 8개 사전 정의 로그 소스: System Log, Auth Log, Kernel Log, Nginx Access/Error, SFPanel, Package Manager (dpkg), Firewall (UFW)
  - **커스텀 로그 소스 추가/삭제** (이름 + 파일 경로 지정, SQLite `custom_log_sources` 테이블에 저장)
  - 로그 소스별 파일 존재 여부 및 크기 표시
  - 줄 수 선택 (100, 500, 1000, 5000줄)
  - 실시간 스트리밍 모드 (WebSocket + `tail -f`, Live 토글)
  - 로그 레벨 감지 및 색상 구분 (ERROR/FATAL=빨강, WARN=노랑, INFO=파랑, DEBUG=회색)
  - 로그 내 텍스트 검색 (하이라이트, 이전/다음 매치 탐색, Ctrl+F 단축키)
  - 자동 스크롤 토글
  - 로그 파일 다운로드 (.log 파일)
  - 줄 번호 표시
  - 허용 목록 기반 접근 제어 (사전 정의 소스 + 커스텀 소스만 접근 가능)
- **관련 기술**: tail 명령어, gorilla/websocket, WebSocket 기반 실시간 스트리밍

### 6. 프로세스 관리

- **설명**: 서버에서 실행 중인 모든 프로세스를 모니터링하고 제어
- **주요 기능**:
  - 전체 프로세스 목록 (PID, 이름, 사용자, CPU%, 메모리%, 상태, 커맨드라인)
  - 프로세스 검색 (이름, 명령어, 사용자, PID로 필터링)
  - 정렬 (CPU, 메모리, PID, 이름)
  - 프로세스 종료 (SIGTERM, SIGKILL, SIGHUP, SIGINT 시그널 선택)
  - 5초 간격 자동 갱신
  - 실시간 시스템 리소스 요약 (CPU/메모리/Swap 사용률 바)
  - Top 10 프로세스 (대시보드용 API)
- **관련 기술**: gopsutil/v4 process, syscall 시그널

### 7. Cron 작업 관리

- **설명**: 시스템 crontab을 웹 UI에서 관리
- **주요 기능**:
  - crontab 목록 조회 (작업, 환경변수, 주석 구분)
  - Cron 작업 생성 (스케줄 + 명령어)
  - Cron 작업 수정 (스케줄/명령어 변경)
  - Cron 작업 삭제
  - 작업 활성화/비활성화 토글 (주석 처리 방식)
  - 스케줄 프리셋 (매분, 매시, 매일, 매주, 매월)
  - 스케줄 설명 자동 생성 ("Every 5 minutes", "@reboot" 등)
  - 스케줄 유효성 검증 (5-필드 형식 + @키워드)
  - 전체 타입 표시 모드 (env, comment 포함)
- **관련 기술**: crontab CLI (`crontab -l`, `crontab -`), 정규식 파싱

### 8. 패키지 관리

- **설명**: 시스템 패키지(apt) 업데이트 및 Docker 설치를 웹에서 관리
- **주요 기능**:
  - **Docker 상태 확인**: 설치 여부, 버전, 실행 상태, Docker Compose 사용 가능 여부
  - **Docker 원클릭 설치**: get.docker.com 스크립트 실행, SSE(Server-Sent Events)로 실시간 출력 스트리밍
  - **시스템 업데이트 확인**: `apt list --upgradable` 파싱 (패키지명, 현재/신규 버전, 아키텍처)
  - **패키지 업그레이드**: 전체 또는 선택적 업그레이드 (`apt-get upgrade`)
  - **패키지 설치**: 이름으로 패키지 설치 (`apt-get install`)
  - **패키지 제거**: 이름으로 패키지 제거 (`apt-get remove`)
  - **패키지 검색**: `apt-cache search` 결과 표시 (최대 50건)
  - 패키지 이름 유효성 검증 (인젝션 방지)
  - 작업 출력 실시간 표시 다이얼로그
- **관련 기술**: apt/apt-get/apt-cache CLI, SSE 스트리밍, 5분 명령 타임아웃

### 9. 네트워크 / VPN 관리

- **설명**: 서버 네트워크 인터페이스, DNS, 라우팅, 본딩 및 VPN 클라이언트(WireGuard, Tailscale)를 웹 UI에서 관리
- **주요 기능**:
  - **인터페이스**: 네트워크 인터페이스 목록 (이름/상태/IP/MAC/속도/MTU), 인터페이스 상세 정보, DHCP/Static 설정 변경, **물리/가상/Docker 인터페이스 분류** (물리적 인터페이스 우선 표시, Docker 네트워크 접이식 분리)
  - **DNS**: DNS 서버 및 검색 도메인 조회
  - **라우팅**: 시스템 라우팅 테이블 조회
  - **본딩**: 네트워크 본드 목록, 생성 (모드/슬레이브/프라이머리 설정), 삭제
  - **Netplan**: 네트워크 설정 적용 (`netplan apply`)
  - **WireGuard VPN**: 설치 상태 확인 및 원클릭 설치, 인터페이스 목록/상세 (피어 정보 포함), 인터페이스 활성화/비활성화 (`wg-quick up/down`), 설정 파일 CRUD (생성/조회/수정/삭제), `.conf` 파일 업로드 지원, PrivateKey 마스킹
  - **Tailscale VPN**: 설치 상태 확인 및 원클릭 설치 (공식 install.sh), 연결/해제/로그아웃, Auth Key 입력 또는 브라우저 인증 URL, 자기 노드 정보 (Hostname/IP/Tailnet/MagicDNS), 피어 목록 (호스트명/IP/OS/온라인 상태/트래픽), Exit Node 선택/해제
- **관련 기술**: Netplan CLI, ip/networkctl, wg/wg-quick, tailscale CLI

### 10. 디스크 관리

- **설명**: 서버 디스크, 파티션, 파일시스템, LVM, RAID, Swap을 웹 UI에서 관리
- **주요 기능**:
  - **디스크 개요**: 디스크 목록 (이름/크기/모델/시리얼), I/O 통계, 디스크 사용량 분석 (경로/깊이별)
  - **SMART 모니터링**: smartmontools 설치 상태 확인 및 원클릭 설치, 디스크별 SMART 정보 조회
  - **파티션**: 디스크별 파티션 목록, 파티션 생성 (시작/끝/파일시스템 타입), 파티션 삭제
  - **파일시스템**: 마운트된 파일시스템 목록, 파티션 포맷 (ext4/xfs/btrfs 등), 마운트/언마운트, 파일시스템 리사이즈
  - **LVM**: PV/VG/LV 목록 및 생성/삭제, LV 리사이즈
  - **RAID**: mdadm RAID 배열 목록 및 상세 정보, RAID 생성 (레벨/디바이스 선택), RAID 삭제, 디스크 추가/제거
  - **Swap**: 스왑 정보 조회, 스왑 파일/파티션 생성/삭제, swappiness 설정, 스왑 리사이즈 (안전성 사전 검사)
- **관련 기술**: lsblk, parted, mkfs, mount, lvm2, mdadm, smartmontools CLI

### 11. 방화벽 관리

- **설명**: UFW 방화벽, Fail2ban 침입 방지 시스템, Docker 방화벽을 웹 UI에서 관리
- **주요 기능**:
  - **UFW 방화벽**: 활성화/비활성화 토글, 규칙 목록 조회 (번호/대상/동작/소스/코멘트/IPv6 구분), 규칙 추가 (action/port/protocol/from/to/comment), 규칙 삭제
  - **리스닝 포트**: `ss` 명령어로 TCP/UDP 리스닝 포트 조회 (프로토콜/주소/포트/PID/프로세스), 포트에서 직접 UFW 규칙 추가 지원
  - **Fail2ban**: 설치 상태 확인 및 원클릭 설치 (`apt-get install -y fail2ban`), **jail 템플릿** (사전 정의된 보호 규칙 — SSH, Nginx 등), jail 생성/삭제, jail 목록 및 상태 조회, jail 활성화/비활성화, jail 설정 변경 (maxretry/bantime/findtime), 차단 IP 해제 (unban)
  - **Docker 방화벽**: DOCKER-USER 체인 규칙 관리 (iptables), Docker 네트워크 트래픽 제어
  - **방화벽 로그**: UFW + Docker-USER 패킷 로그 뷰어
  - 입력값 검증: 포트 번호/범위, IP/CIDR 주소, 프로토콜, action, jail 이름 등 서버 측 정규식 검증
- **관련 기술**: UFW CLI (`ufw`), Fail2ban CLI (`fail2ban-client`), iptables, ss 명령어

### 12. Systemd 서비스 관리

- **설명**: 시스템 서비스(systemd unit)를 웹 UI에서 모니터링하고 제어
- **주요 기능**:
  - 서비스 목록 조회 (이름, 상태, 활성화 여부, 설명)
  - 서비스 시작/중지/재시작
  - 서비스 활성화/비활성화 (부팅 시 자동 시작)
  - 서비스 로그 조회 (journalctl)
- **관련 기술**: systemctl CLI, journalctl

### 14. 앱스토어 (App Store)

- **설명**: GitHub 레포 기반 원클릭 Docker Compose 앱 설치
- **주요 기능**:
  - 앱 카테고리별 탐색 (모니터링, 보안, 미디어, 클라우드, 개발, 인프라 등)
  - 앱 검색 (이름, 설명 기반)
  - 원클릭 설치: Compose YAML 자동 생성 + 환경변수 폼 + `docker compose up -d`
  - 동적 환경변수 설정 (포트, 비밀번호 등 앱별 커스텀)
  - 자동 비밀번호 생성 (`crypto/rand`, 32바이트 hex)
  - SQLite 기반 캐시 (1시간 TTL, 5개 동시 HTTP 요청으로 백그라운드 갱신)
  - 설치된 앱은 Docker Compose Stacks에서 관리 가능
  - 설치 모드: 심플 모드 (환경변수 폼) / 고급 모드 (docker-compose.yml + .env 직접 편집)
  - 포트/컨테이너 이름 충돌 자동 감지 및 대체 포트 제안
  - SSE 기반 설치 진행률 스트리밍 (fetch → prepare → pull → start → done)
- **관련 기술**: GitHub Raw API, Docker Compose, crypto/rand, net/http, SSE 스트리밍

### 15. 클러스터 관리

- **설명**: Proxmox 스타일 대칭 클러스터. 2~32대 노드를 하나의 클러스터로 통합 관리
- **주요 기능**:
  - **Raft 합의 엔진**: `hashicorp/raft` + BoltDB 스토어 (임베디드, 외부 의존 없음). FSM이 JWT 시크릿, 클러스터 이름, 노드 목록(역할/주소/상태), 어드민 계정을 복제
  - **gRPC + mTLS**: 노드 간 제어 채널 (포트 9444), TLS 1.3 상호 인증, ECDSA P-256 자체 CA, 노드 인증서 자동 발급
  - **참가 토큰**: HMAC-SHA256 서명, 1회용, 시간제한 (기본 24시간, 메모리 저장으로 리더 재시작 시 소실)
  - **하트비트 모니터링**: 2초 간격, 3단계 상태 판정 (online → suspect → offline)
  - **JoinEngine 파이프라인** (`internal/cluster/join.go`): 리더→조인노드 파이프라인을 `PreFlight` / `Execute` 두 단계로 분리
    - PreFlight: TCP 연결 → `TokenManager.Peek()`(소비 없이 검증) → 포트 확인 → IP 자동 감지 → 예상 실패 사유 사전 반환
    - Execute: 6단계 원자적 조인 (Join RPC → CA/노드 인증서 저장 → Config 업데이트 → Config 원자 저장 → DB 어드민 동기화 → `LiveActivate` 콜백으로 Manager+gRPC 서버 시작). 각 단계 실패 시 롤백 경로 명시
  - **Zero-Restart 라이프사이클**: `LiveActivate` 콜백 (`main.go`에서 주입)으로 Raft/gRPC를 프로세스 재시작 없이 활성화. 기존 `existingMgr` 파라미터로 Raft 셧다운/재시작 레이스 회피. 탈퇴/해산만 바이너리 재시작 필요
  - **IP 자동 감지** (`internal/cluster/detect.go`): Tailscale(100.64.0.0/10), 동일 서브넷 매칭, TCP 다이얼 기반 감지, 리더 주소 기반 라우팅 힌트
  - **클러스터 업데이트**: 롤링/동시 모드로 전체 클러스터 SFPanel 업데이트 오케스트레이션 (SSE 진행률 스트리밍, 노드별 step+status 이벤트)
  - **CLI 명령어**: `sfpanel cluster init/join/leave/status/token/remove`
  - **웹 UI API**: 클러스터 초기화/참여/탈퇴/해산/업데이트 REST API (~15개 엔드포인트)
- **패키지 레이아웃**: `internal/cluster/` — `manager.go`, `raft_fsm.go`, `grpc_server.go`, `join.go`, `detect.go`, `tls.go`, `token.go`, `ws_relay.go` 외 다수. proto는 `proto/cluster.proto`, 생성물 `proto/cluster.pb.go` / `proto/cluster_grpc.pb.go`
- **설정 확장**: `config.yaml`에 `cluster` 섹션 (enabled, name, node_id, node_name, grpc_port, data_dir, cert_dir, advertise_address, raft_tls)
- **REST 프록시 미들웨어**: `ClusterProxyMiddleware` — `?node=X` 쿼리 파라미터로 원격 노드에 요청 투명 전달. 일반 요청은 gRPC `ProxyRequest`(30초 타임아웃), 스트리밍(SSE/`-stream` 접미사 경로)은 HTTP 직접 릴레이(5분 타임아웃)
- **WebSocket 릴레이** (`internal/cluster/ws_relay.go`): `WrapEchoWSHandler`로 터미널/로그/메트릭/Docker exec 등 모든 WS를 원격 노드로 양방향 포워딩. 메시지 타입(바이너리/텍스트) 보존, 한쪽 종료 시 전파
- **내부 프록시 인증**: CA 인증서 SHA-256 해시 기반, `X-SFPanel-Internal-Proxy` 헤더 상수시간 비교 (JWT 비의존). `X-SFPanel-Original-User` 헤더로 원본 사용자 전파
- **동시성 보호**: `Manager.joiningMu`(Init/Join 중복 방지), `Config.configMu`(Cluster 필드 보호), `Handler.mu` RWMutex(Manager 포인터 동기화), `Handler.OnManagerActivated` 콜백(다른 핸들러가 Manager 활성화 시 갱신)
- **노드 선택 UI**: 사이드바 NodeSelector 컴포넌트, localStorage `sfpanel_current_node` 지속, 노드 전환 시 페이지 즉시 갱신
- **Graceful 누락 처리**: 원격 노드에 ufw/crontab/rsyslog 미설치 시 500 대신 빈 결과 반환
- **알려진 제약**:
  - 토큰은 메모리에만 저장 (리더 재시작 시 기존 토큰 소실)
  - 비클러스터 → 클러스터 마이그레이션 경로 없음 (Init/Join 신규만 지원)
  - TLS 갱신 미자동화 (1년 TTL, 만료 감시 없음, 수동 갱신 필요)
  - 네트워크 분할 시 Raft 안전성만 보장 (분할 뇌 자체는 막지 않음)
- **설계 문서**: `docs/superpowers/specs/2026-04-13-cluster-join-redesign.md` (조인 재설계), `docs/superpowers/research/2026-04-19-docs-overhaul/cluster-inventory.md` (인벤토리)
- **관련 기술**: hashicorp/raft, raft-boltdb, gRPC, protobuf, crypto/x509

### 16. 시스템 튜닝

- **설명**: 커널 매개변수(sysctl)를 웹 UI에서 조회하고 권장값으로 적용하는 시스템 성능 최적화 도구
- **주요 기능**:
  - **튜닝 상태 조회**: 4개 카테고리(네트워크/메모리/파일시스템/보안) 37개 sysctl 매개변수의 현재값 vs 권장값 비교
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
  - **기록 항목**: 사용자명, HTTP 메서드, 경로, 응답 상태 코드, 클라이언트 IP, 노드 ID, 생성 시각
  - **클러스터 지원**: `?node=X` 파라미터로 원격 노드 요청 시 노드 ID 자동 추적
  - **감사 로그 조회**: 페이지네이션 지원 (page/limit 파라미터, 기본 50건, 최대 100건)
  - **감사 로그 삭제**: 전체 감사 로그 일괄 삭제
  - **보안 예외**: 로그인/셋업 엔드포인트는 비밀번호 노출 방지를 위해 기록 제외
- **관련 기술**: Echo 미들웨어, SQLite, 비동기 INSERT (goroutine)

### 18. 시스템 백업/복원

- **설명**: SFPanel 설정 데이터를 백업하고 복원하는 재해 복구 기능
- **주요 기능**:
  - **백업 생성**: SQLite DB + config.yaml + Docker Compose 프로젝트 파일을 tar.gz 아카이브로 다운로드
  - **Compose 파일 포함**: `/opt/stacks/` 하위 모든 프로젝트의 docker-compose.yml, compose.yaml, .env 파일 자동 수집
  - **백업 파일 복원**: tar.gz 업로드 → DB/설정/Compose 파일 복원, 기존 파일 자동 .bak 백업
  - **필수 파일 검증**: sfpanel.db 미포함 시 복원 거부
  - **서비스 자동 재시작**: 복원 완료 후 systemd 서비스 활성 상태이면 자동 재시작
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
  - **서버 접속**: URL 입력 후 health check API로 연결 가능 여부 확인
  - **연결 진단**: 접속 실패 시 포트/방화벽/DNS 등 문제 진단 기능
  - **언어 선택**: Connect 페이지에서 한국어/영어 전환
  - **TauriGuard**: 서버 URL 미설정 시 자동으로 /connect 페이지로 리다이렉트
  - **크로스플랫폼**: macOS, Windows, Linux 지원 (Tauri v2)
- **관련 기술**: Tauri v2, tauri-plugin-http, Rust, WebView

### 21. 알림 시스템

- **설명**: 시스템 리소스 임계치 기반 자동 알림 발송. `internal/feature/alert/` 하위 `handler.go`(REST CRUD) + `manager.go`(백그라운드 평가) + `channels/discord.go`, `channels/telegram.go`(채널 구현).
- **주요 기능**:
  - **모니터링 대상**: CPU 사용률, 메모리 사용률, 디스크 사용률
  - **알림 규칙** (`alert_rules` 테이블): 조건 JSON `{"operator":">","threshold":90}` (연산자 `>`/`>=`/`<`/`<=`, threshold float 0~100), 쿨다운 (기본 300초, 동일 규칙 재발송 억제, 메모리 상태)
  - **알림 채널** (`alert_channels` 테이블): Discord (`{"webhook_url":"..."}`), Telegram (`{"bot_token":"...","chat_id":"..."}`). 채널 `enabled` 플래그로 토글
  - **클러스터 지원**: `node_scope` = `all` 또는 `specific`, `node_scope=specific` 시 `node_ids` JSON 배열로 대상 노드 지정
  - **심각도 수준**: info, warning, critical (`severity` 컬럼)
  - **알림 이력** (`alert_history` 테이블): 발송 시 INSERT (실제 전송 성공 채널 ID 배열만 `sent_channels`에 저장). 규칙 삭제 후에도 보존 (`rule_name` 스냅샷). 자동 정리 없음, 관리자가 UI에서 수동 삭제
  - **평가 주기**: `manager.go`가 60초 간격 ticker로 `WHERE enabled=1` 활성 규칙을 평가, 조건 충족 + 쿨다운 만료 시 매칭 채널에 병렬 발송
  - **채널 테스트**: 채널 생성/편집 후 테스트 알림 발송 엔드포인트 제공
- **관련 기술**: net/http (Discord/Telegram API), database/sql, 고루틴 (백그라운드 평가)
- **설계 문서**: `docs/superpowers/specs/2026-04-07-alert-system-design.md`

### 22. 설정

- **설명**: 패널 설정 및 사용자 계정 관리
- **주요 기능**:
  - **언어 설정**: 영어/한국어 전환 (i18next 브라우저 언어 감지)
  - **터미널 타임아웃**: 유휴 터미널 세션 자동 종료 시간 (분 단위, 0=무제한)
  - **비밀번호 변경**: 현재 비밀번호 검증 후 변경 (최소 8자)
  - **2단계 인증(2FA)**: TOTP 설정/활성화 (QR 코드 표시, 시크릿 키 표시, 6자리 코드 검증)
  - **시스템 정보**: 버전 (API에서 제공), 호스트명, OS, 커널, 업타임 표시
  - 키-값 설정 저장 (SQLite `settings` 테이블, UPSERT 패턴)
- **관련 기술**: i18next, bcrypt, TOTP (pquerna/otp), SQLite

---

## 인증 & 보안

| 항목 | 구현 |
|------|------|
| **인증 방식** | JWT 토큰 기반 (HS256, `Authorization: Bearer <token>` 헤더) |
| **토큰 생성** | `golang-jwt/jwt/v5` — username, 발급/만료 시간 클레임 포함 |
| **토큰 만료** | 설정 가능 (기본 24시간, `config.yaml`의 `token_expiry`) |
| **비밀번호 해싱** | bcrypt (golang.org/x/crypto, DefaultCost) |
| **2단계 인증** | TOTP (pquerna/otp) — Google Authenticator 등 호환, QR 코드 지원 |
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
| `server.port` | port | `19443` | 서버 포트 |
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
| `cluster.grpc_port` | grpc_port | `9444` | gRPC 통신 포트 |
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

총 8개. JWT 미들웨어 적용. 장시간 실행 작업의 실시간 진행률 스트리밍용. 클러스터 프록시 시 HTTP 직접 릴레이(5분 타임아웃).

| 경로 | 용도 | 이벤트 형식 |
|------|------|-----------|
| `POST /api/v1/system/update` | SFPanel 자체 업데이트 | JSON `{step, message}` (downloading/verifying/extracting/replacing/restarting/complete) |
| `POST /api/v1/docker/images/pull` | Docker 이미지 풀 | JSON (Docker API 이벤트 그대로) |
| `POST /api/v1/docker/compose/:project/up-stream` | Compose 프로젝트 시작 | JSON `{phase, line}` |
| `POST /api/v1/docker/compose/:project/update-stream` | Compose 스택 풀+재생성 | JSON `{phase, line}` |
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
make dev-api   # Go 백엔드 (:19443)
make dev-web   # Vite 프론트엔드 (:5173, API 프록시 → :19443)

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
