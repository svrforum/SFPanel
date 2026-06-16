package compose

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreDefinitionWritesFiles(t *testing.T) {
	root := t.TempDir()
	m := MigrationManifest{
		SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml",
		HasEnv: true, Disposition: DispositionRetain,
	}
	files := map[string][]byte{
		"compose/docker-compose.yml": []byte("services:\n  web:\n    image: nginx\n"),
		"compose/.env":               []byte("FOO=bar\n"),
	}
	if err := restoreDefinition(root, m, files); err != nil {
		t.Fatalf("restoreDefinition: %v", err)
	}
	yml, err := os.ReadFile(filepath.Join(root, "demo", "docker-compose.yml"))
	if err != nil || len(yml) == 0 {
		t.Fatalf("compose not written: %v", err)
	}
	env, err := os.ReadFile(filepath.Join(root, "demo", ".env"))
	if err != nil || string(env) != "FOO=bar\n" {
		t.Fatalf(".env not written: %v %q", err, env)
	}
	fi, _ := os.Stat(filepath.Join(root, "demo", ".env"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestRestoreDefinitionRejectsBadStackID(t *testing.T) {
	root := t.TempDir()
	m := MigrationManifest{SchemaVersion: 1, StackID: "../escape", ComposeFile: "docker-compose.yml"}
	if err := restoreDefinition(root, m, map[string][]byte{"compose/docker-compose.yml": []byte("services: {}")}); err == nil {
		t.Fatal("expected bad stack id to be rejected")
	}
}

func TestRestoreDefinitionRejectsTraversalNames(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"..", ".", "a..b", "foo/bar", ".hidden"} {
		m := MigrationManifest{SchemaVersion: 1, StackID: bad, ComposeFile: "docker-compose.yml"}
		err := restoreDefinition(root, m, map[string][]byte{"compose/docker-compose.yml": []byte("services: {}")})
		if err == nil {
			t.Errorf("stack id %q should be rejected", bad)
		}
	}
}

func TestRestoreDefinitionRejectsExtraFileEscape(t *testing.T) {
	root := t.TempDir()
	m := MigrationManifest{
		SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml",
		ExtraFiles: []string{"../escape.txt"},
	}
	files := map[string][]byte{
		"compose/docker-compose.yml": []byte("services: {}"),
		"compose/../escape.txt":      []byte("evil"),
	}
	if err := restoreDefinition(root, m, files); err == nil {
		t.Fatal("extra file escaping the stack dir must be rejected")
	}
}

func TestAbsBindRestorable(t *testing.T) {
	denied := []string{
		"/", "/etc", "/etc/passwd", "/usr/bin", "/var/lib/docker/volumes",
		"/var/lib/sfpanel", "/var/lib/sfpanel/cluster", "/var/log", "/var/spool/cron",
		"/home", "/home/user/.ssh", "/opt/stacks", "/opt/stacks/x", "/boot", "/root/.ssh",
		// relative Host (would resolve against CWD "/") must be refused
		"etc/cron.d", "../../etc/cron.d", "var/lib/sfpanel", "x/y",
	}
	for _, p := range denied {
		if absBindRestorable(p) {
			t.Errorf("absBindRestorable(%q) = true, want false (protected)", p)
		}
	}
	allowed := []string{"/srv/media", "/mnt/data", "/opt/appdata", "/data", "/docker/appdata"}
	for _, p := range allowed {
		if !absBindRestorable(p) {
			t.Errorf("absBindRestorable(%q) = false, want true", p)
		}
	}
}

func TestReadAllRejectsCorruptDataEntry(t *testing.T) {
	// Manifest declares a volume archive with a sha that won't match the bytes.
	m := MigrationManifest{
		SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml", Disposition: DispositionRetain,
		Volumes: []VolumeSpec{{Compose: "data", Docker: "demo_data", Copy: true, Archive: "volumes/demo_data.tar", Sha256: "00deadbeef"}},
	}
	var buf bytes.Buffer
	bw := NewBundleWriter(&buf)
	if err := bw.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	if err := bw.WriteFile("compose/docker-compose.yml", []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bw.WriteFile("volumes/demo_data.tar", []byte("not-the-expected-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bw.Close(); err != nil {
		t.Fatal(err)
	}
	r := NewBundleReader(bytes.NewReader(buf.Bytes()))
	if _, _, _, err := r.ReadAll(t.TempDir()); err == nil {
		t.Fatal("expected checksum-mismatch rejection for corrupt data entry")
	}
}

// TestRestoreDataBindGuards locks in the bind-restore safety fixes: an in-stack
// bind that collapses to the stack dir is refused (#2), and an abs bind to a
// NON-empty host path is refused WITHOUT deleting the pre-existing data (#1 — no
// RemoveAll of an attacker-influenceable absolute path). No docker needed.
func TestRestoreDataBindGuards(t *testing.T) {
	root := t.TempDir()
	stackDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real staged bind archive (top-level entry = "data").
	srcData := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(srcData, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcData, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	arc := filepath.Join(t.TempDir(), "b.tar")
	if _, _, err := archiveBindToFile(context.Background(), srcData, arc); err != nil {
		t.Fatal(err)
	}
	staged := map[string]string{"binds/b.tar": arc}

	// #2: in-stack bind with rel "." resolves to the stack dir itself → refused.
	mCollapse := MigrationManifest{StackID: "demo", Binds: []MountSpec{{Host: stackDir, Kind: "in-stack", Rel: ".", Copy: true, Archive: "binds/b.tar"}}}
	if _, _, err := restoreData(context.Background(), mCollapse, staged, root); err == nil {
		t.Fatal("in-stack bind collapsing to the stack dir must be refused")
	}

	// #1: abs bind to a NON-empty path → refused, and the pre-existing data is NOT deleted.
	absTgt := filepath.Join(t.TempDir(), "media")
	if err := os.MkdirAll(absTgt, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(absTgt, "existing.txt")
	if err := os.WriteFile(keep, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	mAbs := MigrationManifest{StackID: "demo", Binds: []MountSpec{{Host: absTgt, Kind: "abs", Copy: true, Archive: "binds/b.tar"}}}
	if _, _, err := restoreData(context.Background(), mAbs, staged, root); err == nil {
		t.Fatal("abs bind to a non-empty path must be refused")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("pre-existing data at the abs path must NOT be deleted")
	}
}
