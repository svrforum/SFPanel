package featureauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

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
