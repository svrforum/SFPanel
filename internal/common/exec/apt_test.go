package exec

import (
	"context"
	"os"
	osExec "os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A lock file nobody holds is available. The happy path before any apt run.
func TestDpkgLockHeldFalseWhenUnlocked(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock-frontend")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if dpkgLockHeldAt(lockPath) {
		t.Error("reported held for an unlocked file")
	}
}

// On a system without the file — anything not Debian-derived — report "not
// held" so apt produces the real error rather than a misleading 409 from a
// pre-check that cannot know.
func TestDpkgLockHeldFalseWhenFileMissing(t *testing.T) {
	if dpkgLockHeldAt("/nonexistent/sfpanel-test/path") {
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
		if dpkgLockHeldAt(lockPath) {
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
	want := []string{"install", "-y", "--", "nginx"}
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
