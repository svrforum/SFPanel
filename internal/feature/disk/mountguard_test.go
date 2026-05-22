package disk

import "testing"

func TestIsProtectedMountpoint(t *testing.T) {
	cases := []struct {
		path     string
		expected bool
	}{
		{"/", true},
		{"/boot", true},
		{"/boot/efi", true},
		{"/etc", true},
		{"/etc/cron.d", true},
		{"/var/lib/sfpanel", true},
		{"/var/lib/sfpanel/sfpanel.db", true},
		{"/mnt/data", false},
		{"/srv/storage", false},
		{"/home/operator", true}, // /home itself is protected, so /home/* is too
		{"", true},               // empty path → refuse
		{"/..", true},
		{"./relative", true}, // non-absolute → refuse
	}
	for _, tc := range cases {
		got := isProtectedMountpoint(tc.path)
		if got != tc.expected {
			t.Errorf("isProtectedMountpoint(%q) = %v, want %v", tc.path, got, tc.expected)
		}
	}
}
