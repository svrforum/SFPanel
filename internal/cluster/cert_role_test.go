package cluster

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
)

func parseLeaf(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func TestIssueNodeCertCarriesRole(t *testing.T) {
	_, mgr := setupCertDir(t)
	for _, role := range []string{CertRoleManager, CertRoleAgent} {
		certPEM, _, err := mgr.IssueNodeCert("node-x", []string{"127.0.0.1"}, role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if got := CertRole(parseLeaf(t, certPEM)); got != role {
			t.Errorf("CertRole = %q, want %q", got, role)
		}
	}
	if _, _, err := mgr.IssueNodeCert("node-x", []string{"127.0.0.1"}, "admin"); err == nil {
		t.Fatal("IssueNodeCert accepted a role that is neither manager nor agent")
	}
}

// The Raft transport does not authenticate which member speaks, so a
// certificate the cluster CA signed for an agent must not get through its
// handshake: an agent there could claim leadership with a higher term.
func TestRaftTLSRefusesAgentCertificates(t *testing.T) {
	_, mgr := setupCertDir(t)
	cfg, err := mgr.RaftServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("RaftServerTLSConfig has no role check")
	}
	for role, wantErr := range map[string]bool{CertRoleAgent: true, CertRoleManager: false} {
		certPEM, _, err := mgr.IssueNodeCert("peer", []string{"127.0.0.1"}, role)
		if err != nil {
			t.Fatal(err)
		}
		err = cfg.VerifyPeerCertificate(nil, [][]*x509.Certificate{{parseLeaf(t, certPEM)}})
		if (err != nil) != wantErr {
			t.Errorf("%s certificate: err = %v, want error %v", role, err, wantErr)
		}
	}
	// A manager certificate issued before roles existed carries no OU.
	if err := cfg.VerifyPeerCertificate(nil, [][]*x509.Certificate{{{}}}); err != nil {
		t.Errorf("certificate without a role refused: %v", err)
	}
}

// A certificate that names both roles is held to the lesser one.
func TestCertRoleWithBothRolesIsAnAgent(t *testing.T) {
	for _, ous := range [][]string{{CertRoleManager, CertRoleAgent}, {CertRoleAgent, CertRoleManager}} {
		cert := &x509.Certificate{Subject: pkix.Name{OrganizationalUnit: ous}}
		if got := CertRole(cert); got != CertRoleAgent {
			t.Errorf("CertRole(%v) = %q, want %q", ous, got, CertRoleAgent)
		}
	}
}
