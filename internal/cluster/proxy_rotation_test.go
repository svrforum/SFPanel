package cluster

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
)

func TestProxyKeysAt(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	next := ProxyKey{Generation: 2, Secret: "s2", Previous: "s1"}
	switched := ProxyKey{Generation: 2, Secret: "s2", Previous: "s1", SwitchedAt: at}
	done := ProxyKey{Generation: 2, Secret: "s2", SwitchedAt: at}
	for _, tc := range []struct {
		name   string
		k      ProxyKey
		now    time.Time
		sign   string
		accept []string
	}{
		{"every manager takes the next secret before anyone signs with it", next, at, "s1", []string{"s1", "s2"}},
		{"after the switch the old one is accepted for a while", switched, at.Add(time.Minute), "s2", []string{"s2", "s1"}},
		{"and then no more", switched, at.Add(legacyProxyGrace), "s2", []string{"s2"}},
		{"done", done, at, "s2", []string{"s2"}},
	} {
		if sign, accept := proxyKeysAt(tc.k, tc.now); sign != tc.sign || !slices.Equal(accept, tc.accept) {
			t.Fatalf("%s: sign %q accept %v", tc.name, sign, accept)
		}
	}
}

func TestNextProxyStep(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	gen := func(g int64) *int64 { return &g }
	on := func(id string, g *int64) proxyNode {
		return proxyNode{ID: id, Name: id, Version: "test", HeardFrom: true, Online: true, Capable: true, Generation: g}
	}
	next := ProxyKey{Generation: 3, Secret: "s3", Previous: "s2"}
	switched := ProxyKey{Generation: 3, Secret: "s3", Previous: "s2", SwitchedAt: at}
	for _, tc := range []struct {
		name    string
		k       ProxyKey
		nodes   []proxyNode
		now     time.Time
		want    ProxyPhase
		waiting string // one of the step's waiting entries, "" for none
	}{
		{"every manager accepts the next secret", next, []proxyNode{on("m1", gen(3)), on("m2", gen(3))}, at, ProxyPhaseSwitched, ""},
		{"one has not taken it yet", next, []proxyNode{on("m1", gen(3)), on("m2", gen(2))}, at, "", "m2 (has not taken the next secret yet)"},
		{"one reports no generation", next, []proxyNode{on("m2", nil)}, at, "", "m2 (has not taken the next secret yet)"},
		{"an offline manager holds the switch", next, []proxyNode{{ID: "m2", Name: "m2", HeardFrom: true, Capable: true, Generation: gen(3)}}, at, "", "m2 (offline)"},
		{"one on a release that cannot follow holds it", next, []proxyNode{{ID: "m2", Name: "m2", Version: "0.79.0", HeardFrom: true, Online: true}}, at, "", "m2 (0.79.0)"},
		{"one not heard from holds it", next, []proxyNode{{ID: "m2", Name: "m2"}}, at, "", "m2 (not heard from)"},
		{"the next phase has no deadline", next, []proxyNode{on("m2", gen(2))}, at.Add(48 * time.Hour), "", "m2 (has not taken the next secret yet)"},
		{"the old secret leaves once its window has passed", switched, []proxyNode{on("m2", gen(3))}, at.Add(proxyRotationSettled), ProxyPhaseDone, ""},
		{"not before", switched, []proxyNode{on("m2", gen(3))}, at.Add(proxyRotationSettled - time.Second), "", ""},
		{"done stays done", ProxyKey{Generation: 3, Secret: "s3", SwitchedAt: at}, nil, at.Add(time.Hour), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := nextProxyStep(tc.k, tc.nodes, tc.now)
			if step.Next != tc.want {
				t.Fatalf("next %q, want %q (waiting %v)", step.Next, tc.want, step.Waiting)
			}
			if tc.waiting != "" && !slices.Contains(step.Waiting, tc.waiting) {
				t.Fatalf("waiting %v, want %q among them", step.Waiting, tc.waiting)
			}
		})
	}
}

// proxyLeader is a one-manager cluster whose first switch, plan A's, is long
// settled; it signs with that secret, as activation leaves it.
func proxyLeader(t *testing.T) *Manager {
	t.Helper()
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	mgr.ApplyProxySecrets()
	return mgr
}

func proxyRecord(t *testing.T, m *Manager) ProxyKey {
	t.Helper()
	k, ok := proxyKeyOf(m.raft.GetFSM().GetState())
	if !ok {
		t.Fatal("no proxy key record")
	}
	return k
}

// A manager that has not taken the next secret would refuse every request
// signed with it once the others switch: the start waits for every manager,
// says for whom, and writes nothing until then.
func TestStartProxyRotationWaitsForEveryManager(t *testing.T) {
	mgr := proxyLeader(t)
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartProxyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "m2 (not heard from)") {
		t.Fatalf("m2 not heard from: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "0.79.0", Capabilities: []string{CapAgents, CapProxySecret}})
	if _, err := mgr.StartProxyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "m2 (0.79.0)") {
		t.Fatalf("m2 on a release that cannot follow: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyProxyKey); v != "" {
		t.Fatal("a refused start wrote a record")
	}
}

// Until plan A's switch has settled a manager may still accept the derived
// secret: a second change on top of that one is refused.
func TestStartProxyRotationWaitsForTheFirstSwitch(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	mgr, _, _, _ := realLeader(t)
	if _, err := mgr.StartProxyRotation(); !errors.Is(err, ErrRotationRefused) || !errors.Is(err, ErrProxyRotationPending) {
		t.Fatalf("before the first switch settled: %v", err)
	}
}

// The rotation moves forward only: every manager accepts the next secret
// before anyone signs with it, the old one is accepted for a while after the
// switch and then leaves replicated state, and the next rotation replaces
// this one's secret — it never brings an earlier one back.
func TestAProxyRotationMovesForwardOnly(t *testing.T) {
	mgr := proxyLeader(t)
	old := "replicated-secret" // settleRotation's
	gen, err := mgr.StartProxyRotation()
	if err != nil || gen != 1 {
		t.Fatalf("start: %d %v", gen, err)
	}
	k := proxyRecord(t, mgr)
	if k.Previous != old || k.Secret == old || k.Phase() != ProxyPhaseNext {
		t.Fatal("the record does not move from the secret the managers sign with to a new one")
	}
	waitFor(t, "the leader to take the next secret", func() bool { return mgr.ProxyGeneration() == 1 })
	if auth.ClusterProxySecret() != old || !auth.IsInternalProxyRequest(signedProxyRequest(t, k.Secret)) || !auth.IsInternalProxyRequest(signedProxyRequest(t, old)) {
		t.Fatal("in the next phase a manager signs with the current secret and accepts both")
	}
	now := time.Now()
	if err := mgr.advanceProxyRotation(now); err != nil || proxyRecord(t, mgr).Phase() != ProxyPhaseSwitched {
		t.Fatalf("a one-manager cluster did not switch (%v)", err)
	}
	waitFor(t, "the leader to sign with the new secret", func() bool { return auth.ClusterProxySecret() == k.Secret })
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyProxySecret); v != k.Secret {
		t.Fatal("the secret older releases read is not the new one")
	}
	if err := mgr.advanceProxyRotation(now.Add(proxyRotationSettled)); err != nil || proxyRecord(t, mgr).Phase() != ProxyPhaseDone {
		t.Fatalf("the old secret stayed in the record past its window (%v)", err)
	}
	waitFor(t, "the old secret to be refused", func() bool { return !auth.IsInternalProxyRequest(signedProxyRequest(t, old)) })

	gen2, err := mgr.StartProxyRotation()
	if err != nil || gen2 != 2 {
		t.Fatalf("a second rotation: %d %v", gen2, err)
	}
	if k2 := proxyRecord(t, mgr); k2.Previous != k.Secret || k2.Secret == old || k2.Secret == k.Secret {
		t.Fatal("the second rotation does not move from the first one's secret to a new one")
	}
}

// In the next phase nobody signs with the next secret, and a new start
// replaces it. While the old secret is still accepted after a switch a third
// one would not fit: refused.
func TestOneProxyRotationAtATime(t *testing.T) {
	mgr := proxyLeader(t)
	if _, err := mgr.StartProxyRotation(); err != nil {
		t.Fatal(err)
	}
	first := proxyRecord(t, mgr)
	if gen, err := mgr.StartProxyRotation(); err != nil || gen != 2 {
		t.Fatalf("again in the next phase: %d %v", gen, err)
	}
	if k := proxyRecord(t, mgr); k.Previous != first.Previous || k.Secret == first.Secret {
		t.Fatal("a start in the next phase changed the secret signed with, or kept the next one")
	}
	waitFor(t, "the next secret to be taken", func() bool { return mgr.ProxyGeneration() == 2 })
	if err := mgr.advanceProxyRotation(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartProxyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "still accepted") {
		t.Fatalf("a start while the old secret is still accepted: %v", err)
	}
}

// After a restart the FSM shows the last snapshot until the node catches up,
// and the record in it can be an older step's: the floor keeps the node on
// the newest one it applied. It never signs with, or accepts, a secret a
// rotation retired.
func TestARestartedManagerNeverGoesBackToAnOlderProxySecret(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	dataDir := t.TempDir()
	settled := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	manager := func(mirror string, k ProxyKey) *Manager {
		return &Manager{config: &config.ClusterConfig{DataDir: dataDir}, raft: &RaftNode{fsm: fsmWithConfig(t, map[string]string{
			configKeyProxySecret: mirror, configKeyProxyRotatedAt: settled, configKeyProxyKey: string(mustJSON(k)),
		})}}
	}
	manager("s2", ProxyKey{Generation: 2, Secret: "s2", SwitchedAt: time.Now().Add(-time.Hour)}).ApplyProxySecrets()
	auth.SetClusterProxySecret("") // what the restarted process starts from

	restarted := manager("s1", ProxyKey{Generation: 2, Secret: "s2", Previous: "s1"}) // a snapshot from the next phase
	restarted.ApplyProxySecrets()
	if auth.ClusterProxySecret() != "s2" || auth.IsInternalProxyRequest(signedProxyRequest(t, "s1")) {
		t.Fatal("a restart behind the FSM brought back the secret the rotation retired")
	}
	if restarted.ProxyGeneration() != 2 {
		t.Fatalf("reports generation %d", restarted.ProxyGeneration())
	}
}

// That a manager follows a re-rotation is a property of the code; whether it
// has taken one's next secret is what its ping's proxy_generation says.
func TestAManagerReportsProxyRotation(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if !slices.Contains(m.heartbeatCapabilities(), CapProxyRotation) {
		t.Fatal("CapProxyRotation not reported")
	}
}

// The leader names whom the switch waits for once while that stays the same,
// managers only — an agent never holds the managers' secret — and no log line
// carries a secret, the next, the current or the retired one.
func TestAProxyRotationSaysWhomItWaitsForOnceAndNoSecret(t *testing.T) {
	logs := captureLogs(t)
	mgr := proxyLeader(t)
	old := "replicated-secret" // settleRotation's
	if _, err := mgr.StartProxyRotation(); err != nil {
		t.Fatal(err)
	}
	k := proxyRecord(t, mgr)
	for _, n := range []Node{{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter}, {ID: "a1", Name: "a1", Kind: KindAgent}} {
		if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(n)}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the leader to take the next secret", func() bool { return mgr.ProxyGeneration() == 1 })
	for range 2 {
		if err := mgr.advanceProxyRotation(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if proxyRecord(t, mgr).Phase() != ProxyPhaseNext {
		t.Fatal("switched while a manager had not been heard from")
	}
	var waits []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "an internal proxy secret rotation waits") {
			waits = append(waits, line)
		}
	}
	if len(waits) != 1 || !strings.Contains(waits[0], "m2 (not heard from)") || strings.Contains(waits[0], "a1") {
		t.Fatalf("the wait was said as %q, want once, naming m2 alone", waits)
	}

	if err := mgr.raft.Apply(Command{Type: CmdRemoveNode, Key: "m2"}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := mgr.advanceProxyRotation(now); err != nil || proxyRecord(t, mgr).Phase() != ProxyPhaseSwitched {
		t.Fatalf("did not switch once m2 was gone (%v)", err)
	}
	if err := mgr.advanceProxyRotation(now.Add(proxyRotationSettled)); err != nil || proxyRecord(t, mgr).Phase() != ProxyPhaseDone {
		t.Fatalf("did not retire the old secret (%v)", err)
	}
	out := logs.String()
	for _, line := range []string{"started a rotation of the internal proxy secret", "every manager signs with the new internal proxy secret", "the old internal proxy secret is no longer accepted anywhere"} {
		if !strings.Contains(out, line) {
			t.Fatalf("%q was not logged", line)
		}
	}
	if strings.Contains(out, k.Secret) || strings.Contains(out, old) {
		t.Fatal("a secret is in the logs")
	}
}

// Older releases, a manager the watchdog rolled back among them, follow
// cluster_proxy_secret alone: the leader writes it again whenever it is not
// the secret the record signs with.
func TestTheLeaderRepairsTheProxySecretMirror(t *testing.T) {
	mgr := proxyLeader(t)
	if _, err := mgr.StartProxyRotation(); err != nil {
		t.Fatal(err)
	}
	k := proxyRecord(t, mgr)
	waitFor(t, "the leader to take the next secret", func() bool { return mgr.ProxyGeneration() == 1 })
	// A manager not heard from holds the switch: only the repair runs.
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig(configKeyProxySecret, "stale"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.advanceProxyRotation(time.Now()); err != nil || proxyRecord(t, mgr).Phase() != ProxyPhaseNext {
		t.Fatalf("advance: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyProxySecret); v != k.Previous {
		t.Fatalf("in the next phase the mirror is %q, want the secret the managers still sign with", v)
	}
	if err := mgr.raft.Apply(Command{Type: CmdRemoveNode, Key: "m2"}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.advanceProxyRotation(time.Now()); err != nil || proxyRecord(t, mgr).Phase() != ProxyPhaseSwitched {
		t.Fatalf("switch: %v", err)
	}
	if err := mgr.SetConfig(configKeyProxySecret, "stale"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.advanceProxyRotation(time.Now()); err != nil {
		t.Fatal(err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyProxySecret); v != k.Secret {
		t.Fatalf("after the switch the mirror is %q, want the new secret", v)
	}
}

// A record that does not parse, or no secret to move from, would start a
// rotation no manager can follow: refused, and nothing written.
func TestStartProxyRotationRefusesWhatItCannotMoveFrom(t *testing.T) {
	mgr := proxyLeader(t)
	if err := mgr.SetConfig(configKeyProxyKey, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartProxyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("a broken record: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(configKeyProxyKey); v != "{not json" {
		t.Fatal("a refused start wrote a record")
	}
	if err := mgr.raft.Apply(Command{Type: CmdDeleteConfig, Key: configKeyProxyKey}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig(configKeyProxySecret, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartProxyRotation(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "no internal proxy secret to move from") {
		t.Fatalf("no secret to move from: %v", err)
	}
}

// The floor keeps the record a node applied beside what plan A's floor kept,
// and its secret is the one the node signs with: a release from before the
// record, the watchdog's rollback, reads that alone.
func TestTheProxyFloorKeepsTheSigningSecret(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, proxySecretFloorFile)
	if err := os.WriteFile(path, []byte(`{"secret":"s1","rotated_at":"2026-10-01T00:00:00Z","agents_seen":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{config: &config.ClusterConfig{DataDir: dataDir}, raft: &RaftNode{fsm: fsmWithConfig(t, map[string]string{
		configKeyProxySecret: "s1", configKeyProxyKey: string(mustJSON(ProxyKey{Generation: 1, Secret: "s2", Previous: "s1"})),
	})}}
	m.ApplyProxySecrets()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var floor proxySecretFloor
	if err := json.Unmarshal(data, &floor); err != nil {
		t.Fatal(err)
	}
	if floor.Secret != "s1" || floor.RotatedAt != "2026-10-01T00:00:00Z" || !floor.AgentsSeen || floor.Key == nil || floor.Key.Generation != 1 || floor.Key.Secret != "s2" {
		t.Fatalf("the floor holds %s", data)
	}
}
