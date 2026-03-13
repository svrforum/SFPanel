package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// GRPCServer serves the ClusterService.
type GRPCServer struct {
	pb.UnimplementedClusterServiceServer
	manager     *Manager
	server      *grpc.Server
	listener    net.Listener
	localPort   int
	proxySecret string
}

// NewGRPCServer creates and configures the gRPC server with mTLS.
// localPort is the HTTP server port for proxying requests locally.
func NewGRPCServer(mgr *Manager, localPort int) (*GRPCServer, error) {
	tlsConfig, err := mgr.GetTLS().ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load TLS config: %w", err)
	}

	creds := credentials.NewTLS(tlsConfig)
	server := grpc.NewServer(grpc.Creds(creds))

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

	log.Printf("[cluster] gRPC server listening on %s", addr)
	go func() {
		if err := s.server.Serve(lis); err != nil {
			log.Printf("[cluster] gRPC server error: %v", err)
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

	return &pb.JoinResponse{
		Success:     true,
		ClusterName: state.Config["cluster_name"],
		CaCert:      caCert,
		NodeCert:    nodeCert,
		NodeKey:     nodeKey,
		Peers:       pbPeers,
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
	for {
		ping, err := stream.Recv()
		if err != nil {
			return err
		}

		s.manager.GetHeartbeat().RecordHeartbeat(&NodeMetrics{
			NodeID:         ping.NodeId,
			CPUPercent:     ping.CpuPercent,
			MemoryPercent:  ping.MemoryPercent,
			DiskPercent:    ping.DiskPercent,
			ContainerCount: int(ping.ContainerCount),
			Timestamp:      ping.Timestamp,
		})

		if err := stream.Send(&pb.HeartbeatPong{
			LeaderId:  s.manager.GetRaft().LeaderID(),
			Timestamp: ping.Timestamp,
		}); err != nil {
			return err
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

	// Copy headers
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Use internal proxy authentication (bypasses JWT validation)
	if s.proxySecret != "" {
		httpReq.Header.Set("X-SFPanel-Internal-Proxy", s.proxySecret)
	} else if req.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.AuthToken)
	}

	// Execute locally
	client := &http.Client{Timeout: 25 * time.Second}
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

// Subscribe sends cluster events to the client (Phase 5 stub).
func (s *GRPCServer) Subscribe(req *pb.SubscribeRequest, stream pb.ClusterService_SubscribeServer) error {
	<-stream.Context().Done()
	return nil
}
