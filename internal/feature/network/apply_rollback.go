package network

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// netplanRollbackTimeout is how long an applied network change has to be
// confirmed before it is undone.
//
// Sixty seconds, matching the tuning module. Long enough for a page to reload
// over the new configuration and send one request; short enough that an
// operator who has just cut themselves off is not waiting on it.
const netplanRollbackTimeout = 60 * time.Second

// netplanRollback holds the files to put back if a confirmation never comes.
//
// ApplyNetplan's own comment has always named this risk — "remote admins who
// misconfigure their primary interface shouldn't be able to brick the server
// with one click" — and settled for `generate` catching syntax errors, because
// `netplan try` needs an interactive terminal an HTTP handler does not have.
// That covers a malformed file. It does not cover a perfectly valid one that
// happens to move the host off the address the operator is reaching it on,
// which is the way this actually goes wrong.
//
// The tuning module answered the same problem years ago: apply, arm a timer,
// revert unless someone confirms from a connection that still works. If the
// network came back, the confirmation arrives. If it did not, nothing can
// arrive, and that silence is the signal.
var netplanRollback = struct {
	sync.Mutex
	timer    *time.Timer
	files    map[string][]byte // path -> contents before the apply
	dir      string
	cmd      exec.Commander
	deadline time.Time
}{}

// snapshotNetplan reads every netplan file so they can be restored verbatim.
func snapshotNetplan() (map[string][]byte, error) {
	paths, err := findNetplanFiles()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out[p] = data
	}
	return out, nil
}

// armNetplanRollback starts the countdown after a successful apply.
func armNetplanRollback(files map[string][]byte, dir string, cmd exec.Commander) time.Time {
	netplanRollback.Lock()
	defer netplanRollback.Unlock()

	if netplanRollback.timer != nil {
		netplanRollback.timer.Stop()
	}
	netplanRollback.files = files
	netplanRollback.dir = dir
	netplanRollback.cmd = cmd
	netplanRollback.deadline = time.Now().Add(netplanRollbackTimeout)
	netplanRollback.timer = time.AfterFunc(netplanRollbackTimeout, performNetplanRollback)
	return netplanRollback.deadline
}

// confirmNetplan cancels the countdown. Reports whether one was pending.
func confirmNetplan() bool {
	netplanRollback.Lock()
	defer netplanRollback.Unlock()

	if netplanRollback.timer == nil {
		return false
	}
	netplanRollback.timer.Stop()
	clearNetplanRollbackLocked()
	return true
}

func clearNetplanRollbackLocked() {
	netplanRollback.timer = nil
	netplanRollback.files = nil
	netplanRollback.dir = ""
	netplanRollback.cmd = nil
	netplanRollback.deadline = time.Time{}
}

// performNetplanRollback restores the snapshot and re-applies it.
func performNetplanRollback() {
	netplanRollback.Lock()
	defer netplanRollback.Unlock()

	if netplanRollback.files == nil {
		return
	}
	slog.Warn("netplan auto-rollback: no confirmation received, restoring the previous configuration",
		"component", "network", "files", len(netplanRollback.files))

	// Files the apply introduced are removed, not left behind: a new
	// 99-sfpanel.yaml that overrode the old one would keep overriding it
	// after the originals were put back.
	if paths, err := findNetplanFiles(); err == nil {
		for _, p := range paths {
			if _, existed := netplanRollback.files[p]; !existed {
				if err := os.Remove(p); err != nil {
					slog.Error("netplan auto-rollback: could not remove a file the apply added",
						"component", "network", "path", p, "error", err)
				}
			}
		}
	}

	for path, data := range netplanRollback.files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			slog.Error("netplan auto-rollback: could not recreate directory",
				"component", "network", "path", path, "error", err)
			continue
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			slog.Error("netplan auto-rollback: could not restore file",
				"component", "network", "path", path, "error", err)
		}
	}

	if netplanRollback.cmd != nil {
		if out, err := netplanRollback.cmd.Run("netplan", "apply"); err != nil {
			slog.Error("netplan auto-rollback: re-apply failed", "component", "network",
				"error", err, "output", out)
		}
	}
	invalidateNetworkCache()
	clearNetplanRollbackLocked()
	slog.Info("netplan auto-rollback completed", "component", "network")
}

// pendingNetplanRollback reports the deadline of an unconfirmed apply.
func pendingNetplanRollback() (time.Time, bool) {
	netplanRollback.Lock()
	defer netplanRollback.Unlock()
	if netplanRollback.timer == nil {
		return time.Time{}, false
	}
	return netplanRollback.deadline, true
}
