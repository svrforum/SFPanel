package cluster

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func applyCmd(t *testing.T, fsm *FSM, cmd Command) {
	t.Helper()
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if res := fsm.Apply(&raft.Log{Data: data}); res != nil {
		t.Fatalf("Apply returned non-nil: %v", res)
	}
}

// CmdSetRecoveryCodes stores the hash list under the username, an empty list
// clears it, and the codes survive a GetState copy (which feeds snapshots).
func TestFSM_CmdSetRecoveryCodes(t *testing.T) {
	fsm := NewFSM()
	hashes := []string{"h1", "h2", "h3"}

	applyCmd(t, fsm, Command{Type: CmdSetRecoveryCodes, Key: "alice", Value: mustJSON(hashes)})

	if got := fsm.GetRecoveryCodes("alice"); len(got) != 3 || got[0] != "h1" {
		t.Fatalf("GetRecoveryCodes = %v, want %v", got, hashes)
	}

	// GetState deep-copies the map (snapshot path).
	if st := fsm.GetState(); len(st.RecoveryCodes["alice"]) != 3 {
		t.Errorf("GetState dropped recovery codes: %v", st.RecoveryCodes)
	}

	// Mutating the returned slice must not affect FSM state.
	got := fsm.GetRecoveryCodes("alice")
	got[0] = "tampered"
	if fsm.GetRecoveryCodes("alice")[0] != "h1" {
		t.Error("GetRecoveryCodes returned an aliased slice")
	}

	// Empty list clears the entry.
	applyCmd(t, fsm, Command{Type: CmdSetRecoveryCodes, Key: "alice", Value: mustJSON([]string{})})
	if got := fsm.GetRecoveryCodes("alice"); len(got) != 0 {
		t.Errorf("after clear: %v, want empty", got)
	}
}

// A CmdSetAccount (password/TOTP change) must NOT wipe recovery codes — the
// whole point of decoupling them into a separate command/map.
func TestFSM_SetAccountPreservesRecoveryCodes(t *testing.T) {
	fsm := NewFSM()
	applyCmd(t, fsm, Command{Type: CmdSetRecoveryCodes, Key: "alice", Value: mustJSON([]string{"h1", "h2"})})
	applyCmd(t, fsm, Command{Type: CmdSetAccount, Key: "alice", Value: mustJSON(AdminAccount{Username: "alice", Password: "newhash"})})

	if got := fsm.GetRecoveryCodes("alice"); len(got) != 2 {
		t.Errorf("CmdSetAccount wiped recovery codes: %v", got)
	}
	if acct := fsm.GetAccount("alice"); acct == nil || acct.Password != "newhash" {
		t.Errorf("account not updated: %+v", acct)
	}
}

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

// A CmdDisband that is being REPLAYED from the persisted log at startup
// (Index <= replay threshold) must NOT fire the teardown callback — otherwise
// a node that booted with a stale committed disband would self-destruct on
// every restart. A live disband (Index > threshold) still fires.
func TestFSM_CmdDisbandReplaySuppressed(t *testing.T) {
	fsm := NewFSM()
	fired := make(chan string, 1)
	fsm.SetOnDisband(func(id string) { fired <- id })
	fsm.SetReplayThreshold(100) // everything up to index 100 is historical

	data, _ := json.Marshal(Command{Type: CmdDisband, Key: "leader"})

	// Replayed entry (index within the historical range) → suppressed.
	fsm.Apply(&raft.Log{Index: 100, Data: data})
	select {
	case <-fired:
		t.Fatal("replayed CmdDisband (Index<=threshold) must not fire onDisband")
	case <-time.After(300 * time.Millisecond):
	}

	// Live entry (index past the threshold) → fires.
	fsm.Apply(&raft.Log{Index: 101, Data: data})
	select {
	case got := <-fired:
		if got != "leader" {
			t.Fatalf("callback got %q, want %q", got, "leader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live CmdDisband (Index>threshold) did not fire onDisband")
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
