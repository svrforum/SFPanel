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
// against the username with a fresh family_id, and returns the raw token to
// the client. Each login starts a new family — rotations within that family
// share the family_id so theft detection can revoke the whole chain.
func issueRefreshToken(db *sql.DB, username string) (string, error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	tok := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tok))
	hashHex := hex.EncodeToString(hash[:])

	familyID, err := newFamilyID()
	if err != nil {
		return "", err
	}

	_, err = db.Exec(
		`INSERT INTO refresh_tokens (token_hash, username, family_id, expires_at) VALUES (?, ?, ?, ?)`,
		hashHex, username, familyID, time.Now().Add(refreshTokenLifetime).UTC().Format(time.RFC3339),
	)
	if err != nil {
		return "", fmt.Errorf("persist refresh token: %w", err)
	}
	return tok, nil
}

// newFamilyID returns a 32-hex-char random identifier used to group all
// refresh tokens issued from a single login chain.
func newFamilyID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate family id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// rotateRefreshToken validates a refresh token, deletes it, and issues a
// fresh pair (access JWT + new refresh token). Atomic via tx so a partial
// failure doesn't leave the user holding an unusable token.
//
// Returns ErrInvalidRefreshToken when the token doesn't exist or is expired.
func (h *Handler) Refresh(c echo.Context) error {
	// Prefer the httpOnly cookie. Fall back to the JSON body for older
	// clients that haven't picked up the cookie-based flow yet.
	var refreshTokenRaw string
	if cookie, err := c.Request().Cookie(auth.RefreshCookieName); err == nil && cookie.Value != "" {
		refreshTokenRaw = cookie.Value
	} else {
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.Bind(&req); err == nil {
			refreshTokenRaw = req.RefreshToken
		}
	}
	if refreshTokenRaw == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Missing refresh_token")
	}

	hash := sha256.Sum256([]byte(refreshTokenRaw))
	hashHex := hex.EncodeToString(hash[:])

	tx, err := h.DB.Begin()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	defer tx.Rollback()

	var username, expiresStr, familyID string
	var consumedAt sql.NullString
	err = tx.QueryRow(
		`SELECT username, expires_at, family_id, consumed_at FROM refresh_tokens WHERE token_hash = ?`,
		hashHex,
	).Scan(&username, &expiresStr, &familyID, &consumedAt)
	if err == sql.ErrNoRows {
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "Invalid refresh token")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	// OWASP token-reuse detection: a refresh attempt against a tombstone
	// (already-consumed) row means somebody is replaying a token that the
	// legitimate client has already rotated away from. Treat this as theft
	// and revoke every token in the family so the attacker's chain dies.
	if consumedAt.Valid {
		if familyID != "" {
			_, _ = tx.Exec(`DELETE FROM refresh_tokens WHERE family_id = ?`, familyID)
		} else {
			_, _ = tx.Exec(`DELETE FROM refresh_tokens WHERE username = ?`, username)
		}
		_ = tx.Commit()
		slog.Warn("refresh token reuse detected — revoked family",
			"username", username, "family_id", familyID)
		return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "Session revoked")
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

	// Backward-compat: rows created before migration 24 carry family_id=''.
	// Treat them as their own family by allocating one on first rotation.
	if familyID == "" {
		fid, fErr := newFamilyID()
		if fErr != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "rand failed")
		}
		familyID = fid
	}

	// Rotate: tombstone the consumed token (so a later replay triggers theft
	// detection above) and mint a fresh one in the same family.
	if _, err := tx.Exec(
		`UPDATE refresh_tokens SET consumed_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Format(time.RFC3339), hashHex,
	); err != nil {
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
		`INSERT INTO refresh_tokens (token_hash, username, family_id, expires_at) VALUES (?, ?, ?, ?)`,
		newHashHex, username, familyID, time.Now().Add(refreshTokenLifetime).UTC().Format(time.RFC3339),
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

	// Refresh the cookies on rotation so the cookie always carries the
	// latest refresh token; older cookie-based clients otherwise keep
	// presenting the now-consumed value and would trigger theft detection
	// on next call.
	h.writeAuthCookies(c, newTok)

	return response.OK(c, map[string]interface{}{
		"token":         accessTok,
		"refresh_token": newTok,
		"expires_in":    int(accessExpiry.Seconds()),
	})
}

// MintWSTicket issues a single-use 60s ticket for the calling user. The JS
// client trades the long-lived JWT for a ticket right before opening a
// WebSocket so the JWT itself never appears in the URL — that would land it
// in browser history, Referer headers, and reverse-proxy access logs.
func (h *Handler) MintWSTicket(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrMissingToken, "no username in context")
	}
	ticket := auth.MintWSTicket(username)
	if ticket == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Failed to issue ticket")
	}
	return response.OK(c, map[string]interface{}{
		"ticket":     ticket,
		"expires_in": 60,
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
	// Drop expired tokens AND consumed tombstones older than the rotation
	// grace window. The 24h tombstone retention is long enough to catch a
	// realistic replay (browser tab put to sleep, then resumed) without
	// growing the table indefinitely.
	cutoff := time.Now().UTC()
	if _, err := db.Exec(
		`DELETE FROM refresh_tokens WHERE expires_at < ? OR (consumed_at IS NOT NULL AND consumed_at < ?)`,
		cutoff.Format(time.RFC3339),
		cutoff.Add(-24*time.Hour).Format(time.RFC3339),
	); err != nil {
		slog.Warn("refresh token retention prune failed", "error", err)
	}
}
