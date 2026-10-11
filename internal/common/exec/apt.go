package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
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

// AptParseEnv is AptEnv for a run whose output is read. apt translates its
// messages, and the panel's environment carries the host's language: on a
// Korean or German host "upgradable from:" and "Failed to fetch" never appear.
// LANG=C would not do, as LC_ALL and LC_MESSAGES outrank it.
func AptParseEnv() []string {
	return append(AptEnv(), "LC_ALL=C")
}

// AptDpkgOptions settle dpkg's own configuration-file question, which
// DEBIAN_FRONTEND does not reach: when an update changes a file the operator
// has edited, keep the operator's file (the maintainer's version is left
// beside it as .dpkg-dist), and take the maintainer's only where the file
// was never touched. Without them dpkg asks, reads end of file, and leaves the
// package half-configured until someone runs dpkg --configure -a by hand.
var AptDpkgOptions = []string{"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

// DpkgLockHeld reports whether another process currently holds the dpkg
// front-end lock.
//
// False positives are possible — the lock can be taken between this check and
// apt starting — but the cost is a slightly premature error message, and apt
// fails cleanly either way.
func DpkgLockHeld() bool {
	held, _ := dpkgLockHolderAt(dpkgFrontendLockPath)
	return held
}

// dpkgLockError is ErrDpkgLocked naming who holds the lock, when that is known.
func dpkgLockError() error {
	held, pid := dpkgLockHolderAt(dpkgFrontendLockPath)
	if !held {
		return ErrDpkgLocked
	}
	if pid > 0 {
		comm, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
		if name := strings.TrimSpace(string(comm)); name != "" {
			return fmt.Errorf("%w: held by %s (pid %d)", ErrDpkgLocked, name, pid)
		}
		return fmt.Errorf("%w: held by pid %d", ErrDpkgLocked, pid)
	}
	return ErrDpkgLocked
}

// dpkgLockHolderAt takes the path so tests can point at a tempfile. pid is
// the holder's when the lock is a record lock, else 0.
//
// apt and dpkg take this lock with fcntl. flock locks are a separate kind on
// Linux and never see those, so a flock probe — all this used to be — said
// "free" while apt-get was installing, and the panel started a second apt
// into apt's own lock error. F_GETLK sees them and needs no write access.
// flock is still tried after, for anything that does lock that way.
func dpkgLockHolderAt(path string) (held bool, pid int) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false, 0 // missing or unreadable — let apt produce the real error
	}
	defer f.Close()
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lk); err == nil && lk.Type != syscall.F_UNLCK {
		return true, int(lk.Pid)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// EWOULDBLOCK/EAGAIN means held. Anything else (a filesystem without
		// flock, say) is unknown, and unknown means let apt try.
		return errors.Is(err, syscall.EWOULDBLOCK), 0
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, 0
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
		return "", dpkgLockError()
	}
	args := append(append([]string{}, AptDpkgOptions...), "install", "-y", "--")
	args = append(args, packages...)
	return cmd.RunWithEnvCtx(ctx, AptEnv(), "apt-get", args...)
}

// AptUpdateError is apt-get update failing, with what failed.
type AptUpdateError struct {
	Err    error
	Output string
	// Partial is set when every error was a repository apt could not fetch:
	// the other repositories' lists were refreshed, and a package from them
	// can still be installed.
	Partial bool
	// Unauthorized lists the repositories that answered 401, as Proxmox's
	// enterprise repository does for a host without a subscription.
	Unauthorized []string
	// Failed lists the other repositories that could not be fetched.
	Failed []string
	// Errors are apt's E: lines.
	Errors []string
}

func (e *AptUpdateError) Error() string {
	if len(e.Errors) > 0 {
		return "apt-get update failed: " + strings.Join(e.Errors, "; ")
	}
	return "apt-get update failed: " + e.Err.Error()
}

func (e *AptUpdateError) Unwrap() error { return e.Err }

var (
	aptErrorLine = regexp.MustCompile(`(?m)^E: (.+)$`)
	aptFetchLine = regexp.MustCompile(`^Failed to fetch (\S+)\s+(.*)$`)
)

// classifyAptUpdate reads apt-get update's output, run under AptParseEnv.
//
// A repository that cannot be fetched produces "Failed to fetch <uri> <why>",
// then "Some index files failed to download", and with apt 2.x also "The
// repository '…' is not signed" or "does not have a Release file" for it.
// Anything else — a broken sources entry, a full disk — is a real failure.
func classifyAptUpdate(output string, err error) *AptUpdateError {
	ue := &AptUpdateError{Err: err, Output: output}
	seen := map[string]bool{}
	partial := true
	for _, m := range aptErrorLine.FindAllStringSubmatch(output, -1) {
		msg := strings.TrimSpace(m[1])
		ue.Errors = append(ue.Errors, msg)
		switch {
		case aptFetchLine.MatchString(msg):
			f := aptFetchLine.FindStringSubmatch(msg)
			repo := f[1]
			if i := strings.Index(repo, "/dists/"); i > 0 {
				repo = repo[:i]
			}
			if seen[repo] {
				continue
			}
			seen[repo] = true
			if strings.Contains(f[2], "401") {
				ue.Unauthorized = append(ue.Unauthorized, repo)
			} else {
				ue.Failed = append(ue.Failed, repo)
			}
		case strings.HasPrefix(msg, "Some index files failed to download"),
			strings.Contains(msg, "is not signed"),
			strings.Contains(msg, "does not have a Release file"):
		default:
			partial = false
		}
	}
	ue.Partial = partial && len(ue.Errors) > 0
	return ue
}

// AptUpdate refreshes the package lists. A failure is an *AptUpdateError.
func AptUpdate(ctx context.Context, cmd Commander) (string, error) {
	if DpkgLockHeld() {
		return "", dpkgLockError()
	}
	out, err := cmd.RunWithEnvCtx(ctx, AptParseEnv(), "apt-get", "update")
	if err != nil {
		return out, classifyAptUpdate(out, err)
	}
	return out, nil
}

// AptUpdateForInstall refreshes the lists before installing something.
//
// A repository apt could not fetch does not stop the install: the package
// usually comes from another one, whose lists were refreshed. A fresh Proxmox
// host without a subscription has its enterprise repository answer 401 to
// every update, and every installer in the panel used to give up there. That
// partial failure comes back as notice, for the caller to pass on as a
// warning; any other failure is err.
func AptUpdateForInstall(ctx context.Context, cmd Commander) (notice *AptUpdateError, err error) {
	_, err = AptUpdate(ctx, cmd)
	var ue *AptUpdateError
	if errors.As(err, &ue) && ue.Partial {
		return ue, nil
	}
	return nil, err
}

// AptRemove removes packages.
func AptRemove(ctx context.Context, cmd Commander, packages ...string) (string, error) {
	if len(packages) == 0 {
		return "", fmt.Errorf("no packages given")
	}
	if DpkgLockHeld() {
		return "", dpkgLockError()
	}
	args := append([]string{"remove", "-y", "--"}, packages...)
	return cmd.RunWithEnvCtx(ctx, AptEnv(), "apt-get", args...)
}
