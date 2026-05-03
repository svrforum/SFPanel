package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// watchdogUpdate is invoked as a detached subprocess by RunUpdate after the
// binary swap. It polls an in-cluster health endpoint to confirm the new
// binary actually started; if not, it restores the backup and triggers a
// systemctl restart.
//
// Args (positional):
//
//	<bak_path>       — old binary preserved at /usr/local/bin/sfpanel.bak
//	<binary_path>    — live install path (/usr/local/bin/sfpanel)
//	<check_url>      — http://127.0.0.1:<port>/api/v1/system/info
//	<grace_seconds>  — how long to wait before declaring failure
//	[db_bak_path]    — optional: SQLite .bak to restore alongside the binary
//	[db_path]        — optional: live SQLite path the .bak should land at
//
// The watchdog runs from the BACKUP binary (the parent's caller passes
// bak_path as argv[0]) so the systemctl restart that happens just after
// spawning it doesn't kill the watchdog itself. After the grace period the
// watchdog exits regardless; success or failure is logged to stderr where
// systemd's journal will pick it up if the parent's run script captured it.
//
// Forward-only: pre-watchdog binaries don't know this subcommand, so updates
// FROM such a binary still take the old "no rollback" path. Updates TO this
// binary and beyond all benefit.
//
// DB rollback rationale: a new binary that crashed *during migrations* may
// have committed schema changes that the old binary doesn't expect. If we
// roll back the binary without the DB, the old binary boots against an
// alien schema. Restoring the DB .bak in the same atomic-swap step keeps
// the binary and schema in sync.
func watchdogUpdate(args []string) {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "watchdog-update requires <bak_path> <binary_path> <check_url> <grace_seconds> [db_bak_path] [db_path]")
		os.Exit(2)
	}
	bakPath := args[0]
	binaryPath := args[1]
	checkURL := args[2]
	graceSec, err := strconv.Atoi(args[3])
	if err != nil || graceSec <= 0 {
		fmt.Fprintf(os.Stderr, "watchdog-update: invalid grace_seconds %q\n", args[3])
		os.Exit(2)
	}
	var dbBakPath, dbPath string
	if len(args) >= 6 {
		dbBakPath = args[4]
		dbPath = args[5]
	}

	// Give systemd a moment to bring the new binary up before the first poll —
	// the parent already sleeps 2s after the SSE event, but ExecStart can take
	// a few more seconds (DB migrations, embed extraction).
	time.Sleep(3 * time.Second)

	deadline := time.Now().Add(time.Duration(graceSec) * time.Second)
	client := &http.Client{Timeout: 5 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(checkURL)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				// 401 is fine — it means the panel is up, just demanding auth.
				// We don't have a token here, so any reachable response counts as alive.
				fmt.Fprintf(os.Stderr, "watchdog-update: panel healthy (HTTP %d), exiting clean\n", resp.StatusCode)
				return
			}
		}
		time.Sleep(3 * time.Second)
	}

	// New binary never responded — roll back binary first, then DB.
	// Order: stop service → swap binary → swap DB → start service. This
	// keeps the live process from observing a half-rolled-back state.
	fmt.Fprintln(os.Stderr, "watchdog-update: panel unreachable after grace period, rolling back")

	if err := exec.Command("systemctl", "stop", "sfpanel").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "watchdog-update: stop before rollback failed (continuing): %v\n", err)
	}

	if err := restoreFile(bakPath, binaryPath, 0755, "binary"); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(3)
	}
	if dbBakPath != "" && dbPath != "" {
		if _, statErr := os.Stat(dbBakPath); statErr == nil {
			if err := restoreFile(dbBakPath, dbPath, 0600, "database"); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				// Don't exit — binary rollback already happened, restarting on
				// the wrong DB is still better than not restarting at all.
			}
			// Also remove any stale -wal/-shm sidecar files from the failed
			// migration so SQLite doesn't try to replay them against the
			// old DB. WAL is committed; sidecars at this point are noise.
			os.Remove(dbPath + "-wal")
			os.Remove(dbPath + "-shm")
		}
	}

	if err := exec.Command("systemctl", "start", "sfpanel").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "watchdog-update: rollback restart failed: %v\n", err)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stderr, "watchdog-update: rollback complete, sfpanel restarted on previous binary + DB")
}

// restoreFile atomically swaps `bak` into `live` via a `.rollback` temp file
// + rename. mode is the desired mode of the live file (0755 for binary,
// 0600 for the DB). label is a human-readable noun for error messages.
func restoreFile(bak, live string, mode os.FileMode, label string) error {
	data, err := os.ReadFile(bak)
	if err != nil {
		return fmt.Errorf("watchdog-update: cannot read %s backup %s: %v", label, bak, err)
	}
	tmp := live + ".rollback"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("watchdog-update: cannot write %s rollback temp: %v", label, err)
	}
	if err := os.Rename(tmp, live); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("watchdog-update: cannot rename %s rollback: %v", label, err)
	}
	return nil
}
