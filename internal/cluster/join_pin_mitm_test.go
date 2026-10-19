package cluster

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type stubPanel struct{}

func (stubPanel) URL(p string) string                            { return "http://127.0.0.1:1" + p }
func (stubPanel) HTTPClient(time.Duration) (*http.Client, error) { return &http.Client{}, nil }

// freePortPair finds a port whose successor is free too (gRPC and Raft).
func freePortPair(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		l1, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		p := l1.Addr().(*net.TCPAddr).Port
		l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+1))
		l1.Close()
		if err != nil {
			continue
		}
		l2.Close()
		return p
	}
	t.Fatal("no free port pair")
	return 0
}

func selfSignedServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "mitm"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// relayingMITM sits where the operator thinks the leader is, holding no key
// the cluster CA signed. It relays PreFlight to the real leader, so the real
// CA key comes back and the pin matches, and it uses any token it reads to
// join the real cluster itself.
type relayingMITM struct {
	pb.UnimplementedClusterServiceServer
	realAddr string
	mu       sync.Mutex
	tokens   []string
	stolen   *pb.JoinResponse
}

func (m *relayingMITM) PreFlight(ctx context.Context, req *pb.PreFlightRequest) (*pb.PreFlightResponse, error) {
	m.mu.Lock()
	m.tokens = append(m.tokens, req.Token)
	m.mu.Unlock()
	up, err := DialNodeInsecure(m.realAddr)
	if err != nil {
		return nil, err
	}
	defer up.Close()
	resp, err := up.PreFlight(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	if resp.Valid {
		jr, jerr := up.Join(context.Background(), &pb.JoinRequest{
			Token: req.Token, NodeId: "attacker-node", NodeName: "attacker",
			ApiAddress: "127.0.0.1:9", GrpcAddress: "127.0.0.1:8",
		})
		if jerr == nil {
			m.mu.Lock()
			m.stolen = jr
			m.mu.Unlock()
		}
	}
	return resp, nil
}

// realLeader starts a one-manager cluster with its gRPC server and returns
// its address, a join token and the pin its token page would print.
func realLeader(t *testing.T) (*Manager, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	port := freePortPair(t)
	mgr := NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: port, APIPort: 1,
	})
	if err := mgr.Init("victim"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	gs, err := NewGRPCServer(mgr, 1, stubPanel{})
	if err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := gs.Start(addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gs.Stop)
	tok, err := mgr.CreateJoinToken(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := mgr.CAPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return mgr, addr, tok.Token, CAPin(caKey)
}

// The pin is worthless if the token crosses the unverified connection: an
// on-path attacker reads it, relays PreFlight so the real CA key comes back,
// and joins with it first. The token must travel only over the connection
// verified against the pinned key.
func TestJoinSendsTheTokenOnlyToThePinnedLeader(t *testing.T) {
	mgr, realAddr, token, pin := realLeader(t)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &relayingMITM{realAddr: realAddr}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{selfSignedServerCert(t)}, MinVersion: tls.VersionTLS13,
	})))
	pb.RegisterClusterServiceServer(srv, m)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	e := &JoinEngine{Config: &config.Config{}}
	e.Config.Cluster.GRPCPort = freePortPair(t)
	e.Config.Cluster.CertDir = filepath.Join(t.TempDir(), "certs")
	if _, err := e.PreFlight(lis.Addr().String(), token, pin); err == nil || !strings.Contains(err.Error(), "not signed by the pinned cluster CA") {
		t.Fatalf("PreFlight through the attacker: %v", err)
	}
	if _, err := e.Execute(lis.Addr().String(), token, "127.0.0.1", pin); err == nil || !strings.Contains(err.Error(), "not signed by the pinned cluster CA") {
		t.Fatalf("Execute through the attacker: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, seen := range m.tokens {
		if seen != "" {
			t.Fatalf("the unverified connection carried the token %q", seen)
		}
	}
	if m.stolen != nil {
		t.Fatalf("the attacker joined the real cluster: success=%v", m.stolen.Success)
	}
	if n := mgr.GetRaft().GetFSM().GetNode("attacker-node"); n != nil {
		t.Fatalf("the leader lists the attacker: %+v", *n)
	}
}

// With the real leader at the address, the same command joins.
func TestJoinThroughThePinnedLeaderSucceeds(t *testing.T) {
	_, realAddr, token, pin := realLeader(t)
	e := &JoinEngine{Config: &config.Config{}}
	e.Config.Cluster.GRPCPort = freePortPair(t)
	e.Config.Cluster.CertDir = filepath.Join(t.TempDir(), "certs")
	if _, err := e.PreFlight(realAddr, token, pin); err != nil {
		t.Fatalf("PreFlight: %v", err)
	}
	res, err := e.Execute(realAddr, token, "127.0.0.1", pin)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ClusterName != "victim" {
		t.Fatalf("joined %q", res.ClusterName)
	}
}
