package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
)

// PanelCAPublishPath is the endpoint a follower calls on the leader to have its
// panel CA written into replicated state.
const PanelCAPublishPath = "/api/v1/cluster/panel-ca"

// PanelCAPublishRequest is that endpoint's body.
type PanelCAPublishRequest struct {
	NodeID string `json:"node_id"`
	CACert string `json:"ca_cert"`
}

// panelCAPublishInterval is how often a node re-checks that its certificate is
// still in replicated state. It is not a hot path: nothing changes between
// checks unless leadership moved or the node's CA was regenerated.
const panelCAPublishInterval = 2 * time.Minute

// publishPanelCA gets this node's panel CA into replicated state, so peers know
// to reach it over HTTPS and know which authority to trust.
//
// Two paths, because every FSM write is leader-only:
//   - Leader: apply directly.
//   - Follower: ask the leader to apply on its behalf, over the same
//     mTLS-authenticated gRPC proxy the rest of the cluster uses. This is the
//     shape UpdateNodeAddress already uses for per-node state a follower cannot
//     write itself.
//
// Without the follower path this feature would silently half-work: a node that
// is never elected would never publish, so on a two-node cluster with a stable
// leader the follower's certificate would never replicate and every relay to it
// would keep using plaintext.
func (m *Manager) publishPanelCA(ctx context.Context, caPEM string) error {
	if caPEM == "" {
		return nil
	}
	if m.raft != nil && m.raft.IsLeader() {
		return m.SetPanelCA(m.nodeID, caPEM)
	}

	// Already replicated and unchanged — nothing to ask of the leader.
	if m.raft != nil && m.raft.GetFSM().GetState().Config[panelCAKey(m.nodeID)] == caPEM {
		return nil
	}

	leaderID := ""
	if m.raft != nil {
		leaderID = m.raft.LeaderID()
	}
	if leaderID == "" || leaderID == m.nodeID {
		return ErrNotLeader
	}
	body, err := json.Marshal(PanelCAPublishRequest{NodeID: m.nodeID, CACert: caPEM})
	if err != nil {
		return fmt.Errorf("encode panel CA request: %w", err)
	}
	status, respBody, err := m.ProxyToNode(ctx, leaderID, "POST", PanelCAPublishPath, body, "")
	if err != nil {
		return fmt.Errorf("ask leader to publish panel CA: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("leader refused panel CA (HTTP %d): %s", status, string(respBody))
	}
	return nil
}

// StartPanelCAPublisher keeps this node's panel CA in replicated state.
//
// It polls rather than firing once at boot, for the same reason the cluster CA
// seeding watches the leadership edge: publishing needs a leader, and at boot
// there may not be one yet. Polling also covers the cases a one-shot would
// miss — a leader election later, a certificate regenerated after the address
// changed, or a node that joins the cluster long after it enabled TLS.
//
// caPEM is read fresh on each tick by the supplied func so a re-issued CA is
// picked up without a restart.
func (m *Manager) StartPanelCAPublisher(ctx context.Context, readCA func() (string, error)) {
	if readCA == nil {
		return
	}
	safe.Go("panel-ca-publisher", func() {
		ticker := time.NewTicker(panelCAPublishInterval)
		defer ticker.Stop()
		// A short first attempt so a healthy cluster converges in seconds
		// rather than on the first two-minute tick.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()

		var lastErr string
		attempt := func() {
			caPEM, err := readCA()
			if err != nil {
				return // TLS off, or material not ready yet
			}
			publishCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := m.publishPanelCA(publishCtx, caPEM); err != nil {
				// Expected and transient while the cluster has no leader, so
				// log only when the reason changes — otherwise a leaderless
				// window would emit the same line every two minutes forever.
				if msg := err.Error(); msg != lastErr {
					lastErr = msg
					slog.Warn("could not publish panel CA to replicated state yet",
						"component", "cluster", "error", err)
				}
				return
			}
			lastErr = ""
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				attempt()
			case <-ticker.C:
				attempt()
			}
		}
	})
}
