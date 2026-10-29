package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"

	"google.golang.org/grpc/credentials"
)

// A connection is verified once, when it is made, and a CA rotation changes
// what a node trusts without a restart. So each connection this node's
// listeners accept — its gRPC listener, an agent's, and the Raft one — is
// tracked with the CA its client certificate was verified by, and
// ReloadCAPool closes those whose CA it no longer trusts. Left open, a
// connection made under a CA a rotation retires — by whoever kept that CA's
// key, before the rotation began — would stay authorized for as long as
// keepalives hold it, past the rotation meant to end it. A peer that is
// still a member reconnects with the certificate it holds now.
//
// The raw connection is what is tracked and closed: closing it never waits,
// where a TLS close sends an alert first, up to its write deadline.

// trackConn records raw, whose handshake verified chains, as this node's
// until forgetConn. A connection with no verified client certificate is not
// tracked: it reaches only what needs none (PreFlight, Join,
// RegisterAgent). One whose CA this node has stopped trusting since its
// handshake began is refused, as ReloadCAPool would have closed it.
func (t *TLSManager) trackConn(raw net.Conn, chains [][]*x509.Certificate) error {
	if len(chains) == 0 || len(chains[0]) == 0 {
		return nil
	}
	chain := chains[0]
	root := CAPin(chain[len(chain)-1].RawSubjectPublicKeyInfo)
	t.connMu.Lock()
	defer t.connMu.Unlock()
	if !slices.Contains(t.TrustPins(), root) {
		return fmt.Errorf("the CA %s that verified this connection is no longer trusted", root)
	}
	if t.conns == nil {
		t.conns = map[net.Conn]string{}
	}
	t.conns[raw] = root
	return nil
}

// forgetConn stops tracking raw, once it is closed.
func (t *TLSManager) forgetConn(raw net.Conn) {
	t.connMu.Lock()
	delete(t.conns, raw)
	t.connMu.Unlock()
}

// closeUntrustedConns closes every tracked connection a CA outside cas
// verified. Called once ca.crt has been read again: a handshake after that
// verifies against cas, and trackConn refuses one that began before.
func (t *TLSManager) closeUntrustedConns(cas []*x509.Certificate) {
	pins := make([]string, 0, len(cas))
	for _, ca := range cas {
		pins = append(pins, CAPin(ca.RawSubjectPublicKeyInfo))
	}
	var stale []net.Conn
	t.connMu.Lock()
	for raw, root := range t.conns {
		if !slices.Contains(pins, root) {
			stale = append(stale, raw)
			delete(t.conns, raw)
		}
	}
	t.connMu.Unlock()
	for _, raw := range stale {
		_ = raw.Close()
	}
	if len(stale) > 0 {
		slog.Info("closed the connections a CA this node no longer trusts verified", "component", "cluster", "connections", len(stale))
	}
}

// trackingCreds are a gRPC listener's credentials: each connection is
// tracked once its handshake is done. The raw connection gRPC accepted is
// left as it is, which keeps the TCP_USER_TIMEOUT gRPC sets on it.
type trackingCreds struct {
	credentials.TransportCredentials
	tm *TLSManager
}

// serverCreds are the gRPC credentials of a listener built on cfg, one of
// tm's server configs.
func (t *TLSManager) serverCreds(cfg *tls.Config) credentials.TransportCredentials {
	return trackingCreds{TransportCredentials: credentials.NewTLS(cfg), tm: t}
}

func (c trackingCreds) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ServerHandshake(raw)
	if err != nil {
		return nil, nil, err
	}
	var chains [][]*x509.Certificate
	if tlsInfo, ok := info.(credentials.TLSInfo); ok {
		chains = tlsInfo.State.VerifiedChains
	}
	if err := c.tm.trackConn(raw, chains); err != nil {
		_ = raw.Close()
		return nil, nil, err
	}
	return &trackedConn{Conn: conn, raw: raw, tm: c.tm}, info, nil
}

func (c trackingCreds) Clone() credentials.TransportCredentials {
	return trackingCreds{TransportCredentials: c.TransportCredentials.Clone(), tm: c.tm}
}

// trackedConn is a gRPC connection past its handshake, forgotten once
// closed.
type trackedConn struct {
	net.Conn
	raw net.Conn
	tm  *TLSManager
}

func (c *trackedConn) Close() error {
	c.tm.forgetConn(c.raw)
	return c.Conn.Close()
}

// raftConn is a connection the Raft listener accepted. Its handshake runs at
// its first read or write, on the goroutine Raft serves it on, as a
// *tls.Conn's always did — never in Accept, where one peer that stalls would
// hold up every other — and it is tracked from then on.
type raftConn struct {
	*tls.Conn
	tm    *TLSManager
	once  sync.Once
	admit error
}

func (c *raftConn) admitted() error {
	c.once.Do(func() {
		if c.admit = c.Handshake(); c.admit == nil {
			c.admit = c.tm.trackConn(c.NetConn(), c.ConnectionState().VerifiedChains)
		}
	})
	return c.admit
}

func (c *raftConn) Read(b []byte) (int, error) {
	if err := c.admitted(); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

func (c *raftConn) Write(b []byte) (int, error) {
	if err := c.admitted(); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

func (c *raftConn) Close() error {
	c.tm.forgetConn(c.NetConn())
	return c.Conn.Close()
}
