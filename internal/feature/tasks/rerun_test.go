package featuretasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
)

const rerunID = "20261101-130000-0a0b0c"

// startRerun records re as a run's start would and runs it to its end.
func startRerun(t *testing.T, e *fakeEnv, re cluster.RunRecord) {
	t.Helper()
	re.ID, re.StartedBy, re.StartedAt = rerunID, "operator", e.now
	if err := e.store.PutRun(re); err != nil {
		t.Fatal(err)
	}
	for _, id := range re.Nodes {
		if err := e.store.PutRunNode(re.ID, id, cluster.RunNodeRecord{Status: cluster.RunNodePending}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runTask(context.Background(), e, &re); err != nil {
		t.Fatalf("runTask: %v", err)
	}
}

func stepsOf(statuses ...string) []cluster.RunStepRecord {
	var out []cluster.RunStepRecord
	for _, s := range statuses {
		out = append(out, cluster.RunStepRecord{Status: s})
	}
	return out
}

func TestRerunStartsEachNodeAtItsFirstUnsuccessfulStep(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"),
		agent("a1", "web-1"), agent("a2", "web-2"), agent("a3", "web-3"), agent("a4", "web-4"), agent("a5", "web-5"))
	src := newRun(t, e, steps(false, cmdTrue, cmdTrue, cmdTrue), [][]string{{"a1", "a2"}, {"a3"}, {"a4"}, {"a5"}},
		func(r *cluster.RunRecord) { r.Status = cluster.RunFailed })
	ok, failed, unknown, pending, skipped := cluster.RunStepSucceeded, cluster.RunStepFailed, cluster.RunStepUnknown, cluster.RunStepPending, cluster.RunStepSkipped
	nodes := map[string]cluster.RunNodeRecord{
		"a1": {Status: cluster.RunNodeFailed, Steps: stepsOf(ok, failed, pending)},
		"a2": {Status: cluster.RunNodeUnknown, Steps: stepsOf(ok, ok, unknown)},
		"a3": {Status: cluster.RunNodeSucceeded, Steps: stepsOf(ok, ok, ok)},
		"a4": {Status: cluster.RunNodeStopped, Reason: "the run ended before the node's turn"},
		"a5": {Status: cluster.RunNodeSkipped, Steps: stepsOf(skipped, pending, pending)},
	}

	re, startAt := rerunFrom(*src, nodes, []string{"a4", "a3", "a2", "a1", "a5", "not-in-the-run"})
	wantStart := map[string]int{"a1": 1, "a2": 2, "a3": 0, "a4": 0, "a5": 0}
	if !maps.Equal(startAt, wantStart) {
		t.Errorf("start steps = %v, want %v", startAt, wantStart)
	}
	if !maps.Equal(re.StartAt, wantStart) {
		t.Errorf("the rerun records start steps %v, want %v", re.StartAt, wantStart)
	}
	if want := []string{"a1", "a2", "a3", "a4", "a5"}; !reflect.DeepEqual(re.Nodes, want) {
		t.Errorf("nodes = %v, want %v", re.Nodes, want)
	}
	if want := [][]string{{"a1", "a2"}, {"a3"}, {"a4"}, {"a5"}}; !reflect.DeepEqual(re.Waves, want) {
		t.Errorf("waves = %v, want %v", re.Waves, want)
	}

	only, _ := rerunFrom(*src, nodes, []string{"a2"})
	if want := [][]string{{"a2"}}; !reflect.DeepEqual(only.Waves, want) {
		t.Errorf("a rerun of a2 alone has waves %v, want %v", only.Waves, want)
	}

	startRerun(t, e, re)
	wantStarted(t, e, "a1/1", "a1/2", "a2/2", "a3/0", "a3/1", "a3/2", "a4/0", "a4/1", "a4/2", "a5/0", "a5/1", "a5/2")
	a1 := e.store.node(rerunID, "a1")
	if a1.Status != cluster.RunNodeSucceeded {
		t.Errorf("a1 = %s (%q), want succeeded", a1.Status, a1.Reason)
	}
	wantStep(t, a1, 0, cluster.RunStepSucceeded, "in run "+testRunID)
	wantStep(t, a1, 1, cluster.RunStepSucceeded, "")
	a2 := e.store.node(rerunID, "a2")
	wantStep(t, a2, 1, cluster.RunStepSucceeded, "in run "+testRunID)
	if h := e.store.head(rerunID); h.Status != cluster.RunSucceeded {
		t.Errorf("rerun = %s (%q), want succeeded", h.Status, h.Reason)
	}
}

func TestRerunUsesTheRunsOwnRevisionAndInputs(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	doc := "name: greet\ninputs:\n  word: { required: true }\nsteps:\n  - command: { run: \"echo first {{ word }}\" }\n"
	ended := time.Date(2026, 11, 1, 12, 5, 0, 0, time.UTC)
	src := newRun(t, e, doc, [][]string{{"a1"}}, func(r *cluster.RunRecord) {
		r.Inputs = map[string]string{"word": "hello"}
		r.Confirmed, r.Batch, r.StopOnFailure = []int{0}, 5, true
		r.Status, r.Reason, r.Force, r.EndedAt = cluster.RunStopped, "stopped by the operator", true, ended
	})
	// The task has been edited since: the rerun still runs what ran then.
	edited := strings.Replace(doc, "first", "second", 1)
	sum := sha256.Sum256([]byte(edited))
	e.store.revs[hex.EncodeToString(sum[:])] = edited

	re, _ := rerunFrom(*src, map[string]cluster.RunNodeRecord{
		"a1": {Status: cluster.RunNodeFailed, Steps: stepsOf(cluster.RunStepFailed)},
	}, []string{"a1"})
	src.Inputs["word"] = "changed" // the rerun keeps its own copy

	if re.Rev != src.Rev || re.TaskID != src.TaskID || re.TaskName != src.TaskName {
		t.Errorf("rerun = task %s %q rev %s, want task %s %q rev %s", re.TaskID, re.TaskName, re.Rev, src.TaskID, src.TaskName, src.Rev)
	}
	if want := map[string]string{"word": "hello"}; !maps.Equal(re.Inputs, want) {
		t.Errorf("inputs = %v, want %v", re.Inputs, want)
	}
	if !reflect.DeepEqual(re.Confirmed, []int{0}) || re.Batch != 5 || !re.StopOnFailure {
		t.Errorf("confirmed %v, batch %d, stop on failure %v; want [0], 5, true", re.Confirmed, re.Batch, re.StopOnFailure)
	}
	if re.RerunOf != testRunID {
		t.Errorf("rerun of %q, want %q", re.RerunOf, testRunID)
	}
	if re.ID != "" || re.Status != cluster.RunRunning || re.Reason != "" || re.Force || !re.EndedAt.IsZero() {
		t.Errorf("rerun starts as id %q, %s (%q), force %v, ended %v; want no ID, running, no reason, no force, not ended",
			re.ID, re.Status, re.Reason, re.Force, re.EndedAt)
	}

	startRerun(t, e, re)
	spec := e.specs[key("a1", 0)]
	if spec.Command == nil || spec.Command.Run != "echo first 'hello'" {
		t.Errorf("a1 ran %+v, want the run's own document with its own input: echo first 'hello'", spec.Command)
	}
	if !spec.Confirmed {
		t.Error("the step confirmed at the first run's start was not confirmed in its rerun")
	}
}
