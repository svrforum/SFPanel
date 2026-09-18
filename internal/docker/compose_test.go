package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// composeWarning is what `docker compose config` prints on stderr, verbatim
// (Compose v5.1.3), when a compose file interpolates a variable the .env does
// not set. It exits 0 and writes the resolved document to stdout regardless —
// an unset variable is ordinary, and it is exactly the interpolation case the
// deploy guard exists for.
const composeWarning = `time="2026-09-19T01:17:17+09:00" level=warning msg="The \"TZ\" variable is not set. Defaulting to a blank string."`

// managerWithFakeCompose returns a manager whose `docker` is a stub that
// answers any invocation with composeWarning on stderr and doc on stdout, so
// the real resolver can be exercised without a docker daemon.
func managerWithFakeCompose(t *testing.T, doc string) *ComposeManager {
	t.Helper()

	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "stack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "stack", "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	docPath := filepath.Join(bin, "resolved.yml")
	warnPath := filepath.Join(bin, "warning.txt")
	if err := os.WriteFile(docPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warnPath, []byte(composeWarning+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\ncat " + warnPath + " >&2\ncat " + docPath + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return NewComposeManager(base, nil)
}

func TestResolvedConfigYAMLCarriesNoComposeWarnings(t *testing.T) {
	doc := "name: stack\nservices:\n  web:\n    image: nginx\n"
	m := managerWithFakeCompose(t, doc)

	out, err := m.GetResolvedConfigYAML(context.Background(), "stack")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Contains(out, "level=warning") {
		t.Errorf("resolved YAML carries compose's stderr preamble: %q", out)
	}
	if out != doc {
		t.Errorf("resolved YAML = %q, want the document compose wrote to stdout %q", out, doc)
	}
}

func TestGuardAcceptsABenignStackThatWarnedAboutAnUnsetVariable(t *testing.T) {
	// The finding this test pins: nothing forbidden anywhere, but `TZ` is not
	// in the .env. Read together with compose's stderr the document does not
	// parse, and the guard used to answer that parse error as a refusal — so
	// an ordinary stack could not be deployed from the panel at all.
	m := managerWithFakeCompose(t, "name: stack\nservices:\n  web:\n    image: nginx\n    environment:\n      TZ: \"\"\n    volumes:\n      - /opt/stacks/stack/data:/usr/share/nginx/html\n")

	if err := m.guardResolvedCompose(context.Background(), "stack", []string{"up", "-d"}); err != nil {
		t.Errorf("a benign stack with an unset variable was refused: %v", err)
	}
}

func TestGuardRefusesAForbiddenBindBehindAComposeWarning(t *testing.T) {
	// The other half: reading stdout only must not cost the check. A stack
	// that warns AND binds /etc/sfpanel is still refused, by path.
	m := managerWithFakeCompose(t, "name: stack\nservices:\n  web:\n    image: nginx\n    volumes:\n      - /etc/sfpanel:/mnt\n")

	err := m.guardResolvedCompose(context.Background(), "stack", []string{"up", "-d"})
	if err == nil {
		t.Fatal("up accepted a resolved compose binding /etc/sfpanel")
	}
	if !strings.Contains(err.Error(), "/etc/sfpanel") {
		t.Errorf("message %q does not name the path", err)
	}
}

func TestGuardIsBestEffortWhenTheResolvedConfigCannotBeParsed(t *testing.T) {
	m := &ComposeManager{}
	m.resolveConfig = func(context.Context, string) (string, error) {
		// Whatever leaves the document unreadable — here the pollution this
		// round removed, so the guard stays safe even if some other writer
		// re-introduces it.
		return composeWarning + "\nservices:\n  a:\n    image: x\n", nil
	}
	// Unreadable says nothing about the forbidden tier, and `up` re-resolves
	// the same file and reports the real problem. Turning a parse failure into
	// a refusal would block deploys for a reason that is not the tier.
	if err := m.guardResolvedCompose(context.Background(), "stack", []string{"up", "-d"}); err != nil {
		t.Errorf("an unparseable resolved config became a refusal: %v", err)
	}
}
