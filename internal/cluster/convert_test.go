package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/db"
	"gopkg.in/yaml.v3"
)

// A conversion's every gate is checked before the node leaves Raft: checked
// only at the registration, the node would be out of Raft with nowhere to go.
func TestReadyForAgentConversion(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	if err := mgr.ReadyForAgentConversion(mgr.LocalNodeID()); !errors.Is(err, ErrConvertLeader) {
		t.Fatalf("the leader itself: %v", err)
	}
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "a1", Kind: KindAgent})
	if err := mgr.ReadyForAgentConversion("a1"); !errors.Is(err, ErrConvertNotManager) {
		t.Fatalf("an agent: %v", err)
	}
	// A manager's record carries its addresses: its agent registers at their IP.
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter, APIAddress: "192.0.2.20:3628", GRPCAddress: "192.0.2.20:3629"})
	if err := mgr.ReadyForAgentConversion("m2"); err == nil || !strings.Contains(err.Error(), "not been heard from") {
		t.Fatalf("a manager not heard from: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{CapAgents, CapProxySecret}})
	if err := mgr.ReadyForAgentConversion("m2"); !errors.Is(err, ErrProxyRotationPending) {
		t.Fatalf("before the rotation settled: %v", err)
	}
	settleRotation(t, mgr)
	if err := mgr.ReadyForAgentConversion("m2"); err != nil {
		t.Fatalf("ready: %v", err)
	}
}

// What the node's registration under its own ID would refuse, and a quorum
// the managers left would lack, the conversion refuses before anything has
// changed. Each row starts from m2 ready, as TestReadyForAgentConversion
// leaves it.
func TestAConversionIsRefusedWhereItsRegistrationWouldBe(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	ready := Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter, APIAddress: "192.0.2.20:3628", GRPCAddress: "192.0.2.20:3629"}
	for _, tc := range []struct {
		name   string
		target func(*Node)
		caps   []string
		others []Node
		setup  func(t *testing.T)
		want   error
		reason string
	}{
		{name: "the managers left without a quorum", others: []Node{{ID: "m3", Name: "m3", Kind: KindManager, Role: RoleVoter, GRPCAddress: "192.0.2.30:3629"}},
			reason: "quorum"},
		{name: "another manager not ready for agents", others: []Node{{ID: "m3", Name: "m3", Kind: KindManager, Role: RoleNonVoter, GRPCAddress: "192.0.2.30:3629"}},
			want: ErrManagersNotReadyForAgents, reason: "m3 (not heard from)"},
		{name: "a version from before agents", caps: []string{}, reason: "predates agents"},
		{name: "no room for another agent", others: []Node{{ID: "a1", Name: "a1", Kind: KindAgent, GRPCAddress: "192.0.2.40:3629"}},
			setup: func(t *testing.T) {
				mgr.config.MaxAgents = 1
				t.Cleanup(func() { mgr.config.MaxAgents = 0 })
			}, want: ErrMaxAgentsReached},
		{name: "a name another node has", others: []Node{{ID: "a1", Name: "m2", Kind: KindAgent, GRPCAddress: "192.0.2.40:3629"}},
			want: ErrNodeIdentityTaken},
		{name: "an address another node has", others: []Node{{ID: "a1", Name: "a1", Kind: KindAgent, APIAddress: "192.0.2.20:8080", GRPCAddress: "192.0.2.20:3700"}},
			want: ErrNodeIdentityTaken},
		{name: "a name an agent may not have", target: func(n *Node) { n.Name = "m 2" }, reason: `the node name "m 2" is not one`},
		{name: "an address that is not an IP", target: func(n *Node) { n.GRPCAddress = "m2.example:3629" }, reason: "not an IP"},
		{name: "a leader without the CA key", setup: func(t *testing.T) {
			key, err := mgr.tls.LoadCAKey()
			if err != nil {
				t.Fatal(err)
			}
			replicated, _ := mgr.raft.GetFSM().GetConfigValue(configKeyCAKey)
			if err := os.Remove(filepath.Join(mgr.config.CertDir, "ca.key")); err != nil {
				t.Fatal(err)
			}
			applyCmd(t, mgr.raft.GetFSM(), Command{Type: CmdDeleteConfig, Key: configKeyCAKey})
			t.Cleanup(func() {
				_ = mgr.tls.SaveCAKey(key)
				applyCmd(t, mgr.raft.GetFSM(), Command{Type: CmdSetConfig, Key: configKeyCAKey, Value: mustJSON(replicated)})
			})
		}, want: ErrCAKeyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := ready
			if tc.target != nil {
				tc.target(&target)
			}
			addTestNode(t, mgr.raft.GetFSM(), target)
			caps := []string{CapAgents, CapProxySecret}
			if tc.caps != nil {
				caps = tc.caps
			}
			mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: caps})
			for _, n := range tc.others {
				addTestNode(t, mgr.raft.GetFSM(), n)
			}
			t.Cleanup(func() {
				for _, n := range tc.others {
					applyCmd(t, mgr.raft.GetFSM(), Command{Type: CmdRemoveNode, Key: n.ID})
				}
			})
			if tc.setup != nil {
				tc.setup(t)
			}
			err := mgr.ReadyForAgentConversion("m2")
			if !errors.Is(err, ErrConvertRefused) || (tc.want != nil && !errors.Is(err, tc.want)) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("got %v, want a refusal for %v %q", err, tc.want, tc.reason)
			}
		})
	}
	// And with every row undone, the same node is ready.
	addTestNode(t, mgr.raft.GetFSM(), ready)
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{CapAgents, CapProxySecret}})
	if err := mgr.ReadyForAgentConversion("m2"); err != nil {
		t.Fatalf("ready again: %v", err)
	}
}

// The files a manager keeps hold the cluster's keys — the CA key, every
// agent's key, the accounts, the managers' proxy secret — and an agent needs
// none of them.
func TestWipeManagerFiles(t *testing.T) {
	dir := t.TempDir()
	c := &config.ClusterConfig{CertDir: filepath.Join(dir, "certs"), DataDir: filepath.Join(dir, "data")}
	var names []string
	for _, p := range ManagerFiles(c) {
		names = append(names, filepath.Base(p))
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte("x"), 0o600)
	}
	if want := []string{"ca.key", "raft-log.db", "raft-stable.db", "snapshots", "tokens.json", "proxy-secret.json", "peers.json", "peers.info"}; !slices.Equal(names, want) {
		t.Fatalf("ManagerFiles names %v, want %v", names, want)
	}
	// Raft keeps its snapshots in a directory of them.
	snap := filepath.Join(c.DataDir, "snapshots")
	_ = os.Remove(snap)
	_ = os.MkdirAll(filepath.Join(snap, "2-10-1700000000"), 0o700)
	_ = os.WriteFile(filepath.Join(snap, "2-10-1700000000", "state.bin"), []byte("x"), 0o600)
	keep := filepath.Join(c.CertDir, "ca.crt")
	_ = os.WriteFile(keep, []byte("ca"), 0o600)
	removed, err := WipeManagerFiles(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != len(names) {
		t.Fatalf("removed %v", removed)
	}
	for _, p := range ManagerFiles(c) {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s stayed", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("ca.crt went too")
	}
	// Unset directories name nothing: never a relative path in the working one.
	if got := ManagerFiles(&config.ClusterConfig{}); len(got) != 0 {
		t.Fatalf("no directories: %v", got)
	}
}

// renameNode gives the record of id another name, on the leader.
func renameNode(t *testing.T, leader *Manager, id, name string) {
	t.Helper()
	n := leader.raft.GetFSM().GetNode(id)
	if n == nil {
		t.Fatalf("no record of %s", id)
	}
	n.Name = name
	if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(*n)}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

// convertTarget is a manager's config.yaml and what it loads as.
type convertTarget struct {
	Config *config.Config
	Path   string
}

// convertTestConfig writes m's config.yaml as m would run from it — its
// cluster block, the cluster's JWT key, a panel and a database of its own —
// to a temp dir.
func convertTestConfig(t *testing.T, m *Manager) convertTarget {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port = 1 // the panel port joinedManager's record names
	cfg.Database.Path = filepath.Join(dir, "sfpanel.db")
	cfg.Auth.JWTSecret = strings.Repeat("c", 64)
	cfg.Cluster = *m.GetConfig()
	path := filepath.Join(dir, "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return convertTarget{Config: cfg, Path: path}
}

// The whole conversion between two real managers: the second leaves Raft,
// wipes what a manager keeps, and registers under its own ID with its labels.
func TestAManagerConvertsToAnAgent(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	// Not at the leader's 127.0.0.1: an agent's address is its own.
	m2 := joinedManagerAt(t, addr, token, pin, "127.0.0.2")
	waitForVoter(t, m1, m2.LocalNodeID())
	settleRotation(t, m1)
	id := m2.LocalNodeID()
	// Both managers run on this host and are named after it; an agent's
	// name is its own too.
	renameNode(t, m1, id, "m2")
	if err := m1.UpdateNodeLabels(id, map[string]string{"group": "db"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 40*time.Second, "m2 online with its capabilities", func() bool { return m1.ReadyForAgentConversion(id) == nil })
	tok, err := m1.PrepareAgentConversion(id) // bound token + intent record
	if err != nil {
		t.Fatal(err)
	}
	cfg := convertTestConfig(t, m2) // m2's config.yaml, in a temp dir
	err = ConvertToAgent(m2, cfg.Config, cfg.Path, nil, "test", ConvertRequest{
		Token: tok.Token, Pin: pin, Manager: addr, Advertise: "127.0.0.2",
		PanelCA: panelCAPEM(t), CheckPanelCert: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	n := m1.raft.GetFSM().GetNode(id)
	if n == nil || n.Kind != KindAgent || n.Labels["group"] != "db" || n.Name != "m2" {
		t.Fatalf("on the leader: %+v", n)
	}
	if _, in, _ := m1.raft.Suffrage(id); in {
		t.Fatal("still in Raft")
	}
	if v, ok := m1.raft.GetFSM().GetConfigValue(ConversionIntentKey(id)); ok {
		t.Fatalf("the intent record stayed: %s", v)
	}
	for _, p := range ManagerFiles(&cfg.Config.Cluster) {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s stayed", p)
		}
	}
	saved, err := config.Load(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Cluster.IsAgent() || saved.Cluster.NodeID != id || saved.Auth.JWTSecret == strings.Repeat("c", 64) {
		t.Fatalf("saved config: %+v, the cluster's JWT key kept: %v", saved.Cluster, saved.Auth.JWTSecret == strings.Repeat("c", 64))
	}
}

// leftManagerConfig is the config.yaml of a manager that has just left its
// cluster, with the files a manager keeps beside it.
func leftManagerConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port = 1
	cfg.Database.Path = filepath.Join(dir, "sfpanel.db")
	cfg.Auth.JWTSecret = strings.Repeat("c", 64)
	cfg.Cluster = config.ClusterConfig{Enabled: true, Name: "victim", NodeID: "m2", NodeName: "m2", GRPCPort: freePortPair(t),
		AdvertiseAddress: "127.0.0.1", CertDir: filepath.Join(dir, "certs"), DataDir: filepath.Join(dir, "data"), RaftTLS: true}
	for _, p := range ManagerFiles(&cfg.Cluster) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "config.yaml")
	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, path
}

func loadSaved(t *testing.T, path string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// A leave that fails ends the conversion with nothing else changed: the node
// restarts as the manager it was.
func TestAConversionWhoseLeaveFailsChangesNothing(t *testing.T) {
	cfg, path := leftManagerConfig(t)
	before, _ := os.ReadFile(path)
	err := ConvertToAgent(NewManager(&cfg.Cluster), cfg, path, nil, "test", ConvertRequest{Manager: closedAddr(t)})
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("a leave with no Raft: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the config changed")
	}
	for _, p := range ManagerFiles(&cfg.Cluster) {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("%s went: %v", p, err)
		}
	}
}

// Once the node has left, its manager config names a membership that is
// gone: from there every stop lands on a standalone panel. A file that cannot
// be removed ends the conversion before it registers — an agent keeps no
// cluster key — and 'agent join' on the standalone panel names the file.
func TestAConversionThatCannotWipeEndsStandalone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes files from a read-only directory")
	}
	cfg, path := leftManagerConfig(t)
	snap := filepath.Join(cfg.Cluster.DataDir, "snapshots")
	_ = os.Remove(snap)
	stuck := filepath.Join(snap, "2-10-1700000000")
	if err := os.MkdirAll(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stuck, "state.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stuck, 0o500); err != nil { // its entries cannot be removed
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stuck, 0o700) })
	manager := closedAddr(t)
	err := convertLeftManager("m2", "m2", cfg, path, nil, "test", ConvertRequest{Token: "tok", Pin: "sha256:" + strings.Repeat("ab", 32), Manager: manager, Advertise: "127.0.0.1"})
	if err == nil || !strings.Contains(err.Error(), snap) || strings.Contains(err.Error(), manager) {
		t.Fatalf("a wipe that fails: %v", err)
	}
	saved := loadSaved(t, path)
	if saved.Cluster.Enabled || saved.Cluster.IsAgent() || saved.Auth.JWTSecret == strings.Repeat("c", 64) {
		t.Fatalf("saved config: enabled=%v role=%q, the cluster's JWT key kept: %v", saved.Cluster.Enabled, saved.Cluster.Role, saved.Auth.JWTSecret == strings.Repeat("c", 64))
	}
}

// A registration that fails after the wipe leaves a standalone panel holding
// none of a manager's secrets: no manager files, no local account — its
// password hash and TOTP seed — and no copy an update left of the config or
// the database.
func TestAConversionWhoseRegistrationFailsEndsStandaloneWithoutTheSecrets(t *testing.T) {
	cfg, path := leftManagerConfig(t)
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(`INSERT INTO admin (username, password, totp_secret) VALUES ('admin', 'hash', 'SEED')`); err != nil {
		t.Fatal(err)
	}
	baks := []string{path + ".bak", cfg.Database.Path + ".bak"}
	for _, p := range baks {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err = convertLeftManager("m2", "m2", cfg, path, database, "test", ConvertRequest{Token: "tok", Pin: "sha256:" + strings.Repeat("ab", 32), Manager: closedAddr(t), Advertise: "127.0.0.1"})
	if err == nil || !strings.Contains(err.Error(), "standalone") {
		t.Fatalf("a registration that fails: %v", err)
	}
	for _, p := range append(ManagerFiles(&cfg.Cluster), baks...) {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s stayed", p)
		}
	}
	var admins int
	if err := database.QueryRow(`SELECT COUNT(*) FROM admin`).Scan(&admins); err != nil || admins != 0 {
		t.Fatalf("local accounts: %d %v", admins, err)
	}
	if saved := loadSaved(t, path); saved.Cluster.Enabled || saved.Auth.JWTSecret == strings.Repeat("c", 64) {
		t.Fatalf("saved config: enabled=%v, the cluster's JWT key kept: %v", saved.Cluster.Enabled, saved.Auth.JWTSecret == strings.Repeat("c", 64))
	}
}

// Backups a manager took hold sfpanel.db and config.yaml, the cluster's
// secrets among them; they stay, and the conversion says where they are.
func TestManagerBackups(t *testing.T) {
	cfg := &config.Config{}
	cfg.Database.Path = filepath.Join(t.TempDir(), "sfpanel.db")
	dir := filepath.Join(filepath.Dir(cfg.Database.Path), "backups")
	if got := ManagerBackups(cfg); got != "" {
		t.Fatalf("no backups directory: %q", got)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ManagerBackups(cfg); got != "" {
		t.Fatalf("an empty one: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "sfpanel-backup-20261020-030000.tar.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ManagerBackups(cfg); got != dir {
		t.Fatalf("a backup: %q", got)
	}
}
