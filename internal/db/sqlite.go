package db

import (
	"database/sql"
	"fmt"
	"log/slog"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	// modernc.org/sqlite uses _pragma=name(value) format for DSN pragmas.
	//
	// temp_store=MEMORY routes temp tables / sort buffers / per-statement
	// scratch onto the heap instead of /var/tmp. The retention pruners
	// in particular generate noticeable temp-table activity each tick;
	// MEMORY backing removes the disk I/O without growing the resident
	// set materially because the temp regions are short-lived.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_pragma=synchronous(NORMAL)&_pragma=mmap_size(268435456)&_pragma=cache_size(-8000)&_pragma=temp_store(MEMORY)"
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
	slog.Info("SQLite journal mode", "mode", journalMode)
	if journalMode != "wal" {
		slog.Warn("unexpected journal_mode, attempting explicit PRAGMA", "expected", "wal", "got", journalMode)
		if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
			return nil, fmt.Errorf("failed to set WAL mode: %w", err)
		}
	}

	if err := RunMigrations(db); err != nil {
		return nil, err
	}
	return db, nil
}

// CheckpointWAL forces SQLite to merge the -wal sidecar back into the main
// database file (TRUNCATE mode also reclaims the wal). Called before
// snapshot operations like the pre-update DB backup so the .bak is a
// complete, restorable copy — without this, copying just sfpanel.db while
// uncommitted pages live in sfpanel.db-wal would yield a stale snapshot.
func CheckpointWAL(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("wal_checkpoint(TRUNCATE): %w", err)
	}
	return nil
}

// OptimizeOnShutdown runs `PRAGMA optimize` so SQLite can persist learned
// statistics about which indexes the just-finished session relied on. The
// next process boot reads those stats and skips warm-up cost on the first
// few queries against append-only hot tables (audit_logs, alert_history).
// Cheap on a tidy DB, no-op on a fresh one — recommended by upstream.
//
// Best-effort: a failure here doesn't block shutdown.
func OptimizeOnShutdown(db *sql.DB) {
	if _, err := db.Exec(`PRAGMA optimize`); err != nil {
		slog.Warn("PRAGMA optimize failed on shutdown", "component", "db", "error", err)
	}
}
