package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// GRPCClient connects to a remote node's gRPC server.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client pb.ClusterServiceClient
}

// clusterKeepaliveDialOption arms HTTP/2 keepalive so a connection that died
// without the process noticing fails fast instead of swallowing sends — see
// the constants in types.go.
//
// PermitWithoutStream stays false deliberately. Pinging idle connections
// would buy little (the pool re-dials on use, and an RPC on a dead connection
// fails on its own deadline) while making a rolling upgrade noisier: a peer
// still on the previous release runs gRPC's default enforcement policy, which
// answers pings on stream-less connections with GOAWAY. Connections carrying
// the heartbeat stream — the ones that matter — always have an active stream,
// so they are pinged either way.
func clusterKeepaliveDialOption() grpc.DialOption {
	return grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:    clusterKeepaliveTime,
		Timeout: clusterKeepaliveTimeout,
	})
}

// liveTLS is gRPC transport credentials whose client TLS config is built
// anew for each handshake from what this node trusts then. A ClientConn keeps
// its credentials for life and reconnects with them — the pool's, kept while
// in use, and a heartbeat's — so a config built once went on trusting what
// the node trusted when it first dialled, and could not reach a peer whose
// certificate a rotated CA signed.
type liveTLS struct {
	credentials.TransportCredentials // Info, ServerHandshake, OverrideServerName
	build                            func() (*tls.Config, error)
}

// liveClientCreds builds the config once, so that an error in it comes at
// the dial, and then again for every handshake.
func liveClientCreds(build func() (*tls.Config, error)) (credentials.TransportCredentials, error) {
	cfg, err := build()
	if err != nil {
		return nil, err
	}
	return &liveTLS{TransportCredentials: credentials.NewTLS(cfg), build: build}, nil
}

func (c *liveTLS) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	cfg, err := c.build()
	if err != nil {
		return nil, nil, err
	}
	return credentials.NewTLS(cfg).ClientHandshake(ctx, authority, raw)
}

func (c *liveTLS) Clone() credentials.TransportCredentials {
	return &liveTLS{TransportCredentials: c.TransportCredentials.Clone(), build: c.build}
}

// DialNode connects to a peer node with mTLS.
func DialNode(address string, tlsMgr *TLSManager) (*GRPCClient, error) {
	creds, err := liveClientCreds(tlsMgr.ClientTLSConfig)
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
		clusterKeepaliveDialOption(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}

	return &GRPCClient{
		conn:   conn,
		client: pb.NewClusterServiceClient(conn),
	}, nil
}

// DialNodePinned connects to a leader whose certificate must be signed by the
// pinned cluster CA key (pinnedTLSConfig). Used for Join once PreFlight has
// shown the leader holds the CA the operator was told to trust.
func DialNodePinned(address string, caPublicKey []byte) (*GRPCClient, error) {
	tlsCfg, err := pinnedTLSConfig(caPublicKey)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		clusterKeepaliveDialOption(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}
	return &GRPCClient{conn: conn, client: pb.NewClusterServiceClient(conn)}, nil
}

// DialPinnedLeader connects to the leader over a connection verified against
// the CA fingerprint pin, in any form NormalizeCAPin accepts (see
// dialPinnedLeader). An agent joins with it: until the leader answers, the
// pin is all the agent knows of the cluster.
func DialPinnedLeader(leaderAddr, pin string) (*GRPCClient, error) {
	pin, err := NormalizeCAPin(pin)
	if err != nil {
		return nil, err
	}
	return dialPinnedLeader(leaderAddr, pin)
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

// RegisterAgent sends an agent's registration to the leader.
func (c *GRPCClient) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.client.RegisterAgent(ctx, req)
}

// ReportPanelCA sends this node's panel CA to the leader.
func (c *GRPCClient) ReportPanelCA(ctx context.Context, req *pb.ReportPanelCARequest) (*pb.ReportPanelCAResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.client.ReportPanelCA(ctx, req)
}

// OpenAgentHeartbeat opens an agent's heartbeat stream to a manager, which
// lasts until ctx ends or the stream fails. Its pongs must be read: unread,
// they fill the stream's flow-control window and both ends wedge (see the
// drain goroutine in StartLocalMetrics).
func (c *GRPCClient) OpenAgentHeartbeat(ctx context.Context) (pb.ClusterService_AgentHeartbeatClient, error) {
	return c.client.AgentHeartbeat(ctx)
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
	client *GRPCClient
	// lastUsed is the UnixNano of the most recent Get. Expiry is idle-based so
	// a continuously-used healthy connection isn't torn down and re-dialed
	// (re-incurring the mTLS handshake) every connIdleTimeout. gRPC's transport
	// already heals broken connections, so age-based rotation was pure churn.
	lastUsed atomic.Int64
}

const connIdleTimeout = 5 * time.Minute

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
	if entry, ok := p.conns[address]; ok && time.Since(time.Unix(0, entry.lastUsed.Load())) < connIdleTimeout {
		entry.lastUsed.Store(time.Now().UnixNano())
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
	entry := &poolEntry{client: client}
	entry.lastUsed.Store(time.Now().UnixNano())
	p.conns[address] = entry
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
			if time.Since(time.Unix(0, entry.lastUsed.Load())) > connIdleTimeout {
				entry.client.Close()
				delete(p.conns, addr)
				slog.Debug("pool: closed idle conn", "component", "cluster", "addr", addr)
			}
		}
		p.mu.Unlock()
	}
}
