package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
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
