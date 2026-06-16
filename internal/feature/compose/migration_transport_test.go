package compose

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageThenReceiveRoundTrip(t *testing.T) {
	stackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), []byte("services:\n  web:\n    image: nginx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, ".env"), []byte("FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := MigrationManifest{SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml", HasEnv: true, Disposition: DispositionRetain}

	var buf bytes.Buffer
	if err := packageDefinitionBundle(&buf, m, stackDir); err != nil {
		t.Fatalf("package: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	sha := hex.EncodeToString(sum[:])

	got, files, err := receiveBundle(bytes.NewReader(buf.Bytes()), sha, t.TempDir())
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if got.StackID != "demo" {
		t.Fatalf("stackId=%q", got.StackID)
	}
	if string(files["compose/docker-compose.yml"]) == "" {
		t.Fatal("compose file missing from received bundle")
	}
	if string(files["compose/.env"]) != "FOO=bar\n" {
		t.Fatalf(".env mismatch: %q", files["compose/.env"])
	}
}

func TestReceiveBundleRejectsHashMismatch(t *testing.T) {
	stackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := MigrationManifest{SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml", Disposition: DispositionRetain}
	var buf bytes.Buffer
	if err := packageDefinitionBundle(&buf, m, stackDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := receiveBundle(bytes.NewReader(buf.Bytes()), "deadbeef", t.TempDir()); err == nil {
		t.Fatal("expected hash-mismatch rejection")
	}
}

func TestBuildDefinitionManifest(t *testing.T) {
	m := buildDefinitionManifest("demo", "docker-compose.yml", true, "src-node", "amd64", "tgt-node", DispositionClone, false)

	if m.SchemaVersion != 1 {
		t.Fatalf("schemaVersion=%d, want 1", m.SchemaVersion)
	}
	if m.Overwrite {
		t.Fatal("overwrite=true, want false (not acked)")
	}
	if got := buildDefinitionManifest("demo", "docker-compose.yml", true, "src", "amd64", "tgt", DispositionRetain, true); !got.Overwrite {
		t.Fatal("overwrite=false, want true (acked propagates into manifest)")
	}
	if m.StackID != "demo" || m.ComposeProjectName != "demo" {
		t.Fatalf("stackId=%q composeProjectName=%q", m.StackID, m.ComposeProjectName)
	}
	if m.ComposeFile != "docker-compose.yml" {
		t.Fatalf("composeFile=%q", m.ComposeFile)
	}
	if !m.HasEnv {
		t.Fatal("hasEnv=false, want true")
	}
	if m.Disposition != DispositionClone {
		t.Fatalf("disposition=%q, want clone", m.Disposition)
	}
	if m.Source.NodeID != "src-node" || m.Source.Arch != "amd64" {
		t.Fatalf("source=%+v", m.Source)
	}
	if m.Target.NodeID != "tgt-node" {
		t.Fatalf("target=%+v", m.Target)
	}
	// Definition-only bundle: data sections stay empty until later milestones.
	if len(m.Binds) != 0 || len(m.Volumes) != 0 || len(m.Images) != 0 {
		t.Fatalf("expected empty binds/volumes/images, got binds=%d volumes=%d images=%d", len(m.Binds), len(m.Volumes), len(m.Images))
	}
}
