package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withUnitPath temporarily points the package-level unitPath at p for the
// duration of a single test.
func withUnitPath(t *testing.T, p string) {
	t.Helper()
	prev := unitPath
	unitPath = p
	t.Cleanup(func() { unitPath = prev })
}

func TestMigrateRestartPolicy_RewritesOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service")
	original := "[Service]\nType=simple\nExecStart=/usr/local/bin/sfpanel\nRestart=on-failure\nRestartSec=5\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrated=true for Restart=on-failure unit")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Restart=always") {
		t.Errorf("unit file was not rewritten:\n%s", got)
	}
	if strings.Contains(string(got), "Restart=on-failure") {
		t.Errorf("Restart=on-failure still present after migration:\n%s", got)
	}
	// Make sure we didn't lose unrelated lines.
	if !strings.Contains(string(got), "ExecStart=/usr/local/bin/sfpanel") {
		t.Errorf("unrelated directive lost during migration:\n%s", got)
	}
}

func TestMigrateRestartPolicy_AlreadyMigratedIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service")
	original := "[Service]\nRestart=always\nRestartSec=5\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false for already-Restart=always unit")
	}

	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("unit file was modified even though migration was a noop:\n%s", got)
	}
}

func TestMigrateRestartPolicy_MissingUnitIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service") // intentionally not created
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error for missing unit: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false when no unit file exists")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("MigrateRestartPolicy should not create the unit file from scratch")
	}
}

func TestMigrateRestartPolicy_UnknownRestartValueLeftAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service")
	original := "[Service]\nRestart=on-abort\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false for unknown Restart= value")
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("unit file with unknown Restart= was rewritten — we should not overrule the operator:\n%s", got)
	}
}
