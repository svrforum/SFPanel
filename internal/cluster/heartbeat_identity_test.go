package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"sync"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// ctxWithPeerCN synthesizes a gRPC context whose peer presented a verified
// client cert with the given CommonName — the shape credentials.NewTLS
// produces after a successful mTLS handshake.
func ctxWithPeerCN(parent context.Context, cn string) context.Context {
	tlsInfo := credentials.TLSInfo{
		State: tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{
				{Subject: pkix.Name{CommonName: cn}},
			}},
		},
	}
	return peer.NewContext(parent, &peer.Peer{AuthInfo: tlsInfo})
}

func TestExtractPeerCN(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"no peer info", context.Background(), ""},
		{"peer without TLS auth", peer.NewContext(context.Background(), &peer.Peer{}), ""},
		{"empty verified chains", peer.NewContext(context.Background(), &peer.Peer{
			AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{}},
		}), ""},
		{"verified chain with CN", ctxWithPeerCN(context.Background(), "node-uuid-1"), "node-uuid-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractPeerCN(tc.ctx); got != tc.want {
				t.Fatalf("extractPeerCN = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeHeartbeatStream implements pb.ClusterService_HeartbeatServer. Recv
// yields one ping, then blocks until ctx is done so the recv goroutine in
// Heartbeat exits cleanly when the test cancels.
type fakeHeartbeatStream struct {
	grpc.ServerStream
	ctx  context.Context
	once sync.Once
	ping *pb.HeartbeatPing
}

func (f *fakeHeartbeatStream) Context() context.Context     { return f.ctx }
func (f *fakeHeartbeatStream) Send(*pb.HeartbeatPong) error { return nil }
func (f *fakeHeartbeatStream) Recv() (*pb.HeartbeatPing, error) {
	var first bool
	f.once.Do(func() { first = true })
	if first {
		return f.ping, nil
	}
	<-f.ctx.Done()
	return nil, f.ctx.Err()
}

// A stream that reached the handler without a verified client cert (e.g. if
// the interceptor were ever bypassed) must be rejected before any ping is
// processed.
func TestHeartbeat_RejectsCertlessStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHeartbeatStream{
		ctx:  ctx, // no peer info at all
		ping: &pb.HeartbeatPing{NodeId: "node-a", Timestamp: time.Now().Unix()},
	}
	// manager stays nil: the identity check must fire before any manager use.
	s := &GRPCServer{}

	err := s.Heartbeat(stream)
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Fatalf("expected codes.Unauthenticated, got %v", err)
	}
}

// A cluster member presenting node A's cert must not be able to report
// liveness/metrics as node B.
func TestHeartbeat_RejectsSpoofedNodeID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHeartbeatStream{
		ctx:  ctxWithPeerCN(ctx, "node-a"),
		ping: &pb.HeartbeatPing{NodeId: "node-b", Timestamp: time.Now().Unix()},
	}
	// manager stays nil: the mismatch must be rejected before RecordHeartbeat.
	s := &GRPCServer{}

	errCh := make(chan error, 1)
	go func() { errCh <- s.Heartbeat(stream) }()

	select {
	case err := <-errCh:
		if s, ok := status.FromError(err); !ok || s.Code() != codes.PermissionDenied {
			t.Fatalf("expected codes.PermissionDenied, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Heartbeat did not reject spoofed node_id within 2s")
	}
}
