package featureauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// refreshTokenLifetime is how long a refresh token is valid before it must
// be re-issued by re-authenticating. 7 days is the conventional balance —
// long enough to keep "stay signed in" feeling stable, short enough that a
// stolen token isn't permanent.
const refreshTokenLifetime = 7 * 24 * time.Hour

// refreshTokenBytes is the entropy of the opaque token before hex-encoding.
// 32 bytes (256 bits) matches the sha256 hash size.
const refreshTokenBytes = 32

// issueRefreshToken creates a new opaque refresh token, persists its hash
// against the username, and returns the raw token to the client. Old tokens
// for the same username are NOT cleared — multiple concurrent sessions are
// fine and the periodic cleaner drops expired ones.
func issueRefreshToken(db *sql.DB, username string) (string, error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	tok := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tok))
	hashHex := hex.EncodeToString(hash[:])

	_, err := db.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, expires_at) VALUES (?, ?, ?)`,
		hashHex, username, time.Now().Add(refreshTokenLifetime).UTC().Format(time.RFC3339),
	)
	if err != nil {
		return "", fmt.Errorf("persist refresh token: %w", err)
	}
	return tok, nil
}

// rotateRefreshToken validates a refresh token, deletes it, and issues a
// fresh pair (access JWT + new refresh token). Atomic via tx so a partial
// failure doesn't leave the user holding an unusable token.
//
// Returns ErrInvalidRefreshToken when the token doesn't exist or is expired.
func (h *Handler) Refresh(c echo.Context) error {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.Bind(&req); err != nil || req.RefreshToken == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Missing refresh_token")
	}

	hash := sha256.Sum256([]byte(req.RefreshToken))
	hashHex := hex.EncodeToString(hash[:])

	tx, err := h.DB.Begin()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	defer tx.Rollback()

	var username, expiresStr string
	err = tx.QueryRow(
		`SELECT username, expires_at FROM refresh_tokens WHERE token_hash = ?`,
		hashHex,
	).Scan(&username, &expiresStr)
	if err == sql.ErrNoRows {
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "Invalid refresh token")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	expiresAt, parseErr := time.Parse(time.RFC3339, expiresStr)
	if parseErr != nil || time.Now().After(expiresAt) {
		// Expired — drop and reject.
		_, _ = tx.Exec(`DELETE FROM refresh_tokens WHERE token_hash = ?`, hashHex)
		_ = tx.Commit()
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "Refresh token expired")
	}

	// Verify the user still exists. If the admin account was deleted (rare
	// but possible during cluster account ops), the refresh chain dies.
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM admin WHERE username = ?`, username).Scan(&exists); err != nil || exists == 0 {
		_, _ = tx.Exec(`DELETE FROM refresh_tokens WHERE token_hash = ?`, hashHex)
		_ = tx.Commit()
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "User no longer exists")
	}

	// Rotate: drop the consumed token, issue a fresh pair.
	if _, err := tx.Exec(`DELETE FROM refresh_tokens WHERE token_hash = ?`, hashHex); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	// Mint new refresh token within the tx so a Commit failure doesn't leave
	// the new token persisted while the old one is gone.
	newRaw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(newRaw); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "rand failed")
	}
	newTok := hex.EncodeToString(newRaw)
	newHash := sha256.Sum256([]byte(newTok))
	newHashHex := hex.EncodeToString(newHash[:])
	if _, err := tx.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, expires_at) VALUES (?, ?, ?)`,
		newHashHex, username, time.Now().Add(refreshTokenLifetime).UTC().Format(time.RFC3339),
	); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	if err := tx.Commit(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	// Issue access JWT — same parser/validator the login flow uses.
	accessExpiry := h.tokenExpiry()
	accessTok, err := auth.GenerateToken(username, h.Config.Auth.JWTSecret, accessExpiry)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Failed to issue access token")
	}

	return response.OK(c, map[string]interface{}{
		"token":         accessTok,
		"refresh_token": newTok,
		"expires_in":    int(accessExpiry.Seconds()),
	})
}

// tokenExpiry parses cfg.Auth.TokenExpiry, defaulting to 24h on parse error
// (matching the Login handler's behavior).
func (h *Handler) tokenExpiry() time.Duration {
	const defaultExpiry = 24 * time.Hour
	if h.Config == nil || h.Config.Auth.TokenExpiry == "" {
		return defaultExpiry
	}
	d, err := time.ParseDuration(h.Config.Auth.TokenExpiry)
	if err != nil || d <= 0 {
		return defaultExpiry
	}
	return d
}

// StartRefreshTokenRetention prunes expired refresh tokens on a 1h tick.
// Same shape as the audit/alert retention starters in the cmd/sfpanel
// boot sequence.
func StartRefreshTokenRetention(ctx context.Context, db *sql.DB) {
	go func() {
		pruneRefreshTokens(db)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneRefreshTokens(db)
			}
		}
	}()
}

func pruneRefreshTokens(db *sql.DB) {
	if _, err := db.Exec(
		`DELETE FROM refresh_tokens WHERE expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		slog.Warn("refresh token retention prune failed", "error", err)
	}
}
