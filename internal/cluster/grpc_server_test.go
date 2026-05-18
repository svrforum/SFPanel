package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
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

	// Parent reads first result (so the buffer drains once), then "aborts" by
	// not reading any more. The goroutine fills the buffer with one result,
	// then will block on the next push — unless it correctly drops out via
	// the stream context.
	<-recvCh

	// Simulate parent return by cancelling the stream context. The goroutine
	// must exit even though there is no reader and the channel buffer may be
	// full.
	streamCancel()

	select {
	case <-done:
		// Pass.
	case <-time.After(2 * time.Second):
		t.Fatalf("recv goroutine leaked: did not exit within 2s after stream cancel (recvCalls=%d, goroutines=%d)",
			atomic.LoadInt32(&recvCalls), runtime.NumGoroutine())
	}
}

// fakeServerStream implements grpc.ServerStream with a configurable context.
type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

// newStreamCtxWithMTLS synthesizes a context carrying peer info that either
// has a verified TLS chain (authenticated=true) or has no peer info at all
// (authenticated=false) — the latter simulates a connection that bypassed
// client-cert verification because the TLS config used VerifyClientCertIfGiven.
func newStreamCtxWithMTLS(authenticated bool) context.Context {
	ctx := context.Background()
	if !authenticated {
		// No peer info at all — simulates a connection that came in without
		// any TLS client cert (the case the unary interceptor catches and
		// the stream interceptor must also catch).
		return ctx
	}
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
	// PreFlight/Join are intentionally pre-cert. Defensive: even if those
	// ever become streaming RPCs, the stream interceptor should still allow
	// them through, matching the unary interceptor's behaviour.
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
