package system

import (
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// Agents and managers still joining do not vote, so they do not count
// toward the quorum an update must keep.
func TestCountOnlineManagerVoters(t *testing.T) {
	nodes := map[string]*cluster.Node{
		"m2": {ID: "m2", Role: cluster.RoleVoter, Kind: cluster.KindManager},
		"m3": {ID: "m3", Role: cluster.RoleVoter}, // from before Kind existed
		"a1": {ID: "a1", Kind: cluster.KindAgent},
		"j1": {ID: "j1", Role: cluster.RoleNonVoter, Kind: cluster.KindManager},
	}
	health := map[string]cluster.NodeStatus{
		"self": cluster.StatusOnline, "m2": cluster.StatusOnline, "m3": cluster.StatusOnline,
		"a1": cluster.StatusOnline, "j1": cluster.StatusOnline,
	}
	got := countOnlineManagerVoters("self", health, func(id string) *cluster.Node { return nodes[id] })
	if got != 3 { // self, m2, m3
		t.Fatalf("online manager voters = %d, want 3", got)
	}
}

// The quorum an update must keep is a majority of every manager voter,
// online or not; agents and managers still joining are not among them.
func TestCountManagerVoters(t *testing.T) {
	nodes := []*cluster.Node{
		{ID: "self", Role: cluster.RoleVoter, Kind: cluster.KindManager},
		{ID: "m2", Role: cluster.RoleVoter, Kind: cluster.KindManager, Status: cluster.StatusOffline},
		{ID: "m3", Role: cluster.RoleVoter}, // from before Kind existed
		{ID: "a1", Kind: cluster.KindAgent},
		{ID: "j1", Role: cluster.RoleNonVoter, Kind: cluster.KindManager},
	}
	if got := countManagerVoters("self", nodes); got != 3 {
		t.Fatalf("manager voters = %d, want 3", got)
	}
	// This node counts even before the FSM lists it.
	if got := countManagerVoters("self", nodes[1:]); got != 3 {
		t.Fatalf("manager voters without self listed = %d, want 3", got)
	}
}
