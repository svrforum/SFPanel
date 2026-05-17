package settings

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

type Handler struct {
	DB *sql.DB
}

// defaults for settings that haven't been saved yet.
var settingDefaults = map[string]string{
	"terminal_timeout": "30",
	"max_upload_size":  "1024",
}

// userSettableKeys is the explicit allowlist of keys writable via
// PUT /settings. Other modules (e.g. appstore writes appstore_cache) own
// their own keys and write directly via DB; that path stays open.
//
// Without an allowlist here, an admin (or XSS riding their session) could
// poison appstore_cache (subsequent unmarshal is unchecked), overwrite
// terminal_timeout=99999999 (DoS), or grow the settings table unbounded
// with arbitrary garbage keys. The list is intentionally short — adding a
// new user-settable setting should be a deliberate code change, not a
// dynamic API surface.
var userSettableKeys = map[string]bool{
	"terminal_timeout": true,
	"max_upload_size":  true,
}

func (h *Handler) GetSettings(c echo.Context) error {
	result := make(map[string]string)

	// Start from defaults
	for k, v := range settingDefaults {
		result[k] = v
	}

	// Override with DB values
	rows, err := h.DB.Query("SELECT key, value FROM settings")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to read settings")
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		result[key] = value
	}

	return response.OK(c, result)
}

type updateSettingsRequest struct {
	Settings map[string]string `json:"settings"`
}

func (h *Handler) UpdateSettings(c echo.Context) error {
	var req updateSettingsRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if len(req.Settings) == 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrEmptySettings, "No settings provided")
	}

	// Validate all keys against the user-settable allowlist BEFORE writing
	// anything, so a mixed-valid batch fails atomically (no partial state).
	for key, value := range req.Settings {
		if !userSettableKeys[key] {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
				fmt.Sprintf("Setting key %q is not user-settable via this endpoint", key))
		}
		if len(value) > 1000 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
				fmt.Sprintf("Value for %q exceeds maximum length of 1000 characters", key))
		}
	}

	for key, value := range req.Settings {
		_, err := h.DB.Exec(
			"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			key, value,
		)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to save settings")
		}
	}

	return response.OK(c, map[string]string{"message": "Settings updated"})
}

// GetSetting reads a single setting from the DB, returning the default if not found.
func GetSetting(db *sql.DB, key string) string {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if def, ok := settingDefaults[key]; ok {
			return def
		}
		return ""
	}
	return value
}
