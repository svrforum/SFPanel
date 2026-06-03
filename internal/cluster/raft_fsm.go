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
	CmdAddNode       CommandType = iota + 1
	CmdRemoveNode
	CmdUpdateNode
	CmdSetConfig
	CmdDeleteConfig
	CmdSetAccount
	CmdDeleteAccount
	// CmdDisband is applied by the leader to notify every node that the
	// cluster has been dissolved. Each node's FSM.Apply fires the
	// registered onDisband callback; the callback is responsible for
	// local cleanup (wiping cluster material, flipping config, exiting).
	// cmd.Key carries the node ID that initiated the disband.
	CmdDisband
	// CmdSetRecoveryCodes replaces the 2FA recovery-code hash list for the
	// account named by cmd.Key (value = JSON []string of hashes). Decoupled
	// from CmdSetAccount so a password/TOTP change (which replaces the whole
	// AdminAccount) can't wipe the recovery codes. MUST stay last in this iota
	// block — inserting earlier would renumber existing commands and corrupt
	// replay of persisted Raft logs.
	CmdSetRecoveryCodes
)

// AdminAccount represents a cluster-synced user account.
type AdminAccount struct {
	Username   string `json:"username"`
	Password   string `json:"password"`     // bcrypt hash
	TOTPSecret string `json:"totp_secret"`  // base32-encoded, empty if not set
	UpdatedAt  int64  `json:"updated_at"`   // unix timestamp
}

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

	// onDisband is invoked (in a goroutine) when a CmdDisband entry is
	// applied. Set once at Manager wire-up; never changed at runtime.
	onDisband func(fromNodeID string)

	// replayUpTo is the highest Raft log index that already existed when this
	// process started. CmdDisband entries at or below it are being REPLAYED
	// from the persisted log (not freshly applied), so their teardown side
	// effect must be suppressed — otherwise a node that booted with a stale
	// committed disband in its log would self-destruct on every restart.
	// Live disbands (applied after startup) carry Index > replayUpTo. Set
	// once before raft.NewRaft begins replay; 0 means "treat all as live".
	replayUpTo uint64
}

func NewFSM() *FSM {
	return &FSM{
		state: ClusterState{
			Nodes:         make(map[string]*Node),
			Config:        make(map[string]string),
			Accounts:      make(map[string]*AdminAccount),
			RecoveryCodes: make(map[string][]string),
		},
	}
}

// SetOnDisband registers the callback invoked on every CmdDisband apply.
// Call once before the Raft loop starts replaying log entries.
func (f *FSM) SetOnDisband(cb func(fromNodeID string)) {
	f.mu.Lock()
	f.onDisband = cb
	f.mu.Unlock()
}

// SetReplayThreshold records the highest log index present at startup so that
// CmdDisband entries replayed from the persisted log (Index <= threshold) do
// not re-trigger local teardown. Call once, before raft.NewRaft begins replay.
func (f *FSM) SetReplayThreshold(idx uint64) {
	f.mu.Lock()
	f.replayUpTo = idx
	f.mu.Unlock()
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
			// Only update LastSeen for online status or explicit timestamp.
			// Avoid overwriting with "now" when marking a node offline/suspect.
			if !update.LastSeen.IsZero() {
				existing.LastSeen = update.LastSeen
			} else if update.Status == StatusOnline || update.Status == "" {
				existing.LastSeen = time.Now()
			}
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

	case CmdSetAccount:
		var acct AdminAccount
		if err := json.Unmarshal(cmd.Value, &acct); err != nil {
			return err
		}
		if f.state.Accounts == nil {
			f.state.Accounts = make(map[string]*AdminAccount)
		}
		f.state.Accounts[acct.Username] = &acct
		return nil

	case CmdDeleteAccount:
		if f.state.Accounts != nil {
			delete(f.state.Accounts, cmd.Key)
		}
		return nil

	case CmdSetRecoveryCodes:
		var codes []string
		if err := json.Unmarshal(cmd.Value, &codes); err != nil {
			return err
		}
		if f.state.RecoveryCodes == nil {
			f.state.RecoveryCodes = make(map[string][]string)
		}
		if len(codes) == 0 {
			delete(f.state.RecoveryCodes, cmd.Key)
		} else {
			f.state.RecoveryCodes[cmd.Key] = codes
		}
		return nil

	case CmdDisband:
		// Suppress the teardown side effect for entries that are merely being
		// REPLAYED from the persisted log at startup (Index <= replayUpTo).
		// Without this, a node that booted with a stale committed CmdDisband
		// in its log would wipe state and exit on every restart. A live
		// disband (applied after this process started) carries a higher index
		// and falls through to fire the callback.
		if f.replayUpTo > 0 && l.Index <= f.replayUpTo {
			return nil
		}
		// Fire the callback outside the FSM lock. The callback typically
		// wipes disk state and exits the process, both of which must not
		// stall the Raft Apply loop.
		cb := f.onDisband
		from := cmd.Key
		if cb != nil {
			go cb(from)
		}
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

// Restore restores the FSM from a snapshot. Maps absent from older snapshots
// are initialized so subsequent Apply branches can write to them without
// nil-map panics.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	var state ClusterState
	if err := json.NewDecoder(rc).Decode(&state); err != nil {
		return err
	}
	if state.Nodes == nil {
		state.Nodes = make(map[string]*Node)
	}
	if state.Config == nil {
		state.Config = make(map[string]string)
	}
	if state.Accounts == nil {
		state.Accounts = make(map[string]*AdminAccount)
	}
	if state.RecoveryCodes == nil {
		state.RecoveryCodes = make(map[string][]string)
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
	accounts := make(map[string]*AdminAccount, len(f.state.Accounts))
	for k, v := range f.state.Accounts {
		a := *v
		accounts[k] = &a
	}
	recovery := make(map[string][]string, len(f.state.RecoveryCodes))
	for k, v := range f.state.RecoveryCodes {
		recovery[k] = append([]string(nil), v...)
	}
	return ClusterState{
		Name:          f.state.Name,
		Nodes:         nodes,
		Config:        config,
		Accounts:      accounts,
		RecoveryCodes: recovery,
	}
}

// GetRecoveryCodes returns a copy of the 2FA recovery-code hashes for a user,
// or nil if none are stored.
func (f *FSM) GetRecoveryCodes(username string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.state.RecoveryCodes == nil {
		return nil
	}
	return append([]string(nil), f.state.RecoveryCodes[username]...)
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

// GetAccount returns a specific account, or nil.
func (f *FSM) GetAccount(username string) *AdminAccount {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if a, ok := f.state.Accounts[username]; ok {
		copy := *a
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
	if err := sink.Close(); err != nil {
		sink.Cancel()
		return err
	}
	return nil
}

func (s *fsmSnapshot) Release() {}
