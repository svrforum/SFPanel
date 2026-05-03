package firewall

import (
	"reflect"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

const fail2banStatusFixture = `Status
|- Number of jail:	2
` + "`" + `- Jail list:	sshd, apache-auth
`

func TestParseFail2banJailList(t *testing.T) {
	got := parseFail2banJailList(fail2banStatusFixture)
	want := []string{"sshd", "apache-auth"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFail2banJailList_Empty(t *testing.T) {
	got := parseFail2banJailList(`Status
|- Number of jail:	0
` + "`" + `- Jail list:
`)
	if len(got) != 0 {
		t.Errorf("expected 0 jails, got %v", got)
	}
}

const fail2banJailStatusFixture = `Status for the jail: sshd
|- Filter
|  |- Currently failed:	3
|  |- Total failed:	15
|  ` + "`" + `- File list:	/var/log/auth.log
` + "`" + `- Actions
   |- Currently banned:	2
   |- Total banned:	57
   ` + "`" + `- Banned IP list:	192.168.1.100 10.0.0.5
`

func TestParseFail2banJailStatus(t *testing.T) {
	// Mock fail2ban-client get returns: maxretry, bantime, findtime, ignoreip
	mock := exec.NewMockCommander()
	mock.SetOutput("fail2ban-client", "", nil)
	// Default fallback returns empty — covers the "ignoreip" lookup which
	// usually returns empty on a fresh jail.
	h := &Handler{Cmd: mock}

	jail := Fail2banJail{Name: "sshd", BannedIPs: []string{}}
	got := h.parseFail2banJailStatus(fail2banJailStatusFixture, jail)

	if got.BannedCount != 2 {
		t.Errorf("BannedCount: got %d, want 2", got.BannedCount)
	}
	if got.TotalBanned != 57 {
		t.Errorf("TotalBanned: got %d, want 57", got.TotalBanned)
	}
	if got.Filter != "/var/log/auth.log" {
		t.Errorf("Filter: got %q, want /var/log/auth.log", got.Filter)
	}
	want := []string{"192.168.1.100", "10.0.0.5"}
	if !reflect.DeepEqual(got.BannedIPs, want) {
		t.Errorf("BannedIPs: got %v, want %v", got.BannedIPs, want)
	}
}

func TestParseFail2banJailStatus_NoBans(t *testing.T) {
	const fixture = `Status for the jail: sshd
|- Filter
|  ` + "`" + `- File list:	/var/log/auth.log
` + "`" + `- Actions
   |- Currently banned:	0
   |- Total banned:	0
   ` + "`" + `- Banned IP list:
`
	mock := exec.NewMockCommander()
	h := &Handler{Cmd: mock}
	got := h.parseFail2banJailStatus(fixture, Fail2banJail{Name: "sshd", BannedIPs: []string{}})
	if got.BannedCount != 0 || got.TotalBanned != 0 {
		t.Errorf("expected zero counts, got banned=%d total=%d", got.BannedCount, got.TotalBanned)
	}
	if len(got.BannedIPs) != 0 {
		t.Errorf("expected empty BannedIPs, got %v", got.BannedIPs)
	}
}
