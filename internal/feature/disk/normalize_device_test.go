package disk

import "testing"

// The bug this exists for: `pvs -o pv_name` prints "/dev/sdb1", the UI handed
// that back, and the handler validated it with a regex that rejects slashes
// before prefixing "/dev/" itself — so Remove PV could never succeed with the
// name the panel had just shown, and the Create dialog's placeholder was a
// string its own handler refused.
func TestNormalizeDevicePathAcceptsBothForms(t *testing.T) {
	for _, in := range []string{"/dev/sdb1", "sdb1", "  /dev/sdb1  "} {
		got, err := normalizeDevicePath(in)
		if err != nil {
			t.Fatalf("normalizeDevicePath(%q) failed: %v", in, err)
		}
		if got != "/dev/sdb1" {
			t.Errorf("normalizeDevicePath(%q) = %q, want /dev/sdb1", in, got)
		}
	}
}

// The guard has to still be a guard. Each of these must fail, and must fail
// because the name is invalid — not because a prefix was peeled until
// something happened to pass.
func TestNormalizeDevicePathStillRejectsTraversal(t *testing.T) {
	for _, in := range []string{
		"/dev/../etc/shadow",
		"../../etc/shadow",
		"/dev//dev/sdb1",
		"/dev/dev/sdb1",
		"sdb1; rm -rf /",
		"sdb1 /dev/sda",
		"/etc/shadow",
		"",
		"/dev/",
	} {
		got, err := normalizeDevicePath(in)
		if err == nil {
			t.Errorf("normalizeDevicePath(%q) = %q, want an error", in, got)
		}
	}
}
