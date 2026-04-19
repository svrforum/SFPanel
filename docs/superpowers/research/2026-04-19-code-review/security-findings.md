# SFPanel 보안 감사

감사 일시: 2026-04-19
감사 범위: 명령 인젝션 / 경로 순회 / SQL / 비밀 처리 / 인증·인가 / TLS

## P0 — 원격 익스플로잇 또는 권한 상승

### F-01 — TLS 클라이언트 `InsecureSkipVerify: true` 운영 경로
- `internal/api/middleware/proxy.go:34`
- `internal/cluster/ws_relay.go:52`
- `internal/cluster/grpc_client.go:47` (join 전용)

클러스터 HTTP 프록시 + WS 릴레이 양쪽에서 TLS 검증 완전히 비활성화. `ws_relay.go:49`에 "TODO: use cluster's mTLS config" 주석 있음. 동일 네트워크 공격자가 클러스터 트래픽 MITM → `X-SFPanel-Internal-Proxy` secret 탈취 → JWT 우회 가능.

**수정**: `TLSManager.ClientTLSConfig()`의 mTLS 구성으로 교체. `DialNodeInsecure`(join 이전 전용)는 분리 유지.

### F-02 — gRPC 서버 `ClientAuth: VerifyClientCertIfGiven`
`internal/cluster/tls.go:173`

인증서 없는 클라이언트도 연결 가능. PreFlight/Join만 unauthenticated이고 나머지는 mTLS여야 하는데, 인터셉터 레벨의 메서드별 검증이 없으면 모든 gRPC 메서드를 CA 없이 호출 가능.

**수정**: 서버 `tls.RequireAndVerifyClientCert` + PreFlight/Join용 별도 서버 인스턴스 또는 인터셉터로 예외 처리.

## P1 — 인증 후 익스플로잇 또는 정보 유출

### F-03 — NVM `bash -c` 에 `nvmDir` 삽입
`packages/handler.go:633/709/797/804/849/906`. `body.Version`은 정규식 검증되지만 `nvmDir`은 동적으로 결정. 낮은 실제 위험.
**수정**: `cmd.Env`로 `NVM_DIR` 전달 + `. "$NVM_DIR/nvm.sh"`.

### F-04 — `pvs -S vg_name=<vgName>` 필터 선택 언어 인젝션 가능성
`disk/disk_filesystems.go:358`. 실현 가능성 낮음(`validateLVMName`이 `[a-zA-Z0-9+_.:-]`만 허용).
**수정**: 결과 전체 조회 후 Go에서 필터링.

### F-05 — JWT `?token=` 쿼리 파라미터 노출
`middleware/auth.go:67`. 액세스 로그/Referer/브라우저 히스토리 평문 기록.
**수정**: 다운로드 전용 단기 토큰 엔드포인트 또는 액세스 로그 미들웨어에서 `token=` 마스킹.

### F-06 — 커스텀 로그 소스 허용 경로 과다
`logs/handler.go:434`. `/var/log/`, `/opt/`, `/home/`, `/tmp/` 허용. `/home/` → SSH 개인키/`.env`/`.bash_history` 노출. `isReadProtectedPath()` 미적용.
**수정**: `/home/`, `/tmp/` 제거 또는 등록 시 `isReadProtectedPath()` 호출.

### F-07 — `ListDir` `readProtectedPaths` 미적용
`files/handler.go:152`. `ReadFile`/`DownloadFile`은 체크하지만 디렉토리 목록은 `/etc/shadow`, `/etc/sfpanel/cluster/*`(mTLS 인증서), `config.yaml` 등 파일명·크기·mtime 노출.
**수정**: 루프에서 `isReadProtectedPath(fullPath)` 체크 후 제외.

### F-08 — Rate limit off-by-one (5번째 시도가 통과)
`auth/handler.go:177`. `attempt.count++` 후 `>= rateLimitMaxAttempts(5)` 시점에 `blockedUntil` 설정하지만 그 시도 자체는 `return false`로 통과. 실제 허용 6회.

```go
attempt.count++                          // count = 5
if attempt.count >= rateLimitMaxAttempts { // true
    attempt.blockedUntil = now.Add(...)
}
return false  // 5번째 요청도 통과
```

**수정**: `attempt.count >= rateLimitMaxAttempts` 시 `return true`로 차단.

### F-09 — AppStore 고급 설치 모드 = 임의 compose 실행 = root shell
`appstore/handler.go:570`. `advanced: true`에서 클라이언트가 임의 `docker-compose.yml` 제출 → `privileged: true`, `pid: host`, `/:/hostfs` 바인드 마운트 가능. SFPanel이 root로 실행되므로 인증된 관리자가 완전한 호스트 루트 탈출 가능.
**수정**: 의도된 기능이면 문서에 명시 + 2FA 재확인 또는 별도 승인 단계. 아니면 이미지/바인드 블록.

## P2 — 심화 방어

### F-10 — Proxy secret이 CA 해시로 결정론적 도출
`cluster/grpc_server.go:49`. 합법 노드는 secret 재계산 가능 → 다른 노드에서 JWT 우회 가능.
**수정**: `crypto/rand` 32바이트 임의 secret + Raft FSM으로 클러스터 배포.

### F-11 — WireGuard PostUp/PreUp 탐지 패턴 기반
`network/wireguard.go:64`. 기본 케이스는 차단되나 `wg-quick`의 실제 파싱과 미세 불일치 가능성.
**수정**: `(?i)^\s*(postup|postdown|preup|predown)\s*=` 정규식 또는 `wg showconf` 파싱.

## 양호 판정

- **F-12** SQL 인젝션: 모든 쿼리 파라미터화 (`?` 바인딩), `fmt.Sprintf` SQL 사용 없음
- **F-13** bcrypt cost 12 (`internal/auth/hash.go:5`): 권고 수준 이상
- **F-14** Setup 엔드포인트 재호출: 트랜잭션 내 `COUNT(*)` 검증으로 TOCTOU 차단

## 요약 테이블

| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| F-01 | P0 | proxy.go:34, ws_relay.go:52 | InsecureSkipVerify 운영 경로 |
| F-02 | P0 | tls.go:173 | gRPC `VerifyClientCertIfGiven` |
| F-03 | P1 | packages/handler.go:633 | NVM bash -c nvmDir 삽입 |
| F-04 | P1 | disk/disk_filesystems.go:358 | pvs -S 필터 삽입 (실현 낮음) |
| F-05 | P1 | middleware/auth.go:67 | JWT 쿼리 노출 |
| F-06 | P1 | logs/handler.go:434 | 커스텀 로그 /home/, /tmp/ 허용 |
| F-07 | P1 | files/handler.go:152 | ListDir readProtectedPaths 미적용 |
| F-08 | P1 | auth/handler.go:177 | Rate limit off-by-one |
| F-09 | P1 | appstore/handler.go:570 | 고급 모드 임의 compose |
| F-10 | P2 | cluster/grpc_server.go:49 | proxy secret 결정론적 |
| F-11 | P2 | network/wireguard.go:64 | WG PostUp 탐지 패턴 기반 |

**에이전트 클레임은 R0 종합 단계에서 스팟 체크 예정.**
