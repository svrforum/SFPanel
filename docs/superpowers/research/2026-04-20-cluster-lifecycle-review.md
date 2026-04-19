# Cluster Lifecycle Stability Review

검토 일시: 2026-04-20
범위: init → join → node management → leave → disband, plus failure/partial-completion/recovery
참고: R8(`docs/superpowers/research/2026-04-19-code-review/R8-cluster.md`)은 컴포넌트 레벨 보안/레이스 — 본 리뷰는 라이프사이클 플로우 관점

## Overall Verdict

최근 리디자인 커밋들(ba5cd60, 1355a74, 6ccbd7f, e644f5)로 핵심 라이프사이클 스토리는 크게 개선됨. zero-restart join, `joiningMu` 직렬화 게이트, Raft-reopen-race 수정은 모두 타당. FSM Apply/Snapshot/Restore 잠금은 올바르고 교착 위험 없음, TLS 부트스트랩은 일반 경로에서 깔끔함.

그러나 구조적 공백 3가지가 남음:
1. **init**: config-save 단계 실패 시 CA와 Raft data가 디스크에 orphan으로 남는 경로
2. **leave/disband**: 정리 순서와 프로세스 종료 사이의 윈도우
3. **TLS 회전**: 캐싱으로 full restart 없이는 cert 교체 경로가 전무

노드 단위 크래시 버그는 아니지만 수동 개입이 필요한 하드-투-리커버 상태가 발생할 수 있음.

---

## What Looks Solid

- FSM `Apply`/`Snapshot`/`Restore` 잠금 정확 — snapshot/apply 교착 위험 없음
- `joiningMu.TryLock` in HTTP handler — 동시 init/join 레이스 깔끔히 차단
- `RestoreToken` rollback (AddVoter 실패 시) 정확 & 멱등
- CLI `leave`/`remove` 러닝 서버에 위임 → Raft 포트 충돌 회피
- `verifySelfAddress` 백그라운드 리콘사일러 — self-healing 좋음
- `requireClientCertInterceptor` + `VerifyClientCertIfGiven` 조합 — post-join 메서드 게이팅 정확
- heartbeat `StopOnce` 가드 — double-close panic 방지

---

## [P0] Init: config-save 실패 시 잘못된 경로로 RemoveAll → orphan CA+Raft

**Location:** `internal/feature/cluster/handler.go:138-147`

**Flow:** `InitCluster` → `mgr.Init(clusterName)` 성공 (CA와 Raft data 디스크 기록, 리더 선출 완료) → `yaml.Marshal`/`AtomicWriteFile` 실패 → handler가 `mgr.Shutdown()` 후 `os.RemoveAll(h.Config.Cluster.DataDir)`, `os.RemoveAll(h.Config.Cluster.CertDir)` 호출.

**Scenario:** 이 시점의 `h.Config.Cluster.DataDir`/`CertDir`은 init 호출 전 값(또는 `cfgCopy` 복사본). 최초 init인 경우 이 필드들이 비어 있을 수 있고 `RemoveAll("")`는 no-op로 조용히 실패 — 실제로 기록된 디렉터리(`NewRaftNode`/`TLSManager.InitCA`의 기본 경로)는 디스크에 남음. 재시도 시 BoltDB 파일이 존재해 `raft.ErrCantBootstrap`으로 혼란스러운 에러.

**Risk:** 디스크에 stale Raft+CA 잔존, config는 standalone이라 주장 → 운영자가 수동 삭제해야 복구 가능.

**Suggested fix:** `mgr.Init` 호출 전에 최종 DataDir/CertDir을 로컬 변수로 resolve, rollback RemoveAll에 그 변수를 사용. 또는 init 성공 경로에서 하는 것처럼 `mgr.GetConfig()`를 cleanup에서도 호출.

---

## [P1] Init: FSM 쓰기(admin, jwt_secret)가 config 저장 이후 — 그 사이에 크래시하면 FSM 비어있음

**Location:** `internal/feature/cluster/handler.go:166-182`

**Flow:** config 파일 저장 (143) → `LiveActivate`로 Raft 시작 → `SetConfig("jwt_secret", …)` → `SyncAccountFromDB(…)`. 143~182 사이에 프로세스가 죽으면 재부팅 시 `Enabled=true`지만 FSM에 JWT secret과 admin이 없음.

**Scenario:** 재부팅 시 `Manager.Start()` 호출 (Init 아님). FSM은 snapshot에 JWT/admin이 없으므로 빈 상태. 클러스터 모드의 auth 미들웨어가 FSM에서 `jwt_secret`을 빈 문자열로 읽어 서명 검증 우회되거나 기능 전체 파손.

**Risk:** init 중 크래시 시 auth degradation; 자동 복구 경로 없음.

**Suggested fix:** FSM 쓰기(`SetConfig("jwt_secret")`, `SyncAccountFromDB`)를 `Manager.Init()` 내부로, 리더 선출 성공 이후·config 파일 쓰기 전에 옮길 것. Raft log에 먼저 durable하게 기록되면 그 이후의 config 쓰기 중 크래시는 `Enabled=false`로 해석되어 clean reinit 가능.

---

## [P1] Leave: config write → RemoveAll → os.Exit 순서 — 중간 크래시 시 stale state 잔존

**Location:** `internal/feature/cluster/handler.go:624-657`

**Flow:** `LeaveCluster` → `mgr.Leave()` → config 파일 `Enabled=false`로 쓰기 → `RemoveAll(dataDir)` → `RemoveAll(certDir)` → `sleep 2s` → `os.Exit(1)`.

**Scenario:** config 쓰기 이후 SIGKILL/OOM/전원 차단 → 다음 부팅은 standalone으로 시작하지만 Raft data와 cert가 디스크에 영구적으로 남음. 공간 낭비 + 향후 같은 DataDir로 re-join 시 혼란. 부분 실패 케이스: `RemoveAll(dataDir)` 성공, `RemoveAll(certDir)` 권한 에러 → 나중 init의 `InitCA`가 CertDir에 덮어쓰지만 구 CA로 발급된 노드 cert가 orphan.

**Risk:** Stale state 누적; cert 부분 제거 시 재init 운영 리스크.

**Suggested fix:** 순서 반전 — 디렉터리 삭제 먼저, config 쓰기 마지막. 삭제 실패 시 500 리턴하고 config는 그대로 두어 운영자가 재시도 가능하게. config 쓰기는 더 싸고 안정적이니 마지막에.

---

## [P1] Disband: 팔로워에게 통지 경로 없음

**Location:** `internal/feature/cluster/handler.go:660-708`

**Flow:** 리더가 `DisbandCluster` 호출 → `mgr.Shutdown()` (leadership transfer 시도) → config Enabled=false → RemoveAll → `os.Exit(1)`.

**Scenario:** 팔로워는 disband를 직접 통보받지 않음. heartbeat 실패로 리더가 offline으로 전이되지만 팔로워의 `config.yaml`은 여전히 `Enabled=true`, Raft data 잔존. 리더 종료 후 쿼럼 상실(잔여 ≤2노드의 경우). 팔로워는 candidate state 루프 — CPU/로그 공간 소비, 쓰기 불가. 운영자가 각 팔로워에 수동 `leave` 호출 필요.

**Risk:** 멀티노드 disband 운영 복잡도 급증; 팔로워에 self-healing 없음.

**Suggested fix:** `Shutdown()` 이전에 FSM에서 팔로워 목록을 조회해 각 노드에 `Leave` RPC(또는 전용 `Disband` RPC)를 gRPC로 발송. 팔로워의 기존 Leave 핸들러가 cleanup+exit를 알고 있음. 대안: `CmdDisband` FSM 커맨드를 만들어 replicated되면 팔로워가 자율 대응.

---

## [P1] Leave: 리더가 2노드 클러스터에서 나갈 때 leadership transfer 순서 뒤바뀜

**Location:** `internal/cluster/manager.go:417-435`

**Flow:** `Leave()` on leader → `m.raft.RemoveServer(m.nodeID)` (자기를 Raft voter에서 제거).

**Scenario:** 2노드 클러스터(리더+팔로워1)에서 `RemoveServer(leaderNodeID)` 호출 → 쿼럼 2→1로 감소, 팔로워가 쿼럼 획득해 self-elect 가능. 그러나 `RemoveServer`는 리더가 config change를 팔로워에 전파하기 전에 step down시킴. 팔로워가 도달 불가(네트워크 파티션)면 `RemoveServer` future가 30s 타임아웃 후 에러 → `Leave()`는 경고 로그만 찍고 `m.raft.Shutdown()` 계속 → 로컬 상태 wipe → 팔로워는 config change를 영원히 못 봄, election timeout 루프.

3노드 케이스: `RemoveServer`가 성공하면 남은 2노드가 쿼럼이지만 리더의 `Leave()`는 `RemoveServer` 이전에 leadership transfer를 하지 않음. `Shutdown()`이 내부적으로 시도하지만 이미 step down 이후라 `ErrNotLeader`로 실패.

**Risk:** 비정상 2노드 leave에서 팔로워 orphan; 3노드 케이스에서 중복/무의미한 transfer 시도.

**Suggested fix:** `Manager.Leave()`에서 자기가 리더이고 다른 voter가 있으면 `RemoveServer` 전에 `TransferLeadership`을 healthy 팔로워로 호출. `Shutdown()`이 하는 동작을 올바른 시점에 앞당기는 것.

---

## [P2] `onNodeStatusChange`가 리더에서만 FSM에 기록 — 팔로워는 FSM에 stale status 무한 보유

**Location:** `internal/cluster/manager.go:874-898`

**Flow:** heartbeat 모니터 fire → 모든 노드에서 `onNodeStatusChange` 호출 → 리더가 아니면 early return → 리더가 `CmdUpdateNode` FSM apply.

**Scenario:** 팔로워에서 `GetNodes()`/`GetOverview()` 호출 → FSM 읽고 로컬 `HeartbeatManager`로 overlay. 로컬 heartbeat는 팔로워에 데이터가 없음(heartbeat는 리더로 스트림). 따라서 팔로워의 응답은 FSM 저장값 그대로 — 리더 측 마지막 기록 이후 hours 단위로 stale. 대부분 경로에서 proxyToLeader를 통해 읽기를 리더에 위임하므로 실제 영향은 작음.

**Risk:** Follower-local 읽기 경로에서 stale node status; 낮은 영향.

**Suggested fix:** `GetNodes`/`GetOverview`가 팔로워에서 항상 proxy to leader하도록 보장, 또는 heartbeat pong을 통해 팔로워의 `HeartbeatManager`도 live 데이터로 채우기.

---

## [P2] TLS config 캐싱 — cert 회전 경로 사실상 없음

**Location:** `internal/cluster/tls.go:150-176`, `tls.go:180-206`

**Flow:** `ServerTLSConfig()`가 `t.cachedServer` 캐시, 이후 호출은 캐시 반환. `NewGRPCServer`에서 한 번 호출 → `grpc.Creds`에 baked-in.

**Scenario:** Node cert validity 5년, CA 10년(tls.go:97). 수동으로 새 cert를 디스크에 쓰더라도 런닝 gRPC 서버는 full process restart 전까지 구 cert 제공. `ClientTLSConfig`도 동일 캐시. 회전 API·expiry 체크 경고·cert 수명 문서 모두 부재. 5년차에 클러스터 전체 gRPC 실패가 동시 발생 가능.

**Risk:** cert 만료 시 조용한 운영 절벽; IP 변경 같은 reissue 운영도 restart 필수.

**Suggested fix:** `CLAUDE.md`에 cert 수명 문서화. `tls.Config.GetCertificate`/`GetConfigForClient` 콜백으로 TLS handshake마다 또는 ~1h TTL로 디스크에서 다시 읽도록 교체. Go zero-downtime rotation 표준 패턴.

---

## [P2] `HandleJoin`: FSM `CmdAddNode` commit → `AddVoter` 실패 롤백이 Raft voter 제거 안 함

**Location:** `internal/cluster/manager.go:301-317`

**Flow:** `m.raft.Apply(CmdAddNode)` 성공 → `m.raft.AddVoter(...)` 실패 → 롤백: `m.raft.Apply(CmdRemoveNode)` + `RestoreToken`.

**Scenario:** `AddVoter`가 transport-level log commit 후에 future 에러를 리턴하는 좁은 윈도우 — 해당 configuration change가 일부 commit된 상태에서 FSM에만 Remove가 기록됨. 노드가 Raft voter지만 FSM 레코드 없는 상태로 쿼럼 계산에 참여, `GetNodes()`에는 invisible.

**Risk:** 좁은 윈도우지만 invisible voter 생성 가능.

**Suggested fix:** 롤백 블록(line 315)에서 `CmdRemoveNode` apply 전에 `m.raft.RemoveServer(nodeID)`도 호출. `RemoveNode()`가 이미 하는 패턴이고 실제 voter 추가 안 됐으면 멱등.

---

## 요약표

| ID | Sev | Location | Issue |
|----|-----|----------|-------|
| L-01 | P0 | `feature/cluster/handler.go:138-147` | Init 실패 시 잘못된 경로로 RemoveAll → orphan CA+Raft |
| L-02 | P1 | `feature/cluster/handler.go:166-182` | Init 중간 크래시 시 FSM 비어있음 (admin/JWT) |
| L-03 | P1 | `feature/cluster/handler.go:624-657` | Leave config→RemoveAll 순서, 중간 크래시 시 stale state |
| L-04 | P1 | `feature/cluster/handler.go:660-708` | Disband 팔로워 통지 경로 없음 |
| L-05 | P1 | `manager.go:417-435` | Leader Leave에서 TransferLeadership 순서 뒤바뀜 |
| L-06 | P2 | `manager.go:874-898` | 팔로워 로컬 읽기는 stale FSM status |
| L-07 | P2 | `tls.go:150-206` | TLS cert 회전 경로 부재 (캐싱 + baked-in creds) |
| L-08 | P2 | `manager.go:301-317` | HandleJoin AddVoter 실패 롤백이 RemoveServer 누락 |
