// Package ai runs Claude Code, Codex and Gemini CLI inside panel-managed tmux
// sessions and reports their install state per OS account. Sessions are
// per-node processes; nothing here replicates through the cluster FSM, and
// every handler is local-only — ?node= is the proxy middleware's job.
package ai

import (
	"database/sql"
	"os"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
	ToolGemini = "gemini"
	ToolShell  = "shell"
)

// cliTools are the tools that have an install state; shell is always there.
var cliTools = []string{ToolClaude, ToolCodex, ToolGemini}

func validTool(t string) bool {
	switch t {
	case ToolClaude, ToolCodex, ToolGemini, ToolShell:
		return true
	}
	return false
}

// Session states derived from tmux (spec §1).
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateShell   = "shell"
	StateEnded   = "ended"
)

const maxSessions = 20

type Handler struct {
	DB         *sql.DB
	Cmd        exec.Commander
	StacksPath string

	panel      Account     // the account the panel process runs as
	passwdPath string      // /etc/passwd; tests point it at a fixture
	isRoot     func() bool // os.Geteuid() == 0; tests override

	termOnce sync.Once
	term     string // default-terminal chosen once per process

	memoMu     sync.Mutex
	toolMemo   map[string]toolMemoEntry   // "<account>\x00<tool>"
	latestMemo map[string]latestMemoEntry // "<tool>"

	now func() time.Time
}

func NewHandler(db *sql.DB, cmd exec.Commander, stacksPath string) *Handler {
	return &Handler{
		DB:         db,
		Cmd:        cmd,
		StacksPath: stacksPath,
		panel:      panelAccount(),
		passwdPath: "/etc/passwd",
		isRoot:     func() bool { return os.Geteuid() == 0 },
		toolMemo:   map[string]toolMemoEntry{},
		latestMemo: map[string]latestMemoEntry{},
		now:        time.Now,
	}
}

// findShell mirrors the terminal module: bash when present, else sh.
func findShell() string {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "/bin/sh"
}

// Temporary stubs so the package compiles until Tasks 3 and 7 land; they are
// replaced by the real types, not kept.
type toolMemoEntry struct{}
type latestMemoEntry struct{}
type Account struct{ Name string }

func panelAccount() Account { return Account{Name: "root"} }
