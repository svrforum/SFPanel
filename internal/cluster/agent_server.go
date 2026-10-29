package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// AgentGRPCServer is an agent's gRPC listener. Managers reach the agent's
// panel through it; nothing else is served, and only to the managers the
// agent knows.
type AgentGRPCServer struct {
	pb.UnimplementedClusterServiceServer
	local      LocalPanel
	clients    loopbackClients
	managerIDs func() []string
	server     *grpc.Server
	listener   net.Listener
}

// NewAgentGRPCServer builds the listener. managerIDs is asked on every call,
// so a manager the agent stops listing is refused from its next request on.
func NewAgentGRPCServer(tlsMgr *TLSManager, local LocalPanel, managerIDs func() []string) (*AgentGRPCServer, error) {
	cfg, err := tlsMgr.AgentServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load TLS config: %w", err)
	}

	s := &AgentGRPCServer{local: local, managerIDs: managerIDs}
	s.server = grpc.NewServer(
		grpc.Creds(credentials.NewTLS(cfg)),
		grpc.UnaryInterceptor(s.authorizeUnary),
		grpc.StreamInterceptor(s.authorizeStream),
		// The policy of a manager's listener (NewGRPCServer): gRPC's default
		// answers the keepalive pings a manager's client sends with GOAWAY.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             clusterKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterClusterServiceServer(s.server, s)
	return s, nil
}

// authorize admits ProxyRequest from the agent's managers and nothing else.
// The cluster CA signs agents' certificates too, so the role is checked as
// well as the name; the name keeps out a manager the agent no longer lists —
// a removed manager's certificate stays valid for years.
func (s *AgentGRPCServer) authorize(ctx context.Context, method string) error {
	leaf := peerLeaf(ctx)
	if leaf == nil {
		return status.Error(codes.Unauthenticated, "client certificate required")
	}
	if method != "/sfpanel.cluster.ClusterService/ProxyRequest" {
		return status.Errorf(codes.PermissionDenied, "an agent serves only ProxyRequest, not %s", method)
	}
	if CertRole(leaf) != CertRoleManager {
		return status.Errorf(codes.PermissionDenied, "%s is not a manager", leaf.Subject.CommonName)
	}
	if !slices.Contains(s.managerIDs(), leaf.Subject.CommonName) {
		return status.Errorf(codes.PermissionDenied, "%s is not one of this agent's managers", leaf.Subject.CommonName)
	}
	return nil
}

func (s *AgentGRPCServer) authorizeUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := s.authorize(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func (s *AgentGRPCServer) authorizeStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := s.authorize(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
}

// ProxyRequest runs a manager's request against this agent's panel. The
// loopback hop is signed with the secret this process installed, never with
// anything the manager sent (forwardToLocalPanel).
func (s *AgentGRPCServer) ProxyRequest(ctx context.Context, req *pb.APIRequest) (*pb.APIResponse, error) {
	return forwardToLocalPanel(ctx, s.local, &s.clients, req), nil
}

// Start listens on addr and serves in the background.
func (s *AgentGRPCServer) Start(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = lis

	slog.Info("agent gRPC server listening", "component", "agent", "addr", lis.Addr().String())
	go func() {
		if err := s.server.Serve(lis); err != nil {
			slog.Error("agent gRPC server error", "component", "agent", "error", err)
		}
	}()
	return nil
}

// Addr is the address the server listens on, "" before Start.
func (s *AgentGRPCServer) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop stops the server once the calls in flight have finished. It is a no-op
// on a nil server.
func (s *AgentGRPCServer) Stop() {
	if s == nil || s.server == nil {
		return
	}
	s.server.GracefulStop()
}

// DialManager connects an agent to one of its managers. A certificate that
// chains to the cluster CA is not enough: the CA signs agents too, so the
// server must hold a manager's certificate under a name the agent lists, or an
// agent at that address could pose as this agent's manager.
func DialManager(address string, tlsMgr *TLSManager, managerIDs func() []string) (*GRPCClient, error) {
	build := func() (*tls.Config, error) {
		cfg, err := tlsMgr.ClientTLSConfig()
		if err != nil {
			return nil, err
		}
		cfg = cfg.Clone()
		// Called on every handshake, after the chain has been verified against
		// the cluster CA (RootCAs).
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the server presented no certificate")
			}
			leaf := cs.PeerCertificates[0]
			if CertRole(leaf) != CertRoleManager {
				return fmt.Errorf("%s is not a manager", leaf.Subject.CommonName)
			}
			if !slices.Contains(managerIDs(), leaf.Subject.CommonName) {
				return fmt.Errorf("%s is not one of this agent's managers", leaf.Subject.CommonName)
			}
			return nil
		}
		return cfg, nil
	}
	creds, err := liveClientCreds(build)
	if err != nil {
		return nil, fmt.Errorf("client TLS: %w", err)
	}

	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(creds),
		clusterKeepaliveDialOption(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}
	return &GRPCClient{conn: conn, client: pb.NewClusterServiceClient(conn)}, nil
}
