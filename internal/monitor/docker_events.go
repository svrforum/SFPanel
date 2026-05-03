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
