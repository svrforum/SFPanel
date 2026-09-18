package docker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUpRefusesAResolvedForbiddenBind(t *testing.T) {
	m := &ComposeManager{}
	m.resolveConfig = func(context.Context, string) (string, error) {
		// What `docker compose config` answers once the .env expanded
		// ${SECRET_DIR} — the text the panel stored says ${SECRET_DIR}.
		return "services:\n  a:\n    image: x\n    volumes:\n      - /etc/sfpanel:/mnt\n", nil
	}
	err := m.guardResolvedCompose(context.Background(), "stack", []string{"up", "-d"})
	if err == nil {
		t.Fatal("up accepted a resolved compose binding /etc/sfpanel")
	}
	if !strings.Contains(err.Error(), "/etc/sfpanel") {
		t.Errorf("message %q does not name the path", err)
	}
}

func TestUpRefusalReachesTheCallerAsOutput(t *testing.T) {
	m := &ComposeManager{baseDir: t.TempDir()}
	m.resolveConfig = func(context.Context, string) (string, error) {
		return "services:\n  a:\n    image: x\n    volumes:\n      - /etc/sfpanel:/mnt\n", nil
	}
	// ProjectUp and UpdateStack put runCompose's output in front of the
	// operator, not its error, so a refusal that returned "" would arrive as a
	// failure with no reason. The guard runs before docker, so this needs none.
	out, err := m.runCompose(context.Background(), "stack", "up", "-d")
	if err == nil {
		t.Fatal("runCompose accepted a resolved compose binding /etc/sfpanel")
	}
	if !strings.Contains(out, "/etc/sfpanel") {
		t.Errorf("output %q does not name the path the operator has to fix", out)
	}
}

func TestGuardIgnoresEverythingButUp(t *testing.T) {
	m := &ComposeManager{}
	calls := 0
	m.resolveConfig = func(context.Context, string) (string, error) {
		calls++
		return "services:\n  a:\n    image: x\n    volumes:\n      - /etc/sfpanel:/mnt\n", nil
	}
	// `config` is how the resolver itself runs; guarding it would recurse.
	for _, verb := range []string{"config", "down", "ps", "logs"} {
		if err := m.guardResolvedCompose(context.Background(), "stack", []string{verb}); err != nil {
			t.Errorf("%s refused: %v", verb, err)
		}
	}
	if calls != 0 {
		t.Errorf("resolver ran %d times for non-up verbs, want 0", calls)
	}
}

func TestGuardIsBestEffortWhenTheConfigCannotResolve(t *testing.T) {
	m := &ComposeManager{}
	m.resolveConfig = func(context.Context, string) (string, error) { return "", errors.New("missing env file") }
	// `up` would fail with the same error and say it better; turning a
	// resolve failure into a refusal would block deploys for a reason that
	// has nothing to do with the tier.
	if err := m.guardResolvedCompose(context.Background(), "stack", []string{"up", "-d"}); err != nil {
		t.Errorf("resolve failure became a refusal: %v", err)
	}
}

func TestGuardAllowsRiskyResolvedContent(t *testing.T) {
	m := &ComposeManager{}
	m.resolveConfig = func(context.Context, string) (string, error) {
		return "services:\n  a:\n    image: x\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n", nil
	}
	// The operator acknowledged this when they saved it; the deploy path
	// enforces the forbidden tier only.
	if err := m.guardResolvedCompose(context.Background(), "stack", []string{"up", "-d"}); err != nil {
		t.Errorf("acknowledged risky stack refused at deploy: %v", err)
	}
}
