package main

import (
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// reissue-cert names the advertise address, this host's Tailscale addresses
// and every name the certificate it replaces had: a reissue that dropped an
// address another node dials would cut that node off.
func TestReissueCertKeepsEveryNameTheCurrentCertificateHas(t *testing.T) {
	tlsMgr := cluster.NewTLSManager(t.TempDir())
	if err := tlsMgr.InitCA("c"); err != nil {
		t.Fatal(err)
	}
	if got := reissueCertAddresses("192.0.2.11", []string{"100.64.0.10"}, tlsMgr); !slices.Equal(got, []string{"192.0.2.11", "100.64.0.10"}) {
		t.Fatalf("with no certificate yet: %v", got)
	}

	certPEM, keyPEM, err := tlsMgr.IssueNodeCert("node-1", []string{"192.0.2.10", "100.64.0.9", "node-1.example"}, cluster.CertRoleManager)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsMgr.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	got := reissueCertAddresses("192.0.2.11", []string{"100.64.0.10", "100.64.0.9"}, tlsMgr)
	want := []string{"192.0.2.11", "100.64.0.10", "100.64.0.9", "192.0.2.10", "node-1.example"}
	if !slices.Equal(got, want) {
		t.Fatalf("reissueCertAddresses = %v, want %v", got, want)
	}
}
