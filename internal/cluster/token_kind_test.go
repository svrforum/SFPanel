package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

func TestTokenKindIsCheckedBeforeConsumption(t *testing.T) {
	tm := NewTokenManager()
	jt, err := tm.Create(time.Hour, "leader", KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Validate(jt.Token, KindManager); !errors.Is(err, ErrTokenKind) {
		t.Fatalf("agent token used for a manager join: %v", err)
	}
	if _, err := tm.Peek(jt.Token); err != nil {
		t.Fatalf("the refusal consumed the token: %v", err)
	}
	got, err := tm.Validate(jt.Token, KindAgent)
	if err != nil || got.EffectiveKind() != KindAgent {
		t.Fatalf("agent token for an agent: %+v %v", got, err)
	}
	// A token saved before kinds existed is a manager token.
	legacy, _ := tm.Create(time.Hour, "leader", "")
	if _, err := tm.Validate(legacy.Token, KindManager); err != nil {
		t.Fatalf("kindless token refused for a manager: %v", err)
	}
	// Both tokens above are spent, and List drops spent tokens: list a live one.
	live, err := tm.Create(time.Hour, "leader", KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	if infos := tm.List(); len(infos) != 1 || infos[0].ID != tokenFingerprint(live.Token) || infos[0].Kind != KindAgent {
		t.Fatalf("list: %+v", infos)
	}
}

// The kind is saved with the token: an agent token must not come back from a
// leader restart as a kindless entry, which is a manager token. Entries saved
// before kinds existed carry none and load as manager tokens.
func TestTokenKindSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	tm1, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := tm1.Create(time.Hour, "leader", KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := tm1.Create(time.Hour, "leader", KindManager)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the manager token the way v0.79.0 saved every token: no kind.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tf tokenFile
	if err := json.Unmarshal(data, &tf); err != nil {
		t.Fatal(err)
	}
	tf.Tokens[legacy.Token].Kind = ""
	if data, err = json.Marshal(&tf); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"kind"`); n != 1 {
		t.Fatalf("fixture should carry exactly the agent token's kind, has %d: %s", n, data)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	tm2, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]NodeKind{}
	for _, i := range tm2.List() {
		kinds[i.ID] = i.Kind
	}
	if kinds[tokenFingerprint(agent.Token)] != KindAgent || kinds[tokenFingerprint(legacy.Token)] != KindManager {
		t.Fatalf("listed kinds after a restart: %v", kinds)
	}
	if _, err := tm2.Validate(agent.Token, KindManager); !errors.Is(err, ErrTokenKind) {
		t.Fatalf("agent token after a restart used for a manager join: %v", err)
	}
	if _, err := tm2.Validate(legacy.Token, KindAgent); !errors.Is(err, ErrTokenKind) {
		t.Fatalf("kindless token used for an agent: %v", err)
	}
	if _, err := tm2.Validate(legacy.Token, KindManager); err != nil {
		t.Fatalf("kindless token refused for a manager: %v", err)
	}
}

// Restoring a token after a failed join must work even when a Create or List
// ran in between: cleanup drops used tokens, and RestoreToken found nothing.
func TestRestoreSurvivesCleanup(t *testing.T) {
	tm := NewTokenManager()
	jt, _ := tm.Create(time.Hour, "leader", KindManager)
	used, err := tm.Validate(jt.Token, KindManager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Create(time.Hour, "leader", KindManager); err != nil { // runs cleanupLocked
		t.Fatal(err)
	}
	tm.Restore(used)
	if _, err := tm.Peek(jt.Token); err != nil {
		t.Fatalf("restored token unusable: %v", err)
	}
}

// A join refused for a duplicate ID or a full cluster used to burn the token.
func TestJoinRefusalLeavesTheTokenUsable(t *testing.T) {
	mgr, _, token, _ := realLeader(t)
	_, _, _, _, err := mgr.HandleJoin(mgr.LocalNodeID(), "dup", "127.0.0.1:1", "127.0.0.1:2", token)
	if !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("duplicate join: %v", err)
	}
	if _, err := mgr.tokens.Peek(token); err != nil {
		t.Fatalf("the refused join consumed the token: %v", err)
	}
	agentTok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := mgr.HandleJoin("new-node", "n", "127.0.0.1:1", "127.0.0.1:2", agentTok.Token); !errors.Is(err, ErrTokenKind) {
		t.Fatalf("manager join with an agent token: %v", err)
	}
	if _, err := mgr.tokens.Peek(agentTok.Token); err != nil {
		t.Fatalf("the refused join consumed the agent token: %v", err)
	}
	// 31 more managers make the cluster full.
	fsm := mgr.raft.GetFSM()
	for i := 1; i < MaxNodes; i++ {
		addTestNode(t, fsm, Node{ID: fmt.Sprintf("manager-%d", i), Kind: KindManager})
	}
	if _, _, _, _, err := mgr.HandleJoin("new-node", "n", "127.0.0.1:1", "127.0.0.1:2", token); !errors.Is(err, ErrMaxNodesReached) {
		t.Fatalf("join into a full cluster: %v", err)
	}
	if _, err := mgr.tokens.Peek(token); err != nil {
		t.Fatalf("the refused join consumed the token: %v", err)
	}
}

// A join the leader cannot sign for fails with ErrCAKeyUnavailable, which
// tells the operator to restore ca.key and retry; the retry used to find the
// token already used.
func TestJoinThatCannotSignLeavesTheTokenUsable(t *testing.T) {
	mgr, _, token, _ := realLeader(t)
	if err := os.Remove(filepath.Join(mgr.config.CertDir, "ca.key")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.raft.Apply(Command{Type: CmdDeleteConfig, Key: configKeyCAKey}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := mgr.HandleJoin("new-node", "n", "127.0.0.1:1", "127.0.0.1:2", token); !errors.Is(err, ErrCAKeyUnavailable) {
		t.Fatalf("join with no CA key: %v", err)
	}
	if _, err := mgr.tokens.Peek(token); err != nil {
		t.Fatalf("the failed join consumed the token: %v", err)
	}
}

// Agents and managers have separate caps: 32 agents must not stop a manager
// join, and the agent cap is the configured one.
func TestCapacityIsPerKind(t *testing.T) {
	mgr, _, token, _ := realLeader(t)
	fsm := mgr.raft.GetFSM()
	for i := 0; i < MaxNodes; i++ {
		addTestNode(t, fsm, Node{ID: fmt.Sprintf("agent-%d", i), Kind: KindAgent})
	}
	if _, err := mgr.CreateJoinToken(time.Hour, KindManager); err != nil {
		t.Fatalf("agents counted toward the manager cap: %v", err)
	}
	// The join counts managers only as well: refused as a duplicate, not as full.
	if _, _, _, _, err := mgr.HandleJoin(mgr.LocalNodeID(), "dup", "127.0.0.1:1", "127.0.0.1:2", token); !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("agents counted toward the manager cap on join: %v", err)
	}
	// Pre-flight reports the count and the cap of the token's kind.
	if _, n, limit, err := mgr.HandlePreFlight(token); err != nil || n != 1 || limit != MaxNodes {
		t.Fatalf("manager pre-flight: %d/%d %v", n, limit, err)
	}
	agentTok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, n, limit, err := mgr.HandlePreFlight(agentTok.Token); err != nil || n != MaxNodes || limit != config.DefaultMaxAgents {
		t.Fatalf("agent pre-flight: %d/%d %v", n, limit, err)
	}
	mgr.config.MaxAgents = MaxNodes
	if _, err := mgr.CreateJoinToken(time.Hour, KindAgent); !errors.Is(err, ErrMaxAgentsReached) {
		t.Fatalf("agent cap: %v", err)
	}
}
