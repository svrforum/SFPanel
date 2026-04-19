# SFPanel 구조 / 에러 일관성 감사

감사 일시: 2026-04-19
범위: 의존 방향 / 핸들러 구성 / 응답·에러 / 로깅 / 중복 / 데드코드 / 설정

**종합 한 줄 평**: 레이어 사이클 없음, `response.OK/Fail` 패턴 90% 준수, `slog` 전용 준수. feature→feature 의존, WS 응답 불일치, SanitizeOutput 누락, 패키지 레벨 가변 상태가 실질 유지보수 부담.

## 카테고리 1 — 의존성

### S-01 (P1) feature→feature 직접 import
- `files/handler.go:18` → `internal/feature/settings`
- `terminal/handler.go:19` → `internal/feature/settings`

CLAUDE.md 원칙상 feature는 독립이어야 함. 현재 단방향이라 사이클은 없지만 확산 시 DAG 깨짐.
**수정**: `DB *sql.DB` 직접 사용 또는 `internal/common`에 `settings.Get(db,key)` 유틸 추출.

### S-02 (P1) feature→api/middleware 역방향 import
- `logs/handler.go:18`, `terminal/handler.go:17`, `websocket/handler.go:14` → `internal/api/middleware`

`IsInternalProxyRequest` 참조. 레이어 정의상 middleware는 feature 위쪽.
**수정**: `IsInternalProxyRequest`를 `internal/cluster` 또는 `internal/common/proxy`로 이동.

## 카테고리 2 — 핸들러 구성

### S-03 (P1) `defaultLogSources` 패키지 레벨 가변 상태
- `logs/handler.go:32,43`

`SetSFPanelLogPath()`가 런타임 변이. DI 원칙 위반, 테스트 격리 불가.
**수정**: `Handler` 구조체 필드로 이동 + `router.go`에서 주입.

### S-04 (P1) `alertManager.Start()` Stop 미연결
- `router.go:178-182` (TODO 주석 존재)

`Manager.Stop()` 구현돼 있으나 `main.go` shutdown 경로에서 호출 없음.
**수정**: `NewRouter` 반환값에 cleanup 함수 추가 또는 echo shutdown 훅에서 `alertManager.Stop()` 호출.

## 카테고리 3 — 응답/에러

### S-05 (P1) WS 엔드포인트 3곳 raw `c.JSON` 사용
- `websocket/handler.go:56,59`
- `logs/handler.go:87,90`
- `terminal/handler.go:40,43`

```go
return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
```

프론트는 `{"success":false,"error":{"code","message"}}` 기대. 이 경로만 `{"error": "..."}` 반환.
**수정**: `response.Fail(c, 401, response.ErrMissingToken, "missing token")`.

### S-06 (P1) `err.Error()` stderr가 SanitizeOutput 없이 노출
대표:
- `firewall/firewall_ufw.go:25`
- `firewall/firewall_fail2ban.go:50`
- `cron/handler.go:54`
- `network/wireguard.go:324` — `...+err.Error()+"\n"+output` (output 변수 직접 포함!)
- `network/tailscale.go:438` — 동일 패턴
- `system/tuning.go:244/248/259`

docker/compose는 준수하지만 이 모듈들은 전혀 사용 안 함. 특히 wireguard/tailscale의 `output` 직접 연결이 가장 위험.
**수정**: 명령 stderr 포함된 모든 `response.Fail` 메시지에 `response.SanitizeOutput(...)` 적용.

### S-07 (P2) `response/errors.go` 중복/일관성
- 81: `const ErrToolNotInstalled = "TOOL_NOT_INSTALLED"`
- 233: `var ErrToolNotFound = "TOOL_NOT_INSTALLED"` (동일값, 미사용)
- 22-23: `ErrInvalidPath` / `ErrPathInvalid` 중복 개념 (files vs logs)

**수정**: `var` 에러 코드 삭제 또는 `const` 블록으로 이동. `ErrPathInvalid` → `ErrInvalidPath`로 통일.

## 카테고리 4 — 로깅

### S-08 (P1) `alert/manager.go`에 `"component"` 속성 전면 누락
- `alert/manager.go:35,85,171,192,194`

cluster/appstore/tuning/firewall은 모두 `component=xxx` 태그 사용. 운영에서 `component=alert` 필터링 불가.
**수정**: `logger := slog.With("component","alert")` + 전 slog 호출 교체.

## 카테고리 5 — 중복

### S-09 (P2) `authenticateWS` 3중 복사
- `logs/handler.go:81`, `terminal/handler.go:34`, `websocket/handler.go:49`

JWT secret 처리 변경 시 3곳 수정 필요. 현재 S-05의 버그도 3곳 동시 발생 중.
**수정**: `internal/api/middleware` 또는 `internal/auth`에 `AuthenticateWSToken(c, secret) error` 추출.

## 카테고리 6 — 설정

### S-10 (P1) `/opt/stacks` 하드코딩
- `router.go:73,89,130` — ComposePath, NewComposeManager, Handler.ComposePath 3회

CLAUDE.md Runtime Layout은 `/var/lib/sfpanel/compose/`를 공식 경로로 명시. 불일치.
**수정**: `Config`에 `Server.StacksPath` 필드 추가, 기본값 `/opt/stacks`, router가 `cfg.Server.StacksPath` 참조.

### S-11 (P2) `config.Validate()` 빈 JWT secret 통과
- `config/config.go:59`

`Load()`가 빈 경우 자동 생성, 그러나 env `SFPANEL_JWT_SECRET=""`가 명시 설정되면 `ApplyEnvOverrides()`가 빈 값으로 덮어씀 → `Validate()` 통과. 빈 secret으로 서명 = 취약.
**수정**: `Validate()`에 `if Auth.JWTSecret == "" { return err }` 추가.

## 요약 테이블

| # | Sev | 카테고리 | 위치 | 문제 |
|---|----|---------|------|------|
| S-01 | P1 | 의존 | files:18, terminal:19 | feature→feature (settings) |
| S-02 | P1 | 의존 | logs:18, terminal:17, websocket:14 | feature→api/middleware 역방향 |
| S-03 | P1 | 핸들러 | logs/handler.go:32,43 | 패키지 가변 상태 |
| S-04 | P1 | 핸들러 | router.go:182 | alert Stop 미연결 |
| S-05 | P1 | 응답 | websocket/56,59; logs/87,90; terminal/40,43 | WS raw `c.JSON` |
| S-06 | P1 | 응답 | firewall/wireguard/tailscale/cron/system 다수 | SanitizeOutput 누락 + output 직접 노출 |
| S-07 | P2 | 응답 | response/errors.go:22,233 | 중복 에러 코드 |
| S-08 | P1 | 로깅 | alert/manager.go 다수 | `component` 속성 누락 |
| S-09 | P2 | 중복 | logs/81, terminal/34, websocket/49 | authenticateWS 3중 복사 |
| S-10 | P1 | 설정 | router.go:73,89,130 | `/opt/stacks` 하드코딩 |
| S-11 | P2 | 설정 | config/config.go:59 | 빈 JWT secret 통과 |

**에이전트 클레임은 R0 종합 단계에서 스팟 체크 예정.**
