package system

import (
	"strings"
	"testing"
)

// Removing the panel's sysctl file and running `sysctl --system` restores
// nothing: a key that no other file mentions simply keeps whatever is already
// in the kernel. So "Reset to Defaults" left the host exactly as tuned as it
// was, and said the opposite. The values it displaced are recorded in the file
// it writes, and read back here.
func TestPreviousValuesRoundTrip(t *testing.T) {
	previous := map[string]string{
		"net.core.somaxconn":       "4096",
		"vm.swappiness":            "60",
		"net.ipv4.tcp_fin_timeout": "60",
	}
	keys := []string{"net.core.somaxconn", "net.ipv4.tcp_fin_timeout", "vm.swappiness"}

	rendered := strings.Join(previousValueComments(previous, keys), "\n")
	got := parsePreviousValues(rendered)

	if len(got) != len(previous) {
		t.Fatalf("read back %d values, wrote %d: %v", len(got), len(previous), got)
	}
	for k, want := range previous {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// The record is a comment, so sysctl has to ignore it. A line that sysctl
// would parse as an assignment would set the old value at every boot — the
// exact opposite of the intent.
func TestPreviousValuesAreComments(t *testing.T) {
	for _, line := range previousValueComments(map[string]string{"vm.swappiness": "60"}, []string{"vm.swappiness"}) {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("line %q is not a comment; sysctl would apply it", line)
		}
	}
}

// A key with no recorded previous value is skipped rather than written blank —
// restoring "" would be worse than restoring nothing.
func TestPreviousValuesSkipEmpties(t *testing.T) {
	out := previousValueComments(map[string]string{"a.b": "", "c.d": "1"}, []string{"a.b", "c.d"})
	if len(out) != 1 || !strings.Contains(out[0], "c.d") {
		t.Errorf("got %v, want only c.d", out)
	}
}

// The file is world-readable and an operator may have edited it, so what comes
// back is checked rather than trusted — these all end up as `sysctl -w`
// arguments.
func TestParsePreviousValuesRejectsJunk(t *testing.T) {
	content := strings.Join([]string{
		previousMarker + "vm.swappiness = 60",
		previousMarker + "; rm -rf / = 1",
		previousMarker + "NotAKey = 1",
		previousMarker + "no.equals.sign",
		previousMarker + "empty.value = ",
		previousMarker + "../../etc/passwd = 1",
		"vm.swappiness = 1",
	}, "\n")

	got := parsePreviousValues(content)
	if len(got) != 1 {
		t.Fatalf("accepted %d entries, want only the valid one: %v", len(got), got)
	}
	if got["vm.swappiness"] != "60" {
		t.Errorf("got %v, want vm.swappiness=60", got)
	}
}

func TestParsePreviousValuesOnAFileWithoutTheRecord(t *testing.T) {
	// An older file, written before the values were recorded. Empty, not
	// wrong — the handler says so instead of claiming a reset.
	got := parsePreviousValues("# SFPanel System Tuning\nvm.swappiness = 10\n")
	if len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}
