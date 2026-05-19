package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/common/safe"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

const auditMaxRows = 50000

// AuditMiddleware logs all state-changing API requests (POST, PUT, DELETE)
// to the audit_logs table after the handler has executed successfully.
//
// writer is the shared *db.AsyncWriter that drains audit rows on a single
// background goroutine. Each request used to spawn its own `go func()` for
// the INSERT, which under a burst (CI rerun, deploy, mass restart) created
// hundreds of goroutines competing for the (MaxOpenConns=1) writer
// connection. The bounded queue caps that fan-out and lets shutdown drain
// pending rows.
//
// localNodeIDFn returns the cluster node ID this row should be stamped with.
// Resolved per-request (not captured at boot) so that cluster
// initialisation-at-runtime takes effect — a node that started standalone
// and later joined a cluster starts emitting non-empty node_ids without a
// restart. Pass nil or a function returning "" for non-cluster deployments;
// the column then stays empty as before.
func AuditMiddleware(writer *sfdb.AsyncWriter, localNodeIDFn func() string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)

			method := c.Request().Method
			if method == "GET" || method == "HEAD" || method == "OPTIONS" {
				return err
			}

			path := c.Request().URL.Path
			// Skip auth endpoints. The login / setup endpoints handle bodies that
			// contain passwords; the post-auth security flows (change-password,
			// 2fa enable/verify/disable) write their own enriched audit rows via
			// recordSecurityEvent — without this skip the same request would
			// produce two rows (plain + reasoned) and dilute filterable audit data.
			if strings.HasPrefix(path, "/api/v1/auth/login") ||
				strings.HasPrefix(path, "/api/v1/auth/setup") ||
				strings.HasPrefix(path, "/api/v1/auth/change-password") ||
				strings.HasPrefix(path, "/api/v1/auth/2fa") {
				return err
			}

			status := c.Response().Status
			username, _ := c.Get("username").(string)
			ip := c.RealIP()
			// Stamp the row with the LOCAL node that processed the request.
			// The previous read from `c.QueryParam("node")` was always empty
			// because the cluster proxy middleware strips `?node=` before the
			// handler chain proceeds. Forensic reviewers were unable to tell
			// which cluster node served a given write.
			nodeID := ""
			if localNodeIDFn != nil {
				nodeID = localNodeIDFn()
			}

			writer.Submit(func(db *sql.DB) {
				if _, dbErr := db.Exec(
					"INSERT INTO audit_logs (username, method, path, status, ip, node_id) VALUES (?, ?, ?, ?, ?, ?)",
					username, method, path, status, ip, nodeID,
				); dbErr != nil {
					slog.Error("audit log write failed", "error", dbErr)
				}
			})

			return err
		}
	}
}

// StartAuditRetention runs a background goroutine that prunes audit_logs
// down to auditMaxRows every 5 minutes. The previous design piggybacked on
// inserts via a CAS — if the panel was idle for hours, nothing pruned and
// the table grew unbounded after a bursty period.
//
// Returns when ctx is cancelled. Caller is expected to wire ctx to the
// shared bgCtx from main.go so retention stops cleanly on shutdown.
func StartAuditRetention(ctx context.Context, db *sql.DB) {
	safe.Go("audit-retention", func() {
		// Run once on startup so a host that was offline for a long time
		// catches up before waiting another 5 minutes.
		pruneAuditLogs(db)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneAuditLogs(db)
			}
		}
	})
}

func pruneAuditLogs(db *sql.DB) {
	// Only consider unprotected rows for the row-count cap. Protected rows
	// (audit_log_cleared tombstones, today) are excluded both from the cap
	// and from the deletion target, so a flood of normal traffic can't
	// silently push old clear-events past the retention window.
	if _, err := db.Exec(
		`DELETE FROM audit_logs
		   WHERE protected = 0
		     AND id IN (
		         SELECT id FROM audit_logs
		           WHERE protected = 0
		           ORDER BY id DESC
		           LIMIT -1 OFFSET ?
		     )`,
		auditMaxRows,
	); err != nil {
		slog.Warn("audit log retention prune failed", "error", err)
	}
}
