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
