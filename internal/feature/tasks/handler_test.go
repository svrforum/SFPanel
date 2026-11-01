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
		{name: "a run with a step number the task does not have", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/task-runs",
			body: start(func(b map[string]any) { b["confirmed"] = []int{2} }), status: 400, code: response.ErrInvalidRequest},
		{name: "a run", setup: func(f *apiFixture) { f.store("t1", greetDoc) }, method: "POST", path: "/api/v1/cluster/task-runs",
			body: start(func(b map[string]any) { b["confirmed"] = []int{1}; b["batch"] = 2 }), status: 202,
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
					!slices.Equal(head.Confirmed, []int{1}) || fmt.Sprint(head.Waves) != "[[a1 a2] [a3]]" || !head.StartedAt.Equal(f.env.now) {
					t.Errorf("head = %+v (doc %q)", head, doc)
				}
				for _, id := range []string{"a1", "a2", "a3"} {
					if nodes[id].Status != cluster.RunNodePending {
						t.Errorf("node %s = %+v, want pending", id, nodes[id])
					}
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
	for _, id := range h.Nodes {
		if rec := f.env.store.node(d.RunID, id); rec.Status != cluster.RunNodePending {
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
