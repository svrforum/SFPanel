package websocket

import (
	"errors"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// standInLeader replaces the follower's ask to the leader for one test.
func standInLeader(t *testing.T, answer map[string]interface{}, err error) {
	t.Helper()
	orig := leaderOverview
	leaderOverview = func(*cluster.Manager) (map[string]interface{}, error) { return answer, err }
	t.Cleanup(func() { leaderOverview = orig })
}

// A follower's dashboard showed its own view after the first paint: every
// other node offline with no metrics — the leader included — under a "leader
// unreachable" banner, while the leader answered fine. Only the leader hears
// heartbeats, so a follower pushes the leader's overview.
func TestAFollowersSnapshotIsTheLeadersOverview(t *testing.T) {
	standInLeader(t, map[string]interface{}{
		"name": "lab", "node_count": 2, "leader_id": "node-l", "stale": false,
		"nodes":   []interface{}{map[string]interface{}{"id": "node-l", "status": "online"}, map[string]interface{}{"id": "node-f", "status": "online"}},
		"metrics": []interface{}{map[string]interface{}{"node_id": "node-l"}, map[string]interface{}{"node_id": "node-f"}},
	}, nil)
	follower := cluster.NewManager(&config.ClusterConfig{NodeID: "node-f"})

	snap := buildClusterSnapshot(follower)
	if stale, _ := snap["stale"].(bool); stale {
		t.Fatal("a follower that reached the leader flags its snapshot stale")
	}
	nodes, _ := snap["nodes"].([]interface{})
	if len(nodes) != 2 || snap["leader_id"] != "node-l" || snap["name"] != "lab" {
		t.Fatalf("the snapshot is not the leader's overview: %v", snap)
	}
	if metrics, _ := snap["metrics"].([]interface{}); len(metrics) != 2 {
		t.Fatalf("the snapshot lacks the leader's metrics: %v", snap["metrics"])
	}
	if snap["local_id"] != "node-f" || snap["is_leader"] != false {
		t.Fatalf("the snapshot must still name this node as the follower it is: %v", snap)
	}
}

// When the leader cannot be reached the follower falls back to its own view,
// flagged stale so the dashboard says it may be out of date.
func TestAFollowerThatCannotReachTheLeaderFlagsItsSnapshotStale(t *testing.T) {
	standInLeader(t, nil, errors.New("leader unreachable"))
	follower := cluster.NewManager(&config.ClusterConfig{NodeID: "node-f"})

	snap := buildClusterSnapshot(follower)
	if stale, _ := snap["stale"].(bool); !stale {
		t.Fatalf("a follower's own view must be flagged stale: %v", snap)
	}
}

func TestBuildClusterSnapshot_NilManager(t *testing.T) {
	snap := buildClusterSnapshot(nil)
	if enabled, _ := snap["enabled"].(bool); enabled {
		t.Errorf("nil manager should yield enabled=false, got %v", snap)
	}
	// Must not include node/overview keys when there's no cluster.
	if _, ok := snap["nodes"]; ok {
		t.Error("nil manager snapshot should not carry nodes")
	}
}

func recvSnap(ch <-chan map[string]interface{}) bool {
	select {
	case s, ok := <-ch:
		return ok && s != nil
	case <-time.After(2 * time.Second):
		return false
	}
}

func drainClosedSnap(ch <-chan map[string]interface{}) bool {
	for i := 0; i < 10; i++ {
		select {
		case _, ok := <-ch:
			if !ok {
				return true
			}
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}
	return false
}

// TestOverviewBroadcaster_FanOutAndStop verifies the shared sampler delivers to
// multiple subscribers and stops once the last leaves. getManager returns nil,
// so each snapshot is the disabled-cluster payload — enough to exercise the
// fan-out lifecycle without a running Raft.
func TestOverviewBroadcaster_FanOutAndStop(t *testing.T) {
	clusterOverviewBroadcaster.mu.Lock()
	old := overviewBroadcastInterval
	overviewBroadcastInterval = 10 * time.Millisecond
	clusterOverviewBroadcaster.mu.Unlock()
	defer func() {
		clusterOverviewBroadcaster.mu.Lock()
		overviewBroadcastInterval = old
		clusterOverviewBroadcaster.mu.Unlock()
	}()

	getMgr := func() *cluster.Manager { return nil }

	ch1, unsub1 := subscribeOverview(getMgr)
	ch2, unsub2 := subscribeOverview(getMgr)

	if !recvSnap(ch1) {
		t.Fatal("subscriber 1 received no snapshot")
	}
	if !recvSnap(ch2) {
		t.Fatal("subscriber 2 received no snapshot")
	}

	clusterOverviewBroadcaster.mu.Lock()
	running := clusterOverviewBroadcaster.running
	clusterOverviewBroadcaster.mu.Unlock()
	if !running {
		t.Fatal("sampler should be running while subscribers are attached")
	}

	unsub1()
	unsub2()

	deadline := time.Now().Add(2 * time.Second)
	for {
		clusterOverviewBroadcaster.mu.Lock()
		running = clusterOverviewBroadcaster.running
		clusterOverviewBroadcaster.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sampler did not stop after all subscribers left")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !drainClosedSnap(ch1) {
		t.Error("ch1 was not closed after unsubscribe")
	}

	// Idempotent unsubscribe must not panic.
	unsub1()
}
