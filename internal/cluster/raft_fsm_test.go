package cluster

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// L-04: applying a CmdDisband log entry fires the registered onDisband
// callback with the originating node ID and does not block the Apply loop.
func TestFSM_CmdDisbandFiresCallback(t *testing.T) {
	fsm := NewFSM()
	gotCh := make(chan string, 1)
	fsm.SetOnDisband(func(fromNodeID string) {
		gotCh <- fromNodeID
	})

	cmd := Command{Type: CmdDisband, Key: "leader-node-42"}
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if res := fsm.Apply(&raft.Log{Data: data}); res != nil {
		t.Fatalf("Apply returned non-nil result: %v", res)
	}

	select {
	case got := <-gotCh:
		if got != "leader-node-42" {
			t.Fatalf("callback got %q, want %q", got, "leader-node-42")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("onDisband callback was not invoked within 2s")
	}
}

// L-04: if no callback is registered, CmdDisband applies cleanly as a no-op
// (so replaying a log on a node without a wired handler doesn't break).
func TestFSM_CmdDisbandWithoutCallbackIsNoop(t *testing.T) {
	fsm := NewFSM()
	// Intentionally do not SetOnDisband.

	cmd := Command{Type: CmdDisband, Key: "whatever"}
	data, _ := json.Marshal(cmd)
	if res := fsm.Apply(&raft.Log{Data: data}); res != nil {
		t.Fatalf("Apply should no-op when callback missing, got: %v", res)
	}
}

// L-05: pickTransferTarget skips self, non-voter roles, and non-Online
// peers, returning only a suitable voter for leadership handoff.
func TestManager_pickTransferTarget(t *testing.T) {
	// pickTransferTarget inspects m.raft.GetFSM() and m.heartbeat, so it
	// requires a live Manager. Skip unless a minimal harness is wired;
	// this test is a placeholder documenting the expected contract.
	t.Skip("requires Raft harness — covered by integration tests")
}
