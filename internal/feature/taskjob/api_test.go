package taskjob

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// fakeRunner stands in for a job kind: it hands each job it is started with
// to the test and leaves it running until the test finishes it.
type fakeRunner struct {
	mu      sync.Mutex
	starts  int
	ctxs    map[string]context.Context
	started chan *Job
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{started: make(chan *Job, 8), ctxs: map[string]context.Context{}}
}

func (f *fakeRunner) Start(ctx context.Context, job *Job) error {
	f.mu.Lock()
	f.starts++
	f.ctxs[job.Spec.ID] = ctx
	f.mu.Unlock()
	f.started <- job
	return nil
}

// ctx is the context the job with this ID was started under.
func (f *fakeRunner) ctx(id string) context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctxs[id]
}

func (f *fakeRunner) Check(spec tasks.JobSpec) tasks.CheckVerdict {
	if spec.Command.Run == "refuse" {
		return tasks.CheckVerdict{Refused: true, Code: "SOME_GUARD", Reason: "no"}
	}
	return tasks.CheckVerdict{}
}

func (f *fakeRunner) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// next is the job the runner was started with next.
func (f *fakeRunner) next(t *testing.T) *Job {
	t.Helper()
	select {
	case j := <-f.started:
		return j
	case <-time.After(3 * time.Second):
		t.Fatal("the runner was not started")
		return nil
	}
}

// testNode is a node's task-job API, served the way router.go wires it, with
// a fake command runner, a clock the test moves and its own database.
type testNode struct {
	h      *Handler
	e      *echo.Echo
	runner *fakeRunner
	clock  *time.Time
	proc   *proc         // the process its real runners run in, when they do
	delay  time.Duration // between the middleware's check and the handler
}

// testJWTSecret signs the browser sessions the tests send as unsigned calls.
const testJWTSecret = "task-job-test-jwt-secret"


func newTestNode(t *testing.T) *testNode {
	t.Helper()
	dir := t.TempDir()
	database, err := sfdb.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return newTestNodeOn(t, database, filepath.Join(dir, "jobs"))
}

// newTestNodeOn is a node on an existing database and log directory: the same
// node after a restart.
func newTestNodeOn(t *testing.T, db *sql.DB, logDir string) *testNode {
	t.Helper()
	clock := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	n := &testNode{runner: newFakeRunner(), clock: &clock}
	n.h = New(db, exec.NewMockCommander(), logDir, func() platform.Info { return platform.Info{} })
	n.h.now = func() time.Time { return *n.clock }
	n.h.bootID = func() string { return "boot-1" }
	n.h.runners = map[string]Runner{tasks.StepCommand: n.runner}
	n.e = echo.New()
	// The JWT middleware is in front, as in router.go: it is what validates
	// a cluster call's signature, and the handlers take its word for it.
	// n.delay, when set, stands for whatever runs between the two.
	g := n.e.Group("/api/v1", middleware.JWTMiddleware(func() string { return testJWTSecret }), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if n.delay > 0 {
				time.Sleep(n.delay)
			}
			return next(c)
		}
	})
	g.POST("/task-jobs", n.h.StartJob)
	g.POST("/task-jobs/check", n.h.CheckJobs)
	g.GET("/task-jobs/:id", n.h.GetJob)
	g.POST("/task-jobs/:id/cancel", n.h.CancelJob)

	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("cluster-secret", []string{"cluster-secret"})
	return n
}

// spec is a command step of run, at step.
func spec(run string, step int) tasks.JobSpec {
	return tasks.JobSpec{
		ID: run + "-" + strconv.Itoa(step), RunID: run, Task: "Deploy", Step: step,
		Kind: tasks.StepCommand, Command: &tasks.CommandStep{Run: "true"},
	}
}

const (
	runA = "20261101-090000-a1b2c3"
	runB = "20261101-091500-d4e5f6"
)

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func (e envelope) code() string {
	if e.Error == nil {
		return ""
	}
	return e.Error.Code
}

// do sends a request, signed as a cluster call when signed is set and with
// a signed-in browser's token when not.
func (n *testNode) do(t *testing.T, method, uri string, body any, signed bool) (int, envelope) {
	t.Helper()
	var payload string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = string(b)
	}
	req := httptest.NewRequest(method, uri, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if signed {
		req.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(method, uri))
	} else {
		token, err := auth.GenerateToken("operator", testJWTSecret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	n.e.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: %d %q is not the envelope", method, uri, rec.Code, rec.Body)
	}
	return rec.Code, env
}

func (n *testNode) start(t *testing.T, s tasks.JobSpec) (int, envelope) {
	t.Helper()
	return n.do(t, http.MethodPost, "/api/v1/task-jobs", s, true)
}

func view(t *testing.T, env envelope) tasks.JobView {
	t.Helper()
	var v tasks.JobView
	if err := json.Unmarshal(env.Data, &v); err != nil {
		t.Fatalf("data %s: %v", env.Data, err)
	}
	return v
}

// Only a signed cluster call reaches a node's jobs, on every route, and the
// signature is checked before anything else is: an unsigned request for a
// job that does not exist, or by an ID that is not one, still hears that it
// is not a cluster call.
func TestTaskJobsTakeOnlyClusterCalls(t *testing.T) {
	n := newTestNode(t)
	for _, r := range []struct {
		method, uri string
		body        any
	}{
		{http.MethodPost, "/api/v1/task-jobs", spec(runA, 0)},
		{http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{spec(runA, 0)}},
		{http.MethodGet, "/api/v1/task-jobs/" + runA + "-0", nil},
		{http.MethodGet, "/api/v1/task-jobs/not-an-id", nil},
		{http.MethodPost, "/api/v1/task-jobs/" + runA + "-0/cancel", map[string]bool{"force": true}},
	} {
		status, env := n.do(t, r.method, r.uri, r.body, false)
		if status != http.StatusForbidden || env.code() != response.ErrTaskJobClusterOnly {
			t.Fatalf("unsigned %s %s: %d %q, want 403 %s", r.method, r.uri, status, env.code(), response.ErrTaskJobClusterOnly)
		}
	}
	if got := n.runner.startCount(); got != 0 {
		t.Fatalf("an unsigned request started %d jobs", got)
	}

	status, env := n.start(t, spec(runA, 0))
	if status != http.StatusCreated {
		t.Fatalf("signed start: %d %q, want 201", status, env.code())
	}
	n.runner.next(t)
	if status, env := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+runA+"-0", nil, true); status != http.StatusOK {
		t.Fatalf("signed get: %d %q, want 200", status, env.code())
	}
	status, env = n.do(t, http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{spec(runB, 0), func() tasks.JobSpec {
		s := spec(runB, 1)
		s.Command.Run = "refuse"
		return s
	}()}, true)
	if status != http.StatusOK {
		t.Fatalf("signed check: %d %q, want 200", status, env.code())
	}
	var verdicts []tasks.CheckVerdict
	if err := json.Unmarshal(env.Data, &verdicts); err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 2 || verdicts[0].Refused || verdicts[0].Step != 0 || !verdicts[1].Refused || verdicts[1].Code != "SOME_GUARD" || verdicts[1].Step != 1 {
		t.Fatalf("verdicts %+v, want step 0 allowed and step 1 refused by its runner", verdicts)
	}
}

// The handlers take the middleware's word that a call is signed rather than
// checking the signature again: a second check counts the nonce as a replay
// once 50 ms have passed since the first, so a node slow to get from the
// middleware to the handler refused its own leader's genuine call. A browser
// session the middleware let through is still not a cluster call.
func TestATaskJobHandlerTrustsTheMiddlewaresCheck(t *testing.T) {
	n := newTestNode(t)
	n.delay = 60 * time.Millisecond
	status, env := n.start(t, spec(runA, 0))
	if status != http.StatusCreated {
		t.Fatalf("a signed start that reached the handler 60 ms after the middleware: %d %q, want 201", status, env.code())
	}
	n.runner.next(t)
	if status, env := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+runA+"-0", nil, true); status != http.StatusOK {
		t.Fatalf("a signed get 60 ms late: %d %q, want 200", status, env.code())
	}

	n.delay = 0
	for _, r := range []struct {
		method, uri string
		body        any
	}{
		{http.MethodPost, "/api/v1/task-jobs", spec(runB, 0)},
		{http.MethodGet, "/api/v1/task-jobs/" + runA + "-0", nil},
	} {
		status, env := n.do(t, r.method, r.uri, r.body, false)
		if status != http.StatusForbidden || env.code() != response.ErrTaskJobClusterOnly {
			t.Errorf("a browser session's %s %s: %d %q, want 403 %s", r.method, r.uri, status, env.code(), response.ErrTaskJobClusterOnly)
		}
	}
}

// A job ID starts its job once. Asked again — the leader that wrote the ID
// down and died before it heard back, or its successor — the node answers
// with the job it has, running or finished, and starts nothing.
func TestAJobIsStartedOnce(t *testing.T) {
	n := newTestNode(t)
	status, env := n.start(t, spec(runA, 0))
	if status != http.StatusCreated {
		t.Fatalf("first start: %d %q, want 201", status, env.code())
	}
	first := view(t, env)
	job := n.runner.next(t)

	status, env = n.start(t, spec(runA, 0))
	if status != http.StatusOK {
		t.Fatalf("the same ID again: %d %q, want 200 and the job it has", status, env.code())
	}
	if again := view(t, env); again.ID != first.ID || again.Status != tasks.JobRunning || !again.StartedAt.Equal(first.StartedAt) {
		t.Fatalf("the same ID again answered %+v, want the running job %+v", again, first)
	}

	if err := job.Finish(Result{Status: tasks.JobSucceeded, Changed: true}); err != nil {
		t.Fatal(err)
	}
	status, env = n.start(t, spec(runA, 0))
	if status != http.StatusOK || view(t, env).Status != tasks.JobSucceeded {
		t.Fatalf("the same ID after it finished: %d %s, want 200 and the finished job", status, env.Data)
	}
	time.Sleep(100 * time.Millisecond)
	if got := n.runner.startCount(); got != 1 {
		t.Fatalf("the runner was started %d times for one ID", got)
	}
}

// A node runs one job at a time. Another job is turned away naming the one
// in the way — while it runs, while it waits on its reboot, and after the
// panel restarts under it — and is taken once that one has finished.
func TestANodeRunsOneJobAtATime(t *testing.T) {
	n := newTestNode(t)
	if status, env := n.start(t, spec(runA, 0)); status != http.StatusCreated {
		t.Fatalf("first job: %d %q", status, env.code())
	}
	job := n.runner.next(t)

	busy := func(n *testNode, holder string) {
		t.Helper()
		status, env := n.start(t, spec(runB, 0))
		if status != http.StatusConflict || env.code() != response.ErrTaskNodeBusy {
			t.Fatalf("a second job: %d %q, want 409 %s", status, env.code(), response.ErrTaskNodeBusy)
		}
		if d := env.Error.Details; d["job_id"] != holder || d["run_id"] != runA || d["task"] != "Deploy" {
			t.Fatalf("details %v, want the job in the way, %s of run %s", d, holder, runA)
		}
	}
	busy(n, runA+"-0")

	// The panel restarts: the record is what holds the node.
	restarted := newTestNodeOn(t, n.h.DB, n.h.LogDir)
	busy(restarted, runA+"-0")

	if err := job.Finish(Result{Status: tasks.JobFailed, Summary: "exit 1"}); err != nil {
		t.Fatal(err)
	}
	// A job waiting on its reboot holds the node as well.
	if status, env := n.start(t, spec(runA, 1)); status != http.StatusCreated {
		t.Fatalf("the next step: %d %q", status, env.code())
	}
	if _, err := n.h.DB.Exec(`UPDATE task_jobs SET status = ? WHERE id = ?`, tasks.JobRebooting, runA+"-1"); err != nil {
		t.Fatal(err)
	}
	busy(n, runA+"-1")
	if _, err := n.h.DB.Exec(`UPDATE task_jobs SET status = ? WHERE id = ?`, tasks.JobSucceeded, runA+"-1"); err != nil {
		t.Fatal(err)
	}
	if status, env := n.start(t, spec(runB, 0)); status != http.StatusCreated {
		t.Fatalf("a job once the node is free: %d %q, want 201", status, env.code())
	}
}

// What a node is sent is checked before anything is recorded: the ID's form
// and that it names the run and step it is sent with, a kind this node runs,
// and that kind's settings.
func TestAJobThatIsNotOneIsRefused(t *testing.T) {
	n := newTestNode(t)
	for name, mutate := range map[string]func(*tasks.JobSpec){
		"an ID of another form":         func(s *tasks.JobSpec) { s.ID = "x" },
		"an ID of another run":          func(s *tasks.JobSpec) { s.ID = runB + "-0" },
		"an ID of another step":         func(s *tasks.JobSpec) { s.ID = runA + "-3" },
		"no task":                       func(s *tasks.JobSpec) { s.Task = "" },
		"a kind this node does not run": func(s *tasks.JobSpec) { s.Kind = tasks.StepReboot; s.Command = nil; s.Reboot = &tasks.RebootStep{} },
		"no settings for its kind":      func(s *tasks.JobSpec) { s.Command = nil },
		"settings for two kinds":        func(s *tasks.JobSpec) { s.Service = &tasks.ServiceStep{Name: "nginx", Action: "restart"} },
	} {
		s := spec(runA, 0)
		mutate(&s)
		status, env := n.start(t, s)
		if status != http.StatusBadRequest || env.code() != response.ErrInvalidRequest {
			t.Fatalf("%s: %d %q, want 400 %s", name, status, env.code(), response.ErrInvalidRequest)
		}
	}
	if status, env := n.do(t, http.MethodGet, "/api/v1/task-jobs/x", nil, true); status != http.StatusBadRequest || env.code() != response.ErrInvalidRequest {
		t.Fatalf("GET of an ID that is not one: %d %q, want 400 %s", status, env.code(), response.ErrInvalidRequest)
	}
	if status, env := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+runA+"-0", nil, true); status != http.StatusNotFound || env.code() != response.ErrTaskJobNotFound {
		t.Fatalf("GET of a job never started: %d %q, want 404 %s", status, env.code(), response.ErrTaskJobNotFound)
	}
	if got := n.runner.startCount(); got != 0 {
		t.Fatalf("refused specs started %d jobs", got)
	}
}

// A running job is stopped only when forced: its runner's context ends, and
// the runner says how the job ended. Unforced, the node says the job runs to
// its end.
func TestARunningJobIsCancelledOnlyWhenForced(t *testing.T) {
	n := newTestNode(t)
	if status, env := n.start(t, spec(runA, 0)); status != http.StatusCreated {
		t.Fatalf("start: %d %q", status, env.code())
	}
	job := n.runner.next(t)
	cancelURI := "/api/v1/task-jobs/" + runA + "-0/cancel"

	if status, env := n.do(t, http.MethodPost, cancelURI, map[string]bool{"force": false}, true); status != http.StatusConflict || env.code() != response.ErrTaskJobRunning {
		t.Fatalf("unforced: %d %q, want 409 %s", status, env.code(), response.ErrTaskJobRunning)
	}
	if err := n.runner.ctx(job.Spec.ID).Err(); err != nil {
		t.Fatalf("an unforced cancel ended the job's context: %v", err)
	}
	if status, env := n.do(t, http.MethodPost, cancelURI, map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced: %d %q, want 200", status, env.code())
	}
	select {
	case <-n.runner.ctx(job.Spec.ID).Done():
	case <-time.After(3 * time.Second):
		t.Fatal("a forced cancel left the job's context running")
	}
	if status, env := n.do(t, http.MethodPost, "/api/v1/task-jobs/"+runB+"-0/cancel", map[string]bool{"force": true}, true); status != http.StatusNotFound || env.code() != response.ErrTaskJobNotFound {
		t.Fatalf("cancel of a job never started: %d %q, want 404", status, env.code())
	}
}
