package cluster

import (
	"testing"
	"time"
)

// promoteRateLimit is purely table-keeping — no Raft involved — so it can
// be tested directly without spinning up a real cluster.
func TestPromoteRateLimit(t *testing.T) {
	m := &Manager{}

	if !m.promoteRateLimit("node-a") {
		t.Fatal("first attempt for node-a should be allowed")
	}
	// Immediate second attempt: refused.
	if m.promoteRateLimit("node-a") {
		t.Error("second immediate attempt for node-a should be refused (cooldown)")
	}
	// Different nodeID: allowed.
	if !m.promoteRateLimit("node-b") {
		t.Error("first attempt for node-b should be allowed (independent bucket)")
	}

	// Force the cooldown to elapse by rewriting the timestamp directly.
	m.promoteMu.Lock()
	m.promoteAttempts["node-a"] = time.Now().Add(-10 * time.Second)
	m.promoteMu.Unlock()
	if !m.promoteRateLimit("node-a") {
		t.Error("attempt after cooldown should be allowed again")
	}
}

func TestPromoteRateLimit_GCsOldEntries(t *testing.T) {
	m := &Manager{}
	m.promoteRateLimit("node-1") // populate

	// Stuff in many "old" entries to trigger the GC pass.
	m.promoteMu.Lock()
	old := time.Now().Add(-2 * time.Minute)
	for i := 0; i < 100; i++ {
		m.promoteAttempts[string(rune('a'+i%26))+"-old"] = old
	}
	m.promoteMu.Unlock()

	// Trigger GC by another call.
	m.promoteRateLimit("node-trigger-gc")

	m.promoteMu.Lock()
	defer m.promoteMu.Unlock()
	for k, t2 := range m.promoteAttempts {
		if time.Since(t2) > 1*time.Minute {
			t.Errorf("entry %q with age %v should have been GC'd", k, time.Since(t2))
		}
	}
}
