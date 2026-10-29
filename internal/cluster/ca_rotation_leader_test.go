package cluster

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// caLeader is a one-manager cluster — gRPC up, CA seeded — whose proxy
// secret switched long ago, with an agent a1 the leader has heard from on
// code that follows a CA rotation.
func caLeader(t *testing.T) *Manager {
	t.Helper()
	mgr, _, _, _ := realLeader(t)
	// The FSM's callbacks run in goroutines of their own, and one that
	// applies a CA step as the test ends writes into the directories being
	// removed — a file already gone is written again. Once the manager has
	// stopped, a callback still to run waits on the apply lock for good.
	t.Cleanup(func() { mgr.Shutdown(); mgr.caApplyMu.Lock() })
	settleRotation(t, mgr)
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a1", Name: "a1", Kind: KindAgent,
		APIAddress: "192.0.2.11:3628", GRPCAddress: "192.0.2.11:3629"})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	pin := mgr.GetTLS().TrustPins()[0]
	reportA1(mgr, []string{pin}, pin)
	return mgr
}

// reportA1 records a heartbeat from a1: what it says it trusts, and the CA
// behind the certificate its connection presented.
func reportA1(mgr *Manager, trusts []string, certCA string) {
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "a1", Version: "test", Capabilities: []string{CapCARotation}, CATrustPins: trusts, CertCAPin: certCA})
}

func caRecord(t *testing.T, m *Manager) CARotation {
	t.Helper()
	r, ok, err := caRotationOf(m.raft.GetFSM().GetState())
	if err != nil || !ok {
		t.Fatalf("no rotation record: %v", err)
	}
	return r
}

// advanceTo steps the leader at now until the record says want.
func advanceTo(t *testing.T, m *Manager, now time.Time, want CAPhase) {
	t.Helper()
	waitFor(t, "phase "+string(want), func() bool {
		if err := m.advanceCARotation(now); err != nil {
			t.Fatal(err)
		}
		return caRecord(t, m).Phase == want
	})
}

func onlyTrusts(m *Manager, pin string) func() bool {
	return func() bool { p := m.GetTLS().TrustPins(); return len(p) == 1 && p[0] == pin }
}

func pinOfCertPEM(t *testing.T, certPEM string) string {
	t.Helper()
	return CAPin(parseLeaf(t, []byte(certPEM)).RawSubjectPublicKeyInfo)
}

// The rotation waits for every node, agents too, says for whom, and writes
// nothing until then.
func TestStartCARotationWaitsForEveryNode(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a1", Name: "a1", Kind: KindAgent})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartCARotation(0); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "a1 (not heard from)") {
		t.Fatalf("a1 not heard from: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "a1", Version: "0.79.0"})
	if _, err := mgr.StartCARotation(0); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "a1 (0.79.0)") {
		t.Fatalf("a1 on a release that cannot follow: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextCert); v != "" {
		t.Fatal("a refused start wrote the next CA")
	}
}

// The derived proxy secret is sha256(ca.crt): moved while it is still
// accepted, it would change on each node at its next start.
func TestStartCARotationWaitsForTheProxySecret(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	if _, err := mgr.StartCARotation(0); !errors.Is(err, ErrProxyRotationPending) {
		t.Fatalf("before the proxy secret settled: %v", err)
	}
}

// The first step writes every ca.crt from replicated state: a leader whose
// own starts with another CA signed with that one, and would trust it no
// more.
func TestStartCARotationNeedsTheLeadersCAToBeTheClusters(t *testing.T) {
	mgr := caLeader(t)
	other, err := GenerateCA("SFPanel CA - another", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.GetTLS().SaveCACert(other.CertPEM); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartCARotation(0); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "this leader's ca.crt is not the cluster's CA") {
		t.Fatalf("a leader whose ca.crt is another CA: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextCert); v != "" {
		t.Fatal("a refused start wrote the next CA")
	}
}

func TestOneCARotationAtATimeWithinItsBounds(t *testing.T) {
	mgr := caLeader(t)
	if _, err := mgr.StartCARotation(time.Minute); !errors.Is(err, ErrRotationRefused) {
		t.Fatalf("a one-minute phase: %v", err)
	}
	rot, err := mgr.StartCARotation(0)
	if err != nil || rot.Phase != CAPhaseTrust || rot.Seq != 1 || rot.PhaseTimeout != int64(DefaultCAPhaseTimeout.Seconds()) {
		t.Fatalf("start: %+v %v", rot, err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextCert); pinOfCertPEM(t, v) != rot.NextPin {
		t.Fatal("the record names another CA than the one replicated")
	}
	if _, err := mgr.StartCARotation(0); !errors.Is(err, ErrCARotationInProgress) {
		t.Fatalf("a second rotation: %v", err)
	}
}

// Nothing joins while the CA rotates: a node signed by one CA and pinned to
// the other would count in no step. The refusal keeps the token.
func TestNothingJoinsWhileTheCARotates(t *testing.T) {
	mgr := caLeader(t)
	agentTok, _ := mgr.CreateJoinToken(time.Hour, KindAgent)
	managerTok, _ := mgr.CreateJoinToken(time.Hour, KindManager)
	if _, err := mgr.StartCARotation(0); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.HandleRegisterAgent(registerInput(t, agentTok.Token)); !errors.Is(err, ErrCARotationInProgress) {
		t.Fatalf("a registration: %v", err)
	}
	if _, err := mgr.tokens.Peek(agentTok.Token); err != nil {
		t.Fatal("the refusal spent the agent token")
	}
	if _, _, _, _, err := mgr.HandleJoin("j1", "j1", "127.0.0.2:1", "127.0.0.2:2", managerTok.Token, []string{CapAgents}); !errors.Is(err, ErrCARotationInProgress) {
		t.Fatalf("a manager join: %v", err)
	}
	if _, err := mgr.PrepareManagerPromotion("a1"); !errors.Is(err, ErrCARotationInProgress) {
		t.Fatalf("a promotion: %v", err)
	}
}

// A conversion leaves Raft and then registers as an agent, which the
// rotation would refuse: the node would be out of the cluster with nowhere
// to go, so the conversion is refused before it starts.
func TestNoConversionWhileTheCARotates(t *testing.T) {
	mgr := caLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter, APIAddress: "192.0.2.20:3628", GRPCAddress: "192.0.2.20:3629"})
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{CapAgents, CapCARotation}})
	if err := mgr.ReadyForAgentConversion("m2"); err != nil {
		t.Fatalf("ready before the rotation: %v", err)
	}
	if _, err := mgr.StartCARotation(0); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ReadyForAgentConversion("m2"); !errors.Is(err, ErrConvertRefused) || !errors.Is(err, ErrCARotationInProgress) {
		t.Fatalf("a conversion while the CA rotates: %v", err)
	}
}

// The rotation moves on what the leader sees: a1 says it trusts the next CA
// before any certificate moves, and shows one from it on its connection
// before the next CA becomes the cluster's. Then a join command names the
// new CA, and the next CA's key is no longer a key of its own.
func TestACARotationMovesOnEvidence(t *testing.T) {
	mgr := caLeader(t)
	oldPin := mgr.GetTLS().TrustPins()[0]
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	waitFor(t, "the leader to trust both CAs", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
	if err := mgr.advanceCARotation(now); err != nil || caRecord(t, mgr).Phase != CAPhaseTrust {
		t.Fatalf("moved on before a1 trusted the next CA (%v)", err)
	}
	reportA1(mgr, []string{oldPin, rot.NextPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseReissue)
	waitFor(t, "the leader's certificate from the next CA", func() bool { return mgr.GetTLS().ServedCertCAPin() == rot.NextPin })
	if err := mgr.advanceCARotation(now); err != nil || caRecord(t, mgr).Phase != CAPhaseReissue {
		t.Fatalf("promoted while a1 presented a certificate from the current CA (%v)", err)
	}
	reportA1(mgr, []string{oldPin, rot.NextPin}, rot.NextPin)
	advanceTo(t, mgr, now, CAPhasePromote)
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCACert); pinOfCertPEM(t, v) != rot.NextPin {
		t.Fatal("the cluster's CA is not the next one at promote")
	}
	waitFor(t, "the leader to drop the old CA", onlyTrusts(mgr, rot.NextPin))
	reportA1(mgr, []string{rot.NextPin}, rot.NextPin)
	advanceTo(t, mgr, now, CAPhaseDone)
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextKey); v != "" {
		t.Fatal("the next CA's key stayed in replicated state beside the cluster's")
	}
	key, _ := mgr.CAPublicKey()
	if CAPin(key) != rot.NextPin {
		t.Fatal("a join command printed now names the old CA")
	}
}

// A phase that does not finish in its time rolls back and keeps the current
// CA: an offline or broken node holds a rotation up, not the cluster.
func TestAStalledCARotationRollsBackToTheCurrentCA(t *testing.T) {
	mgr := caLeader(t)
	oldPin := mgr.GetTLS().TrustPins()[0]
	if _, err := mgr.StartCARotation(MinCAPhaseTimeout); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the leader to trust both CAs", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
	advanceTo(t, mgr, time.Now().Add(MinCAPhaseTimeout+time.Minute), CAPhaseDrop)
	if r := caRecord(t, mgr); !strings.Contains(r.Reason, "a1 (does not trust the next CA yet)") {
		t.Fatalf("reason %q", r.Reason)
	}
	waitFor(t, "the leader to drop the next CA", onlyTrusts(mgr, oldPin))
	reportA1(mgr, []string{oldPin}, oldPin)
	advanceTo(t, mgr, time.Now(), CAPhaseAborted)
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCACert); pinOfCertPEM(t, v) != oldPin {
		t.Fatal("the cluster's CA changed")
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextKey); v != "" {
		t.Fatal("the next CA's key stayed")
	}
	// The way back keeps why it was taken.
	if r := caRecord(t, mgr); !strings.Contains(r.Reason, "a1 (does not trust the next CA yet)") {
		t.Fatalf("the aborted record says %q", r.Reason)
	}
}

// Stopped in the reissue phase, the rotation moves every certificate the
// next CA signed back to the current one before it drops the next CA — and
// past promote it cannot be stopped.
func TestAbortingAfterCertificatesMovedBringsThemBack(t *testing.T) {
	mgr := caLeader(t)
	oldPin := mgr.GetTLS().TrustPins()[0]
	rot, _ := mgr.StartCARotation(0)
	now := time.Now()
	waitFor(t, "the leader to trust both CAs", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
	reportA1(mgr, []string{oldPin, rot.NextPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseReissue)
	reportA1(mgr, []string{oldPin, rot.NextPin}, rot.NextPin) // a1 renewed
	if r, err := mgr.AbortCARotation(); err != nil || r.Phase != CAPhaseRollback {
		t.Fatalf("abort: %+v %v", r, err)
	}
	waitFor(t, "the leader's certificate back on the current CA", func() bool { return mgr.GetTLS().ServedCertCAPin() == oldPin })
	if err := mgr.advanceCARotation(now); err != nil || caRecord(t, mgr).Phase != CAPhaseRollback {
		t.Fatalf("dropped the next CA while a1 presented a certificate from it (%v)", err)
	}
	reportA1(mgr, []string{oldPin, rot.NextPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseDrop)
	reportA1(mgr, []string{oldPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseAborted)
	abortedSeq := caRecord(t, mgr).Seq
	if r := caRecord(t, mgr); r.Reason != "aborted by the operator" {
		t.Fatalf("the aborted record says %q", r.Reason)
	}

	// The record stays, and the next rotation's seq follows it.
	rot2, err := mgr.StartCARotation(0)
	if err != nil || rot2.Seq != abortedSeq+1 {
		t.Fatalf("a second rotation: %+v %v (the aborted one ended at seq %d)", rot2, err, abortedSeq)
	}
	waitFor(t, "trust", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
	reportA1(mgr, []string{oldPin, rot2.NextPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseReissue)
	waitFor(t, "reissue", func() bool { return mgr.GetTLS().ServedCertCAPin() == rot2.NextPin })
	reportA1(mgr, []string{oldPin, rot2.NextPin}, rot2.NextPin)
	advanceTo(t, mgr, now, CAPhasePromote)
	if _, err := mgr.AbortCARotation(); !errors.Is(err, ErrCARotationPastReturn) {
		t.Fatalf("abort after promote: %v", err)
	}
}

// A leader that takes over finishes a step its predecessor wrote only in
// part: past promote the cluster's CA is the next one, and once the rotation
// has ended the next CA's key leaves replicated state — not before the
// cluster's CA is the next one, or no node would find the CA the record
// names. Until then no new rotation starts on top of it.
func TestTheLeaderFinishesAStepWrittenInPart(t *testing.T) {
	mgr := caLeader(t)
	oldPin := mgr.GetTLS().TrustPins()[0]
	fsm := mgr.raft.GetFSM()
	oldCert, _ := fsm.GetConfigValue(configKeyCACert)
	oldKey, _ := fsm.GetConfigValue(configKeyCAKey)
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	nextCert, _ := fsm.GetConfigValue(configKeyCANextCert)
	nextKey, _ := fsm.GetConfigValue(configKeyCANextKey)
	set := func(kv ...string) {
		t.Helper()
		for i := 0; i < len(kv); i += 2 {
			if err := mgr.SetConfig(kv[i], kv[i+1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now()
	waitFor(t, "the leader to trust both CAs", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
	reportA1(mgr, []string{oldPin, rot.NextPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseReissue)
	waitFor(t, "the leader's certificate from the next CA", func() bool { return mgr.GetTLS().ServedCertCAPin() == rot.NextPin })
	reportA1(mgr, []string{oldPin, rot.NextPin}, rot.NextPin)
	advanceTo(t, mgr, now, CAPhasePromote)

	// As if the leader that wrote the promote record had stopped before the
	// cluster's CA.
	set(configKeyCACert, oldCert, configKeyCAKey, oldKey)
	if err := mgr.advanceCARotation(now); err != nil {
		t.Fatal(err)
	}
	if v, _ := fsm.GetConfigValue(configKeyCACert); pinOfCertPEM(t, v) != rot.NextPin {
		t.Fatal("past promote the cluster's CA was left the old one")
	}
	if v, _ := fsm.GetConfigValue(configKeyCAKey); v != nextKey {
		t.Fatal("past promote the cluster's CA key was left the old one")
	}

	waitFor(t, "the leader to drop the old CA", onlyTrusts(mgr, rot.NextPin))
	reportA1(mgr, []string{rot.NextPin}, rot.NextPin)
	advanceTo(t, mgr, now, CAPhaseDone)
	// As if the leader that wrote the done record had stopped before it took
	// the next CA out — and the one before it, before the cluster's CA.
	set(configKeyCACert, oldCert, configKeyCAKey, oldKey, configKeyCANextCert, nextCert, configKeyCANextKey, nextKey)
	if _, err := mgr.StartCARotation(0); !errors.Is(err, ErrRotationRefused) {
		t.Fatalf("a rotation started over a cluster CA the last one had not finished writing: %v", err)
	}
	if err := mgr.advanceCARotation(now); err != nil {
		t.Fatal(err)
	}
	if v, _ := fsm.GetConfigValue(configKeyCACert); pinOfCertPEM(t, v) != rot.NextPin {
		t.Fatal("the next CA was taken out before it was the cluster's")
	}
	for _, key := range []string{configKeyCANextCert, configKeyCANextKey} {
		if v, _ := fsm.GetConfigValue(key); v != "" {
			t.Fatalf("%s stayed after the rotation ended", key)
		}
	}
	if _, err := caMaterialOf(fsm.GetState()); err != nil {
		t.Fatalf("the record names a CA replicated state does not hold: %v", err)
	}
}

// Agents learn the CAs from the leader's pong — the bundle, the CA their
// certificates must come from, the record's seq — and never a key. A node
// that is not the leader says nothing.
func TestTheCAHintIsTheLeadersAndCarriesNoKey(t *testing.T) {
	mgr := caLeader(t)
	if mgr.caHint() != nil {
		t.Fatal("a hint with no rotation")
	}
	rot, _ := mgr.StartCARotation(0)
	h := mgr.caHint()
	certs, err := certificatesIn(h.Bundle)
	if err != nil || len(certs) != 2 || CAPin(certs[0].RawSubjectPublicKeyInfo) != rot.OldPin || h.SignPin != rot.OldPin || h.Seq != rot.Seq {
		t.Fatalf("hint %+v (%v)", h, err)
	}
	if bytes.Contains(h.Bundle, []byte("PRIVATE KEY")) {
		t.Fatal("the hint carries a key")
	}
	if (&Manager{raft: &RaftNode{fsm: mgr.raft.GetFSM()}}).caHint() != nil {
		t.Fatal("a node that is not the leader sent a hint")
	}
}

// The hint rides an agent's pong; a manager's carries none, as a manager
// follows the rotation from its own FSM.
func TestOnlyAnAgentsPongCarriesTheCAHint(t *testing.T) {
	mgr := caLeader(t)
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})
	s := &GRPCServer{manager: mgr}
	pong := func(id string, serve func(*scriptedHeartbeatStream) error) *pb.HeartbeatPong {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream := &scriptedHeartbeatStream{ctx: ctxWithPeerCN(ctx, id), in: make(chan *pb.HeartbeatPing, 1)}
		done := make(chan error, 1)
		go func() { done <- serve(stream) }()
		stream.in <- &pb.HeartbeatPing{NodeId: id, Timestamp: time.Now().Unix()}
		waitFor(t, "a pong to "+id, func() bool { return stream.pongCount() > 0 })
		cancel()
		<-done
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return stream.pongs[0]
	}
	agent := pong("a1", func(st *scriptedHeartbeatStream) error { return s.AgentHeartbeat(st) })
	if agent.Ca == nil || agent.Ca.Seq != rot.Seq || agent.Ca.SignPin != rot.OldPin {
		t.Fatalf("an agent's pong carries %+v", agent.Ca)
	}
	if manager := pong("m2", func(st *scriptedHeartbeatStream) error { return s.Heartbeat(st) }); manager.Ca != nil {
		t.Fatalf("a manager's pong carries %+v", manager.Ca)
	}
}

// The leader says whom a rotation waits for once while that stays the same,
// and its logs carry pins, never the next CA's key.
func TestACARotationSaysWhomItWaitsForOnceAndNoKey(t *testing.T) {
	logs := captureLogs(t)
	mgr := caLeader(t)
	oldPin := mgr.GetTLS().TrustPins()[0]
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	nextKey, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCANextKey)
	now := time.Now()
	waitFor(t, "the leader to trust both CAs", func() bool { return len(mgr.GetTLS().TrustPins()) == 2 })
	for i := 0; i < 3; i++ {
		if err := mgr.advanceCARotation(now); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(logs.String(), `waiting="a1 (does not trust the next CA yet)"`); n != 1 {
		t.Fatalf("the wait was logged %d times:\n%s", n, logs.String())
	}
	reportA1(mgr, []string{oldPin, rot.NextPin}, oldPin)
	advanceTo(t, mgr, now, CAPhaseReissue)
	out := logs.String()
	if !strings.Contains(out, rot.NextPin) || !strings.Contains(out, "phase=reissue") {
		t.Fatalf("the start and the step are not logged:\n%s", out)
	}
	for _, secret := range []string{"PRIVATE KEY", pemBody([]byte(nextKey))} {
		if strings.Contains(out, secret) {
			t.Fatalf("the logs carry %q:\n%s", secret, out)
		}
	}
}
