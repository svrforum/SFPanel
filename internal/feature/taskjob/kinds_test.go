package taskjob

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// kindNode is a node whose service, file, stack and reboot steps run through
// the real runners, on cmd, with a compose module the test plays, a
// reboot-required file and a boot ID file the test points at its own
// directory.
type kindNode struct {
	*testNode
	cmd      exec.Commander
	compose  *fakeCompose
	required string // the reboot-required file
	bootFile string // the boot ID file
}

func newKindNode(t *testing.T, cmd exec.Commander) *kindNode {
	t.Helper()
	n := newTestNode(t)
	dir := t.TempDir()
	k := &kindNode{testNode: n, cmd: cmd, compose: newFakeCompose(),
		required: filepath.Join(dir, "reboot-required"), bootFile: filepath.Join(dir, "boot_id")}
	writeFile(t, k.bootFile, "boot-1\n")
	k.wire(n)
	return k
}

// wire registers the runners on n, as router.go does.
func (k *kindNode) wire(n *testNode) {
	n.h.Cmd = k.cmd
	n.h.Compose = k.compose
	n.h.BootIDPath = k.bootFile
	n.h.bootID = n.h.readBootID
	n.h.runners = map[string]Runner{}
	n.h.Register(tasks.StepService, NewServiceRunner(k.cmd, platform.Get))
	n.h.Register(tasks.StepFile, FileRunner{})
	n.h.Register(tasks.StepStack, StackStepRunner{})
	reboot := NewRebootRunner(k.cmd, platform.Get)
	reboot.RequiredPath = k.required
	n.h.Register(tasks.StepReboot, reboot)
}

// restart is the same node after a panel restart.
func (k *kindNode) restart(t *testing.T) *kindNode {
	t.Helper()
	r := newTestNodeOn(t, k.h.DB, k.h.LogDir)
	r.clock = k.clock
	next := &kindNode{testNode: r, cmd: k.cmd, compose: k.compose, required: k.required, bootFile: k.bootFile}
	next.wire(r)
	r.h.Recover()
	return next
}

// kindSpec is step `step` of runA, of kind, with its settings set by set.
func kindSpec(step int, kind string, set func(s *tasks.JobSpec)) tasks.JobSpec {
	s := tasks.JobSpec{ID: runA + "-" + strconv.Itoa(step), RunID: runA, Task: "Deploy", Step: step, Kind: kind}
	set(&s)
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// run starts s on the node and returns the job's row once it has ended.
func (k *kindNode) run(t *testing.T, s tasks.JobSpec) record {
	t.Helper()
	startOK(t, k.testNode, s)
	return k.ended(t, s.ID)
}

// logOf is the job's output as written.
func (k *kindNode) logOf(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(k.h.LogDir, id+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeCompose is the compose module as a stack step uses it: stacks are
// documents in a map, and up answers with the output the test sets.
type fakeCompose struct {
	mu       sync.Mutex
	stacks   map[string]string
	calls    []string
	upOutput string
	upErr    error
	upBlock  chan struct{} // when set, Up waits for it, or for its context
	upCtx    context.Context
}

func newFakeCompose() *fakeCompose { return &fakeCompose{stacks: map[string]string{}} }

func (f *fakeCompose) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeCompose) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeCompose) stack(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stacks[name]
}

func (f *fakeCompose) lastUpCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upCtx
}

func (f *fakeCompose) GetProjectYAML(_ context.Context, name string) (string, string, error) {
	f.record("get " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	y, ok := f.stacks[name]
	if !ok {
		return "", "", os.ErrNotExist
	}
	return y, "docker-compose.yml", nil
}

func (f *fakeCompose) CreateProject(_ context.Context, name, yaml string) (*docker.ComposeProject, error) {
	f.record("create " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.stacks[name]; ok {
		return nil, &projectExists{name}
	}
	f.stacks[name] = yaml
	return &docker.ComposeProject{Name: name}, nil
}

func (f *fakeCompose) UpdateProject(_ context.Context, name, yaml string) error {
	f.record("update " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stacks[name] = yaml
	return nil
}

// UpStream hands up's output to onLine a line at a time, as compose prints
// it, before it waits on upBlock.
func (f *fakeCompose) UpStream(ctx context.Context, name string, onLine func(string)) error {
	f.record("up " + name)
	f.mu.Lock()
	f.upCtx = ctx
	block, out, err := f.upBlock, f.upOutput, f.upErr
	f.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line != "" {
			onLine(line)
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			onLine("canceled")
			return ctx.Err()
		}
	}
	return err
}

// projectExists is docker.ErrProjectExists wrapped as CreateProject wraps it.
type projectExists struct{ name string }

func (e *projectExists) Error() string { return "project " + strconv.Quote(e.name) + " already exists" }
func (e *projectExists) Unwrap() error { return docker.ErrProjectExists }

// The preview's verdicts come from the same guards a run meets, step by step
// in the order sent, numbered as sent: a unit the host needs is named as
// needing confirmation, one it cannot lose is refused, and so on for each
// kind — a Proxmox host's reboot, a stack's forbidden and risky tiers, a
// credential path, a package name that is a flag.
func TestCheckNamesWhatNeedsConfirmation(t *testing.T) {
	platform.SetForTest(t, platform.Info{PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2})
	k := newKindNode(t, exec.NewMockCommander())
	k.h.Register(tasks.StepPackages, NewPackageRunner(newFakePackages(t)))
	safe := filepath.Join(t.TempDir(), "motd")

	specs := []tasks.JobSpec{
		kindSpec(0, tasks.StepService, func(s *tasks.JobSpec) {
			s.Service = &tasks.ServiceStep{Name: "pveproxy.service", Action: "stop"}
		}),
		kindSpec(1, tasks.StepService, func(s *tasks.JobSpec) {
			s.Service = &tasks.ServiceStep{Name: "pve-cluster.service", Action: "stop"}
		}),
		kindSpec(2, tasks.StepReboot, func(s *tasks.JobSpec) { s.Reboot = &tasks.RebootStep{} }),
		kindSpec(3, tasks.StepStack, func(s *tasks.JobSpec) {
			s.Stack = &tasks.StackStep{Name: "web", Compose: riskyCompose}
		}),
		kindSpec(4, tasks.StepStack, func(s *tasks.JobSpec) {
			s.Stack = &tasks.StackStep{Name: "web", Compose: forbiddenCompose}
		}),
		kindSpec(5, tasks.StepFile, func(s *tasks.JobSpec) {
			s.File = &tasks.FileStep{Path: "/etc/sfpanel/config.yaml", Content: "x"}
		}),
		kindSpec(6, tasks.StepPackages, func(s *tasks.JobSpec) {
			s.Packages = &tasks.PackagesStep{Install: []string{"--purge"}}
		}),
		kindSpec(7, tasks.StepService, func(s *tasks.JobSpec) {
			s.Service = &tasks.ServiceStep{Name: "nginx.service", Action: "restart"}
		}),
		kindSpec(8, tasks.StepFile, func(s *tasks.JobSpec) { s.File = &tasks.FileStep{Path: safe, Content: "x"} }),
		kindSpec(9, tasks.StepStack, func(s *tasks.JobSpec) { s.Stack = &tasks.StackStep{Name: "web", Compose: plainCompose} }),
	}
	status, env := k.do(t, http.MethodPost, "/api/v1/task-jobs/check", specs, true)
	if status != http.StatusOK {
		t.Fatalf("check: %d %q", status, env.code())
	}
	got := verdicts(t, env)
	want := []struct {
		refused, confirm bool
		code             string
	}{
		{false, true, response.ErrServiceConfirmRequired},
		{true, false, response.ErrServiceProtected},
		{true, false, response.ErrTaskRebootPVEHost},
		{false, true, response.ErrComposeRisky},
		{true, false, response.ErrComposeForbidden},
		{true, false, response.ErrInvalidPath},
		{true, false, response.ErrInvalidPackageName},
		{false, false, ""},
		{false, false, ""},
		{false, false, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("%d verdicts, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		v := got[i]
		if v.Step != i || v.Refused != w.refused || v.NeedsConfirm != w.confirm || v.Code != w.code {
			t.Errorf("step %d: %+v, want step %d refused=%v needs_confirm=%v code %q", i, v, i, w.refused, w.confirm, w.code)
		}
		if (w.refused || w.confirm) && strings.TrimSpace(v.Reason) == "" {
			t.Errorf("step %d: %s without a reason", i, v.Code)
		}
	}

	// Away from Proxmox the same reboot is not refused.
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	_, env = k.do(t, http.MethodPost, "/api/v1/task-jobs/check", specs[2:3], true)
	if v := verdicts(t, env); len(v) != 1 || v[0].Refused || v[0].Step != 2 {
		t.Errorf("reboot off Proxmox: %+v, want allowed", v)
	}
}

func verdicts(t *testing.T, env envelope) []tasks.CheckVerdict {
	t.Helper()
	var v []tasks.CheckVerdict
	if err := json.Unmarshal(env.Data, &v); err != nil {
		t.Fatalf("verdicts %s: %v", env.Data, err)
	}
	return v
}

// waitStatus waits until the job's row is in status.
func (k *kindNode) waitStatus(t *testing.T, id, status string) record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := k.record(t, id)
		if r.status == status {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is %s, want %s", id, r.status, status)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
