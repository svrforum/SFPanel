package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Raft node shut down in this process kept its log files locked, and
// opening them again here waited for the lock forever while holding the Raft
// port: a live join retried after a failure hung the request.
func TestARaftNodeReopensInTheSameProcess(t *testing.T) {
	dir := t.TempDir()
	cfg := RaftConfig{NodeID: "n1", BindAddr: fmt.Sprintf("127.0.0.1:%d", freePortPair(t)), DataDir: dir, Bootstrap: true}
	rn, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50 && !rn.IsLeader(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if err := rn.Shutdown(); err != nil {
		t.Fatal(err)
	}
	cfg.Bootstrap = false
	done := make(chan error, 1)
	go func() {
		again, err := NewRaftNode(cfg)
		if err == nil {
			err = again.Shutdown()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening the same Raft data again in this process blocked")
	}
}

// A Raft node that failed to start kept what it had opened — the Raft port
// and its log files' lock — so starting it again in this process failed on
// the port, or, with the port let go, waited for the lock forever.
func TestAFailedRaftStartLetsGoOfThePortAndTheFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := RaftConfig{NodeID: "n1", BindAddr: fmt.Sprintf("127.0.0.1:%d", freePortPair(t)), DataDir: dir, Bootstrap: true}
	// A peers.json that names no server fails the start once the port and
	// both stores are open.
	peers := filepath.Join(dir, "peers.json")
	if err := os.WriteFile(peers, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRaftNode(cfg); err == nil || !strings.Contains(err.Error(), "empty server list") {
		t.Fatalf("precondition: the start should fail on peers.json: %v", err)
	}
	if err := os.Remove(peers); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		again, err := NewRaftNode(cfg)
		if err == nil {
			err = again.Shutdown()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("starting again after a failed start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("starting again after a failed start blocked")
	}
}
