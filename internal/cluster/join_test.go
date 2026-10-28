package cluster

import (
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/db"
	"gopkg.in/yaml.v3"
)

func TestJoinEngine_Rollback_ConfigSaveFailure(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	configPath := filepath.Join(tmpDir, "config.yaml")

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "0.0.0.0", Port: 8443},
		Auth:   config.AuthConfig{JWTSecret: "old-secret", TokenExpiry: "24h"},
		Cluster: config.ClusterConfig{
			GRPCPort: 9444,
			CertDir:  certDir,
			DataDir:  filepath.Join(tmpDir, "data"),
		},
		Database: config.DatabaseConfig{Path: filepath.Join(tmpDir, "test.db")},
	}

	// Write an initial config
	os.WriteFile(configPath, []byte("server:\n  port: 8443\n"), 0600)

	engine := &JoinEngine{
		ConfigPath: configPath,
		Config:     cfg,
	}

	// Test rollback: save certs then fail config save with read-only path
	os.MkdirAll(certDir, 0755)
	os.WriteFile(filepath.Join(certDir, "ca.crt"), []byte("fake-ca"), 0600)

	// The join has already put the cluster's key into the config by the time
	// any rollback runs; the running panel is still on its own.
	auth.SetJWTSecret("old-secret")
	defer auth.SetJWTSecret("")
	cfg.Auth.JWTSecret = "cluster-secret"

	// Verify rollbackJoin cleans up certs and restores config
	originalConfig, _ := os.ReadFile(configPath)
	engine.rollbackJoin(certDir, originalConfig, "old-secret")

	// The key in memory goes back too: cluster init would otherwise hand the
	// abandoned cluster's key to a new cluster as this node's own.
	if cfg.Auth.JWTSecret != "old-secret" {
		t.Errorf("rollback left the config key at %q, want the node's own", cfg.Auth.JWTSecret)
	}
	// And a failed join never published the cluster's key to the running panel.
	if got := auth.JWTSecret(); got != "old-secret" {
		t.Errorf("running key is %q after a rolled-back join, want the node's own", got)
	}

	// Cert dir should be removed
	if _, err := os.Stat(certDir); !os.IsNotExist(err) {
		t.Error("rollback should have removed cert dir")
	}

	// Config should be restored
	restored, _ := os.ReadFile(configPath)
	if string(restored) != string(originalConfig) {
		t.Error("rollback should have restored original config")
	}
}

func adminRows(t *testing.T, database *sql.DB) []map[string]string {
	t.Helper()
	rows, err := database.Query(`SELECT username, password, COALESCE(totp_secret, '<null>'), COALESCE(recovery_codes, '<null>') FROM admin ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var u, p, totp, codes string
		if err := rows.Scan(&u, &p, &totp, &codes); err != nil {
			t.Fatal(err)
		}
		out = append(out, map[string]string{"username": u, "password": p, "totp": totp, "codes": codes})
	}
	return out
}

// A joining node's own admin under another name used to survive the join: a
// working login here whose tokens, signed with the cluster key, every node
// accepted, outside the cluster's password and 2FA. The join must replace it
// with the cluster admin, username included, and drop its recovery codes.
func TestAdoptClusterAdmin_ReplacesADifferentlyNamedLocalAdmin(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "sfpanel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO admin (username, password, totp_secret, recovery_codes) VALUES ('ops', 'local-hash', NULL, '["local-code"]')`); err != nil {
		t.Fatal(err)
	}

	if err := adoptClusterAdmin(database, "admin", "cluster-hash", "TOTPSECRET"); err != nil {
		t.Fatalf("adoptClusterAdmin: %v", err)
	}

	got := adminRows(t, database)
	if len(got) != 1 || got[0]["username"] != "admin" || got[0]["password"] != "cluster-hash" ||
		got[0]["totp"] != "TOTPSECRET" || got[0]["codes"] != "<null>" {
		t.Fatalf("admin rows after join = %v, want only the cluster admin with no local recovery codes", got)
	}
}

// A node with no local admin (set up only through the CLI) gets one, and a
// cluster without 2FA stays without it: NULL, not an empty secret.
func TestAdoptClusterAdmin_InsertsWhenThereIsNoLocalAdmin(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "sfpanel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := adoptClusterAdmin(database, "admin", "cluster-hash", ""); err != nil {
		t.Fatalf("adoptClusterAdmin: %v", err)
	}

	got := adminRows(t, database)
	if len(got) != 1 || got[0]["username"] != "admin" || got[0]["totp"] != "<null>" {
		t.Fatalf("admin rows after join = %v, want one cluster admin with 2FA off", got)
	}
}

// An agent promoted to a manager joins under its own ID, and its config comes
// out a manager's: one that kept role: agent and its managers list booted it
// as an agent again.
func TestJoinEngineJoinsUnderItsOwnIDAsAManager(t *testing.T) {
	mgr, addr, token, pin := realLeader(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{}
	cfg.Server.Port = 1
	cfg.Cluster.GRPCPort = freePortPair(t)
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	cfg.Cluster.Role = config.ClusterRoleAgent
	cfg.Cluster.Managers = []config.ManagerRef{{ID: mgr.LocalNodeID(), GRPCAddress: addr}}
	e := &JoinEngine{Config: cfg, ConfigPath: path, NodeID: "node-x"}
	res, err := e.Execute(addr, token, "127.0.0.1", pin)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved config.Config
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if res.NodeID != "node-x" || saved.Cluster.NodeID != "node-x" || !saved.Cluster.Enabled || saved.Cluster.Role != "" || len(saved.Cluster.Managers) != 0 {
		t.Fatalf("joined as %q, saved %+v", res.NodeID, saved.Cluster)
	}
	if n := mgr.raft.GetFSM().GetNode("node-x"); n == nil || !n.IsManager() {
		t.Fatalf("on the leader: %+v", n)
	}
}

// An agent being promoted still runs its gRPC server on the cluster gRPC
// port, which the manager binds only after its restart: PreFlight's check
// that the port is free is skipped when asked, and only then.
func TestJoinPreFlightSkipsThePortCheckWhenAsked(t *testing.T) {
	_, addr, token, pin := realLeader(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	e := &JoinEngine{Config: &config.Config{}}
	e.Config.Cluster.GRPCPort = busy.Addr().(*net.TCPAddr).Port
	if _, err := e.PreFlight(addr, token, pin); err == nil || !strings.Contains(err.Error(), "already in use locally") {
		t.Fatalf("a busy port: %v", err)
	}
	e.SkipPortCheck = true
	if _, err := e.PreFlight(addr, token, pin); err != nil {
		t.Fatalf("a busy port, skipped: %v", err)
	}
}
