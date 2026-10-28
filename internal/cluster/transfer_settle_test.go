package cluster

import (
	"io"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

type inmemNode struct {
	id    raft.ServerID
	addr  raft.ServerAddress
	trans *raft.InmemTransport
	r     *raft.Raft
}

// inmemCluster starts three raft nodes on raft's in-memory transport.
func inmemCluster(t *testing.T) []*inmemNode {
	t.Helper()
	var nodes []*inmemNode
	var servers []raft.Server
	for _, id := range []raft.ServerID{"n1", "n2", "n3"} {
		addr, trans := raft.NewInmemTransport("")
		nodes = append(nodes, &inmemNode{id: id, addr: addr, trans: trans})
		servers = append(servers, raft.Server{ID: id, Address: addr})
	}
	for _, a := range nodes {
		for _, b := range nodes {
			if a != b {
				a.trans.Connect(b.addr, b.trans)
			}
		}
	}
	for _, n := range nodes {
		conf := raft.DefaultConfig()
		conf.LocalID = n.id
		conf.HeartbeatTimeout = 100 * time.Millisecond
		conf.ElectionTimeout = 100 * time.Millisecond
		conf.LeaderLeaseTimeout = 100 * time.Millisecond
		conf.CommitTimeout = 10 * time.Millisecond
		conf.LogOutput = io.Discard
		store := raft.NewInmemStore()
		r, err := raft.NewRaft(conf, &raft.MockFSM{}, store, store, raft.NewInmemSnapshotStore(), n.trans)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.BootstrapCluster(raft.Configuration{Servers: servers}).Error(); err != nil {
			t.Fatal(err)
		}
		n.r = r
		t.Cleanup(func() { _ = r.Shutdown().Error() })
	}
	return nodes
}

func inmemLeader(t *testing.T, among ...*inmemNode) *inmemNode {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, n := range among {
			if n.r.State() == raft.Leader {
				return n
			}
		}
	}
	t.Fatal("no leader")
	return nil
}

// Raft answers a timed-out leadership transfer before it clears its
// transfer-in-progress mark, and refuses a transfer or a configuration change
// asked for in between. Asked at once — the next candidate after one that is
// down, or a leave's own removal after its transfer failed — they were
// refused most of the time.
func TestAfterATimedOutTransferTheNextStepIsNotRefused(t *testing.T) {
	nodes := inmemCluster(t)
	first := inmemLeader(t, nodes...)
	var off *inmemNode
	var online []*inmemNode
	for _, n := range nodes {
		if off == nil && n != first {
			off = n
			continue
		}
		online = append(online, n)
	}
	off.trans.DisconnectAll()
	for _, n := range online {
		n.trans.Disconnect(off.addr)
	}

	for i := 0; i < 10; i++ {
		l := inmemLeader(t, online...)
		next := online[0]
		if next == l {
			next = online[1]
		}
		rn := &RaftNode{raft: l.r}
		if err := rn.TransferLeadership(string(off.id)); err == nil {
			t.Fatalf("attempt %d: a transfer to a node that is cut off went through", i)
		}
		if err := rn.TransferLeadership(string(next.id)); err != nil {
			t.Fatalf("attempt %d: the transfer right after a timed-out one: %v", i, err)
		}
	}

	// A configuration change is refused the same way. Removing the node that
	// is down commits; a leader removing itself here could not, whatever the
	// timing: the configuration left would be the online node and the one
	// that is down.
	l := inmemLeader(t, online...)
	rn := &RaftNode{raft: l.r}
	if err := rn.TransferLeadership(string(off.id)); err == nil {
		t.Fatal("a transfer to a node that is cut off went through")
	}
	if err := rn.RemoveServer(string(off.id)); err != nil {
		t.Fatalf("a removal right after a timed-out transfer: %v", err)
	}
}
