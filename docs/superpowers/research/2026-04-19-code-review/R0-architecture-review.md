# SFPanel R0 — 전체 아키텍처 리뷰

작성일: 2026-04-19 · 대상 버전: v0.9.0 · 커밋: `b3d8c10` 이후
근거 문서: `security-findings.md`, `concurrency-findings.md`, `structure-findings.md` (같은 디렉토리)

## 한 줄 평

> 구조의 뼈대(레이어 원칙, 응답 규약, slog 일관성)는 잘 잡혀 있고 큰 방향성은 맞다. 다만 **TLS 신뢰 경계가 느슨하고**(클러스터 TLS), **스트리밍 경로(WS/SSE)의 라이프사이클 규약이 일부 허술하며**, **설정 하드코딩·중복 에러코드·feature→feature 의존 같은 세부 일탈**이 누적돼 있다. 총 40개 발견 사항 중 즉시 픽스 대상(P0)은 소수(3–5건)이고, 나머지는 한 번의 정비 스프린트로 해결 가능한 범위.

## 1. 분포

| 영역 | P0 | P1 | P2 |
|---|---|---|---|
| 보안 | 2 | 6 (F-08 재검증 후 취소) | 2 |
| 동시성/생명주기 | 3 | 10 | 4 |
| 구조/에러/의존 | 0 | 8 | 4 |
| **합계** | **5** | **24** | **10** |

## 2. 즉시 수정 권고 (P0 — 상위 5건)

이 5건은 모두 **운영 환경에서 침해 또는 가용성 사고로 직결**된다. 스팟 체크 결과를 괄호에 표기.

### P0-A. 클러스터 TLS 검증 비활성화 (보안 F-01, F-02)
- `internal/api/middleware/proxy.go:34` — `TLSClientConfig: &tls.Config{InsecureSkipVerify: true}` (**확인**, line 34에 그대로 존재)
- `internal/cluster/ws_relay.go:52` — 동일 패턴 (코드에 "TODO: use cluster's mTLS config" 주석 명시)
- `internal/cluster/tls.go:173` — 서버 `ClientAuth: tls.VerifyClientCertIfGiven` (인증서 없는 클라이언트도 gRPC 호출 가능)

**영향**: 동일 네트워크 MITM → `X-SFPanel-Internal-Proxy` secret 탈취 → 원격 노드의 JWT 우회 → 전체 클러스터 장악. 단일 노드에서는 무관하지만 클러스터 기능을 광고한 이상 P0.

**수정 경로**: `cluster.TLSManager.ClientTLSConfig()`는 이미 존재 (`tls.go` 참조). proxy와 ws_relay가 그걸 쓰도록 바꾸고, gRPC 서버를 `RequireAndVerifyClientCert`로 승격. PreFlight/Join 같은 unauthenticated RPC는 별도 서버 포트 또는 `UnaryInterceptor`에서 선별 허용.

### ~~P0-B. 로그인 Rate limit off-by-one~~ — **재검증 결과: 해석 차이, 현재 구현 OK**
- `internal/feature/auth/handler.go:155-183` 재확인
- `preRecordLoginAttempt`은 auth 체크 전에 시도를 사전 카운트하고, 성공 시 `loginAttempts.Delete(ip)`로 전체를 리셋. 실패 상태에서만 count가 남음.
- 시퀀스(count 기준): 1→2→3→4→5(`blockedUntil` 설정, 통과)→6(차단). 즉 "5회 실패 발생 후 6번째부터 차단"은 fail2ban 스타일로 **README의 "5회 실패 → 차단"과 합치되는 해석**.
- 만약 스펙 의도가 "5번째 시도 자체를 차단"(4 strikes)라면 `>=` 조건에서 `return true`로 바꾸면 됨. 제품 결정 사항.

→ 이 항목은 P0에서 제거. 실질적으로 보안 버그 아님.

### P0-C. 앱스토어 `advanced` 모드 = 호스트 루트 탈출 (보안 F-09)
- `internal/feature/appstore/handler.go:570-593`
- `advanced: true`에서 임의 `docker-compose.yml` 제출 → `privileged: true`/`pid: host`/`/:/hostfs` 가능 → SFPanel이 root로 실행되므로 어드민 계정을 얻은 공격자가 완전한 호스트 루트 획득.

**영향**: 이미 어드민 권한을 가진 사용자에게만 해당되므로 엄밀히 "기능"이라고 볼 수도 있음. 그러나 (a) README/UI에 경고가 없고 (b) 2FA 재확인 같은 재인증 단계도 없음 → 세션 탈취 하나로 호스트 탈출까지 직결.

**수정 방향** (셋 중 택1):
1. 재인증 (`POST /auth/reauth` → 10분짜리 고권한 토큰) 요구
2. YAML을 파싱하여 `privileged`, `pid: host`, 호스트 경로 바인드 블록
3. 문서에 위험 명시 + UI 체크박스 경고

### P0-D. 앨럿 매니저 고루틴에 Shutdown 훅 없음 (동시성 P1-1 / 구조 S-04)
- `internal/api/router.go:182` — `go alertManager.Start(context.Background())` + TODO 주석.
- `Manager.Stop()`은 구현되어 있으나 호출처 없음.

**영향**: graceful shutdown 시 DB 닫힌 후에도 `evaluate()`가 계속 실행 → 경합 에러 로그, 최악의 경우 알림 이중 발송. 실제로는 process exit로 강제 종료되므로 대형 사고는 아니지만, 테스트 혹은 리로드 시나리오에서 누수.

**수정**: `NewRouter`가 `cleanup func()`을 반환하도록 시그니처 변경 → `main.go`에서 Echo shutdown 훅에 등록.

### P0-E. WebSocket 스트리밍 라이프사이클 경합 (동시성 P0-1, P0-2)
- `logs/handler.go:393` 스캐너 고루틴의 `ws.WriteMessage`와 핸들러의 `defer ws.Close()` 경합
- `terminal/handler.go:109` broadcast 중 readersMu 장시간 보유

**확인 상태**: 코드 패턴은 존재. 실제로 panic이 발생할지는 gorilla/websocket 내부 구현에 따라 달라짐(한쪽이 에러 반환으로 끝날 수도 있음). **P0보다는 실질 P1이 적절** — 상시 크래시 아닌 드문 경합.

**수정**: `internal/feature/websocket/handler.go`의 `safeWSWriter` 패턴을 공용 패키지로 추출 후 `logs`, `terminal`, `cluster/ws_relay` 모두에서 사용. 추가로 `WriteDeadline`을 WS 전역으로 설정.

## 3. 중요도 높은 P1 묶음 (우선순위 클러스터링)

한 번의 정비 PR로 묶어서 처리하면 효율적인 묶음:

### 묶음 α — 출력 sanitize 누락 (S-06)
- `firewall/firewall_ufw.go:25`, `firewall/firewall_fail2ban.go:50`
- `cron/handler.go:54`
- `network/wireguard.go:324` (output 변수 직접 연결 — 가장 위험)
- `network/tailscale.go:438` (동일 패턴)
- `system/tuning.go:244/248/259`

**원인**: CLAUDE.md 규칙 "Never return raw command stderr" 미준수 모듈 다수. docker/compose는 준수함. **해결**: 단일 PR로 `response.SanitizeOutput(err.Error())` 치환.

### 묶음 β — WS 인증 경로 표준화 (S-05, S-09, 중복 `authenticateWS`)
3파일의 `c.JSON(401, map[string]string{"error":...})` + 중복 `authenticateWS` 구현이 같은 근원.

**해결**:
1. `internal/api/middleware`(또는 `internal/auth/ws.go`)에 `AuthenticateWSToken(c, secret) error` 공용 함수
2. 이 함수가 `response.Fail(c, 401, response.ErrMissingToken, ...)` / `ErrInvalidToken` 반환
3. `logs`/`terminal`/`websocket`의 3개 복사본을 삭제하고 공용 호출

### 묶음 γ — 설정 하드코딩 & 레이어 위반 (S-01, S-02, S-10)
- `/opt/stacks` 3회 (`router.go:73,89,130`) — **확인 완료**
- `feature/files`, `feature/terminal` → `feature/settings` 직접 import
- `feature/logs`, `feature/terminal`, `feature/websocket` → `api/middleware` 역방향 import

**해결**:
1. `config.Server.StacksPath` 필드 추가 (기본 `/opt/stacks`)
2. `settings.Get(db, key)` 유틸을 `internal/common/settings.go` 혹은 직접 DB 쿼리로 대체
3. `IsInternalProxyRequest`를 `internal/cluster/proxyauth.go` 로 이동

### 묶음 δ — 고루틴/ticker 라이프사이클 정비
P0-D(alert)와 함께 일괄:
- `terminal/handler.go:307` `CleanupTerminalSessions` ticker Stop 없음 (P1-2)
- `monitor/history.go:38` 컬렉터 종료 경로 없음 (P1-7)
- `monitor/update.go:18` update checker 종료 없음 (P2-2)
- `cluster/manager.go:693` `StartLocalMetrics` Shutdown 순서 주석 (P1-3)
- `ws_relay.go:79,101` CloseMessage `WriteDeadline` 누락 (P1-6)

**해결**: `main.go`에 단일 `ctx, cancel := signal.NotifyContext(...)`를 만들고, 긴 수명 컴포넌트에 모두 전파. shutdown 순서를 `main.go` 주석으로 문서화.

### 묶음 ε — 설정/로깅 일관성
- 빈 JWT secret 허용 (S-11) — `config.Validate()`에 체크 추가
- alert/manager.go `"component","alert"` 전면 누락 (S-08) — 10분 작업
- 중복 에러 코드 `ErrToolNotFound`/`ErrPathInvalid` (S-07) — **확인 완료**

## 4. 양호한 영역 (문서화 가치)

- **SQL 인젝션**: 모든 DB 쿼리가 `?` 파라미터 바인딩 사용, `fmt.Sprintf` SQL 없음 (확인 완료)
- **bcrypt cost 12**: 권고 수준 이상
- **Setup 엔드포인트 재호출**: 트랜잭션 내 `COUNT(*)` 검증으로 TOCTOU 방지
- **Commander 패턴**: 핵심 모듈(docker, firewall, services 등)에서 일관된 DI 준수
- **response.OK/Fail**: 약 90% 준수 (WS 3파일 + stderr 직접 연결 제외)
- **slog 전용**: `log.*`/`fmt.Println` 관측용 사용 전무
- **레이어 순환**: feature→feature 2건과 feature→api/middleware 3건 있으나 순환(cycle) 없음

## 5. 권장 진행 순서 (R1~ 전)

P0 묶음은 R1 이후 기능별 리뷰와 병행 **별도 치료 PR**로 진행하는 편이 안전. 제안:

1. **픽스 PR 1** (P0-B 단독): Rate limit 한 줄 픽스 + 테스트 추가. 즉시 가능.
2. **픽스 PR 2** (P0-D, 묶음 δ): 라이프사이클 정비 — alert Stop + ticker Stop + ctx 전파. 1회 작업으로 처리.
3. **픽스 PR 3** (P0-A): 클러스터 TLS 엄격화. 별도 QA 필요 (클러스터 재조인 테스트).
4. **픽스 PR 4** (묶음 α + β): sanitize + WS 인증 표준화. 병렬 가능.
5. **픽스 PR 5** (묶음 γ + ε): 설정/레이어 정리. 기능 영향 없음.
6. **P0-C**는 정책 결정 필요 — UX 방향부터 상의.
7. **P0-E**(WS 경합)는 R1/R2 진행 중 관련 모듈 만날 때 묶어 처리 권장.

## 6. R1~ 진입 체크리스트

기능별 리뷰(R1 이후)는 이 R0의 발견사항과 **중복되지 않게** 진행:
- auth/rate-limit은 R2 auth 리뷰에서 재확인만
- 클러스터 TLS는 R8 cluster 리뷰에서 세부 엔드포인트별 영향 매핑
- WS 경합은 R7 streaming 리뷰에서 실제 재현성 확인
- 묶음 α~ε는 R0 종합 픽스로 선처리 권장 (R1~ 리뷰가 깨끗해짐)

## 부록 A — 미확인(claim only) 항목 스팟 체크 필요

리뷰 에이전트가 낸 주장 중 아직 코드로 재확인하지 않은 건:
- 동시성 P1-5 (`cluster/handler.go:111` configMu 포인터 레이스) — InitCluster 코드 미확인
- 동시성 P1-10 (`ClusterUpdate` context 취소) — 60초 sleep 루프 존재는 사실로 확인됨, 취소 체크 여부 미확인
- 보안 F-03 (NVM bash -c `nvmDir`) — 경로가 사용자 제어 가능한지 확인 필요
- 보안 F-06 (`/home/` 로그 소스 허용) — `logs/handler.go:434` 주변 코드 미확인
- 보안 F-07 (`ListDir` readProtectedPaths 미적용) — 코드 확인 필요

이 부록은 각 기능별 리뷰(R1~)에서 해당 모듈을 다룰 때 자연스럽게 검증됨.
