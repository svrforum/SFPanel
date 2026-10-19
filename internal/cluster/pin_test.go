package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

func caSPKI(t *testing.T, mgr *TLSManager) []byte {
	t.Helper()
	caPEM, err := mgr.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(caPEM)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return ca.RawSubjectPublicKeyInfo
}

func derOf(t *testing.T, certPEM []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block")
	}
	return block.Bytes
}

func TestNormalizeCAPin(t *testing.T) {
	want := "sha256:" + strings.Repeat("ab", 32)
	for _, in := range []string{want, strings.ToUpper(want), strings.Repeat("ab", 32), "  " + want + "\n"} {
		got, err := NormalizeCAPin(in)
		if err != nil || got != want {
			t.Errorf("NormalizeCAPin(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "sha256:abc", "sha256:" + strings.Repeat("zz", 32)} {
		if _, err := NormalizeCAPin(bad); err == nil {
			t.Errorf("NormalizeCAPin(%q) accepted", bad)
		}
	}
}

// The joiner trusts exactly the pinned CA key: a leaf it signed passes,
// whatever address the leaf names, and a leaf from any other CA does not.
func TestPinnedTLSConfig(t *testing.T) {
	_, ca1 := setupCertDir(t)
	_, ca2 := setupCertDir(t)
	cfg, err := pinnedTLSConfig(caSPKI(t, ca1))
	if err != nil {
		t.Fatal(err)
	}
	// Issued for an address other than the one the joiner dials.
	leaf1, _, err := ca1.IssueNodeCert("leader", []string{"203.0.113.9"}, CertRoleManager)
	if err != nil {
		t.Fatal(err)
	}
	leaf2, _, err := ca2.IssueNodeCert("leader", []string{"203.0.113.9"}, CertRoleManager)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{derOf(t, leaf1)}, nil); err != nil {
		t.Fatalf("leaf of the pinned CA refused: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{derOf(t, leaf2)}, nil); err == nil {
		t.Fatal("leaf of another CA accepted")
	}
}

// An agent's certificate is signed by the same CA, so the pin alone would let
// an agent pass for the leader a joiner was sent to. A manager's certificate
// from before roles carries none and still passes.
func TestPinnedTLSRefusesAnAgentLeaf(t *testing.T) {
	_, ca := setupCertDir(t)
	cfg, err := pinnedTLSConfig(caSPKI(t, ca))
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := ca.IssueNodeCert("some-agent", []string{"203.0.113.9"}, CertRoleAgent)
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.VerifyPeerCertificate([][]byte{derOf(t, agent)}, nil)
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("agent leaf as the leader: %v", err)
	}
	legacy, _ := issueLegacyCert(t, ca, "leader", "203.0.113.9", "leader")
	if err := cfg.VerifyPeerCertificate([][]byte{derOf(t, legacy)}, nil); err != nil {
		t.Fatalf("manager leaf from before roles refused: %v", err)
	}
}

// A leaf the pinned CA signed but whose validity has ended is refused.
func TestPinnedTLSRefusesAnExpiredLeaf(t *testing.T) {
	_, ca := setupCertDir(t)
	cfg, err := pinnedTLSConfig(caSPKI(t, ca))
	if err != nil {
		t.Fatal(err)
	}
	caCert, caKey, err := ca.loadCA()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(43),
		Subject:      pkix.Name{CommonName: "leader", OrganizationalUnit: []string{CertRoleManager}},
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     time.Now().Add(-time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.VerifyPeerCertificate([][]byte{der}, nil)
	if err == nil || !strings.Contains(err.Error(), "validity") {
		t.Fatalf("expired leaf: %v", err)
	}
}

// Until the replicated proxy secret takes over, the proxy secret is
// sha256 over the CA certificate PEM. A pin over the certificate would print
// it on every token page; the pin is over the public key.
func TestCAPinIsOverThePublicKey(t *testing.T) {
	_, ca := setupCertDir(t)
	caPEM, _ := ca.LoadCACert()
	sum := sha256.Sum256(caPEM)
	pin := CAPin(caSPKI(t, ca))
	if strings.TrimPrefix(pin, "sha256:") == hex.EncodeToString(sum[:]) {
		t.Fatal("the pin is the certificate hash, which is the legacy proxy secret")
	}
	spkiSum := sha256.Sum256(caSPKI(t, ca))
	if pin != "sha256:"+hex.EncodeToString(spkiSum[:]) {
		t.Fatalf("pin = %s", pin)
	}
}

func TestCheckPreFlightPin(t *testing.T) {
	_, ca := setupCertDir(t)
	spki := caSPKI(t, ca)
	pin := CAPin(spki)
	if err := checkPreFlightPin(&pb.PreFlightResponse{Valid: true, CaPublicKey: spki}, pin, "198.51.100.1:3629"); err != nil {
		t.Fatalf("matching pin refused: %v", err)
	}
	err := checkPreFlightPin(&pb.PreFlightResponse{Valid: true, CaPublicKey: spki}, "sha256:"+strings.Repeat("00", 32), "198.51.100.1:3629")
	if err == nil || !strings.Contains(err.Error(), "fingerprint does not match") {
		t.Fatalf("other pin: %v", err)
	}
	err = checkPreFlightPin(&pb.PreFlightResponse{Valid: true}, pin, "198.51.100.1:3629")
	if err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("leader without a CA key: %v", err)
	}
	// The key is now asked for with no token, so the CA-unavailable answer
	// arrives here instead of as a token error, and must not read as an
	// older leader.
	err = checkPreFlightPin(&pb.PreFlightResponse{Error: errCAUnavailable}, pin, "198.51.100.1:3629")
	if err == nil || !strings.Contains(err.Error(), "cannot read its cluster CA") {
		t.Fatalf("leader that cannot read its CA: %v", err)
	}
}
