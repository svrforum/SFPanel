package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrDpkgLocked reports that another process holds the package manager.
//
// Returned instead of letting apt block on the lock for the whole timeout:
// "something else is installing right now, try again" is an answer, and a
// request that hangs for five minutes and then times out is not.
var ErrDpkgLocked = errors.New("another package operation is already running")

// dpkgFrontendLockPath is where apt takes its package-manager mutex.
const dpkgFrontendLockPath = "/var/lib/dpkg/lock-frontend"

// AptEnv is the environment every apt invocation needs.
//
// Without DEBIAN_FRONTEND=noninteractive, a package with a debconf prompt —
// a kernel upgrade asking about a config file, iptables-persistent asking to
// save rules — blocks forever on a terminal that does not exist, and the
// operator sees a request that hangs until the timeout with no idea why.
// Three of the six apt callers used to omit it.
func AptEnv() []string {
	return []string{"DEBIAN_FRONTEND=noninteractive"}
}

// DpkgLockHeld reports whether another process currently holds the dpkg
// front-end lock.
//
// False positives are possible — the lock can be taken between this check and
// apt starting — but the cost is a slightly premature error message, and apt
// fails cleanly either way.
func DpkgLockHeld() bool { return dpkgLockHeldAt(dpkgFrontendLockPath) }

// dpkgLockHeldAt takes the path so tests can point at a tempfile.
func dpkgLockHeldAt(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false // missing or unreadable — let apt produce the real error
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// EWOULDBLOCK/EAGAIN means held. Anything else (a filesystem without
		// flock, say) is unknown, and unknown means let apt try.
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// AptInstall installs packages, the same way everywhere.
//
// Six handlers shelled out to apt-get and no two agreed: some set
// DEBIAN_FRONTEND and some did not, some bound the subprocess to the request
// and some let it outlive the caller, one had its own copy of the environment
// helper, and only one checked the dpkg lock first. The differences were not
// decisions — the Commander had no method carrying both an environment and a
// context, so every caller gave one of them up.
//
// The "--" matters: a package name is operator input, and without the
// terminator a name beginning with a dash is read as a flag.
func AptInstall(ctx context.Context, cmd Commander, packages ...string) (string, error) {
	if len(packages) == 0 {
		return "", fmt.Errorf("no packages given")
	}
	if DpkgLockHeld() {
		return "", ErrDpkgLocked
	}
	args := append([]string{"install", "-y", "--"}, packages...)
	return cmd.RunWithEnvCtx(ctx, AptEnv(), "apt-get", args...)
}

// AptUpdate refreshes the package lists.
func AptUpdate(ctx context.Context, cmd Commander) (string, error) {
	if DpkgLockHeld() {
		return "", ErrDpkgLocked
	}
	return cmd.RunWithEnvCtx(ctx, AptEnv(), "apt-get", "update")
}

// AptRemove removes packages.
func AptRemove(ctx context.Context, cmd Commander, packages ...string) (string, error) {
	if len(packages) == 0 {
		return "", fmt.Errorf("no packages given")
	}
	if DpkgLockHeld() {
		return "", ErrDpkgLocked
	}
	args := append([]string{"remove", "-y", "--"}, packages...)
	return cmd.RunWithEnvCtx(ctx, AptEnv(), "apt-get", args...)
}
