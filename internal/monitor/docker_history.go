package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/svrforum/SFPanel/internal/common/safe"
)

const (
	dockerCollectInterval  = 30 * time.Second
	dockerStatsCallTimeout = 5 * time.Second
)

// StartDockerHistoryCollector runs the metrics polling loop in a goroutine.
// Returns immediately. The goroutine stops cleanly when ctx is cancelled.
func StartDockerHistoryCollector(ctx context.Context, db *sql.DB, client DockerStatsClient) {
	safe.Go("monitor-docker-history", func() {
		ticker := time.NewTicker(dockerCollectInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collectOnce(ctx, client, db)
			}
		}
	})
}

// collectOnce performs one round of stats collection across all running
// containers. Errors per container are logged + skipped — never aborts.
func collectOnce(ctx context.Context, client DockerStatsClient, db *sql.DB) {
	containers, err := client.ListContainers(ctx)
	if err != nil {
		slog.Warn("docker history: list containers failed", "error", err)
		return
	}
	now := time.Now().UnixMilli()
	for _, c := range containers {
		if c.State != "running" {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, dockerStatsCallTimeout)
		stats, err := client.ContainerStats(callCtx, c.ID)
		cancel()
		if err != nil {
			continue
		}
		cpu := computeCPUPercent(stats)
		memBytes := stats.MemoryStats.Usage
		memPercent := 0.0
		if stats.MemoryStats.Limit > 0 {
			memPercent = (float64(memBytes) / float64(stats.MemoryStats.Limit)) * 100
		}
		name := containerDisplayName(c.Names)
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO container_metrics_history
			 (container_id, container_name, ts, cpu_percent, mem_percent, mem_bytes)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, name, now, cpu, memPercent, memBytes,
		); err != nil {
			slog.Warn("docker history: write failed", "container", name, "error", err)
		}
	}
}

func computeCPUPercent(s *container.StatsResponse) float64 {
	// Guard against uint64 underflow on counter resets (e.g. container
	// restart). A wrapped subtraction would yield a huge positive float64
	// and bypass the <= 0 check below, producing a bogus near-100% sample.
	if s.CPUStats.CPUUsage.TotalUsage < s.PreCPUStats.CPUUsage.TotalUsage {
		return 0
	}
	if s.CPUStats.SystemUsage < s.PreCPUStats.SystemUsage {
		return 0
	}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage - s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage - s.PreCPUStats.SystemUsage)
	if systemDelta <= 0 || cpuDelta <= 0 {
		return 0
	}
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = 1
	}
	return (cpuDelta / systemDelta) * cpus * 100.0
}

func containerDisplayName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
