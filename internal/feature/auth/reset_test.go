package featureauth

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/auth"
)

const resetNew = "a-brand-new-password-42"

func resetAs(t *testing.T, h *Handler, caller, body string) (int, string, string) {
	t.Helper()
	c, rec := newAuthedContext("POST", "/api/v1/auth/reset-password", body, caller)
	if err := h.ResetPassword(c); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	var env struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Username != "" {
		return rec.Code, env.Data.Username, ""
	}
	return rec.Code, env.Error.Code, env.Error.Message
}

func storedHash(t *testing.T, db *sql.DB, username string) (string, string) {
	t.Helper()
	var hash string
	var totp sql.NullString
	if err := db.QueryRow(`SELECT password, totp_secret FROM admin WHERE username = ?`, username).Scan(&hash, &totp); err != nil {
		t.Fatalf("read %s: %v", username, err)
	}
	return hash, totp.String
}

func TestResetPassword_TheCommandSetsANewPassword(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")
	if _, err := issueRefreshToken(db, "alice"); err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}

	code, user, msg := resetAs(t, h, auth.CLIUsername, `{"username":"alice","new_password":"`+resetNew+`"}`)
	if code != http.StatusOK || user != "alice" {
		t.Fatalf("status=%d user=%q msg=%q, want 200 alice", code, user, msg)
	}
	hash, _ := storedHash(t, db, "alice")
	if !auth.CheckPassword(resetNew, hash) {
		t.Error("the new password does not sign in")
	}
	if auth.CheckPassword("oldpassword123", hash) {
		t.Error("the old password still signs in")
	}
	var sessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE username = 'alice'`).Scan(&sessions); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if sessions != 0 {
		t.Errorf("%d refresh chains survived the reset, want 0", sessions)
	}
	waitForAuditRows(t, db, 1)
	who, path, status := readLatestAuditRow(t, db)
	if who != "alice" || path != "/api/v1/auth/password_reset#cli" || status != http.StatusOK {
		t.Errorf("audit row = %q %q %d, want alice /api/v1/auth/password_reset#cli 200", who, path, status)
	}
}

func TestResetPassword_RefusesABrowserSession(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")
	before, _ := storedHash(t, db, "alice")

	code, errCode, msg := resetAs(t, h, "alice", `{"username":"alice","new_password":"`+resetNew+`"}`)
	if code != http.StatusForbidden || errCode != "PERMISSION_DENIED" || !strings.Contains(msg, "sudo sfpanel reset-password") {
		t.Fatalf("status=%d code=%q msg=%q, want 403 PERMISSION_DENIED naming the command", code, errCode, msg)
	}
	if after, _ := storedHash(t, db, "alice"); after != before {
		t.Error("a browser session changed the password without the current one")
	}
	waitForAuditRows(t, db, 1)
	if who, path, status := readLatestAuditRow(t, db); who != "alice" || path != "/api/v1/auth/password_reset#not_cli" || status != http.StatusForbidden {
		t.Errorf("audit row = %q %q %d, want alice /api/v1/auth/password_reset#not_cli 403", who, path, status)
	}
}

func TestResetPassword_RefusesWhileAnAccountHasTheCommandsName(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")
	seedAdmin(t, db, auth.CLIUsername, "whatever-it-is-123", "")
	before, _ := storedHash(t, db, "alice")

	code, errCode, msg := resetAs(t, h, auth.CLIUsername, `{"username":"alice","new_password":"`+resetNew+`"}`)
	if code != http.StatusForbidden || errCode != "PERMISSION_DENIED" || !strings.Contains(msg, "An account is named") {
		t.Fatalf("status=%d code=%q msg=%q, want 403 for the look-alike account", code, errCode, msg)
	}
	if after, _ := storedHash(t, db, "alice"); after != before {
		t.Error("the password changed while an account carries the command's name")
	}
	waitForAuditRows(t, db, 1)
	if who, path, status := readLatestAuditRow(t, db); who != auth.CLIUsername || path != "/api/v1/auth/password_reset#reserved_name" || status != http.StatusForbidden {
		t.Errorf("audit row = %q %q %d, want sfpanel-cli /api/v1/auth/password_reset#reserved_name 403", who, path, status)
	}
}

func TestResetPassword_NoNameMeansTheOnlyAccount(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")

	code, user, msg := resetAs(t, h, auth.CLIUsername, `{"new_password":"`+resetNew+`"}`)
	if code != http.StatusOK || user != "alice" {
		t.Fatalf("status=%d user=%q msg=%q, want the only account reset", code, user, msg)
	}

	seedAdmin(t, db, "bob", "oldpassword456", "")
	code, errCode, msg := resetAs(t, h, auth.CLIUsername, `{"new_password":"`+resetNew+`"}`)
	if code != http.StatusBadRequest || errCode != "MISSING_FIELDS" || !strings.Contains(msg, "--user") {
		t.Fatalf("status=%d code=%q msg=%q, want 400 asking for --user", code, errCode, msg)
	}
}

func TestResetPassword_KeepsTwoFactor(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "JBSWY3DPEHPK3PXP")

	if code, _, msg := resetAs(t, h, auth.CLIUsername, `{"username":"alice","new_password":"`+resetNew+`"}`); code != http.StatusOK {
		t.Fatalf("status=%d msg=%q", code, msg)
	}
	if _, totp := storedHash(t, db, "alice"); totp != "JBSWY3DPEHPK3PXP" {
		t.Errorf("two-factor secret = %q after the reset, want it kept", totp)
	}
}

func TestResetPassword_SaysWhatIsWrong(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")
	for _, tc := range []struct {
		body, code string
		status     int
	}{
		{`{"username":"alice","new_password":"short"}`, "WEAK_PASSWORD", http.StatusBadRequest},
		{`{"username":"alice"}`, "MISSING_FIELDS", http.StatusBadRequest},
		{`{"username":"nobody","new_password":"` + resetNew + `"}`, "USER_NOT_FOUND", http.StatusNotFound},
	} {
		if status, code, msg := resetAs(t, h, auth.CLIUsername, tc.body); status != tc.status || code != tc.code {
			t.Errorf("%s: status=%d code=%q msg=%q, want %d %s", tc.body, status, code, msg, tc.status, tc.code)
		}
	}
	hash, _ := storedHash(t, db, "alice")
	if !auth.CheckPassword("oldpassword123", hash) {
		t.Error("a refused reset changed the password")
	}
}
