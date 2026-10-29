package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// A re-rotation of the managers' internal proxy secret, after plan A's first
// switch from the CA-derived one (proxy_secret.go). The managers' secret
// moves forward only: a newer generation, which every manager accepts before
// any signs with it, and the one it replaces accepted for a little while
// after. The record is one value, so its phase cannot be written in part, and
// cluster_proxy_secret mirrors the secret it signs with for older releases.
// Every node goes by the newer of the replicated record and the one its floor
// kept: after a restart the FSM may show an older step's record until the
// node catches up, and a random secret has no order of its own.
const (
	configKeyProxyKey = "cluster_proxy_key" // JSON ProxyKey: the managers' secret from its first re-rotation on
	// CapProxyRotation is the heartbeat capability of a manager whose code
	// follows a re-rotation; whether it has taken one's next secret is its
	// ping's proxy_generation.
	CapProxyRotation = "proxy-rotation-v1"
)

// ProxyPhase is where a re-rotation stands, read from its record (Phase).
type ProxyPhase string

const (
	ProxyPhaseNext     ProxyPhase = "next"     // every manager accepts Secret; Previous still signs
	ProxyPhaseSwitched ProxyPhase = "switched" // Secret signs; Previous is accepted for legacyProxyGrace
	ProxyPhaseDone     ProxyPhase = "done"     // Secret alone
)

// ProxyKey is the managers' internal proxy secret as a re-rotation writes it:
// the generation, its secret, the one it replaces until that is retired, and
// when the managers switched to signing with the new one.
type ProxyKey struct {
	Generation int64     `json:"generation"`
	Secret     string    `json:"secret"`
	Previous   string    `json:"previous,omitempty"`
	SwitchedAt time.Time `json:"switched_at,omitzero"`
}

// Phase is the record's phase: done once the previous secret is gone, next
// until the switch is recorded, switched in between.
func (k ProxyKey) Phase() ProxyPhase {
	switch {
	case k.Previous == "":
		return ProxyPhaseDone
	case k.SwitchedAt.IsZero():
		return ProxyPhaseNext
	}
	return ProxyPhaseSwitched
}

// order ranks the phases of one generation: a record only moves up.
func (p ProxyPhase) order() int {
	switch p {
	case ProxyPhaseNext:
		return 1
	case ProxyPhaseSwitched:
		return 2
	case ProxyPhaseDone:
		return 3
	}
	return 0
}

// newerThan reports whether k is a later step than o: a higher generation,
// or the same one in a later phase.
func (k ProxyKey) newerThan(o ProxyKey) bool {
	if k.Generation != o.Generation {
		return k.Generation > o.Generation
	}
	return k.Phase().order() > o.Phase().order()
}

// sameAs reports whether k and o are the same step, the times compared as
// instants: one read back from JSON has lost its monotonic reading.
func (k ProxyKey) sameAs(o ProxyKey) bool {
	return k.Generation == o.Generation && k.Secret == o.Secret && k.Previous == o.Previous && k.SwitchedAt.Equal(o.SwitchedAt)
}

// proxyKeyOf is the re-rotation record in state. False with none, and with
// one that does not parse — warned, as clusterJWTKey does: the node then
// goes by its floor, or by plan A's keys.
func proxyKeyOf(state ClusterState) (ProxyKey, bool) {
	raw := state.Config[configKeyProxyKey]
	if raw == "" {
		return ProxyKey{}, false
	}
	var k ProxyKey
	if err := json.Unmarshal([]byte(raw), &k); err != nil || k.Generation <= 0 || k.Secret == "" {
		slog.Warn("the internal proxy secret's rotation record does not parse; ignoring it", "component", "cluster")
		return ProxyKey{}, false
	}
	return k, true
}

// proxyKeysAt says what a manager signs with and accepts under k at now: in
// next the secret everyone signs with is still Previous, and Secret is only
// accepted; from the switch on Secret signs, and Previous is accepted until
// legacyProxyGrace has passed on this node's clock, or the leader's done
// write takes it out, whichever comes first.
func proxyKeysAt(k ProxyKey, now time.Time) (sign string, accept []string) {
	switch k.Phase() {
	case ProxyPhaseNext:
		return k.Previous, []string{k.Previous, k.Secret}
	case ProxyPhaseSwitched:
		if now.Before(k.SwitchedAt.Add(legacyProxyGrace)) {
			return k.Secret, []string{k.Secret, k.Previous}
		}
	}
	return k.Secret, []string{k.Secret}
}

// proxyNode is what the leader knows of a manager at a re-rotation step,
// from its last heartbeat. Capable: it reports CapProxyRotation.
type proxyNode struct {
	ID, Name, Version          string
	HeardFrom, Online, Capable bool
	Generation                 *int64 // the newest secret it accepts; nil: not reported
}

// proxyStep is the leader's decision at a re-rotation step (nextProxyStep).
type proxyStep struct {
	Next    ProxyPhase // "" stays
	Waiting []string   // sorted, each with why
}

// nextProxyStep decides a re-rotation's step from what the leader knows of
// every manager, at now. Next moves to switched once every manager is online,
// follows a re-rotation and says it accepts k's generation: one that does
// not would refuse every request signed with the new secret. Next has no
// deadline — nobody signs with the next secret, so waiting opens nothing.
// Switched moves to done once proxyRotationSettled has passed since the
// switch, past every node's grace with a margin for its clock.
func nextProxyStep(k ProxyKey, managers []proxyNode, now time.Time) proxyStep {
	switch k.Phase() {
	case ProxyPhaseNext:
		var waiting []string
		for _, n := range managers {
			name := n.Name
			if name == "" {
				name = n.ID
			}
			switch {
			case !n.HeardFrom:
				waiting = append(waiting, name+" (not heard from)")
			case !n.Online:
				waiting = append(waiting, name+" (offline)")
			case !n.Capable:
				waiting = append(waiting, fmt.Sprintf("%s (%s)", name, n.Version))
			case n.Generation == nil || *n.Generation < k.Generation:
				waiting = append(waiting, name+" (has not taken the next secret yet)")
			}
		}
		if len(waiting) == 0 {
			return proxyStep{Next: ProxyPhaseSwitched}
		}
		slices.Sort(waiting)
		return proxyStep{Waiting: waiting}
	case ProxyPhaseSwitched:
		if !now.Before(k.SwitchedAt.Add(proxyRotationSettled)) {
			return proxyStep{Next: ProxyPhaseDone}
		}
	}
	return proxyStep{}
}

// StartProxyRotation gives the managers a new internal proxy secret under the
// next generation and returns it (leader only). It writes the record in its
// next phase, where every manager accepts the new secret and goes on signing
// with the one it replaces; advanceProxyRotation switches once each says it
// accepts it. A start in next replaces only the next secret, which nobody
// signs with yet; in switched it is refused, as a manager would then accept
// three. Every gate comes before anything is written, and each refusal wraps
// ErrRotationRefused: plan A's switch not settled (ErrProxyRotationPending),
// as a manager may still accept the derived secret; the previous secret
// still accepted; a manager not online on a release that follows
// (ErrRotationWaiting, naming each). The log carries the generation, never a
// secret.
func (m *Manager) StartProxyRotation() (int64, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return 0, ErrNotLeader
	}
	// The gates read what earlier terms committed: a leader that has not
	// caught up may not see the last record, and write its generation again
	// under another secret — one every floor that holds it refuses.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return 0, fmt.Errorf("leader barrier failed: %w", err)
	}
	m.proxyRotateMu.Lock()
	defer m.proxyRotateMu.Unlock()
	now := time.Now()
	if !m.ProxyRotationSettled(now) {
		return 0, fmt.Errorf("%w: %w", ErrRotationRefused, ErrProxyRotationPending)
	}
	state := m.raft.GetFSM().GetState()
	k, ok := proxyKeyOf(state)
	if !ok && state.Config[configKeyProxyKey] != "" {
		return 0, fmt.Errorf("%w: the internal proxy secret's rotation record does not parse", ErrRotationRefused)
	}
	if ok && k.Phase() == ProxyPhaseSwitched {
		return 0, fmt.Errorf("%w: the previous internal proxy secret is still accepted until %s; start again after that",
			ErrRotationRefused, k.SwitchedAt.Add(proxyRotationSettled).UTC().Format(time.RFC3339))
	}
	if waiting := m.nodesNotReady(state, CapProxyRotation, false); len(waiting) > 0 {
		return 0, fmt.Errorf("%w: %w; waiting for %s", ErrRotationRefused, ErrRotationWaiting, strings.Join(waiting, ", "))
	}
	// Previous is what the managers sign with now.
	var previous string
	switch {
	case !ok:
		previous = state.Config[configKeyProxySecret]
	case k.Phase() == ProxyPhaseNext:
		previous = k.Previous
	default:
		previous = k.Secret
	}
	if previous == "" {
		return 0, fmt.Errorf("%w: the managers have no internal proxy secret to move from", ErrRotationRefused)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return 0, fmt.Errorf("generate proxy secret: %w", err)
	}
	next := ProxyKey{Generation: k.Generation + 1, Secret: hex.EncodeToString(buf), Previous: previous}
	if err := m.writeProxyKey(next); err != nil {
		return 0, fmt.Errorf("replicate the next internal proxy secret: %w", err)
	}
	slog.Info("started a rotation of the internal proxy secret: every manager is to accept the next one", "component", "cluster", "generation", next.Generation)
	return next.Generation, nil
}

// MaybeAdvanceProxyRotation moves the re-rotation on as far as what this
// leader knows of every manager allows. Leader only; on every watchLeader
// tick.
func (m *Manager) MaybeAdvanceProxyRotation() error {
	return m.advanceProxyRotation(time.Now())
}

// advanceProxyRotation moves the re-rotation on by one step when what this
// leader knows of every manager at now allows it (nextProxyStep), and first
// writes cluster_proxy_secret again when it does not mirror the secret the
// record signs with: older releases, a manager the watchdog rolled back
// among them, follow the mirror alone. The switch goes by each manager's own
// word on its heartbeat — the leader sees nothing of an HMAC secret on a
// connection — and a wrong word costs relays, not the secret. Leader only;
// nil with no record, and while this leader's FSM has not caught up: one
// elected a moment ago may hold a step the cluster has moved past.
func (m *Manager) advanceProxyRotation(now time.Time) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	if !m.raft.caughtUpLeader(2 * time.Second) {
		return nil
	}
	m.proxyRotateMu.Lock()
	defer m.proxyRotateMu.Unlock()
	state := m.raft.GetFSM().GetState()
	k, ok := proxyKeyOf(state)
	if !ok {
		return nil
	}
	if sign, _ := proxyKeysAt(k, now); state.Config[configKeyProxySecret] != sign {
		if err := m.SetConfig(configKeyProxySecret, sign); err != nil {
			return fmt.Errorf("mirror the internal proxy secret: %w", err)
		}
	}
	step := nextProxyStep(k, m.proxyNodes(state), now)
	m.noteProxyWaiting(k, step.Waiting)
	next := k
	switch step.Next {
	case ProxyPhaseSwitched:
		next.SwitchedAt = now.UTC()
		if err := m.writeProxyKey(next); err != nil {
			return fmt.Errorf("record the switch to the new internal proxy secret: %w", err)
		}
		slog.Info("every manager signs with the new internal proxy secret; the old one is accepted for two more minutes", "component", "cluster", "generation", k.Generation)
		// After the record: the managers switch by the record, and the
		// mirror is for older releases — the next tick writes it again.
		if err := m.SetConfig(configKeyProxySecret, next.Secret); err != nil {
			slog.Warn("could not mirror the new internal proxy secret for older releases; the leader writes it again", "component", "cluster", "generation", k.Generation, "error", err)
		}
	case ProxyPhaseDone:
		next.Previous = ""
		if err := m.writeProxyKey(next); err != nil {
			return fmt.Errorf("retire the old internal proxy secret: %w", err)
		}
		slog.Info("the old internal proxy secret is no longer accepted anywhere", "component", "cluster", "generation", k.Generation)
	}
	return nil
}

// proxyNodes is what this leader knows of every manager for a re-rotation
// step: each one's last heartbeat — whether its code follows a re-rotation,
// and the newest secret it says it accepts — and itself from what it applied.
// Agents are left out: they never hold the managers' secret.
func (m *Manager) proxyNodes(state ClusterState) []proxyNode {
	health := m.heartbeat.CheckHealth()
	var nodes []proxyNode
	for id, n := range state.Nodes {
		if !n.IsManager() {
			continue
		}
		node := proxyNode{ID: id, Name: n.Name}
		switch met := m.heartbeat.GetMetrics(id); {
		case id == m.nodeID:
			gen := m.ProxyGeneration()
			node.Version, node.HeardFrom, node.Online, node.Capable, node.Generation = m.version, true, true, true, &gen
		case met != nil:
			node.Version, node.HeardFrom, node.Online = met.Version, true, health[id] == StatusOnline
			node.Capable = slices.Contains(met.Capabilities, CapProxyRotation)
			node.Generation = met.ProxyGeneration
		}
		nodes = append(nodes, node)
	}
	slices.SortFunc(nodes, func(a, b proxyNode) int { return strings.Compare(a.ID, b.ID) })
	return nodes
}

// noteProxyWaiting says whom the re-rotation's step waits for, once while
// that stays the same in the same phase and generation: the leader asks on
// every watchLeader tick. Caller holds proxyRotateMu.
func (m *Manager) noteProxyWaiting(k ProxyKey, waiting []string) {
	note := ""
	if len(waiting) > 0 {
		note = fmt.Sprintf("%d %s: %s", k.Generation, k.Phase(), strings.Join(waiting, ", "))
	}
	if note == m.proxyWaiting {
		return
	}
	m.proxyWaiting = note
	if note != "" {
		slog.Info("an internal proxy secret rotation waits", "component", "cluster", "generation", k.Generation, "phase", k.Phase(), "waiting", strings.Join(waiting, ", "))
	}
}

// writeProxyKey replicates the re-rotation record.
func (m *Manager) writeProxyKey(k ProxyKey) error {
	data, err := json.Marshal(k)
	if err != nil {
		return err
	}
	return m.SetConfig(configKeyProxyKey, string(data))
}

// ProxyGeneration is the newest generation of the managers' secret this
// manager accepts; 0 while it goes by plan A's secret.
func (m *Manager) ProxyGeneration() int64 { return m.appliedProxyGen.Load() }
