package taskjob

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/feature/packages"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// fakePackages is the packages module's job API: each transaction is a log
// the test writes to and ends. Like the real one it runs one transaction at
// a time, and reads apt's summary with the real ChangedPackages.
type fakePackages struct {
	mu      sync.Mutex
	dir     string
	started []pkgCall
	txs     map[string]*fakeTx
}

type pkgCall struct {
	kind   string
	names  []string
	update bool
	id     string
}

type fakeTx struct {
	running bool
	result  string
	code    int
}

func newFakePackages(t *testing.T) *fakePackages {
	return &fakePackages{dir: t.TempDir(), txs: map[string]*fakeTx{}}
}

func (f *fakePackages) StartJobTransaction(kind string, names []string, update bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, tx := range f.txs {
		if tx.running {
			return "", &packages.Refusal{Status: http.StatusConflict, Code: response.ErrAPTTransactionRunning, Message: "already running " + id}
		}
	}
	id := fmt.Sprintf("20261101-090000-%04x", len(f.started))
	f.started = append(f.started, pkgCall{kind, names, update, id})
	f.txs[id] = &fakeTx{running: true}
	if err := os.WriteFile(f.logPath(id), []byte(">>> "+kind+" "+strings.Join(names, " ")+"\n"), 0o600); err != nil {
		panic(err)
	}
	return id, nil
}

func (f *fakePackages) JobTransaction(id string) (bool, string, int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx := f.txs[id]
	if tx == nil {
		return false, "", -1, "", packages.ErrNoTransaction
	}
	return tx.running, tx.result, tx.code, f.logPath(id), nil
}

func (f *fakePackages) ChangedPackages(logPath string) (int, bool) {
	return (&packages.Handler{}).ChangedPackages(logPath)
}

func (f *fakePackages) logPath(id string) string { return filepath.Join(f.dir, id+".log") }

// calls is what was started so far.
func (f *fakePackages) calls() []pkgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.started)
}

// next waits for the nth transaction to start and returns it.
func (f *fakePackages) next(t *testing.T, nth int) pkgCall {
	t.Helper()
	eventually(t, fmt.Sprintf("transaction %d", nth+1), func() bool { return len(f.calls()) > nth })
	return f.calls()[nth]
}

// end is transaction id printing out and ending with result.
func (f *fakePackages) end(t *testing.T, id, out, result string, code int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := os.OpenFile(f.logPath(id), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(out); err != nil {
		t.Fatal(err)
	}
	file.Close()
	tx := f.txs[id]
	tx.running, tx.result, tx.code = false, result, code
}

func packagesSpec(run string, p tasks.PackagesStep) tasks.JobSpec {
	s := spec(run, 0)
	s.Kind, s.Command, s.Packages = tasks.StepPackages, nil, &p
	return s
}

func aptSummary(upgraded, installed, removed int) string {
	return fmt.Sprintf("%d upgraded, %d newly installed, %d to remove and 4 not upgraded.\n", upgraded, installed, removed)
}

// update is a transaction of its own, run to its end before the step's other
// transaction starts — the lists an install reads are the fresh ones.
func TestAPackagesJobRunsUpdateFirst(t *testing.T) {
	f, pk := newFakeSystemd(), newFakePackages(t)
	n := newRunnerNode(t, f, pk)
	s := packagesSpec(runA, tasks.PackagesStep{Update: true, Install: []string{"htop", "jq"}})
	startOK(t, n, s)

	first := pk.next(t, 0)
	if first.kind != "update" || first.names != nil || first.update {
		t.Fatalf("first transaction %+v, want update alone", first)
	}
	time.Sleep(20 * time.Millisecond)
	if len(pk.calls()) != 1 {
		t.Fatalf("a second transaction started while the update ran: %+v", pk.calls())
	}
	if u := n.record(t, s.ID).unit; u != "" {
		t.Errorf("recorded unit %q during the update, which runs in the panel", u)
	}
	pk.end(t, first.id, "Hit:1 http://deb.debian.org/debian trixie InRelease\n", "success", 0)

	second := pk.next(t, 1)
	if second.kind != "install" || !slices.Equal(second.names, []string{"htop", "jq"}) || second.update {
		t.Fatalf("second transaction %+v, want install of htop and jq", second)
	}
	eventually(t, "the install's unit recorded", func() bool { return n.record(t, s.ID).unit == "sfpanel-apt-"+second.id+".service" })
	pk.end(t, second.id, aptSummary(0, 2, 0)+"Setting up jq ...\n", "success", 0)

	r := n.ended(t, s.ID)
	if r.status != tasks.JobSucceeded || !r.changed || !sameCode(r.exitCode, intPtr(0)) {
		t.Errorf("job ended %s changed=%v exit %v %q, want succeeded and changed", r.status, r.changed, deref(r.exitCode), r.summary)
	}
	log, _ := os.ReadFile(filepath.Join(n.h.LogDir, s.ID+".log"))
	for _, want := range []string{"trixie InRelease", "Setting up jq"} {
		if strings.Count(string(log), want) != 1 {
			t.Errorf("job log %q, want %q once", log, want)
		}
	}

	// upgrade: all is the platform's upgrade of everything; a failed update
	// stops the step there.
	f2, pk2 := newFakeSystemd(), newFakePackages(t)
	n2 := newRunnerNode(t, f2, pk2)
	s2 := packagesSpec(runB, tasks.PackagesStep{Update: true, UpgradeAll: true})
	startOK(t, n2, s2)
	pk2.end(t, pk2.next(t, 0).id, "E: some failure\n", "failed", 100)
	r2 := n2.ended(t, s2.ID)
	if r2.status != tasks.JobFailed || !sameCode(r2.exitCode, intPtr(100)) || len(pk2.calls()) != 1 {
		t.Errorf("job ended %s exit %v after %+v, want failed with 100 and nothing after the update", r2.status, deref(r2.exitCode), pk2.calls())
	}

	n3 := newRunnerNode(t, newFakeSystemd(), newFakePackages(t))
	pk3 := n3.h.runners[tasks.StepPackages].(*PackageRunner).Packages.(procPkgs).pkgs.(*fakePackages)
	s3 := packagesSpec(runB, tasks.PackagesStep{UpgradeAll: true})
	startOK(t, n3, s3)
	if c := pk3.next(t, 0); c.kind != "upgrade" || c.names != nil {
		t.Errorf("upgrade: all started %+v, want upgrade of everything", c)
	}
}

// A refusal is the packages module's, with its reason: on a Proxmox VE host
// an install that would bring a Docker engine is refused DOCKER_ON_PVE, as
// the Packages page refuses it. Nothing is installed.
func TestAPackagesJobRefusesDockerOnPVE(t *testing.T) {
	platform.SetForTest(t, platform.Info{OSID: "debian", OSVersionID: "13", PVE: true, PVEVersion: "9.2.3"})
	mock := exec.NewMockCommander()
	mock.SetOutput("apt-get", "Inst docker-buildx (0.13.1+ds1-3 Debian:13.1/stable [amd64])\nInst docker.io (26.1.5+dfsg1-9+b9 Debian:13.1/stable [amd64])\n", nil)
	f := newFakeSystemd()
	// The refusal comes before the packages module records a transaction,
	// so the real one runs here without a log directory of its own.
	n := newRunnerNode(t, f, &packages.Handler{Cmd: mock})
	s := packagesSpec(runA, tasks.PackagesStep{Install: []string{"docker-buildx"}})
	startOK(t, n, s)

	r := n.ended(t, s.ID)
	if r.status != tasks.JobFailed || r.changed || !strings.HasPrefix(r.summary, response.ErrDockerOnPVE+": ") || !strings.Contains(r.summary, "docker.io") {
		t.Errorf("job ended %s changed=%v %q, want failed, %s naming docker.io", r.status, r.changed, r.summary, response.ErrDockerOnPVE)
	}
	if len(f.called("systemd-run")) != 0 || slices.ContainsFunc(mock.Calls, func(c exec.MockCall) bool { return !slices.Contains(c.Args, "-s") }) {
		t.Errorf("something ran after the refusal: %+v %+v", f.called("systemd-run"), mock.Calls)
	}
}

// What a step changed is apt's own count, summed over its transactions. apt
// that printed no summary is not "nothing changed": the step says it could
// not count, and counts as a change.
func TestAPackagesJobCountsWhatChanged(t *testing.T) {
	for _, tc := range []struct {
		label     string
		step      tasks.PackagesStep
		outs      []string
		changed   bool
		inSummary string
	}{
		{"nothing to do", tasks.PackagesStep{UpgradeAll: true}, []string{aptSummary(0, 0, 0)}, false, "no packages changed"},
		{"an upgrade", tasks.PackagesStep{UpgradeAll: true}, []string{aptSummary(3, 1, 0)}, true, "4 packages changed"},
		{"an upgrade and a removal", tasks.PackagesStep{Upgrade: []string{"htop"}, Remove: []string{"jq"}}, []string{aptSummary(1, 0, 0), aptSummary(0, 0, 2)}, true, "3 packages changed"},
		{"no summary", tasks.PackagesStep{Install: []string{"htop"}}, []string{"Setting up htop ...\n"}, true, "not counted"},
		{"the lists alone", tasks.PackagesStep{Update: true}, []string{"Hit:1 http://deb.debian.org/debian trixie InRelease\n"}, false, "package lists updated"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			pk := newFakePackages(t)
			n := newRunnerNode(t, newFakeSystemd(), pk)
			s := packagesSpec(runA, tc.step)
			startOK(t, n, s)
			for i, out := range tc.outs {
				pk.end(t, pk.next(t, i).id, out, "success", 0)
			}
			r := n.ended(t, s.ID)
			if r.status != tasks.JobSucceeded || r.changed != tc.changed || !strings.Contains(r.summary, tc.inSummary) {
				t.Errorf("job ended %s changed=%v %q, want succeeded changed=%v %q", r.status, r.changed, r.summary, tc.changed, tc.inSummary)
			}
			if len(pk.calls()) != len(tc.outs) {
				t.Errorf("transactions %+v, want %d", pk.calls(), len(tc.outs))
			}
		})
	}
}

// A panel restart in the middle of a package step loses nothing: the job is
// taken back at the transaction it was in, its output goes on from where it
// was copied to, and the transactions after it still run.
func TestAPackagesJobOutlivesAPanelRestart(t *testing.T) {
	f, pk := newFakeSystemd(), newFakePackages(t)
	n := newRunnerNode(t, f, pk)
	s := packagesSpec(runA, tasks.PackagesStep{Update: true, UpgradeAll: true, Remove: []string{"jq"}})
	startOK(t, n, s)
	pk.end(t, pk.next(t, 0).id, "lists refreshed\n", "success", 0)
	upgrade := pk.next(t, 1)
	// Some output, copied before the panel stops.
	appendTo(t, pk.logPath(upgrade.id), aptSummary(2, 0, 0)+"Unpacking first ...\n")
	logPath := filepath.Join(n.h.LogDir, s.ID+".log")
	eventually(t, "the first output copied", func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), "Unpacking first")
	})

	r := n.restart(t, f, pk)
	if st := r.record(t, s.ID).status; st != tasks.JobRunning {
		t.Fatalf("after the restart the job is %s, want running", st)
	}
	pk.end(t, upgrade.id, "Unpacking second ...\n", "success", 0)
	remove := pk.next(t, 2)
	if remove.kind != "remove" || !slices.Equal(remove.names, []string{"jq"}) {
		t.Fatalf("after the upgrade %+v, want the removal of jq", remove)
	}
	pk.end(t, remove.id, aptSummary(0, 0, 1)+"Removing jq ...\n", "success", 0)

	got := r.ended(t, s.ID)
	if got.status != tasks.JobSucceeded || !got.changed || !strings.Contains(got.summary, "3 packages changed") {
		t.Errorf("job ended %s changed=%v %q, want succeeded, 3 packages changed", got.status, got.changed, got.summary)
	}
	log, _ := os.ReadFile(logPath)
	for _, want := range []string{"lists refreshed", "Unpacking first", "Unpacking second", "Removing jq"} {
		if strings.Count(string(log), want) != 1 {
			t.Errorf("job log %q, want %q once", log, want)
		}
	}
}

// Without force a package step runs to its end; with force the apt unit it
// is in is stopped and nothing after it starts.
func TestAPackagesJobIsCancelledOnlyWhenForced(t *testing.T) {
	f, pk := newFakeSystemd(), newFakePackages(t)
	n := newRunnerNode(t, f, pk)
	s := packagesSpec(runA, tasks.PackagesStep{Install: []string{"htop"}, Remove: []string{"jq"}})
	startOK(t, n, s)
	install := pk.next(t, 0)
	cancelURI := "/api/v1/task-jobs/" + s.ID + "/cancel"
	if status, env := n.do(t, http.MethodPost, cancelURI, map[string]bool{"force": false}, true); status != http.StatusConflict || env.code() != response.ErrTaskJobRunning {
		t.Fatalf("unforced cancel: %d %q, want 409 %s", status, env.code(), response.ErrTaskJobRunning)
	}
	if status, _ := n.do(t, http.MethodPost, cancelURI, map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel: %d", status)
	}
	unit := "sfpanel-apt-" + install.id + ".service"
	eventually(t, "the apt unit stopped", func() bool { return len(f.called("systemctl", "stop", unit)) == 1 })
	pk.end(t, install.id, "", "interrupted", -1)
	if r := n.ended(t, s.ID); r.status != tasks.JobCancelled {
		t.Errorf("job ended %s %q, want cancelled", r.status, r.summary)
	}
	if len(pk.calls()) != 1 {
		t.Errorf("transactions %+v, want none after the cancel", pk.calls())
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// The verdict before a run refuses a name apt-get could not take, for the
// reason the Packages page gives.
func TestAPackagesStepsNamesAreCheckedBeforeARun(t *testing.T) {
	n := newRunnerNode(t, newFakeSystemd(), newFakePackages(t))
	good := packagesSpec(runA, tasks.PackagesStep{Update: true, Install: []string{"htop", "libc6-dev"}})
	bad := packagesSpec(runA, tasks.PackagesStep{Remove: []string{"jq", "--purge"}})
	bad.Step, bad.ID = 1, runA+"-1"
	status, env := n.do(t, http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{good, bad}, true)
	var verdicts []tasks.CheckVerdict
	if err := json.Unmarshal(env.Data, &verdicts); status != http.StatusOK || err != nil || len(verdicts) != 2 {
		t.Fatalf("check: %d %s (%v)", status, env.Data, err)
	}
	if verdicts[0].Refused {
		t.Errorf("good names refused: %+v", verdicts[0])
	}
	if v := verdicts[1]; !v.Refused || v.Code != response.ErrInvalidPackageName || !strings.Contains(v.Reason, "--purge") {
		t.Errorf("verdict %+v, want refused %s naming --purge", v, response.ErrInvalidPackageName)
	}
}
