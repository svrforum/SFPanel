package cluster

import (
	"strings"
	"testing"
	"time"
)

// healthyManagers starts n managers whose FSM lists each as a manager voter
// recorded online, as a join records it, and returns the leader and its
// followers once every follower has applied that. The leader holds a
// heartbeat stream from each follower, as in production: managers ping only
// the leader, and each records itself.
func healthyManagers(t *testing.T, n int) (*Manager, []*Manager) {
	t.Helper()
	mgrs := inmemManagers(t, n)
	var leader *Manager
	waitFor(t, "a leader", func() bool {
		for _, m := range mgrs {
			if m.raft.IsLeader() {
				leader = m
				return true
			}
		}
		return false
	})
	var followers []*Manager
	for _, m := range mgrs {
		// A join records a node online.
		if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: m.nodeID, Name: m.nodeID, Kind: KindManager, Role: RoleVoter, Status: StatusOnline})}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: m.nodeID})
		if m != leader {
			followers = append(followers, m)
			leader.heartbeat.RecordStreamPing(&NodeMetrics{NodeID: m.nodeID}, true)
		}
	}
	// Raft tells a follower an entry committed with the next one: this one
	// carries the commit index past the records above.
	if err := leader.raft.Barrier(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	for _, f := range followers {
		waitFor(t, f.nodeID+" applying the records", func() bool {
			return len(f.raft.GetFSM().GetState().Nodes) == n
		})
	}
	return leader, followers
}

// Managers ping only the leader, so a follower's heartbeat map holds itself
// alone: every follower's leave was refused as if the others were down
// ("only 0 of 2 remaining voters reachable"), and operators learned to force
// past the guard. A follower judges the voters by the status the leader
// keeps in their records.
func TestAFollowerOfAHealthyClusterMayLeave(t *testing.T) {
	for _, n := range []int{2, 3} {
		leader, followers := healthyManagers(t, n)
		for _, f := range followers {
			if reason, refused := f.WouldDropBelowQuorumOnLeave(); refused {
				t.Fatalf("%d managers: follower %s refused: %s", n, f.nodeID, reason)
			}
		}
		if reason, refused := leader.WouldDropBelowQuorumOnLeave(); refused {
			t.Fatalf("%d managers: the leader refused: %s", n, reason)
		}
	}
}

// One whose peer the leader has marked offline is refused, and told how many
// would be left to vote.
func TestAFollowerWhosePeerIsOfflineMayNotLeave(t *testing.T) {
	leader, followers := healthyManagers(t, 3)
	leaving, peer := followers[0], followers[1]
	if err := leader.raft.Apply(Command{Type: CmdUpdateNode, Value: mustJSON(Node{ID: peer.nodeID, Status: StatusOffline})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := leader.raft.Barrier(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the follower applying the status", func() bool {
		n := leaving.raft.GetFSM().GetState().Nodes[peer.nodeID]
		return n != nil && n.Status == StatusOffline
	})
	reason, refused := leaving.WouldDropBelowQuorumOnLeave()
	if !refused || !strings.Contains(reason, "only 1 of 2 remaining voters reachable (need 2 for quorum)") {
		t.Fatalf("with %s offline: %q %v", peer.nodeID, reason, refused)
	}
}

// The leader keeps judging by its own heartbeat streams, which are live: not
// by the records, which it writes from the same pings a minute late.
func TestTheLeaderJudgesALeaveByItsStreams(t *testing.T) {
	leader, followers := healthyManagers(t, 3)
	gone := followers[1].nodeID
	// The record still says online; the stream ended a moment ago.
	leader.heartbeat.StreamEnded(gone)
	if reason, refused := leader.WouldDropBelowQuorumOnLeave(); !refused || !strings.Contains(reason, "only 1 of 2 remaining voters reachable") {
		t.Fatalf("with %s's stream ended: %q %v", gone, reason, refused)
	}
	// The record says offline; it pings again on a new stream.
	if err := leader.raft.Apply(Command{Type: CmdUpdateNode, Value: mustJSON(Node{ID: gone, Status: StatusOffline})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	leader.heartbeat.RecordStreamPing(&NodeMetrics{NodeID: gone}, true)
	if reason, refused := leader.WouldDropBelowQuorumOnLeave(); refused {
		t.Fatalf("with %s streaming again: %s", gone, reason)
	}
}
