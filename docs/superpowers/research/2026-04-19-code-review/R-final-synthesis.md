# SFPanel 전체 코드 리뷰 종합 (R-final)

작성일: 2026-04-19 · 기반: R0 + R1~R10 · 대상 버전: v0.9.0
근거 파일: `docs/superpowers/research/2026-04-19-code-review/*` 12개

## 한 줄 결론

> **기본 설계는 견실하다.** SQL 파라미터 바인딩, bcrypt cost 12, JWT alg pinning, response.OK/Fail 일관 사용, Commander DI 패턴, Raft FSM 정합성 등 핵심 규약은 대부분 지켜져 있다. 그러나 **"root로 실행되는 시스템 관리 도구"라는 신뢰 경계에 대한 방어는 불충분**하다 — 설치 스크립트 무결성 미검증, AppStore/파일 API의 호스트 루트 탈출 경로, XSS + localStorage JWT 조합, 클러스터 TLS 완화 경로가 누적돼 있다. 그래도 수정 대부분이 국소적이며, **4~5개 집중 PR로 대부분의 P0/P1을 정리 가능**하다.

## 전체 분포

| 단계 | P0 | P1 | P2 | 비고 |
|------|----|----|----|------|
| R0 (종합) | 5 | 24 | 10 | F-08 취소 반영 |
| R1 install | 0 | 8 | 6 | |
| R2 auth+settings | 0 | 4 | 4 | |
| R3 files+logs | **1** | 2 | 1 | /etc/cron.d 쓰기 신규 P0 |
| R4 docker+compose | 1 | 4 | 2 | |
| R5 firewall+net+disk | 2 | 5 | 0 | |
| R6 pkg+appstore+sys | 2 | 5 | 1 | |
| R7 streaming+monitor | 0 | 2 | 2 | R0 대부분 재확인 |
| R8 cluster | 0 | 2 | 3 | R0 P0 2건 재확인 |
| R9 작은 모듈 | 0 | 5 | 0 | |
| R10 frontend | 1 | 5 | 0 | marked README XSS |

**전체 P0 누계**: 12건 (R0 5건 + 신규 7건). 그중 일부는 동일 이슈의 다른 측면(AppStore advanced, install 무결성 등).

## 최우선 P0 (원격/권한상승 직결)

### 1. `/etc/cron.d` 등 시스템 디렉토리에 파일 쓰기 가능 (R3 N-01) 🔴 새로 발견
`internal/feature/files/handler.go:32-105`의 `isCriticalPath`가 exact-match 맵. `validatePathForWrite("/etc/cron.d/backdoor")`는 부모 `/etc/cron.d`가 맵에 없어 통과. **인증된 관리자 → 호스트 루트 즉시 상승**:
- `/etc/cron.d/*` → root cron
- `/etc/sudoers.d/*` → sudo 탈취
- `/usr/local/bin/*` → 시스템 바이너리 교체

**수정**: prefix 체크로 전환 (한 줄 변경). R0 P0 목록에 추가해야 할 수준.

### 2. AppStore "Advanced" 모드 (R0 F-09 = R6 C-02) 🔴
`internal/feature/appstore/handler.go:571-593`. 임의 compose YAML 실행, `privileged`/`pid:host`/`/:/hostfs` 전부 허용. 프런트 경고 UI도 없음 (R10 I-4).
**수정**: (1) YAML 파싱 후 위험 패턴 차단, (2) 고권한 재인증, (3) 별도 super-admin 권한 중 선택.

### 3. 설치/서드파티 스크립트 무결성 미검증 (R1 P1-1, R6 C-01) 🔴
- `scripts/install.sh`: 바이너리 tar 다운로드 후 SHA-256 검증 없음 (`checksums.txt` 제공되는데 미사용)
- `packages/handler.go:432/1013`: get.docker.com, claude.ai install.sh를 hash 검증 없이 root 실행
- NVM install.sh도 동일

**수정**: install.sh에 `checksums.txt` 검증 추가 (Go `updatePanel()` 동일 패턴). 서드파티 스크립트는 하드코딩 해시 + `sha256sum -c`.

### 4. 클러스터 TLS 검증 비활성화 (R0 F-01/F-02, R8 재확인) 🔴
- `proxy.go:34`, `ws_relay.go:53` `InsecureSkipVerify: true`
- `tls.go:173` gRPC 서버 `VerifyClientCertIfGiven`

**영향**: 동일 네트워크 MITM → `X-SFPanel-Internal-Proxy` secret 탈취 → JWT 우회 → 전체 클러스터 장악.

**수정**: `TLSManager.ClientTLSConfig()`로 교체 + gRPC `RequireAndVerifyClientCert` + PreFlight/Join은 인터셉터 또는 별도 포트.

### 5. 프런트엔드 XSS via marked README (R10 C-1) 🔴
`pages/AppStoreDetail.tsx:106-135` `RenderedReadme`가 외부 GitHub raw README를 `marked` 파싱 후 sanitize 없이 HTML 주입. JWT가 `localStorage`이므로 즉시 탈취.
**수정**: `dompurify` 도입.

### 6. `runComposeStream` 타임아웃 없음 (R4 C-01) 🟠
스트리밍 `docker compose pull`이 영구 hang. nginx 역프록시 버퍼링이 ctx 취소 막으면 무한 실행.
**수정**: `context.WithTimeout(ctx, 30*time.Minute)`.

### 7. Tailscale authkey가 `ps`/`/proc/cmdline`에 노출 (R5 C-01) 🟠
`tailscale up --authkey=<key>` 전달 → 30일 크리덴셜이 타 로컬 사용자에 노출.
**수정**: `cmd.Env`의 `TS_AUTHKEY` 또는 stdin.

## 핵심 P1 묶음 (한 번에 해결 권장)

### 묶음 α — 인증·세션 강화
- **R2 A-01** TOTP replay 보호 (30초 내 재사용 가능)
- **R2 A-02** `Disable2FA`에 TOTP 재확인 추가
- **R2 A-03** 클러스터 Join 시 `admin_totp_secret` proto 필드 추가 + follower DB 동기화
- **R2 A-04** `ChangePassword`/`Disable2FA` rate limit
- **R1 P4-1** 로그인 감사 로그 기록 (username/IP만, password 제외)
- **R1 P5-1** `?token=` 쿼리 파라미터를 `/files/download`로 한정

### 묶음 β — 파일 시스템 경계
- **R3 N-01** `isCriticalPath` prefix 체크 (P0, 최우선)
- **R0 F-07** `ListDir`에 `readProtectedPaths` 적용
- **R0 F-06** 커스텀 로그 소스에서 `/home/`, `/tmp/` 제거
- **R3 N-02** Scanner 버퍼 256KB로 확장
- **R3 N-03** `safeWSWriter` 적용
- **R3 N-04** cleanPath 저장

### 묶음 γ — 스트리밍/WS 라이프사이클
- **R0 P0-1/P0-2** `safeWSWriter` 공용 추출 + logs/terminal 적용
- **R0 P1-2** `CleanupTerminalSessions` ctx 파라미터
- **R0 P1-4/P1-7** monitor history `sync.Once` + ctx
- **R0 P1-6** WS relay CloseMessage에 `SetWriteDeadline`
- **R0 P1-10** `ClusterUpdate`에 request ctx 체크
- **R7 N-1** scrollback replay 순서 수정
- **R7 N-2** `safeWSWriter.WriteMessage`에 WriteDeadline
- **R4 C-01** runComposeStream 타임아웃
- **R0 P0-D** alert 매니저 Shutdown 훅 (R9 I-1/I-4/I-5도 함께)

### 묶음 δ — Sanitize + 명령 출력 노출
- **R0 S-06** firewall/cron/system/services에서 stderr 원문 노출 — `SanitizeOutput` 적용
- **R9 I-3** services stderr (5 핸들러)
- **R6 I-3** apt/docker output raw
- **R4 M-1** PruneAll sanitize
- **R9 I-2** Discord webhook URL prefix 검증
- **R9 I-1** Telegram HTML parse mode 또는 이스케이프

### 묶음 ε — 입력 검증 강화
- **R4 I-01** 컨테이너/이미지/네트워크 ID 검증
- **R4 I-02/I-03** 검색/log tail 상한
- **R5 I-01** UFW 코멘트 `#` 제거
- **R5 I-02** netplan `try --timeout`
- **R5 I-03** swap tmpfs 차단
- **R5 I-04** WireGuard 설정 원자 쓰기
- **R5 I-05** `getJailInfo` 검증
- **R2 A-06** username 검증
- **R0 S-11/R1 P2-3** JWT secret 길이 검증

### 묶음 ζ — 배포/설치 강화
- **R1 P1-1** 체크섬 검증
- **R1 P1-2** 서비스 중단 순서
- **R1 P1-3** `/var/lib/sfpanel` 0700
- **R1 P1-6** 설치 후 상태 확인
- **R1 P5-2** CORS 설정 파일로 이동
- **R6 I-01** checksums.txt GPG 서명
- **R6 I-02** restore tar 심볼릭 링크 필터
- **R6 I-04** tuning 롤백 전역 변수 동시 요청 가드

## 권장 PR 순서 (현실적 스프린트)

| # | 내용 | 난이도 | 블래스트 |
|---|------|--------|---------|
| 1 | **R3 N-01 파일 critical prefix 체크** + R0 F-07 readProtectedPaths 확장 + R0 F-06 log source 축소 | 소 | 적음 |
| 2 | **R10 C-1 DOMPurify 도입** + R10 I-1 setupChecked 재설정 | 소 | 프런트 한정 |
| 3 | **install.sh 체크섬 + 순서 + 권한 + 상태 확인** (R1 P1-1/2/3/6) | 중 | 설치 스크립트만 |
| 4 | **라이프사이클 통합**: alert Stop + ticker Stop + ctx 전파 + ws_relay WriteDeadline + safeWSWriter 공용 추출 (묶음 γ) | 중 | main.go 수정 |
| 5 | **Sanitize 일괄 적용 + 인증 묶음 α** (CPU rate limit, TOTP replay, Disable2FA 재확인) | 중 | 핸들러 다수 |
| 6 | **클러스터 TLS 엄격화** (R0 P0-A, R8 F-01/F-02) — QA 철저히 | 대 | 클러스터 재조인 필요 |
| 7 | **AppStore advanced 정책 결정** (R0 F-09, R6 C-02) | 정책 | — |
| 8 | **설정/레이어 정리** (`/opt/stacks`, 에러 코드 dedup, WS auth 표준화 — 이미 PR 27ad522로 부분 처리) | 소 | 이미 일부 완료 |

## 이미 처리된 항목

### 27ad522 — 로우리스크 정리
- `ErrToolNotFound` 중복 var 제거
- `ErrCommandTimeout` const 승격
- `ErrPathInvalid` → `ErrInvalidPath` 통일
- `Config.Server.StacksPath` 추가 + `/opt/stacks` 3곳 제거
- `alert/manager.go` `component="alert"` slog 태그
- wireguard/tailscale 7곳 `response.SanitizeOutput`
- R0 F-08 (rate limit off-by-one) — 재검증 후 정상 동작으로 판정

### ff2064e — 고임팩트 P0 1차
- R3 N-01 `isCriticalPath` prefix 체크 → `/etc/cron.d`·`/etc/sudoers.d`·`/usr/local/bin` 보호
- R0 F-07 `ListDir`에서 readProtectedPaths 파일 숨김
- R0 F-06 커스텀 로그 소스 `/home/`·`/tmp/` 제거
- R5 C-01 Tailscale authkey → `TS_AUTHKEY` env var
- R0 S-11 / R1 P2-3 `config.Validate()` JWT secret 길이 ≥ 16
- R4 C-01 `runComposeStream` 30분 하드 타임아웃
- R1 P1-3 install.sh `chmod 700` on `/etc/sfpanel`, `/var/lib/sfpanel`

### c8b59cd — 잔여 P0 6건 일괄
- R2 A-01 TOTP replay 캐시 (30초 내 재사용 차단)
- R0 P0-D / R9 I-1 Alert 매니저 graceful shutdown (NewRouter cleanup 클로저 + main.go에서 호출)
- R1 P1-1 / R6 C-01 install.sh 체크섬 검증 (`checksums.txt` 비교 후에만 systemctl stop)
- R0 F-09 / R6 C-02 AppStore advanced 모드 compose YAML 위험 패턴 차단 (privileged, pid:host, SYS_ADMIN, 위험 bind mounts, docker.sock 등)
- R0 F-01 / R8 F-01 클러스터 proxy + WS relay의 `InsecureSkipVerify` 제거 → `Manager.GetTLS().ClientTLSConfig()` 사용
- R10 C-1 AppStoreDetail의 marked README 출력에 DOMPurify sanitize 적용

### 07ca31a — P1 후속 일괄
- R0 S-06 firewall/services/cron/docker/packages/appstore/tailscale/wireguard SanitizeOutput 전면 적용
- R9 I-1 Telegram Markdown → HTML parse mode + 이스케이프
- R9 I-2 Discord webhook URL prefix 검증 (SSRF 차단)
- R9 I-3 services systemctl stderr SanitizeOutput
- R9 I-4 audit cleanup TOCTOU → CAS + 단일 DELETE
- R9 I-5 crontab 직렬화 mutex
- R5 I-01 UFW 코멘트 `#` 제거, I-02 netplan generate 사전검증, I-03 swap tmpfs 차단, I-04 WireGuard 원자 쓰기, I-05 getJailInfo 검증
- R4 C-01 runComposeStream 30분 하드 타임아웃 (이전 ff2064e에서 이미), I-02 Hub limit 100 상한, I-03 tail 상한, I-04 RollbackStack 부분 실패 abort, M-01 PruneAll sanitize, M-02 TTY resize 음수/극값 차단
- R3 N-02 Scanner 256KB/1MB 버퍼, N-03 safeWSWriter 유사 + scanner drain 대기, N-04 cleanPath INSERT
- R0 P0-1 logs WS 쓰기 직렬화, P1-2 CleanupTerminalSessions ctx, P1-4/7 monitor history sync.Once + ctx, P1-6 ws_relay CloseMessage WriteDeadline, P1-10 ClusterUpdate ctx 취소 관찰
- R7 N-1 scrollback replay 순서 + writeToReader, N-2 safeWSWriter WriteMessage 데드라인
- R8 N-01 Manager에 h.Config.Cluster 복사본 전달 (P1-5)
- R2 A-02 Disable2FA TOTP 재확인, A-04 ChangePassword/Disable2FA rate limit
- R1 P4-1 login 감사 로그 기록, P5-1 `?token=` `/files/download` + `/system/backup` 한정, P5-2 CORS `AllowedOrigins` 설정
- R6 I-02 restore tar 심볼릭 링크 차단, I-03 apt output SanitizeOutput, I-04 tuning 이중 apply guard, I-05 nvm env
- R10 I-2 파일 크기 프런트 경고, I-4 AppStore advanced 경고 배너, I-1 `invalidateSetupCache` export
- AppStore advanced 모드에 compose YAML safety validator 추가

### c92af03 — 클러스터 신뢰 경계 강화
- R0 F-02 / R8 F-02 gRPC `UnaryInterceptor`로 PreFlight/Join 외 모든 메서드에 verified client cert 요구
- R8 N-02 `ClusterUpdate`가 Shutdown 전에 `ProxySecret()` 캡처
- R8 N-03 Token Peek/Validate에 HMAC 재검증 추가

### 54bc562 — 잔여 항목
- R2 A-03 JoinResponse proto에 `admin_totp_secret` 추가 + follower DB UPDATE에 totp 포함
- R1 P2-1 마이그레이션 `ALTER TABLE`을 `PRAGMA table_info` 기반 체크로 교체 (드라이버 에러 문자열 의존성 제거)

### 남은 사항 (정책/범위 이유로 연기)
- **R6 C-01 잔여**: get.docker.com / claude.ai / nvm install.sh 해시 pinning. 업스트림 스크립트가 고정 해시로 배포되지 않으므로 운영자 정책(특정 버전 고정 vs 최신) 결정 필요.
- **R0 S-03**: `defaultLogSources`를 `Handler` 구조체 필드로 이동. 변경 자체는 작지만 기존 `SetSFPanelLogPath()` 호출 경로 재설계 필요 — 현재 initialization-only 사용이라 실제 레이스 없음.
- **R0 S-01/S-02**: feature → feature (settings), feature → api/middleware (IsInternalProxyRequest) 역방향 import. 순환은 없고, 완전 해결은 패키지 재배치를 수반.
- **R10 I-5**: i18n `escapeValue: true`로 복원. 현재 실제 XSS 경로 없음(JSX 자동 이스케이프). 복원 시 HTML 포함 번역 모두 `Trans` 컴포넌트로 바꿔야 해 범위 큼.

## 아키텍처적으로 양호하다고 확인된 영역

- SQL 인젝션: 모든 쿼리 `?` 파라미터 바인딩
- bcrypt cost 12 + 랜덤 salt
- JWT alg pinning (`SigningMethodHMAC` 타입 단언)
- JWT 만료/issuer 검증
- Setup TOCTOU: 트랜잭션 내 COUNT
- ConstantTimeCompare 상수시간 비교
- Raft FSM Apply: I/O 없음, mutex 밖 Unmarshal
- Snapshot/Restore: deep copy, replay semantics 올바름
- Token single-use + 실패 시 RestoreToken
- Commander DI 패턴 핵심 모듈 준수
- `response.OK/Fail` 약 90% 준수
- `slog` 전용 (main.go 제외)
- 레이어 순환 없음 (feature→feature 2건, feature→middleware 3건 있으나 DAG 유지)
- Compose 프로젝트 이름 경로 순회 방어
- WS safeWSWriter 패턴 (websocket 모듈에서)
- 프런트 401 자동 리다이렉트, WS 재연결 지수 백오프

## 파일 맵

```
docs/superpowers/research/2026-04-19-code-review/
├── R0-architecture-review.md      # 종합 (업데이트됨, F-08 취소 반영)
├── R1-install-flow.md             # 신규 설치 + 첫 로그인
├── R2-auth-settings.md            # 인증/설정 심층
├── R3-files-logs.md               # 파일/로그 (N-01 신규 P0)
├── R4-docker-compose.md
├── R5-firewall-network-disk.md
├── R6-packages-appstore-system.md
├── R7-streaming-monitor.md
├── R8-cluster.md                  # R0 교차 검증
├── R9-small-modules.md
├── R10-frontend.md
├── R-final-synthesis.md           # 이 문서
├── security-findings.md           # R0 원본
├── concurrency-findings.md        # R0 원본
└── structure-findings.md          # R0 원본
```

각 보고서는 독립적으로 읽히도록 작성되었으며, 신규 발견은 R0 중복 없이 "신규" 마킹되어 있다.
