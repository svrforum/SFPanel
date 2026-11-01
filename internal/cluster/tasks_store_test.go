package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/tasks"
)

// The task store lists its keys by prefix, and the config map holds the
// cluster's secrets beside them: what comes back is that prefix only, and a
// copy the caller may change.
func TestConfigWithPrefixCopiesOnlyThatPrefix(t *testing.T) {
	f := NewFSM()
	for k, v := range map[string]string{
		"jwt_secret":         "secret",
		"cluster_ca_key":     "key",
		"task/a":             "{}",
		"taskrev/abc":        "name: t",
		"taskrun/20261101-x": "{}",
		"task":               "not under the prefix",
	} {
		f.state.Config[k] = v
	}
	got := f.ConfigWithPrefix("task/")
	if want := map[string]string{"task/a": "{}"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigWithPrefix(task/) = %v, want %v", got, want)
	}
	if _, ok := got["jwt_secret"]; ok {
		t.Fatal("the JWT secret came back")
	}
	got["task/a"] = "changed"
	if v, _ := f.GetConfigValue("task/a"); v != "{}" {
		t.Fatalf("changing the result changed the FSM: %q", v)
	}
}

// taskLeader is a one-manager cluster's leader, for the store's writes.
func taskLeader(t *testing.T) *Manager {
	t.Helper()
	return soleLeader(t)
}

func TestATaskOver64KiBIsRefused(t *testing.T) {
	m := taskLeader(t)
	at := strings.Repeat("a", tasks.MaxDocumentBytes)
	if err := m.PutTask(StoredTask{ID: NewTaskID(), Name: "at the limit", YAML: at}); err != nil {
		t.Fatalf("a task of exactly 64 KiB: %v", err)
	}
	id := NewTaskID()
	err := m.PutTask(StoredTask{ID: id, Name: "over", YAML: at + "a"})
	if !errors.Is(err, ErrTaskTooLarge) {
		t.Fatalf("a task one byte over 64 KiB: %v, want ErrTaskTooLarge", err)
	}
	if _, err := m.GetTask(id); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("the refused task was stored: %v", err)
	}
	if _, err := m.PutTaskRevision(at + "a"); !errors.Is(err, ErrTaskTooLarge) {
		t.Fatalf("a revision one byte over 64 KiB: %v, want ErrTaskTooLarge", err)
	}
}

// Two tasks may not share a name; a task keeps its own when it is saved
// again, and a renamed one frees the old name.
func TestTaskNamesAreUnique(t *testing.T) {
	m := taskLeader(t)
	first, second := NewTaskID(), NewTaskID()
	if err := m.PutTask(StoredTask{ID: first, Name: "deploy", YAML: "name: deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := m.PutTask(StoredTask{ID: second, Name: "deploy", YAML: "name: deploy"}); !errors.Is(err, ErrTaskNameTaken) {
		t.Fatalf("a second task named deploy: %v, want ErrTaskNameTaken", err)
	}
	if err := m.PutTask(StoredTask{ID: first, Name: "deploy", YAML: "name: deploy\n"}); err != nil {
		t.Fatalf("saving deploy again under its own ID: %v", err)
	}
	if err := m.PutTask(StoredTask{ID: first, Name: "deploy-old", YAML: "name: deploy-old"}); err != nil {
		t.Fatal(err)
	}
	if err := m.PutTask(StoredTask{ID: second, Name: "deploy", YAML: "name: deploy"}); err != nil {
		t.Fatalf("deploy once its first task was renamed: %v", err)
	}
	list, err := m.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, st := range list {
		names = append(names, st.Name)
	}
	if want := []string{"deploy", "deploy-old"}; !slices.Equal(names, want) {
		t.Fatalf("tasks %v, want %v", names, want)
	}
}

// A task's ID is a key segment: one carrying a slash would land under
// another prefix, or under a built-in's "builtin/<name>".
func TestATaskIDMustBeAKeySegment(t *testing.T) {
	m := taskLeader(t)
	for _, id := range []string{"", "a/b", "builtin/command", "../x", strings.Repeat("a", 65)} {
		if err := m.PutTask(StoredTask{ID: id, Name: "n" + id, YAML: "name: x"}); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("ID %q: %v, want ErrInvalidTaskID", id, err)
		}
	}
	if id := NewTaskID(); !taskIDPattern.MatchString(id) {
		t.Fatalf("NewTaskID() = %q is not a valid ID", id)
	}
}

func TestATaskIsReadBackAndDeleted(t *testing.T) {
	m := taskLeader(t)
	in := StoredTask{ID: NewTaskID(), Name: "web", YAML: "name: web",
		Source:    TaskSource{Kind: TaskSourceGit, Repo: "https://example.com/ops.git", Branch: "main", Path: "web.yaml", SHA: "abc"},
		UpdatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), UpdatedBy: "admin"}
	if err := m.PutTask(in); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetTask(in.ID)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("GetTask = %+v, %v; want %+v", got, err, in)
	}
	if err := m.DeleteTask(in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetTask(in.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("after the delete: %v", err)
	}
	if err := m.DeleteTask(in.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("deleting it again: %v", err)
	}
	if err := m.PutTask(StoredTask{ID: NewTaskID(), Name: "x", YAML: "y", Source: TaskSource{Kind: "ftp"}}); !errors.Is(err, ErrInvalidTaskSource) {
		t.Fatalf("a source kind neither user nor git: %v", err)
	}
}

// The same document is stored once, under its content hash: the second put
// writes nothing to the Raft log.
func TestARevisionIsStoredOnce(t *testing.T) {
	m := taskLeader(t)
	doc := "name: t\nsteps:\n  - command: { run: \"true\" }\n"
	sha, err := m.PutTaskRevision(doc)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(doc))
	if sha != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha %s is not the document's sha256", sha)
	}
	before := m.raft.raft.LastIndex()
	again, err := m.PutTaskRevision(doc)
	if err != nil || again != sha {
		t.Fatalf("the second put: %s, %v", again, err)
	}
	if after := m.raft.raft.LastIndex(); after != before {
		t.Fatalf("the second put wrote to the log: index %d -> %d", before, after)
	}
	got, err := m.TaskRevision(sha)
	if err != nil || got != doc {
		t.Fatalf("TaskRevision = %q, %v", got, err)
	}
	if _, err := m.TaskRevision(strings.Repeat("0", 64)); !errors.Is(err, ErrTaskRevisionNotFound) {
		t.Fatalf("an unknown revision: %v", err)
	}
}

func TestARunIsReadBackWithItsNodes(t *testing.T) {
	m := taskLeader(t)
	start := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	run := RunRecord{ID: NewRunID(start), TaskID: "t1", TaskName: "web", Rev: "abc",
		Inputs: map[string]string{"pkg": "nginx"}, Nodes: []string{"a1", "m2"}, Waves: [][]string{{"a1"}, {"m2"}},
		Confirmed: []int{1}, Batch: 5, StopOnFailure: true, Status: RunRunning, StartedBy: "admin", StartedAt: start,
		StartAt: map[string]int{"a1": 1}}
	if err := m.PutRunNode(run.ID, "a1", RunNodeRecord{Status: RunNodePending}); !errors.Is(err, ErrTaskRunNotFound) {
		t.Fatalf("a node record before its run: %v, want ErrTaskRunNotFound", err)
	}
	if err := m.PutRun(run); err != nil {
		t.Fatal(err)
	}
	node := RunNodeRecord{Status: RunNodeRunning, Steps: []RunStepRecord{
		{Status: RunStepSucceeded, JobID: run.ID + "-0", Summary: "ok", Changed: true, StartedAt: start, EndedAt: start.Add(time.Second)},
		{Status: RunStepStarting, JobID: run.ID + "-1"},
	}}
	if err := m.PutRunNode(run.ID, "a1", node); err != nil {
		t.Fatal(err)
	}
	got, nodes, err := m.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, run) {
		t.Fatalf("run %+v, want %+v", got, run)
	}
	if want := map[string]RunNodeRecord{"a1": node}; !reflect.DeepEqual(nodes, want) {
		t.Fatalf("nodes %+v, want %+v", nodes, want)
	}
	if _, _, err := m.GetRun(NewRunID(start)); !errors.Is(err, ErrTaskRunNotFound) {
		t.Fatalf("an unknown run: %v", err)
	}
	if err := m.PutRun(RunRecord{ID: "a/b"}); !errors.Is(err, ErrInvalidRunID) {
		t.Fatalf("a run ID with a slash: %v", err)
	}
	if err := m.PutRunNode(run.ID, "x/n/y", node); !errors.Is(err, ErrInvalidRunID) {
		t.Fatalf("a node ID with a slash: %v", err)
	}
}

// Run IDs sort by when they started, which is what ListRuns and PruneRuns
// order by.
func TestRunIDsSortByTime(t *testing.T) {
	base := time.Date(2026, 11, 1, 23, 59, 59, 0, time.FixedZone("KST", 9*3600))
	earlier, later := NewRunID(base), NewRunID(base.Add(time.Second))
	if !regexp.MustCompile(`^20261101-145959-[0-9a-f]{6}$`).MatchString(earlier) {
		t.Fatalf("run ID %q is not YYYYMMDD-HHMMSS-<6hex> in UTC", earlier)
	}
	if earlier >= later {
		t.Fatalf("%s does not sort before %s", earlier, later)
	}
}

// Of 203 runs the 200 newest stay. The three oldest go with their node
// records, and so does the revision only they used; a revision a kept run
// still uses stays, and so does one a run being started has just written.
func TestPruningKeepsTheNewest200AndDropsUnusedRevisions(t *testing.T) {
	m := taskLeader(t)
	oldRev, err := m.PutTaskRevision("name: old")
	if err != nil {
		t.Fatal(err)
	}
	sharedRev, err := m.PutTaskRevision("name: shared")
	if err != nil {
		t.Fatal(err)
	}
	keptRev, err := m.PutTaskRevision("name: kept")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.PutTask(StoredTask{ID: NewTaskID(), Name: "t", YAML: "name: t"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig("jwt_secret", "s"); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 203; i++ {
		rev := keptRev
		switch {
		case i < 2:
			rev = oldRev
		case i == 2 || i == 3:
			rev = sharedRev
		}
		r := RunRecord{ID: NewRunID(base.Add(time.Duration(i) * time.Minute)), Rev: rev, Status: RunSucceeded}
		if err := m.PutRun(r); err != nil {
			t.Fatal(err)
		}
		if err := m.PutRunNode(r.ID, "n1", RunNodeRecord{Status: RunNodeSucceeded}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	// A run being started writes its revision before its record; pruning
	// in between must not take the revision from under it.
	revisionGrace = time.Hour
	t.Cleanup(func() { revisionGrace = 10 * time.Minute })
	starting, err := m.PutTaskRevision("name: starting")
	if err != nil {
		t.Fatal(err)
	}
	// Written before the grace started counting for this test.
	m.taskMu.Lock()
	for _, sha := range []string{oldRev, sharedRev, keptRev} {
		m.taskRevWritten[sha] = time.Now().Add(-2 * time.Hour)
	}
	m.taskMu.Unlock()

	if err := m.PruneRuns(200); err != nil {
		t.Fatal(err)
	}
	runs, err := m.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, r.ID)
	}
	want := slices.Clone(ids[3:])
	slices.Reverse(want)
	if !slices.Equal(got, want) {
		t.Fatalf("kept %d runs, first %v; want the 200 newest, newest first", len(got), got[:min(3, len(got))])
	}
	for _, id := range ids[:3] {
		if left := m.raft.GetFSM().ConfigWithPrefix(runKey(id)); len(left) != 0 {
			t.Fatalf("pruned run %s left %v", id, keysOf(left))
		}
	}
	if _, nodes, err := m.GetRun(ids[3]); err != nil || len(nodes) != 1 {
		t.Fatalf("the oldest kept run: %v, %d node records", err, len(nodes))
	}
	if _, err := m.TaskRevision(oldRev); !errors.Is(err, ErrTaskRevisionNotFound) {
		t.Fatalf("the revision only pruned runs used: %v", err)
	}
	for name, sha := range map[string]string{"shared": sharedRev, "kept": keptRev, "starting": starting} {
		if _, err := m.TaskRevision(sha); err != nil {
			t.Fatalf("the %s revision: %v", name, err)
		}
	}
	if list, _ := m.ListTasks(); len(list) != 1 {
		t.Fatalf("the task definitions: %v", list)
	}
	if v, _ := m.raft.GetFSM().GetConfigValue("jwt_secret"); v != "s" {
		t.Fatal("pruning touched a key outside the task store")
	}

	// Once the grace has passed, the revision no run took is dropped too.
	revisionGrace = 0
	if err := m.PruneRuns(200); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TaskRevision(starting); !errors.Is(err, ErrTaskRevisionNotFound) {
		t.Fatalf("a revision no run used, past the grace: %v", err)
	}
	if _, err := m.TaskRevision(keptRev); err != nil {
		t.Fatalf("the kept revision: %v", err)
	}
}

// A run that has not ended stays however many runs started after it: its
// runner is still writing to it, and a new leader takes it over from it.
func TestPruningNeverDropsARunThatHasNotEnded(t *testing.T) {
	m := taskLeader(t)
	revisionGrace = 0
	t.Cleanup(func() { revisionGrace = 10 * time.Minute })
	rev, err := m.PutTaskRevision("name: long")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	statuses := []string{RunRunning, RunSucceeded, RunStopping, RunHandingOver, RunFailed}
	for i, st := range statuses {
		r := RunRecord{ID: NewRunID(base.Add(time.Duration(i) * time.Minute)), Rev: rev, Status: st}
		if err := m.PutRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.PruneRuns(1); err != nil {
		t.Fatal(err)
	}
	runs, err := m.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, r.Status)
	}
	if want := []string{RunFailed, RunHandingOver, RunStopping, RunRunning}; !slices.Equal(got, want) {
		t.Fatalf("kept %v, want the newest and every run not ended %v", got, want)
	}
	if _, err := m.TaskRevision(rev); err != nil {
		t.Fatalf("the revision of a run not ended: %v", err)
	}
}

func TestManagersAndAgentsReportCapTasks(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if !slices.Contains(m.heartbeatCapabilities(), CapTasks) {
		t.Fatalf("a manager's heartbeat capabilities %v lack %s", m.heartbeatCapabilities(), CapTasks)
	}
	l := &AgentLink{AgentLinkConfig: AgentLinkConfig{NodeID: "a1", TLS: NewTLSManager(t.TempDir())}}
	if caps := l.ping().Capabilities; !slices.Contains(caps, CapTasks) {
		t.Fatalf("an agent's ping capabilities %v lack %s", caps, CapTasks)
	}
}

// Tasks open once every other manager is online and reports CapTasks; the
// ones they wait for are named, with their version. Agents do not hold them
// back: a run leaves an agent without the capability out instead.
func TestTasksWaitForEveryManager(t *testing.T) {
	m := soleLeader(t, "m2", "m3")
	if err := m.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "a1", Name: "a1", Kind: KindAgent})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "0.81.0", Capabilities: []string{CapTasks}})
	m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: "a1", Version: "0.80.0"})
	if got := m.TasksReady(); !slices.Equal(got, []string{"m3 (not heard from)"}) {
		t.Fatalf("TasksReady() = %v", got)
	}
	m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: "m3", Version: "0.80.0"})
	if got := m.TasksReady(); !slices.Equal(got, []string{"m3 (0.80.0)"}) {
		t.Fatalf("TasksReady() = %v, want m3 named with its version", got)
	}
	m.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: "m3", Version: "0.81.0", Capabilities: []string{CapTasks}})
	if got := m.TasksReady(); len(got) != 0 {
		t.Fatalf("TasksReady() = %v once every manager reports %s", got, CapTasks)
	}
}

// Writes go through the leader; every manager reads its own replicated
// copy.
func TestWritesNeedTheLeaderAndReadsWorkOnAnyManager(t *testing.T) {
	mgrs := inmemManagers(t, 2)
	var leader, follower *Manager
	waitFor(t, "a leader", func() bool {
		for i, m := range mgrs {
			if m.raft.IsLeader() {
				leader, follower = m, mgrs[1-i]
				return true
			}
		}
		return false
	})
	id := NewTaskID()
	if err := leader.PutTask(StoredTask{ID: id, Name: "web", YAML: "name: web"}); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{ID: NewRunID(time.Now()), Status: RunRunning}
	if err := leader.PutRun(run); err != nil {
		t.Fatal(err)
	}
	writes := map[string]func() error{
		"PutTask":         func() error { return follower.PutTask(StoredTask{ID: NewTaskID(), Name: "x", YAML: "y"}) },
		"DeleteTask":      func() error { return follower.DeleteTask(id) },
		"PutTaskRevision": func() error { _, err := follower.PutTaskRevision("name: x"); return err },
		"PutRun":          func() error { return follower.PutRun(run) },
		"PutRunNode":      func() error { return follower.PutRunNode(run.ID, "n1", RunNodeRecord{}) },
		"PruneRuns":       func() error { return follower.PruneRuns(200) },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, ErrNotLeader) {
			t.Errorf("%s on a follower: %v, want ErrNotLeader", name, err)
		}
	}
	waitFor(t, "the follower to apply the writes", func() bool {
		_, err := follower.GetTask(id)
		_, _, runErr := follower.GetRun(run.ID)
		return err == nil && runErr == nil
	})
	none := &Manager{}
	if err := none.PutTask(StoredTask{ID: NewTaskID(), Name: "x", YAML: "y"}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("a node with no Raft: %v, want ErrNotLeader", err)
	}
	if _, err := none.ListTasks(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("reading on a node with no Raft: %v, want ErrNotInitialized", err)
	}
}

// An install row is read back with everything the screen words it by: its
// server, the node it became, what it waits on, and its reason's details.
func TestAnInstallRowKeepsItsReasonDetails(t *testing.T) {
	m := taskLeader(t)
	start := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	run := RunRecord{ID: NewRunID(start), Kind: RunKindAgentInstall, TaskID: AgentInstallTaskID, TaskName: "agent install",
		Nodes: []string{"s1"}, Waves: [][]string{{"s1"}}, Batch: 3, Status: RunFailed, StartedBy: "admin", StartedAt: start,
		EndedAt: start.Add(time.Minute), CredentialsUntil: start.Add(11 * time.Minute)}
	if err := m.PutRun(run); err != nil {
		t.Fatal(err)
	}
	row := RunNodeRecord{Status: RunNodeFailed, Reason: "AGENT_INSTALL_PORT_BUSY",
		ReasonDetails: map[string]string{"port": "3628", "owner": "nginx"},
		Host:          "web-1.example", IP: "192.0.2.10", Name: "web-1", Group: "web", NodeID: "a9",
		HostKey: "ED25519 SHA256:abc", Steps: []RunStepRecord{{Status: RunStepFailed, JobID: "install/s1/1"}}}
	if err := m.PutRunNode(run.ID, "s1", row); err != nil {
		t.Fatal(err)
	}
	got, rows, err := m.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, run) {
		t.Fatalf("run %+v, want %+v", got, run)
	}
	if !reflect.DeepEqual(rows["s1"], row) {
		t.Fatalf("row %+v, want %+v", rows["s1"], row)
	}
}
