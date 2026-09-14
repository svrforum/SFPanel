package ai

import (
	"database/sql"
	"path/filepath"
	"testing"

	sfdb "github.com/svrforum/SFPanel/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sfdb.Open(filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStore_RoundTripInCreationOrder(t *testing.T) {
	db := openTestDB(t)
	for _, id := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"} {
		if err := insertSession(db, sessionRow{ID: id, Tool: ToolClaude, Title: "Claude · app", RunAs: "root", CWD: "/opt/stacks/app"}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := listSessionRows(db)
	if err != nil || len(rows) != 2 || rows[0].ID != "aaaaaaaaaaaa" || rows[1].ID != "bbbbbbbbbbbb" {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
	if rows[0].CreatedAt == "" || rows[0].EndedAt.Valid || rows[0].LastAttachedAt.Valid {
		t.Errorf("fresh row: %+v", rows[0])
	}
	got, ok, err := getSessionRow(db, "bbbbbbbbbbbb")
	if err != nil || !ok || got.CWD != "/opt/stacks/app" {
		t.Errorf("get: %+v %v %v", got, ok, err)
	}
	// Not-found is ok=false *with a nil error*: a caller that sees ErrNoRows
	// here would answer 500 instead of 404, so assert the error too.
	if _, ok, err := getSessionRow(db, "cccccccccccc"); ok || err != nil {
		t.Errorf("unknown id must be a clean not-found, got ok=%v err=%v", ok, err)
	}
}

func TestStore_TitleEndedAttachedDelete(t *testing.T) {
	db := openTestDB(t)
	if err := insertSession(db, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolCodex, Title: "x", RunAs: "root", CWD: "/root"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := updateSessionTitle(db, "aaaaaaaaaaaa", "renamed"); err != nil || !ok {
		t.Fatalf("rename: %v %v", ok, err)
	}
	if ok, _ := updateSessionTitle(db, "zzzzzzzzzzzz", "x"); ok {
		t.Error("renaming an unknown id must report not found")
	}
	if err := setSessionEnded(db, "aaaaaaaaaaaa", true); err != nil {
		t.Fatal(err)
	}
	r, _, _ := getSessionRow(db, "aaaaaaaaaaaa")
	if r.Title != "renamed" || !r.EndedAt.Valid {
		t.Errorf("after rename+end: %+v", r)
	}
	// Backdate the stamp before re-stamping. CURRENT_TIMESTAMP has second
	// resolution, so two stamps in the same second are identical and this
	// assertion would hold even with the `AND ended_at IS NULL` clause gone;
	// a distinct prior value is what makes it prove the clause.
	if _, err := db.Exec(`UPDATE ai_sessions SET ended_at = '2026-01-01 00:00:00' WHERE id = ?`, "aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	r, _, _ = getSessionRow(db, "aaaaaaaaaaaa")
	first := r.EndedAt.String
	_ = setSessionEnded(db, "aaaaaaaaaaaa", true) // idempotent: the first stamp survives
	r, _, _ = getSessionRow(db, "aaaaaaaaaaaa")
	if r.EndedAt.String != first {
		t.Errorf("ended_at moved from %q to %q on a second stamp", first, r.EndedAt.String)
	}
	_ = setSessionEnded(db, "aaaaaaaaaaaa", false)
	_ = touchSessionAttached(db, "aaaaaaaaaaaa")
	r, _, _ = getSessionRow(db, "aaaaaaaaaaaa")
	if r.EndedAt.Valid || !r.LastAttachedAt.Valid {
		t.Errorf("after restart+attach: %+v", r)
	}
	if ok, err := deleteSessionRow(db, "aaaaaaaaaaaa"); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, _ := deleteSessionRow(db, "aaaaaaaaaaaa"); ok {
		t.Error("second delete must report not found")
	}
}

func TestStore_RecentDirsAreDistinctNewestFirst(t *testing.T) {
	db := openTestDB(t)
	for i, cwd := range []string{"/a", "/b", "/a", "/c"} {
		id := string(rune('a'+i)) + "aaaaaaaaaaa"
		if err := insertSession(db, sessionRow{ID: id, Tool: ToolShell, Title: id, RunAs: "root", CWD: cwd}); err != nil {
			t.Fatal(err)
		}
		// created_at has second resolution; order by id inside a second, so
		// give each row its own timestamp.
		if _, err := db.Exec(`UPDATE ai_sessions SET created_at = datetime('2026-09-14 00:00:00', ?) WHERE id = ?`, "+"+string(rune('0'+i))+" seconds", id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := recentSessionDirs(db, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "/c" || got[1] != "/a" || got[2] != "/b" {
		t.Errorf("recent = %v, want [/c /a /b]", got)
	}
}
