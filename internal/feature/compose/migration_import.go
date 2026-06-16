package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var validStackID = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// restoreDefinition writes the stack's compose + .env + extra files under
// composeRoot/<stackId>/. The stackId is validated (no traversal) and bundle
// entry names were already validated by the reader. Files keep 0600/0755.
func restoreDefinition(composeRoot string, m MigrationManifest, files map[string][]byte) error {
	if !validStackID.MatchString(m.StackID) {
		return fmt.Errorf("invalid stack id %q", m.StackID)
	}
	stackDir := filepath.Join(composeRoot, m.StackID)
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		return fmt.Errorf("create stack dir: %w", err)
	}
	composeData, ok := files["compose/"+m.ComposeFile]
	if !ok {
		return fmt.Errorf("bundle missing compose file %q", m.ComposeFile)
	}
	if err := writeFileAtomic(filepath.Join(stackDir, m.ComposeFile), composeData, 0o600); err != nil {
		return fmt.Errorf("write compose: %w", err)
	}
	if m.HasEnv {
		if env, ok := files["compose/.env"]; ok {
			if err := writeFileAtomic(filepath.Join(stackDir, ".env"), env, 0o600); err != nil {
				return fmt.Errorf("write .env: %w", err)
			}
		}
	}
	for _, extra := range m.ExtraFiles {
		if data, ok := files["compose/"+extra]; ok {
			dst := filepath.Join(stackDir, filepath.Clean(extra))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("create dir for %s: %w", extra, err)
			}
			if err := writeFileAtomic(dst, data, 0o600); err != nil {
				return fmt.Errorf("write extra %s: %w", extra, err)
			}
		}
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file + rename so a crash
// mid-write can't leave a partial file. The mode is applied before rename.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-migrate-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
