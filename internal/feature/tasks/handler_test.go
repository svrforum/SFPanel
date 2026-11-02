package featuretasks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	featurecluster "github.com/svrforum/SFPanel/internal/feature/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// greetDoc is a task with a required input, run on the agents by default.
const greetDoc = `name: greet
inputs:
  who: { required: true }
targets: { kinds: [agent] }
steps:
  - command: { run: "echo {{ who }}" }
  - command: { run: "echo again" }
`

// apiFixture is one manager's task API over a fake cluster of a leader m1, a
// follower m2 and agents a1–a3. Requests come from the operator.
type apiFixture struct {
	t   *testing.T
	env *fakeEnv
	h   *Handler
	e   *echo.Echo

	mu        sync.Mutex
	forwarded []string // "METHOD path" sent on to the leader
}

func newAPI(t *testing.T) *apiFixture {
	t.Helper()
	env := newFakeEnv(t, "m1", manager("m1", "m1"), manager("m2", "m2"), agent("a1", "a1"), agent("a2", "a2"), agent("a3", "a3"))
	env.readsOutput = true
	f := &apiFixture{t: t, env: env}
	f.h = &Handler{runners: map[string]context.CancelFunc{}, newEnv: func() runEnv { return env }}
	f.h.startGate = (&featurecluster.Handler{}).StartGate
	f.h.toLeader = func(c echo.Context) (bool, error) {
		f.mu.Lock()
		f.forwarded = append(f.forwarded, c.Request().Method+" "+c.Request().URL.Path)
		f.mu.Unlock()
		return true, c.JSON(http.StatusTeapot, response.Response{Success: true, Data: "from the leader"})
	}
	f.e = echo.New()
	f.e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("username", "operator")
			return next(c)
		}
	})
	registerRoutes(f.e.Group("/api/v1/cluster"), f.h)
	return f
}

// registerRoutes registers the task routes as router.go does.
func registerRoutes(g *echo.Group, h *Handler) {
	g.GET("/tasks", h.ListTasks)
	g.POST("/tasks", h.CreateTask)
	g.POST("/tasks/validate", h.ValidateTask)
	g.POST("/tasks/format", h.FormatTask)
	g.POST("/tasks/import/preview", h.PreviewImport)
	g.POST("/tasks/import", h.Import)
	g.GET("/tasks/:id", h.GetTask)
	g.PUT("/tasks/:id", h.UpdateTask)
	g.DELETE("/tasks/:id", h.DeleteTask)
	g.POST("/task-runs/plan", h.PlanRun)
	g.POST("/task-runs", h.StartRun)
	g.GET("/task-runs", h.ListRuns)
	g.GET("/task-runs/:id", h.GetRun)
	g.GET("/task-runs/:id/stream", h.StreamRun)
	g.POST("/task-runs/:id/stop", h.StopRun)
	g.POST("/task-runs/:id/rerun", h.RerunRun)
	g.GET("/task-runs/:id/nodes/:node/steps/:n/output", h.StepOutput)
}

// answer is a response: its status and envelope.
type answer struct {
	Status  int
	Success bool                `json:"success"`
	Data    json.RawMessage     `json:"data"`
	Error   *response.ErrorBody `json:"error"`
}

func (a answer) code() string {
	if a.Error == nil {
		return ""
	}
	return a.Error.Code
}

func (a answer) detail(k string) string {
	if a.Error == nil {
		return ""
	}
	return a.Error.Details[k]
}

func (a answer) data(t *testing.T, out any) {
	t.Helper()
	if err := json.Unmarshal(a.Data, out); err != nil {
		t.Fatalf("data %s: %v", a.Data, err)
	}
}

func (f *apiFixture) do(method, path string, body any) answer {
	f.t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	w := httptest.NewRecorder()
	f.e.ServeHTTP(w, r)
	a := answer{Status: w.Code}
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		f.t.Fatalf("%s %s: answer %q is not the envelope: %v", method, path, w.Body.String(), err)
	}
	return a
}

// store puts a user task in the store and returns its ID.
func (f *apiFixture) store(id, doc string) string {
	f.t.Helper()
	task, problems := tasks.Parse([]byte(doc))
	if task == nil {
		f.t.Fatalf("the test's task does not parse: %+v", problems)
	}
	if err := f.env.store.PutTask(cluster.StoredTask{ID: id, Name: task.Name, YAML: doc, Source: cluster.TaskSource{Kind: cluster.TaskSourceUser}}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// runOf records a run of greetDoc over nodes, in one wave, with change
// applied, and the node records given.
func (f *apiFixture) runOf(id string, status string, recs map[string]cluster.RunNodeRecord, nodes ...string) {
	f.t.Helper()
	rev, _ := f.env.store.PutTaskRevision(greetDoc)
	r := cluster.RunRecord{ID: id, TaskID: "t1", TaskName: "greet", Rev: rev, Inputs: map[string]string{"who": "world"},
		Nodes: nodes, Waves: [][]string{nodes}, Batch: 1, StopOnFailure: true, Status: status, StartedBy: "operator"}
	if err := f.env.store.PutRun(r); err != nil {
		f.t.Fatal(err)
	}
	for _, n := range nodes {
		rec, ok := recs[n]
		if !ok {
			rec = cluster.RunNodeRecord{Status: cluster.RunNodePending}
		}
		if err := f.env.store.PutRunNode(id, n, rec); err != nil {
			f.t.Fatal(err)
		}
	}
}

func wantRefusal(t *testing.T, what string, a answer, status int, code string, details map[string]string) {
	t.Helper()
	if a.Status != status || a.code() != code {
		t.Errorf("%s = %d %q (%+v), want %d %s", what, a.Status, a.code(), a.Error, status, code)
		return
	}
	for k, v := range details {
		if got := a.detail(k); got != v {
			t.Errorf("%s: details[%s] = %q, want %q", what, k, got, v)
		}
	}
}

const (
	srcRun   = "20261031-090000-aaaaaa"
	otherRun = "20261031-100000-bbbbbb"
)

func TestTheTaskRoutesAnswerWithTheirStatusAndCode(t *testing.T) {
	type check func(t *testing.T, f *apiFixture, a answer)
	start := func(change func(b map[string]any)) map[string]any {
		b := map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "world"}}
		if change != nil {
			change(b)
		}
		return b
	}
	cases := []struct {
		name         string
		setup        func(f *apiFixture)
		method, path string
		body         any
		status       int
		code         string
		details      map[string]string
		check        check
	}{
		{name: "the task list", method: "GET", path: "/api/v1/cluster/tasks", status: 200},
		{name: "a new task", method: "POST", path: "/api/v1/cluster/tasks", body: map[string]string{"yaml": greetDoc}, status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var v taskView
				a.data(t, &v)
				got, err := f.env.store.GetTask(v.ID)
				if err != nil || got.Name != "greet" || got.UpdatedBy != "operator" || got.Source.Kind != cluster.TaskSourceUser {
					t.Errorf("stored %+v (%v), want greet by the operator, from the panel", got, err)
				}
			}},
		{name: "a task with problems", method: "POST", path: "/api/v1/cluster/tasks", body: map[string]string{"yaml": "name: x\nsteps:\n  - bogus: 1\n"}, status: 400, code: response.ErrTaskInvalid,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var d struct {
					Problems []tasks.Problem `json:"problems"`
				}
				a.data(t, &d)
				if len(d.Problems) == 0 || d.Problems[0].Line != 3 {
					t.Errorf("problems = %+v, want one on line 3", d.Problems)
				}
			}},
		{name: "a task over 64 KiB", method: "POST", path: "/api/v1/cluster/tasks", body: map[string]string{"yaml": greetDoc + "#" + strings.Repeat("x", tasks.MaxDocumentBytes)}, status: 400, code: response.ErrTaskTooLarge},
		{name: "a task named as another", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/tasks", body: map[string]string{"yaml": greetDoc}, status: 409, code: response.ErrTaskNameTaken},
		{name: "a task named as a built-in", method: "POST", path: "/api/v1/cluster/tasks", body: map[string]string{"yaml": builtinNamed(t, "builtin/command")}, status: 409, code: response.ErrTaskNameTaken},
		{name: "a task renamed as a built-in", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "PUT", path: "/api/v1/cluster/tasks/t1", body: map[string]string{"yaml": builtinNamed(t, "builtin/reboot")}, status: 409, code: response.ErrTaskNameTaken},
		{name: "an unknown task", method: "GET", path: "/api/v1/cluster/tasks/nope", status: 404, code: response.ErrTaskNotFound},
		{name: "a change to an unknown task", method: "PUT", path: "/api/v1/cluster/tasks/nope", body: map[string]string{"yaml": greetDoc}, status: 404, code: response.ErrTaskNotFound},
		{name: "an unknown task deleted", method: "DELETE", path: "/api/v1/cluster/tasks/nope", status: 404, code: response.ErrTaskNotFound},
		{name: "a stored task deleted", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "DELETE", path: "/api/v1/cluster/tasks/t1", status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				if _, err := f.env.store.GetTask("t1"); !errors.Is(err, cluster.ErrTaskNotFound) {
					t.Errorf("t1 is still stored (%v)", err)
				}
			}},
		{name: "a task from Git changed", setup: func(f *apiFixture) {
			_ = f.env.store.PutTask(cluster.StoredTask{ID: "g1", Name: "greet", YAML: greetDoc, Source: cluster.TaskSource{Kind: cluster.TaskSourceGit, Repo: "example/tasks"}})
		}, method: "PUT", path: "/api/v1/cluster/tasks/g1", body: map[string]string{"yaml": greetDoc}, status: 409, code: response.ErrTaskReadOnly},
		{name: "a document checked", method: "POST", path: "/api/v1/cluster/tasks/validate", body: map[string]string{"yaml": "name: x\n"}, status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var d struct {
					Valid    bool            `json:"valid"`
					Problems []tasks.Problem `json:"problems"`
				}
				a.data(t, &d)
				if d.Valid || len(d.Problems) == 0 {
					t.Errorf("validate = %+v, want not valid with problems", d)
				}
			}},

		{name: "a run of an unknown task", method: "POST", path: "/api/v1/cluster/task-runs", body: start(func(b map[string]any) { b["task_id"] = "nope" }), status: 404, code: response.ErrTaskNotFound},
		{name: "a run without a required input", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/task-runs",
			body: start(func(b map[string]any) { delete(b, "inputs") }), status: 400, code: response.ErrTaskInputMissing, details: map[string]string{"input": "who"}},
		{name: "a run with no targets", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/task-runs",
			body: start(func(b map[string]any) { b["targets"] = map[string]any{"groups": []string{"nowhere"}} }), status: 400, code: response.ErrTaskNoTargets},
		{name: "a run over too many targets", setup: func(f *apiFixture) {
			f.store("t1", greetDoc)
			for i := range MaxTargets {
				f.env.nodes = append(f.env.nodes, agent(fmt.Sprintf("x%03d", i), fmt.Sprintf("x%03d", i)))
			}
		}, method: "POST", path: "/api/v1/cluster/task-runs", body: start(nil), status: 400, code: response.ErrTaskTooManyTargets},
		{name: "a run whose only target another run holds", setup: func(f *apiFixture) {
			f.store("t1", greetDoc)
			f.runOf(otherRun, cluster.RunRunning, nil, "a1")
		}, method: "POST", path: "/api/v1/cluster/task-runs", body: start(func(b map[string]any) { b["targets"] = map[string]any{"nodes": []string{"a1"}} }),
			status: 409, code: response.ErrTaskTargetBusy, details: map[string]string{"node_id": "a1", "run_id": otherRun}},
		{name: "a run", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/task-runs",
			body: start(func(b map[string]any) { b["batch"] = 2 }), status: 202,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var d struct {
					RunID string `json:"run_id"`
				}
				a.data(t, &d)
				head, nodes, err := f.env.store.GetRun(d.RunID)
				if err != nil {
					t.Fatalf("run %q is not recorded: %v", d.RunID, err)
				}
				doc, _ := f.env.store.TaskRevision(head.Rev)
				if head.Status != cluster.RunRunning || head.TaskID != "t1" || head.TaskName != "greet" || doc != greetDoc ||
					head.StartedBy != "operator" || head.Inputs["who"] != "world" || head.Batch != 2 || !head.StopOnFailure ||
					len(head.Confirmed) != 0 || fmt.Sprint(head.Waves) != "[[a1 a2] [a3]]" || !head.StartedAt.Equal(f.env.now) {
					t.Errorf("head = %+v (doc %q)", head, doc)
				}
				if len(nodes) != 0 {
					t.Errorf("node records = %+v, want none until each node starts", nodes)
				}
			}},
		{name: "a run of a built-in", method: "POST", path: "/api/v1/cluster/task-runs",
			body: map[string]any{"task_id": "builtin/command", "inputs": map[string]string{"command": "uptime"}, "targets": map[string]any{"nodes": []string{"a1"}}}, status: 202},
		{name: "a plan", setup: func(f *apiFixture) {
			f.store("t1", greetDoc)
			f.env.checks["a2"] = []tasks.CheckVerdict{{Step: 0, NeedsConfirm: true}}
			f.env.health["a3"] = cluster.StatusOffline
		}, method: "POST", path: "/api/v1/cluster/task-runs/plan", body: start(nil), status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var p Plan
				a.data(t, &p)
				if fmt.Sprint(p.WaveIDs()) != "[[a1] [a2]]" || len(p.Left) != 1 || p.Left[0].Reason != LeftOffline || len(p.Waves[1][0].Verdicts) != 1 {
					t.Errorf("plan = %+v", p)
				}
				if runs, _ := f.env.store.ListRuns(); len(runs) != 0 {
					t.Errorf("a plan recorded runs %+v", runs)
				}
			}},
		{name: "a plan without a required input", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/task-runs/plan",
			body: start(func(b map[string]any) { delete(b, "inputs") }), status: 400, code: response.ErrTaskInputMissing, details: map[string]string{"input": "who"}},

		{name: "the run list", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunSucceeded, nil, "a1") }, method: "GET", path: "/api/v1/cluster/task-runs", status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var runs []cluster.RunRecord
				a.data(t, &runs)
				if len(runs) != 1 || runs[0].ID != srcRun {
					t.Errorf("runs = %+v", runs)
				}
			}},
		{name: "a run", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunSucceeded, nil, "a1") }, method: "GET", path: "/api/v1/cluster/task-runs/" + srcRun, status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var d struct {
					Run   cluster.RunRecord                `json:"run"`
					Nodes map[string]cluster.RunNodeRecord `json:"nodes"`
				}
				a.data(t, &d)
				if d.Run.ID != srcRun || d.Nodes["a1"].Status != cluster.RunNodePending {
					t.Errorf("run = %+v", d)
				}
			}},
		{name: "an unknown run", method: "GET", path: "/api/v1/cluster/task-runs/" + srcRun, status: 404, code: response.ErrTaskRunNotFound},
		{name: "an unknown run stopped", method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/stop", status: 404, code: response.ErrTaskRunNotFound},
		{name: "a run stopped", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunRunning, nil, "a1") }, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/stop", status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				if h := f.env.store.head(srcRun); h.Status != cluster.RunStopping || h.Force || h.Reason != "stopped by operator" {
					t.Errorf("head = %s force %v %q, want stopping, not forced, by the operator", h.Status, h.Force, h.Reason)
				}
			}},
		{name: "a run handing over stopped by force", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunHandingOver, nil, "a1") }, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/stop",
			body: map[string]bool{"force": true}, status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				if h := f.env.store.head(srcRun); h.Status != cluster.RunStopping || !h.Force {
					t.Errorf("head = %s force %v, want stopping by force", h.Status, h.Force)
				}
			}},
		{name: "a run being stopped forced", setup: func(f *apiFixture) {
			f.runOf(srcRun, cluster.RunStopping, nil, "a1")
			f.env.store.setHead(srcRun, func(h *cluster.RunRecord) { h.Reason = "stopped by someone" })
		}, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/stop", body: map[string]bool{"force": true}, status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				if h := f.env.store.head(srcRun); h.Status != cluster.RunStopping || !h.Force || h.Reason != "stopped by someone" {
					t.Errorf("head = %s force %v %q, want stopping by force, by the first to stop it", h.Status, h.Force, h.Reason)
				}
			}},
		{name: "an ended run stopped", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunFailed, nil, "a1") }, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/stop", status: 200,
			check: func(t *testing.T, f *apiFixture, a answer) {
				if h := f.env.store.head(srcRun); h.Status != cluster.RunFailed || h.Reason != "" {
					t.Errorf("head = %s %q, want failed as it was", h.Status, h.Reason)
				}
			}},
		{name: "an unknown run run again", method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/rerun?only=failed", status: 404, code: response.ErrTaskRunNotFound},
		{name: "a run under way run again", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunRunning, nil, "a1") }, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/rerun?only=failed",
			status: 409, code: response.ErrTaskRunActive, details: map[string]string{"run_id": srcRun}},
		{name: "a run with no failed node run again", setup: func(f *apiFixture) {
			f.runOf(srcRun, cluster.RunSucceeded, map[string]cluster.RunNodeRecord{"a1": {Status: cluster.RunNodeSucceeded}}, "a1")
		}, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/rerun?only=failed", status: 400, code: response.ErrTaskNoTargets},
		{name: "a run again on nodes it did not run", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunFailed, nil, "a1") }, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/rerun",
			body: map[string]any{"nodes": []string{"a2"}}, status: 400, code: response.ErrTaskNoTargets},
		{name: "a run again on chosen nodes", setup: func(f *apiFixture) { f.runOf(srcRun, cluster.RunFailed, nil, "a1", "a2") }, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/rerun",
			body: map[string]any{"nodes": []string{"a2", "a9"}}, status: 202,
			check: func(t *testing.T, f *apiFixture, a answer) {
				var d struct {
					RunID string `json:"run_id"`
				}
				a.data(t, &d)
				h := f.env.store.head(d.RunID)
				if h.RerunOf != srcRun || !slices.Equal(h.Nodes, []string{"a2"}) || h.StartedBy != "operator" || h.Status != cluster.RunRunning {
					t.Errorf("rerun = %+v", h)
				}
			}},
		{name: "a run again whose node another run holds", setup: func(f *apiFixture) {
			f.runOf(srcRun, cluster.RunFailed, nil, "a1")
			f.runOf(otherRun, cluster.RunRunning, nil, "a1")
		}, method: "POST", path: "/api/v1/cluster/task-runs/" + srcRun + "/rerun?only=failed", status: 409, code: response.ErrTaskTargetBusy,
			details: map[string]string{"node_id": "a1", "run_id": otherRun}},
		{name: "the output of a step no job ran", setup: func(f *apiFixture) {
			f.runOf(srcRun, cluster.RunRunning, map[string]cluster.RunNodeRecord{"a1": {Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepPending}}}}, "a1")
		}, method: "GET",
			path: "/api/v1/cluster/task-runs/" + srcRun + "/nodes/a1/steps/0/output", status: 404, code: response.ErrTaskJobNotFound,
			check: func(t *testing.T, f *apiFixture, a answer) {
				// This manager's answer, not a node's: no node is asked for a job
				// the run never recorded.
				if a.Error != nil && a.Error.Message != "No job has run this step on this node" {
					t.Errorf("message = %q, want this manager's", a.Error.Message)
				}
			}},
		{name: "the output of a step of an unknown run", method: "GET", path: "/api/v1/cluster/task-runs/" + srcRun + "/nodes/a1/steps/0/output", status: 404, code: response.ErrTaskRunNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPI(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			a := f.do(tc.method, tc.path, tc.body)
			if tc.code != "" {
				wantRefusal(t, tc.method+" "+tc.path, a, tc.status, tc.code, tc.details)
			} else if a.Status != tc.status || !a.Success {
				t.Fatalf("%s %s = %d %+v, want %d", tc.method, tc.path, a.Status, a.Error, tc.status)
			}
			if tc.check != nil {
				tc.check(t, f, a)
			}
		})
	}
}

// builtinNamed is a user document that takes built-in id's name.
func builtinNamed(t *testing.T, id string) string {
	t.Helper()
	for _, b := range tasks.Builtins() {
		if b.ID == id {
			task, _ := tasks.Parse(b.YAML)
			return "name: " + task.Name + "\nsteps:\n  - command: { run: \"true\" }\n"
		}
	}
	t.Fatalf("no built-in %s", id)
	return ""
}

func TestARunIsRefusedWhileTheGateIsBusy(t *testing.T) {
	cases := []struct {
		busy    featurecluster.Busy
		status  int
		code    string
		details map[string]string
	}{
		{featurecluster.Busy{UpdateRunning: true}, 409, response.ErrUpdateInProgress, nil},
		{featurecluster.Busy{CARotation: true}, 409, response.ErrClusterCARotating, nil},
		{featurecluster.Busy{Converting: true}, 409, response.ErrTaskClusterChanging, map[string]string{"what": "conversion"}},
		{featurecluster.Busy{Promoting: true}, 409, response.ErrTaskClusterChanging, map[string]string{"what": "promotion"}},
	}
	for _, tc := range cases {
		t.Run(tc.code+fmt.Sprint(tc.details), func(t *testing.T) {
			f := newAPI(t)
			f.store("t1", greetDoc)
			f.h.startGate = func(claim func(featurecluster.Busy) error) error { return claim(tc.busy) }
			a := f.do("POST", "/api/v1/cluster/task-runs", map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}})
			wantRefusal(t, "a run while busy", a, tc.status, tc.code, tc.details)
			if runs, _ := f.env.store.ListRuns(); len(runs) != 0 {
				t.Errorf("a refused run was recorded: %+v", runs)
			}
		})
	}
}

func TestAFollowerSendsWritesToTheLeader(t *testing.T) {
	writes := []struct{ method, path string }{
		{"POST", "/api/v1/cluster/tasks"},
		{"PUT", "/api/v1/cluster/tasks/t1"},
		{"DELETE", "/api/v1/cluster/tasks/t1"},
		{"POST", "/api/v1/cluster/task-runs/plan"},
		{"POST", "/api/v1/cluster/task-runs"},
		{"POST", "/api/v1/cluster/task-runs/" + srcRun + "/stop"},
		{"POST", "/api/v1/cluster/task-runs/" + srcRun + "/rerun"},
		// A preview writes nothing, but the leader is where an import is
		// judged and written, against the tasks it holds.
		{"POST", "/api/v1/cluster/tasks/import/preview"},
		{"POST", "/api/v1/cluster/tasks/import"},
	}
	for _, w := range writes {
		t.Run(w.method+" "+w.path, func(t *testing.T) {
			f := newAPI(t)
			f.store("t1", greetDoc)
			f.runOf(srcRun, cluster.RunFailed, nil, "a1")
			f.env.notLeader = true
			a := f.do(w.method, w.path, map[string]any{"yaml": greetDoc, "task_id": "t1", "inputs": map[string]string{"who": "x"}, "nodes": []string{"a1"}})
			if a.Status != http.StatusTeapot || !slices.Equal(f.forwarded, []string{w.method + " " + w.path}) {
				t.Errorf("answer %d, forwarded %v; want the leader's answer to %s %s", a.Status, f.forwarded, w.method, w.path)
			}
		})
	}

	reads := []string{
		"/api/v1/cluster/tasks",
		"/api/v1/cluster/tasks/t1",
		"/api/v1/cluster/tasks/builtin%2Fcommand",
		"/api/v1/cluster/task-runs",
		"/api/v1/cluster/task-runs/" + srcRun,
	}
	for _, path := range reads {
		t.Run("GET "+path, func(t *testing.T) {
			f := newAPI(t)
			f.store("t1", greetDoc)
			f.runOf(srcRun, cluster.RunFailed, nil, "a1")
			f.env.notLeader = true
			a := f.do("GET", path, nil)
			if a.Status != http.StatusOK || len(f.forwarded) != 0 {
				t.Errorf("answer %d, forwarded %v; want this manager's own answer", a.Status, f.forwarded)
			}
		})
	}
	t.Run("POST /api/v1/cluster/tasks/validate", func(t *testing.T) {
		f := newAPI(t)
		f.env.notLeader = true
		if a := f.do("POST", "/api/v1/cluster/tasks/validate", map[string]string{"yaml": greetDoc}); a.Status != 200 || len(f.forwarded) != 0 {
			t.Errorf("answer %d, forwarded %v; a check writes nothing and is answered here", a.Status, f.forwarded)
		}
	})
}

func TestBuiltinTasksAreReadOnly(t *testing.T) {
	f := newAPI(t)
	f.store("t1", greetDoc)

	var list []taskView
	f.do("GET", "/api/v1/cluster/tasks", nil).data(t, &list)
	byID := map[string]taskView{}
	for _, v := range list {
		byID[v.ID] = v
	}
	for _, b := range tasks.Builtins() {
		v, ok := byID[b.ID]
		if !ok || !v.ReadOnly || v.Source.Kind != sourceBuiltin || v.Name == "" {
			t.Errorf("listed %s = %+v (%v), want it named, read-only and built in", b.ID, v, ok)
		}
	}
	if v := byID["t1"]; v.ReadOnly || v.Name != "greet" {
		t.Errorf("listed t1 = %+v, want greet, not read-only", v)
	}

	var one taskView
	a := f.do("GET", "/api/v1/cluster/tasks/builtin%2Fcommand", nil)
	a.data(t, &one)
	if a.Status != 200 || one.ID != "builtin/command" || one.YAML == "" || one.Task == nil || !one.ReadOnly {
		t.Errorf("GET builtin/command = %d %+v, want its document, parsed, read-only", a.Status, one)
	}

	wantRefusal(t, "PUT builtin/command", f.do("PUT", "/api/v1/cluster/tasks/builtin%2Fcommand", map[string]string{"yaml": greetDoc}), 409, response.ErrTaskReadOnly, nil)
	wantRefusal(t, "DELETE builtin/command", f.do("DELETE", "/api/v1/cluster/tasks/builtin%2Fcommand", nil), 409, response.ErrTaskReadOnly, nil)
	if len(f.env.store.tasks) != 1 {
		t.Errorf("stored tasks = %v, want t1 alone", f.env.store.tasks)
	}
}

func TestARunWaitsForEveryManagerToKnowTasks(t *testing.T) {
	f := newAPI(t)
	f.store("t1", greetDoc)
	f.runOf(srcRun, cluster.RunFailed, nil, "a1")
	f.env.store.notReady = []string{"m2 (offline)", "m3 (v0.80.0)"}

	a := f.do("POST", "/api/v1/cluster/task-runs", map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}})
	wantRefusal(t, "a run", a, 409, response.ErrTaskManagersNotReady, map[string]string{"managers": "m2 (offline), m3 (v0.80.0)"})
	a = f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun?only=failed", nil)
	wantRefusal(t, "a rerun", a, 409, response.ErrTaskManagersNotReady, map[string]string{"managers": "m2 (offline), m3 (v0.80.0)"})
	if runs, _ := f.env.store.ListRuns(); len(runs) != 1 {
		t.Errorf("runs = %+v, want the old one alone", runs)
	}
}

func TestTwoRunsRacingForOneNodeStartOne(t *testing.T) {
	f := newAPI(t)
	f.store("t1", greetDoc)
	// Both requests are planned before either is claimed: the claim's own
	// look at the runs under way is what keeps the second out.
	gate := &featurecluster.Handler{}
	var arrived sync.WaitGroup
	arrived.Add(2)
	f.h.startGate = func(claim func(featurecluster.Busy) error) error {
		arrived.Done()
		arrived.Wait()
		return gate.StartGate(claim)
	}
	body := map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}, "targets": map[string]any{"nodes": []string{"a1"}}}
	answers := make([]answer, 2)
	var wg sync.WaitGroup
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = f.do("POST", "/api/v1/cluster/task-runs", body)
		}()
	}
	wg.Wait()
	slices.SortFunc(answers, func(a, b answer) int { return a.Status - b.Status })
	if answers[0].Status != http.StatusAccepted {
		t.Fatalf("answers = %d %+v and %d %+v, want one 202", answers[0].Status, answers[0].Error, answers[1].Status, answers[1].Error)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	answers[0].data(t, &started)
	wantRefusal(t, "the second run", answers[1], 409, response.ErrTaskTargetBusy, map[string]string{"node_id": "a1", "run_id": started.RunID})
	if runs, _ := f.env.store.ListRuns(); len(runs) != 1 {
		t.Errorf("runs = %+v, want one", runs)
	}
}

func TestOnlyFailedRunsTheNodesThatDidNotSucceed(t *testing.T) {
	f := newAPI(t)
	f.env.nodes = append(f.env.nodes, agent("a4", "a4"))
	f.runOf(srcRun, cluster.RunFailed, map[string]cluster.RunNodeRecord{
		"a1": {Status: cluster.RunNodeSucceeded, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepSucceeded}, {Status: cluster.RunStepSucceeded}}},
		"a2": {Status: cluster.RunNodeFailed, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepSucceeded}, {Status: cluster.RunStepFailed}}},
		"a3": {Status: cluster.RunNodeUnknown, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepUnknown}, {Status: cluster.RunStepPending}}},
		"a4": {Status: cluster.RunNodePending},
	}, "a1", "a2", "a3", "a4")

	a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun?only=failed", nil)
	if a.Status != http.StatusAccepted {
		t.Fatalf("rerun = %d %+v, want 202", a.Status, a.Error)
	}
	var d struct {
		RunID string `json:"run_id"`
	}
	a.data(t, &d)
	h := f.env.store.head(d.RunID)
	if !slices.Equal(h.Nodes, []string{"a2", "a3", "a4"}) || fmt.Sprint(h.StartAt) != "map[a2:1 a3:0 a4:0]" || h.Inputs["who"] != "world" {
		t.Errorf("rerun = nodes %v, start at %v, inputs %v; want a2 from step 1, a3 and a4 from 0, with the run's inputs", h.Nodes, h.StartAt, h.Inputs)
	}
	var got struct {
		Nodes map[string]cluster.RunNodeRecord `json:"nodes"`
	}
	f.do("GET", "/api/v1/cluster/task-runs/"+d.RunID, nil).data(t, &got)
	for _, id := range h.Nodes {
		if rec := got.Nodes[id]; rec.Status != cluster.RunNodePending {
			t.Errorf("node %s = %+v, want pending", id, rec)
		}
	}
}

// TestAStopBetweenTheEnginesReadAndWriteIsKept puts the operator's stop
// between the engine's read of a run's head and its write of it. The stop
// waits for the write and then applies to it; without the shared lock the
// engine's write, made from what it read before, overwrites the stop.
func TestAStopBetweenTheEnginesReadAndWriteIsKept(t *testing.T) {
	writes := map[string]func(f *apiFixture) error{
		"resuming a handed-over run": func(f *apiFixture) error {
			run := f.env.store.head(srcRun)
			return resumeHandedOver(f.env.store, &run)
		},
		"the runner's own head write": func(f *apiFixture) error {
			run := f.env.store.head(srcRun)
			r := &runner{env: f.env, st: f.env.store, run: &run}
			return r.putHead(func(h *cluster.RunRecord) {
				if h.Status == cluster.RunHandingOver {
					h.Status = cluster.RunRunning
				}
			})
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			f := newAPI(t)
			f.runOf(srcRun, cluster.RunHandingOver, nil, "a1")
			// The first read is the engine's; the stop's own read must not wait
			// on this hook (a sync.Once would make it), or it would be held
			// back by the test rather than by the lock.
			var hooked atomic.Bool
			stopped := make(chan answer, 1)
			f.env.store.afterGetRun = func(string) {
				if !hooked.CompareAndSwap(false, true) {
					return
				}
				go func() { stopped <- f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/stop", nil) }()
				// Give the stop the time to land, as it would without the lock.
				select {
				case a := <-stopped:
					stopped <- a
				case <-time.After(50 * time.Millisecond):
				}
			}
			if err := write(f); err != nil {
				t.Fatal(err)
			}
			if a := <-stopped; a.Status != 200 {
				t.Fatalf("stop = %d %+v", a.Status, a.Error)
			}
			if h := f.env.store.head(srcRun); h.Status != cluster.RunStopping || h.Reason != "stopped by operator" {
				t.Errorf("head = %s %q, want the operator's stop kept", h.Status, h.Reason)
			}
		})
	}
}

func TestNodeOutputIsReadFromTheNodesJob(t *testing.T) {
	setup := func(t *testing.T) *apiFixture {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunRunning, map[string]cluster.RunNodeRecord{
			"a1": {Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepRunning, JobID: jobID(srcRun, 0)}, {Status: cluster.RunStepPending}}},
		}, "a1")
		return f
	}
	path := "/api/v1/cluster/task-runs/" + srcRun + "/nodes/a1/steps/0/output?skip=6"

	t.Run("the node's job answers", func(t *testing.T) {
		f := setup(t)
		if _, err := f.env.StartJob(context.Background(), "a1", tasks.JobSpec{ID: jobID(srcRun, 0), RunID: srcRun, Step: 0, Kind: tasks.StepCommand}, "operator"); err != nil {
			t.Fatal(err)
		}
		f.env.output["a1/0"] = "hello world\n"
		a := f.do("GET", path, nil)
		var v tasks.JobView
		a.data(t, &v)
		if a.Status != 200 || v.ID != jobID(srcRun, 0) || v.Output != "world\n" || v.NextSkip != 12 {
			t.Errorf("output = %d %+v, want the job's output from byte 6", a.Status, v)
		}
	})
	t.Run("the node refuses", func(t *testing.T) {
		f := setup(t)
		a := f.do("GET", path, nil)
		wantRefusal(t, "a job the node does not have", a, 404, response.ErrTaskJobNotFound, nil)
		if a.Error != nil && a.Error.Message != "No such task job on this node" {
			t.Errorf("message = %q, want the node's own", a.Error.Message)
		}
	})
	t.Run("the node is not reached", func(t *testing.T) {
		f := setup(t)
		if _, err := f.env.StartJob(context.Background(), "a1", tasks.JobSpec{ID: jobID(srcRun, 0), RunID: srcRun, Step: 0, Kind: tasks.StepCommand}, "operator"); err != nil {
			t.Fatal(err)
		}
		f.env.getErrs["a1/0"] = 1
		wantRefusal(t, "an unreached node", f.do("GET", path, nil), 502, response.ErrClusterNodeUnreachable, map[string]string{"name": "a1"})
	})
	t.Run("a step past the task's", func(t *testing.T) {
		f := setup(t)
		wantRefusal(t, "step 7", f.do("GET", "/api/v1/cluster/task-runs/"+srcRun+"/nodes/a1/steps/7/output", nil), 404, response.ErrTaskJobNotFound, nil)
	})
}

func TestTheGateSeesARunUnderWay(t *testing.T) {
	f := newAPI(t)
	if id := f.h.ActiveRun(); id != "" {
		t.Errorf("ActiveRun with no run = %q", id)
	}
	f.runOf(srcRun, cluster.RunSucceeded, nil, "a1")
	if id := f.h.ActiveRun(); id != "" {
		t.Errorf("ActiveRun with an ended run = %q", id)
	}
	f.runOf(otherRun, cluster.RunStopping, nil, "a2")
	if id := f.h.ActiveRun(); id != otherRun {
		t.Errorf("ActiveRun = %q, want %s", id, otherRun)
	}
	none := &Handler{newEnv: func() runEnv { return nil }}
	if id := none.ActiveRun(); id != "" {
		t.Errorf("ActiveRun outside a cluster = %q", id)
	}
}

func TestOneSupervisorRunsAtATime(t *testing.T) {
	old := superviseInterval
	superviseInterval = 5 * time.Millisecond
	t.Cleanup(func() { superviseInterval = old })

	env := newFakeEnv(t, "m1", manager("m1", "m1"))
	env.stopped = make(chan struct{})
	var mu sync.Mutex
	started := 0
	h := &Handler{runners: map[string]context.CancelFunc{}, newEnv: func() runEnv {
		mu.Lock()
		started++
		mu.Unlock()
		return env
	}}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return started
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.Start(ctx)
	h.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	if n := count(); n != 1 {
		t.Fatalf("supervisors started = %d, want 1", n)
	}

	close(env.stopped) // the manager stops, and its supervisor with it
	deadline := time.Now().Add(2 * time.Second)
	for h.supervising() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.supervising() {
		t.Fatal("the supervisor still runs after its manager stopped")
	}
	env.stopped = make(chan struct{})
	h.Start(ctx) // a manager activated at runtime
	time.Sleep(20 * time.Millisecond)
	if n := count(); n != 2 {
		t.Errorf("supervisors started = %d, want a second once the first ended", n)
	}
}

// sseReader reads a stream's events: each block up to a blank line.
type sseReader struct {
	t *testing.T
	r *bufio.Reader
}

func (s sseReader) next() string {
	s.t.Helper()
	var lines []string
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			s.t.Fatalf("the stream ended (%v) after %q", err, lines)
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			if len(lines) == 0 {
				continue
			}
			return strings.Join(lines, "\n")
		}
		lines = append(lines, line)
	}
}

// event decodes a "data:" block.
func (s sseReader) event(out any) string {
	s.t.Helper()
	block := s.next()
	data, ok := strings.CutPrefix(block, "data: ")
	if !ok {
		s.t.Fatalf("event %q is not data", block)
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &head); err != nil {
		s.t.Fatalf("event %q: %v", data, err)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(data), out); err != nil {
			s.t.Fatalf("event %q: %v", data, err)
		}
	}
	return head.Type
}

func openStream(t *testing.T, f *apiFixture, runID string) sseReader {
	t.Helper()
	srv := httptest.NewServer(f.e)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/cluster/task-runs/"+runID+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return sseReader{t: t, r: bufio.NewReader(resp.Body)}
}

func TestTheStreamStartsWithASnapshotAndKeepsAlive(t *testing.T) {
	old := streamPoll
	streamPoll = 2 * time.Millisecond
	t.Cleanup(func() { streamPoll = old })

	t.Run("a run under way", func(t *testing.T) {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunRunning, nil, "a1", "a2")
		s := openStream(t, f, srcRun)

		var snap struct {
			Run   cluster.RunRecord                `json:"run"`
			Nodes map[string]cluster.RunNodeRecord `json:"nodes"`
		}
		if typ := s.event(&snap); typ != "snapshot" || snap.Run.ID != srcRun || len(snap.Nodes) != 2 || snap.Nodes["a1"].Status != cluster.RunNodePending {
			t.Fatalf("first event = %s %+v, want the run's snapshot", typ, snap)
		}

		f.env.Sleep(streamKeepalive) // the fake clock: nothing has changed for 15s
		if block := s.next(); block != ": keepalive" {
			t.Fatalf("event = %q, want a keepalive after 15s of quiet", block)
		}

		running := cluster.RunNodeRecord{Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepRunning, JobID: jobID(srcRun, 0)}}}
		if err := f.env.store.PutRunNode(srcRun, "a2", running); err != nil {
			t.Fatal(err)
		}
		var node struct {
			NodeID string                `json:"node_id"`
			Record cluster.RunNodeRecord `json:"record"`
		}
		if typ := s.event(&node); typ != "node" || node.NodeID != "a2" || node.Record.Status != cluster.RunNodeRunning || node.Record.Steps[0].JobID != jobID(srcRun, 0) {
			t.Fatalf("event = %s %+v, want a2's new record", typ, node)
		}

		f.env.store.setHead(srcRun, func(h *cluster.RunRecord) { h.Status = cluster.RunSucceeded })
		var run struct {
			Run cluster.RunRecord `json:"run"`
		}
		if typ := s.event(&run); typ != "run" || run.Run.Status != cluster.RunSucceeded {
			t.Fatalf("event = %s %+v, want the head that ended", typ, run)
		}
		if typ := s.event(nil); typ != "end" {
			t.Fatalf("event = %s, want end", typ)
		}
		if line, err := s.r.ReadString('\n'); err == nil {
			t.Errorf("the stream goes on after its end: %q", line)
		}
	})

	t.Run("a run that has ended", func(t *testing.T) {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunFailed, nil, "a1")
		s := openStream(t, f, srcRun)
		if typ := s.event(nil); typ != "snapshot" {
			t.Fatalf("first event = %s, want snapshot", typ)
		}
		if typ := s.event(nil); typ != "end" {
			t.Fatalf("event = %s, want end", typ)
		}
	})

	t.Run("an unknown run", func(t *testing.T) {
		f := newAPI(t)
		wantRefusal(t, "stream", f.do("GET", "/api/v1/cluster/task-runs/"+srcRun+"/stream", nil), 404, response.ErrTaskRunNotFound, nil)
	})
}

// A run is recorded in one write, its head: a head written with only some of
// its nodes' records would be a run that started in part, which a new leader
// would take up although the operator was told to retry. A node with no
// record yet is pending, and the run's readers say so.
func TestARunIsRecordedInOneWrite(t *testing.T) {
	old := streamPoll
	streamPoll = 2 * time.Millisecond
	t.Cleanup(func() { streamPoll = old })

	f := newAPI(t)
	f.store("t1", greetDoc)
	a := f.do("POST", "/api/v1/cluster/task-runs", map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}})
	if a.Status != http.StatusAccepted {
		t.Fatalf("start = %d %+v", a.Status, a.Error)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	a.data(t, &started)
	f.env.store.mu.Lock()
	recorded := len(f.env.store.nodes[started.RunID])
	f.env.store.mu.Unlock()
	if recorded != 0 {
		t.Errorf("node records written at the start = %d, want none: the head alone", recorded)
	}

	var got struct {
		Run   cluster.RunRecord                `json:"run"`
		Nodes map[string]cluster.RunNodeRecord `json:"nodes"`
	}
	f.do("GET", "/api/v1/cluster/task-runs/"+started.RunID, nil).data(t, &got)
	if len(got.Nodes) != 3 {
		t.Errorf("GET nodes = %+v, want a1, a2 and a3", got.Nodes)
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if got.Nodes[id].Status != cluster.RunNodePending {
			t.Errorf("GET node %s = %+v, want pending", id, got.Nodes[id])
		}
	}

	s := openStream(t, f, started.RunID)
	var snap struct {
		Nodes map[string]cluster.RunNodeRecord `json:"nodes"`
	}
	if typ := s.event(&snap); typ != "snapshot" || len(snap.Nodes) != 3 {
		t.Fatalf("first event = %s %+v, want a snapshot of every node", typ, snap)
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if snap.Nodes[id].Status != cluster.RunNodePending {
			t.Errorf("snapshot node %s = %+v, want pending", id, snap.Nodes[id])
		}
	}
}

// A new leader's FSM may not yet hold what the previous one committed: a
// write it judged from that state could undo a run's end or miss a run that
// holds a node. Every write waits for the leader to catch up, and writes
// nothing meanwhile.
func TestWritesWaitForTheLeaderToCatchUp(t *testing.T) {
	writes := []struct{ method, path string }{
		{"POST", "/api/v1/cluster/tasks"},
		{"PUT", "/api/v1/cluster/tasks/t1"},
		{"DELETE", "/api/v1/cluster/tasks/t1"},
		{"POST", "/api/v1/cluster/task-runs/plan"},
		{"POST", "/api/v1/cluster/task-runs"},
		{"POST", "/api/v1/cluster/task-runs/" + otherRun + "/stop"},
		{"POST", "/api/v1/cluster/task-runs/" + srcRun + "/rerun"},
		{"POST", "/api/v1/cluster/tasks/import/preview"},
		{"POST", "/api/v1/cluster/tasks/import"},
	}
	for _, w := range writes {
		t.Run(w.method+" "+w.path, func(t *testing.T) {
			f := newAPI(t)
			// Each write would be answered without the wait: t1 runs on the
			// agents, a3 is free, srcRun has ended and otherRun is under way.
			f.store("t1", "name: other\ntargets: { kinds: [agent] }\nsteps:\n  - command: { run: \"true\" }\n")
			f.runOf(srcRun, cluster.RunFailed, nil, "a1")
			f.runOf(otherRun, cluster.RunRunning, nil, "a2")
			f.env.behind = true
			a := f.do(w.method, w.path, map[string]any{"yaml": greetDoc, "task_id": "t1", "nodes": []string{"a1"}})
			wantRefusal(t, w.method+" "+w.path, a, http.StatusServiceUnavailable, response.ErrInternalError, nil)
			if a.Error != nil && a.Error.Message != "The cluster leader is catching up: retry in a few seconds" {
				t.Errorf("message = %q, want the catching-up one", a.Error.Message)
			}
			if len(f.forwarded) != 0 {
				t.Errorf("forwarded %v: this node is the leader", f.forwarded)
			}
			if runs, _ := f.env.store.ListRuns(); len(runs) != 2 {
				t.Errorf("runs = %+v, want the two recorded before", runs)
			}
			if h := f.env.store.head(otherRun); h.Status != cluster.RunRunning || h.Reason != "" {
				t.Errorf("head = %s %q, want running as it was", h.Status, h.Reason)
			}
			if task, err := f.env.store.GetTask("t1"); err != nil || task.Name != "other" {
				t.Errorf("t1 = %+v (%v), want it as it was", task, err)
			}
			if tasks, _ := f.env.store.ListTasks(); len(tasks) != 1 {
				t.Errorf("tasks = %+v, want t1 alone", tasks)
			}
		})
	}
}

// rebootDoc reboots its targets, whatever /var/run/reboot-required says.
const rebootDoc = "name: reboot them\nsteps:\n  - reboot: { only_if_required: false }\n"

// Two runs rebooting managers at once would each judge the quorum from the
// managers online, and both see it kept: one run at a time reboots managers.
func TestOneRunAtATimeRebootsManagers(t *testing.T) {
	start := func(f *apiFixture, task string, nodes ...string) answer {
		return f.do("POST", "/api/v1/cluster/task-runs", map[string]any{"task_id": task, "targets": map[string]any{"nodes": nodes}})
	}
	setup := func(t *testing.T) *apiFixture {
		f := newAPI(t)
		f.env.nodes = append(f.env.nodes, manager("m3", "m3"))
		f.store("reboot", rebootDoc)
		f.store("t1", "name: uptime\nsteps:\n  - command: { run: uptime }\n")
		return f
	}
	runID := func(t *testing.T, a answer) string {
		t.Helper()
		if a.Status != http.StatusAccepted {
			t.Fatalf("first run = %d %+v, want 202", a.Status, a.Error)
		}
		var d struct {
			RunID string `json:"run_id"`
		}
		a.data(t, &d)
		return d.RunID
	}

	t.Run("two runs rebooting different managers: the second waits", func(t *testing.T) {
		f := setup(t)
		first := runID(t, start(f, "reboot", "m2"))
		wantRefusal(t, "the second run", start(f, "reboot", "m3"), 409, response.ErrTaskRebootBusy, map[string]string{"run_id": first})
		if runs, _ := f.env.store.ListRuns(); len(runs) != 1 {
			t.Errorf("runs = %+v, want the first alone", runs)
		}
	})
	t.Run("a rerun rebooting a manager waits too", func(t *testing.T) {
		f := setup(t)
		rev, _ := f.env.store.PutTaskRevision(rebootDoc)
		_ = f.env.store.PutRun(cluster.RunRecord{ID: srcRun, TaskID: "reboot", Rev: rev, Nodes: []string{"m3"}, Waves: [][]string{{"m3"}}, Batch: 1, Status: cluster.RunFailed})
		first := runID(t, start(f, "reboot", "m2"))
		a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun?only=failed", nil)
		wantRefusal(t, "the rerun", a, 409, response.ErrTaskRebootBusy, map[string]string{"run_id": first})
	})
	for _, tc := range []struct {
		name          string
		first, second []string
		firstTask     string
	}{
		{"an agents' reboot beside a manager's", []string{"m2"}, []string{"a1"}, "reboot"},
		{"a manager's reboot beside an agents'", []string{"a1"}, []string{"m2"}, "reboot"},
		{"a manager's reboot beside a run on a manager that reboots nothing", []string{"m3"}, []string{"m2"}, "t1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			runID(t, start(f, tc.firstTask, tc.first...))
			if a := start(f, "reboot", tc.second...); a.Status != http.StatusAccepted {
				t.Errorf("second run = %d %+v, want 202", a.Status, a.Error)
			}
		})
	}
}

// A rerun is ordered over the cluster as it is now, not in the waves of the
// run it reruns: the leader may have moved, and a node may have changed kind.
func TestARerunIsPlannedAnew(t *testing.T) {
	rerun := func(t *testing.T, f *apiFixture, nodes ...string) cluster.RunRecord {
		t.Helper()
		a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun", map[string]any{"nodes": nodes})
		if a.Status != http.StatusAccepted {
			t.Fatalf("rerun = %d %+v, want 202", a.Status, a.Error)
		}
		var d struct {
			RunID string `json:"run_id"`
		}
		a.data(t, &d)
		return f.env.store.head(d.RunID)
	}
	t.Run("the leadership moved: the new leader goes last", func(t *testing.T) {
		f := newAPI(t)
		// m2 led the run: it went last. m1 leads now.
		f.runOf(srcRun, cluster.RunFailed, nil, "a1", "m1", "m2")
		f.env.store.setHead(srcRun, func(h *cluster.RunRecord) { h.Waves = [][]string{{"a1"}, {"m1"}, {"m2"}} })
		h := rerun(t, f, "a1", "m1", "m2")
		if got := fmt.Sprint(h.Waves); got != "[[a1] [m2] [m1]]" {
			t.Errorf("waves = %s, want [[a1] [m2] [m1]]: the leader last", got)
		}
		if !slices.Equal(h.Nodes, []string{"a1", "m2", "m1"}) {
			t.Errorf("nodes = %v, want them in the new order", h.Nodes)
		}
	})
	t.Run("an agent promoted since: a wave of its own", func(t *testing.T) {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunFailed, nil, "a1", "a2")
		f.env.store.setHead(srcRun, func(h *cluster.RunRecord) { h.Batch = 2 })
		for i, n := range f.env.nodes {
			if n.ID == "a2" {
				f.env.nodes[i] = manager("a2", "a2")
			}
		}
		h := rerun(t, f, "a1", "a2")
		if got := fmt.Sprint(h.Waves); got != "[[a1] [a2]]" {
			t.Errorf("waves = %s, want [[a1] [a2]]: a manager alone", got)
		}
		if h.Batch != 2 || fmt.Sprint(h.StartAt) != "map[a1:0 a2:0]" || h.Inputs["who"] != "world" || h.Rev != f.env.store.head(srcRun).Rev {
			t.Errorf("rerun = %+v, want the source's batch, revision and inputs, each node from step 0", h)
		}
	})
	t.Run("every node offline: no targets", func(t *testing.T) {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunFailed, nil, "a1")
		f.env.health["a1"] = cluster.StatusOffline
		a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun?only=failed", nil)
		wantRefusal(t, "the rerun", a, 400, response.ErrTaskNoTargets, nil)
		if runs, _ := f.env.store.ListRuns(); len(runs) != 1 {
			t.Errorf("runs = %+v, want the source alone", runs)
		}
	})
}

// Inputs are copied into the run record every manager keeps: each value is
// capped, and all of a run's values together.
func TestInputsAreCapped(t *testing.T) {
	const fiveInputs = `name: five inputs
inputs:
  a: {}
  b: {}
  c: {}
  d: {}
  e: {}
targets: { kinds: [agent] }
steps:
  - command: { run: "echo {{ a }} {{ b }} {{ c }} {{ d }} {{ e }}" }
`
	big := strings.Repeat("x", 16<<10)
	for _, path := range []string{"/api/v1/cluster/task-runs/plan", "/api/v1/cluster/task-runs"} {
		t.Run(path, func(t *testing.T) {
			f := newAPI(t)
			f.store("t1", fiveInputs)
			a := f.do("POST", path, map[string]any{"task_id": "t1", "inputs": map[string]string{"a": "small", "b": big + "y"}})
			wantRefusal(t, "one value over 16 KiB", a, 400, response.ErrTaskTooLarge, map[string]string{"input": "b"})

			all := map[string]string{"a": big, "b": big, "c": big, "d": big, "e": "y"}
			a = f.do("POST", path, map[string]any{"task_id": "t1", "inputs": all})
			wantRefusal(t, "the values over 64 KiB together", a, 400, response.ErrTaskTooLarge, map[string]string{"input": "*"})

			if runs, _ := f.env.store.ListRuns(); len(runs) != 0 {
				t.Errorf("runs = %+v, want none", runs)
			}
			all["e"] = ""
			if a := f.do("POST", path, map[string]any{"task_id": "t1", "inputs": all}); !a.Success {
				t.Errorf("values of 64 KiB together = %d %+v, want them taken", a.Status, a.Error)
			}
		})
	}
}

// The preview says which managers a run would wait for, so the operator
// learns it before pressing start.
func TestThePreviewNamesTheManagersNotReady(t *testing.T) {
	f := newAPI(t)
	f.store("t1", greetDoc)
	plan := func() (p struct {
		ManagersNotReady []string `json:"managers_not_ready"`
	}, raw string) {
		a := f.do("POST", "/api/v1/cluster/task-runs/plan", map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}})
		if a.Status != 200 {
			t.Fatalf("plan = %d %+v", a.Status, a.Error)
		}
		a.data(t, &p)
		return p, string(a.Data)
	}
	f.env.store.notReady = []string{"m2 (offline)"}
	if p, _ := plan(); !slices.Equal(p.ManagersNotReady, []string{"m2 (offline)"}) {
		t.Errorf("managers_not_ready = %v, want [m2 (offline)]", p.ManagersNotReady)
	}
	f.env.store.notReady = nil
	if _, raw := plan(); strings.Contains(raw, "managers_not_ready") {
		t.Errorf("plan = %s, want managers_not_ready left out with every manager ready", raw)
	}
}

// The preview names the steps of the document it previewed, so the screen
// labels its verdicts and confirmations from that document rather than one
// it read earlier: the task may have changed in between.
func TestThePreviewNamesTheStepsOfTheDocumentItPreviewed(t *testing.T) {
	f := newAPI(t)
	f.store("t1", `name: greet
targets: { kinds: [agent] }
steps:
  - name: say hello
    command: { run: "echo hello" }
  - service: { name: nginx.service, action: restart }
`)
	steps := func() []runStep {
		t.Helper()
		a := f.do("POST", "/api/v1/cluster/task-runs/plan", map[string]any{"task_id": "t1"})
		if a.Status != 200 {
			t.Fatalf("plan = %d %+v", a.Status, a.Error)
		}
		var p struct {
			Steps []runStep `json:"steps"`
		}
		a.data(t, &p)
		return p.Steps
	}
	want := []runStep{{Name: "say hello", Kind: tasks.StepCommand}, {Kind: tasks.StepService}}
	if got := steps(); !slices.Equal(got, want) {
		t.Errorf("steps = %+v, want %+v", got, want)
	}

	changed := `name: greet
targets: { kinds: [agent] }
steps:
  - name: stop docker
    service: { name: docker.service, action: stop }
  - command: { run: "true" }
  - name: say hello
    command: { run: "echo hello" }
`
	if a := f.do("PUT", "/api/v1/cluster/tasks/t1", map[string]string{"yaml": changed}); a.Status != 200 {
		t.Fatalf("update = %d %+v", a.Status, a.Error)
	}
	want = []runStep{{Name: "stop docker", Kind: tasks.StepService}, {Kind: tasks.StepCommand}, {Name: "say hello", Kind: tasks.StepCommand}}
	if got := steps(); !slices.Equal(got, want) {
		t.Errorf("steps after the change = %+v, want %+v", got, want)
	}
}

// A confirmation is the operator's yes to what the preview showed: a step of
// that document, filled in with those inputs, on those targets. The preview
// names what it showed (plan_id), and a start with confirmations must name
// the same, or a confirmation could unlock a step the operator never saw.
func TestConfirmationsAreTiedToThePreview(t *testing.T) {
	const doc = `name: greet
inputs:
  who: { required: true }
targets: { kinds: [agent] }
steps:
  - command: { run: "echo {{ who }}" }
  - service: { name: "{{ who }}", action: restart }
`
	preview := func(t *testing.T, f *apiFixture, body map[string]any) string {
		t.Helper()
		a := f.do("POST", "/api/v1/cluster/task-runs/plan", body)
		var p struct {
			PlanID string `json:"plan_id"`
		}
		a.data(t, &p)
		if a.Status != 200 || len(p.PlanID) != 64 {
			t.Fatalf("plan = %d %+v, plan_id %q; want 200 with a sha256", a.Status, a.Error, p.PlanID)
		}
		return p.PlanID
	}
	body := func(change ...func(b map[string]any)) map[string]any {
		b := map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "nginx"}}
		for _, c := range change {
			c(b)
		}
		return b
	}
	confirm := func(id string) func(b map[string]any) {
		return func(b map[string]any) { b["plan_id"], b["confirmed"] = id, []int{1} }
	}

	t.Run("the task changed since the preview", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		id := preview(t, f, body())
		changed := strings.Replace(doc, "action: restart", "action: stop", 1)
		if a := f.do("PUT", "/api/v1/cluster/tasks/t1", map[string]string{"yaml": changed}); a.Status != 200 {
			t.Fatalf("PUT = %d %+v", a.Status, a.Error)
		}
		a := f.do("POST", "/api/v1/cluster/task-runs", body(confirm(id)))
		wantRefusal(t, "a start after an edit", a, 409, response.ErrTaskPlanChanged, nil)
	})
	t.Run("other inputs than the preview's", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		id := preview(t, f, body())
		a := f.do("POST", "/api/v1/cluster/task-runs", body(confirm(id), func(b map[string]any) { b["inputs"] = map[string]string{"who": "pve-cluster"} }))
		wantRefusal(t, "a start with other inputs", a, 409, response.ErrTaskPlanChanged, nil)
	})
	t.Run("other targets than the preview's", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		id := preview(t, f, body())
		a := f.do("POST", "/api/v1/cluster/task-runs", body(confirm(id), func(b map[string]any) { b["targets"] = map[string]any{"nodes": []string{"a1"}} }))
		wantRefusal(t, "a start with other targets", a, 409, response.ErrTaskPlanChanged, nil)
	})
	t.Run("confirmations without the preview's plan_id", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		a := f.do("POST", "/api/v1/cluster/task-runs", body(func(b map[string]any) { b["confirmed"] = []int{1} }))
		wantRefusal(t, "a start with no plan_id", a, 400, response.ErrInvalidRequest, nil)
		if a.Error != nil && a.Error.Message != "confirmed steps need the preview's plan_id" {
			t.Errorf("message = %q, want the plan_id one", a.Error.Message)
		}
	})
	t.Run("a step number the task does not have", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		id := preview(t, f, body())
		a := f.do("POST", "/api/v1/cluster/task-runs", body(func(b map[string]any) { b["plan_id"], b["confirmed"] = id, []int{2} }))
		wantRefusal(t, "a start confirming step 2", a, 400, response.ErrInvalidRequest, nil)
		if a.Error != nil && a.Error.Message != "The task has no step 2 to confirm" {
			t.Errorf("message = %q, want the step's", a.Error.Message)
		}
	})
	t.Run("nothing changed: the run starts with its confirmations", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		id := preview(t, f, body())
		a := f.do("POST", "/api/v1/cluster/task-runs", body(confirm(id)))
		if a.Status != http.StatusAccepted {
			t.Fatalf("start = %d %+v, want 202", a.Status, a.Error)
		}
		var d struct {
			RunID string `json:"run_id"`
		}
		a.data(t, &d)
		if h := f.env.store.head(d.RunID); !slices.Equal(h.Confirmed, []int{1}) {
			t.Errorf("confirmed = %v, want [1]", h.Confirmed)
		}
	})
	t.Run("no confirmations: no plan_id needed", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", doc)
		if a := f.do("POST", "/api/v1/cluster/task-runs", body()); a.Status != http.StatusAccepted {
			t.Errorf("start = %d %+v, want 202", a.Status, a.Error)
		}
	})
}

// A node's audit row names the operator a leader's call is for: the
// preview's checks carry who asked for the preview, a stop records who
// stopped the run.
func TestNodeCallsCarryTheOperator(t *testing.T) {
	t.Run("the preview's checks", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", greetDoc)
		if a := f.do("POST", "/api/v1/cluster/task-runs/plan", map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}}); a.Status != 200 {
			t.Fatalf("plan = %d %+v", a.Status, a.Error)
		}
		got := slices.Sorted(slices.Values(f.env.checkedBy))
		if want := []string{"a1:operator", "a2:operator", "a3:operator"}; !slices.Equal(got, want) {
			t.Errorf("checks = %v, want %v", got, want)
		}
	})
	t.Run("a stop", func(t *testing.T) {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunRunning, nil, "a1")
		f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/stop", nil)
		if h := f.env.store.head(srcRun); h.StoppedBy != "operator" {
			t.Errorf("stopped_by = %q, want operator", h.StoppedBy)
		}
	})
	t.Run("a stop forced by another operator", func(t *testing.T) {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunStopping, nil, "a1")
		f.env.store.setHead(srcRun, func(h *cluster.RunRecord) { h.Reason, h.StoppedBy = "stopped by someone", "someone" })
		f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/stop", map[string]bool{"force": true})
		if h := f.env.store.head(srcRun); h.StoppedBy != "operator" || !h.Force {
			t.Errorf("stopped_by = %q, force %v; want the operator who forced it", h.StoppedBy, h.Force)
		}
	})
}

// A node that refuses the manager's read as unauthenticated is the node's
// trouble, not the operator's: a 401 passed through as the node gave it is
// read by the browser as its own session ending, and logs the operator out.
// An agent's is 502 CLUSTER_AGENT_AUTH_FAILED, as a ?node= relay answers it;
// a manager's 502 INTERNAL_ERROR. Any other refusal passes as it was given.
func TestStepOutputDoesNotPassANodes401ToTheBrowser(t *testing.T) {
	setup := func(t *testing.T, node string, refusal error) *apiFixture {
		f := newAPI(t)
		f.runOf(srcRun, cluster.RunRunning, map[string]cluster.RunNodeRecord{
			node: {Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{{Status: cluster.RunStepRunning, JobID: jobID(srcRun, 0)}}},
		}, node)
		f.env.getRefusal = refusal
		return f
	}
	unauthenticated := &jobRefusal{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: "Missing or invalid token"}
	output := func(node string) string {
		return "/api/v1/cluster/task-runs/" + srcRun + "/nodes/" + node + "/steps/0/output"
	}

	t.Run("an agent", func(t *testing.T) {
		f := setup(t, "a1", unauthenticated)
		wantRefusal(t, "an agent's 401", f.do("GET", output("a1"), nil), http.StatusBadGateway, response.ErrClusterAgentAuthFailed, nil)
	})
	t.Run("a manager", func(t *testing.T) {
		f := setup(t, "m2", unauthenticated)
		wantRefusal(t, "a manager's 401", f.do("GET", output("m2"), nil), http.StatusBadGateway, response.ErrInternalError, nil)
	})
	t.Run("any other refusal", func(t *testing.T) {
		f := setup(t, "a1", &jobRefusal{Status: http.StatusNotFound, Code: response.ErrTaskJobNotFound, Message: "No such task job on this node"})
		a := f.do("GET", output("a1"), nil)
		wantRefusal(t, "an agent's 404", a, http.StatusNotFound, response.ErrTaskJobNotFound, nil)
		if a.Error != nil && a.Error.Message != "No such task job on this node" {
			t.Errorf("message = %q, want the node's own", a.Error.Message)
		}
	})
}

// A run's steps are named from the document it ran, kept by its revision:
// the task may since have been changed or deleted. A step with no name has
// its kind alone. A run whose revision is gone answers without steps.
func TestARunAnswersItsStepNamesFromItsRevision(t *testing.T) {
	const namedDoc = `name: named
targets: { kinds: [agent] }
steps:
  - name: say hello
    command: { run: "echo hello" }
  - packages: { update: true }
`
	type runAnswer struct {
		Run   cluster.RunRecord   `json:"run"`
		Steps []map[string]string `json:"steps"`
	}
	get := func(t *testing.T, f *apiFixture, id string) (runAnswer, map[string]json.RawMessage) {
		t.Helper()
		a := f.do("GET", "/api/v1/cluster/task-runs/"+id, nil)
		if a.Status != 200 {
			t.Fatalf("GET run = %d %+v", a.Status, a.Error)
		}
		var out runAnswer
		var raw map[string]json.RawMessage
		a.data(t, &out)
		a.data(t, &raw)
		return out, raw
	}

	t.Run("the task changed, then deleted", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", namedDoc)
		rev, _ := f.env.store.PutTaskRevision(namedDoc)
		if err := f.env.store.PutRun(cluster.RunRecord{ID: srcRun, TaskID: "t1", TaskName: "named", Rev: rev,
			Nodes: []string{"a1"}, Waves: [][]string{{"a1"}}, Batch: 1, Status: cluster.RunSucceeded, StartedBy: "operator"}); err != nil {
			t.Fatal(err)
		}
		if a := f.do("PUT", "/api/v1/cluster/tasks/t1", map[string]string{"yaml": greetDoc}); !a.Success {
			t.Fatalf("change = %d %+v", a.Status, a.Error)
		}
		if a := f.do("DELETE", "/api/v1/cluster/tasks/t1", nil); !a.Success {
			t.Fatalf("delete = %d %+v", a.Status, a.Error)
		}
		got, _ := get(t, f, srcRun)
		want := []map[string]string{{"name": "say hello", "kind": tasks.StepCommand}, {"kind": tasks.StepPackages}}
		if fmt.Sprint(got.Steps) != fmt.Sprint(want) {
			t.Errorf("steps = %v, want %v: the document the run ran", got.Steps, want)
		}
	})
	t.Run("the revision is gone", func(t *testing.T) {
		f := newAPI(t)
		if err := f.env.store.PutRun(cluster.RunRecord{ID: srcRun, TaskID: "t1", TaskName: "named", Rev: strings.Repeat("0", 64),
			Nodes: []string{"a1"}, Waves: [][]string{{"a1"}}, Batch: 1, Status: cluster.RunSucceeded, StartedBy: "operator"}); err != nil {
			t.Fatal(err)
		}
		got, raw := get(t, f, srcRun)
		if steps, ok := raw["steps"]; ok || got.Run.ID != srcRun {
			t.Errorf("answer = run %q, steps %s, want the run without steps", got.Run.ID, steps)
		}
	})
}

// TASK_TOO_LARGE always names the limit that was passed, in details.max_kib,
// so a client can word every refusal the same way; an input's refusal also
// names the input, or "*" for all of them together.
func TestTooLargeAlwaysNamesTheLimit(t *testing.T) {
	const twoInputs = `name: two inputs
inputs:
  a: {}
  b: {}
targets: { kinds: [agent] }
steps:
  - command: { run: "echo {{ a }} {{ b }}" }
`
	big := strings.Repeat("x", 16<<10)
	for _, path := range []string{"/api/v1/cluster/task-runs/plan", "/api/v1/cluster/task-runs"} {
		t.Run(path, func(t *testing.T) {
			f := newAPI(t)
			f.store("t1", twoInputs)
			a := f.do("POST", path, map[string]any{"task_id": "t1", "inputs": map[string]string{"a": big + "y"}})
			wantRefusal(t, "one value over 16 KiB", a, 400, response.ErrTaskTooLarge, map[string]string{"input": "a", "max_kib": "16"})
		})
	}
	t.Run("the values together", func(t *testing.T) {
		f := newAPI(t)
		f.store("t1", `name: five inputs
inputs:
  a: {}
  b: {}
  c: {}
  d: {}
  e: {}
targets: { kinds: [agent] }
steps:
  - command: { run: "echo {{ a }} {{ b }} {{ c }} {{ d }} {{ e }}" }
`)
		a := f.do("POST", "/api/v1/cluster/task-runs", map[string]any{"task_id": "t1", "inputs": map[string]string{"a": big, "b": big, "c": big, "d": big, "e": "y"}})
		wantRefusal(t, "the values over 64 KiB together", a, 400, response.ErrTaskTooLarge, map[string]string{"input": "*", "max_kib": "64"})
	})
	t.Run("the document", func(t *testing.T) {
		f := newAPI(t)
		doc := greetDoc + "#" + strings.Repeat("x", tasks.MaxDocumentBytes)
		for _, path := range []string{"/api/v1/cluster/tasks", "/api/v1/cluster/tasks/validate"} {
			a := f.do("POST", path, map[string]string{"yaml": doc})
			wantRefusal(t, path, a, 400, response.ErrTaskTooLarge, map[string]string{"max_kib": "64"})
		}
		// The store's own refusal, which the size check above keeps from
		// being reached through the API today.
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
		if err := fail(c, storeError(cluster.ErrTaskTooLarge)); err != nil {
			t.Fatal(err)
		}
		var a answer
		if err := json.Unmarshal(c.Response().Writer.(*httptest.ResponseRecorder).Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		a.Status = c.Response().Status
		wantRefusal(t, "the store's refusal", a, 400, response.ErrTaskTooLarge, map[string]string{"max_kib": "64"})
	})
}

// A blank name reaches the store only past Parse, which refuses it first;
// the store's refusal is still the document's problem, not a server error.
func TestTheStoresBlankNameRefusalIsTheDocumentsProblem(t *testing.T) {
	c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
	if err := fail(c, storeError(cluster.ErrTaskNameRequired)); err != nil {
		t.Fatal(err)
	}
	var a answer
	if err := json.Unmarshal(c.Response().Writer.(*httptest.ResponseRecorder).Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	a.Status = c.Response().Status
	wantRefusal(t, "the store's refusal", a, 400, response.ErrTaskInvalid, nil)
	if a.Error == nil || a.Error.Message != "The task's name is blank" {
		t.Errorf("message = %+v, want %q", a.Error, "The task's name is blank")
	}
}

func TestTheFormatRoute(t *testing.T) {
	const path = "/api/v1/cluster/tasks/format"
	model, problems := tasks.Parse([]byte(greetDoc))
	if problems != nil {
		t.Fatal(problems)
	}
	// The model as the editor holds it: the JSON validate answers with.
	var validated struct {
		Task json.RawMessage `json:"task"`
	}
	newAPI(t).do("POST", "/api/v1/cluster/tasks/validate", map[string]string{"yaml": greetDoc}).data(t, &validated)
	task := validated.Task

	t.Run("the task's document", func(t *testing.T) {
		f := newAPI(t)
		a := f.do("POST", path, map[string]any{"task": task})
		if a.Status != http.StatusOK {
			t.Fatalf("answer %d %q, want 200", a.Status, a.code())
		}
		var out struct {
			YAML string `json:"yaml"`
		}
		a.data(t, &out)
		got, problems := tasks.Parse([]byte(out.YAML))
		if problems != nil || !reflect.DeepEqual(got, model) {
			t.Errorf("yaml %q parses as %+v (%+v), want %+v", out.YAML, got, problems, model)
		}
	})

	t.Run("a follower answers it itself", func(t *testing.T) {
		f := newAPI(t)
		f.env.notLeader = true
		a := f.do("POST", path, map[string]any{"task": task})
		if a.Status != http.StatusOK || len(f.forwarded) != 0 {
			t.Errorf("answer %d, forwarded %v; a format writes nothing and is answered here", a.Status, f.forwarded)
		}
	})

	t.Run("a broken body", func(t *testing.T) {
		f := newAPI(t)
		for what, body := range map[string]string{
			"not JSON":       `{"task": `,
			"no task":        `{}`,
			"a task of null": `{"task": null}`,
			"not a task":     `{"task": "name: x"}`,
			"a wrong field":  `{"task": {"steps": "many"}}`,
		} {
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			w := httptest.NewRecorder()
			f.e.ServeHTTP(w, r)
			a := answer{Status: w.Code}
			if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
				t.Fatalf("%s: answer %q is not the envelope: %v", what, w.Body.String(), err)
			}
			wantRefusal(t, what, a, http.StatusBadRequest, response.ErrInvalidRequest, nil)
		}
	})

	// Format re-indents, so a stored document near the limit can come out
	// over it. The editor still gets it, to trim in its YAML view: saving
	// it is what refuses.
	t.Run("a document over 64 KiB", func(t *testing.T) {
		f := newAPI(t)
		big := *model
		big.Description = strings.Repeat("x", tasks.MaxDocumentBytes)
		a := f.do("POST", path, map[string]any{"task": big})
		if a.Status != http.StatusOK {
			t.Fatalf("answer %d %q, want 200", a.Status, a.code())
		}
		var out struct {
			YAML string `json:"yaml"`
		}
		a.data(t, &out)
		if len(out.YAML) <= tasks.MaxDocumentBytes || !strings.Contains(out.YAML, big.Description) {
			t.Errorf("yaml is %d bytes, want the whole document, over %d", len(out.YAML), tasks.MaxDocumentBytes)
		}
		save := f.do("POST", "/api/v1/cluster/tasks", map[string]string{"yaml": out.YAML})
		wantRefusal(t, "saving it", save, http.StatusBadRequest, response.ErrTaskTooLarge, map[string]string{"max_kib": "64"})
	})
}

// A task run's leadership hand-over would cut an install's sessions off, so
// one waits for the other: an install under way refuses a task run's start
// and a rerun, and none under way lets them through.
func TestTaskRunsAndAnInstallExcludeEachOther(t *testing.T) {
	f := newAPI(t)
	f.store("t1", greetDoc)
	f.runOf(srcRun, cluster.RunFailed, nil, "a1")
	fi := &fakeInstalls{active: true}
	f.h.SetInstallRuns(fi)

	start := map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}}
	wantRefusal(t, "a run during an install", f.do("POST", "/api/v1/cluster/task-runs", start), 409, response.ErrAgentInstallActive, nil)
	wantRefusal(t, "a rerun during an install", f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun?only=failed", nil), 409, response.ErrAgentInstallActive, nil)
	if runs, _ := f.env.store.ListRuns(); len(runs) != 1 {
		t.Errorf("runs = %+v, want the refused ones unrecorded", runs)
	}

	fi.mu.Lock()
	fi.active = false
	fi.mu.Unlock()
	if a := f.do("POST", "/api/v1/cluster/task-runs", start); a.Status != http.StatusAccepted {
		t.Errorf("a run with no install = %d %+v, want 202", a.Status, a.Error)
	}
}

func TestAnInstallIsNotRerunAsATask(t *testing.T) {
	f := newAPI(t)
	installRun(t, f.env.store, srcRun, cluster.RunFailed, installRows(), installKeys...)
	for _, path := range []string{"/rerun?only=failed", "/rerun"} {
		a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+path, map[string]any{"nodes": []string{"s1"}})
		wantRefusal(t, "POST "+path, a, 409, response.ErrAgentInstallNotRerunnable, nil)
	}
	if runs, _ := f.env.store.ListRuns(); len(runs) != 1 {
		t.Errorf("runs = %+v, want the install alone", runs)
	}
}

// An install's rows are servers, and their output is in the memory of the
// leader's session: no node has a job for it.
func TestAnInstallsOutputComesFromTheLeadersSession(t *testing.T) {
	path := "/api/v1/cluster/task-runs/" + srcRun + "/nodes/s3/steps/2/output?skip=6"
	setup := func(t *testing.T) (*apiFixture, *fakeInstalls) {
		f := newAPI(t)
		installRun(t, f.env.store, srcRun, cluster.RunRunning, installRows(), installKeys...)
		// A node asked for a job answers 418, which no answer here is.
		f.env.getRefusal = &jobRefusal{Status: http.StatusTeapot, Code: "ASKED_A_NODE"}
		fi := &fakeInstalls{active: true, owns: map[string]bool{srcRun: true},
			view: tasks.JobView{ID: "install/s3/2", Status: tasks.JobRunning, Output: "world\n", NextSkip: 12}}
		f.h.SetInstallRuns(fi)
		return f, fi
	}

	t.Run("the session answers", func(t *testing.T) {
		f, fi := setup(t)
		a := f.do("GET", path, nil)
		var v tasks.JobView
		a.data(t, &v)
		if a.Status != 200 || v.ID != "install/s3/2" || v.Output != "world\n" || v.NextSkip != 12 {
			t.Errorf("output = %d %+v (%+v), want the session's", a.Status, v, a.Error)
		}
		if want := []string{srcRun + "/s3/2/6"}; !slices.Equal(fi.outputs, want) {
			t.Errorf("the session was asked %v, want %v", fi.outputs, want)
		}
	})
	t.Run("no session has it", func(t *testing.T) {
		f, fi := setup(t)
		fi.owns = nil
		wantRefusal(t, "a session gone", f.do("GET", path, nil), 404, response.ErrAgentInstallLogGone, nil)
		if len(fi.outputs) != 0 {
			t.Errorf("a session that does not own the run was asked %v", fi.outputs)
		}
	})
	t.Run("the session has no such output", func(t *testing.T) {
		f, fi := setup(t)
		fi.outErr = errors.New("no row s3")
		wantRefusal(t, "an output the session lacks", f.do("GET", path, nil), 404, response.ErrAgentInstallLogGone, nil)
	})
	t.Run("no installer is set", func(t *testing.T) {
		f, _ := setup(t)
		f.h.SetInstallRuns(nil)
		wantRefusal(t, "no installer", f.do("GET", path, nil), 404, response.ErrAgentInstallLogGone, nil)
	})
	t.Run("a follower asks the leader", func(t *testing.T) {
		f, fi := setup(t)
		f.env.notLeader = true
		a := f.do("GET", path, nil)
		if a.Status != http.StatusTeapot || len(f.forwarded) != 1 || len(fi.outputs) != 0 {
			t.Errorf("answer %d, forwarded %v, asked %v; want the leader's answer", a.Status, f.forwarded, fi.outputs)
		}
	})
}

func TestStoppingAnInstallStopsItsSession(t *testing.T) {
	t.Run("its session stops it", func(t *testing.T) {
		f := newAPI(t)
		installRun(t, f.env.store, srcRun, cluster.RunRunning, installRows(), installKeys...)
		fi := &fakeInstalls{active: true, owns: map[string]bool{srcRun: true}}
		f.h.SetInstallRuns(fi)
		a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/stop", map[string]bool{"force": true})
		var head cluster.RunRecord
		a.data(t, &head)
		if a.Status != 200 || head.ID != srcRun {
			t.Errorf("stop = %d %+v, want the run's head", a.Status, a.Error)
		}
		if want := []string{srcRun + ":operator"}; !slices.Equal(fi.stops, want) {
			t.Errorf("the session was stopped %v, want %v", fi.stops, want)
		}
		if h := f.env.store.head(srcRun); h.Status != cluster.RunRunning {
			t.Errorf("run = %s, want the session to write its own stop", h.Status)
		}
	})
	t.Run("no session runs it", func(t *testing.T) {
		f := newAPI(t)
		installRun(t, f.env.store, srcRun, cluster.RunRunning, installRows(), installKeys...)
		fi := &fakeInstalls{}
		f.h.SetInstallRuns(fi)
		a := f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/stop", nil)
		var head cluster.RunRecord
		a.data(t, &head)
		if a.Status != 200 || head.Status != cluster.RunFailed || head.Reason != response.ErrAgentInstallLeaderChanged {
			t.Errorf("stop = %d %+v (%+v), want the install ended for the leader change", a.Status, head, a.Error)
		}
		if len(fi.stops) != 0 {
			t.Errorf("a session that does not own the run was stopped: %v", fi.stops)
		}
		if rec := f.env.store.node(srcRun, "s1"); rec.Status != cluster.RunNodeFailed || rec.Reason != response.ErrAgentInstallLeaderChanged {
			t.Errorf("row s1 = %+v, want failed for the leader change", rec)
		}
	})
}

// The install is listed with the built-ins, so the screen opens its own
// form from it; it has no document, and no task route runs it.
func TestTheInstallBuiltinIsListedButIsNotATask(t *testing.T) {
	f := newAPI(t)
	f.store("t1", greetDoc)

	var list []taskView
	f.do("GET", "/api/v1/cluster/tasks", nil).data(t, &list)
	n := len(tasks.Builtins())
	if len(list) != n+2 {
		t.Fatalf("listed %d tasks, want %d built-ins, the install and t1", len(list), n)
	}
	v := list[n]
	if v.ID != cluster.AgentInstallTaskID || v.Name == "" || v.Description == "" || !v.ReadOnly || v.Source.Kind != sourceBuiltin || v.YAML != "" {
		t.Errorf("listed after the built-ins: %+v, want the install, named, described, read-only, built in, no document", v)
	}
	if list[n+1].ID != "t1" {
		t.Errorf("listed last: %s, want the stored task", list[n+1].ID)
	}

	id := url.PathEscape(cluster.AgentInstallTaskID)
	var one taskView
	a := f.do("GET", "/api/v1/cluster/tasks/"+id, nil)
	a.data(t, &one)
	if a.Status != 200 || one.ID != cluster.AgentInstallTaskID || one.Name != v.Name || one.YAML != "" || one.Task != nil || !one.ReadOnly {
		t.Errorf("GET the install = %d %+v, want its view without a document or a model", a.Status, one)
	}

	body := map[string]any{"task_id": cluster.AgentInstallTaskID}
	wantRefusal(t, "plan the install", f.do("POST", "/api/v1/cluster/task-runs/plan", body), 404, response.ErrTaskNotFound, nil)
	wantRefusal(t, "start the install", f.do("POST", "/api/v1/cluster/task-runs", body), 404, response.ErrTaskNotFound, nil)
	wantRefusal(t, "PUT the install", f.do("PUT", "/api/v1/cluster/tasks/"+id, map[string]string{"yaml": greetDoc}), 409, response.ErrTaskReadOnly, nil)
	wantRefusal(t, "DELETE the install", f.do("DELETE", "/api/v1/cluster/tasks/"+id, nil), 409, response.ErrTaskReadOnly, nil)
	if runs, _ := f.env.store.ListRuns(); len(runs) != 0 {
		t.Errorf("runs = %+v, want none", runs)
	}
}

// An install's record is replicated to every manager and kept: none of its
// fields may be one a secret could go in. A field named for one fails here
// before review; host_key is the server's public key fingerprint, and
// host_key_tailscale how it compares with the one Tailscale reports (match
// or mismatch).
func TestInstallFieldsCarryNoSecret(t *testing.T) {
	forbidden := []string{"password", "passwd", "passphrase", "secret", "token", "private", "sudo", "credential", "key"}
	allowed := map[string]bool{"host_key": true, "host_key_tailscale": true, "credentials_until": true}
	for _, typ := range []reflect.Type{reflect.TypeFor[cluster.RunRecord](), reflect.TypeFor[cluster.RunNodeRecord](), reflect.TypeFor[cluster.RunStepRecord]()} {
		for i := range typ.NumField() {
			field := typ.Field(i)
			tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if allowed[tag] {
				continue
			}
			for _, word := range forbidden {
				if strings.Contains(strings.ToLower(tag), word) || strings.Contains(strings.ToLower(field.Name), word) {
					t.Errorf("%s.%s (json %q) is named for a secret (%s)", typ.Name(), field.Name, tag, word)
				}
			}
		}
	}
}
