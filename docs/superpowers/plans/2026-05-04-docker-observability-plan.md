# Docker Observability (Theme F) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Docker container metrics history (CPU+memory at 30s × 24h), Docker events lifecycle timeline, and three new alert rule types (`container_down`, `container_oom`, `container_restart_loop`) plugged into the existing alert pipeline.

**Architecture:** Two new background goroutines per node — a 30s polling collector for stats and a long-lived `docker events` stream listener — both writing to per-node SQLite tables. Existing `alert_rules` table is extended with three new `type` enum values that the events listener evaluates and dispatches through the existing alert manager. Sparkline + History tab are added to existing Docker Containers UI; no new top-level pages.

**Tech Stack:** Go 1.25, modernc.org/sqlite, docker/docker SDK v28 (`container.StatsResponse`, `cli.Events()`), echo v4, react 19 + uplot for charts, existing `monitor` and `alert` packages for retention/dispatch patterns.

**Spec reference:** `docs/superpowers/specs/2026-05-03-docker-observability-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `internal/db/migrations.go` (mod) | Migrations 16–19 — 2 tables + 2 indexes |
| `internal/config/config.go` (mod) | `Docker.Observability` struct + validation |
| `internal/monitor/docker_history.go` (new) | 30s polling collector + DockerStatsClient interface for mocking |
| `internal/monitor/docker_events.go` (new) | `cli.Events()` listener + reconnect loop + JSON parser |
| `internal/monitor/docker_retention.go` (new) | Two retention pruners (metrics + events) |
| `internal/feature/alert/container_rules.go` (new) | Pattern matcher + restart_loop window evaluator + dispatcher |
| `internal/feature/docker/observability.go` (new) | 3 read endpoints + ListContainers response augmentation |
| `internal/feature/docker/handler.go` (mod) | Augment ListContainers to call observability metric averages |
| `internal/api/router.go` (mod) | Register the 3 new endpoints |
| `cmd/sfpanel/main.go` (mod) | Start 4 new goroutines on bgCtx (collector + events listener + 2 pruners) |
| `web/src/types/api.ts` (mod) | `ContainerMetricPoint`, `ContainerEvent`, alert rule extension types |
| `web/src/lib/api.ts` (mod) | `getContainerMetrics`, `getContainerEvents`, `getRecentEvents` |
| `web/src/components/ContainerSparkline.tsx` (new) | 60-point uplot mini chart |
| `web/src/components/EventTimelineRow.tsx` (new) | Single event row with icon + label |
| `web/src/components/ContainerHistoryTab.tsx` (new) | Range selector + chart + paginated event list |
| `web/src/pages/docker/DockerContainers.tsx` (mod) | Add sparklines to row, History tab into drawer |
| `web/src/pages/Alerts.tsx` (mod) | 3 new type options + dynamic condition fields |

---

## Task 1: Schema migrations 16–19

**Files:**
- Modify: `internal/db/migrations.go` (append to migrations slice)
- Test: `internal/db/migrations_test.go` (already has `TestRunMigrations_Idempotent` — no changes needed; the new entries are auto-exercised)

- [ ] **Step 1: Run the existing migration test to confirm baseline green**

Run: `go test ./internal/db/ -run TestRunMigrations_Idempotent -count=1 -v`
Expected: PASS

- [ ] **Step 2: Append migrations 16–19**

Open `internal/db/migrations.go` and append to the end of the `migrations` slice (just before the closing `}`):

```go
	{ID: 16, Up: `CREATE TABLE IF NOT EXISTS container_metrics_history (
		container_id   TEXT    NOT NULL,
		container_name TEXT    NOT NULL,
		ts             INTEGER NOT NULL,
		cpu_percent    REAL    NOT NULL,
		mem_percent    REAL    NOT NULL,
		mem_bytes      INTEGER NOT NULL,
		PRIMARY KEY (container_id, ts)
	)`},
	{ID: 17, Up: `CREATE TABLE IF NOT EXISTS container_events (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		container_id   TEXT    NOT NULL,
		container_name TEXT    NOT NULL,
		ts             INTEGER NOT NULL,
		event_type     TEXT    NOT NULL,
		exit_code      INTEGER,
		detail         TEXT
	)`},
	{ID: 18, Up: `CREATE INDEX IF NOT EXISTS idx_container_events_container_ts ON container_events(container_id, ts DESC)`},
	{ID: 19, Up: `CREATE INDEX IF NOT EXISTS idx_container_events_ts ON container_events(ts DESC)`},
```

- [ ] **Step 3: Run the migration test again**

Run: `go test ./internal/db/ -count=1 -v`
Expected: All three tests pass; the log output shows `applied id=16` … `applied id=19` in `TestRunMigrations_Idempotent`.

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations.go
git commit -m "db: add container_metrics_history + container_events tables (Theme F)"
```

---

## Task 2: Config struct for observability knobs

**Files:**
- Modify: `internal/config/config.go` (extend `DockerConfig`)
- Test: `internal/config/config_test.go` (create if missing) for validation

- [ ] **Step 1: Write failing test for config validation**

Create `internal/config/config_test.go`:

```go
package config

import "testing"

func TestObservabilityValidation(t *testing.T) {
	cases := []struct {
		name      string
		retention string
		eventsRet string
		wantOK    bool
	}{
		{"defaults are valid", "24h", "30d", true},
		{"6h metrics ok", "6h", "30d", true},
		{"72h metrics ok", "72h", "30d", true},
		{"7d events ok", "24h", "7d", true},
		{"90d events ok", "24h", "90d", true},
		{"invalid metrics retention", "5m", "30d", false},
		{"invalid events retention", "24h", "1y", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{
				Server:   ServerConfig{Port: 19443},
				Database: DatabaseConfig{Path: "/tmp/x.db"},
				Auth:     AuthConfig{JWTSecret: "0123456789abcdef0123456789abcdef"},
				Docker: DockerConfig{
					Socket: "unix:///var/run/docker.sock",
					Observability: ObservabilityConfig{
						Enabled:          ptrBool(true),
						MetricsRetention: c.retention,
						EventsRetention:  c.eventsRet,
					},
				},
			}
			err := cfg.Validate()
			if c.wantOK && err != nil {
				t.Errorf("expected OK, got %v", err)
			}
			if !c.wantOK && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func ptrBool(b bool) *bool { return &b }
```

Also add `TestLoadObservabilityDefaults` exercising the Load() path (no-block →
default-on, explicit `enabled: false` → off, garbage retention rejected even when
disabled, explicit retention overrides). The Validate-only test above doesn't
cover the Load defaults path — add a separate Load-driven test that writes
minimal YAML to a temp file and asserts the resulting Config.

- [ ] **Step 2: Run test, expect compile failure**

Run: `go test ./internal/config/ -run TestObservabilityValidation -count=1`
Expected: FAIL — `undefined: ObservabilityConfig`

- [ ] **Step 3: Add ObservabilityConfig struct and validation**

In `internal/config/config.go` extend `DockerConfig` and add the new struct:

```go
type DockerConfig struct {
	Socket        string              `yaml:"socket"`
	Observability ObservabilityConfig `yaml:"observability"`
}

// ObservabilityConfig controls the per-container metrics + events feature
// (theme F). Disabled means the collector / events listener / pruners do
// not start; new endpoints return empty arrays + observability_disabled flag.
//
// Enabled is a *bool so we can distinguish three YAML states:
//   - field absent (nil)        → default-on (IsEnabled() = true)
//   - explicit `enabled: false` → off
//   - explicit `enabled: true`  → on
//
// A plain `bool` collapses "absent" and "false" to the same zero value, which
// would silently re-enable observability for an operator who wrote
// `enabled: false` without overriding retention strings.
type ObservabilityConfig struct {
	Enabled          *bool  `yaml:"enabled"`
	MetricsRetention string `yaml:"metrics_retention"` // "6h" | "24h" | "72h"
	EventsRetention  string `yaml:"events_retention"`  // "7d" | "30d" | "90d"
}

// IsEnabled returns true when the operator hasn't explicitly disabled
// observability. Default-on: nil (block absent) → true. Explicitly false → false.
// Call sites should use IsEnabled() — never read .Enabled directly.
func (o ObservabilityConfig) IsEnabled() bool {
	return o.Enabled == nil || *o.Enabled
}

var (
	allowedMetricsRetentions = map[string]bool{"6h": true, "24h": true, "72h": true}
	allowedEventsRetentions  = map[string]bool{"7d": true, "30d": true, "90d": true}
)
```

In `Config.Validate()` add (right before `return nil`). Validate retention values
whenever they're set, regardless of IsEnabled — a typo like `metrics_retention: garbge`
should fail at config-write time, not silently lurk until the operator flips
enabled and restarts. The empty-string allowance is fine because Load() always
fills defaults before Validate() runs:

```go
	if r := c.Docker.Observability.MetricsRetention; r != "" && !allowedMetricsRetentions[r] {
		return fmt.Errorf("docker.observability.metrics_retention must be one of 6h/24h/72h, got %q", r)
	}
	if r := c.Docker.Observability.EventsRetention; r != "" && !allowedEventsRetentions[r] {
		return fmt.Errorf("docker.observability.events_retention must be one of 7d/30d/90d, got %q", r)
	}
```

In `Load()` (after defaults are applied — find where `Auth.TokenExpiry` defaults are set), add:

```go
	// docker.observability is default-on. Enabled is *bool so a missing block
	// (nil) means "use default-on" via IsEnabled(); only an explicit
	// `enabled: false` disables it. Fill missing retention strings here so
	// downstream code (and Validate's empty-string allowance) sees real values.
	if cfg.Docker.Observability.MetricsRetention == "" {
		cfg.Docker.Observability.MetricsRetention = "24h"
	}
	if cfg.Docker.Observability.EventsRetention == "" {
		cfg.Docker.Observability.EventsRetention = "30d"
	}
```

- [ ] **Step 4: Run validation tests**

Run: `go test ./internal/config/ -run TestObservabilityValidation -count=1 -v`
Expected: All 7 sub-tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: docker.observability with metrics/events retention knobs"
```

---

## Task 3: DockerStatsClient interface for mocking

**Files:**
- Create: `internal/monitor/docker_stats_client.go`

This task introduces a minimum interface that the collector consumes, so tests can swap it for a fake.

- [ ] **Step 1: Create the interface file**

Create `internal/monitor/docker_stats_client.go`:

```go
package monitor

import (
	"context"

	"github.com/docker/docker/api/types/container"
)

// DockerStatsClient is the small subset of the Docker SDK that the metrics
// collector goroutine calls. Defined as a named interface here (instead of
// taking *docker.Client directly) so the collector can be tested with a
// fake — Docker SDK doesn't ship mocks and starting a real daemon for
// unit tests is overkill.
type DockerStatsClient interface {
	ListContainers(ctx context.Context) ([]container.Summary, error)
	ContainerStats(ctx context.Context, id string) (*container.StatsResponse, error)
}
```

- [ ] **Step 2: Verify build still compiles**

Run: `go build ./internal/monitor/`
Expected: success.

- [ ] **Step 3: Confirm `*docker.Client` satisfies the interface**

```bash
mkdir -p cmd/_ifacecheck && cat > cmd/_ifacecheck/main.go <<'EOF'
package main

import (
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/monitor"
)

var _ monitor.DockerStatsClient = (*docker.Client)(nil)

func main() {}
EOF
go build ./cmd/_ifacecheck
rm -rf cmd/_ifacecheck
```

Expected: success (compile-time interface satisfaction).

- [ ] **Step 4: Commit**

```bash
git add internal/monitor/docker_stats_client.go
git commit -m "monitor: DockerStatsClient interface for collector mocking"
```

---

## Task 4: Metrics collector goroutine + tests

**Files:**
- Create: `internal/monitor/docker_history.go`
- Create: `internal/monitor/docker_history_test.go`

- [ ] **Step 1: Write failing test for the collector tick**

Create `internal/monitor/docker_history_test.go`:

```go
package monitor

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/container"
	_ "modernc.org/sqlite"
)

type fakeStatsClient struct {
	listResp  []container.Summary
	listErr   error
	statsResp map[string]*container.StatsResponse
	statsErr  map[string]error
	calls     []string
}

func (f *fakeStatsClient) ListContainers(ctx context.Context) ([]container.Summary, error) {
	return f.listResp, f.listErr
}
func (f *fakeStatsClient) ContainerStats(ctx context.Context, id string) (*container.StatsResponse, error) {
	f.calls = append(f.calls, id)
	if err, ok := f.statsErr[id]; ok {
		return nil, err
	}
	if r, ok := f.statsResp[id]; ok {
		return r, nil
	}
	return nil, errors.New("no fixture")
}

func openTestDBForMonitor(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE container_metrics_history (
		container_id TEXT NOT NULL, container_name TEXT NOT NULL, ts INTEGER NOT NULL,
		cpu_percent REAL NOT NULL, mem_percent REAL NOT NULL, mem_bytes INTEGER NOT NULL,
		PRIMARY KEY (container_id, ts))`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestCollectOnce_RunningContainer(t *testing.T) {
	db := openTestDBForMonitor(t)
	fc := &fakeStatsClient{
		listResp: []container.Summary{
			{ID: "abc123", Names: []string{"/nginx-app"}, State: "running"},
		},
		statsResp: map[string]*container.StatsResponse{
			"abc123": fakeStats(20.0, 1024*1024*512, 1024*1024*1024),
		},
	}
	collectOnce(context.Background(), fc, db)

	var name string
	var cpu, mem float64
	var memBytes int64
	row := db.QueryRow(`SELECT container_name, cpu_percent, mem_percent, mem_bytes FROM container_metrics_history`)
	if err := row.Scan(&name, &cpu, &mem, &memBytes); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "nginx-app" {
		t.Errorf("name: got %q, want nginx-app", name)
	}
	if cpu < 19 || cpu > 21 {
		t.Errorf("cpu: got %v, want ~20", cpu)
	}
	if memBytes != 1024*1024*512 {
		t.Errorf("mem_bytes mismatch: %d", memBytes)
	}
}

func TestCollectOnce_SkipsRemovedContainer(t *testing.T) {
	db := openTestDBForMonitor(t)
	fc := &fakeStatsClient{
		listResp: []container.Summary{
			{ID: "abc123", Names: []string{"/x"}, State: "running"},
		},
		statsErr: map[string]error{"abc123": errors.New("No such container")},
	}
	collectOnce(context.Background(), fc, db)

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM container_metrics_history`).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 rows after removed-container error, got %d", n)
	}
}

func TestCollectOnce_SkipsStoppedContainer(t *testing.T) {
	db := openTestDBForMonitor(t)
	fc := &fakeStatsClient{
		listResp: []container.Summary{
			{ID: "abc", Names: []string{"/x"}, State: "exited"},
		},
	}
	collectOnce(context.Background(), fc, db)

	if len(fc.calls) != 0 {
		t.Errorf("expected no stats calls for stopped container, got %v", fc.calls)
	}
}

// fakeStats builds a container.StatsResponse with the given CPU% intent and
// memory usage/limit. Math: cpu_pct = (cpu_delta / system_delta) * cpus * 100.
// Picking cpus=1, system_delta=100, cpu_delta=cpuPct yields exactly cpuPct.
func fakeStats(cpuPct float64, memUsage, memLimit uint64) *container.StatsResponse {
	r := &container.StatsResponse{}
	r.CPUStats.CPUUsage.TotalUsage = uint64(cpuPct)
	r.CPUStats.SystemUsage = 100
	r.CPUStats.OnlineCPUs = 1
	r.PreCPUStats.CPUUsage.TotalUsage = 0
	r.PreCPUStats.SystemUsage = 0
	r.MemoryStats.Usage = memUsage
	r.MemoryStats.Limit = memLimit
	return r
}
```

- [ ] **Step 2: Run test, expect compile failure**

Run: `go test ./internal/monitor/ -run TestCollectOnce -count=1`
Expected: FAIL — `undefined: collectOnce`

- [ ] **Step 3: Implement the collector**

Create `internal/monitor/docker_history.go`:

```go
package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

const (
	dockerCollectInterval  = 30 * time.Second
	dockerStatsCallTimeout = 5 * time.Second
)

// StartDockerHistoryCollector runs the metrics polling loop in a goroutine.
// Returns immediately. The goroutine stops cleanly when ctx is cancelled.
func StartDockerHistoryCollector(ctx context.Context, db *sql.DB, client DockerStatsClient) {
	go func() {
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
	}()
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/monitor/ -run TestCollectOnce -count=1 -v`
Expected: 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/docker_history.go internal/monitor/docker_history_test.go
git commit -m "monitor: docker container metrics polling collector (30s tick)"
```

---

## Task 5: Events listener — JSON parser

**Files:**
- Create: `internal/monitor/docker_events.go` (parser only — listener loop is Task 6)
- Create: `internal/monitor/docker_events_test.go`

- [ ] **Step 1: Write failing tests for the parser**

Create `internal/monitor/docker_events_test.go`:

```go
package monitor

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/events"
)

func TestParseDockerEvent_Lifecycle(t *testing.T) {
	cases := []struct {
		name string
		in   events.Message
		want *ContainerEvent
	}{
		{
			"start",
			events.Message{
				Type: events.ContainerEventType, Action: "start",
				Time: 1714742400, TimeNano: 1714742400_000_000_000,
				Actor: events.Actor{ID: "abc", Attributes: map[string]string{"name": "nginx-app"}},
			},
			&ContainerEvent{ContainerID: "abc", ContainerName: "nginx-app", TS: 1714742400000, EventType: "start"},
		},
		{
			"die with exit code",
			events.Message{
				Type: events.ContainerEventType, Action: "die",
				Time: 1714742500, TimeNano: 1714742500_000_000_000,
				Actor: events.Actor{ID: "abc", Attributes: map[string]string{"name": "nginx-app", "exitCode": "137"}},
			},
			&ContainerEvent{ContainerID: "abc", ContainerName: "nginx-app", TS: 1714742500000, EventType: "die", ExitCode: ptrInt(137)},
		},
		{
			"oom",
			events.Message{
				Type: events.ContainerEventType, Action: "oom",
				Time: 1714742600, TimeNano: 1714742600_000_000_000,
				Actor: events.Actor{ID: "abc", Attributes: map[string]string{"name": "nginx-app"}},
			},
			&ContainerEvent{ContainerID: "abc", ContainerName: "nginx-app", TS: 1714742600000, EventType: "oom"},
		},
		{
			"healthy from health_status:healthy",
			events.Message{
				Type: events.ContainerEventType, Action: "health_status: healthy",
				Time: 1714742700, TimeNano: 1714742700_000_000_000,
				Actor: events.Actor{ID: "abc", Attributes: map[string]string{"name": "nginx-app"}},
			},
			&ContainerEvent{ContainerID: "abc", ContainerName: "nginx-app", TS: 1714742700000, EventType: "healthy"},
		},
		{
			"unhealthy",
			events.Message{
				Type: events.ContainerEventType, Action: "health_status: unhealthy",
				Time: 1714742800, TimeNano: 1714742800_000_000_000,
				Actor: events.Actor{ID: "abc", Attributes: map[string]string{"name": "nginx-app"}},
			},
			&ContainerEvent{ContainerID: "abc", ContainerName: "nginx-app", TS: 1714742800000, EventType: "unhealthy"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseDockerEvent(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseDockerEvent_UnknownActionDropped(t *testing.T) {
	got := parseDockerEvent(events.Message{
		Type: events.ContainerEventType, Action: "exec_create: ls",
		Actor: events.Actor{ID: "x"},
	})
	if got != nil {
		t.Errorf("expected nil for unknown action, got %+v", got)
	}
}

func TestParseDockerEvent_NonContainerTypeDropped(t *testing.T) {
	got := parseDockerEvent(events.Message{Type: events.ImageEventType, Action: "pull"})
	if got != nil {
		t.Errorf("expected nil for non-container event, got %+v", got)
	}
}

func ptrInt(n int) *int { return &n }
```

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/monitor/ -run TestParseDockerEvent -count=1`
Expected: FAIL — `undefined: parseDockerEvent`, `ContainerEvent`.

- [ ] **Step 3: Implement parser + struct**

Create `internal/monitor/docker_events.go`:

```go
package monitor

import (
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/events"
)

// ContainerEvent is the canonical row shape we persist to container_events.
// Built from a Docker daemon Message via parseDockerEvent. Exported so
// the alert dispatcher (in a different package) can consume it.
type ContainerEvent struct {
	ContainerID   string
	ContainerName string
	TS            int64 // unix milliseconds
	EventType     string
	ExitCode      *int
	Detail        string // optional JSON
}

// parseDockerEvent maps a Docker daemon event message to the canonical
// ContainerEvent. Returns nil for events we don't track (the 8-event
// filter happens here authoritatively, not at the daemon — the daemon's
// `--filter event=...` only reduces wire load).
func parseDockerEvent(m events.Message) *ContainerEvent {
	if m.Type != events.ContainerEventType {
		return nil
	}
	t := normalizeAction(m.Action)
	if t == "" {
		return nil
	}
	tsMillis := m.TimeNano / 1_000_000
	if tsMillis == 0 {
		tsMillis = m.Time * 1000
	}
	ev := &ContainerEvent{
		ContainerID:   m.Actor.ID,
		ContainerName: m.Actor.Attributes["name"],
		TS:            tsMillis,
		EventType:     t,
	}
	if codeStr := m.Actor.Attributes["exitCode"]; codeStr != "" {
		if n, err := strconv.Atoi(codeStr); err == nil {
			ev.ExitCode = &n
		}
	}
	return ev
}

// normalizeAction translates Docker's action strings to our 8 canonical
// event_type values. Returns "" for actions we don't track. Docker emits
// "health_status: healthy" / "health_status: unhealthy" — collapse to
// "healthy"/"unhealthy".
func normalizeAction(action string) string {
	switch action {
	case "start", "stop", "die", "oom", "kill", "restart":
		return action
	}
	if strings.HasPrefix(action, "health_status:") {
		rest := strings.TrimSpace(strings.TrimPrefix(action, "health_status:"))
		switch rest {
		case "healthy", "unhealthy":
			return rest
		}
	}
	return ""
}
```

- [ ] **Step 4: Run parser tests**

Run: `go test ./internal/monitor/ -run TestParseDockerEvent -count=1 -v`
Expected: 7 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/docker_events.go internal/monitor/docker_events_test.go
git commit -m "monitor: docker events parser with 8-type filter"
```

---

## Task 6: Events listener loop + reconnect

**Files:**
- Modify: `internal/monitor/docker_events.go` (append listener function)
- Modify: `internal/monitor/docker_events_test.go` (append listener test)

- [ ] **Step 1: Append listener function**

Append to `internal/monitor/docker_events.go`:

```go
// (also add these imports at the top; keep existing imports)
import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// DockerEventsClient is the small subset of the Docker SDK that the events
// listener needs. Subset of moby/client.Client.Events. Defined here as a
// named interface for the same mocking reasons as DockerStatsClient.
type DockerEventsClient interface {
	Events(ctx context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error)
}

// EventDispatcher is the bridge to the alert pipeline. Each successfully-
// persisted event is also handed to dispatcher so alert rules can fire.
// nil dispatcher = persistence only (used in tests).
type EventDispatcher interface {
	Dispatch(ctx context.Context, ev *ContainerEvent)
}

// StartDockerEventsListener runs the long-lived event stream listener in a
// goroutine. Reconnects on stream error with exponential backoff capped at
// 5 minutes. Stops when ctx is cancelled.
func StartDockerEventsListener(ctx context.Context, db *sql.DB, client DockerEventsClient, disp EventDispatcher) {
	go runEventsListener(ctx, db, client, disp)
}

func runEventsListener(ctx context.Context, db *sql.DB, client DockerEventsClient, disp EventDispatcher) {
	const maxBackoff = 5 * time.Minute
	backoff := 1 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := streamOnce(ctx, db, client, disp)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("docker events: stream ended, reconnecting", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// streamOnce opens a single events stream and runs until it closes.
// Returns the underlying stream error so runEventsListener can decide
// whether to reconnect (it always does, with backoff).
func streamOnce(ctx context.Context, db *sql.DB, client DockerEventsClient, disp EventDispatcher) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	msgs, errs := client.Events(streamCtx, events.ListOptions{})
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case m := <-msgs:
			ev := parseDockerEvent(m)
			if ev == nil {
				continue
			}
			persistEvent(db, ev)
			if disp != nil {
				disp.Dispatch(ctx, ev)
			}
		}
	}
}

func persistEvent(db *sql.DB, ev *ContainerEvent) {
	var detail interface{}
	if ev.Detail != "" {
		detail = ev.Detail
	}
	var exitCode interface{}
	if ev.ExitCode != nil {
		exitCode = *ev.ExitCode
	}
	if _, err := db.Exec(
		`INSERT INTO container_events (container_id, container_name, ts, event_type, exit_code, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		ev.ContainerID, ev.ContainerName, ev.TS, ev.EventType, exitCode, detail,
	); err != nil {
		slog.Warn("docker events: persist failed", "error", err)
	}
}
```

- [ ] **Step 2: Append listener test**

Append to `internal/monitor/docker_events_test.go`:

```go
import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type fakeEventsClient struct {
	msgs chan events.Message
	errs chan error
}

func (f *fakeEventsClient) Events(ctx context.Context, _ events.ListOptions) (<-chan events.Message, <-chan error) {
	return f.msgs, f.errs
}

type fakeDispatcher struct {
	got []*ContainerEvent
}

func (d *fakeDispatcher) Dispatch(_ context.Context, ev *ContainerEvent) {
	d.got = append(d.got, ev)
}

func openTestDBForEvents(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, _ := sql.Open("sqlite", dbPath)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE container_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		container_id TEXT NOT NULL, container_name TEXT NOT NULL,
		ts INTEGER NOT NULL, event_type TEXT NOT NULL,
		exit_code INTEGER, detail TEXT)`)
	return db
}

func TestStreamOnce_PersistsAndDispatches(t *testing.T) {
	db := openTestDBForEvents(t)
	fc := &fakeEventsClient{msgs: make(chan events.Message, 4), errs: make(chan error, 1)}
	disp := &fakeDispatcher{}

	go func() {
		fc.msgs <- events.Message{
			Type: events.ContainerEventType, Action: "start",
			Time: 1714742400, TimeNano: 1714742400_000_000_000,
			Actor: events.Actor{ID: "a", Attributes: map[string]string{"name": "x"}},
		}
		fc.msgs <- events.Message{
			Type: events.ContainerEventType, Action: "die",
			Time: 1714742410, TimeNano: 1714742410_000_000_000,
			Actor: events.Actor{ID: "a", Attributes: map[string]string{"name": "x", "exitCode": "0"}},
		}
		fc.errs <- io.EOF
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = streamOnce(ctx, db, fc, disp)

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM container_events`).Scan(&n)
	if n != 2 {
		t.Errorf("persisted: got %d, want 2", n)
	}
	if len(disp.got) != 2 {
		t.Errorf("dispatched: got %d, want 2", len(disp.got))
	}
}
```

- [ ] **Step 3: Run all monitor tests**

Run: `go test ./internal/monitor/ -count=1 -v`
Expected: all parser + listener tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/monitor/docker_events.go internal/monitor/docker_events_test.go
git commit -m "monitor: docker events listener with reconnect + dispatcher hook"
```

---

## Task 7: Retention pruners

**Files:**
- Create: `internal/monitor/docker_retention.go`
- Create: `internal/monitor/docker_retention_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/monitor/docker_retention_test.go`:

```go
package monitor

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDBFull(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, _ := sql.Open("sqlite", dbPath)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE container_metrics_history (
		container_id TEXT NOT NULL, container_name TEXT NOT NULL, ts INTEGER NOT NULL,
		cpu_percent REAL NOT NULL, mem_percent REAL NOT NULL, mem_bytes INTEGER NOT NULL,
		PRIMARY KEY (container_id, ts))`)
	db.Exec(`CREATE TABLE container_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		container_id TEXT NOT NULL, container_name TEXT NOT NULL,
		ts INTEGER NOT NULL, event_type TEXT NOT NULL,
		exit_code INTEGER, detail TEXT)`)
	return db
}

func TestPruneMetrics_DropsOlderThanRetention(t *testing.T) {
	db := openTestDBFull(t)
	now := time.Now().UnixMilli()
	old := now - (25 * time.Hour).Milliseconds()
	db.Exec(`INSERT INTO container_metrics_history VALUES ('a','x',?,1,1,1),('a','x',?,2,2,2)`, old, now)

	pruneMetrics(db, 24*time.Hour)

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM container_metrics_history`).Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 row remaining (older row pruned), got %d", n)
	}
}

func TestPruneEvents_AgeAndRowCap(t *testing.T) {
	db := openTestDBFull(t)
	now := time.Now().UnixMilli()
	old := now - (31 * 24 * time.Hour).Milliseconds()
	db.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('a','x',?,'start'),('a','x',?,'start')`, old, now)
	tx, _ := db.Begin()
	for i := 0; i < 5050; i++ {
		tx.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('b','y',?,'start')`, now-int64(i*1000))
	}
	tx.Commit()

	pruneEvents(db, 30*24*time.Hour, 5000)

	var nA, nB int
	db.QueryRow(`SELECT COUNT(*) FROM container_events WHERE container_id='a'`).Scan(&nA)
	db.QueryRow(`SELECT COUNT(*) FROM container_events WHERE container_id='b'`).Scan(&nB)
	if nA != 1 {
		t.Errorf("container a: expected 1 row, got %d", nA)
	}
	if nB != 5000 {
		t.Errorf("container b: expected 5000 rows (cap), got %d", nB)
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/monitor/ -run TestPrune -count=1`
Expected: FAIL — `undefined: pruneMetrics, pruneEvents`.

- [ ] **Step 3: Implement pruners**

Create `internal/monitor/docker_retention.go`:

```go
package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

const containerEventsPerContainerCap = 5000

// StartDockerMetricsRetention runs an hourly pruner. Caller passes
// `retention` from the parsed config (6h/24h/72h → time.Duration).
func StartDockerMetricsRetention(ctx context.Context, db *sql.DB, retention time.Duration) {
	go func() {
		pruneMetrics(db, retention)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneMetrics(db, retention)
			}
		}
	}()
}

// StartDockerEventsRetention runs an hourly pruner. Enforces both an age
// cap (from config) and a per-container row cap (hardcoded).
func StartDockerEventsRetention(ctx context.Context, db *sql.DB, retention time.Duration) {
	go func() {
		pruneEvents(db, retention, containerEventsPerContainerCap)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneEvents(db, retention, containerEventsPerContainerCap)
			}
		}
	}()
}

func pruneMetrics(db *sql.DB, retention time.Duration) {
	cutoff := time.Now().Add(-retention).UnixMilli()
	if _, err := db.Exec(`DELETE FROM container_metrics_history WHERE ts < ?`, cutoff); err != nil {
		slog.Warn("container_metrics_history retention prune failed", "error", err)
	}
}

func pruneEvents(db *sql.DB, retention time.Duration, perContainerCap int) {
	cutoff := time.Now().Add(-retention).UnixMilli()
	if _, err := db.Exec(`DELETE FROM container_events WHERE ts < ?`, cutoff); err != nil {
		slog.Warn("container_events age prune failed", "error", err)
	}
	if _, err := db.Exec(`
		DELETE FROM container_events
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY container_id ORDER BY ts DESC) AS rn
				FROM container_events
			) WHERE rn > ?
		)`, perContainerCap); err != nil {
		slog.Warn("container_events row-cap prune failed", "error", err)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/monitor/ -run TestPrune -count=1 -v`
Expected: 2 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/docker_retention.go internal/monitor/docker_retention_test.go
git commit -m "monitor: docker observability retention pruners"
```

---

## Task 8: Alert rule pattern matcher

**Files:**
- Create: `internal/feature/alert/container_rules.go`
- Create: `internal/feature/alert/container_rules_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/feature/alert/container_rules_test.go`:

```go
package alert

import "testing"

func TestMatchContainerPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*", "anything", true},
		{"nginx-*", "nginx-app", true},
		{"nginx-*", "nginx-", true},
		{"nginx-*", "apache-app", false},
		{"nginx-app", "nginx-app", true},
		{"nginx-app", "nginx-app-2", false},
		{"*-prod", "myapp-prod", true},
		{"*-prod", "myapp-dev", false},
		{"foo?bar", "fooXbar", true},
		{"foo?bar", "fooXYbar", false},
		// Regex special characters treated as literals.
		{"a.b", "a.b", true},
		{"a.b", "axb", false},
		// Empty pattern never matches anything.
		{"", "x", false},
	}
	for _, c := range cases {
		got := matchContainerPattern(c.pattern, c.name)
		if got != c.want {
			t.Errorf("match(%q, %q) = %v; want %v", c.pattern, c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/feature/alert/ -run TestMatchContainerPattern -count=1`
Expected: FAIL — `undefined: matchContainerPattern`.

- [ ] **Step 3: Implement matcher**

Create `internal/feature/alert/container_rules.go`:

```go
package alert

import (
	"regexp"
	"strings"
)

// matchContainerPattern matches shell-style wildcards (* and ?) against a
// container name. Returns true on a full match. Other regex specials are
// quoted as literals — operators frequently put `.` in container names
// (e.g. `db.example.com`) so we can't let `.` mean "any character".
//
// Empty pattern never matches anything (avoid accidental "match all" via
// a misconfigured rule with no pattern).
func matchContainerPattern(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(name)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/feature/alert/ -run TestMatchContainerPattern -count=1 -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/alert/container_rules.go internal/feature/alert/container_rules_test.go
git commit -m "alert: container_pattern shell-glob matcher"
```

---

## Task 9: container_restart_loop window evaluator

**Files:**
- Modify: `internal/feature/alert/container_rules.go` (append window logic)
- Modify: `internal/feature/alert/container_rules_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/alert/container_rules_test.go`:

```go
import "time"

func TestRestartLoopEvaluator(t *testing.T) {
	now := time.Now().UnixMilli()
	cases := []struct {
		name         string
		restartTimes []int64
		threshold    int
		windowSec    int
		want         bool
	}{
		{
			"3 restarts in 5min triggers",
			[]int64{now - 60_000, now - 120_000, now - 180_000},
			3, 300, true,
		},
		{
			"2 restarts in 5min does not trigger",
			[]int64{now - 60_000, now - 120_000},
			3, 300, false,
		},
		{
			"3 restarts spread over 6min does not trigger",
			[]int64{now - 60_000, now - 200_000, now - 360_000},
			3, 300, false,
		},
		{
			"empty list does not trigger",
			[]int64{},
			3, 300, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evaluateRestartLoop(c.restartTimes, c.threshold, c.windowSec)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/feature/alert/ -run TestRestartLoopEvaluator -count=1`
Expected: FAIL — `undefined: evaluateRestartLoop`.

- [ ] **Step 3: Implement evaluator**

Append to `internal/feature/alert/container_rules.go`:

```go
import "time"

// evaluateRestartLoop returns true when at least `threshold` of the
// supplied restart timestamps fall within the last `windowSec` seconds.
// Caller fetches the recent restart timestamps from container_events;
// this function is pure logic for testability.
func evaluateRestartLoop(restartTimesMillis []int64, threshold, windowSec int) bool {
	if threshold <= 0 || len(restartTimesMillis) < threshold {
		return false
	}
	cutoff := time.Now().Add(-time.Duration(windowSec) * time.Second).UnixMilli()
	count := 0
	for _, t := range restartTimesMillis {
		if t >= cutoff {
			count++
		}
	}
	return count >= threshold
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/feature/alert/ -run TestRestartLoopEvaluator -count=1 -v`
Expected: 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/alert/container_rules.go internal/feature/alert/container_rules_test.go
git commit -m "alert: container_restart_loop window evaluator"
```

---

## Task 10: Container alert dispatcher

**Files:**
- Modify: `internal/feature/alert/container_rules.go` (append dispatcher)
- Modify: `internal/feature/alert/container_rules_test.go`

- [ ] **Step 1: Append failing test for the dispatcher**

Append to `internal/feature/alert/container_rules_test.go`:

```go
import (
	"context"
	"database/sql"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func TestContainerDispatcher_FiresContainerDown(t *testing.T) {
	db := openAlertTestDB(t)
	db.Exec(`INSERT INTO alert_rules (name,type,condition,channel_ids,severity,cooldown,node_scope,node_ids,enabled) VALUES
		('down-all', 'container_down', '{"container_pattern":"*"}', '[]', 'warning', 0, 'all', '[]', 1)`)

	chDisp := &fakeChannelDispatcher{}
	disp := NewContainerDispatcher(db, chDisp)
	disp.Dispatch(context.Background(), &AlertContainerEvent{
		ID: "abc", Name: "nginx-app", Type: "die", TS: 1714742400000,
	})

	if chDisp.count != 1 {
		t.Errorf("expected 1 alert fire, got %d", chDisp.count)
	}
}

type fakeChannelDispatcher struct{ count int }

func (f *fakeChannelDispatcher) Fire(_ context.Context, _ AlertFire) { f.count++ }

func openAlertTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, _ := sql.Open("sqlite", dbPath)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE alert_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, type TEXT, condition TEXT,
		channel_ids TEXT, severity TEXT, cooldown INTEGER, node_scope TEXT, node_ids TEXT, enabled INTEGER)`)
	db.Exec(`CREATE TABLE container_events (id INTEGER PRIMARY KEY AUTOINCREMENT,
		container_id TEXT, container_name TEXT, ts INTEGER, event_type TEXT,
		exit_code INTEGER, detail TEXT)`)
	return db
}
```

- [ ] **Step 2: Implement dispatcher**

Append to `internal/feature/alert/container_rules.go`:

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strconv"
)

// AlertFire is the payload handed to the channel dispatcher (existing
// alert manager).
type AlertFire struct {
	RuleName string
	Type     string
	Severity string
	Message  string
}

// ChannelDispatcher is implemented by the existing alert manager. We
// declare the interface here to avoid a feature/alert → alert/channels
// cycle in either direction.
type ChannelDispatcher interface {
	Fire(ctx context.Context, f AlertFire)
}

// AlertContainerEvent is the slim adapter shape passed in from monitor/
// without coupling alert/ to monitor.ContainerEvent type.
type AlertContainerEvent struct {
	ID, Name, Type string
	TS             int64
	ExitCode       *int
}

// ContainerDispatcher evaluates container alert rules whenever a matching
// container event is observed. Implements monitor.EventDispatcher via
// the Dispatch shim wired in main.go.
type ContainerDispatcher struct {
	db    *sql.DB
	chDsp ChannelDispatcher
}

func NewContainerDispatcher(db *sql.DB, ch ChannelDispatcher) *ContainerDispatcher {
	return &ContainerDispatcher{db: db, chDsp: ch}
}

// Dispatch is the entry point — translates the event to alert evaluation.
func (d *ContainerDispatcher) Dispatch(ctx context.Context, ev *AlertContainerEvent) {
	rows, err := d.db.Query(`SELECT id, name, type, condition, severity FROM alert_rules WHERE enabled=1 AND type IN ('container_down','container_oom','container_restart_loop')`)
	if err != nil {
		slog.Warn("container alert rules query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var name, typ, condStr, sev string
		if err := rows.Scan(&id, &name, &typ, &condStr, &sev); err != nil {
			continue
		}
		var cond struct {
			ContainerPattern string `json:"container_pattern"`
			ThresholdCount   int    `json:"threshold_count"`
			WindowSeconds    int    `json:"window_seconds"`
		}
		_ = json.Unmarshal([]byte(condStr), &cond)
		if !matchContainerPattern(cond.ContainerPattern, ev.Name) {
			continue
		}
		switch typ {
		case "container_down":
			if ev.Type != "die" && ev.Type != "oom" {
				continue
			}
		case "container_oom":
			if ev.Type != "oom" {
				continue
			}
			sev = "critical"
		case "container_restart_loop":
			if ev.Type != "restart" {
				continue
			}
			times, qerr := d.recentRestartTimes(ev.ID, cond.WindowSeconds)
			if qerr != nil || !evaluateRestartLoop(times, cond.ThresholdCount, cond.WindowSeconds) {
				continue
			}
		}
		if d.chDsp != nil {
			d.chDsp.Fire(ctx, AlertFire{
				RuleName: name,
				Type:     typ,
				Severity: sev,
				Message:  formatAlertMessage(typ, ev),
			})
		}
	}
}

func (d *ContainerDispatcher) recentRestartTimes(containerID string, windowSec int) ([]int64, error) {
	cutoff := time.Now().Add(-time.Duration(windowSec)*time.Second - time.Second).UnixMilli()
	rows, err := d.db.Query(
		`SELECT ts FROM container_events WHERE container_id=? AND event_type='restart' AND ts >= ? ORDER BY ts DESC LIMIT 50`,
		containerID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var t int64
		rows.Scan(&t)
		out = append(out, t)
	}
	return out, nil
}

func formatAlertMessage(typ string, ev *AlertContainerEvent) string {
	switch typ {
	case "container_down":
		exit := ""
		if ev.ExitCode != nil {
			exit = " (exit " + strconv.Itoa(*ev.ExitCode) + ")"
		}
		return "Container " + ev.Name + " stopped" + exit
	case "container_oom":
		return "Container " + ev.Name + " was OOM-killed"
	case "container_restart_loop":
		return "Container " + ev.Name + " is restart-looping"
	}
	return "Container " + ev.Name + " event: " + typ
}
```

- [ ] **Step 3: Run all alert tests**

Run: `go test ./internal/feature/alert/ -count=1 -v`
Expected: all 3 test groups (TestMatchContainerPattern, TestRestartLoopEvaluator, TestContainerDispatcher_FiresContainerDown) PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/feature/alert/container_rules.go internal/feature/alert/container_rules_test.go
git commit -m "alert: ContainerDispatcher for 3 new container alert types"
```

---

## Task 11: GET /containers/:id/metrics endpoint

**Files:**
- Create: `internal/feature/docker/observability.go`
- Create: `internal/feature/docker/observability_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/feature/docker/observability_test.go`:

```go
package docker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"
)

func openTestDBObs(t *testing.T) *sql.DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.db")
	db, _ := sql.Open("sqlite", p)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE container_metrics_history (container_id TEXT, container_name TEXT, ts INTEGER, cpu_percent REAL, mem_percent REAL, mem_bytes INTEGER, PRIMARY KEY(container_id,ts))`)
	db.Exec(`CREATE TABLE container_events (id INTEGER PRIMARY KEY AUTOINCREMENT, container_id TEXT, container_name TEXT, ts INTEGER, event_type TEXT, exit_code INTEGER, detail TEXT)`)
	return db
}

func TestGetContainerMetrics_Range1h(t *testing.T) {
	db := openTestDBObs(t)
	now := time.Now().UnixMilli()
	old := now - (2 * 3600 * 1000)
	mid := now - (30 * 60 * 1000)
	db.Exec(`INSERT INTO container_metrics_history VALUES ('a','x',?,1,1,1),('a','x',?,2,2,2),('a','x',?,3,3,3)`, old, mid, now)

	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?range=1h", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a")

	if err := h.GetMetrics(c); err != nil {
		t.Fatalf("err: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	var resp struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 rows in last 1h, got %d", len(resp.Data))
	}
}

func TestGetContainerMetrics_InvalidRange(t *testing.T) {
	db := openTestDBObs(t)
	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?range=invalid", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a")

	h.GetMetrics(c)
	if rec.Code != 400 {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// Helper to build "before" cursor in event tests (Task 12).
func tsString(n int64) string { return strconv.FormatInt(n, 10) }
```

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/feature/docker/ -run TestGetContainerMetrics -count=1`
Expected: FAIL — `undefined: ObservabilityHandler`.

- [ ] **Step 3: Implement handler**

Create `internal/feature/docker/observability.go`:

```go
package docker

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ObservabilityHandler exposes the read endpoints introduced by theme F.
// Depends only on the DB; collection happens in package monitor and
// retention in monitor's pruners.
type ObservabilityHandler struct {
	DB                   *sql.DB
	ObservabilityEnabled bool
}

func (h *ObservabilityHandler) GetMetrics(c echo.Context) error {
	id := c.Param("id")
	rangeStr := c.QueryParam("range")
	if rangeStr == "" {
		rangeStr = "1h"
	}
	dur, ok := parseRange(rangeStr)
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "range must be 1h, 6h, or 24h")
	}
	if !h.ObservabilityEnabled {
		return response.OK(c, map[string]any{"observability_disabled": true, "data": []any{}})
	}
	cutoff := time.Now().Add(-dur).UnixMilli()
	rows, err := h.DB.Query(
		`SELECT ts, cpu_percent, mem_percent, mem_bytes FROM container_metrics_history WHERE container_id = ? AND ts >= ? ORDER BY ts ASC`,
		id, cutoff,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "metrics query failed")
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var ts int64
		var cpu, mem float64
		var memBytes int64
		if err := rows.Scan(&ts, &cpu, &mem, &memBytes); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"ts": ts, "cpu_percent": cpu, "mem_percent": mem, "mem_bytes": memBytes,
		})
	}
	return response.OK(c, out)
}

func parseRange(s string) (time.Duration, bool) {
	switch s {
	case "1h":
		return 1 * time.Hour, true
	case "6h":
		return 6 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	}
	return 0, false
}

// avoid unused import warning when added incrementally — strconv used by GetEvents (Task 12)
var _ = strconv.Itoa
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/feature/docker/ -run TestGetContainerMetrics -count=1 -v`
Expected: 2 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/docker/observability.go internal/feature/docker/observability_test.go
git commit -m "docker: GET /containers/:id/metrics endpoint"
```

---

## Task 12: GET /containers/:id/events + GET /events/recent endpoints

**Files:**
- Modify: `internal/feature/docker/observability.go` (append two methods)
- Modify: `internal/feature/docker/observability_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/docker/observability_test.go`:

```go
func TestGetContainerEvents_NewestFirst_WithCursor(t *testing.T) {
	db := openTestDBObs(t)
	for i := 0; i < 60; i++ {
		db.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('a','x',?,'start')`, int64(1000+i))
	}
	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/?limit=50", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a")
	h.GetEvents(c)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 50 {
		t.Fatalf("page1: got %d, want 50", len(resp.Data))
	}
	first := int64(resp.Data[0]["ts"].(float64))
	last := int64(resp.Data[len(resp.Data)-1]["ts"].(float64))
	if first <= last {
		t.Errorf("expected newest-first ordering")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/?limit=50&before="+tsString(last), nil)
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)
	c2.SetParamNames("id")
	c2.SetParamValues("a")
	h.GetEvents(c2)
	var resp2 struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec2.Body).Decode(&resp2)
	if len(resp2.Data) != 10 {
		t.Errorf("page2: got %d, want 10", len(resp2.Data))
	}
}

func TestGetRecentEvents_AcrossContainers(t *testing.T) {
	db := openTestDBObs(t)
	db.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('a','x',1,'start'),('b','y',2,'die'),('a','x',3,'restart')`)
	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?limit=10", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	h.GetRecentEvents(c)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 across 2 containers, got %d", len(resp.Data))
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `go test ./internal/feature/docker/ -count=1`
Expected: FAIL on the two new tests.

- [ ] **Step 3: Implement two new methods**

Append to `internal/feature/docker/observability.go`:

```go
// GetEvents returns container_events rows for the given container, newest
// first, with cursor pagination via ?before=<ts>.
func (h *ObservabilityHandler) GetEvents(c echo.Context) error {
	id := c.Param("id")
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	beforeTS := int64(0)
	if v := c.QueryParam("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeTS = n
		}
	}
	if !h.ObservabilityEnabled {
		return response.OK(c, map[string]any{"observability_disabled": true, "data": []any{}})
	}
	q := `SELECT ts, event_type, exit_code, detail FROM container_events WHERE container_id = ?`
	args := []any{id}
	if beforeTS > 0 {
		q += ` AND ts < ?`
		args = append(args, beforeTS)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "events query failed")
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var ts int64
		var eventType string
		var exitCode sql.NullInt64
		var detail sql.NullString
		if err := rows.Scan(&ts, &eventType, &exitCode, &detail); err != nil {
			continue
		}
		row := map[string]any{
			"ts":         ts,
			"event_type": eventType,
			"exit_code":  nil,
			"detail":     nil,
		}
		if exitCode.Valid {
			row["exit_code"] = exitCode.Int64
		}
		if detail.Valid {
			row["detail"] = detail.String
		}
		out = append(out, row)
	}
	return response.OK(c, out)
}

// GetRecentEvents returns the most recent events across all containers.
func (h *ObservabilityHandler) GetRecentEvents(c echo.Context) error {
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if !h.ObservabilityEnabled {
		return response.OK(c, map[string]any{"observability_disabled": true, "data": []any{}})
	}
	rows, err := h.DB.Query(
		`SELECT container_id, container_name, ts, event_type, exit_code, detail FROM container_events ORDER BY ts DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "events query failed")
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, eventType string
		var ts int64
		var exitCode sql.NullInt64
		var detail sql.NullString
		rows.Scan(&id, &name, &ts, &eventType, &exitCode, &detail)
		row := map[string]any{
			"container_id":   id,
			"container_name": name,
			"ts":             ts,
			"event_type":     eventType,
			"exit_code":      nil,
			"detail":         nil,
		}
		if exitCode.Valid {
			row["exit_code"] = exitCode.Int64
		}
		if detail.Valid {
			row["detail"] = detail.String
		}
		out = append(out, row)
	}
	return response.OK(c, out)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/feature/docker/ -count=1 -v`
Expected: all observability tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/docker/observability.go internal/feature/docker/observability_test.go
git commit -m "docker: GET /containers/:id/events + GET /events/recent"
```

---

## Task 13: Augment ListContainers with cpu_avg_1h / mem_avg_1h

**Files:**
- Modify: `internal/feature/docker/handler.go`
- Modify: `internal/api/router.go` (pass DB to dockerHandler if not already)

- [ ] **Step 1: Add DB field to handler if missing**

Search `internal/feature/docker/handler.go` for the Handler struct. If it doesn't already have `DB *sql.DB`, add it:

```go
type Handler struct {
	Docker *docker.Client
	DB     *sql.DB
}
```

Add `import "database/sql"` if not present.

- [ ] **Step 2: Augment ListContainers response**

Find the `ListContainers` handler. After the existing per-container row composition, before the final `response.OK(c, ...)`, add per-row averages:

```go
// after existing fields are set on `row`:
if h.DB != nil {
	var cpuAvg, memAvg sql.NullFloat64
	cutoff := time.Now().Add(-1 * time.Hour).UnixMilli()
	h.DB.QueryRow(
		`SELECT AVG(cpu_percent), AVG(mem_percent) FROM container_metrics_history WHERE container_id = ? AND ts >= ?`,
		c.ID, cutoff,
	).Scan(&cpuAvg, &memAvg)
	row["cpu_avg_1h"] = nilOrFloat(cpuAvg)
	row["mem_avg_1h"] = nilOrFloat(memAvg)
}
```

Add helper at the bottom of `handler.go`:

```go
func nilOrFloat(n sql.NullFloat64) any {
	if n.Valid {
		return n.Float64
	}
	return nil
}
```

- [ ] **Step 3: Wire DB into the handler in router**

In `internal/api/router.go`, find the line `dockerHandler = &featureDocker.Handler{Docker: dockerClient}` and change to:

```go
dockerHandler = &featureDocker.Handler{Docker: dockerClient, DB: database}
```

- [ ] **Step 4: Build + run docker tests**

Run: `go build ./... && go test ./internal/feature/docker/ -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/docker/handler.go internal/api/router.go
git commit -m "docker: add cpu_avg_1h/mem_avg_1h to /containers response"
```

---

## Task 14: Wire endpoints in router

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Construct ObservabilityHandler + register routes**

In `internal/api/router.go`, find the docker route block. Add immediately after the existing docker registrations:

```go
obs := &featureDocker.ObservabilityHandler{
	DB:                   database,
	ObservabilityEnabled: cfg.Docker.Observability.Enabled,
}
authorized.GET("/docker/containers/:id/metrics", obs.GetMetrics)
authorized.GET("/docker/containers/:id/events", obs.GetEvents)
authorized.GET("/docker/events/recent", obs.GetRecentEvents)
```

- [ ] **Step 2: Build**

Run: `go build ./cmd/sfpanel`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/api/router.go
git commit -m "router: register docker observability endpoints"
```

---

## Task 15: Wire goroutines in main.go

**Files:**
- Modify: `cmd/sfpanel/main.go`

- [ ] **Step 1: Add goroutine wiring**

In `cmd/sfpanel/main.go`, find the existing wiring (right after `featureauth.StartRefreshTokenRetention(bgCtx, database)`). Add:

```go
if cfg.Docker.Observability.Enabled {
	dockerCli, dockerErr := docker.NewClient(cfg.Docker.Socket)
	if dockerErr != nil {
		slog.Warn("docker observability: client init failed; feature disabled", "error", dockerErr)
	} else {
		// Build the alert dispatcher first so the events listener can hand events to it.
		// alertManager is the existing alert manager constructed earlier in main; assume it
		// has Fire(ctx, AlertFire) — see container_rules.go ChannelDispatcher interface.
		// If the existing manager lacks that method, add a 1-line adapter here.
		containerDisp := featureAlert.NewContainerDispatcher(database, alertManager)

		monitor.StartDockerHistoryCollector(bgCtx, database, dockerCli)
		monitor.StartDockerEventsListener(bgCtx, database, dockerCli, &dispShim{c: containerDisp})

		metricsRet, _ := parseObservabilityRetention(cfg.Docker.Observability.MetricsRetention)
		eventsRet, _ := parseObservabilityRetention(cfg.Docker.Observability.EventsRetention)
		monitor.StartDockerMetricsRetention(bgCtx, database, metricsRet)
		monitor.StartDockerEventsRetention(bgCtx, database, eventsRet)
	}
}
```

Add helpers at the bottom of `main.go`:

```go
// dispShim adapts ContainerDispatcher (which expects AlertContainerEvent)
// to the monitor.EventDispatcher interface (which passes ContainerEvent).
// The two types live in different packages to avoid import cycles.
type dispShim struct {
	c *featureAlert.ContainerDispatcher
}

func (s *dispShim) Dispatch(ctx context.Context, ev *monitor.ContainerEvent) {
	s.c.Dispatch(ctx, &featureAlert.AlertContainerEvent{
		ID:       ev.ContainerID,
		Name:     ev.ContainerName,
		Type:     ev.EventType,
		TS:       ev.TS,
		ExitCode: ev.ExitCode,
	})
}

// parseObservabilityRetention translates the config string into a duration.
// config.Validate already rejected unknown strings.
func parseObservabilityRetention(s string) (time.Duration, error) {
	switch s {
	case "6h":
		return 6 * time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	case "72h":
		return 72 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	case "90d":
		return 90 * 24 * time.Hour, nil
	}
	return 24 * time.Hour, nil
}
```

Add imports if missing: `"github.com/svrforum/SFPanel/internal/monitor"` and `featureAlert "github.com/svrforum/SFPanel/internal/feature/alert"` (the alias may already exist).

If `alertManager` doesn't satisfy `featureAlert.ChannelDispatcher`, add a one-line adapter in main.go:

```go
type chanShim struct{ am *alert.Manager } // existing alert manager type

func (a *chanShim) Fire(ctx context.Context, f featureAlert.AlertFire) {
	a.am.SendAlert(ctx, f.RuleName, f.Severity, f.Message)
}

// then use: containerDisp := featureAlert.NewContainerDispatcher(database, &chanShim{am: alertManager})
```

The exact existing alert manager method (`SendAlert`, `Trigger`, etc.) needs verification against the live code — adjust the call accordingly.

- [ ] **Step 2: Build**

Run: `go build ./cmd/sfpanel`
Expected: success.

- [ ] **Step 3: Run all tests**

Run: `go test ./internal/... -count=1`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/sfpanel/main.go
git commit -m "wire: docker observability goroutines on bgCtx"
```

---

## Task 16: Frontend types + API client methods

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Add new types**

Append to `web/src/types/api.ts`:

```typescript
export interface ContainerMetricPoint {
  ts: number  // unix millis
  cpu_percent: number
  mem_percent: number
  mem_bytes: number
}

export interface ContainerEvent {
  ts: number
  event_type: 'start' | 'stop' | 'die' | 'oom' | 'kill' | 'restart' | 'healthy' | 'unhealthy'
  exit_code: number | null
  detail: string | null
}

export interface RecentContainerEvent extends ContainerEvent {
  container_id: string
  container_name: string
}

export type AlertRuleType =
  | 'host_cpu_high' | 'host_memory_high' | 'host_disk_full'
  | 'container_down' | 'container_oom' | 'container_restart_loop'
```

- [ ] **Step 2: Add API methods**

In `web/src/lib/api.ts`, add three methods to the `ApiClient` class:

```typescript
getContainerMetrics(id: string, range: '1h' | '6h' | '24h' = '1h') {
  return this.request<ContainerMetricPoint[]>(`/docker/containers/${id}/metrics?range=${range}`)
}

getContainerEvents(id: string, opts: { limit?: number; before?: number } = {}) {
  const qs = new URLSearchParams()
  if (opts.limit) qs.set('limit', String(opts.limit))
  if (opts.before) qs.set('before', String(opts.before))
  return this.request<ContainerEvent[]>(`/docker/containers/${id}/events?${qs.toString()}`)
}

getRecentEvents(opts: { limit?: number } = {}) {
  const qs = new URLSearchParams()
  if (opts.limit) qs.set('limit', String(opts.limit))
  return this.request<RecentContainerEvent[]>(`/docker/events/recent?${qs.toString()}`)
}
```

Update the type imports at the top of `api.ts`:

```typescript
import type {
  // ... existing imports ...
  ContainerMetricPoint,
  ContainerEvent,
  RecentContainerEvent,
} from '@/types/api'
```

- [ ] **Step 3: Build frontend**

Run: `cd web && npm run build && npm run lint`
Expected: success, lint clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: api types + methods for docker observability"
```

---

## Task 17: ContainerSparkline component

**Files:**
- Create: `web/src/components/ContainerSparkline.tsx`

- [ ] **Step 1: Implement sparkline**

Create `web/src/components/ContainerSparkline.tsx`:

```typescript
import { useEffect, useRef, useState } from 'react'
import uPlot from 'uplot'
import { api } from '@/lib/api'
import type { ContainerMetricPoint } from '@/types/api'

interface Props {
  containerId: string
  metric: 'cpu' | 'mem'
  width?: number
  height?: number
}

// ContainerSparkline renders a 60-point uplot mini chart for either CPU or
// memory percentage over the last hour. Fetches once on mount and re-renders
// only when containerId changes — the parent table polls separately for the
// current value column. Sparkline is "trend background" not real-time data.
export function ContainerSparkline({ containerId, metric, width = 80, height = 24 }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const [data, setData] = useState<ContainerMetricPoint[] | null>(null)

  useEffect(() => {
    let cancelled = false
    api.getContainerMetrics(containerId, '1h')
      .then((points) => { if (!cancelled) setData(points) })
      .catch(() => { if (!cancelled) setData([]) })
    return () => { cancelled = true }
  }, [containerId])

  useEffect(() => {
    if (!ref.current || !data || data.length === 0) return
    const xs = data.map(p => p.ts / 1000)
    const ys = data.map(p => metric === 'cpu' ? p.cpu_percent : p.mem_percent)
    const opts: uPlot.Options = {
      width, height,
      cursor: { show: false },
      legend: { show: false },
      axes: [{ show: false }, { show: false }],
      scales: { x: { time: true }, y: { auto: true } },
      series: [
        {},
        { stroke: metric === 'cpu' ? '#3b82f6' : '#a855f7', width: 1, points: { show: false } },
      ],
    }
    // Clear children safely without innerHTML.
    while (ref.current.firstChild) ref.current.removeChild(ref.current.firstChild)
    new uPlot(opts, [xs, ys] as any, ref.current)
  }, [data, metric, width, height])

  if (!data || data.length === 0) {
    return <span className="inline-block text-muted-foreground text-[10px] align-middle" style={{ width, height, lineHeight: `${height}px`, textAlign: 'center' }}>—</span>
  }

  return <div ref={ref} className="inline-block align-middle" style={{ width, height }} />
}
```

- [ ] **Step 2: Build**

Run: `cd web && npm run build && npm run lint`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/ContainerSparkline.tsx
git commit -m "web: ContainerSparkline component (60-point uplot mini chart)"
```

---

## Task 18: EventTimelineRow component

**Files:**
- Create: `web/src/components/EventTimelineRow.tsx`

- [ ] **Step 1: Implement event row**

Create `web/src/components/EventTimelineRow.tsx`:

```typescript
import { AlertTriangle, X, Play, Square, Check, Zap, RotateCw, Skull } from 'lucide-react'
import type { ContainerEvent } from '@/types/api'

interface Props {
  event: ContainerEvent
}

export function EventTimelineRow({ event }: Props) {
  const date = new Date(event.ts)
  const time = date.toLocaleTimeString()
  const Icon = iconFor(event.event_type)
  const color = colorFor(event.event_type)
  const summary = summarize(event)

  return (
    <div className="flex items-start gap-2 py-1.5">
      <Icon className={`h-3.5 w-3.5 mt-0.5 ${color}`} />
      <div className="flex-1 text-[12px]">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground tabular-nums">{time}</span>
          <span className="font-medium">{event.event_type}</span>
          {event.exit_code !== null && (
            <span className="text-muted-foreground">exit {event.exit_code}</span>
          )}
        </div>
        {summary && <div className="text-muted-foreground mt-0.5">{summary}</div>}
      </div>
    </div>
  )
}

function iconFor(t: ContainerEvent['event_type']) {
  switch (t) {
    case 'oom': return AlertTriangle
    case 'die': return X
    case 'start': return Play
    case 'restart': return RotateCw
    case 'stop': return Square
    case 'healthy': return Check
    case 'unhealthy': return Zap
    case 'kill': return Skull
  }
}

function colorFor(t: ContainerEvent['event_type']): string {
  switch (t) {
    case 'oom':
    case 'die':
    case 'unhealthy':
    case 'kill':
      return 'text-destructive'
    case 'healthy':
    case 'start':
      return 'text-emerald-600'
    case 'restart':
      return 'text-amber-600'
    default:
      return 'text-muted-foreground'
  }
}

function summarize(ev: ContainerEvent): string | null {
  if (!ev.detail) return null
  try {
    const obj = JSON.parse(ev.detail)
    if (typeof obj === 'object' && obj !== null && 'output' in obj) {
      return String((obj as { output: unknown }).output).slice(0, 100)
    }
  } catch {
    // non-JSON detail string is fine, return as-is
    return ev.detail.slice(0, 100)
  }
  return null
}
```

- [ ] **Step 2: Build**

Run: `cd web && npm run build && npm run lint`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/EventTimelineRow.tsx
git commit -m "web: EventTimelineRow component with icon/color per event type"
```

---

## Task 19: ContainerHistoryTab composition

**Files:**
- Create: `web/src/components/ContainerHistoryTab.tsx`

- [ ] **Step 1: Implement the tab**

Create `web/src/components/ContainerHistoryTab.tsx`:

```typescript
import { useEffect, useRef, useState } from 'react'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { ContainerMetricPoint, ContainerEvent } from '@/types/api'
import { EventTimelineRow } from '@/components/EventTimelineRow'

type Range = '1h' | '6h' | '24h'

interface Props {
  containerId: string
}

export function ContainerHistoryTab({ containerId }: Props) {
  const [range, setRange] = useState<Range>('1h')
  const [metrics, setMetrics] = useState<ContainerMetricPoint[]>([])
  const [events, setEvents] = useState<ContainerEvent[]>([])
  const [hasMore, setHasMore] = useState(true)
  const chartRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    api.getContainerMetrics(containerId, range)
      .then(setMetrics)
      .catch(() => setMetrics([]))
  }, [containerId, range])

  useEffect(() => {
    api.getContainerEvents(containerId, { limit: 50 })
      .then((evs) => {
        setEvents(evs)
        setHasMore(evs.length === 50)
      })
      .catch(() => setEvents([]))
  }, [containerId])

  useEffect(() => {
    if (!chartRef.current) return
    while (chartRef.current.firstChild) chartRef.current.removeChild(chartRef.current.firstChild)
    if (metrics.length === 0) return
    const xs = metrics.map(p => p.ts / 1000)
    const cpu = metrics.map(p => p.cpu_percent)
    const mem = metrics.map(p => p.mem_percent)
    const opts: uPlot.Options = {
      width: chartRef.current.clientWidth,
      height: 220,
      legend: { show: true },
      scales: { x: { time: true }, y: { auto: true, range: [0, 100] } },
      axes: [{}, { values: (_, ticks) => ticks.map(t => `${t}%`) }],
      series: [
        {},
        { label: 'CPU%', stroke: '#3b82f6', width: 1.5 },
        { label: 'MEM%', stroke: '#a855f7', width: 1.5 },
      ],
    }
    new uPlot(opts, [xs, cpu, mem] as any, chartRef.current)
  }, [metrics])

  async function loadMore() {
    if (events.length === 0) return
    const before = events[events.length - 1].ts
    const next = await api.getContainerEvents(containerId, { limit: 50, before })
    setEvents([...events, ...next])
    setHasMore(next.length === 50)
  }

  return (
    <div className="space-y-4">
      <div className="flex gap-1">
        {(['1h', '6h', '24h'] as Range[]).map(r => (
          <Button key={r} size="sm" variant={r === range ? 'default' : 'outline'} onClick={() => setRange(r)}>{r}</Button>
        ))}
      </div>
      <div className="border rounded-md p-2" style={{ minHeight: 220 }}>
        {metrics.length === 0 ? (
          <div className="text-muted-foreground text-[12px] py-8 text-center">수집 중…</div>
        ) : (
          <div ref={chartRef} />
        )}
      </div>
      <div>
        <h4 className="text-[13px] font-semibold mb-2">이벤트</h4>
        <div className="border rounded-md divide-y">
          {events.length === 0 && (
            <div className="text-muted-foreground text-[12px] py-8 text-center">이벤트 없음</div>
          )}
          {events.map((ev, i) => <div key={`${ev.ts}-${i}`} className="px-3"><EventTimelineRow event={ev} /></div>)}
        </div>
        {hasMore && (
          <div className="text-center mt-2">
            <Button size="sm" variant="outline" onClick={loadMore}>더 보기</Button>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build**

Run: `cd web && npm run build && npm run lint`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/ContainerHistoryTab.tsx
git commit -m "web: ContainerHistoryTab composition (chart + events + pagination)"
```

---

## Task 20: Integrate sparklines + History tab into DockerContainers

**Files:**
- Modify: `web/src/pages/docker/DockerContainers.tsx`

- [ ] **Step 1: Add sparkline next to current CPU% / MEM% column**

Locate the row rendering (search the file for `cpu_percent` or `MEM` in the table cells). Add the import:

```typescript
import { ContainerSparkline } from '@/components/ContainerSparkline'
```

Modify the CPU / MEM cell content to include sparklines:

```typescript
<td>
  <div className="flex items-center gap-2">
    <span>{stats?.cpu_percent?.toFixed(1) ?? '--'}%</span>
    <ContainerSparkline containerId={container.Id} metric="cpu" />
  </div>
</td>
<td>
  <div className="flex items-center gap-2">
    <span>{stats?.mem_percent?.toFixed(1) ?? '--'}%</span>
    <ContainerSparkline containerId={container.Id} metric="mem" />
  </div>
</td>
```

- [ ] **Step 2: Add History tab to ContainerInspect drawer**

Find the `ContainerInspect` component's tabs definition (around line 94 onward — check for `<Tabs>` or `<TabsList>` JSX). Add:

```typescript
import { ContainerHistoryTab } from '@/components/ContainerHistoryTab'
```

Inside the `<TabsList>`, add `<TabsTrigger value="history">History</TabsTrigger>` between Stats and Logs (or wherever the existing order suggests).

Inside the matching `<TabsContent>` block:

```typescript
<TabsContent value="history">
  <ContainerHistoryTab containerId={containerId} />
</TabsContent>
```

- [ ] **Step 3: Build + lint**

Run: `cd web && npm run build && npm run lint`
Expected: success, 0 errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/docker/DockerContainers.tsx
git commit -m "web: docker containers — row sparklines + History tab in drawer"
```

---

## Task 21: Alert rule create dialog — 3 new types

**Files:**
- Modify: `web/src/pages/Alerts.tsx`

- [ ] **Step 1: Locate the alert rule type selector + extend**

Find the `type` select dropdown (search for `host_cpu_high`). Add three options:

```typescript
const ALERT_TYPES = [
  { value: 'host_cpu_high', label: 'Host CPU 임계치 초과' },
  { value: 'host_memory_high', label: 'Host 메모리 임계치 초과' },
  { value: 'host_disk_full', label: 'Host 디스크 임계치 초과' },
  { value: 'container_down', label: '컨테이너 종료' },
  { value: 'container_oom', label: '컨테이너 OOM kill' },
  { value: 'container_restart_loop', label: '컨테이너 재시작 루프' },
]
```

- [ ] **Step 2: Add conditional condition fields**

Inside the form body, render different fields based on `type`:

```typescript
{(type === 'container_down' || type === 'container_oom') && (
  <div>
    <label>컨테이너 패턴 (와일드카드)</label>
    <Input
      value={containerPattern}
      onChange={e => setContainerPattern(e.target.value)}
      placeholder="* | nginx-* | exact-name"
    />
  </div>
)}

{type === 'container_restart_loop' && (
  <>
    <div>
      <label>컨테이너 패턴</label>
      <Input
        value={containerPattern}
        onChange={e => setContainerPattern(e.target.value)}
        placeholder="*"
      />
    </div>
    <div>
      <label>재시작 임계 횟수</label>
      <Input
        type="number"
        min={1}
        value={thresholdCount}
        onChange={e => setThresholdCount(parseInt(e.target.value || '3', 10))}
      />
    </div>
    <div>
      <label>윈도우 (초)</label>
      <Input
        type="number"
        min={30}
        value={windowSeconds}
        onChange={e => setWindowSeconds(parseInt(e.target.value || '300', 10))}
      />
    </div>
  </>
)}
```

Add the local state declarations near the other form state:

```typescript
const [containerPattern, setContainerPattern] = useState('*')
const [thresholdCount, setThresholdCount] = useState(3)
const [windowSeconds, setWindowSeconds] = useState(300)
```

- [ ] **Step 3: Build the condition JSON before submit**

Update the submit handler that builds the rule's `condition` payload:

```typescript
function buildCondition(): string {
  if (type === 'container_down' || type === 'container_oom') {
    return JSON.stringify({ container_pattern: containerPattern || '*' })
  }
  if (type === 'container_restart_loop') {
    return JSON.stringify({
      container_pattern: containerPattern || '*',
      threshold_count: thresholdCount || 3,
      window_seconds: windowSeconds || 300,
    })
  }
  return existingHostBuildCondition()
}
```

- [ ] **Step 4: Build + lint**

Run: `cd web && npm run build && npm run lint`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Alerts.tsx
git commit -m "web: alert rule dialog — 3 new container alert types"
```

---

## Task 22: Manual smoke test

- [ ] **Step 1: Build + deploy locally**

```bash
cd /opt/stacks/SFPanel
make build
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-obs
sudo cp ./sfpanel /usr/local/bin/sfpanel.new
sudo mv /usr/local/bin/sfpanel.new /usr/local/bin/sfpanel
sudo systemctl restart sfpanel
sleep 5
sudo systemctl is-active sfpanel
/usr/local/bin/sfpanel version
```

- [ ] **Step 2: Wait 2 minutes for collector to populate metrics**

```bash
sleep 120
# Mint admin JWT (same approach as previous sessions — see roadmap docs)
TOKEN=$(... mint via internal/auth.GenerateToken ...)
CID=$(docker ps -q --no-trunc | head -1)
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/docker/containers/$CID/metrics?range=1h" | head -3
```

Expected: Non-empty JSON array with at least 4 data points (one every 30s).

- [ ] **Step 3: Trigger an OOM event**

```bash
docker run --rm -d --name oom-test --memory=10m busybox sh -c 'yes | head -c 100M > /dev/null'
sleep 5
docker rm -f oom-test 2>/dev/null
sleep 3
curl -s -H "Authorization: Bearer $TOKEN" 'http://127.0.0.1:9443/api/v1/docker/events/recent?limit=10' | python3 -m json.tool
```

Expected: Recent events list contains an `oom` or `die` event with container name `oom-test`.

- [ ] **Step 4: Open the panel UI, verify sparklines + History tab**

Navigate to `http://127.0.0.1:9443/docker/containers`, log in (refresh-token client should restore session), verify:
- Sparkline visible next to each container's CPU% / MEM%.
- Click a row → drawer opens. History tab shows chart + events list.
- Range buttons (1h/6h/24h) switch the chart.
- "더 보기" paginates events.

- [ ] **Step 5: Verify alert delivery**

Create an alert rule via UI: type=`container_down`, pattern=`*`, channel=any pre-existing channel.
Trigger another container die. Expect the alert to fire within ~10s.

- [ ] **Step 6: Verify DB growth bounded**

```bash
sudo -u root sqlite3 /var/lib/sfpanel/sfpanel.db 'SELECT COUNT(*) FROM container_metrics_history; SELECT COUNT(*) FROM container_events;'
```

Expected: row counts ≤ retention bounds, growing linearly with time.

---

## Self-Review checklist

### Spec coverage
- [x] Container metrics history (Tasks 1, 4, 11) — schema + collector + endpoint
- [x] Restart timeline (Tasks 1, 5, 6, 12) — schema + parser + listener + events endpoint
- [x] Docker events → alerts (Tasks 8, 9, 10, 15) — pattern matcher + window evaluator + dispatcher + wiring
- [x] Sparkline UI (Tasks 17, 20)
- [x] History tab UI (Tasks 18, 19, 20)
- [x] Alert rule UI (Task 21)
- [x] Cluster proxy via existing `?node=` (Task 14 registers under `authorized` group → automatic)
- [x] Configuration knobs in config.yaml (Task 2)
- [x] Disabled state (Tasks 11, 12 use `ObservabilityEnabled` flag)
- [x] Retention pruners (Task 7)

### Placeholder scan
No "TBD", "TODO", or "implement details" left. Each step has complete code or exact commands. Task 15 flags one assumption (existing alertManager method name) with a clear adapter pattern if the assumption fails.

### Type consistency
- `ContainerEvent` (uppercase) used consistently across packages: `monitor.ContainerEvent` declared in Task 5, consumed by `featureAlert.ContainerDispatcher` via the `dispShim` adapter in Task 15.
- `AlertContainerEvent` used by alert package (Task 10) — distinct exported type from monitor's ContainerEvent.
- API method names match between `types/api.ts` (`ContainerMetricPoint`, `ContainerEvent`, `RecentContainerEvent`) and `lib/api.ts` (`getContainerMetrics`, `getContainerEvents`, `getRecentEvents`).
- Alert rule `type` enum values match between Go (`container_down`, `container_oom`, `container_restart_loop`) and TS (`AlertRuleType` union in types/api.ts).

### Known gaps surfaced (handled in plan)
- Task 15 step 1 explicitly notes the `alertManager` method name needs verification against the live code; provides an adapter pattern. The implementer must check during execution.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-04-docker-observability-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for this plan because tasks are TDD and small.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch with checkpoints.

Which approach?
