package db

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// migration is one schema change. ID must be stable forever — once a migration
// has shipped, never renumber. The runner records each applied ID in the
// schema_migrations table so re-runs skip already-applied work, which is what
// lets us drop CREATE TABLE IF NOT EXISTS noise from new migrations and trust
// the schema_version log instead.
type migration struct {
	ID  int    // monotonic; never reused
	Up  string // single SQL statement; multi-statement migrations split into separate entries
}

// migrations is append-only. Adding a row at the end of this slice is the only
// supported way to change the schema. Editing or reordering existing entries
// will skip them on hosts that already ran them — DO NOT do that.
var migrations = []migration{
	{ID: 1, Up: `CREATE TABLE IF NOT EXISTS admin (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT NOT NULL UNIQUE,
		password   TEXT NOT NULL,
		totp_secret TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	{ID: 2, Up: `CREATE TABLE IF NOT EXISTS compose_projects (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL UNIQUE,
		yaml_path  TEXT NOT NULL,
		status     TEXT DEFAULT 'stopped',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	// Migration 3 (sessions table) was unused — never referenced outside DDL.
	// Kept registered so existing hosts skip it via schema_migrations and a
	// re-creation isn't attempted; the table itself is left intact on hosts
	// that have it (drop manually if disk space matters).
	{ID: 3, Up: `CREATE TABLE IF NOT EXISTS sessions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash TEXT NOT NULL UNIQUE,
		expires_at DATETIME NOT NULL
	)`},
	{ID: 4, Up: `CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`},
	{ID: 5, Up: `CREATE TABLE IF NOT EXISTS custom_log_sources (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		source_id  TEXT NOT NULL UNIQUE,
		name       TEXT NOT NULL,
		path       TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	{ID: 6, Up: `CREATE TABLE IF NOT EXISTS metrics_history (
		time        INTEGER PRIMARY KEY,
		cpu         REAL NOT NULL,
		mem_percent REAL NOT NULL
	)`},
	{ID: 7, Up: `CREATE TABLE IF NOT EXISTS audit_logs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT NOT NULL DEFAULT '',
		method     TEXT NOT NULL,
		path       TEXT NOT NULL,
		status     INTEGER NOT NULL DEFAULT 0,
		ip         TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	{ID: 8, Up: `CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at)`},
	// Migration 9 was originally a bare ALTER TABLE ADD COLUMN. The runner
	// guards it via schema_migrations now, so a host that ran the old
	// pre-versioned code just gets the row recorded on first boot under
	// the new system (the column already exists; CREATE-style statements
	// with IF NOT EXISTS handle that, and ALTERs that have already run
	// also skip via columnExists).
	{ID: 9, Up: `ALTER TABLE audit_logs ADD COLUMN node_id TEXT NOT NULL DEFAULT ''`},
	{ID: 10, Up: `CREATE TABLE IF NOT EXISTS alert_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		config TEXT NOT NULL,
		enabled INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	{ID: 11, Up: `CREATE TABLE IF NOT EXISTS alert_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		condition TEXT NOT NULL,
		channel_ids TEXT NOT NULL DEFAULT '[]',
		severity TEXT DEFAULT 'warning',
		cooldown INTEGER DEFAULT 300,
		node_scope TEXT DEFAULT 'all',
		node_ids TEXT DEFAULT '[]',
		enabled INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	{ID: 12, Up: `CREATE TABLE IF NOT EXISTS alert_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		rule_id INTEGER,
		rule_name TEXT,
		type TEXT,
		severity TEXT,
		message TEXT,
		node_id TEXT DEFAULT '',
		sent_channels TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`},
	{ID: 13, Up: `CREATE INDEX IF NOT EXISTS idx_alert_history_created_at ON alert_history(created_at)`},
}

// RunMigrations applies every registered migration that hasn't already been
// recorded in the schema_migrations table. Each migration runs inside its own
// transaction; a partial-apply failure rolls back and the boot aborts.
//
// SQLite serialises DDL within a connection, and Open() pins MaxOpenConns=1,
// so two concurrent in-process callers can't race here. Two separate
// processes opening the same DB rely on SQLite's WAL locking — the second
// process will block on the first's transaction.
func RunMigrations(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id         INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("load applied migrations: %w", err)
	}

	// On first boot under the new system, mark every CREATE-IF-NOT-EXISTS
	// migration whose object already lives in the schema as "applied" so we
	// don't re-attempt them. This bridges hosts that ran the pre-versioned
	// migration runner.
	if err := backfillAppliedFromSchema(db, applied); err != nil {
		return fmt.Errorf("backfill applied: %w", err)
	}

	for _, m := range migrations {
		if applied[m.ID] {
			continue
		}
		if err := applyOne(db, m); err != nil {
			return fmt.Errorf("migration %d: %w", m.ID, err)
		}
		applied[m.ID] = true
		slog.Info("schema migration applied", "id", m.ID)
	}
	return nil
}

func loadAppliedMigrations(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT id FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// backfillAppliedFromSchema marks every CREATE-style migration whose target
// object already exists as applied. Run once per boot before the main loop;
// the cost is tiny (≤ 13 quick PRAGMA queries today). This keeps the upgrade
// from a pre-versioned host idempotent on first run.
func backfillAppliedFromSchema(db *sql.DB, applied map[int]bool) error {
	for _, m := range migrations {
		if applied[m.ID] {
			continue
		}
		obj, kind, ok := extractCreateTarget(m.Up)
		if !ok {
			continue
		}
		exists, err := schemaObjectExists(db, kind, obj)
		if err != nil {
			return err
		}
		if exists {
			if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (id) VALUES (?)`, m.ID); err != nil {
				return err
			}
			applied[m.ID] = true
		}
	}
	// ALTER TABLE ADD COLUMN bridge: same idea but matches at column level.
	for _, m := range migrations {
		if applied[m.ID] {
			continue
		}
		table, column, ok := extractAlterAddColumn(m.Up)
		if !ok {
			continue
		}
		exists, err := columnExists(db, table, column)
		if err != nil {
			return err
		}
		if exists {
			if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (id) VALUES (?)`, m.ID); err != nil {
				return err
			}
			applied[m.ID] = true
		}
	}
	return nil
}

// applyOne runs a single migration inside a transaction. SQLite supports DDL
// inside BEGIN..COMMIT, so a syntax error or constraint violation rolls back
// the schema change cleanly instead of leaving the DB half-migrated.
func applyOne(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if _, err := tx.Exec(m.Up); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (id) VALUES (?)`, m.ID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record applied: %w", err)
	}
	return tx.Commit()
}

// columnExists is unchanged from the pre-versioned runner — kept because the
// backfill bridge still needs it for ALTER ADD COLUMN migrations.
func columnExists(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// schemaObjectExists checks sqlite_master for a CREATE TABLE / CREATE INDEX
// target. kind ∈ {"table","index"}.
func schemaObjectExists(db *sql.DB, kind, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`,
		kind, name,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
