package cluster

import (
	"errors"
	"testing"
)

// An agent's first heartbeat promoted it to a Raft voter: the leader then
// needed a vote from a node that never answers, and a one-manager cluster
// lost its leader within seconds.
func TestAgentIsNeverPromotedToVoter(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "agent-1", Kind: KindAgent, GRPCAddress: "127.0.0.1:1"})
	mgr.PromoteOnHeartbeatIfPending("agent-1")
	f := mgr.raft.raft.GetConfiguration()
	if err := f.Error(); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.Configuration().Servers {
		if string(s.ID) == "agent-1" {
			t.Fatalf("the agent entered the Raft configuration as %v", s.Suffrage)
		}
	}
	// ConfirmJoin refuses an agent as well, but the heartbeat has to stop
	// before it: otherwise every agent heartbeat logs "promotion deferred".
	mgr.promoteMu.Lock()
	_, attempted := mgr.promoteAttempts["agent-1"]
	mgr.promoteMu.Unlock()
	if attempted {
		t.Fatal("the agent's heartbeat was taken as an attempt to promote it")
	}
	if err := mgr.ConfirmJoin("agent-1"); !errors.Is(err, ErrNotManager) {
		t.Fatalf("ConfirmJoin for an agent: %v", err)
	}
}
