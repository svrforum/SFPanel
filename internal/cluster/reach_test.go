package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// shortenReachProbes probes every d for one test. Registered before the
// test's managers, the restore runs after they stop.
func shortenReachProbes(t *testing.T, d time.Duration) {
	t.Helper()
	old := reachInterval
	reachInterval = d
	t.Cleanup(func() { reachInterval = old })
}

// probeManager is a manager on a one-node Raft whose FSM holds agents.
func probeManager(t *testing.T, agents ...Node) *Manager {
	t.Helper()
	nodes, _ := inmemRaft(t, "m1")
	for _, a := range agents {
		a.Kind = KindAgent
		if err := nodes[0].Apply(Command{Type: CmdAddNode, Value: mustJSON(a)}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{raft: nodes[0], nodeID: "m1", heartbeat: NewHeartbeatManager(time.Second, time.Minute)}
}

// An agent that never answers costs the probe one dial timeout, and eight
// dial at once: ten such agents end in two rounds of three seconds, every
// one of them unreached, not in ten rounds.
func TestAProbeOfABlackholedAgentEndsInTime(t *testing.T) {
	var agents []Node
	for i := 1; i <= 10; i++ {
		agents = append(agents, Node{ID: fmt.Sprintf("a%d", i), GRPCAddress: fmt.Sprintf("192.0.2.%d:3629", i)})
	}
	m := probeManager(t, agents...)
	var inFlight, most atomic.Int32
	m.reachDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			old := most.Load()
			if n <= old || most.CompareAndSwap(old, n) {
				break
			}
		}
		<-ctx.Done() // a black hole: no answer, ever
		return nil, ctx.Err()
	}

	start := time.Now()
	res := m.probeAgents(context.Background())
	took := time.Since(start)

	if took >= 7*time.Second {
		t.Fatalf("the probe took %v, want under 7s (two rounds of the 3s dial timeout)", took)
	}
	if took < 5*time.Second {
		t.Fatalf("the probe took %v: the dial timeout or the eight at once is not what bounds it", took)
	}
	if got := most.Load(); got != reachParallel {
		t.Fatalf("%d dials ran at once, want %d", got, reachParallel)
	}
	if len(res.Agents) != 10 {
		t.Fatalf("the probe judged %d agents, want 10: %v", len(res.Agents), res.Agents)
	}
	for id, ok := range res.Agents {
		if ok {
			t.Fatalf("agent %s counts as reached through a black hole", id)
		}
	}
	if m.reach.Load() == nil {
		t.Fatal("the probe's result was not kept")
	}
}

// An agent the probe cannot dial at its registered address is tried at its
// other addresses, on the same port.
func TestAProbeTriesAnAgentsOtherAddresses(t *testing.T) {
	m := probeManager(t, Node{ID: "a1", GRPCAddress: "192.0.2.10:3629", Addresses: []string{"192.0.2.10", "100.64.0.10"}})
	var mu sync.Mutex
	var dialed []string
	m.reachDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		if addr != "100.64.0.10:3629" {
			return nil, errors.New("connection refused")
		}
		c, s := net.Pipe()
		_ = s.Close()
		return c, nil
	}
	res := m.probeAgents(context.Background())
	if !res.Agents["a1"] {
		t.Fatalf("a1 is reachable at its Tailscale address, the probe says %v", res.Agents)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(dialed, []string{"192.0.2.10:3629", "100.64.0.10:3629"}) {
		t.Fatalf("dialed %q, want the registered address first, then the other on the same port", dialed)
	}
}

// What a manager reports and weighs is its probe while fresh: a stale or
// missing one is unknown (nil), and a fresh one that reached nothing is an
// empty list — not unknown.
func TestReachableAgentsIsFreshOrUnknown(t *testing.T) {
	m := &Manager{}
	if got := m.reachableAgents(); got != nil {
		t.Fatalf("with no probe: %v, want nil", got)
	}
	m.reach.Store(&reachResult{Agents: map[string]bool{"a1": false}, At: time.Now()})
	if got := m.reachableAgents(); got == nil || len(got) != 0 {
		t.Fatalf("a fresh probe that reached none: %#v, want an empty list", got)
	}
	m.reach.Store(&reachResult{Agents: map[string]bool{"a2": true, "a1": true, "a3": false}, At: time.Now()})
	if got := m.reachableAgents(); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Fatalf("a fresh probe: %v, want [a1 a2]", got)
	}
	m.reach.Store(&reachResult{Agents: map[string]bool{"a1": true}, At: time.Now().Add(-reachFresh - time.Second)})
	if got := m.reachableAgents(); got != nil {
		t.Fatalf("a stale probe: %v, want nil", got)
	}
}

// A node is unreachable only on a fresh probe's word that it tried the node,
// an agent, and failed. Anything less is no verdict, and a relay goes ahead.
func TestOnlyAFreshProbeThatFailedAnAgentCallsItUnreachable(t *testing.T) {
	fsm := fsmWithConfig(t, nil)
	addTestNode(t, fsm, Node{ID: "far", Kind: KindAgent})
	addTestNode(t, fsm, Node{ID: "near", Kind: KindAgent})
	addTestNode(t, fsm, Node{ID: "unprobed", Kind: KindAgent})
	addTestNode(t, fsm, Node{ID: "m2", Kind: KindManager})
	m := &Manager{raft: &RaftNode{fsm: fsm}}
	probe := map[string]bool{"far": false, "near": true, "m2": false}

	if m.Unreachable("far") {
		t.Fatal("with no probe, far is unreachable")
	}
	m.reach.Store(&reachResult{Agents: probe, At: time.Now()})
	for id, want := range map[string]bool{"far": true, "near": false, "unprobed": false, "m2": false, "gone": false} {
		if got := m.Unreachable(id); got != want {
			t.Fatalf("fresh probe: Unreachable(%s) = %v, want %v", id, got, want)
		}
	}
	m.reach.Store(&reachResult{Agents: probe, At: time.Now().Add(-reachFresh - time.Second)})
	if m.Unreachable("far") {
		t.Fatal("a stale probe still calls far unreachable")
	}
}

// A follower probes the agents in the FSM and its pings carry those it
// reaches to the leader.
func TestAManagerReportsTheAgentsItReaches(t *testing.T) {
	shortenReachProbes(t, 200*time.Millisecond)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	leader, addr, token, pin := realLeader(t)
	m2 := joinedManagerAt(t, addr, token, pin, "127.0.0.2")
	if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a1", Name: "a1", Kind: KindAgent, GRPCAddress: lis.Addr().String()})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, 60*time.Second, "the follower's ping to report a1 as reached", func() bool {
		met := leader.GetHeartbeat().GetMetrics(m2.nodeID)
		return met != nil && slices.Equal(met.ReachableAgents, []string{"a1"})
	})
}
