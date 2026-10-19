package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
)

// The internal proxy secret moves from sha256(ca.crt) — derivable by anyone
// holding the CA certificate, which agents will — to a random secret that
// lives in the replicated state of the managers.
const (
	configKeyProxySecret    = "cluster_proxy_secret"
	configKeyProxyRotatedAt = "cluster_proxy_secret_rotated_at"

	// CapProxySecret is the heartbeat capability of a node that accepts the
	// replicated secret.
	CapProxySecret = "proxy-secret-v2"

	// legacyProxyGrace is how long after the rotation the derived secret is
	// still accepted: peers pick the rotation up on their 15-second tick and
	// sign with the derived secret until then.
	legacyProxyGrace = 2 * time.Minute
	// proxySecretSettle is how long the replicated secret must have existed
	// before the leader rotates, so every member has loaded it.
	proxySecretSettle = time.Minute
	// proxyRotationSettled is when the derived secret is dead everywhere:
	// the grace plus a tick of margin. RegisterAgent waits for it.
	proxyRotationSettled = legacyProxyGrace + 3*time.Minute
)

// proxySecretsAt says what this node signs with and accepts. Once an agent
// exists the derived secret is dead whatever the clocks say: the agent holds
// ca.crt, from which it is computed, and the grace below is this node's clock
// measured against a time the leader's clock wrote.
func proxySecretsAt(legacy, replicated, rotatedAt string, now time.Time, hasAgents bool) (sign string, accept []string) {
	if hasAgents && replicated != "" {
		return replicated, []string{replicated}
	}
	at, err := time.Parse(time.RFC3339, rotatedAt)
	if rotatedAt == "" || err != nil || replicated == "" {
		accept = []string{legacy}
		if replicated != "" {
			accept = append(accept, replicated)
		}
		return legacy, accept
	}
	accept = []string{replicated}
	if now.Before(at.Add(legacyProxyGrace)) {
		accept = append(accept, legacy)
	}
	return replicated, accept
}

// shouldRotate reports whether the leader may rotate: the replicated secret
// has existed long enough for every member to load it, and every member but
// the leader is online and reports CapProxySecret. An offline member blocks
// the rotation — rotating past it would cut it off.
func shouldRotate(seededAt, now time.Time, self string, members []string, health map[string]NodeStatus, caps map[string][]string) bool {
	if seededAt.IsZero() || now.Sub(seededAt) < proxySecretSettle {
		return false
	}
	for _, id := range members {
		if id == self {
			continue
		}
		if health[id] != StatusOnline || !slices.Contains(caps[id], CapProxySecret) {
			return false
		}
	}
	return true
}

// rotationMembers are the nodes the leader waits on before it rotates: the
// managers. An agent is never given the managers' secret, so it never
// reports CapProxySecret.
func rotationMembers(state ClusterState) []string {
	var ids []string
	for id, n := range state.Nodes {
		if n.IsManager() {
			ids = append(ids, id)
		}
	}
	return ids
}

// hasAgentNodes reports whether any node is not a manager. A kind this build
// does not know counts as an agent: it ends the derived secret rather than
// keeping it.
func hasAgentNodes(state ClusterState) bool {
	for _, n := range state.Nodes {
		if !n.IsManager() {
			return true
		}
	}
	return false
}

// proxySecretFloorFile keeps the replicated secret and its rotation time as
// this node last applied them, beside the Raft data whose lifetime it
// shares. After a restart the FSM holds only what the last snapshot held
// until the node learns the commit index — seconds, or the length of an
// election — and without the floor the node would sign with, and accept,
// the CA-derived secret again for that long.
const proxySecretFloorFile = "proxy-secret.json"

type proxySecretFloor struct {
	Secret    string `json:"secret"`
	RotatedAt string `json:"rotated_at,omitempty"`
}

// onConfigChange applies the proxy secrets the moment the FSM changes them,
// rather than on the next watchLeader tick.
func (m *Manager) onConfigChange(key string) {
	if key == "" || key == configKeyProxySecret || key == configKeyProxyRotatedAt {
		m.ApplyProxySecrets()
	}
}

// ApplyProxySecrets installs this node's signing and accepted secrets from
// the replicated state. Runs at activation, when the FSM changes them, and
// on every watchLeader tick on every node; a change is logged once. An
// agent's record is not a proxy key, so an agent's arrival takes effect on
// the next tick.
func (m *Manager) ApplyProxySecrets() {
	if m.raft == nil {
		return
	}
	// Held through the install, so two callers cannot leave the older
	// values installed.
	m.appliedSecretsMu.Lock()
	defer m.appliedSecretsMu.Unlock()
	fsm := m.raft.GetFSM()
	replicated, _ := fsm.GetConfigValue(configKeyProxySecret)
	rotatedAt, _ := fsm.GetConfigValue(configKeyProxyRotatedAt)
	replicated, rotatedAt = m.withProxySecretFloor(replicated, rotatedAt)
	sign, accept := proxySecretsAt(m.ProxySecret(), replicated, rotatedAt, time.Now(), hasAgentNodes(fsm.GetState()))
	m.appliedReplicated = replicated
	if sign == m.appliedSign && slices.Equal(accept, m.appliedAccept) {
		return
	}
	m.appliedSign, m.appliedAccept = sign, accept
	auth.SetProxySecrets(sign, accept)
	signing := "ca-derived"
	if replicated != "" && sign == replicated {
		signing = "replicated"
	}
	slog.Info("internal proxy secrets applied", "component", "cluster", "signing", signing, "accepted", len(accept))
}

// withProxySecretFloor fills in from the floor file what the FSM does not
// show yet, and records what it shows. Both values only move forward — the
// secret is seeded once, the rotation recorded once — so the floor never
// overrides the FSM; it stands in for entries not replayed yet. A floor that
// cannot be read is ignored: the FSM then decides alone, as before the floor
// existed. Caller holds appliedSecretsMu.
func (m *Manager) withProxySecretFloor(replicated, rotatedAt string) (string, string) {
	if m.config == nil || m.config.DataDir == "" {
		return replicated, rotatedAt
	}
	path := filepath.Join(m.config.DataDir, proxySecretFloorFile)
	if !m.floorLoaded {
		m.floorLoaded = true
		data, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(data, &m.floor)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.floor = proxySecretFloor{}
			slog.Warn("ignoring the recorded internal proxy secret", "component", "cluster", "path", path, "error", err)
		}
	}
	switch {
	case replicated == "":
		replicated, rotatedAt = m.floor.Secret, m.floor.RotatedAt
	case replicated == m.floor.Secret && rotatedAt == "":
		rotatedAt = m.floor.RotatedAt
	}
	if replicated != "" && (replicated != m.floor.Secret || rotatedAt != m.floor.RotatedAt) {
		next := proxySecretFloor{Secret: replicated, RotatedAt: rotatedAt}
		data, _ := json.Marshal(next)
		if err := config.AtomicWriteFile(path, data, 0600); err != nil {
			slog.Warn("could not record the internal proxy secret", "component", "cluster", "path", path, "error", err)
		} else {
			m.floor = next
		}
	}
	return replicated, rotatedAt
}

// MaybeRotateProxySecret seeds the replicated secret and, once shouldRotate
// allows, records the rotation. Leader only; called on every watchLeader tick.
func (m *Manager) MaybeRotateProxySecret() error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	fsm := m.raft.GetFSM()
	if v, _ := fsm.GetConfigValue(configKeyProxyRotatedAt); v != "" {
		return nil
	}
	replicated, _ := fsm.GetConfigValue(configKeyProxySecret)
	if replicated == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Errorf("generate proxy secret: %w", err)
		}
		if err := m.SetConfig(configKeyProxySecret, hex.EncodeToString(buf)); err != nil {
			return fmt.Errorf("replicate proxy secret: %w", err)
		}
		slog.Info("seeded the replicated internal proxy secret", "component", "cluster")
		return nil
	}
	now := time.Now()
	seenAt := m.replicatedSecretSeenAt(replicated, now)

	members := rotationMembers(fsm.GetState())
	caps := map[string][]string{}
	for _, id := range members {
		if met := m.heartbeat.GetMetrics(id); met != nil {
			caps[id] = met.Capabilities
		}
	}
	if !shouldRotate(seenAt, now, m.nodeID, members, m.heartbeat.CheckHealth(), caps) {
		return nil
	}
	if err := m.SetConfig(configKeyProxyRotatedAt, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record proxy secret rotation: %w", err)
	}
	slog.Info("internal proxy secret rotated: every member now signs with the replicated secret", "component", "cluster")
	return nil
}

// replicatedSecretSeenAt is when this node first saw this replicated secret.
func (m *Manager) replicatedSecretSeenAt(replicated string, now time.Time) time.Time {
	m.appliedSecretsMu.Lock()
	defer m.appliedSecretsMu.Unlock()
	if m.proxySecretSeen != replicated {
		m.proxySecretSeen, m.proxySecretSeenAt = replicated, now
	}
	return m.proxySecretSeenAt
}

// heartbeatCapabilities is what this node reports on its heartbeat:
// CapProxySecret once it accepts the replicated secret — what the leader
// waits for before it rotates — not merely once it runs code that could.
func (m *Manager) heartbeatCapabilities() []string {
	m.appliedSecretsMu.Lock()
	defer m.appliedSecretsMu.Unlock()
	if m.appliedReplicated == "" {
		return nil
	}
	return []string{CapProxySecret}
}

// ProxyRotationSettled reports whether the CA-derived secret is dead on every
// member — what a registration of a node holding ca.crt must wait for.
func (m *Manager) ProxyRotationSettled(now time.Time) bool {
	if m.raft == nil {
		return false
	}
	v, _ := m.raft.GetFSM().GetConfigValue(configKeyProxyRotatedAt)
	at, err := time.Parse(time.RFC3339, v)
	return err == nil && now.Sub(at) > proxyRotationSettled
}
