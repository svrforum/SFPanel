package cluster

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

// configKeyServerAgents holds the connection values every server's Zabbix
// and Wazuh agents are set to, JSON serveragents.Settings: one value for the
// whole cluster, the values a task run's agent steps are filled with, written
// with CmdSetConfig so that a manager of any version applies it.
const configKeyServerAgents = "agents"

// ServerAgentSettings returns the agents' connection values from this
// manager's own replicated state: nil, nil when they were never saved, and
// ErrNotInitialized on a node with no Raft. A value that does not decode is
// an error, not nil: read as nil it would say the values were never saved.
func (m *Manager) ServerAgentSettings() (*serveragents.Settings, error) {
	if m.raft == nil {
		return nil, ErrNotInitialized
	}
	v, ok := m.raft.GetFSM().GetConfigValue(configKeyServerAgents)
	if !ok {
		return nil, nil
	}
	var s serveragents.Settings
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		slog.Warn("unreadable server agent settings in replicated state", "component", "cluster", "key", configKeyServerAgents, "error", err)
		return nil, fmt.Errorf("server agent settings: %w", err)
	}
	return &s, nil
}

// PutServerAgentSettings replaces the agents' connection values (leader
// only; a node with no Raft is not the leader). It stores what it is given:
// the caller normalizes and validates.
func (m *Manager) PutServerAgentSettings(s serveragents.Settings) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return m.putTaskValue(configKeyServerAgents, string(data))
}
