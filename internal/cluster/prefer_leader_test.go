package cluster

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// Every row would hand over but for the one thing it changes, and names the
// reason it does not, or whom it hands to.
func TestChoosePreferredLeader(t *testing.T) {
	now := time.Now()
	base := func() preferInput {
		return preferInput{
			Self:        "self",
			Now:         now,
			LeaderSince: now.Add(-time.Hour),
			Agents:      []string{"a1", "a2"},
			Reach: map[string][]string{
				"self": {"a2"},
				"m1":   {"a1", "a2"},
			},
			Candidates: []string{"m1", "m2"},
			Names:      map[string]string{"self": "self", "m1": "zeta", "m2": "alpha"},
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*preferInput)
		target string
		reason string
	}{
		{"it hands over to a manager that reaches more", func(in *preferInput) {}, "m1", ""},
		{"not while held", func(in *preferInput) { in.Hold = "cluster update running" }, "", "held: cluster update running"},
		{"not ten seconds after its election", func(in *preferInput) { in.LeaderSince = now.Add(-10 * time.Second) }, "", "just elected"},
		{"not five minutes after a hand-over", func(in *preferInput) { in.LastTransfer = now.Add(-5 * time.Minute) }, "", "transferred recently"},
		{"not when its own reach is unknown", func(in *preferInput) { in.Reach["self"] = nil }, "", "own reach unknown"},
		{"not with no agents", func(in *preferInput) { in.Agents = nil }, "", "reaches every agent"},
		{"not when it reaches every agent", func(in *preferInput) { in.Reach["self"] = []string{"a1", "a2"} }, "", "reaches every agent"},
		{"not to a manager that reaches as many, other agents", func(in *preferInput) {
			in.Reach["self"] = []string{"a1"}
			in.Reach["m1"] = []string{"a2"}
		}, "", "no manager reaches more"},
		{"of two that reach as many, the first by name", func(in *preferInput) {
			in.Reach["self"] = []string{"a1"}
			in.Reach["m2"] = []string{"a1", "a2"}
		}, "m2", ""},
		{"not to a manager whose reach is unknown (an older one)", func(in *preferInput) {
			in.Reach["self"] = []string{}
			in.Reach["m1"] = nil
		}, "", "no manager reaches more"},
		{"not to a manager that is no candidate", func(in *preferInput) { in.Candidates = []string{"m2"} }, "", "no manager reaches more"},
		{"agents no longer in the cluster count for nobody", func(in *preferInput) {
			in.Reach["self"] = []string{"a1"}
			in.Reach["m1"] = []string{"a1", "gone"}
		}, "", "no manager reaches more"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.change(&in)
			target, reason := choosePreferredLeader(in)
			if target != tc.target {
				t.Fatalf("target %q (reason %q), want %q", target, reason, tc.target)
			}
			if tc.reason != "" && reason != tc.reason {
				t.Fatalf("reason %q, want %q", reason, tc.reason)
			}
			if tc.target != "" && reason == "" {
				t.Fatal("a hand-over gives no reason")
			}
		})
	}
}

// preferScenario is three managers on raft's in-memory transport and one
// agent that the leader's fresh probe did not reach and another manager's
// did; the third manager's reach is unknown. The leader was elected long
// enough ago to hand over. It returns the leader and the manager that
// reaches the agent.
func preferScenario(t *testing.T) (leader, reaches *Manager) {
	t.Helper()
	mgrs := inmemManagers(t, 3)
	for deadline := time.Now().Add(5 * time.Second); leader == nil && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, m := range mgrs {
			if m.raft.IsLeader() {
				leader = m
			}
		}
	}
	if leader == nil {
		t.Fatal("no leader")
	}
	leader.events = NewEventBus()
	for _, m := range mgrs {
		if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: m.nodeID, Name: m.nodeID, Kind: KindManager, Role: RoleVoter, Status: StatusOnline})}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a1", Name: "a1", Kind: KindAgent, GRPCAddress: "192.0.2.10:3629"})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	leader.reach.Store(&reachResult{Agents: map[string]bool{"a1": false}, At: time.Now()})
	// Probed again before a hand-over, a1 still does not answer the leader.
	leader.reachDial = refusingDial
	for _, m := range mgrs {
		switch {
		case m == leader:
		case reaches == nil:
			reaches = m
			leader.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: m.nodeID, ReachableAgents: []string{"a1"}})
		default:
			leader.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: m.nodeID})
		}
	}
	leader.preferMu.Lock()
	leader.leaderSince = time.Now().Add(-31 * time.Second)
	leader.preferMu.Unlock()
	return leader, reaches
}

// A leader whose probe does not reach an agent that another manager reaches
// hands its role to that manager.
func TestALeaderThatCannotReachAnAgentHandsOver(t *testing.T) {
	leader, reaches := preferScenario(t)
	leader.MaybePreferLeader()
	waitUntil(t, 10*time.Second, reaches.nodeID+" to lead", func() bool { return reaches.raft.IsLeader() })

	leader.preferMu.Lock()
	last := leader.lastTransfer
	leader.preferMu.Unlock()
	if last.IsZero() {
		t.Fatal("the hand-over was not recorded: the next tick could hand over again at once")
	}
}

// refusingDial is an agent that does not answer.
func refusingDial(ctx context.Context, network, addr string) (net.Conn, error) {
	return nil, errors.New("connection refused")
}

// answeringDial is an agent that answers.
func answeringDial(ctx context.Context, network, addr string) (net.Conn, error) {
	c, s := net.Pipe()
	_ = s.Close()
	return c, nil
}

// The probe that says the leader misses an agent can be a minute old, and an
// agent that was down then — restarting, say — is reached first by whichever
// manager probes once it is back. The leader probes again before it hands
// over, and keeps its role when it reaches every agent after all.
func TestALeaderThatReachesTheAgentAgainKeepsItsRole(t *testing.T) {
	leader, _ := preferScenario(t)
	leader.reachDial = answeringDial
	if target, reason := leader.preferLeader(); target != "" || reason != "reaches every agent" {
		t.Fatalf("preferLeader = %q, %q; want no target, reaches every agent", target, reason)
	}
	time.Sleep(time.Second)
	if !leader.raft.IsLeader() {
		t.Fatal("the leader handed over on a probe from before the agent came back")
	}
}

// A cluster update and a CA rotation each stop if the leader changes, so
// while either runs the leader keeps its role and says why.
func TestNoHandOverWhileHeld(t *testing.T) {
	t.Run("a cluster update", func(t *testing.T) {
		leader, _ := preferScenario(t)
		leader.SetLeadershipHold(func() string { return "cluster update running" })
		target, reason := leader.preferLeader()
		if target != "" || reason != "held: cluster update running" {
			t.Fatalf("preferLeader = %q, %q; want no target, held: cluster update running", target, reason)
		}
		time.Sleep(time.Second)
		if !leader.raft.IsLeader() {
			t.Fatal("the leader handed over while a cluster update ran")
		}
	})
	t.Run("a CA rotation", func(t *testing.T) {
		leader, _ := preferScenario(t)
		if err := leader.SetConfig(configKeyCARotation, string(mustJSON(CARotation{Seq: 1, Phase: CAPhaseTrust}))); err != nil {
			t.Fatal(err)
		}
		target, reason := leader.preferLeader()
		if target != "" || reason != "held: cluster CA rotation running" {
			t.Fatalf("preferLeader = %q, %q; want no target, held: cluster CA rotation running", target, reason)
		}
		time.Sleep(time.Second)
		if !leader.raft.IsLeader() {
			t.Fatal("the leader handed over while a CA rotation ran")
		}
	})
}

// An agent registered since the leader's last probe is one the leader has
// no verdict on, while a manager that probed after the registration reaches
// it: the leader's own reach is unknown until its next probe, or every new
// agent would move the leader.
func TestANewAgentDoesNotMoveTheLeader(t *testing.T) {
	leader, reaches := preferScenario(t)
	if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a2", Name: "a2", Kind: KindAgent, GRPCAddress: "192.0.2.11:3629"})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	leader.reach.Store(&reachResult{Agents: map[string]bool{"a1": true}, At: time.Now()})
	leader.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: reaches.nodeID, ReachableAgents: []string{"a1", "a2"}})

	if target, reason := leader.preferLeader(); target != "" || reason != "own reach unknown" {
		t.Fatalf("preferLeader = %q, %q; want no target, own reach unknown", target, reason)
	}
}

// A hand-over that fails is not tried again on the next tick, only after
// the cooldown.
func TestAFailedHandOverWaitsForTheCooldown(t *testing.T) {
	leader, reaches := preferScenario(t)
	// TransferLeadership refuses a target its record says is not online.
	if err := leader.raft.Apply(Command{Type: CmdUpdateNode, Value: mustJSON(Node{ID: reaches.nodeID, Status: StatusOffline})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)
	if target, _ := leader.preferLeader(); target != reaches.nodeID {
		t.Fatalf("the first try handed to %q, want %q", target, reaches.nodeID)
	}
	if !strings.Contains(logs.String(), "could not hand leadership to a manager that reaches more agents") {
		t.Fatalf("the failure was not logged: %s", logs.String())
	}
	if target, reason := leader.preferLeader(); target != "" || reason != "transferred recently" {
		t.Fatalf("the next tick: %q, %q; want no target, transferred recently", target, reason)
	}
	if !leader.raft.IsLeader() {
		t.Fatal("the leader lost its role to a target refused")
	}
}
