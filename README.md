# SFPanel

경량 서버 관리 웹 패널. 올인원 Go 바이너리로 설치 즉시 사용 가능.

## 주요 기능

- **대시보드** — CPU, 메모리, 디스크, 네트워크 실시간 모니터링 (WebSocket)
- **Docker 관리** — 컨테이너, 이미지, 볼륨, 네트워크, Compose 스택, Hub 검색, 리소스 정리
- **파일 관리** — 브라우저 기반 파일 탐색기, Monaco 에디터, 업로드/다운로드
- **터미널** — xterm.js 기반 웹 터미널 (멀티탭, PTY, 세션 유지, 256KB 스크롤백)
- **로그 뷰어** — 시스템/커스텀 로그 실시간 스트리밍, 구조화 파싱 (auth, ufw, sfpanel)
- **프로세스 관리** — 프로세스 목록, 정렬, 시그널 전송
- **크론 작업** — crontab GUI 관리
- **서비스 관리** — systemd 서비스 시작/중지/재시작/활성화/비활성화, 의존성 확인
- **앱스토어** — Docker Compose 기반 원클릭 앱 설치 (Nextcloud, WordPress, GitLab 등 50+ 앱)
- **패키지 관리** — APT 패키지 검색/설치/업그레이드, Docker/Node.js/Claude/Codex/Gemini 원클릭 설치
- **방화벽** — UFW 규칙 관리, 열린 포트 확인, Fail2ban Jail 관리, Docker 네트워크 방화벽 (DOCKER-USER 체인)
- **네트워크/VPN** — 인터페이스 설정 (DHCP/Static), DNS, 라우팅, 본딩, WireGuard, Tailscale
- **디스크 관리** — 파티션, 파일시스템, LVM, RAID, 스왑, S.M.A.R.T.
- **클러스터** — Raft 합의 기반 멀티노드 클러스터 (자동 리더 선출, mTLS, 노드 간 메트릭 공유)
- **알림 시스템** — 조건 기반 알림 규칙, 채널 (Discord, Telegram), 알림 이력
- **시스템 튜닝** — 성능 프로파일 적용 (확인 워크플로우 포함)
- **셀프 관리** — 웹 업데이트 (SSE 스트리밍 + SHA-256 체크섬 검증), 설정 백업/복원
- **감사 로그** — API 요청 기록, 사용자/IP/경로/상태/노드 추적
- **보안** — JWT 인증 + TOTP 2FA, bcrypt 해시, 로그인 rate limiting (5회 실패 → 5분 차단)
- **다국어** — 한국어 / English (브라우저 자동 감지)
- **데스크톱 앱** — Tauri 기반 Windows/Linux/macOS 네이티브 앱

## 아키텍처

```
Go Binary (Echo v4)
├── REST API (230+ endpoints) + WebSocket (6) + SSE (8 streaming)
├── Embedded React SPA (go:embed)
├── SQLite (10 tables — 인증, 설정, 감사 로그, 메트릭, 알림 외)
├── Docker Go SDK (소켓 직접 통신, 미가용 시 Docker 라우트만 비활성)
├── Compose Manager (filesystem 기반, docker compose CLI)
├── System Metrics (gopsutil, 60초 주기 24시간 히스토리)
└── Cluster (HashiCorp Raft + gRPC + mTLS)
    ├── 합의 기반 구성 동기화 (JWT 시크릿, 관리자 계정)
    ├── 비-리더 API 요청 프록시 (gRPC 30s / SSE HTTP relay 5m)
    ├── WebSocket 릴레이 (원격 노드 터미널/로그/메트릭)
    └── Heartbeat 메트릭 수집 (CPU, 메모리, 디스크, 컨테이너)
```

```
internal/
├── api/
│   ├── router.go           # 라우트 등록
│   ├── middleware/          # JWT, 감사 로그, 클러스터 프록시, 요청 로깅
│   └── response/            # 표준 응답, 에러 코드 (150+), 출력 새니타이징
├── feature/                 # 21개 독립 기능 모듈
│   ├── auth/                # JWT, TOTP 2FA, 비밀번호
│   ├── docker/              # 컨테이너, 이미지, 볼륨, 네트워크
│   ├── compose/             # Docker Compose 스택
│   ├── firewall/            # UFW, Fail2ban, Docker 방화벽
│   ├── disk/                # 파티션, 파일시스템, LVM, RAID, 스왑
│   ├── network/             # 인터페이스, WireGuard, Tailscale
│   ├── packages/            # APT, Docker, Node.js, Claude, Codex, Gemini
│   ├── cluster/             # 클러스터 관리 API
│   ├── alert/               # 알림 채널 (Discord/Telegram), 규칙, 이력
│   ├── system/              # 업데이트, 백업/복원, 튜닝
│   ├── appstore/            # 앱스토어
│   ├── services/            # systemd 서비스
│   ├── files/               # 파일 매니저
│   ├── terminal/            # 웹 터미널 (PTY, 256KB 스크롤백)
│   ├── websocket/           # WebSocket 실시간 데이터
│   ├── monitor/             # 대시보드 메트릭
│   ├── logs/                # 로그 뷰어 + 커스텀 소스
│   ├── cron/                # 크론 작업
│   ├── process/             # 프로세스 관리
│   ├── audit/               # 감사 로그 (50k 롤링)
│   └── settings/            # 패널 설정
├── cluster/                 # Raft, gRPC, TLS, 합의 엔진
├── db/                      # SQLite 마이그레이션, 스키마
├── config/                  # YAML 설정 로딩
├── docker/                  # Docker SDK 클라이언트
├── monitor/                 # 메트릭 히스토리 수집
└── common/
    ├── exec/                # Commander 인터페이스 (테스트용 Mock 포함)
    └── logging/             # slog 구조화 로깅
```

## 기술 스택

| 영역 | 기술 |
|------|------|
| Backend | Go 1.24, Echo v4, SQLite (modernc.org/sqlite, CGO-free) |
| Frontend | React 19, TypeScript 5.9, Vite 7, Tailwind CSS v4, shadcn/ui |
| UI | uplot (차트), xterm.js v6 (터미널), Monaco Editor (코드 에디터) |
| Auth | JWT (golang-jwt/jwt/v5) + TOTP (pquerna/otp) + bcrypt |
| Docker | Docker Go SDK v27.5.1 |
| Cluster | HashiCorp Raft v1.7, gRPC v1.79, mTLS (CA 자동 발급) |
| Monitoring | gopsutil v4, gorilla/websocket |
| Desktop | Tauri 2 (Rust, Windows/Linux/macOS) |
| i18n | 한국어 / English (i18next) |
| E2E Test | Playwright |

## 요구사항

- Linux (x86_64 또는 arm64)
- root 권한 (시스템 관리 명령 실행에 필요)
- Docker (선택사항 — 없이도 패널 자체는 작동)

## 설치

> **SFPanel은 root 권한으로 실행됩니다.** 시스템 관리(Docker, 방화벽, 디스크, 패키지 등)를 위해 root가 필요합니다. 설치 후 반드시 **2FA(TOTP)를 활성화**하여 계정 보안을 강화하세요.

root 계정으로 실행:

```bash
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | bash
```

설치 후:
1. `http://<서버IP>:8443` 접속
2. 셋업 위저드에서 관리자 계정 생성
3. **설정 → 2단계 인증 → 2FA 활성화** (권장)
4. 설정 파일: `/etc/sfpanel/config.yaml`

### 수동 설치

[GitHub Releases](https://github.com/svrforum/SFPanel/releases)에서 바이너리 다운로드 후:

```bash
sudo cp sfpanel /usr/local/bin/sfpanel
sudo chmod 755 /usr/local/bin/sfpanel
sudo mkdir -p /etc/sfpanel /var/lib/sfpanel /var/log/sfpanel
# config.yaml 작성 후:
sudo sfpanel /etc/sfpanel/config.yaml
```

## 설정

`/etc/sfpanel/config.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 8443

database:
  path: "/var/lib/sfpanel/sfpanel.db"

auth:
  jwt_secret: "랜덤-시크릿-키"   # 설치 스크립트가 자동 생성
  token_expiry: "24h"

docker:
  socket: "unix:///var/run/docker.sock"

log:
  level: "info"                  # debug, info, warn, error
  file: "/var/log/sfpanel/sfpanel.log"

cluster:
  enabled: false
  name: ""
  node_id: ""
  node_name: ""
  grpc_port: 9444
  data_dir: "/var/lib/sfpanel/cluster"
  cert_dir: "/etc/sfpanel/cluster"
  advertise_address: ""
  raft_tls: false
```

환경 변수 오버라이드:

| 변수 | 설명 |
|------|------|
| `SFPANEL_PORT` | 서버 포트 |
| `SFPANEL_JWT_SECRET` | JWT 서명 시크릿 |
| `SFPANEL_DB_PATH` | SQLite 데이터베이스 경로 |
| `SFPANEL_LOG_LEVEL` | 로그 레벨 (debug, info, warn, error) |

## CLI 명령어

```bash
sfpanel                           # 기본 config.yaml로 실행
sfpanel /path/to/config.yaml      # 지정 설정 파일로 실행
sfpanel version                   # 버전 정보
sfpanel update                    # GitHub에서 최신 버전으로 업데이트
sfpanel reset                     # 데이터베이스 초기화 (셋업 위저드 재실행)
sfpanel help                      # 도움말
```

### 클러스터 CLI

```bash
sfpanel cluster init [--name NAME] [--advertise IP]   # 클러스터 초기화
sfpanel cluster token [--ttl DURATION]                 # 조인 토큰 생성
sfpanel cluster join ADDR:PORT TOKEN [--advertise IP]  # 클러스터 참여
sfpanel cluster status                                 # 클러스터 상태 확인
sfpanel cluster remove NODE_ID                         # 노드 제거
sfpanel cluster leave                                  # 클러스터 탈퇴
```

모든 클러스터 명령은 `--config PATH` 옵션을 지원합니다 (기본값: `/etc/sfpanel/config.yaml`).

## 서비스 관리

```bash
sudo systemctl status sfpanel    # 상태 확인
sudo systemctl restart sfpanel   # 재시작
sudo systemctl stop sfpanel      # 중지
sudo journalctl -u sfpanel -f    # 실시간 로그
```

## 업그레이드

```bash
# 방법 1: 웹 UI
# 설정 → 패널 업데이트 → 업데이트 확인 → 업데이트 설치

# 방법 2: CLI
sudo sfpanel update

# 방법 3: 설치 스크립트 재실행 (이미 설치된 경우 자동 업그레이드)
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | bash
```

업데이트 시 SHA-256 체크섬 검증을 통해 바이너리 무결성을 확인합니다.

## 백업 / 복원

설정 → 설정 백업에서 웹 UI로 백업/복원 가능.

백업에 포함되는 항목:
- `sfpanel.db` — 관리자 계정(+ TOTP), 설정, Compose 프로젝트 메타, 커스텀 로그 소스, 감사 로그, 메트릭 히스토리, 알림 규칙/채널/이력
- `config.yaml` — 서버 포트, JWT 시크릿, DB 경로, Docker 소켓, 클러스터 설정
- `compose/*` — Docker Compose 프로젝트 파일 (docker-compose.yml, .env)

> Docker 데이터(볼륨, 이미지, 컨테이너)는 백업에 포함되지 않습니다.

## 클러스터

SFPanel은 HashiCorp Raft 합의 알고리즘 기반 멀티노드 클러스터를 지원합니다.

### 특징

- **자동 리더 선출** — 리더 장애 시 자동으로 새 리더 선출
- **mTLS 통신** — 노드 간 gRPC 통신에 자동 발급된 CA 인증서 사용
- **토큰 기반 조인** — 시간 제한 HMAC 서명 토큰으로 노드 인증
- **Zero-Restart 라이프사이클** — 클러스터 생성/참여 시 서비스 재시작 없이 즉시 활성화
- **구성 동기화** — JWT 시크릿, 관리자 계정이 모든 노드에 자동 동기화
- **API 프록시** — 비-리더 노드의 API 요청을 리더에게 자동 릴레이
- **WebSocket 릴레이** — 원격 노드의 터미널, 로그를 릴레이로 접속
- **메트릭 공유** — 각 노드의 CPU, 메모리, 디스크, 컨테이너 메트릭을 클러스터 오버뷰에서 집계
- **클러스터 업데이트** — 롤링/동시 모드로 전체 클러스터 SFPanel 업데이트 (SSE 진행률 스트리밍)

### 클러스터 구성

```bash
# 1. 첫 번째 노드에서 클러스터 초기화
sfpanel cluster init --name my-cluster

# 2. 조인 토큰 생성
sfpanel cluster token

# 3. 다른 노드에서 클러스터 참여
sfpanel cluster join 10.0.0.1:9444 <token>
```

클러스터 생성/참여는 웹 UI 및 CLI 모두에서 **서비스 재시작 없이** 즉시 활성화됩니다 (zero-restart, `JoinEngine` PreFlight → Execute 파이프라인). 탈퇴/해산은 서비스 재시작이 필요하며, `scripts/install.sh`가 설치하는 `Restart=always` systemd 유닛(탈퇴/해산 핸들러는 의도적으로 `os.Exit`하여 supervisor 재기동을 유도) 하에서 안전하게 동작합니다.

## 제거

```bash
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | bash -s -- uninstall
```

바이너리와 서비스만 제거. 설정(`/etc/sfpanel`)과 데이터(`/var/lib/sfpanel`)는 보존됩니다.

## 개발

### 빌드

```bash
# 전체 빌드 (프론트엔드 + 백엔드 → 단일 바이너리)
make build

# 또는 수동:
cd web && npm install && npm run build
cd .. && go build -o sfpanel ./cmd/sfpanel
```

### 개발 모드

```bash
# 터미널 1: 프론트엔드 (핫 리로드, API 프록시 → :8443)
cd web && npm run dev

# 터미널 2: 백엔드 (반드시 root)
sudo go run ./cmd/sfpanel
```

> 수동 실행 환경 주의: systemd 없이 바이너리를 직접 띄운 경우, 웹 UI에서 **클러스터 탈퇴/해산** 또는 **패널 업데이트**를 누르면 백엔드가 의도적으로 종료된 뒤 재기동될 supervisor가 없어 패널이 꺼진 채로 남습니다. 이 버튼들은 `Restart=always` systemd 유닛(`scripts/install.sh`로 설치) 하에서만 안전합니다. 클러스터 생성/참여는 재시작 없이 즉시 활성화되므로 수동 환경에서도 안전합니다.

### 테스트

```bash
make test            # Go 단위 테스트
make test-coverage   # 커버리지 리포트
make lint            # Go + 프론트엔드 린트
make ci              # 전체 CI 파이프라인 (lint + test + build)

# E2E 테스트 (Playwright)
cd e2e && npm run test          # 헤드리스
cd e2e && npm run test:headed   # 브라우저 UI
```

## API

모든 REST 응답은 통일된 JSON 형식:

```json
{"success": true, "data": {...}}
{"success": false, "error": {"code": "ERROR_CODE", "message": "..."}}
```

- 인증: `Authorization: Bearer <JWT>` 헤더
- WebSocket 인증: 쿼리 파라미터 `?token=<JWT>`
- 클러스터 원격 노드 호출: 모든 보호 라우트에 `?node=<nodeID>` 추가 시 `ClusterProxyMiddleware`가 대상 노드로 투명 포워딩 (gRPC 30s, SSE/WS는 HTTP/WS 직접 릴레이)
- SSE 스트리밍 엔드포인트 8개 (시스템 업데이트, Docker 이미지 풀, Compose up/update, 패키지·VPN 설치, 클러스터 업데이트)

## 문서

| 문서 | 내용 |
|------|------|
| [docs/specs/tech-features.md](docs/specs/tech-features.md) | 전체 기능 상세 + 기술 스택 |
| [docs/specs/api-spec.md](docs/specs/api-spec.md) | REST/SSE 엔드포인트 전수 + 요청·응답 스키마 |
| [docs/specs/websocket-spec.md](docs/specs/websocket-spec.md) | WebSocket 6개 + SSE 8개 메시지 스키마 + 클러스터 릴레이 |
| [docs/specs/db-schema.md](docs/specs/db-schema.md) | SQLite 10개 테이블 + 보존 정책 + 마이그레이션 |
| [docs/specs/frontend-spec.md](docs/specs/frontend-spec.md) | 페이지/컴포넌트/라우팅/상태/빌드 |
| [CLAUDE.md](CLAUDE.md) | 기여자 가이드 (코드 규약, 테스트 범위, 클러스터 인지) |

## 보안 주의사항

SFPanel은 **root 권한으로 실행**되며, 서버 전체를 관리할 수 있는 강력한 도구입니다. 반드시 아래 보안 조치를 적용하세요.

- **2FA 필수 권장**: 설정 → 2단계 인증에서 TOTP 앱(Google Authenticator 등)으로 2FA를 활성화하세요. 패널이 탈취되면 서버 전체가 위험합니다.
- **강력한 비밀번호**: 초기 설정 시 충분히 긴 비밀번호를 사용하세요 (최소 12자 이상 권장)
- **역방향 프록시 + TLS**: 프로덕션에서는 Nginx/Caddy 등으로 HTTPS를 적용하세요. 기본 포트(8443)는 plain HTTP입니다.
- **접근 제한**: 방화벽(UFW)으로 패널 포트를 신뢰할 수 있는 IP에서만 허용하세요
- **JWT 시크릿**: 설치 스크립트가 32자 랜덤 시크릿을 자동 생성합니다. 수동 설치 시 반드시 고유한 값을 설정하세요
- **로그인 보호**: 5회 실패 시 5분간 IP 차단 (rate limiting 내장)
- **클러스터 보안**: 노드 간 통신은 mTLS로 암호화. 조인 토큰은 시간 제한 HMAC 서명

## 후원

SFPanel이 유용하다면 커피 한 잔으로 개발을 응원해주세요.

[![Buy Me A Coffee](https://img.shields.io/badge/Buy%20Me%20A%20Coffee-FFDD00?style=for-the-badge&logo=buy-me-a-coffee&logoColor=black)](https://buymeacoffee.com/svrforum)

## 라이선스

MIT
