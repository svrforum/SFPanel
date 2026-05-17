package terminal

import (
	"os"
	"testing"
)

func TestTerminalHome_PrefersHOMEEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if got := terminalHome(); got != dir {
		t.Errorf("terminalHome() = %q, want HOME=%q", got, dir)
	}
}

func TestTerminalHome_FallsBackWhenHOMEMissingOrNonexistent(t *testing.T) {
	t.Setenv("HOME", "/this/path/should/not/exist/anywhere")
	got := terminalHome()
	if got == "/this/path/should/not/exist/anywhere" {
		t.Error("terminalHome() should not return a non-existent HOME — chdir would fail")
	}
	// Either UserHomeDir worked (preferred) or we landed on /tmp. Both are
	// guaranteed to be stat-able on Linux.
	if _, err := os.Stat(got); err != nil {
		t.Errorf("fallback %q is not stat-able: %v", got, err)
	}
}

func TestTerminalHome_EmptyHOMEUsesUserHomeOrTmp(t *testing.T) {
	t.Setenv("HOME", "")
	got := terminalHome()
	if got == "" {
		t.Fatal("terminalHome() returned empty")
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("returned path %q is not stat-able: %v", got, err)
	}
}
