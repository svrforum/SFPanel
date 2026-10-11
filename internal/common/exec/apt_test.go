package exec

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	osExec "os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A lock file nobody holds is available. The happy path before any apt run.
func TestDpkgLockHeldFalseWhenUnlocked(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock-frontend")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if held, _ := dpkgLockHolderAt(lockPath); held {
		t.Error("reported held for an unlocked file")
	}
}

// On a system without the file — anything not Debian-derived — report "not
// held" so apt produces the real error rather than a misleading 409 from a
// pre-check that cannot know.
func TestDpkgLockHeldFalseWhenFileMissing(t *testing.T) {
	if held, _ := dpkgLockHolderAt("/nonexistent/sfpanel-test/path"); held {
		t.Error("reported held for a missing file")
	}
}

// The path that matters: another process holds it. flock is reentrant for the
// same fd within a process, so this needs a child — an in-process test would
// pass without ever reaching EWOULDBLOCK.
func TestDpkgLockHeldTrueWhenAnotherProcessHoldsIt(t *testing.T) {
	flockBin, err := osExec.LookPath("flock")
	if err != nil {
		t.Skip("flock binary not available")
	}
	lockPath := filepath.Join(t.TempDir(), "lock-frontend")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := osExec.Command(flockBin, "-x", lockPath, "-c", "sleep 5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start flock child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if held, _ := dpkgLockHolderAt(lockPath); held {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("never observed the lock as held while a child process held it")
}

// Every apt call has to carry the same three things. Six handlers used to
// disagree: three omitted DEBIAN_FRONTEND, two let the subprocess outlive the
// request, one had its own copy of the environment helper, and only one
// checked the lock.
func TestAptInstallShapesTheCommandTheSameWayEveryTime(t *testing.T) {
	m := &MockCommander{}
	if _, err := AptInstall(context.Background(), m, "nginx"); err != nil {
		t.Fatalf("AptInstall: %v", err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(m.Calls))
	}
	got := m.Calls[0]
	if got.Name != "apt-get" {
		t.Errorf("ran %q, want apt-get", got.Name)
	}
	want := append(append([]string{}, AptDpkgOptions...), "install", "-y", "--", "nginx")
	if len(got.Args) != len(want) {
		t.Fatalf("args = %v, want %v", got.Args, want)
	}
	for i := range want {
		if got.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", got.Args, want)
		}
	}
}

// The terminator is not decoration: a package name is operator input, and
// without it a name starting with a dash is read as a flag.
func TestAptCommandsTerminateOptions(t *testing.T) {
	for name, run := range map[string]func(Commander) (string, error){
		"install": func(c Commander) (string, error) { return AptInstall(context.Background(), c, "-x") },
		"remove":  func(c Commander) (string, error) { return AptRemove(context.Background(), c, "-x") },
	} {
		t.Run(name, func(t *testing.T) {
			m := &MockCommander{}
			if _, err := run(m); err != nil {
				t.Fatal(err)
			}
			args := m.Calls[0].Args
			var sawTerminator bool
			for i, a := range args {
				if a == "--" {
					sawTerminator = true
					if i == len(args)-1 || args[i+1] != "-x" {
						t.Errorf("the package does not follow the terminator: %v", args)
					}
				}
			}
			if !sawTerminator {
				t.Errorf("args = %v, want a -- before the package name", args)
			}
		})
	}
}

func TestAptRefusesAnEmptyPackageList(t *testing.T) {
	m := &MockCommander{}
	if _, err := AptInstall(context.Background(), m); err == nil {
		t.Error("installed nothing without complaining")
	}
	if _, err := AptRemove(context.Background(), m); err == nil {
		t.Error("removed nothing without complaining")
	}
	if len(m.Calls) != 0 {
		t.Errorf("ran %v; nothing should have been executed", m.Calls)
	}
}

func TestAptEnvIsNoninteractive(t *testing.T) {
	env := AptEnv()
	if len(env) != 1 || env[0] != "DEBIAN_FRONTEND=noninteractive" {
		t.Errorf("AptEnv() = %v, want DEBIAN_FRONTEND=noninteractive", env)
	}
}

// TestHelperHoldFcntlLock is not a test: TestDpkgLockSeesAFcntlLock runs the
// test binary again with SFPANEL_FCNTL_LOCK set, and this takes the lock the
// way apt and dpkg do and holds it until killed.
func TestHelperHoldFcntlLock(t *testing.T) {
	path := os.Getenv("SFPANEL_FCNTL_LOCK")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		os.Exit(2)
	}
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLKW, &lk); err != nil {
		os.Exit(3)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(time.Minute)
	os.Exit(0)
}

// apt and dpkg lock with fcntl, which a flock probe never sees: the probe
// said "free" while apt-get was installing. The lock has to be held by
// another process, since a process does not see its own record locks.
func TestDpkgLockSeesAFcntlLockAndItsHolder(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock-frontend")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := osExec.Command(os.Args[0], "-test.run=^TestHelperHoldFcntlLock$")
	cmd.Env = append(os.Environ(), "SFPANEL_FCNTL_LOCK="+lockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper did not take the lock: %q %v", line, err)
	}

	held, pid := dpkgLockHolderAt(lockPath)
	if !held {
		t.Fatal("a lock held with fcntl by another process was reported free")
	}
	if pid != cmd.Process.Pid {
		t.Errorf("holder pid %d, want %d", pid, cmd.Process.Pid)
	}
}

// apt-get update on a host whose Proxmox enterprise repository refuses it,
// as apt 3.2 printed it under LC_ALL=C (the Debian repository's key was left
// out on purpose, so it failed too, as an unsigned one).
const aptUpdateEnterprise401 = `Get:1 http://deb.debian.org/debian trixie-updates InRelease [47.3 kB]
Err:1 http://deb.debian.org/debian trixie-updates InRelease
  The following signatures couldn't be verified because the public key is not available: NO_PUBKEY 6ED0E7B82643E131
Err:2 https://enterprise.proxmox.com/debian/pve trixie InRelease
  401  Unauthorized [IP: 203.0.113.10 443]
Reading package lists...
W: OpenPGP signature verification failed: http://deb.debian.org/debian trixie-updates InRelease: The following signatures couldn't be verified because the public key is not available: NO_PUBKEY 6ED0E7B82643E131
E: The repository 'http://deb.debian.org/debian trixie-updates InRelease' is not signed.
E: Failed to fetch https://enterprise.proxmox.com/debian/pve/dists/trixie/InRelease  401  Unauthorized [IP: 203.0.113.10 443]
E: The repository 'https://enterprise.proxmox.com/debian/pve trixie InRelease' is not signed.
`

func TestAptUpdateNamesTheRepositoryThatRefusedIt(t *testing.T) {
	ue := classifyAptUpdate(aptUpdateEnterprise401, errors.New("exit status 100"))
	if !ue.Partial {
		t.Error("only repositories failed; the others were refreshed")
	}
	if want := []string{"https://enterprise.proxmox.com/debian/pve"}; !reflect.DeepEqual(ue.Unauthorized, want) {
		t.Errorf("unauthorized %v, want %v", ue.Unauthorized, want)
	}
}

// A sources file apt cannot read is not a repository being down: nothing was
// refreshed, and installing on top of it is a guess.
func TestABrokenSourceListIsNotPartial(t *testing.T) {
	out := "E: Malformed entry 1 in list file /etc/apt/sources.list.d/x.list (Component)\nE: The list of sources could not be read.\n"
	if ue := classifyAptUpdate(out, errors.New("exit status 100")); ue.Partial {
		t.Errorf("classified as partial: %+v", ue)
	}
	if ue := classifyAptUpdate("", errors.New("signal: killed")); ue.Partial {
		t.Error("a failure with no apt errors at all was classified as partial")
	}
}

// Every installer used to stop at a failed update. A partial one is a
// warning now, and the install goes ahead.
func TestAptUpdateForInstallGoesOnPastARepositoryThatFailed(t *testing.T) {
	m := &MockCommander{Outputs: map[string]MockResult{"apt-get": {Output: aptUpdateEnterprise401, Err: errors.New("exit status 100")}}}
	notice, err := AptUpdateForInstall(context.Background(), m)
	if err != nil {
		t.Fatalf("a partial update stopped the install: %v", err)
	}
	if notice == nil || len(notice.Unauthorized) != 1 {
		t.Errorf("the refusing repository was not passed on: %+v", notice)
	}

	m = &MockCommander{Outputs: map[string]MockResult{"apt-get": {Output: "E: The list of sources could not be read.\n", Err: errors.New("exit status 100")}}}
	if _, err := AptUpdateForInstall(context.Background(), m); err == nil {
		t.Error("an update that refreshed nothing let the install go ahead")
	}
	var ue *AptUpdateError
	if !errors.As(err, &ue) && err != nil {
		t.Errorf("error is %T, want *AptUpdateError", err)
	}
}
