package db

import "database/sql"

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS admin (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT NOT NULL UNIQUE,
		password   TEXT NOT NULL,
		totp_secret TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS sites (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		domain      TEXT NOT NULL UNIQUE,
		doc_root    TEXT NOT NULL,
		php_enabled BOOLEAN DEFAULT 0,
		ssl_enabled BOOLEAN DEFAULT 0,
		ssl_expiry  DATETIME,
		status      TEXT DEFAULT 'active',
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
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
}

func RunMigrations(db *sql.DB) error {
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return err
		}
	}
	return nil
}
