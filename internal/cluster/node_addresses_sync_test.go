package cluster

import (
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIfaces is a host whose interfaces a test changes while its manager
// runs: one Tailscale-like interface per address.
type fakeIfaces struct {
	mu    sync.Mutex
	addrs []string
}

func (f *fakeIfaces) set(addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrs = addrs
}

func (f *fakeIfaces) list() ([]IfaceAddr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []IfaceAddr
	for _, a := range f.addrs {
		out = append(out, IfaceAddr{Name: "tailscale0", IP: net.ParseIP(a), Up: true})
	}
	return out, nil
}

// withIfaces gives a manager f's interfaces before it starts.
func withIfaces(f *fakeIfaces) func(*Manager) {
	return func(m *Manager) { m.listIfaceAddrs = f.list }
}

// shortenAddressReports lets a node's addresses change every d for one test.
// Registered before the test's managers, the restore runs after they stop.
func shortenAddressReports(t *testing.T, d time.Duration) {
	t.Helper()
	old := addressReportEvery
	addressReportEvery = d
	t.Cleanup(func() { addressReportEvery = old })
}

func waitForAddresses(t *testing.T, leader *Manager, id string, want ...string) {
	t.Helper()
	waitUntil(t, 90*time.Second, id+" to be recorded at "+strings.Join(want, ", "), func() bool {
		n := leader.raft.GetFSM().GetNode(id)
		return n != nil && slices.Equal(n.AllAddresses(), want)
	})
}

// A follower's Tailscale address travels on its ping to the leader, which
// writes it into the follower's node record after its advertised address.
func TestAFollowersTailscaleAddressReachesTheNodeRecord(t *testing.T) {
	leader, addr, token, pin := realLeader(t)
	f := &fakeIfaces{}
	f.set("100.64.0.2")
	m2 := joinedManagerWith(t, addr, token, pin, "127.0.0.2", withIfaces(f))

	waitUntil(t, 60*time.Second, "the follower's Tailscale address in its node record", func() bool {
		n := leader.raft.GetFSM().GetNode(m2.nodeID)
		return n != nil && slices.Equal(n.AllAddresses(), []string{"127.0.0.2", "100.64.0.2"})
	})
}

// The leader sends itself no ping: it records its own addresses directly.
func TestTheLeaderRecordsItsOwnAddresses(t *testing.T) {
	f := &fakeIfaces{}
	f.set("100.64.0.1")
	leader, _, _, _ := realLeaderWith(t, withIfaces(f))

	waitUntil(t, 60*time.Second, "the leader's Tailscale address in its own node record", func() bool {
		n := leader.raft.GetFSM().GetNode(leader.nodeID)
		return n != nil && slices.Equal(n.AllAddresses(), []string{"127.0.0.1", "100.64.0.1"})
	})
}

// What reaches the node record is a list of IPs, at most eight, that starts
// with the address the node is already registered at: other nodes dial these.
func TestAnAddressReportIsCheckedBeforeItIsWritten(t *testing.T) {
	shortenAddressReports(t, 0)
	leader, addr, token, pin := realLeader(t)
	m2 := joinedManagerAt(t, addr, token, pin, "127.0.0.2")
	waitForVoter(t, leader, m2.nodeID)

	nine := []string{"127.0.0.2"}
	for i := 1; i <= 8; i++ {
		nine = append(nine, net.IPv4(100, 64, 0, byte(i)).String())
	}
	for _, tc := range []struct {
		addrs  []string
		reason string
	}{
		{[]string{"not-an-ip"}, "not an IP address"},
		{[]string{"127.0.0.2", "not-an-ip"}, "not an IP address"},
		{[]string{"100.64.0.2"}, "must start with the address the node is registered at"},
		{nil, "no addresses"},
		{nine, "more than 8"},
		{[]string{"127.0.0.2", "100.64.0.2", "100.64.0.2"}, "more than once"},
	} {
		err := leader.SetNodeAddresses(m2.nodeID, tc.addrs)
		if !errors.Is(err, ErrInvalidAddresses) || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("SetNodeAddresses(%q) = %v, want ErrInvalidAddresses for %q", tc.addrs, err, tc.reason)
		}
		if got := leader.raft.GetFSM().GetNode(m2.nodeID).AllAddresses(); !slices.Equal(got, []string{"127.0.0.2"}) {
			t.Fatalf("after SetNodeAddresses(%q) the record holds %q", tc.addrs, got)
		}
	}

	// The same list with the primary first is taken.
	if err := leader.SetNodeAddresses(m2.nodeID, []string{"127.0.0.2", "100.64.0.2"}); err != nil {
		t.Fatalf("a valid list was refused: %v", err)
	}
	if got := leader.raft.GetFSM().GetNode(m2.nodeID).AllAddresses(); !slices.Equal(got, []string{"127.0.0.2", "100.64.0.2"}) {
		t.Fatalf("the valid list was not recorded: %q", got)
	}
}

// A manager registered at a name (cluster init --advertise takes one, and its
// certificate names it) keeps that one address: the list holds IPs only, and
// refusing its report on every tick would only fill the log.
func TestAManagerRegisteredAtANameIsLeftAsItIs(t *testing.T) {
	nodes, _ := inmemRaft(t, "m1")
	if err := nodes[0].Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "m1", Kind: KindManager, Role: RoleVoter, GRPCAddress: "panel.example:3629"})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)
	m := &Manager{raft: nodes[0], nodeID: "m1"}
	m.syncOneNodeAddresses("m1", []string{"panel.example", "100.64.0.1"})
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "cannot record a node's addresses") && strings.Contains(line, " node_id=m1 ") {
			t.Fatalf("the leader refused the report of a manager registered at a name: %s", line)
		}
	}
	if got := nodes[0].GetFSM().GetNode("m1").Addresses; got != nil {
		t.Fatalf("the record gained addresses: %v", got)
	}
}

// Only the leader writes node records.
func TestAFollowerDoesNotSetNodeAddresses(t *testing.T) {
	leader, addr, token, pin := realLeader(t)
	m2 := joinedManagerAt(t, addr, token, pin, "127.0.0.2")
	waitForVoter(t, leader, m2.nodeID)
	if err := m2.SetNodeAddresses(m2.nodeID, []string{"127.0.0.2", "100.64.0.2"}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("a follower's SetNodeAddresses = %v, want ErrNotLeader", err)
	}
}

// Pings arrive every half minute; a report that matches the record must not
// cost a Raft entry each time. The interval is lifted so that only the match
// keeps the leader from writing.
func TestAnUnchangedReportWritesNothing(t *testing.T) {
	shortenAddressReports(t, 0)
	f1 := &fakeIfaces{}
	f1.set("100.64.0.1")
	leader, addr, token, pin := realLeaderWith(t, withIfaces(f1))
	f2 := &fakeIfaces{}
	f2.set("100.64.0.2")
	m2 := joinedManagerWith(t, addr, token, pin, "127.0.0.2", withIfaces(f2))
	waitForAddresses(t, leader, m2.nodeID, "127.0.0.2", "100.64.0.2")
	waitForAddresses(t, leader, leader.nodeID, "127.0.0.1", "100.64.0.1")

	before := leader.raft.AppliedIndex()
	leader.syncNodeAddresses()
	leader.syncNodeAddresses()
	if after := leader.raft.AppliedIndex(); after != before {
		t.Fatalf("two ticks with unchanged reports moved the applied index from %d to %d", before, after)
	}
}

// A new address replaces the old one rather than joining it.
func TestAChangedAddressReplacesTheOld(t *testing.T) {
	shortenAddressReports(t, 0)
	leader, addr, token, pin := realLeader(t)
	f := &fakeIfaces{}
	f.set("100.64.0.2")
	m2 := joinedManagerWith(t, addr, token, pin, "127.0.0.2", withIfaces(f))
	waitForAddresses(t, leader, m2.nodeID, "127.0.0.2", "100.64.0.2")

	f.set("100.64.0.3")
	waitForAddresses(t, leader, m2.nodeID, "127.0.0.2", "100.64.0.3")
}

// A node's addresses change at most once per addressReportEvery: a host
// whose interfaces flap must not write a Raft entry on every ping.
func TestAddressesChangeAtMostOncePerInterval(t *testing.T) {
	leader, _, _, _ := realLeader(t)
	if err := leader.SetNodeAddresses(leader.nodeID, []string{"127.0.0.1", "100.64.0.5"}); err != nil {
		t.Fatal(err)
	}
	err := leader.SetNodeAddresses(leader.nodeID, []string{"127.0.0.1", "100.64.0.6"})
	if !errors.Is(err, ErrAddressesChangedRecently) {
		t.Fatalf("a second change within the interval = %v, want ErrAddressesChangedRecently", err)
	}
	if got := leader.raft.GetFSM().GetNode(leader.nodeID).AllAddresses(); !slices.Equal(got, []string{"127.0.0.1", "100.64.0.5"}) {
		t.Fatalf("the record holds %q", got)
	}
	// Reporting what is recorded is answered at any time.
	if err := leader.SetNodeAddresses(leader.nodeID, []string{"127.0.0.1", "100.64.0.5"}); err != nil {
		t.Fatalf("an unchanged report within the interval = %v", err)
	}
}
