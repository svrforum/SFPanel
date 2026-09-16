package ai

import (
	"bufio"
	"io"
	"log/slog"
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
		// Degrading to the panel account alone is safe but silent: the run-as
		// selector would simply lose every other account, which looks like a
		// product decision rather than an unreadable /etc/passwd.
		slog.Warn("ai could not read the account database", "component", "ai", "path", h.passwdPath, "err", err)
		return allowedAccounts(h.panel, true, nil)
	}
	defer f.Close()
	return allowedAccounts(h.panel, true, parsePasswd(f))
}

// accountPath is the PATH every account-facing subprocess starts with: the
// distribution default, the same one systemd gives a system unit. The login
// shell inside the session replaces it with the account's own.
const accountPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// accountEnv is the environment of everything the module runs as an account —
// tmux servers and clients, the tool probe, an installer. It is built, never
// inherited, for two independent reasons:
//
//   - The panel has no HOME to pass on. systemd.exec sets $HOME only for units
//     with User=, and the panel's unit has none; bash does not fill it in, and
//     tmux copies its spawn environment into the session. `$HOME/.local/bin`
//     in /etc/skel/.profile (and in Claude's own installer) then expands to
//     `/.local/bin`, so root's Claude reads as "not installed" and a root
//     session drops straight to `claude: command not found`.
//   - What crosses runuser into another account has to be chosen. The panel's
//     environment carries `SFPANEL_JWT_SECRET` when the operator uses that
//     supported override (internal/config/config.go) and whatever else their
//     unit's `Environment=` lines add; a process running as that account can
//     read its own /proc/self/environ, and the JWT secret is the ability to
//     mint admin tokens. Same boundary attachEnv draws for the attach client.
func accountEnv(acct Account) []string {
	return []string{
		"PATH=" + accountPath,
		"HOME=" + acct.Home,
		"USER=" + acct.Name,
		"LOGNAME=" + acct.Name,
		"SHELL=" + acct.Shell,
		"LANG=C.UTF-8",
		"COLORTERM=truecolor",
	}
}

// envArgv is accountEnv as an `env` argv prefix — exec.Commander can only
// append to the panel's own environment, so the prefix is the honest route,
// and it covers the setsid fallback too. A non-panel account gets `env -i`:
// nothing of the panel's environment survives. The panel's own account is not
// a boundary (same uid, same privileges), so there it only pins the variables
// above on top of what the process already has.
func (h *Handler) envArgv(acct Account) []string {
	argv := []string{"env"}
	if acct.Name != h.panel.Name {
		argv = append(argv, "-i")
	}
	return append(argv, accountEnv(acct)...)
}

// profileVar is the single environment entry a profile adds: the variable the
// tool reads for its configuration and credential directory (profiles.go),
// pointed at that profile's directory.
//
// ok=false means "no variable at all", and it is an answer, not an error the
// caller has to handle: the default profile is the tool's own directory and
// must leave the variable unset rather than set it to something, a tool
// outside profileEnv has no variable to set, and a name profileDir refuses is
// one that never passed CreateSession's membership check — the refusal
// belongs there, where it can be answered with a code, not here.
func profileVar(acct Account, tool, profile string) (string, bool) {
	name, ok := profileEnv[tool]
	if !ok || profile == "" {
		return "", false
	}
	dir, err := profileDir(acct, tool, profile)
	if err != nil {
		return "", false
	}
	return name + "=" + dir, true
}

// sessionEnvArgv is envArgv plus that entry: the environment of a spawn that
// carries one session's tool. Only the spawn takes it — the probe and the
// installer keep envArgv, because they answer whether a tool is installed and
// no profile changes that.
func (h *Handler) sessionEnvArgv(acct Account, tool, profile string) []string {
	argv := h.envArgv(acct)
	if v, ok := profileVar(acct, tool, profile); ok {
		argv = append(argv, v)
	}
	return argv
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
