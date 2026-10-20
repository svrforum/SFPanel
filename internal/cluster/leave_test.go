package cluster

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

// joinedManager joins a second real manager, in this process, to the
// cluster at addr — Raft, gRPC and heartbeats included.
func joinedManager(t *testing.T, addr, token, pin string) *Manager {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port = 1
	cfg.Cluster.GRPCPort = freePortPair(t)
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	cfg.Cluster.DataDir = filepath.Join(dir, "raft")
	var mgr *Manager
	e := &JoinEngine{Config: cfg, OnActivate: func(c *config.Config, _ string, _ *Manager) (*Manager, error) {
		m := NewManager(&c.Cluster)
		if err := m.Start(); err != nil {
			return nil, err
		}
		gs, err := NewGRPCServer(m, 1, stubPanel{})
		if err != nil {
			return nil, err
		}
		if err := gs.Start(fmt.Sprintf("127.0.0.1:%d", c.Cluster.GRPCPort)); err != nil {
			return nil, err
		}
		go func() { <-m.GetHeartbeat().Done(); gs.Stop() }()
		m.StartLocalMetrics(func() (float64, float64, float64, int) { return 1, 1, 1, 0 })
		mgr = m
		return m, nil
	}}
	if _, err := e.Execute(addr, token, "127.0.0.1", pin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	return mgr
}

func waitUntil(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out after %v waiting for %s", limit, what)
}

func waitForVoter(t *testing.T, leader *Manager, id string) {
	t.Helper()
	waitUntil(t, 60*time.Second, id+" to become a voter", func() bool {
		n := leader.raft.GetFSM().GetNode(id)
		return n != nil && n.Role == RoleVoter
	})
}

// A leader that leaves hands leadership over, then asks the new leader to
// take it out. It asked at once, found no leader yet — the new one is known
// some milliseconds later — and shut its Raft down with its voter record in
// place: the one manager left had no quorum.
func TestALeaderThatLeavesIsRemovedByTheNewLeader(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	m2 := joinedManager(t, addr, token, pin)
	waitForVoter(t, m1, m2.LocalNodeID())
	if err := m1.Leave(); err != nil {
		t.Fatalf("leave: %v", err)
	}
	waitUntil(t, 15*time.Second, "the remaining manager to lead alone", func() bool {
		if !m2.IsLeader() {
			return false
		}
		f := m2.raft.raft.GetConfiguration()
		return f.Error() == nil && len(f.Configuration().Servers) == 1 && m2.raft.GetFSM().GetNode(m1.LocalNodeID()) == nil
	})
}

// A leave no leader took says so. 'cluster leave' printed "Leader notified of
// departure." whatever happened, and the node stayed in the others' records.
func TestALeaveNoLeaderTookSaysSo(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	m2 := joinedManager(t, addr, token, pin)
	waitForVoter(t, m1, m2.LocalNodeID())
	// The leader's Raft stops dead: no handover, and no quorum left.
	if err := m1.raft.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := m2.Leave(); !errors.Is(err, ErrLeaveUnacknowledged) {
		t.Fatalf("a leave with no leader to take it: %v", err)
	}
}
