package featurecluster

import (
	"strings"
	"testing"
)

func TestValidateLabels_Accepts(t *testing.T) {
	cases := []map[string]string{
		nil,
		{},
		{"env": "prod"},
		{"region": "kr-1", "tier": "core"},
		{"foo.bar": "baz", "x_y-z": "1.2.3"},
	}
	for _, l := range cases {
		if err := validateLabels(l); err != nil {
			t.Errorf("validateLabels(%v) rejected: %v", l, err)
		}
	}
}

func TestValidateLabels_RejectsInvalid(t *testing.T) {
	long := strings.Repeat("a", 64)
	cases := []struct {
		labels map[string]string
		why    string
	}{
		{map[string]string{long: "v"}, "key too long"},
		{map[string]string{"k": long}, "value too long"},
		{map[string]string{"-bad": "v"}, "key starts with non-alnum"},
		{map[string]string{"bad key": "v"}, "key contains space"},
		{map[string]string{"k": "bad value"}, "value contains space"},
		{map[string]string{"": "v"}, "empty key"},
	}
	for _, c := range cases {
		if err := validateLabels(c.labels); err == nil {
			t.Errorf("validateLabels(%v) should have been rejected (%s)", c.labels, c.why)
		}
	}
}

func TestValidateLabels_RejectsTooMany(t *testing.T) {
	tooMany := make(map[string]string, 33)
	for i := 0; i < 33; i++ {
		tooMany[strings.Repeat("a", 4)+string(rune('0'+i))] = "v"
	}
	if err := validateLabels(tooMany); err == nil {
		t.Error("33 labels should be rejected (max 32)")
	}
}
