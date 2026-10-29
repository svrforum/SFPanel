package cluster

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
)

// installed records what AgentKeys hands the process — what the panel signs
// its loopback hop with and accepts from the managers. A real leader runs in
// these tests, and the process-wide secrets are its as well.
type installed struct {
	mu     sync.Mutex
	sign   string
	accept []string
}

func (i *installed) set(sign string, accept []string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.sign, i.accept = sign, slices.Clone(accept)
}

func (i *installed) get() (string, []string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.sign, slices.Clone(i.accept)
}

func agentKeysWith(t *testing.T, key string) (*AgentKeys, *TLSManager, *installed) {
	t.Helper()
	tm := NewTLSManager(filepath.Join(t.TempDir(), "certs"))
	if err := tm.SaveAgentKey(key); err != nil {
		t.Fatal(err)
	}
	in := &installed{}
	k, err := LoadAgentKeys(tm, in.set)
	if err != nil {
		t.Fatal(err)
	}
	k.Install(time.Now())
	return k, tm, in
}

// Until the managers sign with the new key the agent signs with the old one
// and accepts both, and agent.key stays the managers' key — all an older
// release reads. After the switch it signs with the new one, accepts the old
// one for a while, and the file keeps no key.
func TestAgentKeysTakeThenSwitch(t *testing.T) {
	k, tm, in := agentKeysWith(t, "k1")
	if err := k.Take("k2", 1); err != nil {
		t.Fatal(err)
	}
	if sign, accept := in.get(); sign != "k1" || !slices.Equal(accept, []string{"k1", "k2"}) {
		t.Fatalf("after the take: sign %q accept %v", sign, accept)
	}
	if onDisk, _ := tm.LoadAgentKey(); onDisk != "k1" {
		t.Fatal("agent.key changed before the managers sign with the new key")
	}
	now := time.Now()
	if err := k.Switch(1, now); err != nil {
		t.Fatal(err)
	}
	if sign, accept := in.get(); sign != "k2" || !slices.Equal(accept, []string{"k2", "k1"}) {
		t.Fatalf("after the switch: sign %q accept %v", sign, accept)
	}
	k.Install(now.Add(legacyProxyGrace))
	if _, accept := in.get(); !slices.Equal(accept, []string{"k2"}) {
		t.Fatalf("the old key outlived its window: %v", accept)
	}
	if onDisk, _ := tm.LoadAgentKey(); onDisk != "k2" {
		t.Fatal("agent.key is not the managers' key after the switch")
	}
	if key, gen := k.Current(); key != "k2" || gen != 1 {
		t.Fatalf("current generation %d", gen)
	}
	if data, _ := os.ReadFile(filepath.Join(tm.certDir, AgentKeyFile)); strings.Contains(string(data), "k1") || strings.Contains(string(data), "k2") {
		t.Fatal("the rotation's file kept a key after the switch")
	}
}

// A crash between writing agent.key and the file leaves the new key in both:
// the boot finishes the switch rather than wait for a key it holds.
func TestAgentKeysFinishASwitchACrashCut(t *testing.T) {
	tm := NewTLSManager(filepath.Join(t.TempDir(), "certs"))
	if err := tm.SaveAgentKey("k2"); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(tm.certDir, AgentKeyFile), mustJSON(agentKeyState{Next: "k2", NextGeneration: 1})); err != nil {
		t.Fatal(err)
	}
	k, err := LoadAgentKeys(tm, func(string, []string) {})
	if err != nil {
		t.Fatal(err)
	}
	if key, gen := k.Current(); key != "k2" || gen != 1 {
		t.Fatalf("current generation %d", gen)
	}
	if next, _ := k.Pending(); next != "" {
		t.Fatal("the switch was left pending")
	}
}

// A file that does not parse leaves the agent on agent.key alone, as an older
// release runs: that is the managers' key until the agent confirms another.
func TestAgentKeysIgnoreAFileThatDoesNotParse(t *testing.T) {
	tm := NewTLSManager(filepath.Join(t.TempDir(), "certs"))
	if err := tm.SaveAgentKey("k1"); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(tm.certDir, AgentKeyFile), []byte("{")); err != nil {
		t.Fatal(err)
	}
	in := &installed{}
	k, err := LoadAgentKeys(tm, in.set)
	if err != nil {
		t.Fatal(err)
	}
	k.Install(time.Now())
	if sign, accept := in.get(); sign != "k1" || !slices.Equal(accept, []string{"k1"}) {
		t.Fatalf("sign %q accept %v", sign, accept)
	}
}

// startLinkWith runs a link for the agent whose certificate is in tm, with
// its keys.
func startLinkWith(t *testing.T, tm *TLSManager, nodeID string, keys *AgentKeys, seed []config.ManagerRef) *AgentLink {
	t.Helper()
	link, err := NewAgentLink(AgentLinkConfig{NodeID: nodeID, TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"), Version: "test",
		Interval: 100 * time.Millisecond, Advertise: "127.0.0.1", Keys: keys}, seed)
	if err != nil {
		t.Fatal(err)
	}
	goRun(t, link)
	return link
}

// A real agent moves to a new key over its link: the leader's pong says a
// rotation is on, the agent fetches its key and confirms it, and the
// managers then sign with the key the agent holds, which the agent signs
// with too while it still accepts the old one.
func TestAnAgentMovesToANewKeyOverItsLink(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	in := &installed{}
	keys, err := LoadAgentKeys(tm, in.set)
	if err != nil {
		t.Fatal(err)
	}
	startLinkWith(t, tm, res.NodeID, keys, []config.ManagerRef{{ID: mgr.LocalNodeID(), GRPCAddress: addr}})
	waitFor(t, "a heartbeat that says the agent can rotate its key", func() bool {
		met := mgr.GetHeartbeat().GetMetrics(res.NodeID)
		return met != nil && slices.Contains(met.Capabilities, CapAgentKeyRotation)
	})
	gen, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to move to its new key", func() bool { _, g := keys.Current(); return g == gen })
	key, _ := keys.Current()
	if key == res.AgentKey || agentKeyIn(mgr, res.NodeID) != key {
		t.Fatal("the managers do not sign with the key the agent moved to")
	}
	if sign, accept := in.get(); sign != key || !slices.Contains(accept, res.AgentKey) {
		t.Fatal("right after the move the agent signs with another key, or no longer accepts the old one")
	}
}

// A leader that cannot rotate keys — a manager from before them answers
// Unimplemented — leaves the agent on its key and the link running; it is
// not a manager from before agents. The agent asks again a minute later, not
// on every pong.
func TestAnAgentKeepsItsKeyWhenTheLeaderCannotRotateIt(t *testing.T) {
	logs := captureLogs(t)
	_, ca := setupCertDir(t)
	mgr := startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{pong: func(self string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{FromLeader: true, LeaderGrpcAddress: self, AgentKey: &pb.AgentKeyHint{Generation: 1}}, nil
	}})
	agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
	if err := agent.SaveAgentKey("k1"); err != nil {
		t.Fatal(err)
	}
	keys, err := LoadAgentKeys(agent, func(string, []string) {})
	if err != nil {
		t.Fatal(err)
	}
	link := startLinkWith(t, agent, "agent-1", keys, []config.ManagerRef{{ID: "m1", GRPCAddress: mgr.self}})
	waitFor(t, "the agent to ask for its key", func() bool { return mgr.rekeys.Load() >= 1 })
	if key, gen := keys.Current(); key != "k1" || gen != 0 {
		t.Fatal("the key changed")
	}
	if next, _ := keys.Pending(); next != "" || link.Removed() {
		t.Fatal("the agent took a key, or the link stopped")
	}
	time.Sleep(500 * time.Millisecond) // pings every 100 ms: several pongs, each with the hint
	if n := mgr.rekeys.Load(); n != 1 {
		t.Fatalf("asked the leader for a key %d times", n)
	}
	if !strings.Contains(logs.String(), "the key stays as it is") || strings.Contains(logs.String(), errManagerPredatesAgents.Error()) {
		t.Fatalf("the answer was not read as a manager that cannot rotate keys: %s", logs)
	}
}

// A follower's word is not taken for a key rotation: nothing is asked of it.
func TestAnAgentTakesAKeyHintFromTheLeaderOnly(t *testing.T) {
	_, ca := setupCertDir(t)
	follower := startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{pong: func(self string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{LeaderGrpcAddress: self, AgentKey: &pb.AgentKeyHint{Generation: 1}}, nil
	}})
	agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
	if err := agent.SaveAgentKey("k1"); err != nil {
		t.Fatal(err)
	}
	keys, err := LoadAgentKeys(agent, func(string, []string) {})
	if err != nil {
		t.Fatal(err)
	}
	startLinkWith(t, agent, "agent-1", keys, []config.ManagerRef{{ID: "m1", GRPCAddress: follower.self}})
	waitFor(t, "a heartbeat session", func() bool { return follower.streams.Load() >= 1 })
	time.Sleep(500 * time.Millisecond) // pings every 100 ms: several pongs, each with the hint
	if follower.rekeys.Load() != 0 {
		t.Fatal("the agent acted on a follower's hint")
	}
}

// A join writes a new agent.key, which no rotation issued: a rotation file
// an earlier membership left names a generation and a key of another
// identity.
func TestAnAgentJoinStartsWithNoKeyRotation(t *testing.T) {
	mgr, addr, _, pin := realLeader(t)
	settleRotation(t, mgr)
	t.Cleanup(func() { auth.SetJWTSecret("") })
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := agentJoinTarget(t)
	leftover := filepath.Join(e.Config.Cluster.CertDir, AgentKeyFile)
	if err := replaceFile(leftover, mustJSON(agentKeyState{Generation: 5, Next: "earlier", NextGeneration: 6})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(addr, tok.Token, "127.0.0.2", pin, panelCAPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); err == nil {
		t.Fatal("an earlier membership's key rotation stayed")
	}
}

// Each row of rotateKey's table: what the agent asks the leader, with which
// key it proves each step, and the key it ends on.
func TestRotateKeyFollowsTheHint(t *testing.T) {
	_, ca := setupCertDir(t)
	issues := func(req *pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
		if req.Confirm {
			return &pb.RotateAgentKeyResponse{Success: true, Switched: true}, nil
		}
		return &pb.RotateAgentKeyResponse{Success: true, AgentKey: "k2"}, nil
	}
	fetch, confirm := agentKeyProof("k1", "fetch", "agent-1", 1), agentKeyProof("k2", "confirm", "agent-1", 1)
	for _, c := range []struct {
		name    string
		taken   bool // the agent took k2 before the hint
		hint    *pb.AgentKeyHint
		answer  func(*pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error)
		asked   []string // the proofs, in order
		err     string
		current string
	}{
		{"nothing due", false, &pb.AgentKeyHint{Generation: 0}, issues, nil, "", "k1"},
		{"a new rotation", false, &pb.AgentKeyHint{Generation: 1}, issues, []string{fetch, confirm}, "", "k2"},
		{"a key taken, not confirmed", true, &pb.AgentKeyHint{Generation: 1}, issues, []string{confirm}, "", "k2"},
		{"the confirm's answer lost", true, &pb.AgentKeyHint{Generation: 1, Current: true}, issues, nil, "", "k2"},
		{"the managers sign with a key never taken", false, &pb.AgentKeyHint{Generation: 1, Current: true}, issues, nil, errAgentKeyNotHeld.Error(), "k1"},
		{"a fetch that answers no key", false, &pb.AgentKeyHint{Generation: 1}, func(*pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
			return &pb.RotateAgentKeyResponse{Success: true, Switched: true}, nil
		}, []string{fetch}, errAgentKeyNotHeld.Error(), "k1"},
		{"a refusal", false, &pb.AgentKeyHint{Generation: 1}, func(*pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
			return &pb.RotateAgentKeyResponse{Error: "no"}, nil
		}, []string{fetch}, "refused: no", "k1"},
		// The leader took the confirm of k2, and the answer never came: the
		// managers sign with k2 when the next rotation starts, so k1 proves
		// nothing, k2 proves the fetch, and the agent moves to k2 first.
		{"the confirm's answer lost before another rotation", true, &pb.AgentKeyHint{Generation: 2}, func(req *pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
			switch req.Proof {
			case agentKeyProof("k2", "fetch", "agent-1", 2):
				return &pb.RotateAgentKeyResponse{Success: true, AgentKey: "k3"}, nil
			case agentKeyProof("k3", "confirm", "agent-1", 2):
				return &pb.RotateAgentKeyResponse{Success: true, Switched: true}, nil
			}
			return &pb.RotateAgentKeyResponse{Error: ErrAgentKeyProof.Error()}, nil
		}, []string{agentKeyProof("k1", "fetch", "agent-1", 2), agentKeyProof("k2", "fetch", "agent-1", 2), agentKeyProof("k3", "confirm", "agent-1", 2)}, "", "k3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var asked []string
			mgr := startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{rekey: func(req *pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
				mu.Lock()
				asked = append(asked, req.Proof)
				mu.Unlock()
				return c.answer(req)
			}})
			agent := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
			if err := agent.SaveAgentKey("k1"); err != nil {
				t.Fatal(err)
			}
			keys, err := LoadAgentKeys(agent, func(string, []string) {})
			if err != nil {
				t.Fatal(err)
			}
			if c.taken {
				if err := keys.Take("k2", 1); err != nil {
					t.Fatal(err)
				}
			}
			link := &AgentLink{AgentLinkConfig: AgentLinkConfig{NodeID: "agent-1", TLS: agent, Keys: keys}}
			client, err := DialManager(mgr.self, agent, func() []string { return []string{"m1"} })
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = link.rotateKey(t.Context(), client, c.hint)
			if (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
				t.Fatalf("error %v, want %q", err, c.err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(asked, c.asked) {
				t.Fatalf("asked with %d proofs, want %d (or other keys)", len(asked), len(c.asked))
			}
			if key, _ := keys.Current(); key != c.current {
				t.Fatalf("ends on %q", key)
			}
		})
	}
}

// An agent whose confirm the leader took but whose answer it never got holds
// agent.key as it was and the key it confirmed as its next, while the
// managers sign with the latter. A rotation started after that refused its
// fetch, proved with agent.key, for good: the agent went on accepting the key
// the rotation meant to retire, and could prove no certificate renewal
// either. It proves the fetch with the key it confirmed, and moves to that
// key before it takes the new one.
func TestAConfirmWhoseAnswerWasLostDoesNotStrandTheAgent(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	in := &installed{}
	keys, err := LoadAgentKeys(tm, in.set)
	if err != nil {
		t.Fatal(err)
	}
	keys.Install(time.Now())
	leaf, _ := tm.NodeCertLeaf()
	gen, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	issued, _, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(res.AgentKey, "fetch", res.NodeID, gen), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Take(issued, gen); err != nil {
		t.Fatal(err)
	}
	if _, switched, err := mgr.HandleRotateAgentKey(leaf, gen, agentKeyProof(issued, "confirm", res.NodeID, gen), true); err != nil || !switched {
		t.Fatalf("confirm: %v %v", switched, err)
	}
	// The answer never reached the agent.
	next, err := mgr.StartAgentKeyRotation()
	if err != nil {
		t.Fatal(err)
	}
	link := &AgentLink{AgentLinkConfig: AgentLinkConfig{NodeID: res.NodeID, TLS: tm, Keys: keys}}
	client, err := DialManager(addr, tm, func() []string { return []string{mgr.LocalNodeID()} })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := link.rotateKey(t.Context(), client, mgr.agentKeyHint(res.NodeID)); err != nil {
		t.Fatalf("the next rotation: %v", err)
	}
	key, g := keys.Current()
	if g != next || key != agentKeyIn(mgr, res.NodeID) {
		t.Fatalf("the agent is on generation %d, %s the key the managers sign with", g, map[bool]string{true: "with", false: "not with"}[key == agentKeyIn(mgr, res.NodeID)])
	}
	if _, accept := in.get(); slices.Contains(accept, res.AgentKey) {
		t.Fatal("the agent still accepts the key it registered with")
	}
}
