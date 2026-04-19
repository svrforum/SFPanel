# R2 — auth + settings

검토 일시: 2026-04-19
범위: `internal/feature/auth/handler.go`, `internal/auth/*`, `internal/feature/settings/handler.go`, `internal/api/middleware/auth.go`, 클러스터 account 동기화

## P1 신규 (R0 미포함)

### A-01 TOTP replay 보호 없음
**위치**: `internal/auth/totp.go:15`
`pquerna/otp`의 `totp.Validate`만 호출. 사용된 코드 캐시가 없어 동일 30초 window 내 같은 코드 재사용 허용. MITM/어깨너머 관찰 시 즉시 재사용 가능.
**수정**: `sync.Map`에 `{secret}:{code}:{period}` 키를 TTL 30초로 저장 후 이미 있으면 거부.

### A-02 `Disable2FA`가 TOTP 재확인 없이 비밀번호만 요구
**위치**: `feature/auth/handler.go:272-306`
세션+비밀번호 탈취 시 물리 TOTP 장치 없이 2FA 해제 가능. Google/GitHub 업계 표준은 현재 TOTP 요구.
**수정**: 요청 body에 `totp_code` 추가 + `auth.ValidateCode` 검증 후 NULL.

### A-03 클러스터 Join 시 follower에 TOTP secret 미전파
**위치**: `cluster/grpc_server.go:127-128`, `cluster/join.go:209-217`
`JoinResponse` proto에 `admin_totp_secret` 필드 없음. follower 로컬 DB는 `password`만 UPDATE. Raft FSM은 `TOTPSecret`을 복제하지만, Raft commit 전 찰나에 로컬 DB fallback으로 TOTP 없이 로그인 가능.
**수정**: `proto/cluster.proto`의 `JoinResponse`에 `admin_totp_secret` 추가 → grpc_server에서 채움 → join.go UPDATE에 포함.

### A-04 `ChangePassword`/`Disable2FA` rate limit 없음
**위치**: `feature/auth/handler.go:308, 272`
`Login`만 `preRecordLoginAttempt` 적용. 두 엔드포인트 모두 bcrypt(cost=12, ~수백ms) 호출 → 유효 JWT를 가진 공격자의 CPU 소모 DoS 가능.
**수정**: `preRecordLoginAttempt` 재사용 또는 Echo RateLimiter 미들웨어.

## P2 신규

### A-05 `recordFailedLogin` dead code
`feature/auth/handler.go:185-205`. 정의만 있고 호출처 없음. `preRecordLoginAttempt`로 일원화한 잔재. 삭제 대상.

### A-06 `SetupAdmin` username 입력 검증 없음
`feature/auth/handler.go:397-399`. 빈 문자열 체크만. 길이/문자 제한 없어 공백/제어문자/매우 긴 문자열 DB 삽입 가능. JWT `sub` claim과 TOTP URI에 그대로 전달됨.
**수정**: `^[a-zA-Z0-9._-]{1,64}$` 정규식.

### A-07 Settings `UpdateSettings` 키 허용 목록 없음
`feature/settings/handler.go:63-73`. 임의 키 허용 (appstore 동적 키 때문에 의도적). 키 200자/값 1000자 제한 외 방어 없음 → 테이블 오염 위험.
**수정**: allowedKeys 목록 또는 모듈 prefix namespace(`appstore.`, `terminal.`).

### A-08 Rate limit 메모리 전용, 재시작 시 초기화
`feature/auth/handler.go:24` `var loginAttempts sync.Map`. `/api/system/update`나 SIGTERM으로 프로세스 재시작시 rate limit 초기화. `Restart=always`와 결합 시 재시작 용이.
**수정 옵션**: SQLite 영속 카운터 또는 fail2ban 통합.

## 양호 (확인 완료)
- bcrypt cost 12
- JWT alg pinning: `ParseWithClaims` 내 `token.Method.(*jwt.SigningMethodHMAC)` 타입 단언으로 `alg=none`/비HMAC 거부
- JWT 만료 검증: golang-jwt/v5 기본 동작, `WithoutClaimsValidation()` 미사용
- Issuer 검증: `jwt.WithIssuer("sfpanel")`
- Setup TOCTOU: 트랜잭션 내 COUNT
- SQL 파라미터 바인딩
- ConstantTimeCompare
- ChangePassword가 현재 비밀번호 검증
- 2FA Setup/Verify 분리 (DB에는 검증된 secret만 저장)
- Cluster FSM Config와 settings DB 분리 (JWT secret은 FSM만)

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| A-01 | P1 | auth/totp.go:15 | TOTP replay 보호 없음 |
| A-02 | P1 | auth/handler.go:272 | Disable2FA TOTP 재확인 없음 |
| A-03 | P1 | cluster/grpc_server.go:127, join.go:209 | Join 시 TOTP 미전파 |
| A-04 | P1 | auth/handler.go:308, 272 | bcrypt DoS (rate limit 없음) |
| A-05 | P2 | auth/handler.go:185 | recordFailedLogin dead code |
| A-06 | P2 | auth/handler.go:397 | username 검증 없음 |
| A-07 | P2 | settings/handler.go:63 | 키 허용 목록 없음 |
| A-08 | P2 | auth/handler.go:24 | rate limit 메모리 전용 |
