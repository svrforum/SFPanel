package terminal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/feature/settings"
)

const scrollbackBufSize = 256 * 1024 // 256 KB ring buffer per session
const maxTerminalSessions = 20       // Maximum concurrent terminal sessions

var Upgrader = websocket.Upgrader{
	// CheckOrigin allows all origins because auth uses explicit JWT token
	// in query params, not cookies. CSWSH is not a risk since credentials
	// are never sent automatically by the browser.
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func authenticateWS(c echo.Context, jwtSecret string) error {
	if middleware.IsInternalProxyRequest(c.Request()) {
		return nil
	}
	token := c.QueryParam("token")
	if token == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
	}
	if _, err := auth.ParseToken(token, jwtSecret); err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
	}
	return nil
}

// ringBuffer is a fixed-size circular byte buffer that keeps the most recent
// output, dropping the oldest bytes when the buffer is full.
type ringBuffer struct {
	buf  []byte
	pos  int
	full bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size)}
}

func (r *ringBuffer) Write(p []byte) {
	for _, b := range p {
		r.buf[r.pos] = b
		r.pos++
		if r.pos >= len(r.buf) {
			r.pos = 0
			r.full = true
		}
	}
}

func (r *ringBuffer) Bytes() []byte {
	if !r.full {
		return r.buf[:r.pos]
	}
	out := make([]byte, len(r.buf))
	n := copy(out, r.buf[r.pos:])
	copy(out[n:], r.buf[:r.pos])
	return out
}

type terminalSession struct {
	mu         sync.Mutex
	ptmx       *os.File
	cmd        *exec.Cmd
	lastUse    time.Time
	scrollback *ringBuffer
	writeMu    sync.Mutex // protects ptmx.Write and pty.Setsize (both write to the PTY fd)
	// readers keeps track of connected WebSocket clients so the
	// background PTY reader goroutine can fan-out output.
	readers   map[*websocket.Conn]struct{}
	readersMu sync.Mutex
	started   bool // whether the background reader is running
}

func (s *terminalSession) addReader(ws *websocket.Conn) {
	s.readersMu.Lock()
	s.readers[ws] = struct{}{}
	s.readersMu.Unlock()
}

func (s *terminalSession) removeReader(ws *websocket.Conn) {
	s.readersMu.Lock()
	delete(s.readers, ws)
	s.readersMu.Unlock()
}

// broadcast sends data to all connected WebSocket readers and also saves
// it to the scrollback buffer.
func (s *terminalSession) broadcast(data []byte) {
	s.mu.Lock()
	s.scrollback.Write(data)
	s.mu.Unlock()

	s.readersMu.Lock()
	for ws := range s.readers {
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
			delete(s.readers, ws)
		}
	}
	s.readersMu.Unlock()
}

// writeToReader writes to a specific reader under the same readersMu that
// broadcast holds, so scrollback replay on reconnect can't interleave with
// a simultaneous broadcast and tear the frame.
func (s *terminalSession) writeToReader(ws *websocket.Conn, data []byte) {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	if _, ok := s.readers[ws]; !ok {
		return
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
		delete(s.readers, ws)
	}
}

// startReader spawns a goroutine that reads from the PTY and broadcasts
// to all connected WebSocket clients. It runs for the lifetime of the session.
// When the PTY closes (e.g. user types 'exit'), the session is automatically
// cleaned up from the global sessions map.
func (s *terminalSession) startReader(sessionID string) {
	if s.started {
		return
	}
	s.started = true
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := s.ptmx.Read(buf)
			if err != nil {
				// PTY closed (shell exited) — clean up session
				sessionsMu.Lock()
				if sessions[sessionID] == s {
					s.ptmx.Close()
					if s.cmd.Process != nil {
						s.cmd.Process.Kill()
					}
					s.cmd.Wait()
					delete(sessions, sessionID)
				}
				sessionsMu.Unlock()
				return
			}
			s.broadcast(buf[:n])
		}
	}()
}

var (
	sessions   = make(map[string]*terminalSession)
	sessionsMu sync.Mutex
)

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func findShell() string {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "/bin/sh"
}

// TerminalWS creates a new PTY session or reconnects to an existing one
// and bridges it over a WebSocket. Authentication via query param token.
// Query param session_id identifies the session; on reconnect the scrollback
// buffer is replayed so the user sees previous output.
func TerminalWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := authenticateWS(c, jwtSecret); err != nil {
			return err
		}

		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		sessionID := c.QueryParam("session_id")
		if sessionID == "" {
			sessionID = "default"
		}

		sessionsMu.Lock()
		sess, exists := sessions[sessionID]
		if exists {
			// Check if the process is still alive
			if sess.cmd.ProcessState != nil {
				sess.ptmx.Close()
				delete(sessions, sessionID)
				exists = false
			}
		}
		sessionsMu.Unlock()

		if exists {
			sess.mu.Lock()
			sess.lastUse = time.Now()
			sess.mu.Unlock()

			// Register this WebSocket as a reader BEFORE replaying scrollback.
			// Otherwise a PTY write arriving between snapshot and addReader
			// would be lost: the broadcast goroutine wouldn't find this conn
			// in readers, and the replay path wouldn't include it.
			sess.addReader(ws)
			defer sess.removeReader(ws)

			// Replay scrollback buffer so the reconnected client sees history.
			sess.mu.Lock()
			history := sess.scrollback.Bytes()
			sess.mu.Unlock()
			if len(history) > 0 {
				// Go through broadcast's mutex path indirectly by using the
				// session's readers-map write: a dedicated write wouldn't race
				// broadcast on this conn because broadcast holds readersMu
				// around its iteration, and addReader above is under the same
				// mutex.
				sess.writeToReader(ws, history)
			}
		} else {
			// Check session limit before creating a new one
			sessionsMu.Lock()
			if len(sessions) >= maxTerminalSessions {
				sessionsMu.Unlock()
				ws.WriteMessage(websocket.TextMessage,
					[]byte(fmt.Sprintf("\r\nError: maximum terminal sessions reached (%d). Close unused sessions first.\r\n", maxTerminalSessions)))
				return nil
			}

			// Create new PTY session
			shell := findShell()
			cmd := exec.Command(shell)
			cmd.Dir = "/root"
			cmd.Env = append(os.Environ(),
				"TERM=xterm-256color",
				"LANG=ko_KR.UTF-8",
				"LC_ALL=ko_KR.UTF-8",
				"HOME=/root",
			)

			ptmx, err := pty.Start(cmd)
			if err != nil {
				sessionsMu.Unlock()
				ws.WriteMessage(websocket.TextMessage, []byte("Failed to start shell: "+err.Error()))
				return nil
			}

			sess = &terminalSession{
				ptmx:       ptmx,
				cmd:        cmd,
				lastUse:    time.Now(),
				scrollback: newRingBuffer(scrollbackBufSize),
				readers:    make(map[*websocket.Conn]struct{}),
			}
			sessions[sessionID] = sess
			sessionsMu.Unlock()

			sess.addReader(ws)
			defer sess.removeReader(ws)

			// Start the background PTY reader
			sess.startReader(sessionID)
		}

		// WebSocket -> PTY (runs until the WebSocket closes)
		for {
			msgType, msg, err := ws.ReadMessage()
			if err != nil {
				return nil
			}

			// Check for resize messages (JSON text)
			if msgType == websocket.TextMessage {
				var resize resizeMsg
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					sess.writeMu.Lock()
					pty.Setsize(sess.ptmx, &pty.Winsize{
						Cols: resize.Cols,
						Rows: resize.Rows,
					})
					sess.writeMu.Unlock()
					continue
				}
			}

			sess.writeMu.Lock()
			_, writeErr := sess.ptmx.Write(msg)
			sess.writeMu.Unlock()
			if writeErr != nil {
				return nil
			}

			sess.mu.Lock()
			sess.lastUse = time.Now()
			sess.mu.Unlock()
		}
	}
}

// CleanupTerminalSessions removes idle terminal sessions based on the
// terminal_timeout setting (in minutes). A value of 0 means never expire.
// The goroutine stops when ctx is cancelled (main.go wires this to the
// graceful shutdown signal).
func CleanupTerminalSessions(ctx context.Context, db *sql.DB) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			timeoutStr := settings.GetSetting(db, "terminal_timeout")
			timeoutMin, err := strconv.Atoi(timeoutStr)
			if err != nil || timeoutMin < 0 {
				timeoutMin = 30
			}
			if timeoutMin == 0 {
				continue // 0 = never expire
			}

			timeout := time.Duration(timeoutMin) * time.Minute
			// Collect expired sessions under lock, clean up outside lock
			type expired struct {
				id   string
				sess *terminalSession
			}
			var toClean []expired
			sessionsMu.Lock()
			for id, sess := range sessions {
				sess.mu.Lock()
				idle := time.Since(sess.lastUse) > timeout
				sess.mu.Unlock()
				if idle {
					delete(sessions, id)
					toClean = append(toClean, expired{id, sess})
				}
			}
			sessionsMu.Unlock()
			for _, e := range toClean {
				e.sess.ptmx.Close()
				if e.sess.cmd.Process != nil {
					e.sess.cmd.Process.Kill()
				}
				e.sess.cmd.Wait()
			}
		}
	}()
}
