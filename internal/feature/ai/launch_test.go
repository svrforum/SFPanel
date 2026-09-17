package ai

import (
	"slices"
	"strings"
	"testing"
)

// Claude takes flags; the order is fixed so an argv assertion elsewhere can be
// exact rather than order-insensitive.
func TestToolArgv_Claude(t *testing.T) {
	got, err := toolArgv(ToolClaude, LaunchOptions{Continue: "last", Permission: "acceptEdits", Model: "opus-5", Extra: []string{"--verbose"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--continue", "--permission-mode", "acceptEdits", "--model", "opus-5", "--verbose"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, _ := toolArgv(ToolClaude, LaunchOptions{Continue: "pick"}); !slices.Equal(got, []string{"--resume"}) {
		t.Errorf("pick → %q, want [--resume]", got)
	}
	if got, _ := toolArgv(ToolClaude, LaunchOptions{Dangerous: true}); !slices.Equal(got, []string{"--dangerously-skip-permissions"}) {
		t.Errorf("dangerous → %q", got)
	}
	if got, _ := toolArgv(ToolClaude, LaunchOptions{}); len(got) != 0 {
		t.Errorf("no options must add nothing, got %q", got)
	}
}

// Codex continues with a SUBCOMMAND, which has to come first — a flag-shaped
// translation would make `codex --continue`, which does not exist.
func TestToolArgv_CodexResumeIsASubcommandAndComesFirst(t *testing.T) {
	got, err := toolArgv(ToolCodex, LaunchOptions{Continue: "last", Permission: "never", Sandbox: "workspace-write", Model: "gpt-6", Extra: []string{"--search"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"resume", "--last", "--ask-for-approval", "never", "--sandbox", "workspace-write", "-m", "gpt-6", "--search"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, _ := toolArgv(ToolCodex, LaunchOptions{Continue: "pick"}); !slices.Equal(got, []string{"resume"}) {
		t.Errorf("pick → %q, want [resume]", got)
	}
	// Without a continue choice there must be no subcommand at all.
	got, _ = toolArgv(ToolCodex, LaunchOptions{Sandbox: "read-only"})
	if slices.Contains(got, "resume") {
		t.Errorf("no continue must emit no subcommand, got %q", got)
	}
	if got, _ := toolArgv(ToolCodex, LaunchOptions{Dangerous: true}); !slices.Equal(got, []string{"--dangerously-bypass-approvals-and-sandbox"}) {
		t.Errorf("dangerous → %q", got)
	}
}

func TestToolArgv_RefusesToolsWithoutOptions(t *testing.T) {
	for _, tl := range []string{ToolShell, ToolGemini, "vim"} {
		if _, err := toolArgv(tl, LaunchOptions{Continue: "last"}); err == nil {
			t.Errorf("%s must refuse options rather than drop them", tl)
		}
		if got, err := toolArgv(tl, LaunchOptions{}); err != nil || len(got) != 0 {
			t.Errorf("%s with no options must be fine and empty, got %q %v", tl, got, err)
		}
	}
}

func TestValidateLaunch(t *testing.T) {
	ok := []struct {
		tool string
		o    LaunchOptions
	}{
		{ToolClaude, LaunchOptions{Continue: "last", Permission: "bypassPermissions"}},
		{ToolClaude, LaunchOptions{Permission: "plan", Model: "claude-opus-5"}},
		{ToolCodex, LaunchOptions{Permission: "on-request", Sandbox: "danger-full-access"}},
		{ToolCodex, LaunchOptions{Extra: []string{"--search", "-c", "model=x"}}},
	}
	for _, c := range ok {
		if err := validateLaunch(c.tool, c.o); err != nil {
			t.Errorf("%s %+v: %v", c.tool, c.o, err)
		}
	}
	bad := []struct {
		name, tool, rule string
		o                LaunchOptions
	}{
		{"continue value", ToolClaude, "continue", LaunchOptions{Continue: "yes"}},
		{"claude mode on codex", ToolCodex, "approval", LaunchOptions{Permission: "acceptEdits"}},
		{"codex policy on claude", ToolClaude, "permission", LaunchOptions{Permission: "never"}},
		{"sandbox on claude", ToolClaude, "sandbox", LaunchOptions{Sandbox: "read-only"}},
		{"sandbox value", ToolCodex, "sandbox", LaunchOptions{Sandbox: "wide-open"}},
		{"model shape", ToolClaude, "model", LaunchOptions{Model: "opus 5"}},
		{"extra with a space", ToolCodex, "argument", LaunchOptions{Extra: []string{"--prompt hello"}}},
		{"extra with a semicolon", ToolCodex, "argument", LaunchOptions{Extra: []string{"--x;rm"}}},
		{"too many extras", ToolCodex, "argument", LaunchOptions{Extra: []string{"-1", "-2", "-3", "-4", "-5", "-6", "-7", "-8", "-9"}}},
		{"extra too long", ToolCodex, "argument", LaunchOptions{Extra: []string{"--" + strings.Repeat("a", 70)}}},
	}
	for _, c := range bad {
		err := validateLaunch(c.tool, c.o)
		if err == nil {
			t.Errorf("%s: accepted %+v, want a refusal", c.name, c.o)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), c.rule) {
			t.Errorf("%s: message %q does not name the rule (%q)", c.name, err, c.rule)
		}
	}
}

func TestLaunchEncodeDecode(t *testing.T) {
	if s, err := (LaunchOptions{}).Encode(); err != nil || s != "" {
		t.Errorf("zero must encode to the empty string, got %q %v", s, err)
	}
	o := LaunchOptions{Continue: "last", Permission: "plan", Extra: []string{"--verbose"}}
	s, err := o.Encode()
	if err != nil || !strings.Contains(s, `"continue":"last"`) {
		t.Fatalf("encode = %q, %v", s, err)
	}
	back, err := decodeLaunch(s)
	if err != nil || back.Continue != "last" || back.Permission != "plan" || len(back.Extra) != 1 {
		t.Errorf("round trip = %+v, %v", back, err)
	}
	if z, err := decodeLaunch(""); err != nil || !z.IsZero() {
		t.Errorf("empty must decode to the zero value, got %+v %v", z, err)
	}
	// A row written by a newer panel must never break a restart.
	fwd, err := decodeLaunch(`{"continue":"last","futureThing":{"a":1}}`)
	if err != nil || fwd.Continue != "last" {
		t.Errorf("unknown fields must be ignored, got %+v %v", fwd, err)
	}
}
