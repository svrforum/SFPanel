package cluster

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

// CommandType identifies the type of Raft log entry.
type CommandType uint8

const (
	CmdAddNode    CommandType = iota + 1
	CmdRemoveNode
	CmdUpdateNode
	CmdSetConfig
	CmdDeleteConfig
)

// Command is the payload applied to the Raft FSM.
type Command struct {
	Type  CommandType     `json:"type"`
	Key   string          `json:"key,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// FSM implements raft.FSM to manage cluster state.
type FSM struct {
	mu    sync.RWMutex
	state ClusterState
}

func NewFSM() *FSM {
	return &FSM{
		state: ClusterState{
			Nodes:  make(map[string]*Node),
			Config: make(map[string]string),
		},
	}
}

// Apply a Raft log entry to the FSM.
func (f *FSM) Apply(l *raft.Log) interface{} {
	var cmd Command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		return fmt.Errorf("unmarshal command: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch cmd.Type {
	case CmdAddNode:
		var node Node
		if err := json.Unmarshal(cmd.Value, &node); err != nil {
			return err
		}
		f.state.Nodes[node.ID] = &node
		return nil

	case CmdRemoveNode:
		delete(f.state.Nodes, cmd.Key)
		return nil

	case CmdUpdateNode:
		var update Node
		if err := json.Unmarshal(cmd.Value, &update); err != nil {
			return err
		}
		if existing, ok := f.state.Nodes[update.ID]; ok {
			if update.Status != "" {
				existing.Status = update.Status
			}
			if update.Role != "" {
				existing.Role = update.Role
			}
			if update.Labels != nil {
				existing.Labels = update.Labels
			}
			if update.APIAddress != "" {
				existing.APIAddress = update.APIAddress
			}
			if update.GRPCAddress != "" {
				existing.GRPCAddress = update.GRPCAddress
			}
			existing.LastSeen = time.Now()
		}
		return nil

	case CmdSetConfig:
		var val string
		if err := json.Unmarshal(cmd.Value, &val); err != nil {
			return err
		}
		f.state.Config[cmd.Key] = val
		return nil

	case CmdDeleteConfig:
		delete(f.state.Config, cmd.Key)
		return nil

	default:
		return fmt.Errorf("unknown command type: %d", cmd.Type)
	}
}

// Snapshot returns an FSM snapshot.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	data, err := json.Marshal(f.state)
	if err != nil {
		return nil, err
	}
	return &fsmSnapshot{data: data}, nil
}

// Restore restores the FSM from a snapshot.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	var state ClusterState
	if err := json.NewDecoder(rc).Decode(&state); err != nil {
		return err
	}

	f.mu.Lock()
	f.state = state
	f.mu.Unlock()
	return nil
}

// GetState returns a copy of the current cluster state.
func (f *FSM) GetState() ClusterState {
	f.mu.RLock()
	defer f.mu.RUnlock()

	nodes := make(map[string]*Node, len(f.state.Nodes))
	for k, v := range f.state.Nodes {
		n := *v
		nodes[k] = &n
	}
	config := make(map[string]string, len(f.state.Config))
	for k, v := range f.state.Config {
		config[k] = v
	}
	return ClusterState{
		Name:   f.state.Name,
		Nodes:  nodes,
		Config: config,
	}
}

// GetNode returns a specific node, or nil.
func (f *FSM) GetNode(id string) *Node {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if n, ok := f.state.Nodes[id]; ok {
		copy := *n
		return &copy
	}
	return nil
}

type fsmSnapshot struct {
	data []byte
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s.data); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}
