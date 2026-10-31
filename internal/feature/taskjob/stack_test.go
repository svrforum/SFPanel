package taskjob

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/composex"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	plainCompose     = "services:\n  app:\n    image: nginx\n"
	otherCompose     = "services:\n  app:\n    image: nginx:1.27\n"
	riskyCompose     = "services:\n  app:\n    image: nginx\n    privileged: true\n"
	forbiddenCompose = "services:\n  app:\n    image: nginx\n    volumes:\n      - /etc/sfpanel:/panel\n"
)

func stackSpec(step int, name, compose string, up, confirmed bool) tasks.JobSpec {
	return kindSpec(step, tasks.StepStack, func(s *tasks.JobSpec) {
		s.Stack = &tasks.StackStep{Name: name, Compose: compose, Up: up}
		s.Confirmed = confirmed
	})
}

// A stack step refuses what the compose endpoints refuse, for the same
// reason: a name the compose module would not create, a document that is
// not compose, the forbidden tier — confirmed or not — and the risky tier
// until the step was confirmed. Nothing is written for a refused step.
func TestAStackStepIsRefusedForTheEndpointsReasons(t *testing.T) {
	report := func(doc string) composex.Report {
		r, err := composex.Analyze(doc)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	_, yamlErr := composex.Analyze("services: [")
	for _, tc := range []struct {
		label     string
		name      string
		compose   string
		confirmed bool
		code      string // "" when the step runs
		msg       string
	}{
		{"a reserved name", "web.migbak", plainCompose, true, response.ErrInvalidName, docker.ValidateProjectName("web.migbak").Error()},
		{"a name with a slash", "a/b", plainCompose, true, response.ErrInvalidName, docker.ValidateProjectName("a/b").Error()},
		{"not YAML", "web", "services: [", true, response.ErrInvalidYAML, yamlErr.Error()},
		{"the forbidden tier, confirmed", "web", forbiddenCompose, true, response.ErrComposeForbidden, report(forbiddenCompose).Error(true).Error()},
		{"the risky tier, not confirmed", "web", riskyCompose, false, response.ErrComposeRisky, report(riskyCompose).Error(false).Error()},
		{"the risky tier, confirmed", "web", riskyCompose, true, "", ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			k := newKindNode(t, exec.NewMockCommander())
			r := k.run(t, stackSpec(0, tc.name, tc.compose, true, tc.confirmed))
			if tc.code == "" {
				if r.status != tasks.JobSucceeded || k.compose.stack(tc.name) != tc.compose {
					t.Errorf("job ended %s %q, stack %q; want succeeded and written", r.status, r.summary, k.compose.stack(tc.name))
				}
				return
			}
			if r.status != tasks.JobFailed || r.summary != tc.code+": "+tc.msg || r.changed {
				t.Errorf("job ended %s changed=%v %q, want failed %q", r.status, r.changed, r.summary, tc.code+": "+tc.msg)
			}
			if calls := k.compose.callList(); len(calls) != 0 {
				t.Errorf("a refused step reached compose: %v", calls)
			}
		})
	}
}

// A stack step creates a stack that is not there and updates one that is.
// The same compose document is not written again and, brought up with
// nothing to start, is no change; up starting or recreating a container is.
// Up runs under a 30-minute bound.
func TestAStackStepSaysWhetherItChangedTheStack(t *testing.T) {
	k := newKindNode(t, exec.NewMockCommander())
	for i, tc := range []struct {
		label    string
		compose  string
		up       bool
		upOutput string
		changed  bool
		writes   []string
	}{
		{"a new stack", plainCompose, false, "", true, []string{"create web"}},
		{"the same compose", plainCompose, false, "", false, nil},
		{"another compose", otherCompose, false, "", true, []string{"create web", "update web"}},
		{"the same compose, up, all running", otherCompose, true, " Container web-app-1  Running\n", false, nil},
		{"the same compose, up, recreated", otherCompose, true, " Container web-app-1  Recreated\n Container web-app-1  Started\n", true, nil},
	} {
		k.compose.mu.Lock()
		k.compose.calls, k.compose.upOutput = nil, tc.upOutput
		k.compose.mu.Unlock()
		r := k.run(t, stackSpec(i, "web", tc.compose, tc.up, false))
		if r.status != tasks.JobSucceeded || r.changed != tc.changed {
			t.Errorf("%s: %s changed=%v %q, want succeeded changed=%v", tc.label, r.status, r.changed, r.summary, tc.changed)
		}
		var writes []string
		upCalled := false
		for _, c := range k.compose.callList() {
			if strings.HasPrefix(c, "create ") || strings.HasPrefix(c, "update ") {
				writes = append(writes, c)
			}
			upCalled = upCalled || c == "up web"
		}
		if !slices.Equal(writes, tc.writes) || upCalled != tc.up {
			t.Errorf("%s: compose saw %v, want writes %v and up=%v", tc.label, k.compose.callList(), tc.writes, tc.up)
		}
		if k.compose.stack("web") != tc.compose {
			t.Errorf("%s: the stack holds %q", tc.label, k.compose.stack("web"))
		}
		if tc.up {
			if dl, ok := k.compose.lastUpCtx().Deadline(); !ok || time.Until(dl) < 29*time.Minute || time.Until(dl) > 30*time.Minute {
				t.Errorf("%s: up ran with deadline %v (%v), want 30 minutes", tc.label, dl, ok)
			}
			if !strings.Contains(k.logOf(t, r.spec.ID), tc.upOutput) {
				t.Errorf("%s: the log lacks up's output: %q", tc.label, k.logOf(t, r.spec.ID))
			}
		}
	}
}

// Up failing fails the step with compose's words.
func TestAStackStepWhoseUpFailsFails(t *testing.T) {
	k := newKindNode(t, exec.NewMockCommander())
	k.compose.upOutput, k.compose.upErr = "Error response from daemon: pull access denied for nginx-typo", errors.New("exit status 1")
	r := k.run(t, stackSpec(0, "web", plainCompose, true, false))
	if r.status != tasks.JobFailed || !strings.HasPrefix(r.summary, response.ErrComposeError+": ") || !strings.Contains(r.summary, "pull access denied") || !r.changed {
		t.Errorf("job ended %s changed=%v %q, want failed COMPOSE_ERROR with compose's words, changed (the file was written)", r.status, r.changed, r.summary)
	}
}

// Up runs inside the panel: a restart while it runs leaves the job
// interrupted, not succeeded and not running.
func TestAStackJobCutOffByARestartIsInterrupted(t *testing.T) {
	k := newKindNode(t, exec.NewMockCommander())
	k.compose.upBlock = make(chan struct{})
	s := stackSpec(0, "web", plainCompose, true, false)
	startOK(t, k.testNode, s)
	eventually(t, "up", func() bool { return slices.Contains(k.compose.callList(), "up web") })
	r := k.restart(t)
	if rec := r.record(t, s.ID); rec.status != tasks.JobInterrupted {
		t.Errorf("after the restart the job is %s %q, want interrupted", rec.status, rec.summary)
	}
	close(k.compose.upBlock)
}
