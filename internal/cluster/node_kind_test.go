package cluster

import (
	"encoding/json"
	"testing"
)

func addTestNode(t *testing.T, fsm *FSM, n Node) {
	t.Helper()
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	applyCmd(t, fsm, Command{Type: CmdAddNode, Value: data})
}

func TestNodeIsManager(t *testing.T) {
	for kind, want := range map[NodeKind]bool{"": true, KindManager: true, KindAgent: false} {
		if got := (&Node{Kind: kind}).IsManager(); got != want {
			t.Errorf("Kind %q: IsManager = %v, want %v", kind, got, want)
		}
	}
}

// A status update must not wipe the kind: CmdUpdateNode merges only the
// fields an update carries, and every heartbeat-driven status change is one.
func TestFSMUpdateNodeKeepsKind(t *testing.T) {
	fsm := NewFSM()
	addTestNode(t, fsm, Node{ID: "a1", Kind: KindAgent, Role: RoleNonVoter})
	data, _ := json.Marshal(Node{ID: "a1", Status: StatusOffline})
	applyCmd(t, fsm, Command{Type: CmdUpdateNode, Value: data})
	if got := fsm.GetNode("a1").Kind; got != KindAgent {
		t.Fatalf("Kind after a status update = %q, want agent", got)
	}
	data, _ = json.Marshal(Node{ID: "a1", Kind: KindManager})
	applyCmd(t, fsm, Command{Type: CmdUpdateNode, Value: data})
	if got := fsm.GetNode("a1").Kind; got != KindManager {
		t.Fatalf("Kind after an explicit update = %q, want manager", got)
	}
}

func TestFSMGetConfigValue(t *testing.T) {
	fsm := fsmWithConfig(t, map[string]string{"k": "v"})
	if v, ok := fsm.GetConfigValue("k"); !ok || v != "v" {
		t.Fatalf("GetConfigValue(k) = %q, %v", v, ok)
	}
	if _, ok := fsm.GetConfigValue("missing"); ok {
		t.Fatal("GetConfigValue reported a missing key as present")
	}
}
