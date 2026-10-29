package cluster

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The record of a CA rotation, and the decision at each of its steps. The
// next CA sits in replicated state beside the cluster's own, and the record
// names both by pin. The leader writes the next CA and then the record; every
// later step writes the record again with the next seq. Every node acts on
// the record alone, and only once the CAs it names are there: replicated
// state changes one key per entry, and between two writes of a step a node
// sees half of it. The record is never deleted — a rotation ends in done or
// aborted, and the next one's seq goes on from there — so a node that keeps
// the seq it applied never takes the older step a lagging FSM shows for a
// newer one.
const (
	configKeyCANextCert = "cluster_ca_next_cert"
	configKeyCANextKey  = "cluster_ca_next_key"
	configKeyCARotation = "cluster_ca_rotation" // JSON CARotation
	// CapCARotation is the heartbeat capability of a node that follows a CA
	// rotation's record — what a rotation waits for on every node, agents
	// too, before it starts.
	CapCARotation = "ca-rotation-v1"
)

// CAPhase is a CA rotation's step. A rotation goes trust (every node trusts
// both CAs) → reissue (the next CA signs, and every node moves its
// certificate to it) → promote (the next CA is the cluster's, and every node
// drops the old one) → done. Stopped before promote — its phase out of time,
// or by the operator — it goes back keeping the current CA: rollback (the
// current CA signs again, and every certificate the next CA signed moves
// back) → drop (every node drops the next CA) → aborted. Past promote there
// is no way back: a node may have dropped the old CA already.
type CAPhase string

const (
	CAPhaseTrust, CAPhaseReissue, CAPhasePromote, CAPhaseDone CAPhase = "trust", "reissue", "promote", "done"
	CAPhaseRollback, CAPhaseDrop, CAPhaseAborted              CAPhase = "rollback", "drop", "aborted"
)

// CARotation is the record of a CA rotation (configKeyCARotation). OldPin and
// NextPin are the two CAs (CAPin), which the pairs in replicated state must
// match before a node acts on the record. The trust and reissue phases may
// take PhaseTimeout seconds from PhaseAt (none if it is not positive), and
// Reason says why a rotation went back.
type CARotation struct {
	ID           string    `json:"id"`
	Seq          uint64    `json:"seq"`
	Phase        CAPhase   `json:"phase"`
	OldPin       string    `json:"old_pin"`
	NextPin      string    `json:"next_pin"`
	StartedAt    time.Time `json:"started_at"`
	PhaseAt      time.Time `json:"phase_at"`
	PhaseTimeout int64     `json:"phase_timeout_seconds"`
	Reason       string    `json:"reason,omitempty"`
}

// Active reports whether the rotation is under way: trust, reissue, promote,
// rollback or drop. One that has ended, or whose phase this release does not
// know, is not.
func (r CARotation) Active() bool {
	switch r.Phase {
	case CAPhaseTrust, CAPhaseReissue, CAPhasePromote, CAPhaseRollback, CAPhaseDrop:
		return true
	}
	return false
}

// caRotationOf reads the CA rotation record: false with none, as in a
// cluster that never rotated its CA. A record that does not parse is an
// error, on which the caller changes nothing.
func caRotationOf(state ClusterState) (CARotation, bool, error) {
	raw := state.Config[configKeyCARotation]
	if raw == "" {
		return CARotation{}, false, nil
	}
	var r CARotation
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return CARotation{}, false, fmt.Errorf("parse the CA rotation record: %w", err)
	}
	return r, true, nil
}

// errCAViewIncomplete: the rotation record names a CA this node's replicated
// state does not hold yet, or holds another under that name — a step half
// replicated, which the next change completes.
var errCAViewIncomplete = errors.New("the CA rotation record names a CA this node's replicated state does not hold yet")

// caMaterial is the CAs a node holds in a rotation's phase (caMaterialOf).
type caMaterial struct {
	Seq   uint64
	Phase CAPhase
	Sign  *CAPair             // signs certificates now
	Disk  *CAPair             // the pair ca.crt's first block names, whose key ca.key holds
	Trust []*x509.Certificate // the CAs to trust, Disk's first
}

// SignPin is the pin of the CA that signs now, "" with none: a failure
// message prints it.
func (c caMaterial) SignPin() string {
	if c.Sign == nil {
		return ""
	}
	return c.Sign.Pin()
}

// TrustPEM is ca.crt as the phase has it: every CA to trust, Disk's first —
// the block joins, pins and older releases read as the cluster's CA.
func (c caMaterial) TrustPEM() []byte {
	var out []byte
	for _, cert := range c.Trust {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}
	return out
}

// TrustPins are the pins of the CAs to trust, in TrustPEM's order.
func (c caMaterial) TrustPins() []string {
	pins := make([]string, 0, len(c.Trust))
	for _, cert := range c.Trust {
		pins = append(pins, CAPin(cert.RawSubjectPublicKeyInfo))
	}
	return pins
}

// caMaterialOf is what replicated state has this node hold now: with no
// record, the cluster's CA alone; in a rotation, by its phase, where cur is
// cluster_ca_{cert,key} and next is cluster_ca_next_{cert,key} —
//
//	phase            signs  ca.crt first  trusts
//	trust            cur    cur           cur, next
//	reissue          next   cur           cur, next
//	promote, done    next   next          next
//	rollback         cur    cur           cur, next
//	drop, aborted    cur    cur           cur
//
// Each pair must be there, certificate and key, and be the CA the record
// names: cur OldPin, next NextPin. Else errCAViewIncomplete, and the caller
// moves nothing. From promote on, the next CA is taken from either place: the
// leader writes it over the cluster's own after the promote record and
// removes the next keys after the done record, and a new rotation writes its
// next CA there before its own record.
func caMaterialOf(state ClusterState) (caMaterial, error) {
	rec, ok, err := caRotationOf(state)
	if err != nil {
		return caMaterial{}, err
	}
	if !ok {
		cur, err := ParseCAPair([]byte(state.Config[configKeyCACert]), []byte(state.Config[configKeyCAKey]))
		if err != nil {
			return caMaterial{}, fmt.Errorf("the cluster CA in replicated state: %w", err)
		}
		return caMaterial{Sign: cur, Disk: cur, Trust: []*x509.Certificate{cur.Cert}}, nil
	}
	mat := caMaterial{Seq: rec.Seq, Phase: rec.Phase}
	switch rec.Phase {
	case CAPhaseTrust, CAPhaseReissue, CAPhaseRollback:
		cur, err := namedCA(state, configKeyCACert, configKeyCAKey, rec.OldPin)
		if err != nil {
			return caMaterial{}, err
		}
		next, err := namedCA(state, configKeyCANextCert, configKeyCANextKey, rec.NextPin)
		if err != nil {
			return caMaterial{}, err
		}
		mat.Sign, mat.Disk, mat.Trust = cur, cur, []*x509.Certificate{cur.Cert, next.Cert}
		if rec.Phase == CAPhaseReissue {
			mat.Sign = next
		}
	case CAPhasePromote, CAPhaseDone:
		next, err := namedCA(state, configKeyCANextCert, configKeyCANextKey, rec.NextPin)
		if err != nil {
			if next, err = namedCA(state, configKeyCACert, configKeyCAKey, rec.NextPin); err != nil {
				return caMaterial{}, fmt.Errorf("%w: %s is neither the next CA nor the cluster's", errCAViewIncomplete, rec.NextPin)
			}
		}
		mat.Sign, mat.Disk, mat.Trust = next, next, []*x509.Certificate{next.Cert}
	case CAPhaseDrop, CAPhaseAborted:
		cur, err := namedCA(state, configKeyCACert, configKeyCAKey, rec.OldPin)
		if err != nil {
			return caMaterial{}, err
		}
		mat.Sign, mat.Disk, mat.Trust = cur, cur, []*x509.Certificate{cur.Cert}
	default:
		return caMaterial{}, fmt.Errorf("the CA rotation record is in phase %q, which this release does not know", rec.Phase)
	}
	return mat, nil
}

// namedCA is the pair replicated state holds under certKey and keyKey, when
// it is the CA pin names.
func namedCA(state ClusterState, certKey, keyKey, pin string) (*CAPair, error) {
	ca, err := ParseCAPair([]byte(state.Config[certKey]), []byte(state.Config[keyKey]))
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%s, %s): %w", errCAViewIncomplete, pin, certKey, keyKey, err)
	}
	if got := ca.Pin(); got != pin {
		return nil, fmt.Errorf("%w: %s holds %s, not %s", errCAViewIncomplete, certKey, got, pin)
	}
	return ca, nil
}

// caNode is what the leader knows of a node at a rotation step: from its last
// heartbeat, what it says it trusts and which CA signed the certificate its
// connection presented. Capable: it reports CapCARotation.
type caNode struct {
	ID, Name, Version                   string
	Manager, HeardFrom, Online, Capable bool
	Trusts                              []string  // pins
	CertCA                              string    // pin of the CA behind the certificate its connection presented
	HeardAt                             time.Time // when that heartbeat came
}

// caStep is the leader's decision at a rotation step (nextCAStep).
type caStep struct {
	Next    CAPhase  // "" stays
	Waiting []string // sorted: who holds the step, each with why
	Reason  string   // set with an automatic rollback
}

// nextCAStep decides a rotation's step from what the leader knows of every
// node, at now: the phase that follows once every node the phase waits for
// shows it is ready —
//
//   - trust: every node says it trusts both CAs, before any certificate
//     moves;
//   - reissue: every node's connection presented a certificate from the next
//     CA, before the next CA becomes the cluster's;
//   - promote: every manager, and every agent online, says it trusts the next
//     CA alone. An agent away has passed the reissue step and holds a
//     certificate from the next CA: it catches up from the leader's pong when
//     it is back, and waiting for it would hold joins up with no way back;
//   - rollback: every node's connection presented a certificate from the
//     current CA again, before the next CA is dropped, on a heartbeat heard
//     since the phase began. Before the reissue every certificate came from
//     the current CA, so a node's last heartbeat from then says so of a node
//     renewed from the next CA since, whose new connection is still to come:
//     dropped on it, an agent is refused everywhere, and the drop renews
//     nothing;
//   - drop: every manager, and every agent online, says it trusts the
//     current CA alone; an agent away holds a certificate from it, and
//     catches up the same way.
//
// A node not heard from, offline or on a release without CapCARotation holds
// a step it counts in, named for that. Trust and reissue roll back once
// their PhaseTimeout has passed with nodes still waiting — trust to drop,
// reissue to rollback — with Reason naming who held them. Promote never
// does; rollback and drop wait as long as it takes, being the way back.
func nextCAStep(r CARotation, nodes []caNode, now time.Time) caStep {
	var (
		next, onTimeout CAPhase
		ready           func(caNode) bool
		why             string
		agentsOnline    bool // an agent counts only while online
		heardSince      bool // a heartbeat from before the phase began counts for nothing
	)
	switch r.Phase {
	case CAPhaseTrust:
		next, onTimeout, why = CAPhaseReissue, CAPhaseDrop, "does not trust the next CA yet"
		ready = func(n caNode) bool {
			return slices.Contains(n.Trusts, r.OldPin) && slices.Contains(n.Trusts, r.NextPin)
		}
	case CAPhaseReissue:
		next, onTimeout, why = CAPhasePromote, CAPhaseRollback, "still presents a certificate from the current CA"
		ready = func(n caNode) bool { return n.CertCA == r.NextPin }
	case CAPhasePromote:
		next, agentsOnline, why = CAPhaseDone, true, "still trusts the old CA"
		ready = func(n caNode) bool { return slices.Equal(n.Trusts, []string{r.NextPin}) }
	case CAPhaseRollback:
		next, heardSince, why = CAPhaseDrop, true, "still presents a certificate from the next CA"
		ready = func(n caNode) bool { return n.CertCA == r.OldPin }
	case CAPhaseDrop:
		next, agentsOnline, why = CAPhaseAborted, true, "still trusts the next CA"
		ready = func(n caNode) bool { return slices.Equal(n.Trusts, []string{r.OldPin}) }
	default:
		// done, aborted, or a phase this release does not know: nothing to
		// decide
		return caStep{}
	}
	var waiting []string
	for _, n := range nodes {
		if agentsOnline && !n.Manager && (!n.HeardFrom || !n.Online) {
			continue
		}
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
		case heardSince && !n.HeardAt.After(r.PhaseAt):
			waiting = append(waiting, fmt.Sprintf("%s (not heard from since the %s phase began)", name, r.Phase))
		case !ready(n):
			waiting = append(waiting, fmt.Sprintf("%s (%s)", name, why))
		}
	}
	if len(waiting) == 0 {
		return caStep{Next: next}
	}
	slices.Sort(waiting)
	step := caStep{Waiting: waiting}
	if timeout := time.Duration(r.PhaseTimeout) * time.Second; onTimeout != "" && timeout > 0 && now.After(r.PhaseAt.Add(timeout)) {
		step.Next = onTimeout
		step.Reason = fmt.Sprintf("the %s phase did not finish in %v: waiting for %s", r.Phase, timeout, strings.Join(waiting, ", "))
	}
	return step
}

// abortPhase is where an operator's stop leads from the rotation's phase:
// trust to drop, as no certificate has moved yet, and reissue to rollback,
// which moves every certificate the next CA signed back to the current one
// before the next CA is dropped. A rotation going back already, one past
// promote and one that has ended are not stopped.
func abortPhase(r CARotation) (CAPhase, error) {
	switch r.Phase {
	case CAPhaseTrust:
		return CAPhaseDrop, nil
	case CAPhaseReissue:
		return CAPhaseRollback, nil
	case CAPhaseRollback, CAPhaseDrop:
		return "", fmt.Errorf("the rotation is going back to the current CA already: %w", ErrCARotationInProgress)
	case CAPhasePromote:
		return "", ErrCARotationPastReturn
	case CAPhaseDone, CAPhaseAborted, "":
		return "", ErrNoCARotation
	}
	return "", fmt.Errorf("the CA rotation is in phase %q, which this release does not know", r.Phase)
}
