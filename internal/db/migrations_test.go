package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openTempDB returns a fresh on-disk SQLite (in-memory uses one connection
// per Open which complicates schema visibility checks).
func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRunMigrations_Idempotent(t *testing.T) {
	db := openTempDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Re-running must be a no-op — every migration's ID is in
	// schema_migrations and the runner's loop short-circuits.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("second run: %v", err)
	}

	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	for _, m := range migrations {
		if !applied[m.ID] {
			t.Errorf("migration %d not recorded as applied", m.ID)
		}
	}
}

// TestRunMigrations_BridgesPreVersionedHosts simulates a host that ran the
// old migration runner: every CREATE-style migration's table/index already
// exists, but schema_migrations doesn't. RunMigrations must record those as
// applied without re-attempting them (which would fail on duplicate-column
// for the bare ALTER, etc.).
func TestRunMigrations_BridgesPreVersionedHosts(t *testing.T) {
	db := openTempDB(t)

	// Simulate the pre-versioned schema by running each migration body
	// directly without the schema_migrations bookkeeping.
	for _, m := range migrations {
		// Skip the ALTER unless the table already exists (it requires
		// audit_logs to be present). For this fixture the migration order
		// puts CREATE audit_logs at ID=7 ahead of the ALTER at ID=9, so
		// raw replay works.
		if _, err := db.Exec(m.Up); err != nil {
			t.Fatalf("seed migration %d: %v", m.ID, err)
		}
	}

	// Now RunMigrations is called without any schema_migrations records.
	// The backfill bridge should detect each migration's target object
	// already exists and record it without re-running the body.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("bridge run: %v", err)
	}

	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	for _, m := range migrations {
		if !applied[m.ID] {
			t.Errorf("bridge: migration %d not recorded", m.ID)
		}
	}
}

// TestRunMigrations_PartialFailureRollsBack verifies a failing migration
// rolls back cleanly: previously-applied migrations stay, the failing one
// leaves no schema artefacts, and schema_migrations doesn't record it.
func TestRunMigrations_PartialFailureRollsBack(t *testing.T) {
	db := openTempDB(t)

	// Run the real migrations first so schema_migrations is populated.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Inject a deliberately-broken migration with an unused fake ID.
	bad := migration{
		ID: 9999,
		Up: `CREATE TABLE willfail (
			id INTEGER PRIMARY KEY,
			-- syntax error: missing comma forces SQLite to abort
			name TEXT NOT NULL DEFAULT "x" extra_token
		)`,
	}
	if err := applyOne(db, bad); err == nil {
		t.Fatal("expected applyOne to fail on syntax error")
	}

	// schema_migrations must NOT have 9999.
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	if applied[9999] {
		t.Errorf("failing migration was recorded as applied")
	}

	// And willfail must not exist (rollback worked).
	exists, err := schemaObjectExists(db, "table", "willfail")
	if err != nil {
		t.Fatalf("schemaObjectExists: %v", err)
	}
	if exists {
		t.Errorf("willfail table leaked despite rollback")
	}
}

func TestExtractCreateTarget(t *testing.T) {
	cases := []struct {
		stmt    string
		name    string
		kind    string
		matched bool
	}{
		{"CREATE TABLE foo (id INTEGER)", "foo", "table", true},
		{"CREATE TABLE IF NOT EXISTS bar (id INTEGER)", "bar", "table", true},
		{"  create table\n  baz (id INTEGER)", "baz", "table", true},
		{"CREATE INDEX idx_x ON foo(id)", "idx_x", "index", true},
		{"CREATE UNIQUE INDEX IF NOT EXISTS u_x ON foo(id)", "u_x", "index", true},
		{"ALTER TABLE foo ADD COLUMN x TEXT", "", "", false},
		{"-- comment", "", "", false},
	}
	for _, c := range cases {
		name, kind, ok := extractCreateTarget(c.stmt)
		if ok != c.matched || name != c.name || kind != c.kind {
			t.Errorf("extractCreateTarget(%q) = (%q,%q,%v); want (%q,%q,%v)",
				c.stmt, name, kind, ok, c.name, c.kind, c.matched)
		}
	}
}

func TestExtractAlterAddColumn(t *testing.T) {
	cases := []struct {
		stmt   string
		table  string
		column string
		ok     bool
	}{
		{"ALTER TABLE audit_logs ADD COLUMN node_id TEXT NOT NULL DEFAULT ''", "audit_logs", "node_id", true},
		{"alter table foo add column bar INTEGER", "foo", "bar", true},
		{"CREATE TABLE foo (id INTEGER)", "", "", false},
	}
	for _, c := range cases {
		table, col, ok := extractAlterAddColumn(c.stmt)
		if ok != c.ok || table != c.table || col != c.column {
			t.Errorf("extractAlterAddColumn(%q) = (%q,%q,%v); want (%q,%q,%v)",
				c.stmt, table, col, ok, c.table, c.column, c.ok)
		}
	}
}
