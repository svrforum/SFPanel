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
