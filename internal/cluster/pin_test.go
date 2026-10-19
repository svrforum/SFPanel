package cluster

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"

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
}
