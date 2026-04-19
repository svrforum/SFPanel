# R8 — 클러스터 서브시스템

검토 일시: 2026-04-19
범위: `internal/cluster/*`, `internal/api/middleware/proxy.go`, `internal/feature/cluster/handler.go`, `cmd/sfpanel/cluster_commands.go`

## R0 항목 재검증

| R0 ID | 결론 | 근거 |
|---|---|---|
| F-01 InsecureSkipVerify (proxy + ws_relay) | CONFIRMED | `proxy.go:34`, `ws_relay.go:53` |
| F-02 VerifyClientCertIfGiven | CONFIRMED (의도적이나 위험) | `tls.go:173` |
| F-10 proxy secret CA 해시 결정론적 | CONFIRMED | `manager.go:529-538`, `grpc_server.go:50-53` |
| P1-3 Shutdown 순서 | PARTIAL — gRPC 서버 Stop이 `Manager.Shutdown()`에 미포함 | `manager.go:811-843` |
| P1-5 configMu 포인터 race | CONFIRMED | `handler.go:121-128` |
| P1-6 WS relay CloseMessage WriteDeadline | CONFIRMED | `ws_relay.go:79, 101` |
| P1-10 ClusterUpdate context 취소 무시 | CONFIRMED | `handler.go:824` |

## P1 신규

### N-01 InitCluster가 `&h.Config.Cluster` 포인터를 Manager에 전달 → 이후 동일 주소에 쓰기
위치: `feature/cluster/handler.go:121-128`

```go
mgr := cluster.NewManager(&h.Config.Cluster)  // Manager가 포인터 저장
// configMu.Unlock() ... Init() 실행 ...
h.Config.Cluster = *mgr.GetConfig()  // 같은 주소 덮어쓰기
```

Manager는 `m.config = cfg`로 포인터 유지(`manager.go:41`). configMu 해제 후 `Init()` 실행 중 외부 `GetStatus` 등이 `h.Config.Cluster`를 읽으면 데이터 레이스. P1-5와 동일 근본 원인.

수정:
```go
cfgCopy := h.Config.Cluster
mgr := cluster.NewManager(&cfgCopy)
```

### N-02 ClusterUpdate 고루틴이 `mgr.Shutdown()` 후 `mgr.ProxySecret()` 호출
위치: `feature/cluster/handler.go:886-895`

현재는 TLSManager가 Shutdown에서 정리되지 않아 우연히 동작하지만 의미상 use-after-shutdown. 미래에 TLSManager cleanup 추가 시 nil panic.

수정: secret을 Shutdown 전에 캡처.

## P2 신규

### N-03 Join 토큰 HMAC 검증이 실제로 수행되지 않음
위치: `token.go:54-90` (Validate/Peek)

`Generate`는 `hex(raw) + "." + hex(hmac)` 만들지만 `Validate`는 in-memory map 조회만 함. 현재 map 외부에서 토큰 문자열을 주입할 방법이 없어 무해하지만, 향후 DB 영속화 시 즉시 위조 가능.

### N-04 `DialNodeInsecure`의 TOFU 위험 미문서화
위치: `join.go:58`

조이닝 노드는 CA가 없어 초기 연결 시 TLS 검증 불가 — 불가피. 그러나 MITM이 가짜 리더로 유도하면 공격자의 CA를 받게 됨. 조인 이후 mTLS로 보호되지만 초기 handshake의 TOFU 위험은 운영 문서에 명시 필요.

### N-05 SSE 스트리밍 판별 하드코딩 허용목록
위치: `proxy.go:22-27`

`isStreamingEndpoint`가 `/up-stream`/`/update-stream`/`/system/update`/`/appstore/.../install` 네 패턴만 처리. 미래에 `-stream` 접미사 없는 SSE 추가 시 gRPC 단방향 프록시로 잘못 라우팅되어 버퍼링/잘림.

## 양호
- Token single-use: `Validate()`가 `Used=true` 마킹 + Raft Apply 실패 시 `RestoreToken` 롤백
- FSM Apply 뮤텍스: `json.Unmarshal`이 뮤텍스 밖에서 수행, 상태 변경만 lock 내 (`raft_fsm.go:58-65`). I/O 없음
- Snapshot/Restore: RLock 하 JSON 직렬화, Restore는 Lock + 전체 교체 — replay semantics 올바름
- `GetState()` 방어적 deep copy — 외부 수정이 FSM 상태 영향 없음
- `subtle.ConstantTimeCompare` 상수시간 비교
- `RemoveNode` 순서: Raft → FSM → connPool → heartbeat — 올바름
- `AtomicWriteFile`로 join/init 모두 원자적 쓰기

## 요약
| ID | Sev | 위치 | 상태 | 문제 |
|----|----|------|------|------|
| F-01 | P0 | proxy.go:34, ws_relay.go:53 | CONFIRMED | InsecureSkipVerify |
| F-02 | P0 | tls.go:173 | CONFIRMED | VerifyClientCertIfGiven |
| F-10 | P1 | manager.go:529 | CONFIRMED | 결정론적 proxy secret |
| N-01 | P1 | handler.go:122 | 신규 | Manager에 `&h.Config.Cluster` 포인터 |
| P1-5 | P1 | handler.go:121-128 | CONFIRMED (N-01과 동일) | configMu race |
| P1-10 | P1 | handler.go:824 | CONFIRMED | ClusterUpdate ctx 취소 무시 |
| P1-6 | P1 | ws_relay.go:79,101 | CONFIRMED | CloseMessage WriteDeadline |
| P1-3 | P1 | manager.go:811 | PARTIAL | gRPC Stop 경로 없음 |
| N-02 | P1 | handler.go:886-895 | 신규 | Shutdown 후 ProxySecret() |
| N-03 | P2 | token.go:54-90 | 신규 | HMAC 검증 부재 (현재 무해) |
| N-04 | P2 | join.go:58 | 신규 | TOFU 위험 미문서화 |
| N-05 | P2 | proxy.go:22-27 | 신규 | SSE 판별 하드코딩 |
