package firewall

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

const installFail2ban = "apt-get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -- fail2ban"

func installCmd(t *testing.T) *ufwArgsCommander {
	t.Helper()
	prev := fail2banSettle
	fail2banSettle = 0
	t.Cleanup(func() { fail2banSettle = prev })
	m := newUFWArgsCommander()
	m.set("apt-get update", "", nil)
	m.set(installFail2ban, "Setting up fail2ban (1.0.2-2) ...\n", nil)
	m.set("systemctl is-active fail2ban", "active\n", nil)
	return m
}

// systemctl start returns once fail2ban-server has forked; on Debian 12 with
// no auth.log the sshd jail stops it a moment later. The install reported
// success for a fail2ban already dead, and the page then hid every action.
func TestInstallReportsAFail2banThatStoppedAfterStarting(t *testing.T) {
	m := installCmd(t)
	m.set("systemctl is-active fail2ban", "failed\n", errors.New("exit status 3"))
	m.set("journalctl -u fail2ban -n 8 --no-pager -o cat",
		"Started fail2ban.service - Fail2Ban Service.\nERROR   Failed during configuration: Have not found any log file for sshd jail\nfail2ban.service: Main process exited, code=exited, status=255/EXCEPTION\n", nil)

	rec := call(t, (&Handler{Cmd: m}).InstallFail2ban, http.MethodPost, "/fail2ban/install", "")
	e := decode(t, rec)
	if rec.Code != http.StatusInternalServerError || e.Error.Code != "FAIL2BAN_START_ERROR" ||
		!strings.Contains(e.Error.Message, "Have not found any log file for sshd jail") {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

// A Proxmox host without a subscription has its enterprise repository answer
// 401 to every apt-get update, and the install stopped there. fail2ban comes
// from Debian's repositories: the install goes on and says which one failed.
func TestInstallGoesPastARepositoryThatRefusesAccess(t *testing.T) {
	m := installCmd(t)
	m.set("apt-get update", "Hit:1 http://deb.debian.org/debian trixie InRelease\n"+
		"Err:2 https://enterprise.proxmox.com/debian/pve trixie InRelease\n  401  Unauthorized [IP: 203.0.113.10 443]\n"+
		"E: Failed to fetch https://enterprise.proxmox.com/debian/pve/dists/trixie/InRelease  401  Unauthorized [IP: 203.0.113.10 443]\n"+
		"E: The repository 'https://enterprise.proxmox.com/debian/pve trixie InRelease' is not signed.\n",
		errors.New("exit status 100"))

	rec := call(t, (&Handler{Cmd: m}).InstallFail2ban, http.MethodPost, "/fail2ban/install", "")
	e := decode(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	var data struct {
		Warnings []struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Warnings) != 1 || data.Warnings[0].Code != "APT_REPO_UNAUTHORIZED" ||
		data.Warnings[0].Details["uri"] != "https://enterprise.proxmox.com/debian/pve" {
		t.Errorf("warnings %+v", data.Warnings)
	}
	if !strings.Contains(strings.Join(m.calls, "\n"), installFail2ban) {
		t.Errorf("fail2ban was not installed: %q", m.calls)
	}
}

func createJail(t *testing.T, info platform.Info, body string) string {
	t.Helper()
	platform.SetForTest(t, info)
	dir := t.TempDir()
	prevDir, prevExists := fail2banJailDir, logPathExists
	fail2banJailDir = dir
	logPathExists = func(string) bool { return false }
	t.Cleanup(func() { fail2banJailDir, logPathExists = prevDir, prevExists })

	cmd := exec.NewMockCommander()
	cmd.SetOutput("exists:fail2ban-client", "", nil)
	cmd.SetOutput("fail2ban-client", "Status\n|- Number of jail:\t0\n`- Jail list:\t\n", nil)
	rec := call(t, (&Handler{Cmd: cmd}).CreateJail, http.MethodPost, "/fail2ban/jails", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "sshd.local"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A host with only the journal has no auth.log, and a jail naming it stops
// fail2ban 1.0 from starting, every other jail with it. The jail reads the
// journal there, the way Debian 13 sets up its own sshd jail.
func TestOnAJournalOnlyHostTheSSHJailReadsTheJournal(t *testing.T) {
	// The page sends the template's log path back as it showed it.
	body := `{"id":"sshd","max_retry":5,"ban_time":600,"find_time":600,"log_path":"/var/log/auth.log"}`

	got := createJail(t, pveHost, body)
	if !strings.Contains(got, "\nbackend = systemd\njournalmatch = _SYSTEMD_UNIT=ssh.service + _COMM=sshd\n") || strings.Contains(got, "logpath") {
		t.Errorf("journal-only host wrote\n%s", got)
	}

	got = createJail(t, platform.Info{OSID: "ubuntu"}, body)
	if !strings.Contains(got, "\nlogpath = /var/log/auth.log\n") || strings.Contains(got, "backend") {
		t.Errorf("host with syslog wrote\n%s", got)
	}
}
