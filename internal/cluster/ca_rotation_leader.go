package cluster

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// The leader's half of a CA rotation; ca_rotation.go holds the record and the
// decision at each step. StartCARotation writes the next CA and then the
// record naming it once every gate has passed, and nothing before: no
// rotation under way; every node, agents too, online on a release that
// follows a rotation; the proxy secret switched for good, as the derived one
// is sha256(ca.crt); this leader's ca.crt the CA replicated state names.
// advanceCARotation then moves the record on evidence alone — what each node
// says on its heartbeat it trusts, and the CA behind the certificate its
// heartbeat connection presented, which the leader saw itself rather than
// took a node's word for. The trust and reissue phases go back once the time
// the start gave them is out, and the operator can stop a rotation
// (AbortCARotation) until promote: past it a node may have dropped the old
// CA, and there is no way back. A leader moves the record, and tells agents of
// it on their pongs, only once its FSM holds what earlier terms committed:
// one elected a moment ago may hold a step the cluster has moved past, and an
// agent that renewed under a CA the cluster dropped would be cut off.
const (
	DefaultCAPhaseTimeout = 30 * time.Minute
	MinCAPhaseTimeout     = 5 * time.Minute
	MaxCAPhaseTimeout     = 24 * time.Hour
)

// StartCARotation starts a rotation of the cluster CA (leader only): it makes
// the next CA, replicates it, and then the record that names it in its trust
// phase, which every node applies. phaseTimeout is how long the trust and
// reissue phases may each take before the rotation goes back; 0 is
// DefaultCAPhaseTimeout. Every gate comes before anything is written, and a
// refusal wraps ErrRotationRefused: a rotation under way
// (ErrCARotationInProgress), the proxy secret still switching
// (ErrProxyRotationPending), a node not online on a release that follows a
// rotation (ErrRotationWaiting, naming each), this leader's ca.crt or the
// cluster's CA in replicated state not the CA the last record left, or a
// phase time out of bounds. The log names the CAs by pin, never a key.
func (m *Manager) StartCARotation(phaseTimeout time.Duration) (CARotation, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return CARotation{}, ErrNotLeader
	}
	// The gates read what earlier terms committed: a leader that has not
	// caught up may not see the rotation under way, or the seq the last one
	// ended at.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return CARotation{}, fmt.Errorf("leader barrier failed: %w", err)
	}
	m.caRotateMu.Lock()
	defer m.caRotateMu.Unlock()
	now := time.Now()
	state := m.raft.GetFSM().GetState()
	last, ok, err := caRotationOf(state)
	if err != nil {
		return CARotation{}, fmt.Errorf("%w: %w", ErrRotationRefused, err)
	}
	if ok && last.Active() {
		return CARotation{}, fmt.Errorf("%w: %w", ErrRotationRefused, ErrCARotationInProgress)
	}
	if !m.ProxyRotationSettled(now) {
		return CARotation{}, fmt.Errorf("%w: %w", ErrRotationRefused, ErrProxyRotationPending)
	}
	if waiting := m.nodesNotReady(state, CapCARotation, true); len(waiting) > 0 {
		return CARotation{}, fmt.Errorf("%w: %w; waiting for %s", ErrRotationRefused, ErrRotationWaiting, strings.Join(waiting, ", "))
	}
	mat, err := caMaterialOf(state)
	if err != nil {
		return CARotation{}, fmt.Errorf("%w: %w", ErrRotationRefused, err)
	}
	cur := mat.Disk.Pin()
	// The record names the current CA where replicated state holds the
	// cluster's: a rotation that ended in done wrote the CA it moved to
	// there after its record.
	if _, err := namedCA(state, configKeyCACert, configKeyCAKey, cur); err != nil {
		return CARotation{}, fmt.Errorf("%w: the last CA rotation has not finished writing the cluster's CA, which the leader does within a minute: %w", ErrRotationRefused, err)
	}
	// The first step writes every ca.crt from replicated state: a leader
	// whose own starts with another CA signed with that one, and would trust
	// it no more.
	onDisk := "no certificate"
	if crt, err := m.tls.LoadCACert(); err == nil {
		if certs := pemCertificates(crt); len(certs) > 0 {
			onDisk = CAPin(certs[0].RawSubjectPublicKeyInfo)
		}
	}
	if onDisk != cur {
		return CARotation{}, fmt.Errorf("%w: this leader's ca.crt is not the cluster's CA: it starts with %s, and replicated state names %s", ErrRotationRefused, onDisk, cur)
	}
	if phaseTimeout == 0 {
		phaseTimeout = DefaultCAPhaseTimeout
	}
	if phaseTimeout < MinCAPhaseTimeout || phaseTimeout > MaxCAPhaseTimeout {
		return CARotation{}, fmt.Errorf("%w: a phase may take from %v to %v, not %v", ErrRotationRefused, MinCAPhaseTimeout, MaxCAPhaseTimeout, phaseTimeout)
	}

	next, err := GenerateCA(fmt.Sprintf("SFPanel CA - %s (%s)", state.Config["cluster_name"], now.UTC().Format("2006-01-02")), now)
	if err != nil {
		return CARotation{}, err
	}
	// The next CA before the record that names it: a node acts on the record
	// alone, once the CAs it names are there.
	if err := m.SetConfig(configKeyCANextCert, string(next.CertPEM)); err != nil {
		return CARotation{}, fmt.Errorf("replicate the next CA: %w", err)
	}
	if err := m.SetConfig(configKeyCANextKey, string(next.KeyPEM)); err != nil {
		return CARotation{}, fmt.Errorf("replicate the next CA's key: %w", err)
	}
	rec := CARotation{
		ID:           uuid.New().String(),
		Seq:          last.Seq + 1,
		Phase:        CAPhaseTrust,
		OldPin:       cur,
		NextPin:      next.Pin(),
		StartedAt:    now.UTC(),
		PhaseAt:      now.UTC(),
		PhaseTimeout: int64(phaseTimeout / time.Second),
	}
	if err := m.writeCARotation(rec); err != nil {
		return CARotation{}, fmt.Errorf("record the CA rotation: %w", err)
	}
	slog.Info("started a CA rotation: every node is to trust the next CA", "component", "cluster",
		"seq", rec.Seq, "current_ca", rec.OldPin, "next_ca", rec.NextPin, "phase_timeout", phaseTimeout.String())
	return rec, nil
}

// MaybeAdvanceCARotation moves the CA rotation on as far as what this leader
// knows of every node allows. Leader only; on every watchLeader tick.
func (m *Manager) MaybeAdvanceCARotation() error {
	return m.advanceCARotation(time.Now())
}

// advanceCARotation moves the CA rotation on by one step when what this
// leader knows of every node at now allows it (nextCAStep): the record with
// the next seq, and after it, at promote, the next CA written as the
// cluster's, and once the rotation has ended, the next CA taken out of
// replicated state. What a step leaves for after its record is finished here
// first when it was not — a write that failed, or a leader that stopped
// between the two. Leader only; nil with no rotation, and while this
// leader's FSM has not caught up.
func (m *Manager) advanceCARotation(now time.Time) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	if !m.raft.caughtUpLeader(2 * time.Second) {
		return nil
	}
	m.caRotateMu.Lock()
	defer m.caRotateMu.Unlock()
	state := m.raft.GetFSM().GetState()
	rec, ok, err := caRotationOf(state)
	if err != nil || !ok {
		return err
	}
	// The next CA leaves only once it is the cluster's: taken out before,
	// no node would find the CA a done record names.
	if rec.Phase == CAPhasePromote || rec.Phase == CAPhaseDone {
		if err := m.writeNextCAAsTheClusters(state, rec); err != nil {
			return err
		}
	}
	if rec.Phase == CAPhaseDone || rec.Phase == CAPhaseAborted {
		return m.removeNextCA(state)
	}

	step := nextCAStep(rec, m.caNodes(state), now)
	m.noteCAWaiting(rec, step.Waiting)
	if step.Next == "" {
		return nil
	}
	next := rec
	next.Seq, next.Phase, next.PhaseAt = rec.Seq+1, step.Next, now.UTC()
	// Only the step that turns back gives a reason; the way back keeps it.
	if step.Reason != "" {
		next.Reason = step.Reason
	}
	if err := m.writeCARotation(next); err != nil {
		return fmt.Errorf("record the CA rotation's %s phase: %w", next.Phase, err)
	}
	if step.Reason != "" {
		slog.Warn("a CA rotation goes back to the current CA", "component", "cluster", "phase", next.Phase, "seq", next.Seq, "reason", step.Reason)
	} else {
		slog.Info("a CA rotation moved on", "component", "cluster", "phase", next.Phase, "seq", next.Seq)
	}
	switch next.Phase {
	case CAPhasePromote:
		// After the record: until a node sees it, cluster_ca_* is its
		// current CA, and from promote on it reads the next CA from either
		// place.
		return m.writeNextCAAsTheClusters(state, next)
	case CAPhaseDone, CAPhaseAborted:
		return m.removeNextCA(state)
	}
	return nil
}

// AbortCARotation stops the CA rotation before its point of no return (leader
// only) and keeps the current CA: from trust the next CA is dropped, from
// reissue every certificate the next CA signed moves back to the current one
// first (abortPhase). The record says the operator stopped it. Refused past
// promote (ErrCARotationPastReturn), with no rotation under way
// (ErrNoCARotation), and on one going back already.
func (m *Manager) AbortCARotation() (CARotation, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return CARotation{}, ErrNotLeader
	}
	// As in StartCARotation: a leader that has not caught up would stop a
	// step the cluster may have moved past.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return CARotation{}, fmt.Errorf("leader barrier failed: %w", err)
	}
	m.caRotateMu.Lock()
	defer m.caRotateMu.Unlock()
	rec, _, err := caRotationOf(m.raft.GetFSM().GetState())
	if err != nil {
		return CARotation{}, err
	}
	phase, err := abortPhase(rec) // no record: ErrNoCARotation
	if err != nil {
		return CARotation{}, err
	}
	next := rec
	next.Seq, next.Phase, next.PhaseAt, next.Reason = rec.Seq+1, phase, time.Now().UTC(), "aborted by the operator"
	if err := m.writeCARotation(next); err != nil {
		return CARotation{}, fmt.Errorf("record the stop: %w", err)
	}
	slog.Info("the operator stopped a CA rotation: it goes back to the current CA", "component", "cluster", "phase", next.Phase, "seq", next.Seq)
	return next, nil
}

// CARotationActive reports whether a CA rotation is under way, which joins,
// registrations, conversions, promotions, join tokens and updates wait for. A
// record that does not parse counts as one: a join refused is tried again,
// and a node signed by a CA a rotation moves away from is cut off.
func (m *Manager) CARotationActive() bool {
	if m.raft == nil {
		return false
	}
	rec, ok, err := caRotationOf(m.raft.GetFSM().GetState())
	return err != nil || (ok && rec.Active())
}

// caNodes is what this leader knows of every node for a rotation step: each
// one's last heartbeat — which CA it says it trusts, which signed the
// certificate its connection presented, and when it came — and itself from
// its own files, as of now.
func (m *Manager) caNodes(state ClusterState) []caNode {
	health := m.heartbeat.CheckHealth()
	seen := m.heartbeat.GetLastSeen()
	nodes := make([]caNode, 0, len(state.Nodes))
	for id, n := range state.Nodes {
		node := caNode{ID: id, Name: n.Name, Manager: n.IsManager()}
		switch met := m.heartbeat.GetMetrics(id); {
		case id == m.nodeID:
			node.Version, node.HeardFrom, node.Online, node.Capable = m.version, true, true, true
			node.Trusts, node.CertCA, node.HeardAt = m.tls.TrustPins(), m.tls.ServedCertCAPin(), time.Now()
		case met != nil:
			node.Version, node.HeardFrom, node.Online = met.Version, true, health[id] == StatusOnline
			node.Capable = slices.Contains(met.Capabilities, CapCARotation)
			node.Trusts, node.CertCA, node.HeardAt = met.CATrustPins, met.CertCAPin, seen[id]
		}
		nodes = append(nodes, node)
	}
	slices.SortFunc(nodes, func(a, b caNode) int { return strings.Compare(a.ID, b.ID) })
	return nodes
}

// caHint is what this leader tells agents of the cluster CA on each pong:
// the CAs to trust, the one ca.crt starts with first, the CA their
// certificates must come from, and the record's seq. Nil with no rotation
// record, off the leader, and on a leader whose FSM has not caught up in its
// term: a leader elected a moment ago may hold a step the cluster has moved
// past, and an agent that renewed under a dropped CA would be cut off.
func (m *Manager) caHint() *pb.ClusterCA {
	if m.raft == nil || !m.raft.caughtUpLeader(2*time.Second) {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	if _, ok, err := caRotationOf(state); err != nil || !ok {
		return nil
	}
	mat, err := caMaterialOf(state)
	if err != nil {
		return nil
	}
	return &pb.ClusterCA{Bundle: mat.TrustPEM(), SignPin: mat.SignPin(), Seq: mat.Seq}
}

// noteCAWaiting says whom the rotation's step waits for, once while that
// stays the same in the same phase: the leader asks on every watchLeader
// tick. Caller holds caRotateMu.
func (m *Manager) noteCAWaiting(rec CARotation, waiting []string) {
	note := ""
	if len(waiting) > 0 {
		note = string(rec.Phase) + ": " + strings.Join(waiting, ", ")
	}
	if note == m.caWaiting {
		return
	}
	m.caWaiting = note
	if note != "" {
		slog.Info("a CA rotation waits", "component", "cluster", "phase", rec.Phase, "waiting", strings.Join(waiting, ", "))
	}
}

// writeCARotation replicates the rotation record.
func (m *Manager) writeCARotation(r CARotation) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return m.SetConfig(configKeyCARotation, string(data))
}

// writeNextCAAsTheClusters writes the next CA over cluster_ca_{cert,key}
// unless they hold it already: from promote on it is the cluster's CA, and
// replicated state names it there, for releases from before rotations too.
func (m *Manager) writeNextCAAsTheClusters(state ClusterState, rec CARotation) error {
	if _, err := namedCA(state, configKeyCACert, configKeyCAKey, rec.NextPin); err == nil {
		return nil
	}
	next, err := namedCA(state, configKeyCANextCert, configKeyCANextKey, rec.NextPin)
	if err != nil {
		return fmt.Errorf("write the next CA as the cluster's: %w", err)
	}
	for _, kv := range [][2]string{{configKeyCACert, string(next.CertPEM)}, {configKeyCAKey, string(next.KeyPEM)}} {
		if state.Config[kv[0]] == kv[1] {
			continue
		}
		if err := m.SetConfig(kv[0], kv[1]); err != nil {
			return fmt.Errorf("write the next CA as the cluster's, %s: %w", kv[0], err)
		}
	}
	return nil
}

// removeNextCA takes the next CA out of replicated state once the rotation
// has ended, its key first: what a failure between the two leaves is a
// certificate.
func (m *Manager) removeNextCA(state ClusterState) error {
	for _, key := range []string{configKeyCANextKey, configKeyCANextCert} {
		if _, ok := state.Config[key]; !ok {
			continue
		}
		if err := m.raft.Apply(Command{Type: CmdDeleteConfig, Key: key}, 5*time.Second); err != nil {
			return fmt.Errorf("remove %s: %w", key, err)
		}
	}
	return nil
}
