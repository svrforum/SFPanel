package cluster

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func applyCmd(t *testing.T, fsm *FSM, cmd Command) {
	t.Helper()
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if res := fsm.Apply(&raft.Log{Data: data}); res != nil {
		t.Fatalf("Apply returned non-nil: %v", res)
	}
}

// CmdSetRecoveryCodes stores the hash list under the username, an empty list
// clears it, and the codes survive a GetState copy (which feeds snapshots).
func TestFSM_CmdSetRecoveryCodes(t *testing.T) {
	fsm := NewFSM()
	hashes := []string{"h1", "h2", "h3"}

	applyCmd(t, fsm, Command{Type: CmdSetRecoveryCodes, Key: "alice", Value: mustJSON(hashes)})

	if got := fsm.GetRecoveryCodes("alice"); len(got) != 3 || got[0] != "h1" {
		t.Fatalf("GetRecoveryCodes = %v, want %v", got, hashes)
	}

	// GetState deep-copies the map (snapshot path).
	if st := fsm.GetState(); len(st.RecoveryCodes["alice"]) != 3 {
		t.Errorf("GetState dropped recovery codes: %v", st.RecoveryCodes)
	}

	// Mutating the returned slice must not affect FSM state.
	got := fsm.GetRecoveryCodes("alice")
	got[0] = "tampered"
	if fsm.GetRecoveryCodes("alice")[0] != "h1" {
		t.Error("GetRecoveryCodes returned an aliased slice")
	}

	// Empty list clears the entry.
	applyCmd(t, fsm, Command{Type: CmdSetRecoveryCodes, Key: "alice", Value: mustJSON([]string{})})
	if got := fsm.GetRecoveryCodes("alice"); len(got) != 0 {
		t.Errorf("after clear: %v, want empty", got)
	}
}

// A CmdSetAccount (password/TOTP change) must NOT wipe recovery codes — the
// whole point of decoupling them into a separate command/map.
func TestFSM_SetAccountPreservesRecoveryCodes(t *testing.T) {
	fsm := NewFSM()
	applyCmd(t, fsm, Command{Type: CmdSetRecoveryCodes, Key: "alice", Value: mustJSON([]string{"h1", "h2"})})
	applyCmd(t, fsm, Command{Type: CmdSetAccount, Key: "alice", Value: mustJSON(AdminAccount{Username: "alice", Password: "newhash"})})

	if got := fsm.GetRecoveryCodes("alice"); len(got) != 2 {
		t.Errorf("CmdSetAccount wiped recovery codes: %v", got)
	}
	if acct := fsm.GetAccount("alice"); acct == nil || acct.Password != "newhash" {
		t.Errorf("account not updated: %+v", acct)
	}
}

// L-04: applying a CmdDisband log entry fires the registered onDisband
// callback with the originating node ID and does not block the Apply loop.
func TestFSM_CmdDisbandFiresCallback(t *testing.T) {
	fsm := NewFSM()
	gotCh := make(chan string, 1)
	fsm.SetOnDisband(func(fromNodeID string) {
		gotCh <- fromNodeID
	})

	cmd := Command{Type: CmdDisband, Key: "leader-node-42"}
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if res := fsm.Apply(&raft.Log{Data: data}); res != nil {
		t.Fatalf("Apply returned non-nil result: %v", res)
	}

	select {
	case got := <-gotCh:
		if got != "leader-node-42" {
			t.Fatalf("callback got %q, want %q", got, "leader-node-42")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("onDisband callback was not invoked within 2s")
	}
}

// L-04: if no callback is registered, CmdDisband applies cleanly as a no-op
// (so replaying a log on a node without a wired handler doesn't break).
func TestFSM_CmdDisbandWithoutCallbackIsNoop(t *testing.T) {
	fsm := NewFSM()
	// Intentionally do not SetOnDisband.

	cmd := Command{Type: CmdDisband, Key: "whatever"}
	data, _ := json.Marshal(cmd)
	if res := fsm.Apply(&raft.Log{Data: data}); res != nil {
		t.Fatalf("Apply should no-op when callback missing, got: %v", res)
	}
}

// A CmdDisband that is being REPLAYED from the persisted log at startup
// (Index <= replay threshold) must NOT fire the teardown callback — otherwise
// a node that booted with a stale committed disband would self-destruct on
// every restart. A live disband (Index > threshold) still fires.
func TestFSM_CmdDisbandReplaySuppressed(t *testing.T) {
	fsm := NewFSM()
	fired := make(chan string, 1)
	fsm.SetOnDisband(func(id string) { fired <- id })
	fsm.SetReplayThreshold(100) // everything up to index 100 is historical

	data, _ := json.Marshal(Command{Type: CmdDisband, Key: "leader"})

	// Replayed entry (index within the historical range) → suppressed.
	fsm.Apply(&raft.Log{Index: 100, Data: data})
	select {
	case <-fired:
		t.Fatal("replayed CmdDisband (Index<=threshold) must not fire onDisband")
	case <-time.After(300 * time.Millisecond):
	}

	// Live entry (index past the threshold) → fires.
	fsm.Apply(&raft.Log{Index: 101, Data: data})
	select {
	case got := <-fired:
		if got != "leader" {
			t.Fatalf("callback got %q, want %q", got, "leader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live CmdDisband (Index>threshold) did not fire onDisband")
	}
}

// L-05: the leadership handoff before a leave or shutdown goes to an online
// manager voter other than this node: Raft refuses a non-voter, and an agent
// is never a member, whatever role its record carries.
func TestManager_pickTransferTarget(t *testing.T) {
	fsm := NewFSM()
	addTestNode(t, fsm, Node{ID: "self", Kind: KindManager, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "agent", Kind: KindAgent, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "joining", Kind: KindManager, Role: RoleNonVoter})
	addTestNode(t, fsm, Node{ID: "down", Kind: KindManager, Role: RoleVoter})
	hb := NewHeartbeatManager(time.Minute, 3*time.Minute)
	for _, id := range []string{"self", "agent", "joining"} {
		hb.RecordHeartbeat(&NodeMetrics{NodeID: id})
	}
	m := &Manager{nodeID: "self", raft: &RaftNode{fsm: fsm}, heartbeat: hb}
	if got := m.pickTransferTarget(); got != "" {
		t.Fatalf("picked %q with no other online manager voter", got)
	}
	addTestNode(t, fsm, Node{ID: "m2", Role: RoleVoter}) // from before Kind existed
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m2"})
	if got := m.pickTransferTarget(); got != "m2" {
		t.Fatalf("picked %q, want m2", got)
	}
}

// A handover goes to the manager that reaches the most agents, so the agents
// keep a leader they can dial; ties go by name, and a manager whose heartbeat
// stream has ended is not offered it, though its last ping is recent.
func TestPickTransferTargetPrefersTheManagerThatReachesMore(t *testing.T) {
	fsm := NewFSM()
	addTestNode(t, fsm, Node{ID: "self", Name: "self", Kind: KindManager, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "m3", Name: "m3", Kind: KindManager, Role: RoleVoter})
	hb := NewHeartbeatManager(time.Minute, 3*time.Minute)
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m2", ReachableAgents: []string{}})
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m3", ReachableAgents: []string{"a1", "a2"}})
	m := &Manager{nodeID: "self", raft: &RaftNode{fsm: fsm}, heartbeat: hb}
	if got := m.pickTransferTarget(); got != "m3" {
		t.Fatalf("picked %q, want m3: it reaches 2 agents, m2 none", got)
	}

	// m4 reaches the most, but its stream ended: by LiveHealth it is offline.
	addTestNode(t, fsm, Node{ID: "m4", Name: "m4", Kind: KindManager, Role: RoleVoter})
	hb.RecordStreamPing(&NodeMetrics{NodeID: "m4", ReachableAgents: []string{"a1", "a2", "a3"}}, true)
	hb.StreamEnded("m4")
	if got := m.pickTransferTarget(); got != "m3" {
		t.Fatalf("picked %q, want m3: m4's heartbeat stream has ended", got)
	}

	// m5 reaches as many as m3 and its name comes first; a manager without
	// a name sorts by its ID.
	addTestNode(t, fsm, Node{ID: "m5", Name: "alpha", Kind: KindManager, Role: RoleVoter})
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m5", ReachableAgents: []string{"a1", "a2"}})
	if got := m.pickTransferTarget(); got != "m5" {
		t.Fatalf("picked %q, want m5: it reaches as many as m3, and alpha comes before m3", got)
	}
	addTestNode(t, fsm, Node{ID: "a-unnamed", Kind: KindManager, Role: RoleVoter})
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "a-unnamed", ReachableAgents: []string{"a1", "a2"}})
	if got := m.pickTransferTarget(); got != "a-unnamed" {
		t.Fatalf("picked %q, want a-unnamed: with no name, its ID a-unnamed comes before alpha", got)
	}
}

// A manager whose reach is unknown is offered a handover after one that
// reaches none: none is still known.
func TestPickTransferTargetPutsAnUnknownReachLast(t *testing.T) {
	fsm := NewFSM()
	addTestNode(t, fsm, Node{ID: "self", Name: "self", Kind: KindManager, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "m3", Name: "m3", Kind: KindManager, Role: RoleVoter})
	hb := NewHeartbeatManager(time.Minute, 3*time.Minute)
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m2"}) // no reach reported
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m3", ReachableAgents: []string{}})
	m := &Manager{nodeID: "self", raft: &RaftNode{fsm: fsm}, heartbeat: hb}
	if got := m.pickTransferTarget(); got != "m3" {
		t.Fatalf("picked %q, want m3: m2's reach is unknown, m3's is none", got)
	}
}

// ReachCount answers this manager's own fresh probe, another's last ping, and
// -1 for what it does not know.
func TestReachCount(t *testing.T) {
	hb := NewHeartbeatManager(time.Minute, 3*time.Minute)
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m2", ReachableAgents: []string{"a1", "a2"}})
	hb.RecordHeartbeat(&NodeMetrics{NodeID: "m3"})
	m := &Manager{nodeID: "self", heartbeat: hb}
	for id, want := range map[string]int{"self": -1, "m2": 2, "m3": -1, "gone": -1} {
		if got := m.ReachCount(id); got != want {
			t.Errorf("ReachCount(%q) = %d, want %d", id, got, want)
		}
	}
	m.reach.Store(&reachResult{Agents: map[string]bool{"a1": true, "a2": false}, At: time.Now()})
	if got := m.ReachCount("self"); got != 1 {
		t.Errorf("ReachCount(self) with a fresh probe that reached a1 = %d, want 1", got)
	}
	m.reach.Store(&reachResult{Agents: map[string]bool{"a1": false}, At: time.Now()})
	if got := m.ReachCount("self"); got != 0 {
		t.Errorf("ReachCount(self) with a fresh probe that reached none = %d, want 0", got)
	}
	m.reach.Store(&reachResult{Agents: map[string]bool{"a1": true}, At: time.Now().Add(-time.Hour)})
	if got := m.ReachCount("self"); got != -1 {
		t.Errorf("ReachCount(self) with a stale probe = %d, want -1", got)
	}
}
