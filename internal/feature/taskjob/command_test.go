package taskjob

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// fakeSystemd is a Commander that plays systemd for the job runners:
// systemd-run starts a unit that writes to the log its command line names,
// systemctl answers from what has run, and the test ends each unit the way a
// real one ends. Nothing here starts a process.
type fakeSystemd struct {
	mu      sync.Mutex
	calls   []exec.MockCall
	active  map[string]bool
	results map[string]string // unit → what `systemctl show -p Result` says
	logs    map[string]string // unit → the log it writes to
	noRun   bool              // systemd-run is not installed
}

func newFakeSystemd() *fakeSystemd {
	return &fakeSystemd{active: map[string]bool{}, results: map[string]string{}, logs: map[string]string{}}
}

func (f *fakeSystemd) answer(name string, args []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, exec.MockCall{Name: name, Args: append([]string(nil), args...)})
	switch {
	case name == "systemd-run":
		var unit string
		for i, a := range args {
			if u, ok := strings.CutPrefix(a, "--unit="); ok {
				unit = u
			}
			if a == "-p" && i+1 < len(args) {
				if p, ok := strings.CutPrefix(args[i+1], "StandardOutput=append:"); ok {
					f.logs[unit] = p
				}
			}
		}
		f.active[unit] = true
		return "", nil
	case name == "systemctl" && len(args) == 2 && args[0] == "is-active":
		if f.active[args[1]] {
			return "active\n", nil
		}
		return "inactive\n", errors.New("exit status 3")
	case name == "systemctl" && len(args) == 2 && args[0] == "stop":
		f.active[args[1]] = false
		return "", nil
	case name == "systemctl" && len(args) == 5 && args[0] == "show":
		if r := f.results[args[4]]; r != "" {
			return r + "\n", nil
		}
		return "success\n", nil
	}
	return "", nil
}

func (f *fakeSystemd) Run(name string, args ...string) (string, error) { return f.answer(name, args) }
func (f *fakeSystemd) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeSystemd) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeSystemd) RunWithInput(_ string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeSystemd) RunCtx(_ context.Context, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeSystemd) RunWithEnvCtx(_ context.Context, _ []string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeSystemd) Exists(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return name != "systemd-run" || !f.noRun
}

// called returns the calls of name whose arguments start with prefix.
func (f *fakeSystemd) called(name string, prefix ...string) []exec.MockCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []exec.MockCall
	for _, c := range f.calls {
		if c.Name == name && len(c.Args) >= len(prefix) && slices.Equal(c.Args[:len(prefix)], prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeSystemd) isActive(unit string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active[unit]
}

// write is the unit's command printing out.
func (f *fakeSystemd) write(t *testing.T, unit, out string) {
	t.Helper()
	f.mu.Lock()
	log := f.logs[unit]
	f.mu.Unlock()
	if log == "" {
		t.Fatalf("unit %s was never started", unit)
	}
	file, err := os.OpenFile(log, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(out); err != nil {
		t.Fatal(err)
	}
}

// exit is the unit's command ending with code, as the script reports it.
func (f *fakeSystemd) exit(t *testing.T, unit string, code int) {
	t.Helper()
	f.write(t, unit, "done\n"+exitMarker+strconv.Itoa(code)+"\n")
	f.stopped(unit, "")
}

// stopped is the unit going away without its script saying how it ended;
// result is what systemd says of it ("" for nothing).
func (f *fakeSystemd) stopped(unit, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active[unit] = false
	if result != "" {
		f.results[unit] = result
	}
}

// useSystemd makes the runners see systemd, or not, until the test ends.
func useSystemd(t *testing.T, on bool) {
	t.Helper()
	prev := systemdActive
	systemdActive = func() bool { return on }
	t.Cleanup(func() { systemdActive = prev })
}

// proc is one panel process. When it dies (kill), every call it makes to
// systemd or the packages module never returns, so its watchers stop where
// they are, as the process's goroutines would.
type proc struct {
	dead chan struct{}
	once sync.Once
}

func newProc() *proc { return &proc{dead: make(chan struct{})} }

func (p *proc) kill() { p.once.Do(func() { close(p.dead) }) }

func (p *proc) gate() {
	select {
	case <-p.dead:
		select {}
	default:
	}
}

// procCmd is systemd as one process calls it.
type procCmd struct {
	*fakeSystemd
	p *proc
}

func (c procCmd) RunWithTimeout(d time.Duration, name string, args ...string) (string, error) {
	c.p.gate()
	return c.fakeSystemd.RunWithTimeout(d, name, args...)
}

func (c procCmd) Exists(name string) bool {
	c.p.gate()
	return c.fakeSystemd.Exists(name)
}

// procPkgs is the packages module as one process calls it.
type procPkgs struct {
	pkgs PackageTransactions
	p    *proc
}

func (w procPkgs) StartJobTransaction(kind string, names []string, update bool) (string, error) {
	w.p.gate()
	return w.pkgs.StartJobTransaction(kind, names, update)
}

func (w procPkgs) JobTransaction(id string) (bool, string, int, string, error) {
	w.p.gate()
	return w.pkgs.JobTransaction(id)
}

func (w procPkgs) ChangedPackages(logPath string) (int, bool) {
	w.p.gate()
	return w.pkgs.ChangedPackages(logPath)
}

// newRunnerNode is a node whose command and packages steps run through the
// real runners, on a Commander that plays systemd.
func newRunnerNode(t *testing.T, f *fakeSystemd, pkgs PackageTransactions) *testNode {
	t.Helper()
	useSystemd(t, true)
	n := newTestNode(t)
	registerRunners(n, f, pkgs)
	return n
}

func registerRunners(n *testNode, f *fakeSystemd, pkgs PackageTransactions) {
	n.proc = newProc()
	cmd := procCmd{f, n.proc}
	n.h.Cmd = cmd
	n.h.runners = map[string]Runner{}
	c := NewCommandRunner(cmd)
	c.poll = 2 * time.Millisecond
	n.h.Register(tasks.StepCommand, c)
	if pkgs != nil {
		p := NewPackageRunner(procPkgs{pkgs, n.proc})
		p.poll = 2 * time.Millisecond
		n.h.Register(tasks.StepPackages, p)
	}
}

// restart is the same node after a panel restart: the old process dies, and
// a new handler on the same database and logs registers its runners and
// recovers.
func (n *testNode) restart(t *testing.T, f *fakeSystemd, pkgs PackageTransactions) *testNode {
	t.Helper()
	n.proc.kill()
	r := newTestNodeOn(t, n.h.DB, n.h.LogDir)
	r.clock = n.clock
	registerRunners(r, f, pkgs)
	r.h.Recover()
	return r
}

// commandSpec is a command step running script, under timeout (0 for the
// default).
func commandSpec(run string, script string, timeout time.Duration) tasks.JobSpec {
	s := spec(run, 0)
	s.Command = &tasks.CommandStep{Script: script, Timeout: timeout}
	return s
}

// record reads the job's row.
func (n *testNode) record(t *testing.T, id string) record {
	t.Helper()
	r, err := n.h.get(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ended waits for the job to end and returns its row.
func (n *testNode) ended(t *testing.T, id string) record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := n.record(t, id)
		if !active(r.status) {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is still %s", id, r.status)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// eventually waits for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func startOK(t *testing.T, n *testNode, s tasks.JobSpec) {
	t.Helper()
	if status, env := n.start(t, s); status != http.StatusCreated {
		t.Fatalf("start: %d %q", status, env.code())
	}
}

func unitOf(id string) string { return "sfpanel-task-" + id + ".service" }

// A command step runs in a transient unit of its own, which a panel restart
// does not stop: the systemd-run command line names the unit, has it
// collected when it ends, bounds it by the step's timeout and appends its
// output to the job's log. The script is a file the unit runs, never an
// argument: nothing the step says reaches a command line.
func TestACommandJobRunsInATransientUnit(t *testing.T) {
	f := newFakeSystemd()
	n := newRunnerNode(t, f, nil)
	s := commandSpec(runA, "echo secret-body\n", 0)
	startOK(t, n, s)
	eventually(t, "systemd-run", func() bool { return len(f.called("systemd-run")) == 1 })

	log := filepath.Join(n.h.LogDir, s.ID+".log")
	script := filepath.Join(n.h.LogDir, s.ID+".sh")
	want := []string{
		"--unit=" + unitOf(s.ID), "--collect", "--quiet",
		"-p", "RuntimeMaxSec=600",
		"-p", "StandardOutput=append:" + log,
		"-p", "StandardError=inherit",
		"--", "/bin/sh", "-c", `"$@"; rc=$?; echo "[sfpanel-task] exit=$rc"; exit $rc`, "sh", "/bin/sh", script,
	}
	got := f.called("systemd-run")[0].Args
	if !slices.Equal(got, want) {
		t.Errorf("systemd-run %q\nwant         %q", got, want)
	}
	if slices.ContainsFunc(got, func(a string) bool { return strings.Contains(a, "secret-body") }) {
		t.Errorf("the script reached the command line: %q", got)
	}
	body, err := os.ReadFile(script)
	if err != nil || string(body) != "echo secret-body\n" {
		t.Errorf("script %q (%v), want the step's script", body, err)
	}
	if st, err := os.Stat(script); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("script mode %v (%v), want 0700", st.Mode().Perm(), err)
	}
	if u := n.record(t, s.ID).unit; u != unitOf(s.ID) {
		t.Errorf("recorded unit %q, want %q: a restarted panel asks systemd about it", u, unitOf(s.ID))
	}

	// The step's own timeout is the unit's, in seconds, and a one-line run
	// is a script of one line.
	f2 := newFakeSystemd()
	n2 := newRunnerNode(t, f2, nil)
	s2 := spec(runB, 0)
	s2.Command = &tasks.CommandStep{Run: "apt-get moo", Timeout: 90 * time.Second}
	startOK(t, n2, s2)
	eventually(t, "systemd-run", func() bool { return len(f2.called("systemd-run")) == 1 })
	if args := f2.called("systemd-run")[0].Args; !slices.Contains(args, "RuntimeMaxSec=90") {
		t.Errorf("systemd-run %q, want RuntimeMaxSec=90", args)
	}
	if body, _ := os.ReadFile(filepath.Join(n2.h.LogDir, s2.ID+".sh")); string(body) != "apt-get moo\n" {
		t.Errorf("script %q, want the one line", body)
	}
}

// How a command job ended is what its script wrote last: the exit marker.
// A unit that went away without one was stopped or killed before the
// command returned — interrupted, never a success. A command always counts
// as a change. The same holds for a job whose unit ended while the panel was
// restarting, and a unit still running at boot is watched again.
func TestACommandJobsResultIsItsExitMarker(t *testing.T) {
	for _, tc := range []struct {
		label   string
		end     func(f *fakeSystemd, t *testing.T, unit string)
		status  string
		code    *int
		summary string
	}{
		{"exit 0", func(f *fakeSystemd, t *testing.T, u string) { f.exit(t, u, 0) }, tasks.JobSucceeded, intPtr(0), "exit 0"},
		{"exit 3", func(f *fakeSystemd, t *testing.T, u string) { f.exit(t, u, 3) }, tasks.JobFailed, intPtr(3), "exit 3"},
		{"no marker", func(f *fakeSystemd, t *testing.T, u string) {
			f.write(t, u, "half of it\n")
			f.stopped(u, "")
		}, tasks.JobInterrupted, nil, "without saying how it ended"},
	} {
		check := func(t *testing.T, r record) {
			t.Helper()
			if r.status != tc.status || !r.changed || !sameCode(r.exitCode, tc.code) || !strings.Contains(r.summary, tc.summary) {
				t.Errorf("job ended %s changed=%v exit %v %q, want %s changed exit %v %q",
					r.status, r.changed, deref(r.exitCode), r.summary, tc.status, deref(tc.code), tc.summary)
			}
		}
		t.Run(tc.label, func(t *testing.T) {
			f := newFakeSystemd()
			n := newRunnerNode(t, f, nil)
			s := commandSpec(runA, "true\n", 0)
			startOK(t, n, s)
			eventually(t, "the unit", func() bool { return f.isActive(unitOf(s.ID)) })
			tc.end(f, t, unitOf(s.ID))
			check(t, n.ended(t, s.ID))
			if _, err := os.Stat(filepath.Join(n.h.LogDir, s.ID+".sh")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the script outlived its job: %v", err)
			}
		})
		t.Run(tc.label+", ended while the panel restarted", func(t *testing.T) {
			f := newFakeSystemd()
			n := newRunnerNode(t, f, nil)
			s := commandSpec(runA, "true\n", 0)
			startOK(t, n, s)
			eventually(t, "the unit", func() bool { return f.isActive(unitOf(s.ID)) })
			// The panel stops, and the unit ends before it is back.
			n.proc.kill()
			tc.end(f, t, unitOf(s.ID))
			check(t, n.restart(t, f, nil).ended(t, s.ID))
		})
		t.Run(tc.label+", still running at boot", func(t *testing.T) {
			f := newFakeSystemd()
			n := newRunnerNode(t, f, nil)
			s := commandSpec(runA, "true\n", 0)
			startOK(t, n, s)
			eventually(t, "the unit", func() bool { return f.isActive(unitOf(s.ID)) })
			r := n.restart(t, f, nil)
			if st := r.record(t, s.ID).status; st != tasks.JobRunning {
				t.Fatalf("after the restart the job is %s, want still running", st)
			}
			tc.end(f, t, unitOf(s.ID))
			check(t, r.ended(t, s.ID))
		})
	}
}

// RuntimeMaxSec ends a command that runs past its step's timeout; the job
// says that is why it failed, rather than that it was interrupted.
func TestACommandJobThatTimesOutSaysSo(t *testing.T) {
	f := newFakeSystemd()
	n := newRunnerNode(t, f, nil)
	s := commandSpec(runA, "sleep 1000\n", 90*time.Second)
	startOK(t, n, s)
	eventually(t, "the unit", func() bool { return f.isActive(unitOf(s.ID)) })
	f.stopped(unitOf(s.ID), "timeout")
	r := n.ended(t, s.ID)
	if r.status != tasks.JobFailed || r.exitCode != nil || !strings.Contains(r.summary, "timed out after 1m30s") {
		t.Errorf("job ended %s exit %v %q, want failed: timed out after 1m30s", r.status, deref(r.exitCode), r.summary)
	}
	if calls := f.called("systemctl", "show"); len(calls) == 0 || !slices.Equal(calls[0].Args, []string{"show", "-p", "Result", "--value", unitOf(s.ID)}) {
		t.Errorf("systemctl show calls %+v, want the unit's Result asked", calls)
	}

	// --collect may unload the unit before systemd is asked; one that went
	// silent once its time was up timed out all the same.
	f2 := newFakeSystemd()
	n2 := newRunnerNode(t, f2, nil)
	s2 := commandSpec(runB, "sleep 1000\n", 90*time.Second)
	startOK(t, n2, s2)
	eventually(t, "the unit", func() bool { return f2.isActive(unitOf(s2.ID)) })
	*n2.clock = n2.clock.Add(91 * time.Second)
	f2.stopped(unitOf(s2.ID), "")
	if r := n2.ended(t, s2.ID); r.status != tasks.JobFailed || !strings.Contains(r.summary, "timed out after 1m30s") {
		t.Errorf("job ended %s %q, want failed: timed out after 1m30s", r.status, r.summary)
	}
}

// By default a job runs to its end; only a forced cancel stops its unit,
// and that holds for a job a restarted panel took back as well.
func TestACommandJobIsCancelledOnlyWhenForced(t *testing.T) {
	f := newFakeSystemd()
	n := newRunnerNode(t, f, nil)
	s := commandSpec(runA, "sleep 1000\n", 0)
	startOK(t, n, s)
	unit := unitOf(s.ID)
	eventually(t, "the unit", func() bool { return f.isActive(unit) })
	cancelURI := "/api/v1/task-jobs/" + s.ID + "/cancel"

	if status, env := n.do(t, http.MethodPost, cancelURI, map[string]bool{"force": false}, true); status != http.StatusConflict || env.code() != response.ErrTaskJobRunning {
		t.Fatalf("unforced cancel: %d %q, want 409 %s", status, env.code(), response.ErrTaskJobRunning)
	}
	time.Sleep(20 * time.Millisecond)
	if len(f.called("systemctl", "stop")) != 0 || !f.isActive(unit) || n.record(t, s.ID).status != tasks.JobRunning {
		t.Fatalf("an unforced cancel stopped the job: %+v", f.called("systemctl", "stop"))
	}

	// The panel restarts; the job is taken back, and a forced cancel stops
	// the unit it runs in.
	r := n.restart(t, f, nil)
	if status, _ := r.do(t, http.MethodPost, cancelURI, map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel: %d", status)
	}
	got := r.ended(t, s.ID)
	if stops := f.called("systemctl", "stop"); len(stops) != 1 || !slices.Equal(stops[0].Args, []string{"stop", unit}) {
		t.Errorf("stops %+v, want systemctl stop %s", stops, unit)
	}
	if got.status != tasks.JobCancelled || !got.changed {
		t.Errorf("job ended %s changed=%v %q, want cancelled", got.status, got.changed, got.summary)
	}
}

// A step that could not be handed to systemd failed, and nothing ran.
func TestACommandJobWhoseUnitDidNotStartFailed(t *testing.T) {
	f := newFakeSystemd()
	n := newRunnerNode(t, f, nil)
	n.h.Register(tasks.StepCommand, &CommandRunner{Cmd: failingRun{f}, poll: 2 * time.Millisecond})
	s := commandSpec(runA, "true\n", 0)
	startOK(t, n, s)
	r := n.ended(t, s.ID)
	if r.status != tasks.JobFailed || r.changed || !strings.Contains(r.summary, "systemd-run") {
		t.Errorf("job ended %s changed=%v %q, want failed naming systemd-run, nothing changed", r.status, r.changed, r.summary)
	}
}

// failingRun is a systemd whose systemd-run refuses.
type failingRun struct{ *fakeSystemd }

func (f failingRun) RunWithTimeout(d time.Duration, name string, args ...string) (string, error) {
	if name == "systemd-run" {
		return "Failed to start transient service unit: Access denied", errors.New("exit status 1")
	}
	return f.fakeSystemd.RunWithTimeout(d, name, args...)
}

// Without systemd the panel runs the script itself: same log, same marker,
// same timeout and cancel. (These run /bin/sh.)
func TestACommandJobRunsDirectlyWithoutSystemd(t *testing.T) {
	f := newFakeSystemd()
	f.noRun = true
	n := newRunnerNode(t, f, nil)

	s := commandSpec(runA, "echo hello from the job\nexit 3\n", 0)
	startOK(t, n, s)
	r := n.ended(t, s.ID)
	if r.status != tasks.JobFailed || !sameCode(r.exitCode, intPtr(3)) || !r.changed {
		t.Errorf("job ended %s exit %v changed=%v %q, want failed with 3", r.status, deref(r.exitCode), r.changed, r.summary)
	}
	log, _ := os.ReadFile(filepath.Join(n.h.LogDir, s.ID+".log"))
	if !strings.Contains(string(log), "hello from the job\n"+exitMarker+"3\n") {
		t.Errorf("log %q, want the output and the marker", log)
	}
	if len(f.called("systemd-run")) != 0 || n.record(t, s.ID).unit != "" {
		t.Errorf("a unit was used without systemd")
	}

	n2 := newRunnerNode(t, f, nil)
	s2 := commandSpec(runB, "sleep 30\n", 200*time.Millisecond)
	startOK(t, n2, s2)
	if r := n2.ended(t, s2.ID); r.status != tasks.JobFailed || !strings.Contains(r.summary, "timed out after 200ms") {
		t.Errorf("job ended %s %q, want failed: timed out", r.status, r.summary)
	}

	n3 := newRunnerNode(t, f, nil)
	s3 := commandSpec(runB, "sleep 30\n", 0)
	startOK(t, n3, s3)
	time.Sleep(50 * time.Millisecond)
	if status, _ := n3.do(t, http.MethodPost, "/api/v1/task-jobs/"+s3.ID+"/cancel", map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel: %d", status)
	}
	if r := n3.ended(t, s3.ID); r.status != tasks.JobCancelled {
		t.Errorf("job ended %s %q, want cancelled", r.status, r.summary)
	}
}

func sameCode(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
