// Package lifecycle contains install/upgrade glue for keeping sfpanel's
// on-disk systemd unit in sync with what the current binary expects.
package lifecycle

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
)

// unitPath is the file that scripts/install.sh writes. It's a var rather
// than a const so tests can point MigrateRestartPolicy at a temp file.
var unitPath = "/etc/systemd/system/sfpanel.service"

// systemdRuntimePath is created by PID-1 systemd on boot. Its presence is
// the canonical signal that this process is running under systemd — see
// `sd_booted(3)`. It's a var so tests can point IsSystemdActive at a temp
// directory.
var systemdRuntimePath = "/run/systemd/system"

// daemonReload runs `systemctl daemon-reload`. It's a var so tests can
// substitute a no-op: the real command blocks for the systemctl timeout on
// a host without a running systemd, which made the migration test a slow,
// environment-dependent outlier.
var daemonReload = func() error {
	return exec.Command("systemctl", "daemon-reload").Run()
}

// IsSystemdActive reports whether the current host is being managed by
// systemd (i.e. systemd is PID 1 and has set up its runtime directory).
//
// Used by update/restore handlers to decide between `systemctl restart`
// (systemd hosts) and a self-exit fallback (Docker containers, manual
// bare-process runs, supervisord-backed installs, etc.) where invoking
// systemctl would either fail outright or return success against the
// host's systemd rather than the container's process tree.
func IsSystemdActive() bool {
	st, err := os.Stat(systemdRuntimePath)
	if err != nil {
		return false
	}
	return st.IsDir()
}

// MigrateRestartPolicy rewrites an existing sfpanel systemd unit so the
// Restart= directive is "always" instead of "on-failure".
//
// Background: the cluster init/join/disband HTTP handlers intentionally
// exit with code 0 after responding to the client, expecting systemd to
// cycle the process so it picks up the new cluster config. With
// Restart=on-failure (what pre-fix installs shipped), systemd treats a
// clean exit as success and refuses to restart the service, leaving the
// panel dark. Rewriting the directive to Restart=always makes intentional
// exits valid supervisor wakeup signals again.
//
// Returns true if the unit file was modified (and daemon-reload issued),
// false if nothing needed to change or if no unit file is present at all
// — the latter is the common case on manual/dev deployments and is not
// an error.
func MigrateRestartPolicy() (bool, error) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	// Already migrated: nothing to do.
	if bytes.Contains(data, []byte("Restart=always")) {
		return false, nil
	}

	// Only touch the exact directive we know about. If an operator has set
	// Restart= to some other value (no, never, on-abort, ...) we leave
	// their choice alone and stay out of the way.
	updated := bytes.Replace(data, []byte("Restart=on-failure"), []byte("Restart=always"), 1)
	if bytes.Equal(updated, data) {
		return false, nil
	}

	if err := os.WriteFile(unitPath, updated, 0644); err != nil {
		return false, err
	}

	// daemon-reload is best-effort: if it fails (systemctl missing, not PID
	// 1, etc.) the migrated unit still takes effect on next boot. We log
	// the failure but don't propagate it because the file write — the part
	// the caller cares about — already succeeded.
	if err := daemonReload(); err != nil {
		slog.Warn("systemd daemon-reload after unit migration failed", "error", err)
	}
	return true, nil
}

// privateTmpLines are the two lines install.sh wrote from v0.13 to v0.78.11:
// the directive and its entry in the unit's hardening comment.
var privateTmpLines = map[string]bool{
	"PrivateTmp=true": true,
	"# - PrivateTmp: panel doesn't share /tmp with other services": true,
}

// MigratePrivateTmp drops the PrivateTmp=true that install.sh used to write.
// It gives the service a mount namespace of its own, and systemd does not
// propagate mounts from there to the host (systemd.exec(5) rules it out for
// services that mount for the host), so disks and network shares the panel
// mounted stayed invisible to the host, its shells and Docker until the next
// boot. The running process keeps its namespace until it restarts; the disk
// handlers mount in PID 1's namespace meanwhile.
//
// Only those exact lines are removed: a PrivateTmp an operator set some other
// way is theirs. Returns true if the unit file was modified (and
// daemon-reload issued); a missing unit is not an error.
func MigratePrivateTmp() (bool, error) {
	return dropUnitLines(privateTmpLines)
}

// restrictSUIDSGIDLines are the two lines install.sh wrote from v0.13.1 on:
// the directive and its entry in the unit's hardening comment. Every version
// wrote them exactly so.
var restrictSUIDSGIDLines = map[string]bool{
	"RestrictSUIDSGID=true": true,
	"# - RestrictSUIDSGID: panel never creates suid/sgid files": true,
}

// MigrateRestrictSUIDSGID drops the RestrictSUIDSGID=true that install.sh
// used to write. The comment beside it was true of the panel's own code and
// not of what the panel runs: the setting is a seccomp filter every child
// inherits, and dpkg sets the mode of each file it unpacks, so every package
// carrying a setuid or setgid file (passwd, sudo, openssh-client, mount,
// util-linux) failed to install or upgrade from the panel with "error setting
// permissions … Operation not permitted", and apt left it half-installed.
// A filter cannot be lifted from a running process, so this takes effect at
// the next restart; the Packages page's upgrade and install already run apt
// outside the panel, in a transient unit, and do not wait for it.
//
// Only those exact lines are removed: a RestrictSUIDSGID an operator set some
// other way is theirs. Returns true if the unit file was modified (and
// daemon-reload issued); a missing unit is not an error.
func MigrateRestrictSUIDSGID() (bool, error) {
	return dropUnitLines(restrictSUIDSGIDLines)
}

// dropUnitLines removes the unit's lines that, trimmed, are exactly one of
// lines, and reloads systemd if any were.
func dropUnitLines(lines map[string]bool) (bool, error) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	var kept []byte
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if lines[string(bytes.TrimSpace(line))] {
			continue
		}
		kept = append(kept, line...)
	}
	if bytes.Equal(kept, data) {
		return false, nil
	}

	if err := os.WriteFile(unitPath, kept, 0644); err != nil {
		return false, err
	}
	if err := daemonReload(); err != nil {
		slog.Warn("systemd daemon-reload after unit migration failed", "error", err)
	}
	return true, nil
}
