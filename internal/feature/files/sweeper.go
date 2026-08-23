package files

import (
	"context"
	"log/slog"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
)

// trashSweepInterval is how often expired trash is cleared. Hourly is far more
// often than the seven-day retention needs; the point is that a panel left
// running for months does not accumulate a backlog that all expires at once.
const trashSweepInterval = time.Hour

// StartTrashSweeper clears trash past its retention window.
//
// Without it the trash is not a safety net but a slow leak: every delete the
// operator ever made, held forever on the filesystem they were trying to clear.
// Bound to the caller's context so a SIGTERM stops it.
func StartTrashSweeper(ctx context.Context) {
	safe.Go("files-trash-sweeper", func() {
		// Sweep once at boot as well: a panel that is restarted more often
		// than the interval would otherwise never reach a tick.
		if removed, err := SweepTrash(); err != nil {
			slog.Warn("trash sweep failed", "component", "files", "error", err)
		} else if removed > 0 {
			slog.Info("cleared expired trash", "component", "files", "entries", removed)
		}

		ticker := time.NewTicker(trashSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				removed, err := SweepTrash()
				if err != nil {
					slog.Warn("trash sweep failed", "component", "files", "error", err)
					continue
				}
				if removed > 0 {
					slog.Info("cleared expired trash", "component", "files", "entries", removed)
				}
			}
		}
	})
}
