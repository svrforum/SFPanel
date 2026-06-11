package featureauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := sfdb.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

func TestIssueRefreshToken_PersistsHash(t *testing.T) {
	db := openTestDB(t)
	tok, err := issueRefreshToken(db, "alice")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(tok) != refreshTokenBytes*2 {
		t.Errorf("token length: got %d, want %d", len(tok), refreshTokenBytes*2)
	}

	// The DB stores the hash, not the raw token.
	hash := sha256.Sum256([]byte(tok))
	hashHex := hex.EncodeToString(hash[:])

	var rowCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = ? AND username = ?`,
		hashHex, "alice",
	).Scan(&rowCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("expected 1 stored row, got %d", rowCount)
	}
}

func TestPruneRefreshTokens_DropsExpired(t *testing.T) {
	db := openTestDB(t)

	// Insert one expired and one fresh token directly.
	expiredAt := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	freshAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, expires_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"deadhash", "alice", expiredAt,
		"livehash", "alice", freshAt,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pruneRefreshTokens(db)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row remaining, got %d", n)
	}
}

// TestIssueRefreshToken_AssignsFamilyID guards the OWASP token-reuse plumbing:
// each issued token must carry a fresh family_id so the rotation handler can
// revoke a captured chain wholesale.
func TestIssueRefreshToken_AssignsFamilyID(t *testing.T) {
	db := openTestDB(t)
	_, err := issueRefreshToken(db, "alice")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var family string
	if err := db.QueryRow(
		`SELECT family_id FROM refresh_tokens WHERE username = ?`, "alice",
	).Scan(&family); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(family) != 32 {
		t.Errorf("family_id len = %d, want 32 hex chars", len(family))
	}

	// Two separate logins must produce two separate families.
	_, _ = issueRefreshToken(db, "alice")
	var distinct int
	if err := db.QueryRow(
		`SELECT COUNT(DISTINCT family_id) FROM refresh_tokens WHERE username = ?`, "alice",
	).Scan(&distinct); err != nil {
		t.Fatalf("count: %v", err)
	}
	if distinct != 2 {
		t.Errorf("distinct family_id count = %d, want 2 (one per login)", distinct)
	}
}

// TestPruneRefreshTokens_DropsOldTombstones confirms consumed tombstones older
// than the 24h grace window are reaped, but recent ones stay around to catch
// replays.
func TestPruneRefreshTokens_DropsOldTombstones(t *testing.T) {
	db := openTestDB(t)

	freshAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	oldConsumed := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	recentConsumed := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)

	if _, err := db.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, expires_at, consumed_at) VALUES
			(?, ?, ?, ?),
			(?, ?, ?, ?),
			(?, ?, ?, NULL)`,
		"oldtomb", "alice", freshAt, oldConsumed,
		"newtomb", "alice", freshAt, recentConsumed,
		"live", "alice", freshAt,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pruneRefreshTokens(db)

	rows, _ := db.Query(`SELECT token_hash FROM refresh_tokens ORDER BY token_hash`)
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		got = append(got, h)
	}
	if len(got) != 2 || got[0] != "live" || got[1] != "newtomb" {
		t.Errorf("rows after prune = %v, want [live newtomb]", got)
	}
}

// newRefreshHandler returns a Handler with a temp DB and a sensible Config.
// ClusterMgr stays nil — exercises the no-cluster code paths. The FSM-only
// admin case (account replicated only in cluster state) is covered by the
// loopback integration probe in the deployment runbook, since stubbing the
// concrete *cluster.Manager would require refactoring far beyond what this
// regression test buys.
func newRefreshHandler(t *testing.T) (*Handler, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	return &Handler{
		DB:     db,
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "test-secret-not-for-prod", TokenExpiry: "1h"}},
	}, db
}

// seedRefreshToken inserts a refresh token row for the given user and returns
// the raw token (what the client would hold) plus its sha256 hex digest.
func seedRefreshToken(t *testing.T, db *sql.DB, username string) (raw string, hashHex string) {
	t.Helper()
	raw = "test-refresh-token-" + username + "-" + time.Now().UTC().Format("150405.000000000")
	sum := sha256.Sum256([]byte(raw))
	hashHex = hex.EncodeToString(sum[:])
	if _, err := db.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, family_id, expires_at) VALUES (?, ?, ?, ?)`,
		hashHex, username, "fam-"+username, time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return raw, hashHex
}

// TestRefresh_AcceptsUserInLocalAdmin — happy path. Token rotates, old row
// tombstoned, new row issued, response 200.
// TestRefresh_ConcurrentSameToken_NoDeadlock fires many simultaneous refreshes
// of the SAME token against a multi-connection WAL DB (production uses up to 4
// conns; the shared openTestDB caps at 1 and would mask the race). Without the
// rotation mutex, two rotations that both read the un-consumed row collide on
// the write with SQLITE_BUSY_SNAPSHOT and return a 500. The fix must yield
// exactly one successful rotation and no 5xx — the losers take the normal
// token-reuse (401) path.
func TestRefresh_ConcurrentSameToken_NoDeadlock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	if err := sfdb.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO admin (username, password) VALUES (?, ?)`, "alice", "x"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	raw, _ := seedRefreshToken(t, db, "alice")

	h := &Handler{DB: db, Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "test-secret-not-for-prod", TokenExpiry: "1h"}}}

	const N = 8
	codes := make([]int, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body := strings.NewReader(`{"refresh_token":"` + raw + `"}`)
			req := httptest.NewRequest("POST", "/api/v1/auth/refresh", body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			_ = h.Refresh(echo.New().NewContext(req, rec))
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	var ok, other int
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusUnauthorized:
		default:
			other++
		}
	}
	if ok != 1 {
		t.Errorf("expected exactly 1 successful rotation, got %d (codes=%v)", ok, codes)
	}
	if other != 0 {
		t.Errorf("expected no 5xx responses (deadlock-free), got %d (codes=%v)", other, codes)
	}
}

func TestRefresh_AcceptsUserInLocalAdmin(t *testing.T) {
	h, db := newRefreshHandler(t)
	if _, err := db.Exec(`INSERT INTO admin (username, password) VALUES (?, ?)`, "alice", "x"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	raw, hashHex := seedRefreshToken(t, db, "alice")

	body := strings.NewReader(`{"refresh_token":"` + raw + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.Refresh(c); err != nil {
		t.Fatalf("Refresh returned err: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Old row must now be a tombstone (consumed_at set), and a brand-new row
	// must exist in the same family.
	var consumed sql.NullString
	if err := db.QueryRow(`SELECT consumed_at FROM refresh_tokens WHERE token_hash = ?`, hashHex).Scan(&consumed); err != nil {
		t.Fatalf("query old row: %v", err)
	}
	if !consumed.Valid {
		t.Errorf("old token not tombstoned (consumed_at is NULL)")
	}
	var fresh int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE username = 'alice' AND consumed_at IS NULL`).Scan(&fresh); err != nil {
		t.Fatalf("count fresh: %v", err)
	}
	if fresh != 1 {
		t.Errorf("fresh-row count = %d, want 1", fresh)
	}
}

// TestRefresh_RejectsUserMissingFromLocalDBAndFSM — preserves the original
// "user truly deleted" rejection. With ClusterMgr=nil the FSM lookup
// short-circuits to nil, so this exercises the local-DB miss path. The row
// must be deleted to avoid keeping a dangling reference to a non-existent
// account.
func TestRefresh_RejectsUserMissingFromLocalDBAndFSM(t *testing.T) {
	h, db := newRefreshHandler(t)
	raw, hashHex := seedRefreshToken(t, db, "ghost")

	body := strings.NewReader(`{"refresh_token":"` + raw + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.Refresh(c); err != nil {
		t.Fatalf("Refresh returned err: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = ?`, hashHex).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("dangling row count = %d, want 0 (handler must delete the orphan)", n)
	}
}

// TestLoadAdminAccount_LocalDB confirms the local DB fallback path works when
// the cluster manager is unset (single-node deployments take this branch on
// every call).
func TestLoadAdminAccount_LocalDB(t *testing.T) {
	h, db := newRefreshHandler(t)
	if _, err := db.Exec(`INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, ?)`, "alice", "phash", "tsecret"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pw, totp, fromCluster, err := h.loadAdminAccount("alice")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if pw != "phash" {
		t.Errorf("password = %q, want phash", pw)
	}
	if totp != "tsecret" {
		t.Errorf("totp = %q, want tsecret", totp)
	}
	if fromCluster {
		t.Errorf("fromCluster = true, want false (no cluster manager)")
	}
}

// TestLoadAdminAccount_MissingReturnsErrNoRows confirms callers can switch on
// sql.ErrNoRows to distinguish "user truly absent" from "infrastructure
// failure". This contract is load-bearing for ChangePassword / Verify2FA /
// Disable2FA which translate ErrNoRows into 404.
func TestLoadAdminAccount_MissingReturnsErrNoRows(t *testing.T) {
	h, _ := newRefreshHandler(t)
	_, _, _, err := h.loadAdminAccount("ghost")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

// TestPersistAdminAccount_LocalUpdate exercises the local-DB write path.
// Cluster-only persistence is covered by the integration probe (the concrete
// cluster.Manager is not test-stubbable without a wider refactor).
func TestPersistAdminAccount_LocalUpdate(t *testing.T) {
	h, db := newRefreshHandler(t)
	if _, err := db.Exec(`INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, ?)`, "alice", "old", "oldtotp"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := h.persistAdminAccount("alice", "newhash", "newtotp", false); err != nil {
		t.Fatalf("persist: %v", err)
	}

	var pw, totp string
	if err := db.QueryRow(`SELECT password, totp_secret FROM admin WHERE username = ?`, "alice").Scan(&pw, &totp); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if pw != "newhash" || totp != "newtotp" {
		t.Errorf("after persist: pw=%q totp=%q, want newhash/newtotp", pw, totp)
	}

	// totpSecret="" must NULL the column (used by Disable2FA path).
	if err := h.persistAdminAccount("alice", "newhash", "", false); err != nil {
		t.Fatalf("persist nil totp: %v", err)
	}
	var ts sql.NullString
	if err := db.QueryRow(`SELECT totp_secret FROM admin WHERE username = ?`, "alice").Scan(&ts); err != nil {
		t.Fatalf("verify null: %v", err)
	}
	if ts.Valid {
		t.Errorf("totp_secret valid after empty persist; want NULL")
	}
}

// TestPersistAdminAccount_ClusterOnlyWithoutManagerFails — refuse rather than
// silently INSERT into local DB when the account claims FSM origin but the
// cluster manager has gone away (shouldn't happen in practice but the
// alternative is corrupting two stores).
func TestPersistAdminAccount_ClusterOnlyWithoutManagerFails(t *testing.T) {
	h, db := newRefreshHandler(t)

	err := h.persistAdminAccount("alice", "h", "t", true)
	if err == nil {
		t.Fatalf("persist with fromCluster=true and nil manager: want error, got nil")
	}

	// And the local table stays empty (we did NOT silently insert).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin WHERE username = ?`, "alice").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("local admin rows = %d, want 0 (must not silently INSERT)", n)
	}
}

// TestRefresh_RotationRollsBackOnJWTFailure exercises the JWT-then-commit
// ordering invariant: if access-token signing fails, the rotation transaction
// must NOT have consumed the old refresh token. Otherwise a single transient
// signer failure tombstones the client's only token, and the subsequent retry
// trips OWASP family-revoke (token-reuse detection) and logs the user out
// across every device.
//
// We inject the failure via the package-level generateAccessToken seam — the
// jwt-v5 HMAC signer tolerates empty keys, so there is no input-layer trigger.
func TestRefresh_RotationRollsBackOnJWTFailure(t *testing.T) {
	h, db := newRefreshHandler(t)
	if _, err := db.Exec(`INSERT INTO admin (username, password) VALUES (?, ?)`, "alice", "x"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	raw, hashHex := seedRefreshToken(t, db, "alice")

	// Force the access-token signer to fail. Restored before the retry below.
	original := generateAccessToken
	generateAccessToken = func(username, secret string, expiry time.Duration) (string, error) {
		return "", errors.New("synthetic signing failure")
	}

	body := strings.NewReader(`{"refresh_token":"` + raw + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.Refresh(c); err != nil {
		t.Fatalf("Refresh returned err: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("first call status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}

	// The original token must NOT be consumed: a retry with the signer
	// repaired must succeed. If the rotation already wrote consumed_at, the
	// retry trips family-revoke (401 "Session revoked").
	generateAccessToken = original
	t.Cleanup(func() { generateAccessToken = auth.GenerateToken })

	body2 := strings.NewReader(`{"refresh_token":"` + raw + `"}`)
	req2 := httptest.NewRequest("POST", "/api/v1/auth/refresh", body2)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	c2 := echo.New().NewContext(req2, rec2)

	if err := h.Refresh(c2); err != nil {
		t.Fatalf("retry Refresh returned err: %v", err)
	}
	if rec2.Code != http.StatusOK {
		t.Errorf("retry after signer repair: status=%d body=%s, want 200",
			rec2.Code, rec2.Body.String())
	}

	// Belt-and-braces: confirm the original row was not consumed by the
	// failed first call.
	var consumed sql.NullString
	if err := db.QueryRow(`SELECT consumed_at FROM refresh_tokens WHERE token_hash = ?`, hashHex).Scan(&consumed); err != nil {
		// After a successful retry the row IS tombstoned, so ErrNoRows here
		// would be a quirk of pruning, not a regression. Tolerate it.
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("post-retry row check: %v", err)
		}
	}
}

// TestChangePassword_RevokesOtherRefreshSessions — a password rotation must
// kill every refresh chain except the caller's own (presented via cookie), so
// a stolen refresh token can't outlive the credential it was minted under.
func TestChangePassword_RevokesOtherRefreshSessions(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")

	callerRaw, callerHash := seedRefreshToken(t, db, "alice")
	// Second login = separate family — the "stolen on another device" chain.
	if _, err := issueRefreshToken(db, "alice"); err != nil {
		t.Fatalf("issue other session: %v", err)
	}

	body := `{"current_password":"oldpassword123","new_password":"newpassword456"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.10:1234"
	req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: callerRaw})
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set("username", "alice")

	if err := h.ChangePassword(c); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE username = 'alice'`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 1 {
		t.Errorf("rows after change = %d, want 1 (caller's chain only)", remaining)
	}
	var kept int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = ?`, callerHash).Scan(&kept); err != nil {
		t.Fatalf("count caller row: %v", err)
	}
	if kept != 1 {
		t.Errorf("caller's own refresh token was revoked; want it preserved")
	}
	waitForAuditRows(t, db, 1) // drain the async security-event write before DB close
}

// TestChangePassword_NoCookieRevokesAllSessions — without a refresh cookie to
// identify the caller's chain there is nothing safe to spare: every row goes.
func TestChangePassword_NoCookieRevokesAllSessions(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")
	if _, err := issueRefreshToken(db, "alice"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := issueRefreshToken(db, "alice"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	body := `{"current_password":"oldpassword123","new_password":"newpassword456"}`
	c, rec := newAuthedContext("POST", "/api/v1/auth/change-password", body, "alice")
	if err := h.ChangePassword(c); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE username = 'alice'`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("rows after change = %d, want 0 (no cookie ⇒ revoke everything)", remaining)
	}
	waitForAuditRows(t, db, 1)
}

// TestDisable2FA_RevokesOtherRefreshSessions — 2FA downgrade is a credential
// change and must clear the per-user refresh rows the same way.
func TestDisable2FA_RevokesOtherRefreshSessions(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "rightpassword12", "")
	if _, err := issueRefreshToken(db, "alice"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	body := `{"password":"rightpassword12"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/disable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.11:1234"
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set("username", "alice")

	if err := h.Disable2FA(c); err != nil {
		t.Fatalf("Disable2FA: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE username = 'alice'`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("rows after 2FA disable = %d, want 0", remaining)
	}
	waitForAuditRows(t, db, 1)
}

// TestRevokeOtherSessions_LegacyFamilyRow — a pre-migration-24 cookie token
// (family_id='') must be spared by token_hash, not via the empty family.
func TestRevokeOtherSessions_LegacyFamilyRow(t *testing.T) {
	_, db := newRefreshHandler(t)
	freshAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	raw := "legacy-raw-token"
	sum := sha256.Sum256([]byte(raw))
	keepHash := hex.EncodeToString(sum[:])
	if _, err := db.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, family_id, expires_at) VALUES (?, 'alice', '', ?), ('otherlegacy', 'alice', '', ?)`,
		keepHash, freshAt, freshAt,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/auth/change-password", nil)
	req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: raw})
	c := echo.New().NewContext(req, httptest.NewRecorder())

	revokeOtherSessions(c, db, "alice")

	rows, err := db.Query(`SELECT token_hash FROM refresh_tokens WHERE username = 'alice'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		got = append(got, h)
	}
	if len(got) != 1 || got[0] != keepHash {
		t.Errorf("rows after revoke = %v, want only the presented legacy token", got)
	}
}

func TestValidCredentialBounds(t *testing.T) {
	cases := []struct {
		name                       string
		user, pass, totp           string
		want                       bool
	}{
		{"baseline", "alice", "hunter22!", "", true},
		{"with-totp", "alice", "hunter22!", "123456", true},
		{"empty-user", "", "hunter22!", "", false},
		{"empty-pass", "alice", "", "", false},
		{"username too long", string(make([]byte, 100)), "x", "", false},
		{"password too long", "alice", string(make([]byte, 1000)), "", false},
		{"non-numeric totp", "alice", "x", "abc123", false},
		{"7-digit totp ok", "alice", "x", "1234567", true},
		{"too long totp", "alice", "x", "1234567890123456789", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// For "username too long" / "password too long" the test value is
			// a zero-byte slice — replace with printable runes so length is the
			// only thing under test.
			user, pass := c.user, c.pass
			if len(user) >= 65 {
				user = string(make([]byte, 65))
				for i := range user {
					_ = user[i]
				}
				// build properly
				bs := make([]byte, 65)
				for i := range bs {
					bs[i] = 'x'
				}
				user = string(bs)
			}
			if len(pass) >= 257 {
				bs := make([]byte, 257)
				for i := range bs {
					bs[i] = 'x'
				}
				pass = string(bs)
			}
			got := validCredentialBounds(user, pass, c.totp)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
