package featureauth

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// ResetPassword sets an account's password without the current one. It is
// `sudo sfpanel reset-password`, and nothing else may call it: an operator
// who has forgotten the password has no other way back in short of deleting
// the database, which on a cluster member throws away its membership too.
//
// The caller must be the sfpanel command itself (auth.CLIUsername), whose
// token is signed with the JWT secret only root on a manager can read. A
// browser session is refused: the HTTP API never sets a password without the
// current one. That is not a boundary against a stolen session — the panel's
// root terminal opens on a session alone, and the command runs there — only
// the API keeping the rule /auth/change-password keeps. Through the cluster
// the caller's name travels as the proxy's Original-User, which the sending
// node sets from its own authenticated session, never from the request.
func (h *Handler) ResetPassword(c echo.Context) error {
	if caller, _ := c.Get("username").(string); caller != auth.CLIUsername {
		// The audit middleware skips this route, so the refusal is recorded
		// here: a session reaching for it is worth seeing.
		h.recordSecurityEvent(c, "password_reset", "not_cli", http.StatusForbidden)
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied,
			"Only `sudo sfpanel reset-password` on a manager can reset a password")
	}
	// An account that carries the command's name, made before setup refused
	// it, would make its own sessions look like the command.
	if _, _, _, err := h.loadAdminAccount(auth.CLIUsername); err == nil {
		h.recordSecurityEvent(c, "password_reset", "reserved_name", http.StatusForbidden)
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied,
			"An account is named "+auth.CLIUsername+"; reset is refused while it exists")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	// The account lives in the cluster FSM, which only the leader writes.
	if handled, err := middleware.ProxyToLeader(c, h.getClusterMgr()); handled {
		return err
	}

	var req struct {
		Username    string `json:"username"`
		NewPassword string `json:"new_password"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.NewPassword == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "New password is required")
	}
	if msg, ok := validatePassword(req.NewPassword); !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrWeakPassword, msg)
	}

	username := req.Username
	if username == "" {
		names, err := h.accountNames()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
		}
		if len(names) != 1 {
			return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields,
				"Name the account to reset (--user); this panel has more than one, or none")
		}
		username = names[0]
	}

	_, totpSecret, fromCluster, err := h.loadAdminAccount(username)
	if errors.Is(err, sql.ErrNoRows) {
		return response.Fail(c, http.StatusNotFound, response.ErrUserNotFound, "User not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHashError, "Failed to hash new password")
	}
	// Two-factor stays as it is: this restores a forgotten password, not a
	// lost device, and recovery codes already cover the device.
	if err := h.persistAdminAccount(username, newHash, totpSecret, fromCluster); err != nil {
		return h.failClusterPersist(c, err)
	}

	// The command has no session of its own, so every refresh chain this
	// node holds for the account ends, as after a password change.
	revokeOtherSessions(c, h.DB, username)

	// The row names the account whose password changed; the command is the
	// reason, since the caller is always the command.
	h.insertSecurityAuditRow(username, c.RealIP(), "password_reset", "cli", http.MethodPost, http.StatusOK,
		c.Request().Header.Get("X-SFPanel-Original-Node"))
	return response.OK(c, map[string]string{"username": username})
}

// accountNames lists the accounts a login can use: the cluster's when this
// node is in one that holds any, else the local table's — the order
// loadAdminAccount reads them in.
func (h *Handler) accountNames() ([]string, error) {
	if mgr := h.getClusterMgr(); mgr != nil {
		if accts := mgr.GetAccounts(); len(accts) > 0 {
			names := make([]string, 0, len(accts))
			for name := range accts {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, nil
		}
	}
	rows, err := h.DB.Query("SELECT username FROM admin ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
