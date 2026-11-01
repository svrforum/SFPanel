package featuretasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// fakeStore is the task store in memory. Every write is copied, so what a
// test reads is what was recorded at that moment.
type fakeStore struct {
	mu     sync.Mutex
	revs   map[string]string
	heads  map[string]cluster.RunRecord
	nodes  map[string]map[string]cluster.RunNodeRecord
	pruned []int
	tasks  map[string]cluster.StoredTask
	// notReady is what TasksReady names.
	notReady []string
	// afterGetRun runs once a run's head has been read, before GetRun
	// returns it.
	afterGetRun func(id string)
}

func newFakeStore() *fakeStore {
	return &fakeStore{revs: map[string]string{}, heads: map[string]cluster.RunRecord{}, nodes: map[string]map[string]cluster.RunNodeRecord{}, tasks: map[string]cluster.StoredTask{}}
}

func (s *fakeStore) PutTask(t cluster.StoredTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(t.YAML) > tasks.MaxDocumentBytes {
		return cluster.ErrTaskTooLarge
	}
	if strings.TrimSpace(t.Name) == "" {
		return cluster.ErrTaskNameRequired
	}
	for _, other := range s.tasks {
		if other.ID != t.ID && other.Name == t.Name {
			return cluster.ErrTaskNameTaken
		}
	}
	s.tasks[t.ID] = t
	return nil
}

func (s *fakeStore) GetTask(id string) (cluster.StoredTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return cluster.StoredTask{}, cluster.ErrTaskNotFound
	}
	return t, nil
}

func (s *fakeStore) ListTasks() ([]cluster.StoredTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []cluster.StoredTask
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *fakeStore) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return cluster.ErrTaskNotFound
	}
	delete(s.tasks, id)
	return nil
}

func (s *fakeStore) PutTaskRevision(yaml string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := sha256.Sum256([]byte(yaml))
	sha := hex.EncodeToString(sum[:])
	s.revs[sha] = yaml
	return sha, nil
}

func (s *fakeStore) TasksReady() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.notReady)
}

func (s *fakeStore) TaskRevision(sha string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.revs[sha]
	if !ok {
		return "", cluster.ErrTaskRevisionNotFound
	}
	return v, nil
}

func (s *fakeStore) GetRun(id string) (cluster.RunRecord, map[string]cluster.RunNodeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.heads[id]
	if !ok {
		return cluster.RunRecord{}, nil, cluster.ErrTaskRunNotFound
	}
	nodes := map[string]cluster.RunNodeRecord{}
	for k, v := range s.nodes[id] {
		v.Steps = slices.Clone(v.Steps)
		nodes[k] = v
	}
	hook := s.afterGetRun
	s.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	s.mu.Lock()
	return h, nodes, nil
}

func (s *fakeStore) ListRuns() ([]cluster.RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []cluster.RunRecord
	for _, h := range s.heads {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (s *fakeStore) PutRun(r cluster.RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heads[r.ID] = r
	return nil
}

func (s *fakeStore) PutRunNode(runID, nodeID string, rec cluster.RunNodeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.heads[runID]; !ok {
		return cluster.ErrTaskRunNotFound
	}
	if s.nodes[runID] == nil {
		s.nodes[runID] = map[string]cluster.RunNodeRecord{}
	}
	rec.Steps = slices.Clone(rec.Steps)
	s.nodes[runID][nodeID] = rec
	return nil
}

func (s *fakeStore) PruneRuns(keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned = append(s.pruned, keep)
	return nil
}

func (s *fakeStore) head(id string) cluster.RunRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heads[id]
}

func (s *fakeStore) node(runID, nodeID string) cluster.RunNodeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.nodes[runID][nodeID]
	rec.Steps = slices.Clone(rec.Steps)
	return rec
}

// setHead changes a run's head as an operator's request would.
func (s *fakeStore) setHead(id string, change func(h *cluster.RunRecord)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.heads[id]
	change(&h)
	s.heads[id] = h
}

// fakeJob is one job on one fake node.
type fakeJob struct {
	node      string
	spec      tasks.JobSpec
	polls     int
	cancelled bool
}

// fakeEnv is a cluster of fake nodes on a fake clock. A job succeeds on its
// first poll unless the test says otherwise, per "node/step".
type fakeEnv struct {
	t     *testing.T
	mu    sync.Mutex
	local string
	nodes []cluster.Node
	// health: a node not named is online.
	health      map[string]cluster.NodeStatus
	outdated    map[string]bool
	unreachable map[string]bool
	notLeader   bool
	// behind is a leader whose FSM has not yet applied every entry the
	// previous leader committed.
	behind  bool
	stopped chan struct{}
	store   *fakeStore
	now     time.Time

	checks   map[string][]tasks.CheckVerdict
	checkErr map[string]error

	// refuse makes StartJob answer with the error; outcome is the view a
	// job ends with (default succeeded); polls how many polls it runs for
	// (default 1); getErrs how many polls fail to reach the node first;
	// startStatus the status the job starts in (default running, rebooting
	// for a reboot).
	refuse      map[string]error
	outcome     map[string]tasks.JobView
	polls       map[string]int
	getErrs     map[string]int
	startStatus map[string]string
	// onPoll runs as a job is polled, before it answers.
	onPoll func(node string, step, poll int)
	// onStart runs as a job is posted, before it is recorded.
	onStart func(node string, spec tasks.JobSpec)

	// readsOutput lets GetJob be asked for output, as the API's output
	// route does; output is each job's whole output, per "node/step".
	readsOutput bool
	output      map[string]string
	// getRefusal, when set, is what every GetJob answers with: a node's
	// refusal of the read.
	getRefusal error

	jobs    map[string]*fakeJob
	posts   []string // node/step, every post, whether it started a job or not
	started []string // node/step, in order posted
	cancels []string // node/step force-cancelled
	// checkedBy and cancelledBy are "node:operator" for every Check and
	// CancelJob, as the node's audit row would name them.
	checkedBy   []string
	cancelledBy []string
	specs       map[string]tasks.JobSpec
	handTo      string
	handErr     error
	handedAt    []string // the run's head status as HandOver was called
}

func newFakeEnv(t *testing.T, local string, nodes ...cluster.Node) *fakeEnv {
	return &fakeEnv{
		t: t, local: local, nodes: nodes, store: newFakeStore(),
		now:    time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		health: map[string]cluster.NodeStatus{}, outdated: map[string]bool{}, unreachable: map[string]bool{},
		checks: map[string][]tasks.CheckVerdict{}, checkErr: map[string]error{},
		refuse: map[string]error{}, outcome: map[string]tasks.JobView{}, polls: map[string]int{},
		getErrs: map[string]int{}, startStatus: map[string]string{},
		jobs: map[string]*fakeJob{}, specs: map[string]tasks.JobSpec{}, output: map[string]string{},
	}
}

func key(node string, step int) string { return node + "/" + fmt.Sprint(step) }

func manager(id, name string) cluster.Node {
	return cluster.Node{ID: id, Name: name, Kind: cluster.KindManager, Role: cluster.RoleVoter, GRPCAddress: "192.0.2.10:3629"}
}

func agent(id, name string) cluster.Node {
	return cluster.Node{ID: id, Name: name, Kind: cluster.KindAgent, GRPCAddress: "198.51.100.10:3629"}
}

func (e *fakeEnv) IsLeader() bool  { return !e.notLeader }
func (e *fakeEnv) CaughtUp() bool  { return !e.notLeader && !e.behind }
func (e *fakeEnv) LocalID() string { return e.local }

// Done is the manager's lifetime: a test that supervises closes stopped.
func (e *fakeEnv) Done() <-chan struct{} { return e.stopped }
func (e *fakeEnv) Nodes() []cluster.Node {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.nodes)
}

func (e *fakeEnv) Health() map[string]cluster.NodeStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]cluster.NodeStatus{}
	for _, n := range e.nodes {
		out[n.ID] = cluster.StatusOnline
	}
	for id, s := range e.health {
		out[id] = s
	}
	return out
}

func (e *fakeEnv) Capable(id string) bool     { return !e.outdated[id] }
func (e *fakeEnv) Unreachable(id string) bool { return e.unreachable[id] }
func (e *fakeEnv) Store() runStore            { return e.store }
func (e *fakeEnv) Tasks() taskStore           { return e.store }

func (e *fakeEnv) Now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *fakeEnv) Sleep(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *fakeEnv) HandOver() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range e.store.heads {
		e.handedAt = append(e.handedAt, h.Status)
	}
	return e.handTo, e.handErr
}

func (e *fakeEnv) Check(_ context.Context, nodeID string, specs []tasks.JobSpec, username string) ([]tasks.CheckVerdict, error) {
	e.mu.Lock()
	e.checkedBy = append(e.checkedBy, nodeID+":"+username)
	e.mu.Unlock()
	if err := e.checkErr[nodeID]; err != nil {
		return nil, err
	}
	return e.checks[nodeID], nil
}

func (e *fakeEnv) StartJob(_ context.Context, nodeID string, spec tasks.JobSpec, _ string) (tasks.JobView, error) {
	if e.onStart != nil {
		e.onStart(nodeID, spec)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	k := key(nodeID, spec.Step)
	e.specs[k] = spec
	e.posts = append(e.posts, k)
	if err := e.refuse[k]; err != nil {
		return tasks.JobView{}, err
	}
	if j, ok := e.jobs[nodeID+"/"+spec.ID]; ok {
		return e.viewLocked(j), nil
	}
	e.started = append(e.started, k)
	j := &fakeJob{node: nodeID, spec: spec}
	e.jobs[nodeID+"/"+spec.ID] = j
	return e.viewLocked(j), nil
}

func (e *fakeEnv) viewLocked(j *fakeJob) tasks.JobView {
	k := key(j.node, j.spec.Step)
	if j.cancelled {
		return tasks.JobView{ID: j.spec.ID, Kind: j.spec.Kind, Status: tasks.JobCancelled, Summary: "cancelled"}
	}
	end := e.polls[k]
	if end == 0 {
		end = 1
	}
	if j.polls >= end {
		v, ok := e.outcome[k]
		if !ok {
			v = tasks.JobView{Status: tasks.JobSucceeded}
		}
		v.ID, v.Kind = j.spec.ID, j.spec.Kind
		return v
	}
	status := e.startStatus[k]
	if status == "" {
		status = tasks.JobRunning
		if j.spec.Kind == tasks.StepReboot {
			status = tasks.JobRebooting
		}
	}
	return tasks.JobView{ID: j.spec.ID, Kind: j.spec.Kind, Status: status}
}

func (e *fakeEnv) GetJob(_ context.Context, nodeID, jobID string, skip int64) (tasks.JobView, error) {
	if skip != statusOnly && !e.readsOutput {
		e.t.Errorf("GetJob(%s, %s) asked for output from %d: the leader reads the state only", nodeID, jobID, skip)
	}
	if e.getRefusal != nil {
		return tasks.JobView{}, e.getRefusal
	}
	e.mu.Lock()
	j, ok := e.jobs[nodeID+"/"+jobID]
	if !ok {
		e.mu.Unlock()
		return tasks.JobView{}, &jobRefusal{Status: http.StatusNotFound, Code: response.ErrTaskJobNotFound, Message: "No such task job on this node"}
	}
	j.polls++
	poll, step := j.polls, j.spec.Step
	k := key(nodeID, step)
	e.mu.Unlock()
	if e.onPoll != nil {
		e.onPoll(nodeID, step, poll)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.getErrs[k] > 0 {
		e.getErrs[k]--
		j.polls-- // the node never heard this poll
		return tasks.JobView{}, errors.New("connection refused")
	}
	v := e.viewLocked(j)
	if out := e.output[k]; skip < int64(len(out)) {
		v.Output, v.NextSkip = out[skip:], int64(len(out))
	}
	return v, nil
}

func (e *fakeEnv) CancelJob(_ context.Context, nodeID, jobID string, force bool, username string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancelledBy = append(e.cancelledBy, nodeID+":"+username)
	j, ok := e.jobs[nodeID+"/"+jobID]
	if !ok {
		return &jobRefusal{Status: http.StatusNotFound, Code: response.ErrTaskJobNotFound}
	}
	if !force {
		return &jobRefusal{Status: http.StatusConflict, Code: response.ErrTaskJobRunning}
	}
	e.cancels = append(e.cancels, key(nodeID, j.spec.Step))
	j.cancelled = true
	return nil
}

func (e *fakeEnv) startedList() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.started)
}

func (e *fakeEnv) postList() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.posts)
}

func (e *fakeEnv) cancelList() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.cancels)
}

const testRunID = "20261101-120000-abcdef"

// newRun records a run of doc over waves and returns it, as a run's start
// would leave it: the revision, the head running, every node pending.
func newRun(t *testing.T, e *fakeEnv, doc string, waves [][]string, change ...func(r *cluster.RunRecord)) *cluster.RunRecord {
	t.Helper()
	task, problems := tasks.Parse([]byte(doc))
	if task == nil {
		t.Fatalf("the test's task does not parse: %+v", problems)
	}
	sum := sha256.Sum256([]byte(doc))
	rev := hex.EncodeToString(sum[:])
	e.store.revs[rev] = doc
	r := cluster.RunRecord{
		ID: testRunID, TaskID: "t1", TaskName: task.Name, Rev: rev, Waves: waves,
		Batch: 1, StopOnFailure: task.StopOnFailure, Status: cluster.RunRunning,
		StartedBy: "operator", StartedAt: e.now,
	}
	for _, w := range waves {
		r.Nodes = append(r.Nodes, w...)
	}
	for _, c := range change {
		c(&r)
	}
	if err := e.store.PutRun(r); err != nil {
		t.Fatal(err)
	}
	for _, id := range r.Nodes {
		if err := e.store.PutRunNode(r.ID, id, cluster.RunNodeRecord{Status: cluster.RunNodePending}); err != nil {
			t.Fatal(err)
		}
	}
	return &r
}

// steps writes a task document of the given steps, each a YAML flow line.
func steps(stopOnFailure bool, lines ...string) string {
	var b strings.Builder
	b.WriteString("name: test task\n")
	fmt.Fprintf(&b, "stop_on_failure: %v\n", stopOnFailure)
	b.WriteString("steps:\n")
	for _, l := range lines {
		b.WriteString("  - " + l + "\n")
	}
	return b.String()
}

// fakeInstalls is the agent installer as the engine sees it: which install
// runs have a session here, and the calls made to it.
type fakeInstalls struct {
	mu     sync.Mutex
	active bool
	owns   map[string]bool
	// view and outErr are what StepOutput answers.
	view   tasks.JobView
	outErr error

	asked   int      // Owns calls
	stops   []string // "runID:by"
	outputs []string // "runID/key/step/skip"
}

func (f *fakeInstalls) Active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *fakeInstalls) Owns(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.owns[runID]
}

func (f *fakeInstalls) Stop(runID, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, runID+":"+by)
	return nil
}

func (f *fakeInstalls) StepOutput(runID, key string, step int, skip int64) (tasks.JobView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputs = append(f.outputs, fmt.Sprintf("%s/%s/%d/%d", runID, key, step, skip))
	return f.view, f.outErr
}

func (f *fakeInstalls) askedOwns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

// installRun records an agent install run over the rows keys, with the row
// records given; a row not given has none yet, as one whose turn has not
// come.
func installRun(t *testing.T, st *fakeStore, id, status string, recs map[string]cluster.RunNodeRecord, keys ...string) {
	t.Helper()
	r := cluster.RunRecord{ID: id, Kind: cluster.RunKindAgentInstall, TaskID: cluster.AgentInstallTaskID, TaskName: "agent install",
		Nodes: keys, Waves: [][]string{keys}, Batch: 3, Status: status, StartedBy: "operator",
		StartedAt: time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), CredentialsUntil: time.Date(2026, 11, 1, 12, 10, 0, 0, time.UTC)}
	if err := st.PutRun(r); err != nil {
		t.Fatal(err)
	}
	for k, rec := range recs {
		if err := st.PutRunNode(id, k, rec); err != nil {
			t.Fatal(err)
		}
	}
}

// installRows are an install's rows as a leader change finds them: s1 not
// yet started, s2 waiting on its host key, s3 installing, s4 registered as
// node a9 and being checked, s5 done as node a8, s6 refused by its
// pre-flight.
func installRows() map[string]cluster.RunNodeRecord {
	step := func(statuses ...string) []cluster.RunStepRecord {
		var out []cluster.RunStepRecord
		for _, s := range statuses {
			out = append(out, cluster.RunStepRecord{Status: s})
		}
		return out
	}
	p, r, ok := cluster.RunStepPending, cluster.RunStepRunning, cluster.RunStepSucceeded
	return map[string]cluster.RunNodeRecord{
		"s2": {Status: cluster.RunNodeRunning, Host: "198.51.100.2", IP: "198.51.100.2", Awaiting: "host_key", HostKey: "ED25519 SHA256:abc", Steps: step(r, p, p, p, p)},
		"s3": {Status: cluster.RunNodeRunning, Host: "web-3", IP: "198.51.100.3", Steps: step(ok, ok, r, p, p)},
		"s4": {Status: cluster.RunNodeRunning, Host: "100.64.0.4", IP: "100.64.0.4", NodeID: "a9", Steps: step(ok, ok, ok, ok, r)},
		"s5": {Status: cluster.RunNodeSucceeded, Host: "198.51.100.5", IP: "198.51.100.5", NodeID: "a8", Steps: step(ok, ok, ok, ok, ok)},
		"s6": {Status: cluster.RunNodeFailed, Reason: response.ErrAgentInstallOS, Host: "198.51.100.6", IP: "198.51.100.6", Steps: step(ok, cluster.RunStepFailed, p, p, p)},
	}
}

var installKeys = []string{"s1", "s2", "s3", "s4", "s5", "s6"}
