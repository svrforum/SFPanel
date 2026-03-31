package handlers

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
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

type appStoreAppDetail struct {
	App           AppStoreMeta `json:"app"`
	Compose       string       `json:"compose"`
	Readme        string       `json:"readme"`
	ReadmeBaseURL string       `json:"readme_base_url,omitempty"`
	Installed     bool         `json:"installed"`
}

type appStoreInstallRecord struct {
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// SSE event payload (typed — avoids map[string]interface{})
type sseEvent struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Done    bool   `json:"done"`
	Success bool   `json:"success"`
}

// Refresh response
type refreshResult struct {
	Message    string `json:"message"`
	Apps       int    `json:"apps"`
	Categories int    `json:"categories"`
}

// Flattened installed app (matches frontend AppStoreInstalledApp type)
type installedAppResponse struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

const (
	appStoreBaseURL = "https://raw.githubusercontent.com/svrforum/SFPanel-appstore/main/"
	cacheTTL        = 1 * time.Hour
	httpTimeout     = 30 * time.Second
)

var validAppID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,49}$`)

type AppStoreHandler struct {
	DB          *sql.DB
	ComposePath string // "/opt/stacks"

	mu           sync.RWMutex
	categories   []AppStoreCategory
	apps         []AppStoreMeta
	cachedAt     time.Time
	refreshing   sync.Mutex // prevents concurrent refresh calls
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *AppStoreHandler) httpGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP GET %s returned %d", url, resp.StatusCode)
	}
	const maxResponseSize = 10 * 1024 * 1024 // 10MB
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

func (h *AppStoreHandler) ensureCache() error {
	h.mu.RLock()
	valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}
	// Try loading from DB first (fast restart)
	h.loadCacheFromDB()
	h.mu.RLock()
	valid = !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}
	return h.refreshCache()
}

func (h *AppStoreHandler) refreshCache() error {
	h.refreshing.Lock()
	defer h.refreshing.Unlock()

	// Double-check after acquiring lock
	h.mu.RLock()
	valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
	h.mu.RUnlock()
	if valid {
		return nil
	}

	// 1. Fetch categories.json (raw URL, no API rate limit)
	catData, err := h.httpGet(appStoreBaseURL + "categories.json")
	if err != nil {
		return fmt.Errorf("fetch categories: %w", err)
	}
	var cats []AppStoreCategory
	if err := json.Unmarshal(catData, &cats); err != nil {
		return fmt.Errorf("parse categories: %w", err)
	}

	// 2. Fetch index.json for app ID list (raw URL, no API rate limit)
	indexData, err := h.httpGet(appStoreBaseURL + "index.json")
	if err != nil {
		return fmt.Errorf("fetch index: %w", err)
	}
	var appIDs []string
	if err := json.Unmarshal(indexData, &appIDs); err != nil {
		return fmt.Errorf("parse index: %w", err)
	}

	// 3. Fetch each app's metadata.json in parallel (max 5 concurrent)
	type metaResult struct {
		meta AppStoreMeta
		ok   bool
	}
	results := make([]metaResult, len(appIDs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // max 5 concurrent fetches

	for i, appID := range appIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			metaURL := appStoreBaseURL + "apps/" + id + "/metadata.json"
			metaData, err := h.httpGet(metaURL)
			if err != nil {
				log.Printf("[appstore] skip %s: fetch error: %v", id, err)
				return
			}
			var meta AppStoreMeta
			if err := json.Unmarshal(metaData, &meta); err != nil {
				log.Printf("[appstore] skip %s: parse error: %v", id, err)
				return
			}
			if meta.ID == "" {
				meta.ID = id
			}
			results[idx] = metaResult{meta: meta, ok: true}
		}(i, appID)
	}
	wg.Wait()

	var apps []AppStoreMeta
	for _, r := range results {
		if r.ok {
			apps = append(apps, r.meta)
		}
	}

	h.mu.Lock()
	h.categories = cats
	h.apps = apps
	h.cachedAt = time.Now()
	// Persist cache to DB for fast restart
	go h.persistCache()
	h.mu.Unlock()
	return nil
}

func (h *AppStoreHandler) persistCache() {
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

func (h *AppStoreHandler) loadCacheFromDB() {
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

func (h *AppStoreHandler) isInstalled(appID string) bool {
	key := "appstore_installed_" + appID
	var value string
	err := h.DB.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil || value == "" {
		return false
	}
	// Verify compose file still exists on disk
	composePath := filepath.Join(h.ComposePath, appID, "docker-compose.yml")
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		// Stack was removed externally — clean up the stale record
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

// sendSSE writes a typed SSE event to the response writer.
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

// GET /appstore/categories
func (h *AppStoreHandler) GetCategories(c echo.Context) error {
	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	h.mu.RLock()
	cats := h.categories
	h.mu.RUnlock()

	return response.OK(c, cats)
}

// GET /appstore/apps?category=xxx
func (h *AppStoreHandler) ListApps(c echo.Context) error {
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

// GET /appstore/apps/:id
func (h *AppStoreHandler) GetApp(c echo.Context) error {
	id := c.Param("id")
	if !validAppID.MatchString(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid app ID")
	}

	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	// Find app in cache
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

	// Fetch compose YAML and README from GitHub in parallel
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
		if found.Source != "" && strings.Contains(found.Source, "github.com") {
			parts := strings.TrimSuffix(found.Source, "/")
			repoPath := strings.TrimPrefix(parts, "https://github.com/")
			for _, branch := range []string{"main", "master", "develop"} {
				url := "https://raw.githubusercontent.com/" + repoPath + "/" + branch + "/README.md"
				if data, err := h.httpGet(url); err == nil {
					content = string(data)
					baseURL = "https://raw.githubusercontent.com/" + repoPath + "/" + branch + "/"
					break
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

	detail := appStoreAppDetail{
		App:           *found,
		Compose:       string(composeResult.data),
		Readme:        readmeResult.content,
		ReadmeBaseURL: readmeResult.baseURL,
		Installed:     h.isInstalled(id),
	}

	return response.OK(c, detail)
}

// POST /appstore/apps/:id/install — SSE streaming installation
func (h *AppStoreHandler) InstallApp(c echo.Context) error {
	id := c.Param("id")
	if !validAppID.MatchString(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid app ID")
	}

	// Parse request body
	var req struct {
		Env      map[string]string `json:"env"`
		Compose  string            `json:"compose"`  // advanced mode: custom docker-compose.yml
		EnvRaw   string            `json:"env_raw"`   // advanced mode: custom .env content
		Advanced bool              `json:"advanced"`  // true = use compose/env_raw fields
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "Invalid request body")
	}

	if err := h.ensureCache(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to load app store: "+err.Error())
	}

	// Find app meta
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

	// Fetch compose data once (used for both pre-flight and installation)
	var composeData []byte
	if !req.Advanced {
		composeURL := appStoreBaseURL + "apps/" + id + "/docker-compose.yml"
		var err error
		composeData, err = h.httpGet(composeURL)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to fetch compose: "+err.Error())
		}
	}

	// Pre-flight checks (return JSON errors before SSE starts)
	stackDir := filepath.Join(h.ComposePath, id)

	// 1. Check directory doesn't already exist
	if _, err := os.Stat(stackDir); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "Stack directory already exists: "+stackDir)
	}

	// 2. Check port conflicts — resolve env defaults for port values
	conflicts := h.checkPortConflicts(found, req.Env)
	if len(conflicts) > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrPortConflict, "Port conflict: "+strings.Join(conflicts, ", "))
	}

	// 3. Check container name conflicts
	var nameConflicts []string
	if composeData != nil {
		nameConflicts = h.checkContainerNameConflicts(composeData)
	} else if req.Advanced && req.Compose != "" {
		nameConflicts = h.checkContainerNameConflicts([]byte(req.Compose))
	}
	if len(nameConflicts) > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrContainerConflict, "Container name conflict: "+strings.Join(nameConflicts, ", "))
	}

	// Set up SSE
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.Writer.(http.Flusher)
	send := func(stage, message string, done, success bool) {
		sendSSE(w, flusher, sseEvent{Stage: stage, Message: message, Done: done, Success: success})
	}

	// Create directory
	send("prepare", "Creating directory: "+stackDir, false, true)
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		send("prepare", "Failed to create directory: "+err.Error(), true, false)
		return nil
	}

	// Cleanup on failure
	cleanup := func() {
		composePath := filepath.Join(stackDir, "docker-compose.yml")
		_, _ = runCommand("docker", "compose", "-f", composePath, "down", "-v", "--remove-orphans")
		_ = os.RemoveAll(stackDir)
	}

	composePath := filepath.Join(stackDir, "docker-compose.yml")

	if req.Advanced {
		// Advanced mode: use user-provided compose and env content directly
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
		// Simple mode: use pre-fetched compose data
		send("fetch", "docker-compose.yml ready", false, true)

		if err := os.WriteFile(composePath, composeData, 0644); err != nil {
			cleanup()
			send("prepare", "Failed to write compose file: "+err.Error(), true, false)
			return nil
		}
		send("prepare", "docker-compose.yml written", false, true)

		// Build .env content from form values
		var envLines []string
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

	// Pull images first (stream output)
	send("pull", "Pulling images...", false, true)
	h.streamCommand(w, flusher, "pull", "docker", "compose", "-f", composePath, "pull")

	// Start containers (stream output)
	send("start", "Starting containers...", false, true)
	exitCode := h.streamCommand(w, flusher, "start", "docker", "compose", "-f", composePath, "up", "-d")

	if exitCode != 0 {
		cleanup()
		send("start", "Failed to start app", true, false)
		return nil
	}

	// Save install record
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

// streamCommand runs a command and streams its combined output as SSE events.
// Returns the exit code.
func (h *AppStoreHandler) streamCommand(w io.Writer, flusher http.Flusher, stage string, name string, args ...string) int {
	cmd := exec.Command(name, args...)
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

// checkPortConflicts checks if any ports the app needs are already in use.
// It resolves port env vars from user values or defaults.
func (h *AppStoreHandler) checkPortConflicts(meta *AppStoreMeta, envVals map[string]string) []string {
	// Collect ports to check from metadata
	portsToCheck := make(map[int]bool)
	for _, p := range meta.Ports {
		portsToCheck[p] = true
	}

	// Also check env vars of type "port" — user may have customized them
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

	var conflicts []string
	for port := range portsToCheck {
		if isPortInUse(port) {
			conflicts = append(conflicts, fmt.Sprintf("%d", port))
		}
	}
	return conflicts
}

// parsePort parses a string to a port number.
func parsePort(s string) int {
	var port int
	if _, err := fmt.Sscanf(s, "%d", &port); err == nil && port > 0 && port <= 65535 {
		return port
	}
	return 0
}

// isPortInUse checks if a TCP port is currently listening.
func isPortInUse(port int) bool {
	out, err := exec.Command("ss", "-tlnH", "sport", "=", fmt.Sprintf(":%d", port)).Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// checkContainerNameConflicts checks if any containers from the compose data
// would conflict with existing containers (by parsing container_name from YAML).
func (h *AppStoreHandler) checkContainerNameConflicts(composeData []byte) []string {
	// Simple regex to extract container_name values from YAML
	re := regexp.MustCompile(`(?m)^\s+container_name:\s*(\S+)`)
	matches := re.FindAllSubmatch(composeData, -1)
	if len(matches) == 0 {
		return nil
	}

	// Check each container name against existing containers
	out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").Output()
	if err != nil {
		return nil
	}
	existing := make(map[string]bool)
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name != "" {
			existing[name] = true
		}
	}

	var conflicts []string
	for _, m := range matches {
		name := string(m[1])
		if existing[name] {
			conflicts = append(conflicts, name)
		}
	}
	return conflicts
}

// GET /appstore/installed
func (h *AppStoreHandler) GetInstalled(c echo.Context) error {
	rows, err := h.DB.Query("SELECT key, value FROM settings WHERE key LIKE 'appstore_installed_%'")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to query installed apps")
	}
	defer rows.Close()

	var result []installedAppResponse
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

// POST /appstore/refresh
func (h *AppStoreHandler) RefreshCache(c echo.Context) error {
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
