package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/svrforum/SFPanel/internal/auth"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// localHTTPClient is reused for all proxy requests to 127.0.0.1 to leverage
// HTTP connection pooling instead of creating a new client per request.
var localHTTPClient = &http.Client{Timeout: 30 * time.Second}

// localHTTPClientLong serves proxied requests whose local work can exceed 30s
// (compose up/down/update/import/rollback — first image pull or git clone).
// The fixed 30s on localHTTPClient would otherwise abort these on the loopback
// hop even though the proxy middleware grants the gRPC call a 5-minute budget
// for /docker/compose/ paths — silently turning a slow cross-node compose
// operation into a 502.
var localHTTPClientLong = &http.Client{Timeout: 5 * time.Minute}

// requiresLongLocalTimeout reports whether a proxied path may legitimately run
// longer than the default 30s loopback timeout. Kept in sync with the 5-minute
// branch in middleware/proxy.go's proxyToNodeGRPC.
func requiresLongLocalTimeout(path string) bool {
	return strings.Contains(path, "/docker/compose/")
}

// GRPCServer serves the ClusterService.
type GRPCServer struct {
	pb.UnimplementedClusterServiceServer
	manager     *Manager
	server      *grpc.Server
	listener    net.Listener
	localPort   int
	proxySecret string
}

// unauthenticatedMethods lists gRPC methods that a joining node can legitimately
// call before it has a CA-issued client certificate. Everything else requires
// a verified peer certificate.
// unauthenticatedMethods uses the fully-qualified proto method names from
// the generated code (`sfpanel.cluster.ClusterService/*`). Joining nodes
// haven't received a CA-issued client cert yet, so these two RPCs are the
// only ones permitted without a verified peer certificate.
var unauthenticatedMethods = map[string]bool{
	"/sfpanel.cluster.ClusterService/PreFlight": true,
	"/sfpanel.cluster.ClusterService/Join":      true,
}

// requireClientCertInterceptor rejects RPCs that need mTLS but came in without
// a verified peer certificate. Combined with VerifyClientCertIfGiven on the
// TLS handshake, this lets PreFlight/Join land without a client cert while
// every other method is still gated on cluster CA trust.
func requireClientCertInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if unauthenticatedMethods[info.FullMethod] {
		return handler(ctx, req)
	}
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing peer info")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 {
		return nil, status.Error(codes.Unauthenticated, "client certificate required for this method")
	}
	return handler(ctx, req)
}

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

// extractPeerCN returns the CommonName of the verified mTLS client leaf
// certificate on a gRPC context, or "" when no verified chain is present.
// Node certs are issued with CN == node ID (IssueNodeCert), so the CN is
// the transport-level node identity.
func extractPeerCN(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return ""
	}
	return tlsInfo.State.VerifiedChains[0][0].Subject.CommonName
}

// recvResult is the message shape pushed onto the heartbeat recv channel.
type recvResult struct {
	ping *pb.HeartbeatPing
	err  error
}

// runHeartbeatRecvLoop reads from a heartbeat stream and pushes each result
// onto recvCh. Exits when the recv callback returns an error OR when ctx is
// cancelled — the ctx.Done() arm prevents leaks when the parent stops
// consuming (channel buffer full + no reader would otherwise block forever).
func runHeartbeatRecvLoop(ctx context.Context, recv func() (*pb.HeartbeatPing, error), recvCh chan<- recvResult) {
	for {
		ping, err := recv()
		select {
		case recvCh <- recvResult{ping, err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// NewGRPCServer creates and configures the gRPC server with mTLS.
// localPort is the HTTP server port for proxying requests locally.
func NewGRPCServer(mgr *Manager, localPort int) (*GRPCServer, error) {
	tlsConfig, err := mgr.GetTLS().ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load TLS config: %w", err)
	}

	creds := credentials.NewTLS(tlsConfig)
	server := grpc.NewServer(
		grpc.Creds(creds),
		grpc.UnaryInterceptor(requireClientCertInterceptor),
		grpc.StreamInterceptor(requireClientCertStreamInterceptor),
		// Accept the keepalive pings our own clients send (see the constants
		// in types.go). gRPC's default policy — 5-minute minimum, no pings on
		// idle connections — would answer them with GOAWAY/ENHANCE_YOUR_CALM
		// and tear down exactly the connections keepalive is meant to keep
		// verifiable.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             clusterKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
	)

	// Derive proxy secret from CA cert (shared across all cluster nodes)
	proxySecret := ""
	if caCert, caErr := mgr.GetTLS().LoadCACert(); caErr == nil {
		hash := sha256.Sum256(caCert)
		proxySecret = hex.EncodeToString(hash[:])
	}

	s := &GRPCServer{
		manager:     mgr,
		server:      server,
		localPort:   localPort,
		proxySecret: proxySecret,
	}
	pb.RegisterClusterServiceServer(server, s)

	return s, nil
}

// Start listens and serves on the configured gRPC port.
func (s *GRPCServer) Start(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = lis

	slog.Info("gRPC server listening", "component", "cluster", "addr", addr)
	go func() {
		if err := s.server.Serve(lis); err != nil {
			slog.Error("gRPC server error", "component", "cluster", "error", err)
		}
	}()
	return nil
}

// Stop gracefully stops the gRPC server.
func (s *GRPCServer) Stop() {
	if s.server != nil {
		s.server.GracefulStop()
	}
}

// ProxySecret returns the cluster-internal proxy authentication secret.
func (s *GRPCServer) ProxySecret() string {
	return s.proxySecret
}

// Join handles a node join request.
func (s *GRPCServer) Join(ctx context.Context, req *pb.JoinRequest) (*pb.JoinResponse, error) {
	caCert, nodeCert, nodeKey, peers, err := s.manager.HandleJoin(
		req.NodeId, req.NodeName, req.ApiAddress, req.GrpcAddress, req.Token,
	)
	if err != nil {
		return &pb.JoinResponse{Success: false, Error: err.Error()}, nil
	}

	pbPeers := make([]*pb.NodeInfo, 0, len(peers))
	for _, p := range peers {
		pbPeers = append(pbPeers, &pb.NodeInfo{
			Id:          p.ID,
			Name:        p.Name,
			ApiAddress:  p.APIAddress,
			GrpcAddress: p.GRPCAddress,
			Role:        string(p.Role),
			Status:      string(p.Status),
		})
	}

	state := s.manager.GetRaft().GetFSM().GetState()
	jwtSecret, adminUser, adminPassHash, adminTOTPSecret := s.manager.GetJWTAndAdminFull()

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
		AdminTotpSecret:   adminTOTPSecret,
		RaftTls:           s.manager.config.RaftTLS,
	}, nil
}

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

// Leave handles a node leave request.
func (s *GRPCServer) Leave(ctx context.Context, req *pb.LeaveRequest) (*pb.LeaveResponse, error) {
	if err := s.manager.RemoveNode(req.NodeId); err != nil {
		return &pb.LeaveResponse{Success: false, Error: err.Error()}, nil
	}
	return &pb.LeaveResponse{Success: true}, nil
}

// Heartbeat implements bidirectional heartbeat streaming.
func (s *GRPCServer) Heartbeat(stream pb.ClusterService_HeartbeatServer) error {
	// Sourced from the package-level constant so it stays coupled to the
	// follower send interval — an idle timeout equal to (or below) the send
	// interval races the timer ahead of the next ping and kills the stream.
	// See metricsStreamSendInterval in types.go.
	const idleTimeout = metricsStreamIdleTimeout

	// Bind the stream to its mTLS identity. The stream interceptor already
	// guarantees a verified client cert here; node certs carry CN == node ID,
	// so a ping whose NodeId differs from the peer CN is a cluster member
	// spoofing another node's liveness/metrics (and could trigger
	// PromoteOnHeartbeatIfPending for a node that never connected).
	// PreFlight/Join are unary and unaffected by this check.
	peerCN := extractPeerCN(stream.Context())
	if peerCN == "" {
		return status.Error(codes.Unauthenticated, "heartbeat requires a verified client certificate")
	}

	// Single goroutine for receiving — the runHeartbeatRecvLoop helper also
	// drops out on stream.Context().Done() so it doesn't leak when this
	// outer select exits via timeout/done while the channel buffer is full.
	recvCh := make(chan recvResult, 1)
	go runHeartbeatRecvLoop(stream.Context(), stream.Recv, recvCh)

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	for {
		select {
		case result := <-recvCh:
			if result.err != nil {
				return result.err
			}
			if result.ping.NodeId != peerCN {
				return status.Errorf(codes.PermissionDenied,
					"heartbeat node_id %q does not match peer certificate identity", result.ping.NodeId)
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)

			s.manager.GetHeartbeat().RecordHeartbeat(&NodeMetrics{
				NodeID:         result.ping.NodeId,
				CPUPercent:     result.ping.CpuPercent,
				MemoryPercent:  result.ping.MemoryPercent,
				DiskPercent:    result.ping.DiskPercent,
				ContainerCount: int(result.ping.ContainerCount),
				Version:        result.ping.Version,
				Timestamp:      result.ping.Timestamp,
			})

			// Two-phase join: a node added as non-voter via HandleJoin gets
			// promoted on its first successful heartbeat — that's the
			// strongest in-cluster signal that the new node is fully up
			// (gRPC connected, certs accepted, Raft caught up). Idempotent
			// for already-voter nodes; a missing FSM entry just no-ops.
			s.manager.PromoteOnHeartbeatIfPending(result.ping.NodeId)

			if err := stream.Send(&pb.HeartbeatPong{
				LeaderId:  s.manager.GetRaft().LeaderID(),
				Timestamp: result.ping.Timestamp,
			}); err != nil {
				return err
			}
		case <-timer.C:
			return fmt.Errorf("heartbeat stream idle timeout (%v)", idleTimeout)
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// ProxyRequest forwards an API request to this node's local HTTP handler.
func (s *GRPCServer) ProxyRequest(ctx context.Context, req *pb.APIRequest) (*pb.APIResponse, error) {
	// Build local HTTP request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	localURL := fmt.Sprintf("http://127.0.0.1:%d%s", s.localPort, req.Path)
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, localURL, body)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       []byte(fmt.Sprintf(`{"success":false,"error":{"code":"PROXY_ERROR","message":"%s"}}`, err.Error())),
		}, nil
	}

	// Copy headers from the inbound gRPC request, but skip the auth
	// credentials: Authorization and the internal-proxy secrets are set
	// fresh below from THIS node's secrets, never trusted from the peer.
	//
	// X-SFPanel-Original-User / X-SFPanel-Original-Node are deliberately NOT
	// skipped. They carry the identity the *forwarding* node already
	// authenticated (proxy.go re-sets Original-User from its JWT-validated
	// c.Get("username")), and the loopback handler consumes them for audit
	// attribution. Trust here rests on two facts: this RPC is gated on a
	// verified cluster-CA client cert (requireClientCertInterceptor), and
	// every member already shares the JWT signing secret — a malicious
	// member could forge any identity by minting a JWT directly, so
	// stripping these headers would buy no security while collapsing every
	// cross-node forwarded action to "admin" in the audit log. Do NOT add
	// them to this skip list. (See cluster/CLAUDE.md "Header copy".)
	for k, v := range req.Headers {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "X-Sfpanel-Internal-Proxy",
			"X-Sfpanel-Internal-Proxy-V2":
			continue
		case "Accept-Encoding":
			// Let the loopback handler return PLAIN — the forwarding (edge) node
			// re-compresses once for the browser. Keeping Accept-Encoding would
			// gzip here AND at the edge → a double-gzipped body the browser can't
			// decode (Content-Encoding says gzip only once).
			continue
		}
		httpReq.Header.Set(k, v)
	}

	// Use internal proxy authentication (bypasses JWT validation): v2-only
	// (HMAC + nonce + timestamp). The loopback receiver is this same binary,
	// which accepts v2; the v1 static-secret header is no longer sent.
	if s.proxySecret != "" {
		if v2 := auth.SignProxyRequestV2(req.Method, req.Path); v2 != "" {
			httpReq.Header.Set("X-SFPanel-Internal-Proxy-V2", v2)
		}
	} else if req.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.AuthToken)
	}

	// Execute locally (reuse connection pool). Long-running paths get the
	// 5-minute client so the loopback hop doesn't cap below the gRPC budget.
	client := localHTTPClient
	if requiresLongLocalTimeout(req.Path) {
		client = localHTTPClientLong
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 502,
			Body:       []byte(fmt.Sprintf(`{"success":false,"error":{"code":"PROXY_ERROR","message":"%s"}}`, err.Error())),
		}, nil
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       []byte(`{"success":false,"error":{"code":"PROXY_ERROR","message":"failed to read response"}}`),
		}, nil
	}

	// The whole body ships in one unary APIResponse, which gRPC caps at its
	// 4 MiB default receive size on the requesting node. A response over that
	// would fail with a cryptic transport error; return an actionable one
	// instead. Large/streaming routes are expected to use the HTTP relay (a
	// -stream suffix or the proxy allowlist), not this unary path.
	const maxUnaryProxyBodyBytes = 4*1024*1024 - 64*1024 // headroom under the 4 MiB cap
	if len(respBody) > maxUnaryProxyBodyBytes {
		return &pb.APIResponse{
			StatusCode: 502,
			Body:       []byte(`{"success":false,"error":{"code":"PROXY_RESPONSE_TOO_LARGE","message":"response too large for the cross-node unary proxy; route it through the streaming relay"}}`),
		}, nil
	}

	// Collect response headers
	respHeaders := make(map[string]string)
	for k, v := range httpResp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	return &pb.APIResponse{
		StatusCode: int32(httpResp.StatusCode),
		Body:       respBody,
		Headers:    respHeaders,
	}, nil
}

// GetMetrics returns current node metrics.
func (s *GRPCServer) GetMetrics(ctx context.Context, req *pb.MetricsRequest) (*pb.MetricsResponse, error) {
	m := s.manager.GetHeartbeat().GetMetrics(req.NodeId)
	if m == nil {
		return &pb.MetricsResponse{NodeId: req.NodeId}, nil
	}
	return &pb.MetricsResponse{
		NodeId:         m.NodeID,
		CpuPercent:     m.CPUPercent,
		MemoryPercent:  m.MemoryPercent,
		DiskPercent:    m.DiskPercent,
		ContainerCount: int32(m.ContainerCount),
		UptimeSeconds:  m.UptimeSeconds,
	}, nil
}

// Subscribe sends cluster events to the client.
func (s *GRPCServer) Subscribe(req *pb.SubscribeRequest, stream pb.ClusterService_SubscribeServer) error {
	return status.Errorf(codes.Unimplemented, "Subscribe is not yet implemented")
}
