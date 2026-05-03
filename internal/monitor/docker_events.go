package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"time"

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
	t := normalizeAction(string(m.Action))
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

// Tunables for the events listener reconnect loop. These are vars (not consts)
// so tests can override them to keep run-time short. Production code never
// mutates them.
var (
	eventsListenerInitialBackoff   = 1 * time.Second
	eventsListenerMaxBackoff       = 5 * time.Minute
	eventsListenerSuccessThreshold = 1 * time.Minute
)

func runEventsListener(ctx context.Context, db *sql.DB, client DockerEventsClient, disp EventDispatcher) {
	backoff := eventsListenerInitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := streamOnce(ctx, db, client, disp)
		if ctx.Err() != nil {
			return
		}
		// If the stream stayed up long enough to be considered "healthy",
		// reset the backoff so the next reconnect is fast — otherwise a
		// burst of early failures would leave us at the 5-minute cap even
		// after hours of successful operation.
		if time.Since(started) >= eventsListenerSuccessThreshold {
			backoff = eventsListenerInitialBackoff
		}
		slog.Warn("docker events: stream ended, reconnecting", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > eventsListenerMaxBackoff {
			backoff = eventsListenerMaxBackoff
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
				safeDispatch(ctx, disp, ev)
			}
		}
	}
}

// safeDispatch isolates dispatcher panics so a buggy alert rule (Tasks 8–10
// will plug into this hook) cannot kill the events listener goroutine.
func safeDispatch(ctx context.Context, disp EventDispatcher, ev *ContainerEvent) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("docker events: dispatcher panicked", "panic", r, "container", ev.ContainerID, "event", ev.EventType)
		}
	}()
	disp.Dispatch(ctx, ev)
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
