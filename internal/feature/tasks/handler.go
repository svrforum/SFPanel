// Package featuretasks is the leader's half of group tasks: it plans a run
// of a task over its targets and runs it, node by node, through each node's
// task-job routes (internal/feature/taskjob). Definitions and run records
// live in the cluster's replicated state (internal/cluster/tasks_store.go),
// so any manager can serve them and a new leader takes a run over.
package featuretasks

import (
	"context"
	"sync"

	"github.com/svrforum/SFPanel/internal/cluster"
	sfdb "github.com/svrforum/SFPanel/internal/db"
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

	// runners are the runs this node is running, by run ID.
	mu      sync.Mutex
	runners map[string]context.CancelFunc
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
// ctx does. It reports whether it started a runner.
func (h *Handler) launch(ctx context.Context, run cluster.RunRecord) bool {
	env := h.newEnv()
	if env == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.runners[run.ID]; ok {
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	h.runners[run.ID] = cancel
	go func() {
		defer func() {
			cancel()
			h.mu.Lock()
			delete(h.runners, run.ID)
			h.mu.Unlock()
		}()
		if err := runTask(ctx, env, &run); err != nil {
			logger.Warn("task run left unfinished on this node", "run", run.ID, "error", err)
		}
	}()
	return true
}
