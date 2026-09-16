package ai

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	osExec "os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/wsorigin"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/feature/audit"
)

var attachUpgrader = websocket.Upgrader{CheckOrigin: wsorigin.CheckOrigin}

// Same half-open detection as the terminal: ping every 30 s, give up on a
// client that has not ponged for 70 s.
const (
	attachPingInterval = 30 * time.Second
	attachReadDeadline = 70 * time.Second
	attachWriteTimeout = 10 * time.Second
)

// replayHistory turns capture-pane output (LF-separated, ANSI kept by -e)
// into terminal bytes with CR LF line ends, so xterm lays the lines out
// exactly as tmux did.
func replayHistory(out string) []byte {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	return []byte(strings.ReplaceAll(out, "\n", "\r\n") + "\r\n")
}

// Enable wheel reporting on every attach, including sessions created by older
// panels with mouse off. tmux uses the terminal's alternate buffer: its history
// lives in tmux, not in xterm's scrollback. Mouse input reaches copy mode or the
// active CLI according to tmux's native bindings.
func (h *Handler) attachArgv(acct Account, id string) (string, []string) {
	name, argv := h.tmuxBase(acct)
	return name, append(argv, "set-option", "-t", id, "mouse", "on", ";", "attach-session", "-t", id)
}

// What the attach client runs with.
const (
	attachTerm = "TERM=xterm-256color"
	attachPath = "PATH=/usr/local/bin:/usr/bin:/bin"
	attachLang = "LANG=C.UTF-8"
)

// attachEnv is the environment of the tmux client. The panel's own account
// gets the panel's environment — same account, same privileges, nothing to
// cross. Any other account gets these four variables and nothing else: the
// client is exec'd through runuser and then runs as *them*, and after that
// exec its /proc/self/environ is readable by them, so the panel's environment
// would be theirs to read. `SFPANEL_JWT_SECRET` is a supported config
// override (internal/config/config.go), i.e. the ability to mint admin
// tokens; systemd's `NOTIFY_SOCKET` is a smaller instance of the same leak.
// A tmux client needs no more than this: -f /dev/null leaves no config to
// find and the socket is named absolutely with -S, so neither HOME nor
// TMUX_TMPDIR decides where it looks.
//
// The session's profile is deliberately not among them. The variable belongs
// to the process running the tool, and that process's environment was fixed
// when the session was spawned (spec §4); this client only draws the pane it
// attaches to, so setting it here would change nothing and could only
// disagree with what the pane actually runs on.
func attachEnv(base []string, acct, panel Account) []string {
	if acct.Name == panel.Name {
		return append(base, attachTerm)
	}
	return []string{attachTerm, attachPath, attachLang, "HOME=" + acct.Home}
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// AttachWS — GET /ws/ai/attach?session_id=. One tmux client per socket; the
// client dies with the socket and the session does not notice.
func (h *Handler) AttachWS(jwtSecret string, auditWriter *sfdb.AsyncWriter, localNodeIDFn func() string) echo.HandlerFunc {
	return func(c echo.Context) error {
		username, err := auth.AuthenticateWSUpgrade(c.Response(), c.Request(), jwtSecret)
		if err != nil {
			return err
		}
		id := c.QueryParam("session_id")
		if !validSessionID(id) {
			return response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
		}
		// A live session without a row is attached as the panel account — but
		// only when the table really has no row for it. A read error is not a
		// not-found: silently switching the account would attach the wrong one.
		acct := h.panel
		row, found, err := getSessionRow(h.DB, id)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the session")
		}
		if found {
			a, ok := h.resolveAccount(row.RunAs)
			if !ok {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "the session's account no longer exists")
			}
			acct = a
		}

		ws, err := attachUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		nodeID := ""
		if localNodeIDFn != nil {
			nodeID = localNodeIDFn()
		}
		// /ws/* bypasses AuditMiddleware; this is a host shell, so leave a row.
		audit.RecordWSSession(auditWriter, username, c.Request().URL.Path, c.RealIP(), nodeID)
		slog.Info("ai session attach", "component", "ai", "user", username, "id", id, "account", acct.Name)

		var writeMu sync.Mutex
		send := func(mt int, b []byte) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			_ = ws.SetWriteDeadline(time.Now().Add(attachWriteTimeout))
			return ws.WriteMessage(mt, b)
		}

		// 1. Gone? One notice, then close — the page refreshes its list.
		if _, err := h.tmux(acct, "has-session", "-t", id); err != nil {
			_ = send(websocket.TextMessage, []byte("\r\n[session ended]\r\n"))
			return nil
		}

		// 2. Replay history into the normal buffer before attach switches to
		// tmux's alternate screen. Live history navigation is handled by tmux.
		if out, err := h.tmux(acct, "capture-pane", "-p", "-e", "-J", "-t", id, "-S", "-"+strconv.Itoa(historyLines), "-E", "-1"); err == nil {
			if b := replayHistory(out); len(b) > 0 {
				if err := send(websocket.BinaryMessage, b); err != nil {
					return nil
				}
			}
		}

		// 3. The attach client. os/exec directly: pty.Start needs the raw
		// *exec.Cmd, and the process lives exactly as long as this socket.
		name, argv := h.attachArgv(acct, id)
		cmd := osExec.Command(name, argv...)
		cmd.Env = attachEnv(os.Environ(), acct, h.panel)
		ptmx, err := pty.Start(cmd)
		if err != nil {
			_ = send(websocket.TextMessage, []byte("Failed to attach: "+response.SanitizeOutput(err.Error())))
			return nil
		}
		defer func() {
			ptmx.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		}()
		_ = touchSessionAttached(h.DB, id)

		// PTY → WS. When the client exits (session killed elsewhere) the
		// read ends and the socket is closed, which unblocks the loop below.
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 8192)
			for {
				n, err := ptmx.Read(buf)
				if n > 0 {
					if werr := send(websocket.BinaryMessage, buf[:n]); werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		go func() {
			<-done
			_ = send(websocket.TextMessage, []byte("\r\n[detached]\r\n"))
			_ = ws.Close()
		}()
		go func() {
			t := time.NewTicker(attachPingInterval)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					if err := send(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()
		_ = ws.SetReadDeadline(time.Now().Add(attachReadDeadline))
		ws.SetPongHandler(func(string) error {
			_ = ws.SetReadDeadline(time.Now().Add(attachReadDeadline))
			return nil
		})

		// WS → PTY (runs until the socket closes).
		for {
			mt, msg, err := ws.ReadMessage()
			if err != nil {
				return nil
			}
			if mt == websocket.TextMessage {
				var r resizeMsg
				if json.Unmarshal(msg, &r) == nil && r.Type == "resize" {
					if r.Cols == 0 || r.Rows == 0 {
						continue // a 0×0 winsize wedges full-screen TUIs
					}
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: r.Cols, Rows: r.Rows})
					continue
				}
			}
			if _, err := ptmx.Write(msg); err != nil {
				return nil
			}
		}
	}
}
