package compose

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// CleanupOrphanMigrationStaging sweeps migration scratch left under the stacks
// root by an import/migration that was killed mid-flight (process crash, host
// reboot, SIGKILL). Called once at boot. Best-effort: every branch logs and
// continues so a single stuck entry can't block startup.
//
//   - .mig-pkg-* / .migrate-stage-* : pure temp (bundle + staged archives).
//     Always safe to delete — nothing references them after the process dies.
//   - <id>.migbak : an overwrite import renamed the prior stack aside here before
//     wiping it, and crashed before restoring or removing it. If <id> no longer
//     exists, the prior tenant survives ONLY in the backup and nothing conflicts,
//     so restore it. If <id> exists, we can't safely tell which is authoritative
//     — leave the backup and warn the operator.
func CleanupOrphanMigrationStaging(composeRoot string) {
	entries, err := os.ReadDir(composeRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, ".mig-pkg-"), strings.HasPrefix(name, ".migrate-stage-"):
			if rerr := os.RemoveAll(filepath.Join(composeRoot, name)); rerr == nil {
				slog.Info("swept orphaned migration staging dir", "component", "compose", "dir", name)
			} else {
				slog.Warn("failed to sweep orphaned migration staging dir", "component", "compose", "dir", name, "error", rerr)
			}
		case strings.HasSuffix(name, ".migbak"):
			orig := strings.TrimSuffix(name, ".migbak")
			origPath := filepath.Join(composeRoot, orig)
			backupPath := filepath.Join(composeRoot, name)
			if _, statErr := os.Stat(origPath); os.IsNotExist(statErr) {
				// No live <id> to conflict — restore the prior tenant's definition.
				if rerr := os.Rename(backupPath, origPath); rerr == nil {
					slog.Warn("restored prior stack from a leftover migration backup", "component", "compose", "stack", orig)
				} else {
					slog.Error("failed to restore prior stack from migration backup", "component", "compose", "stack", orig, "error", rerr)
				}
			} else {
				slog.Warn("leftover migration backup present; manual cleanup may be needed", "component", "compose", "backup", name)
			}
		}
	}
}
