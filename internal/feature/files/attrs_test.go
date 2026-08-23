package files

import (
	"os"
	"testing"
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
