package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"
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

// PreFlight checks token validity without consuming it.
func (c *GRPCClient) PreFlight(ctx context.Context, token string) (*pb.PreFlightResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.client.PreFlight(ctx, &pb.PreFlightRequest{Token: token})
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

// ConnPool manages a pool of reusable gRPC connections to cluster nodes.
type ConnPool struct {
	mu     sync.RWMutex
	conns  map[string]*poolEntry
	tls    *TLSManager
	stopCh chan struct{}
}

type poolEntry struct {
	client  *GRPCClient
	created time.Time
}

const connMaxAge = 5 * time.Minute

// NewConnPool creates a connection pool.
func NewConnPool(tlsMgr *TLSManager) *ConnPool {
	pool := &ConnPool{
		conns:  make(map[string]*poolEntry),
		tls:    tlsMgr,
		stopCh: make(chan struct{}),
	}
	// Background cleanup of stale connections
	go pool.cleanup()
	return pool
}

// Get returns a cached or new connection to the given address.
func (p *ConnPool) Get(address string) (*GRPCClient, error) {
	p.mu.RLock()
	if entry, ok := p.conns[address]; ok && time.Since(entry.created) < connMaxAge {
		p.mu.RUnlock()
		return entry.client, nil
	}
	p.mu.RUnlock()

	// Create new connection
	client, err := DialNode(address, p.tls)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// Close old connection if exists
	if old, ok := p.conns[address]; ok {
		old.client.Close()
	}
	p.conns[address] = &poolEntry{client: client, created: time.Now()}
	p.mu.Unlock()

	return client, nil
}

// Remove closes and removes a specific connection.
func (p *ConnPool) Remove(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.conns[address]; ok {
		entry.client.Close()
		delete(p.conns, address)
	}
}

// Close closes all pooled connections and stops the cleanup goroutine.
func (p *ConnPool) Close() {
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for addr, entry := range p.conns {
		entry.client.Close()
		delete(p.conns, addr)
	}
}

func (p *ConnPool) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		for addr, entry := range p.conns {
			if time.Since(entry.created) > connMaxAge {
				entry.client.Close()
				delete(p.conns, addr)
				slog.Debug("pool: closed stale conn", "component", "cluster", "addr", addr)
			}
		}
		p.mu.Unlock()
	}
}
