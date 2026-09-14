package ai

import (
	"bufio"
	"io"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
)

// Account is an OS account a session may run as. Only Name is serialised;
// the rest drives runuser, HOME lookups and socket ownership.
type Account struct {
	Name  string `json:"name"`
	UID   int    `json:"-"`
	GID   int    `json:"-"`
	Home  string `json:"-"`
	Shell string `json:"-"`
}

// panelAccount is the account the panel process runs as, read from the OS
// user database rather than $HOME (a systemd unit inherits no HOME).
func panelAccount() Account {
	u, err := user.Current()
	if err != nil || u == nil {
		return Account{Name: "root", Home: "/root", Shell: findShell()}
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return Account{Name: u.Username, UID: uid, GID: gid, Home: u.HomeDir, Shell: findShell()}
}

// parsePasswd reads passwd(5) lines; malformed lines are skipped.
func parsePasswd(r io.Reader) []Account {
	var out []Account
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 7 {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, Account{Name: f[0], UID: uid, GID: gid, Home: f[5], Shell: f[6]})
	}
	return out
}

// minLoginUID is where Debian/Ubuntu start regular accounts.
const minLoginUID = 1000

func loginShell(shell string) bool {
	base := path.Base(shell)
	return shell != "" && base != "nologin" && base != "false"
}

// allowedAccounts is the run-as allowlist (spec §2): the panel's own account
// always; every regular login account too, but only when the panel is root
// and can therefore drop to them.
func allowedAccounts(panel Account, panelIsRoot bool, passwd []Account) []Account {
	out := []Account{panel}
	if !panelIsRoot {
		return out
	}
	for _, a := range passwd {
		if a.Name == panel.Name || a.UID < minLoginUID || !loginShell(a.Shell) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (h *Handler) accounts() []Account {
	if !h.isRoot() {
		return allowedAccounts(h.panel, false, nil)
	}
	f, err := os.Open(h.passwdPath)
	if err != nil {
		return allowedAccounts(h.panel, true, nil)
	}
	defer f.Close()
	return allowedAccounts(h.panel, true, parsePasswd(f))
}

// resolveAccount maps a client-supplied name onto the allowlist. An empty
// name means the panel account.
func (h *Handler) resolveAccount(name string) (Account, bool) {
	if name == "" {
		return h.panel, true
	}
	for _, a := range h.accounts() {
		if a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}
