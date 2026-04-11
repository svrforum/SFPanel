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
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		slog.Warn("systemd daemon-reload after unit migration failed", "error", err)
	}
	return true, nil
}
