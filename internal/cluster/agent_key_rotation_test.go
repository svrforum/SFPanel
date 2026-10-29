package cluster

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/protobuf/proto"
)

// keyAgent is a real leader with a registered agent — its agent key in tm,
// as the join leaves it — heard from on code that follows an agent key
// rotation.
func keyAgent(t *testing.T) (*Manager, *RegisterAgentResult, *TLSManager) {
	t.Helper()
	mgr, _, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	reportKeyCapable(mgr, res.NodeID)
	return mgr, res, tm
}

func reportKeyCapable(mgr *Manager, id string) {
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: id, Version: "test", Capabilities: []string{CapCARotation, CapAgentKeyRotation}})
}

func agentKeyIn(m *Manager, id string) string {
	v, _ := m.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(id))
	return v
}

// The start waits for every manager, as every rotation does. An online agent
// on a release that cannot take a new key would stay on its old one
// unnoticed: the start names it. One this leader has not heard from does not
// hold the start — it moves when it is back.
func TestStartAgentKeyRotationGates(t *testing.T) {
	mgr, res, _ := keyAgent(t)
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartAgentKeyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "m2 (not heard from)") {
		t.Fatalf("a manager not heard from: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{CapAgents, CapAgentKeyRotation}})
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: res.NodeID, Version: "0.79.0"})
	if _, err := mgr.StartAgentKeyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "agent-host (0.79.0)") {
		t.Fatalf("an online agent on an older release: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyAgentKeysRotation); v != "" {
		t.Fatal("a refused start wrote the record")
	}
	reportKeyCapable(mgr, res.NodeID)
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a2", Name: "a2", Kind: KindAgent})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if gen, err := mgr.StartAgentKeyRotation(); err != nil || gen != 1 {
		t.Fatalf("with an agent this leader has not heard from: %d %v", gen, err)
	}
	// A record that does not parse would start again at generation 1, which
	// an agent that switched at it would be told it holds.
	if err := mgr.SetConfig(configKeyAgentKeysRotation, "{"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartAgentKeyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("over a record that does not parse: %v", err)
	}
}

// The agent takes the key issued to it with a proof made with the key it
// holds, and says it holds the new one with a proof made with that. Only then
// do the managers sign with the new key, and the old one is gone from
// replicated state.
func TestTheManagersMoveToAnAgentsNewKeyOnlyOnceItConfirms(t *testing.T) {
	mgr, res, tm := keyAgent(t)
	oldKey := agentKeyIn(mgr, res.NodeID)
	gen, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	if h := mgr.agentKeyHint(res.NodeID); h == nil || h.Generation != gen || h.Current {
		t.Fatalf("hint before the move: %+v", h)
	}
	leaf, _ := tm.NodeCertLeaf()
	fetch := func() (string, bool, error) {
		return mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(oldKey, "fetch", res.NodeID, gen), false)
	}
	newKey, switched, err := fetch()
	if err != nil || switched || newKey == "" || newKey == oldKey {
		t.Fatalf("fetch: switched %v, a new key issued %v (%v)", switched, newKey != "" && newKey != oldKey, err)
	}
	if again, _, err := fetch(); err != nil || again != newKey {
		t.Fatal("a second fetch issued another key: the agent may have kept the first answer")
	}
	if agentKeyIn(mgr, res.NodeID) != oldKey {
		t.Fatal("the managers sign with the new key before the agent confirmed it holds it")
	}
	confirm := func() (bool, error) {
		_, switched, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(newKey, "confirm", res.NodeID, gen), true)
		return switched, err
	}
	if switched, err := confirm(); err != nil || !switched {
		t.Fatalf("confirm: %v %v", switched, err)
	}
	if agentKeyIn(mgr, res.NodeID) != newKey {
		t.Fatal("the managers do not sign with the new key after the confirmation")
	}
	for k, v := range mgr.raft.GetFSM().GetState().Config {
		if strings.Contains(v, oldKey) {
			t.Fatalf("the old key is still in replicated state, under %s", k)
		}
		if strings.HasPrefix(k, agentKeyNextPrefix) && strings.Contains(v, newKey) {
			t.Fatal("the rotation's record still holds the key after the switch")
		}
	}
	if h := mgr.agentKeyHint(res.NodeID); h == nil || !h.Current {
		t.Fatalf("hint after the move: %+v", h)
	}
	// An agent whose confirmation's answer was lost asks again.
	if switched, err := confirm(); err != nil || !switched {
		t.Fatalf("a repeated confirm: %v %v", switched, err)
	}
	if _, _, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(oldKey, "confirm", res.NodeID, gen), true); !errors.Is(err, ErrAgentKeyProof) {
		t.Fatalf("a repeated confirm made with the old key: %v", err)
	}
	// The next rotation replaces this one: the agent moves again, from the
	// key it moved to.
	gen2, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	if h := mgr.agentKeyHint(res.NodeID); h == nil || h.Generation != gen2 || h.Current {
		t.Fatalf("hint once the next rotation started: %+v", h)
	}
	if k, switched, err := mgr.HandleRotateAgentKey(leaf, gen2, agentKeyProof(newKey, "fetch", res.NodeID, gen2), false); err != nil || switched || k == "" || k == newKey {
		t.Fatalf("a fetch in the next rotation: switched %v, a new key issued %v (%v)", switched, k != "" && k != newKey, err)
	}
}

// The certificate alone gets no key: whoever holds the CA key could make one
// for any agent's ID. A fetch needs the key the agent holds, a confirm the
// key issued to it, and each the rotation in progress.
func TestRotateAgentKeyRefusals(t *testing.T) {
	mgr, res, tm := keyAgent(t)
	oldKey := agentKeyIn(mgr, res.NodeID)
	leaf, _ := tm.NodeCertLeaf()
	call := func(gen int64, key, action string, confirm bool) error {
		_, _, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(key, action, res.NodeID, gen), confirm)
		return err
	}
	if err := call(1, oldKey, "fetch", false); !errors.Is(err, ErrNoAgentKeyDue) {
		t.Fatalf("with no rotation: %v", err)
	}
	gen, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	if err := call(gen+1, oldKey, "fetch", false); !errors.Is(err, ErrNoAgentKeyDue) {
		t.Fatalf("another generation: %v", err)
	}
	if err := call(gen, "another-key", "fetch", false); !errors.Is(err, ErrAgentKeyProof) {
		t.Fatalf("a fetch proved with another key: %v", err)
	}
	if err := call(gen, oldKey, "confirm", false); !errors.Is(err, ErrAgentKeyProof) {
		t.Fatalf("a confirm's proof taken for a fetch: %v", err)
	}
	if err := call(gen, oldKey, "confirm", true); !errors.Is(err, ErrNoAgentKeyDue) {
		t.Fatalf("a confirm before any fetch: %v", err)
	}
	if err := call(gen, oldKey, "fetch", false); err != nil {
		t.Fatal(err)
	}
	if err := call(gen, oldKey, "confirm", true); !errors.Is(err, ErrAgentKeyProof) {
		t.Fatalf("a confirm made with the old key: %v", err)
	}
	if agentKeyIn(mgr, res.NodeID) != oldKey {
		t.Fatal("a refused confirm moved the managers")
	}
	// A manager has no agent key to rotate: its certificate gets nothing.
	own, err := mgr.tls.NodeCertLeaf()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.HandleRotateAgentKey(own, gen, agentKeyProof(oldKey, "fetch", own.Subject.CommonName, gen), false); !errors.Is(err, ErrNoAgentKeyDue) {
		t.Fatalf("a manager's certificate: %v", err)
	}
	// With no key on record there is nothing to prove: a proof made with ""
	// is anyone's.
	if err := mgr.raft.Apply(Command{Type: CmdDeleteConfig, Key: AgentKeyConfigKey(res.NodeID)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.raft.Apply(Command{Type: CmdDeleteConfig, Key: AgentKeyNextConfigKey(res.NodeID)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := call(gen, "", "fetch", false); !errors.Is(err, ErrAgentKeyProof) {
		t.Fatalf("with no agent key on record: %v", err)
	}
}

// The hint rides the leader's pong to that agent and carries no key; a node
// that is not the leader says nothing.
func TestTheLeaderTellsAnAgentOfItsKeyRotation(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	reportKeyCapable(mgr, res.NodeID)
	gen, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := tm.NodeCertLeaf()
	issued, _, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(res.AgentKey, "fetch", res.NodeID, gen), false)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialManager(addr, tm, func() []string { return []string{mgr.LocalNodeID()} })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.OpenAgentHeartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.HeartbeatPing{NodeId: res.NodeID, Timestamp: time.Now().Unix(), Version: "test"}); err != nil {
		t.Fatal(err)
	}
	pong, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if h := pong.GetAgentKey(); h.GetGeneration() != gen || h.GetCurrent() {
		t.Fatalf("hint %+v", h)
	}
	if wire, _ := proto.Marshal(pong); bytes.Contains(wire, []byte(issued)) || bytes.Contains(wire, []byte(res.AgentKey)) {
		t.Fatal("the pong carries a key")
	}
	if (&Manager{raft: &RaftNode{fsm: mgr.raft.GetFSM()}}).agentKeyHint(res.NodeID) != nil {
		t.Fatal("a node that is not the leader sent a hint")
	}
}

// A removed agent's issued key goes with its record: kept, it would stay in
// replicated state for a node that no longer exists.
func TestRemovingAnAgentTakesItsIssuedKeyWithIt(t *testing.T) {
	mgr, res, tm := keyAgent(t)
	gen, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := tm.NodeCertLeaf()
	if _, _, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(res.AgentKey, "fetch", res.NodeID, gen), false); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RemoveNode(res.NodeID); err != nil {
		t.Fatal(err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyNextConfigKey(res.NodeID)); v != "" {
		t.Fatal("the issued key stayed")
	}
}

func TestAManagerReportsAgentKeyRotation(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if !slices.Contains(m.heartbeatCapabilities(), CapAgentKeyRotation) {
		t.Fatal("CapAgentKeyRotation not reported")
	}
}
