package db

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	// modernc.org/sqlite uses _pragma=name(value) format for DSN pragmas
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}

	// Verify WAL mode is active
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		return nil, fmt.Errorf("failed to verify journal_mode: %w", err)
	}
	log.Printf("SQLite journal_mode: %s", journalMode)
	if journalMode != "wal" {
		log.Printf("WARNING: expected journal_mode=wal but got %s, attempting explicit PRAGMA", journalMode)
		if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
			return nil, fmt.Errorf("failed to set WAL mode: %w", err)
		}
	}

	if err := RunMigrations(db); err != nil {
		return nil, err
	}
	return db, nil
}
