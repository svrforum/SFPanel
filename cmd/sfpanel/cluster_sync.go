package main

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// leaderPollInterval is how often syncBootstrapState rechecks leadership while
// waiting out its leaderWait deadline.
const leaderPollInterval = 200 * time.Millisecond

// syncBootstrapState polls until this node is leader (up to leaderWait), then
// pushes the local admin account and JWT secret into the Raft FSM. Leadership
// is required because both are Raft Applies. Best-effort: it logs and skips on
// any sub-failure (the post-restart boot sync remains a backstop) and returns
// without error in the normal "synced" or "nothing to sync" cases.
//
// Shared by two callers: the boot-time goroutine in main.go (run async so boot
// doesn't block) and the CLI `cluster init` direct-mode path in
// cluster_commands.go (run synchronously before mgr.Shutdown(), so the FSM
// holds the admin+JWT state before the next systemd restart rather than racing
// the boot goroutine).
func syncBootstrapState(mgr *cluster.Manager, database *sql.DB, jwtSecret string, leaderWait time.Duration) {
	deadline := time.Now().Add(leaderWait)
	for time.Now().Before(deadline) {
		if mgr.IsLeader() {
			break
		}
		time.Sleep(leaderPollInterval)
	}
	if !mgr.IsLeader() {
		slog.Debug("cluster sync skipped: not leader", "wait", leaderWait)
		return
	}
	var username, passwordHash string
	var totpSecret sql.NullString
	if err := database.QueryRow("SELECT username, password, totp_secret FROM admin LIMIT 1").Scan(&username, &passwordHash, &totpSecret); err == nil {
		totp := ""
		if totpSecret.Valid {
			totp = totpSecret.String
		}
		if syncErr := mgr.SyncAccountFromDB(username, passwordHash, totp); syncErr != nil {
			slog.Debug("account cluster sync skipped", "error", syncErr)
		}
	}
	if jwtSecret != "" {
		if cErr := mgr.SetConfig("jwt_secret", jwtSecret); cErr != nil {
			slog.Debug("jwt_secret cluster sync skipped", "error", cErr)
		}
	}
}
