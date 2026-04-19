# R7 — terminal + websocket + monitor

검토 일시: 2026-04-19
범위: `internal/feature/terminal/handler.go`, `internal/feature/websocket/handler.go`, `internal/feature/monitor/handler.go`, `internal/monitor/{history,collector,update}.go`

## R0 항목 검증

| R0 ID | 위치 | 판정 |
|---|---|---|
| P0-1 | `logs/handler.go:393-400` | **CONFIRMED** — 스캐너 고루틴이 `ws.WriteMessage` 직접, safeWSWriter 미사용 |
| P0-2 | `terminal/handler.go:109-121` | **CONFIRMED** — broadcast()가 per-conn 뮤텍스 없이 WriteMessage 직접 |
| P1-2 | `terminal/handler.go:307-346` | **CONFIRMED** — `ticker.Stop()`은 defer지만 고루틴 종료 ctx/채널 없음 |
| P1-4 | `monitor/history.go:23-27` | **CONFIRMED** — `historyPoints/DB/Mu` 패키지 전역, `sync.Once` 없음 |
| P1-6 | `cluster/ws_relay.go:79, 101` | **CONFIRMED** — WriteMessage CloseMessage 전 SetWriteDeadline 없음 |
| P1-7 | `monitor/history.go:38-49` | **CONFIRMED** — ctx 파라미터 없는 `for range ticker.C` 무한 루프 |
| S-09 | `logs/handler.go:81`, `terminal/handler.go:34`, `websocket/handler.go:50` | **CONFIRMED** — authenticateWS 3중 복사. logs/terminal의 raw c.JSON 반환도 겸함 |

## P1 신규

### N-1 Scrollback replay 후 `addReader` 등록 사이 메시지 유실
**위치**: `terminal/handler.go:214-223`

재연결 흐름:
1. `sess.scrollback.Bytes()` 읽음 (mu 해제)
2. `ws.WriteMessage(scrollback)` 전송
3. `sess.addReader(ws)` 등록

2→3 사이에 PTY 리더가 `broadcast()`로 새 데이터 전송해도 이 conn은 `readers`에 없어 유실. Scrollback replay 자체도 `safeWSWriter` 미경유로 동시 write 경합 가능.

**수정**: `addReader(ws)` 먼저 호출 → scrollback replay는 `safeWSWriter` 경유.

### N-2 `safeWSWriter.WriteMessage`에 WriteDeadline 없음
**위치**: `websocket/handler.go:35-39`

`WriteJSON`은 `SetWriteDeadline(+10s)` 설정하지만 `WriteMessage`는 무기한. `ContainerLogsWS`/`ComposeLogsWS`의 스캐너가 WriteMessage 사용 → 클라이언트 멈춤 시 고루틴 영구 블로킹.

**수정**: `WriteMessage` 구현에도 `SetWriteDeadline(time.Now().Add(10*time.Second))`.

## P2 신규

### N-3 `historyInterval` 주석-값 불일치
**위치**: `monitor/history.go:19-20`
주석 "every 30 seconds" vs 값 `60 * time.Second`. `historyMaxLen = 2880`은 30초 기준 24h 계산값 → 실제 60초 간격이면 48h 분량.

### N-4 update checker 응답 본문 크기 제한 없음
**위치**: `monitor/update.go:29-46`
`json.NewDecoder(resp.Body).Decode(...)` 직접. GitHub API나 중간자가 대형 응답 보내면 메모리 과다 점유.
**수정**: `io.LimitReader(resp.Body, 64*1024)`.

## 양호
- `websocket/handler.go`의 `safeWSWriter`: MetricsWS/ContainerLogsWS/ComposeLogsWS/ContainerExecWS 일관 적용
- PTY 세션 정리: `startReader` 고루틴 내 exit 시 `ptmx.Close → cmd.Kill → cmd.Wait` 순서
- `monitor/history.GetHistoryRange`: RLock 하 방어적 copy 반환
- `monitor/update.go`: RWMutex로 cachedLatest 보호
- ContainerLogsWS/ComposeLogsWS: `done`/`scanDone` 양방향 종료 연동
- ContainerExecWS: ctx 취소 시 `hijacked.Close()` → reader 고루틴 언블로킹

## 요약
| ID | Sev | 위치 | 상태 | 문제 |
|----|----|------|------|------|
| P0-2 | P0 | terminal/handler.go:109 | CONFIRMED | broadcast 경합 |
| P0-1 | P0 | logs/handler.go:393 | CONFIRMED | WS write 경합 |
| N-1 | P1 | terminal/handler.go:214 | 신규 | scrollback→addReader 메시지 유실 |
| N-2 | P1 | websocket/handler.go:35 | 신규 | safeWSWriter WriteMessage 데드라인 없음 |
| P1-2/4/6/7/S-09 | P1 | 여러 위치 | CONFIRMED | R0 재확인 |
| N-3 | P2 | monitor/history.go:19 | 신규 | 주석-값 불일치 |
| N-4 | P2 | monitor/update.go:29 | 신규 | 응답 크기 제한 없음 |
