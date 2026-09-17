package ai

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultSocketRoot is where a root panel's tmux sockets live.
	// Deliberately not /tmp (`-L sfpanel` would put them in /tmp/tmux-<uid>):
	// the shipped unit sets PrivateTmp=true, so the service's /tmp is a
	// private mount that systemd *deletes* when the service stops — and every
	// `systemctl restart sfpanel`, self-update included, is a stop. The tmux
	// servers themselves do survive, in their own transient units; their
	// sockets would not. The restarted panel would get a fresh empty /tmp,
	// report every session `ended`, and leave the servers running with no
	// handle at all — not even `tmux -L sfpanel ls` from a root SSH shell,
	// which is in a different mount namespace.
	//
	// /run is shared and nothing removes it. **Never make this a
	// RuntimeDirectory= on sfpanel.service**: systemd deletes a
	// RuntimeDirectory when the unit stops, which is the very failure this
	// path exists to avoid — it would put the sockets back inside the panel's
	// lifetime through a different door.
	defaultSocketRoot = "/run/sfpanel/ai"
	socketName        = "sfpanel"

	// tmuxMinVersion is the floor for the option set in tmuxOptions.
	// `window-size latest` arrived in 3.1, and the set as a whole is verified
	// only from 3.2 — Ubuntu 22.04 and Debian 12, both older releases being out
	// of support. Below it tmux answers a create with a usage error, which
	// reached the operator as a bare COMMAND_FAILED naming nothing useful.
	tmuxMinVersion = "3.2"

	historyLines = 2000
	// waitingAfter: a tool that has printed nothing for this long is at a
	// prompt — every one of these CLIs animates a spinner while it works.
	waitingAfter = 5 * time.Second
	tmuxTimeout  = 15 * time.Second
)

// socketRoot picks where the sockets live. /run is only the root panel's to
// create; a panel running as an ordinary account cannot write /run/sfpanel at
// all, which used to fail every session on such an install. It has a single
// account — itself — so its own state directory serves.
func socketRoot(stateDir string, panelIsRoot bool) string {
	if panelIsRoot {
		return defaultSocketRoot
	}
	return filepath.Join(stateDir, "ai")
}

// socketPath is the account's socket, one directory per uid so tmux can bind
// it (and its lock file) while running as them.
func (h *Handler) socketPath(acct Account) string {
	return filepath.Join(h.socketRoot, strconv.Itoa(acct.UID), socketName)
}

// ensureSocketDir prepares the socket directory for the spawn that starts an
// account's tmux server. Under a root panel the levels above are 0711 root —
// every account can traverse to its own, none can write or list — and the leaf
// is 0700 owned by the account.
//
// A non-root panel gets neither half, on purpose. It has only its own account,
// so nothing has to traverse in; and its root's parent is the panel state
// directory, which is 0700 and holds the database — widening it to 0711 to
// make room for accounts that cannot exist would be a real loss for no gain.
// Giving a directory away is root's privilege too, and there is nobody to give
// it to.
func (h *Handler) ensureSocketDir(acct Account) error {
	panelIsRoot := h.isRoot()
	if panelIsRoot {
		for _, d := range []string{filepath.Dir(h.socketRoot), h.socketRoot} {
			if err := os.MkdirAll(d, 0o711); err != nil {
				return err
			}
			if err := os.Chmod(d, 0o711); err != nil { // MkdirAll applies the umask
				return err
			}
		}
	} else if err := os.MkdirAll(h.socketRoot, 0o700); err != nil {
		return err
	}
	dir := filepath.Dir(h.socketPath(acct))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if !panelIsRoot {
		return nil
	}
	return h.chown(dir, acct.UID, acct.GID)
}

// tmuxBase is how every tmux invocation starts: never the account's own
// config (-f /dev/null — the operator's ~/.tmux.conf auto-restores sessions),
// always the panel's own socket named absolutely (-S, see defaultSocketRoot),
// and runuser only when the account is not the panel's own.
func (h *Handler) tmuxBase(acct Account) (string, []string) {
	argv := []string{"-f", "/dev/null", "-S", h.socketPath(acct)}
	if acct.Name != h.panel.Name {
		return "runuser", append([]string{"-u", acct.Name, "--", "tmux"}, argv...)
	}
	return "tmux", argv
}

// tmuxCmd is tmuxBase behind the account's explicit environment (accounts.go).
// Every tmux invocation goes through it except the attach client, which sets
// its own environment through cmd.Env — and must, because it needs a TERM.
func (h *Handler) tmuxCmd(acct Account) (string, []string) {
	prefix := h.envArgv(acct)
	name, argv := h.tmuxBase(acct)
	return prefix[0], append(append(prefix[1:], name), argv...)
}

// tmux runs one tmux command as the account.
func (h *Handler) tmux(acct Account, args ...string) (string, error) {
	name, argv := h.tmuxCmd(acct)
	return h.Cmd.RunWithTimeout(tmuxTimeout, name, append(argv, args...)...)
}

// tmuxOptions is the panel's entire tmux configuration. It is applied in the
// same invocation that creates a session, before new-session, so it governs
// the very first window; there is no file because the state dir is 0700 and
// a non-root account could not read one. prefix None: nothing an operator
// types into a CLI prompt is tmux's to eat. status off gives the pane the full
// screen. mouse on lets wheel/touch input reach tmux history or a mouse-aware
// CLI; the outer terminal's alternate buffer has no scrollback of its own.
func tmuxOptions(defaultTerminal string) [][]string {
	return [][]string{
		{"set", "-g", "prefix", "None"},
		{"set", "-g", "status", "off"},
		{"set", "-g", "mouse", "on"},
		{"set", "-g", "history-limit", "20000"},
		{"set", "-g", "default-terminal", defaultTerminal},
		{"set", "-g", "terminal-overrides", ",xterm-256color:Tc"},
		{"set", "-sg", "escape-time", "10"},
		{"set", "-g", "window-size", "latest"},
		{"set", "-g", "aggressive-resize", "on"},
		{"set", "-g", "monitor-bell", "on"},
		{"set", "-g", "bell-action", "any"},
		{"set", "-g", "visual-bell", "off"},
		{"set", "-g", "remain-on-exit", "off"},
		{"set", "-g", "exit-empty", "on"},
	}
}

// defaultTerminal is tmux-256color when the host has its terminfo, else the
// universally present screen-256color. Decided once per process.
func (h *Handler) defaultTerminal() string {
	h.termOnce.Do(func() {
		h.term = "tmux-256color"
		if _, err := h.Cmd.Run("infocmp", "tmux-256color"); err != nil {
			h.term = "screen-256color"
		}
	})
	return h.term
}

func (h *Handler) haveSystemdRun() bool { return h.Cmd.Exists("systemd-run") }

// serviceFormAvailable is whether the next spawn could take the server form:
// only root may ask PID 1 to run a unit as another account, and systemd-run
// has to be on the host. The one predicate behind all three of spawnFormFor,
// persistence() and the systemd_run field of the /ai/tools bundle — when the
// bundle asked haveSystemdRun() alone, a non-root panel put the "will not
// survive a panel restart" marker on every tab and answered the page that
// everything was fine, so nothing on it explained the marker.
func (h *Handler) serviceFormAvailable() bool { return h.isRoot() && h.haveSystemdRun() }

// leadingVersion is the major.minor at the *start* of a tmux version string:
// "3.2a" and "3.6a" parse, "next-3.5" and "" do not. ok=false is the answer
// for anything unreadable, and every caller treats that as supported — a
// version string we cannot parse is no reason to refuse to run.
var leadingVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

func leadingVersion(v string) (int, int, bool) {
	m := leadingVersionRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// tmuxVersionSupported takes `tmux -V`'s output minus its "tmux " prefix.
func tmuxVersionSupported(v string) bool {
	major, minor, ok := leadingVersion(v)
	if !ok {
		return true
	}
	wantMajor, wantMinor, _ := leadingVersion(tmuxMinVersion)
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}

// tmuxVersionTTL is how long `tmux -V` is believed. Not sync.Once: the page
// asks for the version on every poll, so the probe has to be memoised, but the
// refusal below the floor tells the operator to upgrade tmux from the Packages
// page — and a value pinned for the panel's lifetime kept naming the old one
// afterwards, so the banner asked for something that did not work until the
// panel restarted. Ten minutes, the same window toolStatus uses.
const tmuxVersionTTL = 10 * time.Minute

// tmuxVersion is `tmux -V` without its prefix, re-probed after tmuxVersionTTL.
// A failed probe keeps the last answer rather than clearing it: "" reads as
// supported, so forgetting a version we know is below the floor would let a
// create through to a usage error.
func (h *Handler) tmuxVersion() string {
	h.memoMu.Lock()
	v, at := h.tmuxVer, h.tmuxVerAt
	h.memoMu.Unlock()
	if !at.IsZero() && h.now().Sub(at) < tmuxVersionTTL {
		return v
	}
	if out, err := h.Cmd.Run("tmux", "-V"); err == nil {
		v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "tmux "))
	}
	h.memoMu.Lock()
	h.tmuxVer, h.tmuxVerAt = v, h.now()
	h.memoMu.Unlock()
	return v
}

// tmuxTooOld is the refusal message for a tmux below the floor, or "" when the
// host is fine. Both versions are named: "tmux 3.2 is required" alone leaves
// the operator guessing what they have.
func (h *Handler) tmuxTooOld() string {
	v := h.tmuxVersion()
	if tmuxVersionSupported(v) {
		return ""
	}
	return fmt.Sprintf("tmux %s or newer is required (found %s)", tmuxMinVersion, v)
}

// unitName is the transient unit that holds one account's tmux server. Per
// uid, never per session: the server outlives every session it carries, and
// the unit ends only when the last one exits (exit-empty on).
func unitName(acct Account) string { return "sfpanel-ai-" + strconv.Itoa(acct.UID) }

// spawnForm is which of the two shapes in spec §1 a spawn takes.
type spawnForm int

const (
	// spawnService asks PID 1 for a transient service that *is* the account's
	// tmux server. Only when no server is running for the account.
	spawnService spawnForm = iota
	// spawnClient talks to a server that is already up: an ordinary tmux
	// client, which exits as soon as new-session has been created.
	spawnClient
	// spawnSetsid is the fallback that has to start the server itself without
	// systemd's help — it survives the browser, not a panel restart.
	spawnSetsid
)

// spawnFormFor picks the form for the next spawn. The server form needs all
// three of: no server yet, a panel that is root (only root may ask PID 1 to
// run a unit as another account), and systemd-run on the host.
func (h *Handler) spawnFormFor(acct Account) spawnForm {
	if h.serverRunning(acct) {
		return spawnClient
	}
	if h.serviceFormAvailable() {
		return spawnService
	}
	return spawnSetsid
}

// serverRunning asks the socket whether the account already has a tmux server.
// Deliberately not liveWindows: that one folds every failure into an empty map,
// and "no windows" and "no server" must not be the same answer here — the first
// means talk to the socket, the second means start a unit.
func (h *Handler) serverRunning(acct Account) bool {
	name, argv := h.tmuxCmd(acct)
	_, err := h.Cmd.RunWithTimeout(tmuxTimeout, name, append(argv, "list-sessions")...)
	return err == nil
}

// sessionCommands is every tmux argument after `-f /dev/null -S <socket>`: the
// panel's options, applied in the same invocation and before new-session so
// they govern the very first window, then the session itself. The login shell
// restores the account's PATH (~/.local/bin, nvm); the wrapper drops into an
// interactive shell when the tool exits instead of closing the session.
//
// The wrapper is -lic, not -l -c: ~/.local/bin joins PATH from the account's
// .bashrc, and the Debian/Ubuntu skeleton opens that file with
// `[ -z "$PS1" ] && return`, so a *non*-interactive shell returns before the
// line that extends PATH and never sees the tool. `bash -l -c` exited 127 with
// "claude: command not found" for root — the panel's own default account —
// and dropped the pane straight to the fallback shell. The `shell` tool takes
// no flag: `bash -l` on tmux's tty is interactive already.
//
// No `-e` pairs: the session environment is the server's, and the server got
// it from systemd (--setenv) or from the env prefix that started it.
func sessionCommands(term, id, cwd, tool string) []string {
	var argv []string
	for _, opt := range tmuxOptions(term) {
		argv = append(argv, opt...)
		argv = append(argv, ";")
	}
	shell := findShell()
	argv = append(argv, "new-session", "-d", "-s", id, "-c", cwd, "--")
	if tool == ToolShell {
		return append(argv, shell, "-l")
	}
	return append(argv, shell, "-lic", fmt.Sprintf(`command "$0" "$@"; exec %s -l`, shell), tool)
}

// spawnArgv builds the one command that creates a session (spec §1):
//
//	# server form — the account has no tmux server yet
//	systemd-run --unit=sfpanel-ai-<uid> --collect --uid=<acct> --gid=<gid>
//	  -p Type=forking --setenv=LANG=C.UTF-8 --setenv=COLORTERM=truecolor --
//	  tmux -f /dev/null -S <socket> <options…> ; new-session -d -s <id> …
//
//	# client form — the server is already up (or the setsid fallback)
//	[setsid] env [-i] HOME=… … [runuser -u <acct> --]
//	  tmux -f /dev/null -S <socket> <options…> ; new-session -d -s <id> …
//
// A *service*, not a scope. systemd-run --scope forks the caller, so the tmux
// server would inherit the panel's environment (SFPANEL_JWT_SECRET included,
// readable by the account in /proc/<pid>/environ) and the panel's PrivateTmp
// mount namespace — which systemd tears down on every `systemctl restart
// sfpanel`, leaving the sessions this design exists to preserve with a /tmp
// that no longer exists. A service is spawned by PID 1 instead: its cgroup is
// /system.slice/sfpanel-ai-<uid>.service, its mount namespace is the host's,
// and --uid makes systemd set HOME/USER/LOGNAME/SHELL itself, so no runuser
// and no env prefix are needed. The client form keeps both, because there it
// is the panel that forks (accounts.go explains the env -i boundary).
func (h *Handler) spawnArgv(id, cwd, tool string, acct Account, form spawnForm) (string, []string) {
	cmds := sessionCommands(h.defaultTerminal(), id, cwd, tool)
	if form == spawnService {
		argv := []string{
			"--unit=" + unitName(acct), "--collect",
			"--uid=" + acct.Name, "--gid=" + strconv.Itoa(acct.GID),
			"-p", "Type=forking",
			"--setenv=LANG=C.UTF-8", "--setenv=COLORTERM=truecolor",
			"--", "tmux", "-f", "/dev/null", "-S", h.socketPath(acct),
		}
		return "systemd-run", append(argv, cmds...)
	}
	name, argv := h.tmuxCmd(acct)
	argv = append(argv, cmds...)
	if form == spawnSetsid {
		return "setsid", append([]string{name}, argv...)
	}
	return name, argv
}

// liveWindow is one line of listWindowsFormat for a session's current window.
type liveWindow struct {
	Session  string
	Command  string
	Attached bool
	Activity time.Time
	Bell     bool
}

const listWindowsFormat = "#{session_name}\t#{window_active}\t#{pane_current_command}\t#{session_attached}\t#{window_activity}\t#{window_bell_flag}"

// parseListWindows keeps the current window of each session (an operator can
// open more from the shell; the state of the one they would see is what
// matters). Lines that do not have exactly six fields are ignored.
func parseListWindows(out string) map[string]liveWindow {
	live := map[string]liveWindow{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 6 || f[1] != "1" {
			continue
		}
		act, _ := strconv.ParseInt(f[4], 10, 64)
		attached, _ := strconv.Atoi(f[3])
		live[f[0]] = liveWindow{Session: f[0], Command: f[2], Attached: attached > 0, Activity: time.Unix(act, 0), Bell: f[5] == "1"}
	}
	return live
}

var shellNames = map[string]bool{"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true}

// deriveState is the state table of spec §1 for a live session.
func deriveState(w liveWindow, now time.Time) string {
	if shellNames[w.Command] {
		return StateShell
	}
	if w.Bell || now.Sub(w.Activity) >= waitingAfter {
		return StateWaiting
	}
	return StateWorking
}

// liveWindows lists the account's sessions on the panel socket. A non-zero
// exit is the normal "no server running" answer once every session has
// ended, so it is an empty map, not an error.
func (h *Handler) liveWindows(acct Account) map[string]liveWindow {
	out, err := h.tmux(acct, "list-windows", "-a", "-F", listWindowsFormat)
	if err != nil {
		slog.Debug("tmux list-windows", "component", "ai", "account", acct.Name, "err", err)
		return map[string]liveWindow{}
	}
	return parseListWindows(out)
}
