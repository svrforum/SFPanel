package handlers

import (
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
)

type loginAttempt struct {
	mu           sync.Mutex
	count        int
	firstAt      time.Time
	blockedUntil time.Time
}

var loginAttempts sync.Map

const (
	rateLimitMaxAttempts   = 5
	rateLimitWindow        = 60 * time.Second
	rateLimitBlockDuration = 5 * time.Minute
)

type AuthHandler struct {
	DB     *sql.DB
	Config *config.Config
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

func (h *AuthHandler) Login(c echo.Context) error {
	ip := c.RealIP()
	if val, ok := loginAttempts.Load(ip); ok {
		attempt := val.(*loginAttempt)
		attempt.mu.Lock()
		blocked := time.Now().Before(attempt.blockedUntil)
		attempt.mu.Unlock()
		if blocked {
			return response.Fail(c, http.StatusTooManyRequests, response.ErrRateLimited, "Too many login attempts. Try again later.")
		}
	}

	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Username == "" || req.Password == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Username and password are required")
	}

	var id int
	var passwordHash string
	var totpSecret sql.NullString
	err := h.DB.QueryRow(
		"SELECT id, password, totp_secret FROM admin WHERE username = ?",
		req.Username,
	).Scan(&id, &passwordHash, &totpSecret)
	if err == sql.ErrNoRows {
		recordFailedLogin(ip)
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidCredentials, "Invalid username or password")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if !auth.CheckPassword(req.Password, passwordHash) {
		recordFailedLogin(ip)
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidCredentials, "Invalid username or password")
	}

	if totpSecret.Valid && totpSecret.String != "" {
		if req.TOTPCode == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrTOTPRequired, "2FA code is required")
		}
		if !auth.ValidateCode(totpSecret.String, req.TOTPCode) {
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
	return response.OK(c, map[string]string{"token": token})
}

func recordFailedLogin(ip string) {
	now := time.Now()
	newAttempt := &loginAttempt{count: 1, firstAt: now}
	val, loaded := loginAttempts.LoadOrStore(ip, newAttempt)
	if !loaded {
		return
	}
	attempt := val.(*loginAttempt)
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if now.Sub(attempt.firstAt) > rateLimitWindow {
		attempt.count = 1
		attempt.firstAt = now
		attempt.blockedUntil = time.Time{}
		return
	}
	attempt.count++
	if attempt.count >= rateLimitMaxAttempts {
		attempt.blockedUntil = now.Add(rateLimitBlockDuration)
	}
}

func (h *AuthHandler) Setup2FA(c echo.Context) error {
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

func (h *AuthHandler) Verify2FA(c echo.Context) error {
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

	return response.OK(c, map[string]string{"message": "2FA enabled successfully"})
}

// Get2FAStatus returns whether 2FA is enabled for the authenticated user.
func (h *AuthHandler) Get2FAStatus(c echo.Context) error {
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

// Disable2FA removes the TOTP secret, disabling 2FA. Requires current password for verification.
func (h *AuthHandler) Disable2FA(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrNoUser, "No authenticated user")
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Password == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Password is required")
	}

	var passwordHash string
	err := h.DB.QueryRow("SELECT password FROM admin WHERE username = ?", username).Scan(&passwordHash)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if !auth.CheckPassword(req.Password, passwordHash) {
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidPassword, "Invalid password")
	}

	_, err = h.DB.Exec("UPDATE admin SET totp_secret = NULL WHERE username = ?", username)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to disable 2FA")
	}

	return response.OK(c, map[string]string{"message": "2FA disabled successfully"})
}

// ChangePassword allows an authenticated user to change their password.
func (h *AuthHandler) ChangePassword(c echo.Context) error {
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

	// Verify current password
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

	// Hash and save new password
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHashError, "Failed to hash new password")
	}

	_, err = h.DB.Exec("UPDATE admin SET password = ? WHERE username = ?", newHash, username)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to update password")
	}

	return response.OK(c, map[string]string{"message": "Password changed successfully"})
}

// GetSetupStatus checks if initial setup is required (no admin exists).
// This is a PUBLIC endpoint — no auth required.
func (h *AuthHandler) GetSetupStatus(c echo.Context) error {
	var count int
	err := h.DB.QueryRow("SELECT COUNT(*) FROM admin").Scan(&count)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	return response.OK(c, map[string]bool{"setup_required": count == 0})
}

// SetupAdmin creates the initial admin account. Only works if no admin exists yet.
// This is a PUBLIC endpoint — no auth required.
func (h *AuthHandler) SetupAdmin(c echo.Context) error {
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

	// Generate JWT so user is logged in immediately
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
