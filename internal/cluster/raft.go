package cluster

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// RaftNode wraps the hashicorp/raft instance.
type RaftNode struct {
	raft *raft.Raft
	fsm  *FSM
}

// RaftConfig holds Raft initialization parameters.
type RaftConfig struct {
	NodeID    string
	BindAddr  string
	DataDir   string
	Bootstrap bool
}

// NewRaftNode creates and starts a Raft instance.
func NewRaftNode(cfg RaftConfig) (*RaftNode, error) {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID)
	raftCfg.HeartbeatTimeout = 5000 * time.Millisecond
	raftCfg.ElectionTimeout = 5000 * time.Millisecond
	raftCfg.CommitTimeout = 2000 * time.Millisecond
	raftCfg.SnapshotInterval = 10 * time.Minute
	raftCfg.SnapshotThreshold = 256

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve bind addr: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	logStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft-log.db"))
	if err != nil {
		return nil, fmt.Errorf("create log store: %w", err)
	}

	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft-stable.db"))
	if err != nil {
		return nil, fmt.Errorf("create stable store: %w", err)
	}

	snapshotStore, err := raft.NewFileSnapshotStore(cfg.DataDir, 2, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create snapshot store: %w", err)
	}

	fsm := NewFSM()

	r, err := raft.NewRaft(raftCfg, fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft: %w", err)
	}

	if cfg.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raft.ServerID(cfg.NodeID),
					Address: raft.ServerAddress(cfg.BindAddr),
				},
			},
		}
		f := r.BootstrapCluster(configuration)
		if err := f.Error(); err != nil && err != raft.ErrCantBootstrap {
			return nil, fmt.Errorf("bootstrap: %w", err)
		}
	}

	log.Printf("[cluster] Raft node started: id=%s addr=%s bootstrap=%v", cfg.NodeID, cfg.BindAddr, cfg.Bootstrap)

	return &RaftNode{raft: r, fsm: fsm}, nil
}

// Apply submits a command to the Raft cluster.
func (rn *RaftNode) Apply(cmd Command, timeout time.Duration) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}

	f := rn.raft.Apply(data, timeout)
	if err := f.Error(); err != nil {
		return fmt.Errorf("raft apply: %w", err)
	}

	if resp := f.Response(); resp != nil {
		if err, ok := resp.(error); ok {
			return err
		}
	}
	return nil
}

// AddVoter adds a new voter node to the Raft cluster.
func (rn *RaftNode) AddVoter(nodeID, address string) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}

	f := rn.raft.AddVoter(
		raft.ServerID(nodeID),
		raft.ServerAddress(address),
		0, 30*time.Second,
	)
	return f.Error()
}

// RemoveServer removes a node from the Raft cluster.
func (rn *RaftNode) RemoveServer(nodeID string) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}

	f := rn.raft.RemoveServer(
		raft.ServerID(nodeID),
		0, 30*time.Second,
	)
	return f.Error()
}

// IsLeader returns true if this node is the Raft leader.
func (rn *RaftNode) IsLeader() bool {
	return rn.raft.State() == raft.Leader
}

// LeaderID returns the current leader's node ID.
func (rn *RaftNode) LeaderID() string {
	_, id := rn.raft.LeaderWithID()
	return string(id)
}

// LeaderGRPCAddress returns the gRPC address of the current leader.
func (rn *RaftNode) LeaderGRPCAddress() string {
	leaderID := rn.LeaderID()
	if leaderID == "" {
		return ""
	}
	state := rn.fsm.GetState()
	if node, ok := state.Nodes[leaderID]; ok {
		return node.GRPCAddress
	}
	return ""
}

// State returns the current Raft state string.
func (rn *RaftNode) State() string {
	return rn.raft.State().String()
}

// GetFSM returns the FSM for reading state.
func (rn *RaftNode) GetFSM() *FSM {
	return rn.fsm
}

// TransferLeadership transfers leadership to the target node.
func (rn *RaftNode) TransferLeadership(targetNodeID string) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}

	// Find the target server address from Raft configuration
	configFuture := rn.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return fmt.Errorf("get raft config: %w", err)
	}

	var targetAddr raft.ServerAddress
	found := false
	for _, server := range configFuture.Configuration().Servers {
		if string(server.ID) == targetNodeID {
			targetAddr = server.Address
			found = true
			break
		}
	}
	if !found {
		return ErrNodeNotFound
	}

	f := rn.raft.LeadershipTransferToServer(raft.ServerID(targetNodeID), targetAddr)
	return f.Error()
}

// Shutdown cleanly stops the Raft node with a timeout.
func (rn *RaftNode) Shutdown() error {
	done := make(chan error, 1)
	go func() {
		f := rn.raft.Shutdown()
		done <- f.Error()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		log.Println("[cluster] Raft shutdown timed out after 10s, forcing")
		return nil
	}
}
