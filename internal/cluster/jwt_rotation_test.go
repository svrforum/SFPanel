package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// appliedKeys records what an applier was given, as the feature handler's
// would apply it.
type appliedKeys struct {
	mu   sync.Mutex
	keys []JWTKey
	fail int // calls to fail before one succeeds
}

func (a *appliedKeys) apply(k JWTKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys = append(a.keys, k)
	if a.fail > 0 {
		a.fail--
		return errors.New("disk full")
	}
	return nil
}

func (a *appliedKeys) all() []JWTKey {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]JWTKey(nil), a.keys...)
}

// jwtLeader is a one-manager cluster whose FSM holds the key it was
// initialised with, with an applier wired as the feature handler wires its own.
func jwtLeader(t *testing.T) (*Manager, *appliedKeys) {
	t.Helper()
	mgr, _, _, _ := realLeader(t)
	if err := mgr.SetConfig(configKeyJWTSecret, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	a := &appliedKeys{}
	mgr.SetJWTKeyApplier(a.apply, 0)
	return mgr, a
}

// A manager that cannot follow a rotation would go on accepting the retired
// key, and a token minted with it has no bound on its life — so the rotation
// waits for every manager, says for whom, and changes nothing until then.
func TestRotateJWTKeyWaitsForEveryManager(t *testing.T) {
	mgr, a := jwtLeader(t)
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RotateJWTKey(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "m2 (not heard from)") {
		t.Fatalf("m2 not heard from: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "0.79.0", Capabilities: []string{CapAgents, CapProxySecret}})
	if _, err := mgr.RotateJWTKey(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "m2 (0.79.0)") {
		t.Fatalf("m2 on a release that cannot follow: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyJWTKey); v != "" || len(a.all()) != 0 {
		t.Fatal("a refused rotation changed something")
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{CapAgents, CapJWTRotation}})
	if gen, err := mgr.RotateJWTKey(); err != nil || gen != 1 {
		t.Fatalf("rotate: %d %v", gen, err)
	}
}

// The new key lands in one entry under the next generation. Every manager,
// this one too, applies it as its FSM changes, and the join answer and older
// releases find it where they read the key. No log line carries it.
func TestRotateJWTKeyAppliesTheNewKey(t *testing.T) {
	logs := captureLogs(t)
	mgr, a := jwtLeader(t)
	gen, err := mgr.RotateJWTKey()
	if err != nil || gen != 1 {
		t.Fatalf("rotate: %d %v", gen, err)
	}
	waitFor(t, "the leader to apply it", func() bool { return len(a.all()) == 1 && mgr.JWTGeneration() == 1 })
	k := a.all()[0]
	if k.Generation != 1 || len(k.Secret) != 64 || k.Secret == strings.Repeat("a", 64) {
		t.Fatalf("applied %+v", k)
	}
	if plain, _ := mgr.raft.GetFSM().GetConfigValue(configKeyJWTSecret); plain != k.Secret {
		t.Fatal("the plain key older releases read is not the new one")
	}
	if js, _, _, _ := mgr.GetJWTAndAdminFull(); js != k.Secret {
		t.Fatal("a join would hand out another key")
	}
	out := logs.String()
	if !strings.Contains(out, "rotated the cluster's JWT key") || !strings.Contains(out, "this manager runs the cluster's new JWT key") {
		t.Fatal("the rotation and the apply were not logged")
	}
	if strings.Contains(out, k.Secret) {
		t.Fatal("the new key is in the logs")
	}
}

// The leader's own sessions end too: with nothing wired to apply the key, it
// would go on running the retired one.
func TestRotateJWTKeyNeedsTheLeaderToApplyItToo(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	if err := mgr.SetConfig(configKeyJWTSecret, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RotateJWTKey(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "cannot apply a new JWT key") {
		t.Fatalf("a leader with no applier: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyJWTKey); v != "" {
		t.Fatal("a refused rotation wrote a key")
	}
}

// The plain key is a copy for older releases, which answer joins with it — a
// manager the watchdog rolled back among them. Where the copy differs, the
// record is the key: this release's join answer reads the record, and the
// leader writes the copy again.
func TestTheLeaderRepairsThePlainCopyOfTheKey(t *testing.T) {
	mgr, _ := jwtLeader(t)
	if _, err := mgr.RotateJWTKey(); err != nil {
		t.Fatal(err)
	}
	k, _ := clusterJWTKey(mgr.raft.GetFSM().GetState())
	if err := mgr.SetConfig(configKeyJWTSecret, "stale"); err != nil { // as a failed copy, or a boot seed run before the FSM caught up, leaves it
		t.Fatal(err)
	}
	if js, _, _, _ := mgr.GetJWTAndAdminFull(); js != k.Secret {
		t.Fatal("a join would hand out the stale copy")
	}
	mgr.repairJWTMirror()
	if plain, _ := mgr.raft.GetFSM().GetConfigValue(configKeyJWTSecret); plain != k.Secret {
		t.Fatal("the copy older releases read was not repaired")
	}
}

// After a restart the FSM holds the last snapshot until the node learns the
// commit index, and the key it shows then may be one a rotation retired:
// only a newer generation than the one the node runs is applied. A failed
// apply is tried again, and is not counted as done.
func TestApplyJWTKeyTakesANewerGenerationOnly(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	a := &appliedKeys{fail: 1}
	m.SetJWTKeyApplier(a.apply, 2) // this manager runs generation 2
	set := func(k JWTKey) {
		applyCmd(t, m.raft.fsm, Command{Type: CmdSetConfig, Key: configKeyJWTKey, Value: mustJSON(string(mustJSON(k)))})
	}
	set(JWTKey{Generation: 1, Secret: "older"})
	m.ApplyJWTKey()
	if len(a.all()) != 0 {
		t.Fatalf("applied an older generation: %+v", a.all())
	}
	set(JWTKey{Generation: 3, Secret: "newer"})
	m.ApplyJWTKey() // fails once
	if m.JWTGeneration() != 2 {
		t.Fatal("a failed apply was counted as done")
	}
	m.ApplyJWTKey()
	m.ApplyJWTKey()
	if got := a.all(); len(got) != 2 || m.JWTGeneration() != 3 {
		t.Fatalf("applies %+v, generation %d", got, m.JWTGeneration())
	}
}

// The capability says this manager follows a rotation, which it does only
// once something is wired to apply it.
func TestJWTRotationCapabilityOnlyOnceTheApplierIsWired(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if containsString(m.heartbeatCapabilities(), CapJWTRotation) {
		t.Fatal("reported with nothing to apply a key")
	}
	m.SetJWTKeyApplier(func(JWTKey) error { return nil }, 0)
	if !containsString(m.heartbeatCapabilities(), CapJWTRotation) {
		t.Fatal("not reported once wired")
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// A manager that joins after a rotation runs its generation from the start:
// at 0 it would take the next rotation for one it missed only after its
// sessions ended for nothing, and a number from an earlier cluster would
// stand above every rotation of this one.
func TestAJoinedManagerStartsOnTheClustersGeneration(t *testing.T) {
	t.Cleanup(func() { auth.SetJWTSecret("") }) // the join publishes the cluster's key process-wide
	mgr, addr, token, pin := realLeader(t)
	if err := mgr.SetConfig(configKeyJWTSecret, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	mgr.SetJWTKeyApplier(func(JWTKey) error { return nil }, 0)
	if _, err := mgr.RotateJWTKey(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Auth: config.AuthConfig{JWTSecret: strings.Repeat("b", 64), JWTGeneration: 9}}
	cfg.Server.Port = 1
	cfg.Cluster.GRPCPort = freePortPair(t)
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	e := &JoinEngine{Config: cfg, ConfigPath: path}
	if _, err := e.Execute(addr, token, "127.0.0.1", pin); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var saved config.Config
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	k, _ := clusterJWTKey(mgr.raft.GetFSM().GetState())
	if saved.Auth.JWTGeneration != 1 || saved.Auth.JWTSecret != k.Secret {
		t.Fatalf("joined with generation %d", saved.Auth.JWTGeneration)
	}
}
