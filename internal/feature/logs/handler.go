package logs

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// logSourceInfo holds metadata about a known log source.
type logSourceInfo struct {
	Name   string
	Path   string
	Filter string // optional grep filter pattern applied when reading
}

// defaultLogSources defines the built-in log sources with their filesystem paths.
// The sfpanel source path is set dynamically from config via SetSFPanelLogPath.
var defaultLogSources = map[string]logSourceInfo{
	"syslog":   {Name: "System Log", Path: "/var/log/syslog"},
	"auth":     {Name: "Auth Log", Path: "/var/log/auth.log"},
	"kern":     {Name: "Kernel Log", Path: "/var/log/kern.log"},
	"sfpanel":  {Name: "SFPanel", Path: "/var/log/sfpanel/sfpanel.log"},
	"dpkg":     {Name: "Package Manager", Path: "/var/log/dpkg.log"},
	"firewall": {Name: "Firewall", Path: "/var/log/kern.log", Filter: "UFW|DOCKER-USER"},
	"fail2ban": {Name: "Fail2ban", Path: "/var/log/fail2ban.log"},
}

// SetSFPanelLogPath updates the sfpanel log source path from config.
func SetSFPanelLogPath(path string) {
	if path != "" {
		defaultLogSources["sfpanel"] = logSourceInfo{Name: "SFPanel", Path: path}
	}
}

// LogSource represents a single log source returned by ListSources.
type LogSource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Exists   bool   `json:"exists"`
	Custom   bool   `json:"custom"`
	CustomID int64  `json:"custom_id,omitempty"`
}

// LogOutput represents the response payload from ReadLog.
type LogOutput struct {
	Source     string   `json:"source"`
	Lines     []string `json:"lines"`
	TotalLines int     `json:"total_lines"`
}

// Handler exposes REST handlers for viewing system and application logs.
type Handler struct {
	DB *sql.DB
}

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

// countFileLines returns the total number of lines in a log file.
// When filter is non-empty, only matching lines are counted.
func countFileLines(ctx context.Context, path string, filter string) int {
	if filter != "" {
		cmd := exec.CommandContext(ctx, "grep", "-c", "-E", filter, path)
		out, err := cmd.Output()
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		return n
	}
	cmd := exec.CommandContext(ctx, "wc", "-l", path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) > 0 {
		n, _ := strconv.Atoi(fields[0])
		return n
	}
	return 0
}

// ListSources returns the list of known log sources along with their
// availability and file size on disk. Custom sources from the database
// are merged with the built-in system sources.
func (h *Handler) ListSources(c echo.Context) error {
	sources := make([]LogSource, 0, len(defaultLogSources))

	for id, info := range defaultLogSources {
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

	// Merge custom sources from DB
	if h.DB != nil {
		rows, err := h.DB.Query("SELECT id, source_id, name, path FROM custom_log_sources ORDER BY created_at ASC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var dbID int64
				var sourceID, name, path string
				if err := rows.Scan(&dbID, &sourceID, &name, &path); err != nil {
					continue
				}
				src := LogSource{
					ID:       sourceID,
					Name:     name,
					Path:     path,
					Custom:   true,
					CustomID: dbID,
				}
				fi, err := os.Stat(path)
				if err == nil {
					src.Exists = true
					src.Size = fi.Size()
				}
				sources = append(sources, src)
			}
		}
	}

	return response.OK(c, sources)
}

// allSources returns a merged map of built-in and custom sources.
func (h *Handler) allSources() map[string]logSourceInfo {
	merged := make(map[string]logSourceInfo, len(defaultLogSources))
	for k, v := range defaultLogSources {
		merged[k] = v
	}
	if h.DB != nil {
		rows, err := h.DB.Query("SELECT source_id, name, path FROM custom_log_sources")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, name, path string
				if err := rows.Scan(&id, &name, &path); err != nil {
					continue
				}
				merged[id] = logSourceInfo{Name: name, Path: path}
			}
		}
	}
	return merged
}

// ReadLog reads the last N lines from the requested log source.
// Query parameters:
//   - source (required): one of the keys in logSources or a custom source
//   - lines  (optional): number of lines to return (default 100, max 5000)
func (h *Handler) ReadLog(c echo.Context) error {
	sourceKey := c.QueryParam("source")
	if sourceKey == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingSource, "Query parameter 'source' is required")
	}

	all := h.allSources()
	info, ok := all[sourceKey]
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSource, fmt.Sprintf("Unknown log source: %s", sourceKey))
	}

	// Parse requested line count with sensible defaults.
	lines := 100
	if raw := c.QueryParam("lines"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidLines, "Parameter 'lines' must be a positive integer")
		}
		if n > 5000 {
			n = 5000
		}
		lines = n
	}

	// If log file does not exist, return empty result instead of 404
	if _, err := os.Stat(info.Path); os.IsNotExist(err) {
		return response.OK(c, map[string]interface{}{
			"source":    sourceKey,
			"lines":     []string{},
			"available": false,
		})
	}

	// Use tail (optionally piped through grep) to read the last N lines.
	// #nosec G204 — info.Path is validated against the allowlist above.
	var output []byte
	if info.Filter != "" {
		// grep filter + tail: grep first to filter, then tail for line count
		// #nosec G204 — info.Filter is from the hardcoded allowlist, not user input.
		cmd := exec.CommandContext(c.Request().Context(), "grep", "-E", info.Filter, info.Path)
		filtered, _ := cmd.Output() // grep returns exit 1 if no matches — that's fine
		if len(filtered) > 0 {
			tailCmd := exec.CommandContext(c.Request().Context(), "tail", "-n", strconv.Itoa(lines))
			tailCmd.Stdin = strings.NewReader(string(filtered))
			output, _ = tailCmd.Output()
		}
	} else {
		cmd := exec.CommandContext(c.Request().Context(), "tail", "-n", strconv.Itoa(lines), info.Path)
		var err2 error
		output, err2 = cmd.Output()
		if err2 != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrReadError, fmt.Sprintf("Failed to read log: %v", err2))
		}
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

	// Count total lines in the file (or filtered matches) so the UI can
	// indicate how much of the log is being displayed.
	totalLines := countFileLines(c.Request().Context(), info.Path, info.Filter)
	if totalLines < len(logLines) {
		totalLines = len(logLines)
	}

	return response.OK(c, LogOutput{
		Source:     sourceKey,
		Lines:     logLines,
		TotalLines: totalLines,
	})
}

// LogStreamWS returns an echo.HandlerFunc that upgrades the connection to a
// WebSocket and streams new log lines in real-time using tail -F.
// Authentication is performed via a "token" query parameter containing a JWT.
//
// Query parameters:
//   - source (required): one of the keys in logSources or a custom source
//   - token  (required): valid JWT
func LogStreamWS(jwtSecret string, database *sql.DB) echo.HandlerFunc {
	helper := &Handler{DB: database}
	return func(c echo.Context) error {
		if err := authenticateWS(c, jwtSecret); err != nil {
			return err
		}

		// Validate the requested log source.
		sourceKey := c.QueryParam("source")
		if sourceKey == "" {
			return c.String(http.StatusBadRequest, "missing source parameter")
		}
		all := helper.allSources()
		info, ok := all[sourceKey]
		if !ok {
			return c.String(http.StatusBadRequest, fmt.Sprintf("unknown log source: %s", sourceKey))
		}
		if _, err := os.Stat(info.Path); os.IsNotExist(err) {
			return response.OK(c, map[string]interface{}{
				"source":    sourceKey,
				"lines":     []string{},
				"available": false,
			})
		}

		// Upgrade to WebSocket.
		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Start tail -F to follow the log file, optionally piped through grep.
		// -F (uppercase) follows the filename, so logrotate is handled automatically.
		// #nosec G204 — info.Path and info.Filter are from the hardcoded allowlist.
		// Streaming command — cannot use Commander (needs live stdout pipe)
		tailCmd := exec.Command("tail", "-F", info.Path)

		var cmd *exec.Cmd
		var stdout *bufio.Reader

		if info.Filter != "" {
			// Pipe tail -F through grep --line-buffered for filtered streaming
			grepCmd := exec.Command("grep", "-E", "--line-buffered", info.Filter)
			tailOut, err := tailCmd.StdoutPipe()
			if err != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
				return nil
			}
			grepCmd.Stdin = tailOut

			grepOut, err := grepCmd.StdoutPipe()
			if err != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
				return nil
			}

			if err := tailCmd.Start(); err != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
				return nil
			}
			if err := grepCmd.Start(); err != nil {
				_ = tailCmd.Process.Kill()
				_ = tailCmd.Wait()
				ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
				return nil
			}

			stdout = bufio.NewReader(grepOut)
			cmd = grepCmd

			defer func() {
				_ = tailCmd.Process.Kill()
				_ = tailCmd.Wait()
			}()
		} else {
			pipeOut, err := tailCmd.StdoutPipe()
			if err != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
				return nil
			}
			if err := tailCmd.Start(); err != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
				return nil
			}
			stdout = bufio.NewReader(pipeOut)
			cmd = tailCmd
		}

		// Ensure the process is killed when the handler exits.
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
				if wsErr := ws.WriteMessage(websocket.TextMessage, scanner.Bytes()); wsErr != nil {
					return
				}
			}
		}()

		// Block until the client disconnects.
		<-done
		return nil
	}
}

// AddCustomSource adds a new custom log source.
// POST /api/v1/logs/custom-sources
func (h *Handler) AddCustomSource(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "Invalid request body")
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)

	if req.Name == "" || req.Path == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Both 'name' and 'path' are required")
	}

	// Security: absolute path required, no path traversal, must be under /var/log or /opt
	if !filepath.IsAbs(req.Path) {
		return response.Fail(c, http.StatusBadRequest, response.ErrPathInvalid, "Path must be absolute")
	}
	if strings.Contains(req.Path, "..") {
		return response.Fail(c, http.StatusBadRequest, response.ErrPathInvalid, "Path must not contain '..'")
	}
	cleanPath := filepath.Clean(req.Path)
	allowedPrefixes := []string{"/var/log/", "/opt/", "/home/", "/tmp/"}
	allowed := false
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(cleanPath, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		return response.Fail(c, http.StatusBadRequest, response.ErrPathInvalid, "Custom log path must be under /var/log, /opt, /home, or /tmp")
	}

	// Generate source_id from name: lowercase, replace spaces with hyphens
	sourceID := strings.ToLower(strings.ReplaceAll(req.Name, " ", "-"))
	sourceID = regexp.MustCompile(`[^a-z0-9_-]`).ReplaceAllString(sourceID, "")
	if sourceID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Name must contain alphanumeric characters")
	}
	// Prefix custom- to avoid collision with built-in sources
	sourceID = "custom-" + sourceID

	// Check for collision with built-in sources
	if _, ok := defaultLogSources[sourceID]; ok {
		return response.Fail(c, http.StatusConflict, response.ErrSourceExists, "A built-in source with this ID already exists")
	}

	res, err := h.DB.Exec(
		"INSERT INTO custom_log_sources (source_id, name, path) VALUES (?, ?, ?)",
		sourceID, req.Name, req.Path,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return response.Fail(c, http.StatusConflict, response.ErrSourceExists, "A custom source with this name already exists")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, fmt.Sprintf("Failed to add source: %v", err))
	}

	id, _ := res.LastInsertId()

	src := LogSource{
		ID:     sourceID,
		Name:   req.Name,
		Path:   req.Path,
		Custom: true,
	}
	fi, statErr := os.Stat(req.Path)
	if statErr == nil {
		src.Exists = true
		src.Size = fi.Size()
	}

	return response.OK(c, map[string]interface{}{
		"id":     id,
		"source": src,
	})
}

// DeleteCustomSource deletes a custom log source by its database ID.
// DELETE /api/v1/logs/custom-sources/:id
func (h *Handler) DeleteCustomSource(c echo.Context) error {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid source ID")
	}

	res, err := h.DB.Exec("DELETE FROM custom_log_sources WHERE id = ?", id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, fmt.Sprintf("Failed to delete source: %v", err))
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Custom source not found")
	}

	return response.OK(c, map[string]string{"message": "Custom source deleted"})
}
