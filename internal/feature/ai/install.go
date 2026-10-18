package ai

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/release"
)

const claudeInstallerURL = "https://claude.ai/install.sh"

var npmPackages = map[string]string{
	ToolCodex:  "@openai/codex",
	ToolGemini: "@google/gemini-cli",
}

// lookPath finds the panel's own npm and downloadNVM fetches nvm's installer.
// Vars for tests.
var (
	lookPath    = osExec.LookPath
	downloadNVM = func(ctx context.Context) (string, string, error) {
		return release.DownloadInstaller(ctx, release.NVMInstallerURL)
	}
)

// nvmNpmScript is accountNpmCommand's nvm route, for `bash -c`: $0 is the
// package, $1 nvm's installer when the account has no nvm yet. nvm.sh is
// loaded with --no-use, so a default that resolves to nothing — or a .nvmrc
// in the working directory — cannot fail the load; the script chooses the
// version itself. Not a login shell: nothing a dotfile prints or starts
// stands between the panel and the install.
const nvmNpmScript = `[ -z "$1" ] || bash "$1" || exit
. "$NVM_DIR/nvm.sh" --no-use || exit
if [ "$(nvm version default)" = N/A ]; then
	echo ">>> Installing Node.js LTS ..."
	nvm install --lts && nvm alias default "$(nvm current)" || exit
else
	nvm use default || exit
fi
echo ">>> Installing $0 ..."
npm install -g "$0"`

// InstallStream — POST /ai/tools/:tool/install-stream?user=
func (h *Handler) InstallStream(c echo.Context) error { return h.streamInstall(c) }

// UpdateStream — POST /ai/tools/:tool/update-stream?user=. Same command as
// install: Claude's installer always installs the latest version and
// repoints ~/.local/bin/claude; npm gets @latest.
func (h *Handler) UpdateStream(c echo.Context) error { return h.streamInstall(c) }

func (h *Handler) streamInstall(c echo.Context) error {
	tool := c.Param("tool")
	if tool == ToolShell || !validTool(tool) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidTool, "tool must be claude, codex or gemini")
	}
	acct, ok := h.resolveAccount(c.QueryParam("user"))
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "user is not a login account on this node")
	}
	defer func() {
		if tool == ToolClaude {
			h.invalidateTool(acct.Name, tool) // per-user installer: ~/.local/bin
			return
		}
		// npm -g: system-wide, or into the account's own prefix
		// (accountNpmCommand). Every account's entry covers both.
		h.invalidateToolEveryAccount(tool)
	}()

	// Assert the flusher while the response is still uncommitted, so this
	// failure can answer with a code like the guards above it.
	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSEError, "Streaming not supported")
	}
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	sendLine := func(line string) {
		// Every line — status, error, streamed output — is sanitised here.
		fmt.Fprintf(c.Response(), "data: %s\n\n", response.SanitizeOutput(line))
		flusher.Flush()
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Minute)
	defer cancel()

	// os/exec directly: output streams to the client line by line, and the
	// subprocess dies with the request through ctx.
	var cmd *osExec.Cmd
	verify := false
	switch tool {
	case ToolClaude:
		sendLine(">>> Installing Claude Code CLI for " + acct.Name + " ...")
		dlCtx, dlCancel := context.WithTimeout(ctx, 30*time.Second)
		defer dlCancel()
		scriptPath, dlOut, err := release.DownloadInstaller(dlCtx, claudeInstallerURL)
		for _, line := range splitLines(dlOut) {
			sendLine(line)
		}
		if err != nil {
			sendLine("ERROR: Failed to download Claude install script: " + err.Error())
			sendLine("[DONE]")
			return nil
		}
		defer os.Remove(scriptPath)
		if err := release.VerifyInstaller(scriptPath, "SFPANEL_CLAUDE_INSTALLER_SHA256", "claude"); err != nil {
			sendLine("ERROR: " + err.Error())
			sendLine("[DONE]")
			return nil
		}
		if acct.Name != h.panel.Name {
			// The temp file is 0600 root. Hand it to the account that will run
			// it: the installer writes only under that account's home, and the
			// account gains nothing by editing a script it is about to run.
			if err := os.Chown(scriptPath, acct.UID, acct.GID); err != nil {
				sendLine("ERROR: could not hand the installer to " + acct.Name + ": " + err.Error())
				sendLine("[DONE]")
				return nil
			}
			cmd = osExec.CommandContext(ctx, "runuser", "-u", acct.Name, "--", "bash", scriptPath)
		} else {
			cmd = osExec.CommandContext(ctx, "bash", scriptPath)
		}
		cmd.Env = installEnv(os.Environ(), acct, h.panel)
	case ToolCodex, ToolGemini:
		// Named rather than `default`: a fourth tool must arrive with its own
		// install command instead of falling into `npm install -g …@latest`.
		pkg := npmPackages[tool] + "@latest"
		if npm := h.systemNpm(); npm != "" {
			sendLine(">>> Installing " + pkg + " via npm ...")
			cmd = osExec.CommandContext(ctx, npm, "install", "-g", pkg)
		} else {
			var cleanup func()
			if cmd, cleanup = h.accountNpmCommand(ctx, acct, pkg, sendLine); cmd == nil {
				sendLine("[DONE]")
				return nil
			}
			defer cleanup()
			verify = true
		}
	default:
		// Unreachable: validTool above rejects everything else.
		sendLine("ERROR: no install command for this tool")
		sendLine("[DONE]")
		return nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start install: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	scanner := bufio.NewScanner(stdout)
	exec.PrepareScanner(scanner)
	for scanner.Scan() {
		if line := scanner.Text(); !jobControlNoise(line) {
			sendLine(line)
		}
	}
	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: install failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	if verify && !h.toolFound(acct, tool) {
		// The account's own prefix reaches its PATH only through its
		// dotfiles, and a tool its login shell cannot find is one its
		// sessions cannot start. Say so here, rather than report an install
		// the card then lists as missing.
		sendLine("ERROR: " + npmPackages[tool] + " was installed, but " + acct.Name + "'s login shell does not find " + tool)
		sendLine("[DONE]")
		return nil
	}
	slog.Info("ai tool installed", "component", "ai", "tool", tool, "account", acct.Name)
	sendLine(">>> " + toolNames[tool] + " installed successfully!")
	sendLine("[DONE]")
	return nil
}

// systemNpm is the npm on the panel's own PATH when an install through it
// serves every account — a distribution or NodeSource npm, under /usr — and
// "" otherwise. One that resolves into a login account's home is that
// account's: the packages page links /usr/local/bin/npm into root's nvm, and
// `npm install -g` through the link writes under /root, which no other
// account can even enter.
func (h *Handler) systemNpm() string {
	p, err := lookPath("npm")
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	for _, a := range h.accounts() {
		if a.Home != "" && a.Home != "/" && (real == a.Home || strings.HasPrefix(real, a.Home+"/")) {
			return ""
		}
	}
	return p
}

// accountNpmCommand is `npm install -g pkg` run as the account with its own
// Node.js, for a host with no systemNpm. Node.js comes first when the account
// has none: the button promises Codex, not a trip to the packages page to
// install Node.js and back. Which Node.js, in this order:
//
//   - The account's nvm, ~/.nvm, where nvm's installer and the packages
//     page's Node.js button both put it, on its default when that resolves.
//     A default that resolves to nothing gets the current LTS installed and
//     the default pinned to it: nvm rewrites lts/* from every release index
//     it reads, so a default of lts/* turns N/A once a newer LTS ships, and
//     every tool installed under it drops off PATH.
//   - Without nvm, the npm the account's login shell finds — fnm, volta, a
//     prefix of the operator's own — run in that same shell, so an nvm laid
//     over it does not shadow the operator's setup.
//   - Neither: nvm's installer, then the LTS.
//
// As the account, not as the panel: the prefix is under the account's home,
// and its sessions and the tool probe find the tool there through the same
// dotfiles. A nil command means the failure is already in the stream.
func (h *Handler) accountNpmCommand(ctx context.Context, acct Account, pkg string, sendLine func(string)) (*osExec.Cmd, func()) {
	nvmDir := filepath.Join(acct.Home, ".nvm")
	installer := ""
	cleanup := func() {}
	if _, err := os.Stat(filepath.Join(nvmDir, "nvm.sh")); err != nil {
		if h.toolFound(acct, "npm") {
			sendLine(">>> Installing " + pkg + " with " + acct.Name + "'s npm ...")
			return h.asAccount(ctx, acct, nil, findShell(), "-lic", `npm install -g "$0"`, pkg), cleanup
		}
		sendLine(">>> " + acct.Name + " has no Node.js: installing nvm and Node.js LTS first ...")
		dlCtx, dlCancel := context.WithTimeout(ctx, 30*time.Second)
		defer dlCancel()
		path, dlOut, err := downloadNVM(dlCtx)
		for _, line := range splitLines(dlOut) {
			sendLine(line)
		}
		if err != nil {
			sendLine("ERROR: Failed to download nvm install script: " + err.Error())
			return nil, nil
		}
		cleanup = func() { os.Remove(path) }
		if err := release.VerifyInstaller(path, "SFPANEL_NVM_INSTALLER_SHA256", "nvm"); err != nil {
			cleanup()
			sendLine("ERROR: " + err.Error())
			return nil, nil
		}
		if acct.Name != h.panel.Name {
			// The same hand-over as Claude's installer gets.
			if err := h.chown(path, acct.UID, acct.GID); err != nil {
				cleanup()
				sendLine("ERROR: could not hand the installer to " + acct.Name + ": " + err.Error())
				return nil, nil
			}
		}
		installer = path
	}
	return h.asAccount(ctx, acct, []string{"NVM_DIR=" + nvmDir}, "bash", "-c", nvmNpmScript, pkg, installer), cleanup
}

// asAccount runs argv as the account the way this file's installers run:
// under runuser unless it is the panel's own account, on installEnv plus the
// account's SHELL — nvm's installer adds itself to the dotfile of the shell
// SHELL names, and a systemd unit's environment has no SHELL — and extra.
func (h *Handler) asAccount(ctx context.Context, acct Account, extra []string, argv ...string) *osExec.Cmd {
	if acct.Name != h.panel.Name {
		argv = append([]string{"runuser", "-u", acct.Name, "--"}, argv...)
	}
	cmd := osExec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(append(installEnv(os.Environ(), acct, h.panel), "SHELL="+acct.Shell), extra...)
	return cmd
}

// toolFound runs the tool probe afresh, past the memo.
func (h *Handler) toolFound(acct Account, tool string) bool {
	out, err := h.shellAs(acct, probeScript, tool)
	if err != nil {
		return false
	}
	_, _, ok := parseProbe(out)
	return ok
}

// jobControlNoise is what an interactive bash with no terminal writes first
// (see shellAs). It says nothing about the install.
func jobControlNoise(line string) bool {
	return strings.Contains(line, "cannot set terminal process group") || strings.Contains(line, "no job control in this shell")
}

// installEnv is the environment an installer subprocess runs with.
//
// The panel's own account gets the panel environment plus a noninteractive apt
// and, crucially, its HOME: the panel inherits no HOME of its own — a systemd
// system unit is started without one (see accounts.go) — and
// claude.ai/install.sh dereferences $HOME with no fallback, so without this
// the script would put ~/.claude at the filesystem root. os/exec keeps the
// last value of a duplicated key, so the appended HOME wins over any
// inherited one.
//
// Any other account gets the fixed account base and nothing else, for the
// reason attachEnv gives: the script runs as *them* under runuser, so the
// panel's environment — `SFPANEL_JWT_SECRET` among it — would be theirs to
// read out of /proc/self/environ.
func installEnv(base []string, acct, panel Account) []string {
	if acct.Name != panel.Name {
		return append(accountEnv(acct), "DEBIAN_FRONTEND=noninteractive")
	}
	return append(base, "DEBIAN_FRONTEND=noninteractive", "HOME="+acct.Home)
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
