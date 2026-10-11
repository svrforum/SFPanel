package network

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// netplanBaselinePath keeps the netplan files as they stood before the panel
// first rewrote them after the last confirmed apply: the configuration the
// host last ran on. A var so tests can move it.
//
// Save writes /etc/netplan and Apply only puts it into effect, so by the time
// Apply runs the files are already the new ones. A snapshot taken there
// "restored" the change it was meant to undo (#114). The baseline is on disk
// so that a panel restart between Save and Apply, an update for one, does
// not lose the way back, and 0600 because netplan files can hold Wi-Fi keys.
var netplanBaselinePath = "/var/lib/sfpanel/netplan-baseline.json"

// bootIDPath tells one boot from the next. A var so tests can move it.
var bootIDPath = "/proc/sys/kernel/random/boot_id"

type netplanBaseline struct {
	BootID string            `json:"boot_id"`
	Files  map[string][]byte `json:"files"`
}

func currentBootID() string {
	data, err := os.ReadFile(bootIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// recordNetplanBaseline keeps the files as they are now, before a save
// changes them, unless an earlier save since the last confirmed apply
// already did: two saves and then an apply go back to before the first.
func recordNetplanBaseline() error {
	if _, recorded, err := loadNetplanBaseline(); err == nil && recorded {
		return nil
	}
	files, err := snapshotNetplan()
	if err != nil {
		return err
	}
	data, err := json.Marshal(netplanBaseline{BootID: currentBootID(), Files: files})
	if err != nil {
		return err
	}
	dir := filepath.Dir(netplanBaselinePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".netplan-baseline-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, netplanBaselinePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// loadNetplanBaseline returns the files recorded by the first save since the
// last confirmed apply. recorded is false when there is none, or when it was
// recorded before the last boot: netplan applies /etc/netplan at boot, so
// after a reboot the files on disk are the ones the host is running.
func loadNetplanBaseline() (files map[string][]byte, recorded bool, err error) {
	data, err := os.ReadFile(netplanBaselinePath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var b netplanBaseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, false, err
	}
	if b.BootID != currentBootID() {
		clearNetplanBaseline()
		return nil, false, nil
	}
	if b.Files == nil {
		b.Files = map[string][]byte{}
	}
	return b.Files, true, nil
}

// clearNetplanBaseline drops the baseline once the configuration it guarded
// has been confirmed or rolled back.
func clearNetplanBaseline() {
	if err := os.Remove(netplanBaselinePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("netplan: could not remove the rollback baseline",
			"component", "network", "path", netplanBaselinePath, "error", err)
	}
}

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
	clearNetplanBaseline()
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
	clearNetplanBaseline()
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
