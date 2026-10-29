package cluster

import (
	"slices"
	"strings"
	"time"
)

// RotationStatus is where the cluster stands in its secret rotations, node
// by node: what GET /cluster/rotation answers, and a rotation's screen polls.
// It names every secret by its generation or its pin and carries none — no
// key, no certificate PEM, no secret of any kind.
type RotationStatus struct {
	JWT JWTRotationStatus `json:"jwt"`
	CA  CARotationStatus  `json:"ca"`
}

// JWTRotationStatus is the cluster's JWT key by its generation — 0 for the
// key a cluster has had since its init — and the generation each manager
// runs.
type JWTRotationStatus struct {
	Generation int64            `json:"generation"`
	RotatedAt  *time.Time       `json:"rotated_at,omitempty"`
	Managers   []JWTManagerView `json:"managers"`
}

// JWTManagerView is a manager in a JWT key rotation: the generation it runs
// — this leader's own, the others' as their heartbeats report it — and
// whether it follows a rotation (CapJWTRotation).
type JWTManagerView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Online     bool   `json:"online"`
	Generation *int64 `json:"generation,omitempty"` // nil: not reported
	CanRotate  bool   `json:"can_rotate"`
}

// CARotationStatus is the cluster CA by its pin — while a rotation is under
// way, the one it moves away from — and the last CA rotation. RaftTLS says
// whether Raft's port is under TLS at all: on a cluster whose port is not,
// a CA rotation does not reach it.
type CARotationStatus struct {
	CurrentPin string          `json:"current_pin"`
	RaftTLS    bool            `json:"raft_tls"`
	Rotation   *CARotationView `json:"rotation,omitempty"` // nil: never rotated
}

// CARotationView is the CA rotation record, with when its phase runs out
// (trust and reissue only, which go back then), whom its step waits for and
// what the leader knows of each node.
type CARotationView struct {
	CARotation
	Deadline *time.Time   `json:"deadline,omitempty"`
	Waiting  []string     `json:"waiting,omitempty"`
	Nodes    []CANodeView `json:"nodes"`
}

// CANodeView is a node in a CA rotation as the leader knows it from its last
// heartbeat (this leader from its own files): the CAs it says it trusts, and
// the CA behind the certificate its connection presented, both by pin; and
// whether it follows a rotation (CapCARotation).
type CANodeView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Online    bool     `json:"online"`
	Trusts    []string `json:"trusts"`
	CertCA    string   `json:"cert_ca"`
	CanRotate bool     `json:"can_rotate"`
}

// RotationStatus is where the cluster stands in its rotations (leader only:
// only the leader hears every node). It is refused when this node cannot
// confirm it still leads: one cut off from the others would show what it
// last heard. Secrets go by generation and CAs by pin; no key or certificate
// PEM is read into it.
func (m *Manager) RotationStatus() (*RotationStatus, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return nil, ErrNotLeader
	}
	if err := m.raft.VerifyLeader(2 * time.Second); err != nil {
		return nil, ErrNotLeader
	}
	state := m.raft.GetFSM().GetState()
	ca, err := m.caRotationStatus(state, time.Now())
	if err != nil {
		return nil, err
	}
	return &RotationStatus{JWT: m.jwtRotationStatus(state), CA: ca}, nil
}

// jwtRotationStatus is the JWT half of RotationStatus. This leader's own
// generation is the one it runs, and nil while it has no applier: it then
// cannot follow a rotation, nor start one.
func (m *Manager) jwtRotationStatus(state ClusterState) JWTRotationStatus {
	st := JWTRotationStatus{Managers: []JWTManagerView{}}
	if k, ok := clusterJWTKey(state); ok {
		st.Generation = k.Generation
		if !k.RotatedAt.IsZero() {
			at := k.RotatedAt
			st.RotatedAt = &at
		}
	}
	health := m.heartbeat.CheckHealth()
	for id, n := range state.Nodes {
		if !n.IsManager() {
			continue
		}
		v := JWTManagerView{ID: id, Name: n.Name}
		switch met := m.heartbeat.GetMetrics(id); {
		case id == m.nodeID:
			v.Online, v.CanRotate = true, m.jwtApplierSet.Load()
			if v.CanRotate {
				gen := m.JWTGeneration()
				v.Generation = &gen
			}
		case met != nil:
			v.Online = health[id] == StatusOnline
			v.CanRotate = slices.Contains(met.Capabilities, CapJWTRotation)
			if met.JWTGeneration != nil {
				gen := *met.JWTGeneration
				v.Generation = &gen
			}
		}
		st.Managers = append(st.Managers, v)
	}
	slices.SortFunc(st.Managers, func(a, b JWTManagerView) int { return strings.Compare(a.ID, b.ID) })
	return st
}

// caRotationStatus is the CA half of RotationStatus at now: the record as it
// stands, whom its step waits for (nextCAStep, which decides the step) and
// each node as the step sees it (caNodes). A record that does not parse is
// an error: joins and updates wait while it stands (CARotationActive), and a
// status without it would hide why.
func (m *Manager) caRotationStatus(state ClusterState, now time.Time) (CARotationStatus, error) {
	st := CARotationStatus{RaftTLS: m.config.RaftTLS}
	rec, ok, err := caRotationOf(state)
	if err != nil {
		return st, err
	}
	if ok && rec.Active() {
		st.CurrentPin = rec.OldPin
	} else if certs := pemCertificates([]byte(state.Config[configKeyCACert])); len(certs) > 0 {
		st.CurrentPin = CAPin(certs[0].RawSubjectPublicKeyInfo)
	}
	if !ok {
		return st, nil
	}
	nodes := m.caNodes(state)
	view := &CARotationView{CARotation: rec, Waiting: nextCAStep(rec, nodes, now).Waiting, Nodes: make([]CANodeView, 0, len(nodes))}
	if timeout := time.Duration(rec.PhaseTimeout) * time.Second; timeout > 0 && (rec.Phase == CAPhaseTrust || rec.Phase == CAPhaseReissue) {
		deadline := rec.PhaseAt.Add(timeout)
		view.Deadline = &deadline
	}
	for _, n := range nodes {
		kind := KindAgent
		if n.Manager {
			kind = KindManager
		}
		trusts := n.Trusts
		if trusts == nil {
			trusts = []string{}
		}
		view.Nodes = append(view.Nodes, CANodeView{ID: n.ID, Name: n.Name, Kind: string(kind), Online: n.Online,
			Trusts: trusts, CertCA: n.CertCA, CanRotate: n.Capable})
	}
	st.Rotation = view
	return st, nil
}
