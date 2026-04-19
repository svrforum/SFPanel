# SFPanel 동시성 & 생명주기 감사

검토 범위: `internal/` 전체 (현재 working tree, 커밋 후 상태)
검토 일시: 2026-04-19

## P0 — 크래시 / 데드락 / 데이터 손실

### P0-1 — `logs/handler.go:393` WS 쓰기 비직렬화 → 패닉 가능
`LogStreamWS`의 scanner 고루틴이 `ws.WriteMessage()`를 직접 호출. 에러 경로(line 332/339/348 등)와 `defer ws.Close()`(내부 쓰기 잠금)가 같은 connection에 동시 쓰기를 유발. gorilla/websocket은 동시 쓰기에서 패닉.

**수정**: `safeWSWriter` 패턴(`internal/feature/websocket/`에 이미 있음) 재사용 또는 공용 패키지로 추출.

### P0-2 — `terminal/handler.go:109` `broadcast()` 비직렬화 쓰기 → 패닉
`readersMu` 잠금 아래 각 `*websocket.Conn`에 직접 `WriteMessage`. 동일 세션에 여러 클라이언트 붙을 때 per-conn 뮤텍스가 없어 reconnect + PTY 출력 경쟁 시나리오에서 패닉. 또한 `readersMu`가 WriteMessage 지속 시간 내내 유지되어 다른 WS 핸들러의 `addReader`/`removeReader`를 블로킹.

**수정**: readers map 타입을 `map[*connWithMu]struct{}` 형태로 변경하여 per-connection 뮤텍스.

### P0-3 — `grpc_server.go:165` Heartbeat recvCh 고루틴 블로킹
`recvCh` 버퍼 크기 1. 메인 루프가 timer/ctx로 반환된 후 수신 고루틴이 다음 Recv 결과를 채널에 보내려 할 때 수신자 없어 블로킹. gRPC 스트림 취소가 깨울 때까지 고루틴 점유. 동시 heartbeat 연결 많으면 누적.

**수정**:
```go
select {
case recvCh <- recvResult{ping, err}:
case <-stream.Context().Done():
    return
}
```

## P1 — 리소스 누수 / 레이스 / 혼란 동작

### P1-1 — `router.go:182` alert 고루틴에 종료 훅 없음
`alertManager.Start(context.Background())`. `Stop()` 정의는 있으나 어디서도 호출 안 됨. 코드에 TODO 주석 존재. graceful shutdown 시 DB 닫힌 후에도 `evaluate()`가 접근.

**수정**: `main.go`에서 shutdown context 주입 + `defer alertManager.Stop()`.

### P1-2 — `terminal/handler.go:307` `CleanupTerminalSessions` ticker 미종료
`time.NewTicker` 생성 후 `ticker.Stop()` 호출 없음. 고루틴 종료 방법 없음.

**수정**: ctx 파라미터 추가하거나 `Stop()` 헬퍼 노출.

### P1-3 — `cluster/manager.go:693` Shutdown 순서 미문서화
`Shutdown()`이 `heartbeat.Stop()` → `connPool.Close()` → `raft.Shutdown()` 순서로 호출. 현재 `StartLocalMetrics`의 `grpcClient`는 직접 dial이라 안전하지만, 향후 풀 사용으로 바뀌면 이중 close 위험.

**수정**: Shutdown 순서를 주석으로 명시.

### P1-4 — `monitor/history.go:23` 패키지 전역 상태 + 중복 Start 위험
`historyPoints`/`historyDB`/`historyMu`가 package-level. `StartHistoryCollector`를 두 번 호출하면 두 고루틴이 같은 슬라이스를 수정 (뮤텍스 덕에 레이스는 아니지만, 60초마다 중복 INSERT).

**수정**: `sync.Once` 추가 또는 구조체로 캡슐화.

### P1-5 — `cluster/handler.go:111` `InitCluster` configMu 간격 포인터 레이스
`configMu` 해제 후 `cluster.NewManager(&h.Config.Cluster)`에 포인터 전달. `Init()` 실행 중 Manager가 이 구조체를 수정하는데 다른 핸들러(예: `GetStatus`)가 configMu 없이 동일 필드 읽을 수 있음.

**수정**: `NewManager`에 복사본 전달.

### P1-6 — `ws_relay.go:79,101` CloseMessage 전송에 WriteDeadline 없음
ReadDeadline은 설정되나 WriteDeadline은 없음. 느린 원격 노드에 CloseMessage 보낼 때 wg.Done 호출 못해 Echo 핸들러 고루틴 영구 블로킹.

**수정**: `ws.SetWriteDeadline(time.Now().Add(5*time.Second))` 추가.

### P1-7 — `monitor/history.go:38` 히스토리 컬렉터 종료 경로 없음
context/채널 미관찰. 프로세스 수명과 동일하므로 실제 누수는 아니지만, 미래 DB 생명주기 추가 시 닫힌 DB 접근 위험.

### P1-8 — `logs/handler.go:43` 패키지 수준 맵 비보호 수정
`SetSFPanelLogPath()`가 `defaultLogSources` 맵을 뮤텍스 없이 수정. `ListSources()`/`allSources()`와 동시 접근 시 race detector에 걸림.

### P1-9 — `manager.go:692` Shutdown 순서 주석 필요 (P1-3과 중복)

### P1-10 — `cluster/handler.go:798` `ClusterUpdate` context 취소 미확인
`simultaneous` 모드에서 `updateNode` 내 `time.Sleep(5 * time.Second)` × 12회 반복. 클라이언트 SSE 끊어도 60초간 고루틴이 핸들러 차지.

**수정**: `c.Request().Context()` done 체크.

## P2 — 스타일 / 강화

### P2-1 — `grpc_server.go:165` recvCh 버퍼 크기 1 타이밍 감수성 (P0-3 대응)
### P2-2 — `monitor/update.go:18` update checker 종료 불가 (프로세스 수명 동일)
### P2-3 — `auth/handler.go:185` `recordFailedLogin` 데드코드 가능성 (`preRecordLoginAttempt`가 대체)
### P2-4 — `cluster/raft_fsm.go:64` `Apply` `f.mu` 잠금 아래 JSON 파싱 (현재 문제 없으나 외부 I/O 추가 금지 주석 권장)

## 요약 테이블

| ID | Sev | 위치 | 문제 |
|----|----|------|------|
| P0-1 | P0 | `logs/handler.go:393` | WS 쓰기 비직렬화 → 패닉 |
| P0-2 | P0 | `terminal/handler.go:109` | broadcast() WS 쓰기 경쟁 → 패닉 |
| P0-3 | P0 | `grpc_server.go:166` | Heartbeat recvCh 고루틴 누수 |
| P1-1 | P1 | `router.go:182` | alert 고루틴 종료 없음 |
| P1-2 | P1 | `terminal/handler.go:307` | CleanupSessions Ticker 미종료 |
| P1-3 | P1 | `manager.go:693` | Shutdown 순서 미문서화 |
| P1-4 | P1 | `monitor/history.go:23` | 전역 상태, 중복 시작 위험 |
| P1-5 | P1 | `cluster/handler.go:111` | configMu 간격 Config 포인터 레이스 |
| P1-6 | P1 | `ws_relay.go:79` | CloseMessage 전송 WriteDeadline 없음 |
| P1-7 | P1 | `monitor/history.go:38` | 컬렉터 종료 경로 없음 |
| P1-8 | P1 | `logs/handler.go:43` | 패키지 맵 비보호 수정 |
| P1-10 | P1 | `cluster/handler.go:798` | ClusterUpdate context 취소 미확인 |
| P2-1 | P2 | `grpc_server.go:165` | recvCh 타이밍 감수성 (P0-3) |
| P2-2 | P2 | `monitor/update.go:18` | update checker 종료 불가 |
| P2-3 | P2 | `auth/handler.go:185` | recordFailedLogin 데드코드 |
| P2-4 | P2 | `raft_fsm.go:64` | Apply 뮤텍스 아래 JSON 파싱 주석 |

**에이전트 클레임은 R0 종합 단계에서 스팟 체크 예정.**
