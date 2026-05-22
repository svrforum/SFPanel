package appstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomic_ModeAndContents asserts the helper honours the
// requested file mode and writes the exact bytes. This is the regression
// guard for the 0o644 -> 0o600 tightening: compose YAML carries inline
// secrets through `environment:` blocks, and any future caller that
// re-introduces a wider mode flips this test.
func TestWriteFileAtomic_ModeAndContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	payload := []byte("services:\n  app:\n    image: example/app\n")
	if err := writeFileAtomic(path, payload, 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 0600", got)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("contents mismatch: got %q want %q", got, payload)
	}
}

// TestWriteFileAtomic_NoTempLeftover walks the directory after a successful
// write and refuses to find any *.sfpanel.tmp residue. A crash between
// WriteFile and Rename is the only way one survives; in the success path,
// the rename must move the temp into place atomically.
func TestWriteFileAtomic_NoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	if err := writeFileAtomic(path, []byte("x: 1\n"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sfpanel.tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestWriteFileAtomic_OverwriteExisting verifies that a second write to the
// same path replaces the contents (rename-over-existing) and preserves the
// requested mode. This is the normal "re-install over a prior partial" path.
func TestWriteFileAtomic_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	// Pre-seed with a wider mode + different content to confirm both are
	// overwritten by the atomic write.
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode after overwrite = %o, want 0600", got)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("contents after overwrite = %q, want \"new\"", got)
	}
}
