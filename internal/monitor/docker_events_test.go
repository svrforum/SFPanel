package monitor

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
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
	mu  sync.Mutex
	got []*ContainerEvent
}

func (d *fakeDispatcher) Dispatch(_ context.Context, ev *ContainerEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
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
		// Both msgs/errs channels are buffered, so without a sync point
		// streamOnce's select can race between picking the next msg vs
		// returning EOF. Wait until the dispatcher records both events
		// before signalling stream end.
		for {
			disp.mu.Lock()
			n := len(disp.got)
			disp.mu.Unlock()
			if n >= 2 {
				break
			}
			time.Sleep(time.Millisecond)
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
	disp.mu.Lock()
	dispatched := len(disp.got)
	disp.mu.Unlock()
	if dispatched != 2 {
		t.Errorf("dispatched: got %d, want 2", dispatched)
	}
}

// scriptedEventsClient produces a different (msgs, errs) pair per call to
// Events(), driven by a per-connection callback. This lets a single
// runEventsListener loop see distinct stream lifetimes (e.g. healthy stream,
// then immediate EOF) without sharing channels across reconnects.
type scriptedEventsClient struct {
	scripts []func(msgs chan<- events.Message, errs chan<- error)
	calls   atomic.Int32
}

func (s *scriptedEventsClient) Events(ctx context.Context, _ events.ListOptions) (<-chan events.Message, <-chan error) {
	msgs := make(chan events.Message, 4)
	errs := make(chan error, 1)
	idx := int(s.calls.Add(1) - 1)
	if idx >= len(s.scripts) {
		// No more scripted behavior — block until ctx ends, then EOF.
		go func() {
			<-ctx.Done()
			errs <- io.EOF
		}()
		return msgs, errs
	}
	go s.scripts[idx](msgs, errs)
	return msgs, errs
}

func TestRunEventsListener_BackoffResetsAfterSuccess(t *testing.T) {
	// Override tunables so the test runs in well under a second. The reset
	// threshold is 100ms — far above any scheduling jitter we expect on CI
	// — and the initial backoff is 10ms so a "reset" reconnect completes
	// quickly while a "doubled" reconnect (20ms after the first 10ms) is
	// still measurable.
	prevInitial, prevMax, prevThreshold := eventsListenerInitialBackoff, eventsListenerMaxBackoff, eventsListenerSuccessThreshold
	eventsListenerInitialBackoff = 10 * time.Millisecond
	eventsListenerMaxBackoff = 200 * time.Millisecond
	eventsListenerSuccessThreshold = 100 * time.Millisecond
	t.Cleanup(func() {
		eventsListenerInitialBackoff = prevInitial
		eventsListenerMaxBackoff = prevMax
		eventsListenerSuccessThreshold = prevThreshold
	})

	db := openTestDBForEvents(t)

	// Record the wall-clock time at which Events() is called for each
	// reconnect. The gap between call N and call N+1 = (stream N run-time)
	// + (sleep before reconnect). For our success-after-long-stream test
	// to pass, the gap between calls 2 and 3 must be ≈ initial backoff
	// (10ms), not doubled (20ms+) — proving the reset happened.
	var callTimesMu sync.Mutex
	var callTimes []time.Time
	recordCall := func() {
		callTimesMu.Lock()
		callTimes = append(callTimes, time.Now())
		callTimesMu.Unlock()
	}

	healthyStream := func(msgs chan<- events.Message, errs chan<- error) {
		recordCall()
		msgs <- events.Message{
			Type: events.ContainerEventType, Action: "start",
			Time: 1714742400, TimeNano: 1714742400_000_000_000,
			Actor: events.Actor{ID: "a", Attributes: map[string]string{"name": "x"}},
		}
		// Stay "up" longer than the success threshold.
		time.Sleep(150 * time.Millisecond)
		errs <- io.EOF
	}
	immediateEOF := func(_ chan<- events.Message, errs chan<- error) {
		recordCall()
		errs <- io.EOF
	}

	sc := &scriptedEventsClient{
		scripts: []func(chan<- events.Message, chan<- error){
			immediateEOF,  // call 0: short stream — backoff doubles to 20ms
			healthyStream, // call 1: long stream (≥ 100ms) — backoff resets after this
			immediateEOF,  // call 2: short stream after healthy one — sleep should be 10ms
			immediateEOF,  // call 3: stop here
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		runEventsListener(ctx, db, sc, nil)
		close(done)
	}()

	// Wait for at least 4 Events() calls or context expiry.
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sc.calls.Load() >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	callTimesMu.Lock()
	snapshot := append([]time.Time(nil), callTimes...)
	callTimesMu.Unlock()
	if len(snapshot) < 4 {
		t.Fatalf("expected ≥ 4 Events() calls, got %d", len(snapshot))
	}

	// After the healthy stream (call index 1), backoff resets to 10ms. The
	// stream at call 2 EOF's immediately, so the gap from call-2-start to
	// call-3-start should be ≈ initial backoff (10ms). With doubling (no
	// reset) it would be ≥ 40ms (10→20→40 across the three short streams).
	// Allow ample slack for scheduler jitter under -race.
	gapAfterHealthy := snapshot[3].Sub(snapshot[2])
	if gapAfterHealthy > 25*time.Millisecond {
		t.Errorf("backoff did not reset: gap after healthy stream = %v, want ≤ ~25ms (initial backoff is 10ms)", gapAfterHealthy)
	}
}

// panickingDispatcher panics on its first Dispatch call, then records all
// subsequent events. Used to verify safeDispatch isolates the panic.
type panickingDispatcher struct {
	mu    sync.Mutex
	calls int
	got   []*ContainerEvent
}

func (d *panickingDispatcher) Dispatch(_ context.Context, ev *ContainerEvent) {
	d.mu.Lock()
	d.calls++
	calls := d.calls
	d.mu.Unlock()
	if calls == 1 {
		panic("synthetic dispatcher panic")
	}
	d.mu.Lock()
	d.got = append(d.got, ev)
	d.mu.Unlock()
}

func (d *panickingDispatcher) snapshot() (int, []*ContainerEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls, append([]*ContainerEvent(nil), d.got...)
}

func TestStreamOnce_DispatcherPanicDoesNotKillListener(t *testing.T) {
	db := openTestDBForEvents(t)
	fc := &fakeEventsClient{msgs: make(chan events.Message, 4), errs: make(chan error, 1)}
	disp := &panickingDispatcher{}

	go func() {
		fc.msgs <- events.Message{
			Type: events.ContainerEventType, Action: "start",
			Time: 1714742400, TimeNano: 1714742400_000_000_000,
			Actor: events.Actor{ID: "a", Attributes: map[string]string{"name": "x"}},
		}
		fc.msgs <- events.Message{
			Type: events.ContainerEventType, Action: "die",
			Time: 1714742410, TimeNano: 1714742410_000_000_000,
			Actor: events.Actor{ID: "a", Attributes: map[string]string{"name": "x", "exitCode": "1"}},
		}
		// Block EOF until streamOnce has actually delivered both messages
		// to the dispatcher; otherwise the select can race between picking
		// the next msg vs returning EOF (both channels are buffered).
		for {
			calls, _ := disp.snapshot()
			if calls >= 2 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		fc.errs <- io.EOF
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// If the panic propagates, this call would panic and fail the test.
	err := streamOnce(ctx, db, fc, disp)
	if err != io.EOF {
		t.Errorf("expected io.EOF after stream end, got %v", err)
	}

	calls, got := disp.snapshot()
	if calls != 2 {
		t.Errorf("dispatcher called %d times, want 2", calls)
	}
	// First call panicked → not recorded. Second call should be recorded.
	if len(got) != 1 {
		t.Fatalf("expected 1 recorded event after panic recovery, got %d", len(got))
	}
	if got[0].EventType != "die" {
		t.Errorf("recorded event type = %q, want %q", got[0].EventType, "die")
	}

	// Both events should still have been persisted (persistence runs before
	// dispatch, so the panic doesn't affect the DB row).
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM container_events`).Scan(&n)
	if n != 2 {
		t.Errorf("persisted: got %d, want 2", n)
	}
}
