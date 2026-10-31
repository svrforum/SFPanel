package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

func applyNodeUpdate(t *testing.T, f *FSM, n Node) {
	t.Helper()
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	applyCmd(t, f, Command{Type: CmdUpdateNode, Value: data})
}

// A node update that names addresses replaces them; one that does not keeps them.
func TestAnUpdateCarriesANodesAddresses(t *testing.T) {
	f := NewFSM()
	addTestNode(t, f, Node{ID: "m1", Name: "m1", GRPCAddress: "192.0.2.10:3629", APIAddress: "192.0.2.10:3628", Role: RoleVoter})
	applyNodeUpdate(t, f, Node{ID: "m1", Addresses: []string{"192.0.2.10", "100.64.0.10"}})
	if got := f.GetNode("m1").AllAddresses(); !slices.Equal(got, []string{"192.0.2.10", "100.64.0.10"}) {
		t.Fatalf("after the update: %v", got)
	}
	applyNodeUpdate(t, f, Node{ID: "m1", Status: StatusOnline})
	if got := f.GetNode("m1").AllAddresses(); len(got) != 2 {
		t.Fatalf("a status update dropped the addresses: %v", got)
	}
}

// A node with no recorded addresses has its gRPC host.
func TestANodeWithoutAddressesHasItsGRPCHost(t *testing.T) {
	n := &Node{GRPCAddress: "192.0.2.10:3629"}
	if got := n.AllAddresses(); !slices.Equal(got, []string{"192.0.2.10"}) {
		t.Fatal(got)
	}
}

// What AllAddresses returns is the caller's: changing it leaves the record as
// it was.
func TestAllAddressesIsACopy(t *testing.T) {
	f := NewFSM()
	addTestNode(t, f, Node{ID: "m1", GRPCAddress: "192.0.2.10:3629"})
	applyNodeUpdate(t, f, Node{ID: "m1", Addresses: []string{"192.0.2.10", "100.64.0.10"}})
	st := f.GetState()
	got := st.Nodes["m1"].AllAddresses()
	got[0] = "198.51.100.1"
	if again := f.GetNode("m1").AllAddresses(); again[0] != "192.0.2.10" {
		t.Fatalf("changing the returned list changed the record: %v", again)
	}
}

// Agents and pongs learn every address of every manager.
func TestManagerInfosCarryEveryAddress(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	f := m.raft.GetFSM()
	addTestNode(t, f, Node{ID: "m1", Name: "m1", GRPCAddress: "192.0.2.10:3629", APIAddress: "192.0.2.10:3628", Role: RoleVoter, Kind: KindManager})
	applyNodeUpdate(t, f, Node{ID: "m1", Addresses: []string{"192.0.2.10", "100.64.0.10"}})
	infos := m.managerInfos()
	if len(infos) != 1 || !slices.Equal(infos[0].Addresses, []string{"192.0.2.10", "100.64.0.10"}) {
		t.Fatalf("managerInfos: %v", infos)
	}
}

// A follower sends an agent to every address of the leader, each with the
// leader's gRPC port.
func TestTheLeaderIsDialledAtEveryAddress(t *testing.T) {
	n := &Node{GRPCAddress: "192.0.2.10:3629", Addresses: []string{"192.0.2.10", "100.64.0.10", "2001:db8::10"}}
	want := []string{"192.0.2.10:3629", "100.64.0.10:3629", "[2001:db8::10]:3629"}
	if got := grpcDialAddresses(n); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := grpcDialAddresses(nil); got != nil {
		t.Fatalf("no leader: %v", got)
	}
	if got := grpcDialAddresses(&Node{GRPCAddress: "not-an-address"}); got != nil {
		t.Fatalf("a gRPC address without a port: %v", got)
	}
}

// A follower's refusal names every address of the leader, so an agent that
// cannot reach the one it was given can try the others.
func TestAFollowerNamesEveryAddressOfTheLeader(t *testing.T) {
	nodes, _ := inmemRaft(t, "m1", "m2")
	leader, follower := nodes[0], nodes[1]
	if err := leader.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "m1", Kind: KindManager, Role: RoleVoter, GRPCAddress: "198.51.100.1:3629", Addresses: []string{"198.51.100.1", "100.64.0.1"}})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); follower.GetFSM().GetNode("m1") == nil || follower.LeaderID() != "m1"; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the follower did not catch up")
		}
	}
	s := &GRPCServer{manager: &Manager{raft: follower}}
	resp, err := s.RegisterAgent(context.Background(), &pb.RegisterAgentRequest{Token: "t"})
	if err != nil || resp.Error != ErrNotLeader.Error() || resp.LeaderGrpcAddress != "198.51.100.1:3629" ||
		!slices.Equal(resp.LeaderAddresses, []string{"198.51.100.1:3629", "100.64.0.1:3629"}) {
		t.Fatalf("a follower's answer: %v %+v", err, resp)
	}
}

// An agent cannot register at an IP a manager holds as an extra address: the
// certificate it would get names that IP, and peers that reach the manager
// there would accept it as the manager.
func TestAnAgentCannotTakeAManagersExtraAddress(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	data, _ := json.Marshal(Node{ID: mgr.LocalNodeID(), Addresses: []string{"127.0.0.1", "100.64.0.10"}})
	if err := mgr.raft.Apply(Command{Type: CmdUpdateNode, Value: data}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	in := registerInput(t, tok.Token)
	in.APIAddress, in.GRPCAddress = "100.64.0.10:3628", "100.64.0.10:3629"
	if _, err := mgr.HandleRegisterAgent(in); !errors.Is(err, ErrNodeIdentityTaken) || err.Error() != "another node already uses this name or address" {
		t.Fatalf("got %v, want %v", err, ErrNodeIdentityTaken)
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("the refusal consumed the token: %v", err)
	}
}
