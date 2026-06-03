package websocket

import (
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
)

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
