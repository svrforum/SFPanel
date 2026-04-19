# SFPanel 클러스터 서브시스템 설계 맵

**작성 일시**: 2026-04-19  
**대상**: `internal/cluster/`, `internal/feature/cluster/`, `cmd/sfpanel/cluster_commands.go`  
**상태**: 2026-04-13 설계 문서 기준으로 검증 완료 ✅

---

## 목차

1. [아키텍처 개요](#아키텍처-개요)
2. [컴포넌트별 책임](#컴포넌트별-책임)
3. [복제 상태 카탈로그](#복제-상태-카탈로그)
4. [조인 흐름 시퀀스](#조인-흐름-시퀀스)
5. [프록시 미들웨어 동작 매트릭스](#프록시-미들웨어-동작-매트릭스)
6. [실패 모드 매트릭스](#실패-모드-매트릭스)
7. [설계 대비 현황](#설계-대비-현황)

---

## 아키텍처 개요

### ASCII 다이어그램

```
┌─────────────────────────────────────────────────────────────────────┐
│ SFPanel 클러스터 아키텍처                                             │
└─────────────────────────────────────────────────────────────────────┘

                        ┌──────────────────────────┐
                        │   HTTP 클라이언트         │
                        │   (UI / CLI / 외부 API)  │
                        └──────────┬───────────────┘
                                   │
                        ┌──────────▼───────────────┐
                        │  Echo HTTP 라우터         │
                        │  :8443 (기본)            │
                        └──────────┬───────────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │                             │
        ┌───────────▼────────────┐    ┌─────────▼──────────────┐
        │  인증 미들웨어           │    │  프록시 미들웨어        │
        │  (JWT, mTLS)           │    │  (클러스터 모드)       │
        └───────────┬────────────┘    └─────────┬──────────────┘
                    │                           │
    (비클러스터 모드)│          (클러스터 모드)  │
                    │                           │
        ┌───────────▼──────────────────────────▼──────────┐
        │  로컬 핸들러 체인                                  │
        │  (노드, 컨테이너, 서비스 등)                       │
        └────────────────────────────────────────────────────┘


     리더/팔로워 노드 (각각)
    ┌──────────────────────────────────────────┐
    │                                          │
    │ ┌──────────────────────────────────────┐ │
    │ │ Cluster Manager                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ Raft FSM                       │   │ │
    │ │ │ ├─ Nodes (map[id]*Node)        │   │ │
    │ │ │ ├─ Config (map[key]string)     │   │ │
    │ │ │ │  ├─ cluster_name             │   │ │
    │ │ │ │  ├─ jwt_secret               │   │ │
    │ │ │ │  └─ raft_tls                 │   │ │
    │ │ │ └─ Accounts (map[user]*Acct)   │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ │                                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ RaftNode                       │   │ │
    │ │ │ ├─ Raft 합의 엔진              │   │ │
    │ │ │ ├─ RaftTLS (노드간)            │   │ │
    │ │ │ └─ Persistent 로그/스냅샷      │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ │                                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ TLSManager                     │   │ │
    │ │ │ ├─ CA cert (클러스터 전체)     │   │ │
    │ │ │ ├─ Node cert (mTLS)           │   │ │
    │ │ │ └─ Node key                   │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ │                                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ TokenManager                   │   │ │
    │ │ │ ├─ JoinToken 생성/검증        │   │ │
    │ │ │ └─ TTL 관리 (기본 24h)        │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ │                                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ HeartbeatManager               │   │ │
    │ │ │ ├─ 노드 상태 모니터링          │   │ │
    │ │ │ └─ 메트릭 수집 (60초 간격)    │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ │                                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ ConnPool (gRPC 연결 풀)        │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ │                                      │ │
    │ │ ┌────────────────────────────────┐   │ │
    │ │ │ EventBus                       │   │ │
    │ │ │ (상태 변경 이벤트)             │   │ │
    │ │ └────────────────────────────────┘   │ │
    │ └──────────────────────────────────────┘ │
    │                                          │
    │ ┌──────────────────────────────────────┐ │
    │ │ gRPC 서버 (:9444)                    │ │
    │ │ ├─ PreFlight(token)                 │ │
    │ │ ├─ Join(req) → (certs, jwt_secret) │ │
    │ │ ├─ Leave(nodeId)                    │ │
    │ │ ├─ Heartbeat (스트림)               │ │
    │ │ ├─ ProxyRequest (리더 포워딩)       │ │
    │ │ ├─ GetMetrics                       │ │
    │ │ └─ Subscribe (이벤트 스트림)        │ │
    │ └──────────────────────────────────────┘ │
    │                                          │
    └──────────────────────────────────────────┘


 클러스터 간 통신
    ┌─────────┐         Raft TLS        ┌─────────┐
    │ 리더     │ ◄──────────────────────► │ 팔로워-1 │
    │ (9445)  │        (TCP :9445)      │ (9445)  │
    └────┬────┘                         └─────────┘
         │
         │ mTLS (gRPC)
         │ 조인, 메트릭, 하트비트
         │
         ├─────────────────────────────┐
         │                             │
    ┌────▼─────┐               ┌──────▼────┐
    │ 팔로워-2  │               │ 팔로워-3   │
    │ (9445)   │               │ (9445)    │
    └──────────┘               └───────────┘
```

### 노드 간 통신 프로토콜

**Raft TLS (노드간 합의)**
- TCP 포트: `api_port + 1` (기본 9445)
- 프로토콜: hashicorp/raft (암호화되지 않음 또는 Opt-in TLS)
- 용도: Raft 로그 복제, 리더 선출, 스냅샷 전송
- TLS: `config.Cluster.RaftTLS` 플래그로 제어

**mTLS over gRPC (클러스터 RPC)**
- TCP 포트: `grpc_port` (기본 9444)
- 프로토콜: gRPC + TLS 인증서 (CA/node cert)
- 용도: 조인, 하트비트, 메트릭, 프록시 포워딩
- 인증: 클라이언트 인증서 (선택적 - 조인 시 null 허용)

---

## 컴포넌트별 책임

### 1. `Manager` (`manager.go`)

**책임**
- 클러스터 노드의 중앙 조정자
- Raft, TLS, 토큰 관리자, 하트비트 모니터 생명주기 관리
- 공개 API: `Init()`, `Start()`, `Shutdown()`
- 노드 추가/제거 및 상태 쿼리

**주요 메서드**

| 메서드 | 리더 전용 | 용도 |
|--------|---------|------|
| `Init(name)` | N/A | 새 클러스터 부트스트랩 (리더가 됨) |
| `Start()` | N/A | 기존 클러스터 노드 시작 |
| `Shutdown()` | N/A | 클러스터 정리 및 종료 |
| `HandleJoin(nodeId, name, apiAddr, grpcAddr, token)` | ✓ | 조인 요청 처리, 인증서 발급 |
| `HandlePreFlight(token)` | ✓ | 토큰 사전 검증 (소비하지 않음) |
| `GetJWTAndAdmin()` | ✓ | 리더의 JWT 시크릿 및 어드민 계정 반환 |
| `RemoveNode(nodeId)` | ✓ | Raft를 통해 노드 제거 |
| `GetRaft()` | ✓ | Raft 엔진 접근 |
| `GetTLS()` | ✓ | TLS 관리자 접근 |
| `GetConfig()` | ✓ | 클러스터 구성 읽기 |
| `SetConfig(key, value)` | ✓ | FSM을 통해 구성 설정 (리더만) |
| `SyncAccountFromDB(user, hash, totp)` | ✓ | FSM에 어드민 계정 동기화 |
| `ProxySecret()` | ✓ | 프록시 인증 시크릿 (CA 인증서 해시) |

**경계**
- **소유**: Raft, TLS, 토큰, 하트비트, 연결 풀
- **위임**: 실제 gRPC 서버 시작은 `cmd/sfpanel/main.go`가 담당
- **제약**: 팔로워는 `HandleJoin`/`RemoveNode`를 거부 (`ErrNotLeader`)

---

### 2. `RaftFSM` (`raft_fsm.go`)

**복제 상태** (클러스터 전체 동기화)

```go
type ClusterState struct {
    Name     string                   // 클러스터 이름
    Nodes    map[string]*Node         // 모든 노드 상태
    Config   map[string]string        // Key-value 설정 (jwt_secret, cluster_name, raft_tls)
    Accounts map[string]*AdminAccount // 동기화된 어드민 계정
}
```

**Raft 명령 타입** (로그에 저장됨)

| 명령 | 페이로드 | 용도 |
|------|--------|------|
| `CmdAddNode` | `Node{ID, Name, APIAddr, GRPCAddr, Role, Status}` | 노드 조인 |
| `CmdRemoveNode` | Key=nodeId | 노드 제거 |
| `CmdUpdateNode` | `Node` (부분 업데이트) | 주소/상태/레이블 변경 |
| `CmdSetConfig` | Key=key, Value=JSON 문자열 | JWT 시크릿, 클러스터명, raft_tls 플래그 설정 |
| `CmdDeleteConfig` | Key=key | 구성 삭제 |
| `CmdSetAccount` | `AdminAccount{Username, Password, TOTPSecret}` | 어드민 계정 동기화 |
| `CmdDeleteAccount` | Key=username | 계정 삭제 |

**로컬 전용 상태** (Raft 미사용)
- 하트비트 메트릭 (CPU, 메모리, 디스크)
- 하트비트 타이머 및 의심 상태
- gRPC 연결 캐시

**스냅샷**
- `ClusterState` 전체를 JSON으로 직렬화
- Raft 로그 크기 제한 시 자동 생성

---

### 3. `GRPCServer` (`grpc_server.go`)

**공개 RPC 서비스**

```protobuf
service ClusterService {
    rpc PreFlight(PreFlightRequest) returns (PreFlightResponse);
    rpc Join(JoinRequest) returns (JoinResponse);
    rpc Leave(LeaveRequest) returns (LeaveResponse);
    rpc Heartbeat(stream HeartbeatPing) returns (stream HeartbeatPong);
    rpc ProxyRequest(APIRequest) returns (APIResponse);
    rpc GetMetrics(MetricsRequest) returns (MetricsResponse);
    rpc Subscribe(SubscribeRequest) returns (stream ClusterEvent);
}
```

**각 RPC 동작**

| RPC | 인증 | 리더전용 | 설명 |
|-----|------|---------|------|
| **PreFlight** | ✗ | ✓ | 토큰 유효성 검사 (소비 안 함), 클러스터 정보 반환 |
| **Join** | ✗ | ✓ | 노드 등록, 인증서 발급, JWT/어드민 포함하여 반환 |
| **Leave** | mTLS | ✓ | 노드 제거 (자신 제거 불가) |
| **Heartbeat** | mTLS | ✓ | 양방향 스트림: 팔로워→리더 메트릭, 리더→팔로워 리더ID |
| **ProxyRequest** | 헤더 | ✗ | 요청을 로컬 HTTP로 포워드, 비클러스터 환경과 동일한 응답 |
| **GetMetrics** | mTLS | ✓ | 노드 메트릭 조회 |
| **Subscribe** | mTLS | ✓ | 이벤트 스트림 구독 (노드 조인/제거/상태 변경) |

**프록시 시크릿**
- CA 인증서의 SHA256 해시
- 미들웨어가 `X-SFPanel-Internal-Proxy: <secret>` 검증에 사용
- 로컬 HTTP 프록시 요청 인증

---

### 4. `JoinEngine` (`join.go`)

**책임**
- CLI와 Web UI가 공유하는 조인 파이프라인
- 사전 비행(PreFlight) 검증 및 실행
- 롤백 가능한 원자적 상태 전환

**공개 인터페이스**

```go
type JoinEngine struct {
    ConfigPath   string
    Config       *config.Config
    DB           *sql.DB
    OnActivate   LiveActivateFunc  // nil = config-only 모드
}

type LiveActivateFunc func(cfg *config.Config, cfgPath string, existingMgr *Manager) (*Manager, error)

func (e *JoinEngine) PreFlight(leaderAddr, token string) (*PreFlightResult, error)
func (e *JoinEngine) Execute(leaderAddr, token, advertiseAddr string) (*JoinResult, error)
```

**PreFlight 파이프라인** (5단계)

1. TCP 연결 테스트 (리더 도달 가능성)
2. gRPC PreFlight RPC 호출 (`TokenManager.Peek()` 사용, 토큰 소비 안 함)
3. 로컬 gRPC 포트 가용성 확인
4. IP 자동 감지 (리더 주소 기반)
5. 결과 반환: 클러스터명, 노드 수, 권장 IP

**Execute 파이프라인** (6단계, 롤백 가능)

1. gRPC Join RPC 호출 → CA/노드 인증서, JWT 시크릿, 어드민 계정 받음
2. 인증서 저장 (`CertDir/`)
3. Config 업데이트 (cluster.enabled=true, jwt_secret, raft_tls 등)
4. Config 파일 원자 저장 (원본 백업)
5. DB에서 어드민 자격증명 동기화 (있으면)
6. LiveActivate 콜백 호출 (있으면) → Manager + gRPC 서버 시작

**롤백 전략**
- Step 1 실패 → 로컬 변경 없음, 에러만 반환
- Step 2 실패 → CertDir 삭제
- Step 4 실패 → CertDir 삭제, 원본 config 복원
- Step 6 실패 → CertDir 삭제, 원본 config 복원

---

### 5. `TLSManager` (`tls.go`)

**책임**
- CA 인증서 생성 및 관리
- 노드별 mTLS 인증서 발급

**주요 메서드**

| 메서드 | 용도 |
|--------|------|
| `InitCA(clusterName)` | CA 인증서/키 생성 |
| `IssueNodeCert(nodeId, []ipAddrs)` | 노드 인증서 발급 (1년 TTL) |
| `SaveCACert(pemData)` | CA 인증서 저장 (`/etc/sfpanel/cluster/ca.crt`) |
| `SaveNodeCert(certPEM, keyPEM)` | 노드 인증서 저장 (`node.crt`, `node.key`) |
| `LoadCACert() → []byte` | CA 인증서 로드 |
| `ServerTLSConfig() → *tls.Config` | gRPC 서버 TLS 설정 (mTLS 요청) |
| `ClientTLSConfig() → *tls.Config` | gRPC 클라이언트 TLS 설정 (인증서 기반) |

**파일 위치** (CertDir = `/etc/sfpanel/cluster`)
- `ca.crt` — 클러스터 CA 인증서
- `ca.key` — CA 개인키 (리더만 보유)
- `node.crt` — 이 노드의 인증서
- `node.key` — 이 노드의 개인키

---

### 6. `TokenManager` (`token.go`)

**책임**
- 조인용 일회용 토큰 생성
- TTL 기반 검증

**공개 메서드**

| 메서드 | 용도 | 반환값 |
|--------|------|--------|
| `Create(ttl, createdBy)` | 토큰 생성 | `*JoinToken` |
| `Peek(token)` | 토큰 검증 (소비 안 함) | 에러: ErrTokenNotFound, ErrTokenExpired, ErrTokenUsed |
| `Validate(token)` | 토큰 검증 (소비함) | 에러: ErrTokenNotFound, ErrTokenExpired, ErrTokenUsed |
| `RestoreToken(token)` | 토큰 미사용 상태로 복원 | 없음 |

**토큰 상태 머신**

```
생성됨 ─Peek(1)─> 검증됨 ─Peek(2)─> 검증됨
  │                 │                │
  └─Validate──> 사용됨               │
                                    │
                         Validate──> 사용됨
```

**기본값**
- TTL: 24시간 (ConfigAPI로 생성 시)
- 메모리 저장 (휘발성, 리더 재시작 시 손실)
- 자동 정리: 만료되거나 사용된 토큰 삭제

---

### 7. `HeartbeatManager` (`heartbeat.go`)

**책임**
- 팔로워 → 리더 메트릭 수집
- 노드 온라인/오프라인 상태 감시

**동작**
- 간격: 60초
- 타임아웃: 180초 (3분)
- 의심 상태: 1회 실패 후 상태 = "suspect"
- 오프라인 상태: 3회 연속 실패 후 상태 = "offline"

**콜백**
- `OnStatusChange(nodeId, newStatus)` — 상태 변경 시 호출

---

### 8. `DetectAdvertiseAddress` (`detect.go`)

**책임**
- 리더와 통신 가능한 로컬 IP 감지

**전략** (순서대로 시도)

1. **Tailscale 감지**: 리더 IP가 100.64.0.0/10 범위
   → 로컬 Tailscale IP 찾기 (100.x.x.x)

2. **같은 서브넷**: 리더 IP와 같은 /24 서브넷의 로컬 IP 찾기

3. **TCP 다이얼**: 리더에 TCP 연결, 로컬 주소 추출
   → `net.Dial("tcp", leaderAddr)` → `conn.LocalAddr().IP`

4. **모두 실패**: 에러 반환 (127.0.0.1 폴백 없음)

**보조 함수**
- `IsTailscaleIP(ip)` — 100.64.0.0/10 범위 확인 (익스포트됨, 핸들러가 사용)
- `DetectFallbackIP()` — 첫 번째 non-loopback IPv4 (Init 전용)

---

### 9. `Proxy Middleware` (`middleware/proxy.go`)

**책임**
- 비리더 노드의 요청을 리더로 포워드

**요청 흐름**

```
클라이언트 요청 → [프록시 미들웨어]
                 ├─ 클러스터 모드 아님? → 로컬 핸들러 (차단 없음)
                 ├─ 리더? → 로컬 핸들러 (차단 없음)
                 ├─ 비리더?
                 │  ├─ 스트리밍 엔드포인트? → SSE 직렬 릴레이
                 │  └─ 일반 엔드포인트? → gRPC ProxyRequest 호출
                 │     ├─ 성공? → 응답 반환
                 │     └─ 실패? → 503 Service Unavailable
```

**인증 방식**

| 요청 타입 | 인증 헤더 |
|----------|----------|
| 로컬 프록시 | `X-SFPanel-Internal-Proxy: <CA_SHA256>` |
| 원본 요청 | `Authorization: Bearer <JWT>` (그대로 전달) |
| 사용자 정보 | `X-SFPanel-Original-User: <username>` (추적용) |

**스트리밍 엔드포인트** (SSE 직렬)
- 경로 패턴: `*-stream`, `/system/update`, `/appstore/apps/*/install`
- 처리: HTTP 직접 연결, 이벤트 실시간 전달 (gRPC 아님)

---

### 10. `Handler` (`internal/feature/cluster/handler.go`)

**책임**
- HTTP REST API 제공
- JoinEngine 호출
- 클러스터 상태 조회

**공개 엔드포인트**

| 메서드 | 경로 | 기능 | 사전조건 |
|--------|------|------|---------|
| POST | `/api/v1/cluster/init` | 새 클러스터 부트스트랩 | 미가입 |
| POST | `/api/v1/cluster/join` | 기존 클러스터 조인 | 미가입 |
| GET | `/api/v1/cluster/status` | 클러스터 상태 조회 | 없음 |
| POST | `/api/v1/cluster/token/create` | 조인 토큰 생성 | 리더 |
| GET | `/api/v1/cluster/nodes` | 노드 목록 조회 | 없음 |
| POST | `/api/v1/cluster/remove` | 노드 제거 | 리더 |
| GET | `/api/v1/cluster/network-interfaces` | 로컬 네트워크 인터페이스 | 없음 |

**동시성 보호**
- `joiningMu` — Init/Join 중복 방지 (TryLock 사용)
- `configMu` — Config 필드 업데이트 동기화
- `mu` (RWMutex) — Manager 포인터 읽기/쓰기

**OnManagerActivated 콜백**
- Init/Join 후 Manager 설정 완료 시 호출
- 다른 핸들러 (인증 등)가 Manager 포인터 갱신 가능

---

### 11. CLI 명령 (`cmd/sfpanel/cluster_commands.go`)

**서버 인식 위임**

모든 cluster 서브명령은:
1. 서버 실행 여부 확인 (`isServerRunning()`)
2. 실행 중 → `callLocalAPI()` 호출 (동일 Config의 JWT 사용)
3. 미실행 → JoinEngine 직접 호출 (config-only 모드)

**서브명령**

| 명령 | 기능 | 서버 위임 |
|------|------|---------|
| `init [--name] [--advertise IP]` | 클러스터 초기화 | ✓ |
| `join <addr> <token> [--advertise IP]` | 클러스터 조인 | ✓ |
| `token [--ttl]` | 조인 토큰 생성 | ✓ (리더 필요) |
| `status` | 상태 조회 | ✓ |
| `remove <nodeId>` | 노드 제거 | ✓ (리더 필요) |
| `leave` | 클러스터 이탈 | ✓ (팔로워만) |

---

### 12. `Config` 구조

**클러스터 구성** (`config.ClusterConfig`)

```go
type ClusterConfig struct {
    Enabled          bool   // 클러스터 모드 활성화
    Name             string // 클러스터 이름
    NodeID           string // 이 노드의 UUID
    NodeName         string // 호스트명
    AdvertiseAddress string // 다른 노드가 도달 가능한 IP
    APIPort          int    // HTTP 포트 (기본 8443)
    GRPCPort         int    // gRPC 포트 (기본 9444)
    CertDir          string // TLS 인증서 디렉터리
    DataDir          string // Raft 로그/스냅샷 디렉터리
    RaftTLS          bool   // Raft 노드간 TLS 암호화 활성화
}
```

**Auth 구성에 동기화됨**
- `Config.Auth.JWTSecret` → FSM `Config["jwt_secret"]`로 복제
- Join 응답에 포함되어 팔로워가 자동 수신

---

## 복제 상태 카탈로그

### Raft FSM을 통해 복제되는 데이터

| 항목 | 타입 | 리더 수정 권한 | 팔로워 로컬 읽기 | 용도 |
|------|------|--------------|-------------|------|
| **Nodes** | map[id]*Node | 리더만 | ✓ | 클러스터 멤버십, 헬스 상태 |
| **Config** | map[string]string | 리더만 | ✓ | jwt_secret, cluster_name, raft_tls |
| **Accounts** | map[user]*AdminAccount | 리더만 | ✓ | 어드민 계정 동기화 (username, password, totp) |

### 로컬 전용 상태 (복제 안 됨)

| 항목 | 저장소 | 용도 |
|------|--------|------|
| gRPC 연결 풀 | ConnPool 메모리 | 리더로의 연결 재사용 |
| 하트비트 메트릭 | HeartbeatManager 메모리 | CPU, 메모리, 디스크 % |
| 노드 의심 타이머 | HeartbeatManager 메모리 | 온라인/의심/오프라인 상태 전환 |
| 구성 파일 | `/etc/sfpanel/config.yaml` | 부팅 후 노드 식별 |
| Raft 로그 | `/var/lib/sfpanel/cluster/` | 합의 보장 |
| TLS 인증서 | `/etc/sfpanel/cluster/` | mTLS 통신 |

---

## 조인 흐름 시퀀스

### End-to-End: CLI에서 클러스터 조인

```
사용자: sfpanel cluster join 192.168.1.100:9444 abc123def456

┌─────────────────────┐
│ cluster_commands.go │
│   clusterJoin()     │
└──────────┬──────────┘
           │
           ├─ 1. 서버 실행 중? (TCP :8443)
           │    Yes → callLocalAPI 위임
           │    No  → JoinEngine 직접 실행 (단계 2→)
           │
           │
        2. [JoinEngine.PreFlight()]
           │
           ├─ 2a. TCP connect 192.168.1.100:9444 (3s timeout)
           │      ├─ 성공? 계속
           │      └─ 실패? 에러: "cannot reach leader"
           │
           ├─ 2b. gRPC 클라이언트 생성 (DialNodeInsecure)
           │      ├─ 성공? 계속
           │      └─ 실패? 에러: "gRPC not responding"
           │
           ├─ 2c. PreFlight RPC 호출
           │      │
           │      └─> [리더의 GRPCServer.PreFlight]
           │         ├─ TokenManager.Peek(token)
           │         │  ├─ 토큰 미존재? → ErrTokenNotFound
           │         │  ├─ 토큰 만료? → ErrTokenExpired
           │         │  ├─ 토큰 사용됨? → ErrTokenUsed
           │         │  └─ 유효? 계속
           │         ├─ FSM 읽기 (클러스터명, 노드 수, max 노드)
           │         └─ PreFlightResponse 반환
           │
           │      ├─ 유효하지 않음? 에러 반환
           │      └─ 유효? 계속
           │
           ├─ 2d. 로컬 gRPC 포트 가용성 확인 (:9444)
           │      ├─ 사용 중? → 에러: "gRPC port already in use"
           │      └─ 가용? 계속
           │
           ├─ 2e. IP 자동 감지 (DetectAdvertiseAddress)
           │      ├─ Tailscale? 로컬 TS IP 찾기
           │      ├─ 같은 서브넷? 로컬 IP 찾기
           │      ├─ 다이얼? TCP 연결의 로컬 주소 사용
           │      └─ 모두 실패? 추천 IP 없음 (사용자 입력 요청)
           │
           └─ 2f. PreFlightResult 반환
                 ├─ clusterName: "my-cluster"
                 ├─ nodeCount: 2
                 ├─ maxNodes: 32
                 ├─ recommendedIP: "192.168.1.101"
                 └─ reason: "same network as leader"
              
              [여기서 사전 검증 완료, 실제 조인은 아직 아님]
              
        3. [JoinEngine.Execute(leaderAddr, token, advertiseAddr)]
           │
           ├─ 3a. advertiseAddr 최종 결정
           │      ├─ 사용자 입력? 사용
           │      ├─ 없으면 auto-detect 사용
           │      └─ 실패? 에러
           │
           ├─ 3b. NodeID 생성 (uuid.New())
           │
           ├─ 3c. Join RPC 호출
           │      │
           │      └─> [리더의 GRPCServer.Join()]
           │         ├─ Manager.HandleJoin(nodeId, name, apiAddr, grpcAddr, token)
           │         │  │
           │         │  ├─ TokenManager.Validate(token)  ◄─ 토큰 '소비'
           │         │  │  ├─ 토큰 불유효? ErrTokenNotFound/Expired/Used 반환
           │         │  │  └─ 유효? 토큰 마킹 (Used=true) 계속
           │         │  │
           │         │  ├─ 노드 ID 중복 확인 (ErrNodeAlreadyExists)
           │         │  │
           │         │  ├─ TLSManager.IssueNodeCert(nodeId, [apiAddr])
           │         │  │  └─ 클라이언트 인증서 생성 (1년 TTL)
           │         │  │
           │         │  ├─ Raft FSM Apply: CmdAddNode
           │         │  │  └─ FSM.Nodes[nodeId] = Node{...}
           │         │  │
           │         │  ├─ FSM 상태 읽기 (Config, Accounts)
           │         │  │
           │         │  └─ (caCert, nodeCert, nodeKey, peers, jwt_secret, admin_username, admin_password_hash) 반환
           │         │
           │         ├─ JoinResponse 생성
           │         └─ 반환
           │
           │      ├─ Join 실패? 에러: "join rejected: ..."
           │      └─ 성공? 계속
           │
           ├─ 3d. 원본 config 백업
           │
           ├─ 3e. 인증서 저장
           │      ├─ TLSManager.SaveCACert(resp.CaCert)
           │      │  └─ /etc/sfpanel/cluster/ca.crt
           │      ├─ TLSManager.SaveNodeCert(resp.NodeCert, resp.NodeKey)
           │      │  └─ /etc/sfpanel/cluster/node.{crt,key}
           │      └─ 실패? rollback: certDir 삭제, 에러 반환
           │
           ├─ 3f. Config 업데이트
           │      ├─ cluster.enabled = true
           │      ├─ cluster.name = resp.clusterName
           │      ├─ cluster.nodeId = nodeId
           │      ├─ cluster.advertiseAddress = advertiseAddr
           │      ├─ cluster.gRPCPort = grpcPort
           │      ├─ cluster.raftTLS = resp.raftTls
           │      ├─ auth.jwtSecret = resp.jwtSecret (있으면)
           │      └─ 계속
           │
           ├─ 3g. Config 원자 저장 (/etc/sfpanel/config.yaml)
           │      └─ 실패? rollback: certDir 삭제, 원본 config 복원, 에러 반환
           │
           ├─ 3h. 로컬 DB 어드민 계정 동기화 (있으면)
           │      └─ UPDATE admin SET password = ? WHERE username = ?
           │
           ├─ 3i. LiveActivate 콜백 호출 (있으면)
           │      │
           │      └─ [main.go의 liveActivate 클로저]
           │         ├─ cluster.NewManager(config)
           │         ├─ manager.Start()
           │         │  ├─ RaftNode 시작 (Bootstrap=false, 기존 로그 로드)
           │         │  └─ HeartbeatManager 시작
           │         ├─ cluster.NewGRPCServer(manager, apiPort)
           │         ├─ grpcServer.Start(":9444")
           │         ├─ middleware.SetClusterProxySecret(...)
           │         └─ Manager 반환
           │         
           │      ├─ 콜백 실패? rollback: certDir 삭제, 원본 config 복원, 에러 반환
           │      └─ 성공? 계속
           │
           ├─ 3j. Handler.Manager 포인터 업데이트
           │
           └─ 3k. 성공 응답 반환
                 ├─ clusterName
                 ├─ nodeId
                 └─ live: true (if LiveActivate succeeded)

[프로세스 완료]

CLI 출력:
  ✓ Successfully joined cluster 'my-cluster'
  ✓ Node ID: <nodeId>
  ✓ Live activation complete (웹 UI 즉시 사용 가능)
  
비즈니스 로직:
  - 같은 프로세스 내에서 클러스터 모드 활성화
  - 팔로워가 즉시 리더에 하트비트 전송
  - 리더가 이 노드의 주소 기록
  - 웹 UI/API 프록시 미들웨어 활성화
```

### Init 흐름 (새 클러스터 생성)

```
사용자: POST /api/v1/cluster/init
  Body: {"name": "my-cluster", "advertise_address": "192.168.1.100"}

┌─────────────────────┐
│ Handler.InitCluster │
└──────────┬──────────┘
           │
        1. 검증
           ├─ 이미 클러스터 모드? → HTTP 400 "Already part of a cluster"
           ├─ ConfigPath 없음? → HTTP 500
           └─ 계속
           │
        2. advertiseAddress 결정
           ├─ 요청된 주소? 사용
           ├─ 없으면 DetectFallbackIP() 시도
           └─ 실패? HTTP 400 "Cannot detect..."
           │
        3. Config 업데이트 (메모리)
           ├─ cluster.advertiseAddress = addr
           ├─ cluster.gRPCPort = port (기본 9444)
           ├─ cluster.apiPort = 8443
           └─ 계속
           │
        4. Manager.Init(clusterName)
           │
           └─> [Manager.Init() 시작]
              │
              ├─ TLS CA 생성
              │  └─ /etc/sfpanel/cluster/ca.{crt,key}
              │
              ├─ Node cert 발급 및 저장
              │  └─ /etc/sfpanel/cluster/node.{crt,key}
              │
              ├─ RaftNode 시작 (Bootstrap=true)
              │  └─ Leader로 자동 선출 (단일 노드)
              │
              ├─ Raft FSM Apply: CmdAddNode (self)
              │
              ├─ Raft FSM Apply: CmdSetConfig (cluster_name)
              │
              ├─ HeartbeatManager 시작
              │
              └─ ✓ Init 완료, Manager 반환
           │
        5. Config 원자 저장 (/etc/sfpanel/config.yaml)
           ├─ 실패? Manager.Shutdown(), certDir 삭제, 에러 반환
           └─ 성공? 계속
           │
        6. LiveActivate 콜백 (있으면)
           │
           └─> [main.go의 liveActivate 클로저]
              ├─ 4단계에서 만든 Manager 재사용 (existingMgr 파라미터)
              ├─ gRPC 서버만 시작
              └─ Manager 반환
           │
        7. Handler.Manager 포인터 설정
           │
        8. FSM에 jwt_secret, raft_tls 저장
           │
        9. DB에서 어드민 계정 읽고 FSM 동기화
           │
        10. 성공 응답 반환
            ├─ live: true (if LiveActivate succeeded)
            └─ HTTP 200 OK

비즈니스 로직:
  - 새로운 1-노드 클러스터 생성
  - 리더 자동 선출
  - 웹 UI 즉시 사용 가능 (조인 토큰 생성 가능)
```

---

## 프록시 미들웨어 동작 매트릭스

### 요청 처리 흐름

| 상황 | 클러스터 모드 | 노드 역할 | 엔드포인트 타입 | 동작 | 결과 |
|------|-------------|---------|-------------|------|------|
| A1 | ✗ (비활성) | N/A | 모든 요청 | 로컬 핸들러 | 200 (일반 처리) |
| A2 | ✓ | 리더 | 일반 | 로컬 핸들러 | 200 (일반 처리) |
| A3 | ✓ | 리더 | 스트리밍 | 로컬 핸들러 | 200 (SSE 스트림) |
| B1 | ✓ | 팔로워 | 일반 (쓰기) | gRPC ProxyRequest | 200 (리더 응답) |
| B2 | ✓ | 팔로워 | 일반 (쓰기) | gRPC ProxyRequest 실패 | 503 Service Unavailable |
| B3 | ✓ | 팔로워 | 스트리밍 | HTTP 직렬 릴레이 | 200 (SSE 스트림) |
| B4 | ✓ | 팔로워 | 읽기 전용 쿼리 | gRPC ProxyRequest (선택) | 200 (로컬/리더) |

### 인증 헤더 위임

**Case A: 비클러스터 또는 리더**
```
클라이언트 Authorization: Bearer <JWT>
    ↓
로컬 핸들러 (변경 없음, JWT 검증)
```

**Case B: 팔로워 → 리더 프록시**

```
[gRPC ProxyRequest 호출]

클라이언트 Authorization: Bearer <JWT>
    ↓
프록시 미들웨어 (추출, 사용 안 함)
    ↓
gRPC 요청 헤더:
  X-SFPanel-Internal-Proxy: <CA_SHA256>  ◄─ 리더 검증용
  Authorization: Bearer <JWT>            ◄─ 리더 전달용
  X-SFPanel-Original-User: <user>        ◄─ 감사 추적용
    ↓
리더 로컬 핸들러 (JWT 검증)
    ↓
응답 반환
```

### 스트리밍 엔드포인트 특수 처리

**경로 패턴**
- `*-stream` (예: `/up-stream`, `/update-stream`)
- `/system/update`
- `/appstore/apps/*/install`

**처리 방식**
1. 팔로워는 리더의 HTTP API에 직접 연결
2. SSE 이벤트를 실시간으로 클라이언트에 전달 (gRPC 오버헤드 없음)
3. 연결 끊김 시 자동 재시도 없음 (클라이언트 책임)

---

## 실패 모드 매트릭스

### 초기화 실패 시나리오

| 단계 | 실패 원인 | 감지 방법 | 복구 | 결과 |
|------|---------|---------|------|------|
| 1. CA 생성 | 권한 부족 | mkdir/write 에러 | config 원본 유지, 재시도 | HTTP 500 |
| 2. Node cert | 인증서 생성 실패 | crypto 에러 | CA/cert 삭제, config 원본 유지 | HTTP 500 |
| 3. Raft 시작 | DataDir 접근 불가 | raft.NewRaft 에러 | cert 삭제, config 원본 유지 | HTTP 500 |
| 4. Config 저장 | 디스크 부족 | write 에러 | cert/raft 삭제, config 원본 유지 | HTTP 500 |
| 5. gRPC 시작 | 포트 충돌 | listen 에러 | 전체 롤백 | HTTP 500 |

### 조인 실패 시나리오

| 단계 | 실패 원인 | 감지 방법 | 복구 | 결과 |
|------|---------|---------|------|------|
| PreFlight-A | 리더 도달 불가 | TCP timeout | 로컬 변경 없음 | HTTP 502 "cannot reach leader" |
| PreFlight-B | gRPC 미응답 | DialNodeInsecure 에러 | 로컬 변경 없음 | HTTP 502 "gRPC not responding" |
| PreFlight-C | 토큰 불유효 | PreFlight RPC 반환 invalid=true | 로컬 변경 없음 | HTTP 401 "token does not exist" |
| Execute-1 | Join RPC 실패 | Join 호출 에러 | 인증서 삭제 안 함 (아직) | HTTP 500 "join failed" |
| Execute-2 | CA cert 저장 실패 | write 에러 | 인증서 디렉터리만 삭제 | HTTP 500 "failed to save CA cert" |
| Execute-3 | Node cert 저장 실패 | write 에러 | CertDir 전체 삭제 | HTTP 500 "failed to save node cert" |
| Execute-4 | Config 저장 실패 | write 에러 | CertDir 삭제, 원본 config 복원 | HTTP 500 "failed to save config" |
| Execute-5 | LiveActivate 실패 | callback 에러 | CertDir 삭제, 원본 config 복원 | HTTP 200 (config OK, 재시작 필요) |

### 런타임 클러스터 실패 시나리오

| 시나리오 | 감지 | 자동 복구 | 동작 |
|---------|------|---------|------|
| **리더 죽음** | 팔로워 하트비트 타임아웃 (180s) | Raft 재선출 (몇 초) | 팔로워 1→리더, 다른 팔로워는 새 리더에 재연결 |
| **팔로워 죽음** | 리더 하트비트 타임아웃 | status=offline (3 실패 후) | 리더가 FSM에 status 업데이트, 클라이언트는 다른 팔로워로 재시도 |
| **네트워크 분할** | 각 파티션의 Raft 별도 선출 가능 | 수동 개입 필요 | 분할 발생 시 더 작은 파티션은 쓰기 불가 (Raft 합의 규칙) |
| **TLS cert 만료** | 다음 gRPC 연결 시도 시 | 수동 갱신 필요 | API 호출 실패, 조인 거부 |
| **Join 토큰 만료** | PreFlight/Join RPC 시 | 새 토큰 생성 필요 | HTTP 401 "token has expired" |
| **디스크 부족** | Raft 스냅샷 저장 시 | 수동 디스크 정리 | 클러스터 읽기는 OK, 쓰기는 블록 |

### 팔로워에서 리더 포워딩 실패

| 원인 | HTTP 상태 | 클라이언트 대응 |
|------|---------|-------------|
| 리더 gRPC 서버 다운 | 503 Service Unavailable | 재시도 또는 다른 팔로워 |
| 리더 HTTP 로컬 포트 다운 | 503 Service Unavailable | 재시도 또는 다른 팔로워 |
| 요청 타임아웃 (30s 기본) | 504 Gateway Timeout | 재시도 |
| 리더에서 권한 거부 (401) | 401 Unauthorized | 토큰 갱신 후 재시도 |
| 리더 내부 에러 (500) | 500 Internal Server Error | 로그 확인, 수동 개입 |

---

## 설계 대비 현황

### 계획 대비 구현 현황

| 항목 | 계획 (2026-04-13 스펙) | 현황 (실제 코드) | 상태 | 비고 |
|------|---------------------|-------------|------|------|
| **JoinEngine** | 새로 생성 | ✓ 존재 (join.go) | ✅ | PreFlight + Execute 파이프라인 완성 |
| **PreFlight RPC** | 새로 생성 | ✓ 존재 (grpc_server.go:134) | ✅ | TokenManager.Peek() 사용 |
| **JoinResponse 확장** | jwt_secret, admin_username, admin_password_hash | ✓ 포함 (grpc_server.go:126-128) | ✅ | proto에 정의됨 |
| **LiveActivate 콜백** | main.go 주입 | ✓ 존재 | ✅ | 타입: func(cfg, cfgPath, existingMgr) (*Manager, error) |
| **IP 자동 감지** | DetectAdvertiseAddress | ✓ 존재 (detect.go) | ✅ | Tailscale, subnet, TCP dial 전략 포함 |
| **Token Peek 메서드** | 새로 추가 | ✓ 존재 (token.go:386) | ✅ | 토큰 소비하지 않음 |
| **토큰 에러 분리** | ErrTokenNotFound, ErrTokenExpired | ✓ 존재 (errors.go) | ✅ | ErrInvalidToken 제거됨 |
| **rollback 메커니즘** | Execute 실패 시 rollback | ✓ 존재 (join.go:149) | ✅ | rollbackJoin() 함수 |
| **CLI 서버 위임** | isServerRunning 체크 | ✓ 존재 (cluster_commands.go) | ✅ | callLocalAPI 사용 |
| **Handler.OnManagerActivated** | 없음 (스펙 외) | ✓ 존재 (handler.go:57) | ✅ | 실제 구현에서 추가됨 (개선사항) |
| **config.Cluster.RaftTLS** | raft_tls 플래그 | ✓ 존재 | ✅ | Join 응답에 포함 (grpc_server.go:129) |
| **Handler 동시성** | 제한 없음 | ✓ joiningMu, configMu (handler.go:48-49) | ✅ | 추가적 보호 구현됨 |

### 설계 의도 대비 편차

#### 1. LiveActivateFunc 서명 변경 ✅

**계획**
```go
type LiveActivateFunc func(cfg *config.Config, cfgPath string) (*Manager, error)
```

**실제**
```go
type LiveActivateFunc func(cfg *config.Config, cfgPath string, existingMgr *Manager) (*Manager, error)
```

**이유**: Init 흐름에서 Manager를 재사용하기 위해 (Raft 셧다운/재시작 경쟁 조건 피함)
**영향**: 긍정적 - Init의 atomicity 개선

#### 2. 예상 외 추가: Handler.OnManagerActivated 콜백 ✅

**계획**: 스펙에 없음

**실제**: handler.go:55-57에 추가
```go
OnManagerActivated func(*cluster.Manager)
```

**이유**: 다른 핸들러 (인증 등)가 Manager 포인터 갱신 필요
**영향**: 긍정적 - 느슨한 결합

#### 3. 예상 외 추가: Handler 동시성 보호 ✅

**계획**: 제한 없음

**실제**: 
- `joiningMu` — Init/Join 중복 방지
- `configMu` — Config.Cluster 필드 보호
- `mu` (RWMutex) — Manager 포인터 보호

**이유**: 동시 요청 시 상태 일관성
**영향**: 긍정적 - 레이스 컨디션 방지

#### 4. gRPC 서버 시작 위치 ✅

**계획**: JoinEngine.OnActivate에서

**실제**: main.go에서 정의된 liveActivate 클로저에서 (올바름)

**영향**: 올바른 구현 - main.go이 버전, 미들웨어에 접근하므로

#### 5. Raft TLS 플래그 처리 ✅

**계획**: 리더가 init 시 설정

**실제**: 
- Init: NewRaftNode에 RaftTLS=true 설정 (manager.go:93)
- Join: 리더의 RaftTLS를 응답에 포함, 팔로워가 수신 (join.go:178)

**영향**: 올바른 구현

### 최근 커밋에서의 주요 변경

```
6ccbd7f fix: avoid Raft shutdown/reopen race in InitCluster by reusing manager
  → LiveActivateFunc에 existingMgr 파라미터 추가
  
1355a74 fix: data race on Handler.Manager, gRPC server cleanup, rollback ordering
  → Handler 동시성 보호 추가 (joiningMu, configMu, mu)
  → JoinEngine rollback 순서 개선
  
ba5cd60 feat: cluster join redesign — zero-restart, shared engine, pre-flight validation
  → JoinEngine, PreFlight RPC, IP 자동 감지, LiveActivate 도입
```

이 세 커밋이 스펙 구현의 핵심이며, 모두 완료됨.

---

## 추가 아키텍처 노트

### 상태 동기화 흐름

```
리더 노드
├─ FSM.State (Raft 합의)
│  ├─ Nodes
│  ├─ Config
│  └─ Accounts
└─ 로컬 상태 (메모리)
   ├─ gRPC 연결 풀
   └─ 하트비트 메트릭

팔로워 노드 (N개)
├─ FSM.State (Raft 합의를 통해 복제)
│  ├─ Nodes
│  ├─ Config
│  └─ Accounts
└─ 로컬 상태 (메모리)
   ├─ gRPC 연결 풀 (리더로)
   └─ 하트비트 메트릭 (자신)

[리더 ↔ 팔로워 하트비트 스트림]
팔로워 → 리더: CPU%, Mem%, Disk%, Container 수, 버전
리더 → 팔로워: 리더 ID, 타임스탬프

[리더 ↔ 팔로워 Raft 복제]
리더: Raft 로그 항목 발행
팔로워: 로그 수신 → FSM Apply → State 동기화
```

### 프록시 미들웨어 요청 라우팅

```
HTTP 요청 도착
    ↓
[Auth 미들웨어] JWT/mTLS 검증
    ↓
[프록시 미들웨어]
    ├─ 클러스터 모드 아님? → PASS (로컬 핸들러로)
    ├─ 이 노드가 리더? → PASS (로컬 핸들러로)
    ├─ 이 노드가 팔로워?
    │  ├─ 스트리밍 엔드포인트?
    │  │  └─ HTTP 직렬 릴레이 (리더 IP:port)
    │  └─ 일반 엔드포인트?
    │     └─ gRPC ProxyRequest (리더의 gRPC 서버로)
    └─ ...
    ↓
[로컬 핸들러]
    ├─ 요청 처리
    └─ 응답
```

### Raft 로그 구조

```
[Raft 로그 파일]
/var/lib/sfpanel/cluster/raft/
├─ logs.db (현재 로그)
├─ meta.json (현재 항 기록)
└─ snapshots/
   ├─ snapshot-<index>-<term>.snap
   └─ ...

[로그 항목 예]
{
  "type": "CmdAddNode",
  "value": {
    "id": "uuid-...",
    "name": "node-2",
    "api_address": "192.168.1.101:8443",
    "grpc_address": "192.168.1.101:9444",
    "role": "voter",
    "status": "online"
  }
}
```

---

## 알려진 제약 및 미해결 사항

### 1. 토큰 휘발성

- 토큰은 메모리에만 저장 (Leader 재시작 시 손실)
- 대응: 토큰 TTL 짧음 (기본 24h), 재생성 쉬움

### 2. 마이그레이션 경로 없음

- 기존 비클러스터 노드를 클러스터로 변환할 수 없음 (오직 신규 Init 또는 Join만)
- 대응: 초기 설정 시에만 고려

### 3. 수동 Raft 멤버십 관리 없음

- `raft member add/remove` 같은 저수준 명령 없음
- Join/Leave API만 존재
- 대응: 웹 UI 또는 CLI로 충분

### 4. TLS 인증서 만료 감시 없음

- 1년 TTL로 발급, 만료 경고 없음
- 대응: 수동 갱신 프로세스 필요 (향후 개선)

### 5. 분할 뇌 시나리오 제한 없음

- 네트워크 분할 시 양쪽 모두 리더 선출 가능
- Raft 안전성: 더 작은 파티션은 합의 불가 (쓰기 불가)
- 대응: 네트워크 설계 및 모니터링 권장

---

## 요약

SFPanel의 클러스터 서브시스템은 **2026-04-13 설계 문서를 완벽하게 구현**했습니다.

**핵심 성과:**
1. ✅ Zero-restart 조인 (LiveActivate)
2. ✅ 공유된 JoinEngine (CLI/Web)
3. ✅ 사전 비행(PreFlight) 검증
4. ✅ 리더 인식 IP 자동 감지
5. ✅ 원자적 롤백 (조인 실패 시)
6. ✅ FSM을 통한 클러스터 상태 복제
7. ✅ 팔로워 → 리더 자동 프록시
8. ✅ mTLS 및 Raft TLS

**추가 개선사항:**
- Handler 동시성 보호
- OnManagerActivated 콜백
- LiveActivateFunc existingMgr 파라미터

모든 주요 컴포넌트가 명확한 책임 경계를 가지며, 조인 흐름은 견고한 에러 처리 및 롤백 메커니즘을 갖추고 있습니다.

