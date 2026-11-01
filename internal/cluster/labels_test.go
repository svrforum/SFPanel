package cluster

import (
	"strings"
	"testing"
)

// The label rules are the cluster's: the label route checks them, and an
// agent install checks a server's group with them before it touches the
// server, so a group the route would refuse fails before any SSH work, not
// after it.
func TestValidateLabels(t *testing.T) {
	for _, l := range []map[string]string{
		nil,
		{},
		{"env": "prod"},
		{"region": "kr-1", "tier": "core"},
		{"foo.bar": "baz", "x_y-z": "1.2.3"},
		{"group": strings.Repeat("a", 63)},
	} {
		if err := ValidateLabels(l); err != nil {
			t.Errorf("ValidateLabels(%v) rejected: %v", l, err)
		}
	}

	long := strings.Repeat("a", 64)
	for _, c := range []struct {
		labels map[string]string
		reason string
	}{
		{map[string]string{"group": "웹서버"}, `label value for "group" has invalid characters`},
		{map[string]string{"group": "web servers"}, `label value for "group" has invalid characters`},
		{map[string]string{"group": long}, `label value for "group" exceeds 63 chars`},
		{map[string]string{long: "v"}, "must be 1..63 chars"},
		{map[string]string{"": "v"}, "must be 1..63 chars"},
		{map[string]string{"-bad": "v"}, "has invalid characters"},
		{map[string]string{"bad key": "v"}, "has invalid characters"},
	} {
		if err := ValidateLabels(c.labels); err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("ValidateLabels(%v) = %v, want a refusal for %q", c.labels, err, c.reason)
		}
	}

	tooMany := make(map[string]string, 33)
	for i := 0; i < 33; i++ {
		tooMany[strings.Repeat("a", 4)+string(rune('0'+i))] = "v"
	}
	if err := ValidateLabels(tooMany); err == nil || !strings.Contains(err.Error(), "too many labels") {
		t.Errorf("33 labels: %v", err)
	}
}
