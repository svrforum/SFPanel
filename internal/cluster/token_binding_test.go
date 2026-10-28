package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A token bound to a node is good for that node, from its address, only:
// unbound, whoever held it could register as that node somewhere else, and
// the managers would send the node's terminals there.
func TestABoundAgentTokenIsForItsNodeOnly(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	tok, err := mgr.CreateBoundJoinToken(time.Hour, KindAgent, "node-x", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RegisterAgentInput){
		"another ID":      func(in *RegisterAgentInput) { in.NodeID = "node-y" },
		"no ID":           func(in *RegisterAgentInput) { in.NodeID = "" },
		"another address": func(in *RegisterAgentInput) { in.APIAddress, in.GRPCAddress = "192.0.2.11:3628", "192.0.2.11:3629" },
	} {
		in := registerInput(t, tok.Token)
		in.NodeID = "node-x"
		mutate(&in)
		if _, err := mgr.HandleRegisterAgent(in); !errors.Is(err, ErrTokenBinding) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("a refusal spent the token: %v", err)
	}
	free, _ := mgr.CreateJoinToken(time.Hour, KindAgent)
	in := registerInput(t, free.Token)
	in.NodeID = "node-x"
	if _, err := mgr.HandleRegisterAgent(in); !errors.Is(err, ErrTokenBinding) {
		t.Fatalf("an unbound token asking for an ID: %v", err)
	}
}

// The binding is saved with the token: loaded without it after a leader
// restart, the token would refuse the node it was made for — an ID asked for
// with a token made for none.
func TestABoundTokenKeepsItsBindingAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	tm, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatal(err)
	}
	jt, err := tm.CreateFor(time.Hour, "leader", KindAgent, "node-x", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if tm, err = NewPersistedTokenManager(path); err != nil {
		t.Fatal(err)
	}
	if got, err := tm.Peek(jt.Token); err != nil || got.NodeID != "node-x" || got.IP != "192.0.2.10" {
		t.Fatalf("after a restart: %+v %v", got, err)
	}
}

// A manager converted to an agent comes back under its own ID, with the name
// and labels it had: the leader kept them in the conversion's intent record.
func TestAConvertedNodeComesBackWithItsIDAndLabels(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	joined := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	intent, _ := json.Marshal(ConversionIntent{Name: "db-1", Labels: map[string]string{"group": "db"}, JoinedAt: joined})
	if err := mgr.SetConfig(ConversionIntentKey("node-x"), string(intent)); err != nil {
		t.Fatal(err)
	}
	tok, _ := mgr.CreateBoundJoinToken(time.Hour, KindAgent, "node-x", "192.0.2.10")
	in := registerInput(t, tok.Token)
	in.NodeID = "node-x"
	res, err := mgr.HandleRegisterAgent(in)
	if err != nil {
		t.Fatal(err)
	}
	n := mgr.raft.GetFSM().GetNode("node-x")
	if res.NodeID != "node-x" || n == nil || n.Kind != KindAgent || n.Name != "db-1" || n.Labels["group"] != "db" || !n.JoinedAt.Equal(joined) {
		t.Fatalf("registered %+v as %+v", res.NodeID, n)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(ConversionIntentKey("node-x")); v != "" {
		t.Fatal("the intent record stayed")
	}
}

// A node comes back under its ID only once its record is gone. A record still
// there, at another name and address, is no conflict identityTaken sees, and
// the registration would write an agent's record over it.
func TestARegistrationUnderAnIDStillRecordedIsRefused(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	still := Node{ID: "node-x", Name: "other", APIAddress: "192.0.2.20:3628", GRPCAddress: "192.0.2.20:3629", Kind: KindAgent}
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(still)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tok, _ := mgr.CreateBoundJoinToken(time.Hour, KindAgent, "node-x", "192.0.2.10")
	in := registerInput(t, tok.Token)
	in.NodeID = "node-x"
	if _, err := mgr.HandleRegisterAgent(in); !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("got %v", err)
	}
	if n := mgr.raft.GetFSM().GetNode("node-x"); n == nil || n.Name != "other" || n.GRPCAddress != still.GRPCAddress {
		t.Fatalf("the record became %+v", n)
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("the refusal spent the token: %v", err)
	}
}

// An agent promoted to a manager keeps its ID, name and labels, and its
// agent key goes: the managers sign for a manager with their own secret.
func TestABoundManagerJoinPromotesTheAgentRecord(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	// The agent's record and key as a registration writes them, but on
	// loopback at a free port pair: the join makes it a Raft non-voter at its
	// gRPC port + 1, which the leader then dials. A documentation address is
	// dialled through this host's default route and hangs for the transport's
	// 10 s, which the leader's shutdown at the end of the test waits out.
	port := freePortPair(t)
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", port)
	res := Node{ID: "agent-1", Name: "agent-host", APIAddress: "127.0.0.1:1", GRPCAddress: grpcAddr, Kind: KindAgent, Status: StatusOnline}
	if err := mgr.SetConfig(AgentKeyConfigKey(res.ID), "the-agent-key"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(res)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateNodeLabels(res.ID, map[string]string{"group": "edge"}); err != nil {
		t.Fatal(err)
	}
	// A second agent, registered while every manager is heard from.
	agentTok, _ := mgr.CreateJoinToken(time.Hour, KindAgent)
	in := registerInput(t, agentTok.Token)
	in.NodeName, in.APIAddress, in.GRPCAddress = "agent-2", "192.0.2.12:3628", "192.0.2.12:3629"
	other, err := mgr.HandleRegisterAgent(in)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := mgr.CreateBoundJoinToken(time.Hour, KindManager, res.ID, "127.0.0.1")
	// The agent holds its Raft port while it joins, as BecomeManager does:
	// the leader dials it before it makes the record a manager's.
	held, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+1))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = mgr.HandleJoin(res.ID, "renamed", res.APIAddress, res.GRPCAddress, tok.Token, []string{CapAgents})
	_ = held.Close()
	if err != nil {
		t.Fatal(err)
	}
	n := mgr.raft.GetFSM().GetNode(res.ID)
	if n == nil || !n.IsManager() || n.Role != RoleNonVoter || n.Labels["group"] != "edge" || n.Name != "agent-host" {
		t.Fatalf("record %+v", n)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.ID)); v != "" {
		t.Fatal("the agent key stayed")
	}
	// Unbound, an agent's ID is taken, as before: a manager token makes no
	// agent a manager.
	free, _ := mgr.CreateJoinToken(time.Hour, KindManager)
	if _, _, _, _, err := mgr.HandleJoin(other.NodeID, "n", "192.0.2.12:3628", "192.0.2.12:3629", free.Token, []string{CapAgents}); !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("an unbound token for an agent's ID: %v", err)
	}
	if n := mgr.raft.GetFSM().GetNode(other.NodeID); n == nil || n.Kind != KindAgent {
		t.Fatalf("the agent's record became %+v", n)
	}
}

// A manager token bound to an agent admits that agent at its address and no
// other join: not another ID, not an address elsewhere — the panel's either,
// where the managers relay the node's terminals to — and not an ID that is a
// manager's or has no record.
func TestABoundManagerTokenIsForItsNodeOnly(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	res, _ := registeredAgent(t, mgr)
	tok, err := mgr.CreateBoundJoinToken(time.Hour, KindManager, res.NodeID, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	for name, join := range map[string]struct{ id, api, grpc string }{
		"another ID":            {"node-y", "192.0.2.10:3628", "192.0.2.10:3629"},
		"another gRPC address":  {res.NodeID, "192.0.2.10:3628", "192.0.2.11:3629"},
		"another panel address": {res.NodeID, "192.0.2.11:3628", "192.0.2.10:3629"},
	} {
		if _, _, _, _, err := mgr.HandleJoin(join.id, "n", join.api, join.grpc, tok.Token, []string{CapAgents}); !errors.Is(err, ErrTokenBinding) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("a refusal spent the token: %v", err)
	}
	if n := mgr.raft.GetFSM().GetNode(res.NodeID); n == nil || n.Kind != KindAgent {
		t.Fatalf("record %+v", n)
	}

	self, _ := mgr.CreateBoundJoinToken(time.Hour, KindManager, mgr.LocalNodeID(), "127.0.0.1")
	if _, _, _, _, err := mgr.HandleJoin(mgr.LocalNodeID(), "n", "127.0.0.1:1", "127.0.0.1:2", self.Token, []string{CapAgents}); !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("a manager's ID: %v", err)
	}
	gone, _ := mgr.CreateBoundJoinToken(time.Hour, KindManager, "node-gone", "192.0.2.13")
	if _, _, _, _, err := mgr.HandleJoin("node-gone", "n", "192.0.2.13:3628", "192.0.2.13:3629", gone.Token, []string{CapAgents}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("an ID with no record: %v", err)
	}
}

// A promotion Raft refuses — here because the agent's Raft address would be
// the leader's own — leaves the agent as it was: its record and key back, out
// of Raft, and the token good for a retry.
func TestAFailedPromotionPutsTheAgentBack(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	agent := Node{
		ID: "a1", Name: "a1-host", APIAddress: "127.0.0.1:1", GRPCAddress: addr, Kind: KindAgent,
		Labels: map[string]string{"group": "edge"}, JoinedAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Second),
	}
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(agent)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig(AgentKeyConfigKey("a1"), "the-agent-key"); err != nil {
		t.Fatal(err)
	}
	tok, err := mgr.CreateBoundJoinToken(time.Hour, KindManager, "a1", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = mgr.HandleJoin("a1", "n", agent.APIAddress, agent.GRPCAddress, tok.Token, []string{CapAgents})
	if err == nil || !strings.Contains(err.Error(), "add nonvoter") {
		t.Fatalf("got %v, want Raft's refusal to add the node", err)
	}
	n := mgr.raft.GetFSM().GetNode("a1")
	if n == nil || n.Kind != KindAgent || n.Role != "" || n.Name != "a1-host" || n.Labels["group"] != "edge" || !n.JoinedAt.Equal(agent.JoinedAt) {
		t.Fatalf("record %+v", n)
	}
	if k, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey("a1")); k != "the-agent-key" {
		t.Fatalf("the agent key is %q", k)
	}
	if _, listed, _ := mgr.raft.Suffrage("a1"); listed {
		t.Fatal("left in Raft")
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("the token was not put back: %v", err)
	}
}
