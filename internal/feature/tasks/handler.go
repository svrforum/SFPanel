// Package featuretasks is the leader's half of group tasks: it plans a run
// of a task over its targets and runs it, node by node, through each node's
// task-job routes (internal/feature/taskjob). Definitions and run records
// live in the cluster's replicated state (internal/cluster/tasks_store.go),
// so any manager can serve them and a new leader takes a run over.
package featuretasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/feature/audit"
	featurecluster "github.com/svrforum/SFPanel/internal/feature/cluster"
	"github.com/svrforum/SFPanel/internal/paneltls"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// Handler serves group tasks on a manager and runs the runs this node leads.
type Handler struct {
	// getMgr is the cluster manager, nil until the node is in a cluster:
	// read per use, as a manager is activated without a restart.
	getMgr func() *cluster.Manager
	// gate is where a run starts, with a cluster update, a CA rotation, a
	// conversion and a promotion (StartGate).
	gate *featurecluster.Handler
	// audit records a run's start and end under the operator's name.
	audit *sfdb.AsyncWriter
	// self is this node's own panel port, for the leader's own jobs.
	self paneltls.Self
	// newEnv is the cluster as a run sees it from this node, nil outside
	// one: clusterEnv, which tests replace.
	newEnv func() runEnv
	// startGate is gate.StartGate, and toLeader sends a request on to the
	// leader (middleware.ProxyToLeader): both replaced in tests.
	startGate func(claim func(featurecluster.Busy) error) error
	toLeader  func(c echo.Context) (handled bool, err error)

	// runners are the runs this node is running, by run ID; wg counts
	// their goroutines. runCtx is the supervisor's context while it runs,
	// which every runner lives under; supervisorOn says one runs.
	mu           sync.Mutex
	runners      map[string]context.CancelFunc
	wg           sync.WaitGroup
	runCtx       context.Context
	supervisorOn bool
}

// New returns a Handler.
func New(getMgr func() *cluster.Manager, gate *featurecluster.Handler, audit *sfdb.AsyncWriter, self paneltls.Self) *Handler {
	h := &Handler{getMgr: getMgr, gate: gate, audit: audit, self: self, runners: map[string]context.CancelFunc{}}
	h.newEnv = h.clusterEnv
	h.startGate = func(claim func(featurecluster.Busy) error) error {
		if h.gate == nil {
			return claim(featurecluster.Busy{})
		}
		return h.gate.StartGate(claim)
	}
	h.toLeader = func(c echo.Context) (bool, error) { return middleware.ProxyToLeader(c, h.getMgr()) }
	return h
}

// Start runs the supervisor (supervise) for the cluster manager this node
// has now: at boot, and again each time a manager is activated without a
// restart. One runs at a time: a start while one runs does nothing. It ends
// by itself with its manager, and at once outside a cluster.
func (h *Handler) Start(ctx context.Context) {
	h.mu.Lock()
	if h.supervisorOn {
		h.mu.Unlock()
		return
	}
	h.supervisorOn = true
	h.mu.Unlock()
	go func() {
		defer func() {
			h.mu.Lock()
			h.supervisorOn = false
			h.mu.Unlock()
		}()
		h.supervise(ctx)
	}()
}

// supervising reports whether a supervisor runs.
func (h *Handler) supervising() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.supervisorOn
}

// ActiveRun is the ID of a run under way in this node's replicated state, or
// "" with none: the start gate's view of the runs (SetTaskRunsActive), so a
// cluster update, a CA rotation, a conversion and a promotion wait for it.
func (h *Handler) ActiveRun() string {
	env := h.newEnv()
	if env == nil {
		return ""
	}
	runs, err := env.Store().ListRuns()
	if err != nil {
		logger().Warn("listing task runs for the start gate", "error", err)
		return ""
	}
	for _, r := range runs {
		if r.Active() {
			return r.ID
		}
	}
	return ""
}

// clusterEnv is the cluster as a run sees it from this node, or nil outside
// one.
func (h *Handler) clusterEnv() runEnv {
	mgr := h.getMgr()
	if mgr == nil {
		return nil
	}
	return clusterRunEnv{mgr: mgr, self: h.self}
}

// launch runs run on this node unless it already does, until it ends or
// ctx does. It reports whether it started a runner. A run that has ended by
// the time the runner reads it is left alone, so a look at the runs taken
// just before a runner finished does not start the run over. The leader
// that ends a run records its end in the audit log.
func (h *Handler) launch(ctx context.Context, run cluster.RunRecord) bool {
	env := h.newEnv()
	if env == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ctx.Err() != nil {
		return false // the supervisor is stopping and waits for its runners
	}
	if _, ok := h.runners[run.ID]; ok {
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	h.runners[run.ID] = cancel
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() {
			cancel()
			h.mu.Lock()
			delete(h.runners, run.ID)
			h.mu.Unlock()
		}()
		st := env.Store()
		if head, _, err := st.GetRun(run.ID); err != nil || !head.Active() {
			return
		}
		if err := runTask(ctx, env, &run); err != nil {
			logger().Warn("task run left unfinished on this node", "run", run.ID, "error", err)
			return
		}
		if head, _, err := st.GetRun(run.ID); err == nil && !head.Active() {
			h.auditEnd(env.LocalID(), head)
		}
	}()
	return true
}

// begin starts a run the operator has just recorded: it writes the run's
// start to the audit log and runs it under the supervisor. With no
// supervisor running yet, the supervisor takes the run up when it starts.
func (h *Handler) begin(run cluster.RunRecord) {
	local := ""
	if env := h.newEnv(); env != nil {
		local = env.LocalID()
	}
	h.auditStart(local, run)
	h.mu.Lock()
	ctx := h.runCtx
	h.mu.Unlock()
	if ctx != nil {
		h.launch(ctx, run)
	}
}

// taskRunsAuditPath is where a run's audit rows sit: its start mirrors the
// request that made it, POST under the run's path; its end is no request, so
// its method says so and its path carries how the run ended. Neither carries
// the run's inputs.
const taskRunsAuditPath = "/api/v1/cluster/task-runs/"

// auditStart records a run's start under the operator who started it.
func (h *Handler) auditStart(nodeID string, run cluster.RunRecord) {
	audit.RecordEvent(h.audit, run.StartedBy, http.MethodPost, taskRunsAuditPath+run.ID+"#start", http.StatusAccepted, nodeID)
}

// auditEnd records how a run ended under the operator who started it.
func (h *Handler) auditEnd(nodeID string, run cluster.RunRecord) {
	audit.RecordEvent(h.audit, run.StartedBy, "TASK", taskRunsAuditPath+run.ID+"#end:"+run.Status, http.StatusOK, nodeID)
}

// The task and task-run API (design §8), under the manager-only /cluster
// group. Writes and the plan are the leader's: a follower sends them on
// (forward). Reads, the stream and a step's output answer from the
// receiving manager's own replicated state.

// sourceBuiltin is the source of a built-in task as the API shows it: built
// into every manager, never stored.
const sourceBuiltin = "builtin"

// apiError is a refusal the API answers with: its status, code and details.
type apiError struct {
	status  int
	code    string
	message string
	details map[string]string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func refusal(status int, code, message string, details map[string]string) *apiError {
	return &apiError{status: status, code: code, message: message, details: details}
}

// fail answers err: a refusal as itself; a write a follower was left with
// (the leader changed under it) 503; anything else 500.
func fail(c echo.Context, err error) error {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		return response.FailWith(c, ae.status, ae.code, ae.message, ae.details)
	case errors.Is(err, cluster.ErrNotLeader):
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "This node is not the cluster leader: retry in a few seconds")
	}
	logger().Error("task API", "path", c.Path(), "error", err)
	return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "The task store could not be read or written")
}

// apiEnv is the cluster for one request, or nil after answering that this
// node is in none.
func (h *Handler) apiEnv(c echo.Context) (runEnv, error) {
	env := h.newEnv()
	if env == nil {
		return nil, response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	return env, nil
}

// forward sends a write on to the leader when this manager is not it, as
// the cluster handler's label and token writes do. On the leader it answers
// 503 until the leader has caught up (CaughtUp, as the supervisor waits for):
// a new leader's FSM may not yet hold what the previous one committed, and a
// write judged from it could undo a run's end or miss a run that holds a
// node. handled says the request was answered.
func (h *Handler) forward(c echo.Context, env runEnv) (handled bool, err error) {
	if !env.IsLeader() {
		return h.toLeader(c)
	}
	if !env.CaughtUp() {
		return true, response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "The cluster leader is catching up: retry in a few seconds")
	}
	return false, nil
}

func operator(c echo.Context) string {
	username, _ := c.Get("username").(string)
	return username
}

// taskDoc is a task as the API knows it: stored, or built in.
type taskDoc struct {
	ID        string
	Name      string
	YAML      string
	Source    cluster.TaskSource
	UpdatedAt time.Time
	UpdatedBy string
}

func (d taskDoc) builtin() bool { return d.Source.Kind == sourceBuiltin }

// readOnly reports whether the task may not be changed here: a built-in, or
// one imported from Git, whose next fetch would undo the change.
func (d taskDoc) readOnly() bool { return d.builtin() || d.Source.Kind == cluster.TaskSourceGit }

// builtins are the built-in tasks with their names, read once.
var builtins = sync.OnceValue(func() []taskDoc {
	var out []taskDoc
	for _, b := range tasks.Builtins() {
		d := taskDoc{ID: b.ID, Name: b.ID, YAML: string(b.YAML), Source: cluster.TaskSource{Kind: sourceBuiltin}}
		if t, _ := tasks.Parse(b.YAML); t != nil {
			d.Name = t.Name
		}
		out = append(out, d)
	}
	return out
})

func builtinDoc(id string) (taskDoc, bool) {
	for _, d := range builtins() {
		if d.ID == id {
			return d, true
		}
	}
	return taskDoc{}, false
}

// builtinName reports whether a built-in task has name: a stored task may
// not take it, or an operator could not tell the two apart.
func builtinName(name string) bool {
	return slices.ContainsFunc(builtins(), func(d taskDoc) bool { return d.Name == name })
}

// taskParam is the task ID in the path. A built-in's ID has a slash, which
// the panel sends escaped (builtin%2Fcommand) and Echo hands over as sent.
func taskParam(c echo.Context) string {
	id := c.Param("id")
	if u, err := url.PathUnescape(id); err == nil {
		return u
	}
	return id
}

// loadTask is the task id names, stored or built in.
func loadTask(st taskStore, id string) (taskDoc, error) {
	if d, ok := builtinDoc(id); ok {
		return d, nil
	}
	t, err := st.GetTask(id)
	if errors.Is(err, cluster.ErrTaskNotFound) {
		return taskDoc{}, refusal(http.StatusNotFound, response.ErrTaskNotFound, "No such task", nil)
	}
	if err != nil {
		return taskDoc{}, err
	}
	return taskDoc{ID: t.ID, Name: t.Name, YAML: t.YAML, Source: t.Source, UpdatedAt: t.UpdatedAt, UpdatedBy: t.UpdatedBy}, nil
}

// taskView is a task as the API answers with it. The list leaves out the
// document; one task carries it, parsed when it parses.
type taskView struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Source      cluster.TaskSource `json:"source"`
	ReadOnly    bool               `json:"read_only"`
	UpdatedAt   time.Time          `json:"updated_at,omitzero"`
	UpdatedBy   string             `json:"updated_by,omitempty"`
	YAML        string             `json:"yaml,omitempty"`
	Task        *tasks.Task        `json:"task,omitempty"`
}

func viewOf(d taskDoc, full bool) taskView {
	v := taskView{ID: d.ID, Name: d.Name, Source: d.Source, ReadOnly: d.readOnly(), UpdatedAt: d.UpdatedAt, UpdatedBy: d.UpdatedBy}
	t, _ := tasks.Parse([]byte(d.YAML))
	if t != nil {
		v.Description = t.Description
	}
	if full {
		v.YAML, v.Task = d.YAML, t
	}
	return v
}

// taskBody is the body of a task's create, change and check: the document.
type taskBody struct {
	YAML string `json:"yaml"`
}

// checkDocument parses a task document for a create or a change. Too large
// is 400 TASK_TOO_LARGE; problems are 400 TASK_INVALID, listed with their
// lines in data.problems, as error details are strings only.
func checkDocument(c echo.Context, doc string) (*tasks.Task, bool, error) {
	if len(doc) > tasks.MaxDocumentBytes {
		return nil, false, response.FailWith(c, http.StatusBadRequest, response.ErrTaskTooLarge,
			cluster.ErrTaskTooLarge.Error(), map[string]string{"max_kib": strconv.Itoa(tasks.MaxDocumentBytes >> 10)})
	}
	t, problems := tasks.Parse([]byte(doc))
	if t == nil {
		return nil, false, c.JSON(http.StatusBadRequest, response.Response{
			Success: false,
			Data:    map[string]any{"problems": problems},
			Error:   &response.ErrorBody{Code: response.ErrTaskInvalid, Message: "The task document has problems"},
		})
	}
	return t, true, nil
}

// storeError answers a store write's refusal.
func storeError(err error) error {
	switch {
	case errors.Is(err, cluster.ErrTaskNameTaken):
		return refusal(http.StatusConflict, response.ErrTaskNameTaken, "Another task already has this name", nil)
	case errors.Is(err, cluster.ErrTaskTooLarge):
		return refusal(http.StatusBadRequest, response.ErrTaskTooLarge, err.Error(), map[string]string{"max_kib": strconv.Itoa(tasks.MaxDocumentBytes >> 10)})
	case errors.Is(err, cluster.ErrTaskNotFound):
		return refusal(http.StatusNotFound, response.ErrTaskNotFound, "No such task", nil)
	}
	return err
}

var errBuiltinName = refusal(http.StatusConflict, response.ErrTaskNameTaken, "A built-in task already has this name", nil)

// ListTasks answers GET /cluster/tasks: the built-ins, read-only, then the
// stored tasks by name.
func (h *Handler) ListTasks(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	stored, err := env.Tasks().ListTasks()
	if err != nil {
		return fail(c, err)
	}
	out := make([]taskView, 0, len(builtins())+len(stored))
	for _, d := range builtins() {
		out = append(out, viewOf(d, false))
	}
	for _, t := range stored {
		out = append(out, viewOf(taskDoc{ID: t.ID, Name: t.Name, YAML: t.YAML, Source: t.Source, UpdatedAt: t.UpdatedAt, UpdatedBy: t.UpdatedBy}, false))
	}
	return response.OK(c, out)
}

// GetTask answers GET /cluster/tasks/:id with its document, parsed.
func (h *Handler) GetTask(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	d, err := loadTask(env.Tasks(), taskParam(c))
	if err != nil {
		return fail(c, err)
	}
	return response.OK(c, viewOf(d, true))
}

// ValidateTask answers POST /cluster/tasks/validate {yaml}: whether the
// document is a task, its problems with their lines, and the task parsed.
// It writes nothing, so any manager answers it.
func (h *Handler) ValidateTask(c echo.Context) error {
	var body taskBody
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if len(body.YAML) > tasks.MaxDocumentBytes {
		return response.FailWith(c, http.StatusBadRequest, response.ErrTaskTooLarge,
			cluster.ErrTaskTooLarge.Error(), map[string]string{"max_kib": strconv.Itoa(tasks.MaxDocumentBytes >> 10)})
	}
	t, problems := tasks.Parse([]byte(body.YAML))
	if problems == nil {
		problems = []tasks.Problem{}
	}
	return response.OK(c, map[string]any{"valid": t != nil, "problems": problems, "task": t})
}

// formatBody is the body of a format: a task as the editor holds it, the
// model validate answers with.
type formatBody struct {
	Task *tasks.Task `json:"task"`
}

// FormatTask answers POST /cluster/tasks/format {task}: the document the task
// is, for the editor's YAML view (validate goes the other way). It writes
// nothing and reads no cluster state, so any manager answers it. A model that
// is not a valid task still gets its document; saving it is what refuses.
func (h *Handler) FormatTask(c echo.Context) error {
	var body formatBody
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if body.Task == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "task is required")
	}
	doc, err := tasks.Format(body.Task)
	if err != nil {
		logger().Error("task format", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "The task could not be written as YAML")
	}
	if len(doc) > tasks.MaxDocumentBytes {
		return response.FailWith(c, http.StatusBadRequest, response.ErrTaskTooLarge,
			cluster.ErrTaskTooLarge.Error(), map[string]string{"max_kib": strconv.Itoa(tasks.MaxDocumentBytes >> 10)})
	}
	return response.OK(c, map[string]string{"yaml": string(doc)})
}

// CreateTask answers POST /cluster/tasks {yaml} (leader): the task stored
// under a new ID, named by its document.
func (h *Handler) CreateTask(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	var body taskBody
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	t, ok, err := checkDocument(c, body.YAML)
	if !ok {
		return err
	}
	if builtinName(t.Name) {
		return fail(c, errBuiltinName)
	}
	st := cluster.StoredTask{ID: cluster.NewTaskID(), Name: t.Name, YAML: body.YAML,
		Source: cluster.TaskSource{Kind: cluster.TaskSourceUser}, UpdatedAt: env.Now().UTC(), UpdatedBy: operator(c)}
	if err := env.Tasks().PutTask(st); err != nil {
		return fail(c, storeError(err))
	}
	return response.OK(c, viewOf(taskDoc{ID: st.ID, Name: st.Name, YAML: st.YAML, Source: st.Source, UpdatedAt: st.UpdatedAt, UpdatedBy: st.UpdatedBy}, true))
}

// UpdateTask answers PUT /cluster/tasks/:id {yaml} (leader): the stored
// task's new document, and the name it gives. A built-in or a task from Git
// is 409 TASK_READ_ONLY.
func (h *Handler) UpdateTask(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	d, err := loadTask(env.Tasks(), taskParam(c))
	if err != nil {
		return fail(c, err)
	}
	if d.readOnly() {
		return response.Fail(c, http.StatusConflict, response.ErrTaskReadOnly, "This task is read-only: built in, or imported from Git")
	}
	var body taskBody
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	t, ok, err := checkDocument(c, body.YAML)
	if !ok {
		return err
	}
	if builtinName(t.Name) {
		return fail(c, errBuiltinName)
	}
	st := cluster.StoredTask{ID: d.ID, Name: t.Name, YAML: body.YAML, Source: d.Source, UpdatedAt: env.Now().UTC(), UpdatedBy: operator(c)}
	if err := env.Tasks().PutTask(st); err != nil {
		return fail(c, storeError(err))
	}
	return response.OK(c, viewOf(taskDoc{ID: st.ID, Name: st.Name, YAML: st.YAML, Source: st.Source, UpdatedAt: st.UpdatedAt, UpdatedBy: st.UpdatedBy}, true))
}

// DeleteTask answers DELETE /cluster/tasks/:id (leader). A built-in is 409
// TASK_READ_ONLY. A task from Git may go: nothing else would remove it. Its
// runs keep their own copy of the document.
func (h *Handler) DeleteTask(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	d, err := loadTask(env.Tasks(), taskParam(c))
	if err != nil {
		return fail(c, err)
	}
	if d.builtin() {
		return response.Fail(c, http.StatusConflict, response.ErrTaskReadOnly, "A built-in task cannot be deleted")
	}
	if err := env.Tasks().DeleteTask(d.ID); err != nil {
		return fail(c, storeError(err))
	}
	return response.OK(c, map[string]string{"id": d.ID})
}

// runRequest is the body of POST /cluster/task-runs and of its plan. Left
// out, the targets, the batch and stop_on_failure are the task's.
type runRequest struct {
	TaskID        string            `json:"task_id"`
	Inputs        map[string]string `json:"inputs"`
	Targets       *tasks.Targets    `json:"targets"`
	Batch         int               `json:"batch"`
	StopOnFailure *bool             `json:"stop_on_failure"`
	// Confirmed are the steps (0-based) the operator confirmed, in the
	// preview PlanID names: required with any confirmation.
	Confirmed []int  `json:"confirmed"`
	PlanID    string `json:"plan_id"`
}

// Input values are copied into the run record, which every manager keeps:
// each is capped, and all of a run's values together.
const (
	maxInputBytes  = 16 << 10
	maxInputsBytes = 64 << 10
)

// runInputs are the values given for t's own inputs: the run record is
// replicated to every manager, and a key the task never reads has no place
// in it.
func runInputs(t *tasks.Task, given map[string]string) map[string]string {
	var inputs map[string]string
	for _, in := range t.Inputs {
		if v, ok := given[in.Name]; ok {
			if inputs == nil {
				inputs = map[string]string{}
			}
			inputs[in.Name] = v
		}
	}
	return inputs
}

// checkInputSizes refuses a value over maxInputBytes, naming its input, and
// values over maxInputsBytes together, as input "*": 400 TASK_TOO_LARGE,
// with the limit passed in max_kib, as a document's refusal has it.
func checkInputSizes(inputs map[string]string) error {
	names := slices.Sorted(maps.Keys(inputs))
	total := 0
	for _, name := range names {
		v := inputs[name]
		if len(v) > maxInputBytes {
			return refusal(http.StatusBadRequest, response.ErrTaskTooLarge,
				fmt.Sprintf("Input %s is over %d KiB", name, maxInputBytes>>10), map[string]string{"input": name, "max_kib": strconv.Itoa(maxInputBytes >> 10)})
		}
		total += len(v)
	}
	if total > maxInputsBytes {
		return refusal(http.StatusBadRequest, response.ErrTaskTooLarge,
			fmt.Sprintf("The inputs are over %d KiB together", maxInputsBytes>>10), map[string]string{"input": "*", "max_kib": strconv.Itoa(maxInputsBytes >> 10)})
	}
	return nil
}

// runTarget is what a run request asks for, read and checked.
type runTarget struct {
	doc     taskDoc
	task    *tasks.Task
	targets tasks.Targets
	batch   int
}

// planID names what a preview showed, so a start's confirmations apply to
// that and nothing else: hex sha256 over the document's revision, the task's
// own inputs as given and the targets as given or the task's own (both as
// JSON, whose map keys are sorted).
func planID(doc string, inputs map[string]string, targets tasks.Targets) string {
	rev := sha256.Sum256([]byte(doc))
	in, _ := json.Marshal(inputs)
	tg, _ := json.Marshal(targets)
	h := sha256.New()
	h.Write([]byte(hex.EncodeToString(rev[:])))
	h.Write([]byte{0})
	h.Write(in)
	h.Write([]byte{0})
	h.Write(tg)
	return hex.EncodeToString(h.Sum(nil))
}

// readRunRequest binds a run request and loads its task, its input values
// capped (checkInputSizes).
func readRunRequest(c echo.Context, st taskStore) (runRequest, runTarget, error) {
	var req runRequest
	if err := c.Bind(&req); err != nil {
		return req, runTarget{}, refusal(http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body", nil)
	}
	d, err := loadTask(st, req.TaskID)
	if err != nil {
		return req, runTarget{}, err
	}
	t, problems := tasks.Parse([]byte(d.YAML))
	if t == nil {
		msg := "The task's document has problems"
		if len(problems) > 0 {
			msg += fmt.Sprintf(": line %d: %s", problems[0].Line, problems[0].Message)
		}
		return req, runTarget{}, refusal(http.StatusBadRequest, response.ErrTaskInvalid, msg, nil)
	}
	if err := checkInputSizes(runInputs(t, req.Inputs)); err != nil {
		return req, runTarget{}, err
	}
	rt := runTarget{doc: d, task: t, targets: t.Targets, batch: runBatch(t, req.Batch)}
	if req.Targets != nil {
		rt.targets = *req.Targets
	}
	return req, rt, nil
}

// planError answers what ordering or filling in a run refused.
func planError(err error) error {
	var missing *tasks.InputMissingError
	switch {
	case errors.As(err, &missing):
		return refusal(http.StatusBadRequest, response.ErrTaskInputMissing, missing.Error(), map[string]string{"input": missing.Name})
	case errors.Is(err, ErrTooManyTargets):
		return refusal(http.StatusBadRequest, response.ErrTaskTooManyTargets, err.Error(), map[string]string{"max": strconv.Itoa(MaxTargets)})
	}
	return err
}

func busyNode(nodeID, runID string) error {
	return refusal(http.StatusConflict, response.ErrTaskTargetBusy, "Node "+nodeID+" is taken by run "+runID+", which is under way",
		map[string]string{"node_id": nodeID, "run_id": runID})
}

func managersNotReady(st taskStore) error {
	if wait := st.TasksReady(); len(wait) > 0 {
		return refusal(http.StatusConflict, response.ErrTaskManagersNotReady,
			"Every manager must run a version that keeps group tasks: waiting for "+strings.Join(wait, ", "),
			map[string]string{"managers": strings.Join(wait, ", ")})
	}
	return nil
}

// PlanRun answers POST /cluster/task-runs/plan (leader), the preview: the
// waves, the targets left out with why, each node's verdicts on the steps,
// the managers a start would wait for, and the plan_id a start confirming
// steps sends back. It records nothing.
func (h *Handler) PlanRun(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	req, rt, err := readRunRequest(c, env.Tasks())
	if err != nil {
		return fail(c, err)
	}
	plan, err := planRun(env, rt.task, rt.targets, req.Inputs, rt.batch, operator(c))
	if err != nil {
		return fail(c, planError(err))
	}
	plan.PlanID = planID(rt.doc.YAML, runInputs(rt.task, req.Inputs), rt.targets)
	plan.ManagersNotReady = env.Tasks().TasksReady()
	return response.OK(c, plan)
}

// StartRun answers POST /cluster/task-runs (leader): the run is judged and
// recorded in one StartGate claim, then started; 202 {run_id}. Every
// manager must keep task runs (TasksReady). Confirmed steps need the
// preview's plan_id, and one that no longer matches is 409
// TASK_PLAN_CHANGED. The plan asks no node for its
// verdicts, so the request stays well inside the thirty seconds a
// follower's forward of it gets; the preview asked them.
func (h *Handler) StartRun(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	st := env.Tasks()
	req, rt, err := readRunRequest(c, st)
	if err != nil {
		return fail(c, err)
	}
	if err := managersNotReady(st); err != nil {
		return fail(c, err)
	}
	// A confirmation is a yes to what the preview showed: the same document,
	// inputs and targets, or it could unlock a step the operator never saw.
	if len(req.Confirmed) > 0 {
		switch req.PlanID {
		case "":
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "confirmed steps need the preview's plan_id")
		case planID(rt.doc.YAML, runInputs(rt.task, req.Inputs), rt.targets):
		default:
			return response.Fail(c, http.StatusConflict, response.ErrTaskPlanChanged, "the task, its inputs or its targets changed since the preview: preview again")
		}
	}
	confirmed := slices.Clone(req.Confirmed)
	slices.Sort(confirmed)
	confirmed = slices.Compact(confirmed)
	for _, n := range confirmed {
		if n < 0 || n >= len(rt.task.Steps) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, fmt.Sprintf("The task has no step %d to confirm", n))
		}
	}

	plan, byID, err := orderRun(env, rt.targets, rt.batch)
	if err != nil {
		return fail(c, planError(err))
	}
	if _, err := renderPlan(rt.task, req.Inputs, byID, plan); err != nil {
		return fail(c, planError(err))
	}
	if len(plan.Waves) == 0 {
		for _, n := range plan.Left {
			if n.Reason == LeftBusy {
				return fail(c, busyNode(n.ID, n.RunID))
			}
		}
		return response.Fail(c, http.StatusBadRequest, response.ErrTaskNoTargets, "The run would take no node")
	}

	inputs := runInputs(rt.task, req.Inputs)
	stopOnFailure := rt.task.StopOnFailure
	if req.StopOnFailure != nil {
		stopOnFailure = *req.StopOnFailure
	}
	run := cluster.RunRecord{
		TaskID: rt.doc.ID, TaskName: rt.task.Name, Inputs: inputs,
		Nodes: plan.NodeIDs(), Waves: plan.WaveIDs(), Confirmed: confirmed,
		Batch: rt.batch, StopOnFailure: stopOnFailure, Status: cluster.RunRunning, StartedBy: operator(c),
	}
	if err := h.claim(env, &run, rt.doc.YAML); err != nil {
		return fail(c, err)
	}
	h.begin(run)
	return response.Accepted(c, map[string]string{"run_id": run.ID})
}

// claim judges and records run under the start gate, with doc the document
// it runs: refused while a cluster update, a CA rotation, a conversion or a
// promotion is under way, or while another run holds one of its nodes — read
// again here, as two requests for one node may both have planned before
// either was recorded. It gives the run its ID, revision and start time, and
// writes its head alone: one write, so a run is recorded whole or not at all,
// and a failed write leaves nothing a leader would take up. A node has no
// record until its turn comes (the engine writes it as the node starts);
// until then it is pending, as GetRun and the stream show it (withPending).
func (h *Handler) claim(env runEnv, run *cluster.RunRecord, doc string) error {
	st := env.Tasks()
	return h.startGate(func(b featurecluster.Busy) error {
		switch {
		case b.UpdateRunning:
			return refusal(http.StatusConflict, response.ErrUpdateInProgress, "A cluster update is under way", nil)
		case b.CARotation:
			return refusal(http.StatusConflict, response.ErrClusterCARotating, cluster.ErrCARotationInProgress.Error(), nil)
		case b.Converting:
			return refusal(http.StatusConflict, response.ErrTaskClusterChanging, "A manager is being converted to an agent", map[string]string{"what": "conversion"})
		case b.Promoting:
			return refusal(http.StatusConflict, response.ErrTaskClusterChanging, "An agent is being promoted to a manager", map[string]string{"what": "promotion"})
		}
		held, err := heldNodes(st)
		if err != nil {
			return err
		}
		for _, id := range run.Nodes {
			if other := held[id]; other != "" {
				return busyNode(id, other)
			}
		}
		if other, err := rebootBusy(env, doc, run.Nodes); err != nil {
			return err
		} else if other != "" {
			return refusal(http.StatusConflict, response.ErrTaskRebootBusy,
				"Run "+other+" reboots a manager and is under way: one run at a time reboots managers", map[string]string{"run_id": other})
		}
		rev, err := st.PutTaskRevision(doc)
		if err != nil {
			return storeError(err)
		}
		now := env.Now().UTC()
		run.ID, run.Rev, run.StartedAt = cluster.NewRunID(now), rev, now
		return st.PutRun(*run)
	})
}

// rebootBusy names the run under way that, like a run of doc over nodes,
// reboots a manager, or "" when the run reboots no manager or no other does.
// Each run judges the managers' quorum from the managers online, so two runs
// rebooting managers at once could each see it kept and reboot one apiece;
// one at a time keeps a manager rebooting visible to the quorum check. A run
// that reboots agents only is not held back. Nodes are judged by their kind
// now.
func rebootBusy(env runEnv, doc string, nodes []string) (string, error) {
	managers := map[string]bool{}
	for _, n := range env.Nodes() {
		if n.IsManager() {
			managers[n.ID] = true
		}
	}
	if !rebootsManager(doc, nodes, managers) {
		return "", nil
	}
	st := env.Tasks()
	runs, err := st.ListRuns()
	if err != nil {
		return "", err
	}
	for _, r := range runs {
		if !r.Active() {
			continue
		}
		other, err := st.TaskRevision(r.Rev)
		if errors.Is(err, cluster.ErrTaskRevisionNotFound) {
			continue // the run cannot read its own document, and ends at once
		}
		if err != nil {
			return "", err
		}
		if rebootsManager(other, r.Nodes, managers) {
			return r.ID, nil
		}
	}
	return "", nil
}

// rebootsManager reports whether doc has a reboot step and nodes include a
// manager.
func rebootsManager(doc string, nodes []string, managers map[string]bool) bool {
	t, _ := tasks.Parse([]byte(doc))
	if t == nil || !slices.ContainsFunc(t.Steps, func(s tasks.Step) bool { return s.Kind == tasks.StepReboot }) {
		return false
	}
	return slices.ContainsFunc(nodes, func(id string) bool { return managers[id] })
}

// withPending is a run's node records with every node of the run that has
// none yet recorded pending, so a client sees every node from the start.
func withPending(run cluster.RunRecord, nodes map[string]cluster.RunNodeRecord) map[string]cluster.RunNodeRecord {
	if nodes == nil {
		nodes = map[string]cluster.RunNodeRecord{}
	}
	for _, id := range run.Nodes {
		if _, ok := nodes[id]; !ok {
			nodes[id] = cluster.RunNodeRecord{Status: cluster.RunNodePending}
		}
	}
	return nodes
}

// ListRuns answers GET /cluster/task-runs: every run's head, newest first,
// from this manager's own replicated state.
func (h *Handler) ListRuns(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	runs, err := env.Store().ListRuns()
	if err != nil {
		return fail(c, err)
	}
	if runs == nil {
		runs = []cluster.RunRecord{}
	}
	return response.OK(c, runs)
}

// runNotFound is 404 TASK_RUN_NOT_FOUND for cluster.ErrTaskRunNotFound.
func runNotFound(err error) error {
	if errors.Is(err, cluster.ErrTaskRunNotFound) {
		return refusal(http.StatusNotFound, response.ErrTaskRunNotFound, "No such task run", nil)
	}
	return err
}

// runStep is a step of the document a run ran, as a client labels it.
type runStep struct {
	Name string `json:"name,omitempty"`
	Kind string `json:"kind"`
}

// runSteps are the steps of the document the run ran, read from its
// revision: the task may since have changed or gone. Nil when the revision
// cannot be read or does not parse.
func runSteps(st taskStore, run cluster.RunRecord) []runStep {
	doc, err := st.TaskRevision(run.Rev)
	if err != nil {
		logger().Debug("run's revision not read", "run", run.ID, "rev", run.Rev, "error", err)
		return nil
	}
	t, problems := tasks.Parse([]byte(doc))
	if t == nil {
		logger().Debug("run's revision does not parse", "run", run.ID, "rev", run.Rev, "problems", len(problems))
		return nil
	}
	steps := make([]runStep, 0, len(t.Steps))
	for _, s := range t.Steps {
		steps = append(steps, runStep{Name: s.Name, Kind: s.Kind})
	}
	return steps
}

// GetRun answers GET /cluster/task-runs/:id: the run's head and its nodes'
// records, from this manager's own replicated state; a node not yet started
// is pending. steps names the steps of the document the run ran, and is
// left out when that revision is gone.
func (h *Handler) GetRun(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	run, nodes, err := env.Store().GetRun(c.Param("id"))
	if err != nil {
		return fail(c, runNotFound(err))
	}
	out := map[string]any{"run": run, "nodes": withPending(run, nodes)}
	if steps := runSteps(env.Tasks(), run); steps != nil {
		out["steps"] = steps
	}
	return response.OK(c, out)
}

// StopRun answers POST /cluster/task-runs/:id/stop {force} (leader): a run
// under way goes to stopping, by the operator; the engine starts no new
// step, and with force cancels the command jobs under way. A run already
// stopping is forced when asked; one that has ended is answered as it is.
// StoppedBy names who stopped it, or forced the stop: a forced cancel goes to
// the node under that name.
// The head is read and written under headMu, so a write the engine makes
// from an earlier read does not undo the stop.
func (h *Handler) StopRun(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	var body struct {
		Force bool `json:"force"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	st := env.Store()
	headMu.Lock()
	defer headMu.Unlock()
	head, _, err := st.GetRun(c.Param("id"))
	if err != nil {
		return fail(c, runNotFound(err))
	}
	changed := false
	switch head.Status {
	case cluster.RunRunning, cluster.RunHandingOver:
		head.Status, head.Force, head.Reason = cluster.RunStopping, body.Force, "stopped by "+operator(c)
		head.StoppedBy, changed = operator(c), true
	case cluster.RunStopping:
		if body.Force && !head.Force {
			head.Force, head.StoppedBy, changed = true, operator(c), true
		}
	}
	if changed {
		if err := st.PutRun(head); err != nil {
			return fail(c, err)
		}
	}
	return response.OK(c, head)
}

// RerunRun answers POST /cluster/task-runs/:id/rerun (leader): a new run of
// the run's own document, inputs and confirmations over the nodes chosen —
// with ?only=failed every node that did not succeed, else the body's
// {"nodes": [...]} that the run ran — each from its first step that did not
// succeed. The run must have ended. The nodes are ordered anew over the
// cluster as it is now (orderRun), as the leader may have moved and a node
// changed kind since, and the ones a new run would leave out are left out;
// the new run is judged and recorded as any run is (claim). 202 {run_id}.
func (h *Handler) RerunRun(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	st := env.Tasks()
	src, nodes, err := st.GetRun(c.Param("id"))
	if err != nil {
		return fail(c, runNotFound(err))
	}
	if src.Active() {
		return response.FailWith(c, http.StatusConflict, response.ErrTaskRunActive, "Run "+src.ID+" is still under way",
			map[string]string{"run_id": src.ID})
	}
	var only []string
	switch c.QueryParam("only") {
	case "failed":
		for _, id := range src.Nodes {
			if nodes[id].Status != cluster.RunNodeSucceeded {
				only = append(only, id)
			}
		}
	case "":
		var body struct {
			Nodes []string `json:"nodes"`
		}
		if err := c.Bind(&body); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
		}
		only = body.Nodes
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "only takes failed")
	}
	run, startAt := rerunFrom(src, nodes, only)
	if len(run.Nodes) == 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrTaskNoTargets, "None of the nodes chosen is one the run ran, or every one succeeded")
	}
	if err := managersNotReady(st); err != nil {
		return fail(c, err)
	}
	plan, _, err := orderRun(env, tasks.Targets{Nodes: run.Nodes}, run.Batch)
	if err != nil {
		return fail(c, planError(err))
	}
	if len(plan.Waves) == 0 {
		for _, n := range plan.Left {
			if n.Reason == LeftBusy {
				return fail(c, busyNode(n.ID, n.RunID))
			}
		}
		return response.Fail(c, http.StatusBadRequest, response.ErrTaskNoTargets, "The run would take no node: every node chosen is offline, outdated, out of reach or gone")
	}
	run.Nodes, run.Waves, run.StartAt = plan.NodeIDs(), plan.WaveIDs(), map[string]int{}
	for _, id := range run.Nodes {
		run.StartAt[id] = startAt[id]
	}
	doc, err := st.TaskRevision(src.Rev)
	if err != nil {
		return fail(c, fmt.Errorf("the run's document: %w", err))
	}
	run.StartedBy = operator(c)
	if err := h.claim(env, &run, doc); err != nil {
		return fail(c, err)
	}
	h.begin(run)
	return response.Accepted(c, map[string]string{"run_id": run.ID})
}

// StepOutput answers GET /cluster/task-runs/:id/nodes/:node/steps/:n/output
// ?skip=: the step's job as its node has it, output from byte skip on. The
// output stays on the node; this manager asks it for the job the run
// recorded. A step no job has run yet is 404 TASK_JOB_NOT_FOUND; the node's
// refusal comes back as it gave it, but for a 401, which is 502
// CLUSTER_AGENT_AUTH_FAILED from an agent and 502 INTERNAL_ERROR from a
// manager; a node not reached is 502 CLUSTER_NODE_UNREACHABLE.
func (h *Handler) StepOutput(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	_, nodes, err := env.Store().GetRun(c.Param("id"))
	if err != nil {
		return fail(c, runNotFound(err))
	}
	nodeID := c.Param("node")
	step, err := strconv.Atoi(c.Param("n"))
	if err != nil || step < 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid step")
	}
	skip := int64(0)
	if s := c.QueryParam("skip"); s != "" {
		if skip, err = strconv.ParseInt(s, 10, 64); err != nil || skip < 0 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid skip")
		}
	}
	rec, ok := nodes[nodeID]
	if !ok || step >= len(rec.Steps) || rec.Steps[step].JobID == "" {
		return response.Fail(c, http.StatusNotFound, response.ErrTaskJobNotFound, "No job has run this step on this node")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), callTimeout)
	defer cancel()
	view, err := env.GetJob(ctx, nodeID, rec.Steps[step].JobID, skip)
	if err == nil {
		return response.OK(c, view)
	}
	var ref *jobRefusal
	name, isAgent := nodeID, false
	for _, n := range env.Nodes() {
		if n.ID == nodeID {
			isAgent = !n.IsManager()
			if n.Name != "" {
				name = n.Name
			}
		}
	}
	switch {
	case errors.As(err, &ref) && ref.Status == http.StatusUnauthorized:
		// The node refused this manager's signed read: its trouble, not the
		// operator's session here ending, which is how the browser reads a
		// 401, logging them out. An agent's is answered as a ?node= relay
		// answers it (proxyToNodeGRPC).
		if isAgent {
			return response.FailWith(c, http.StatusBadGateway, response.ErrClusterAgentAuthFailed,
				"the agent refused the request as unauthenticated", map[string]string{"name": name})
		}
		return response.FailWith(c, http.StatusBadGateway, response.ErrInternalError,
			name+" refused the request as unauthenticated", map[string]string{"name": name})
	case errors.As(err, &ref):
		return response.FailWith(c, ref.Status, ref.Code, ref.Message, ref.Details)
	}
	return response.FailWith(c, http.StatusBadGateway, response.ErrClusterNodeUnreachable,
		name+" did not answer: "+response.SanitizeOutput(err.Error()), map[string]string{"name": name})
}
