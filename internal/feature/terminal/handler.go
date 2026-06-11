package terminal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/safe"
)

// errUnauthenticatedWS is returned by authenticateWS when the upgrade should
// be refused. The 401 response is already written; the caller need only
// propagate err so Echo treats the request as terminal. Without this sentinel
// the previous "return c.JSON(401, …)" form returned nil on a successful
// header write, leaving the caller to proceed with username == "" and
// reopening the per-user binding hijack that authenticateWS exists to close.
var errUnauthenticatedWS = errors.New("terminal: WebSocket not authenticated")

const scrollbackBufSize = 256 * 1024 // 256 KB ring buffer per session
const maxTerminalSessions = 20       // Maximum concurrent terminal sessions
// readerSendQueue bounds the per-reader buffer. With typical PTY payloads
// of 1–4 KB the queue holds ~64–256 KB of in-flight output before broadcast
// declares the client too slow and tears it down — well above the 10s
// SetWriteDeadline window so a transient stall doesn't trigger a kick,
// well below the point at which one slow client could starve the host.
const readerSendQueue = 64

// idleReapAfter is how long a session sits with zero readers before the
// cleanup goroutine reaps it, regardless of the operator-configured
// terminal_timeout. Without this guard a user who closes a browser tab on
// a long-running command (tail -f, apt upgrade) keeps the PTY alive for
// the full terminal_timeout window (default 30m) with no observer.
const idleReapAfter = 5 * time.Minute

// terminalWSPingInterval / terminalWSReadDeadline detect a half-open client
// (laptop sleep, NAT/router drop) on the WS->PTY input loop, which otherwise
// parks on ReadMessage until TCP RTO fires (minutes). The reader arms a read
// deadline and re-arms it on every pong; writeLoop pings on the interval. The
// deadline must exceed the interval so a live-but-idle client always pongs in
// time (gorilla auto-replies to pings).
const terminalWSPingInterval = 30 * time.Second
const terminalWSReadDeadline = 70 * time.Second

// tauriOrigins are the desktop wrapper's webview origins — the same three
// the CORS allowlist in router.go carries. Keys are lowercase. Webviews DO
// stamp an Origin on WS upgrades, so the empty-Origin allowance alone does
// not cover the desktop app.
var tauriOrigins = map[string]bool{
	"tauri://localhost":       true,
	"http://tauri.localhost":  true,
	"https://tauri.localhost": true,
}

// sameOriginOrEmpty mirrors websocket/handler.go's CheckOrigin: accept
// when Origin is absent (curl/websocat), matches the request host, or is
// a Tauri desktop webview origin. Anything else is a foreign Origin from
// a CSWSH attempt and the upgrade is refused.
func sameOriginOrEmpty(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if tauriOrigins[strings.ToLower(origin)] {
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

// authenticateWS verifies the WebSocket upgrade and returns the authenticated
// username so the caller can bind per-user state (e.g. PTY session key).
//
// For direct requests the username comes from the ticket/JWT verified by
// auth.AuthenticateWSRequest. For cluster-internal forwards (gated by the
// HMAC-validated X-SFPanel-Internal-Proxy headers) the originating node has
// already authenticated the operator and stamps the verified username into
// X-SFPanel-Original-User; the proxy middleware strips any caller-supplied
// copy of that header before re-setting it from the JWT-derived username, so
// it is authoritative here.
func authenticateWS(c echo.Context, jwtSecret string) (string, error) {
	if auth.IsInternalProxyRequest(c.Request()) {
		// Defence-in-depth: the proxy middleware already strips and rewrites
		// X-SFPanel-Original-User from the JWT-derived username, so this
		// header should never be empty here. If it ever is, refuse rather
		// than letting buildSessionKey("", id) yield a key that collides
		// across every empty-username request — the exact hijack defect
		// this binding was added to close. We can't fall back to "admin"
		// the way the JWT middleware does for non-terminal handlers,
		// because that would shadow a real admin's PTY sessions.
		user := c.Request().Header.Get("X-SFPanel-Original-User")
		if user == "" {
			_ = c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return "", errUnauthenticatedWS
		}
		return user, nil
	}
	if user := auth.AuthenticateWSRequest(c.Request(), jwtSecret); user != "" {
		return user, nil
	}
	// c.JSON returns nil on a successful write — propagating that would
	// leave the caller's err-check passing and the handler proceeding
	// with an empty username. Always return a non-nil error so empty
	// username can never reach buildSessionKey.
	_ = c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return "", errUnauthenticatedWS
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
	// The byte-by-byte loop was O(len(p)) function calls + branches.
	// PTY reads land in 4 KB chunks at minimum and broadcast pushes the
	// same chunk into the scrollback for every payload; on a noisy app
	// (tail -f, build log) this was a measurable CPU hotspot. Bulk-copy
	// handles the common case (payload fits in the remaining tail) with
	// one builtin copy(), and the wraparound case with at most two.
	n := len(p)
	cap := len(r.buf)
	if cap == 0 {
		return
	}
	// If incoming data is bigger than the whole ring, only the last
	// `cap` bytes can ever survive — skip the prefix.
	if n >= cap {
		copy(r.buf, p[n-cap:])
		r.pos = 0
		r.full = true
		return
	}
	tail := cap - r.pos
	if n <= tail {
		copy(r.buf[r.pos:], p)
		r.pos += n
		if r.pos == cap {
			r.pos = 0
			r.full = true
		}
		return
	}
	copy(r.buf[r.pos:], p[:tail])
	copy(r.buf, p[tail:])
	r.pos = n - tail
	r.full = true
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
	mu               sync.Mutex
	ptmx             *os.File
	cmd              *exec.Cmd
	lastUse          time.Time
	lastReaderLeftAt time.Time // set when the last reader leaves; zero while readers attached
	scrollback       *ringBuffer
	writeMu          sync.Mutex // protects ptmx.Write and pty.Setsize (both write to the PTY fd)
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

	// Clear the idle stamp — a new reader is attached so the "no readers"
	// reaper branch doesn't apply.
	s.mu.Lock()
	s.lastReaderLeftAt = time.Time{}
	s.mu.Unlock()

	return state
}

func (s *terminalSession) removeReader(ws *websocket.Conn) {
	s.readersMu.Lock()
	if state, ok := s.readers[ws]; ok {
		state.kick()
		delete(s.readers, ws)
	}
	empty := len(s.readers) == 0
	s.readersMu.Unlock()

	if empty {
		s.mu.Lock()
		s.lastReaderLeftAt = time.Now()
		s.mu.Unlock()
	}
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
		empty := len(s.readers) == 0
		s.readersMu.Unlock()
		if empty {
			s.mu.Lock()
			s.lastReaderLeftAt = time.Now()
			s.mu.Unlock()
		}
	}()
	ticker := time.NewTicker(terminalWSPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-state.done:
			return
		case <-ticker.C:
			// writeLoop owns all writes to ws, so the keepalive ping is sent
			// here rather than from a separate goroutine (gorilla conns are not
			// safe for concurrent writes).
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
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
// Two concurrent reconnections to the same session key used to both pass
// the unsynchronized `started` flag check and both start a reader,
// double-consuming PTY output and corrupting the session.
func (s *terminalSession) startReader(sessionKey string) {
	s.startOnce.Do(func() {
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := s.ptmx.Read(buf)
				if err != nil {
					// PTY closed (shell exited) — clean up session
					sessionsMu.Lock()
					if sessions[sessionKey] == s {
						s.ptmx.Close()
						if s.cmd.Process != nil {
							s.cmd.Process.Kill()
						}
						s.cmd.Wait()
						delete(sessions, sessionKey)
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

// SessionInfo describes a live PTY session for the reattach picker.
type SessionInfo struct {
	SessionID   string    `json:"session_id"`
	LastUse     time.Time `json:"last_use"`
	Attached    bool      `json:"attached"`
	ReaderCount int       `json:"reader_count"`
}

// listSessionsFor returns the live PTY sessions owned by username. The trailing
// NUL in the key prefix prevents one operator's name from matching another's
// as a substring (e.g. "admin" vs "admin2").
func listSessionsFor(username string) []SessionInfo {
	prefix := username + "\x00"
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	out := make([]SessionInfo, 0)
	for key, s := range sessions {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		s.readersMu.Lock()
		rc := len(s.readers)
		s.readersMu.Unlock()
		s.mu.Lock()
		last := s.lastUse
		s.mu.Unlock()
		out = append(out, SessionInfo{
			SessionID:   key[len(prefix):],
			LastUse:     last,
			Attached:    rc > 0,
			ReaderCount: rc,
		})
	}
	return out
}

// ListSessions returns the caller's live PTY sessions so the UI can offer a
// reattach picker instead of always minting a fresh session_id — which would
// otherwise strand the preserved shell and its scrollback. The server keeps
// sessions alive across disconnects; without this the operator had no way to
// discover and re-enter them.
func ListSessions(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrMissingToken, "authentication required")
	}
	return response.OK(c, map[string]interface{}{"sessions": listSessionsFor(username)})
}

// TerminalWS creates a new PTY session or reconnects to an existing one
// and bridges it over a WebSocket. Authentication via query param token.
// Query param session_id identifies the session; on reconnect the scrollback
// buffer is replayed so the user sees previous output.
func TerminalWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		username, err := authenticateWS(c, jwtSecret)
		if err != nil {
			return err
		}

		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		sessionKey := buildSessionKey(username, c.QueryParam("session_id"))

		// Audit the session open. Log the original operator-facing session_id
		// (not the composed key, which embeds a NUL byte) so the username
		// alone is the actor identifier.
		slog.Info("terminal session opened",
			"component", "terminal",
			"user", username,
			"session_id", c.QueryParam("session_id"))

		sessionsMu.Lock()
		sess, exists := sessions[sessionKey]
		if exists {
			// Check if the process is still alive
			if sess.cmd.ProcessState != nil {
				sess.ptmx.Close()
				delete(sessions, sessionKey)
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
			sessions[sessionKey] = sess
			sessionsMu.Unlock()

			state := sess.addReader(ws)
			defer sess.removeReader(ws)
			go sess.writeLoop(ws, state)

			// Start the background PTY reader
			sess.startReader(sessionKey)
		}

		// Arm a read deadline so a half-open client is detected within
		// ~terminalWSReadDeadline; writeLoop pings and the pong re-arms it.
		_ = ws.SetReadDeadline(time.Now().Add(terminalWSReadDeadline))
		ws.SetPongHandler(func(string) error {
			_ = ws.SetReadDeadline(time.Now().Add(terminalWSReadDeadline))
			return nil
		})

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

// isReadyForReap reports whether the session should be cleaned up at `now`.
// If there are zero readers attached, the session is reaped after
// idleReapAfter regardless of the per-input timeout. If readers are
// attached, the legacy lastUse + perInputTimeout semantics apply.
//
// The zero-readers branch fires even when perInputTimeout == 0 (operator
// set terminal_timeout=0 for never-expire). That's intentional: a tab
// close on a long-running command must not be allowed to leak a PTY
// forever just because the operator opted out of the per-input timeout.
//
// The caller must hold no locks on sess — this helper takes them itself.
func isReadyForReap(sess *terminalSession, now time.Time, perInputTimeout time.Duration) bool {
	sess.readersMu.Lock()
	readerCount := len(sess.readers)
	sess.readersMu.Unlock()

	sess.mu.Lock()
	lastUse := sess.lastUse
	lastLeft := sess.lastReaderLeftAt
	sess.mu.Unlock()

	if readerCount == 0 {
		return !lastLeft.IsZero() && now.Sub(lastLeft) > idleReapAfter
	}
	if perInputTimeout == 0 {
		return false // 0 = never expire
	}
	return now.Sub(lastUse) > perInputTimeout
}

// CleanupTerminalSessions removes idle terminal sessions based on the
// terminal_timeout setting (in minutes). A value of 0 means never expire
// for the per-input branch; sessions with zero readers are still reaped
// after idleReapAfter (see isReadyForReap).
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

			// timeout = 0 means "never expire" for the per-input branch.
			// The zero-readers branch inside isReadyForReap still applies,
			// so this pass continues rather than `continue`-ing.
			timeout := time.Duration(timeoutMin) * time.Minute

			// Collect expired sessions under lock, clean up outside lock
			type expired struct {
				id   string
				sess *terminalSession
			}
			var toClean []expired
			now := time.Now()
			sessionsMu.Lock()
			for id, sess := range sessions {
				if isReadyForReap(sess, now, timeout) {
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
