# SFPanel

경량 서버 관리 웹 패널. 올인원 Go 바이너리로 설치 즉시 사용 가능.

## 주요 기능

- **대시보드** — CPU, 메모리, 디스크, 네트워크 실시간 모니터링 (WebSocket)
- **Docker 관리** — 컨테이너, 이미지, 볼륨, 네트워크, Compose 스택, Hub 검색, 리소스 정리
- **파일 관리** — 브라우저 기반 파일 탐색기, Monaco 에디터, 업로드/다운로드
- **터미널** — xterm.js 기반 웹 터미널 (멀티탭, PTY, 세션 유지)
- **로그 뷰어** — 시스템/커스텀 로그 실시간 스트리밍, 구조화 파싱 (auth, ufw, sfpanel)
- **프로세스 관리** — 프로세스 목록, 정렬, 시그널 전송
- **크론 작업** — crontab GUI 관리
- **서비스 관리** — systemd 서비스 시작/중지/재시작/활성화/비활성화, 의존성 확인
- **앱스토어** — Docker Compose 기반 원클릭 앱 설치 (Nextcloud, WordPress, GitLab 등 50+ 앱)
- **패키지 관리** — APT 패키지 검색/설치/업그레이드, Docker 원클릭 설치
- **방화벽** — UFW 규칙 관리, 열린 포트 확인, Fail2ban Jail 관리, Docker 네트워크 방화벽
- **네트워크/VPN** — 인터페이스 설정 (DHCP/Static), DNS, 라우팅, 본딩, WireGuard, Tailscale
- **디스크 관리** — 파티션, 파일시스템, LVM, RAID, 스왑, S.M.A.R.T.
- **셀프 관리** — 웹 업데이트 (SSE 스트리밍 + SHA-256 체크섬 검증), 설정 백업/복원
- **감사 로그** — API 요청 기록, 사용자/IP/경로/상태 추적
- **보안** — JWT 인증 + TOTP 2FA, bcrypt 해시, WebSocket subprotocol 인증, 로그인 rate limiting

## 아키텍처

```
Go Binary (Echo v4)
├── REST API (168 endpoints) + WebSocket (5 endpoints)
├── Embedded React SPA (go:embed)
├── SQLite (설정, 인증, 감사 로그, 메트릭 히스토리)
├── Docker Go SDK (소켓 직접 통신)
├── Compose Manager (filesystem 기반, docker compose CLI)
└── System Metrics (gopsutil, 30초 주기 히스토리)
```

## 기술 스택

| 영역 | 기술 |
|------|------|
| Backend | Go 1.24, Echo v4, SQLite (modernc.org/sqlite CGO-free) |
| Frontend | React 19, TypeScript, Vite 7, Tailwind CSS v4, shadcn/ui |
| UI | Recharts (차트), xterm.js v6 (터미널), Monaco Editor (코드 에디터) |
| Auth | JWT (golang-jwt/jwt/v5) + TOTP (pquerna/otp) + bcrypt |
| Docker | Docker Go SDK v27.5.1 |
| Monitoring | gopsutil v4, gorilla/websocket |
| i18n | 한국어 / English |

## 요구사항

- Linux (x86_64 또는 arm64)
- root 권한 (시스템 관리 명령 실행에 필요)
- Docker (선택사항 — 없이도 패널 자체는 작동)

## 설치

> ⚠️ **SFPanel은 root 권한으로 실행됩니다.** 시스템 관리(Docker, 방화벽, 디스크, 패키지 등)를 위해 root가 필요합니다. 설치 후 반드시 **2FA(TOTP)를 활성화**하여 계정 보안을 강화하세요.

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
  level: "info"
  file: "/var/log/sfpanel/sfpanel.log"
```

## CLI 명령어

```bash
sfpanel                     # 기본 config.yaml로 실행
sfpanel /path/to/config.yaml  # 지정 설정 파일로 실행
sfpanel version             # 버전 정보
sfpanel update              # GitHub에서 최신 버전으로 업데이트
sfpanel reset               # 데이터베이스 초기화 (셋업 위저드 재실행)
sfpanel help                # 도움말
```

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
- `sfpanel.db` — 관리자 계정, 세션, 설정, 감사 로그, 메트릭 히스토리
- `config.yaml` — 서버 포트, JWT 시크릿, DB 경로, Docker 소켓
- `compose/*` — Docker Compose 프로젝트 파일 (docker-compose.yml, .env)

> Docker 데이터(볼륨, 이미지, 컨테이너)는 백업에 포함되지 않습니다.

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

### 린트

```bash
golangci-lint run ./...
cd web && npm run lint
```

## API

모든 응답은 통일된 JSON 형식:

```json
{"success": true, "data": {...}}
{"success": false, "error": {"code": "...", "message": "..."}}
```

인증: `Authorization: Bearer <JWT>` 헤더. WebSocket은 subprotocol(`sfpanel.jwt.<token>`) 기반 인증.

## ⚠️ 보안 주의사항

SFPanel은 **root 권한으로 실행**되며, 서버 전체를 관리할 수 있는 강력한 도구입니다. 반드시 아래 보안 조치를 적용하세요.

- **2FA 필수 권장**: 설정 → 2단계 인증에서 TOTP 앱(Google Authenticator 등)으로 2FA를 활성화하세요. 패널이 탈취되면 서버 전체가 위험합니다.
- **강력한 비밀번호**: 초기 설정 시 충분히 긴 비밀번호를 사용하세요 (최소 12자 이상 권장)
- **역방향 프록시 + TLS**: 프로덕션에서는 Nginx/Caddy 등으로 HTTPS를 적용하세요. 기본 포트(8443)는 plain HTTP입니다.
- **접근 제한**: 방화벽(UFW)으로 패널 포트를 신뢰할 수 있는 IP에서만 허용하세요
- **JWT 시크릿**: 설치 스크립트가 32자 랜덤 시크릿을 자동 생성합니다. 수동 설치 시 반드시 고유한 값을 설정하세요
- **로그인 보호**: 5회 실패 시 5분간 IP 차단 (rate limiting 내장)

## 후원

SFPanel이 유용하다면 커피 한 잔으로 개발을 응원해주세요.

[![Buy Me A Coffee](https://img.shields.io/badge/Buy%20Me%20A%20Coffee-FFDD00?style=for-the-badge&logo=buy-me-a-coffee&logoColor=black)](https://buymeacoffee.com/svrforum)

## 라이선스

MIT
