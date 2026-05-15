package featureauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"

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
