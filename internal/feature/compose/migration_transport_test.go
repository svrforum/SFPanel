package compose

import (
	"bytes"
	"context"
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

	got, files, _, err := receiveBundle(bytes.NewReader(buf.Bytes()), sha, t.TempDir())
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

func TestPackageFullBundleBindRoundTrip(t *testing.T) {
	stackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), []byte("services:\n  web:\n    image: nginx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bindDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bindDir, "data.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := MigrationManifest{
		SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml",
		Disposition: DispositionRetain,
		Binds:       []MountSpec{{Host: bindDir, Kind: "abs", Copy: true}},
	}
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar")
	sha, err := packageFullBundle(context.Background(), bundlePath, &m, stackDir, t.TempDir())
	if err != nil {
		t.Fatalf("packageFullBundle: %v", err)
	}
	if len(sha) != 64 {
		t.Fatalf("bundle sha = %q, want 64-hex", sha)
	}
	if m.Binds[0].Archive == "" || m.Binds[0].Sha256 == "" || m.Binds[0].Bytes <= 0 {
		t.Fatalf("bind not archived into manifest: %+v", m.Binds[0])
	}

	bf, err := os.Open(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer bf.Close()
	got, _, staged, err := receiveBundle(bf, sha, t.TempDir())
	if err != nil {
		t.Fatalf("receiveBundle: %v", err)
	}
	if len(got.Binds) != 1 || got.Binds[0].Archive != m.Binds[0].Archive {
		t.Fatalf("manifest binds mismatch: %+v", got.Binds)
	}
	// Data entries stream to disk (staged), verified against the manifest sha.
	stagedPath, ok := staged[m.Binds[0].Archive]
	if !ok {
		t.Fatalf("bind archive %q not staged; have %v", m.Binds[0].Archive, stagedKeys(staged))
	}
	if fi, serr := os.Stat(stagedPath); serr != nil || fi.Size() <= 0 {
		t.Fatalf("staged bind archive missing/empty: %v", serr)
	}
}

func stagedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
	if _, _, _, err := receiveBundle(bytes.NewReader(buf.Bytes()), "deadbeef", t.TempDir()); err == nil {
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
