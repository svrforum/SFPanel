package cluster

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// inmemManagers starts n managers on raft's in-memory transport, with the FSM
// the panel runs and the commit timeout raft.go gives it: how long a follower
// may wait to learn that an entry committed, when no other entry follows.
func inmemManagers(t *testing.T, n int) []*Manager {
	t.Helper()
	var trans []*raft.InmemTransport
	var servers []raft.Server
	for i := 0; i < n; i++ {
		addr, tr := raft.NewInmemTransport("")
		trans = append(trans, tr)
		servers = append(servers, raft.Server{ID: raft.ServerID(fmt.Sprintf("m%d", i+1)), Address: addr})
	}
	for _, a := range trans {
		for _, b := range trans {
			if a != b {
				a.Connect(b.LocalAddr(), b)
			}
		}
	}
	var mgrs []*Manager
	for i, s := range servers {
		conf := raft.DefaultConfig()
		conf.LocalID = s.ID
		conf.HeartbeatTimeout = 100 * time.Millisecond
		conf.ElectionTimeout = 100 * time.Millisecond
		conf.LeaderLeaseTimeout = 100 * time.Millisecond
		conf.CommitTimeout = 1000 * time.Millisecond
		conf.LogOutput = io.Discard
		fsm := NewFSM()
		store := raft.NewInmemStore()
		r, err := raft.NewRaft(conf, fsm, store, store, raft.NewInmemSnapshotStore(), trans[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := r.BootstrapCluster(raft.Configuration{Servers: servers}).Error(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Shutdown().Error() })
		mgrs = append(mgrs, &Manager{raft: &RaftNode{raft: r, fsm: fsm}, nodeID: string(s.ID), heartbeat: NewHeartbeatManager(time.Second, time.Minute)})
	}
	return mgrs
}

// A follower applies an entry once an AppendEntries carries the commit index
// past it, and Raft sends that with the next entry or after CommitTimeout,
// never with a heartbeat. The leader stopped as soon as it applied the
// disband: of two managers, the follower was left holding a disband it never
// applied, in a cluster it could not commit in alone — four times in five on
// the rig. Every manager here stops on the disband as performDisband does.
func TestADisbandReachesEveryManagerBeforeTheLeaderStops(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprintf("%d managers", n), func(t *testing.T) {
			mgrs := inmemManagers(t, n)
			var leader *Manager
			for deadline := time.Now().Add(5 * time.Second); leader == nil && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				for _, m := range mgrs {
					if m.raft.IsLeader() {
						leader = m
					}
				}
			}
			if leader == nil {
				t.Fatal("no leader")
			}
			for _, m := range mgrs {
				if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: m.nodeID, Name: m.nodeID, Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
					t.Fatal(err)
				}
				if m != leader {
					leader.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: m.nodeID})
				}
			}
			disbanded := map[string]chan struct{}{}
			for _, m := range mgrs {
				ch := make(chan struct{})
				disbanded[m.nodeID] = ch
				m.SetOnDisband(func(string) {
					close(ch)
					m.ShutdownForDisband()
				})
			}

			if err := leader.Disband(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			for _, m := range mgrs {
				select {
				case <-disbanded[m.nodeID]:
				case <-time.After(5 * time.Second):
					t.Fatalf("%s never applied the disband", m.nodeID)
				}
			}
		})
	}
}

// soleLeader is a one-manager cluster's leader whose FSM also lists the
// managers named, which send it heartbeats in a test and nothing else.
func soleLeader(t *testing.T, others ...string) *Manager {
	t.Helper()
	leader := inmemManagers(t, 1)[0]
	waitFor(t, "a leader", leader.raft.IsLeader)
	for _, id := range append([]string{leader.nodeID}, others...) {
		if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: id, Name: id, Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	return leader
}

// A barrier commits on a majority: of three managers or more, one lagging
// behind could miss the disband and stay in a cluster with no leader. The
// leader stays while another manager still streams heartbeats to it — each
// closes its stream as it applies the disband and stops — and meanwhile it
// keeps sending entries a lagging one can catch up with.
func TestADisbandsLeaderStaysWhileAnotherManagerStillStreams(t *testing.T) {
	leader := soleLeader(t, "m2")
	leader.heartbeat.RecordStreamPing(&NodeMetrics{NodeID: "m2"}, true)
	ended := make(chan time.Time, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		ended <- time.Now()
		leader.heartbeat.StreamEnded("m2")
	}()
	start := time.Now()
	leader.ShutdownForDisband()
	returned := time.Now()
	if at := <-ended; returned.Before(at) {
		t.Fatalf("the leader stopped %v before m2's stream ended", at.Sub(returned))
	}
	if took := returned.Sub(start); took > 2*time.Second {
		t.Fatalf("the leader stayed %v after m2's stream ended", took)
	}
}

// One that never closes its stream holds the leader only so long, and is
// named: it needs a forced leave or a forced disband on its own server.
func TestADisbandsLeaderNamesTheManagersStillStreaming(t *testing.T) {
	logs := captureLogs(t)
	prev := disbandStreamWait
	disbandStreamWait = 300 * time.Millisecond
	t.Cleanup(func() { disbandStreamWait = prev })
	leader := soleLeader(t, "m2", "m3")
	leader.heartbeat.RecordStreamPing(&NodeMetrics{NodeID: "m2"}, true)
	leader.heartbeat.RecordStreamPing(&NodeMetrics{NodeID: "m3"}, true)
	leader.heartbeat.StreamEnded("m3")
	start := time.Now()
	leader.ShutdownForDisband()
	if took := time.Since(start); took < 300*time.Millisecond || took > 2*time.Second {
		t.Fatalf("the leader stayed %v, not the bound", took)
	}
	var named []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "had not stopped") {
			named = append(named, line)
		}
	}
	if len(named) != 1 || !strings.Contains(named[0], "m2") || strings.Contains(named[0], "m3") {
		t.Fatalf("one line should name m2 alone: %q", named)
	}
}
