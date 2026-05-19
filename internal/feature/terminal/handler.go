package terminal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/safe"
)

const scrollbackBufSize = 256 * 1024 // 256 KB ring buffer per session
const maxTerminalSessions = 20       // Maximum concurrent terminal sessions
// readerSendQueue bounds the per-reader buffer. With typical PTY payloads
// of 1–4 KB the queue holds ~64–256 KB of in-flight output before broadcast
// declares the client too slow and tears it down — well above the 10s
// SetWriteDeadline window so a transient stall doesn't trigger a kick,
// well below the point at which one slow client could starve the host.
const readerSendQueue = 64

// sameOriginOrEmpty mirrors websocket/handler.go's CheckOrigin: accept
// when Origin is absent (curl/websocat/desktop wrapper) or its host
// matches the request host. Anything else is a foreign Origin from a
// CSWSH attempt and the upgrade is refused.
func sameOriginOrEmpty(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

var Upgrader = websocket.Upgrader{
	CheckOrigin: sameOriginOrEmpty,
}

func authenticateWS(c echo.Context, jwtSecret string) error {
	if auth.IsInternalProxyRequest(c.Request()) {
		return nil
	}
	if user := auth.AuthenticateWSRequest(c.Request(), jwtSecret); user != "" {
		return nil
	}
	return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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
	// readers maps each connected WebSocket to its per-reader send state
	// so broadcast can fan-out output without holding up the PTY reader
	// on any one slow client.
	readers   map[*websocket.Conn]*readerState
	readersMu sync.Mutex
	startOnce sync.Once // ensures the PTY-reader goroutine starts exactly once
}

// readerState wires one WebSocket to a bounded send queue drained by a
// per-reader writer goroutine. broadcast pushes a (copied) payload onto
// send with a non-blocking select; on overflow it closes done, which the
// writer's select arm picks up and tears the connection down. The PTY
// reader therefore never blocks behind a stalled client, and the slow
// client is dropped rather than head-of-line-blocking the session.
type readerState struct {
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func (rs *readerState) kick() {
	rs.closeOnce.Do(func() { close(rs.done) })
}

func (s *terminalSession) addReader(ws *websocket.Conn) *readerState {
	state := &readerState{
		send: make(chan []byte, readerSendQueue),
		done: make(chan struct{}),
	}
	s.readersMu.Lock()
	s.readers[ws] = state
	s.readersMu.Unlock()
	return state
}

func (s *terminalSession) removeReader(ws *websocket.Conn) {
	s.readersMu.Lock()
	if state, ok := s.readers[ws]; ok {
		state.kick()
		delete(s.readers, ws)
	}
	s.readersMu.Unlock()
}

// writeLoop drains the per-reader send queue and writes to the WebSocket.
// Exits on done close (broadcast declared the client too slow), on
// channel close, or on WriteMessage error (transport gone). On any exit
// the connection is closed and the readers-map entry is removed.
func (s *terminalSession) writeLoop(ws *websocket.Conn, state *readerState) {
	defer func() {
		_ = ws.Close()
		s.readersMu.Lock()
		if cur, ok := s.readers[ws]; ok && cur == state {
			delete(s.readers, ws)
		}
		s.readersMu.Unlock()
	}()
	for {
		select {
		case <-state.done:
			return
		case data, ok := <-state.send:
			if !ok {
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
	}
}

// broadcast records output in scrollback and enqueues it for every
// connected reader. A non-blocking send keeps the PTY reader off the
// critical path of any individual writer goroutine; overflow on a
// reader's queue means the client is so slow it has fallen >=64 frames
// behind, at which point we kick it.
func (s *terminalSession) broadcast(data []byte) {
	s.mu.Lock()
	s.scrollback.Write(data)
	s.mu.Unlock()

	// data is the PTY read buffer reused on the next iteration; the
	// writer goroutine will hand the same slice to ws.WriteMessage on a
	// different goroutine, so we must copy before enqueuing.
	payload := make([]byte, len(data))
	copy(payload, data)

	s.readersMu.Lock()
	for _, state := range s.readers {
		select {
		case state.send <- payload:
		default:
			state.kick()
		}
	}
	s.readersMu.Unlock()
}

// writeToReader enqueues data into one specific reader's queue (used by
// scrollback replay on reconnect). Same non-blocking semantics as
// broadcast — if the new reader can't drain even the historical buffer
// we treat them as too slow and disconnect rather than stalling here.
func (s *terminalSession) writeToReader(ws *websocket.Conn, data []byte) {
	s.readersMu.Lock()
	state, ok := s.readers[ws]
	s.readersMu.Unlock()
	if !ok {
		return
	}
	payload := make([]byte, len(data))
	copy(payload, data)
	select {
	case state.send <- payload:
	default:
		state.kick()
	}
}

// startReader spawns the PTY-reader goroutine exactly once per session.
// Two concurrent reconnections to the same sessionID used to both pass
// the unsynchronized `started` flag check and both start a reader,
// double-consuming PTY output and corrupting the session.
func (s *terminalSession) startReader(sessionID string) {
	s.startOnce.Do(func() {
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
					// Kick any remaining readers so their writer
					// goroutines stop blocking on the empty queue.
					s.readersMu.Lock()
					for _, state := range s.readers {
						state.kick()
					}
					s.readersMu.Unlock()
					return
				}
				s.broadcast(buf[:n])
			}
		}()
	})
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

// terminalHome resolves the directory the PTY session should chdir into and
// expose as HOME. Previously this was hardcoded to "/root", which broke
// installs where sfpanel runs under a non-root systemd unit (the chdir
// fails and the shell exits immediately with a cryptic error). We prefer
// the calling process's HOME (set by systemd via User= or by the operator's
// shell), then fall back to os.UserHomeDir(), then /tmp as a last resort
// so the PTY at least starts somewhere writable.
func terminalHome() string {
	if h := os.Getenv("HOME"); h != "" {
		if _, err := os.Stat(h); err == nil {
			return h
		}
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		if _, err := os.Stat(h); err == nil {
			return h
		}
	}
	return "/tmp"
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
			state := sess.addReader(ws)
			defer sess.removeReader(ws)
			go sess.writeLoop(ws, state)

			// Replay scrollback buffer so the reconnected client sees history.
			sess.mu.Lock()
			history := sess.scrollback.Bytes()
			sess.mu.Unlock()
			if len(history) > 0 {
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
			home := terminalHome()
			cmd.Dir = home
			cmd.Env = append(os.Environ(),
				"TERM=xterm-256color",
				"LANG=ko_KR.UTF-8",
				"LC_ALL=ko_KR.UTF-8",
				"HOME="+home,
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
				readers:    make(map[*websocket.Conn]*readerState),
			}
			sessions[sessionID] = sess
			sessionsMu.Unlock()

			state := sess.addReader(ws)
			defer sess.removeReader(ws)
			go sess.writeLoop(ws, state)

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
	safe.Go("terminal-cleanup", func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			// Read terminal_timeout directly from the settings table instead of
			// importing the settings feature module (which would be a
			// feature → feature dependency). Missing row is not an error;
			// Scan returns sql.ErrNoRows and we fall back to the default.
			var timeoutStr string
			_ = db.QueryRow("SELECT value FROM settings WHERE key = ?", "terminal_timeout").Scan(&timeoutStr)
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
	})
}
