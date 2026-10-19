package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
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

// proxySecretsAt says what this node signs with and accepts.
func proxySecretsAt(legacy, replicated, rotatedAt string, now time.Time) (sign string, accept []string) {
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

// ApplyProxySecrets installs this node's signing and accepted secrets from
// the replicated state. Runs at activation and on every watchLeader tick on
// every node; a change is logged once.
func (m *Manager) ApplyProxySecrets() {
	if m.raft == nil {
		return
	}
	fsm := m.raft.GetFSM()
	replicated, _ := fsm.GetConfigValue(configKeyProxySecret)
	rotatedAt, _ := fsm.GetConfigValue(configKeyProxyRotatedAt)
	sign, accept := proxySecretsAt(m.ProxySecret(), replicated, rotatedAt, time.Now())
	m.appliedSecretsMu.Lock()
	changed := sign != m.appliedSign || !slices.Equal(accept, m.appliedAccept)
	m.appliedSign, m.appliedAccept = sign, accept
	m.appliedSecretsMu.Unlock()
	if !changed {
		return
	}
	auth.SetProxySecrets(sign, accept)
	signing := "ca-derived"
	if replicated != "" && sign == replicated {
		signing = "replicated"
	}
	slog.Info("internal proxy secrets applied", "component", "cluster", "signing", signing, "accepted", len(accept))
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
	m.appliedSecretsMu.Lock()
	if m.proxySecretSeenAt.IsZero() {
		m.proxySecretSeenAt = now
	}
	seenAt := m.proxySecretSeenAt
	m.appliedSecretsMu.Unlock()

	state := fsm.GetState()
	members := make([]string, 0, len(state.Nodes))
	caps := map[string][]string{}
	for id := range state.Nodes {
		members = append(members, id)
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
