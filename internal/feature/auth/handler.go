package featureauth

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
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

	loginAttempts.Delete(ip)
	h.recordLoginEvent(req.Username, ip, http.StatusOK, "success")
	return response.OK(c, map[string]string{"token": token})
}

// recordLoginEvent appends a row to audit_logs for login outcomes. The audit
// middleware skips /auth/login to avoid logging passwords, so this is the
// only path that produces a login trail; never include the password here.
// The `reason` goes into the path column so breach investigations can filter
// by success/invalid_password/invalid_totp without a schema change.
func (h *Handler) recordLoginEvent(username, ip string, status int, reason string) {
	if h.DB == nil {
		return
	}
	path := "/api/v1/auth/login#" + reason
	go func() {
		_, _ = h.DB.Exec(
			"INSERT INTO audit_logs (username, method, path, status, ip, node_id) VALUES (?, ?, ?, ?, ?, ?)",
			username, "POST", path, status, ip, "",
		)
	}()
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

	return response.OK(c, setup2FAResponse{
		Secret: key.Secret(),
		URL:    key.URL(),
	})
}

func (h *Handler) Verify2FA(c echo.Context) error {
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidTOTP, "Invalid 2FA code")
	}

	_, err := h.DB.Exec("UPDATE admin SET totp_secret = ? WHERE username = ?", req.Secret, username)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to save 2FA secret")
	}

	h.syncAccountToCluster(username)

	return response.OK(c, map[string]string{"message": "2FA enabled successfully"})
}

func (h *Handler) Get2FAStatus(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	var totpSecret sql.NullString
	err := h.DB.QueryRow("SELECT totp_secret FROM admin WHERE username = ?", username).Scan(&totpSecret)
	if err == sql.ErrNoRows {
		return response.OK(c, map[string]bool{"enabled": false})
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	enabled := totpSecret.Valid && totpSecret.String != ""
	return response.OK(c, map[string]bool{"enabled": enabled})
}

func (h *Handler) Disable2FA(c echo.Context) error {
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

	var passwordHash string
	var totpSecret sql.NullString
	err := h.DB.QueryRow(
		"SELECT password, totp_secret FROM admin WHERE username = ?",
		username,
	).Scan(&passwordHash, &totpSecret)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if !auth.CheckPassword(req.Password, passwordHash) {
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidPassword, "Invalid password")
	}

	// If 2FA is currently active, require a valid current TOTP code. This
	// prevents a session-only attacker (stolen JWT + stolen password but
	// no physical device) from downgrading the account to password-only.
	if totpSecret.Valid && totpSecret.String != "" {
		if req.TOTPCode == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrTOTPRequired, "Current 2FA code is required to disable 2FA")
		}
		if !auth.ValidateCode(totpSecret.String, req.TOTPCode) {
			return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidTOTP, "Invalid 2FA code")
		}
	}

	_, err = h.DB.Exec("UPDATE admin SET totp_secret = NULL WHERE username = ?", username)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to disable 2FA")
	}

	h.syncAccountToCluster(username)
	// Clear the limiter on success so the legitimate user isn't locked out.
	loginAttempts.Delete(ip)
	return response.OK(c, map[string]string{"message": "2FA disabled successfully"})
}

func (h *Handler) ChangePassword(c echo.Context) error {
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

	var passwordHash string
	err := h.DB.QueryRow("SELECT password FROM admin WHERE username = ?", username).Scan(&passwordHash)
	if err == sql.ErrNoRows {
		return response.Fail(c, http.StatusNotFound, response.ErrUserNotFound, "User not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if !auth.CheckPassword(req.CurrentPassword, passwordHash) {
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidPassword, "Current password is incorrect")
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHashError, "Failed to hash new password")
	}

	_, err = h.DB.Exec("UPDATE admin SET password = ? WHERE username = ?", newHash, username)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to update password")
	}

	// Sync to cluster
	h.syncAccountToCluster(username)

	// Successful change — clear rate-limit counter for this IP.
	loginAttempts.Delete(ip)
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

	return response.OK(c, map[string]string{"token": token})
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
