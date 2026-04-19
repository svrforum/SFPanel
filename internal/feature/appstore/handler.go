package appstore

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	osExec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// ---------------------------------------------------------------------------
// Types matching GitHub repo JSON schema
// ---------------------------------------------------------------------------

type AppStoreCategory struct {
	ID   string            `json:"id"`
	Name map[string]string `json:"name"`
	Icon string            `json:"icon"`
}

type AppStoreEnvDef struct {
	Key      string            `json:"key"`
	Label    map[string]string `json:"label"`
	Type     string            `json:"type"`
	Default  string            `json:"default"`
	Required bool              `json:"required"`
	Generate bool              `json:"generate,omitempty"`
	Options  []string          `json:"options,omitempty"`
}

type AppStoreFeature struct {
	Title       map[string]string `json:"title"`
	Description map[string]string `json:"description"`
	Icon        string            `json:"icon,omitempty"`
}

type AppStoreMeta struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description map[string]string `json:"description"`
	Category    string            `json:"category"`
	Version     string            `json:"version"`
	Website     string            `json:"website"`
	Source      string            `json:"source"`
	Icon        string            `json:"icon,omitempty"`
	Ports       []int             `json:"ports"`
	Env         []AppStoreEnvDef  `json:"env"`
	Features    []AppStoreFeature `json:"features,omitempty"`
}

type appStoreAppListItem struct {
	AppStoreMeta
	Installed bool `json:"installed"`
}

type portStatus struct {
	Port      int  `json:"port"`
	InUse     bool `json:"in_use"`
	Suggested int  `json:"suggested,omitempty"`
}

type appStoreAppDetail struct {
	App           AppStoreMeta `json:"app"`
	Compose       string       `json:"compose"`
	Readme        string       `json:"readme"`
	ReadmeBaseURL string       `json:"readme_base_url,omitempty"`
	Installed     bool         `json:"installed"`
	PortStatus    []portStatus `json:"port_status,omitempty"`
}

type appStoreInstallRecord struct {
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

type sseEvent struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Done    bool   `json:"done"`
	Success bool   `json:"success"`
}

type refreshResult struct {
	Message    string `json:"message"`
	Apps       int    `json:"apps"`
	Categories int    `json:"categories"`
}

type installedAppResponse struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// ---------------------------------------------------------------------------
// Exec helpers
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

const (
	appStoreBaseURL = "https://raw.githubusercontent.com/svrforum/SFPanel-appstore/main/"
	cacheTTL        = 1 * time.Hour
	httpTimeout     = 30 * time.Second
)

var validAppID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,49}$`)
var validRepoPath = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

type Handler struct {
	DB          *sql.DB
	ComposePath string
	Cmd         exec.Commander

	mu         sync.RWMutex
	categories []AppStoreCategory
	apps       []AppStoreMeta
	cachedAt   time.Time
	refreshing sync.Mutex
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *Handler) httpGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP GET %s returned %d", url, resp.StatusCode)
	}
	const maxResponseSize = 10 * 1024 * 1024
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

func (h *Handler) ensureCache() error {
	h.mu.RLock()
	valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}
	h.loadCacheFromDB()
	h.mu.RLock()
	valid = !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}
	return h.refreshCache()
}

func (h *Handler) refreshCache() error {
	h.refreshing.Lock()
	defer h.refreshing.Unlock()

	h.mu.RLock()
	valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}

	catData, err := h.httpGet(appStoreBaseURL + "categories.json")
	if err != nil {
		return fmt.Errorf("fetch categories: %w", err)
	}
	cats := make([]AppStoreCategory, 0)
	if err := json.Unmarshal(catData, &cats); err != nil {
		return fmt.Errorf("parse categories: %w", err)
	}

	indexData, err := h.httpGet(appStoreBaseURL + "index.json")
	if err != nil {
		return fmt.Errorf("fetch index: %w", err)
	}
	var appIDs []string
	if err := json.Unmarshal(indexData, &appIDs); err != nil {
		return fmt.Errorf("parse index: %w", err)
	}

	type metaResult struct {
		meta AppStoreMeta
		ok   bool
	}
	results := make([]metaResult, len(appIDs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for i, appID := range appIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			metaURL := appStoreBaseURL + "apps/" + id + "/metadata.json"
			metaData, err := h.httpGet(metaURL)
			if err != nil {
				slog.Warn("skip app: fetch error", "component", "appstore", "app_id", id, "error", err)
				return
			}
			var meta AppStoreMeta
			if err := json.Unmarshal(metaData, &meta); err != nil {
				slog.Warn("skip app: parse error", "component", "appstore", "app_id", id, "error", err)
				return
			}
			if meta.ID == "" {
				meta.ID = id
			}
			results[idx] = metaResult{meta: meta, ok: true}
		}(i, appID)
	}
	wg.Wait()

	apps := make([]AppStoreMeta, 0)
	for _, r := range results {
		if r.ok {
			apps = append(apps, r.meta)
		}
	}

	h.mu.Lock()
	h.categories = cats
	h.apps = apps
	h.cachedAt = time.Now()
	go h.persistCache()
	h.mu.Unlock()
	return nil
}

func (h *Handler) persistCache() {
	h.mu.RLock()
	cacheData := struct {
		Categories []AppStoreCategory `json:"categories"`
		Apps       []AppStoreMeta     `json:"apps"`
		CachedAt   time.Time          `json:"cached_at"`
	}{h.categories, h.apps, h.cachedAt}
	h.mu.RUnlock()

	data, err := json.Marshal(cacheData)
	if err != nil {
		return
	}
	_, _ = h.DB.Exec(
		"INSERT INTO settings (key, value) VALUES ('appstore_cache', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		string(data),
	)
}

func (h *Handler) loadCacheFromDB() {
	var value string
	err := h.DB.QueryRow("SELECT value FROM settings WHERE key = 'appstore_cache'").Scan(&value)
	if err != nil {
		return
	}
	var cacheData struct {
		Categories []AppStoreCategory `json:"categories"`
		Apps       []AppStoreMeta     `json:"apps"`
		CachedAt   time.Time          `json:"cached_at"`
	}
	if err := json.Unmarshal([]byte(value), &cacheData); err != nil {
		return
	}
	if time.Since(cacheData.CachedAt) < cacheTTL {
		h.mu.Lock()
		h.categories = cacheData.Categories
		h.apps = cacheData.Apps
		h.cachedAt = cacheData.CachedAt
		h.mu.Unlock()
	}
}

func (h *Handler) isInstalled(appID string) bool {
	key := "appstore_installed_" + appID
	var value string
	err := h.DB.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil || value == "" {
		return false
	}
	composePath := filepath.Join(h.ComposePath, appID, "docker-compose.yml")
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		_, _ = h.DB.Exec("DELETE FROM settings WHERE key = ?", key)
		return false
	}
	return true
}

func generatePassword(length int) string {
	b := make([]byte, length/2+1)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

func sendSSE(w io.Writer, flusher http.Flusher, event sseEvent) {
	jsonData, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	if flusher != nil {
		flusher.Flush()
	}
}

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

func (h *Handler) GetCategories(c echo.Context) error {
	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	h.mu.RLock()
	cats := h.categories
	h.mu.RUnlock()

	return response.OK(c, cats)
}

func (h *Handler) ListApps(c echo.Context) error {
	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	category := c.QueryParam("category")

	h.mu.RLock()
	allApps := h.apps
	h.mu.RUnlock()

	var result []appStoreAppListItem
	for _, app := range allApps {
		if category != "" && app.Category != category {
			continue
		}
		result = append(result, appStoreAppListItem{
			AppStoreMeta: app,
			Installed:    h.isInstalled(app.ID),
		})
	}

	if result == nil {
		result = []appStoreAppListItem{}
	}

	return response.OK(c, result)
}

func (h *Handler) GetApp(c echo.Context) error {
	id := c.Param("id")
	if !validAppID.MatchString(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid app ID")
	}

	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	h.mu.RLock()
	var found *AppStoreMeta
	for _, app := range h.apps {
		if app.ID == id {
			a := app
			found = &a
			break
		}
	}
	h.mu.RUnlock()

	if found == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "App not found")
	}

	type fetchResult struct {
		data []byte
		err  error
	}

	composeCh := make(chan fetchResult, 1)
	go func() {
		composeURL := appStoreBaseURL + "apps/" + id + "/docker-compose.yml"
		data, err := h.httpGet(composeURL)
		composeCh <- fetchResult{data, err}
	}()

	readmeCh := make(chan struct {
		content string
		baseURL string
	}, 1)
	go func() {
		content := ""
		baseURL := ""
		if found.Source != "" && strings.HasPrefix(found.Source, "https://github.com/") {
			parts := strings.TrimSuffix(found.Source, "/")
			repoPath := strings.TrimPrefix(parts, "https://github.com/")
			if validRepoPath.MatchString(repoPath) {
				for _, branch := range []string{"main", "master", "develop"} {
					url := "https://raw.githubusercontent.com/" + repoPath + "/" + branch + "/README.md"
					if data, err := h.httpGet(url); err == nil {
						content = string(data)
						baseURL = "https://raw.githubusercontent.com/" + repoPath + "/" + branch + "/"
						break
					}
				}
			}
		}
		readmeCh <- struct {
			content string
			baseURL string
		}{content, baseURL}
	}()

	composeResult := <-composeCh
	if composeResult.err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to fetch compose file: "+composeResult.err.Error())
	}

	readmeResult := <-readmeCh

	ports := make([]portStatus, 0)
	for _, p := range found.Ports {
		ps := portStatus{Port: p, InUse: h.isPortInUse(p)}
		if ps.InUse {
			ps.Suggested = h.findFreePort(p)
		}
		ports = append(ports, ps)
	}
	for _, env := range found.Env {
		if env.Type == "port" && env.Default != "" {
			if port := parsePort(env.Default); port > 0 {
				ps := portStatus{Port: port, InUse: h.isPortInUse(port)}
				if ps.InUse {
					ps.Suggested = h.findFreePort(port)
				}
				ports = append(ports, ps)
			}
		}
	}

	detail := appStoreAppDetail{
		App:           *found,
		Compose:       string(composeResult.data),
		Readme:        readmeResult.content,
		ReadmeBaseURL: readmeResult.baseURL,
		Installed:     h.isInstalled(id),
		PortStatus:    ports,
	}

	return response.OK(c, detail)
}

func (h *Handler) InstallApp(c echo.Context) error {
	id := c.Param("id")
	if !validAppID.MatchString(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid app ID")
	}

	var req struct {
		Env      map[string]string `json:"env"`
		Compose  string            `json:"compose"`
		EnvRaw   string            `json:"env_raw"`
		Advanced bool              `json:"advanced"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "Invalid request body")
	}

	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	h.mu.RLock()
	var found *AppStoreMeta
	for _, app := range h.apps {
		if app.ID == id {
			a := app
			found = &a
			break
		}
	}
	h.mu.RUnlock()

	if found == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "App not found")
	}

	var composeData []byte
	if !req.Advanced {
		composeURL := appStoreBaseURL + "apps/" + id + "/docker-compose.yml"
		var err error
		composeData, err = h.httpGet(composeURL)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to fetch compose: "+err.Error())
		}
	}

	stackDir := filepath.Join(h.ComposePath, id)

	if _, err := os.Stat(stackDir); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "Stack directory already exists: "+stackDir)
	}

	conflicts := h.checkPortConflicts(found, req.Env)
	if len(conflicts) > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrPortConflict, "Port conflict: "+strings.Join(conflicts, ", "))
	}

	nameConflicts := make([]string, 0)
	if composeData != nil {
		nameConflicts = h.checkContainerNameConflicts(composeData)
	} else if req.Advanced && req.Compose != "" {
		nameConflicts = h.checkContainerNameConflicts([]byte(req.Compose))
	}
	if len(nameConflicts) > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrContainerConflict, "Container name conflict: "+strings.Join(nameConflicts, ", "))
	}

	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.Writer.(http.Flusher)
	send := func(stage, message string, done, success bool) {
		sendSSE(w, flusher, sseEvent{Stage: stage, Message: message, Done: done, Success: success})
	}

	send("prepare", "Creating directory: "+stackDir, false, true)
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		send("prepare", "Failed to create directory: "+err.Error(), true, false)
		return nil
	}

	cleanup := func() {
		composePath := filepath.Join(stackDir, "docker-compose.yml")
		_, _ = h.Cmd.Run("docker", "compose", "-f", composePath, "down", "-v", "--remove-orphans")
		_ = os.RemoveAll(stackDir)
	}

	composePath := filepath.Join(stackDir, "docker-compose.yml")

	if req.Advanced {
		if strings.TrimSpace(req.Compose) == "" {
			cleanup()
			send("prepare", "docker-compose.yml content is empty", true, false)
			return nil
		}
		send("prepare", "Writing custom docker-compose.yml...", false, true)
		if err := os.WriteFile(composePath, []byte(req.Compose), 0644); err != nil {
			cleanup()
			send("prepare", "Failed to write compose file: "+err.Error(), true, false)
			return nil
		}
		send("prepare", "docker-compose.yml written", false, true)

		if strings.TrimSpace(req.EnvRaw) != "" {
			envPath := filepath.Join(stackDir, ".env")
			if err := os.WriteFile(envPath, []byte(req.EnvRaw), 0600); err != nil {
				cleanup()
				send("prepare", "Failed to write .env file: "+err.Error(), true, false)
				return nil
			}
			send("prepare", ".env file written", false, true)
		}
	} else {
		send("fetch", "docker-compose.yml ready", false, true)

		if err := os.WriteFile(composePath, composeData, 0644); err != nil {
			cleanup()
			send("prepare", "Failed to write compose file: "+err.Error(), true, false)
			return nil
		}
		send("prepare", "docker-compose.yml written", false, true)

		envLines := make([]string, 0)
		if req.Env == nil {
			req.Env = make(map[string]string)
		}
		for _, envDef := range found.Env {
			value := ""
			if userVal, ok := req.Env[envDef.Key]; ok {
				value = userVal
			} else if envDef.Generate {
				value = generatePassword(32)
			} else if envDef.Default != "" {
				value = envDef.Default
			}
			envLines = append(envLines, envDef.Key+"="+value)
		}

		if len(envLines) > 0 {
			envPath := filepath.Join(stackDir, ".env")
			envContent := strings.Join(envLines, "\n") + "\n"
			if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
				cleanup()
				send("prepare", "Failed to write .env file: "+err.Error(), true, false)
				return nil
			}
			send("prepare", ".env file written", false, true)
		}
	}

	// Use a detached context with timeout so docker operations continue even if
	// the HTTP client disconnects (SSE writes will silently fail on closed conn)
	installCtx, installCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer installCancel()

	send("pull", "Pulling images...", false, true)
	h.streamCommand(installCtx, w, flusher, "pull", "docker", "compose", "-f", composePath, "pull")

	send("start", "Starting containers...", false, true)
	exitCode := h.streamCommand(installCtx, w, flusher, "start", "docker", "compose", "-f", composePath, "up", "-d")

	if exitCode != 0 {
		cleanup()
		send("start", "Failed to start app", true, false)
		return nil
	}

	record := appStoreInstallRecord{
		Version:     found.Version,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Name:        found.Name,
		Description: found.Description["en"],
		Icon:        found.Icon,
	}
	recordJSON, _ := json.Marshal(record)
	settingsKey := "appstore_installed_" + id
	_, _ = h.DB.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		settingsKey, string(recordJSON),
	)

	send("done", "App installed successfully", true, true)
	return nil
}

func (h *Handler) streamCommand(ctx context.Context, w io.Writer, flusher http.Flusher, stage string, name string, args ...string) int {
	// Streaming command — cannot use Commander (needs live stdout pipe)
	cmd := osExec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return -1
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendSSE(w, flusher, sseEvent{Stage: stage, Message: "Command failed to start: " + err.Error(), Done: false, Success: false})
		return -1
	}

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		sendSSE(w, flusher, sseEvent{Stage: stage, Message: scanner.Text(), Done: false, Success: true})
	}

	if err := cmd.Wait(); err != nil {
		return 1
	}
	return 0
}

func (h *Handler) checkPortConflicts(meta *AppStoreMeta, envVals map[string]string) []string {
	portsToCheck := make(map[int]bool)
	for _, p := range meta.Ports {
		portsToCheck[p] = true
	}

	for _, envDef := range meta.Env {
		if envDef.Type == "port" {
			val := ""
			if v, ok := envVals[envDef.Key]; ok && v != "" {
				val = v
			} else if envDef.Default != "" {
				val = envDef.Default
			}
			if val != "" {
				if port := parsePort(val); port > 0 {
					portsToCheck[port] = true
				}
			}
		}
	}

	conflicts := make([]string, 0)
	for port := range portsToCheck {
		if h.isPortInUse(port) {
			conflicts = append(conflicts, fmt.Sprintf("%d", port))
		}
	}
	return conflicts
}

func parsePort(s string) int {
	var port int
	if _, err := fmt.Sscanf(s, "%d", &port); err == nil && port > 0 && port <= 65535 {
		return port
	}
	return 0
}

func (h *Handler) isPortInUse(port int) bool {
	out, err := h.Cmd.Run("ss", "-tlnH", "sport", "=", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(out)) > 0
}

func (h *Handler) findFreePort(from int) int {
	for i := 1; i <= 100; i++ {
		candidate := from + i
		if candidate > 65535 {
			break
		}
		if !h.isPortInUse(candidate) {
			return candidate
		}
	}
	return 0
}

func (h *Handler) checkContainerNameConflicts(composeData []byte) []string {
	re := regexp.MustCompile(`(?m)^\s+container_name:\s*(\S+)`)
	matches := re.FindAllSubmatch(composeData, -1)
	if len(matches) == 0 {
		return nil
	}

	out, err := h.Cmd.Run("docker", "ps", "-a", "--format", "{{.Names}}")
	if err != nil {
		return nil
	}
	existing := make(map[string]bool)
	for _, name := range strings.Split(strings.TrimSpace(out), "\n") {
		if name != "" {
			existing[name] = true
		}
	}

	conflicts := make([]string, 0)
	for _, m := range matches {
		name := string(m[1])
		if existing[name] {
			conflicts = append(conflicts, name)
		}
	}
	return conflicts
}

func (h *Handler) GetInstalled(c echo.Context) error {
	rows, err := h.DB.Query("SELECT key, value FROM settings WHERE key LIKE 'appstore_installed_%'")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to query installed apps")
	}
	defer rows.Close()

	result := make([]installedAppResponse, 0)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		appID := strings.TrimPrefix(key, "appstore_installed_")
		var record appStoreInstallRecord
		if err := json.Unmarshal([]byte(value), &record); err != nil {
			continue
		}
		result = append(result, installedAppResponse{
			ID:          appID,
			Version:     record.Version,
			InstalledAt: record.InstalledAt,
			Name:        record.Name,
			Description: record.Description,
			Icon:        record.Icon,
		})
	}

	if result == nil {
		result = []installedAppResponse{}
	}

	return response.OK(c, result)
}

func (h *Handler) RefreshCache(c echo.Context) error {
	if err := h.refreshCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to refresh app store: "+err.Error())
	}

	h.mu.RLock()
	appCount := len(h.apps)
	catCount := len(h.categories)
	h.mu.RUnlock()

	return response.OK(c, refreshResult{
		Message:    "Cache refreshed",
		Apps:       appCount,
		Categories: catCount,
	})
}
