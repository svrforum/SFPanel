package security

import (
	"encoding/json"
	"testing"
)

func TestPolicy_RoundTrip(t *testing.T) {
	in := Policy{
		Mode: ModeWarn,
		Rules: []Rule{
			{Pattern: "ghcr.io/myorg/*", Identity: Identity{
				SubjectPrefix: "https://github.com/myorg/myrepo/.github/workflows/release.yaml@refs/tags/v",
				Issuer:        "https://token.actions.githubusercontent.com",
			}},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Policy
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != ModeWarn {
		t.Fatalf("mode: got %q want %q", out.Mode, ModeWarn)
	}
	if len(out.Rules) != 1 || out.Rules[0].Pattern != "ghcr.io/myorg/*" {
		t.Fatalf("rules: %+v", out.Rules)
	}
}

func TestPolicy_DefaultIsOff(t *testing.T) {
	var p Policy
	if !p.IsOff() {
		t.Fatalf("zero-value policy should be off, got mode %q", p.Mode)
	}
}

func TestPolicy_Validate(t *testing.T) {
	cases := []struct {
		name    string
		p       Policy
		wantErr bool
	}{
		{"valid off", Policy{Mode: ModeOff}, false},
		{"valid warn no rules", Policy{Mode: ModeWarn}, false},
		{"invalid mode", Policy{Mode: "kapow"}, true},
		{"empty pattern", Policy{Mode: ModeWarn, Rules: []Rule{{Identity: Identity{SubjectPrefix: "x", Issuer: "y"}}}}, true},
		{"empty subject", Policy{Mode: ModeWarn, Rules: []Rule{{Pattern: "p", Identity: Identity{Issuer: "y"}}}}, true},
		{"empty issuer", Policy{Mode: ModeWarn, Rules: []Rule{{Pattern: "p", Identity: Identity{SubjectPrefix: "x"}}}}, true},
		{"valid full", Policy{Mode: ModeRequire, Rules: []Rule{{Pattern: "ghcr.io/foo/*", Identity: Identity{SubjectPrefix: "x", Issuer: "y"}}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
