package ai

import (
	"strings"
	"testing"
)

func names(as []Account) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

func TestParsePasswd_SkipsMalformedLines(t *testing.T) {
	got := parsePasswd(strings.NewReader(passwdFixture))
	if len(got) != 7 {
		t.Fatalf("parsed %d accounts, want 7 (the broken line must be skipped)", len(got))
	}
	if got[2].Name != "alice" || got[2].UID != 1000 || got[2].Home != "/home/alice" || got[2].Shell != "/bin/bash" {
		t.Errorf("alice parsed as %+v", got[2])
	}
}

// Only the panel account plus regular login accounts may run a session:
// uid >= 1000 with a real shell. nologin/false shells and system accounts
// are out even though they are in passwd.
func TestAllowedAccounts_RootPanel(t *testing.T) {
	got := allowedAccounts(Account{Name: "root", UID: 0}, true, parsePasswd(strings.NewReader(passwdFixture)))
	want := "root alice dave"
	if strings.Join(names(got), " ") != want {
		t.Errorf("allowed = %v, want %q", names(got), want)
	}
}

// A service account can have a real login shell — Debian's postgres and git
// both do — so the shell check alone does not keep system accounts out. The
// uid floor is what excludes them.
func TestAllowedAccounts_SystemAccountWithLoginShell(t *testing.T) {
	passwd := parsePasswd(strings.NewReader("postgres:x:110:117:PostgreSQL:/var/lib/postgresql:/bin/bash\n"))
	got := allowedAccounts(Account{Name: "root", UID: 0}, true, passwd)
	if strings.Join(names(got), " ") != "root" {
		t.Errorf("allowed = %v, want [root]: uid 110 is below the login floor", names(got))
	}
}

// A panel that is not root cannot switch accounts, so the list is itself.
func TestAllowedAccounts_NonRootPanelIsOnlyItself(t *testing.T) {
	got := allowedAccounts(Account{Name: "alice", UID: 1000}, false, parsePasswd(strings.NewReader(passwdFixture)))
	if strings.Join(names(got), " ") != "alice" {
		t.Errorf("allowed = %v, want [alice]", names(got))
	}
}

func TestResolveAccount(t *testing.T) {
	h := newTestHandler(t, nil)
	if a, ok := h.resolveAccount(""); !ok || a.Name != "root" {
		t.Errorf("empty name must resolve to the panel account, got %+v %v", a, ok)
	}
	if a, ok := h.resolveAccount("dave"); !ok || a.Home != "/home/dave" {
		t.Errorf("dave: got %+v %v", a, ok)
	}
	for _, bad := range []string{"bob", "carol", "nobody", "daemon", "mallory", "../root"} {
		if _, ok := h.resolveAccount(bad); ok {
			t.Errorf("%q must not resolve", bad)
		}
	}
}
