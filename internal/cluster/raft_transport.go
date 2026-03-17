package cluster

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/hashicorp/raft"
)

// tlsStreamLayer wraps a TCP listener with TLS for encrypted Raft transport.
type tlsStreamLayer struct {
	listener net.Listener
	addr     net.Addr
	tlsConf  *tls.Config
}

func (t *tlsStreamLayer) Accept() (net.Conn, error) {
	return t.listener.Accept()
}

func (t *tlsStreamLayer) Close() error {
	return t.listener.Close()
}

func (t *tlsStreamLayer) Addr() net.Addr {
	return t.addr
}

func (t *tlsStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", string(address), t.tlsConf)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// newRaftTransport creates a Raft transport. If TLS is configured, the transport
// uses TLS-encrypted connections. Otherwise, plain TCP is used.
func newRaftTransport(cfg RaftConfig, addr *net.TCPAddr) (raft.Transport, error) {
	if cfg.TLS == nil || !cfg.RaftTLS {
		t, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
		if err != nil {
			return nil, fmt.Errorf("create transport: %w", err)
		}
		return t, nil
	}

	serverTLS, err := cfg.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("raft server TLS: %w", err)
	}

	clientTLS, err := cfg.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("raft client TLS: %w", err)
	}

	// Create TLS listener
	listener, err := tls.Listen("tcp", cfg.BindAddr, serverTLS)
	if err != nil {
		return nil, fmt.Errorf("raft TLS listen: %w", err)
	}

	stream := &tlsStreamLayer{
		listener: listener,
		addr:     listener.Addr(),
		tlsConf:  clientTLS,
	}

	transport := raft.NewNetworkTransport(stream, 3, 10*time.Second, os.Stderr)
	log.Println("[cluster] Raft transport using TLS encryption")
	return transport, nil
}
