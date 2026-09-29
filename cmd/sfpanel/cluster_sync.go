package main

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// leaderPollInterval is how often syncBootstrapState rechecks leadership while
// waiting out its leaderWait deadline.
const leaderPollInterval = 200 * time.Millisecond

// syncBootstrapState polls until this node is leader (up to leaderWait), then
// seeds the Raft FSM with the local admin account and JWT secret — but only
// the ones the FSM does not hold yet. Leadership is required because both are
// Raft Applies. Best-effort: it logs and skips on any sub-failure (the
// post-restart boot sync remains a backstop) and returns without error in the
// normal "synced" or "nothing to sync" cases.
//
// Seed, never overwrite: once clustered, password and 2FA changes go to the
// FSM alone, so this node's local admin row is whatever it was at join time.
// Pushing it unconditionally let any node that won an election inside the
// boot window revert the cluster password, switch 2FA off on every node, or
// add its own local admin as a second cluster account.
//
// Shared by two callers: the boot-time goroutine in main.go (run async so boot
// doesn't block) and the CLI `cluster init` direct-mode path in
// cluster_commands.go (run synchronously before mgr.Shutdown(), so the FSM
// holds the admin+JWT state before the next systemd restart rather than racing
// the boot goroutine).
//
// ctx cancels the leadership wait: the boot goroutine passes the long-lived
// background context so a SIGTERM during the up-to-leaderWait poll stops it
// promptly instead of outliving the DB it reads. The CLI passes
// context.Background() (short-lived, no shutdown signal); the leaderWait
// deadline still caps it.
func syncBootstrapState(ctx context.Context, mgr *cluster.Manager, database *sql.DB, jwtSecret string, leaderWait time.Duration) {
	ticker := time.NewTicker(leaderPollInterval)
	defer ticker.Stop()
	deadline := time.Now().Add(leaderWait)
	for !mgr.IsLeader() {
		select {
		case <-ctx.Done():
			slog.Debug("cluster sync skipped: shutdown before leadership")
			return
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			slog.Debug("cluster sync skipped: not leader", "wait", leaderWait)
			return
		}
	}
	clusterSecret, clusterAdmin, _, _ := mgr.GetJWTAndAdminFull()
	if clusterAdmin != "" {
		slog.Debug("account cluster sync skipped: cluster already holds an account")
	} else if _, err := mgr.SeedAdminFromDB(database); err != nil {
		slog.Debug("account cluster sync skipped", "error", err)
	}
	if jwtSecret != "" && clusterSecret == "" {
		if cErr := mgr.SetConfig("jwt_secret", jwtSecret); cErr != nil {
			slog.Debug("jwt_secret cluster sync skipped", "error", cErr)
		}
	}
	// Migration path for existing clusters: replicate the leader's on-disk CA
	// into the FSM so a future non-founder leader can still sign joins. No-op
	// once seeded, or if this leader has no CA key on disk. See SeedClusterCA.
	if seedErr := mgr.SeedClusterCA(); seedErr != nil {
		slog.Debug("cluster CA seed skipped", "error", seedErr)
	}
}
