package ai

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidProfileName(t *testing.T) {
	ok := []string{"work", "client-a", "p.2", "A1", "x", strings.Repeat("a", 32)}
	bad := []string{"", ".", "..", "a/b", "../etc", ".hidden", "-lead", "a b", "a\x00b", strings.Repeat("a", 33), "work/../../root"}
	for _, n := range ok {
		if !validProfileName(n) {
			t.Errorf("validProfileName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if validProfileName(n) {
			t.Errorf("validProfileName(%q) = true, want false — it would become a directory name", n)
		}
	}
}

// profileDir must validate the name, join it, and then check the JOINED path,
// which is the lesson the file manager records: the destination only exists
// once the halves are joined.
func TestProfileDir(t *testing.T) {
	acct := Account{Name: "alice", Home: "/home/alice"}
	got, err := profileDir(acct, ToolCodex, "work")
	if err != nil || got != "/home/alice/.sfpanel-ai/codex/work" {
		t.Fatalf("got (%q, %v), want /home/alice/.sfpanel-ai/codex/work", got, err)
	}
	if _, err := profileDir(acct, ToolCodex, "../../etc"); err == nil {
		t.Error("a traversing name must be refused, not joined")
	}
	if _, err := profileDir(acct, ToolShell, "work"); err == nil {
		t.Error("a tool without profile support must be refused")
	}
	if _, err := profileDir(Account{Name: "x", Home: "relative/home"}, ToolCodex, "work"); err == nil {
		t.Error("a non-absolute account home must be refused")
	}
	if root := profileRoot(acct, ToolClaude); root != filepath.Join("/home/alice", profileRootName, "claude") {
		t.Errorf("profileRoot = %q", root)
	}
}

func TestToolSupportsProfiles(t *testing.T) {
	for _, tl := range []string{ToolClaude, ToolCodex} {
		if !toolSupportsProfiles(tl) {
			t.Errorf("%s must support profiles", tl)
		}
	}
	// Gemini has no verified config-directory override, and a shell session
	// runs no tool — neither may offer a picker that cannot work.
	for _, tl := range []string{ToolGemini, ToolShell, "vim"} {
		if toolSupportsProfiles(tl) {
			t.Errorf("%s must not claim profile support", tl)
		}
	}
}
