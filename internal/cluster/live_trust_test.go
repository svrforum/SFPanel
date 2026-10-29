package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// underNextCA gives node nodeID, in role, a cert dir that trusts the CA in
// cur's cert dir and next, and a certificate from next.
func underNextCA(t *testing.T, cur *TLSManager, next *CAPair, nodeID, role string) *TLSManager {
	t.Helper()
	curPEM, err := cur.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	tm := NewTLSManager(filepath.Join(t.TempDir(), "certs"))
	if err := tm.SaveCACert(append(append([]byte{}, curPEM...), next.CertPEM...)); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := IssueNodeCertBy(next, nodeID, []string{"127.0.0.1"}, role)
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	return tm
}

// trustBoth adds next to what tm trusts, on disk and from the next handshake.
func trustBoth(t *testing.T, tm *TLSManager, next *CAPair) {
	t.Helper()
	cur, err := tm.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveCACert(append(append([]byte{}, cur...), next.CertPEM...)); err != nil {
		t.Fatal(err)
	}
	if err := tm.ReloadCAPool(); err != nil {
		t.Fatal(err)
	}
}

func nextCA(t *testing.T) *CAPair {
	t.Helper()
	next, err := GenerateCA("SFPanel CA - test-cluster (next)", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return next
}

// A manager's listener built before the next CA existed takes a peer whose
// certificate the next CA signed once it trusts that CA, without a restart:
// every listener kept the pool it was built with, and rotating the CA meant
// restarting every node at once.
func TestAListenerTakesTheNextCAWithoutARestart(t *testing.T) {
	_, server := setupCertDir(t)
	addr := serveFake(t, server, &preAgentManager{}, nil)
	next := nextCA(t)
	peer := underNextCA(t, server, next, "node-2", CertRoleManager)
	preFlight := func() error {
		c, err := DialNode(addr, peer)
		if err != nil {
			return err
		}
		defer c.Close()
		_, err = c.PreFlight(context.Background(), "")
		return err
	}
	if err := preFlight(); status.Code(err) != codes.Unavailable {
		t.Fatalf("a certificate from a CA the server does not trust yet: %v", err)
	}
	trustBoth(t, server, next)
	if err := preFlight(); status.Code(err) != codes.Unimplemented {
		t.Fatalf("once the server trusts the next CA: %v, want the call to reach it (Unimplemented)", err)
	}
}

// A connection made before the client trusted the next CA reconnects later
// with what the client trusts then: the pool's connections and a heartbeat's
// kept the CAs of the moment they were dialled, and could not reach a server
// that had moved to the next CA.
func TestAConnectionReconnectsWithTheTrustOfTheMoment(t *testing.T) {
	_, client := setupCertDir(t)
	next := nextCA(t)
	server := underNextCA(t, client, next, "node-2", CertRoleManager)
	addr := serveFake(t, server, &preAgentManager{}, nil)
	c, err := DialNode(addr, client)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func() error { _, err := c.PreFlight(context.Background(), ""); return err }
	if err := call(); status.Code(err) != codes.Unavailable {
		t.Fatalf("a server certificate from a CA the client does not trust yet: %v", err)
	}
	trustBoth(t, client, next)
	waitFor(t, "the same connection to reach the server", func() bool { return status.Code(call()) == codes.Unimplemented })
}

// serveRaftTLS accepts on the Raft listener tm builds and writes "ok" to every
// connection whose handshake passes.
func serveRaftTLS(t *testing.T, tm *TLSManager) string {
	t.Helper()
	cfg, err := tm.RaftServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if conn.(*tls.Conn).Handshake() == nil {
					_, _ = conn.Write([]byte("ok"))
				}
			}()
		}
	}()
	return lis.Addr().String()
}

func readOK(conn net.Conn, err error) error {
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = io.ReadFull(conn, make([]byte, 2))
	return err
}

// The Raft listener follows the trust too, and still refuses an agent's
// certificate whichever CA signed it.
func TestTheRaftListenerFollowsTheTrustAndStillRefusesAgents(t *testing.T) {
	_, server := setupCertDir(t)
	addr := serveRaftTLS(t, server)
	next := nextCA(t)
	manager := underNextCA(t, server, next, "node-2", CertRoleManager)
	agent := underNextCA(t, server, next, "agent-1", CertRoleAgent)
	dial := func(tm *TLSManager) error {
		cfg, err := tm.ClientTLSConfig()
		if err != nil {
			return err
		}
		return readOK(tls.Dial("tcp", addr, cfg))
	}
	if err := dial(manager); err == nil || !strings.Contains(err.Error(), "unknown certificate authority") {
		t.Fatalf("before the listener trusts the next CA: %v", err)
	}
	trustBoth(t, server, next)
	if err := dial(manager); err != nil {
		t.Fatalf("a manager under the next CA, once trusted: %v", err)
	}
	if err := dial(agent); err == nil || !strings.Contains(err.Error(), "bad certificate") {
		t.Fatalf("an agent under the next CA: %v", err)
	}
}

// The Raft dialer builds its config for each connection: one built once kept
// the CAs of the moment the transport started.
func TestTheRaftDialerFollowsTheTrust(t *testing.T) {
	_, client := setupCertDir(t)
	next := nextCA(t)
	addr := serveRaftTLS(t, underNextCA(t, client, next, "node-2", CertRoleManager))
	layer := &tlsStreamLayer{client: client.ClientTLSConfig}
	dial := func() error { return readOK(layer.Dial(raft.ServerAddress(addr), time.Second)) }
	if err := dial(); err == nil || !strings.Contains(err.Error(), "certificate signed by unknown authority") {
		t.Fatalf("before the dialer trusts the next CA: %v", err)
	}
	trustBoth(t, client, next)
	if err := dial(); err != nil {
		t.Fatalf("after: %v", err)
	}
}

// A node that has just moved its certificate presents it at the next
// handshake, not a minute later: the leader goes by what each node presents.
func TestReloadNodeCertPresentsTheNewCertificateAtOnce(t *testing.T) {
	_, tm := setupCertDir(t)
	cfg, err := tm.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := cfg.GetCertificate(nil)
	next := nextCA(t)
	trustBoth(t, tm, next)
	if err := tm.ReissueNodeCertBy(next, "node-1", []string{"127.0.0.1"}, CertRoleManager); err != nil {
		t.Fatal(err)
	}
	if err := tm.ReloadNodeCert(); err != nil {
		t.Fatal(err)
	}
	second, _ := cfg.GetCertificate(nil)
	if bytes.Equal(first.Certificate[0], second.Certificate[0]) {
		t.Fatal("the old certificate is still presented")
	}
	if got := tm.ServedCertCAPin(); got != next.Pin() {
		t.Fatalf("served from %q, want the next CA", got)
	}
}

// What the node trusts, in ca.crt's order; a ca.crt that does not parse
// leaves the trust as it was, rather than trusting nothing.
func TestTrustPinsFollowCACrt(t *testing.T) {
	certDir, tm := setupCertDir(t)
	cur, _, err := tm.loadCA()
	if err != nil {
		t.Fatal(err)
	}
	next := nextCA(t)
	trustBoth(t, tm, next)
	pins := tm.TrustPins()
	if len(pins) != 2 || pins[0] != CAPin(cur.RawSubjectPublicKeyInfo) || pins[1] != next.Pin() {
		t.Fatalf("pins %v", pins)
	}
	if err := replaceFile(filepath.Join(certDir, "ca.crt"), []byte("not a certificate")); err != nil {
		t.Fatal(err)
	}
	if err := tm.ReloadCAPool(); err == nil {
		t.Fatal("a ca.crt with no certificate was taken")
	}
	if got := tm.TrustPins(); len(got) != 2 {
		t.Fatalf("a bad ca.crt changed the trust: %v", got)
	}
}

// trustBothPEM adds the CA certPEM to what tm trusts, as trustBoth does.
func trustBothPEM(t *testing.T, tm *TLSManager, certPEM []byte) {
	t.Helper()
	cur, err := tm.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveCACert(append(append([]byte{}, cur...), certPEM...)); err != nil {
		t.Fatal(err)
	}
	if err := tm.ReloadCAPool(); err != nil {
		t.Fatal(err)
	}
}

// A connection is verified once, when it is made. When the trust narrows,
// the Raft listener ends each connection a CA it no longer trusts verified,
// and keeps those under a CA it still trusts: left open, one made under a
// CA a rotation retired — by whoever kept that CA's key — was served until
// it broke, and the Raft transport does not say which member speaks.
func TestNarrowedTrustEndsTheRaftConnectionsItNoLongerVerifies(t *testing.T) {
	_, server := setupCertDir(t)
	layer, err := newTLSStreamLayer(server, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { layer.Close() })
	go func() {
		for {
			conn, err := layer.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	next := nextCA(t)
	trustBoth(t, server, next)
	dial := func(tm *TLSManager) net.Conn {
		cfg, err := tm.ClientTLSConfig()
		if err != nil {
			t.Fatal(err)
		}
		conn, err := tls.Dial("tcp", layer.Addr().String(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	echo := func(conn net.Conn) error {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte("ok")); err != nil {
			return err
		}
		_, err := io.ReadFull(conn, make([]byte, 2))
		return err
	}
	underOld := dial(tlsWithRole(t, server, "node-2", CertRoleManager))
	underNext := dial(underNextCA(t, server, next, "node-3", CertRoleManager))
	for _, conn := range []net.Conn{underOld, underNext} {
		if err := echo(conn); err != nil {
			t.Fatal(err)
		}
	}
	// As at promote: the next CA alone.
	if err := server.SaveCACert(next.CertPEM); err != nil {
		t.Fatal(err)
	}
	if err := server.ReloadCAPool(); err != nil {
		t.Fatal(err)
	}
	if err := echo(underOld); err == nil || os.IsTimeout(err) {
		t.Fatalf("the connection the old CA verified is still served: %v", err)
	}
	if err := echo(underNext); err != nil {
		t.Fatalf("the connection the next CA verified ended too: %v", err)
	}
}

// A handshake that began before the trust narrowed was verified against the
// CAs of that moment, and finishes after ReloadCAPool has closed what it
// knew of: it is refused once it is done, not tracked under the CA dropped.
func TestAHandshakeFromBeforeTheTrustNarrowedIsRefused(t *testing.T) {
	_, tm := setupCertDir(t)
	leaf, err := tlsWithRole(t, tm, "node-2", CertRoleManager).NodeCertLeaf()
	if err != nil {
		t.Fatal(err)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{Roots: mustPool(t, tm), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	if err != nil {
		t.Fatal(err)
	}
	next := nextCA(t)
	if err := tm.SaveCACert(next.CertPEM); err != nil {
		t.Fatal(err)
	}
	if err := tm.ReloadCAPool(); err != nil {
		t.Fatal(err)
	}
	raw, peer := net.Pipe()
	defer peer.Close()
	if err := tm.trackConn(raw, chains); err == nil || !strings.Contains(err.Error(), "is no longer trusted") {
		t.Fatalf("a connection the dropped CA verified: %v", err)
	}
	if len(tm.conns) != 0 {
		t.Fatal("it was tracked")
	}
}

func mustPool(t *testing.T, tm *TLSManager) *x509.CertPool {
	t.Helper()
	pool, err := tm.loadCAPool()
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
