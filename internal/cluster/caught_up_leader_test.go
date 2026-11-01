package cluster

import (
	"testing"
	"time"
)

func TestOnlyTheLeaderIsACaughtUpLeader(t *testing.T) {
	mgrs := inmemManagers(t, 2)
	var leader, follower *Manager
	for deadline := time.Now().Add(5 * time.Second); leader == nil && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for i, m := range mgrs {
			if m.raft.IsLeader() {
				leader, follower = m, mgrs[1-i]
			}
		}
	}
	if leader == nil {
		t.Fatal("no leader")
	}
	if !leader.CaughtUpLeader(2 * time.Second) {
		t.Error("the leader is not a caught-up leader")
	}
	if follower.CaughtUpLeader(2 * time.Second) {
		t.Error("a follower is a caught-up leader")
	}
	if (&Manager{}).CaughtUpLeader(time.Second) {
		t.Error("a manager with no Raft is a caught-up leader")
	}
}
