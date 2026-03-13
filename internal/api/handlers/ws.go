package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/monitor"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}

		originURL, err := url.Parse(origin)
		if err != nil {
			return false
		}

		if originURL.Host == r.Host {
			return true
		}

		// Development exceptions for local frontend/backend pairs.
		if (originURL.Host == "localhost:5173" || originURL.Host == "127.0.0.1:5173") &&
			(r.Host == "localhost:8443" || r.Host == "127.0.0.1:8443") {
			return true
		}

		return false
	},
}

const wsAuthProtocolPrefix = "sfpanel.jwt."

func authenticateWebSocketRequest(r *http.Request, jwtSecret string) (*auth.Claims, string, error) {
	header := r.Header.Get("Authorization")
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return nil, "", errors.New("invalid authorization header")
		}
		claims, err := auth.ParseToken(parts[1], jwtSecret)
		if err != nil {
			return nil, "", err
		}
		return claims, "", nil
	}

	for _, raw := range websocket.Subprotocols(r) {
		if strings.HasPrefix(raw, wsAuthProtocolPrefix) {
			token := strings.TrimPrefix(raw, wsAuthProtocolPrefix)
			claims, err := auth.ParseToken(token, jwtSecret)
			if err != nil {
				return nil, "", err
			}
			return claims, raw, nil
		}
	}

	return nil, "", errors.New("missing token")
}

func upgradeAuthorizedWebSocket(c echo.Context, protocol string) (*websocket.Conn, error) {
	if protocol == "" {
		return upgrader.Upgrade(c.Response(), c.Request(), nil)
	}
	headers := http.Header{}
	headers.Set("Sec-WebSocket-Protocol", protocol)
	return upgrader.Upgrade(c.Response(), c.Request(), headers)
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

// MetricsWS handles WebSocket connections for real-time metrics streaming.
// Authentication is done via Authorization header or WebSocket subprotocol.
func MetricsWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		_, protocol, err := authenticateWebSocketRequest(c.Request(), jwtSecret)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}
		ws, err := upgradeAuthorizedWebSocket(c, protocol)
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
		_, protocol, err := authenticateWebSocketRequest(c.Request(), jwtSecret)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or missing token"})
		}

		containerID := c.Param("id")

		ws, err := upgradeAuthorizedWebSocket(c, protocol)
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
		_, protocol, err := authenticateWebSocketRequest(c.Request(), jwtSecret)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or missing token"})
		}

		containerID := c.Param("id")

		ws, err := upgradeAuthorizedWebSocket(c, protocol)
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
