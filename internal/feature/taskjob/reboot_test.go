package taskjob

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/tasks"
)

func rebootSpec(step int, onlyIfRequired bool, wait time.Duration) tasks.JobSpec {
	return kindSpec(step, tasks.StepReboot, func(s *tasks.JobSpec) {
		s.Reboot = &tasks.RebootStep{OnlyIfRequired: onlyIfRequired, Wait: wait}
	})
}

// lockedMock is a MockCommander whose calls the test may read while a
// runner is still making them.
type lockedMock struct {
	mu sync.Mutex
	m  *exec.MockCommander
}

// systemdRunMock is a MockCommander on a host with systemd-run.
func systemdRunMock() *lockedMock {
	m := exec.NewMockCommander()
	m.SetOutput("exists:systemd-run", "", nil)
	return &lockedMock{m: m}
}

func (l *lockedMock) Run(name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.Run(name, args...)
}
func (l *lockedMock) RunWithTimeout(d time.Duration, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.RunWithTimeout(d, name, args...)
}
func (l *lockedMock) RunWithEnv(env []string, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.RunWithEnv(env, name, args...)
}
func (l *lockedMock) RunWithInput(in string, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.RunWithInput(in, name, args...)
}
func (l *lockedMock) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.RunCtx(ctx, name, args...)
}
func (l *lockedMock) RunWithEnvCtx(ctx context.Context, env []string, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.RunWithEnvCtx(ctx, env, name, args...)
}
func (l *lockedMock) Exists(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.Exists(name)
}

// calledName is the calls of name so far.
func calledName(l *lockedMock, name string) []exec.MockCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []exec.MockCall
	for _, c := range l.m.Calls {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// A reboot sent to a Proxmox VE host is refused before anything is
// scheduled — even one that is required — with the code the engine shows as
// "skipped (Proxmox host)": systemd-run never runs, no job is recorded, and
// the node is free for the next step.
func TestARebootIsRefusedOnAProxmoxHostBeforeAnythingIsScheduled(t *testing.T) {
	platform.SetForTest(t, platform.Info{PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2})
	useSystemd(t, true)
	m := systemdRunMock()
	k := newKindNode(t, m)
	writeFile(t, k.required, "*** System restart required ***\n")

	s := rebootSpec(0, false, 0)
	status, env := k.start(t, s)
	if status != http.StatusConflict || env.code() != response.ErrTaskRebootPVEHost {
		t.Fatalf("reboot on Proxmox: %d %q, want 409 %s", status, env.code(), response.ErrTaskRebootPVEHost)
	}
	if calls := calledName(m, "systemd-run"); len(calls) != 0 {
		t.Errorf("systemd-run ran on a Proxmox host: %v", calls)
	}
	if _, err := k.h.get(s.ID); err != errNotFound {
		t.Errorf("the refused job was recorded: %v", err)
	}
	next := spec(runA, 1)
	k.h.Register(tasks.StepCommand, k.runner)
	if status, env := k.start(t, next); status != http.StatusCreated {
		t.Errorf("the next step: %d %q, want 201 (the node is free)", status, env.code())
	}
}

// Without systemd there is nothing to schedule a reboot with: 409
// TASK_REBOOT_UNSUPPORTED, before anything runs.
func TestARebootWithoutSystemdIsRefused(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	useSystemd(t, false)
	m := systemdRunMock()
	k := newKindNode(t, m)
	status, env := k.start(t, rebootSpec(0, false, 0))
	if status != http.StatusConflict || env.code() != response.ErrTaskRebootUnsupported {
		t.Fatalf("reboot without systemd: %d %q, want 409 %s", status, env.code(), response.ErrTaskRebootUnsupported)
	}
	if calls := calledName(m, "systemd-run"); len(calls) != 0 {
		t.Errorf("systemd-run ran: %v", calls)
	}
}

// only_if_required with no reboot required ends the step at once, a success
// that changed nothing, and schedules nothing.
func TestARebootThatIsNotRequiredIsSkipped(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	useSystemd(t, true)
	m := systemdRunMock()
	k := newKindNode(t, m)
	r := k.run(t, rebootSpec(0, true, 0))
	if r.status != tasks.JobSucceeded || r.changed || r.summary != "not required" {
		t.Errorf("job ended %s changed=%v %q, want succeeded, unchanged, \"not required\"", r.status, r.changed, r.summary)
	}
	if calls := calledName(m, "systemd-run"); len(calls) != 0 {
		t.Errorf("systemd-run ran: %v", calls)
	}
}

// A reboot is scheduled five seconds out in a transient timer named after
// the job, and the job waits in "rebooting", holding the node, with the boot
// it started in recorded. After a restart in the same boot it still waits;
// once the node has booted again — another boot ID — it succeeded.
func TestARebootJobSucceedsOnceTheBootIDChanges(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	useSystemd(t, true)
	m := systemdRunMock()
	k := newKindNode(t, m)
	writeFile(t, k.required, "*** System restart required ***\n")

	s := rebootSpec(0, true, 0)
	startOK(t, k.testNode, s)
	rec := k.waitStatus(t, s.ID, tasks.JobRebooting)
	eventually(t, "systemd-run", func() bool { return len(calledName(m, "systemd-run")) == 1 })
	want := []string{"--on-active=5", "--unit=sfpanel-task-reboot-" + s.ID, "systemctl", "reboot"}
	if got := calledName(m, "systemd-run")[0].Args; !slices.Equal(got, want) {
		t.Errorf("systemd-run %q, want %q", got, want)
	}
	if rec.bootID != "boot-1" {
		t.Errorf("recorded boot %q, want boot-1", rec.bootID)
	}
	k.h.Register(tasks.StepCommand, k.runner)
	if status, env := k.start(t, spec(runA, 1)); status != http.StatusConflict || env.code() != response.ErrTaskNodeBusy {
		t.Errorf("another step while rebooting: %d %q, want 409 %s", status, env.code(), response.ErrTaskNodeBusy)
	}

	same := k.restart(t)
	if r := same.record(t, s.ID); r.status != tasks.JobRebooting {
		t.Errorf("after a restart in the same boot the job is %s %q, want rebooting", r.status, r.summary)
	}

	writeFile(t, k.bootFile, "boot-2\n")
	booted := same.restart(t)
	r := booted.record(t, s.ID)
	if r.status != tasks.JobSucceeded || !r.changed {
		t.Errorf("after the reboot the job is %s changed=%v %q, want succeeded, changed", r.status, r.changed, r.summary)
	}
}

// A reboot that has not happened by the step's wait frees the node: the job
// fails, saying so, rather than holding the node for ever.
func TestARebootThatDoesNotHappenFreesTheNode(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	useSystemd(t, true)
	k := newKindNode(t, systemdRunMock())
	r := k.run(t, rebootSpec(0, false, 30*time.Millisecond))
	if r.status != tasks.JobFailed || !strings.Contains(r.summary, "did not reboot") {
		t.Errorf("job ended %s %q, want failed: did not reboot", r.status, r.summary)
	}
}

// A forced cancel calls off a scheduled reboot: the timer is stopped and the
// job ends cancelled. Without force the job keeps waiting.
func TestAForcedCancelCallsOffAScheduledReboot(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	useSystemd(t, true)
	m := systemdRunMock()
	k := newKindNode(t, m)
	s := rebootSpec(0, false, 0)
	startOK(t, k.testNode, s)
	k.waitStatus(t, s.ID, tasks.JobRebooting)
	eventually(t, "systemd-run", func() bool { return len(calledName(m, "systemd-run")) == 1 })

	uri := "/api/v1/task-jobs/" + s.ID + "/cancel"
	if status, env := k.do(t, http.MethodPost, uri, map[string]bool{"force": false}, true); status != http.StatusConflict || env.code() != response.ErrTaskJobRunning {
		t.Errorf("cancel without force: %d %q, want 409 %s", status, env.code(), response.ErrTaskJobRunning)
	}
	if status, env := k.do(t, http.MethodPost, uri, map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel: %d %q", status, env.code())
	}
	r := k.ended(t, s.ID)
	if r.status != tasks.JobCancelled {
		t.Errorf("job ended %s %q, want cancelled", r.status, r.summary)
	}
	stopped := false
	for _, c := range calledName(m, "systemctl") {
		stopped = stopped || slices.Equal(c.Args, []string{"stop", "sfpanel-task-reboot-" + s.ID + ".timer"})
	}
	if !stopped {
		t.Errorf("the timer was not stopped: %v", calledName(m, "systemctl"))
	}
}

// forceCancelReboot starts a reboot job on a host whose
// `systemctl is-system-running` answers state, cancels it with force once it
// is rebooting, and returns the node, the mock and the job's ID once the
// runner has asked the system's state.
func forceCancelReboot(t *testing.T, state string, stateErr error) (*kindNode, *lockedMock, string) {
	t.Helper()
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	useSystemd(t, true)
	m := systemdRunMock()
	m.m.SetOutput("systemctl", state+"\n", stateErr)
	k := newKindNode(t, m)
	s := rebootSpec(0, false, 0)
	startOK(t, k.testNode, s)
	k.waitStatus(t, s.ID, tasks.JobRebooting)
	eventually(t, "systemd-run", func() bool { return len(calledName(m, "systemd-run")) == 1 })
	if status, env := k.do(t, http.MethodPost, "/api/v1/task-jobs/"+s.ID+"/cancel", map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel: %d %q", status, env.code())
	}
	return k, m, s.ID
}

// A forced cancel that comes after the timer fired — the system already
// stopping for the reboot — cannot call the reboot off: the job stays
// rebooting, holding the node, for the next boot to settle by its boot ID,
// rather than ending cancelled while the host reboots under it.
func TestAForcedCancelLeavesARebootAlreadyUnderWayRebooting(t *testing.T) {
	k, m, id := forceCancelReboot(t, "stopping", errors.New("exit status 1"))
	eventually(t, "the cancel to be taken up", func() bool {
		for _, c := range calledName(m, "systemctl") {
			if slices.Equal(c.Args, []string{"is-system-running"}) {
				return true
			}
		}
		return k.record(t, id).status != tasks.JobRebooting
	})
	// Give the runner the moment to finish the job, were it going to.
	time.Sleep(50 * time.Millisecond)
	if r := k.record(t, id); r.status != tasks.JobRebooting {
		t.Errorf("the job is %s %q, want still rebooting: the reboot was under way", r.status, r.summary)
	}
}

// While the system is still running, a forced cancel calls the reboot off.
func TestAForcedCancelCallsOffARebootNotYetUnderWay(t *testing.T) {
	k, _, id := forceCancelReboot(t, "running", nil)
	r := k.ended(t, id)
	if r.status != tasks.JobCancelled || r.summary != "cancelled: the reboot was called off" {
		t.Errorf("the job ended %s %q, want cancelled: the reboot was called off", r.status, r.summary)
	}
}
