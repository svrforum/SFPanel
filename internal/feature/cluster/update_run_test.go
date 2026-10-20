package featurecluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/paneltls"
	"github.com/svrforum/SFPanel/internal/release"
)

const okStream = "data: {\"step\":\"downloading\",\"message\":\"Downloading\"}\n\ndata: {\"step\":\"complete\",\"message\":\"Update complete\"}\n\n"

// fakeUpdateEnv records what the update asked, in order.
type fakeUpdateEnv struct {
	mu        sync.Mutex
	calls     []string
	leaderFor int // IsLeader answers true this many times, then false; <0: always true
	leaderN   int
	trigger   map[string]string // node → SSE answer (default okStream)
	versions  map[string]string // node → version Version answers (default the target)
	transfer  error
	self      string // the leader's own answer (default okStream)
}

func (f *fakeUpdateEnv) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}
func (f *fakeUpdateEnv) IsLeader() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaderN++
	return f.leaderFor < 0 || f.leaderN <= f.leaderFor
}
func (f *fakeUpdateEnv) Trigger(_ context.Context, id string, _ []byte) (int, []byte, error) {
	f.record("trigger:" + id)
	if s, ok := f.trigger[id]; ok {
		return 200, []byte(s), nil
	}
	return 200, []byte(okStream), nil
}
func (f *fakeUpdateEnv) Version(_ context.Context, id string) (string, error) {
	if v, ok := f.versions[id]; ok {
		return v, nil
	}
	return "0.81.0", nil
}
func (f *fakeUpdateEnv) TransferTo(id string) error {
	f.record("transfer:" + id)
	return f.transfer
}
func (f *fakeUpdateEnv) SelfUpdate([]byte) (int, []byte, error) {
	f.record("self")
	if f.self != "" {
		return 200, []byte(f.self), nil
	}
	return 200, []byte(okStream), nil
}
func (f *fakeUpdateEnv) Sleep(time.Duration) {}

func (f *fakeUpdateEnv) index(call string) int {
	for i, c := range f.calls {
		if c == call {
			return i
		}
	}
	return -1
}

func testPlan() updatePlan {
	n := func(id, name string, manager bool) updateNode {
		return updateNode{ID: id, Name: name, Version: "0.80.0", Manager: manager, Voter: manager}
	}
	leader := n("m1", "m1", true)
	return updatePlan{
		Waves:    [][]updateNode{{n("a1", "web-1", false), n("a2", "web-2", false)}, {n("a3", "web-3", false)}},
		Managers: []updateNode{n("m2", "m2", true)},
		Leader:   &leader,
	}
}

var testRelease = &release.Release{TagName: "v0.81.0"}

type events struct {
	mu  sync.Mutex
	all []map[string]interface{}
}

func (e *events) emit(ev map[string]interface{}) {
	e.mu.Lock()
	e.all = append(e.all, ev)
	e.mu.Unlock()
}

func (e *events) overall() map[string]interface{} {
	for i := len(e.all) - 1; i >= 0; i-- {
		if o, ok := e.all[i]["overall"]; ok && o != "started" {
			return e.all[i]
		}
	}
	return nil
}

func TestAnUpdateGoesAgentsThenManagersThenTheLeader(t *testing.T) {
	env := &fakeUpdateEnv{leaderFor: -1}
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	order := []string{"trigger:a3", "trigger:m2", "transfer:m2", "self"}
	for i := 1; i < len(order); i++ {
		if env.index(order[i-1]) < 0 || env.index(order[i-1]) > env.index(order[i]) {
			t.Fatalf("calls %v: %s must come before %s", env.calls, order[i-1], order[i])
		}
	}
	for _, a := range []string{"trigger:a1", "trigger:a2"} {
		if i := env.index(a); i < 0 || i > env.index("trigger:a3") {
			t.Fatalf("calls %v: the first wave must finish before the second", env.calls)
		}
	}
	if o := ev.overall(); o["overall"] != "complete" || o["leader_restarting"] != true {
		t.Fatalf("final event %v", o)
	}
}

// A node's update answers 200 before it streams, so a checksum, signature or
// download failure is only in the stream; it was counted as updated, and the
// rolling update went on to the next node.
func TestAFailureInsideTheStreamStopsTheWave(t *testing.T) {
	env := &fakeUpdateEnv{leaderFor: -1, trigger: map[string]string{
		"a2": "data: {\"step\":\"error\",\"message\":\"Checksum mismatch for sfpanel.tar.gz\"}\n\n",
	}}
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	for _, never := range []string{"trigger:a3", "trigger:m2", "transfer:m2", "self"} {
		if env.index(never) >= 0 {
			t.Fatalf("calls %v: %s after a failed wave", env.calls, never)
		}
	}
	o := ev.overall()
	if o["overall"] != "error" || !strings.Contains(fmt.Sprint(o["message"]), "Wave 1 of 2") || !strings.Contains(fmt.Sprint(o["message"]), "web-2") {
		t.Fatalf("final event %v", o)
	}
	found := false
	for _, e := range ev.all {
		if e["node_id"] == "a2" && e["step"] == "error" && strings.Contains(fmt.Sprint(e["message"]), "Checksum mismatch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the node's own reason was not reported: %v", ev.all)
	}
}

// The rollback watchdog reverts a new binary that does not come up within its
// grace, and the node answers on its old version again — after the sixty
// seconds the update waited, which then counted it as updated.
func TestANodeThatComesBackOnItsOldVersionFailed(t *testing.T) {
	env := &fakeUpdateEnv{leaderFor: -1, versions: map[string]string{"a1": "0.80.0"}}
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	if env.index("trigger:a3") >= 0 {
		t.Fatalf("calls %v: the update went on", env.calls)
	}
	found := false
	for _, e := range ev.all {
		if e["node_id"] == "a1" && e["step"] == "error" && strings.Contains(fmt.Sprint(e["message"]), "rolled back") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no rollback reported for a1: %v", ev.all)
	}
}

func TestLosingLeadershipStopsTheUpdate(t *testing.T) {
	env := &fakeUpdateEnv{leaderFor: 1} // leads for the first wave only
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	if env.index("trigger:a3") >= 0 || env.index("self") >= 0 {
		t.Fatalf("calls %v after leadership moved", env.calls)
	}
	if o := ev.overall(); o["overall"] != "error" || !strings.Contains(fmt.Sprint(o["message"]), "Leadership moved") {
		t.Fatalf("final event %v", o)
	}
}

// With another manager to take over, a leader that could not hand over does
// not update itself: it would restart as the leader, and its managers would
// stand without one for its whole restart.
func TestALeaderThatCannotHandOverIsNotUpdated(t *testing.T) {
	env := &fakeUpdateEnv{leaderFor: -1, transfer: errors.New("timed out")}
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	if env.index("self") >= 0 {
		t.Fatalf("calls %v: the leader updated itself without a handover", env.calls)
	}
	if o := ev.overall(); o["overall"] != "error" || !strings.Contains(fmt.Sprint(o["message"]), "handed over") {
		t.Fatalf("final event %v", o)
	}
}

// A manager already on the target takes leadership like one this run
// updated. A run that stops because the handover failed leaves every other
// manager on the target, and the next run then updated the leader with no
// handover at all — the restart the stop was there to prevent.
func TestALeaderHandsOverToAManagerAlreadyOnTheTarget(t *testing.T) {
	p := testPlan()
	m2 := p.Managers[0]
	m2.Version = "0.81.0"
	p.Managers = nil
	p.Current = []updateNode{{ID: "a9", Name: "web-9", Version: "0.81.0"}, m2}

	env := &fakeUpdateEnv{leaderFor: -1}
	var ev events
	runUpdatePlan(env, p, testRelease, "rolling", ev.emit)
	if i := env.index("transfer:m2"); i < 0 || i > env.index("self") {
		t.Fatalf("calls %v: the leader updated itself without handing over to m2", env.calls)
	}
	if env.index("transfer:a9") >= 0 {
		t.Fatalf("calls %v: an agent was offered leadership", env.calls)
	}

	refused := &fakeUpdateEnv{leaderFor: -1, transfer: errors.New("timed out")}
	var ev2 events
	runUpdatePlan(refused, p, testRelease, "rolling", ev2.emit)
	if refused.index("self") >= 0 {
		t.Fatalf("calls %v: the leader updated itself without a handover", refused.calls)
	}
	if o := ev2.overall(); o["overall"] != "error" || !strings.Contains(fmt.Sprint(o["message"]), "handed over") {
		t.Fatalf("final event %v", o)
	}
}

// One manager has no one to hand over to; it updates itself, and its agents
// carry on without it for the restart.
func TestALoneManagerUpdatesItselfWithoutAHandover(t *testing.T) {
	p := testPlan()
	p.Managers = nil
	env := &fakeUpdateEnv{leaderFor: -1}
	var ev events
	runUpdatePlan(env, p, testRelease, "rolling", ev.emit)
	if env.index("self") < 0 || len(env.calls) == 0 || strings.HasPrefix(env.calls[len(env.calls)-1], "transfer") {
		t.Fatalf("calls %v", env.calls)
	}
	for _, c := range env.calls {
		if strings.HasPrefix(c, "transfer") {
			t.Fatalf("calls %v: a handover with no one to take it", env.calls)
		}
	}
}

// The leader's own update was announced complete before it began, and its
// result never reported.
func TestTheLeadersOwnFailureIsReported(t *testing.T) {
	env := &fakeUpdateEnv{leaderFor: -1, self: "data: {\"step\":\"error\",\"message\":\"Download failed (HTTP 404)\"}\n\n"}
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	if o := ev.overall(); o["overall"] != "error" || !strings.Contains(fmt.Sprint(o["message"]), "Download failed") {
		t.Fatalf("final event %v", o)
	}
}

// answersOnce answers Version for its nodes once, on the version given, and
// never again: a node that went down and stayed down.
type answersOnce struct {
	*fakeUpdateEnv
	once  map[string]string
	asked map[string]bool
}

func (e *answersOnce) Version(ctx context.Context, id string) (string, error) {
	e.mu.Lock()
	v, ok := e.once[id]
	again := e.asked[id]
	e.asked[id] = true
	e.mu.Unlock()
	switch {
	case !ok:
		return e.fakeUpdateEnv.Version(ctx, id)
	case again:
		return "", errors.New("connection refused")
	}
	return v, nil
}

// A node still answers on its old version for a moment after its update
// returns, while it shuts down. One that then never came back was reported as
// answering on that version — rolled back — when it answered nothing at all.
func TestANodeThatNeverComesBackIsNotReportedRolledBack(t *testing.T) {
	env := &answersOnce{fakeUpdateEnv: &fakeUpdateEnv{leaderFor: -1}, once: map[string]string{"a1": "0.80.0"}, asked: map[string]bool{}}
	var ev events
	runUpdatePlan(env, testPlan(), testRelease, "rolling", ev.emit)
	var msg string
	for _, e := range ev.all {
		if e["node_id"] == "a1" && e["step"] == "error" {
			msg = fmt.Sprint(e["message"])
		}
	}
	if !strings.Contains(msg, "did not answer on v0.81.0") || strings.Contains(msg, "rolled back") {
		t.Fatalf("a1 reported as %q", msg)
	}
}

// The leader's own update streams back over its loopback. A stream cut off
// before it said how the update ended — the request's five minutes running
// out mid-download, say — was read as a finished update, and the run
// announced the leader restarting on the new version.
func TestASelfUpdateStreamThatBreaksOffIsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"step\":\"downloading\",\"message\":\"Downloading\"}\n\n")
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	env := clusterUpdateEnv{self: paneltls.Self{Port: srv.Listener.Addr().(*net.TCPAddr).Port}}
	status, body, err := env.SelfUpdate([]byte(`{}`))
	if msg := updateFailure(status, body, err); !strings.Contains(msg, "broke off") {
		t.Fatalf("a stream cut off mid-download read as %q (status %d, err %v)", msg, status, err)
	}
}

func TestFirstSSEError(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{okStream, ""},
		{"data: {\"step\":\"error\",\"message\":\"Signature verification failed\"}\n\n", "Signature verification failed"},
		{"data: not json\n\ndata: {\"step\":\"error\"}\n\n", "the update failed"},
		{`{"success":true,"data":{"status":"up_to_date"}}`, ""},
	} {
		if got := firstSSEError([]byte(tc.body)); got != tc.want {
			t.Errorf("firstSSEError(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}
