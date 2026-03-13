package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// GRPCClient connects to a remote node's gRPC server.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client pb.ClusterServiceClient
}

// DialNode connects to a peer node with mTLS.
func DialNode(address string, tlsMgr *TLSManager) (*GRPCClient, error) {
	tlsConfig, err := tlsMgr.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("client TLS: %w", err)
	}

	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}

	return &GRPCClient{
		conn:   conn,
		client: pb.NewClusterServiceClient(conn),
	}, nil
}

// DialNodeInsecure connects with TLS but skips server cert verification and
// does not present a client certificate. Used during initial join before certs are available.
func DialNodeInsecure(address string) (*GRPCClient, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
	}
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}

	return &GRPCClient{
		conn:   conn,
		client: pb.NewClusterServiceClient(conn),
	}, nil
}

// Join sends a join request to the leader.
func (c *GRPCClient) Join(ctx context.Context, req *pb.JoinRequest) (*pb.JoinResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.client.Join(ctx, req)
}

// Leave notifies the leader that this node is leaving.
func (c *GRPCClient) Leave(ctx context.Context, nodeID string) (*pb.LeaveResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.client.Leave(ctx, &pb.LeaveRequest{NodeId: nodeID})
}

// GetMetrics requests metrics for a specific node.
func (c *GRPCClient) GetMetrics(ctx context.Context, nodeID string) (*pb.MetricsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.client.GetMetrics(ctx, &pb.MetricsRequest{NodeId: nodeID})
}

// ProxyRequest forwards an API request to the remote node.
func (c *GRPCClient) ProxyRequest(ctx context.Context, req *pb.APIRequest) (*pb.APIResponse, error) {
	return c.client.ProxyRequest(ctx, req)
}

// Close releases the gRPC connection.
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
