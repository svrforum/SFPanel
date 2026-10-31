package featurecluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/release"
)

// aRun is the run ID the tests' task runs are known by.
const aRun = "20261101-120000-a1b2c3"

// noRelease answers an update's release lookup with none, so an update that
// got past its refusal ends at once instead of asking GitHub.
func noRelease(t *testing.T) {
	t.Helper()
	gh := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(gh.Close)
	prev := release.LatestURL
	release.LatestURL = gh.URL
	t.Cleanup(func() { release.LatestURL = prev })
}

// refusedForTheRun checks that body is a refusal for the task run aRun: its
// code, and the run it names in its details.
func refusedForTheRun(t *testing.T, what string, status int, body string) {
	t.Helper()
	var out struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &out) // an update that started answers in SSE
	if status != http.StatusConflict || out.Error.Code != response.ErrTaskRunActive || out.Error.Details["run_id"] != aRun {
		t.Fatalf("%s during a task run: %d %s", what, status, body)
	}
}

// A cluster update restarts nodes under a task run's steps: it waits for the
// run, and the refusal names it.
func TestAClusterUpdateWaitsForATaskRun(t *testing.T) {
	noRelease(t)
	h := &Handler{Manager: tokenLeader(t)}
	h.SetTaskRunsActive(func() string { return aRun })
	status, body := call(t, h.ClusterUpdate, http.MethodPost, "/api/v1/cluster/update", `{"mode":"rolling"}`)
	refusedForTheRun(t, "an update", status, body)
	if h.updateRunning.Load() {
		t.Fatal("the refused update holds the update slot")
	}
}

// A CA rotation restarts no node but reissues every certificate a run's calls
// ride on: it waits for the run too, and starts nothing meanwhile.
func TestACARotationWaitsForATaskRun(t *testing.T) {
	mgr := settledLeader(t)
	h := &Handler{Manager: mgr}
	h.SetTaskRunsActive(func() string { return aRun })
	status, body := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", "")
	refusedForTheRun(t, "a CA rotation", status, body)
	if mgr.CARotationActive() {
		t.Fatal("the refused CA rotation started")
	}
}

// A conversion takes a manager out of Raft under a run's steps: it waits for
// the run, asks no node and makes no token; once the run is over it goes.
func TestAConversionWaitsForATaskRun(t *testing.T) {
	mgr := tokenLeader(t)
	convertible(t, mgr)
	var calls []nodeCall
	h := &Handler{Manager: mgr, callNode: answering(&calls, http.StatusAccepted, `{"success":true,"data":{"message":"leaving"}}`, nil)}
	h.SetTaskRunsActive(func() string { return aRun })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/nodes/m2/convert", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("m2")
	if err := h.ConvertNode(c); err != nil {
		t.Fatal(err)
	}
	refusedForTheRun(t, "a conversion", rec.Code, rec.Body.String())
	if len(calls) != 0 {
		t.Fatalf("the node was asked: %+v", calls)
	}
	if tokens, err := mgr.ListJoinTokens(); err != nil || len(tokens) != 0 {
		t.Fatalf("tokens %v %v", tokens, err)
	}
	if _, ok := mgr.GetRaft().GetFSM().GetConfigValue(cluster.ConversionIntentKey("m2")); ok {
		t.Fatal("the refused conversion recorded its intent")
	}
	// A run over is one no longer reported: nothing holds the conversion back.
	h.SetTaskRunsActive(func() string { return "" })
	if status, code, msg := postConvert(t, h, "m2"); status != http.StatusAccepted {
		t.Fatalf("convert once the run is over: %d %s %q", status, code, msg)
	}
}

// A promotion restarts an agent as a manager under a run's steps: it waits
// for the run, asks no agent and makes no token; once the run is over it goes.
func TestAPromotionWaitsForATaskRun(t *testing.T) {
	mgr := tokenLeader(t)
	promotable(t, mgr)
	var calls []nodeCall
	h := &Handler{Manager: mgr, callNode: answering(&calls, http.StatusAccepted, `{"success":true,"data":{"message":"joining"}}`, nil)}
	h.SetTaskRunsActive(func() string { return aRun })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/nodes/a1/promote", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a1")
	if err := h.PromoteNode(c); err != nil {
		t.Fatal(err)
	}
	refusedForTheRun(t, "a promotion", rec.Code, rec.Body.String())
	if len(calls) != 0 {
		t.Fatalf("the agent was asked: %+v", calls)
	}
	if tokens, err := mgr.ListJoinTokens(); err != nil || len(tokens) != 0 {
		t.Fatalf("tokens %v %v", tokens, err)
	}
	h.SetTaskRunsActive(func() string { return "" })
	if status, code, msg := postPromote(t, h, "a1"); status != http.StatusAccepted {
		t.Fatalf("promote once the run is over: %d %s %q", status, code, msg)
	}
}

// errBusyForTheUpdate is the fake task run's refusal while an update runs.
var errBusyForTheUpdate = errors.New("a cluster update is running")

// fakeRuns is feature/tasks as the gate sees it: a run is started through
// StartGate, refused while anything else is under way, and recorded inside
// the claim — where the next starter to take startMu reads it.
type fakeRuns struct {
	mu     sync.Mutex
	active string
}

func (f *fakeRuns) running() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *fakeRuns) start(h *Handler, runID string) error {
	return h.StartGate(func(b Busy) error {
		switch {
		case b.UpdateRunning:
			return errBusyForTheUpdate
		case b.CARotation, b.Converting:
			return fmt.Errorf("busy: %+v", b)
		}
		if r := f.running(); r != "" {
			return fmt.Errorf("run %s is under way", r)
		}
		f.mu.Lock()
		f.active = runID
		f.mu.Unlock()
		return nil
	})
}

func (f *fakeRuns) finish() {
	f.mu.Lock()
	f.active = ""
	f.mu.Unlock()
}

// A task run and an update asked for at the same moment: exactly one starts,
// and the other is refused for it — never both, never neither.
func TestATaskRunAndAnUpdateStartedTogetherOneWins(t *testing.T) {
	// The release lookup of an update that started waits until the test lets
	// it go, so the update holds its slot while the run is judged.
	arrived := make(chan struct{}, 1)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(func() { gh.CloseClientConnections(); gh.Close() })
	prev := release.LatestURL
	release.LatestURL = gh.URL
	t.Cleanup(func() { release.LatestURL = prev })

	h := &Handler{Manager: tokenLeader(t)}
	runs := &fakeRuns{}
	h.SetTaskRunsActive(runs.running)

	updates, taskRuns := 0, 0
	for round := 0; round < 100; round++ {
		start := make(chan struct{})
		ran, updated := make(chan error, 1), make(chan string, 1)
		go func() {
			<-start
			ran <- runs.start(h, aRun)
		}()
		go func() {
			<-start
			status, body := call(t, h.ClusterUpdate, http.MethodPost, "/api/v1/cluster/update", `{"mode":"rolling"}`)
			updated <- fmt.Sprint(status, " ", body)
		}()
		close(start)
		switch err := <-ran; {
		case err == nil:
			taskRuns++
			if upd := <-updated; !strings.Contains(upd, response.ErrTaskRunActive) || !strings.Contains(upd, aRun) {
				t.Fatalf("round %d: the update beside a task run answered %s", round, upd)
			}
			if h.updateRunning.Load() {
				t.Fatalf("round %d: an update started alongside the task run", round)
			}
			runs.finish()
		case errors.Is(err, errBusyForTheUpdate):
			updates++
			if r := runs.running(); r != "" {
				t.Fatalf("round %d: the refused run was recorded as %s", round, r)
			}
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: the update that won never asked for a release", round)
			}
			gh.CloseClientConnections()
			if upd := <-updated; !strings.HasPrefix(upd, "200 ") {
				t.Fatalf("round %d: the update that won answered %s", round, upd)
			}
		default:
			t.Fatalf("round %d: the run was refused for %v", round, err)
		}
		waitForCond(t, "the update to end", func() bool { return !h.updateRunning.Load() })
	}
	t.Logf("task runs won %d rounds, updates %d", taskRuns, updates)
}

// StartGate tells a claim what is under way: nothing on an idle leader, and
// a conversion from the moment it is admitted until it has been answered.
func TestAStartGateSeesAConversionUnderWay(t *testing.T) {
	mgr := tokenLeader(t)
	convertible(t, mgr)
	inConversion := make(chan Busy, 1)
	h := &Handler{Manager: mgr}
	h.callNode = func(context.Context, string, string, string, []byte, string) (int, []byte, error) {
		// The node is being asked: the conversion is under way.
		_ = h.StartGate(func(b Busy) error { inConversion <- b; return nil })
		return http.StatusAccepted, []byte(`{"success":true,"data":{}}`), nil
	}

	var idle Busy
	if err := h.StartGate(func(b Busy) error { idle = b; return nil }); err != nil {
		t.Fatal(err)
	}
	if idle != (Busy{}) {
		t.Fatalf("nothing under way, and the gate saw %+v", idle)
	}
	if status, code, msg := postConvert(t, h, "m2"); status != http.StatusAccepted {
		t.Fatalf("convert: %d %s %q", status, code, msg)
	}
	if b := <-inConversion; b != (Busy{Converting: true}) {
		t.Fatalf("during a conversion the gate saw %+v", b)
	}
	var after Busy
	_ = h.StartGate(func(b Busy) error { after = b; return nil })
	if after.Converting {
		t.Fatal("the answered conversion is still seen as under way")
	}
}

// StartGate tells a claim a promotion is under way from the moment it is
// admitted until it has been answered: the agent restarts as a manager, which
// a task run's steps must not meet.
func TestAStartGateSeesAPromotionUnderWay(t *testing.T) {
	mgr := tokenLeader(t)
	promotable(t, mgr)
	inPromotion := make(chan Busy, 1)
	h := &Handler{Manager: mgr}
	h.callNode = func(context.Context, string, string, string, []byte, string) (int, []byte, error) {
		// The agent is being asked: the promotion is under way.
		_ = h.StartGate(func(b Busy) error { inPromotion <- b; return nil })
		return http.StatusAccepted, []byte(`{"success":true,"data":{"message":"joining"}}`), nil
	}
	if status, code, msg := postPromote(t, h, "a1"); status != http.StatusAccepted {
		t.Fatalf("promote: %d %s %q", status, code, msg)
	}
	if b := <-inPromotion; b != (Busy{Promoting: true}) {
		t.Fatalf("during a promotion the gate saw %+v", b)
	}
	var after Busy
	_ = h.StartGate(func(b Busy) error { after = b; return nil })
	if after.Promoting {
		t.Fatal("the answered promotion is still seen as under way")
	}
}

// StartGate tells a claim a CA rotation is under way, and the claim's refusal
// is StartGate's.
func TestAStartGateSeesACARotationAndReturnsTheClaimsRefusal(t *testing.T) {
	mgr := settledLeader(t)
	h := &Handler{Manager: mgr}
	if _, err := mgr.StartCARotation(0); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("refused")
	var during Busy
	if err := h.StartGate(func(b Busy) error { during = b; return refusal }); !errors.Is(err, refusal) {
		t.Fatalf("the claim's refusal came back as %v", err)
	}
	if during != (Busy{CARotation: true}) {
		t.Fatalf("during a CA rotation the gate saw %+v", during)
	}
}
