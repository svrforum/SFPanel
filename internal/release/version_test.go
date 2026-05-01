package release

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.99", 1},
		{"2.0.0", "1.99.99", 1},
		{"0.11.1", "0.11.10", -1},
		{"0.11.10", "0.11.2", 1},
		// Tolerate optional "v" prefix on either side.
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "v1.2.3", 0},
	}
	for _, tt := range tests {
		got, err := CompareVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q) unexpected error: %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareVersionsInvalid(t *testing.T) {
	cases := []struct{ a, b string }{
		{"", "1.0.0"},
		{"1.0.0", ""},
		{"1.0", "1.0.0"},      // wrong segment count
		{"1.0.0.0", "1.0.0"},  // wrong segment count
		{"1.0.x", "1.0.0"},    // non-numeric
		{"1.0.0", "abc.d.ef"}, // non-numeric
	}
	for _, c := range cases {
		if _, err := CompareVersions(c.a, c.b); err == nil {
			t.Errorf("CompareVersions(%q, %q) expected error, got nil", c.a, c.b)
		}
	}
}

func TestIsForwardUpdate(t *testing.T) {
	if ok, _ := IsForwardUpdate("0.11.1", "0.11.2"); !ok {
		t.Error("0.11.1 -> 0.11.2 should be forward")
	}
	if ok, _ := IsForwardUpdate("0.11.2", "0.11.1"); ok {
		t.Error("0.11.2 -> 0.11.1 should NOT be forward (downgrade)")
	}
	if ok, _ := IsForwardUpdate("0.11.1", "0.11.1"); ok {
		t.Error("0.11.1 -> 0.11.1 should NOT be forward (equal)")
	}
	if _, err := IsForwardUpdate("1.0", "1.0.0"); err == nil {
		t.Error("invalid current version should error")
	}
}
