package websocket

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/monitor"
)

// sameOriginOrEmpty allows WS upgrades from the same Host as the request
// (the panel UI in a normal browser) and from non-browser clients that omit
// the Origin header entirely (curl, websocat, the desktop wrapper). Anything
// else — a foreign Origin set by a malicious page — is rejected, defending
// against CSWSH even though the ?ticket=/?token= path doesn't ride cookies.
func sameOriginOrEmpty(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	// Compare host:port; gorilla normalizes Request.Host the same way.
	return strings.EqualFold(u.Host, r.Host)
}

var Upgrader = websocket.Upgrader{
	CheckOrigin: sameOriginOrEmpty,
}

// safeWSWriter wraps websocket.Conn with a mutex for concurrent write safety.
type safeWSWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *safeWSWriter) WriteMessage(messageType int, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A stalled client (TCP RWIN exhausted, flaky link) must not be able to
	// pin the writer goroutine forever. The 10s deadline matches WriteJSON.
	w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteMessage(messageType, data)
}

func (w *safeWSWriter) WriteJSON(v interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteJSON(v)
}

const (
	// wsReadDeadline gives the client wsPingInterval + slack to send a pong.
	// If we don't see one within this window the read goroutine errors,
	// which is what we want — it propagates to ctx.Cancel and tears the
	// session down. Without this, a half-open WS (router/NAT timeout, lid
	// closed) leaves the docker exec session and its goroutines alive.
	wsReadDeadline = 60 * time.Second
	wsPingInterval = 25 * time.Second
)

// startWSKeepalive arms a read deadline + pong handler on the WS and starts
// a goroutine that pings the client every wsPingInterval. The ping goroutine
// exits when ctx is cancelled (parent handler tearing down). Returns a
// no-op cleanup function for symmetry with defer patterns.
//
// The pattern is the standard gorilla/websocket keepalive recipe. We need
// it on every long-lived WS handler — without it, a half-open connection
// (browser tab crashes mid-flight, NAT entry expires) leaves the read
// goroutine parked indefinitely on ReadMessage and the entire session tree
// alive: docker exec process, log scanner, bridge goroutines.
func startWSKeepalive(ctx context.Context, ws *websocket.Conn, writer *safeWSWriter) {
	_ = ws.SetReadDeadline(time.Now().Add(wsReadDeadline))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(wsReadDeadline))
		return nil
	})
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writer.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()
}

// AuthenticateWS validates a WebSocket request via a single-use ticket
// (preferred — JWT never lands in the URL/access log) or, for back-compat
// with older JS clients, via the ?token= JWT path. Internal cluster proxy
// requests authenticated by mTLS bypass both. Returns nil on success.
func AuthenticateWS(c echo.Context, jwtSecret string) error {
	if auth.IsInternalProxyRequest(c.Request()) {
		return nil
	}
	if user := auth.AuthenticateWSRequest(c.Request(), jwtSecret); user != "" {
		return nil
	}
	return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

// MetricsWS handles WebSocket connections for real-time metrics streaming.
func MetricsWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := AuthenticateWS(c, jwtSecret); err != nil {
			return err
		}

		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		writer := &safeWSWriter{conn: ws}
		// Without keepalive a half-open WS (laptop lid closed, NAT entry
		// expired) leaves the reader goroutine parked on ReadMessage and
		// this loop pushing 2-second JSON payloads into a socket that
		// never drains until the TCP RTO catches up — minutes, not
		// seconds. The pong-driven read deadline kicks the reader so the
		// done channel closes and we tear down promptly.
		startWSKeepalive(ctx, ws, writer)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return nil
			case <-ticker.C:
				metrics, err := monitor.GetMetrics()
				if err != nil {
					continue
				}
				if err := writer.WriteJSON(metrics); err != nil {
					return nil
				}
			}
		}
	}
}

// ContainerLogsWS streams container logs over a WebSocket connection.
func ContainerLogsWS(dockerClient *docker.Client, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := AuthenticateWS(c, jwtSecret); err != nil {
			return err
		}

		containerID := c.Param("id")

		tail := c.QueryParam("tail")
		timestamps := c.QueryParam("timestamps") == "true"
		stream := c.QueryParam("stream")
		since := c.QueryParam("since")

		opts := docker.LogOptions{
			Tail:       tail,
			Timestamps: timestamps,
			Stream:     stream,
			Since:      since,
		}

		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		logReader, err := dockerClient.ContainerLogs(ctx, containerID, opts)
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}
		defer logReader.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					cancel()
					return
				}
			}
		}()

		writer := &safeWSWriter{conn: ws}
		startWSKeepalive(ctx, ws, writer)

		scanDone := make(chan struct{})
		go func() {
			defer close(scanDone)
			scanner := bufio.NewScanner(logReader)
			commonExec.PrepareScanner(scanner)
			for scanner.Scan() {
				select {
				case <-ctx.Done():
					return
				default:
				}
				line := scanner.Bytes()
				if len(line) > 8 && (line[0] == 1 || line[0] == 2) {
					line = line[8:]
				}
				line = append(line, '\n')
				if err := writer.WriteMessage(websocket.TextMessage, line); err != nil {
					return
				}
			}
		}()

		select {
		case <-done:
		case <-scanDone:
		}
		cancel()
		return nil
	}
}

// ComposeLogsWS streams compose project logs over a WebSocket connection.
func ComposeLogsWS(composeManager *docker.ComposeManager, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := AuthenticateWS(c, jwtSecret); err != nil {
			return err
		}

		project := c.Param("project")
		tail := 100
		if t := c.QueryParam("tail"); t != "" {
			if v, err := parseInt(t); err == nil && v > 0 {
				tail = v
			}
		}
		service := c.QueryParam("service")

		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					cancel()
					return
				}
			}
		}()

		writer := &safeWSWriter{conn: ws}

		streamDone := make(chan struct{})
		go func() {
			defer close(streamDone)
			err := composeManager.StreamLogs(ctx, project, tail, service, func(line string) {
				if writeErr := writer.WriteMessage(websocket.TextMessage, []byte(line+"\n")); writeErr != nil {
					cancel()
				}
			})
			if err != nil {
				writer.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()+"\n"))
			}
		}()

		select {
		case <-done:
		case <-streamDone:
		}
		cancel()
		return nil
	}
}

func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit character in integer: %c", c)
		}
		n = n*10 + int(c-'0')
		if n > 1000000 {
			return 0, fmt.Errorf("value too large")
		}
	}
	return n, nil
}

// ContainerExecWS creates an exec session in a container and bridges
// it over a WebSocket for interactive terminal access.
func ContainerExecWS(dockerClient *docker.Client, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := AuthenticateWS(c, jwtSecret); err != nil {
			return err
		}

		containerID := c.Param("id")

		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		hijacked, execID, err := dockerClient.ContainerExec(ctx, containerID, []string{"/bin/sh"})
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}
		defer hijacked.Close()

		writer := &safeWSWriter{conn: ws}
		startWSKeepalive(ctx, ws, writer)

		// Close hijacked connection when context is cancelled (from either goroutine)
		// This unblocks hijacked.Reader.Read() in the Docker reader goroutine
		go func() {
			<-ctx.Done()
			hijacked.Close()
		}()

		// Docker -> WS: read from exec session and forward to WebSocket
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer cancel() // signal the writer goroutine to stop
			buf := make([]byte, 4096)
			for {
				n, err := hijacked.Reader.Read(buf)
				if err != nil {
					return
				}
				if err := writer.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
					return
				}
			}
		}()

		// WS -> Docker: read from WebSocket and forward to exec session
		go func() {
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					cancel()
					return
				}
				var resizeMsg struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(msg, &resizeMsg) == nil && resizeMsg.Type == "resize" {
					// Defensive bounds check: negative values wrap to huge uint
					// in ExecResize; >65535 is larger than any real terminal.
					if resizeMsg.Cols > 0 && resizeMsg.Rows > 0 &&
						resizeMsg.Cols <= 65535 && resizeMsg.Rows <= 65535 {
						_ = dockerClient.ExecResize(ctx, execID, resizeMsg.Cols, resizeMsg.Rows)
					}
					continue
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
				if _, err := hijacked.Conn.Write(msg); err != nil {
					return
				}
			}
		}()

		<-done
		return nil
	}
}
