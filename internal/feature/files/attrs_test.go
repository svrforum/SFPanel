package files

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
)

func TestParseFileMode(t *testing.T) {
	ok := map[string]os.FileMode{
		"644":  0o644,
		"0644": 0o644,
		"755":  0o755,
		"600":  0o600,
		"0000": 0,
		"777":  0o777,
	}
	for raw, want := range ok {
		got, err := parseFileMode(raw)
		if err != nil {
			t.Errorf("parseFileMode(%q) errored: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("parseFileMode(%q) = %04o, want %04o", raw, got, want)
		}
	}

	// setuid through four digits in a text field is not an operation a file
	// manager should offer by accident.
	for _, raw := range []string{"4755", "2755", "1777"} {
		if _, err := parseFileMode(raw); err == nil {
			t.Errorf("parseFileMode(%q) accepted a setuid/setgid/sticky bit", raw)
		}
	}

	for _, raw := range []string{"", "abc", "999", "rwxr-xr-x", "0o644", "-644", "00644"} {
		if _, err := parseFileMode(raw); err == nil {
			t.Errorf("parseFileMode(%q) accepted a malformed mode", raw)
		}
	}
}

func TestResolveOwner(t *testing.T) {
	// Numeric ids must work even with no passwd entry — a container writing as
	// uid 100999 is exactly the case an operator needs to repair, and it is the
	// case where a name lookup cannot help.
	uid, gid, err := resolveOwner("100999", "100999")
	if err != nil {
		t.Fatalf("numeric ids rejected: %v", err)
	}
	if uid != 100999 || gid != 100999 {
		t.Errorf("uid/gid = %d/%d, want 100999/100999", uid, gid)
	}

	// root exists everywhere this panel runs.
	if uid, _, err := resolveOwner("root", ""); err != nil || uid != 0 {
		t.Errorf("resolveOwner(root) = %d, %v; want 0, nil", uid, err)
	}

	// An empty field means "leave it alone", which chown spells -1.
	if uid, gid, err := resolveOwner("root", ""); err != nil || gid != -1 {
		t.Errorf("empty group = %d (uid %d, err %v), want -1", gid, uid, err)
	}

	if _, _, err := resolveOwner("", ""); err == nil {
		t.Error("resolveOwner with neither user nor group should be refused")
	}
	if _, _, err := resolveOwner("definitely-not-a-real-account-name", ""); err == nil {
		t.Error("an unknown user name should be refused rather than silently skipped")
	}
}

// chmod follows a symlink, so acting on a link row would change the target's
// mode with nothing on screen saying so — the operator names one file and a
// different one changes. Linux stores no permissions on a link anyway.
func TestChangeModeRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}

	status, code := postJSON(t, (&Handler{}).ChangeMode, "/files/chmod", map[string]any{
		"path": link, "mode": "0777",
	})
	if status != http.StatusBadRequest || code != response.ErrInvalidPath {
		t.Fatalf("chmod on a symlink returned %d/%s, want 400/%s", status, code, response.ErrInvalidPath)
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("target mode = %v — the link was followed", info.Mode().Perm())
	}
}

func TestChangeModeWorksOnARealFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, code := postJSON(t, (&Handler{}).ChangeMode, "/files/chmod", map[string]any{
		"path": target, "mode": "0755",
	}); status != http.StatusOK {
		t.Fatalf("chmod returned %d/%s, want 200", status, code)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}
