package featurecluster

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateClusterName_Accepts(t *testing.T) {
	for in, want := range map[string]string{
		"homelab":               "homelab",
		"  prod cluster  ":      "prod cluster",
		"서버실 A":                 "서버실 A",
		strings.Repeat("가", 64): strings.Repeat("가", 64),
	} {
		got, err := validateClusterName(in)
		if err != nil || got != want {
			t.Errorf("validateClusterName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestValidateClusterName_Rejects(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		strings.Repeat("a", 65),
		"two\nlines",
		"red\x1b[31m",
		"tab\there",
		"bad\xffutf8",
	} {
		if got, err := validateClusterName(in); !errors.Is(err, errClusterName) {
			t.Errorf("validateClusterName(%q) = %q, %v; want errClusterName", in, got, err)
		}
	}
}
