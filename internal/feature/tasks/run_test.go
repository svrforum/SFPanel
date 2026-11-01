package featuretasks

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	cmdTrue  = `command: { run: "true" }`
	cmdFalse = `command: { run: "false" }`
	pkgsStep = `packages: { update: true }`
)

func mustRun(t *testing.T, e *fakeEnv, run *cluster.RunRecord) {
	t.Helper()
	if err := runTask(context.Background(), e, run); err != nil {
		t.Fatalf("runTask: %v", err)
	}
}

func wantStarted(t *testing.T, e *fakeEnv, want ...string) {
	t.Helper()
	got := e.startedList()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("jobs started = %v, want %v", got, want)
	}
}

func wantNode(t *testing.T, e *fakeEnv, id, status, reason string) cluster.RunNodeRecord {
	t.Helper()
	rec := e.store.node(testRunID, id)
	if rec.Status != status || !strings.Contains(rec.Reason, reason) {
		t.Errorf("node %s = %s (%q), want %s with a reason containing %q", id, rec.Status, rec.Reason, status, reason)
	}
	return rec
}

func wantStep(t *testing.T, rec cluster.RunNodeRecord, i int, status, summary string) {
	t.Helper()
	if i >= len(rec.Steps) {
		t.Errorf("step %d not recorded: %+v", i, rec.Steps)
		return
	}
	s := rec.Steps[i]
	if s.Status != status || !strings.Contains(s.Summary, summary) {
		t.Errorf("step %d = %s (%q), want %s with a summary containing %q", i, s.Status, s.Summary, status, summary)
	}
}

func wantHead(t *testing.T, e *fakeEnv, status, reason string) cluster.RunRecord {
	t.Helper()
	h := e.store.head(testRunID)
	if h.Status != status || !strings.Contains(h.Reason, reason) {
		t.Errorf("run = %s (%q), want %s with a reason containing %q", h.Status, h.Reason, status, reason)
	}
	return h
}

func TestAFailedStepStopsThatNode(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"), agent("a2", "web-2"))
	e.outcome[key("a1", 0)] = tasks.JobView{Status: tasks.JobFailed, Summary: "exit status 1"}
	run := newRun(t, e, steps(true, cmdFalse, cmdTrue), [][]string{{"a1", "a2"}})
	mustRun(t, e, run)

	wantStarted(t, e, "a1/0", "a2/0", "a2/1")
	rec := wantNode(t, e, "a1", cluster.RunNodeFailed, "step 1 (command) failed")
	wantStep(t, rec, 0, cluster.RunStepFailed, "exit status 1")
	wantStep(t, rec, 1, cluster.RunStepPending, "")
	rec = wantNode(t, e, "a2", cluster.RunNodeSucceeded, "")
	wantStep(t, rec, 1, cluster.RunStepSucceeded, "")
	wantHead(t, e, cluster.RunFailed, "")
}

func TestContinueOnErrorGoesOn(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	e.outcome[key("a1", 0)] = tasks.JobView{Status: tasks.JobFailed, Summary: "exit status 1"}
	run := newRun(t, e, steps(true, "{ continue_on_error: true, "+cmdFalse+" }", cmdTrue), [][]string{{"a1"}})
	mustRun(t, e, run)

	wantStarted(t, e, "a1/0", "a1/1")
	rec := wantNode(t, e, "a1", cluster.RunNodeSucceeded, "")
	wantStep(t, rec, 0, cluster.RunStepFailed, "exit status 1")
	wantStep(t, rec, 1, cluster.RunStepSucceeded, "")
	wantHead(t, e, cluster.RunSucceeded, "")
}

func TestStopOnFailureStartsNoNewBatch(t *testing.T) {
	setup := func(t *testing.T, stop bool) *fakeEnv {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), manager("m2", "mgr-2"), agent("a1", "web-1"), agent("a2", "web-2"), agent("a3", "web-3"))
		e.outcome[key("a1", 0)] = tasks.JobView{Status: tasks.JobFailed, Summary: "exit status 1"}
		e.polls[key("a2", 0)] = 3 // a2 is still running when a1 fails
		run := newRun(t, e, steps(stop, cmdTrue), [][]string{{"a1", "a2"}, {"a3"}, {"m2"}})
		mustRun(t, e, run)
		return e
	}
	t.Run("on: the wave finishes and nothing after it starts", func(t *testing.T) {
		e := setup(t, true)
		wantStarted(t, e, "a1/0", "a2/0")
		wantNode(t, e, "a2", cluster.RunNodeSucceeded, "")
		wantNode(t, e, "a3", cluster.RunNodeStopped, "stops on failure")
		wantNode(t, e, "m2", cluster.RunNodeStopped, "stops on failure")
		wantHead(t, e, cluster.RunFailed, "stops on failure")
	})
	t.Run("off: every wave runs", func(t *testing.T) {
		e := setup(t, false)
		wantStarted(t, e, "a1/0", "a2/0", "a3/0", "m2/0")
		wantHead(t, e, cluster.RunFailed, "")
	})
}

func TestStopStartsNoNewStepAndLeavesRunningJobs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		step  string
		force bool
	}{
		{"a stop leaves a command job to end", cmdTrue, false},
		{"a forced stop leaves a package job to end", pkgsStep, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"), agent("a2", "web-2"))
			e.polls[key("a1", 0)] = 3
			e.onPoll = func(node string, step, poll int) {
				if node == "a1" && step == 0 && poll == 1 {
					e.store.setHead(testRunID, func(h *cluster.RunRecord) { h.Status, h.Force = cluster.RunStopping, tc.force })
				}
			}
			run := newRun(t, e, steps(true, tc.step, cmdTrue), [][]string{{"a1"}, {"a2"}})
			mustRun(t, e, run)

			if c := e.cancelList(); len(c) != 0 {
				t.Errorf("jobs cancelled = %v, want none", c)
			}
			wantStarted(t, e, "a1/0")
			rec := wantNode(t, e, "a1", cluster.RunNodeStopped, reasonStopped)
			wantStep(t, rec, 0, cluster.RunStepSucceeded, "")
			wantStep(t, rec, 1, cluster.RunStepPending, "")
			wantNode(t, e, "a2", cluster.RunNodeStopped, reasonStopped)
			if h := wantHead(t, e, cluster.RunStopped, reasonStopped); h.EndedAt.IsZero() {
				t.Error("the stopped run has no end time")
			}
		})
	}
}

func TestForceStopCancelsCommandJobs(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	e.polls[key("a1", 0)] = 100
	e.onPoll = func(node string, step, poll int) {
		if poll == 1 {
			e.store.setHead(testRunID, func(h *cluster.RunRecord) { h.Status, h.Force = cluster.RunStopping, true })
		}
	}
	run := newRun(t, e, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}})
	mustRun(t, e, run)

	if c := e.cancelList(); !slices.Equal(c, []string{"a1/0"}) {
		t.Errorf("jobs cancelled = %v, want [a1/0]", c)
	}
	wantStarted(t, e, "a1/0")
	rec := wantNode(t, e, "a1", cluster.RunNodeStopped, reasonStopped)
	wantStep(t, rec, 0, cluster.RunStepCancelled, "")
	wantHead(t, e, cluster.RunStopped, reasonStopped)
}

func TestTheJobIDIsRecordedBeforeTheJobStarts(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"), agent("a2", "web-2"))
	var seen []string
	e.onStart = func(node string, spec tasks.JobSpec) {
		if spec.ID != jobID(testRunID, spec.Step) || spec.RunID != testRunID {
			t.Errorf("%s step %d posted as job %q of run %q", node, spec.Step, spec.ID, spec.RunID)
		}
		rec := e.store.node(testRunID, node)
		if spec.Step >= len(rec.Steps) {
			t.Errorf("%s step %d posted with no step recorded", node, spec.Step)
			return
		}
		s := rec.Steps[spec.Step]
		if s.Status != cluster.RunStepStarting || s.JobID != spec.ID {
			t.Errorf("%s step %d posted while recorded as %s with job %q, want starting with %q", node, spec.Step, s.Status, s.JobID, spec.ID)
		}
		seen = append(seen, key(node, spec.Step))
	}
	run := newRun(t, e, steps(true, cmdTrue, cmdTrue), [][]string{{"a1"}, {"a2"}})
	mustRun(t, e, run)
	if len(seen) != 4 {
		t.Errorf("posts seen = %v, want four", seen)
	}
	rec := wantNode(t, e, "a2", cluster.RunNodeSucceeded, "")
	if len(rec.Steps) != 2 || rec.Steps[1].JobID != testRunID+"-1" {
		t.Errorf("a2's steps = %+v, want step 2 with job %s-1", rec.Steps, testRunID)
	}
}

func TestARebootOfAManagerThatWouldBreakQuorumIsSkippedAndStopsTheRun(t *testing.T) {
	setup := func(t *testing.T, m3 cluster.NodeStatus) *fakeEnv {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), manager("m2", "mgr-2"), manager("m3", "mgr-3"), manager("m4", "mgr-4"))
		e.health["m3"] = m3
		run := newRun(t, e, steps(true, `reboot: { only_if_required: false }`), [][]string{{"m2"}, {"m4"}, {"m1"}})
		mustRun(t, e, run)
		return e
	}
	t.Run("one voter down: rebooting another leaves two of four", func(t *testing.T) {
		e := setup(t, cluster.StatusOffline)
		wantStarted(t, e)
		rec := wantNode(t, e, "m2", cluster.RunNodeSkipped, reasonQuorum)
		wantStep(t, rec, 0, cluster.RunStepSkipped, reasonQuorum)
		wantNode(t, e, "m4", cluster.RunNodeStopped, reasonQuorum)
		wantHead(t, e, cluster.RunStopped, "quorum: rebooting mgr-2")
		if len(e.handedAt) != 0 {
			t.Error("the leader handed over after the run stopped")
		}
	})
	t.Run("every voter up: three of four stay, and it reboots", func(t *testing.T) {
		e := setup(t, cluster.StatusOnline)
		e2 := e.startedList()
		if !slices.Contains(e2, "m2/0") || !slices.Contains(e2, "m4/0") {
			t.Errorf("jobs started = %v, want m2's and m4's reboots", e2)
		}
	})
}

func TestARebootWaitsForTheNodeToComeBack(t *testing.T) {
	t.Run("it comes back: the next step runs", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		e.getErrs[key("a1", 0)] = 5 // down while it boots
		e.polls[key("a1", 0)] = 3
		run := newRun(t, e, steps(true, `reboot: { only_if_required: false, wait: 10m }`, cmdTrue), [][]string{{"a1"}})
		mustRun(t, e, run)
		wantStarted(t, e, "a1/0", "a1/1")
		rec := wantNode(t, e, "a1", cluster.RunNodeSucceeded, "")
		wantStep(t, rec, 0, cluster.RunStepSucceeded, "")
	})
	for _, tc := range []struct {
		name    string
		getErrs int
	}{
		{"it never answers again", 1 << 20},
		{"it answers but never reboots", 0},
	} {
		t.Run(tc.name+": the step fails once the wait is over", func(t *testing.T) {
			e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
			e.getErrs[key("a1", 0)] = tc.getErrs
			e.polls[key("a1", 0)] = 1 << 20
			start := e.now
			run := newRun(t, e, steps(true, `reboot: { only_if_required: false, wait: 1m }`, cmdTrue), [][]string{{"a1"}})
			mustRun(t, e, run)
			wantStarted(t, e, "a1/0")
			rec := wantNode(t, e, "a1", cluster.RunNodeFailed, "step 1")
			wantStep(t, rec, 0, cluster.RunStepFailed, "did not come back within 1m0s")
			if waited := e.now.Sub(start); waited < time.Minute || waited > time.Minute+2*pollInterval {
				t.Errorf("waited %v for a one-minute reboot", waited)
			}
		})
	}
}

func TestTheLeaderHandsOverBeforeItsOwnSteps(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), manager("m2", "mgr-2"), agent("a1", "web-1"))
	e.handTo = "m2"
	run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}, {"m2"}, {"m1"}})
	mustRun(t, e, run)

	wantStarted(t, e, "a1/0", "m2/0")
	if !slices.Equal(e.handedAt, []string{cluster.RunHandingOver}) {
		t.Errorf("the run's head as the leadership was handed over = %v, want [%s]", e.handedAt, cluster.RunHandingOver)
	}
	wantHead(t, e, cluster.RunHandingOver, "")
	wantNode(t, e, "m1", cluster.RunNodePending, "")
	if len(e.store.pruned) != 0 {
		t.Error("a run handed over was ended and pruned")
	}
}

func TestALoneManagerRunsItsOwnStepsItself(t *testing.T) {
	t.Run("no voter to take over: the leader runs its steps", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}, {"m1"}})
		mustRun(t, e, run)
		wantStarted(t, e, "a1/0", "m1/0")
		wantNode(t, e, "m1", cluster.RunNodeSucceeded, "")
		wantHead(t, e, cluster.RunSucceeded, "")
	})
	t.Run("a handover that fails runs nothing on the leader", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), manager("m2", "mgr-2"))
		e.handTo, e.handErr = "m2", errors.New("timed out")
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"m2"}, {"m1"}})
		mustRun(t, e, run)
		wantStarted(t, e, "m2/0")
		wantNode(t, e, "m1", cluster.RunNodeFailed, "leadership could not be handed over to mgr-2: timed out")
		wantHead(t, e, cluster.RunFailed, "")
	})
}

func TestAProxmoxHostsRebootStepIsSkippedNotFailed(t *testing.T) {
	for _, tc := range []struct {
		code       string
		node, step string
	}{
		{response.ErrTaskRebootPVEHost, cluster.RunNodeSucceeded, cluster.RunStepSkipped},
		{response.ErrTaskRebootUnsupported, cluster.RunNodeFailed, cluster.RunStepFailed},
	} {
		t.Run(tc.code, func(t *testing.T) {
			e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "pve"))
			e.refuse[key("a1", 0)] = &jobRefusal{Status: http.StatusConflict, Code: tc.code, Message: "refused"}
			run := newRun(t, e, steps(true, `reboot: { only_if_required: false }`, cmdTrue), [][]string{{"a1"}})
			mustRun(t, e, run)
			rec := wantNode(t, e, "a1", tc.node, "")
			if tc.code == response.ErrTaskRebootPVEHost {
				wantStep(t, rec, 0, tc.step, reasonProxmoxHost)
				wantStarted(t, e, "a1/1")
			} else {
				wantStep(t, rec, 0, tc.step, tc.code)
				wantStarted(t, e)
			}
		})
	}
}

func TestAnUnconfirmedStepIsNotConfirmedOnTheNode(t *testing.T) {
	stop := `service: { name: nginx.service, action: stop }`
	t.Run("not ticked: sent unconfirmed, the node refuses, the node fails", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		e.refuse[key("a1", 0)] = &jobRefusal{Status: http.StatusConflict, Code: "SERVICE_CONFIRM_REQUIRED", Message: "confirm stopping nginx.service"}
		run := newRun(t, e, steps(true, stop, stop), [][]string{{"a1"}}, func(r *cluster.RunRecord) { r.Confirmed = []int{1} })
		mustRun(t, e, run)
		if e.specs[key("a1", 0)].Confirmed {
			t.Error("step 1 was sent confirmed, though the operator did not tick it")
		}
		rec := wantNode(t, e, "a1", cluster.RunNodeFailed, "step 1")
		wantStep(t, rec, 0, cluster.RunStepFailed, "SERVICE_CONFIRM_REQUIRED")
		wantStep(t, rec, 1, cluster.RunStepPending, "")
	})
	t.Run("ticked: sent confirmed", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		run := newRun(t, e, steps(true, stop, stop), [][]string{{"a1"}}, func(r *cluster.RunRecord) { r.Confirmed = []int{1} })
		mustRun(t, e, run)
		if e.specs[key("a1", 0)].Confirmed || !e.specs[key("a1", 1)].Confirmed {
			t.Errorf("confirmed sent = %v, %v; want false, true", e.specs[key("a1", 0)].Confirmed, e.specs[key("a1", 1)].Confirmed)
		}
	})
}

func TestANodeBusyWithAnotherRunFailsWhateverTheStepSays(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	e.refuse[key("a1", 0)] = &jobRefusal{Status: http.StatusConflict, Code: response.ErrTaskNodeBusy, Message: "busy",
		Details: map[string]string{"job_id": "20261101-110000-000001-0", "run_id": "20261101-110000-000001", "task": "other"}}
	run := newRun(t, e, steps(true, "{ continue_on_error: true, "+cmdTrue+" }", cmdTrue), [][]string{{"a1"}})
	mustRun(t, e, run)
	wantStarted(t, e)
	wantNode(t, e, "a1", cluster.RunNodeFailed, "of run 20261101-110000-000001 (other)")
}

func TestAFinishedRunPrunesOldRecords(t *testing.T) {
	for _, fail := range []bool{false, true} {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		want := cluster.RunSucceeded
		if fail {
			e.outcome[key("a1", 0)] = tasks.JobView{Status: tasks.JobFailed}
			want = cluster.RunFailed
		}
		run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
		mustRun(t, e, run)
		h := wantHead(t, e, want, "")
		if !h.EndedAt.Equal(e.now) {
			t.Errorf("%s run ended at %v, want %v", want, h.EndedAt, e.now)
		}
		if !slices.Equal(e.store.pruned, []int{200}) {
			t.Errorf("%s run pruned %v, want [200]", want, e.store.pruned)
		}
	}
}

func TestALeaderThatLostItsRoleLeavesTheRunAsRecorded(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	e.notLeader = true
	run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
	if err := runTask(context.Background(), e, run); !errors.Is(err, errAbandoned) {
		t.Fatalf("runTask = %v, want errAbandoned", err)
	}
	wantStarted(t, e)
	wantHead(t, e, cluster.RunRunning, "")
}

func TestARunHasOneRunnerOnThisNode(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	e.polls[key("a1", 0)] = 1 << 20
	run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
	h := &Handler{runners: map[string]context.CancelFunc{}, newEnv: func() runEnv { return e }}
	ctx, cancel := context.WithCancel(context.Background())
	if !h.launch(ctx, *run) {
		t.Fatal("the first launch started no runner")
	}
	if h.launch(ctx, *run) {
		t.Error("a second launch of the same run started another runner")
	}
	cancel()
	for deadline := time.Now().Add(5 * time.Second); ; {
		h.mu.Lock()
		n := len(h.runners)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the runner did not end with its context")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAStepSummaryIsCappedAtACharacterBoundary(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	e.outcome[key("a1", 0)] = tasks.JobView{Status: tasks.JobFailed, Summary: strings.Repeat("가", 100)} // 300 bytes
	run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
	mustRun(t, e, run)
	got := e.store.node(testRunID, "a1").Steps[0].Summary
	if len(got) > maxSummary || !utf8.ValidString(got) || !strings.HasPrefix(strings.Repeat("가", 100), got) || len(got) < maxSummary-3 {
		t.Errorf("summary = %d bytes %q, want at most %d bytes of whole characters", len(got), got, maxSummary)
	}
}
