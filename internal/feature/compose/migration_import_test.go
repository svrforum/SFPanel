package compose

import (
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
