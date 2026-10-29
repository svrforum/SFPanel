package cluster

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// stopsApplyingCAAtEnd stops mgr as the test ends and then holds its CA
// apply lock, as caLeader does: the FSM's callbacks run in goroutines of
// their own, and one that applies a CA step as the test ends writes into the
// directories being removed.
func stopsApplyingCAAtEnd(t *testing.T, mgr *Manager) {
	t.Helper()
	t.Cleanup(func() { mgr.Shutdown(); mgr.caApplyMu.Lock() })
}

func nodeKeyOf(t *testing.T, tm *TLSManager) *ecdsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tm.certDir, "node.key"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func csrFor(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// rotateWithAgentTo starts a CA rotation on mgr, the agent id reporting
// what each step needs, and steps it to want (trust or reissue).
func rotateWithAgentTo(t *testing.T, mgr *Manager, id string, want CAPhase) CARotation {
	t.Helper()
	oldPin := mgr.GetTLS().TrustPins()[0]
	report := func(trusts []string, certCA string) {
		mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: id, Version: "test", Capabilities: []string{CapCARotation}, CATrustPins: trusts, CertCAPin: certCA})
	}
	report([]string{oldPin}, oldPin)
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	if want == CAPhaseReissue {
		waitFor(t, "the leader to trust both CAs", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
		report([]string{oldPin, rot.NextPin}, oldPin)
		advanceTo(t, mgr, time.Now(), CAPhaseReissue)
	}
	return caRecord(t, mgr)
}

// An agent moves its certificate to the CA a rotation signs with, for the key
// it has, under its own ID and registered IP — and nothing else comes back
// but certificates.
func TestRenewCertificateSignsFromTheRotationsCA(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	stopsApplyingCAAtEnd(t, mgr)
	res, tm := registeredAgent(t, mgr)
	rot := rotateWithAgentTo(t, mgr, res.NodeID, CAPhaseReissue)
	leaf, _ := tm.NodeCertLeaf()
	key := nodeKeyOf(t, tm)
	csr := csrFor(t, key)
	agentKey, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID))
	certPEM, bundle, err := mgr.HandleRenewCertificate(leaf, csr, renewalProof(agentKey, res.NodeID, csr), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := parseLeaf(t, certPEM)
	nextCert, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextCert)
	if err := got.CheckSignatureFrom(parseLeaf(t, []byte(nextCert))); err != nil {
		t.Fatalf("not from the next CA: %v", err)
	}
	if got.Subject.CommonName != res.NodeID || CertRole(got) != CertRoleAgent || !key.PublicKey.Equal(got.PublicKey) ||
		len(got.IPAddresses) != 1 || got.IPAddresses[0].String() != "192.0.2.10" {
		t.Fatalf("renewed %+v %v", got.Subject, got.IPAddresses)
	}
	if bytes.Contains(bundle, []byte("PRIVATE KEY")) || rot.Phase != CAPhaseReissue {
		t.Fatal("the answer carries a key")
	}
}

// A certificate from the CA being replaced is not enough to move to the next
// one: whoever holds that CA's key could make one for any agent's ID. The
// renewal needs the agent's own key too, is for the key the agent already
// presents, comes once a minute, and only while a rotation moves certificates.
func TestRenewCertificateRefusals(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	stopsApplyingCAAtEnd(t, mgr)
	res, tm := registeredAgent(t, mgr)
	leaf, _ := tm.NodeCertLeaf()
	key := nodeKeyOf(t, tm)
	csr := csrFor(t, key)
	agentKey, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID))
	proof := renewalProof(agentKey, res.NodeID, csr)
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, proof, time.Now()); !errors.Is(err, ErrNoRenewalDue) {
		t.Fatalf("with no rotation: %v", err)
	}
	rotateWithAgentTo(t, mgr, res.NodeID, CAPhaseReissue)
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, renewalProof("another-key", res.NodeID, csr), time.Now()); !errors.Is(err, ErrRenewalProof) {
		t.Fatalf("a proof made with another key: %v", err)
	}
	other, _ := agentCSR(t)
	if _, _, err := mgr.HandleRenewCertificate(leaf, other, renewalProof(agentKey, res.NodeID, other), time.Now()); !errors.Is(err, ErrRenewalKey) {
		t.Fatalf("a request for another key: %v", err)
	}
	now := time.Now()
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, proof, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, proof, now.Add(10*time.Second)); !errors.Is(err, ErrRenewalTooSoon) {
		t.Fatalf("again at once: %v", err)
	}
}

// A renewal is refused to a manager's certificate, which a manager reissues
// itself, and before the rotation moves certificates: in its trust step the
// next CA is not one every node trusts yet.
func TestRenewCertificateWaitsForCertificatesToMove(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	stopsApplyingCAAtEnd(t, mgr)
	res, tm := registeredAgent(t, mgr)
	leaf, _ := tm.NodeCertLeaf()
	csr := csrFor(t, nodeKeyOf(t, tm))
	agentKey, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID))
	proof := renewalProof(agentKey, res.NodeID, csr)
	managerLeaf, _ := mgr.GetTLS().NodeCertLeaf()
	if _, _, err := mgr.HandleRenewCertificate(managerLeaf, csr, proof, time.Now()); err == nil || !strings.Contains(err.Error(), "is not an agent") {
		t.Fatalf("a manager's certificate: %v", err)
	}
	if rot := rotateWithAgentTo(t, mgr, res.NodeID, CAPhaseTrust); rot.Phase != CAPhaseTrust {
		t.Fatalf("phase %s", rot.Phase)
	}
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, proof, time.Now()); !errors.Is(err, ErrNoRenewalDue) {
		t.Fatalf("in the trust phase: %v", err)
	}
}

// On the way back the leader signs from the current CA again, until every
// certificate is back and the next CA is dropped — then nothing. A proof
// is the agent's key's alone: with no key on record, one made with none
// counts for nothing.
func TestRenewCertificateFollowsTheWayBack(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	stopsApplyingCAAtEnd(t, mgr)
	res, tm := registeredAgent(t, mgr)
	oldPin := mgr.GetTLS().TrustPins()[0]
	curCert, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCACert)
	leaf, _ := tm.NodeCertLeaf()
	csr := csrFor(t, nodeKeyOf(t, tm))
	agentKey, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID))
	proof := renewalProof(agentKey, res.NodeID, csr)
	rot := rotateWithAgentTo(t, mgr, res.NodeID, CAPhaseReissue)

	if err := mgr.raft.Apply(Command{Type: CmdDeleteConfig, Key: AgentKeyConfigKey(res.NodeID)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, renewalProof("", res.NodeID, csr), time.Now()); !errors.Is(err, ErrRenewalProof) {
		t.Fatalf("with no agent key on record: %v", err)
	}
	if err := mgr.SetConfig(AgentKeyConfigKey(res.NodeID), agentKey); err != nil {
		t.Fatal(err)
	}

	if back, err := mgr.AbortCARotation(); err != nil || back.Phase != CAPhaseRollback {
		t.Fatalf("abort: %+v %v", back, err)
	}
	certPEM, _, err := mgr.HandleRenewCertificate(leaf, csr, proof, time.Now())
	if err != nil {
		t.Fatalf("in the rollback phase: %v", err)
	}
	if err := parseLeaf(t, certPEM).CheckSignatureFrom(parseLeaf(t, []byte(curCert))); err != nil {
		t.Fatalf("not from the current CA: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: res.NodeID, Version: "test", Capabilities: []string{CapCARotation},
		CATrustPins: []string{oldPin, rot.NextPin}, CertCAPin: oldPin})
	advanceTo(t, mgr, time.Now(), CAPhaseDrop)
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, proof, time.Now().Add(2*renewalInterval)); !errors.Is(err, ErrNoRenewalDue) {
		t.Fatalf("in the drop phase: %v", err)
	}
}

// The way back goes by heartbeats heard since it began. Before the reissue
// every certificate came from the current CA, so an agent's last heartbeat
// from then says so of an agent the leader has since renewed from the next
// CA, whose connection showing that is still to come: the next CA dropped on
// it, the agent is refused everywhere, and the drop renews nothing.
func TestTheWayBackGoesByHeartbeatsSinceItBegan(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	stopsApplyingCAAtEnd(t, mgr)
	res, tm := registeredAgent(t, mgr)
	oldPin := mgr.GetTLS().TrustPins()[0]
	// Its last heartbeat: trusting both CAs, presenting the current one's
	// certificate.
	rot := rotateWithAgentTo(t, mgr, res.NodeID, CAPhaseReissue)
	leaf, _ := tm.NodeCertLeaf()
	csr := csrFor(t, nodeKeyOf(t, tm))
	agentKey, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID))
	if _, _, err := mgr.HandleRenewCertificate(leaf, csr, renewalProof(agentKey, res.NodeID, csr), time.Now()); err != nil {
		t.Fatal(err)
	}
	if back, err := mgr.AbortCARotation(); err != nil || back.Phase != CAPhaseRollback {
		t.Fatalf("abort: %+v %v", back, err)
	}
	waitFor(t, "the leader's certificate back on the current CA", func() bool { return mgr.GetTLS().ServedCertCAPin() == oldPin })
	if err := mgr.advanceCARotation(time.Now()); err != nil || caRecord(t, mgr).Phase != CAPhaseRollback {
		t.Fatalf("dropped the next CA on a heartbeat from before the way back (%v): phase %s", err, caRecord(t, mgr).Phase)
	}
	step := nextCAStep(caRecord(t, mgr), mgr.caNodes(mgr.raft.GetFSM().GetState()), time.Now())
	if want := "agent-host (not heard from since the rollback phase began)"; !slices.Contains(step.Waiting, want) {
		t.Fatalf("the way back waits for %q, not %q", step.Waiting, want)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: res.NodeID, Version: "test", Capabilities: []string{CapCARotation},
		CATrustPins: []string{oldPin, rot.NextPin}, CertCAPin: oldPin})
	advanceTo(t, mgr, time.Now(), CAPhaseDrop)
}

// A renewed certificate is installed only when it is this agent's in all the
// leader does not choose: its key, its ID and role, the address it
// registered at, and a CA it trusts. node.key is never touched, and the one
// installed is presented at once, not after the reload's minute.
func TestInstallRenewedCertTakesOnlyThisAgentsCertificate(t *testing.T) {
	_, ca := setupCertDir(t)
	cur, _ := ca.LoadCACert()
	next, _ := GenerateCA("next", time.Now())
	other, _ := GenerateCA("other", time.Now())
	agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
	if err := agent.SaveCACert(append(append([]byte{}, cur...), next.CertPEM...)); err != nil {
		t.Fatal(err)
	}
	csr := csrFor(t, nodeKeyOf(t, agent))
	sign := func(by *CAPair, id, ip, role string) []byte {
		t.Helper()
		cert, err := SignNodeCSRBy(by, id, []string{ip}, role, csr)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	anotherKey, _, err := IssueNodeCertBy(next, "agent-1", []string{"127.0.0.1"}, CertRoleAgent)
	if err != nil {
		t.Fatal(err)
	}
	keyBefore, _ := os.ReadFile(filepath.Join(agent.certDir, "node.key"))
	certBefore, _ := os.ReadFile(filepath.Join(agent.certDir, "node.crt"))
	curPin := agent.ServedCertCAPin() // loads the certificate now presented
	for _, tc := range []struct {
		name, reason string
		cert         []byte
	}{
		{"not a certificate", "not PEM", []byte("nothing")},
		{"for another key", "not for this node's key", anotherKey},
		{"for another node", "not this agent", sign(next, "agent-2", "127.0.0.1", CertRoleAgent)},
		{"in the manager role", "not this agent", sign(next, "agent-1", "127.0.0.1", CertRoleManager)},
		{"naming another address", "does not name", sign(next, "agent-1", "192.0.2.99", CertRoleAgent)},
		{"from a CA not trusted", "not signed by a CA this node trusts", sign(other, "agent-1", "127.0.0.1", CertRoleAgent)},
	} {
		if err := agent.installRenewedCert(tc.cert, "agent-1", "127.0.0.1"); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(agent.certDir, "node.crt")); !bytes.Equal(after, certBefore) || agent.ServedCertCAPin() != curPin {
		t.Fatal("a refused certificate was installed")
	}
	if err := agent.installRenewedCert(sign(next, "agent-1", "127.0.0.1", CertRoleAgent), "agent-1", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if got := agent.ServedCertCAPin(); got != next.Pin() {
		t.Fatalf("presents a certificate from %s, want the next CA", got)
	}
	if after, _ := os.ReadFile(filepath.Join(agent.certDir, "node.key")); !bytes.Equal(after, keyBefore) {
		t.Fatal("node.key changed")
	}
}
