package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
	"github.com/sfpanel/sfpanel/internal/auth"
)

// logSourceInfo holds metadata about a known log source.
type logSourceInfo struct {
	Name string
	Path string
}

// logSources defines the allowed log sources and their filesystem paths.
// Only files present in this map can be read; arbitrary file access is
// prevented by validating the requested source key against this map.
var logSources = map[string]logSourceInfo{
	"syslog":       {Name: "System Log", Path: "/var/log/syslog"},
	"auth":         {Name: "Auth Log", Path: "/var/log/auth.log"},
	"kern":         {Name: "Kernel Log", Path: "/var/log/kern.log"},
	"nginx-access": {Name: "Nginx Access", Path: "/var/log/nginx/access.log"},
	"nginx-error":  {Name: "Nginx Error", Path: "/var/log/nginx/error.log"},
	"sfpanel":      {Name: "SFPanel", Path: "/var/log/sfpanel.log"},
	"dpkg":         {Name: "Package Manager", Path: "/var/log/dpkg.log"},
	"ufw":          {Name: "Firewall (UFW)", Path: "/var/log/ufw.log"},
}

// LogSource represents a single log source returned by ListSources.
type LogSource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Exists bool   `json:"exists"`
}

// LogOutput represents the response payload from ReadLog.
type LogOutput struct {
	Source     string   `json:"source"`
	Lines      []string `json:"lines"`
	TotalLines int      `json:"total_lines"`
}

// LogsHandler exposes REST handlers for viewing system and application logs.
type LogsHandler struct{}

// ListSources returns the list of known log sources along with their
// availability and file size on disk.
func (h *LogsHandler) ListSources(c echo.Context) error {
	sources := make([]LogSource, 0, len(logSources))

	for id, info := range logSources {
		src := LogSource{
			ID:   id,
			Name: info.Name,
			Path: info.Path,
		}

		fi, err := os.Stat(info.Path)
		if err == nil {
			src.Exists = true
			src.Size = fi.Size()
		}

		sources = append(sources, src)
	}

	return response.OK(c, sources)
}

// ReadLog reads the last N lines from the requested log source.
// Query parameters:
//   - source (required): one of the keys in logSources
//   - lines  (optional): number of lines to return (default 100, max 5000)
func (h *LogsHandler) ReadLog(c echo.Context) error {
	sourceKey := c.QueryParam("source")
	if sourceKey == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_SOURCE", "Query parameter 'source' is required")
	}

	info, ok := logSources[sourceKey]
	if !ok {
		return response.Fail(c, http.StatusBadRequest, "INVALID_SOURCE", fmt.Sprintf("Unknown log source: %s", sourceKey))
	}

	// Parse requested line count with sensible defaults.
	lines := 100
	if raw := c.QueryParam("lines"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return response.Fail(c, http.StatusBadRequest, "INVALID_LINES", "Parameter 'lines' must be a positive integer")
		}
		if n > 5000 {
			n = 5000
		}
		lines = n
	}

	// Ensure the file exists before attempting to read.
	if _, err := os.Stat(info.Path); os.IsNotExist(err) {
		return response.Fail(c, http.StatusNotFound, "LOG_NOT_FOUND", fmt.Sprintf("Log file does not exist: %s", info.Path))
	}

	// Use tail to efficiently read the last N lines.
	// #nosec G204 — info.Path is validated against the allowlist above.
	cmd := exec.CommandContext(c.Request().Context(), "tail", "-n", strconv.Itoa(lines), info.Path)
	output, err := cmd.Output()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "READ_ERROR", fmt.Sprintf("Failed to read log: %v", err))
	}

	// Split output into individual lines, trimming the trailing empty entry
	// that results from a final newline.
	raw := strings.TrimRight(string(output), "\n")
	var logLines []string
	if raw == "" {
		logLines = []string{}
	} else {
		logLines = strings.Split(raw, "\n")
	}

	return response.OK(c, LogOutput{
		Source:     sourceKey,
		Lines:      logLines,
		TotalLines: len(logLines),
	})
}

// LogStreamWS returns an echo.HandlerFunc that upgrades the connection to a
// WebSocket and streams new log lines in real-time using tail -f.
// Authentication is performed via a "token" query parameter containing a JWT.
//
// Query parameters:
//   - source (required): one of the keys in logSources
//   - token  (required): valid JWT
func LogStreamWS(jwtSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Validate JWT from query parameter.
		token := c.QueryParam("token")
		if token == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}
		if _, err := auth.ParseToken(token, jwtSecret); err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		}

		// Validate the requested log source.
		sourceKey := c.QueryParam("source")
		if sourceKey == "" {
			return c.String(http.StatusBadRequest, "missing source parameter")
		}
		info, ok := logSources[sourceKey]
		if !ok {
			return c.String(http.StatusBadRequest, fmt.Sprintf("unknown log source: %s", sourceKey))
		}
		if _, err := os.Stat(info.Path); os.IsNotExist(err) {
			return c.String(http.StatusNotFound, fmt.Sprintf("log file does not exist: %s", info.Path))
		}

		// Upgrade to WebSocket.
		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Start tail -f to follow the log file.
		// #nosec G204 — info.Path is validated against the allowlist above.
		cmd := exec.Command("tail", "-f", info.Path)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}

		if err := cmd.Start(); err != nil {
			ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
			return nil
		}

		// Ensure the tail process is killed when the handler exits.
		defer func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}()

		// Read goroutine to detect client disconnect.
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()

		// Stream new log lines to the WebSocket client.
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				if err := ws.WriteMessage(websocket.TextMessage, scanner.Bytes()); err != nil {
					return
				}
			}
		}()

		// Block until the client disconnects.
		<-done
		return nil
	}
}
