package db

import (
	"database/sql"
	"strings"
)

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
}

func RunMigrations(db *sql.DB) error {
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			// ALTER TABLE ADD COLUMN fails if column already exists — safe to ignore
			if strings.Contains(m, "ALTER TABLE") && strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}
