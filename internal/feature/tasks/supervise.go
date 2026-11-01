package featuretasks

import (
	"context"
	"net"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
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
// returned. One supervisor runs per Handler. An agent install is no task:
// it is left to its session, or ended when this node has none for it
// (endLeftInstall); one that has ended with credentials kept for a session
// this node does not have has their time cleared (clearLeftCredentials).
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
			logger().Info("task run supervisor stopped with the cluster manager")
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
			logger().Info("this node no longer leads: its task runs are left to the leader", "runs", n)
		}
		return
	}
	st := env.Store()
	runs, err := st.ListRuns()
	if err != nil {
		logger().Warn("listing task runs", "error", err)
		return
	}
	for _, run := range runs {
		if !run.Active() && run.Kind == cluster.RunKindAgentInstall && !run.CredentialsUntil.IsZero() {
			if err := h.clearLeftCredentials(env, run.ID); err != nil {
				logger().Warn("clearing the credentials time of an agent install left by a leader change", "run", run.ID, "error", err)
			}
			continue
		}
		if !run.Active() || h.hasRunner(run.ID) {
			continue
		}
		if run.Kind == cluster.RunKindAgentInstall {
			if ir := h.installRuns(); ir != nil && ir.Owns(run.ID) {
				continue
			}
			if err := h.endLeftInstall(env, run.ID); err != nil {
				logger().Warn("ending an agent install left behind by a leader change", "run", run.ID, "error", err)
			}
			continue
		}
		if run.Status == cluster.RunHandingOver {
			if err := resumeHandedOver(st, &run); err != nil {
				logger().Warn("taking a handed-over task run back to running", "run", run.ID, "error", err)
				continue
			}
		}
		if h.launch(ctx, run) {
			logger().Info("task run taken over by this leader", "run", run.ID, "task", run.TaskName)
		}
	}
}

// resumeHandedOver puts a run handed over with the leadership back to
// running, from its latest record: a stop written since is kept, and one
// written while it does waits for it (headMu).
func resumeHandedOver(st runStore, run *cluster.RunRecord) error {
	headMu.Lock()
	defer headMu.Unlock()
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

// endLeftInstall ends install id, under way with no session on this node to
// run it: the leader changed or restarted, and the credentials and the
// token went with the session. Every row that had not ended fails for the
// leader change (AGENT_INSTALL_LEADER_CHANGED) — a row that registered keeps
// its NodeID, which names the node it became — and its step under way is
// unknown: what ran on the server under systemd-run may have gone on. A row
// whose install step had begun and that names no node is given the agent
// at its IP, if there is one: install.sh registers it inside its join,
// before the session could see it. Its wait for the operator ends with it. The run fails for the same reason, its
// credentials time cleared, and its end is audited as a run's is. It is
// read and written under headMu, as a stop is; one that has ended, or that
// a session on this node now owns, is left as it is.
func (h *Handler) endLeftInstall(env runEnv, id string) error {
	st := env.Store()
	headMu.Lock()
	defer headMu.Unlock()
	head, recs, err := st.GetRun(id)
	if err != nil {
		return err
	}
	if !head.Active() {
		return nil
	}
	if ir := h.installRuns(); ir != nil && ir.Owns(id) {
		return nil
	}
	now := env.Now().UTC()
	for _, key := range head.Nodes {
		rec, ok := recs[key]
		if ok && rec.Status != cluster.RunNodePending && rec.Status != cluster.RunNodeRunning {
			continue
		}
		if rec.NodeID == "" && installBegan(rec) {
			if a, ok := cluster.AgentAt(env.Nodes(), net.ParseIP(rec.IP)); ok {
				rec.NodeID = a.ID
			}
		}
		rec.Status, rec.Reason = cluster.RunNodeFailed, response.ErrAgentInstallLeaderChanged
		rec.Awaiting, rec.CheckURL = "", ""
		for i, s := range rec.Steps {
			if s.Status == cluster.RunStepStarting || s.Status == cluster.RunStepRunning {
				rec.Steps[i].Status, rec.Steps[i].EndedAt = cluster.RunStepUnknown, now
			}
		}
		if err := st.PutRunNode(id, key, rec); err != nil {
			return err
		}
	}
	head.Status, head.Reason, head.EndedAt = cluster.RunFailed, response.ErrAgentInstallLeaderChanged, now
	head.CredentialsUntil = time.Time{}
	if err := st.PutRun(head); err != nil {
		return err
	}
	logger().Warn("agent install ended: it was left behind by a leader change", "run", id)
	if err := st.PruneRuns(keepRuns); err != nil {
		logger().Warn("pruning task runs", "error", err)
	}
	h.auditEnd(env.LocalID(), head)
	return nil
}

// clearLeftCredentials clears the credentials time of install id, ended
// with credentials kept for a recheck or a revert, when no session on this
// node holds it: the credentials stay in the memory of the leader that ran
// the install, so after a leader change nothing here can use them, and the
// record must not say they last. It is read and written under headMu, as
// endLeftInstall's is; one a session on this node now holds is left to it.
func (h *Handler) clearLeftCredentials(env runEnv, id string) error {
	st := env.Store()
	headMu.Lock()
	defer headMu.Unlock()
	head, _, err := st.GetRun(id)
	if err != nil {
		return err
	}
	if head.Active() || head.CredentialsUntil.IsZero() {
		return nil
	}
	if ir := h.installRuns(); ir != nil && ir.Owns(id) {
		return nil
	}
	head.CredentialsUntil = time.Time{}
	if err := st.PutRun(head); err != nil {
		return err
	}
	logger().Info("agent install's credentials time cleared: its session is on another leader", "run", id)
	return nil
}

// installBegan reports whether an install row's install step (its third)
// had begun: a row still in its connection or pre-flight ran no install.
func installBegan(rec cluster.RunNodeRecord) bool {
	const installStep = 2
	if len(rec.Steps) <= installStep {
		return false
	}
	st := rec.Steps[installStep].Status
	return st != cluster.RunStepPending && st != cluster.RunStepSkipped
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
