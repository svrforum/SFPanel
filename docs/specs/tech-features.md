# SFPanel 기술 스택 & 기능 스펙

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
| Recharts | ^3.7.0 | 시스템 메트릭 차트 (CPU/메모리 시계열) |
| xterm.js | ^6.0.0 (`@xterm/xterm`) | 웹 기반 터미널 에뮬레이터 |
| xterm Addons | fit ^0.11.0, search ^0.16.0, web-links ^0.12.0 | 터미널 자동 크기 조절, 검색, 링크 감지 |
| Monaco Editor | ^4.7.0 (`@monaco-editor/react`) | 파일 편집기 / Compose YAML 편집 (구문 강조, 자동 완성) |
| i18next | ^25.8.13 | 다국어 지원 프레임워크 |
| react-i18next | ^16.5.4 | React용 i18n 바인딩 |
| i18next-browser-languagedetector | ^8.2.1 | 브라우저 언어 자동 감지 |
| Lucide React | ^0.575.0 | 아이콘 라이브러리 |
| Sonner | ^2.0.7 | 토스트 알림 |
| next-themes | ^0.4.6 | 다크/라이트 테마 전환 |
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

---

## 기능 목록

### 1. 시스템 대시보드

- **설명**: 서버의 전체 상태를 한눈에 파악하는 실시간 모니터링 대시보드
- **주요 기능**:
  - 실시간 CPU/메모리/디스크/네트워크 메트릭 카드 (WebSocket으로 2초 간격 업데이트)
  - CPU/메모리 사용률 시계열 차트 (최대 24시간, 30초 간격 2880 포인트)
  - 호스트 정보 표시 (호스트명, OS, 플랫폼, 커널, 업타임, CPU 코어 수)
  - 네트워크 I/O 속도 (송/수신 bytes/sec) 및 누적 전송량
  - Swap 메모리 사용량
  - Docker 컨테이너 요약 (실행 중/중지/전체 개수, 최근 5개 컨테이너 상태)
  - Top 10 프로세스 (CPU 사용률 기준, 10초 간격 자동 갱신)
  - 최근 시스템 로그 (syslog 마지막 8줄)
  - 빠른 액션 바로가기 (파일, Docker, 패키지, Cron, 로그)
  - WebSocket 연결 상태 표시 (Live/Disconnected)
- **관련 기술**: gopsutil v4, gorilla/websocket, Recharts, Echo WebSocket

### 2. Docker 관리

- **설명**: Docker 리소스의 전체 생명주기를 웹 UI에서 관리
- **주요 기능**:
  - **컨테이너**: 목록 조회 (전체/실행 중/중지), 상세 검사 (포트/환경변수/마운트/네트워크), 시작/중지/재시작/삭제, CPU/메모리 통계, 실시간 로그 스트리밍 (WebSocket), 컨테이너 내부 셸 접속 (WebSocket + exec, TTY 리사이즈 지원)
  - **이미지**: 로컬 이미지 목록, 이미지 풀 (레지스트리에서 다운로드), 이미지 삭제 (강제)
  - **볼륨**: 볼륨 목록, 생성, 삭제 (강제)
  - **네트워크**: 네트워크 목록, 생성 (드라이버 선택: bridge 기본), 삭제
  - **Docker Compose**: 프로젝트 CRUD (YAML 파일 디스크 저장 + DB 추적), Monaco 에디터로 YAML 편집, `docker compose up -d` / `docker compose down` 실행, 프로젝트 상태 관리 (running/stopped)
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
  - 로그 소스별 파일 존재 여부 및 크기 표시
  - 줄 수 선택 (100, 500, 1000, 5000줄)
  - 실시간 스트리밍 모드 (WebSocket + `tail -f`, Live 토글)
  - 로그 레벨 감지 및 색상 구분 (ERROR/FATAL=빨강, WARN=노랑, INFO=파랑, DEBUG=회색)
  - 로그 내 텍스트 검색 (하이라이트, 이전/다음 매치 탐색, Ctrl+F 단축키)
  - 자동 스크롤 토글
  - 로그 파일 다운로드 (.log 파일)
  - 줄 번호 표시
  - 허용 목록 기반 접근 제어 (임의 파일 접근 차단)
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

### 10. 방화벽 관리

- **설명**: UFW 방화벽 및 Fail2ban 침입 방지 시스템을 웹 UI에서 관리
- **주요 기능**:
  - **UFW 방화벽**: 활성화/비활성화 토글, 규칙 목록 조회 (번호/대상/동작/소스/코멘트/IPv6 구분), 규칙 추가 (action/port/protocol/from/to/comment), 규칙 삭제
  - **리스닝 포트**: `ss` 명령어로 TCP/UDP 리스닝 포트 조회 (프로토콜/주소/포트/PID/프로세스), 포트에서 직접 UFW 규칙 추가 지원
  - **Fail2ban**: 설치 상태 확인 및 원클릭 설치 (`apt-get install -y fail2ban`), jail 목록 및 상태 조회, jail 활성화/비활성화, 차단 IP 해제 (unban), jail 설정 확인 (maxretry, bantime, findtime)
  - 입력값 검증: 포트 번호/범위, IP/CIDR 주소, 프로토콜, action, jail 이름 등 서버 측 정규식 검증
- **관련 기술**: UFW CLI (`ufw`), Fail2ban CLI (`fail2ban-client`), ss 명령어

### 11. 설정

- **설명**: 패널 설정 및 사용자 계정 관리
- **주요 기능**:
  - **언어 설정**: 영어/한국어 전환 (i18next 브라우저 언어 감지)
  - **터미널 타임아웃**: 유휴 터미널 세션 자동 종료 시간 (분 단위, 0=무제한)
  - **비밀번호 변경**: 현재 비밀번호 검증 후 변경 (최소 8자)
  - **2단계 인증(2FA)**: TOTP 설정/활성화 (QR 코드 표시, 시크릿 키 표시, 6자리 코드 검증)
  - **시스템 정보**: 버전, 호스트명, OS, 커널, 업타임 표시
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
| **로그 접근 제어** | 허용 목록(allowlist) 기반 — 사전 정의된 로그 소스만 읽기 가능 |
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
| `server.port` | port | `8443` | 서버 포트 |
| `database.path` | path | `./sfpanel.db` | SQLite 데이터베이스 파일 경로 |
| `auth.jwt_secret` | jwt_secret | (없음) | JWT 서명 시크릿 (반드시 변경 필요) |
| `auth.token_expiry` | token_expiry | `24h` | JWT 토큰 만료 시간 (Go duration 형식) |
| `docker.socket` | socket | `unix:///var/run/docker.sock` | Docker 소켓 경로 |
| `log.level` | level | `info` | 로그 레벨 (debug, info, warn, error) |
| `log.file` | file | (없음) | 로그 파일 경로 |

### 런타임 설정 (SQLite 저장)

| 키 | 기본값 | 설명 |
|-----|--------|------|
| `terminal_timeout` | `30` | 터미널 유휴 세션 타임아웃 (분, 0=무제한) |

---

## 데이터베이스 스키마

SQLite (WAL 모드, 5초 busy timeout) — 자동 마이그레이션 5개 테이블:

| 테이블 | 용도 |
|--------|------|
| `admin` | 관리자 계정 (username, password hash, TOTP secret) |
| `sites` | 웹사이트 관리 (domain, doc_root, PHP/SSL 설정) — 미래 확장용 |
| `compose_projects` | Docker Compose 프로젝트 (name, yaml_path, status) |
| `sessions` | 세션 토큰 해시 (미래 확장용) |
| `settings` | 키-값 설정 저장소 |

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
| GET | `/api/v1/system/info` | 시스템 정보 + 현재 메트릭 |
| GET | `/api/v1/system/metrics-history` | 24시간 메트릭 히스토리 |
| GET | `/api/v1/system/processes` | Top 10 프로세스 |
| GET | `/api/v1/system/processes/list` | 전체 프로세스 목록 (검색/정렬) |
| POST | `/api/v1/system/processes/:pid/kill` | 프로세스 시그널 전송 |
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
| GET | `/api/v1/packages/updates` | 업데이트 가능 패키지 조회 |
| POST | `/api/v1/packages/upgrade` | 패키지 업그레이드 |
| POST | `/api/v1/packages/install` | 패키지 설치 |
| POST | `/api/v1/packages/remove` | 패키지 제거 |
| GET | `/api/v1/packages/search` | 패키지 검색 |
| GET | `/api/v1/packages/docker-status` | Docker 설치 상태 확인 |
| POST | `/api/v1/packages/install-docker` | Docker 설치 (SSE 스트리밍) |
| GET | `/api/v1/firewall/status` | UFW 상태 조회 |
| POST | `/api/v1/firewall/enable` | UFW 활성화 |
| POST | `/api/v1/firewall/disable` | UFW 비활성화 |
| GET | `/api/v1/firewall/rules` | UFW 규칙 목록 |
| POST | `/api/v1/firewall/rules` | UFW 규칙 추가 |
| DELETE | `/api/v1/firewall/rules/:number` | UFW 규칙 삭제 |
| GET | `/api/v1/firewall/ports` | 리스닝 포트 목록 (ss) |
| GET | `/api/v1/fail2ban/status` | Fail2ban 설치/실행 상태 |
| POST | `/api/v1/fail2ban/install` | Fail2ban 설치 |
| GET | `/api/v1/fail2ban/jails` | Fail2ban jail 목록 |
| GET | `/api/v1/fail2ban/jails/:name` | Fail2ban jail 상세 |
| POST | `/api/v1/fail2ban/jails/:name/enable` | Fail2ban jail 활성화 |
| POST | `/api/v1/fail2ban/jails/:name/disable` | Fail2ban jail 비활성화 |
| POST | `/api/v1/fail2ban/jails/:name/unban` | Fail2ban IP 차단 해제 |

### Docker 전용 (Docker 사용 가능 시에만 등록)

| 메서드 | 경로 | 설명 |
|--------|------|------|
| GET | `/api/v1/docker/containers` | 컨테이너 목록 |
| GET | `/api/v1/docker/containers/:id/inspect` | 컨테이너 상세 정보 |
| GET | `/api/v1/docker/containers/:id/stats` | 컨테이너 CPU/메모리 통계 |
| POST | `/api/v1/docker/containers/:id/start` | 컨테이너 시작 |
| POST | `/api/v1/docker/containers/:id/stop` | 컨테이너 중지 |
| POST | `/api/v1/docker/containers/:id/restart` | 컨테이너 재시작 |
| DELETE | `/api/v1/docker/containers/:id` | 컨테이너 삭제 |
| GET | `/api/v1/docker/images` | 이미지 목록 |
| POST | `/api/v1/docker/images/pull` | 이미지 풀 |
| DELETE | `/api/v1/docker/images/:id` | 이미지 삭제 |
| GET | `/api/v1/docker/volumes` | 볼륨 목록 |
| POST | `/api/v1/docker/volumes` | 볼륨 생성 |
| DELETE | `/api/v1/docker/volumes/:name` | 볼륨 삭제 |
| GET | `/api/v1/docker/networks` | 네트워크 목록 |
| POST | `/api/v1/docker/networks` | 네트워크 생성 |
| DELETE | `/api/v1/docker/networks/:id` | 네트워크 삭제 |
| GET | `/api/v1/docker/compose` | Compose 프로젝트 목록 |
| POST | `/api/v1/docker/compose` | Compose 프로젝트 생성 |
| GET | `/api/v1/docker/compose/:project` | Compose 프로젝트 조회 (YAML 포함) |
| PUT | `/api/v1/docker/compose/:project` | Compose YAML 업데이트 |
| DELETE | `/api/v1/docker/compose/:project` | Compose 프로젝트 삭제 |
| POST | `/api/v1/docker/compose/:project/up` | Compose Up (detached) |
| POST | `/api/v1/docker/compose/:project/down` | Compose Down |

### WebSocket 엔드포인트 (쿼리 파라미터 토큰 인증)

| 경로 | 설명 |
|------|------|
| `/ws/metrics` | 실시간 시스템 메트릭 (2초 간격) |
| `/ws/logs` | 실시간 로그 스트리밍 (`tail -f`) |
| `/ws/terminal` | 서버 터미널 (PTY 세션, 재연결 지원) |
| `/ws/docker/containers/:id/logs` | 컨테이너 로그 스트리밍 |
| `/ws/docker/containers/:id/exec` | 컨테이너 셸 접속 (exec) |

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

| 페이지 | 파일 | 설명 |
|--------|------|------|
| Login | `web/src/pages/Login.tsx` | 로그인 (username + password + TOTP) |
| Setup | `web/src/pages/Setup.tsx` | 최초 관리자 계정 생성 위저드 |
| Dashboard | `web/src/pages/Dashboard.tsx` | 시스템 대시보드 |
| Docker | `web/src/pages/Docker.tsx` | Docker 관리 (탭: Containers, Images, Volumes, Networks, Compose) |
| DockerContainers | `web/src/pages/docker/DockerContainers.tsx` | 컨테이너 관리 |
| DockerImages | `web/src/pages/docker/DockerImages.tsx` | 이미지 관리 |
| DockerVolumes | `web/src/pages/docker/DockerVolumes.tsx` | 볼륨 관리 |
| DockerNetworks | `web/src/pages/docker/DockerNetworks.tsx` | 네트워크 관리 |
| DockerCompose | `web/src/pages/docker/DockerCompose.tsx` | Compose 프로젝트 관리 |
| Terminal | `web/src/pages/Terminal.tsx` | 웹 터미널 (다중 탭) |
| Files | `web/src/pages/Files.tsx` | 파일 관리자 |
| Logs | `web/src/pages/Logs.tsx` | 로그 뷰어 |
| CronJobs | `web/src/pages/CronJobs.tsx` | Cron 작업 관리 |
| Processes | `web/src/pages/Processes.tsx` | 프로세스 관리 |
| Packages | `web/src/pages/Packages.tsx` | 패키지 관리 + Docker 설치 |
| Firewall | `web/src/pages/Firewall.tsx` | 방화벽 관리 (탭: UFW Rules, Open Ports, Fail2ban) |
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
# 2. go build -o sfpanel ./cmd/sfpanel      → 바이너리 생성 (~25MB)

# 개발 모드
make dev-api   # Go 백엔드 (:8443)
make dev-web   # Vite 프론트엔드 (:5173, API 프록시 → :8443)

# 린트
make lint      # golangci-lint + eslint
```

### CI/CD 파이프라인

- **트리거**: `v*` 태그 푸시 (예: `v0.1.0`)
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

- **Docker 비의존**: Docker가 없어도 패널 자체는 정상 동작 (Docker 기능만 비활성화)
- **자동 마이그레이션**: 첫 실행 시 SQLite 스키마 자동 생성 (5개 테이블)
- **백그라운드 수집**: 30초 간격 메트릭 히스토리 수집 (인메모리, 24시간 보관)
- **세션 정리**: 1분 간격 유휴 터미널 세션 자동 정리
- **SPA 라우팅**: 모든 경로에 대해 `index.html` 폴백 제공 (API/WS 경로 제외)
- **CORS**: 개발 모드용 localhost:5173 허용

---

## CLI 커맨드

SFPanel 바이너리는 서버 실행 외에도 관리 명령을 지원합니다.

| 커맨드 | 설명 |
|--------|------|
| `sfpanel [config.yaml]` | 패널 서버 시작 (기본 설정: `config.yaml`) |
| `sfpanel version` | 버전 정보 출력 (버전, 커밋 해시, 빌드 날짜) |
| `sfpanel update` | GitHub Releases에서 최신 버전 다운로드 및 자동 업데이트. 현재 아키텍처(amd64/arm64) 자동 감지. systemd 서비스 실행 중이면 자동 재시작. |
| `sfpanel reset` | 데이터베이스 삭제 및 초기화 (셋업 위저드로 복귀). 확인 프롬프트(y/N) 표시. |
| `sfpanel help` | 사용법 도움말 출력 |

**update 동작 과정:**
1. GitHub API에서 최신 릴리즈 버전 조회
2. 현재 버전과 비교 (동일하면 "Already up to date" 출력)
3. 사용자 확인(y/N) 후 바이너리 다운로드 (tar.gz)
4. 현재 바이너리 경로에 atomic replace (`.new` 임시 파일 → rename)
5. systemd 서비스 활성 상태이면 `systemctl restart sfpanel` 자동 실행
