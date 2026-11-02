package scripts

import (
	"os/exec"
	"strings"
	"testing"
)

// unitHeredoc is the body of the heredoc setup_systemd writes the unit
// from: the lines between `cat > "$unit" <<EOF` and the EOF that ends it.
func unitHeredoc(t *testing.T) string {
	t.Helper()
	const open = "cat > \"$unit\" <<EOF\n"
	s := string(InstallScript)
	i := strings.Index(s, open)
	if i < 0 {
		t.Fatalf("install.sh has no %q", strings.TrimSpace(open))
	}
	body := s[i+len(open):]
	end := strings.Index(body, "\nEOF\n")
	if end < 0 {
		t.Fatal("the unit's heredoc has no EOF line")
	}
	return body[:end+1]
}

// The unit is a heredoc that expands its two paths and nothing else.
// install.sh runs under set -u, and an agent install runs it under
// systemd-run, whose environment has no HOME: a $HOME left unescaped in one
// of the unit's comments stopped every such install right after its join,
// with the agent registered and its service never written. Only the unit's
// text is expanded here, through cat, with the environment systemd-run
// gives; nothing is installed.
func TestTheUnitExpandsOnlyItsPaths(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	script := "set -euo pipefail\nINSTALL_DIR=/usr/local/bin\nCONFIG_DIR=/etc/sfpanel\ncat <<EOF\n" + unitHeredoc(t) + "EOF\n"
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the unit does not expand without HOME: %v\n%s", err, out)
	}
	unit := string(out)
	if !strings.Contains(unit, "\nExecStart=/usr/local/bin/sfpanel /etc/sfpanel/config.yaml\n") {
		t.Fatalf("the unit's ExecStart is not the installed binary and config:\n%s", unit)
	}
	if !strings.Contains(unit, "$HOME of other users") {
		t.Fatalf("the ProtectHome comment lost its literal $HOME:\n%s", unit)
	}
}
