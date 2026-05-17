package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/db"
)

// openTestDB opens an on-disk SQLite, runs every registered migration, and
// returns the DB handle. Using an on-disk file (not :memory:) keeps the WAL
// semantics consistent with production.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "audit_test.db")
	d, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	require.NoError(t, err)
	d.SetMaxOpenConns(1)
	require.NoError(t, d.Ping())
	t.Cleanup(func() { d.Close() })
	require.NoError(t, db.RunMigrations(d))
	return d
}

// insertLog seeds an audit row with control over created_at + protected so
// the time-window tests don't have to wait for the wall clock.
func insertLog(t *testing.T, d *sql.DB, username, method, path string, protectedFlag bool, createdAt time.Time) int64 {
	t.Helper()
	p := 0
	if protectedFlag {
		p = 1
	}
	res, err := d.Exec(
		`INSERT INTO audit_logs (username, method, path, status, ip, node_id, protected, created_at)
		 VALUES (?, ?, ?, 200, '127.0.0.1', '', ?, ?)`,
		username, method, path, p, createdAt.UTC().Format("2006-01-02 15:04:05"),
	)
	require.NoError(t, err)
	id, _ := res.LastInsertId()
	return id
}

func newClearRequest(t *testing.T, query string, username string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	url := "/api/v1/audit/logs"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	if username != "" {
		c.Set("username", username)
	}
	return c, rec
}

// TestClearAuditLogs_WritesTombstoneAndDeletesUnprotected verifies the core
// guarantee: clear-all wipes ordinary rows but leaves a protected tombstone
// behind that captures who did it.
func TestClearAuditLogs_WritesTombstoneAndDeletesUnprotected(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}
	now := time.Now()
	insertLog(t, d, "alice", "POST", "/api/v1/foo", false, now.Add(-1*time.Hour))
	insertLog(t, d, "alice", "DELETE", "/api/v1/bar", false, now.Add(-30*time.Minute))

	c, rec := newClearRequest(t, "", "admin")
	require.NoError(t, h.ClearAuditLogs(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// Only the tombstone should remain.
	var count int
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&count))
	require.Equal(t, 1, count, "exactly one row (tombstone) should remain")

	var username, path string
	var protectedInt int
	require.NoError(t, d.QueryRow("SELECT username, path, protected FROM audit_logs LIMIT 1").Scan(&username, &path, &protectedInt))
	require.Equal(t, "admin", username)
	require.Equal(t, 1, protectedInt, "tombstone must be protected")
	require.Contains(t, path, actionAuditLogCleared)
	require.Contains(t, path, "deleted=2", "tombstone path should record target count")

	// Response body shape.
	var body struct {
		Success bool                   `json:"success"`
		Data    ClearAuditLogsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, int64(2), body.Data.Deleted)
	require.Greater(t, body.Data.TombstoneID, int64(0))
}

// TestClearAuditLogs_SecondClearPreservesFirstTombstone is the "attacker
// re-wipes" scenario — the second clear must not erase the first wipe's
// tombstone. Without the protected column this is exactly the gap.
func TestClearAuditLogs_SecondClearPreservesFirstTombstone(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}

	// First clear (no rows to clear; just creates tombstone #1).
	c1, _ := newClearRequest(t, "", "attacker")
	require.NoError(t, h.ClearAuditLogs(c1))

	// Add an ordinary row and clear again.
	insertLog(t, d, "victim", "POST", "/api/v1/passwd", false, time.Now())
	c2, _ := newClearRequest(t, "", "attacker")
	require.NoError(t, h.ClearAuditLogs(c2))

	// Both tombstones survive; the victim row is gone.
	rows, err := d.Query("SELECT username, protected FROM audit_logs ORDER BY id ASC")
	require.NoError(t, err)
	defer rows.Close()
	var seen []struct {
		User  string
		Prot  int
	}
	for rows.Next() {
		var u string
		var p int
		require.NoError(t, rows.Scan(&u, &p))
		seen = append(seen, struct {
			User string
			Prot int
		}{u, p})
	}
	require.Len(t, seen, 2, "two tombstones must remain after two clears")
	for _, r := range seen {
		require.Equal(t, "attacker", r.User)
		require.Equal(t, 1, r.Prot)
	}
}

// TestClearAuditLogs_DaysScope verifies `?days=N` only deletes rows older
// than the cutoff and leaves newer ones alone.
func TestClearAuditLogs_DaysScope(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}
	now := time.Now()
	oldID := insertLog(t, d, "alice", "POST", "/old", false, now.Add(-60*24*time.Hour))
	insertLog(t, d, "alice", "POST", "/recent", false, now.Add(-1*time.Hour))

	c, rec := newClearRequest(t, "days=30", "admin")
	require.NoError(t, h.ClearAuditLogs(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// /old (60d) is gone, /recent (1h) survives, tombstone is added.
	var oldCount, recentCount, tombstoneCount int
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE id = ?", oldID).Scan(&oldCount))
	require.Equal(t, 0, oldCount, "60-day-old row should be deleted")
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE path = '/recent'").Scan(&recentCount))
	require.Equal(t, 1, recentCount, "1h-old row must survive a 30-day cutoff")
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE protected = 1").Scan(&tombstoneCount))
	require.Equal(t, 1, tombstoneCount)
}

// TestClearAuditLogs_BeforeScope verifies `?before=ISO8601` semantics.
func TestClearAuditLogs_BeforeScope(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}
	cutoff := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	insertLog(t, d, "alice", "POST", "/march", false, cutoff.Add(-10*24*time.Hour))
	insertLog(t, d, "alice", "POST", "/may", false, cutoff.Add(30*24*time.Hour))

	c, rec := newClearRequest(t, "before=2026-04-01", "admin")
	require.NoError(t, h.ClearAuditLogs(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var marchCount, mayCount int
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE path = '/march'").Scan(&marchCount))
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE path = '/may'").Scan(&mayCount))
	require.Equal(t, 0, marchCount, "March row should be deleted (before cutoff)")
	require.Equal(t, 1, mayCount, "May row must survive (after cutoff)")
}

// TestClearAuditLogs_ProtectedRowsNeverDeleted is the strongest invariant:
// even a "delete every unprotected row" call must leave any pre-existing
// protected row alone.
func TestClearAuditLogs_ProtectedRowsNeverDeleted(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}
	// Seed a protected row in the past (simulating a much older tombstone).
	oldTombstone := insertLog(t, d, "earlier_admin", "DELETE", "/api/v1/audit/logs#audit_log_cleared:all:deleted=0",
		true, time.Now().Add(-365*24*time.Hour))

	// Add normal rows and clear-all.
	insertLog(t, d, "alice", "POST", "/foo", false, time.Now())
	c, _ := newClearRequest(t, "", "admin")
	require.NoError(t, h.ClearAuditLogs(c))

	// Old tombstone is still there.
	var stillThere int
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE id = ?", oldTombstone).Scan(&stillThere))
	require.Equal(t, 1, stillThere, "pre-existing protected row must survive clear-all")

	// And a scoped clear over the whole history likewise leaves it alone.
	c2, _ := newClearRequest(t, "days=1", "admin")
	require.NoError(t, h.ClearAuditLogs(c2))
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE id = ?", oldTombstone).Scan(&stillThere))
	require.Equal(t, 1, stillThere, "pre-existing protected row must survive scoped clear too")
}

// TestClearAuditLogs_InvalidParams covers the 400 branches.
func TestClearAuditLogs_InvalidParams(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}

	cases := []struct {
		name  string
		query string
	}{
		{"both days and before", "days=30&before=2026-01-01"},
		{"days zero", "days=0"},
		{"days negative", "days=-5"},
		{"days non-numeric", "days=abc"},
		{"before garbage", "before=not-a-date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newClearRequest(t, tc.query, "admin")
			require.NoError(t, h.ClearAuditLogs(c))
			require.Equal(t, http.StatusBadRequest, rec.Code, "want 400 for %s", tc.query)
		})
	}

	// And nothing was inserted on the bad-request path (no spurious tombstone).
	var n int
	require.NoError(t, d.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&n))
	require.Equal(t, 0, n, "invalid params must not produce a tombstone")
}

// TestListAuditLogs_IncludesProtectedField confirms the SELECT path exposes
// the new column so UI can render a badge.
func TestListAuditLogs_IncludesProtectedField(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}
	insertLog(t, d, "alice", "POST", "/foo", false, time.Now())
	insertLog(t, d, "admin", "DELETE", "/api/v1/audit/logs#audit_log_cleared:all:deleted=0", true, time.Now())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs", nil)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	require.NoError(t, h.ListAuditLogs(c))

	var body struct {
		Success bool              `json:"success"`
		Data    AuditLogsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, 2, body.Data.Total)
	require.Len(t, body.Data.Logs, 2)

	var sawProtected, sawUnprotected bool
	for _, l := range body.Data.Logs {
		if l.Protected {
			sawProtected = true
		} else {
			sawUnprotected = true
		}
	}
	require.True(t, sawProtected, "expected one protected row in response")
	require.True(t, sawUnprotected, "expected one unprotected row in response")
}

// Sanity that the path marker is well-formed for an investigator grep.
func TestTombstonePathFormat(t *testing.T) {
	d := openTestDB(t)
	h := &Handler{DB: d}
	insertLog(t, d, "alice", "POST", "/x", false, time.Now())
	c, _ := newClearRequest(t, "days=1", "admin")
	require.NoError(t, h.ClearAuditLogs(c))

	var path string
	require.NoError(t, d.QueryRow("SELECT path FROM audit_logs WHERE protected = 1").Scan(&path))
	require.Contains(t, path, fmt.Sprintf("#%s:scoped:", actionAuditLogCleared))
}
