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

func TestFSMApply_ForkCreate(t *testing.T) {
	fsm := NewFSM()
	rec := &ForkRecord{
		ID:        "fork-abc",
		Name:      "My Stack",
		Compose:   "services:\n  web:\n    image: nginx:1\n",
		CreatedAt: 1714742400000,
	}
	val, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Type: CmdForkCreate, Value: val}
	data, _ := json.Marshal(cmd)
	if applyErr := fsm.Apply(&raft.Log{Data: data}); applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	got := fsm.GetState().Forks["fork-abc"]
	if got == nil {
		t.Fatal("expected fork in state")
	}
	if got.Name != "My Stack" {
		t.Errorf("name: got %q want %q", got.Name, "My Stack")
	}
}

func TestFSMApply_ForkDelete(t *testing.T) {
	fsm := NewFSM()
	fsm.state.Forks["fork-x"] = &ForkRecord{ID: "fork-x", Name: "x"}
	cmd := Command{Type: CmdForkDelete, Key: "fork-x"}
	data, _ := json.Marshal(cmd)
	if applyErr := fsm.Apply(&raft.Log{Data: data}); applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	if _, ok := fsm.GetState().Forks["fork-x"]; ok {
		t.Fatal("expected fork removed")
	}
}

func TestFSMApply_ForkUpdate_MetadataOnly(t *testing.T) {
	fsm := NewFSM()
	fsm.state.Forks["fork-x"] = &ForkRecord{
		ID:          "fork-x",
		Name:        "old",
		Description: "old desc",
		Category:    "old cat",
		Compose:     "services: {}",
	}
	patch := &ForkRecord{
		ID:          "fork-x",
		Name:        "new",
		Description: "new desc",
		Category:    "new cat",
		// Compose intentionally empty in patch — must NOT overwrite existing.
	}
	val, _ := json.Marshal(patch)
	cmd := Command{Type: CmdForkUpdate, Key: "fork-x", Value: val}
	data, _ := json.Marshal(cmd)
	if err := fsm.Apply(&raft.Log{Data: data}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := fsm.GetState().Forks["fork-x"]
	if got.Name != "new" || got.Description != "new desc" || got.Category != "new cat" {
		t.Errorf("metadata not updated: %+v", got)
	}
	if got.Compose != "services: {}" {
		t.Errorf("compose was overwritten: %q", got.Compose)
	}
}
