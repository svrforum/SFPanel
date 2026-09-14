package ai

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	osExec "os/exec"
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
	defer h.invalidateTool(acct.Name, tool)

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSEError, "Streaming not supported")
	}
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
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	default:
		if !h.Cmd.Exists("npm") {
			sendLine("ERROR: npm is not installed. Please install Node.js first.")
			sendLine("[DONE]")
			return nil
		}
		pkg := npmPackages[tool]
		sendLine(">>> Installing " + pkg + "@latest via npm ...")
		cmd = osExec.CommandContext(ctx, "npm", "install", "-g", pkg+"@latest")
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
		sendLine(scanner.Text())
	}
	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: install failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	slog.Info("ai tool installed", "component", "ai", "tool", tool, "account", acct.Name)
	sendLine(">>> " + toolNames[tool] + " installed successfully!")
	sendLine("[DONE]")
	return nil
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
