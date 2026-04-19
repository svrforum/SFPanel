# Cluster Join Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign cluster init/join to work without restarts, with shared CLI/Web logic, pre-flight validation, and leader-aware IP detection.

**Architecture:** A new `JoinEngine` in `internal/cluster/join.go` handles the full join pipeline (pre-flight → join → cert save → config save → live activate). Both CLI and Web UI call this engine. A `LiveActivateFunc` callback injected from `main.go` starts Manager + gRPC server in-process. Protobuf is extended with `PreFlight` RPC and JWT/admin fields on `JoinResponse`.

**Tech Stack:** Go 1.23, gRPC/protobuf, hashicorp/raft, mTLS

**Spec:** `docs/superpowers/specs/2026-04-13-cluster-join-redesign.md`

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `internal/cluster/detect.go` | Leader-aware IP auto-detection (Tailscale, subnet matching, dial-based) |
| `internal/cluster/detect_test.go` | Tests for IP detection logic |
| `internal/cluster/join.go` | JoinEngine: PreFlight + Execute pipelines with rollback |
| `internal/cluster/join_test.go` | JoinEngine unit tests |

### Modified Files
| File | Changes |
|------|---------|
| `proto/cluster.proto` | Add `PreFlight` RPC, extend `JoinResponse` with jwt/admin fields |
| `internal/cluster/proto/cluster.pb.go` | Regenerated |
| `internal/cluster/proto/cluster_grpc.pb.go` | Regenerated |
| `internal/cluster/errors.go` | Split `ErrInvalidToken` → `ErrTokenNotFound` + `ErrTokenExpired` |
| `internal/cluster/token.go` | Add `Peek()` method, use new error types |
| `internal/cluster/grpc_server.go` | Add `PreFlight` handler, extend `Join` to include JWT/admin in response |
| `internal/cluster/grpc_client.go` | Add `PreFlight()` client method |
| `internal/cluster/manager.go` | Add `HandlePreFlight()`, remove `DetectOutboundIP()`, add `GetJWTAndAdmin()` |
| `internal/feature/cluster/handler.go` | Add `LiveActivate` field, rewrite `InitCluster`/`JoinCluster` to use JoinEngine, remove `os.Exit()` |
| `cmd/sfpanel/cluster_commands.go` | Rewrite `clusterJoin`/`clusterInit` to use JoinEngine, add server-aware delegation |
| `cmd/sfpanel/main.go` | Inject `LiveActivate` callback into handler, remove follower JWT sync + os.Exit(0) |
| `internal/api/router.go` | Pass `LiveActivate` callback and `database` to cluster handler |

---

### Task 1: Extend Protobuf Schema

**Files:**
- Modify: `proto/cluster.proto`
- Regenerate: `internal/cluster/proto/cluster.pb.go`, `internal/cluster/proto/cluster_grpc.pb.go`

- [ ] **Step 1: Update proto file with new messages and fields**

Edit `proto/cluster.proto` to add `PreFlight` RPC and extend `JoinResponse`:

```protobuf
syntax = "proto3";
package sfpanel.cluster;
option go_package = "github.com/svrforum/SFPanel/internal/cluster/proto";

service ClusterService {
    rpc PreFlight(PreFlightRequest) returns (PreFlightResponse);
    rpc Join(JoinRequest) returns (JoinResponse);
    rpc Leave(LeaveRequest) returns (LeaveResponse);
    rpc Heartbeat(stream HeartbeatPing) returns (stream HeartbeatPong);
    rpc ProxyRequest(APIRequest) returns (APIResponse);
    rpc GetMetrics(MetricsRequest) returns (MetricsResponse);
    rpc Subscribe(SubscribeRequest) returns (stream ClusterEvent);
}

message PreFlightRequest {
    string token = 1;
}

message PreFlightResponse {
    bool   valid = 1;
    string error = 2;
    string cluster_name = 3;
    int32  node_count = 4;
    int32  max_nodes = 5;
}

message JoinRequest {
    string token = 1;
    string node_id = 2;
    string node_name = 3;
    string api_address = 4;
    string grpc_address = 5;
    bytes  tls_cert = 6;
}

message JoinResponse {
    bool   success = 1;
    string error = 2;
    string cluster_name = 3;
    bytes  ca_cert = 4;
    bytes  node_cert = 5;
    bytes  node_key = 6;
    repeated NodeInfo peers = 7;
    string jwt_secret = 8;
    string admin_username = 9;
    string admin_password_hash = 10;
}

message LeaveRequest {
    string node_id = 1;
}

message LeaveResponse {
    bool   success = 1;
    string error = 2;
}

message HeartbeatPing {
    string node_id = 1;
    double cpu_percent = 2;
    double memory_percent = 3;
    int32  container_count = 4;
    int64  timestamp = 5;
    double disk_percent = 6;
    string version = 7;
}

message HeartbeatPong {
    string leader_id = 1;
    int64  timestamp = 2;
}

message APIRequest {
    string method = 1;
    string path = 2;
    bytes  body = 3;
    map<string, string> headers = 4;
    string auth_token = 5;
}

message APIResponse {
    int32  status_code = 1;
    bytes  body = 2;
    map<string, string> headers = 3;
}

message MetricsRequest {
    string node_id = 1;
}

message MetricsResponse {
    string node_id = 1;
    double cpu_percent = 2;
    double memory_percent = 3;
    double disk_percent = 4;
    int32  container_count = 5;
    int64  uptime_seconds = 6;
}

message SubscribeRequest {
    string node_id = 1;
    repeated string event_types = 2;
}

message ClusterEvent {
    string event_type = 1;
    string node_id = 2;
    string detail = 3;
    int64  timestamp = 4;
}

message NodeInfo {
    string id = 1;
    string name = 2;
    string api_address = 3;
    string grpc_address = 4;
    string role = 5;
    string status = 6;
}
```

- [ ] **Step 2: Regenerate Go code**

Run:
```bash
protoc --go_out=. --go-grpc_out=. proto/cluster.proto
```

Expected: `internal/cluster/proto/cluster.pb.go` and `cluster_grpc.pb.go` regenerated with `PreFlightRequest`, `PreFlightResponse` types and `PreFlight` RPC method on the service interface.

- [ ] **Step 3: Verify compilation**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: Compilation fails with "missing PreFlight method on UnimplementedClusterServiceServer" — this is expected and will be fixed in Task 3.

- [ ] **Step 4: Commit**

```bash
git add proto/cluster.proto internal/cluster/proto/
git commit -m "proto: add PreFlight RPC, extend JoinResponse with jwt/admin fields"
```

---

### Task 2: Split Token Errors and Add Peek

**Files:**
- Modify: `internal/cluster/errors.go`
- Modify: `internal/cluster/token.go`
- Create: `internal/cluster/token_test.go`

- [ ] **Step 1: Write token tests**

Create `internal/cluster/token_test.go`:

```go
package cluster

import (
	"testing"
	"time"
)

func TestTokenManager_CreateAndValidate(t *testing.T) {
	tm := NewTokenManager()
	token, err := tm.Create(time.Hour, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := tm.Validate(token.Token); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Second validate should fail with ErrTokenUsed
	if err := tm.Validate(token.Token); err != ErrTokenUsed {
		t.Fatalf("expected ErrTokenUsed, got %v", err)
	}
}

func TestTokenManager_Peek_DoesNotConsume(t *testing.T) {
	tm := NewTokenManager()
	token, err := tm.Create(time.Hour, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Peek should succeed
	if err := tm.Peek(token.Token); err != nil {
		t.Fatalf("Peek: %v", err)
	}

	// Peek again should still succeed (not consumed)
	if err := tm.Peek(token.Token); err != nil {
		t.Fatalf("Peek again: %v", err)
	}

	// Validate should still work after Peek
	if err := tm.Validate(token.Token); err != nil {
		t.Fatalf("Validate after Peek: %v", err)
	}
}

func TestTokenManager_Peek_NotFound(t *testing.T) {
	tm := NewTokenManager()
	if err := tm.Peek("nonexistent"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestTokenManager_Peek_Expired(t *testing.T) {
	tm := NewTokenManager()
	token, _ := tm.Create(1*time.Millisecond, "test")
	time.Sleep(5 * time.Millisecond)

	if err := tm.Peek(token.Token); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestTokenManager_Validate_Expired(t *testing.T) {
	tm := NewTokenManager()
	token, _ := tm.Create(1*time.Millisecond, "test")
	time.Sleep(5 * time.Millisecond)

	if err := tm.Validate(token.Token); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestTokenManager_Validate_NotFound(t *testing.T) {
	tm := NewTokenManager()
	if err := tm.Validate("nonexistent"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run TestTokenManager -v`
Expected: FAIL — `ErrTokenNotFound`, `ErrTokenExpired`, and `Peek` are undefined.

- [ ] **Step 3: Update errors.go**

Replace `internal/cluster/errors.go`:

```go
package cluster

import "errors"

var (
	ErrNotInitialized     = errors.New("cluster not initialized")
	ErrAlreadyInitialized = errors.New("cluster already initialized")
	ErrNotLeader          = errors.New("not the cluster leader")
	ErrNodeNotFound       = errors.New("node not found")
	ErrNodeAlreadyExists  = errors.New("node already exists in cluster")
	ErrTokenNotFound      = errors.New("token does not exist")
	ErrTokenExpired       = errors.New("token has expired")
	ErrTokenUsed          = errors.New("join token already used")
	ErrMaxNodesReached    = errors.New("maximum node count reached")
	ErrSelfRemove         = errors.New("cannot remove self from cluster")
	ErrCertGenFailed      = errors.New("certificate generation failed")
	ErrRaftTimeout        = errors.New("raft operation timed out")
	ErrGRPCConnFailed     = errors.New("gRPC connection failed")
)
```

Note: `ErrInvalidToken` is removed and replaced by `ErrTokenNotFound` and `ErrTokenExpired`.

- [ ] **Step 4: Update token.go with Peek and new error types**

Replace `internal/cluster/token.go`:

```go
package cluster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// TokenManager handles join token creation and validation.
type TokenManager struct {
	mu     sync.Mutex
	tokens map[string]*JoinToken
	secret []byte
}

func NewTokenManager() *TokenManager {
	secret := make([]byte, 32)
	rand.Read(secret)
	return &TokenManager{
		tokens: make(map[string]*JoinToken),
		secret: secret,
	}
}

// Create generates a new time-limited, single-use join token.
func (tm *TokenManager) Create(ttl time.Duration, createdBy string) (*JoinToken, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	mac := hmac.New(sha256.New, tm.secret)
	mac.Write(raw)
	token := hex.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))

	jt := &JoinToken{
		Token:     token,
		ExpiresAt: time.Now().Add(ttl),
		CreatedBy: createdBy,
	}
	tm.tokens[token] = jt

	tm.cleanupLocked()

	return jt, nil
}

// Peek checks token validity without consuming it.
// Returns ErrTokenNotFound, ErrTokenExpired, or ErrTokenUsed.
func (tm *TokenManager) Peek(token string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	jt, ok := tm.tokens[token]
	if !ok {
		return ErrTokenNotFound
	}
	if jt.Used {
		return ErrTokenUsed
	}
	if time.Now().After(jt.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

// Validate checks the token and marks it as used.
// Returns ErrTokenNotFound, ErrTokenExpired, or ErrTokenUsed.
func (tm *TokenManager) Validate(token string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	jt, ok := tm.tokens[token]
	if !ok {
		return ErrTokenNotFound
	}
	if jt.Used {
		return ErrTokenUsed
	}
	if time.Now().After(jt.ExpiresAt) {
		delete(tm.tokens, token)
		return ErrTokenExpired
	}

	jt.Used = true
	return nil
}

// RestoreToken marks a token as unused so it can be retried.
func (tm *TokenManager) RestoreToken(token string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if jt, ok := tm.tokens[token]; ok {
		jt.Used = false
	}
}

func (tm *TokenManager) cleanupLocked() {
	now := time.Now()
	for k, t := range tm.tokens {
		if now.After(t.ExpiresAt) || t.Used {
			delete(tm.tokens, k)
		}
	}
}
```

- [ ] **Step 5: Fix ErrInvalidToken references throughout codebase**

Search and replace all references to `ErrInvalidToken`:

In `internal/feature/cluster/handler.go:38`, change:
```go
case errors.Is(err, cluster.ErrInvalidToken), errors.Is(err, cluster.ErrTokenUsed):
```
to:
```go
case errors.Is(err, cluster.ErrTokenNotFound), errors.Is(err, cluster.ErrTokenExpired), errors.Is(err, cluster.ErrTokenUsed):
```

In `internal/cluster/manager.go:253` (`HandleJoin`), `Validate` already returns the new error types — no change needed there.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run TestTokenManager -v`
Expected: All 6 tests PASS.

- [ ] **Step 7: Run full test suite**

Run: `cd /opt/stacks/SFPanel && go test ./...`
Expected: All tests pass (no regressions from ErrInvalidToken removal).

- [ ] **Step 8: Commit**

```bash
git add internal/cluster/errors.go internal/cluster/token.go internal/cluster/token_test.go internal/feature/cluster/handler.go
git commit -m "refactor: split token errors, add Peek method for pre-flight validation"
```

---

### Task 3: Add PreFlight Handler and Extend Join Response on gRPC Server

**Files:**
- Modify: `internal/cluster/grpc_server.go:95-126`
- Modify: `internal/cluster/grpc_client.go`
- Modify: `internal/cluster/manager.go`

- [ ] **Step 1: Add HandlePreFlight and GetJWTAndAdmin to Manager**

In `internal/cluster/manager.go`, add these methods after `HandleJoin` (after line ~316):

```go
// HandlePreFlight validates a token without consuming it (for pre-flight checks).
func (m *Manager) HandlePreFlight(token string) (clusterName string, nodeCount, maxNodes int, err error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return "", 0, 0, ErrNotLeader
	}

	if err := m.tokens.Peek(token); err != nil {
		return "", 0, 0, err
	}

	state := m.raft.GetFSM().GetState()
	return state.Config["cluster_name"], len(state.Nodes), MaxNodes, nil
}

// GetJWTAndAdmin returns the leader's JWT secret and primary admin account
// for inclusion in join responses.
func (m *Manager) GetJWTAndAdmin() (jwtSecret, adminUser, adminPassHash string) {
	if m.raft == nil {
		return "", "", ""
	}
	state := m.raft.GetFSM().GetState()
	jwtSecret = state.Config["jwt_secret"]

	// Return the first (primary) admin account
	for _, acct := range state.Accounts {
		return jwtSecret, acct.Username, acct.Password
	}
	return jwtSecret, "", ""
}
```

- [ ] **Step 2: Add PreFlight handler to gRPC server**

In `internal/cluster/grpc_server.go`, add after the `Join` method (after line ~126):

```go
// PreFlight validates a join token without consuming it.
func (s *GRPCServer) PreFlight(ctx context.Context, req *pb.PreFlightRequest) (*pb.PreFlightResponse, error) {
	clusterName, nodeCount, maxNodes, err := s.manager.HandlePreFlight(req.Token)
	if err != nil {
		return &pb.PreFlightResponse{Valid: false, Error: err.Error()}, nil
	}
	return &pb.PreFlightResponse{
		Valid:       true,
		ClusterName: clusterName,
		NodeCount:   int32(nodeCount),
		MaxNodes:    int32(maxNodes),
	}, nil
}
```

- [ ] **Step 3: Extend Join handler to include JWT and admin in response**

In `internal/cluster/grpc_server.go`, modify the `Join` method. Replace lines 116-126:

```go
	state := s.manager.GetRaft().GetFSM().GetState()
	jwtSecret, adminUser, adminPassHash := s.manager.GetJWTAndAdmin()

	return &pb.JoinResponse{
		Success:           true,
		ClusterName:       state.Config["cluster_name"],
		CaCert:            caCert,
		NodeCert:          nodeCert,
		NodeKey:           nodeKey,
		Peers:             pbPeers,
		JwtSecret:         jwtSecret,
		AdminUsername:     adminUser,
		AdminPasswordHash: adminPassHash,
	}, nil
```

- [ ] **Step 4: Add PreFlight client method**

In `internal/cluster/grpc_client.go`, add after the `Join` method (after line ~67):

```go
// PreFlight checks token validity without consuming it.
func (c *GRPCClient) PreFlight(ctx context.Context, token string) (*pb.PreFlightResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.client.PreFlight(ctx, &pb.PreFlightRequest{Token: token})
}
```

- [ ] **Step 5: Verify compilation**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: Compiles successfully.

- [ ] **Step 6: Run tests**

Run: `cd /opt/stacks/SFPanel && go test ./...`
Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/cluster/grpc_server.go internal/cluster/grpc_client.go internal/cluster/manager.go
git commit -m "feat: add PreFlight RPC and JWT/admin fields in Join response"
```

---

### Task 4: Leader-Aware IP Auto-Detection

**Files:**
- Create: `internal/cluster/detect.go`
- Create: `internal/cluster/detect_test.go`
- Modify: `internal/cluster/manager.go` (remove `DetectOutboundIP`)

- [ ] **Step 1: Write detection tests**

Create `internal/cluster/detect_test.go`:

```go
package cluster

import (
	"net"
	"testing"
)

func TestIsTailscaleIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"100.100.100.100", true},
		{"100.63.255.255", false},  // just below CGNAT range
		{"100.128.0.0", false},     // just above CGNAT range
		{"192.168.1.1", false},
		{"10.0.0.1", false},
	}
	for _, tt := range tests {
		if got := isTailscaleIP(net.ParseIP(tt.ip)); got != tt.want {
			t.Errorf("isTailscaleIP(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestDetectAdvertiseAddress_LeaderDial(t *testing.T) {
	// When leader address is reachable, detect should use the local IP
	// from the TCP connection. We test with localhost as a guaranteed-reachable target.
	// Start a temporary TCP listener to simulate a leader.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr, err := DetectAdvertiseAddress(ln.Addr().String())
	if err != nil {
		t.Fatalf("DetectAdvertiseAddress: %v", err)
	}
	if addr != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1 for localhost leader, got %s", addr)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run "TestIsTailscale|TestDetectAdvertise" -v`
Expected: FAIL — functions not defined.

- [ ] **Step 3: Implement detect.go**

Create `internal/cluster/detect.go`:

```go
package cluster

import (
	"fmt"
	"net"
	"time"
)

// DetectAdvertiseAddress determines the best local IP to advertise to the cluster,
// using the leader's address as a routing hint.
//
// Strategy:
//  1. If leader is on a Tailscale IP (100.64.0.0/10), find local Tailscale IP.
//  2. Find a local IP on the same subnet as the leader.
//  3. Dial the leader and use the local side of the TCP connection.
//  4. Return error (no silent 127.0.0.1 fallback).
func DetectAdvertiseAddress(leaderAddr string) (string, error) {
	leaderHost, _, err := net.SplitHostPort(leaderAddr)
	if err != nil {
		// leaderAddr might be just an IP without port
		leaderHost = leaderAddr
	}

	leaderIP := net.ParseIP(leaderHost)

	// Strategy 1: Tailscale
	if leaderIP != nil && isTailscaleIP(leaderIP) {
		if tsIP, ok := findLocalTailscaleIP(); ok {
			return tsIP, nil
		}
	}

	// Strategy 2: Same subnet
	if leaderIP != nil {
		if localIP, ok := findSameSubnetIP(leaderIP); ok {
			return localIP, nil
		}
	}

	// Strategy 3: Dial the leader to discover local IP
	conn, err := net.DialTimeout("tcp", leaderAddr, 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("cannot reach leader at %s to detect local IP: %w", leaderAddr, err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.TCPAddr)
	if localAddr.IP.IsLoopback() {
		// Loopback means leader is on the same machine — still valid for testing
		return localAddr.IP.String(), nil
	}
	return localAddr.IP.String(), nil
}

// isTailscaleIP returns true if the IP is in the Tailscale CGNAT range (100.64.0.0/10).
func isTailscaleIP(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	// 100.64.0.0/10 = first byte 100, second byte 64-127
	return ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}

// findLocalTailscaleIP returns the first local IPv4 address in the Tailscale CGNAT range.
func findLocalTailscaleIP() (string, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", false
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip != nil && isTailscaleIP(ip) {
			return ip.String(), true
		}
	}
	return "", false
}

// findSameSubnetIP finds a local IP that shares a /24 subnet with the target IP.
// This is a heuristic — /24 covers the vast majority of LAN configurations.
func findSameSubnetIP(targetIP net.IP) (string, bool) {
	targetIP = targetIP.To4()
	if targetIP == nil {
		return "", false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			// Check if both IPs are in this interface's subnet
			if ipnet.Contains(targetIP) {
				return ip.String(), true
			}
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run detection tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run "TestIsTailscale|TestDetectAdvertise" -v`
Expected: All tests PASS.

- [ ] **Step 5: Remove DetectOutboundIP from manager.go**

In `internal/cluster/manager.go`, find and delete the `DetectOutboundIP` function (around line 862-877). Then update all callers within `manager.go` to use `DetectAdvertiseAddress`:

In `Init()` (line 76-78), replace:
```go
	advertise := m.config.AdvertiseAddress
	if advertise == "" {
		advertise = DetectOutboundIP()
```
with:
```go
	advertise := m.config.AdvertiseAddress
	if advertise == "" {
		var detectErr error
		apiAddr := fmt.Sprintf("%s:%d", "8.8.8.8", 80) // fallback for init (no leader)
		advertise, detectErr = DetectAdvertiseAddress(apiAddr)
		if detectErr != nil {
			// Init has no leader to dial; fall back to first non-loopback IP
			advertise = detectFallbackIP()
		}
```

Add a simple fallback for init (no leader exists yet):

```go
// detectFallbackIP returns the first non-loopback IPv4 address.
// Used only during cluster init when no leader address is available.
func detectFallbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}
```

In `Start()` (line 157-161) and `verifySelfAddress()` (line 192-194), make the same replacement.

- [ ] **Step 6: Verify compilation and tests**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./...`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
git add internal/cluster/detect.go internal/cluster/detect_test.go internal/cluster/manager.go
git commit -m "feat: leader-aware IP auto-detection with Tailscale support"
```

---

### Task 5: JoinEngine — PreFlight and Execute Pipelines

**Files:**
- Create: `internal/cluster/join.go`
- Create: `internal/cluster/join_test.go`

- [ ] **Step 1: Write JoinEngine tests**

Create `internal/cluster/join_test.go`:

```go
package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

func TestJoinEngine_Rollback_ConfigSaveFailure(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	configPath := filepath.Join(tmpDir, "config.yaml")

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "0.0.0.0", Port: 8443},
		Auth:   config.AuthConfig{JWTSecret: "old-secret", TokenExpiry: "24h"},
		Cluster: config.ClusterConfig{
			GRPCPort: 9444,
			CertDir:  certDir,
			DataDir:  filepath.Join(tmpDir, "data"),
		},
		Database: config.DatabaseConfig{Path: filepath.Join(tmpDir, "test.db")},
	}

	// Write an initial config
	os.WriteFile(configPath, []byte("server:\n  port: 8443\n"), 0600)

	engine := &JoinEngine{
		ConfigPath: configPath,
		Config:     cfg,
	}

	// Test rollback: save certs then fail config save with read-only path
	os.MkdirAll(certDir, 0755)
	os.WriteFile(filepath.Join(certDir, "ca.crt"), []byte("fake-ca"), 0600)

	// Verify rollbackJoin cleans up certs and restores config
	originalConfig, _ := os.ReadFile(configPath)
	engine.rollbackJoin(certDir, originalConfig)

	// Cert dir should be removed
	if _, err := os.Stat(certDir); !os.IsNotExist(err) {
		t.Error("rollback should have removed cert dir")
	}

	// Config should be restored
	restored, _ := os.ReadFile(configPath)
	if string(restored) != string(originalConfig) {
		t.Error("rollback should have restored original config")
	}
}
```

- [ ] **Step 2: Implement JoinEngine**

Create `internal/cluster/join.go`:

```go
package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// LiveActivateFunc starts Manager + gRPC server in-process after a successful join/init.
// Returns the new Manager. The callback is responsible for starting the gRPC server,
// setting middleware proxy secret, etc.
type LiveActivateFunc func(cfg *config.Config, cfgPath string) (*Manager, error)

// JoinEngine handles the full cluster join pipeline for both CLI and Web UI.
type JoinEngine struct {
	ConfigPath   string
	Config       *config.Config
	DB           *sql.DB           // for updating admin credentials
	OnActivate   LiveActivateFunc  // nil = config-only mode (CLI without running server)
}

// PreFlightResult contains the result of a pre-flight check.
type PreFlightResult struct {
	ClusterName   string
	NodeCount     int
	MaxNodes      int
	RecommendedIP string
	IPReason      string
}

// JoinResult contains the result of a successful join.
type JoinResult struct {
	ClusterName string
	NodeID      string
	NodeName    string
	Manager     *Manager // non-nil only if OnActivate was provided and succeeded
}

// PreFlight validates that a join can succeed before committing any changes.
func (e *JoinEngine) PreFlight(leaderAddr, token string) (*PreFlightResult, error) {
	// Step 1: TCP connection test
	conn, err := net.DialTimeout("tcp", leaderAddr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot reach leader at %s: %w", leaderAddr, err)
	}
	conn.Close()

	// Step 2: gRPC PreFlight RPC
	client, err := DialNodeInsecure(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("leader is not responding to cluster requests at %s: %w", leaderAddr, err)
	}
	defer client.Close()

	resp, err := client.PreFlight(context.Background(), token)
	if err != nil {
		return nil, fmt.Errorf("pre-flight request failed: %w", err)
	}
	if !resp.Valid {
		return nil, fmt.Errorf("%s", preFlightErrorMessage(resp.Error))
	}

	// Step 3: Local gRPC port check
	grpcPort := e.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = e.Config.Server.Port + 1
	}
	if ln, lnErr := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort)); lnErr != nil {
		return nil, fmt.Errorf("gRPC port %d is already in use locally", grpcPort)
	} else {
		ln.Close()
	}

	// Step 4: Detect advertise address
	recommendedIP, ipErr := DetectAdvertiseAddress(leaderAddr)
	ipReason := "detected from connection to leader"
	if ipErr != nil {
		recommendedIP = ""
		ipReason = fmt.Sprintf("auto-detection failed: %v", ipErr)
	} else {
		leaderHost, _, _ := net.SplitHostPort(leaderAddr)
		if isTailscaleIP(net.ParseIP(leaderHost)) && isTailscaleIP(net.ParseIP(recommendedIP)) {
			ipReason = "Tailscale network matches leader"
		} else if leaderHost != "" {
			ipReason = "same network as leader"
		}
	}

	return &PreFlightResult{
		ClusterName:   resp.ClusterName,
		NodeCount:     int(resp.NodeCount),
		MaxNodes:      int(resp.MaxNodes),
		RecommendedIP: recommendedIP,
		IPReason:      ipReason,
	}, nil
}

// Execute runs the full join pipeline: gRPC join → certs → config → live activate.
func (e *JoinEngine) Execute(leaderAddr, token, advertiseAddr string) (*JoinResult, error) {
	if advertiseAddr == "" {
		detected, err := DetectAdvertiseAddress(leaderAddr)
		if err != nil {
			return nil, fmt.Errorf("cannot detect advertise address: %w", err)
		}
		advertiseAddr = detected
	}

	nodeID := uuid.New().String()
	hostname, _ := os.Hostname()

	grpcPort := e.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = e.Config.Server.Port + 1
	}
	apiAddr := fmt.Sprintf("%s:%d", advertiseAddr, e.Config.Server.Port)
	grpcAddr := fmt.Sprintf("%s:%d", advertiseAddr, grpcPort)

	slog.Info("joining cluster", "component", "cluster", "leader", leaderAddr, "advertise", advertiseAddr)

	// Step 1: gRPC Join RPC
	client, err := DialNodeInsecure(leaderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to leader: %w", err)
	}
	defer client.Close()

	resp, err := client.Join(context.Background(), &pb.JoinRequest{
		Token:       token,
		NodeId:      nodeID,
		NodeName:    hostname,
		ApiAddress:  apiAddr,
		GrpcAddress: grpcAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("join request failed: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("join rejected: %s", resp.Error)
	}

	// Backup original config for rollback
	var originalConfig []byte
	if e.ConfigPath != "" {
		originalConfig, _ = os.ReadFile(e.ConfigPath)
	}

	certDir := e.Config.Cluster.CertDir
	if certDir == "" {
		certDir = DefaultCertDir
	}

	// Step 2: Save certs
	tlsMgr := NewTLSManager(certDir)
	if err := tlsMgr.SaveCACert(resp.CaCert); err != nil {
		return nil, fmt.Errorf("failed to save CA cert: %w", err)
	}
	if err := tlsMgr.SaveNodeCert(resp.NodeCert, resp.NodeKey); err != nil {
		os.RemoveAll(certDir)
		return nil, fmt.Errorf("failed to save node cert: %w", err)
	}

	// Step 3: Update config
	e.Config.Cluster.Enabled = true
	e.Config.Cluster.Name = resp.ClusterName
	e.Config.Cluster.NodeID = nodeID
	e.Config.Cluster.NodeName = hostname
	e.Config.Cluster.AdvertiseAddress = advertiseAddr
	e.Config.Cluster.GRPCPort = grpcPort

	if resp.JwtSecret != "" {
		e.Config.Auth.JWTSecret = resp.JwtSecret
	}

	// Step 4: Save config atomically
	if e.ConfigPath != "" {
		data, err := yaml.Marshal(e.Config)
		if err != nil {
			e.rollbackJoin(certDir, originalConfig)
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		if err := config.AtomicWriteFile(e.ConfigPath, data, 0600); err != nil {
			e.rollbackJoin(certDir, originalConfig)
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	// Step 5: Update admin credentials from leader
	if e.DB != nil && resp.AdminUsername != "" && resp.AdminPasswordHash != "" {
		_, err := e.DB.Exec(
			"UPDATE admin SET password = ? WHERE username = ?",
			resp.AdminPasswordHash, resp.AdminUsername,
		)
		if err != nil {
			slog.Warn("failed to sync admin credentials from leader", "error", err)
		}
	}

	// Step 6: Live activate (if callback provided)
	var mgr *Manager
	if e.OnActivate != nil {
		e.Config.Cluster.APIPort = e.Config.Server.Port
		mgr, err = e.OnActivate(e.Config, e.ConfigPath)
		if err != nil {
			e.rollbackJoin(certDir, originalConfig)
			return nil, fmt.Errorf("live activation failed: %w", err)
		}
	}

	slog.Info("cluster join successful", "component", "cluster",
		"cluster_name", resp.ClusterName, "node_id", nodeID, "live", mgr != nil)

	return &JoinResult{
		ClusterName: resp.ClusterName,
		NodeID:      nodeID,
		NodeName:    hostname,
		Manager:     mgr,
	}, nil
}

// rollbackJoin cleans up certs and restores original config on failure.
func (e *JoinEngine) rollbackJoin(certDir string, originalConfig []byte) {
	os.RemoveAll(certDir)
	if e.ConfigPath != "" && originalConfig != nil {
		os.WriteFile(e.ConfigPath, originalConfig, 0600)
	}
	e.Config.Cluster.Enabled = false
	slog.Warn("join rolled back", "component", "cluster")
}

// preFlightErrorMessage maps internal error strings to user-friendly messages.
func preFlightErrorMessage(errStr string) string {
	switch errStr {
	case ErrTokenNotFound.Error():
		return "token does not exist — check for typos"
	case ErrTokenExpired.Error():
		return "token has expired — create a new one on the leader"
	case ErrTokenUsed.Error():
		return "token has already been used"
	case ErrNotLeader.Error():
		return "node is not the cluster leader"
	case ErrMaxNodesReached.Error():
		return "cluster has reached maximum node count"
	default:
		return errStr
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run TestJoinEngine -v`
Expected: PASS.

- [ ] **Step 4: Verify full compilation**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: Compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/cluster/join.go internal/cluster/join_test.go
git commit -m "feat: JoinEngine with PreFlight, Execute, and rollback"
```

---

### Task 6: Rewrite Web UI Handler to Use JoinEngine

**Files:**
- Modify: `internal/feature/cluster/handler.go`
- Modify: `internal/api/router.go:149`
- Modify: `cmd/sfpanel/main.go:120-220`

- [ ] **Step 1: Update Handler struct**

In `internal/feature/cluster/handler.go`, replace the `Handler` struct (line 45-49):

```go
type Handler struct {
	Manager      *cluster.Manager
	Config       *config.Config
	ConfigPath   string
	DB           *sql.DB
	LiveActivate cluster.LiveActivateFunc
}
```

Add `"database/sql"` to the imports.

- [ ] **Step 2: Rewrite JoinCluster handler**

Replace the entire `JoinCluster` method (lines 129-231) with:

```go
func (h *Handler) JoinCluster(c echo.Context) error {
	if h.Manager != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Already part of a cluster")
	}

	var body struct {
		LeaderAddress    string `json:"leader_address"`
		Token            string `json:"token"`
		AdvertiseAddress string `json:"advertise_address"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if body.LeaderAddress == "" || body.Token == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "leader_address and token are required")
	}

	engine := &cluster.JoinEngine{
		ConfigPath: h.ConfigPath,
		Config:     h.Config,
		DB:         h.DB,
		OnActivate: h.LiveActivate,
	}

	// Pre-flight check
	pfResult, err := engine.PreFlight(body.LeaderAddress, body.Token)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, err.Error())
	}

	advertise := body.AdvertiseAddress
	if advertise == "" {
		advertise = pfResult.RecommendedIP
	}

	// Execute join
	result, err := engine.Execute(body.LeaderAddress, body.Token, advertise)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
	}

	// Update handler's Manager pointer for subsequent requests
	if result.Manager != nil {
		h.Manager = result.Manager
	}

	return response.OK(c, map[string]interface{}{
		"message":      "Joined cluster successfully",
		"cluster_name": result.ClusterName,
		"node_id":      result.NodeID,
		"live":         result.Manager != nil,
	})
}
```

- [ ] **Step 3: Rewrite InitCluster handler**

Replace the `InitCluster` method (lines 51-127) with:

```go
func (h *Handler) InitCluster(c echo.Context) error {
	if h.Manager != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Already part of a cluster")
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}

	var body struct {
		Name             string `json:"name"`
		AdvertiseAddress string `json:"advertise_address"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	clusterName := body.Name
	if clusterName == "" {
		clusterName = "sfpanel"
	}

	advertise := body.AdvertiseAddress
	if advertise == "" {
		advertise = cluster.DetectFallbackIP()
	}
	if advertise == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cannot detect advertise address. Please provide one.")
	}

	h.Config.Cluster.AdvertiseAddress = advertise

	grpcPort := h.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = h.Config.Server.Port + 1
		h.Config.Cluster.GRPCPort = grpcPort
	}

	h.Config.Cluster.APIPort = h.Config.Server.Port
	mgr := cluster.NewManager(&h.Config.Cluster)
	if err := mgr.Init(clusterName); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Init failed: %v", err))
	}

	h.Config.Cluster = *mgr.GetConfig()

	// Save config
	data, err := yaml.Marshal(h.Config)
	if err != nil {
		mgr.Shutdown()
		os.RemoveAll(h.Config.Cluster.DataDir)
		os.RemoveAll(h.Config.Cluster.CertDir)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
	}
	if err := config.AtomicWriteFile(h.ConfigPath, data, 0600); err != nil {
		mgr.Shutdown()
		os.RemoveAll(h.Config.Cluster.DataDir)
		os.RemoveAll(h.Config.Cluster.CertDir)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, fmt.Sprintf("Failed to save config: %v", err))
	}

	mgr.Shutdown()

	// Live activate
	if h.LiveActivate != nil {
		newMgr, err := h.LiveActivate(h.Config, h.ConfigPath)
		if err != nil {
			slog.Error("live activation failed after init", "error", err)
			return response.OK(c, map[string]interface{}{
				"message": "Cluster initialized but live activation failed. Restart required.",
				"name":    clusterName,
				"node_id": h.Config.Cluster.NodeID,
				"live":    false,
			})
		}
		h.Manager = newMgr

		// Store JWT secret in FSM
		if h.Config.Auth.JWTSecret != "" {
			newMgr.SetConfig("jwt_secret", h.Config.Auth.JWTSecret)
		}

		// Sync admin account to FSM
		if h.DB != nil {
			var username, passwordHash string
			var totpSecret sql.NullString
			if err := h.DB.QueryRow("SELECT username, password, totp_secret FROM admin LIMIT 1").Scan(&username, &passwordHash, &totpSecret); err == nil {
				totp := ""
				if totpSecret.Valid {
					totp = totpSecret.String
				}
				newMgr.SyncAccountFromDB(username, passwordHash, totp)
			}
		}
	}

	slog.Info("cluster initialized via UI", "component", "cluster", "name", clusterName)

	return response.OK(c, map[string]interface{}{
		"message": "Cluster initialized successfully",
		"name":    clusterName,
		"node_id": h.Config.Cluster.NodeID,
		"live":    h.Manager != nil,
	})
}
```

- [ ] **Step 4: Update GetNetworkInterfaces to accept leader_addr**

In the same handler file, update `GetNetworkInterfaces` to add recommended IP:

```go
func (h *Handler) GetNetworkInterfaces(c echo.Context) error {
	type ifaceInfo struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	var result []ifaceInfo
	ifaces, err := net.Interfaces()
	if err != nil {
		return response.OK(c, map[string]interface{}{"interfaces": []interface{}{}, "recommended": "", "reason": ""})
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			result = append(result, ifaceInfo{
				Name:    iface.Name,
				Address: ip.String(),
			})
		}
	}

	recommended := ""
	reason := ""
	leaderAddr := c.QueryParam("leader_addr")
	if leaderAddr != "" {
		if ip, err := cluster.DetectAdvertiseAddress(leaderAddr); err == nil {
			recommended = ip
			leaderHost, _, _ := net.SplitHostPort(leaderAddr)
			if cluster.IsTailscaleIP(net.ParseIP(leaderHost)) {
				reason = "Tailscale network matches leader"
			} else {
				reason = "same network as leader"
			}
		}
	}

	return response.OK(c, map[string]interface{}{
		"interfaces":  result,
		"recommended": recommended,
		"reason":      reason,
	})
}
```

Note: This requires exporting `IsTailscaleIP` — rename `isTailscaleIP` to `IsTailscaleIP` in `detect.go`.

- [ ] **Step 5: Export DetectFallbackIP and IsTailscaleIP in detect.go**

In `internal/cluster/detect.go`, rename:
- `isTailscaleIP` → `IsTailscaleIP` (exported)
- `detectFallbackIP` → `DetectFallbackIP` (exported)

Update all internal callers in `detect.go` and `manager.go` to use the new names.

- [ ] **Step 6: Update router.go to pass DB and LiveActivate**

In `internal/api/router.go`, update line 149:

```go
clusterHandler := &featureCluster.Handler{
	Manager:      clusterMgr,
	Config:       cfg,
	ConfigPath:   cfgPath,
	DB:           database,
	LiveActivate: liveActivate,
}
```

Update `NewRouter` signature to accept `liveActivate cluster.LiveActivateFunc`:

```go
func NewRouter(database *sql.DB, cfg *config.Config, webFS fs.FS, version string, clusterMgr *cluster.Manager, cfgPath string, liveActivate cluster.LiveActivateFunc) *echo.Echo {
```

- [ ] **Step 7: Update main.go — inject LiveActivate, remove JWT sync**

In `cmd/sfpanel/main.go`:

Define the `LiveActivateFunc` before the router creation (around line 230). This replaces the cluster startup block that currently only runs when `cfg.Cluster.Enabled` is true:

```go
	// Define LiveActivate callback for dynamic cluster activation
	liveActivate := cluster.LiveActivateFunc(func(activeCfg *config.Config, activeCfgPath string) (*cluster.Manager, error) {
		activeCfg.Cluster.APIPort = activeCfg.Server.Port
		mgr := cluster.NewManager(&activeCfg.Cluster)
		mgr.SetVersion(version)
		if err := mgr.Start(); err != nil {
			return nil, fmt.Errorf("cluster start: %w", err)
		}

		// Start metrics collection
		metricsDocker, _ := docker.NewClient(activeCfg.Docker.Socket)
		mgr.StartLocalMetrics(func() (float64, float64, float64, int) {
			m, mErr := monitor.GetCoreMetrics()
			if mErr != nil {
				return 0, 0, 0, 0
			}
			diskPercent := 0.0
			if d, dErr := monitor.GetDiskPercent(); dErr == nil {
				diskPercent = d
			}
			containers := 0
			if metricsDocker != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				if list, lErr := metricsDocker.ListContainers(ctx); lErr == nil {
					for _, c := range list {
						if c.State == "running" {
							containers++
						}
					}
				}
				cancel()
			}
			return m.CPU, m.MemPercent, diskPercent, containers
		})

		// Start gRPC server
		grpcServer, grpcErr := cluster.NewGRPCServer(mgr, activeCfg.Server.Port)
		if grpcErr != nil {
			mgr.Shutdown()
			return nil, fmt.Errorf("gRPC server: %w", grpcErr)
		}
		grpcAddr := fmt.Sprintf("0.0.0.0:%d", activeCfg.Cluster.GRPCPort)
		if startErr := grpcServer.Start(grpcAddr); startErr != nil {
			mgr.Shutdown()
			return nil, fmt.Errorf("gRPC listen %s: %w", grpcAddr, startErr)
		}
		middleware.SetClusterProxySecret(grpcServer.ProxySecret())

		return mgr, nil
	})
```

In the existing `if cfg.Cluster.Enabled` block (line 122), use the same callback:

```go
	var clusterMgr *cluster.Manager
	if cfg.Cluster.Enabled {
		var err error
		clusterMgr, err = liveActivate(cfg, cfgPath)
		if err != nil {
			slog.Warn("cluster start failed", "error", err)
		} else {
			defer clusterMgr.Shutdown()
			slog.Info("cluster mode active", "component", "cluster", "name", cfg.Cluster.Name, "node_id", cfg.Cluster.NodeID)

			// Leader-only: sync JWT secret and admin account to FSM
			go func() {
				time.Sleep(5 * time.Second)
				var username, passwordHash string
				var totpSecret sql.NullString
				if err := database.QueryRow("SELECT username, password, totp_secret FROM admin LIMIT 1").Scan(&username, &passwordHash, &totpSecret); err == nil {
					totp := ""
					if totpSecret.Valid {
						totp = totpSecret.String
					}
					if syncErr := clusterMgr.SyncAccountFromDB(username, passwordHash, totp); syncErr != nil {
						slog.Debug("account cluster sync skipped", "error", syncErr)
					}
				}
				if cfg.Auth.JWTSecret != "" {
					clusterMgr.SetConfig("jwt_secret", cfg.Auth.JWTSecret)
				}
			}()
		}
	}
```

**Delete** the entire follower JWT sync block (lines 200-218 that contain `os.Exit(0)`).

Update the `NewRouter` call to pass `liveActivate`:

```go
e := api.NewRouter(database, cfg, sfpanel.WebDistFS, version, clusterMgr, cfgPath, liveActivate)
```

- [ ] **Step 8: Verify compilation**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: Compiles.

- [ ] **Step 9: Run tests**

Run: `cd /opt/stacks/SFPanel && go test ./...`
Expected: All pass.

- [ ] **Step 10: Commit**

```bash
git add internal/feature/cluster/handler.go internal/api/router.go cmd/sfpanel/main.go internal/cluster/detect.go
git commit -m "feat: rewrite Web UI handlers with JoinEngine and LiveActivate"
```

---

### Task 7: Rewrite CLI Commands to Use JoinEngine

**Files:**
- Modify: `cmd/sfpanel/cluster_commands.go`

- [ ] **Step 1: Rewrite clusterJoin**

Replace the `clusterJoin` function (lines 176-258) with:

```go
func clusterJoin(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) < 2 {
		fmt.Println("Usage: sfpanel cluster join <leader-address:port> <token> [--advertise IP] [--config PATH]")
		os.Exit(1)
	}

	leaderAddr := rest[0]
	token := rest[1]

	var advertise string
	for i := 2; i < len(rest); i++ {
		if rest[i] == "--advertise" && i+1 < len(rest) {
			advertise = rest[i+1]
			i++
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Cluster.Enabled {
		log.Fatal("This node is already part of a cluster.")
	}

	// Try to delegate to running server
	if isServerRunning(cfg.Server.Port) {
		fmt.Println("Server is running — delegating join to live server...")
		body := map[string]string{
			"leader_address":    leaderAddr,
			"token":             token,
			"advertise_address": advertise,
		}
		raw, err := callLocalAPI(cfg, "POST", "/api/v1/cluster/join", body)
		if err != nil {
			log.Fatalf("Join via server failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}

	// Server not running — use JoinEngine directly (config-only mode)
	engine := &cluster.JoinEngine{
		ConfigPath: cfgPath,
		Config:     cfg,
	}

	fmt.Printf("Pre-flight check against %s...\n", leaderAddr)
	pf, err := engine.PreFlight(leaderAddr, token)
	if err != nil {
		log.Fatalf("Pre-flight failed: %v", err)
	}
	fmt.Printf("  Cluster: %s (%d/%d nodes)\n", pf.ClusterName, pf.NodeCount, pf.MaxNodes)

	if advertise == "" {
		if cfg.Cluster.AdvertiseAddress != "" {
			advertise = cfg.Cluster.AdvertiseAddress
		} else {
			advertise = pf.RecommendedIP
		}
	}
	fmt.Printf("  Advertise IP: %s (%s)\n", advertise, pf.IPReason)

	result, err := engine.Execute(leaderAddr, token, advertise)
	if err != nil {
		log.Fatalf("Join failed: %v", err)
	}

	fmt.Printf("\nSuccessfully joined cluster '%s'.\n", result.ClusterName)
	fmt.Printf("Node ID: %s\n", result.NodeID)
	fmt.Println("\nRestart sfpanel to activate: sudo systemctl restart sfpanel")
}
```

- [ ] **Step 2: Rewrite clusterInit**

Replace `clusterInit` (lines 135-174) with:

```go
func clusterInit(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	clusterName := "sfpanel"
	var advertise string

	for i := 0; i < len(rest); i++ {
		if rest[i] == "--name" && i+1 < len(rest) {
			clusterName = rest[i+1]
			i++
		}
		if rest[i] == "--advertise" && i+1 < len(rest) {
			advertise = rest[i+1]
			i++
		}
	}

	// Try to delegate to running server
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Cluster.Enabled {
		log.Fatal("Cluster already initialized. Use 'sfpanel cluster status' to check.")
	}

	if isServerRunning(cfg.Server.Port) {
		fmt.Println("Server is running — delegating init to live server...")
		body := map[string]string{
			"name":              clusterName,
			"advertise_address": advertise,
		}
		raw, err := callLocalAPI(cfg, "POST", "/api/v1/cluster/init", body)
		if err != nil {
			log.Fatalf("Init via server failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}

	// Server not running — init directly
	if advertise == "" {
		advertise = cfg.Cluster.AdvertiseAddress
	}
	if advertise == "" {
		advertise = cluster.DetectFallbackIP()
	}
	if advertise == "" {
		log.Fatal("Cannot detect advertise address. Use --advertise IP.")
	}

	cfg.Cluster.AdvertiseAddress = advertise
	cfg.Cluster.APIPort = cfg.Server.Port
	mgr := cluster.NewManager(&cfg.Cluster)
	if err := mgr.Init(clusterName); err != nil {
		log.Fatalf("Failed to initialize cluster: %v", err)
	}

	cfg.Cluster = *mgr.GetConfig()
	if err := saveConfig(cfgPath, cfg); err != nil {
		log.Printf("Warning: failed to save config: %v", err)
	}

	mgr.Shutdown()

	fmt.Printf("Cluster '%s' initialized successfully.\n", clusterName)
	fmt.Printf("Node ID: %s\n", cfg.Cluster.NodeID)
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Restart sfpanel: sudo systemctl restart sfpanel")
	fmt.Println("  2. Create a join token: sfpanel cluster token")
	fmt.Println("  3. On other nodes: sfpanel cluster join <this-ip>:9444 <token>")
}
```

- [ ] **Step 3: Add isServerRunning helper**

Add this function in `cluster_commands.go`:

```go
// isServerRunning checks if the local sfpanel server is listening.
func isServerRunning(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
```

Add `"net"` to imports if not present.

- [ ] **Step 4: Update imports**

Add to imports in `cluster_commands.go`:
```go
"github.com/svrforum/SFPanel/internal/cluster"
```

Remove unused imports (`"github.com/google/uuid"`, `pb "..."`) since JoinEngine handles those internally now.

- [ ] **Step 5: Verify compilation**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: Compiles.

- [ ] **Step 6: Run tests**

Run: `cd /opt/stacks/SFPanel && go test ./...`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/sfpanel/cluster_commands.go
git commit -m "feat: rewrite CLI cluster commands with JoinEngine and server delegation"
```

---

### Task 8: Integration Test and Cleanup

**Files:**
- All modified files
- Verify: `make build`, `make test`

- [ ] **Step 1: Run full build**

Run: `cd /opt/stacks/SFPanel && make build`
Expected: Frontend + backend build succeeds.

- [ ] **Step 2: Run full test suite**

Run: `cd /opt/stacks/SFPanel && make test`
Expected: All tests pass.

- [ ] **Step 3: Remove dead code**

Search for any remaining references to:
- `DetectOutboundIP` — should be fully replaced
- `ErrInvalidToken` — should be fully replaced by `ErrTokenNotFound`/`ErrTokenExpired`
- `os.Exit(1)` or `os.Exit(0)` in cluster handler — should be removed

Run:
```bash
grep -rn "DetectOutboundIP\|ErrInvalidToken\|os\.Exit" internal/feature/cluster/handler.go cmd/sfpanel/main.go
```
Expected: No matches in handler.go. Only legitimate os.Exit calls in main.go (startup failures, not cluster operations).

- [ ] **Step 4: Run lint**

Run: `cd /opt/stacks/SFPanel && make lint`
Expected: No new lint errors.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore: cleanup dead code from cluster join redesign"
```
