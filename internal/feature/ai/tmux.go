package ai

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultSocketRoot is where the panel's tmux sockets live. Deliberately
	// not /tmp (`-L sfpanel` would put them in /tmp/tmux-<uid>): the shipped
	// unit sets PrivateTmp=true, so the service's /tmp is a private mount that
	// systemd *deletes* when the service stops — and every `systemctl restart
	// sfpanel`, self-update included, is a stop. The tmux servers themselves do
	// survive, in their own scopes; their sockets would not. The restarted
	// panel would get a fresh empty /tmp, report every session `ended`, and
	// leave the servers running with no handle at all — not even `tmux -L
	// sfpanel ls` from a root SSH shell, which is in a different mount
	// namespace. /run is shared and nothing removes it (never give the unit a
	// RuntimeDirectory=sfpanel: that would).
	defaultSocketRoot = "/run/sfpanel/ai"
	socketName        = "sfpanel"

	historyLines = 2000
	// waitingAfter: a tool that has printed nothing for this long is at a
	// prompt — every one of these CLIs animates a spinner while it works.
	waitingAfter = 5 * time.Second
	tmuxTimeout  = 15 * time.Second
)

// socketPath is the account's socket, one directory per uid so tmux can bind
// it (and its lock file) while running as them.
func (h *Handler) socketPath(acct Account) string {
	return filepath.Join(h.socketRoot, strconv.Itoa(acct.UID), socketName)
}

// ensureSocketDir prepares the socket directory for the spawn that starts an
// account's tmux server. The levels above are 0711 root — every account can
// traverse to its own, none can write or list — and the leaf is 0700 owned by
// the account.
func (h *Handler) ensureSocketDir(acct Account) error {
	for _, d := range []string{filepath.Dir(h.socketRoot), h.socketRoot} {
		if err := os.MkdirAll(d, 0o711); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o711); err != nil { // MkdirAll applies the umask
			return err
		}
	}
	dir := filepath.Dir(h.socketPath(acct))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
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
// types into a CLI prompt is tmux's to eat. status/mouse off: the pane fills
// the screen, so lines scrolling off the top land in xterm's own scrollback
// and the existing wheel/touch handlers keep working.
func tmuxOptions(defaultTerminal string) [][]string {
	return [][]string{
		{"set", "-g", "prefix", "None"},
		{"set", "-g", "status", "off"},
		{"set", "-g", "mouse", "off"},
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

// spawnArgv builds the one command that creates a session (spec §1):
//
//	systemd-run --scope --collect --unit=sfpanel-ai-<id> -- env [-i] HOME=… …
//	  [runuser -u <acct> --] tmux -f /dev/null -S <socket> <options…> ;
//	  new-session -d -s <id> -c <cwd>
//	  -- bash -l -c 'command "$0" "$@"; exec bash -l' <tool>
//
// The scope puts the tmux server outside sfpanel.service's cgroup so it
// survives a panel restart. The env prefix comes before runuser so nothing of
// the panel's environment reaches the account (accounts.go) — and so the
// account has a HOME at all. The login shell restores the account's PATH
// (~/.local/bin, nvm); the wrapper drops into an interactive shell when the
// tool exits instead of closing the session. Without systemd-run the session
// is merely setsid'd: it survives the browser but not a panel restart.
func (h *Handler) spawnArgv(id, cwd, tool string, acct Account, haveSystemdRun bool) (string, []string) {
	name, argv := h.tmuxCmd(acct)
	for _, opt := range tmuxOptions(h.defaultTerminal()) {
		argv = append(argv, opt...)
		argv = append(argv, ";")
	}
	shell := findShell()
	argv = append(argv, "new-session", "-d", "-s", id, "-c", cwd,
		"-e", "LANG=C.UTF-8", "-e", "COLORTERM=truecolor", "--")
	if tool == ToolShell {
		argv = append(argv, shell, "-l")
	} else {
		argv = append(argv, shell, "-l", "-c", fmt.Sprintf(`command "$0" "$@"; exec %s -l`, shell), tool)
	}
	if haveSystemdRun {
		return "systemd-run", append([]string{"--scope", "--quiet", "--collect", "--unit=sfpanel-ai-" + id, "--", name}, argv...)
	}
	return "setsid", append([]string{name}, argv...)
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
