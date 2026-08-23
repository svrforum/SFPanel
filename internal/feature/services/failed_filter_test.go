package services

import "testing"

// The guard here is the state match. A substring search over the record would
// pass a "no failed" assertion just as happily while returning units whose
// description mentions failure — so the fixtures below include exactly that.
func TestFailedOnlyMatchesStateNotDescription(t *testing.T) {
	in := []ServiceInfo{
		{Name: "nginx.service", ActiveState: "active", SubState: "running", Description: "web"},
		{Name: "borg.service", ActiveState: "failed", SubState: "failed", Description: "backup"},
		{Name: "rescue.service", ActiveState: "inactive", SubState: "dead", Description: "Recovery for a failed boot"},
		{Name: "watch.service", ActiveState: "activating", SubState: "failed", Description: "restarting"},
	}

	got := failedOnly(in)

	if len(got) != 2 {
		t.Fatalf("got %d units, want 2: %+v", len(got), got)
	}
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
	}
	if !names["borg.service"] {
		t.Error("a unit failed on both fields was dropped")
	}
	if !names["watch.service"] {
		t.Error("a unit failed on sub_state alone was dropped")
	}
	if names["rescue.service"] {
		t.Error("matched a healthy unit on the word 'failed' in its description")
	}
}

func TestFailedOnlyReturnsEmptyNotNil(t *testing.T) {
	got := failedOnly([]ServiceInfo{{Name: "ok.service", ActiveState: "active"}})
	if got == nil {
		t.Fatal("returned nil; JSON-encodes as null and the caller slices it")
	}
	if len(got) != 0 {
		t.Fatalf("got %d units, want 0", len(got))
	}
}
