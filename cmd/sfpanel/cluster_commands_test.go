package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/db"
)

// attachCSRFIfNeeded synthesizes a same-value cookie+header pair so
// CLI-issued state-changing calls satisfy CSRFProtect middleware. Tested
// here so a future refactor that breaks the same-value invariant — or
// drops the cookie on the safe-method path — is caught early. The
// middleware-side reception is covered by TestCSRFProtect_HeaderMatchAccepted
// in internal/api/middleware/csrf_test.go.

func TestAttachCSRFIfNeeded_AttachesSameValueOnPOST(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:3628/api/v1/cluster/leader-transfer", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if err := attachCSRFIfNeeded(req); err != nil {
		t.Fatalf("attach: %v", err)
	}
	cookie, err := req.Cookie(auth.CSRFCookieName)
	if err != nil {
		t.Fatalf("CSRF cookie missing: %v", err)
	}
	header := req.Header.Get(auth.CSRFHeaderName)
	if header == "" {
		t.Fatalf("CSRF header missing")
	}
	if cookie.Value != header {
		t.Errorf("cookie %q != header %q (middleware does constant-time compare)", cookie.Value, header)
	}
	if len(cookie.Value) < 16 {
		t.Errorf("token too short: %d chars, want >=16 hex", len(cookie.Value))
	}
}

func TestAttachCSRFIfNeeded_SkipsSafeMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			req, err := http.NewRequest(method, "http://127.0.0.1:3628/api/v1/cluster/status", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if err := attachCSRFIfNeeded(req); err != nil {
				t.Fatalf("attach: %v", err)
			}
			if _, err := req.Cookie(auth.CSRFCookieName); err == nil {
				t.Errorf("%s should not carry CSRF cookie (safe method bypasses middleware)", method)
			}
			if req.Header.Get(auth.CSRFHeaderName) != "" {
				t.Errorf("%s should not carry CSRF header", method)
			}
		})
	}
}

func TestAttachCSRFIfNeeded_TokensAreUnique(t *testing.T) {
	req1, _ := http.NewRequest(http.MethodPost, "/x", nil)
	req2, _ := http.NewRequest(http.MethodPost, "/x", nil)
	if err := attachCSRFIfNeeded(req1); err != nil {
		t.Fatal(err)
	}
	if err := attachCSRFIfNeeded(req2); err != nil {
		t.Fatal(err)
	}
	c1, _ := req1.Cookie(auth.CSRFCookieName)
	c2, _ := req2.Cookie(auth.CSRFCookieName)
	if c1.Value == c2.Value {
		t.Errorf("two attach calls produced identical tokens: %q (rand should diverge)", c1.Value)
	}
}

// TestSyncBootstrapState_NotLeaderBailsFast pins the deadline-bail path of the
// shared bootstrap-sync helper. A Manager with a nil raft (never Init'd) reports
// IsLeader()==false, so syncBootstrapState must poll until leaderWait elapses,
// then return promptly without ever touching SetConfig (which would error, not
// panic, on a nil raft). We assert it returns well under 1s for a 50ms wait and
// does not panic. The happy-path FSM write needs a live Raft leader and is only
// reachable via integration, not this unit harness.
func TestSyncBootstrapState_NotLeaderBailsFast(t *testing.T) {
	mgr := cluster.NewManager(&config.ClusterConfig{NodeID: "t"})

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "sfpanel.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	start := time.Now()
	syncBootstrapState(context.Background(), mgr, database, "secret", 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("not-leader bail took %v, want < 1s (leaderWait was 50ms)", elapsed)
	}
}

// TestSyncBootstrapState_NotLeaderWithAdminRowBailsFast is the same bail path
// but with an admin row present in the DB. The helper must still bail on
// leadership before running the admin SELECT, so the row is never read and the
// function returns fast without panic.
func TestSyncBootstrapState_NotLeaderWithAdminRowBailsFast(t *testing.T) {
	mgr := cluster.NewManager(&config.ClusterConfig{NodeID: "t"})

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "sfpanel.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	if _, err := database.Exec(
		"INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, ?)",
		"admin", "hash", nil,
	); err != nil {
		t.Fatalf("seed admin row: %v", err)
	}

	start := time.Now()
	syncBootstrapState(context.Background(), mgr, database, "secret", 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("not-leader bail took %v, want < 1s (leaderWait was 50ms)", elapsed)
	}
}

// TestSyncBootstrapState_ContextCancelExitsPromptly pins the C9 shutdown path:
// when the supplied context is already cancelled, the helper must abandon its
// leadership wait immediately rather than running the full leaderWait deadline.
// A nil-raft Manager reports IsLeader()==false forever, so without ctx-cancel
// this would block for the whole 30s. We assert it returns well under a second.
func TestSyncBootstrapState_ContextCancelExitsPromptly(t *testing.T) {
	mgr := cluster.NewManager(&config.ClusterConfig{NodeID: "t"})

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "sfpanel.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	syncBootstrapState(ctx, mgr, database, "", 30*time.Second)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("cancelled-ctx exit took %v, want < 1s (leaderWait was 30s; shutdown must interrupt the wait)", elapsed)
	}
}

// TestSaveConfigWritesRestrictivePerms guards against accidentally widening
// config.yaml perms — the file holds the JWT secret and must stay 0600.
func TestSaveConfigWritesRestrictivePerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := &config.Config{}
	cfg.Server.Port = 19443
	cfg.Auth.JWTSecret = "super-secret-test-token"

	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config.yaml perm = %o, want 0600 (JWT secret must not be world-readable)", got)
	}
}
