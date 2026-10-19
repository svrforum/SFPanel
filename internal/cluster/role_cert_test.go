package cluster

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// issueLegacyCert signs a node certificate the way v0.78 did: no OU.
func issueLegacyCert(t *testing.T, tlsMgr *TLSManager, nodeID string, ip, dns string) (certPEM, keyPEM []byte) {
	t.Helper()
	caCert, caKey, err := tlsMgr.loadCA()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{Organization: []string{"SFPanel Cluster"}, CommonName: nodeID},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
		DNSNames:     []string{dns},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// A manager whose certificate predates roles reissues it with the manager
// role, keeping its identity and the addresses it was valid for; a second
// pass leaves the new certificate alone.
func TestEnsureRoleCertGivesALegacyCertificateTheManagerRole(t *testing.T) {
	_, tlsMgr := setupCertDir(t)
	certPEM, keyPEM := issueLegacyCert(t, tlsMgr, "node-1", "10.0.0.5", "node-1.example")
	if err := tlsMgr.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	m := &Manager{tls: tlsMgr, nodeID: "node-1", raft: &RaftNode{fsm: NewFSM()}}
	if err := m.EnsureRoleCert(); err != nil {
		t.Fatal(err)
	}
	leaf, err := tlsMgr.NodeCertLeaf()
	if err != nil {
		t.Fatal(err)
	}
	if CertRole(leaf) != CertRoleManager || leaf.Subject.CommonName != "node-1" {
		t.Fatalf("reissued cert: role %q, CN %q", CertRole(leaf), leaf.Subject.CommonName)
	}
	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "10.0.0.5" || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "node-1.example" {
		t.Fatalf("reissued cert lost its addresses: %v %v", leaf.IPAddresses, leaf.DNSNames)
	}
	if err := m.EnsureRoleCert(); err != nil {
		t.Fatal(err)
	}
	again, _ := tlsMgr.NodeCertLeaf()
	if again.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Fatal("EnsureRoleCert reissued a certificate that already had a role")
	}
}

// The reissue re-signs the key the node already has and replaces node.crt in
// one rename. A new key written beside the new certificate left a window — a
// crash between the two writes, or the one-minute reload landing between
// them — in which certificate and key did not match and every handshake
// failed.
func TestEnsureRoleCertKeepsTheKey(t *testing.T) {
	certDir, tlsMgr := setupCertDir(t)
	certPEM, keyPEM := issueLegacyCert(t, tlsMgr, "node-1", "203.0.113.5", "node-1.example")
	if err := tlsMgr.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	m := &Manager{tls: tlsMgr, nodeID: "node-1", raft: &RaftNode{fsm: NewFSM()}}
	if err := m.EnsureRoleCert(); err != nil {
		t.Fatal(err)
	}
	keyAfter, err := os.ReadFile(filepath.Join(certDir, "node.key"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keyAfter, keyPEM) {
		t.Fatal("EnsureRoleCert replaced node.key")
	}
	certAfter, err := os.ReadFile(filepath.Join(certDir, "node.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(certAfter, keyAfter); err != nil {
		t.Fatalf("the reissued certificate does not match the key: %v", err)
	}
	if CertRole(parseLeaf(t, certAfter)) != CertRoleManager {
		t.Fatal("the reissued certificate has no manager role")
	}
	entries, err := os.ReadDir(certDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("left %s behind", e.Name())
		}
	}
}
