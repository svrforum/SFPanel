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
