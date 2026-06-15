package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/volume"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/safe"
	"github.com/svrforum/SFPanel/internal/docker"
)

const (
	volumeUsageInterval     = 5 * time.Minute
	volumeUsageInitialDelay = 30 * time.Second
	duPerVolumeTimeout      = 30 * time.Second
)

// VolumeListerFunc returns the current list of Docker volumes.
type VolumeListerFunc func() []*volume.Volume

// StartVolumeUsageCollector launches a 5-minute ticker goroutine that
// sequentially measures `du -sb` for every Docker volume and writes the
// result to docker_volume_usage. Stops cleanly on ctx cancellation.
func StartVolumeUsageCollector(ctx context.Context, db *sql.DB, dockerCli *docker.Client) {
	if dockerCli == nil {
		slog.Warn("volume usage collector: docker client nil; not starting")
		return
	}
	safe.Go("monitor-volume-usage", func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(volumeUsageInitialDelay):
		}
		cmd := exec.NewCommander()
		lister := func() []*volume.Volume {
			lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			vs, err := dockerCli.ListVolumes(lctx)
			if err != nil {
				slog.Warn("volume usage collector: list volumes failed", "error", err)
				return nil
			}
			return vs
		}
		measureVolumeUsageOnce(db, cmd, lister)
		ticker := time.NewTicker(volumeUsageInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				measureVolumeUsageOnce(db, cmd, lister)
			}
		}
	})
}

// measureVolumeUsageOnce performs one tick: enumerates volumes, runs `du -sb`
// sequentially per volume, writes result to cache.
func measureVolumeUsageOnce(db *sql.DB, cmd exec.Commander, lister VolumeListerFunc) {
	volumes := lister()
	now := time.Now().UnixMilli()
	for _, v := range volumes {
		// Non-local drivers (NFS plugins etc.) have no host-measurable data
		// dir; skip silently — a warn here would repeat every 5 minutes.
		if v.Driver != "" && v.Driver != "local" {
			continue
		}
		path := v.Mountpoint
		if path == "" {
			path = "/var/lib/docker/volumes/" + v.Name + "/_data"
		}
		out, err := cmd.RunWithTimeout(duPerVolumeTimeout, "du", "-sb", path)
		if err != nil {
			slog.Warn("volume usage: du failed", "volume", v.Name, "error", err)
			continue
		}
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) == 0 {
			continue
		}
		bytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			slog.Warn("volume usage: parse bytes failed", "volume", v.Name, "raw", fields[0])
			continue
		}
		if _, err := db.Exec(
			`INSERT OR REPLACE INTO docker_volume_usage (volume_name, size_bytes, measured_at) VALUES (?, ?, ?)`,
			v.Name, bytes, now,
		); err != nil {
			slog.Warn("volume usage: db write failed", "volume", v.Name, "error", err)
		}
	}

	// Prune rows for volumes that no longer exist so the cache tracks the
	// current volume set rather than every volume ever measured (otherwise a
	// host that churns ephemeral volumes grows this table unbounded). Guarded
	// on a non-empty list so a transient list/du failure — which yields zero
	// volumes — can't wipe the cache; stale rows clear on the next populated
	// tick. Volumes skipped this tick (non-local driver, du failure) stay in
	// `volumes` so they are retained.
	if len(volumes) > 0 {
		names := make([]any, 0, len(volumes))
		placeholders := make([]string, 0, len(volumes))
		for _, v := range volumes {
			names = append(names, v.Name)
			placeholders = append(placeholders, "?")
		}
		query := `DELETE FROM docker_volume_usage WHERE volume_name NOT IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := db.Exec(query, names...); err != nil {
			slog.Warn("volume usage: stale-row prune failed", "error", err)
		}
	}
}
