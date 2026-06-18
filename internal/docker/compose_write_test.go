package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomic_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	if err := writeFileAtomic(path, []byte("services: {}\n"), 0600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "services: {}\n" {
		t.Fatalf("content=%q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("new file mode=%o want 0600", info.Mode().Perm())
	}

	// No temp file litter after a successful rename.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}

func TestWriteFileAtomic_PreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("content=%q want %q", data, "new")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0644 {
		t.Fatalf("mode=%o want preserved 0644", info.Mode().Perm())
	}
}

func TestWriteFileAtomic_ErrorOnMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "f.yml")
	if err := writeFileAtomic(path, []byte("x"), 0600); err == nil {
		t.Fatal("expected error writing into missing directory")
	}
}

func TestCreateProject_WritesCompose0600(t *testing.T) {
	m := NewComposeManager(t.TempDir(), nil)
	ctx := context.Background()

	p, err := m.CreateProject(ctx, "proj", "services: {}\n")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	yamlPath := filepath.Join(p.Path, "docker-compose.yml")
	info, err := os.Stat(yamlPath)
	if err != nil {
		t.Fatalf("stat compose file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("compose file mode=%o want 0600", info.Mode().Perm())
	}

	if _, err := m.CreateProject(ctx, "proj", "services: {}\n"); err == nil {
		t.Fatal("duplicate CreateProject should fail")
	}
}

func TestUpdateProject_PreservesExistingMode(t *testing.T) {
	baseDir := t.TempDir()
	m := NewComposeManager(baseDir, nil)
	ctx := context.Background()

	projDir := filepath.Join(baseDir, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yamlPath := filepath.Join(projDir, "compose.yaml")
	if err := os.WriteFile(yamlPath, []byte("old"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := m.UpdateProject(ctx, "proj", "services: {}\n"); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	data, _ := os.ReadFile(yamlPath)
	if string(data) != "services: {}\n" {
		t.Fatalf("content=%q", data)
	}
	info, _ := os.Stat(yamlPath)
	if info.Mode().Perm() != 0644 {
		t.Fatalf("mode=%o want preserved 0644", info.Mode().Perm())
	}
}

func TestUpdateProjectEnv_Writes0600(t *testing.T) {
	baseDir := t.TempDir()
	m := NewComposeManager(baseDir, nil)
	ctx := context.Background()

	if _, err := m.CreateProject(ctx, "proj", "services: {}\n"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := m.UpdateProjectEnv(ctx, "proj", "SECRET=x\n"); err != nil {
		t.Fatalf("UpdateProjectEnv: %v", err)
	}
	envPath := filepath.Join(baseDir, "proj", ".env")
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat .env: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf(".env mode=%o want 0600", info.Mode().Perm())
	}
}

func TestValidateProjectNameReservesMigrationNamespace(t *testing.T) {
	m := NewComposeManager(t.TempDir(), nil)
	for _, bad := range []string{"foo.migbak", ".mig-pkg-1", ".migrate-stage-x", ".hidden"} {
		if err := m.validateProjectName(bad); err == nil {
			t.Errorf("reserved name %q should be refused", bad)
		}
	}
	for _, ok := range []string{"foo", "my-stack", "n8n", "app.v2"} {
		if err := m.validateProjectName(ok); err != nil {
			t.Errorf("legit name %q rejected: %v", ok, err)
		}
	}
}
