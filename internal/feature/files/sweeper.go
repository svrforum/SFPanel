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

// thumbCacheMaxAge is how long an unused thumbnail survives. The cache keys on
// modification time, so an edited file orphans its old entry rather than
// replacing it — without a sweep the directory only ever grows.
const thumbCacheMaxAge = 30 * 24 * time.Hour

// StartTrashSweeper clears trash past its retention window, and unused
// thumbnails alongside it.
//
// Without it the trash is not a safety net but a slow leak: every delete the
// operator ever made, held forever on the filesystem they were trying to clear.
// Bound to the caller's context so a SIGTERM stops it.
func StartTrashSweeper(ctx context.Context) {
	sweep := func() {
		if removed, err := SweepTrash(); err != nil {
			slog.Warn("trash sweep failed", "component", "files", "error", err)
		} else if removed > 0 {
			slog.Info("cleared expired trash", "component", "files", "entries", removed)
		}
		if removed, err := SweepThumbnails(thumbCacheMaxAge); err != nil {
			slog.Warn("thumbnail sweep failed", "component", "files", "error", err)
		} else if removed > 0 {
			slog.Info("cleared unused thumbnails", "component", "files", "entries", removed)
		}
	}

	safe.Go("files-trash-sweeper", func() {
		// Sweep once at boot as well: a panel that is restarted more often
		// than the interval would otherwise never reach a tick.
		sweep()

		ticker := time.NewTicker(trashSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	})
}
