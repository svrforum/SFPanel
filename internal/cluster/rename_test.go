package cluster

import (
	"errors"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

// A node with no Raft running is not the leader: the rename is refused with
// ErrNotLeader rather than dereferencing a nil Raft.
func TestRenameCluster_RefusedOffTheLeader(t *testing.T) {
	m := NewManager(&config.ClusterConfig{NodeID: "n1"})
	if err := m.RenameCluster("renamed"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("RenameCluster without Raft = %v, want ErrNotLeader", err)
	}
}
