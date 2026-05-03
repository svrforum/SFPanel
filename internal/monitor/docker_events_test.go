package monitor

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	_ "modernc.org/sqlite"
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
