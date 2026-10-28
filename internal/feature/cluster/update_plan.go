package featurecluster

import (
	"sort"
	"strings"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// updateNode is a node as a cluster update sees it.
type updateNode struct {
	ID, Name, Version string
	Manager, Voter    bool
}

// updatePlan is the order a cluster update takes nodes in.
type updatePlan struct {
	Waves    [][]updateNode // agents, wave by wave
	Managers []updateNode   // the other managers, one at a time
	Leader   *updateNode    // this node, last; nil when it runs the target already
	Skipped  []updateNode   // offline by the leader's heartbeats, whatever they last ran: not asked
	Current  []updateNode   // online and running the target already: not asked
}

// planClusterUpdate orders a cluster update: agents first, waveSize at a
// time, so a bad release stops on agents before any manager restarts; then
// the other managers one at a time; then this node, which runs the update.
// Within each group nodes go by name, then ID, so a run takes the same order
// every time. A wave of fewer than one is one.
func planClusterUpdate(state cluster.ClusterState, health map[string]cluster.NodeStatus, versions map[string]string, localID, target string, waveSize int) updatePlan {
	if waveSize < 1 {
		waveSize = 1
	}
	var plan updatePlan
	var agents, managers []updateNode
	for id, n := range state.Nodes {
		un := updateNode{ID: id, Name: n.Name, Version: versions[id], Manager: n.IsManager(), Voter: n.Role == cluster.RoleVoter}
		// Health before version: the version of an offline node is only
		// what it last reported, and one counted current would be announced
		// online and offered the leadership.
		switch {
		case id == localID && sameVersion(un.Version, target):
			plan.Current = append(plan.Current, un)
		case id == localID:
			local := un
			plan.Leader = &local
		case health[id] != cluster.StatusOnline:
			plan.Skipped = append(plan.Skipped, un)
		case sameVersion(un.Version, target):
			plan.Current = append(plan.Current, un)
		case un.Manager:
			managers = append(managers, un)
		default:
			agents = append(agents, un)
		}
	}
	for _, s := range [][]updateNode{agents, managers, plan.Skipped, plan.Current} {
		sortByName(s)
	}
	for len(agents) > 0 {
		k := min(waveSize, len(agents))
		plan.Waves = append(plan.Waves, agents[:k])
		agents = agents[k:]
	}
	plan.Managers = managers
	return plan
}

func sortByName(s []updateNode) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Name != s[j].Name {
			return s[i].Name < s[j].Name
		}
		return s[i].ID < s[j].ID
	})
}

// sameVersion compares versions with or without a leading v. An unknown
// version is never the same as anything.
func sameVersion(a, b string) bool {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	return a != "" && a == b
}
