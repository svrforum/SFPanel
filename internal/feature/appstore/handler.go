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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
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
	// Featured renders the app first and shows a "Featured" badge on cards.
	Featured bool `json:"featured,omitempty"`
	// Screenshots is an optional gallery of image URLs shown on the detail page.
	Screenshots []string `json:"screenshots,omitempty"`
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
	// Catalog now lives in the main repo under /appstore (migrated from the
	// separate SFPanel-appstore repo). Still fetched at runtime — a catalog-only
	// commit to main updates every panel within the cache TTL, no release needed.
	appStoreBaseURL    = "https://raw.githubusercontent.com/svrforum/SFPanel/main/appstore/"
	appStoreBundleFile = "catalog.json"
	cacheTTL           = 1 * time.Hour
	httpTimeout        = 30 * time.Second
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
	stale      bool
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
	return h.refreshCache(false)
}

// fetchCatalogLegacy is the pre-bundle fetch path: categories.json + index.json
// + one metadata.json per app (concurrency-limited). Kept as a fallback for a
// `main` that doesn't yet carry catalog.json.
func (h *Handler) fetchCatalogLegacy() ([]AppStoreCategory, []AppStoreMeta, error) {
	catData, err := h.httpGet(appStoreBaseURL + "categories.json")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch categories: %w", err)
	}
	cats := make([]AppStoreCategory, 0)
	if err := json.Unmarshal(catData, &cats); err != nil {
		return nil, nil, fmt.Errorf("parse categories: %w", err)
	}

	indexData, err := h.httpGet(appStoreBaseURL + "index.json")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch index: %w", err)
	}
	var appIDs []string
	if err := json.Unmarshal(indexData, &appIDs); err != nil {
		return nil, nil, fmt.Errorf("parse index: %w", err)
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
			metaData, err := h.httpGet(appStoreBaseURL + "apps/" + id + "/metadata.json")
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
	return cats, apps, nil
}

// fetchCatalogBundle fetches the single bundled catalog.json. When force is set
// a per-minute cache-bust query sidesteps the ~5-min raw.githubusercontent CDN
// window.
func (h *Handler) fetchCatalogBundle(force bool) ([]AppStoreCategory, []AppStoreMeta, error) {
	url := appStoreBaseURL + appStoreBundleFile
	if force {
		url += fmt.Sprintf("?v=%d", time.Now().Unix()/60)
	}
	data, err := h.httpGet(url)
	if err != nil {
		return nil, nil, err
	}
	var bundle struct {
		Categories []AppStoreCategory `json:"categories"`
		Apps       []AppStoreMeta     `json:"apps"`
	}
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, nil, fmt.Errorf("parse catalog bundle: %w", err)
	}
	if len(bundle.Apps) == 0 {
		return nil, nil, fmt.Errorf("catalog bundle has no apps")
	}
	return bundle.Categories, bundle.Apps, nil
}

func (h *Handler) refreshCache(force bool) error {
	h.refreshing.Lock()
	defer h.refreshing.Unlock()

	if !force {
		h.mu.RLock()
		valid := !h.cachedAt.IsZero() && time.Since(h.cachedAt) < cacheTTL
		h.mu.RUnlock()
		if valid {
			return nil
		}
	}

	cats, apps, err := h.fetchCatalogBundle(force)
	if err != nil {
		slog.Warn("appstore: bundle fetch failed, falling back to per-app walk",
			"component", "appstore", "error", err)
		cats, apps, err = h.fetchCatalogLegacy()
	}
	if err != nil {
		// Serve-stale: if we already have a catalog (in-mem or DB), keep it and
		// flag it stale instead of failing the whole store offline.
		h.mu.RLock()
		haveCache := len(h.apps) > 0
		h.mu.RUnlock()
		if haveCache {
			h.mu.Lock()
			h.stale = true
			h.mu.Unlock()
			slog.Warn("appstore: refresh failed; serving stale cache",
				"component", "appstore", "error", err)
			return nil
		}
		return err
	}

	h.mu.Lock()
	h.categories = cats
	h.apps = apps
	h.cachedAt = time.Now()
	h.stale = false
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
	h.mu.Lock()
	defer h.mu.Unlock()
	// Load the DB cache when in-mem is empty or the DB copy is newer. Caches
	// older than the TTL are accepted as a last resort (offline survivability)
	// and flagged stale rather than discarded.
	if h.cachedAt.IsZero() || cacheData.CachedAt.After(h.cachedAt) {
		h.categories = cacheData.Categories
		h.apps = cacheData.Apps
		h.cachedAt = cacheData.CachedAt
		h.stale = time.Since(cacheData.CachedAt) >= cacheTTL
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

// writeFileAtomic writes data to path via a temp file + rename so a crash
// between the compose write and the .env write doesn't leave the staging
// directory in a partial state (compose present, .env missing). Without this,
// the next install retry would hit EEXIST on os.Mkdir(stackDir) and fail to
// recover cleanly. The temp file inherits the requested mode; rename keeps it.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".sfpanel.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func sendSSE(w io.Writer, flusher http.Flusher, event sseEvent) {
	jsonData, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	if flusher != nil {
		flusher.Flush()
	}
}

// verifyAdvancedReAuth requires the caller to re-prove their password
// before InstallApp will accept arbitrary compose YAML. A stolen JWT
// (XSS, leaked from a non-loopback ?token= URL, exfiltrated cookie) alone
// must not be sufficient to escalate to host root via a compose file with
// privileged: true / pid: host / hostfs binds — the bcrypt verify here is
// the second factor for that escalation path.
//
// Username comes from c.Get("username") which the JWT middleware sets;
// the password hash comes from the local admin row (replicated from the
// FSM in cluster mode). The bcrypt compare is intentionally slow and
// sits behind the global IP-based login rate limiter applied at the
// middleware layer, so unlimited password guessing against this endpoint
// is no faster than against /auth/login.
func (h *Handler) verifyAdvancedReAuth(c echo.Context, password string) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		slog.Warn("advanced install missing authenticated username", "component", "appstore")
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidCredentials, "Re-authentication required for advanced install")
	}
	if password == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields,
			"Password is required for advanced install")
	}
	var hash string
	err := h.DB.QueryRow("SELECT password FROM admin WHERE username = ?", username).Scan(&hash)
	if err != nil {
		slog.Warn("advanced install: failed to load admin row",
			"component", "appstore", "username", username, "error", err)
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidCredentials,
			"Re-authentication required for advanced install")
	}
	if !auth.CheckPassword(password, hash) {
		slog.Warn("advanced install: bad password",
			"component", "appstore", "username", username, "remote_ip", c.RealIP())
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidPassword,
			"Incorrect password")
	}
	return nil
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

	// Bulk-load installed-state in a single query instead of per-app
	// (N+1 against the settings table). isInstalled also stat()s the
	// compose file path; we mirror that fallback here to keep the
	// stale-entry cleanup behaviour intact.
	installed := h.installedSet()

	var result []appStoreAppListItem
	for _, app := range allApps {
		if category != "" && app.Category != category {
			continue
		}
		result = append(result, appStoreAppListItem{
			AppStoreMeta: app,
			Installed:    installed[app.ID],
		})
	}

	if result == nil {
		result = []appStoreAppListItem{}
	}

	return response.OK(c, result)
}

// installedSet returns a set of app IDs currently installed. Replaces the
// N+1 isInstalled-per-app pattern in ListApps with a single SELECT plus
// per-row stat-check. The stat check matches isInstalled's behaviour: a
// settings-row that points at a missing compose file is treated as not
// installed AND the row is GC'd.
func (h *Handler) installedSet() map[string]bool {
	out := map[string]bool{}
	rows, err := h.DB.Query("SELECT key FROM settings WHERE key LIKE 'appstore_installed_%'")
	if err != nil {
		return out
	}
	defer rows.Close()
	const prefix = "appstore_installed_"
	stale := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			continue
		}
		appID := strings.TrimPrefix(key, prefix)
		if appID == key {
			continue
		}
		composePath := filepath.Join(h.ComposePath, appID, "docker-compose.yml")
		if _, err := os.Stat(composePath); os.IsNotExist(err) {
			stale = append(stale, key)
			continue
		}
		out[appID] = true
	}
	for _, k := range stale {
		_, _ = h.DB.Exec("DELETE FROM settings WHERE key = ?", k)
	}
	return out
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

	// Cap the request body. Advanced installs carry user-submitted
	// compose YAML + env raw text; a malicious or buggy client otherwise
	// could push a 100 MB body and force the panel to buffer it before
	// validation runs. 1 MB is several orders of magnitude past any
	// legitimate compose file (the entire docker-compose spec fits in
	// well under 100 KB) and forecloses the DoS shape.
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 1<<20)

	var req struct {
		Env      map[string]string `json:"env"`
		Compose  string            `json:"compose"`
		EnvRaw   string            `json:"env_raw"`
		Advanced bool              `json:"advanced"`
		// Password is the operator's current password; required when
		// Advanced=true because that branch lets the operator submit
		// arbitrary compose YAML — a host-root primitive that already
		// validateAdvancedCompose gates structurally, but step-up
		// re-auth blocks the case where an attacker has merely stolen
		// the JWT (XSS, cookie exfil) without the credential itself.
		Password string `json:"password,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "Invalid request body")
	}

	if req.Advanced {
		if err := h.verifyAdvancedReAuth(c, req.Password); err != nil {
			return err
		}
	}

	// Simple mode: env VALUES are user-supplied and get written verbatim as
	// `KEY=value` lines into .env. A value carrying a newline could inject
	// additional env lines (or compose directives once interpolated), so
	// reject it up front — before any fetch, write, or SSE stream starts.
	// Keys are catalog-defined and already constrained, so values are the
	// only untrusted surface here.
	if !req.Advanced {
		for k, v := range req.Env {
			if strings.ContainsAny(v, "\n\r") {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody,
					"Environment value for "+k+" must not contain newline characters")
			}
		}
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

	// Ensure parent ComposePath exists so the os.Mkdir below isn't a
	// stat+create race: two concurrent installs for the same id would
	// otherwise both pass an os.Stat check and both succeed in MkdirAll.
	// os.Mkdir (non-recursive) is atomic and fails with EEXIST.
	if err := os.MkdirAll(h.ComposePath, 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError, "Failed to ensure compose dir: "+err.Error())
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
	// os.Mkdir + EEXIST as the atomic race-free conflict check. A second
	// install request for the same id during the install window now gets
	// a clear "already in progress / installed" error instead of silently
	// racing on docker compose up.
	if err := os.Mkdir(stackDir, 0755); err != nil {
		if os.IsExist(err) {
			send("prepare", "Stack directory already exists (concurrent install?): "+stackDir, true, false)
		} else {
			send("prepare", "Failed to create directory: "+err.Error(), true, false)
		}
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
		// Advanced mode lets the user submit arbitrary compose YAML, which
		// otherwise becomes a trivial host-root-escape primitive
		// (privileged: true, pid: host, /:/hostfs bind, docker.sock bind).
		// Reject the most obvious patterns before handing the file to
		// `docker compose up -d`.
		if err := validateAdvancedCompose(req.Compose); err != nil {
			cleanup()
			send("prepare", "Refused compose file: "+err.Error(), true, false)
			return nil
		}
		send("prepare", "Writing custom docker-compose.yml...", false, true)
		// 0o600: compose YAML can carry inline secrets (environment: blocks).
		// Atomic write closes the crash window between compose + .env writes
		// that would otherwise leave a half-staged stackDir.
		if err := writeFileAtomic(composePath, []byte(req.Compose), 0o600); err != nil {
			cleanup()
			send("prepare", "Failed to write compose file: "+err.Error(), true, false)
			return nil
		}
		send("prepare", "docker-compose.yml written", false, true)

		if strings.TrimSpace(req.EnvRaw) != "" {
			envPath := filepath.Join(stackDir, ".env")
			if err := writeFileAtomic(envPath, []byte(req.EnvRaw), 0o600); err != nil {
				cleanup()
				send("prepare", "Failed to write .env file: "+err.Error(), true, false)
				return nil
			}
			send("prepare", ".env file written", false, true)
		}
	} else {
		send("fetch", "docker-compose.yml ready", false, true)

		if err := writeFileAtomic(composePath, composeData, 0o600); err != nil {
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
			if err := writeFileAtomic(envPath, []byte(envContent), 0o600); err != nil {
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

// composeDownArgs builds the `docker compose ... down` argument list. Volumes
// (and thus app data) are removed only when keepData is false.
func composeDownArgs(composePath string, keepData bool) []string {
	args := []string{"compose", "-f", composePath, "down", "--remove-orphans"}
	if !keepData {
		args = append(args, "-v")
	}
	return args
}

// UninstallApp tears down an installed app: `docker compose down -v
// --remove-orphans`, then removes the staging directory and the
// appstore_installed_<id> settings row. Per-node action — the ?node=
// proxy middleware forwards to the target node before this handler runs,
// so it stays local-only (same as InstallApp). Normal JSON endpoint, not
// SSE: the teardown is fast and the result is a single OK/Fail.
func (h *Handler) UninstallApp(c echo.Context) error {
	id := c.Param("id")
	if !validAppID.MatchString(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid app ID")
	}

	keepData := c.QueryParam("keep_data") == "true"

	stackDir := filepath.Join(h.ComposePath, id)
	composePath := filepath.Join(stackDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "App is not installed")
	}

	// Tear the stack down. Use RunCtx so the subprocess dies if the request
	// is cancelled. On failure we deliberately leave the directory in place
	// so the operator can inspect/retry rather than losing state to a
	// half-completed teardown.
	out, err := h.Cmd.RunCtx(c.Request().Context(), "docker", composeDownArgs(composePath, keepData)...)
	if err != nil {
		slog.Warn("appstore uninstall: compose down failed", "component", "appstore", "app_id", id, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError,
			"Failed to stop app: "+response.SanitizeOutput(out))
	}

	if err := os.RemoveAll(stackDir); err != nil {
		slog.Warn("appstore uninstall: failed to remove stack dir", "component", "appstore", "app_id", id, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrAppStoreError,
			"Failed to remove app files: "+err.Error())
	}

	_, _ = h.DB.Exec("DELETE FROM settings WHERE key = ?", "appstore_installed_"+id)

	return response.OK(c, map[string]string{"message": "App uninstalled successfully"})
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
		sendSSE(w, flusher, sseEvent{Stage: stage, Message: "Command failed to start: " + response.SanitizeOutput(err.Error()), Done: false, Success: false})
		return -1
	}

	scanner := bufio.NewScanner(pipe)
	exec.PrepareScanner(scanner)
	for scanner.Scan() {
		sendSSE(w, flusher, sseEvent{Stage: stage, Message: response.SanitizeOutput(scanner.Text()), Done: false, Success: true})
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
	used := h.portsInUseSnapshot()
	for port := range portsToCheck {
		if _, taken := used[port]; taken {
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

// portsInUseSnapshot runs `ss -tlnH` once and returns a set of TCP ports
// currently listening on any interface. The previous isPortInUse fired
// one subprocess per port; checkPortConflicts then ran it for every
// declared port of the app + findFreePort up to 100 more times before
// declaring failure, so installing one app could fan out 100+ ss calls.
// One pass is O(open ports) regardless of how many candidates we test.
func (h *Handler) portsInUseSnapshot() map[int]struct{} {
	used := map[int]struct{}{}
	out, err := h.Cmd.Run("ss", "-tlnH")
	if err != nil {
		return used
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// `ss -tlnH` rows look like:
		//   LISTEN 0  511  0.0.0.0:80  0.0.0.0:*
		// Local Address:Port is column index 3 in the no-header output.
		if len(fields) < 4 {
			continue
		}
		addr := fields[3]
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			if p, err := strconv.Atoi(addr[i+1:]); err == nil {
				used[p] = struct{}{}
			}
		}
	}
	return used
}

func (h *Handler) isPortInUse(port int) bool {
	out, err := h.Cmd.Run("ss", "-tlnH", "sport", "=", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(out)) > 0
}

// findFreePortInUsed scans candidates [from+1, from+100] against the
// pre-built set, returning the first unused port or 0 if every candidate
// is taken. Splitting the snapshot from the search lets checkPortConflicts
// reuse it across all of an app's declared ports.
func (h *Handler) findFreePortInUsed(from int, used map[int]struct{}) int {
	for i := 1; i <= 100; i++ {
		candidate := from + i
		if candidate > 65535 {
			break
		}
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
	return 0
}

// findFreePort retained for callers that don't pre-build the set; one
// snapshot per call is still O(1) subprocesses instead of O(N).
func (h *Handler) findFreePort(from int) int {
	return h.findFreePortInUsed(from, h.portsInUseSnapshot())
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

type appStoreStatus struct {
	Stale    bool      `json:"stale"`
	CachedAt time.Time `json:"cached_at"`
	Apps     int       `json:"apps"`
}

// GetStatus reports whether the served catalog is stale (last refresh failed,
// serving a cached copy) so the UI can show an offline banner. Never errors —
// a brand-new panel with no cache simply reports stale=false, apps=0.
func (h *Handler) GetStatus(c echo.Context) error {
	_ = h.ensureCache()
	h.mu.RLock()
	st := appStoreStatus{Stale: h.stale, CachedAt: h.cachedAt, Apps: len(h.apps)}
	h.mu.RUnlock()
	return response.OK(c, st)
}

func (h *Handler) RefreshCache(c echo.Context) error {
	if err := h.refreshCache(true); err != nil {
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
