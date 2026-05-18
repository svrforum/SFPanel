# A1: P0 cluster + exec foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` (inline) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Close 3 foundational P0s identified in [R-final](../research/2026-05-18-module-hardening/R-final.md) as a single small commit — gRPC stream auth bypass (P0-1), heartbeat recv goroutine leak (P0-2), and `RunWithTimeout(0)` silent zero-deadline (P0-3).

**Architecture:** All three live in foundational packages — `internal/common/exec/exec.go` (P0-3), `internal/cluster/grpc_server.go` (P0-1, P0-2). Each patch is small and independent; tests are unit-level. Single commit because the patches together represent "foundational P0 sweep" with no shared symbols crossing the patches.

**Tech Stack:** Go 1.25, gRPC 1.81, hashicorp/raft 1.7.3, standard `testing`.

**Out of scope:**
- Switching `tls.VerifyClientCertIfGiven` to `RequireAndVerifyClientCert` + separate PreFlight/Join listener — the stream interceptor approach is smaller and preserves the existing handshake semantics.
- Rewriting Commander to accept `context.Context` (Pattern F sweep, planned as B6 — much larger touch).
- Test harness for full gRPC mTLS server in the heartbeat-goroutine test — instead extract the goroutine body into a testable helper.

---

## File structure

- **Modify** `internal/common/exec/exec.go` — guard `RunWithTimeout(timeout)` against `timeout <= 0` (1-line fix).
- **Modify** `internal/common/exec/exec_test.go` — add `TestSystemCommander_RunWithTimeout_ZeroOrNegativeUsesDefault`.
- **Modify** `internal/cluster/grpc_server.go` — extract recv-loop into `runHeartbeatRecvLoop` helper + add stream interceptor + register it.
- **Create** `internal/cluster/grpc_server_test.go` — test for `runHeartbeatRecvLoop` no-leak under parent abort, plus test for `requireClientCertStreamInterceptor` allow/deny.

---

## Task 1 — P0-3: `RunWithTimeout(0)` immediate deadline

**Files:**
- Modify: `internal/common/exec/exec.go:36-52`
- Modify: `internal/common/exec/exec_test.go` (append test)

- [ ] **Step 1.1: Write the failing test**

Append to `internal/common/exec/exec_test.go`:

```go
func TestSystemCommander_RunWithTimeout_ZeroOrNegativeUsesDefault(t *testing.T) {
	// timeout=0 must NOT instantly time out — it must run the command and succeed.
	// Same for negative timeouts (defensive).
	cmd := NewCommander()

	for _, tc := range []time.Duration{0, -1 * time.Second} {
		out, err := cmd.RunWithTimeout(tc, "echo", "hi")
		if err != nil {
			t.Fatalf("timeout=%v: unexpected error: %v", tc, err)
		}
		if out != "hi\n" {
			t.Fatalf("timeout=%v: expected 'hi\\n', got %q", tc, out)
		}
	}
}
```

- [ ] **Step 1.2: Run the test to verify it fails**

Run: `go test ./internal/common/exec/ -run TestSystemCommander_RunWithTimeout_ZeroOrNegativeUsesDefault -v`
Expected: FAIL with "command timed out after 0s" (or "after -1s").

- [ ] **Step 1.3: Implement the fix**

In `internal/common/exec/exec.go`, change `RunWithTimeout`:

```go
func (c *SystemCommander) RunWithTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	if timeout <= 0 {
		// Zero or negative timeout would deadline immediately. Treat as
		// "use default" — easy footgun when a config field carrying a
		// timeout defaults to a zero-value time.Duration.
		timeout = DefaultTimeout
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	if ctx.Err() == context.DeadlineExceeded {
		slog.Warn("command timeout", "cmd", name, "duration_ms", duration.Milliseconds())
		return string(out), fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		slog.Debug("command failed", "cmd", name, "duration_ms", duration.Milliseconds(), "error", err)
	}
	return string(out), err
}
```

- [ ] **Step 1.4: Run the test to verify it passes**

Run: `go test ./internal/common/exec/ -run TestSystemCommander_RunWithTimeout_ZeroOrNegativeUsesDefault -v`
Expected: PASS.

---

## Task 2 — P0-2: Heartbeat recv goroutine leak

**Files:**
- Modify: `internal/cluster/grpc_server.go` (extract recv loop into helper)
- Create: `internal/cluster/grpc_server_test.go` (no-leak assertion)

- [ ] **Step 2.1: Write the failing test**

Create `internal/cluster/grpc_server_test.go` with this content:

```go
package cluster

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// TestHeartbeatRecvLoop_NoLeakOnParentAbort verifies the recv goroutine exits
// promptly when the parent stops consuming. Was a P0 leak: the send to recvCh
// blocks forever if buffer is full and parent has returned.
func TestHeartbeatRecvLoop_NoLeakOnParentAbort(t *testing.T) {
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	recvCh := make(chan recvResult, 1)
	var recvCalls int32
	recv := func() (*pb.HeartbeatPing, error) {
		atomic.AddInt32(&recvCalls, 1)
		// Always succeed instantly — simulates a chatty stream.
		return &pb.HeartbeatPing{NodeId: "n1", Timestamp: time.Now().Unix()}, nil
	}

	done := make(chan struct{})
	go func() {
		runHeartbeatRecvLoop(streamCtx, recv, recvCh)
		close(done)
	}()

	// Parent reads first result (so buffer drained) then "aborts" by NOT
	// reading any more. Goroutine will fill the buffer with one result, then
	// block trying to push the next. Without the fix it stays blocked forever.
	<-recvCh

	// Simulate parent return by cancelling the stream context. The goroutine
	// must exit even though there is no reader and the channel buffer may be full.
	streamCancel()

	select {
	case <-done:
		// Pass.
	case <-time.After(2 * time.Second):
		t.Fatalf("recv goroutine leaked: did not exit within 2s after stream cancel (recvCalls=%d)", atomic.LoadInt32(&recvCalls))
	}
}
```

- [ ] **Step 2.2: Run the test to verify it fails**

Run: `go test ./internal/cluster/ -run TestHeartbeatRecvLoop_NoLeakOnParentAbort -v`
Expected: FAIL — `runHeartbeatRecvLoop` and `recvResult` not defined.

- [ ] **Step 2.3: Implement the fix**

In `internal/cluster/grpc_server.go`:

1. Move the `recvResult` struct out of the function body to package scope (so the test can use it).

Replace the inline definition inside `Heartbeat` (currently around line 196-199):

```go
type recvResult struct {
    ping *pb.HeartbeatPing
    err  error
}
```

with a package-level type:

```go
// recvResult is the message shape pushed onto the heartbeat recv channel.
type recvResult struct {
	ping *pb.HeartbeatPing
	err  error
}
```

Put it above `Heartbeat` (e.g. just below `unauthenticatedMethods`).

2. Extract the recv-loop into a helper:

```go
// runHeartbeatRecvLoop reads from a heartbeat stream and pushes each result
// onto recvCh. Exits when the recv callback returns an error OR when ctx is
// cancelled (so the goroutine doesn't leak if the parent stops consuming).
func runHeartbeatRecvLoop(ctx context.Context, recv func() (*pb.HeartbeatPing, error), recvCh chan<- recvResult) {
	for {
		ping, err := recv()
		select {
		case recvCh <- recvResult{ping, err}:
		case <-ctx.Done():
			// Parent stopped consuming — don't block on a full channel.
			return
		}
		if err != nil {
			return
		}
	}
}
```

3. Replace the inline goroutine in `Heartbeat` (currently lines 200-211) with a call:

```go
recvCh := make(chan recvResult, 1)
go runHeartbeatRecvLoop(stream.Context(), stream.Recv, recvCh)
```

- [ ] **Step 2.4: Run the test to verify it passes**

Run: `go test ./internal/cluster/ -run TestHeartbeatRecvLoop_NoLeakOnParentAbort -v`
Expected: PASS.

---

## Task 3 — P0-1: gRPC stream auth bypass

**Files:**
- Modify: `internal/cluster/grpc_server.go` (add stream interceptor + register it)
- Modify: `internal/cluster/grpc_server_test.go` (append interceptor tests)

- [ ] **Step 3.1: Write the failing tests**

Append to `internal/cluster/grpc_server_test.go`:

```go
import (
	// (extend existing imports — keep these alphabetized with what's already there)
	"crypto/tls"
	"crypto/x509"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// fakeServerStream implements grpc.ServerStream with a configurable context.
type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

func newStreamCtxWithMTLS(authenticated bool) context.Context {
	ctx := context.Background()
	if !authenticated {
		// No peer info at all — simulates a connection that bypassed TLS auth.
		return ctx
	}
	// Synthesize peer info with a VerifiedChains entry — simulates a successful
	// mTLS handshake. The cert content doesn't matter; only len(VerifiedChains).
	tlsInfo := credentials.TLSInfo{
		State: tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{{}}},
		},
	}
	return peer.NewContext(ctx, &peer.Peer{AuthInfo: tlsInfo})
}

func TestRequireClientCertStreamInterceptor_RejectsCertless(t *testing.T) {
	ss := &fakeServerStream{ctx: newStreamCtxWithMTLS(false)}
	info := &grpc.StreamServerInfo{FullMethod: "/sfpanel.cluster.ClusterService/Heartbeat"}
	handlerCalled := false
	handler := func(srv any, ss grpc.ServerStream) error { handlerCalled = true; return nil }

	err := requireClientCertStreamInterceptor(nil, ss, info, handler)

	if handlerCalled {
		t.Fatalf("handler must NOT be called when peer has no verified cert")
	}
	if err == nil {
		t.Fatalf("expected unauthenticated error, got nil")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Fatalf("expected codes.Unauthenticated, got %v", err)
	}
}

func TestRequireClientCertStreamInterceptor_AllowsAuthenticatedStream(t *testing.T) {
	ss := &fakeServerStream{ctx: newStreamCtxWithMTLS(true)}
	info := &grpc.StreamServerInfo{FullMethod: "/sfpanel.cluster.ClusterService/Heartbeat"}
	handlerCalled := false
	handler := func(srv any, ss grpc.ServerStream) error { handlerCalled = true; return nil }

	err := requireClientCertStreamInterceptor(nil, ss, info, handler)

	if !handlerCalled {
		t.Fatalf("handler must be called when peer has verified cert")
	}
	if err != nil {
		t.Fatalf("unexpected error from authenticated stream: %v", err)
	}
}

func TestRequireClientCertStreamInterceptor_AllowsUnauthenticatedMethod(t *testing.T) {
	// PreFlight/Join are intentionally pre-cert; verify they still pass through
	// stream interceptor when invoked as streams (defensive — they are unary today).
	ss := &fakeServerStream{ctx: newStreamCtxWithMTLS(false)}
	info := &grpc.StreamServerInfo{FullMethod: "/sfpanel.cluster.ClusterService/PreFlight"}
	handlerCalled := false
	handler := func(srv any, ss grpc.ServerStream) error { handlerCalled = true; return nil }

	err := requireClientCertStreamInterceptor(nil, ss, info, handler)

	if !handlerCalled {
		t.Fatalf("handler must be called for unauthenticated method")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 3.2: Run the tests to verify they fail**

Run: `go test ./internal/cluster/ -run TestRequireClientCertStreamInterceptor -v`
Expected: FAIL — `requireClientCertStreamInterceptor` not defined.

- [ ] **Step 3.3: Implement the fix**

In `internal/cluster/grpc_server.go`, add the stream interceptor function just below `requireClientCertInterceptor` (around line 68):

```go
// requireClientCertStreamInterceptor mirrors requireClientCertInterceptor for
// streaming RPCs. Without this, grpc.UnaryInterceptor alone leaves the
// streaming surface (Heartbeat) reachable without a verified peer cert
// because tls.VerifyClientCertIfGiven accepts handshakes with no client cert.
func requireClientCertStreamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if unauthenticatedMethods[info.FullMethod] {
		return handler(srv, ss)
	}
	p, ok := peer.FromContext(ss.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "missing peer info")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 {
		return status.Error(codes.Unauthenticated, "client certificate required for this method")
	}
	return handler(srv, ss)
}
```

Then update `NewGRPCServer` to register it (around line 79-82):

```go
server := grpc.NewServer(
	grpc.Creds(creds),
	grpc.UnaryInterceptor(requireClientCertInterceptor),
	grpc.StreamInterceptor(requireClientCertStreamInterceptor),
)
```

- [ ] **Step 3.4: Run the tests to verify they pass**

Run: `go test ./internal/cluster/ -run TestRequireClientCertStreamInterceptor -v`
Expected: PASS (all 3 cases).

---

## Task 4 — Verification

- [ ] **Step 4.1: Run full package test suites**

Run: `go test ./internal/common/exec/... ./internal/cluster/...`
Expected: all tests pass.

- [ ] **Step 4.2: Run a broader test pass to verify no regression**

Run: `go test ./internal/...`
Expected: all pass (or the same set that was failing before this PR is still the same set — never a new failure).

- [ ] **Step 4.3: Verify lint cleanliness**

Run: `make lint` (or `golangci-lint run ./internal/common/exec/... ./internal/cluster/...` if make takes too long).
Expected: no new findings.

---

## Task 5 — Commit

- [ ] **Step 5.1: Stage and commit**

```bash
git add internal/common/exec/exec.go \
        internal/common/exec/exec_test.go \
        internal/cluster/grpc_server.go \
        internal/cluster/grpc_server_test.go \
        docs/superpowers/plans/2026-05-18-fix-p0-cluster-and-exec-foundations.md \
        docs/superpowers/plans/2026-05-18-module-hardening-program.md \
        docs/superpowers/research/2026-05-18-module-hardening/

git commit -m "$(cat <<'EOF'
exec + cluster: close 3 foundational P0s (heartbeat goroutine, gRPC stream auth, RunWithTimeout(0))

- common/exec: RunWithTimeout(0|<0) now uses DefaultTimeout instead of deadlining immediately
- cluster/grpc_server: extract heartbeat recv loop, exit on stream ctx done to drop the buffer-full leak
- cluster/grpc_server: add stream interceptor mirroring the unary one so VerifyClientCertIfGiven doesn't leave streaming RPCs (Heartbeat) reachable without a peer cert
- docs: 2026-05-18 module hardening program + R-final synthesis + per-tier research

Refs the 2026-05-18 review program (296 findings, 25 P0). This closes P0-1, P0-2, P0-3 from R-final.
EOF
)"
```

The commit signing/authoring requirements from CLAUDE.md (svrforum author + no AI references) are honored — no Claude attribution in the message body and the commit will be made via the user's normal `git commit` (env vars handled at the shell level).

---

## Self-review

- [x] Spec coverage — P0-1, P0-2, P0-3 each get a dedicated task with failing test → implementation → passing test.
- [x] No placeholders — every step has the actual code or command.
- [x] Type consistency — `recvResult` is package-scope in both the test and the implementation (Task 2 step 2.3); `runHeartbeatRecvLoop` signature matches between test and impl.
- [x] Test isolation — none of the new tests need a running gRPC server or real cluster; all use direct function invocation with mocked dependencies.
- [x] CLAUDE.md compliance — commit message has no Claude attribution; uses error codes/responses unchanged; no new os/exec direct calls; subprocess output unchanged (no SanitizeOutput regression).
