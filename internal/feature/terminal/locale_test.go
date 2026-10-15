package terminal

import (
	"errors"
	"testing"

	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
)

func TestPickLocale(t *testing.T) {
	cases := []struct {
		name       string
		installed  []string
		systemLang string
		want       string
	}{
		// A host with the Korean locale generated keeps it.
		{"ko generated", []string{"C", "C.utf8", "POSIX", "ko_KR.utf8"}, "C.UTF-8", "ko_KR.UTF-8"},
		// Proxmox VE as installed: en_US only, and LANG says so.
		{"proxmox", []string{"C", "C.utf8", "en_US.utf8", "POSIX"}, "en_US.UTF-8", "en_US.UTF-8"},
		// A LANG naming a locale nobody generated would warn like ko_KR did.
		{"lang not generated", []string{"C", "C.utf8", "POSIX"}, "de_DE.UTF-8", "C.UTF-8"},
		{"no lang", []string{"C", "C.utf8", "POSIX"}, "", "C.UTF-8"},
		// The terminal is UTF-8; LANG=C would garble anything else.
		{"lang C", []string{"C", "C.utf8", "POSIX"}, "C", "C.UTF-8"},
		{"latin-1 lang", []string{"C", "C.utf8", "de_DE.iso88591"}, "de_DE.ISO-8859-1", "C.UTF-8"},
		{"spelled utf-8", []string{"C.UTF-8", "en_GB.UTF-8"}, "en_GB.utf8", "en_GB.utf8"},
	}
	for _, c := range cases {
		if got := pickLocale(c.installed, c.systemLang); got != c.want {
			t.Errorf("%s: pickLocale(%v, %q) = %q, want %q", c.name, c.installed, c.systemLang, got, c.want)
		}
	}
}

func TestSessionLocaleAsksTheHost(t *testing.T) {
	prev := localeCmd
	t.Cleanup(func() { localeCmd = prev })
	t.Setenv("LANG", "en_US.UTF-8")

	m := commonExec.NewMockCommander()
	m.SetOutput("locale", "C\nC.utf8\nen_US.utf8\nPOSIX\n", nil)
	localeCmd = m
	if got := sessionLocale(); got != "en_US.UTF-8" {
		t.Errorf("sessionLocale = %q, want en_US.UTF-8", got)
	}
	if len(m.Calls) != 1 || m.Calls[0].Name != "locale" || len(m.Calls[0].Args) != 1 || m.Calls[0].Args[0] != "-a" {
		t.Errorf("calls = %+v, want locale -a", m.Calls)
	}

	// locale -a failing leaves the host's LANG.
	m = commonExec.NewMockCommander()
	m.SetOutput("locale", "", errors.New("not found"))
	localeCmd = m
	if got := sessionLocale(); got != "en_US.UTF-8" {
		t.Errorf("sessionLocale without locale -a = %q, want the host's LANG", got)
	}
}
