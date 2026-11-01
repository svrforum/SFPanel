// Package featuretasks is the leader's half of group tasks: it plans a run
// of a task over its targets and runs it, node by node, through each node's
// task-job routes (internal/feature/taskjob). Definitions and run records
// live in the cluster's replicated state (internal/cluster/tasks_store.go),
// so any manager can serve them and a new leader takes a run over.
package featuretasks

import (
	"context"
	"net/http"
	"sync"

	"github.com/svrforum/SFPanel/internal/cluster"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/feature/audit"
	featurecluster "github.com/svrforum/SFPanel/internal/feature/cluster"
	"github.com/svrforum/SFPanel/internal/paneltls"
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

	// runners are the runs this node is running, by run ID; wg counts
	// their goroutines. runCtx is the supervisor's context while it runs,
	// which every runner lives under.
	mu      sync.Mutex
	runners map[string]context.CancelFunc
	wg      sync.WaitGroup
	runCtx  context.Context
}

// New returns a Handler.
func New(getMgr func() *cluster.Manager, gate *featurecluster.Handler, audit *sfdb.AsyncWriter, self paneltls.Self) *Handler {
	h := &Handler{getMgr: getMgr, gate: gate, audit: audit, self: self, runners: map[string]context.CancelFunc{}}
	h.newEnv = h.clusterEnv
	return h
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
			logger.Warn("task run left unfinished on this node", "run", run.ID, "error", err)
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
