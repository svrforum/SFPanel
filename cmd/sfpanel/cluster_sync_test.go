package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/db"
)

// freeRaftPorts returns a gRPC port whose Raft neighbour (port+1) is also free
// on loopback, since Manager.Init binds Raft at GRPCPort+1.
func freeRaftPorts(t *testing.T) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if next, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+1)); err == nil {
			next.Close()
			return port
		}
	}
	t.Fatal("no free port pair on loopback")
	return 0
}

// singleNodeLeader initialises a real one-node cluster on loopback: the only
// way to reach the leader branch of syncBootstrapState.
func singleNodeLeader(t *testing.T) *cluster.Manager {
	t.Helper()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{
		NodeID:           "boot-test",
		NodeName:         "boot-test",
		AdvertiseAddress: "127.0.0.1",
		GRPCPort:         freeRaftPorts(t),
		APIPort:          3628,
		DataDir:          filepath.Join(dir, "data"),
		CertDir:          filepath.Join(dir, "certs"),
	})
	if err := mgr.Init("boot-test"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(mgr.Shutdown)
	return mgr
}

func openAdminDB(t *testing.T, username, passHash string) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "sfpanel.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec("INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, NULL)", username, passHash); err != nil {
		t.Fatalf("seed admin row: %v", err)
	}
	return database
}

// While clustered, password and 2FA changes are written to the FSM only, so
// this node's local admin row is whatever it was at join time. A node that
// wins an election inside the boot window used to push that row back and
// overwrite the cluster account: the password reverted and 2FA switched off on
// every node.
func TestSyncBootstrapState_LeaderKeepsTheClusterAccount(t *testing.T) {
	mgr := singleNodeLeader(t)
	if err := mgr.SetAccount(cluster.AdminAccount{Username: "admin", Password: "hash-changed-later", TOTPSecret: "TOTPSECRET"}); err != nil {
		t.Fatalf("SetAccount: %v", err)
	}
	if err := mgr.SetConfig("jwt_secret", "cluster-secret"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	database := openAdminDB(t, "admin", "hash-at-join-time")

	syncBootstrapState(context.Background(), mgr, database, "stale-local-secret", 5*time.Second)

	acct := mgr.GetAccount("admin")
	if acct == nil || acct.Password != "hash-changed-later" || acct.TOTPSecret != "TOTPSECRET" {
		t.Fatalf("cluster account was overwritten by the local row: %+v", acct)
	}
	if js, _, _, _ := mgr.GetJWTAndAdminFull(); js != "cluster-secret" {
		t.Fatalf("cluster jwt_secret = %q, want the replicated value kept", js)
	}
}

// A local admin under another name must not become a second cluster admin.
func TestSyncBootstrapState_LeaderAddsNoSecondAdmin(t *testing.T) {
	mgr := singleNodeLeader(t)
	if err := mgr.SetAccount(cluster.AdminAccount{Username: "admin", Password: "cluster-hash"}); err != nil {
		t.Fatalf("SetAccount: %v", err)
	}
	database := openAdminDB(t, "ops", "local-hash")

	syncBootstrapState(context.Background(), mgr, database, "", 5*time.Second)

	if accts := mgr.GetAccounts(); len(accts) != 1 || accts["admin"] == nil {
		t.Fatalf("cluster accounts = %v, want only admin", accts)
	}
}

// The seed itself still has to happen: a cluster whose FSM holds no account
// yet (the CLI init path, or a panel upgrading into account replication) takes
// the local admin and secret — and the admin's 2FA recovery codes, which were
// left behind: once the account lived in the FSM, login read the FSM's empty
// list and every code the operator had saved stopped working.
func TestSyncBootstrapState_LeaderSeedsAnEmptyCluster(t *testing.T) {
	mgr := singleNodeLeader(t)
	database := openAdminDB(t, "admin", "local-hash")
	if _, err := database.Exec(`UPDATE admin SET recovery_codes = ? WHERE username = 'admin'`, `["hash-one","hash-two"]`); err != nil {
		t.Fatalf("seed recovery codes: %v", err)
	}

	syncBootstrapState(context.Background(), mgr, database, "local-secret", 5*time.Second)

	if acct := mgr.GetAccount("admin"); acct == nil || acct.Password != "local-hash" {
		t.Fatalf("empty cluster was not seeded from the local admin: %+v", acct)
	}
	if codes := mgr.GetRecoveryCodes("admin"); len(codes) != 2 || codes[0] != "hash-one" || codes[1] != "hash-two" {
		t.Fatalf("recovery codes in the cluster = %v, want the two saved locally", codes)
	}
	if js, _, _, _ := mgr.GetJWTAndAdminFull(); js != "local-secret" {
		t.Fatalf("cluster jwt_secret = %q, want the local secret seeded", js)
	}
}
