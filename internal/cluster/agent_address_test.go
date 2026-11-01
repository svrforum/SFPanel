package cluster

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
)

// m1 is a manager with a LAN and a Tailscale address, registered at the LAN
// one, as a manager on a tailnet is.
func m1Ref() config.ManagerRef {
	return config.ManagerRef{ID: "m1", GRPCAddress: "192.0.2.10:3629", Addresses: []string{"192.0.2.10", "100.64.0.10"}}
}

func m1Info() *pb.NodeInfo {
	return &pb.NodeInfo{Id: "m1", GrpcAddress: "192.0.2.10:3629", Addresses: []string{"192.0.2.10", "100.64.0.10"}, Kind: string(KindManager)}
}

func TestRankAddressesPutsTheAgentsOwnNetworkFirst(t *testing.T) {
	for _, tc := range []struct {
		own  string
		ips  []string
		want []string
	}{
		{"100.64.0.30", []string{"192.0.2.10", "100.64.0.10"}, []string{"100.64.0.10", "192.0.2.10"}},
		{"192.0.2.30", []string{"192.0.2.10", "100.64.0.10"}, []string{"192.0.2.10", "100.64.0.10"}},
		{"192.0.2.30", []string{"100.64.0.10", "192.0.2.10", "198.51.100.10"}, []string{"192.0.2.10", "198.51.100.10", "100.64.0.10"}},
		// With ports, as a follower names the leader to a joining agent.
		{"100.64.0.30", []string{"192.0.2.10:3629", "100.64.0.10:3629"}, []string{"100.64.0.10:3629", "192.0.2.10:3629"}},
	} {
		if got := rankAddresses(tc.own, tc.ips); !slices.Equal(got, tc.want) {
			t.Errorf("own %s, %v: got %v, want %v", tc.own, tc.ips, got, tc.want)
		}
	}
}

// The leader is tried first, at the address on the agent's own network, then
// at its others; a manager with no Tailscale address comes after, at the
// one it has.
func TestNextTargetsTriesATailscaleAgentsNetworkFirst(t *testing.T) {
	st := AgentState{Leader: "192.0.2.10:3629", Managers: []config.ManagerRef{
		{ID: "m3", GRPCAddress: "192.0.2.12:3629", Addresses: []string{"192.0.2.12"}},
		m1Ref(),
	}}
	want := []string{"100.64.0.10:3629", "192.0.2.10:3629", "192.0.2.12:3629"}
	if got := nextTargets(st, "100.64.0.30"); !slices.Equal(got, want) {
		t.Fatalf("targets %v, want %v", got, want)
	}
	// An agent on the LAN keeps to the order it always had, every manager's
	// LAN address, and tries the leader's Tailscale one after them.
	want = []string{"192.0.2.10:3629", "192.0.2.12:3629", "100.64.0.10:3629"}
	if got := nextTargets(st, "192.0.2.30"); !slices.Equal(got, want) {
		t.Fatalf("targets of a LAN agent %v, want %v", got, want)
	}
}

// When the leader goes away, as it does on every restart, its address on the
// agent's own network fails first. Every manager the agent has an address
// for on that network comes next: a follower there names the leader within
// one attempt, where the leader's address on another network, which the
// agent may not route to, costs a dial timeout and an Interval before any
// other manager is tried. An address a manager was last reached at counts as
// the agent's own network.
func TestNextTargetsTriesEveryManagerOnTheAgentsNetworkBeforeAnyOther(t *testing.T) {
	m2 := config.ManagerRef{ID: "m2", GRPCAddress: "192.0.2.11:3629", Addresses: []string{"192.0.2.11", "100.64.0.11"}}
	m3 := config.ManagerRef{ID: "m3", GRPCAddress: "192.0.2.12:3629", Addresses: []string{"192.0.2.12"}}
	st := AgentState{Leader: "192.0.2.10:3629", Managers: []config.ManagerRef{m2, m3, m1Ref()}}
	for _, tc := range []struct {
		own     string
		reached map[string]string
		want    []string
	}{
		{"192.0.2.30", nil, []string{"192.0.2.10:3629", "192.0.2.11:3629", "192.0.2.12:3629", "100.64.0.10:3629", "100.64.0.11:3629"}},
		{"100.64.0.30", nil, []string{"100.64.0.10:3629", "100.64.0.11:3629", "192.0.2.10:3629", "192.0.2.11:3629", "192.0.2.12:3629"}},
		// m2 was last reached on the LAN by an agent on the tailnet.
		{"100.64.0.30", map[string]string{"m2": "192.0.2.11:3629"}, []string{"100.64.0.10:3629", "192.0.2.11:3629", "100.64.0.11:3629", "192.0.2.10:3629", "192.0.2.12:3629"}},
	} {
		st.Reached = tc.reached
		if got := nextTargets(st, tc.own); !slices.Equal(got, tc.want) {
			t.Errorf("agent at %s, reached %v: targets %v, want %v", tc.own, tc.reached, got, tc.want)
		}
	}
}

// A state file and a config from before manager addresses name each manager
// by its gRPC address alone: the agent dials it there, as it always did.
func TestAStateFromBeforeAddressesDialsEachManagerAtItsGRPCAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-state.json")
	old := `{"managers":[{"id":"m1","grpc_address":"192.0.2.1:3629"},{"id":"m2","grpc_address":"192.0.2.2:3629"}],"leader":"192.0.2.2:3629"}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadAgentState(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := nextTargets(*st, "100.64.0.30"); !slices.Equal(got, []string{"192.0.2.2:3629", "192.0.2.1:3629"}) {
		t.Fatalf("targets %v", got)
	}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != old {
		t.Fatalf("an old state saves differently:\n%s\n%s", data, old)
	}
}

// newIdleLink is a link that is not run, for what its methods do to its state.
func newIdleLink(t *testing.T, own string, seed []config.ManagerRef) *AgentLink {
	t.Helper()
	link, err := NewAgentLink(AgentLinkConfig{NodeID: "agent-1", StatePath: filepath.Join(t.TempDir(), "agent-state.json"), Advertise: own}, seed)
	if err != nil {
		t.Fatal(err)
	}
	return link
}

// A follower names the leader by ID and by its registered (LAN) address. An
// agent on Tailscale goes to the leader's Tailscale address, not the LAN one
// the pong spells out.
func TestAPongNamesTheLeaderByID(t *testing.T) {
	m2 := config.ManagerRef{ID: "m2", GRPCAddress: "192.0.2.11:3629", Addresses: []string{"192.0.2.11", "100.64.0.11"}}
	link := newIdleLink(t, "100.64.0.30", []config.ManagerRef{m2, m1Ref()})
	p := &pb.HeartbeatPong{LeaderId: "m1", LeaderGrpcAddress: "192.0.2.10:3629", Managers: []*pb.NodeInfo{
		{Id: "m2", GrpcAddress: "192.0.2.11:3629", Addresses: []string{"192.0.2.11", "100.64.0.11"}, Kind: string(KindManager)},
		m1Info(),
	}}
	if err := link.applyPong(p, ""); err != nil {
		t.Fatal(err)
	}
	if got := link.redirectTarget(p); got != "100.64.0.10:3629" {
		t.Fatalf("redirected to %s, want the leader's Tailscale address", got)
	}
	if got := nextTargets(link.snapshot(), link.Advertise); len(got) == 0 || got[0] != "100.64.0.10:3629" {
		t.Fatalf("after the redirect the link tries %v first", got)
	}
}

// The answers to the panel-CA report, a key rotation and a renewal name the
// leader by its gRPC address alone: the agent finds the manager registered
// there and dials it on its own network; an address it has no manager at is
// dialled as it is.
func TestAnRPCAnswerNamingTheLeaderByAddressIsDialledOnTheAgentsNetwork(t *testing.T) {
	link := newIdleLink(t, "100.64.0.30", []config.ManagerRef{m1Ref()})
	if got := link.leaderAt("192.0.2.10:3629"); got != "100.64.0.10:3629" {
		t.Fatalf("the leader at 192.0.2.10:3629 is dialled at %s", got)
	}
	if got := link.leaderAt("192.0.2.99:3629"); got != "192.0.2.99:3629" {
		t.Fatalf("a leader the agent does not list is dialled at %s", got)
	}
}

// The leader's list, now with addresses, is saved once: the same list in
// the next pong is not a change, and the state file is not written again.
func TestAManagerListWithAddressesIsSavedAndCompared(t *testing.T) {
	link := newIdleLink(t, "100.64.0.30", []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.10:3629"}})
	pong := func() *pb.HeartbeatPong {
		return &pb.HeartbeatPong{FromLeader: true, LeaderId: "m1", LeaderGrpcAddress: "192.0.2.10:3629", Managers: []*pb.NodeInfo{m1Info()}}
	}
	if err := link.applyPong(pong(), ""); err != nil {
		t.Fatal(err)
	}
	st, err := LoadAgentState(link.StatePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Managers) != 1 || !slices.Equal(st.Managers[0].Addresses, []string{"192.0.2.10", "100.64.0.10"}) {
		t.Fatalf("saved managers %+v", st.Managers)
	}
	if err := os.Remove(link.StatePath); err != nil {
		t.Fatal(err)
	}
	if err := link.applyPong(pong(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link.StatePath); !os.IsNotExist(err) {
		t.Fatalf("the same list was saved again: %v", err)
	}
}

// routedDial stands loopback listeners in for the addresses a test lists,
// which no test host has, and records every address the link dials. An
// address with no route is refused without a dial.
type routedDial struct {
	mu     sync.Mutex
	routes map[string]string
	tried  []string
}

func (d *routedDial) dialled() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.tried)
}

func startRoutedLink(t *testing.T, tm *TLSManager, own string, seed []config.ManagerRef, routes map[string]string) (*AgentLink, *routedDial) {
	t.Helper()
	link, err := NewAgentLink(AgentLinkConfig{
		NodeID: "agent-1", TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"), Version: "test",
		Interval: 100 * time.Millisecond, Advertise: own,
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	d := &routedDial{routes: routes}
	link.dial = func(target string) (*GRPCClient, error) {
		d.mu.Lock()
		d.tried = append(d.tried, target)
		real, ok := d.routes[target]
		d.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("%s has no route in this test", target)
		}
		return DialManager(real, tm, link.ManagerIDs)
	}
	goRun(t, link)
	return link, d
}

// m1Leader is a fake leader m1 whose pongs list it with both its addresses.
func m1Leader(t *testing.T, ca *TLSManager) *fakeManager {
	t.Helper()
	return startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{pong: func(string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{FromLeader: true, LeaderId: "m1", LeaderGrpcAddress: "192.0.2.10:3629", Managers: []*pb.NodeInfo{m1Info()}}, nil
	}})
}

// An agent on Tailscale whose leader is registered at a LAN address the agent
// cannot reach: it dials the leader's Tailscale address first, and heartbeats
// there.
func TestAgentLinkReachesTheLeaderOnItsOwnNetwork(t *testing.T) {
	_, ca := setupCertDir(t)
	leader := m1Leader(t, ca)
	link, d := startRoutedLink(t, tlsWithRole(t, ca, "agent-1", CertRoleAgent), "100.64.0.30", []config.ManagerRef{m1Ref()},
		map[string]string{"100.64.0.10:3629": leader.self, "192.0.2.10:3629": closedAddr(t)})
	waitFor(t, "a heartbeat to the leader", func() bool { return leader.streams.Load() > 0 })
	if got := d.dialled(); got[0] != "100.64.0.10:3629" {
		t.Fatalf("dialled %v: the leader's Tailscale address was not first", got)
	}
	waitFor(t, "the address that worked to be saved", func() bool {
		st, err := LoadAgentState(link.StatePath, nil)
		return err == nil && st.Reached["m1"] == "100.64.0.10:3629"
	})
}

// The address a session went through is tried first next time, before the
// one the agent's network would rank first, and is saved once.
func TestTheAddressThatWorkedIsTriedFirst(t *testing.T) {
	_, ca := setupCertDir(t)
	leader := m1Leader(t, ca)
	// The Tailscale address does not answer; the LAN one does.
	link, d := startRoutedLink(t, tlsWithRole(t, ca, "agent-1", CertRoleAgent), "100.64.0.30", []config.ManagerRef{m1Ref()},
		map[string]string{"100.64.0.10:3629": closedAddr(t), "192.0.2.10:3629": leader.self})
	waitFor(t, "the address that worked to be saved", func() bool {
		st, err := LoadAgentState(link.StatePath, nil)
		return err == nil && st.Reached["m1"] == "192.0.2.10:3629"
	})
	if got := d.dialled(); !slices.Equal(got[:2], []string{"100.64.0.10:3629", "192.0.2.10:3629"}) {
		t.Fatalf("dialled %v", got)
	}
	if got := nextTargets(link.snapshot(), link.Advertise); !slices.Equal(got, []string{"192.0.2.10:3629", "100.64.0.10:3629"}) {
		t.Fatalf("next targets %v: the address that worked is not first", got)
	}
	// The same address again is no change: the state file is not rewritten.
	if err := os.Remove(link.StatePath); err != nil {
		t.Fatal(err)
	}
	link.noteReached("192.0.2.10:3629")
	if _, err := os.Stat(link.StatePath); !os.IsNotExist(err) {
		t.Fatalf("the same address was saved again: %v", err)
	}
}

// A follower answers a joining agent with every address of the leader. The
// agent is on Tailscale and dials the leader's Tailscale address, not the LAN
// one the follower names first, and registers there.
func TestRegistrationFollowsTheLeaderOnTheJoinersNetwork(t *testing.T) {
	mgr, leaderAddr, _, pin := realLeader(t)
	settleRotation(t, mgr)
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	follower := startFakeRegistrar(t, mgr.GetTLS(), &fakeRegistrar{answer: func(string, *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
		return &pb.RegisterAgentResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: "192.0.2.10:3629",
			LeaderAddresses: []string{"192.0.2.10:3629", "100.64.0.10:3629"}}
	}})
	e, _ := agentJoinTarget(t)
	var tried []string
	routes := map[string]string{follower.self: follower.self, "100.64.0.10:3629": leaderAddr, "192.0.2.10:3629": closedAddr(t)}
	e.dial = func(addr, pin string) (*GRPCClient, error) {
		tried = append(tried, addr)
		real, ok := routes[addr]
		if !ok {
			return nil, fmt.Errorf("%s has no route in this test", addr)
		}
		return DialPinnedLeader(real, pin)
	}
	res, err := e.Execute(follower.self, tok.Token, "100.64.0.30", pin, panelCAPEM(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tried, []string{follower.self, "100.64.0.10:3629"}) {
		t.Fatalf("dialled %v: the leader's Tailscale address was not tried first", tried)
	}
	if n := mgr.raft.GetFSM().GetNode(res.NodeID); n == nil || n.Kind != KindAgent {
		t.Fatalf("not registered with the leader: %+v", n)
	}
	if got := e.Config.Cluster.Managers; len(got) != 1 || len(got[0].Addresses) == 0 {
		t.Fatalf("the agent config lists %+v: the managers' addresses were not kept", got)
	}
}

// startImpostor serves f under a certificate another CA signed, while its
// pre-flight hands out the cluster CA's public key, which any caller can
// learn: what a host squatting on an address the leader is listed at could
// answer.
func startImpostor(t *testing.T, cluster, other *TLSManager, f *fakeRegistrar) *fakeRegistrar {
	t.Helper()
	f.caKey = caPublicKey(t, cluster)
	cfg, err := tlsWithRole(t, other, "m1", CertRoleManager).ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.self = lis.Addr().String()
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)))
	pb.RegisterClusterServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return f
}

// The leader's first address on the joiner's network answers the pre-flight
// with the CA's public key but fails the pinned handshake: someone else holds
// that address. The join moves on to the leader's next address instead of
// stopping, and the impostor never sees the request.
func TestARegistrationMovesPastAnAddressThatFailsThePinnedHandshake(t *testing.T) {
	mgr, leaderAddr, _, pin := realLeader(t)
	settleRotation(t, mgr)
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	_, other := setupCertDir(t)
	impostor := startImpostor(t, mgr.GetTLS(), other, &fakeRegistrar{})
	follower := startFakeRegistrar(t, mgr.GetTLS(), &fakeRegistrar{answer: func(string, *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
		return &pb.RegisterAgentResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: "192.0.2.10:3629",
			LeaderAddresses: []string{"192.0.2.10:3629", "100.64.0.10:3629"}}
	}})
	e, _ := agentJoinTarget(t)
	routes := map[string]string{follower.self: follower.self, "100.64.0.10:3629": impostor.self, "192.0.2.10:3629": leaderAddr}
	var tried []string
	e.dial = func(addr, pin string) (*GRPCClient, error) {
		tried = append(tried, addr)
		real, ok := routes[addr]
		if !ok {
			return nil, fmt.Errorf("%s has no route in this test", addr)
		}
		return DialPinnedLeader(real, pin)
	}
	res, err := e.Execute(follower.self, tok.Token, "100.64.0.30", pin, panelCAPEM(t))
	if err != nil {
		t.Fatalf("the join stopped at the impostor (dialled %v): %v", tried, err)
	}
	if !slices.Equal(tried, []string{follower.self, "100.64.0.10:3629", "192.0.2.10:3629"}) {
		t.Fatalf("dialled %v", tried)
	}
	if n := mgr.raft.GetFSM().GetNode(res.NodeID); n == nil || n.Kind != KindAgent {
		t.Fatalf("not registered with the leader: %+v", n)
	}
	if impostor.registers.Load() != 0 {
		t.Fatal("the request reached the impostor")
	}
}
