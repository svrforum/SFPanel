# Cluster Join Redesign

**Date**: 2026-04-13
**Status**: Approved
**Goal**: Redesign cluster init/join flow for a seamless, zero-restart experience with reliable IP auto-detection, unified CLI/Web logic, and robust error handling.

---

## Problem Statement

The current cluster join flow requires multiple manual restarts, has fragile IP auto-detection, duplicates logic between CLI and Web UI, lacks pre-flight validation, and handles errors poorly. JWT secret synchronization requires an additional restart cycle on follower nodes.

### Key Pain Points

1. **Two restarts required**: join → restart → JWT sync → restart
2. **No pre-flight checks**: failures discovered only mid-join
3. **CLI/Web logic duplication**: inconsistent behavior (e.g., gRPC port defaults differ)
4. **IP auto-detection depends on 8.8.8.8**: fails in air-gapped/overlay networks
5. **Token errors are generic**: "invalid token" with no distinction between expired/used/wrong
6. **CLI has no rollback**: partial failure leaves orphaned certs

---

## Architecture

### Core Change: Live Activation

Replace the `os.Exit()` + systemd restart pattern with in-process live activation.

```
Current flow:
  Join API → config save → os.Exit(1) → systemd restart → Manager.Start()
           → JWT sync from FSM → os.Exit(0) → systemd restart

New flow:
  Join API → PreFlight → gRPC Join → cert save → config save
           → LiveActivate(Manager + gRPC Server) → done
```

### New Components

#### 1. JoinEngine (`internal/cluster/join.go`)

Shared pipeline used by both CLI and Web UI handlers.

```go
type JoinEngine struct {
    ConfigPath string
    Config     *config.Config
    OnActivate LiveActivateFunc // nil for CLI-without-server mode
}

type LiveActivateFunc func(cfg *config.Config) (*Manager, error)

type PreFlightResult struct {
    ClusterName string
    NodeCount   int
    MaxNodes    int
    RecommendedIP string
    IPReason      string
}

type JoinResult struct {
    ClusterName string
    NodeID      string
    Manager     *Manager // non-nil if LiveActivate succeeded
}
```

**PreFlight pipeline**:
1. TCP connection test to leader (3s timeout)
2. Insecure gRPC connection → `PreFlight` RPC call
   - Confirms leader status
   - Token validity peek (does NOT consume the token)
   - Returns cluster name + node count
3. Local gRPC port availability check
4. Advertise address detection using leader IP as hint

**Execute pipeline** (atomic with rollback):
1. gRPC `Join` RPC → receive CA cert, node cert, key, JWT secret, admin credentials, peers
2. Save certs to CertDir
3. Update config (cluster.enabled, node_id, advertise_addr, jwt_secret, etc.)
4. Atomic write config.yaml (backup original first)
5. Update local admin DB with leader's credentials
6. Call LiveActivate callback:
   - `cluster.NewManager(cfg.Cluster)` + `Manager.Start()`
   - `NewGRPCServer(mgr, port)` + `Start(addr)`
   - Update Handler.Manager pointer
   - Set middleware proxy secret
7. Return success

**Rollback chain**:
- Step 6 fails → restore original config + delete CertDir
- Step 4 fails → delete CertDir
- Step 2 fails → no local changes, just return error

#### 2. PreFlight RPC (new gRPC method)

New RPC on `ClusterService`:

```protobuf
message PreFlightRequest {
  string token = 1;
}

message PreFlightResponse {
  bool valid = 1;
  string error = 2;
  string cluster_name = 3;
  int32 node_count = 4;
  int32 max_nodes = 5;
}
```

Calls `TokenManager.Peek(token)` — validates without consuming.

#### 3. JoinResponse Extension

Add fields to the existing `JoinResponse` protobuf:

```protobuf
message JoinResponse {
  // ... existing fields 1-7 ...
  string jwt_secret = 8;          // Leader's JWT signing secret
  string admin_username = 9;      // Leader's admin username
  string admin_password_hash = 10; // Leader's admin bcrypt hash
}
```

This eliminates the follower JWT sync + restart cycle entirely. The joining node receives everything it needs in a single RPC call.

#### 4. LiveActivate Callback

Injected from `main.go` into `Handler` as a closure:

```go
// In main.go, constructing the handler:
clusterHandler := &featureCluster.Handler{
    Config:     cfg,
    ConfigPath: cfgPath,
    LiveActivate: func(cfg *config.Config) (*cluster.Manager, error) {
        mgr := cluster.NewManager(&cfg.Cluster)
        mgr.SetVersion(version)
        if err := mgr.Start(); err != nil {
            return nil, err
        }
        grpcServer, err := cluster.NewGRPCServer(mgr, cfg.Server.Port)
        if err != nil {
            mgr.Shutdown()
            return nil, err
        }
        grpcAddr := fmt.Sprintf("0.0.0.0:%d", cfg.Cluster.GRPCPort)
        if err := grpcServer.Start(grpcAddr); err != nil {
            mgr.Shutdown()
            return nil, err
        }
        middleware.SetClusterProxySecret(grpcServer.ProxySecret())
        return mgr, nil
    },
}
```

The callback has access to `main.go` scope resources (version, middleware, etc.) through closure capture.

---

## IP Auto-Detection

### New Strategy: Leader-Aware Detection

Replace `DetectOutboundIP()` with `DetectAdvertiseAddress(leaderAddr string) string`:

```
1. Parse leader IP from leaderAddr
2. Check if leader IP is a Tailscale address (100.x.x.x range)
   → If yes, find local Tailscale interface IP → use it
3. Check if any local interface shares the same subnet as leader IP
   → If yes, use that local IP (same LAN scenario)
4. Dial TCP to leaderAddr, inspect local address of the connection
   → Use that IP (replaces 8.8.8.8 dependency)
5. All above fail → return error (no silent 127.0.0.1 fallback)
```

**Key improvement**: Uses the actual leader address as the routing hint, so the detected IP is guaranteed to be on a network path that reaches the leader.

### Tailscale Detection

```go
func isTailscaleIP(ip net.IP) bool {
    // Tailscale CGNAT range: 100.64.0.0/10
    cgnat := net.IPNet{
        IP:   net.ParseIP("100.64.0.0"),
        Mask: net.CIDRMask(10, 32),
    }
    return cgnat.Contains(ip)
}

func findLocalTailscaleIP() (string, bool) {
    // Iterate interfaces, find one in 100.64.0.0/10
}
```

### Web UI Integration

`GetNetworkInterfaces` API response enhanced:

```json
{
  "interfaces": [
    {"name": "eth0", "address": "10.0.0.5"},
    {"name": "tailscale0", "address": "100.124.104.128"}
  ],
  "recommended": "100.124.104.128",
  "reason": "Tailscale network matches leader"
}
```

Endpoint accepts optional `leader_addr` query param to compute recommendation.

---

## Error Handling

### Token Error Granularity

Replace generic `ErrInvalidToken` with specific errors:

```go
var (
    ErrTokenNotFound = errors.New("token does not exist")
    ErrTokenExpired  = errors.New("token has expired")
    ErrTokenUsed     = errors.New("token has already been used")  // existing
)
```

`TokenManager.Peek()` and `Validate()` both return these specific errors.

### Pre-Flight Error Messages

| Stage | Error Message |
|-------|---------------|
| TCP connect | `"Cannot reach leader at {addr}: connection refused"` |
| gRPC connect | `"Leader is not responding to cluster requests at {addr}"` |
| Not leader | `"Node at {addr} is not the cluster leader"` |
| Token not found | `"Token does not exist — check for typos"` |
| Token expired | `"Token expired at {time} — create a new one on the leader"` |
| Port conflict | `"gRPC port {port} is already in use locally"` |
| Max nodes | `"Cluster already has {n}/{max} nodes"` |

---

## Init Flow Changes

`InitCluster` also uses LiveActivate instead of `os.Exit(1)`:

```
InitCluster new flow:
  1. Initialize TLS CA + issue node cert
  2. Bootstrap Raft (wait for leader election)
  3. Register self in Raft FSM
  4. Update + atomically save config
  5. LiveActivate callback → start Manager + gRPC Server
  6. Store JWT secret in FSM
  7. Return success (restart: false)
```

Same rollback pattern as JoinEngine.

---

## Existing Node Address Updates

When a new node joins from a different network (e.g., Tailscale joining a LAN cluster), existing nodes may need to update their advertise_address.

### Approach: Event-Driven Address Recheck

1. **Enhance `verifySelfAddress`**: Currently runs once with 10s sleep. Change to also trigger on new node join events.
2. **Leader broadcasts `AddressCheck` via heartbeat stream**: When a new node joins, leader sends a signal to all existing nodes to re-run `verifySelfAddress`.
3. Each node rechecks and auto-corrects its FSM entry if config changed.

**Note**: This updates FSM entries to match each node's config. If a node's config still says `advertise_address: 192.168.1.172` but needs to be `100.81.3.85`, the operator must update the node's config. The `UpdateNodeAddress` API can update both FSM and config.yaml atomically.

---

## CLI Changes

### Server-Aware CLI

```
sfpanel cluster join <addr> <token>
  → Is server running? (check TCP to 127.0.0.1:port)
    → YES: callLocalAPI POST /api/v1/cluster/join → live activation
    → NO:  JoinEngine.Execute(LiveActivate=nil) → config save only → print "restart to activate"

sfpanel cluster init [--name NAME]
  → Same pattern: delegate to running server if available
```

This gives CLI users the same zero-restart experience when the server is running.

### Backward Compatibility

- `sfpanel cluster join <addr> <token>` syntax unchanged
- Config file format unchanged (new fields are additive)
- Existing clusters continue working without migration

---

## Protobuf Changes Summary

Files to regenerate: `internal/cluster/proto/cluster.pb.go`, `cluster_grpc.pb.go`

New RPC:
- `PreFlight(PreFlightRequest) returns (PreFlightResponse)`

Extended message:
- `JoinResponse`: add `jwt_secret` (field 8), `admin_username` (field 9), `admin_password_hash` (field 10)

New messages:
- `PreFlightRequest`: `token` (string)
- `PreFlightResponse`: `valid` (bool), `error` (string), `cluster_name` (string), `node_count` (int32), `max_nodes` (int32)

---

## Files to Create/Modify

### New Files
- `internal/cluster/join.go` — JoinEngine (PreFlight + Execute + rollback)
- `internal/cluster/join_test.go` — JoinEngine unit tests
- `internal/cluster/detect.go` — Leader-aware IP auto-detection
- `internal/cluster/detect_test.go` — IP detection tests

### Modified Files
- `internal/cluster/proto/cluster.pb.go` — Regenerated with new messages/fields
- `internal/cluster/proto/cluster_grpc.pb.go` — Regenerated with PreFlight RPC
- `internal/cluster/manager.go` — Add `Peek` to TokenManager, remove DetectOutboundIP
- `internal/cluster/token.go` — Add `Peek()` method, split `ErrInvalidToken` into specific errors
- `internal/cluster/grpc_server.go` — Add PreFlight handler, extend Join to include JWT/admin
- `internal/feature/cluster/handler.go` — Use JoinEngine, add LiveActivate field, remove os.Exit()
- `cmd/sfpanel/cluster_commands.go` — Use JoinEngine, server-aware delegation
- `cmd/sfpanel/main.go` — Inject LiveActivate callback, remove follower JWT sync + os.Exit(0)
- `internal/api/router.go` — Pass LiveActivate to handler

### Deleted Code
- `main.go:200-218` — Follower JWT sync + os.Exit(0) logic
- `handler.go` os.Exit(1) in InitCluster and JoinCluster
- `manager.go` DetectOutboundIP function (replaced by detect.go)

---

## Testing Strategy

1. **JoinEngine unit tests**: Mock gRPC client, verify PreFlight/Execute pipeline stages, verify rollback on each failure point
2. **TokenManager.Peek tests**: Peek doesn't consume token, returns specific errors
3. **DetectAdvertiseAddress tests**: Tailscale IP selection, same-subnet selection, leader-dial fallback
4. **LiveActivate integration tests**: Mock callback verifies Manager + gRPC server started
5. **Rollback tests**: Config save failure → cert cleanup + original config restored
6. **CLI delegation tests**: Server running → callLocalAPI; server not running → local execute
