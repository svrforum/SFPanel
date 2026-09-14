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

	socketRoot string                       // where the tmux sockets live; tests use a temp dir
	chown      func(string, int, int) error // os.Chown; a test binary is not root

	// spawnMu serialises session creation. The server form claims one fixed
	// unit name per account, so the "is there a server yet" check and the
	// spawn that acts on it have to be one step.
	spawnMu sync.Mutex

	termOnce sync.Once
	term     string // default-terminal chosen once per process

	memoMu     sync.Mutex
	toolMemo   map[string]toolMemoEntry   // "<account>\x00<tool>"
	latestMemo map[string]latestMemoEntry // "<tool>"
	tmuxVer    string                     // `tmux -V` minus its prefix
	tmuxVerAt  time.Time                  // when tmuxVer was probed; zero = never

	now func() time.Time
}

// NewHandler wires the module. stateDir is the panel's state directory (the
// directory holding the SQLite database); it is where the tmux sockets go when
// the panel is not root and /run/sfpanel is out of reach.
func NewHandler(db *sql.DB, cmd exec.Commander, stacksPath, stateDir string) *Handler {
	isRoot := func() bool { return os.Geteuid() == 0 }
	return &Handler{
		DB:         db,
		Cmd:        cmd,
		StacksPath: stacksPath,
		panel:      panelAccount(),
		passwdPath: "/etc/passwd",
		isRoot:     isRoot,
		socketRoot: socketRoot(stateDir, isRoot()),
		chown:      os.Chown,
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
