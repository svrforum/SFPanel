package packages

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// apt-get update on a Proxmox VE host without a subscription, as apt 3.2
// prints it under LC_ALL=C: the enterprise repository refuses, the others
// are refreshed.
const updateEnterprise401 = `Hit:1 http://deb.debian.org/debian trixie InRelease
Hit:2 http://download.proxmox.com/debian/pve trixie InRelease
Err:3 https://enterprise.proxmox.com/debian/pve trixie InRelease
  401  Unauthorized [IP: 203.0.113.10 443]
Reading package lists...
E: Failed to fetch https://enterprise.proxmox.com/debian/pve/dists/trixie/InRelease  401  Unauthorized [IP: 203.0.113.10 443]
E: The repository 'https://enterprise.proxmox.com/debian/pve trixie InRelease' is not signed.
`

// unitCommand is what systemd-run was asked to run, after its own "--".
func unitCommand(t *testing.T, f *fakeHost) []string {
	t.Helper()
	runs := f.calls("systemd-run")
	if len(runs) != 1 {
		t.Fatalf("systemd-run ran %d times, want 1: %+v", len(runs), f.all())
	}
	args := runs[0].Args
	i := slices.Index(args, "--")
	if i < 0 {
		t.Fatalf("no -- in %q", args)
	}
	return args[i+1:]
}

// The upgrade is not the request's child any more: PID 1 runs it as a
// transient service that writes to a log, and the request streams the log.
// The verb is the platform's: Proxmox VE requires dist-upgrade, and
// elsewhere "upgrade" alone held back the packages an update brings in.
func TestUpgradeRunsInATransientUnitWithThePlatformsVerb(t *testing.T) {
	for _, tc := range []struct {
		name string
		pve  bool
		verb []string
	}{
		{"proxmox", true, []string{"-y", "dist-upgrade"}},
		{"ubuntu", false, []string{"-y", "--with-new-pkgs", "upgrade"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, dir := txSetup(t, tc.pve)
			rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
			settle(t, h)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}

			want := append([]string{"/bin/sh", "-c", txScript, "sh", "apt-get"}, exec.AptDpkgOptions...)
			want = append(want, tc.verb...)
			if got := unitCommand(t, f); !reflect.DeepEqual(got, want) {
				t.Errorf("unit runs %q\nwant      %q", got, want)
			}

			args := f.calls("systemd-run")[0].Args
			tx := currentTx(t, h)
			log := filepath.Join(dir, tx.ID+".log")
			for _, a := range []string{"--unit=sfpanel-apt-" + tx.ID + ".service", "--collect", "StandardOutput=append:" + log, "--setenv=DEBIAN_FRONTEND=noninteractive"} {
				if !slices.Contains(args, a) {
					t.Errorf("systemd-run %q lacks %q", args, a)
				}
			}
			// A scope is forked by the caller: it would be the panel's child,
			// in the panel's sandbox, and die with it.
			if slices.Contains(args, "--scope") {
				t.Errorf("systemd-run %q makes a scope", args)
			}

			// The lists are refreshed first, in the panel, before the unit.
			calls := f.all()
			upd := slices.IndexFunc(calls, func(c exec.MockCall) bool { return c.Name == "apt-get" && slices.Equal(c.Args, []string{"update"}) })
			run := slices.IndexFunc(calls, func(c exec.MockCall) bool { return c.Name == "systemd-run" })
			if upd < 0 || upd > run {
				t.Errorf("apt-get update at %d, systemd-run at %d: %+v", upd, run, calls)
			}

			lines := sseData(rec.Body.String())
			for _, l := range []string{"Setting up example (1.0) ...", ">>> Packages upgraded successfully", "[DONE]"} {
				if !slices.Contains(lines, l) {
					t.Errorf("stream lacks %q:\n%s", l, rec.Body.String())
				}
			}
			if strings.Contains(rec.Body.String(), txExitMarker) {
				t.Errorf("the exit marker reached the page:\n%s", rec.Body.String())
			}
			if !strings.HasPrefix(rec.Body.String(), "id: "+tx.ID+"\n\n") {
				t.Errorf("the stream does not name its transaction first:\n%s", rec.Body.String())
			}
			if tx.Running || tx.Result != txSuccess || tx.ExitCode == nil || *tx.ExitCode != 0 {
				t.Errorf("transaction %+v, want a success with exit code 0", tx)
			}
		})
	}
}

// Upgrading named packages keeps install --only-upgrade, with dpkg's
// configuration-file answers like every other run.
func TestUpgradeOfNamedPackagesStaysOnlyUpgrade(t *testing.T) {
	h, f, _ := txSetup(t, true)
	rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{"packages":["openssh-client","sudo"]}`)
	settle(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	want := append([]string{"/bin/sh", "-c", txScript, "sh", "apt-get"}, exec.AptDpkgOptions...)
	want = append(want, "install", "--only-upgrade", "-y", "--", "openssh-client", "sudo")
	if got := unitCommand(t, f); !reflect.DeepEqual(got, want) {
		t.Errorf("unit runs %q\nwant      %q", got, want)
	}
}

// A Proxmox VE host without a subscription: the enterprise repository
// answers 401 and apt-get update exits 100. The other repositories were
// refreshed, so the upgrade goes on, and the refusal comes back as a warning
// that names the repository.
func TestUpgradeGoesOnPastARepositoryThatRefusedIt(t *testing.T) {
	h, f, _ := txSetup(t, true)
	f.setArgs("apt-get update", updateEnterprise401, errors.New("exit status 100"))

	rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	settle(t, h)

	if len(f.calls("systemd-run")) != 1 {
		t.Fatalf("the upgrade did not start after a partial update: %+v", f.all())
	}
	tx := currentTx(t, h)
	if tx.Result != txSuccess {
		t.Errorf("result %q, want success", tx.Result)
	}
	if len(tx.Warnings) != 1 || tx.Warnings[0].Code != response.ErrAPTRepoUnauthorized ||
		tx.Warnings[0].Details["uri"] != "https://enterprise.proxmox.com/debian/pve" {
		t.Errorf("warnings %+v, want APT_REPO_UNAUTHORIZED for the enterprise repository", tx.Warnings)
	}
	if !slices.Contains(sseData(rec.Body.String()), "WARNING: repository https://enterprise.proxmox.com/debian/pve refused access (401)") {
		t.Errorf("the stream does not say which repository refused:\n%s", rec.Body.String())
	}
}

// An update that refreshed nothing stops the upgrade before dpkg is touched.
func TestUpgradeStopsWhenTheListsCannotBeRead(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.setArgs("apt-get update", "E: Malformed entry 1 in list file /etc/apt/sources.list.d/x.list (Component)\nE: The list of sources could not be read.\n", errors.New("exit status 100"))

	rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	settle(t, h)

	if n := len(f.calls("systemd-run")); n != 0 {
		t.Errorf("systemd-run ran %d times after a failed update", n)
	}
	tx := currentTx(t, h)
	if tx.Result != txFailed {
		t.Errorf("result %q, want failed", tx.Result)
	}
	lines := sseData(rec.Body.String())
	if !slices.Contains(lines, "ERROR: apt-get update failed; nothing was changed") || lines[len(lines)-1] != "[DONE]" {
		t.Errorf("stream:\n%s", rec.Body.String())
	}
}

// apt-get failing in its unit is a failure, with apt's status.
func TestAFailedUnitIsAFailedTransaction(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.exit = 100
	rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	settle(t, h)
	tx := currentTx(t, h)
	if tx.Result != txFailed || tx.ExitCode == nil || *tx.ExitCode != 100 {
		t.Errorf("transaction %+v, want failed with exit code 100", tx)
	}
	if !slices.Contains(sseData(rec.Body.String()), "ERROR: apt-get failed with exit status 100") {
		t.Errorf("stream:\n%s", rec.Body.String())
	}
}

// A unit stopped or killed before its script could say how apt ended is not
// a success: dpkg may have been left part-way.
func TestAUnitThatVanishedIsInterrupted(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.hold = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	}()
	eventually(t, "the unit to start", func() bool { return f.started() != "" })
	f.kill(f.started())
	<-done
	settle(t, h)
	if tx := currentTx(t, h); tx.Result != txInterrupted || tx.ExitCode != nil {
		t.Errorf("transaction %+v, want interrupted with no exit code", tx)
	}
}

// One transaction at a time: everything that would run apt is refused while
// one runs, with the code that lets the page follow the running one instead.
func TestASecondTransactionIsRefusedWhileOneRuns(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.hold = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	}()
	eventually(t, "the unit to start", func() bool { return f.started() != "" })
	first := currentTx(t, h)

	// Bounded, so that a second transaction let through ends in a failed
	// assertion here rather than in a stream that waits for its unit.
	call := func(t *testing.T, fn echo.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		return callCtx(t, ctx, fn, method, target, body)
	}
	for _, tc := range []struct {
		name string
		run  func() (int, string, map[string]string)
	}{
		{"upgrade", func() (int, string, map[string]string) {
			rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
			code, d := failure(t, rec)
			return rec.Code, code, d
		}},
		{"install-stream", func() (int, string, map[string]string) {
			rec := call(t, h.InstallPackageStream, http.MethodPost, "/", `{"name":"htop"}`)
			code, d := failure(t, rec)
			return rec.Code, code, d
		}},
		{"install", func() (int, string, map[string]string) {
			rec := call(t, h.InstallPackage, http.MethodPost, "/", `{"name":"htop"}`)
			code, d := failure(t, rec)
			return rec.Code, code, d
		}},
		{"remove", func() (int, string, map[string]string) {
			rec := call(t, h.RemovePackage, http.MethodPost, "/", `{"name":"htop"}`)
			code, d := failure(t, rec)
			return rec.Code, code, d
		}},
		{"check updates", func() (int, string, map[string]string) {
			rec := call(t, h.CheckUpdates, http.MethodGet, "/", "")
			code, d := failure(t, rec)
			return rec.Code, code, d
		}},
	} {
		status, code, details := tc.run()
		if status != http.StatusConflict || code != response.ErrAPTTransactionRunning ||
			details["id"] != first.ID || details["kind"] != txUpgrade {
			t.Errorf("%s while a transaction runs: %d %s %v, want 409 %s naming %s", tc.name, status, code, details, response.ErrAPTTransactionRunning, first.ID)
		}
	}
	if n := len(f.calls("systemd-run")); n != 1 {
		t.Errorf("systemd-run ran %d times, want 1", n)
	}
	if n := len(f.calls("apt-get")); n != 1 {
		t.Errorf("apt-get ran %d times, want the first transaction's update only: %+v", n, f.calls("apt-get"))
	}

	f.end(f.started(), 0)
	<-done
	settle(t, h)
}

// Something outside the panel holding dpkg (unattended-upgrades, a shell)
// refuses the transaction up front instead of letting it fail on the lock.
func TestATransactionWaitsForAForeignLockHolder(t *testing.T) {
	h, f, _ := txSetup(t, false)
	dpkgLockHeld = func() bool { return true }
	rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	if code, _ := failure(t, rec); rec.Code != http.StatusConflict || code != response.ErrAPTUpgradeError {
		t.Errorf("got %d %s, want 409 %s", rec.Code, code, response.ErrAPTUpgradeError)
	}
	if n := len(f.calls("systemd-run")) + len(f.calls("apt-get")); n != 0 {
		t.Errorf("ran apt or a unit while dpkg was held: %+v", f.all())
	}
	if currentTx(t, h) != nil {
		t.Error("a refused transaction was recorded")
	}
}

// A panel that restarted while apt ran has forgotten the transaction; its
// unit has not. The panel finds it, refuses a second one, and answers the
// page with the running one so it can follow it.
func TestARestartedPanelFindsTheRunningTransaction(t *testing.T) {
	h, f, dir := txSetup(t, false)
	const id = "20261012-101500-abcd"
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"id":"`+id+`","kind":"upgrade","packages":[],"command":"apt-get -y --with-new-pkgs upgrade","started_at":"2026-10-12T10:15:00Z"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".log"), []byte(">>> apt-get update\nUnpacking example (1.1) ...\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	unit := "sfpanel-apt-" + id + ".service"
	f.live = unit + " loaded active running SFPanel package upgrade\n"
	f.mu.Lock()
	f.active[unit], f.units[unit] = true, filepath.Join(dir, id+".log")
	f.mu.Unlock()

	tx := currentTx(t, h)
	if tx == nil || tx.ID != id || !tx.Running || tx.Kind != txUpgrade {
		t.Fatalf("transaction %+v, want %s running", tx, id)
	}
	rec := call(t, h.InstallPackageStream, http.MethodPost, "/", `{"name":"htop"}`)
	if code, d := failure(t, rec); rec.Code != http.StatusConflict || code != response.ErrAPTTransactionRunning || d["id"] != id {
		t.Errorf("got %d %s %v, want 409 %s naming %s", rec.Code, code, d, response.ErrAPTTransactionRunning, id)
	}

	f.end(unit, 0)
	settle(t, h)
	if tx := currentTx(t, h); tx.Running || tx.Result != txSuccess {
		t.Errorf("after its unit ended: %+v", tx)
	}
}

// The request that started the transaction going away — a closed tab, a
// proxy's idle timeout, the cluster relay's window — stops the streaming,
// never apt. Not even during the apt-get update before the unit exists.
func TestClosingTheStreamLeavesTheTransactionRunning(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.hold = true
	gate := make(chan struct{})
	f.updateGate = gate

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		callCtx(t, ctx, h.UpgradePackages, http.MethodPost, "/", `{}`)
	}()
	eventually(t, "apt-get update to start", func() bool { return len(f.calls("apt-get")) == 1 })
	if f.started() != "" {
		t.Fatal("the unit started before the update finished")
	}
	cancel() // the tab is closed while the lists refresh
	<-done
	close(gate)

	eventually(t, "the unit to start", func() bool { return f.started() != "" })
	if tx := currentTx(t, h); !tx.Running {
		t.Fatalf("the transaction stopped with its stream: %+v", tx)
	}

	// And a page following it again leaves while dpkg runs.
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		defer close(done)
		callCtx(t, ctx, h.FollowTransaction, http.MethodGet, "/", "")
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if tx := currentTx(t, h); !tx.Running {
		t.Fatalf("the transaction stopped with a follower's stream: %+v", tx)
	}
	for _, c := range f.all() {
		if c.Name == "systemctl" && len(c.Args) > 0 && (c.Args[0] == "stop" || c.Args[0] == "kill") {
			t.Errorf("the unit was stopped: %v", c)
		}
	}

	f.end(f.started(), 0)
	settle(t, h)
	if tx := currentTx(t, h); tx.Result != txSuccess {
		t.Errorf("transaction %+v, want success", tx)
	}
}

// Following a transaction again skips the lines the page already has, and
// ends the same way the first stream would have.
func TestFollowingATransactionSkipsWhatThePageHas(t *testing.T) {
	h, _, dir := txSetup(t, false)
	const id = "20261012-093000-0a1b"
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"id":"`+id+`","kind":"install","packages":["htop"],"command":"apt-get install -y -- htop","started_at":"2026-10-12T09:30:00Z"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	log := "one\ntwo\nthree\n" + txExitMarker + "0\n"
	if err := os.WriteFile(filepath.Join(dir, id+".log"), []byte(log), 0o640); err != nil {
		t.Fatal(err)
	}

	rec := call(t, h.FollowTransaction, http.MethodGet, "/?id="+id+"&skip=2", "")
	if got, want := sseData(rec.Body.String()), []string{"three", ">>> htop installed successfully", "[DONE]"}; !reflect.DeepEqual(got, want) {
		t.Errorf("skip=2 streamed %q, want %q", got, want)
	}
	rec = call(t, h.FollowTransaction, http.MethodGet, "/?id="+id, "")
	if got := sseData(rec.Body.String()); len(got) != 5 || got[0] != "one" {
		t.Errorf("from the start: %q", got)
	}

	rec = call(t, h.FollowTransaction, http.MethodGet, "/?id=../../etc/passwd", "")
	if code, _ := failure(t, rec); rec.Code != http.StatusBadRequest || code != response.ErrInvalidID {
		t.Errorf("a path for an id: %d %s", rec.Code, code)
	}
	rec = call(t, h.FollowTransaction, http.MethodGet, "/?id=20261012-093000-ffff", "")
	if code, _ := failure(t, rec); rec.Code != http.StatusNotFound || code != response.ErrNotFound {
		t.Errorf("an id with no log: %d %s", rec.Code, code)
	}
}

// apt can be silent for minutes; the stream is not, or a proxy closes it.
func TestTheStreamKeepsTheConnectionAlive(t *testing.T) {
	h, f, _ := txSetup(t, false)
	txKeepalive = 5 * time.Millisecond
	f.hold = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string)
	go func() {
		done <- callCtx(t, ctx, h.UpgradePackages, http.MethodPost, "/", `{}`).Body.String()
	}()
	eventually(t, "the unit to start", func() bool { return f.started() != "" })
	time.Sleep(30 * time.Millisecond)
	cancel()
	body := <-done
	if !strings.Contains(body, ": keepalive\n\n") {
		t.Errorf("no keepalive in a silent stream:\n%s", body)
	}
	f.end(f.started(), 0)
	settle(t, h)
}

// dpkg redraws its progress with carriage returns; the page gets the last
// state, and no bare CR, which ends a line in SSE.
func TestCarriageReturnsDoNotReachTheStream(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.unitOut = "(Reading database ... 5%\r(Reading database ... 100%\r(Reading database ... 41234 files and directories currently installed.)\n"
	rec := call(t, h.UpgradePackages, http.MethodPost, "/", `{}`)
	settle(t, h)
	if strings.Contains(rec.Body.String(), "\r") {
		t.Errorf("a CR reached the stream: %q", rec.Body.String())
	}
	if !slices.Contains(sseData(rec.Body.String()), "(Reading database ... 41234 files and directories currently installed.)") {
		t.Errorf("stream:\n%s", rec.Body.String())
	}
}

// Without systemd (a container, a bare process) the panel runs apt as its
// own child, as it always did, through the same log.
func TestWithoutSystemdTheTransactionRunsAsThePanelsChild(t *testing.T) {
	h, f, _ := txSetup(t, false)
	systemdActive = func() bool { return false }
	var ran []string
	prev := launchDirect
	launchDirect = func(tx *aptTx) (func(), error) {
		ran = tx.argv
		appendFile(tx.logPath(), "done\n"+txExitMarker+"0\n")
		return tx.finishFromLog, nil
	}
	t.Cleanup(func() { launchDirect = prev })

	call(t, h.InstallPackageStream, http.MethodPost, "/", `{"name":"htop"}`)
	settle(t, h)
	if n := len(f.calls("systemd-run")); n != 0 {
		t.Errorf("systemd-run ran %d times on a host without systemd", n)
	}
	if want := installArgv("htop"); !reflect.DeepEqual(ran, want) {
		t.Errorf("ran %q, want %q", ran, want)
	}
}

// The direct runner itself, on harmless commands: what they print reaches
// the log, and their status the transaction.
func TestLaunchDirectRecordsHowTheCommandEnded(t *testing.T) {
	for _, tc := range []struct {
		argv   []string
		result string
		code   int
	}{
		{[]string{"echo", "hello from the transaction"}, txSuccess, 0},
		{[]string{"false"}, txFailed, 1},
	} {
		tx := newTx(txInstall, []string{"example"}, tc.argv)
		tx.dir = t.TempDir()
		if err := tx.create(); err != nil {
			t.Fatal(err)
		}
		wait, err := launchDirect(tx)
		if err != nil {
			t.Fatal(err)
		}
		wait()
		v := tx.view()
		if v.Result != tc.result || v.ExitCode == nil || *v.ExitCode != tc.code {
			t.Errorf("%q: %+v, want %s with %d", tc.argv, v, tc.result, tc.code)
		}
		if data, _ := os.ReadFile(tx.logPath()); tc.code == 0 && !strings.Contains(string(data), "hello from the transaction\n") {
			t.Errorf("log: %q", data)
		}
	}
}

// Only the newest logs are kept.
func TestOldTransactionLogsArePruned(t *testing.T) {
	dir := t.TempDir()
	prev := txKeep
	txKeep = 2
	t.Cleanup(func() { txKeep = prev })
	ids := []string{"20261010-000000-0001", "20261011-000000-0002", "20261012-000000-0003"}
	for _, id := range ids {
		for _, ext := range []string{".log", ".json"} {
			if err := os.WriteFile(filepath.Join(dir, id+ext), nil, 0o640); err != nil {
				t.Fatal(err)
			}
		}
	}
	pruneTxLogs(dir, ids[2])
	for i, id := range ids {
		_, err := os.Stat(filepath.Join(dir, id+".log"))
		if gone := os.IsNotExist(err); gone != (i == 0) {
			t.Errorf("%s: removed=%v", id, gone)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ids[0]+".json")); !os.IsNotExist(err) {
		t.Error("the description of a pruned log was kept")
	}
}
