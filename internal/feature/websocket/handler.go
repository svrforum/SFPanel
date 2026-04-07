package websocket

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

var Upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		host := r.Host
		if host == "localhost:5173" || host == "localhost:8443" {
			return true
		}
		if len(origin) > 7 {
			for _, prefix := range []string{"https://", "http://"} {
				if len(origin) > len(prefix) && origin[:len(prefix)] == prefix {
					if origin[len(prefix):] == host {
						return true
					}
				}
			}
		}
		return true
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

// AuthenticateWS validates a WebSocket request via JWT token query param
// or internal cluster proxy header. Returns nil on success, error response on failure.
func AuthenticateWS(c echo.Context, jwtSecret string) error {
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

		scanDone := make(chan struct{})
		go func() {
			defer close(scanDone)
			scanner := bufio.NewScanner(logReader)
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
			return 0, context.DeadlineExceeded
		}
		n = n*10 + int(c-'0')
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

		ctx := c.Request().Context()
		hijacked, execID, err := dockerClient.ContainerExec(ctx, containerID, []string{"/bin/sh"})
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}
		defer hijacked.Close()

		writer := &safeWSWriter{conn: ws}

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

		go func() {
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
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
