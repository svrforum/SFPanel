package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/monitor"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// MetricsWS handles WebSocket connections for real-time metrics streaming.
// Authentication is done via a "token" query parameter.
func MetricsWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Validate JWT from query parameter
		token := c.QueryParam("token")
		if token == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}

		_, err := auth.ParseToken(token, jwtSecret)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		}

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Start a goroutine to read from the WebSocket (detect client disconnect)
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
				if err := ws.WriteJSON(metrics); err != nil {
					return nil // client disconnected
				}
			}
		}
	}
}

// ContainerLogsWS streams container logs over a WebSocket connection.
// Authentication is done via a "token" query parameter. The container
// ID is read from the :id path parameter.
func ContainerLogsWS(dockerClient *docker.Client, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Validate JWT from query parameter
		token := c.QueryParam("token")
		if token == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}
		if _, err := auth.ParseToken(token, jwtSecret); err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		}

		containerID := c.Param("id")

		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx := c.Request().Context()
		logReader, err := dockerClient.ContainerLogs(ctx, containerID)
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}
		defer logReader.Close()

		// Read goroutine to detect client disconnect
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()

		// Stream log lines to WebSocket.
		// Docker log stream has an 8-byte header per frame (for multiplexed
		// stdout/stderr). We strip the header and send each line with a
		// trailing newline so the terminal renders line breaks correctly.
		go func() {
			scanner := bufio.NewScanner(logReader)
			for scanner.Scan() {
				line := scanner.Bytes()
				// Docker multiplexed log lines have an 8-byte header.
				// If the line is longer than 8 bytes and starts with a
				// stream type byte (0x01 for stdout, 0x02 for stderr),
				// strip the header.
				if len(line) > 8 && (line[0] == 1 || line[0] == 2) {
					line = line[8:]
				}
				// Append newline so the terminal renders line breaks
				line = append(line, '\n')
				if err := ws.WriteMessage(websocket.TextMessage, line); err != nil {
					return
				}
			}
		}()

		<-done
		return nil
	}
}

// ContainerExecWS creates an exec session in a container and bridges
// it over a WebSocket for interactive terminal access. Authentication
// is done via a "token" query parameter.
func ContainerExecWS(dockerClient *docker.Client, jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Validate JWT from query parameter
		token := c.QueryParam("token")
		if token == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}
		if _, err := auth.ParseToken(token, jwtSecret); err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
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

		// exec stdout -> WebSocket (use TextMessage so browser receives string, not Blob)
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 4096)
			for {
				n, err := hijacked.Reader.Read(buf)
				if err != nil {
					return
				}
				if err := ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
					return
				}
			}
		}()

		// WebSocket -> exec stdin (handle resize messages separately)
		go func() {
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					// Close the exec stdin to signal EOF
					hijacked.Close()
					return
				}
				// Check if message is a resize command
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
