package featuretasks

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/cluster"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// shared is one cluster seen from its managers: the same fake nodes, the
// same replicated store, and one leadership the test moves between them.
// Each manager has its own Handler, as each runs its own panel.
type shared struct {
	*fakeEnv // the nodes and the store

	mu     sync.Mutex
	leader string
	posted []post
	// onPosted runs once a manager's post has reached the node; an error it
	// returns is the answer that manager gets instead of the node's.
	onPosted func(by string, spec tasks.JobSpec) error
}

// post is one job a manager posted to a node.
type post struct {
	by, node, job string
	step          int
}

func newShared(t *testing.T, leader string, nodes ...cluster.Node) *shared {
	return &shared{fakeEnv: newFakeEnv(t, leader, nodes...), leader: leader}
}

func (c *shared) setLeader(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leader = id
}

func (c *shared) leaderID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leader
}

// ran is how many times the node really ran step key ("node/step"): the
// fake node starts a job only for an ID it has not seen.
func (c *shared) ran(k string) int {
	n := 0
	for _, s := range c.startedList() {
		if s == k {
			n++
		}
	}
	return n
}

func (c *shared) postsBy(by, node string, step int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for _, p := range c.posted {
		if p.by == by && p.node == node && p.step == step {
			ids = append(ids, p.job)
		}
	}
	return ids
}

// handler is manager id's Handler.
func (c *shared) handler(id string) *Handler {
	v := managerView{shared: c, id: id}
	return &Handler{runners: map[string]context.CancelFunc{}, newEnv: func() runEnv { return v }}
}

// managerView is the cluster as manager id sees it: it leads only while the
// test says so, and the store refuses its writes while it does not, as Raft
// refuses a follower's.
type managerView struct {
	*shared
	id string
}

func (v managerView) LocalID() string { return v.id }
func (v managerView) IsLeader() bool  { return v.leaderID() == v.id }
func (v managerView) CaughtUp() bool  { return v.IsLeader() }
func (v managerView) Store() runStore { return viewStore(v) }

func (v managerView) StartJob(ctx context.Context, nodeID string, spec tasks.JobSpec, username string) (tasks.JobView, error) {
	view, err := v.fakeEnv.StartJob(ctx, nodeID, spec, username)
	v.mu.Lock()
	v.posted = append(v.posted, post{by: v.id, node: nodeID, job: spec.ID, step: spec.Step})
	hook := v.onPosted
	v.mu.Unlock()
	if err == nil && hook != nil {
		if herr := hook(v.id, spec); herr != nil {
			return tasks.JobView{}, herr
		}
	}
	return view, err
}

// viewStore is the store as one manager writes it.
type viewStore managerView

func (s viewStore) TaskRevision(sha string) (string, error) { return s.store.TaskRevision(sha) }
func (s viewStore) GetRun(id string) (cluster.RunRecord, map[string]cluster.RunNodeRecord, error) {
	return s.store.GetRun(id)
}
func (s viewStore) ListRuns() ([]cluster.RunRecord, error) { return s.store.ListRuns() }

func (s viewStore) PutRun(r cluster.RunRecord) error {
	if !managerView(s).IsLeader() {
		return cluster.ErrNotLeader
	}
	return s.store.PutRun(r)
}

func (s viewStore) PutRunNode(runID, nodeID string, rec cluster.RunNodeRecord) error {
	if !managerView(s).IsLeader() {
		return cluster.ErrNotLeader
	}
	return s.store.PutRunNode(runID, nodeID, rec)
}

func (s viewStore) PruneRuns(keep int) error {
	if !managerView(s).IsLeader() {
		return cluster.ErrNotLeader
	}
	return s.store.PruneRuns(keep)
}

// shortSupervise makes the supervisor look every few milliseconds.
func shortSupervise(t *testing.T) {
	old := superviseInterval
	superviseInterval = 5 * time.Millisecond
	t.Cleanup(func() { superviseInterval = old })
}

// supervising runs h's supervisor until stop is called or the test ends,
// and waits for it to return, its runners with it.
func supervising(t *testing.T, h *Handler) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.supervise(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the supervisor did not return once its context ended")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waited 5s for %s", what)
		}
	}
}

func (h *Handler) runnerCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.runners)
}

func runEnded(st *fakeStore, id string) func() bool {
	return func() bool { return !st.head(id).Active() && st.head(id).Status != "" }
}

// m1 posts the first step and loses the leadership before the node's answer
// reaches it: the step is recorded starting with its job ID, and the node
// has the job. m2 takes the run over, asks the node for that ID and follows
// the job rather than post it again.
func TestANewLeaderTakesOverARunningRun(t *testing.T) {
	shortSupervise(t)
	c := newShared(t, "m1", manager("m1", "mgr-1"), manager("m2", "mgr-2"), agent("a1", "web-1"))
	newRun(t, c.fakeEnv, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}})
	var lose sync.Once
	c.onPosted = func(by string, spec tasks.JobSpec) error {
		if by != "m1" {
			return nil
		}
		lose.Do(func() { c.setLeader("m2") })
		return errors.New("connection reset by peer")
	}
	supervising(t, c.handler("m1"))
	supervising(t, c.handler("m2"))
	waitFor(t, "the run to end", runEnded(c.store, testRunID))

	wantHead(t, c.fakeEnv, cluster.RunSucceeded, "")
	if n := c.ran("a1/0"); n != 1 {
		t.Errorf("a1 ran step 0 %d times, want once", n)
	}
	if n := c.ran("a1/1"); n != 1 {
		t.Errorf("a1 ran step 1 %d times, want once", n)
	}
	want := jobID(testRunID, 0)
	if ids := c.postsBy("m2", "a1", 1); len(ids) == 0 {
		t.Fatal("m2 never posted step 1: the run was not taken over")
	}
	if ids := c.postsBy("m2", "a1", 0); len(ids) != 0 {
		t.Errorf("m2 posted step 0 as %v, want it followed: the node has the job", ids)
	}
	rec := wantNode(t, c.fakeEnv, "a1", cluster.RunNodeSucceeded, "")
	wantStep(t, rec, 0, cluster.RunStepSucceeded, "")
	if rec.Steps[0].JobID != want {
		t.Errorf("step 0 is recorded as job %q, want %q", rec.Steps[0].JobID, want)
	}
}

// m1 loses the leadership while the job runs: the step is recorded running.
// m2 takes the run over and follows the job the node already has.
func TestATakeoverAfterTheJobStartedDoesNotStartItAgain(t *testing.T) {
	shortSupervise(t)
	c := newShared(t, "m1", manager("m1", "mgr-1"), manager("m2", "mgr-2"), agent("a1", "web-1"))
	c.polls[key("a1", 0)] = 3
	c.onPoll = func(node string, step, poll int) {
		if node == "a1" && step == 0 && poll == 1 {
			c.setLeader("m2")
		}
	}
	newRun(t, c.fakeEnv, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}})
	supervising(t, c.handler("m1"))
	supervising(t, c.handler("m2"))
	waitFor(t, "the run to end", runEnded(c.store, testRunID))

	wantHead(t, c.fakeEnv, cluster.RunSucceeded, "")
	if n := c.ran("a1/0"); n != 1 {
		t.Errorf("a1 ran step 0 %d times, want once", n)
	}
	rec := wantNode(t, c.fakeEnv, "a1", cluster.RunNodeSucceeded, "")
	wantStep(t, rec, 0, cluster.RunStepSucceeded, "")
	wantStep(t, rec, 1, cluster.RunStepSucceeded, "")
}

func TestAnInterruptedJobLeavesTheNodeUnknown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recorded string // the step's status as the old leader left it
		onNode   bool   // the node has the job
		outcome  string
		summary  string
	}{
		{"recorded running, the node answers interrupted", cluster.RunStepRunning, true, tasks.JobInterrupted, "interrupted"},
		{"recorded starting, the node answers interrupted", cluster.RunStepStarting, true, tasks.JobInterrupted, "interrupted"},
		{"recorded running, the node has no record of the job", cluster.RunStepRunning, false, "", "no record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFakeEnv(t, "m2", manager("m1", "mgr-1"), manager("m2", "mgr-2"), agent("a1", "web-1"))
			run := newRun(t, e, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}})
			id := jobID(testRunID, 0)
			if tc.onNode {
				if _, err := e.StartJob(context.Background(), "a1", tasks.JobSpec{ID: id, RunID: testRunID, Step: 0, Kind: tasks.StepCommand}, "operator"); err != nil {
					t.Fatal(err)
				}
				e.outcome[key("a1", 0)] = tasks.JobView{Status: tc.outcome}
			}
			if err := e.store.PutRunNode(testRunID, "a1", cluster.RunNodeRecord{Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{
				{Status: tc.recorded, JobID: id, StartedAt: e.now},
				{Status: cluster.RunStepPending},
			}}); err != nil {
				t.Fatal(err)
			}
			mustRun(t, e, run)

			want := []string(nil)
			if tc.onNode {
				want = []string{"a1/0"} // the old leader's, above
			}
			wantStarted(t, e, want...)
			rec := wantNode(t, e, "a1", cluster.RunNodeUnknown, "step 1")
			wantStep(t, rec, 0, cluster.RunStepUnknown, tc.summary)
			wantStep(t, rec, 1, cluster.RunStepPending, "")
			wantHead(t, e, cluster.RunFailed, "")
		})
	}
}

func TestTheOldLeaderStopsItsRunnerWhenItLosesLeadership(t *testing.T) {
	shortSupervise(t)
	c := newShared(t, "m1", manager("m1", "mgr-1"), manager("m2", "mgr-2"), agent("a1", "web-1"))
	c.polls[key("a1", 0)] = 1 << 30 // runs until the runner stops asking
	newRun(t, c.fakeEnv, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}})
	h := c.handler("m1")
	supervising(t, h)
	waitFor(t, "m1 to start a1's first step", func() bool { return c.ran("a1/0") == 1 })

	c.setLeader("m2")
	for deadline := time.Now().Add(5 * time.Second); h.runnerCount() != 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("m1's runner still runs 5s after m1 lost the leadership")
		}
	}
	if n := c.ran("a1/1"); n != 0 {
		t.Errorf("a1 ran step 1 %d times, want none", n)
	}
	wantHead(t, c.fakeEnv, cluster.RunRunning, "")
}

// The old leader handed the leadership over when its own turn came, so the
// run is recorded handing over. The new leader puts it back to running
// before it runs the old leader's steps.
func TestANewLeaderResumesARunHandedToIt(t *testing.T) {
	shortSupervise(t)
	c := newShared(t, "m2", manager("m1", "mgr-1"), manager("m2", "mgr-2"))
	newRun(t, c.fakeEnv, steps(true, cmdTrue), [][]string{{"m1"}}, func(r *cluster.RunRecord) { r.Status = cluster.RunHandingOver })
	var seen []string
	c.onPosted = func(by string, spec tasks.JobSpec) error {
		status := c.store.head(testRunID).Status
		c.mu.Lock()
		seen = append(seen, status)
		c.mu.Unlock()
		return nil
	}
	supervising(t, c.handler("m2"))
	waitFor(t, "the run to end", runEnded(c.store, testRunID))

	wantHead(t, c.fakeEnv, cluster.RunSucceeded, "")
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Equal(seen, []string{cluster.RunRunning}) {
		t.Errorf("the run was %v as m1's step was posted, want [running]", seen)
	}
	wantNode(t, c.fakeEnv, "m1", cluster.RunNodeSucceeded, "")
}

func TestTheSupervisorStopsWithTheManager(t *testing.T) {
	shortSupervise(t)
	e := newFakeEnv(t, "m1", manager("m1", "mgr-1"), agent("a1", "web-1"))
	e.stopped = make(chan struct{})
	e.polls[key("a1", 0)] = 1 << 30
	newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
	h := &Handler{runners: map[string]context.CancelFunc{}, newEnv: func() runEnv { return e }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.supervise(context.Background())
	}()
	waitFor(t, "the run's runner to start", func() bool { return h.runnerCount() == 1 })
	close(e.stopped)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor still runs 5s after its manager stopped")
	}
	if n := h.runnerCount(); n != 0 {
		t.Errorf("%d runners outlived the supervisor", n)
	}
}

// auditRow is one audit_logs row.
type auditRow struct {
	User, Method, Path string
	Status             int
	Node               string
}

func openAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "audit.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	if err := sfdb.RunMigrations(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestARunIsAuditedAtStartAndEndUnderTheOperatorsName(t *testing.T) {
	const secret = "s3cret-input"
	doc := "name: greet\ninputs:\n  word: { required: true }\nsteps:\n  - command: { run: \"echo {{ word }}\" }\n"
	for _, tc := range []struct {
		name    string
		outcome string
	}{
		{cluster.RunSucceeded, tasks.JobSucceeded},
		{cluster.RunFailed, tasks.JobFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortSupervise(t)
			d := openAuditDB(t)
			wctx, wcancel := context.WithCancel(context.Background())
			w := sfdb.NewAsyncWriter(wctx, d, "audit", 16)

			e := newFakeEnv(t, "m1", manager("m1", "mgr-1"), agent("a1", "web-1"))
			e.outcome[key("a1", 0)] = tasks.JobView{Status: tc.outcome}
			run := newRun(t, e, doc, [][]string{{"a1"}}, func(r *cluster.RunRecord) { r.Inputs = map[string]string{"word": secret} })
			h := &Handler{audit: w, runners: map[string]context.CancelFunc{}, newEnv: func() runEnv { return e }}
			stop := supervising(t, h)
			h.begin(*run)
			waitFor(t, "the run to end", runEnded(e.store, testRunID))
			stop()
			wcancel()
			w.Wait()

			rows, err := d.Query("SELECT username, method, path, status, node_id FROM audit_logs ORDER BY path")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var got []auditRow
			for rows.Next() {
				var r auditRow
				if err := rows.Scan(&r.User, &r.Method, &r.Path, &r.Status, &r.Node); err != nil {
					t.Fatal(err)
				}
				got = append(got, r)
			}
			want := []auditRow{
				{"operator", "TASK", "/api/v1/cluster/task-runs/" + testRunID + "#end:" + tc.name, 200, "m1"},
				{"operator", "POST", "/api/v1/cluster/task-runs/" + testRunID + "#start", 202, "m1"},
			}
			if !slices.Equal(got, want) {
				t.Errorf("audit rows = %+v, want %+v", got, want)
			}
			for _, r := range got {
				if strings.Contains(r.Path, secret) {
					t.Errorf("an audit row carries an input: %+v", r)
				}
			}
		})
	}
}

// The old leader lost the leadership while an agent rebooted: the agent is
// offline as the new leader takes over, and its reboot is followed to its
// end rather than the node failed for being offline.
func TestATakeoverDuringARebootWaitsForTheNode(t *testing.T) {
	e := newFakeEnv(t, "m2", manager("m1", "mgr-1"), manager("m2", "mgr-2"), agent("a1", "web-1"))
	run := newRun(t, e, steps(true, `reboot: { wait: 10m }`), [][]string{{"a1"}})
	id := jobID(testRunID, 0)
	if _, err := e.StartJob(context.Background(), "a1", tasks.JobSpec{ID: id, RunID: testRunID, Step: 0, Kind: tasks.StepReboot}, "operator"); err != nil {
		t.Fatal(err)
	}
	e.health["a1"] = cluster.StatusOffline
	e.getErrs[key("a1", 0)] = 3 // still booting for the first polls
	if err := e.store.PutRunNode(testRunID, "a1", cluster.RunNodeRecord{Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{
		{Status: cluster.RunStepRunning, JobID: id, StartedAt: e.now},
	}}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, e, run)

	wantStarted(t, e, "a1/0") // the old leader's, above
	rec := wantNode(t, e, "a1", cluster.RunNodeSucceeded, "")
	wantStep(t, rec, 0, cluster.RunStepSucceeded, "")
	wantHead(t, e, cluster.RunSucceeded, "")
}

// A look at the runs taken just before a runner ended its run still lists
// the run: a runner launched from it leaves the ended run as it is.
func TestAnEndedRunIsNotRunAgain(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "mgr-1"), agent("a1", "web-1"))
	run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
	e.store.setHead(testRunID, func(h *cluster.RunRecord) { h.Status, h.EndedAt = cluster.RunSucceeded, e.now })
	h := &Handler{runners: map[string]context.CancelFunc{}, newEnv: func() runEnv { return e }}
	if !h.launch(context.Background(), *run) {
		t.Fatal("launch started no runner")
	}
	h.wg.Wait()
	wantStarted(t, e)
	wantHead(t, e, cluster.RunSucceeded, "")
	if e.store.pruned != nil {
		t.Errorf("the ended run was finished again: pruned %v", e.store.pruned)
	}
}

// A leader writes a step's end and its node's end apart, so one lost between
// the two leaves the node running with the step ended. The leader that takes
// the run over closes the node from its own record and starts nothing after
// a step that stopped it.
func TestATakeoverClosesANodeFromItsOwnRecord(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recorded []string
		offline  bool
		status   string
		reason   string
		head     string
	}{
		{"a failed step: the node failed", []string{cluster.RunStepFailed, cluster.RunStepPending}, false, cluster.RunNodeFailed, "step 1 (command) failed", cluster.RunFailed},
		{"an unknown step: the node unknown", []string{cluster.RunStepUnknown, cluster.RunStepPending}, false, cluster.RunNodeUnknown, "step 1: the node did not answer", cluster.RunFailed},
		{"a cancelled step: the node stopped", []string{cluster.RunStepCancelled, cluster.RunStepPending}, false, cluster.RunNodeStopped, reasonStopped, cluster.RunSucceeded},
		{"every step ended, the node offline: succeeded", []string{cluster.RunStepSucceeded, cluster.RunStepSucceeded}, true, cluster.RunNodeSucceeded, "", cluster.RunSucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFakeEnv(t, "m2", manager("m1", "mgr-1"), manager("m2", "mgr-2"), agent("a1", "web-1"))
			run := newRun(t, e, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}})
			var recorded []cluster.RunStepRecord
			for i, s := range tc.recorded {
				rec := cluster.RunStepRecord{Status: s}
				if s != cluster.RunStepPending {
					rec.JobID, rec.StartedAt, rec.EndedAt = jobID(testRunID, i), e.now, e.now
				}
				if s == cluster.RunStepUnknown {
					rec.Summary = "the node did not answer: connection refused"
				}
				recorded = append(recorded, rec)
			}
			if err := e.store.PutRunNode(testRunID, "a1", cluster.RunNodeRecord{Status: cluster.RunNodeRunning, Steps: recorded}); err != nil {
				t.Fatal(err)
			}
			if tc.offline {
				e.health["a1"] = cluster.StatusOffline
			}
			mustRun(t, e, run)

			wantStarted(t, e)
			rec := wantNode(t, e, "a1", tc.status, tc.reason)
			if tc.status == cluster.RunNodeSucceeded && rec.Reason != "" {
				t.Errorf("a1's reason = %q, want none", rec.Reason)
			}
			wantStep(t, rec, 1, tc.recorded[1], "")
			wantHead(t, e, tc.head, "")
		})
	}
}

// A step recorded starting may never have reached its node. The leader that
// takes the run over asks the node for the recorded job before it posts it
// again: a job the node has is followed, never posted; one it has not is a
// step not yet started, which the stop and a manager's quorum apply to.
func TestATakeoverAsksAboutAStartingStepBeforePostingIt(t *testing.T) {
	starting := func(t *testing.T, e *fakeEnv, node string) {
		t.Helper()
		if err := e.store.PutRunNode(testRunID, node, cluster.RunNodeRecord{Status: cluster.RunNodeRunning, Steps: []cluster.RunStepRecord{
			{Status: cluster.RunStepStarting, JobID: jobID(testRunID, 0), StartedAt: e.now},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a manager's reboot the node never got, a voter down: skipped for the quorum", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), manager("m2", "mgr-2"), manager("m3", "mgr-3"), manager("m4", "mgr-4"))
		e.health["m3"] = cluster.StatusOffline
		run := newRun(t, e, steps(true, `reboot: { only_if_required: false }`), [][]string{{"m2"}, {"m4"}, {"m1"}})
		starting(t, e, "m2")
		mustRun(t, e, run)

		if p := e.postList(); len(p) != 0 {
			t.Errorf("posts = %v, want none", p)
		}
		rec := wantNode(t, e, "m2", cluster.RunNodeSkipped, reasonQuorum)
		wantStep(t, rec, 0, cluster.RunStepSkipped, reasonQuorum)
		wantHead(t, e, cluster.RunStopped, "quorum: rebooting mgr-2")
	})
	t.Run("a job the node has: followed, not posted", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
		if _, err := e.StartJob(context.Background(), "a1", tasks.JobSpec{ID: jobID(testRunID, 0), RunID: testRunID, Step: 0, Kind: tasks.StepCommand}, "operator"); err != nil {
			t.Fatal(err)
		}
		e.posts = nil // the old leader's post, above
		starting(t, e, "a1")
		mustRun(t, e, run)

		if p := e.postList(); len(p) != 0 {
			t.Errorf("posts = %v, want none: the node has the job", p)
		}
		rec := wantNode(t, e, "a1", cluster.RunNodeSucceeded, "")
		wantStep(t, rec, 0, cluster.RunStepSucceeded, "")
		wantHead(t, e, cluster.RunSucceeded, "")
	})
	t.Run("a post that never arrived, the run stopping: stopped, nothing posted", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"), agent("a2", "web-2"))
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}, {"a2"}})
		starting(t, e, "a1")
		// Stopped as the wave starts: the runner's first read and the wave's
		// own look saw the run running, the node's look sees it stopping.
		reads := 0
		e.store.afterGetRun = func(string) {
			if reads++; reads == 2 {
				e.store.setHead(testRunID, func(h *cluster.RunRecord) { h.Status, h.Reason = cluster.RunStopping, "stopped by operator" })
			}
		}
		mustRun(t, e, run)

		if p := e.postList(); len(p) != 0 {
			t.Errorf("posts = %v, want none", p)
		}
		wantNode(t, e, "a1", cluster.RunNodeStopped, "stopped by operator")
		wantHead(t, e, cluster.RunStopped, "stopped by operator")
	})
	t.Run("the node does not answer the question: posted again with the recorded ID", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
		if _, err := e.StartJob(context.Background(), "a1", tasks.JobSpec{ID: jobID(testRunID, 0), RunID: testRunID, Step: 0, Kind: tasks.StepCommand}, "operator"); err != nil {
			t.Fatal(err)
		}
		e.posts = nil // the old leader's post, above
		e.getErrs[key("a1", 0)] = 1
		starting(t, e, "a1")
		mustRun(t, e, run)

		if p := e.postList(); !slices.Equal(p, []string{"a1/0"}) {
			t.Errorf("posts = %v, want a1/0 posted again", p)
		}
		wantStarted(t, e, "a1/0") // the old leader's: the node kept one job for the ID
		wantNode(t, e, "a1", cluster.RunNodeSucceeded, "")
	})
	t.Run("a post that never arrived, the run going on: posted with the recorded ID", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
		starting(t, e, "a1")
		mustRun(t, e, run)

		wantStarted(t, e, "a1/0")
		if s := e.specs[key("a1", 0)]; s.ID != jobID(testRunID, 0) {
			t.Errorf("posted as job %q, want %q", s.ID, jobID(testRunID, 0))
		}
		wantNode(t, e, "a1", cluster.RunNodeSucceeded, "")
	})
}
