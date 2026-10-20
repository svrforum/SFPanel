package cluster

import (
	"errors"
	"testing"

	"github.com/hashicorp/raft"
)

func TestPromotionReady(t *testing.T) {
	u := func(v uint64) *uint64 { return &v }
	for _, tc := range []struct {
		name          string
		applied       *uint64
		leaderApplied uint64
		want          bool
	}{
		{"a joiner from before the field: promoted as before", nil, 500, true},
		{"its Raft never received an entry", u(0), 500, false},
		{"never received one, with the leader's own index under the lag", u(0), 10, false},
		{"far behind", u(100), 500, false},
		{"caught up, a few entries behind", u(450), 500, true},
		{"level with the leader", u(500), 500, true},
	} {
		if got := promotionReady(tc.applied, tc.leaderApplied); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A joining manager whose Raft never caught up — the leader cannot reach its
// Raft port — stays a non-voter however often it heartbeats. Promoted, it
// took a one-manager cluster's leadership with it: the leader could no longer
// reach a majority of two.
func TestAManagerWhoseRaftIsBehindIsNotPromoted(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleNonVoter, GRPCAddress: "127.0.0.1:1"})
	if err := mgr.raft.AddNonvoter("m2", "127.0.0.1:2"); err != nil {
		t.Fatal(err)
	}
	zero := uint64(0)
	mgr.PromoteOnHeartbeatIfPending("m2", &zero)
	if s, in, err := mgr.raft.Suffrage("m2"); err != nil || !in || s != raft.Nonvoter {
		t.Fatalf("m2 is %v (listed %v, %v)", s, in, err)
	}
	if !mgr.IsLeader() {
		t.Fatal("the leader lost its leadership")
	}
}

// A record the FSM has and Raft does not list is not a joiner. Re-adding it as
// a voter undid a removal whose second write had failed, and took the quorum
// when the node was gone.
func TestConfirmJoinDoesNotReAddANodeRaftDoesNotList(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "ghost", Kind: KindManager, Role: RoleNonVoter, GRPCAddress: "127.0.0.1:1"})
	if err := mgr.ConfirmJoin("ghost"); !errors.Is(err, ErrNotRaftNonvoter) {
		t.Fatalf("got %v", err)
	}
	if _, in, _ := mgr.raft.Suffrage("ghost"); in {
		t.Fatal("ghost was added to Raft")
	}
	if !mgr.IsLeader() {
		t.Fatal("the leader lost its leadership")
	}
}

// A promotion whose role write failed leaves a Raft voter the FSM calls a
// non-voter. The node's next heartbeat writes the role, and only the role.
// The leader stands in for that node: the one voter of a one-manager cluster.
func TestAFailedRoleWriteHealsOnTheNextHeartbeat(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	n := *mgr.raft.GetFSM().GetNode(mgr.LocalNodeID())
	n.Role = RoleNonVoter
	addTestNode(t, mgr.raft.GetFSM(), n)
	mgr.PromoteOnHeartbeatIfPending(n.ID, nil)
	got := mgr.raft.GetFSM().GetNode(n.ID)
	if got == nil {
		t.Fatal("the node's record is gone")
	}
	if got.Role != RoleVoter {
		t.Fatalf("the FSM role stayed %q", got.Role)
	}
}
