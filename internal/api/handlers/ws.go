package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/monitor"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow same-origin and configured origins
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // same-site requests
		}
		// In dev mode, allow localhost origins
		host := r.Host
		if host == "localhost:5173" || host == "localhost:8443" {
			return true
		}
		// Allow requests where origin host matches the request host
		if len(origin) > 7 {
			// Strip scheme (http:// or https://)
			for _, prefix := range []string{"https://", "http://"} {
				if len(origin) > len(prefix) && origin[:len(prefix)] == prefix {
					if origin[len(prefix):] == host {
						return true
					}
				}
			}
		}
		return true // fallback: allow for single-user panel
	},
}

// safeWSWriter wraps websocket.Conn with a mutex for concurrent write safety.
type safeWSWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *safeWSWriter) WriteMessage(messageType int, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(messageType, data)
}

func (w *safeWSWriter) WriteJSON(v interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteJSON(v)
}

// authenticateWS validates a WebSocket request via JWT token query param
// or internal cluster proxy header. Returns nil on success, error response on failure.
func authenticateWS(c echo.Context, jwtSecret string) error {
	// Trust cluster-internal relay requests
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

// MetricsWS handles WebSocket connections for real-time metrics streaming.
// Authentication is done via a "token" query parameter.
func MetricsWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := authenticateWS(c, jwtSecret); err != nil {
			return err
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()

		writer := &safeWSWriter{conn: ws}
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
		if err := authenticateWS(c, jwtSecret); err != nil {
			return err
		}

		containerID := c.Param("id")

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Use a cancellable context so the log reader goroutine stops
		// when the client disconnects.
		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		logReader, err := dockerClient.ContainerLogs(ctx, containerID)
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
					cancel() // cancel context to stop scanner goroutine
					return
				}
			}
		}()

		writer := &safeWSWriter{conn: ws}

		// Stream log lines to WebSocket.
		go func() {
			scanner := bufio.NewScanner(logReader)
			for scanner.Scan() {
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

		<-done
		return nil
	}
}

// ContainerExecWS creates an exec session in a container and bridges
// it over a WebSocket for interactive terminal access.
func ContainerExecWS(dockerClient *docker.Client, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := authenticateWS(c, jwtSecret); err != nil {
			return err
		}

		containerID := c.Param("id")

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx := c.Request().Context()
		hijacked, execID, err := dockerClient.ContainerExec(ctx, containerID, []string{"/bin/sh"})
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}
		defer hijacked.Close()

		writer := &safeWSWriter{conn: ws}

		// exec stdout -> WebSocket
		done := make(chan struct{})
		go func() {
			defer close(done)
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

		// WebSocket -> exec stdin (handle resize messages separately)
		go func() {
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					hijacked.Close()
					return
				}
				var resizeMsg struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(msg, &resizeMsg) == nil && resizeMsg.Type == "resize" {
					_ = dockerClient.ExecResize(c.Request().Context(), execID, resizeMsg.Cols, resizeMsg.Rows)
					continue
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
