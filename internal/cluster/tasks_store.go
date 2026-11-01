package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/raft"

	"github.com/svrforum/SFPanel/internal/tasks"
)

// CapTasks is the heartbeat capability of a node whose code runs task jobs
// and, on a manager, keeps the task store. Every manager and every agent
// reports it — a property of the code. A run starts only once every manager
// reports it (TasksReady), and leaves out a target that does not.
const CapTasks = "tasks-v1"

// The task store lives in the replicated config map, under three prefixes,
// written with CmdSetConfig and CmdDeleteConfig only so that a manager of any
// version applies them:
//
//	task/<id>                    a task definition, JSON StoredTask
//	taskrev/<sha256 of the YAML> a document a run was started from, the YAML
//	taskrun/<runID>              a run's head, JSON RunRecord
//	taskrun/<runID>/n/<nodeID>   one node's part of a run, JSON RunNodeRecord
//
// A run's progress rewrites only its node records, each small, so a step
// that starts or ends does not rewrite the whole run. The same map holds the
// cluster's secrets: the store lists by prefix (FSM.ConfigWithPrefix) and
// never copies the whole state.
const (
	taskKeyPrefix    = "task/"
	taskRevKeyPrefix = "taskrev/"
	taskRunKeyPrefix = "taskrun/"
	runNodeInfix     = "/n/"
)

// Statuses of a run's head.
const (
	RunRunning     = "running"
	RunStopping    = "stopping"
	RunHandingOver = "handing_over"
	RunSucceeded   = "succeeded"
	RunFailed      = "failed"
	RunStopped     = "stopped"
)

// Statuses of one node in a run.
const (
	RunNodePending   = "pending"
	RunNodeRunning   = "running"
	RunNodeSucceeded = "succeeded"
	RunNodeFailed    = "failed"
	RunNodeSkipped   = "skipped"
	RunNodeUnknown   = "unknown"
	RunNodeStopped   = "stopped"
)

// Statuses of one step on one node.
const (
	RunStepPending   = "pending"
	RunStepStarting  = "starting"
	RunStepRunning   = "running"
	RunStepSucceeded = "succeeded"
	RunStepFailed    = "failed"
	RunStepSkipped   = "skipped"
	RunStepUnknown   = "unknown"
	RunStepCancelled = "cancelled"
)

// RunKindAgentInstall is the kind of a run that is an agent install over SSH
// (internal/feature/agentinstall): its rows are servers, keyed s1, s2, … in
// the order given, and its steps the install's five fixed phases. A run of
// a task has no kind. The install's session runs in the memory of the
// leader that started it, with the credentials; the task engine never runs
// one.
const RunKindAgentInstall = "agent-install"

// AgentInstallTaskID is the task ID an install run records, and the
// built-in the task list shows for it: it has no document, and no task
// route runs it.
const AgentInstallTaskID = "builtin/agent-install"

// Where a stored task came from.
const (
	TaskSourceUser = "user"
	TaskSourceGit  = "git"
)

var (
	ErrTaskTooLarge         = fmt.Errorf("a task document is at most %d KiB", tasks.MaxDocumentBytes>>10)
	ErrTaskNameTaken        = errors.New("another task already has this name")
	ErrTaskNameRequired     = errors.New("a task needs a name")
	ErrInvalidTaskID        = errors.New("a task ID is 1 to 64 letters, digits, '-' or '_'")
	ErrInvalidTaskSource    = errors.New("a task's source is user or git")
	ErrTaskNotFound         = errors.New("task not found")
	ErrTaskRevisionNotFound = errors.New("task revision not found")
	ErrTaskRunNotFound      = errors.New("task run not found")
	ErrInvalidRunID         = errors.New("invalid task run or node ID")
)

var (
	taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	runIDPattern  = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)
)

// revisionGrace is how long a pruning leaves a revision this leader has just
// written or reused, though no run uses it: a run being started writes its
// revision first and its record after, and a pruning in between must not
// take the revision from under it. A variable so tests can shorten it.
var revisionGrace = 10 * time.Minute

// TaskSource is where a stored task came from: written in the panel (user),
// or imported from a Git repository at a commit.
type TaskSource struct {
	Kind   string `json:"kind"`
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	Path   string `json:"path,omitempty"`
	SHA    string `json:"sha,omitempty"`
}

// StoredTask is a task definition in replicated state. Built-in tasks are
// never stored: every manager carries them in its binary.
type StoredTask struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	YAML      string     `json:"yaml"`
	Source    TaskSource `json:"source"`
	UpdatedAt time.Time  `json:"updated_at"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// RunRecord is a run's head: what was started, by whom, over which nodes,
// and where the run stands. Every manager keeps it, in its Raft log and
// snapshots: nothing secret goes in it.
type RunRecord struct {
	ID string `json:"id"`
	// Kind is "" for a run of a task, RunKindAgentInstall for an install,
	// whose Nodes are its row keys and which has no Rev.
	Kind     string `json:"kind,omitempty"`
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`
	// Rev is the sha256 of the document the run was started from, stored
	// under taskrev/<Rev>.
	Rev    string            `json:"rev"`
	Inputs map[string]string `json:"inputs,omitempty"`
	// Nodes are the targets' IDs in the order they run; Waves the same nodes
	// as the run takes them, one wave after another.
	Nodes []string   `json:"nodes"`
	Waves [][]string `json:"waves"`
	// Confirmed are the steps (0-based) the operator confirmed at the start.
	Confirmed     []int  `json:"confirmed,omitempty"`
	Batch         int    `json:"batch"`
	StopOnFailure bool   `json:"stop_on_failure"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	// Force marks a forced stop: command jobs are cancelled, not left to end.
	Force bool `json:"force,omitempty"`
	// StoppedBy is the operator who stopped the run, or forced its stop: the
	// node rows of the jobs a forced stop cancels carry this name.
	StoppedBy string    `json:"stopped_by,omitempty"`
	StartedBy string    `json:"started_by"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	// RerunOf is the run this one runs again; StartAt the first step (0-based)
	// each of its nodes starts at.
	RerunOf string         `json:"rerun_of,omitempty"`
	StartAt map[string]int `json:"start_at,omitempty"`
	// CredentialsUntil is when an install's session drops the credentials
	// it keeps after the install ended with a failed row, for a recheck or a
	// revert; zero when it keeps none. The credentials never leave that
	// leader's memory.
	CredentialsUntil time.Time `json:"credentials_until,omitzero"`
}

// Active reports whether the run has not ended: a leader owes it a runner,
// and a pruning never drops it.
func (r RunRecord) Active() bool {
	switch r.Status {
	case RunRunning, RunStopping, RunHandingOver:
		return true
	}
	return false
}

// RunNodeRecord is one node's part of a run, or one server's row of an
// install. An install row's Reason is one of the AGENT_INSTALL_* row codes
// of internal/api/response, which the screen words.
type RunNodeRecord struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// ReasonDetails are an install row's reason's details (the port that is
	// busy, the OS seen), which the screen words the code with.
	ReasonDetails map[string]string `json:"reason_details,omitempty"`
	Steps         []RunStepRecord   `json:"steps,omitempty"`

	// An install row's server: Host as the operator gave it, IP the address
	// it resolved to, which the agent registers at; Name and Group the node
	// name and group label asked for. NodeID is the node it registered as.
	Host   string `json:"host,omitempty"`
	IP     string `json:"ip,omitempty"`
	Name   string `json:"name,omitempty"`
	Group  string `json:"group,omitempty"`
	NodeID string `json:"node_id,omitempty"`
	// SFPanelBefore is whether an install row's server had SFPanel, its
	// binary or its data, before the install: set once the pre-flight
	// passes. Nothing purges such a server.
	SFPanelBefore bool `json:"sfpanel_before,omitempty"`
	// Awaiting is what the row waits for the operator on: host_key, check,
	// standalone or review. HostKey is the server's host key fingerprint
	// ("ED25519 SHA256:…") to approve, CheckURL the address Tailscale SSH
	// asks the operator to open.
	Awaiting string `json:"awaiting,omitempty"`
	HostKey  string `json:"host_key,omitempty"`
	CheckURL string `json:"check_url,omitempty"`
}

// RunStepRecord is one step on one node. JobID is written before the job is
// started, so a new leader asks the node for that job rather than start it
// again. The output stays on the node, under the job.
type RunStepRecord struct {
	Status    string    `json:"status"`
	JobID     string    `json:"job_id,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Changed   bool      `json:"changed,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
}

// NewTaskID returns a new task ID: 16 random hex digits.
func NewTaskID() string {
	return hex.EncodeToString(randomBytes(8))
}

// NewRunID returns a run ID for a run started at now: YYYYMMDD-HHMMSS-<6hex>
// in UTC, so run IDs sort by when their runs started.
func NewRunID(now time.Time) string {
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(randomBytes(3))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	// crypto/rand.Read never returns an error (Go 1.24+): it crashes the
	// program instead of handing out a short read.
	_, _ = rand.Read(b)
	return b
}

func taskKey(id string) string     { return taskKeyPrefix + id }
func taskRevKey(sha string) string { return taskRevKeyPrefix + sha }
func runKey(id string) string      { return taskRunKeyPrefix + id }
func runNodeKey(runID, nodeID string) string {
	return runKey(runID) + runNodeInfix + nodeID
}

// validNodeSegment reports whether a node ID can end a key: a node record's
// key must not reach under another prefix.
func validNodeSegment(id string) bool {
	return id != "" && len(id) <= 128 && !strings.ContainsAny(id, "/\x00")
}

// TasksReady names the managers a task run waits for, each with why: every
// manager but this leader must be online and report CapTasks. Agents do not
// hold runs back; a run leaves out one that does not report it. On a node
// with no Raft it names none: the writes a run starts with refuse there.
func (m *Manager) TasksReady() []string {
	if m.raft == nil {
		return nil
	}
	return m.nodesNotReady(m.raft.GetFSM().GetState(), CapTasks, false)
}

// taskLeader reports whether this manager may write the task store: only the
// leader does. A node with no Raft is not the leader.
func (m *Manager) taskLeader() bool {
	return m.raft != nil && m.raft.IsLeader()
}

// taskFSM is the FSM the store reads, on any manager; ErrNotInitialized on a
// node with no Raft.
func (m *Manager) taskFSM() (*FSM, error) {
	if m.raft == nil {
		return nil, ErrNotInitialized
	}
	return m.raft.GetFSM(), nil
}

// putTaskValue writes one store key. A leader that lost its role before the
// entry was appended says ErrNotLeader, as the check before it would have.
func (m *Manager) putTaskValue(key, value string) error {
	err := m.SetConfig(key, value)
	if errors.Is(err, raft.ErrNotLeader) {
		return ErrNotLeader
	}
	return err
}

func (m *Manager) deleteTaskValue(key string) error {
	err := m.raft.Apply(Command{Type: CmdDeleteConfig, Key: key}, 5*time.Second)
	if errors.Is(err, raft.ErrNotLeader) {
		return ErrNotLeader
	}
	return err
}

// PutTask stores a task definition, new or changed (leader only). The
// document is at most tasks.MaxDocumentBytes, and no other task may have the
// same name. A task with no source was written in the panel; one with no
// time is stamped now.
func (m *Manager) PutTask(t StoredTask) error {
	if !m.taskLeader() {
		return ErrNotLeader
	}
	if !taskIDPattern.MatchString(t.ID) {
		return ErrInvalidTaskID
	}
	if len(t.YAML) > tasks.MaxDocumentBytes {
		return ErrTaskTooLarge
	}
	if strings.TrimSpace(t.Name) == "" {
		return ErrTaskNameRequired
	}
	switch t.Source.Kind {
	case "":
		t.Source.Kind = TaskSourceUser
	case TaskSourceUser, TaskSourceGit:
	default:
		return ErrInvalidTaskSource
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}

	// The name check and the write under one lock: checked apart, two tasks
	// saved at once could both take a free name.
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	for _, other := range m.storedTasks() {
		if other.ID != t.ID && other.Name == t.Name {
			return ErrTaskNameTaken
		}
	}
	return m.putTaskValue(taskKey(t.ID), string(data))
}

// GetTask returns a stored task, from this manager's own replicated state.
func (m *Manager) GetTask(id string) (StoredTask, error) {
	fsm, err := m.taskFSM()
	if err != nil {
		return StoredTask{}, err
	}
	if !taskIDPattern.MatchString(id) {
		return StoredTask{}, ErrTaskNotFound
	}
	v, ok := fsm.GetConfigValue(taskKey(id))
	if !ok {
		return StoredTask{}, ErrTaskNotFound
	}
	var t StoredTask
	if err := json.Unmarshal([]byte(v), &t); err != nil {
		return StoredTask{}, fmt.Errorf("task %s: %w", id, err)
	}
	return t, nil
}

// ListTasks returns the stored tasks by name, from this manager's own
// replicated state.
func (m *Manager) ListTasks() ([]StoredTask, error) {
	if _, err := m.taskFSM(); err != nil {
		return nil, err
	}
	return m.storedTasks(), nil
}

// storedTasks reads every stored task, by name. One that does not decode is
// logged and left out: it must not hide the others.
func (m *Manager) storedTasks() []StoredTask {
	var out []StoredTask
	for k, v := range m.raft.GetFSM().ConfigWithPrefix(taskKeyPrefix) {
		var t StoredTask
		if err := json.Unmarshal([]byte(v), &t); err != nil {
			slog.Warn("unreadable task definition in replicated state", "component", "cluster", "key", k, "error", err)
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// DeleteTask removes a stored task (leader only). Its runs keep their own
// copy of the document, under taskrev/.
func (m *Manager) DeleteTask(id string) error {
	if !m.taskLeader() {
		return ErrNotLeader
	}
	if !taskIDPattern.MatchString(id) {
		return ErrTaskNotFound
	}
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	if _, ok := m.raft.GetFSM().GetConfigValue(taskKey(id)); !ok {
		return ErrTaskNotFound
	}
	return m.deleteTaskValue(taskKey(id))
}

// PutTaskRevision stores the document a run is started from under its
// sha256 and returns the sha (leader only). A document already stored is not
// written again: the same task run 200 times keeps one copy.
func (m *Manager) PutTaskRevision(yaml string) (string, error) {
	if !m.taskLeader() {
		return "", ErrNotLeader
	}
	if len(yaml) > tasks.MaxDocumentBytes {
		return "", ErrTaskTooLarge
	}
	sum := sha256.Sum256([]byte(yaml))
	sha := hex.EncodeToString(sum[:])

	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	// Noted before the write, and on a reuse too: the run this is for has no
	// record yet, and a pruning must leave its revision alone meanwhile.
	if m.taskRevWritten == nil {
		m.taskRevWritten = map[string]time.Time{}
	}
	m.taskRevWritten[sha] = time.Now()
	if _, ok := m.raft.GetFSM().GetConfigValue(taskRevKey(sha)); ok {
		return sha, nil
	}
	if err := m.putTaskValue(taskRevKey(sha), yaml); err != nil {
		return "", err
	}
	return sha, nil
}

// TaskRevision returns a stored document by its sha256, from this manager's
// own replicated state.
func (m *Manager) TaskRevision(sha string) (string, error) {
	fsm, err := m.taskFSM()
	if err != nil {
		return "", err
	}
	if sha == "" || strings.Contains(sha, "/") {
		return "", ErrTaskRevisionNotFound
	}
	v, ok := fsm.GetConfigValue(taskRevKey(sha))
	if !ok {
		return "", ErrTaskRevisionNotFound
	}
	return v, nil
}

// PutRun writes a run's head (leader only).
func (m *Manager) PutRun(r RunRecord) error {
	if !m.taskLeader() {
		return ErrNotLeader
	}
	if !runIDPattern.MatchString(r.ID) {
		return ErrInvalidRunID
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return m.putTaskValue(runKey(r.ID), string(data))
}

// PutRunNode writes one node's part of a run (leader only). The run's head
// must exist: a node record without one would be listed nowhere.
func (m *Manager) PutRunNode(runID, nodeID string, rec RunNodeRecord) error {
	if !m.taskLeader() {
		return ErrNotLeader
	}
	if !runIDPattern.MatchString(runID) || !validNodeSegment(nodeID) {
		return ErrInvalidRunID
	}
	if _, ok := m.raft.GetFSM().GetConfigValue(runKey(runID)); !ok {
		return ErrTaskRunNotFound
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return m.putTaskValue(runNodeKey(runID, nodeID), string(data))
}

// GetRun returns a run's head and its node records by node ID, from this
// manager's own replicated state.
func (m *Manager) GetRun(id string) (RunRecord, map[string]RunNodeRecord, error) {
	fsm, err := m.taskFSM()
	if err != nil {
		return RunRecord{}, nil, err
	}
	if !runIDPattern.MatchString(id) {
		return RunRecord{}, nil, ErrTaskRunNotFound
	}
	v, ok := fsm.GetConfigValue(runKey(id))
	if !ok {
		return RunRecord{}, nil, ErrTaskRunNotFound
	}
	var r RunRecord
	if err := json.Unmarshal([]byte(v), &r); err != nil {
		return RunRecord{}, nil, fmt.Errorf("task run %s: %w", id, err)
	}
	prefix := runKey(id) + runNodeInfix
	nodes := map[string]RunNodeRecord{}
	for k, v := range fsm.ConfigWithPrefix(prefix) {
		var rec RunNodeRecord
		if err := json.Unmarshal([]byte(v), &rec); err != nil {
			return RunRecord{}, nil, fmt.Errorf("task run %s, %s: %w", id, k, err)
		}
		nodes[strings.TrimPrefix(k, prefix)] = rec
	}
	return r, nodes, nil
}

// ListRuns returns every run's head, newest first, from this manager's own
// replicated state.
func (m *Manager) ListRuns() ([]RunRecord, error) {
	fsm, err := m.taskFSM()
	if err != nil {
		return nil, err
	}
	return runHeads(fsm.ConfigWithPrefix(taskRunKeyPrefix)), nil
}

// runHeads decodes the run heads among the taskrun/ entries, newest first.
// One that does not decode is logged and left out.
func runHeads(entries map[string]string) []RunRecord {
	var runs []RunRecord
	for k, v := range entries {
		id := strings.TrimPrefix(k, taskRunKeyPrefix)
		if strings.Contains(id, "/") {
			continue // a node record
		}
		var r RunRecord
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			slog.Warn("unreadable task run in replicated state", "component", "cluster", "key", k, "error", err)
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID > runs[j].ID })
	return runs
}

// PruneRuns keeps the keep newest runs and every run that has not ended, and
// deletes the rest with their node records (leader only). Then it deletes the
// revisions no kept run uses, but one this leader wrote or reused within
// revisionGrace: its run may not be recorded yet. A node record whose run has
// no head is deleted too.
func (m *Manager) PruneRuns(keep int) error {
	if !m.taskLeader() {
		return ErrNotLeader
	}
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	fsm := m.raft.GetFSM()

	entries := fsm.ConfigWithPrefix(taskRunKeyPrefix)
	kept := map[string]bool{}
	usedRevs := map[string]bool{}
	for i, r := range runHeads(entries) {
		if i < keep || r.Active() {
			kept[r.ID] = true
			usedRevs[r.Rev] = true
		}
	}
	var drop []string
	runs := 0
	for k := range entries {
		id, _, nodeKey := strings.Cut(strings.TrimPrefix(k, taskRunKeyPrefix), "/")
		if !kept[id] {
			drop = append(drop, k)
			if !nodeKey {
				runs++
			}
		}
	}
	now := time.Now()
	revs := 0
	for k := range fsm.ConfigWithPrefix(taskRevKeyPrefix) {
		sha := strings.TrimPrefix(k, taskRevKeyPrefix)
		if usedRevs[sha] {
			continue
		}
		if at, ok := m.taskRevWritten[sha]; ok && now.Sub(at) < revisionGrace {
			continue
		}
		drop = append(drop, k)
		revs++
	}
	for sha, at := range m.taskRevWritten {
		if now.Sub(at) >= revisionGrace {
			delete(m.taskRevWritten, sha)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	sort.Strings(drop)
	for _, k := range drop {
		if err := m.deleteTaskValue(k); err != nil {
			return fmt.Errorf("prune task runs: %s: %w", k, err)
		}
	}
	slog.Info("pruned task runs", "component", "cluster", "runs", runs, "revisions", revs, "keys", len(drop))
	return nil
}
