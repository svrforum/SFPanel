package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDraftUsesBufferWithoutChangingProject(t *testing.T) {
	base := t.TempDir()
	manager := NewComposeManager(base, nil)
	const saved = "services:\n  app:\n    image: alpine\n"
	project, err := manager.CreateProject(context.Background(), "draft-test", saved)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// Fake CLI is the only executable used; no Docker daemon or production files.
	script := `#!/bin/sh
printf '%s\n' "$@" > "$DRAFT_CAPTURE/args"
pwd > "$DRAFT_CAPTURE/cwd"
cat > "$DRAFT_CAPTURE/stdin"
if /usr/bin/grep -q INVALID "$DRAFT_CAPTURE/stdin"; then
 echo 'invalid draft'; exit 1
fi
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("DRAFT_CAPTURE", bin)
	out, err := manager.ValidateDraft(context.Background(), "draft-test", "services: INVALID\n")
	if err == nil || !strings.Contains(out, "invalid draft") {
		t.Fatalf("draft was not validated: %q, %v", out, err)
	}
	input, _ := os.ReadFile(filepath.Join(bin, "stdin"))
	if string(input) != "services: INVALID\n" {
		t.Fatalf("wrong input: %q", input)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "args"))
	if !strings.Contains(string(args), "-f\n-\nconfig\n--quiet\n") {
		t.Fatalf("not stdin config validation: %s", args)
	}
	cwd, _ := os.ReadFile(filepath.Join(bin, "cwd"))
	if strings.TrimSpace(string(cwd)) != project.Path {
		t.Fatalf("relative files resolve in wrong directory: %s", cwd)
	}
	stored, _ := os.ReadFile(filepath.Join(project.Path, "docker-compose.yml"))
	if string(stored) != saved {
		t.Fatal("validation overwrote saved configuration")
	}
	if _, err := manager.ValidateDraft(context.Background(), "../escape", saved); err == nil {
		t.Fatal("accepted invalid project name")
	}
	if _, err := manager.ValidateDraft(context.Background(), "draft-test", saved); err != nil {
		t.Fatal(err)
	}
}
