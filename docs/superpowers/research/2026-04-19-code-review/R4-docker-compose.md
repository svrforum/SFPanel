# R4 — docker + compose

검토 일시: 2026-04-19
범위: `internal/feature/docker/handler.go`, `internal/feature/compose/handler.go`, `internal/docker/client.go`, `internal/docker/compose.go`, `internal/feature/websocket/handler.go`(컨테이너 WS 경로)

**판정**: 전반 양호 — 프로젝트명 경로순회 방어, 서비스/이미지명 정규식, `safeWSWriter` 일관 적용. Docker SDK 단일 클라이언트 동시성 OK.

## P0

### C-01 `runComposeStream`에 자체 타임아웃 없음
**위치**: `internal/docker/compose.go:344-386`
배치 경로 `runCompose`는 `context.WithTimeout(ctx, 5*time.Minute)` 추가하지만, 스트리밍 경로 `runComposeStream`은 상위 ctx만 의존. `UpStream`, `UpdateStackStream`이 이 함수 경유.
`docker compose pull`이 레지스트리 문제로 영구 hang하거나, nginx 역프록시가 TCP 연결을 버퍼링하면 클라이언트 연결 종료 후에도 프로세스가 무한 실행.
**수정**: `ctx, cancel := context.WithTimeout(ctx, 30*time.Minute); defer cancel()`.

## P1

### I-01 컨테이너 ID / 이미지 ID / 네트워크 ID 입력 검증 없음
**위치**: `feature/docker/handler.go` 전반 (StartContainer, StopContainer, RestartContainer, RemoveContainer, InspectContainer 등)
`c.Param("id")`를 검증 없이 SDK 전달. `compose.go`는 `validProjectName`/`validServiceName`/`validImageID` 정규식 완비한 것과 불일치. 직접 인젝션은 아니지만 방어 심층 원칙 + 코드베이스 일관성 위반.
**수정**: `validateContainerID(id string) error` 헬퍼 + 모든 param 경로 적용.

### I-02 Hub 검색 limit 상한 없음
**위치**: `feature/docker/handler.go:563-574`
`?limit=10000` 시 SDK `ImageSearch`에 그대로 전달. 응답이 핸들러 메모리에 전부 수집 후 JSON 직렬화.
**수정**: `if parsed > 100 { parsed = 100 }`.

### I-03 `ServiceLogs`/`ComposeLogsWS` tail 상한 없음
**위치**: `feature/compose/handler.go:357`, `feature/websocket/handler.go:197`
`?tail=1000000` → `docker compose logs --tail 1000000` → 수백 MB 버퍼링. files/handler.go의 `MaxBytesReader` 같은 방어 없음.
**수정**: `const maxTailLines = 10000` 적용.

### I-04 `RollbackStack` 부분 실패 시 혼합 버전 기동
**위치**: `internal/docker/compose.go:786-800`
재태깅 루프에서 실패 시 `slog.Warn + continue`로 건너뜀 → 일부 서비스만 롤백된 상태에서 `up --force-recreate` → 혼합 버전 스택. 클라이언트에 성공처럼 응답.
**수정**: 재태깅 실패 시 전체 abort (`return`).

## P2

### M-01 `PruneAll` 에러 `SanitizeOutput` 없이 반환
`feature/docker/handler.go:519-526`. 다른 Docker 에러는 모두 sanitize하는데 여기만 예외. Docker 소켓 경로/cgroup 마운트 내부 정보 노출 가능.

### M-02 TTY resize cols/rows 음수 → uint 극값
`feature/websocket/handler.go:322-329`, `docker/client.go:200-205`. `uint(-1) = 18446744073709551615` Docker 데몬에 전달. 데몬이 거부하지만 클라이언트 단 예측 가능한 방어 없음.
**수정**: `if resizeMsg.Cols <= 0 || Rows <= 0 || > 65535 { continue }`.

## 요약
| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| C-01 | P0 | docker/compose.go:344 | 스트리밍 경로 타임아웃 없음 |
| I-01 | P1 | feature/docker/handler.go 전반 | 컨테이너/이미지/네트워크 ID 검증 없음 |
| I-02 | P1 | feature/docker/handler.go:563 | Hub 검색 limit 상한 없음 |
| I-03 | P1 | feature/compose/handler.go:357, websocket/handler.go:197 | tail 상한 없음 |
| I-04 | P1 | docker/compose.go:786 | RollbackStack 부분 실패 혼합 버전 |
| M-01 | P2 | feature/docker/handler.go:519 | PruneAll SanitizeOutput 없음 |
| M-02 | P2 | websocket/handler.go:322 | TTY resize 음수 uint |
