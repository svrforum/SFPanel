package cluster

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

func testServerAgentSettings() serveragents.Settings {
	return serveragents.Settings{
		Zabbix: serveragents.ZabbixSettings{
			Server:       "192.0.2.10",
			ServerActive: "zbx.example.net:10051",
			HostMetadata: "linux",
		},
		Wazuh: serveragents.WazuhSettings{
			Manager: "192.0.2.11",
			Groups:  "default",
			Version: serveragents.DefaultWazuhVersion,
		},
		UpdatedAt: time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC),
		UpdatedBy: "admin",
	}
}

// The connection values are written through the leader, under the one key
// agents, and every manager reads its own replicated copy.
func TestServerAgentSettingsAreWrittenByTheLeaderAndReadEverywhere(t *testing.T) {
	mgrs := inmemManagers(t, 2)
	var leader, follower *Manager
	waitFor(t, "a leader", func() bool {
		for i, m := range mgrs {
			if m.raft.IsLeader() {
				leader, follower = m, mgrs[1-i]
				return true
			}
		}
		return false
	})
	s := testServerAgentSettings()
	if err := leader.PutServerAgentSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := follower.PutServerAgentSettings(s); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("PutServerAgentSettings on a follower: %v, want ErrNotLeader", err)
	}
	waitFor(t, "the follower to read the values", func() bool {
		got, err := follower.ServerAgentSettings()
		return err == nil && got != nil && reflect.DeepEqual(*got, s)
	})
	if got, err := leader.ServerAgentSettings(); err != nil || got == nil || !reflect.DeepEqual(*got, s) {
		t.Fatalf("the leader reads %+v, %v; want %+v", got, err, s)
	}
	stored := leader.raft.GetFSM().ConfigWithPrefix("agents")
	if keys := slices.Sorted(maps.Keys(stored)); !slices.Equal(keys, []string{"agents"}) {
		t.Fatalf("replicated keys under agents: %v, want exactly [agents]", keys)
	}
	if err := (&Manager{}).PutServerAgentSettings(s); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("PutServerAgentSettings with no Raft: %v, want ErrNotLeader", err)
	}
}

// Values never saved read as none, not as an error; with no Raft there is
// nothing to read.
func TestUnsavedServerAgentSettingsReadAsNil(t *testing.T) {
	got, err := taskLeader(t).ServerAgentSettings()
	if err != nil || got != nil {
		t.Fatalf("ServerAgentSettings() before any save = %+v, %v; want nil, nil", got, err)
	}
	if _, err := (&Manager{}).ServerAgentSettings(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("ServerAgentSettings() with no Raft: %v, want ErrNotInitialized", err)
	}
}

// A replicated value that does not decode is an error: read as nil it would
// say the values were never saved.
func TestUnreadableServerAgentSettingsAreAnError(t *testing.T) {
	m := taskLeader(t)
	if err := m.SetConfig(configKeyServerAgents, "{not json"); err != nil {
		t.Fatal(err)
	}
	got, err := m.ServerAgentSettings()
	if err == nil || got != nil {
		t.Fatalf("ServerAgentSettings() of an unreadable value = %+v, %v; want an error", got, err)
	}
}

func TestEveryNodeSaysItRunsAgentSteps(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if caps := m.heartbeatCapabilities(); !slices.Contains(caps, CapServerAgentStep) {
		t.Fatalf("a manager's heartbeat capabilities %v lack %s", caps, CapServerAgentStep)
	}
	l := &AgentLink{AgentLinkConfig: AgentLinkConfig{NodeID: "a-1", TLS: NewTLSManager(t.TempDir())}}
	if caps := l.ping().Capabilities; !slices.Contains(caps, CapServerAgentStep) {
		t.Fatalf("an agent's ping capabilities %v lack %s", caps, CapServerAgentStep)
	}
}

// A manager that runs task jobs but not agent steps holds back a run that
// has one, named with its version; TasksReady, which asks for task jobs
// only, does not wait for it.
func TestTasksReadyForNamesManagersWithoutTheCapability(t *testing.T) {
	m := soleLeader(t, "m2")
	m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "0.97.0", Capabilities: []string{CapTasks}})
	if got := m.TasksReadyFor(CapServerAgentStep); !slices.Equal(got, []string{"m2 (0.97.0)"}) {
		t.Fatalf("TasksReadyFor(%s) = %v, want m2 named with its version", CapServerAgentStep, got)
	}
	if got := m.TasksReady(); len(got) != 0 {
		t.Fatalf("TasksReady() = %v for a manager that reports %s", got, CapTasks)
	}
	m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "0.98.0", Capabilities: []string{CapTasks, CapServerAgentStep}})
	if got := m.TasksReadyFor(CapServerAgentStep); len(got) != 0 {
		t.Fatalf("TasksReadyFor(%s) = %v once every manager reports it", CapServerAgentStep, got)
	}
	if got := (&Manager{}).TasksReadyFor(CapServerAgentStep); got != nil {
		t.Fatalf("TasksReadyFor with no Raft = %v, want none", got)
	}
}
