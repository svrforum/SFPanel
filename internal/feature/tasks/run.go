package featuretasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

var logger = slog.With("component", "tasks")

// Variables so tests can shorten them.
var (
	// pollInterval is how often a running job is asked how it stands.
	pollInterval = 2 * time.Second
	// callTimeout bounds one call to a node: ProxyToNode sets none.
	callTimeout = 10 * time.Second
	// startAttempts is how many times a job is posted while its node does
	// not answer. The node keeps one job per ID, so posting again never
	// starts it twice.
	startAttempts = 3
	// lostAfter is how long a node may go without answering about a job,
	// other than a reboot, before the step's result counts as unknown.
	lostAfter = 5 * time.Minute
)

const (
	// keepRuns is how many runs the store keeps once one ends.
	keepRuns = 200
	// maxSummary caps a step's summary in the run record: a snapshot holds
	// up to 200 nodes of 50 steps.
	maxSummary = 200
	// statusOnly asks a node for a job's state and none of its output: the
	// node reads from min(skip, size). The output stays on the node.
	statusOnly = math.MaxInt64
)

// Reasons the run record gives.
const (
	reasonQuorum       = "quorum"
	reasonProxmoxHost  = "Proxmox host"
	reasonStopped      = "stopped by the operator"
	reasonEarlierFail  = "a node failed and the run stops on failure: no further nodes were started"
	reasonNotHandedOff = "leadership could not be handed over"
)

// errAbandoned ends a runner that can no longer write the run: its context
// ended, or this node lost the leadership. The run stays as recorded for the
// next leader to take over.
var errAbandoned = errors.New("the run was left for the next leader")

// jobID is the job one step of a run is on every node: the run and the step
// are enough, as each node's jobs are recorded apart.
func jobID(runID string, step int) string { return runID + "-" + strconv.Itoa(step) }

// nodeOutcome is how one node's part of a run ended.
type nodeOutcome int

const (
	nodeDone      nodeOutcome = iota // every step succeeded, was skipped, or failed with continue_on_error
	nodeFailed                       // a step failed, or the node could not be run
	nodeStopped                      // the operator stopped the run before the node finished
	nodeQuorum                       // a reboot would have cost the managers their quorum
	nodeAbandoned                    // errAbandoned
)

// runner is one run on this leader.
type runner struct {
	ctx   context.Context
	env   runEnv
	st    runStore
	run   *cluster.RunRecord
	task  *tasks.Task
	nodes map[string]cluster.Node

	mu   sync.Mutex
	recs map[string]cluster.RunNodeRecord

	headMu sync.Mutex
	head   cluster.RunRecord
	headAt time.Time
}

// runTask runs run on this leader, wave by wave, as the plan put them in
// run.Waves: the nodes of a wave at once, each node's steps one after
// another. Each step's job ID goes into the node's record before the job is
// started, so a leader that takes the run over asks the node for that job
// rather than start it again. A failed step stops its node unless it
// continues on error; with StopOnFailure a wave with a failed node is the
// last. A run being stopped starts no new step, and a forced stop cancels
// the command jobs under way. When the leader's own turn comes it hands the
// leadership over and returns; the next leader runs it as any manager. A run
// that ends writes how, and prunes old runs. It returns errAbandoned, with
// the run left as recorded, when its context ends or it loses the
// leadership.
func runTask(ctx context.Context, env runEnv, run *cluster.RunRecord) error {
	r := &runner{ctx: ctx, env: env, st: env.Store(), run: run, nodes: map[string]cluster.Node{}}
	head, recs, err := r.st.GetRun(run.ID)
	if err != nil {
		return fmt.Errorf("read run %s: %w", run.ID, err)
	}
	r.head, r.headAt, r.recs = head, env.Now(), recs
	if r.recs == nil {
		r.recs = map[string]cluster.RunNodeRecord{}
	}

	doc, err := r.st.TaskRevision(run.Rev)
	if err != nil {
		return r.finish(cluster.RunFailed, "the task's document could not be read: "+err.Error())
	}
	t, problems := tasks.Parse([]byte(doc))
	if t == nil {
		msg := "the task's document does not parse"
		if len(problems) > 0 {
			msg += fmt.Sprintf(": line %d: %s", problems[0].Line, problems[0].Message)
		}
		return r.finish(cluster.RunFailed, msg)
	}
	r.task = t
	for _, n := range env.Nodes() {
		r.nodes[n.ID] = n
	}

	failed := false
	for _, wave := range run.Waves {
		if ctx.Err() != nil || !env.IsLeader() {
			return errAbandoned
		}
		head, err := r.readHead(true)
		if err != nil {
			return errAbandoned
		}
		if head.Status == cluster.RunStopping {
			return r.finish(cluster.RunStopped, stopReason(head))
		}
		if failed && run.StopOnFailure {
			return r.finish(cluster.RunFailed, reasonEarlierFail)
		}

		var todo []string
		for _, id := range wave {
			switch r.record(id).Status {
			case cluster.RunNodeFailed, cluster.RunNodeUnknown:
				failed = true
			case cluster.RunNodeSucceeded, cluster.RunNodeSkipped, cluster.RunNodeStopped:
			default:
				todo = append(todo, id)
			}
		}
		if len(todo) == 0 {
			continue
		}
		if local := env.LocalID(); slices.Contains(todo, local) {
			handed, err := r.handOver()
			if err != nil {
				return errAbandoned
			}
			if handed {
				return nil
			}
			if r.record(local).Status == cluster.RunNodeFailed {
				failed = true
				todo = slices.DeleteFunc(todo, func(id string) bool { return id == local })
			}
		}

		outcomes := make([]nodeOutcome, len(todo))
		var wg sync.WaitGroup
		for i, id := range todo {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes[i] = r.runNode(id)
			}()
		}
		wg.Wait()
		quorum := ""
		for i, o := range outcomes {
			switch o {
			case nodeAbandoned:
				return errAbandoned
			case nodeFailed:
				failed = true
			case nodeQuorum:
				quorum = todo[i]
			}
		}
		if quorum != "" {
			return r.finish(cluster.RunStopped, fmt.Sprintf("%s: rebooting %s would leave the managers without a quorum", reasonQuorum, r.name(quorum)))
		}
	}
	if head, err := r.readHead(true); err == nil && head.Status == cluster.RunStopping {
		return r.finish(cluster.RunStopped, stopReason(head))
	}
	if failed {
		return r.finish(cluster.RunFailed, "")
	}
	return r.finish(cluster.RunSucceeded, "")
}

func stopReason(head cluster.RunRecord) string {
	if head.Reason != "" {
		return head.Reason
	}
	return reasonStopped
}

func (r *runner) name(id string) string {
	if n, ok := r.nodes[id]; ok && n.Name != "" {
		return n.Name
	}
	return id
}

// readHead is the run's head as recorded. A fresh read goes to the store;
// otherwise one read within the poll interval is reused, so the nodes of a
// wave polling at once do not each read the store.
func (r *runner) readHead(fresh bool) (cluster.RunRecord, error) {
	r.headMu.Lock()
	defer r.headMu.Unlock()
	now := r.env.Now()
	if !fresh && now.Sub(r.headAt) < pollInterval {
		return r.head, nil
	}
	head, _, err := r.st.GetRun(r.run.ID)
	if err != nil {
		return cluster.RunRecord{}, err
	}
	r.head, r.headAt = head, now
	return head, nil
}

// putHead writes the run's head with change applied to its latest record.
func (r *runner) putHead(change func(h *cluster.RunRecord)) error {
	head, err := r.readHead(true)
	if err != nil {
		return err
	}
	change(&head)
	if err := r.st.PutRun(head); err != nil {
		return err
	}
	r.headMu.Lock()
	r.head = head
	r.headMu.Unlock()
	return nil
}

// handOver is the leader's turn: it marks the run handing over and hands the
// leadership to another manager voter, which takes the run over. With none
// to take it the run goes back to running and the leader runs its own
// steps; one that could not be handed to fails the leader's part of the run,
// as running steps that may restart the leader would leave the managers
// without one.
func (r *runner) handOver() (handed bool, err error) {
	if err := r.putHead(func(h *cluster.RunRecord) {
		if h.Status == cluster.RunRunning {
			h.Status = cluster.RunHandingOver
		}
	}); err != nil {
		return false, err
	}
	if head, _ := r.readHead(false); head.Status != cluster.RunHandingOver {
		return false, nil // being stopped: the node's own steps see it
	}
	target, herr := r.env.HandOver()
	if target != "" && herr == nil {
		logger.Info("task run handed over with the leadership", "run", r.run.ID, "to", target)
		return true, nil
	}
	if err := r.putHead(func(h *cluster.RunRecord) {
		if h.Status == cluster.RunHandingOver {
			h.Status = cluster.RunRunning
		}
	}); err != nil {
		return false, err
	}
	if herr != nil {
		local := r.env.LocalID()
		rec := r.newRecord(local)
		rec.Status, rec.Reason = cluster.RunNodeFailed, reasonNotHandedOff+" to "+r.name(target)+": "+herr.Error()
		if err := r.write(local, rec); err != nil {
			return false, err
		}
		logger.Warn("task run could not hand the leadership over", "run", r.run.ID, "to", target, "error", herr)
	}
	return false, nil
}

// record is node id's latest record.
func (r *runner) record(id string) cluster.RunNodeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recs[id]
}

// newRecord is node id's latest record with a step for every step of the
// task.
func (r *runner) newRecord(id string) cluster.RunNodeRecord {
	rec := r.record(id)
	steps := make([]cluster.RunStepRecord, len(r.task.Steps))
	copy(steps, rec.Steps)
	for i := range steps {
		if steps[i].Status == "" {
			steps[i].Status = cluster.RunStepPending
		}
	}
	rec.Steps = steps
	return rec
}

// write records node id's part of the run.
func (r *runner) write(id string, rec cluster.RunNodeRecord) error {
	rec.Steps = slices.Clone(rec.Steps)
	r.mu.Lock()
	r.recs[id] = rec
	r.mu.Unlock()
	return r.st.PutRunNode(r.run.ID, id, rec)
}

// fail ends node id's part of the run with why.
func (r *runner) fail(id string, rec cluster.RunNodeRecord, status, reason string) nodeOutcome {
	rec.Status, rec.Reason = status, reason
	if err := r.write(id, rec); err != nil {
		return nodeAbandoned
	}
	switch status {
	case cluster.RunNodeStopped:
		return nodeStopped
	case cluster.RunNodeSkipped:
		return nodeQuorum
	}
	return nodeFailed
}

// runNode runs node id's steps in order, from the first that has not ended.
func (r *runner) runNode(id string) nodeOutcome {
	rec := r.newRecord(id)
	n, ok := r.nodes[id]
	switch {
	case !ok:
		return r.fail(id, rec, cluster.RunNodeFailed, "the node is no longer in the cluster")
	case id != r.env.LocalID() && r.env.Health()[id] != cluster.StatusOnline:
		return r.fail(id, rec, cluster.RunNodeFailed, "the node was offline when its turn came")
	case r.env.Unreachable(id):
		return r.fail(id, rec, cluster.RunNodeFailed, "this leader does not reach the node")
	}
	specs, err := tasks.Render(r.task, r.run.Inputs, nodeVars(n))
	if err != nil {
		return r.fail(id, rec, cluster.RunNodeFailed, err.Error())
	}
	rec.Status, rec.Reason = cluster.RunNodeRunning, ""
	if err := r.write(id, rec); err != nil {
		return nodeAbandoned
	}

	for i, spec := range specs {
		switch rec.Steps[i].Status {
		case cluster.RunStepPending, cluster.RunStepStarting, cluster.RunStepRunning:
		default:
			continue // ended before
		}
		if r.ctx.Err() != nil {
			return nodeAbandoned
		}
		head, err := r.readHead(true)
		if err != nil {
			return nodeAbandoned
		}
		if head.Status == cluster.RunStopping {
			return r.fail(id, rec, cluster.RunNodeStopped, stopReason(head))
		}
		spec.ID, spec.RunID = jobID(r.run.ID, i), r.run.ID
		spec.Confirmed = slices.Contains(r.run.Confirmed, i)

		if spec.Kind == tasks.StepReboot && n.IsManager() && !r.quorumWithout(id) {
			rec.Steps[i] = cluster.RunStepRecord{Status: cluster.RunStepSkipped, Summary: reasonQuorum, EndedAt: r.env.Now().UTC()}
			logger.Warn("task run skipped a manager's reboot: quorum", "run", r.run.ID, "node", id)
			return r.fail(id, rec, cluster.RunNodeSkipped, reasonQuorum)
		}

		res := r.runStep(id, &rec, i, spec)
		switch res {
		case stepAbandoned:
			return nodeAbandoned
		case stepDone:
			continue
		case stepBusy:
			return r.fail(id, rec, cluster.RunNodeFailed, rec.Reason)
		case stepCancelled:
			return r.fail(id, rec, cluster.RunNodeStopped, reasonStopped)
		case stepUnknown:
			return r.fail(id, rec, cluster.RunNodeUnknown, fmt.Sprintf("step %d: %s", i+1, rec.Steps[i].Summary))
		case stepFailed:
			if r.task.Steps[i].ContinueOnError {
				continue
			}
			return r.fail(id, rec, cluster.RunNodeFailed, fmt.Sprintf("step %d (%s) failed", i+1, stepName(r.task.Steps[i])))
		}
	}
	rec.Status, rec.Reason = cluster.RunNodeSucceeded, ""
	if err := r.write(id, rec); err != nil {
		return nodeAbandoned
	}
	return nodeDone
}

func stepName(s tasks.Step) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Kind
}

// quorumWithout reports whether the manager voters online by live health,
// node id left out, are still a majority of all manager voters.
func (r *runner) quorumWithout(id string) bool {
	health := r.env.Health()
	voters, online := 0, 0
	for _, n := range r.env.Nodes() {
		if !n.IsManager() || n.Role != cluster.RoleVoter {
			continue
		}
		voters++
		if n.ID != id && health[n.ID] == cluster.StatusOnline {
			online++
		}
	}
	return online >= voters/2+1
}

// stepResult is how one step ended.
type stepResult int

const (
	stepDone      stepResult = iota // succeeded, or skipped by the node's own rule
	stepFailed                      // refused, or the job failed
	stepBusy                        // the node is running another run's job
	stepCancelled                   // a forced stop cancelled the job
	stepUnknown                     // the node no longer says how the job ended
	stepAbandoned                   // errAbandoned
)

// runStep runs step i on node id: it records the step starting with its job
// ID, starts the job, and asks how it stands every pollInterval until it
// ends.
func (r *runner) runStep(id string, rec *cluster.RunNodeRecord, i int, spec tasks.JobSpec) stepResult {
	step := &rec.Steps[i]
	*step = cluster.RunStepRecord{Status: cluster.RunStepStarting, JobID: spec.ID, StartedAt: r.env.Now().UTC()}
	// Recorded before the job is posted: a leader that takes over after the
	// post asks for this ID, and the node has one job per ID.
	if err := r.write(id, *rec); err != nil {
		return stepAbandoned
	}
	end := func(status, summary string, changed bool) {
		step.Status, step.Summary, step.Changed, step.EndedAt = status, capSummary(summary), changed, r.env.Now().UTC()
	}

	view, err := r.start(id, spec)
	if err != nil {
		if r.ctx.Err() != nil {
			return stepAbandoned
		}
		res := stepFailed
		var ref *jobRefusal
		switch {
		case errors.As(err, &ref) && ref.Code == response.ErrTaskRebootPVEHost && spec.Kind == tasks.StepReboot:
			// The design reboots no Proxmox host: the VMs on it stop too.
			end(cluster.RunStepSkipped, reasonProxmoxHost, false)
			res = stepDone
		case errors.As(err, &ref) && ref.Code == response.ErrTaskNodeBusy:
			end(cluster.RunStepFailed, err.Error(), false)
			rec.Reason = fmt.Sprintf("busy with job %s of run %s (%s)", ref.Details["job_id"], ref.Details["run_id"], ref.Details["task"])
			res = stepBusy
		case ref != nil:
			end(cluster.RunStepFailed, err.Error(), false)
		default:
			// The node did not answer: the job may have started.
			end(cluster.RunStepUnknown, "the node did not answer: "+err.Error(), false)
			res = stepUnknown
		}
		if err := r.write(id, *rec); err != nil {
			return stepAbandoned
		}
		return res
	}

	step.Status = cluster.RunStepRunning
	if err := r.write(id, *rec); err != nil {
		return stepAbandoned
	}
	view, res := r.wait(id, spec, view, step.StartedAt)
	if res == stepAbandoned {
		return stepAbandoned
	}
	if res == stepUnknown {
		end(cluster.RunStepUnknown, view.Summary, false)
	} else {
		switch view.Status {
		case tasks.JobSucceeded:
			end(cluster.RunStepSucceeded, view.Summary, view.Changed)
		case tasks.JobFailed:
			end(cluster.RunStepFailed, failSummary(view), view.Changed)
			res = stepFailed
		case tasks.JobCancelled:
			end(cluster.RunStepCancelled, view.Summary, view.Changed)
			res = stepCancelled
		default: // interrupted, or a status this leader does not know
			end(cluster.RunStepUnknown, "the job's result is not known ("+view.Status+")", view.Changed)
			res = stepUnknown
		}
	}
	if err := r.write(id, *rec); err != nil {
		return stepAbandoned
	}
	return res
}

// start posts the job, again while the node does not answer: the node keeps
// one job per ID, so a post that arrived is not run twice.
func (r *runner) start(id string, spec tasks.JobSpec) (tasks.JobView, error) {
	var err error
	for attempt := 0; attempt < startAttempts; attempt++ {
		if attempt > 0 {
			r.env.Sleep(pollInterval)
		}
		if r.ctx.Err() != nil {
			return tasks.JobView{}, r.ctx.Err()
		}
		ctx, cancel := context.WithTimeout(r.ctx, callTimeout)
		var v tasks.JobView
		v, err = r.env.StartJob(ctx, id, spec, r.run.StartedBy)
		cancel()
		var ref *jobRefusal
		if err == nil || errors.As(err, &ref) {
			return v, err
		}
	}
	return tasks.JobView{}, err
}

// active reports whether a job is still under way on its node.
func active(status string) bool {
	return status == tasks.JobRunning || status == tasks.JobRebooting
}

// cancellable reports whether a forced stop cancels a job of kind: a command
// (its unit is stopped) and a reboot not yet under way (its timer is). A
// package, service, file or stack job is left to end, as cutting apt short
// can leave dpkg broken.
func cancellable(kind string) bool {
	return kind == tasks.StepCommand || kind == tasks.StepReboot
}

// wait asks node id how job spec stands until it ends. A reboot is waited
// for until its wait runs out, through the node not answering while it
// boots; any other job until the node has not answered for lostAfter. A run
// stopped by force cancels a command job, once.
func (r *runner) wait(id string, spec tasks.JobSpec, view tasks.JobView, started time.Time) (tasks.JobView, stepResult) {
	var rebootBy time.Time
	if spec.Kind == tasks.StepReboot {
		wait := tasks.DefaultRebootWait
		if spec.Reboot != nil && spec.Reboot.Wait > 0 {
			wait = spec.Reboot.Wait
		}
		rebootBy = started.Add(wait)
	}
	lastHeard := r.env.Now()
	cancelled := false
	for active(view.Status) {
		if r.ctx.Err() != nil {
			return view, stepAbandoned
		}
		r.env.Sleep(pollInterval)
		if !cancelled && cancellable(spec.Kind) {
			if head, err := r.readHead(false); err == nil && head.Status == cluster.RunStopping && head.Force {
				ctx, cancel := context.WithTimeout(r.ctx, callTimeout)
				err := r.env.CancelJob(ctx, id, spec.ID, true)
				cancel()
				if err == nil {
					cancelled = true
					logger.Info("task run cancelled a job: forced stop", "run", r.run.ID, "node", id, "job", spec.ID)
				}
			}
		}
		ctx, cancel := context.WithTimeout(r.ctx, callTimeout)
		v, err := r.env.GetJob(ctx, id, spec.ID, statusOnly)
		cancel()
		now := r.env.Now()
		if err == nil {
			view, lastHeard = v, now
			if spec.Kind == tasks.StepReboot && view.Status == tasks.JobRebooting && !now.Before(rebootBy) {
				return failedView(view, fmt.Sprintf("did not come back within %v", rebootBy.Sub(started))), stepDone
			}
			continue
		}
		if r.ctx.Err() != nil {
			return view, stepAbandoned
		}
		if refusalCode(err) == response.ErrTaskJobNotFound {
			return tasks.JobView{Summary: "the node has no record of the job"}, stepUnknown
		}
		if spec.Kind == tasks.StepReboot {
			if !now.Before(rebootBy) {
				return failedView(view, fmt.Sprintf("did not come back within %v", rebootBy.Sub(started))), stepDone
			}
			continue // booting
		}
		if now.Sub(lastHeard) >= lostAfter {
			return tasks.JobView{Summary: "the node stopped answering: " + err.Error()}, stepUnknown
		}
	}
	return view, stepDone
}

// failedView is view ended failed with summary.
func failedView(view tasks.JobView, summary string) tasks.JobView {
	view.Status, view.Summary = tasks.JobFailed, summary
	return view
}

// failSummary is what a failed job's step says: the node's summary, else its
// exit code.
func failSummary(v tasks.JobView) string {
	if v.Summary != "" {
		return v.Summary
	}
	if v.ExitCode != nil {
		return fmt.Sprintf("exit code %d", *v.ExitCode)
	}
	return "failed"
}

// capSummary cuts s to maxSummary bytes, at a character boundary.
func capSummary(s string) string {
	if len(s) <= maxSummary {
		return s
	}
	cut := maxSummary
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// finish ends the run: every node not yet started is recorded stopped, with
// why, and the head with status, reason and the time; then old runs are
// pruned.
func (r *runner) finish(status, reason string) error {
	nodeReason := reason
	if nodeReason == "" {
		nodeReason = "the run ended before the node's turn"
	}
	for _, id := range r.run.Nodes {
		rec := r.record(id)
		if rec.Status != "" && rec.Status != cluster.RunNodePending {
			continue
		}
		if r.task != nil {
			rec = r.newRecord(id)
		}
		rec.Status, rec.Reason = cluster.RunNodeStopped, nodeReason
		if err := r.write(id, rec); err != nil {
			return errAbandoned
		}
	}
	if err := r.putHead(func(h *cluster.RunRecord) {
		h.Status, h.Reason, h.EndedAt = status, reason, r.env.Now().UTC()
	}); err != nil {
		return errAbandoned
	}
	logger.Info("task run ended", "run", r.run.ID, "task", r.run.TaskName, "status", status, "reason", reason)
	if err := r.st.PruneRuns(keepRuns); err != nil {
		logger.Warn("pruning task runs", "error", err)
	}
	return nil
}
