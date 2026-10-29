package cluster

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

// The cluster's JWT key, as the leader rotates it and every manager follows.
const (
	configKeyJWTKey    = "jwt_key"    // JSON JWTKey: the cluster's JWT key since its first rotation
	configKeyJWTSecret = "jwt_secret" // the plain key every cluster has had since init; a mirror of jwt_key after a rotation
	// CapJWTRotation is the heartbeat capability of a manager that applies
	// the key a rotation replicates — what the leader waits for before it
	// rotates.
	CapJWTRotation = "jwt-rotation-v1"
)

// JWTKey is the cluster's JWT key and its generation: a rotation writes the
// next generation, and a manager applies a key only when its generation is
// higher than the one it runs.
type JWTKey struct {
	Generation int64     `json:"generation"`
	Secret     string    `json:"secret"`
	RotatedAt  time.Time `json:"rotated_at"`
}

// clusterJWTKey is the cluster's JWT key: the record a rotation writes, else
// the plain key every cluster has had since its init, as generation 0.
func clusterJWTKey(state ClusterState) (JWTKey, bool) {
	if raw := state.Config[configKeyJWTKey]; raw != "" {
		var k JWTKey
		if err := json.Unmarshal([]byte(raw), &k); err == nil && k.Secret != "" && k.Generation > 0 {
			return k, true
		}
		slog.Warn("the cluster's JWT key record does not parse; using the plain key", "component", "cluster")
	}
	if s := state.Config[configKeyJWTSecret]; s != "" {
		return JWTKey{Secret: s}, true
	}
	return JWTKey{}, false
}

// SetJWTKeyApplier wires what makes this manager run a new cluster JWT key —
// the feature handler, which holds config.yaml, the sessions and the running
// key — and the generation the node runs now. From then on the manager
// reports CapJWTRotation, and it applies the FSM's key at once if it is newer.
func (m *Manager) SetJWTKeyApplier(apply func(JWTKey) error, running int64) {
	m.jwtMu.Lock()
	m.jwtApply = apply
	m.jwtRunning.Store(running)
	m.jwtApplierSet.Store(apply != nil)
	m.jwtMu.Unlock()
	m.ApplyJWTKey()
}

// ApplyJWTKey makes this manager run the cluster's JWT key when the FSM holds
// a newer generation than the one it runs. Only a newer one: after a restart
// the FSM holds the last snapshot until this node learns the commit index,
// and the key it shows then may be one a rotation retired. Runs when the FSM
// changes the key, after a snapshot restore, at wiring and on every
// watchLeader tick; a failed apply is tried again then.
func (m *Manager) ApplyJWTKey() {
	if m.raft == nil {
		return
	}
	m.jwtMu.Lock()
	defer m.jwtMu.Unlock()
	if m.jwtApply == nil {
		return
	}
	k, ok := clusterJWTKey(m.raft.GetFSM().GetState())
	if !ok || k.Generation <= m.jwtRunning.Load() {
		return
	}
	if err := m.jwtApply(k); err != nil {
		slog.Warn("could not finish applying the cluster's new JWT key; trying again", "component", "cluster", "generation", k.Generation, "error", err)
		return
	}
	m.jwtRunning.Store(k.Generation)
	slog.Info("this manager runs the cluster's new JWT key; every session on it has ended", "component", "cluster", "generation", k.Generation)
}

// JWTGeneration is the generation of the cluster's JWT key this manager runs.
func (m *Manager) JWTGeneration() int64 { return m.jwtRunning.Load() }

// RotateJWTKey gives the cluster a new random JWT key under the next
// generation and returns that generation; every manager, this one too,
// applies it as its FSM changes (ApplyJWTKey). Leader only. The key and its
// generation are one entry: written apart, a leader that died between the
// two writes would leave a new key under an old generation, which no manager
// applies while every joiner is handed it. Nothing is written until every
// other manager is online and reports CapJWTRotation: one that cannot follow
// goes on accepting the retired key, and a token minted with it has no bound
// on its life.
func (m *Manager) RotateJWTKey() (int64, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return 0, ErrNotLeader
	}
	m.jwtRotateMu.Lock()
	defer m.jwtRotateMu.Unlock()
	// A leader that has not caught up may not see the last rotation and
	// write its generation again: the same generation under another key is
	// applied by no manager.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return 0, fmt.Errorf("leader barrier failed: %w", err)
	}
	state := m.raft.GetFSM().GetState()
	if waiting := m.nodesNotReady(state, CapJWTRotation, false); len(waiting) > 0 {
		return 0, fmt.Errorf("%w: %w; waiting for %s", ErrRotationRefused, ErrRotationWaiting, strings.Join(waiting, ", "))
	}
	if !m.jwtApplierSet.Load() {
		return 0, fmt.Errorf("%w: this manager cannot apply a new JWT key", ErrRotationRefused)
	}
	cur, _ := clusterJWTKey(state)
	// A record that does not parse leaves the plain key as generation 0,
	// below what every manager runs: the next generation would be applied by
	// none of them, the answer would say every session ended, and joiners
	// would be handed a key the others never run.
	if state.Config[configKeyJWTKey] != "" && cur.Generation == 0 {
		return 0, fmt.Errorf("%w: the cluster's JWT key record does not parse", ErrRotationRefused)
	}
	next := JWTKey{Generation: cur.Generation + 1, Secret: config.NewSecret(), RotatedAt: time.Now().UTC()}
	data, err := json.Marshal(next)
	if err != nil {
		return 0, fmt.Errorf("encode the new JWT key: %w", err)
	}
	if err := m.SetConfig(configKeyJWTKey, string(data)); err != nil {
		return 0, fmt.Errorf("replicate the new JWT key: %w", err)
	}
	// The rotation is done once the record is in: the mirror is for older
	// releases, and the leader's tick writes it again (repairJWTMirror).
	if err := m.SetConfig(configKeyJWTSecret, next.Secret); err != nil {
		slog.Warn("could not copy the new JWT key to the plain key older releases read; the leader writes it again", "component", "cluster", "generation", next.Generation, "error", err)
	}
	slog.Info("rotated the cluster's JWT key", "component", "cluster", "generation", next.Generation)
	return next.Generation, nil
}

// repairJWTMirror writes jwt_key's secret to jwt_secret again when the two
// differ: older releases, a manager rolled back by the watchdog among them,
// read the plain key — for join answers too. Leader only, once its FSM has
// caught up; on every watchLeader tick. It takes the rotations' lock, or a
// repair that read the state before a rotation would write the key that
// rotation retired over its new mirror.
func (m *Manager) repairJWTMirror() {
	if m.raft == nil || !m.raft.IsLeader() || !m.raft.caughtUpLeader(2*time.Second) {
		return
	}
	m.jwtRotateMu.Lock()
	defer m.jwtRotateMu.Unlock()
	// With no record, the key is the plain key itself.
	state := m.raft.GetFSM().GetState()
	k, _ := clusterJWTKey(state)
	if state.Config[configKeyJWTSecret] == k.Secret {
		return
	}
	if err := m.SetConfig(configKeyJWTSecret, k.Secret); err != nil {
		slog.Warn("could not copy the cluster's JWT key to the plain key older releases read", "component", "cluster", "generation", k.Generation, "error", err)
		return
	}
	slog.Info("copied the cluster's JWT key to the plain key older releases read", "component", "cluster", "generation", k.Generation)
}
