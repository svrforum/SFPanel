package featurecluster

import (
	"reflect"
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
)

func ids(nodes []updateNode) []string {
	out := []string{}
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// Agents go first, in waves, so a bad release stops on them before any
// manager restarts; then the other managers one at a time; this node — the
// one running the update — last. Within each group by name, so a run takes
// the same order every time: map order made it random.
func TestPlanClusterUpdate(t *testing.T) {
	state := cluster.ClusterState{Nodes: map[string]*cluster.Node{
		"m1": {ID: "m1", Name: "m1", Kind: cluster.KindManager, Role: cluster.RoleVoter},
		"m3": {ID: "m3", Name: "m3", Kind: cluster.KindManager, Role: cluster.RoleVoter},
		"m2": {ID: "m2", Name: "m2", Kind: cluster.KindManager, Role: cluster.RoleVoter},
		"a3": {ID: "a3", Name: "web-3", Kind: cluster.KindAgent},
		"a1": {ID: "a1", Name: "web-1", Kind: cluster.KindAgent},
		"a2": {ID: "a2", Name: "web-2", Kind: cluster.KindAgent},
		"a4": {ID: "a4", Name: "web-4", Kind: cluster.KindAgent},
		"a5": {ID: "a5", Name: "web-5", Kind: cluster.KindAgent},
	}}
	health := map[string]cluster.NodeStatus{}
	versions := map[string]string{}
	for id := range state.Nodes {
		health[id] = cluster.StatusOnline
		versions[id] = "0.80.0"
	}
	health["a4"] = cluster.StatusOffline
	versions["a5"] = "0.81.0"

	p := planClusterUpdate(state, health, versions, "m1", "v0.81.0", 2)
	var waves [][]string
	for _, w := range p.Waves {
		waves = append(waves, ids(w))
	}
	if !reflect.DeepEqual(waves, [][]string{{"a1", "a2"}, {"a3"}}) {
		t.Errorf("waves %v", waves)
	}
	if got := ids(p.Managers); !reflect.DeepEqual(got, []string{"m2", "m3"}) {
		t.Errorf("managers %v", got)
	}
	if p.Leader == nil || p.Leader.ID != "m1" {
		t.Errorf("leader %+v", p.Leader)
	}
	if got := ids(p.Skipped); !reflect.DeepEqual(got, []string{"a4"}) {
		t.Errorf("skipped %v", got)
	}
	if got := ids(p.Current); !reflect.DeepEqual(got, []string{"a5"}) {
		t.Errorf("current %v", got)
	}

	// A wave of fewer than one is one; a leader on the target already is not
	// asked to update.
	versions["m1"] = "v0.81.0"
	p = planClusterUpdate(state, health, versions, "m1", "0.81.0", 0)
	if len(p.Waves) != 3 || p.Leader != nil {
		t.Errorf("wave size 0: %d waves, leader %+v", len(p.Waves), p.Leader)
	}
}

func TestSameVersion(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.81.0", "v0.81.0", true}, {"v0.81.0", "v0.81.0", true}, {"0.80.0", "0.81.0", false}, {"", "0.81.0", false}, {"", "", false}} {
		if got := sameVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("sameVersion(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

// A node the leader's heartbeats show offline is skipped whatever version it
// last reported: counted current, the stream said it was online, and the
// leader offered it as the manager to hand leadership to.
func TestPlanSkipsAnOfflineNodeOnTheTarget(t *testing.T) {
	state := cluster.ClusterState{Nodes: map[string]*cluster.Node{
		"m1": {ID: "m1", Name: "m1", Kind: cluster.KindManager, Role: cluster.RoleVoter},
		"m2": {ID: "m2", Name: "m2", Kind: cluster.KindManager, Role: cluster.RoleVoter},
		"m3": {ID: "m3", Name: "m3", Kind: cluster.KindManager, Role: cluster.RoleVoter},
	}}
	health := map[string]cluster.NodeStatus{"m1": cluster.StatusOnline, "m2": cluster.StatusOffline, "m3": cluster.StatusOnline}
	versions := map[string]string{"m1": "0.80.0", "m2": "0.81.0", "m3": "0.81.0"}
	p := planClusterUpdate(state, health, versions, "m1", "v0.81.0", 1)
	if got := ids(p.Skipped); !reflect.DeepEqual(got, []string{"m2"}) {
		t.Errorf("skipped %v", got)
	}
	if got := ids(p.Current); !reflect.DeepEqual(got, []string{"m3"}) {
		t.Errorf("current %v", got)
	}
	if p.Leader == nil || p.Leader.ID != "m1" {
		t.Errorf("leader %+v", p.Leader)
	}
}
