package featureauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
)

const recoveryCodeCount = 10

// recoveryCodeAlphabet excludes ambiguous characters (0/o, 1/i/l) so operators
// can read codes off a printout without transcription errors.
const recoveryCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// generateRecoveryCodes returns recoveryCodeCount fresh codes (shown to the
// operator once) plus their hashes (what we store). Each code is two 5-char
// groups joined by a hyphen, ~49 bits of entropy.
func generateRecoveryCodes() (plaintext, hashes []string, err error) {
	plaintext = make([]string, recoveryCodeCount)
	hashes = make([]string, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		var sb strings.Builder
		for j, b := range raw {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryCodeAlphabet[int(b)%len(recoveryCodeAlphabet)])
		}
		code := sb.String()
		plaintext[i] = code
		hashes[i] = hashRecoveryCode(code)
	}
	return plaintext, hashes, nil
}

// normalizeRecoveryCode strips formatting so "ABCDE-FGHIJ", "abcde fghij" and
// "abcdefghij" all hash identically.
func normalizeRecoveryCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

// hashRecoveryCode hashes a code for storage. Recovery codes are high-entropy
// (unlike user passwords), so a fast SHA-256 is sufficient and avoids a slow
// bcrypt comparison per code on the login path.
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// loadRecoveryCodes returns the stored hashes and whether the account is
// cluster-replicated (so persistRecoveryCodes writes back to the same store).
func (h *Handler) loadRecoveryCodes(username string) (hashes []string, fromCluster bool, err error) {
	if acct := h.getClusterAccount(username); acct != nil {
		if mgr := h.getClusterMgr(); mgr != nil {
			return mgr.GetRecoveryCodes(username), true, nil
		}
		return nil, true, nil
	}
	var raw sql.NullString
	if err := h.DB.QueryRow("SELECT recovery_codes FROM admin WHERE username = ?", username).Scan(&raw); err != nil {
		return nil, false, err
	}
	if raw.Valid && raw.String != "" {
		_ = json.Unmarshal([]byte(raw.String), &hashes)
	}
	return hashes, false, nil
}

// persistRecoveryCodes writes the hash list back. Cluster accounts replicate via
// Raft (leader-only — followers get ErrNotLeader); local accounts UPDATE the
// admin row.
func (h *Handler) persistRecoveryCodes(username string, hashes []string, fromCluster bool) error {
	if fromCluster {
		mgr := h.getClusterMgr()
		if mgr == nil {
			return errors.New("cluster account requires an active cluster manager")
		}
		return mgr.SetRecoveryCodes(username, hashes)
	}
	var val interface{}
	if len(hashes) > 0 {
		b, err := json.Marshal(hashes)
		if err != nil {
			return err
		}
		val = string(b)
	}
	_, err := h.DB.Exec("UPDATE admin SET recovery_codes = ? WHERE username = ?", val, username)
	return err
}

// consumeRecoveryCode checks code against the stored hashes; on a match it
// removes that hash (persisting the shrunk list) and returns true. A non-match
// returns (false, nil); a persistence failure returns the error (e.g.
// cluster.ErrNotLeader when a follower tries to consume a cluster account's
// code).
func (h *Handler) consumeRecoveryCode(username, code string) (bool, error) {
	hashes, fromCluster, err := h.loadRecoveryCodes(username)
	if err != nil {
		return false, err
	}
	target := hashRecoveryCode(code)
	idx := -1
	for i, hh := range hashes {
		if subtle.ConstantTimeCompare([]byte(hh), []byte(target)) == 1 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	remaining := make([]string, 0, len(hashes)-1)
	remaining = append(remaining, hashes[:idx]...)
	remaining = append(remaining, hashes[idx+1:]...)
	if err := h.persistRecoveryCodes(username, remaining, fromCluster); err != nil {
		return false, err
	}
	return true, nil
}

// RegenerateRecoveryCodes generates a fresh set of recovery codes (invalidating
// any previous set), requiring 2FA to be enabled first. Returns the plaintext
// codes once — they are not retrievable afterwards.
// POST /auth/2fa/recovery
func (h *Handler) RegenerateRecoveryCodes(c echo.Context) error {
	// Cluster admin recovery codes replicate via Raft, so only the leader can
	// Apply. Followers forward the whole request to the leader.
	if handled, err := middleware.ProxyToLeader(c, h.getClusterMgr()); handled {
		return err
	}

	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrMissingToken, "No authenticated user")
	}

	_, totpSecret, fromCluster, err := h.loadAdminAccount(username)
	if errors.Is(err, sql.ErrNoRows) {
		return response.Fail(c, http.StatusNotFound, response.ErrUserNotFound, "User not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	if totpSecret == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			"Enable 2FA before generating recovery codes")
	}

	plaintext, hashes, err := generateRecoveryCodes()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Failed to generate codes")
	}
	if err := h.persistRecoveryCodes(username, hashes, fromCluster); err != nil {
		return h.failClusterPersist(c, err)
	}

	h.recordSecurityEvent(c, "2fa_recovery_regenerate", "success", http.StatusOK)
	return response.OK(c, map[string]interface{}{"codes": plaintext})
}

// GetRecoveryStatus reports whether recovery codes exist and how many remain.
// GET /auth/2fa/recovery/status
func (h *Handler) GetRecoveryStatus(c echo.Context) error {
	username, _ := c.Get("username").(string)
	if username == "" {
		return response.Fail(c, http.StatusUnauthorized, response.ErrMissingToken, "No authenticated user")
	}
	hashes, _, err := h.loadRecoveryCodes(username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	return response.OK(c, map[string]interface{}{
		"generated": len(hashes) > 0,
		"remaining": len(hashes),
	})
}
