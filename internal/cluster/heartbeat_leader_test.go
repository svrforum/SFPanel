package cluster

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

// A manager that joins pings its leader once its FSM names the leader, not
// at its first send after that, up to thirty seconds on
// (metricsStreamSendInterval): until it pings, the leader lists it offline
// and does not promote it to voter.
func TestAJoinedManagerPingsItsLeaderOnceItKnowsWhereItIs(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	m2 := joinedManager(t, addr, token, pin)
	waitUntil(t, 10*time.Second, "m2 to ping m1", func() bool {
		_, ok := m1.GetHeartbeat().GetLastSeen()[m2.LocalNodeID()]
		return ok
	})
}

// A manager that stays a follower when the leadership moves pings the new
// leader once it knows of it, not at its next send. Until it does, the new
// leader lists it offline (GetNodes) and holds a task run back for it
// (TasksReady: "m3 (not heard from)"); the rig measured 29 s after a
// transfer.
func TestAFollowerPingsTheNewLeaderOnceItKnowsOfIt(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	// As every manager does once it runs: m1 follows too, once it hands over.
	m1.StartLocalMetrics(func() (float64, float64, float64, int) { return 1, 1, 1, 0 })
	m2 := joinedManager(t, addr, token, pin)
	waitForVoter(t, m1, m2.LocalNodeID())
	another, err := m1.CreateJoinToken(10*time.Minute, KindManager)
	if err != nil {
		t.Fatal(err)
	}
	// A follower's heartbeat is the same whether or not it is a voter yet.
	m3 := joinedManager(t, addr, another.Token, pin)
	waitUntil(t, 45*time.Second, "both followers to ping m1", func() bool {
		seen := m1.GetHeartbeat().GetLastSeen()
		_, two := seen[m2.LocalNodeID()]
		_, three := seen[m3.LocalNodeID()]
		return two && three
	})
	if _, ok := m2.GetHeartbeat().GetLastSeen()[m3.LocalNodeID()]; ok {
		t.Fatal("m2 saw m3 ping before it led")
	}

	moved := time.Now()
	if err := m1.raft.TransferLeadership(m2.LocalNodeID()); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	// m3 stayed a follower; m1 became one. Each next sends thirty seconds
	// after its last ping to m1.
	waitUntil(t, 5*time.Second, "m3 and m1 to ping m2, the new leader", func() bool {
		seen := m2.GetHeartbeat().GetLastSeen()
		three, ok3 := seen[m3.LocalNodeID()]
		one, ok1 := seen[m1.LocalNodeID()]
		return m2.IsLeader() && ok3 && three.After(moved) && ok1 && one.After(moved)
	})
	if got := m2.TasksReady(); len(got) != 0 {
		t.Errorf("the new leader waits for %v", got)
	}
}

// A leader that hands its role over names the new one to the agents on its
// heartbeat streams at once. An agent pings every thirty seconds, and until
// it moves the new leader lists it offline and leaves it out of a run.
func TestAnAgentMovesToTheNewLeaderOnceTheOldOneHandsOver(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	res, tm := registeredAgent(t, m1)
	link, err := NewAgentLink(AgentLinkConfig{
		NodeID: res.NodeID, TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"), Version: "test",
		// As in production: the old leader's word moves it, not a ping.
		Interval: 30 * time.Second, Collect: func() (float64, float64, float64, int) { return 1, 2, 3, 4 },
		Advertise: "192.0.2.10",
	}, []config.ManagerRef{{ID: m1.LocalNodeID(), GRPCAddress: addr}})
	if err != nil {
		t.Fatal(err)
	}
	goRun(t, link)
	waitUntil(t, 10*time.Second, "m1 to record the agent", func() bool { return m1.GetHeartbeat().GetMetrics(res.NodeID) != nil })
	m2 := joinedManager(t, addr, token, pin)
	waitForVoter(t, m1, m2.LocalNodeID())

	moved := time.Now()
	if err := m1.raft.TransferLeadership(m2.LocalNodeID()); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	waitUntil(t, 5*time.Second, "the agent to ping m2, the new leader", func() bool {
		at, ok := m2.GetHeartbeat().GetLastSeen()[res.NodeID]
		return m2.IsLeader() && ok && at.After(moved)
	})
}
