package cluster

import (
	"bytes"
	"crypto/x509"
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

// addressCertManager is a manager for node-1 advertising advertise, with
// tailscale on its interfaces and a certificate on disk naming sans.
func addressCertManager(t *testing.T, advertise string, tailscale []string, sans ...string) (*Manager, *TLSManager) {
	t.Helper()
	_, tlsMgr := setupCertDir(t)
	certPEM, keyPEM, err := tlsMgr.IssueNodeCert("node-1", sans, CertRoleManager)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsMgr.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	ifaces := &fakeIfaces{}
	ifaces.set(tailscale...)
	m := &Manager{
		tls:            tlsMgr,
		nodeID:         "node-1",
		config:         &config.ClusterConfig{AdvertiseAddress: advertise},
		raft:           &RaftNode{fsm: NewFSM()},
		listIfaceAddrs: ifaces.list,
	}
	return m, tlsMgr
}

func leafIPs(leaf *x509.Certificate) []string {
	var out []string
	for _, ip := range leaf.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

func mustLeaf(t *testing.T, tlsMgr *TLSManager) *x509.Certificate {
	t.Helper()
	leaf, err := tlsMgr.NodeCertLeaf()
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

// A manager whose certificate names only its LAN address is reissued for its
// Tailscale address too, on the key it has, so a server that reaches it over
// Tailscale can verify it.
func TestAManagerCertificateGainsItsTailscaleAddress(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "192.0.2.10", []string{"100.64.0.10"}, "192.0.2.10")
	before := mustLeaf(t, tlsMgr)

	changed, err := m.EnsureAddressCert()
	if err != nil || !changed {
		t.Fatalf("EnsureAddressCert = %v, %v; want true, nil", changed, err)
	}
	after := mustLeaf(t, tlsMgr)
	if ips := leafIPs(after); !slices.Contains(ips, "192.0.2.10") || !slices.Contains(ips, "100.64.0.10") {
		t.Fatalf("reissued certificate names %v", ips)
	}
	if !bytes.Equal(after.RawSubjectPublicKeyInfo, before.RawSubjectPublicKeyInfo) {
		t.Fatal("the reissued certificate is for another key")
	}
	if CertRole(after) != CertRoleManager || after.Subject.CommonName != "node-1" {
		t.Fatalf("reissued certificate: role %q, CN %q", CertRole(after), after.Subject.CommonName)
	}
	// Presented at once, not at the next one-minute reload.
	served, err := m.tls.getNodeCert()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(served.Certificate[0], after.Raw) {
		t.Fatal("the reissued certificate is not the one presented")
	}
}

// A certificate that names every address already is left alone: this runs
// on every watchLeader tick.
func TestACoveredCertificateIsLeftAlone(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "192.0.2.10", []string{"100.64.0.10"}, "192.0.2.10")
	if changed, err := m.EnsureAddressCert(); err != nil || !changed {
		t.Fatalf("first EnsureAddressCert = %v, %v", changed, err)
	}
	first := mustLeaf(t, tlsMgr)
	changed, err := m.EnsureAddressCert()
	if err != nil || changed {
		t.Fatalf("second EnsureAddressCert = %v, %v; want false, nil", changed, err)
	}
	if mustLeaf(t, tlsMgr).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("a certificate naming every address was reissued")
	}
}

// The addresses the cluster records for this manager count too: the leader
// keeps them in its node record, and other nodes dial what it holds.
func TestAManagerCertificateGainsTheAddressesItsRecordHolds(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "192.0.2.10", nil, "192.0.2.10")
	applyCmd(t, m.raft.fsm, Command{Type: CmdAddNode, Value: mustJSON(Node{
		ID: "node-1", GRPCAddress: "192.0.2.10:3629", Addresses: []string{"192.0.2.10", "198.51.100.7"},
	})})
	if changed, err := m.EnsureAddressCert(); err != nil || !changed {
		t.Fatalf("EnsureAddressCert = %v, %v; want true, nil", changed, err)
	}
	if ips := leafIPs(mustLeaf(t, tlsMgr)); !slices.Contains(ips, "198.51.100.7") {
		t.Fatalf("reissued certificate names %v, not the recorded 198.51.100.7", ips)
	}
}

// A manager registered at a host name has that name in its record and its
// advertise address; the certificate names it as a DNS SAN, and it never
// counts as an address the certificate lacks — else every tick would
// reissue.
func TestAHostNameNeverTriggersAnAddressReissue(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "panel.example", nil, "panel.example", "192.0.2.10")
	applyCmd(t, m.raft.fsm, Command{Type: CmdAddNode, Value: mustJSON(Node{
		ID: "node-1", GRPCAddress: "panel.example:3629", Addresses: []string{"192.0.2.10"},
	})})
	before := mustLeaf(t, tlsMgr)
	changed, err := m.EnsureAddressCert()
	if err != nil || changed {
		t.Fatalf("EnsureAddressCert = %v, %v; want false, nil", changed, err)
	}
	if mustLeaf(t, tlsMgr).SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatal("a host name reissued the certificate")
	}
}

// A CA rotation moves every certificate itself, step by step; a reissue in
// between would sign with whichever CA ca.crt starts with.
func TestNoAddressReissueDuringACARotation(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "192.0.2.10", []string{"100.64.0.10"}, "192.0.2.10")
	r := CARotation{ID: "r1", Seq: 1, Phase: CAPhaseTrust, OldPin: "old", NextPin: "next"}
	applyCmd(t, m.raft.fsm, Command{Type: CmdSetConfig, Key: configKeyCARotation, Value: mustJSON(string(mustJSON(r)))})
	before := mustLeaf(t, tlsMgr)

	changed, err := m.EnsureAddressCert()
	if err != nil || changed {
		t.Fatalf("EnsureAddressCert = %v, %v; want false, nil", changed, err)
	}
	if mustLeaf(t, tlsMgr).SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatal("the certificate was reissued during a CA rotation")
	}
}

// A reissue only adds: an address the manager no longer finds — a Tailscale
// address gone for now — stays, so a node still dialling it is not cut off.
func TestAnAddressReissueKeepsOldSANs(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "192.0.2.10", []string{"100.64.0.10"}, "192.0.2.10", "100.64.0.9", "node-1.example")
	if changed, err := m.EnsureAddressCert(); err != nil || !changed {
		t.Fatalf("EnsureAddressCert = %v, %v; want true, nil", changed, err)
	}
	after := mustLeaf(t, tlsMgr)
	ips := leafIPs(after)
	for _, want := range []string{"192.0.2.10", "100.64.0.9", "100.64.0.10"} {
		if !slices.Contains(ips, want) {
			t.Fatalf("reissued certificate names %v, without %s", ips, want)
		}
	}
	if !slices.Equal(after.DNSNames, []string{"node-1.example"}) {
		t.Fatalf("reissued certificate lost its DNS name: %v", after.DNSNames)
	}
}

// A certificate in the agent role is not a manager's to rewrite: a manager
// converting to an agent swaps its certificate for one while this manager's
// ticks may still run.
func TestAnAgentCertificateIsNotReissuedAsAManagers(t *testing.T) {
	m, tlsMgr := addressCertManager(t, "192.0.2.10", []string{"100.64.0.10"}, "192.0.2.10")
	certPEM, keyPEM, err := tlsMgr.IssueNodeCert("node-1", []string{"192.0.2.10"}, CertRoleAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsMgr.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	changed, err := m.EnsureAddressCert()
	if err != nil || changed {
		t.Fatalf("EnsureAddressCert = %v, %v; want false, nil", changed, err)
	}
	if CertRole(mustLeaf(t, tlsMgr)) != CertRoleAgent {
		t.Fatal("an agent certificate was reissued in the manager role")
	}
}

// MergeAddresses lists each address once, in first-seen order, an IP in its
// canonical form whatever form it came in.
func TestMergeAddresses(t *testing.T) {
	got := MergeAddresses(
		[]string{"192.0.2.10", "panel.example"},
		[]string{"::ffff:192.0.2.10", "100.64.0.10", ""},
		[]string{"panel.example", "2001:db8:0::1", "2001:db8::1"},
	)
	want := []string{"192.0.2.10", "panel.example", "100.64.0.10", "2001:db8::1"}
	if !slices.Equal(got, want) {
		t.Fatalf("MergeAddresses = %v, want %v", got, want)
	}
}

// Init's first certificate names the manager's Tailscale addresses beside
// the address it advertises.
func TestInitCertificateNamesTheTailscaleAddress(t *testing.T) {
	ifaces := &fakeIfaces{}
	ifaces.set("100.64.0.10")
	m, _, _, _ := realLeaderWith(t, withIfaces(ifaces))
	ips := leafIPs(mustLeaf(t, m.tls))
	if !slices.Contains(ips, "127.0.0.1") || !slices.Contains(ips, "100.64.0.10") {
		t.Fatalf("Init's certificate names %v", ips)
	}
}
