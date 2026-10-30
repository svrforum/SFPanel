package cluster

import (
	"fmt"
	"io"
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
