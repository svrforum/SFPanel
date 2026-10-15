package terminal

import (
	"os"
	"strings"
	"time"

	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
)

// localeCmd lists the installed locales; tests replace it.
var localeCmd commonExec.Commander = commonExec.NewCommander()

// sessionLocale is LANG for a new shell, picked when the session starts:
// ko_KR.UTF-8 where it is installed, else the host's own LANG (systemd hands
// the panel /etc/default/locale), else C.UTF-8, which every target carries.
//
// It used to be ko_KR.UTF-8 and LC_ALL=ko_KR.UTF-8 everywhere. Where that
// locale is not generated — Debian installs only the install language,
// Proxmox VE only en_US.UTF-8 — bash warned at every start, Korean file names
// showed as '?', and every Perl program, which is all of Proxmox's qm, pct
// and pvesh, printed its five-line locale warning on each run. LC_ALL is no
// longer set at all: it overrides every LC_* the operator sets in the shell.
func sessionLocale() string {
	out, err := localeCmd.RunWithTimeout(5*time.Second, "locale", "-a")
	if err != nil {
		// Nothing says what is installed: the host's own choice is the
		// safest, and C.UTF-8 when it has none.
		if lang := os.Getenv("LANG"); lang != "" {
			return lang
		}
		return "C.UTF-8"
	}
	return pickLocale(strings.Fields(out), os.Getenv("LANG"))
}

func pickLocale(installed []string, systemLang string) string {
	have := make(map[string]bool, len(installed))
	for _, l := range installed {
		have[normLocale(l)] = true
	}
	if have[normLocale("ko_KR.UTF-8")] {
		return "ko_KR.UTF-8"
	}
	// The terminal speaks UTF-8; a LANG=C or a Latin-1 locale would garble
	// whatever is not ASCII.
	if n := normLocale(systemLang); have[n] && strings.Contains(n, ".utf8") {
		return systemLang
	}
	return "C.UTF-8"
}

// normLocale folds the spellings of one locale together: locale -a says
// "ko_KR.utf8" for what LANG calls "ko_KR.UTF-8".
func normLocale(l string) string {
	name, codeset, ok := strings.Cut(l, ".")
	if !ok {
		return l
	}
	codeset, modifier, hasModifier := strings.Cut(codeset, "@")
	out := name + "." + strings.ToLower(strings.ReplaceAll(codeset, "-", ""))
	if hasModifier {
		out += "@" + modifier
	}
	return out
}
