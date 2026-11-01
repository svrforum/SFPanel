package featuretasks

import (
	"context"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// superviseInterval is how often the supervisor looks at the runs in the
// cluster's state. A variable so tests can shorten it.
var superviseInterval = 5 * time.Second

// supervise keeps a runner for every run in progress while this node is the
// caught-up leader, and none while it is not. The run records are in Raft,
// so a new leader takes a run over on its first look: a run left handing
// over goes back to running, as the old leader handed the leadership over
// for its own steps and the new one runs them as any manager's. A node that
// is not the leader, or leads with an FSM still replaying, cancels its
// runners: what it would write is refused, and the leader runs them. It
// returns when ctx ends or the cluster manager stops, once its runners have
// returned. One supervisor runs per Handler.
func (h *Handler) supervise(ctx context.Context) {
	env := h.newEnv()
	if env == nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.runCtx = ctx
	h.mu.Unlock()
	defer func() {
		// A launch after the cancel sees it under h.mu and starts nothing,
		// so every runner's wg.Add comes before the Wait.
		cancel()
		h.mu.Lock()
		h.runCtx = nil
		h.mu.Unlock()
		h.wg.Wait()
	}()

	ticker := time.NewTicker(superviseInterval)
	defer ticker.Stop()
	for {
		h.superviseOnce(ctx, env)
		select {
		case <-ctx.Done():
			return
		case <-env.Done():
			logger.Info("task run supervisor stopped with the cluster manager")
			return
		case <-ticker.C:
		}
	}
}

// superviseOnce is one look: it stops this node's runners unless it is the
// caught-up leader, and otherwise starts one for each run in progress that
// has none.
func (h *Handler) superviseOnce(ctx context.Context, env runEnv) {
	if !env.CaughtUp() {
		if n := h.stopRunners(); n > 0 {
			logger.Info("this node no longer leads: its task runs are left to the leader", "runs", n)
		}
		return
	}
	st := env.Store()
	runs, err := st.ListRuns()
	if err != nil {
		logger.Warn("listing task runs", "error", err)
		return
	}
	for _, run := range runs {
		if !run.Active() || h.hasRunner(run.ID) {
			continue
		}
		if run.Status == cluster.RunHandingOver {
			if err := resumeHandedOver(st, &run); err != nil {
				logger.Warn("taking a handed-over task run back to running", "run", run.ID, "error", err)
				continue
			}
		}
		if h.launch(ctx, run) {
			logger.Info("task run taken over by this leader", "run", run.ID, "task", run.TaskName)
		}
	}
}

// resumeHandedOver puts a run handed over with the leadership back to
// running, from its latest record: a stop written since is kept.
func resumeHandedOver(st runStore, run *cluster.RunRecord) error {
	head, _, err := st.GetRun(run.ID)
	if err != nil {
		return err
	}
	if head.Status == cluster.RunHandingOver {
		head.Status = cluster.RunRunning
		if err := st.PutRun(head); err != nil {
			return err
		}
	}
	*run = head
	return nil
}

func (h *Handler) hasRunner(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.runners[id]
	return ok
}

// stopRunners cancels every runner on this node and returns how many there
// were. Each returns at its next check and leaves its run as recorded.
func (h *Handler) stopRunners() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, cancel := range h.runners {
		cancel()
	}
	return len(h.runners)
}
