package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// downloadInstaller must place the script in a private, randomly named temp
// file — the fixed /tmp name it replaced was a TOCTOU hole (a local user
// could swap the file between the hash check and execution).
func TestDownloadInstaller_PrivateTempFile(t *testing.T) {
	if _, err := osExec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	t.Setenv("TMPDIR", t.TempDir())

	const body = "#!/bin/sh\necho ok\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	path, out, err := downloadInstaller(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("downloadInstaller failed: %v (curl output: %q)", err, out)
	}
	defer os.Remove(path)

	base := filepath.Base(path)
	if !strings.HasPrefix(base, "sfpanel-installer-") || !strings.HasSuffix(base, ".sh") {
		t.Errorf("unexpected temp name %q: want sfpanel-installer-*.sh", base)
	}
	if base == "sfpanel-installer-.sh" {
		t.Errorf("temp name %q has no random component", base)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("temp file mode = %o, want 600", perm)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != body {
		t.Errorf("downloaded content = %q, want %q", got, body)
	}
}

// A failed download must return an empty path and not leave a temp file behind.
func TestDownloadInstaller_CleansUpOnFailure(t *testing.T) {
	if _, err := osExec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // curl -f turns HTTP 404 into a non-zero exit
	}))
	defer srv.Close()

	path, _, err := downloadInstaller(context.Background(), srv.URL)
	if err == nil {
		os.Remove(path)
		t.Fatal("expected error for HTTP 404 download")
	}
	if path != "" {
		t.Errorf("path = %q, want empty on failure", path)
	}

	entries, dirErr := os.ReadDir(tmp)
	if dirErr != nil {
		t.Fatalf("read temp dir: %v", dirErr)
	}
	if len(entries) != 0 {
		t.Errorf("temp file leaked on failure: %v", entries)
	}
}
