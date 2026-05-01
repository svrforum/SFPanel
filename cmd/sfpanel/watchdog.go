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
// Args (positional, all required):
//
//	<bak_path>       — old binary preserved at /usr/local/bin/sfpanel.bak
//	<binary_path>    — live install path (/usr/local/bin/sfpanel)
//	<check_url>      — http://127.0.0.1:<port>/api/v1/system/info
//	<grace_seconds>  — how long to wait before declaring failure
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
func watchdogUpdate(args []string) {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "watchdog-update requires <bak_path> <binary_path> <check_url> <grace_seconds>")
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

	// New binary never responded — roll back.
	fmt.Fprintln(os.Stderr, "watchdog-update: panel unreachable after grace period, rolling back")

	bakData, readErr := os.ReadFile(bakPath)
	if readErr != nil {
		fmt.Fprintf(os.Stderr, "watchdog-update: cannot read backup %s: %v\n", bakPath, readErr)
		os.Exit(3)
	}

	tmpPath := binaryPath + ".rollback"
	if err := os.WriteFile(tmpPath, bakData, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "watchdog-update: cannot write rollback temp: %v\n", err)
		os.Exit(3)
	}
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "watchdog-update: cannot rename rollback: %v\n", err)
		os.Exit(3)
	}

	if err := exec.Command("systemctl", "restart", "sfpanel").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "watchdog-update: rollback restart failed: %v\n", err)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stderr, "watchdog-update: rollback complete, sfpanel restarted on previous binary")
}
