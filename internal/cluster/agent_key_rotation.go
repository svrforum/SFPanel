package cluster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// A rotation of the agents' internal-proxy keys, the keys the managers sign
// for each agent with. agent_key/<id> is the key the managers sign for an
// agent with, and stays so: every reader, an older manager's too, follows it.
// A rotation issues each agent a new key at its first fetch, and moves the
// managers to it only once the agent proves it holds it — the agent accepts
// both keys meanwhile, so the managers never sign with a key the agent does
// not take.
const (
	configKeyAgentKeysRotation = "agent_keys_rotation" // JSON AgentKeysRotation: the last agent key rotation started
	agentKeyNextPrefix         = "agent_key_next/"
	// CapAgentKeyRotation is the heartbeat capability of a node whose code
	// follows an agent key rotation.
	CapAgentKeyRotation = "agent-key-rotation-v1"
)

// AgentKeyNextConfigKey is the replicated key of one agent's move to a
// rotation's key (agentKeyNext).
func AgentKeyNextConfigKey(nodeID string) string { return agentKeyNextPrefix + nodeID }

// AgentKeysRotation is the last agent key rotation started.
type AgentKeysRotation struct {
	Generation int64     `json:"generation"`
	StartedAt  time.Time `json:"started_at"`
}

// agentKeyNext is one agent's move to a rotation's key: Key is issued at the
// agent's first fetch and cleared at SwitchedAt, when it became agent_key/<id>.
type agentKeyNext struct {
	Generation int64     `json:"generation"`
	Key        string    `json:"key,omitempty"`
	SwitchedAt time.Time `json:"switched_at,omitzero"`
}

// switchedAt reports whether the managers sign for the agent with the key
// generation issued it.
func (n agentKeyNext) switchedAt(generation int64) bool {
	return n.Generation == generation && !n.SwitchedAt.IsZero()
}

// agentKeysRotationOf is the last agent key rotation started in state. False
// with none, and with one that does not parse — warned.
func agentKeysRotationOf(state ClusterState) (AgentKeysRotation, bool) {
	raw := state.Config[configKeyAgentKeysRotation]
	if raw == "" {
		return AgentKeysRotation{}, false
	}
	var r AgentKeysRotation
	if err := json.Unmarshal([]byte(raw), &r); err != nil || r.Generation <= 0 {
		slog.Warn("the agent key rotation record does not parse; ignoring it", "component", "cluster")
		return AgentKeysRotation{}, false
	}
	return r, true
}

// agentKeyNextOf is nodeID's move to a rotation's key in state. False with
// none, and with one that does not parse — warned.
func agentKeyNextOf(state ClusterState, nodeID string) (agentKeyNext, bool) {
	raw := state.Config[AgentKeyNextConfigKey(nodeID)]
	if raw == "" {
		return agentKeyNext{}, false
	}
	var n agentKeyNext
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		slog.Warn("an agent's key rotation record does not parse; ignoring it", "component", "cluster", "node_id", nodeID)
		return agentKeyNext{}, false
	}
	return n, true
}

func (m *Manager) writeAgentKeyNext(nodeID string, n agentKeyNext) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return m.SetConfig(AgentKeyNextConfigKey(nodeID), string(data))
}

// agentKeyProof binds a step of an agent key rotation to a key: a fetch is
// made with the key the agent holds now — a certificate alone, which whoever
// holds the CA key can make for any agent's ID, gets no key — and a confirm
// with the key issued, which says the agent holds it.
func agentKeyProof(key, action, nodeID string, generation int64) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "agent-key\n%s\n%s\n%d", action, nodeID, generation)
	return hex.EncodeToString(mac.Sum(nil))
}

// olderOnlineAgents names the agents online by this leader's heartbeats whose
// code does not report capability, as "<name> (<version>)", sorted. Left as
// they are, they would stay on their old key and nobody would know. An
// offline agent, or one this leader has not heard from, is not named: the
// managers sign for it with its old key until it confirms a new one, so it is
// not cut off when it comes back, and takes its key then.
func (m *Manager) olderOnlineAgents(state ClusterState, capability string) []string {
	health := m.heartbeat.CheckHealth()
	var older []string
	for id, n := range state.Nodes {
		if n.IsManager() || health[id] != StatusOnline {
			continue
		}
		met := m.heartbeat.GetMetrics(id)
		if met == nil || slices.Contains(met.Capabilities, capability) {
			continue
		}
		name := n.Name
		if name == "" {
			name = id
		}
		older = append(older, fmt.Sprintf("%s (%s)", name, met.Version))
	}
	slices.Sort(older)
	return older
}

// StartAgentKeyRotation starts a rotation of every agent's internal-proxy key
// under the next generation and returns it (leader only). It writes the
// record alone: each agent takes its key when its pong's hint names the
// generation (HandleRotateAgentKey). A rotation some agent has not finished
// is replaced: the new generation is what every agent moves to. Every gate
// comes before anything is written, and each refusal wraps
// ErrRotationRefused: a manager not online on a release that follows
// (ErrRotationWaiting), an online agent on a release that does not. The log
// carries the generation, never a key.
func (m *Manager) StartAgentKeyRotation() (int64, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return 0, ErrNotLeader
	}
	// The gates read what earlier terms committed: a leader that has not
	// caught up may not see the last record, and start its generation again —
	// one an agent that switched at it would be told it holds.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return 0, fmt.Errorf("leader barrier failed: %w", err)
	}
	m.agentKeyRotateMu.Lock()
	defer m.agentKeyRotateMu.Unlock()
	state := m.raft.GetFSM().GetState()
	if waiting := m.nodesNotReady(state, CapAgentKeyRotation, false); len(waiting) > 0 {
		return 0, fmt.Errorf("%w: %w; waiting for %s", ErrRotationRefused, ErrRotationWaiting, strings.Join(waiting, ", "))
	}
	if older := m.olderOnlineAgents(state, CapAgentKeyRotation); len(older) > 0 {
		return 0, fmt.Errorf("%w: %w; waiting for %s", ErrRotationRefused, ErrRotationWaiting, strings.Join(older, ", "))
	}
	last, ok := agentKeysRotationOf(state)
	if !ok && state.Config[configKeyAgentKeysRotation] != "" {
		return 0, fmt.Errorf("%w: the agent key rotation record does not parse", ErrRotationRefused)
	}
	next := AgentKeysRotation{Generation: last.Generation + 1, StartedAt: time.Now().UTC()}
	data, err := json.Marshal(next)
	if err != nil {
		return 0, err
	}
	if err := m.SetConfig(configKeyAgentKeysRotation, string(data)); err != nil {
		return 0, fmt.Errorf("record the agent key rotation: %w", err)
	}
	agents := 0
	for _, n := range state.Nodes {
		if !n.IsManager() {
			agents++
		}
	}
	slog.Info("started an agent key rotation", "component", "cluster", "generation", next.Generation, "agents", agents)
	return next.Generation, nil
}

// HandleRotateAgentKey runs one step of an agent key rotation for the agent
// whose verified certificate is peer (leader only). A fetch (!confirm) hands
// it the key the rotation issued it, made at its first fetch and the same on
// every one after — an agent may have lost the answer — and a confirm, made
// with that key, says the agent holds it on disk and accepts it: only then do
// the managers sign for it with the new key, and the old one leaves
// replicated state. Moving them at the fetch would have them sign with a key
// the agent may never have stored. The certificate is not enough for either
// step — whoever holds the CA key can make one for any agent's ID — so a
// fetch is proved with the key the managers sign with now, a confirm with the
// key issued (agentKeyProof). A leader whose FSM has not caught up in its
// term answers ErrNotLeader: it may hold an older generation, or miss the
// key it issued. A refusal changes nothing; switched says the managers sign
// with the issued key.
func (m *Manager) HandleRotateAgentKey(peer *x509.Certificate, generation int64, proof string, confirm bool) (key string, switched bool, err error) {
	if m.raft == nil || !m.raft.IsLeader() || !m.raft.caughtUpLeader(2*time.Second) {
		return "", false, ErrNotLeader
	}
	if peer == nil {
		return "", false, errors.New("an agent key rotation needs the agent's client certificate")
	}
	id := peer.Subject.CommonName
	if node := m.raft.GetFSM().GetNode(id); node == nil || node.IsManager() {
		// A manager is signed for with the managers' own secret: it has no
		// agent key to rotate.
		return "", false, ErrNoAgentKeyDue
	}
	m.agentKeyRotateMu.Lock()
	defer m.agentKeyRotateMu.Unlock()
	// Read under the lock: a removal or a promotion takes the agent's keys
	// out under it, and a step that read the state before must not write one
	// back.
	state := m.raft.GetFSM().GetState()
	if node := state.Nodes[id]; node == nil || node.IsManager() {
		return "", false, ErrNoAgentKeyDue
	}
	rec, ok := agentKeysRotationOf(state)
	if !ok || rec.Generation != generation {
		return "", false, ErrNoAgentKeyDue
	}
	// The key the managers sign for the agent with. With none there is
	// nothing to prove: agentKeyProof("", …) is anyone's.
	signing := state.Config[AgentKeyConfigKey(id)]
	proved := func(k, action string) bool {
		return k != "" && hmac.Equal([]byte(proof), []byte(agentKeyProof(k, action, id, generation)))
	}
	next, _ := agentKeyNextOf(state, id)
	if !confirm {
		if next.switchedAt(generation) {
			return "", true, nil
		}
		if !proved(signing, "fetch") {
			return "", false, ErrAgentKeyProof
		}
		if next.Generation == generation && next.Key != "" {
			return next.Key, false, nil
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", false, fmt.Errorf("generate agent key: %w", err)
		}
		issued := hex.EncodeToString(buf) // the form of the key a registration makes
		if err := m.writeAgentKeyNext(id, agentKeyNext{Generation: generation, Key: issued}); err != nil {
			return "", false, fmt.Errorf("record the agent's new key: %w", err)
		}
		return issued, false, nil
	}
	switch {
	case next.switchedAt(generation):
		// The agent lost the answer to its confirm: the key it proves with
		// is the one the managers sign with now.
		if !proved(signing, "confirm") {
			return "", false, ErrAgentKeyProof
		}
		return "", true, nil
	case next.Generation == generation && next.Key != "":
		if !proved(next.Key, "confirm") {
			return "", false, ErrAgentKeyProof
		}
		// The key first, the record after: a crash between leaves
		// agent_key/<id> the issued key, which the agent's next confirm
		// finishes by this same path. The other order would have the hint
		// say "switched" while the managers sign with the old key.
		if err := m.SetConfig(AgentKeyConfigKey(id), next.Key); err != nil {
			return "", false, fmt.Errorf("move the managers to the agent's new key: %w", err)
		}
		if err := m.writeAgentKeyNext(id, agentKeyNext{Generation: generation, SwitchedAt: time.Now().UTC()}); err != nil {
			return "", false, fmt.Errorf("record the agent's switch to its new key: %w", err)
		}
		slog.Info("the managers sign for an agent with its new key", "component", "cluster", "node_id", id, "generation", generation)
		return "", true, nil
	}
	return "", false, ErrNoAgentKeyDue
}

// agentKeyHint is what the leader's pong tells agent nodeID of the last agent
// key rotation: its generation, and whether the managers sign for the agent
// with that generation's key. Never a key. Nil with no rotation, from a node
// that is not the leader, and from a leader whose FSM has not caught up: one
// elected a moment ago may hold an older generation, or not yet the agent's
// switch, and would send it back for a key it holds.
func (m *Manager) agentKeyHint(nodeID string) *pb.AgentKeyHint {
	if m.raft == nil || !m.raft.caughtUpLeader(2*time.Second) {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	rec, ok := agentKeysRotationOf(state)
	if !ok {
		return nil
	}
	next, _ := agentKeyNextOf(state, nodeID)
	return &pb.AgentKeyHint{Generation: rec.Generation, Current: next.switchedAt(rec.Generation)}
}
