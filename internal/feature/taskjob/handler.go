// Package taskjob is a node's half of a group task. The leader's engine
// starts one step of a run here as a job, asks after it and reads its output;
// what kind of step it is decides which Runner does the work.
//
// Every route takes signed cluster calls only (auth.IsInternalProxyRequest),
// and a manager will not relay one for a browser with ?node=
// (middleware.ClusterProxyMiddleware): a step started around the engine would
// skip its gates and its audit. The routes exist on managers and agents alike.
//
// Per node, never replicated: a node runs one job at a time — a job running or
// waiting on its reboot holds it, across a restart too, because the record in
// task_jobs is what holds it — and a job ID is started once, so a leader that
// sends the same ID again, or its successor, gets the job that is there.
package taskjob

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// DefaultLogDir keeps each job's output, <id>.log, beside the apt
// transactions' logs.
const DefaultLogDir = "/var/log/sfpanel/task-jobs"

// idPattern is a job ID: the run ID (UTC time and six hex digits), then the
// step's index, counted from 0.
var idPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}-[0-9]{1,2}$`)

var logger = slog.With("component", "taskjob")

// Runner does one kind of step.
type Runner interface {
	// Start runs job, or hands it to something that outlives the call — a
	// systemd unit, a scheduled reboot — and returns. The job is over when
	// the runner calls job.Finish; an error from Start finishes it failed.
	// ctx ends when the job is cancelled with force.
	Start(ctx context.Context, job *Job) error
	// Check is the cheap verdict on a step before a run: the guards that
	// need nothing run, as the endpoints for the same thing apply them.
	Check(spec tasks.JobSpec) tasks.CheckVerdict
}

// Handler serves /api/v1/task-jobs.
type Handler struct {
	DB  *sql.DB
	Cmd exec.Commander
	// LogDir holds each job's output, <id>.log.
	LogDir string
	// Platform says what kind of host this is, for runners whose guards
	// differ on Proxmox VE.
	Platform func() platform.Info

	now     func() time.Time
	bootID  func() string
	runners map[string]Runner

	// startMu makes "is the node free, is the ID new, record it" one step.
	startMu sync.Mutex
	// cancels ends the context of each job this process started.
	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

// New returns a Handler for the step kinds this node runs.
func New(db *sql.DB, cmd exec.Commander, logDir string, platformInfo func() platform.Info) *Handler {
	return &Handler{
		DB:       db,
		Cmd:      cmd,
		LogDir:   logDir,
		Platform: platformInfo,
		now:      time.Now,
		bootID:   readBootID,
		runners:  map[string]Runner{},
		cancels:  map[string]context.CancelFunc{},
	}
}

// readBootID is the kernel's ID for this boot, which a reboot changes.
func readBootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// refuseNotCluster answers a request that is not a signed cluster call.
// Checking for one (auth.IsInternalProxyRequest) is the first thing every
// handler does: the check records the request's nonce, and a second check of
// the same request passes only within 50 ms of the first, which the
// middleware before this one already spent some of.
func refuseNotCluster(c echo.Context) error {
	return response.Fail(c, http.StatusForbidden, response.ErrTaskJobClusterOnly,
		"Task jobs are started by the cluster's task engine only")
}

// StartJob starts a job: POST /task-jobs with a tasks.JobSpec. 201 and the
// job for a new ID; 200 and the job that is there for an ID the node has;
// 409 TASK_NODE_BUSY while another job holds the node. The runner goes on
// after the answer.
func (h *Handler) StartJob(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return refuseNotCluster(c)
	}
	var spec tasks.JobSpec
	if err := c.Bind(&spec); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid job")
	}
	runner, msg := h.validate(spec)
	if msg != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, msg)
	}
	username, _ := c.Get("username").(string)

	h.startMu.Lock()
	rec, err := h.get(spec.ID)
	if err == nil {
		h.startMu.Unlock()
		return response.OK(c, h.view(rec, 0, false))
	}
	if !errors.Is(err, errNotFound) {
		h.startMu.Unlock()
		logger.Error("reading a job", "id", spec.ID, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Could not read the node's jobs")
	}
	if holder, err := h.active(); err != nil {
		h.startMu.Unlock()
		logger.Error("reading the node's running job", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Could not read the node's jobs")
	} else if holder != nil {
		h.startMu.Unlock()
		return response.FailWith(c, http.StatusConflict, response.ErrTaskNodeBusy,
			"This node is running another task job", map[string]string{
				"job_id": holder.spec.ID, "run_id": holder.spec.RunID, "task": holder.spec.Task,
			})
	}
	rec = record{spec: spec, status: tasks.JobRunning, bootID: h.bootID(), username: username, startedAt: h.now().UTC()}
	if err := h.createLog(spec.ID); err != nil {
		h.startMu.Unlock()
		logger.Error("creating a job's log", "id", spec.ID, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Could not create the job's log")
	}
	if err := h.insert(rec); err != nil {
		h.startMu.Unlock()
		logger.Error("recording a job", "id", spec.ID, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Could not record the job")
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancelMu.Lock()
	h.cancels[spec.ID] = cancel
	h.cancelMu.Unlock()
	h.startMu.Unlock()

	if err := h.sweep(); err != nil {
		logger.Warn("sweeping old jobs", "error", err)
	}
	logger.Info("task job started", "id", spec.ID, "task", spec.Task, "kind", spec.Kind, "user", username)
	job := &Job{Spec: spec, LogPath: h.logPath(spec.ID), StartedAt: rec.startedAt, h: h}
	go func() {
		if err := runner.Start(ctx, job); err != nil {
			logger.Warn("task job did not start", "id", spec.ID, "error", err)
			if ferr := job.Finish(Result{Status: tasks.JobFailed, Summary: response.SanitizeOutput(err.Error())}); ferr != nil {
				logger.Error("recording a job's end", "id", spec.ID, "error", ferr)
			}
		}
	}()
	return c.JSON(http.StatusCreated, response.Response{Success: true, Data: h.view(rec, 0, false)})
}

// validate checks a spec before anything is recorded and returns the runner
// for its kind, or what is wrong with it.
func (h *Handler) validate(s tasks.JobSpec) (Runner, string) {
	if !idPattern.MatchString(s.ID) {
		return nil, "Invalid job ID"
	}
	if s.ID != s.RunID+"-"+strconv.Itoa(s.Step) {
		return nil, "The job ID does not name its run and step"
	}
	if s.Step < 0 || s.Step >= tasks.MaxSteps {
		return nil, "Invalid step"
	}
	if strings.TrimSpace(s.Task) == "" {
		return nil, "The job names no task"
	}
	runner := h.runners[s.Kind]
	if runner == nil {
		return nil, "This node does not run " + strconv.Quote(s.Kind) + " steps"
	}
	if msg := checkSettings(s); msg != "" {
		return nil, msg
	}
	return runner, ""
}

// checkSettings says what is wrong with a spec's settings: exactly the one
// its kind names is set.
func checkSettings(s tasks.JobSpec) string {
	set := map[string]bool{
		tasks.StepPackages: s.Packages != nil,
		tasks.StepService:  s.Service != nil,
		tasks.StepCommand:  s.Command != nil,
		tasks.StepFile:     s.File != nil,
		tasks.StepStack:    s.Stack != nil,
		tasks.StepReboot:   s.Reboot != nil,
	}
	if !set[s.Kind] {
		return "The job has no " + s.Kind + " settings"
	}
	for kind, ok := range set {
		if ok && kind != s.Kind {
			return "A " + s.Kind + " job carries " + kind + " settings"
		}
	}
	return ""
}

// GetJob answers GET /task-jobs/:id?skip=<bytes>: the job's state and its
// output from skip on (see readOutput).
func (h *Handler) GetJob(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return refuseNotCluster(c)
	}
	rec, ok, err := h.lookup(c)
	if !ok {
		return err
	}
	skip := int64(0)
	if s := c.QueryParam("skip"); s != "" {
		v, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil || v < 0 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid skip")
		}
		skip = v
	}
	return response.OK(c, h.view(rec, skip, true))
}

// lookup finds the job :id names. Without one, ok is false and the answer
// saying why is written; err is what writing it returned.
func (h *Handler) lookup(c echo.Context) (rec record, ok bool, err error) {
	id := c.Param("id")
	if !idPattern.MatchString(id) {
		return record{}, false, response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid job ID")
	}
	rec, err = h.get(id)
	if errors.Is(err, errNotFound) {
		return record{}, false, response.Fail(c, http.StatusNotFound, response.ErrTaskJobNotFound, "No such task job on this node")
	}
	if err != nil {
		logger.Error("reading a job", "id", id, "error", err)
		return record{}, false, response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Could not read the node's jobs")
	}
	return rec, true, nil
}

// CancelJob answers POST /task-jobs/:id/cancel {force}. A job that is over is
// answered as it is. A running one is stopped only with force — its
// runner's context ends, and the runner says how the job ended — because by
// default a job runs to its end. 409 TASK_JOB_RUNNING without force.
func (h *Handler) CancelJob(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return refuseNotCluster(c)
	}
	var req struct {
		Force bool `json:"force"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request")
	}
	rec, ok, err := h.lookup(c)
	if !ok {
		return err
	}
	if !active(rec.status) {
		return response.OK(c, h.view(rec, 0, false))
	}
	if !req.Force {
		return response.Fail(c, http.StatusConflict, response.ErrTaskJobRunning,
			"The job is running; it is stopped only when forced")
	}
	h.cancelMu.Lock()
	cancel := h.cancels[rec.spec.ID]
	h.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	logger.Info("task job cancelled", "id", rec.spec.ID)
	return response.OK(c, h.view(rec, 0, false))
}

// CheckJobs answers POST /task-jobs/check with []tasks.JobSpec: each step's
// verdict from its kind's runner, in the order sent. A kind this node does
// not run is refused.
func (h *Handler) CheckJobs(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return refuseNotCluster(c)
	}
	var specs []tasks.JobSpec
	if err := c.Bind(&specs); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid steps")
	}
	if len(specs) > tasks.MaxSteps {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Too many steps")
	}
	verdicts := make([]tasks.CheckVerdict, 0, len(specs))
	for _, s := range specs {
		var v tasks.CheckVerdict
		if runner := h.runners[s.Kind]; runner == nil {
			v = tasks.CheckVerdict{Refused: true, Code: response.ErrInvalidRequest, Reason: "this node does not run " + strconv.Quote(s.Kind) + " steps"}
		} else if msg := checkSettings(s); msg != "" {
			v = tasks.CheckVerdict{Refused: true, Code: response.ErrInvalidRequest, Reason: msg}
		} else {
			v = runner.Check(s)
		}
		v.Step = s.Step
		verdicts = append(verdicts, v)
	}
	return response.OK(c, verdicts)
}
