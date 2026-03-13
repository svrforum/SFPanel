# SFPanel Cluster 설계 문서

## 개요

SFPanel에 Proxmox 스타일 대칭 클러스터 기능을 추가한다.
동일 바이너리로 단독/클러스터 모두 동작하며, 어느 노드에 접속해도 전체 클러스터를 관리할 수 있다.

**목표:**
- 2~32대 노드를 하나의 클러스터로 묶어 통합 관리
- 13개 기능 전부 원격 노드에서 실행 가능
- 단일 바이너리 유지 (`sfpanel` 하나로 동작)
- 외부 의존성 없음 (PostgreSQL, Redis 등 불필요)
- 리더 장애 시 자동 승격 (Raft 합의)

**범위 제외 (후속 작업):**
- 멀티유저 / RBAC
- 컨테이너 라이브 마이그레이션
- 볼륨 복제

---

## 아키텍처

```
┌─────────── SFPanel Cluster ───────────────────────┐
│                                                     │
│  Node A             Node B            Node C        │
│  ┌──────────┐      ┌──────────┐      ┌──────────┐  │
│  │ SFPanel  │      │ SFPanel  │      │ SFPanel  │  │
│  │ (Leader) │      │(Follower)│      │(Follower)│  │
│  │          │      │          │      │          │  │
│  │ Raft DB ◄──────► Raft DB ◄──────► Raft DB  │  │
│  │ (rqlite) │      │ (rqlite) │      │ (rqlite) │  │
│  │          │      │          │      │          │  │
│  │ gRPC ◄─────────► gRPC ◄─────────► gRPC     │  │
│  │ WS Relay │      │ WS Relay │      │ WS Relay │  │
│  └──────────┘      └──────────┘      └──────────┘  │
│                                                     │
│  동일 바이너리 / 어느 노드든 풀 UI / 자동 리더 선출  │
└─────────────────────────────────────────────────────┘
```

### 설계 원칙

| 원칙 | 설명 |
|------|------|
| **단일 바이너리** | 클러스터/단독 모드 구분 없이 동일 바이너리. 클러스터 미설정 시 기존과 100% 동일 |
| **대칭 노드** | 모든 노드가 동등. 어느 노드 UI에서든 전체 관리 가능 |
| **로컬 우선** | 각 노드는 독립적으로 자기 서버 관리 가능. 클러스터 분리돼도 로컬 기능 유지 |
| **제로 외부 의존** | Raft DB 내장. PostgreSQL, etcd 등 외부 서비스 불필요 |
| **점진적 확장** | 단독 사용 → `cluster init` → `cluster join`으로 점진적 확장 |

---

## 핵심 결정 사항

| 항목 | 결정 | 근거 |
|------|------|------|
| 토폴로지 | Proxmox 스타일 대칭 클러스터 | 모든 노드 동등, 리더 자동 선출 |
| 규모 | 2~32대 (쿼럼 3대 이상 권장) | Proxmox 동일 기준 |
| 관리 범위 | 13개 기능 전부 | 원격 노드의 Docker/파일/터미널 등 모두 접근 |
| 중앙 DB | 임베디드 Raft (rqlite 방식) | 외부 의존 없음, 단일 바이너리 유지 |
| 제어 채널 | gRPC + mTLS | 타입 안전, 양방향 스트리밍, 상호 인증 |
| 실시간 채널 | WebSocket 릴레이 | 기존 WS 인프라 재사용 (터미널/로그/메트릭) |
| 노드 참가 | CLI + Web UI, 시간제한 1회용 토큰 | 자동화 + 편의성 |
| 멀티유저 | Phase 2 (후속) | 클러스터 안정화 먼저 |

---

## 레이어 구조

```
┌─ Web UI (React) ─────────────────────────────┐
│  노드 선택기 | 클러스터 뷰 | 기존 13개 기능   │
├─ API Layer ──────────────────────────────────┤
│  기존 168 REST + ?node=X 라우팅              │
│  새 클러스터 API (/api/v1/cluster/*)          │
├─ Proxy Layer ────────────────────────────────┤
│  로컬 요청 → 직접 실행                        │
│  원격 요청 → gRPC로 해당 노드에 전달           │
├─ Cluster Bus ────────────────────────────────┤
│  gRPC+mTLS: 노드 등록/헬스체크/명령 전달       │
│  WebSocket: 메트릭/로그/터미널 릴레이          │
├─ Raft DB (rqlite 방식) ─────────────────────┤
│  클러스터 설정, 노드 목록, 감사 로그            │
│  + 로컬 SQLite (기존 데이터, 오프라인 운영)     │
├─ Local Services ─────────────────────────────┤
│  Docker SDK | gopsutil | exec | PTY          │
└──────────────────────────────────────────────┘
```

---

## 컴포넌트 상세

### 1. Raft DB (클러스터 상태 저장소)

**라이브러리:** `hashicorp/raft` + `rqlite/rqlite` 참고 구현

**저장 데이터 (Raft 합의 필요):**
- 클러스터 설정 (이름, 토큰 시크릿)
- 노드 목록 (ID, 이름, 주소, 상태, 역할)
- 클러스터 알림 규칙
- 감사 로그

**로컬 SQLite 유지 (기존):**
- admin 계정, 세션
- 로컬 메트릭 히스토리 (24h)
- Compose 프로젝트 목록
- 설정 (key-value)

**스키마 추가:**

```sql
-- Raft DB (클러스터 전체 공유)
CREATE TABLE cluster_nodes (
    id          TEXT PRIMARY KEY,       -- UUID
    name        TEXT NOT NULL,          -- 표시 이름
    address     TEXT NOT NULL,          -- IP:gRPC포트
    api_address TEXT NOT NULL,          -- IP:8443
    role        TEXT DEFAULT 'voter',   -- voter | nonvoter
    status      TEXT DEFAULT 'online',  -- online | offline | joining
    labels      TEXT DEFAULT '{}',      -- JSON 라벨
    joined_at   DATETIME NOT NULL,
    last_seen   DATETIME NOT NULL
);

CREATE TABLE cluster_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE cluster_audit_log (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id   TEXT NOT NULL,
    action    TEXT NOT NULL,
    target    TEXT,
    detail    TEXT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### 2. gRPC 서비스 (제어 채널)

**포트:** 9443 (기본, 설정 가능)

**Proto 정의:**

```protobuf
syntax = "proto3";
package sfpanel.cluster;

// 노드 간 제어 서비스
service ClusterService {
    // 노드 등록/참가
    rpc Join(JoinRequest) returns (JoinResponse);
    rpc Leave(LeaveRequest) returns (LeaveResponse);

    // 헬스체크 (양방향 스트리밍)
    rpc Heartbeat(stream HeartbeatPing) returns (stream HeartbeatPong);

    // API 프록시 (원격 노드에 REST API 실행)
    rpc ProxyRequest(APIRequest) returns (APIResponse);

    // 메트릭 수집
    rpc GetMetrics(MetricsRequest) returns (MetricsResponse);

    // 이벤트 브로드캐스트
    rpc Subscribe(SubscribeRequest) returns (stream ClusterEvent);
}

message JoinRequest {
    string token = 1;
    string node_id = 2;
    string node_name = 3;
    string api_address = 4;    // :8443
    string grpc_address = 5;   // :9443
    bytes  tls_cert = 6;       // 노드의 TLS 인증서
}

message APIRequest {
    string method = 1;         // GET, POST, PUT, DELETE
    string path = 2;           // /api/v1/docker/containers
    bytes  body = 3;
    map<string, string> headers = 4;
    string auth_token = 5;     // JWT 전달
}

message APIResponse {
    int32  status_code = 1;
    bytes  body = 2;
    map<string, string> headers = 3;
}

message HeartbeatPing {
    string node_id = 1;
    double cpu_percent = 2;
    double memory_percent = 3;
    int32  container_count = 4;
    int64  timestamp = 5;
}

message ClusterEvent {
    string event_type = 1;     // node_joined, node_left, node_down, container_started, ...
    string node_id = 2;
    string detail = 3;
    int64  timestamp = 4;
}
```

### 3. mTLS 인증

**인증서 관리:**

```
/etc/sfpanel/cluster/
├── ca.crt              # 클러스터 CA (cluster init 시 자동 생성)
├── ca.key              # CA 개인키 (리더만 보관)
├── node.crt            # 이 노드의 인증서
├── node.key            # 이 노드의 개인키
└── peers/              # 피어 인증서 캐시
    ├── node-b.crt
    └── node-c.crt
```

**플로우:**
1. `cluster init` → 자체 CA 생성 + 리더 인증서 발급
2. `cluster token` → 토큰에 CA fingerprint 포함
3. `cluster join` → 토큰으로 초기 연결 → CA에서 새 인증서 발급 → mTLS 전환

### 4. API 프록시 레이어

**라우팅 규칙:**

```go
// 미들웨어: ?node= 파라미터 확인
func ClusterProxyMiddleware(clusterMgr *cluster.Manager) echo.MiddlewareFunc {
    return func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(c echo.Context) error {
            nodeID := c.QueryParam("node")

            // node 파라미터 없거나 자기 자신 → 로컬 실행
            if nodeID == "" || nodeID == clusterMgr.LocalNodeID() {
                return next(c)
            }

            // 원격 노드로 gRPC 프록시
            return clusterMgr.ProxyToNode(c, nodeID)
        }
    }
}
```

**적용:** 기존 168개 엔드포인트 코드 변경 없음. 미들웨어가 투명하게 프록시.

### 5. WebSocket 릴레이

**원격 노드의 터미널/로그/메트릭 접근:**

```
Client (Browser)
    ↓ WS /ws/terminal?node=node-b
접속한 Node A
    ↓ gRPC ProxyRequest (WS upgrade)
    또는 Node A → Node B로 WS 터널링
Node B
    ↓ 로컬 PTY 세션 생성
    ↑ stdout/stderr 스트리밍
```

**구현 방식:**
- 메트릭: gRPC `Heartbeat` 스트리밍으로 수집 → WS로 클라이언트에 전달
- 터미널/로그: Node A가 Node B에 WS 연결 → 클라이언트와 양방향 릴레이
- Docker 로그: 동일 WS 릴레이 패턴

### 6. 프론트엔드 변경

**노드 선택기 (Layout.tsx):**
```
┌──────────────────────────────────────────┐
│ SFPanel          [Node A ▼] [클러스터 뷰] │
│ 서버 관리                                 │
├──────────────────────────────────────────┤
│ 📊 대시보드                               │
│ 🐳 Docker                                │
│ ...                                       │
```

- 노드 드롭다운 선택 시 `api.setCurrentNode(nodeId)` → 모든 API에 `?node=X` 자동 추가
- "클러스터 뷰" 선택 시 전 노드 집계 대시보드

**새 페이지:**

| 경로 | 페이지 | 설명 |
|------|--------|------|
| `/cluster` | ClusterOverview | 전 노드 집계 메트릭 대시보드 |
| `/cluster/nodes` | ClusterNodes | 노드 목록, 상태, 추가/제거 |
| `/cluster/join` | ClusterJoin | 노드 참가 토큰 생성 UI |

**API 클라이언트 변경 (api.ts):**

```typescript
class ApiClient {
    private currentNode: string | null = null

    setCurrentNode(nodeId: string | null) {
        this.currentNode = nodeId
    }

    private buildUrl(path: string): string {
        const url = new URL(path, this.baseUrl)
        if (this.currentNode) {
            url.searchParams.set('node', this.currentNode)
        }
        return url.toString()
    }

    // 기존 172개 메서드 변경 불필요 — buildUrl이 자동으로 ?node= 추가
}

// 클러스터 전용 API
async getClusterNodes(): Promise<ClusterNode[]> { ... }
async getClusterOverview(): Promise<ClusterOverview> { ... }
async createJoinToken(): Promise<{ token: string, command: string }> { ... }
async removeNode(nodeId: string): Promise<void> { ... }
```

---

## CLI 명령어

```bash
# 클러스터 초기화 (첫 노드)
sfpanel cluster init [--name my-cluster]

# 참가 토큰 생성
sfpanel cluster token [--ttl 24h]

# 클러스터 참가
sfpanel cluster join <leader-ip>:<grpc-port> <token>

# 클러스터 상태 확인
sfpanel cluster status

# 노드 제거
sfpanel cluster remove <node-id>

# 클러스터 해제 (단독 모드로 복귀)
sfpanel cluster leave
```

---

## 포트 사용

| 포트 | 프로토콜 | 용도 |
|------|----------|------|
| 8443 | HTTPS | Web UI + REST API (기존) |
| 9443 | gRPC+mTLS | 클러스터 제어 채널 (새로 추가) |

---

## 설정 파일 확장

```yaml
# /etc/sfpanel/config.yaml
server:
  port: 8443

# 새 섹션
cluster:
  enabled: false              # cluster init 시 true로 변경
  name: ""
  node_id: ""                 # UUID, 자동 생성
  node_name: ""               # 표시 이름, 기본값: hostname
  grpc_port: 9443
  data_dir: /var/lib/sfpanel/cluster  # Raft 데이터
  advertise_address: ""       # 다른 노드가 접근할 IP
  peers: []                   # 부트스트랩 피어 목록
```

---

## 헬스체크 & 장애 감지

| 항목 | 값 |
|------|-----|
| 하트비트 간격 | 2초 |
| 타임아웃 | 10초 (5회 미스) |
| 상태 | online → suspect → offline |
| 리더 선출 | Raft 프로토콜 (자동) |
| 리더 전환 시간 | ~5초 |

---

## 구현 Phase

### Phase 1: Cluster Core (기반)
- `internal/cluster/` 패키지 생성
- Raft DB 통합 (`hashicorp/raft`)
- 노드 등록/탈퇴/헬스체크
- mTLS 인증서 자동 생성/교환
- CLI 명령어 (`cluster init/join/leave/status/token`)
- 설정 파일 확장

### Phase 2: Proxy Layer (API 원격 실행)
- gRPC 서비스 구현 (ProxyRequest)
- `?node=X` 미들웨어
- 기존 168개 API 투명 프록시
- 클러스터 API 엔드포인트 추가

### Phase 3: 실시간 릴레이 (터미널/로그/메트릭)
- 원격 WS 터미널 릴레이
- 원격 Docker 로그 릴레이
- 클러스터 메트릭 집계
- 원격 Docker exec 릴레이

### Phase 4: 프론트엔드
- 노드 선택기 (Layout.tsx)
- api.ts `currentNode` 지원
- ClusterOverview 대시보드
- ClusterNodes 관리 페이지
- 노드 참가 토큰 생성 UI

### Phase 5: 고급 기능
- 자동 리더 선출 최적화
- 클러스터 알림 (노드 다운 알림 등)
- 클러스터 감사 로그 통합
- 노드 라벨/태그 기반 필터링
