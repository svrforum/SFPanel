package cluster

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
)

// A real agent follows a whole rotation over its link: it trusts both CAs
// from the leader's pong, renews its certificate when the leader moves
// certificates, shows the new one on a new connection — which is what lets
// the leader promote the next CA — and drops the old CA after it.
func TestAnAgentFollowsACARotation(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	stopsApplyingCAAtEnd(t, mgr)
	res, tm := registeredAgent(t, mgr)
	runLink(t, mgr, addr, res, tm, nil, nil)
	waitFor(t, "a heartbeat that says the agent follows a rotation", func() bool {
		met := mgr.GetHeartbeat().GetMetrics(res.NodeID)
		return met != nil && slices.Contains(met.Capabilities, CapCARotation)
	})
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	waitFor(t, "the agent to trust both CAs", func() bool { return len(tm.TrustPins()) == 2 })
	advanceTo(t, mgr, now, CAPhaseReissue)
	waitFor(t, "the agent's certificate from the next CA", func() bool { return tm.ServedCertCAPin() == rot.NextPin })
	advanceTo(t, mgr, now, CAPhasePromote)
	waitFor(t, "the agent to drop the old CA", func() bool { p := tm.TrustPins(); return len(p) == 1 && p[0] == rot.NextPin })
	advanceTo(t, mgr, now, CAPhaseDone)
	data, _ := os.ReadFile(filepath.Join(tm.certDir, "ca.crt"))
	if len(pemCertificates(data)) != 1 {
		t.Fatal("ca.crt on the agent holds more than the new CA")
	}
}

// hintingLeader is a fake leader whose pongs carry hint.
func hintingLeader(t *testing.T, ca *TLSManager, hint func() *pb.ClusterCA, fromLeader bool) *fakeManager {
	t.Helper()
	return startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{pong: func(self string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{FromLeader: fromLeader, LeaderGrpcAddress: self, Ca: hint()}, nil
	}})
}

func caCrtOf(t *testing.T, tm *TLSManager) []byte {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(tm.certDir, "ca.crt"))
	return data
}

// A follower's word is not taken for the CAs, nor a bundle that leaves out
// the CA behind the very connection it came on — written, it would cut the
// agent off from that manager — nor a hint older than the one taken. Each is
// refused for its own reason, which the agent says; a follower's hint is
// not looked at.
func TestAnAgentTakesOnlyAWholeNewerHintFromTheLeader(t *testing.T) {
	_, ca := setupCertDir(t)
	cur, _ := ca.LoadCACert()
	next, _ := GenerateCA("next", time.Now())
	other, _ := GenerateCA("other", time.Now())
	good := append(append([]byte{}, cur...), next.CertPEM...)
	leaf, _, err := ca.IssueNodeCert("m9", []string{"127.0.0.1"}, CertRoleManager)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		bundle     []byte
		seq        uint64
		fromLeader bool
		state      uint64 // the seq the agent already took
		reason     string // what the agent says of it; "" for nothing at all
	}{
		{"from a follower", good, 1, false, 0, ""},
		{"without the CA of its connection", other.CertPEM, 1, true, 0, errCAHintOutlivesConnection.Error()},
		{"with a private key in it", append(append([]byte{}, good...), next.KeyPEM...), 1, true, 0, "not a certificate"},
		{"with a certificate that is not a CA", append(append([]byte{}, cur...), leaf...), 1, true, 0, "which is not a CA"},
		{"larger than 64 KiB", bytes.Repeat(good, maxCABundleBytes/len(good)+1), 1, true, 0, "larger than 64 KiB"},
		{"older than the one taken", good, 3, true, 5, "older than the step 5 taken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
			before := caCrtOf(t, agent)
			mgr := hintingLeader(t, ca, func() *pb.ClusterCA {
				return &pb.ClusterCA{Bundle: tc.bundle, SignPin: CAPin(parseLeaf(t, cur).RawSubjectPublicKeyInfo), Seq: tc.seq}
			}, tc.fromLeader)
			statePath := filepath.Join(t.TempDir(), "agent-state.json")
			if err := (&AgentState{Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: mgr.self}}, CASeq: tc.state}).Save(statePath); err != nil {
				t.Fatal(err)
			}
			link, err := NewAgentLink(AgentLinkConfig{NodeID: "agent-1", TLS: agent, StatePath: statePath, Version: "test", Advertise: "127.0.0.1",
				Interval: 100 * time.Millisecond}, nil)
			if err != nil {
				t.Fatal(err)
			}
			goRun(t, link)
			waitFor(t, "a heartbeat session", func() bool { return mgr.streams.Load() >= 1 })
			time.Sleep(500 * time.Millisecond) // pings every 100 ms: several pongs, each with the hint
			if !bytes.Equal(caCrtOf(t, agent), before) {
				t.Fatal("ca.crt changed")
			}
			if tc.reason == "" && strings.Contains(logs.String(), "CA hint") {
				t.Fatalf("a follower's hint was looked at: %s", logs)
			}
			if tc.reason != "" && !strings.Contains(logs.String(), tc.reason) {
				t.Fatalf("not refused for its reason (%s): %s", tc.reason, logs)
			}
		})
	}
}

// A leader that cannot renew — a manager from before rotations answers
// Unimplemented — leaves the agent's certificate as it is and the link
// running; it is not a manager from before agents. The agent asks again a
// minute later, not on every pong.
func TestAnAgentKeepsItsCertificateWhenTheLeaderCannotRenew(t *testing.T) {
	logs := captureLogs(t)
	_, ca := setupCertDir(t)
	cur, _ := ca.LoadCACert()
	next, _ := GenerateCA("next", time.Now())
	bundle := append(append([]byte{}, cur...), next.CertPEM...)
	mgr := hintingLeader(t, ca, func() *pb.ClusterCA { return &pb.ClusterCA{Bundle: bundle, SignPin: next.Pin(), Seq: 1} }, true)
	agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
	if err := agent.SaveAgentKey("agent-key"); err != nil {
		t.Fatal(err)
	}
	certBefore, _ := os.ReadFile(filepath.Join(agent.certDir, "node.crt"))
	link := startLink(t, agent, "agent-1", []config.ManagerRef{{ID: "m1", GRPCAddress: mgr.self}})
	waitFor(t, "the agent to trust both CAs", func() bool { return len(agent.TrustPins()) == 2 })
	waitFor(t, "more heartbeats", func() bool { return mgr.streams.Load() >= 1 })
	time.Sleep(500 * time.Millisecond)
	if certAfter, _ := os.ReadFile(filepath.Join(agent.certDir, "node.crt")); !bytes.Equal(certBefore, certAfter) {
		t.Fatal("the certificate changed")
	}
	if link.Removed() {
		t.Fatal("the link stopped")
	}
	if n := mgr.renews.Load(); n != 1 {
		t.Fatalf("asked the leader to renew %d times", n)
	}
	if !strings.Contains(logs.String(), "the certificate stays as it is") || strings.Contains(logs.String(), errManagerPredatesAgents.Error()) {
		t.Fatalf("the answer was not read as a manager that cannot renew: %s", logs)
	}
}

// The leader signs from the CA its hint names. A certificate from another
// one — the rotation moved on since the hint — is not installed, though the
// agent trusts that CA as well: the next hint says what to do.
func TestAnAgentInstallsOnlyACertificateFromTheCAItsHintNamed(t *testing.T) {
	_, ca := setupCertDir(t)
	cur, _ := ca.LoadCACert()
	curCA, err := ca.diskCA()
	if err != nil {
		t.Fatal(err)
	}
	next, _ := GenerateCA("next", time.Now())
	bundle := append(append([]byte{}, cur...), next.CertPEM...)
	for _, tc := range []struct {
		name      string
		signer    *CAPair
		installed bool
	}{
		{"from another CA it trusts", curCA, false},
		{"from the CA the hint named", next, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
			if err := agent.SaveAgentKey("agent-key"); err != nil {
				t.Fatal(err)
			}
			certBefore, _ := os.ReadFile(filepath.Join(agent.certDir, "node.crt"))
			// The manager trusts both CAs, so the agent's next connection
			// gets in whichever certificate it presents.
			leader := tlsWithRole(t, ca, "m1", CertRoleManager)
			if err := leader.SaveCACert(bundle); err != nil {
				t.Fatal(err)
			}
			mgr := startFakeManager(t, leader, &fakeManager{
				pong: func(self string) (*pb.HeartbeatPong, error) {
					return &pb.HeartbeatPong{FromLeader: true, LeaderGrpcAddress: self, Ca: &pb.ClusterCA{Bundle: bundle, SignPin: next.Pin(), Seq: 1}}, nil
				},
				renew: func(req *pb.RenewCertificateRequest) (*pb.RenewCertificateResponse, error) {
					cert, err := SignNodeCSRBy(tc.signer, "agent-1", []string{"127.0.0.1"}, CertRoleAgent, req.Csr)
					if err != nil {
						return nil, err
					}
					return &pb.RenewCertificateResponse{Success: true, NodeCert: cert, CaBundle: bundle}, nil
				},
			})
			startLink(t, agent, "agent-1", []config.ManagerRef{{ID: "m1", GRPCAddress: mgr.self}})
			waitFor(t, "a renewal", func() bool { return mgr.renews.Load() >= 1 })
			if tc.installed {
				waitFor(t, "the certificate from the next CA", func() bool { return agent.ServedCertCAPin() == next.Pin() })
				return
			}
			time.Sleep(300 * time.Millisecond)
			if certAfter, _ := os.ReadFile(filepath.Join(agent.certDir, "node.crt")); !bytes.Equal(certBefore, certAfter) {
				t.Fatal("a certificate from another CA than the hint's was installed")
			}
			if !strings.Contains(logs.String(), "is not from "+next.Pin()) {
				t.Fatalf("not refused for its CA: %s", logs)
			}
		})
	}
}

// A connection keeps the certificate the manager showed when it was made. A
// manager that moved its own certificate to the next CA since — a renewal's
// new connection a moment ahead of the leader's own move, or a leader
// elected mid-step — goes on showing the old one on it, and a hint without
// that CA is refused there for good: the rotation would wait on this agent
// in a phase that has no time limit. The agent tries a new connection, which
// shows the manager's certificate as it is now, and takes the hint there.
func TestAnAgentTakesAHintOnANewConnectionOnceTheManagerMoved(t *testing.T) {
	_, ca := setupCertDir(t)
	cur, _ := ca.LoadCACert()
	next, _ := GenerateCA("next", time.Now())
	leaderTLS := tlsWithRole(t, ca, "m1", CertRoleManager)
	mgr := startFakeManager(t, leaderTLS, &fakeManager{pong: func(self string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{FromLeader: true, LeaderGrpcAddress: self, Ca: &pb.ClusterCA{Bundle: next.CertPEM, SignPin: next.Pin(), Seq: 3}}, nil
	}})
	agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
	// Both CAs trusted, as after a rotation's trust step.
	if err := agent.SaveCACert(append(append([]byte{}, cur...), next.CertPEM...)); err != nil {
		t.Fatal(err)
	}
	startLink(t, agent, "agent-1", []config.ManagerRef{{ID: "m1", GRPCAddress: mgr.self}})
	waitFor(t, "a session on the manager's old certificate", func() bool { return mgr.streams.Load() >= 1 })
	time.Sleep(300 * time.Millisecond) // pongs with the hint on it
	if p := agent.TrustPins(); len(p) != 2 {
		t.Fatalf("took a bundle without the CA of its connection: %v", p)
	}
	if err := leaderTLS.ReissueNodeCertBy(next, "m1", []string{"127.0.0.1"}, CertRoleManager); err != nil {
		t.Fatal(err)
	}
	if err := leaderTLS.ReloadNodeCert(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to take the hint on a new connection", func() bool {
		p := agent.TrustPins()
		return len(p) == 1 && p[0] == next.Pin()
	})
}
