package compose

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestBundleWriteThenRead(t *testing.T) {
	var buf bytes.Buffer
	w := NewBundleWriter(&buf)
	m := MigrationManifest{SchemaVersion: 1, StackID: "x", ComposeFile: "docker-compose.yml", Disposition: DispositionRetain}
	if err := w.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFile("compose/docker-compose.yml", []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r := NewBundleReader(bytes.NewReader(buf.Bytes()))
	got, files, err := r.ReadAll("/tmp/does-not-write-in-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.StackID != "x" {
		t.Fatalf("manifest stackId=%q", got.StackID)
	}
	if string(files["compose/docker-compose.yml"]) != "services: {}\n" {
		t.Fatalf("compose content mismatch: %q", files["compose/docker-compose.yml"])
	}
}

func TestBundleRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("evil")
	_ = tw.WriteHeader(&tar.Header{Name: "../../etc/evil", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()

	r := NewBundleReader(bytes.NewReader(buf.Bytes()))
	if _, _, err := r.ReadAll("/tmp/x"); err == nil {
		t.Fatal("expected traversal entry to be rejected")
	}
}
