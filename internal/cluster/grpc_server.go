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

// loopbackClients caches the clients for the loopback hop. Clients are built
// once and reused so the connection pool survives across proxied requests —
// building one per request would also mean re-reading and re-parsing the CA on
// every cross-node call.
type loopbackClients struct {
	mu          sync.Mutex
	short, long *http.Client
}

// get returns the cached client, the five-minute one when long.
func (lc *loopbackClients) get(local LocalPanel, long bool) (*http.Client, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	target := &lc.short
	timeout := localHTTPTimeout
	if long {
		target = &lc.long
		timeout = localHTTPLongTimeout
	}
	if *target == nil {
		c, err := local.HTTPClient(timeout)
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
	local   LocalPanel
	clients loopbackClients
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

// peerRootPin is the CAPin of the CA at the root of the caller's verified
// chain — which CA signed the certificate this connection presented — or "".
func peerRootPin(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return ""
	}
	chain := info.State.VerifiedChains[0]
	return CAPin(chain[len(chain)-1].RawSubjectPublicKeyInfo)
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
	callerAgent                           // an agent node in the FSM, with an agent's certificate
)

// methodPolicy is an allow-list: a method missing here is refused, so an RPC
// added later stays closed until someone decides who may call it.
// PreFlight, Join and RegisterAgent never reach it (unauthenticatedMethods).
var methodPolicy = map[string]callerPolicy{
	"/sfpanel.cluster.ClusterService/Heartbeat":        callerManager,
	"/sfpanel.cluster.ClusterService/AgentHeartbeat":   callerAgent,
	"/sfpanel.cluster.ClusterService/Leave":            callerManager, // for itself only: see Leave
	"/sfpanel.cluster.ClusterService/GetMetrics":       callerManager,
	"/sfpanel.cluster.ClusterService/Subscribe":        callerManager,
	"/sfpanel.cluster.ClusterService/ProxyRequest":     callerManager,
	"/sfpanel.cluster.ClusterService/ReportPanelCA":    callerMember, // for itself only: see ReportPanelCA
	"/sfpanel.cluster.ClusterService/RenewCertificate": callerAgent,  // for itself only: see RenewCertificate
	"/sfpanel.cluster.ClusterService/RotateAgentKey":   callerAgent,  // for itself only: see RotateAgentKey
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
	if policy == callerAgent && (node.IsManager() || CertRole(leaf) != CertRoleAgent) {
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

	// Each connection is tracked, and closed once the CA that verified it is
	// no longer trusted (conn_trust.go).
	creds := mgr.GetTLS().serverCreds(tlsConfig)

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
		req.NodeId, req.NodeName, req.ApiAddress, req.GrpcAddress, req.Token, req.Capabilities,
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
	// The generation is read before the key (GetJWTAndAdminFull reads the
	// FSM again): with a rotation between the two reads, the joiner gets the
	// new key under the old generation and applies it once more, rather than
	// a retired key under the new one, which it would never replace.
	jwtKey, _ := clusterJWTKey(state)
	jwtSecret, adminUser, adminPassHash, adminTOTPSecret := s.manager.GetJWTAndAdminFull()

	return &pb.JoinResponse{
		Success:           true,
		ClusterName:       state.Config["cluster_name"],
		CaCert:            caCert,
		NodeCert:          nodeCert,
		NodeKey:           nodeKey,
		Peers:             pbPeers,
		JwtSecret:         jwtSecret,
		JwtGeneration:     jwtKey.Generation,
		AdminUsername:     adminUser,
		AdminPasswordHash: adminPassHash,
		AdminTotpSecret:   adminTOTPSecret,
		RaftTls:           s.manager.config.RaftTLS,
	}, nil
}

// RegisterAgent registers an agent (leader only). A follower answers with the
// leader's address so the agent can re-dial it with the same CA pin.
func (s *GRPCServer) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	if !s.manager.IsLeader() {
		return &pb.RegisterAgentResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: s.manager.GetRaft().LeaderGRPCAddress()}, nil
	}
	res, err := s.manager.HandleRegisterAgent(RegisterAgentInput{Token: req.Token, CSR: req.Csr, NodeName: req.NodeName, APIAddress: req.ApiAddress, GRPCAddress: req.GrpcAddress, PanelCA: req.PanelCaCert, Version: req.Version, NodeID: req.NodeId})
	if err != nil {
		return &pb.RegisterAgentResponse{Error: err.Error()}, nil
	}
	return &pb.RegisterAgentResponse{Success: true, NodeId: res.NodeID, NodeCert: res.NodeCert, CaCert: res.CACert, AgentKey: res.AgentKey, Managers: res.Managers, ClusterName: res.ClusterName}, nil
}

// ReportPanelCA stores the caller's panel CA so managers can reach its panel
// over HTTPS. The node is the caller's certificate, never a request field.
// A follower answers with the leader's address, where the caller reports
// instead.
func (s *GRPCServer) ReportPanelCA(ctx context.Context, req *pb.ReportPanelCARequest) (*pb.ReportPanelCAResponse, error) {
	// The caller policy has checked membership already; checked again here
	// because what is stored is keyed by this CN. The FSM is read directly,
	// as in Heartbeat.
	cn := extractPeerCN(ctx)
	if cn == "" || s.manager.raft.GetFSM().GetNode(cn) == nil {
		return nil, notMemberError(s.manager, cn)
	}
	if !s.manager.IsLeader() {
		return &pb.ReportPanelCAResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: s.manager.GetRaft().LeaderGRPCAddress()}, nil
	}
	if err := s.manager.SetReportedPanelCA(cn, string(req.CaCert), time.Now()); err != nil {
		return &pb.ReportPanelCAResponse{Error: err.Error()}, nil
	}
	return &pb.ReportPanelCAResponse{Success: true}, nil
}

// RenewCertificate signs the calling agent a certificate from the CA a
// rotation signs with now (HandleRenewCertificate). The node is the caller's
// verified certificate, never a request field; a follower answers with the
// leader's address, where the agent asks again.
func (s *GRPCServer) RenewCertificate(ctx context.Context, req *pb.RenewCertificateRequest) (*pb.RenewCertificateResponse, error) {
	leaf := peerLeaf(ctx)
	if leaf == nil {
		return nil, status.Error(codes.Unauthenticated, "client certificate required for this method")
	}
	if !s.manager.IsLeader() {
		return &pb.RenewCertificateResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: s.manager.GetRaft().LeaderGRPCAddress()}, nil
	}
	cert, bundle, err := s.manager.HandleRenewCertificate(leaf, req.Csr, req.Proof, time.Now())
	if err != nil {
		return &pb.RenewCertificateResponse{Error: err.Error()}, nil
	}
	return &pb.RenewCertificateResponse{Success: true, NodeCert: cert, CaBundle: bundle}, nil
}

// RotateAgentKey hands the calling agent the internal-proxy key an agent key
// rotation issued it, and moves the managers to it once the agent says it
// holds it (HandleRotateAgentKey). The node is the caller's verified
// certificate, never a request field; a follower answers with the leader's
// address, where the agent asks again.
func (s *GRPCServer) RotateAgentKey(ctx context.Context, req *pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
	leaf := peerLeaf(ctx)
	if leaf == nil {
		return nil, status.Error(codes.Unauthenticated, "client certificate required for this method")
	}
	if !s.manager.IsLeader() {
		return &pb.RotateAgentKeyResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: s.manager.GetRaft().LeaderGRPCAddress()}, nil
	}
	key, switched, err := s.manager.HandleRotateAgentKey(leaf, req.Generation, req.Proof, req.Confirm)
	if err != nil {
		return &pb.RotateAgentKeyResponse{Error: err.Error()}, nil
	}
	return &pb.RotateAgentKeyResponse{Success: true, AgentKey: key, Switched: switched}, nil
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

// Heartbeat is a manager's heartbeat to the leader.
func (s *GRPCServer) Heartbeat(stream pb.ClusterService_HeartbeatServer) error {
	return s.serveHeartbeat(stream, true)
}

// AgentHeartbeat is an agent's. It is a method of its own so that a manager
// from before agents never takes an agent's ping for a manager's: it answers
// this one Unimplemented, where its Heartbeat makes every node that pings it a
// Raft voter. An agent is never promoted, so this one does not try.
func (s *GRPCServer) AgentHeartbeat(stream pb.ClusterService_AgentHeartbeatServer) error {
	return s.serveHeartbeat(stream, false)
}

// membershipRecheck is how often an open heartbeat stream checks, between
// pings, that its node is still a member. An agent pings every thirty
// seconds; its removal should reach it in seconds, and a disband waits for
// that.
var membershipRecheck = 2 * time.Second

// serveHeartbeat runs a heartbeat stream; promote says the caller is a
// manager, which its first ping may promote to voter.
func (s *GRPCServer) serveHeartbeat(stream grpc.BidiStreamingServer[pb.HeartbeatPing, pb.HeartbeatPong], promote bool) error {
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
	// Which CA signed the certificate this stream's connection presented —
	// what a CA rotation's leader goes by. A node that moves its certificate
	// to another CA reconnects to show the new one (ApplyClusterCA, the
	// agent's renewal).
	certCA := peerRootPin(stream.Context())

	// Single goroutine for receiving — the runHeartbeatRecvLoop helper also
	// drops out on stream.Context().Done() so it doesn't leak when this
	// outer select exits via timeout/done while the channel buffer is full.
	recvCh := make(chan recvResult, 1)
	go runHeartbeatRecvLoop(stream.Context(), stream.Recv, recvCh)

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	member := time.NewTicker(membershipRecheck)
	defer member.Stop()

	// Counted from its first ping, so that a stream refused at that ping says
	// nothing about a node that is up on another (LiveHealth).
	counted := false
	defer func() {
		if counted {
			s.manager.GetHeartbeat().StreamEnded(peerCN)
		}
	}()

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
			// change to react to. Between pings membershipRecheck does the
			// same. The FSM is read directly: Manager.GetNode also copies the
			// whole state and the health table per call.
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
				JWTGeneration:  result.ping.JwtGeneration,
				CATrustPins:    result.ping.CaTrustPins,
				CertCAPin:      certCA,
				// What a re-rotation of the managers' secret goes by; only a
				// manager's means anything.
				ProxyGeneration: result.ping.ProxyGeneration,
			})
			if !counted {
				counted = true
				s.manager.GetHeartbeat().StreamOpened(peerCN)
			}
			// The ping's time is the sender's clock, in seconds — a manager's
			// and an agent's alike.
			s.manager.noteClockSkew(result.ping.NodeId, result.ping.Timestamp)

			// Two-phase join: a node added as non-voter via HandleJoin gets
			// promoted on the first heartbeat whose Raft applied index shows
			// its Raft has caught up (a node older than that field: on its
			// first). A heartbeat alone says only that gRPC is up and its
			// certs are accepted, not that the leader reaches its Raft port.
			// Idempotent for already-voter nodes; a missing FSM entry just
			// no-ops.
			if promote {
				s.manager.PromoteOnHeartbeatIfPending(result.ping.NodeId, result.ping.RaftAppliedIndex)
			}

			pong := &pb.HeartbeatPong{
				LeaderId:          s.manager.GetRaft().LeaderID(),
				Timestamp:         result.ping.Timestamp,
				LeaderGrpcAddress: s.manager.GetRaft().LeaderGRPCAddress(),
				Managers:          s.manager.managerInfos(),
				FromLeader:        s.manager.IsLeader(),
			}
			if !promote {
				// Agents learn a CA rotation from here; managers from their FSM.
				pong.Ca = s.manager.caHint()
				// And an agent key rotation, as a generation: never a key.
				pong.AgentKey = s.manager.agentKeyHint(peerCN)
			}
			if err := stream.Send(pong); err != nil {
				return err
			}
		case <-member.C:
			if s.manager.raft.GetFSM().GetNode(peerCN) == nil {
				return notMemberError(s.manager, peerCN)
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
	return forwardToLocalPanel(ctx, s.local, &s.clients, req), nil
}

// forwardToLocalPanel runs a proxied API request against this node's own
// panel: the loopback hop behind ProxyRequest, on a manager and on an agent.
// It never fails the call itself — an error is an APIResponse with a JSON
// body (proxyErrorBody), which the sending node relays like any other answer.
func forwardToLocalPanel(ctx context.Context, local LocalPanel, clients *loopbackClients, req *pb.APIRequest) *pb.APIResponse {
	// Build local HTTP request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	localURL := local.URL(req.Path)
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, localURL, body)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       proxyErrorBody("PROXY_ERROR", err.Error()),
		}
	}

	// Copy headers from the inbound gRPC request, but skip the auth
	// credentials: the internal-proxy header is set fresh below from THIS
	// node's secret, and neither it nor Authorization is trusted from the peer.
	//
	// X-SFPanel-Original-User / X-SFPanel-Original-Node are deliberately NOT
	// skipped. They carry the identity the *forwarding* node already
	// authenticated (proxy.go re-sets Original-User from its JWT-validated
	// c.Get("username")), and the loopback handler consumes them for audit
	// attribution. Trust here rests on two facts: on a manager this RPC is
	// open only to managers (a verified cluster-CA client cert, then
	// authorizeCaller's callerManager policy), and every manager already
	// shares the JWT signing secret — a malicious manager could forge any
	// identity by minting a JWT directly, so stripping these headers would
	// buy no security while collapsing every cross-node forwarded action to
	// "admin" in the audit log. Do NOT add them to this skip list. (See
	// cluster/CLAUDE.md "Header copy".) On an agent, a request gets here only
	// once its caller is verified as one of the agent's managers
	// (AgentGRPCServer.authorize), each of which holds the agent's key and
	// could sign any request to it under any name.
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
	client, err := clients.get(local, requiresLongLocalTimeout(req.Path))
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       proxyErrorBody("PROXY_ERROR", "local client: "+err.Error()),
		}
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 502,
			Body:       proxyErrorBody("PROXY_ERROR", err.Error()),
		}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return &pb.APIResponse{
			StatusCode: 500,
			Body:       proxyErrorBody("PROXY_ERROR", "failed to read response"),
		}
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
		}
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
	}
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
