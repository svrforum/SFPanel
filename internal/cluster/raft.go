package cluster

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	TLS       *TLSManager // TLS manager (always passed for cert access)
	RaftTLS   bool        // if true, Raft transport uses TLS encryption (new clusters only)
}

// NewRaftNode creates and starts a Raft instance.
func NewRaftNode(cfg RaftConfig) (*RaftNode, error) {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID)
	raftCfg.HeartbeatTimeout = 2000 * time.Millisecond
	raftCfg.ElectionTimeout = 5000 * time.Millisecond
	raftCfg.LeaderLeaseTimeout = 2000 * time.Millisecond
	raftCfg.CommitTimeout = 1000 * time.Millisecond
	raftCfg.SnapshotInterval = 10 * time.Minute
	raftCfg.SnapshotThreshold = 2048

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve bind addr: %w", err)
	}

	transport, err := newRaftTransport(cfg, addr)
	if err != nil {
		return nil, err
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

	slog.Info("Raft node started", "component", "cluster", "id", cfg.NodeID, "addr", cfg.BindAddr, "bootstrap", cfg.Bootstrap)

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

// Barrier blocks until all preceding leader operations have replicated to
// followers. Used by sensitive multi-step Apply chains (HandleJoin, Disband)
// to fail-fast when leadership has silently lapsed: if the local node is
// no longer leader-of-quorum, Barrier returns the underlying Raft error
// rather than letting the caller proceed with stale state.
func (rn *RaftNode) Barrier(timeout time.Duration) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}
	f := rn.raft.Barrier(timeout)
	return f.Error()
}

// VerifyLeader confirms this node is still the leader of a quorum-acked
// cluster *right now*. Read-side analogue of Barrier — used by GetStatus /
// GetNodes / GetOverview to refuse serving stale data when the local
// Raft thinks it's leader but lease/quorum has actually lapsed
// (e.g. mid-partition before the lease timeout fires). Sub-200ms in
// healthy clusters; errors out at `timeout` when partitioned.
func (rn *RaftNode) VerifyLeader(timeout time.Duration) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}
	done := make(chan error, 1)
	go func() {
		done <- rn.raft.VerifyLeader().Error()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return ErrNotLeader
	}
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

// AddNonvoter adds a node as a non-voting member. Used during the first phase
// of two-phase join — the node receives Raft state and replicates the log
// without contributing to quorum, so a mid-join crash can't drop the cluster
// below quorum on a 2-of-3 cluster.
func (rn *RaftNode) AddNonvoter(nodeID, address string) error {
	if rn.raft.State() != raft.Leader {
		return ErrNotLeader
	}
	f := rn.raft.AddNonvoter(
		raft.ServerID(nodeID),
		raft.ServerAddress(address),
		0, 30*time.Second,
	)
	return f.Error()
}

// PromoteToVoter upgrades a previously-added non-voter to voter. Called once
// the joining node confirms it has caught up with the log and is ready to
// contribute to quorum. Idempotent — re-promoting a voter is a no-op in
// HashiCorp Raft.
func (rn *RaftNode) PromoteToVoter(nodeID, address string) error {
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
		slog.Warn("Raft shutdown timed out after 10s, forcing", "component", "cluster")
		return nil
	}
}
