package featureauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

type loginAttempt struct {
	mu           sync.Mutex
	count        int
	firstAt      time.Time
	blockedUntil time.Time
}

var loginAttempts sync.Map

var setupLimiter = struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}{attempts: make(map[string][]time.Time)}

const (
	rateLimitMaxAttempts   = 5
	rateLimitWindow        = 60 * time.Second
	rateLimitBlockDuration = 5 * time.Minute
)

type Handler struct {
	DB         *sql.DB
	Config     *config.Config
	clusterMu  sync.RWMutex
	ClusterMgr *cluster.Manager // nil when cluster not active
	// AuditWriter serializes security-event INSERTs onto the shared
	// background drain. Nil-safe: insertSecurityAuditRow falls back to a
	// direct synchronous INSERT (preserving today's behavior) so tests
	// that construct Handler{} bare don't deadlock waiting for an async
	// drain that was never started.
	AuditWriter *sfdb.AsyncWriter
}

// SetClusterMgr updates the cluster manager reference at runtime.
// Called when a node joins or initializes a cluster while the server is running.
func (h *Handler) SetClusterMgr(m *cluster.Manager) {
	h.clusterMu.Lock()
	defer h.clusterMu.Unlock()
	h.ClusterMgr = m
}

func (h *Handler) getClusterMgr() *cluster.Manager {
	h.clusterMu.RLock()
	defer h.clusterMu.RUnlock()
	return h.ClusterMgr
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type setup2FAResponse struct {
	Secret string `json:"secret"`
	URL    string `json:"url"`
}

type verify2FARequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type setupAdminRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) Login(c echo.Context) error {
	ip := c.RealIP()

	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Username == "" || req.Password == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Username and password are required")
	}
	// Bound the size of credential fields so a multi-megabyte payload can't
	// stretch the bcrypt verifier or exhaust memory before the rate limit
	// even fires. Bcrypt itself caps at 72 bytes; longer passwords are
	// silently truncated, which is fine.
	if !validCredentialBounds(req.Username, req.Password, req.TOTPCode) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Credential field exceeds bounds")
	}

	// Atomically check rate limit and pre-increment before expensive bcrypt check.
	// This prevents concurrent requests from bypassing the limiter.
	if blocked := preRecordLoginAttempt(ip); blocked {
		return response.Fail(c, http.StatusTooManyRequests, response.ErrRateLimited, "Too many login attempts. Try again later.")
	}

	var passwordHash string
	var totpSecretStr string

	// Try cluster FSM first, fallback to local DB
	if acct := h.getClusterAccount(req.Username); acct != nil {
		passwordHash = acct.Password
		totpSecretStr = acct.TOTPSecret
	} else {
		var totpSecret sql.NullString
		err := h.DB.QueryRow(
			"SELECT id, password, totp_secret FROM admin WHERE username = ?",
			req.Username,
		).Scan(new(int), &passwordHash, &totpSecret)
		if err == sql.ErrNoRows {
			return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidCredentials, "Invalid username or password")
		}
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
		}
		if totpSecret.Valid {
			totpSecretStr = totpSecret.String
		}
	}

	if !auth.CheckPassword(req.Password, passwordHash) {
		h.recordLoginEvent(req.Username, ip, http.StatusUnauthorized, "invalid_password")
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidCredentials, "Invalid username or password")
	}

	if totpSecretStr != "" {
		if req.TOTPCode == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrTOTPRequired, "2FA code is required")
		}
		if !auth.ValidateCode(totpSecretStr, req.TOTPCode) {
			// preRecordLoginAttempt already counted this attempt; no additional recordFailedLogin
			// to avoid double-counting (which would lock out after ~3 TOTP fumbles instead of 5)
			h.recordLoginEvent(req.Username, ip, http.StatusUnauthorized, "invalid_totp")
			return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidTOTP, "Invalid 2FA code")
		}
	}

	expiry, err := time.ParseDuration(h.Config.Auth.TokenExpiry)
	if err != nil {
		expiry = 24 * time.Hour
	}

	token, err := auth.GenerateToken(req.Username, h.Config.Auth.JWTSecret, expiry)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTokenError, "Failed to generate token")
	}

	// Issue a refresh token alongside the access JWT. Failure here only
	// disables silent re-auth — the access token still works, so the user
	// just has to log in again when it expires. Logged for diagnostics.
	refreshTok, refreshErr := issueRefreshToken(h.DB, req.Username)
	if refreshErr != nil {
		slog.Warn("refresh token issuance failed", "username", req.Username, "error", refreshErr)
	}

	// Plant the refresh token in an httpOnly+SameSite=Strict cookie so XSS
	// can't reach it from JS. Also issue a CSRF token (JS-readable cookie)
	// that the client must echo via X-CSRF-Token on state-changing requests.
	h.writeAuthCookies(c, refreshTok)

	loginAttempts.Delete(ip)
	h.recordLoginEvent(req.Username, ip, http.StatusOK, "success")
	return response.OK(c, map[string]interface{}{
		"token":         token,
		"refresh_token": refreshTok,
		"expires_in":    int(expiry.Seconds()),
	})
}

// writeAuthCookies sets the refresh + CSRF cookies on the response. Centralised
// so Login, Setup, and Refresh produce identical cookies. Secure flag is
// derived from the request scheme (works on both plain HTTP and TLS-fronted
// deployments).
func (h *Handler) writeAuthCookies(c echo.Context, refreshTok string) {
	if refreshTok == "" {
		return
	}
	secure := auth.IsSecureRequest(c.Request())
	w := c.Response().Writer
	auth.SetRefreshCookie(w, refreshTok, 7*24*time.Hour, secure)
	if csrf := auth.GenerateCSRFToken(); csrf != "" {
		auth.SetCSRFCookie(w, csrf, 7*24*time.Hour, secure)
	}
}

// Logout clears the refresh + CSRF cookies and revokes the refresh token in
// the DB so a captured cookie can't be replayed even if the browser ignores
// the Max-Age=-1.
func (h *Handler) Logout(c echo.Context) error {
	if cookie, err := c.Request().Cookie(auth.RefreshCookieName); err == nil && cookie.Value != "" {
		// Hash the cookie value and delete the matching row. Best-effort —
		// if the token isn't in the DB (already rotated, expired, etc.)
		// nothing to do.
		hashed := sha256Hex(cookie.Value)
		_, _ = h.DB.Exec(`DELETE FROM refresh_tokens WHERE token_hash = ?`, hashed)
	}
	auth.ClearAuthCookies(c.Response().Writer, auth.IsSecureRequest(c.Request()))
	return response.OK(c, map[string]string{"message": "logged out"})
}

// sha256Hex hashes the input and returns hex — same algorithm refresh.go
// uses on storage so the lookup matches the persisted row.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// recordLoginEvent appends a row to audit_logs for login outcomes. The audit
// middleware skips /auth/login to avoid logging passwords, so this is the
// only path that produces a login trail; never include the password here.
// The `reason` goes into the path column so breach investigations can filter
// by success/invalid_password/invalid_totp without a schema change.
//
// Thin wrapper over insertSecurityAuditRow so the original signature stays
// stable for the Login handler while the richer recordSecurityEvent below
// covers the post-auth handlers.
func (h *Handler) recordLoginEvent(username, ip string, status int, reason string) {
	// Login never auto-forwards (the login endpoint is in the CSRF bootstrap
	// list and runs on whichever node the user actually hit), so origin node
	// is just the local node and we leave it empty for the row.
	h.insertSecurityAuditRow(username, ip, "login", reason, "POST", status, "")
}

// recordSecurityEvent appends a row to audit_logs annotating a security-
// relevant state change (password rotation, 2FA enable/disable, 2FA verify).
// Mirrors recordLoginEvent's path format — /api/v1/auth/<action>#<reason> —
// so a single audit query can filter every auth-flow outcome by reason.
//
// The audit middleware skips /api/v1/auth/2fa and /api/v1/auth/change-password
// so the row this writes is the canonical record for the request; without
// that skip there'd be two rows per call (one plain, one reasoned).
//
// Username and IP come from the echo context so callers don't have to
// thread them through. Status is passed explicitly because it must match
// what response.Fail / response.OK returns, not whatever Echo has set on
// the writer when this function fires (the goroutine may race the writer).
func (h *Handler) recordSecurityEvent(c echo.Context, action, reason string, status int) {
	username, _ := c.Get("username").(string)
	ip := c.RealIP()
	method := "POST"
	if c.Request() != nil && c.Request().Method != "" {
		method = c.Request().Method
	}
	// Origin node is set by the follower's proxyToNodeGRPC when an admin
	// action was auto-forwarded; lets a forensic reviewer attribute the
	// row to the node where the user actually authenticated, not the
	// leader where the action landed. Empty when the request ran locally.
	originNode := ""
	if c.Request() != nil {
		originNode = c.Request().Header.Get("X-SFPanel-Original-Node")
	}
	h.insertSecurityAuditRow(username, ip, action, reason, method, status, originNode)
}

// insertSecurityAuditRow is the shared write path. Stays internal so both
// public helpers produce identical row shapes. nodeID stamps the cluster
// node where the user initiated the action (empty for non-cluster or
// locally-handled requests); callers pull it from the X-SFPanel-Original-Node
// header that the follower-side auto-forward sets in proxyToNodeGRPC.
func (h *Handler) insertSecurityAuditRow(username, ip, action, reason, method string, status int, nodeID string) {
	if h.DB == nil {
		return
	}
	path := "/api/v1/auth/" + action + "#" + reason
	insert := func(db *sql.DB) {
		if _, err := db.Exec(
			"INSERT INTO audit_logs (username, method, path, status, ip, node_id) VALUES (?, ?, ?, ?, ?, ?)",
			username, method, path, status, ip, nodeID,
		); err != nil {
			slog.Warn("security audit insert failed", "component", "auth", "action", action, "reason", reason, "error", err)
		}
	}
	if h.AuditWriter != nil && h.AuditWriter.Submit(insert) {
		return
	}
	// Fallback: AuditWriter not wired (tests construct Handler{} bare) or
	// queue was full. A short-lived goroutine here preserves the
	// non-blocking behavior callers expect from recordSecurityEvent.
	go insert(h.DB)
}

// preRecordLoginAttempt atomically checks the rate limit and pre-increments the
// attempt counter. Returns true if the IP is currently blocked. This must be
// called before the expensive bcrypt comparison so that concurrent requests
// cannot bypass the limiter.
func preRecordLoginAttempt(ip string) (blocked bool) {
	now := time.Now()
	newAttempt := &loginAttempt{count: 1, firstAt: now}
	val, loaded := loginAttempts.LoadOrStore(ip, newAttempt)
	if !loaded {
		return false
	}
	attempt := val.(*loginAttempt)
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if now.Before(attempt.blockedUntil) {
		return true
	}
	if now.Sub(attempt.firstAt) > rateLimitWindow {
		attempt.count = 1
		attempt.firstAt = now
		attempt.blockedUntil = time.Time{}
		return false
	}
	attempt.count++
	if attempt.count >= rateLimitMaxAttempts {
		attempt.blockedUntil = now.Add(rateLimitBlockDuration)
	}
	return false
}

func (h *Handler) Setup2FA(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	key, err := auth.GenerateSecret(username)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTOTPError, "Failed to generate 2FA secret")
	}

	h.recordSecurityEvent(c, "2fa_setup", "success", http.StatusOK)
	return response.OK(c, setup2FAResponse{
		Secret: key.Secret(),
		URL:    key.URL(),
	})
}

func (h *Handler) Verify2FA(c echo.Context) error {
	// Cluster admin row is replicated via Raft FSM, so only the leader can
	// Apply the change. Followers transparently forward the request to the
	// leader instead of surfacing ErrNotLeader to the operator.
	if handled, err := middleware.ProxyToLeader(c, h.getClusterMgr()); handled {
		return err
	}

	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	var req verify2FARequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Secret == "" || req.Code == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Secret and code are required")
	}

	if !auth.ValidateCode(req.Secret, req.Code) {
		h.recordSecurityEvent(c, "2fa_verify", "invalid_code", http.StatusBadRequest)
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidTOTP, "Invalid 2FA code")
	}

	passwordHash, _, fromCluster, err := h.loadAdminAccount(username)
	if errors.Is(err, sql.ErrNoRows) {
		h.recordSecurityEvent(c, "2fa_verify", "user_not_found", http.StatusNotFound)
		return response.Fail(c, http.StatusNotFound, response.ErrUserNotFound, "User not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if err := h.persistAdminAccount(username, passwordHash, req.Secret, fromCluster); err != nil {
		return h.failClusterPersist(c, err)
	}

	h.recordSecurityEvent(c, "2fa_verify", "success", http.StatusOK)
	return response.OK(c, map[string]string{"message": "2FA enabled successfully"})
}

func (h *Handler) Get2FAStatus(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	_, totpSecret, _, err := h.loadAdminAccount(username)
	if errors.Is(err, sql.ErrNoRows) {
		return response.OK(c, map[string]bool{"enabled": false})
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	return response.OK(c, map[string]bool{"enabled": totpSecret != ""})
}

func (h *Handler) Disable2FA(c echo.Context) error {
	// Followers forward to the leader before doing local work — the FSM
	// SetAccount call below would otherwise return ErrNotLeader and the
	// rate-limit ledger on the follower would still tick for a request
	// that ultimately ran on the leader.
	if handled, err := middleware.ProxyToLeader(c, h.getClusterMgr()); handled {
		return err
	}

	// Gate behind the per-IP rate limiter so the bcrypt compare below
	// can't be used as a CPU DoS by a session-hijacked attacker.
	ip := c.RealIP()
	if preRecordLoginAttempt(ip) {
		return response.Fail(c, http.StatusTooManyRequests, response.ErrRateLimited, "Too many attempts, try again later")
	}

	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	var req struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Password == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Password is required")
	}

	passwordHash, totpSecret, fromCluster, err := h.loadAdminAccount(username)
	if errors.Is(err, sql.ErrNoRows) {
		return response.Fail(c, http.StatusNotFound, response.ErrUserNotFound, "User not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if !auth.CheckPassword(req.Password, passwordHash) {
		h.recordSecurityEvent(c, "2fa_disable", "invalid_password", http.StatusUnauthorized)
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidPassword, "Invalid password")
	}

	// If 2FA is currently active, require a valid current TOTP code. This
	// prevents a session-only attacker (stolen JWT + stolen password but
	// no physical device) from downgrading the account to password-only.
	if totpSecret != "" {
		if req.TOTPCode == "" {
			h.recordSecurityEvent(c, "2fa_disable", "totp_required", http.StatusBadRequest)
			return response.Fail(c, http.StatusBadRequest, response.ErrTOTPRequired, "Current 2FA code is required to disable 2FA")
		}
		if !auth.ValidateCode(totpSecret, req.TOTPCode) {
			h.recordSecurityEvent(c, "2fa_disable", "invalid_totp", http.StatusUnauthorized)
			return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidTOTP, "Invalid 2FA code")
		}
	}

	if err := h.persistAdminAccount(username, passwordHash, "", fromCluster); err != nil {
		return h.failClusterPersist(c, err)
	}

	// Clear the limiter on success so the legitimate user isn't locked out.
	loginAttempts.Delete(ip)
	h.recordSecurityEvent(c, "2fa_disable", "success", http.StatusOK)
	return response.OK(c, map[string]string{"message": "2FA disabled successfully"})
}

func (h *Handler) ChangePassword(c echo.Context) error {
	// Followers forward to the leader. The admin row replicates via Raft
	// FSM and only the leader can Apply; without this hop the operator
	// would see "must run on leader node" no matter which node they happen
	// to be logged into.
	if handled, err := middleware.ProxyToLeader(c, h.getClusterMgr()); handled {
		return err
	}

	// Rate-limit per-IP to prevent the bcrypt verify below being used for
	// sustained CPU DoS against an admin session.
	ip := c.RealIP()
	if preRecordLoginAttempt(ip) {
		return response.Fail(c, http.StatusTooManyRequests, response.ErrRateLimited, "Too many attempts, try again later")
	}

	var req changePasswordRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Current password and new password are required")
	}

	if len(req.NewPassword) < 8 {
		return response.Fail(c, http.StatusBadRequest, response.ErrWeakPassword, "New password must be at least 8 characters")
	}

	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	passwordHash, totpSecret, fromCluster, err := h.loadAdminAccount(username)
	if errors.Is(err, sql.ErrNoRows) {
		return response.Fail(c, http.StatusNotFound, response.ErrUserNotFound, "User not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if !auth.CheckPassword(req.CurrentPassword, passwordHash) {
		h.recordSecurityEvent(c, "password_change", "invalid_password", http.StatusUnauthorized)
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidPassword, "Current password is incorrect")
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHashError, "Failed to hash new password")
	}

	if err := h.persistAdminAccount(username, newHash, totpSecret, fromCluster); err != nil {
		return h.failClusterPersist(c, err)
	}

	// Successful change — clear rate-limit counter for this IP.
	loginAttempts.Delete(ip)
	h.recordSecurityEvent(c, "password_change", "success", http.StatusOK)
	return response.OK(c, map[string]string{"message": "Password changed successfully"})
}

func (h *Handler) GetSetupStatus(c echo.Context) error {
	var count int
	err := h.DB.QueryRow("SELECT COUNT(*) FROM admin").Scan(&count)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	return response.OK(c, map[string]bool{"setup_required": count == 0})
}

func (h *Handler) SetupAdmin(c echo.Context) error {
	ip := c.RealIP()
	now := time.Now()
	setupLimiter.mu.Lock()
	// Garbage collect stale entries for all IPs
	for k, timestamps := range setupLimiter.attempts {
		var valid []time.Time
		for _, t := range timestamps {
			if now.Sub(t) < time.Minute {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(setupLimiter.attempts, k)
		} else {
			setupLimiter.attempts[k] = valid
		}
	}
	attempts := setupLimiter.attempts[ip]
	if len(attempts) >= 5 {
		setupLimiter.mu.Unlock()
		return response.Fail(c, http.StatusTooManyRequests, response.ErrRateLimited, "Too many setup attempts. Try again later.")
	}
	setupLimiter.attempts[ip] = append(attempts, now)
	setupLimiter.mu.Unlock()

	var req setupAdminRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Username == "" || req.Password == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Username and password are required")
	}
	if !validCredentialBounds(req.Username, req.Password, "") {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Username or password exceeds bounds")
	}

	if len(req.Password) < 8 {
		return response.Fail(c, http.StatusBadRequest, response.ErrWeakPassword, "Password must be at least 8 characters")
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHashError, "Failed to hash password")
	}

	tx, err := h.DB.Begin()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM admin").Scan(&count); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	if count > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadySetup, "Admin account already exists")
	}

	if _, err := tx.Exec("INSERT INTO admin (username, password) VALUES (?, ?)", req.Username, hash); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to create admin account")
	}

	if err := tx.Commit(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to create admin account")
	}

	// Sync new account to cluster
	h.syncAccountToCluster(req.Username)

	expiry, err := time.ParseDuration(h.Config.Auth.TokenExpiry)
	if err != nil {
		expiry = 24 * time.Hour
	}

	token, err := auth.GenerateToken(req.Username, h.Config.Auth.JWTSecret, expiry)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTokenError, "Admin created but failed to generate token")
	}

	refreshTok, refreshErr := issueRefreshToken(h.DB, req.Username)
	if refreshErr != nil {
		slog.Warn("refresh token issuance failed (setup)", "username", req.Username, "error", refreshErr)
	}

	h.writeAuthCookies(c, refreshTok)

	return response.OK(c, map[string]interface{}{
		"token":         token,
		"refresh_token": refreshTok,
		"expires_in":    int(expiry.Seconds()),
	})
}

// getClusterAccount returns an account from the cluster FSM, or nil if cluster is not active.
func (h *Handler) getClusterAccount(username string) *cluster.AdminAccount {
	mgr := h.getClusterMgr()
	if mgr == nil {
		return nil
	}
	return mgr.GetAccount(username)
}

// syncAccountToCluster reads the account from local DB and proposes it to Raft.
// Best-effort: logs errors but does not fail the parent operation.
func (h *Handler) syncAccountToCluster(username string) {
	mgr := h.getClusterMgr()
	if mgr == nil {
		return
	}

	var passwordHash string
	var totpSecret sql.NullString
	err := h.DB.QueryRow("SELECT password, totp_secret FROM admin WHERE username = ?", username).Scan(&passwordHash, &totpSecret)
	if err != nil {
		slog.Warn("failed to read account for cluster sync", "username", username, "error", err)
		return
	}

	totp := ""
	if totpSecret.Valid {
		totp = totpSecret.String
	}

	if err := mgr.SyncAccountFromDB(username, passwordHash, totp); err != nil {
		slog.Warn("failed to sync account to cluster", "username", username, "error", err)
	}
}

// loadAdminAccount returns an account's current state and indicates whether
// it came from the cluster FSM. Mirrors Login's lookup order (FSM first,
// local DB fallback) so handlers that mutate the account stay consistent
// with how Login authenticated it.
//
// Returns (sql.ErrNoRows-compatible) error when the username exists in
// neither store.
func (h *Handler) loadAdminAccount(username string) (passwordHash, totpSecret string, fromCluster bool, err error) {
	if acct := h.getClusterAccount(username); acct != nil {
		return acct.Password, acct.TOTPSecret, true, nil
	}
	var ts sql.NullString
	err = h.DB.QueryRow("SELECT password, totp_secret FROM admin WHERE username = ?", username).Scan(&passwordHash, &ts)
	if err != nil {
		return "", "", false, err
	}
	if ts.Valid {
		totpSecret = ts.String
	}
	return passwordHash, totpSecret, false, nil
}

// persistAdminAccount writes the desired account state back to whichever
// store the account came from. Cluster-only accounts go through Raft
// (leader-only); local accounts UPDATE the admin table and best-effort
// sync to the cluster afterwards.
//
// Returns cluster.ErrNotLeader when called on a follower for a cluster-only
// account — caller is responsible for translating that to a user-facing
// hint about switching to the leader node.
func (h *Handler) persistAdminAccount(username, passwordHash, totpSecret string, fromCluster bool) error {
	if fromCluster {
		mgr := h.getClusterMgr()
		if mgr == nil {
			// Account claimed it came from FSM but manager is gone — refuse
			// rather than silently corrupting state by falling back to a
			// local INSERT.
			return errors.New("cluster account requires an active cluster manager")
		}
		return mgr.SetAccount(cluster.AdminAccount{
			Username:   username,
			Password:   passwordHash,
			TOTPSecret: totpSecret,
		})
	}
	var totp interface{}
	if totpSecret != "" {
		totp = totpSecret
	}
	if _, err := h.DB.Exec(
		"UPDATE admin SET password = ?, totp_secret = ? WHERE username = ?",
		passwordHash, totp, username,
	); err != nil {
		return err
	}
	h.syncAccountToCluster(username)
	return nil
}

// failClusterPersist maps persistAdminAccount errors to a useful HTTP
// response. ErrNotLeader gets a 503 + leader hint so the user can switch
// to the leader node in the UI.
func (h *Handler) failClusterPersist(c echo.Context, err error) error {
	if errors.Is(err, cluster.ErrNotLeader) {
		hint := "Account changes for cluster admins must run on the leader node."
		if mgr := h.getClusterMgr(); mgr != nil {
			if raft := mgr.GetRaft(); raft != nil {
				if leaderID := raft.LeaderID(); leaderID != "" {
					hint += " Switch to node " + leaderID + " and retry."
				}
			}
		}
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, hint)
	}
	return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to persist account changes")
}
