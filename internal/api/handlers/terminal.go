package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

const scrollbackBufSize = 256 * 1024 // 256 KB ring buffer per session

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
		if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
			delete(s.readers, ws)
		}
	}
	s.readersMu.Unlock()
}

// startReader spawns a goroutine that reads from the PTY and broadcasts
// to all connected WebSocket clients. It runs for the lifetime of the session.
func (s *terminalSession) startReader() {
	if s.started {
		return
	}
	s.started = true
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := s.ptmx.Read(buf)
			if err != nil {
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
		token := c.QueryParam("token")
		if token == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}
		if _, err := auth.ParseToken(token, jwtSecret); err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
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
			sess.lastUse = time.Now()

			// Replay scrollback buffer so the reconnected client sees history
			sess.mu.Lock()
			history := sess.scrollback.Bytes()
			sess.mu.Unlock()
			if len(history) > 0 {
				ws.WriteMessage(websocket.BinaryMessage, history)
			}

			// Register this WebSocket as a reader
			sess.addReader(ws)
			defer sess.removeReader(ws)
		} else {
			// Create new PTY session
			shell := findShell()
			cmd := exec.Command(shell)
			cmd.Env = append(os.Environ(),
				"TERM=xterm-256color",
				"LANG=en_US.UTF-8",
			)

			ptmx, err := pty.Start(cmd)
			if err != nil {
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

			sessionsMu.Lock()
			sessions[sessionID] = sess
			sessionsMu.Unlock()

			sess.addReader(ws)
			defer sess.removeReader(ws)

			// Start the background PTY reader
			sess.startReader()
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
					pty.Setsize(sess.ptmx, &pty.Winsize{
						Cols: resize.Cols,
						Rows: resize.Rows,
					})
					continue
				}
			}

			if _, err := sess.ptmx.Write(msg); err != nil {
				return nil
			}
		}
	}
}

// CleanupTerminalSessions removes idle terminal sessions based on the
// terminal_timeout setting (in minutes). A value of 0 means never expire.
func CleanupTerminalSessions(db *sql.DB) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			timeoutStr := GetSetting(db, "terminal_timeout")
			timeoutMin, err := strconv.Atoi(timeoutStr)
			if err != nil || timeoutMin < 0 {
				timeoutMin = 30
			}
			if timeoutMin == 0 {
				continue // 0 = never expire
			}

			timeout := time.Duration(timeoutMin) * time.Minute
			sessionsMu.Lock()
			for id, sess := range sessions {
				if time.Since(sess.lastUse) > timeout {
					sess.ptmx.Close()
					sess.cmd.Process.Kill()
					sess.cmd.Wait()
					delete(sessions, id)
				}
			}
			sessionsMu.Unlock()
		}
	}()
}
