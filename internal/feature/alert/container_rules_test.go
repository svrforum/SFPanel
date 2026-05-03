package alert

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

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
