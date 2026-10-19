package cluster

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
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

// Proxy timeouts, picked by ProxyTimeoutFor for the sending node's gRPC call
// and this node's loopback hop alike. The long one serves the operations in
// longProxyPaths, whose local work can exceed 30s (a first image pull, mkfs on
// a large volume, an apt install); a loopback client fixed at 30s would abort
// these on this hop while the sender still waited, silently turning a slow
// cross-node operation into a 502.
const (
	localHTTPTimeout     = 30 * time.Second
	localHTTPLongTimeout = 5 * time.Minute
)

// localClient returns a cached client for the loopback hop. Clients are built
// once and reused so the connection pool survives across proxied requests —
// building one per request would also mean re-reading and re-parsing the CA on
// every cross-node call.
func (s *GRPCServer) localClient(long bool) (*http.Client, error) {
	s.localClientMu.Lock()
	defer s.localClientMu.Unlock()
	target := &s.localHTTP
	timeout := localHTTPTimeout
	if long {
		target = &s.localHTTPLong
		timeout = localHTTPLongTimeout
	}
	if *target == nil {
		c, err := s.local.HTTPClient(timeout)
		if err != nil {
			return nil, err
		}
		*target = c
	}
	return *target, nil
}

// requiresLongLocalTimeout reports whether a proxied path may legitimately run
// longer than the default 30s loopback timeout.
func requiresLongLocalTimeout(path string) bool {
	return ProxyTimeoutFor(path) == localHTTPLongTimeout
}

// GRPCServer serves the ClusterService.
type GRPCServer struct {
	pb.UnimplementedClusterServiceServer
	manager   *Manager
	server    *grpc.Server
	listener  net.Listener
	localPort int
	// local describes how to reach this node's own panel port. It replaces the
	// former hardcoded "http://127.0.0.1:%d" so the hop follows the panel when
	// it moves to TLS — this is the choke point for EVERY gRPC-relayed
	// cross-node request, so a missed scheme here breaks all ?node= proxying.
	local LocalPanel

	localClientMu sync.Mutex
	localHTTP     *http.Client
	localHTTPLong *http.Client
}

// LocalPanel is the subset of paneltls.Self this package needs. It is an
// interface so internal/cluster does not have to import the TLS package (and
// so tests can drive the hop against an httptest.Server).
type LocalPanel interface {
	URL(path string) string
	HTTPClient(timeout time.Duration) (*http.Client, error)
}

// unauthenticatedMethods lists gRPC methods that a joining node can legitimately
// call before it has a CA-issued client certificate. Everything else requires
// a verified peer certificate.
// unauthenticatedMethods uses the fully-qualified proto method names from
// the generated code (`sfpanel.cluster.ClusterService/*`). Joining nodes and
// registering agents haven't received a CA-issued client cert yet, so these
// RPCs are the only ones permitted without a verified peer certificate.
var unauthenticatedMethods = map[string]bool{
	"/sfpanel.cluster.ClusterService/PreFlight":     true,
	"/sfpanel.cluster.ClusterService/Join":          true,
	"/sfpanel.cluster.ClusterService/RegisterAgent": true,
}

// requireClientCertInterceptor rejects RPCs that need mTLS but came in without
// a verified peer certificate. Combined with VerifyClientCertIfGiven on the
// TLS handshake, this lets PreFlight/Join/RegisterAgent land without a client
// cert while every other method is still gated on cluster CA trust.
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

// peerLeaf returns the verified mTLS client leaf certificate on a gRPC
// context, or nil when no verified chain is present.
func peerLeaf(ctx context.Context) *x509.Certificate {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return nil
	}
	return tlsInfo.State.VerifiedChains[0][0]
}

// extractPeerCN returns the CommonName of the verified client leaf, or "".
// Node certs are issued with CN == node ID (IssueNodeCert), so the CN is the
// transport-level node identity.
func extractPeerCN(ctx context.Context) string {
	if leaf := peerLeaf(ctx); leaf != nil {
		return leaf.Subject.CommonName
	}
	return ""
}

// callerPolicy says who may call a method once mTLS has verified the
// caller's certificate.
type callerPolicy int

const (
	callerMember  callerPolicy = iota + 1 // any node in the FSM
	callerManager                         // a manager node in the FSM
)

// methodPolicy is an allow-list: a method missing here is refused, so an RPC
// added later stays closed until someone decides who may call it.
// PreFlight, Join and RegisterAgent never reach it (unauthenticatedMethods).
var methodPolicy = map[string]callerPolicy{
	"/sfpanel.cluster.ClusterService/Heartbeat":     callerMember,
	"/sfpanel.cluster.ClusterService/Leave":         callerManager, // for itself only: see Leave
	"/sfpanel.cluster.ClusterService/GetMetrics":    callerManager,
	"/sfpanel.cluster.ClusterService/Subscribe":     callerManager,
	"/sfpanel.cluster.ClusterService/ProxyRequest":  callerManager,
	"/sfpanel.cluster.ClusterService/ReportPanelCA": callerMember, // for itself only: see ReportPanelCA
}

// authorizeCaller checks the verified peer certificate against methodPolicy.
// Before this, any certificate the cluster CA signed could call anything —
// proxy to any node, remove any node.
func (s *GRPCServer) authorizeCaller(ctx context.Context, method string) error {
	policy, ok := methodPolicy[method]
	if !ok {
		return status.Errorf(codes.PermissionDenied, "%s is not open to cluster members", method)
	}
	leaf := peerLeaf(ctx)
	if leaf == nil {
		return status.Error(codes.Unauthenticated, "client certificate required for this method")
	}
	if s.manager == nil || s.manager.raft == nil {
		return status.Error(codes.Unavailable, "cluster state not available")
	}
	cn := leaf.Subject.CommonName
	node := s.manager.raft.GetFSM().GetNode(cn)
	if node == nil {
		return notMemberError(s.manager, cn)
	}
	if policy == callerManager && (!node.IsManager() || CertRole(leaf) == CertRoleAgent) {
		return status.Errorf(codes.PermissionDenied, "%s may not call %s", cn, method)
	}
	return nil
}

func (s *GRPCServer) authorizeUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if unauthenticatedMethods[info.FullMethod] {
		return handler(ctx, req)
	}
	if err := s.authorizeCaller(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func (s *GRPCServer) authorizeStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if unauthenticatedMethods[info.FullMethod] {
		return handler(srv, ss)
	}
	if err := s.authorizeCaller(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
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
func NewGRPCServer(mgr *Manager, localPort int, local LocalPanel) (*GRPCServer, error) {
	tlsConfig, err := mgr.GetTLS().ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load TLS config: %w", err)
	}

	creds := credentials.NewTLS(tlsConfig)

	s := &GRPCServer{
		manager:   mgr,
		localPort: localPort,
		local:     local,
	}
	s.server = grpc.NewServer(
		grpc.Creds(creds),
		grpc.ChainUnaryInterceptor(requireClientCertInterceptor, s.authorizeUnary),
		grpc.ChainStreamInterceptor(requireClientCertStreamInterceptor, s.authorizeStream),
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
	pb.RegisterClusterServiceServer(s.server, s)

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
//
// The CA key goes back whatever the token says. A joiner asks with no token
// first, over a connection it cannot verify yet, compares the key with its
// pin, and sends the token only over a connection verified against that key:
// a server in the middle that relays this answer never sees the token.
func (s *GRPCServer) PreFlight(ctx context.Context, req *pb.PreFlightRequest) (*pb.PreFlightResponse, error) {
	caKey, err := s.manager.CAPublicKey()
	if err != nil {
		return &pb.PreFlightResponse{Valid: false, Error: errCAUnavailable}, nil
	}
	clusterName, nodeCount, maxNodes, err := s.manager.HandlePreFlight(req.Token)
	if err != nil {
		return &pb.PreFlightResponse{Valid: false, Error: err.Error(), CaPublicKey: caKey}, nil
	}
	return &pb.PreFlightResponse{
		Valid:       true,
		ClusterName: clusterName,
		NodeCount:   int32(nodeCount),
		MaxNodes:    int32(maxNodes),
		CaPublicKey: caKey,
	}, nil
}

// Leave handles a node leave request.
func (s *GRPCServer) Leave(ctx context.Context, req *pb.LeaveRequest) (*pb.LeaveResponse, error) {
	// A member may take only itself out. Removing another node is the
	// operator's call, made through the panel API on the leader.
	if caller := extractPeerCN(ctx); caller != req.NodeId {
		return nil, status.Errorf(codes.PermissionDenied, "%s may not remove %s", caller, req.NodeId)
	}
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
			// Membership is checked on every ping, not only when the stream
			// opened: otherwise a node removed while its stream is open stays
			// recorded as alive for as long as it keeps pinging, and an agent
			// never closes its stream on its own — it has no Raft leader
			// change to react to. The FSM is read directly: Manager.GetNode
			// also copies the whole state and the health table per call.
			if s.manager.raft.GetFSM().GetNode(peerCN) == nil {
				return notMemberError(s.manager, peerCN)
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
				Capabilities:   result.ping.Capabilities,
			})

			// Two-phase join: a node added as non-voter via HandleJoin gets
			// promoted on its first successful heartbeat — that's the
			// strongest in-cluster signal that the new node is fully up
			// (gRPC connected, certs accepted, Raft caught up). Idempotent
			// for already-voter nodes; a missing FSM entry just no-ops.
			s.manager.PromoteOnHeartbeatIfPending(result.ping.NodeId)

			if err := stream.Send(&pb.HeartbeatPong{
				LeaderId:          s.manager.GetRaft().LeaderID(),
				Timestamp:         result.ping.Timestamp,
				LeaderGrpcAddress: s.manager.GetRaft().LeaderGRPCAddress(),
				Managers:          s.manager.managerInfos(),
				FromLeader:        s.manager.IsLeader(),
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

// proxyErrorBody is the JSON error the loopback hop returns. Built with
// encoding/json: error text holds quotes (Go's URL errors quote the URL),
// which a %s template turned into a body nothing could parse.
func proxyErrorBody(code, message string) []byte {
	b, _ := json.Marshal(map[string]any{"success": false, "error": map[string]string{"code": code, "message": message}})
	return b
}

// ProxyRequest forwards an API request to this node's local HTTP handler.
func (s *GRPCServer) ProxyRequest(ctx context.Context, req *pb.APIRequest) (*pb.APIResponse, error) {
	// Build local HTTP request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	localURL := s.local.URL(req.Path)
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, localURL, body)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       proxyErrorBody("PROXY_ERROR", err.Error()),
		}, nil
	}

	// Copy headers from the inbound gRPC request, but skip the auth
	// credentials: the internal-proxy header is set fresh below from THIS
	// node's secret, and neither it nor Authorization is trusted from the peer.
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
	// (HMAC + nonce + timestamp), signed with the secret this node has
	// installed. The loopback receiver is this same binary, which accepts v2;
	// the v1 static-secret header is no longer sent. The sender's AuthToken is
	// never used: it was the operator's access token, and a node with no
	// secret installed must send the hop bare and be refused, not run it on
	// that token.
	if v2 := auth.SignProxyRequestV2(req.Method, req.Path); v2 != "" {
		httpReq.Header.Set("X-SFPanel-Internal-Proxy-V2", v2)
	}

	// Execute locally (reuse connection pool). Long-running paths get the
	// 5-minute client so the loopback hop doesn't cap below the gRPC budget.
	client, err := s.localClient(requiresLongLocalTimeout(req.Path))
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       proxyErrorBody("PROXY_ERROR", "local client: "+err.Error()),
		}, nil
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 502,
			Body:       proxyErrorBody("PROXY_ERROR", err.Error()),
		}, nil
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       proxyErrorBody("PROXY_ERROR", "failed to read response"),
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
			Body:       proxyErrorBody("PROXY_RESPONSE_TOO_LARGE", "response too large for the cross-node unary proxy; route it through the streaming relay"),
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
