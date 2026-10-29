package cluster

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
)

// restartedLeader is a one-node cluster member that wrote entries — after
// 3000 others, so that replaying them takes a while — then stopped before a
// snapshot (threshold 2048, interval ten minutes) and started again. It comes
// back the moment it leads, when its FSM holds none of them yet: a leader of
// a new term applies its predecessors' entries only once it has committed
// one of its own.
func restartedLeader(t *testing.T, tlsMgr *TLSManager, entries map[string]string) *Manager {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "raft")
	first := proxyTestManager(t, tlsMgr, dataDir, true)
	for i := 0; i < 3000; i++ {
		if err := first.SetConfig(fmt.Sprintf("filler/%d", i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range entries {
		if err := first.SetConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.raft.Shutdown(); err != nil {
		t.Fatal(err)
	}
	rn, err := NewRaftNode(RaftConfig{NodeID: "n1", BindAddr: fmt.Sprintf("127.0.0.1:%d", freePortPair(t)), DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rn.Shutdown() })
	m := &Manager{config: &config.ClusterConfig{DataDir: t.TempDir()}, tls: tlsMgr, nodeID: "n1", raft: rn,
		heartbeat: NewHeartbeatManager(time.Minute, 3*time.Minute)}
	for start := time.Now(); !rn.IsLeader(); time.Sleep(50 * time.Microsecond) {
		if time.Since(start) > 20*time.Second {
			t.Fatal("the restarted node never led")
		}
	}
	return m
}

// replayed waits until the restarted leader's FSM holds the last filler entry.
func replayed(t *testing.T, m *Manager) {
	t.Helper()
	waitUntil(t, 20*time.Second, "the replay", func() bool {
		_, ok := m.raft.GetFSM().GetConfigValue("filler/2999")
		return ok
	})
}

// Seeding the proxy secret "because there is none" in that moment replaced
// the one every member had switched to, under the old rotation time.
func TestARestartedLeaderKeepsTheProxySecret(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	m := restartedLeader(t, nil, map[string]string{
		configKeyProxySecret:    "S1",
		configKeyProxyRotatedAt: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
	})
	if err := m.MaybeRotateProxySecret(); err != nil {
		t.Fatal(err)
	}
	replayed(t, m)
	time.Sleep(200 * time.Millisecond) // a seed proposed in the window lands after the replay
	if v, _ := m.raft.GetFSM().GetConfigValue(configKeyProxySecret); v != "S1" {
		t.Fatalf("the restarted leader replaced the committed proxy secret S1 with %q", v)
	}
}

// The same moment for the cluster CA: once a rotation has replaced it, a
// leader whose FSM looks empty must not seed the CA on its own disk over it.
func TestARestartedLeaderSeedsNoCAOverTheReplicatedOne(t *testing.T) {
	_, tlsMgr := setupCertDir(t) // this node's disk holds a CA of its own
	m := restartedLeader(t, tlsMgr, map[string]string{configKeyCACert: "replicated-cert", configKeyCAKey: "replicated-key"})
	if err := m.SeedClusterCA(); err != nil {
		t.Fatal(err)
	}
	replayed(t, m)
	time.Sleep(200 * time.Millisecond)
	if v, _ := m.raft.GetFSM().GetConfigValue(configKeyCACert); v != "replicated-cert" {
		t.Fatal("the restarted leader seeded the CA on its disk over the replicated one")
	}
}
