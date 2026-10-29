package cluster

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNextCAStep(t *testing.T) {
	old, next := "sha256:old", "sha256:next"
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rec := func(p CAPhase) CARotation {
		return CARotation{Phase: p, OldPin: old, NextPin: next, PhaseAt: at, PhaseTimeout: int64((30 * time.Minute).Seconds())}
	}
	node := func(id string, manager bool, trusts []string, certCA string) caNode {
		return caNode{ID: id, Name: id, Version: "test", Manager: manager, HeardFrom: true, Online: true, Capable: true, Trusts: trusts, CertCA: certCA}
	}
	offline := func(n caNode) caNode { n.Online = false; return n }
	older := func(n caNode) caNode { n.Capable, n.Version = false, "0.79.0"; return n }
	unheard := func(n caNode) caNode { n.HeardFrom, n.Online = false, false; return n }
	unlimited := func(r CARotation) CARotation { r.PhaseTimeout = 0; return r }
	both, onlyOld, onlyNext := []string{old, next}, []string{old}, []string{next}
	soon, late := at.Add(time.Minute), at.Add(31*time.Minute)
	cases := []struct {
		name    string
		r       CARotation
		nodes   []caNode
		now     time.Time
		want    CAPhase
		waiting string // one of the step's waiting entries, "" for none
	}{
		{"every node trusts both", rec(CAPhaseTrust), []caNode{node("m2", true, both, old), node("a1", false, both, old)}, soon, CAPhaseReissue, ""},
		{"an agent does not trust the next CA yet", rec(CAPhaseTrust), []caNode{node("m2", true, both, old), node("a1", false, onlyOld, old)}, soon, "", "a1 (does not trust the next CA yet)"},
		{"an offline agent holds the trust phase", rec(CAPhaseTrust), []caNode{offline(node("a1", false, both, old))}, soon, "", "a1 (offline)"},
		{"an agent on an older release holds it", rec(CAPhaseTrust), []caNode{older(node("a1", false, nil, old))}, soon, "", "a1 (0.79.0)"},
		{"past its time the trust phase rolls back", rec(CAPhaseTrust), []caNode{node("a1", false, onlyOld, old)}, late, CAPhaseDrop, "a1 (does not trust the next CA yet)"},
		{"every certificate comes from the next CA", rec(CAPhaseReissue), []caNode{node("m2", true, both, next), node("a1", false, both, next)}, soon, CAPhasePromote, ""},
		{"a node still presents a certificate from the current CA", rec(CAPhaseReissue), []caNode{node("a1", false, both, old)}, soon, "", "a1 (still presents a certificate from the current CA)"},
		{"past its time the reissue phase rolls back", rec(CAPhaseReissue), []caNode{node("a1", false, both, old)}, late, CAPhaseRollback, "a1 (still presents a certificate from the current CA)"},
		{"promote waits for every manager to drop the old CA", rec(CAPhasePromote), []caNode{node("m2", true, both, next)}, soon, "", "m2 (still trusts the old CA)"},
		{"promote does not wait for an offline agent", rec(CAPhasePromote), []caNode{node("m2", true, onlyNext, next), offline(node("a1", false, both, next))}, soon, CAPhaseDone, ""},
		{"promote never rolls back", rec(CAPhasePromote), []caNode{node("m2", true, both, next)}, at.Add(48 * time.Hour), "", "m2 (still trusts the old CA)"},
		{"rollback waits for every certificate to come back", rec(CAPhaseRollback), []caNode{node("a1", false, both, next)}, late, "", "a1 (still presents a certificate from the next CA)"},
		{"rollback ends in the drop", rec(CAPhaseRollback), []caNode{node("a1", false, both, old)}, soon, CAPhaseDrop, ""},
		{"the drop waits for the managers", rec(CAPhaseDrop), []caNode{node("m2", true, both, old)}, late, "", "m2 (still trusts the next CA)"},
		{"the drop ends the rotation", rec(CAPhaseDrop), []caNode{node("m2", true, onlyOld, old), offline(node("a1", false, both, old))}, soon, CAPhaseAborted, ""},
		// Every manager counts where an agent away does not, whether this
		// leader has not heard from it or it went quiet.
		{"a manager not heard from holds the trust phase", rec(CAPhaseTrust), []caNode{unheard(node("m2", true, both, old))}, soon, "", "m2 (not heard from)"},
		{"promote waits for an offline manager", rec(CAPhasePromote), []caNode{offline(node("m2", true, onlyNext, next))}, soon, "", "m2 (offline)"},
		{"promote does not wait for an agent not heard from", rec(CAPhasePromote), []caNode{node("m2", true, onlyNext, next), unheard(node("a1", false, nil, ""))}, soon, CAPhaseDone, ""},
		{"the drop waits for a manager not heard from", rec(CAPhaseDrop), []caNode{unheard(node("m2", true, onlyOld, old))}, late, "", "m2 (not heard from)"},
		// The time limit is for a phase nodes hold up: one with every node
		// ready moves on, and one with no limit waits.
		{"past its time a phase every node finished moves on", rec(CAPhaseTrust), []caNode{node("a1", false, both, old)}, late, CAPhaseReissue, ""},
		{"a phase with no time limit never rolls back", unlimited(rec(CAPhaseReissue)), []caNode{node("a1", false, both, old)}, at.Add(48 * time.Hour), "", "a1 (still presents a certificate from the current CA)"},
		{"a rotation that ended goes nowhere", rec(CAPhaseDone), []caNode{offline(node("m2", true, both, old))}, late, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := nextCAStep(tc.r, tc.nodes, tc.now)
			if step.Next != tc.want {
				t.Fatalf("next %q, want %q (waiting %v)", step.Next, tc.want, step.Waiting)
			}
			if tc.waiting != "" && !slices.Contains(step.Waiting, tc.waiting) {
				t.Fatalf("waiting %v, want %q among them", step.Waiting, tc.waiting)
			}
			automatic := tc.r.Phase != tc.want && (tc.want == CAPhaseDrop || tc.want == CAPhaseRollback) && tc.r.Phase != CAPhaseRollback
			if automatic && !strings.Contains(step.Reason, "did not finish in 30m0s") {
				t.Fatalf("an automatic rollback with reason %q", step.Reason)
			}
		})
	}
}

// caState is replicated state as a rotation in phase leaves it: cur in
// cluster_ca_*, next in cluster_ca_next_*, and the record.
func caState(t *testing.T, cur, next *CAPair, phase CAPhase) ClusterState {
	t.Helper()
	r := CARotation{ID: "r1", Seq: 4, Phase: phase, OldPin: cur.Pin(), NextPin: next.Pin()}
	return ClusterState{Config: map[string]string{
		configKeyCACert: string(cur.CertPEM), configKeyCAKey: string(cur.KeyPEM),
		configKeyCANextCert: string(next.CertPEM), configKeyCANextKey: string(next.KeyPEM),
		configKeyCARotation: string(mustJSON(r)),
	}}
}

// Each phase has one CA that signs and a list to trust, the CA ca.crt
// starts with first — what joins, pins and older releases read as the
// cluster's. A step half replicated is not taken for a whole one.
func TestCAMaterialOf(t *testing.T) {
	cur, _ := GenerateCA("cur", time.Now())
	next, _ := GenerateCA("next", time.Now())
	for _, tc := range []struct {
		phase      CAPhase
		sign, disk string
		trust      []string
	}{
		{CAPhaseTrust, cur.Pin(), cur.Pin(), []string{cur.Pin(), next.Pin()}},
		{CAPhaseReissue, next.Pin(), cur.Pin(), []string{cur.Pin(), next.Pin()}},
		{CAPhasePromote, next.Pin(), next.Pin(), []string{next.Pin()}},
		{CAPhaseRollback, cur.Pin(), cur.Pin(), []string{cur.Pin(), next.Pin()}},
		{CAPhaseDrop, cur.Pin(), cur.Pin(), []string{cur.Pin()}},
	} {
		mat, err := caMaterialOf(caState(t, cur, next, tc.phase))
		if err != nil || mat.SignPin() != tc.sign || mat.Disk.Pin() != tc.disk || !slices.Equal(mat.TrustPins(), tc.trust) {
			t.Fatalf("%s: sign %s disk %v trust %v (%v)", tc.phase, mat.SignPin(), mat.Disk, mat.TrustPins(), err)
		}
	}
	// promote after the leader wrote the next CA over the current one, and
	// done after it removed the next keys
	done := caState(t, next, next, CAPhaseDone)
	delete(done.Config, configKeyCANextCert)
	delete(done.Config, configKeyCANextKey)
	r := CARotation{Seq: 6, Phase: CAPhaseDone, OldPin: cur.Pin(), NextPin: next.Pin()}
	done.Config[configKeyCARotation] = string(mustJSON(r))
	if mat, err := caMaterialOf(done); err != nil || mat.SignPin() != next.Pin() {
		t.Fatalf("done: %v %v", mat.SignPin(), err)
	}
	// the record before the next CA's key
	torn := caState(t, cur, next, CAPhaseTrust)
	delete(torn.Config, configKeyCANextKey)
	if _, err := caMaterialOf(torn); !errors.Is(err, errCAViewIncomplete) {
		t.Fatalf("a next CA without its key: %v", err)
	}
	// a record naming another next CA
	other, _ := GenerateCA("other", time.Now())
	wrong := caState(t, cur, other, CAPhaseTrust)
	wrong.Config[configKeyCARotation] = string(mustJSON(CARotation{Seq: 4, Phase: CAPhaseTrust, OldPin: cur.Pin(), NextPin: next.Pin()}))
	if _, err := caMaterialOf(wrong); !errors.Is(err, errCAViewIncomplete) {
		t.Fatalf("a record naming another CA: %v", err)
	}
}

// With no record a node holds the cluster's CA alone; a record moves nothing
// unless the current CA too is the one it names, nor while it does not
// parse; and the next rotation's CA, written before that rotation's record,
// is not taken for the one the done record names.
func TestCAMaterialOfTakesNoOtherCA(t *testing.T) {
	cur, _ := GenerateCA("cur", time.Now())
	next, _ := GenerateCA("next", time.Now())
	other, _ := GenerateCA("other", time.Now())
	none := caState(t, cur, next, CAPhaseTrust)
	delete(none.Config, configKeyCARotation)
	if mat, err := caMaterialOf(none); err != nil || mat.SignPin() != cur.Pin() || mat.Disk.Pin() != cur.Pin() || !slices.Equal(mat.TrustPins(), []string{cur.Pin()}) {
		t.Fatalf("no record: sign %s trust %v (%v)", mat.SignPin(), mat.TrustPins(), err)
	}
	wrongCur := caState(t, other, next, CAPhaseTrust)
	wrongCur.Config[configKeyCARotation] = string(mustJSON(CARotation{Seq: 4, Phase: CAPhaseTrust, OldPin: cur.Pin(), NextPin: next.Pin()}))
	if _, err := caMaterialOf(wrongCur); !errors.Is(err, errCAViewIncomplete) {
		t.Fatalf("a record naming another current CA: %v", err)
	}
	broken := caState(t, cur, next, CAPhaseTrust)
	broken.Config[configKeyCARotation] = "{"
	if _, ok, err := caRotationOf(broken); ok || err == nil || !strings.Contains(err.Error(), "parse the CA rotation record") {
		t.Fatalf("a record that does not parse: %v %v", ok, err)
	}
	if _, err := caMaterialOf(broken); err == nil || !strings.Contains(err.Error(), "parse the CA rotation record") {
		t.Fatalf("material from a record that does not parse: %v", err)
	}
	// the leader halfway through writing the next CA over the cluster's own
	half := caState(t, cur, next, CAPhasePromote)
	half.Config[configKeyCACert] = string(next.CertPEM)
	if mat, err := caMaterialOf(half); err != nil || mat.SignPin() != next.Pin() {
		t.Fatalf("promote halfway: %v %v", mat.SignPin(), err)
	}
	after := caState(t, next, other, CAPhaseDone)
	after.Config[configKeyCARotation] = string(mustJSON(CARotation{Seq: 6, Phase: CAPhaseDone, OldPin: cur.Pin(), NextPin: next.Pin()}))
	if mat, err := caMaterialOf(after); err != nil || mat.SignPin() != next.Pin() || !slices.Equal(mat.TrustPins(), []string{next.Pin()}) {
		t.Fatalf("done, with the next rotation's CA in: sign %s trust %v (%v)", mat.SignPin(), mat.TrustPins(), err)
	}
}

// ca.crt as a phase has it is every CA to trust, in order, in a bundle
// another node's strict reader takes whole — and no key.
func TestTrustPEMIsTheCAsToTrust(t *testing.T) {
	cur, _ := GenerateCA("cur", time.Now())
	next, _ := GenerateCA("next", time.Now())
	mat, err := caMaterialOf(caState(t, cur, next, CAPhaseReissue))
	if err != nil {
		t.Fatal(err)
	}
	certs, err := certificatesIn(mat.TrustPEM())
	if err != nil {
		t.Fatal(err)
	}
	var pins []string
	for _, c := range certs {
		pins = append(pins, CAPin(c.RawSubjectPublicKeyInfo))
	}
	if !slices.Equal(pins, []string{cur.Pin(), next.Pin()}) || !slices.Equal(pins, mat.TrustPins()) {
		t.Fatalf("ca.crt would hold %v, trust pins %v", pins, mat.TrustPins())
	}
	if strings.Contains(string(mat.TrustPEM()), "PRIVATE KEY") {
		t.Fatal("ca.crt would hold a key")
	}
}

func TestAbortPhase(t *testing.T) {
	for phase, want := range map[CAPhase]CAPhase{CAPhaseTrust: CAPhaseDrop, CAPhaseReissue: CAPhaseRollback} {
		if got, err := abortPhase(CARotation{Phase: phase}); err != nil || got != want {
			t.Fatalf("%s: %s %v", phase, got, err)
		}
	}
	if _, err := abortPhase(CARotation{Phase: CAPhasePromote}); !errors.Is(err, ErrCARotationPastReturn) {
		t.Fatalf("promote: %v", err)
	}
	if _, err := abortPhase(CARotation{Phase: CAPhaseDone}); !errors.Is(err, ErrNoCARotation) {
		t.Fatalf("done: %v", err)
	}
}

// A rotation going back already is not stopped again, and one that ended or
// never began has nothing to stop.
func TestAbortPhaseRefusesWhatIsNotUnderWay(t *testing.T) {
	for phase, want := range map[CAPhase]error{
		CAPhaseRollback: ErrCARotationInProgress, CAPhaseDrop: ErrCARotationInProgress,
		CAPhaseAborted: ErrNoCARotation, "": ErrNoCARotation,
	} {
		if got, err := abortPhase(CARotation{Phase: phase}); got != "" || !errors.Is(err, want) {
			t.Fatalf("%q: %q %v, want %v", phase, got, err, want)
		}
	}
}

// A rotation is under way from trust until it is done or aborted: joins and
// the rest wait while it is.
func TestCARotationActive(t *testing.T) {
	for phase, want := range map[CAPhase]bool{
		CAPhaseTrust: true, CAPhaseReissue: true, CAPhasePromote: true, CAPhaseRollback: true, CAPhaseDrop: true,
		CAPhaseDone: false, CAPhaseAborted: false, "": false,
	} {
		if got := (CARotation{Phase: phase}).Active(); got != want {
			t.Fatalf("%q: active %v, want %v", phase, got, want)
		}
	}
}
