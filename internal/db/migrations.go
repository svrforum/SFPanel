package db

import (
	"database/sql"
	"regexp"
	"strings"
)

// alterAddColumnRe extracts "<table>" and "<column>" from statements of the
// shape `ALTER TABLE <table> ADD COLUMN <column> <type>...` so the runner can
// skip them idempotently by checking the live schema instead of matching
// driver-specific error messages.
var alterAddColumnRe = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+(\w+)\b`)

// columnExists uses SQLite's pragma_table_info so migration idempotency
// doesn't depend on the driver's specific error wording.
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

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS admin (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT NOT NULL UNIQUE,
		password   TEXT NOT NULL,
		totp_secret TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS compose_projects (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL UNIQUE,
		yaml_path  TEXT NOT NULL,
		status     TEXT DEFAULT 'stopped',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash TEXT NOT NULL UNIQUE,
		expires_at DATETIME NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS custom_log_sources (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		source_id  TEXT NOT NULL UNIQUE,
		name       TEXT NOT NULL,
		path       TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS metrics_history (
		time        INTEGER PRIMARY KEY,
		cpu         REAL NOT NULL,
		mem_percent REAL NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS audit_logs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT NOT NULL DEFAULT '',
		method     TEXT NOT NULL,
		path       TEXT NOT NULL,
		status     INTEGER NOT NULL DEFAULT 0,
		ip         TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at)`,
	// Phase 5: add node_id column for cluster-aware audit logging
	`ALTER TABLE audit_logs ADD COLUMN node_id TEXT NOT NULL DEFAULT ''`,
	// Alert system tables
	`CREATE TABLE IF NOT EXISTS alert_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		config TEXT NOT NULL,
		enabled INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS alert_rules (
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
	)`,
	`CREATE TABLE IF NOT EXISTS alert_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		rule_id INTEGER,
		rule_name TEXT,
		type TEXT,
		severity TEXT,
		message TEXT,
		node_id TEXT DEFAULT '',
		sent_channels TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_alert_history_created_at ON alert_history(created_at)`,
}

func RunMigrations(db *sql.DB) error {
	for _, m := range migrations {
		// Idempotent ALTER TABLE ADD COLUMN: skip if the column already
		// exists. Previously we matched on the driver's error string
		// ("duplicate column"), which would break silently whenever the
		// SQLite driver (or a future replacement) reworded its errors.
		if match := alterAddColumnRe.FindStringSubmatch(m); match != nil {
			exists, err := columnExists(db, match[1], match[2])
			if err != nil {
				return err
			}
			if exists {
				continue
			}
		}
		if _, err := db.Exec(m); err != nil {
			// Belt-and-suspenders: keep the legacy duplicate-column error
			// fallback so a driver that still raises the old message won't
			// fail the boot even if the pragma check above somehow missed.
			if strings.Contains(m, "ALTER TABLE") && strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}
