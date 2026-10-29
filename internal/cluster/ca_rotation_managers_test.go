package cluster

import (
	"fmt"
	"net"
	"slices"
	"testing"
	"time"
)

// Two real managers rotate their CA through every step — the follower moves
// its trust and its certificate from its own FSM and shows them on its
// heartbeat — and then the follower restarts trusting the new CA alone:
// its Raft and its heartbeat come back over new connections, which only the
// new certificates pass.
func TestTwoManagersRotateTheirCAAndRestartOnTheNewOne(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	settleRotation(t, m1)
	m2 := joinedManager(t, addr, token, pin)
	id2 := m2.LocalNodeID()
	waitForVoter(t, m1, id2)
	waitUntil(t, 60*time.Second, "m2's heartbeat to say it follows a CA rotation", func() bool {
		met := m1.GetHeartbeat().GetMetrics(id2)
		return met != nil && slices.Contains(met.Capabilities, CapCARotation) && len(met.CATrustPins) == 1
	})
	rot, err := m1.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	// m2 shows each step on its next heartbeat, thirty seconds apart.
	for _, phase := range []CAPhase{CAPhaseReissue, CAPhasePromote, CAPhaseDone} {
		waitUntil(t, 90*time.Second, "phase "+string(phase), func() bool {
			_ = m1.MaybeAdvanceCARotation()
			return caRecord(t, m1).Phase == phase
		})
	}
	for _, m := range []*Manager{m1, m2} {
		if p, from := m.GetTLS().TrustPins(), m.GetTLS().ServedCertCAPin(); !slices.Equal(p, []string{rot.NextPin}) || from != rot.NextPin {
			t.Fatalf("%s trusts %v and presents a certificate from %q, want %s alone", m.LocalNodeID(), p, from, rot.NextPin)
		}
	}

	// m2 restarts. With two voters there is no quorum until it is back.
	cfg := *m2.GetConfig()
	grpcAddr := fmt.Sprintf("%s:%d", cfg.AdvertiseAddress, cfg.GRPCPort)
	m2.Shutdown() // joinedManager's cleanup stops it again: Shutdown runs once
	waitUntil(t, 30*time.Second, "m2's gRPC address to be free", func() bool {
		l, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			return false
		}
		_ = l.Close()
		return true
	})
	// What m1 knows of the node from now on comes over a new connection.
	m1.GetHeartbeat().RemoveNode(id2)
	m3 := NewManager(&cfg)
	if err := m3.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m3.Shutdown)
	gs, err := NewGRPCServer(m3, 1, stubPanel{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gs.Start(grpcAddr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gs.Stop)
	m3.StartLocalMetrics(func() (float64, float64, float64, int) { return 1, 1, 1, 0 })

	// The restarted node may win an election its absence brought on: m1 is
	// handed leadership back, as the heartbeat checked below goes to the
	// leader.
	waitUntil(t, 60*time.Second, "a leader again", func() bool { return m1.IsLeader() || m3.IsLeader() })
	if m3.IsLeader() {
		if err := m3.raft.TransferLeadership(m1.LocalNodeID()); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, 30*time.Second, "m1 to lead again", m1.IsLeader)
	}
	if err := m1.SetConfig("after-restart", "x"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 60*time.Second, "the restarted m2 to take an entry over Raft", func() bool {
		v, _ := m3.raft.GetFSM().GetConfigValue("after-restart")
		return v == "x"
	})
	waitUntil(t, 60*time.Second, "the restarted m2's heartbeat on a certificate from the new CA", func() bool {
		met := m1.GetHeartbeat().GetMetrics(id2)
		return met != nil && met.CertCAPin == rot.NextPin
	})
}
