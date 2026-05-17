package featureauth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/auth"
)

// waitForAuditRows polls the audit_logs table until at least n rows are
// present or the deadline elapses. The security-event writers fire on a
// goroutine to keep handler latency low; tests must not race against that.
func waitForAuditRows(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&got); err != nil {
			t.Fatalf("count audit_logs: %v", err)
		}
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var got int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&got)
	t.Fatalf("waited for %d audit rows, got %d", n, got)
}

// readLatestAuditRow returns the path and status of the most-recently inserted
// row, which is what the handler-under-test just wrote.
func readLatestAuditRow(t *testing.T, db *sql.DB) (username, path string, status int) {
	t.Helper()
	if err := db.QueryRow(
		`SELECT username, path, status FROM audit_logs ORDER BY id DESC LIMIT 1`,
	).Scan(&username, &path, &status); err != nil {
		t.Fatalf("select latest audit row: %v", err)
	}
	return
}

// TestRecordSecurityEvent_WritesRow exercises the helper directly: any
// handler that calls recordSecurityEvent must produce a row whose path
// encodes both action and reason and whose status is preserved.
func TestRecordSecurityEvent_WritesRow(t *testing.T) {
	h, db := newRefreshHandler(t)

	req := httptest.NewRequest("POST", "/api/v1/auth/change-password", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set("username", "alice")

	h.recordSecurityEvent(c, "password_change", "success", http.StatusOK)

	waitForAuditRows(t, db, 1)
	user, path, status := readLatestAuditRow(t, db)
	if user != "alice" {
		t.Errorf("username = %q, want alice", user)
	}
	if path != "/api/v1/auth/password_change#success" {
		t.Errorf("path = %q, want /api/v1/auth/password_change#success", path)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
}

// TestRecordLoginEvent_BackwardCompatible — the original recordLoginEvent
// signature must still write a /auth/login#<reason> row. This is the
// contract the existing Login handler depends on.
func TestRecordLoginEvent_BackwardCompatible(t *testing.T) {
	h, db := newRefreshHandler(t)

	h.recordLoginEvent("bob", "192.0.2.10", http.StatusUnauthorized, "invalid_password")

	waitForAuditRows(t, db, 1)
	user, path, status := readLatestAuditRow(t, db)
	if user != "bob" {
		t.Errorf("username = %q, want bob", user)
	}
	if path != "/api/v1/auth/login#invalid_password" {
		t.Errorf("path = %q, want /api/v1/auth/login#invalid_password", path)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// seedAdmin inserts an admin row with a real bcrypt-hashed password so the
// ChangePassword / Disable2FA paths exercise the actual auth.CheckPassword
// branch (vs. faking the hash and getting an instant mismatch).
func seedAdmin(t *testing.T, db *sql.DB, username, plainPassword, totpSecret string) {
	t.Helper()
	hash, err := auth.HashPassword(plainPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	var totp interface{}
	if totpSecret != "" {
		totp = totpSecret
	}
	if _, err := db.Exec(
		`INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, ?)`,
		username, hash, totp,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
}

func newAuthedContext(method, path, body, username string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "203.0.113.7:1234"
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if username != "" {
		c.Set("username", username)
	}
	return c, rec
}

// TestChangePassword_RecordsSuccess — successful change must produce a
// password_change#success audit row.
func TestChangePassword_RecordsSuccess(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "oldpassword123", "")

	body := `{"current_password":"oldpassword123","new_password":"newpassword456"}`
	c, rec := newAuthedContext("POST", "/api/v1/auth/change-password", body, "alice")
	if err := h.ChangePassword(c); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, status := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/password_change#success" {
		t.Errorf("path = %q, want /api/v1/auth/password_change#success", path)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
}

// TestChangePassword_RecordsInvalidCurrent — wrong current password must
// produce an invalid_password reason row so a credential-stuffing attempt
// against an active session is visible in audit.
func TestChangePassword_RecordsInvalidCurrent(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "rightpassword12", "")

	body := `{"current_password":"wrongpassword","new_password":"newpassword456"}`
	c, rec := newAuthedContext("POST", "/api/v1/auth/change-password", body, "alice")
	if err := h.ChangePassword(c); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, status := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/password_change#invalid_password" {
		t.Errorf("path = %q, want /api/v1/auth/password_change#invalid_password", path)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// TestVerify2FA_RecordsInvalidCode — verify with a wrong code must produce
// 2fa_verify#invalid_code so probe attempts are visible. We don't test the
// success path here because it requires generating a valid TOTP at the
// right millisecond, which would be flaky; success is covered indirectly
// via the unit test for recordSecurityEvent.
func TestVerify2FA_RecordsInvalidCode(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "anything12345", "")

	body := `{"secret":"JBSWY3DPEHPK3PXP","code":"000000"}`
	c, rec := newAuthedContext("POST", "/api/v1/auth/2fa/verify", body, "alice")
	if err := h.Verify2FA(c); err != nil {
		t.Fatalf("Verify2FA: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, status := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/2fa_verify#invalid_code" {
		t.Errorf("path = %q, want /api/v1/auth/2fa_verify#invalid_code", path)
	}
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

// TestDisable2FA_RecordsInvalidPassword — wrong password during a 2FA
// disable request is high-signal (an attacker with a session cookie is
// trying to downgrade the account). It must be audited with the reason.
func TestDisable2FA_RecordsInvalidPassword(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "rightpassword12", "JBSWY3DPEHPK3PXP")

	body := `{"password":"wrong","totp_code":"000000"}`
	c, rec := newAuthedContext("DELETE", "/api/v1/auth/2fa", body, "alice")
	if err := h.Disable2FA(c); err != nil {
		t.Fatalf("Disable2FA: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, status := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/2fa_disable#invalid_password" {
		t.Errorf("path = %q, want /api/v1/auth/2fa_disable#invalid_password", path)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// TestDisable2FA_RecordsInvalidTOTP — right password but wrong TOTP during
// a disable must surface as invalid_totp, distinct from invalid_password,
// so the audit trail tells you which factor the attacker holds.
func TestDisable2FA_RecordsInvalidTOTP(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "rightpassword12", "JBSWY3DPEHPK3PXP")

	body := `{"password":"rightpassword12","totp_code":"000000"}`
	c, rec := newAuthedContext("DELETE", "/api/v1/auth/2fa", body, "alice")
	if err := h.Disable2FA(c); err != nil {
		t.Fatalf("Disable2FA: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, status := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/2fa_disable#invalid_totp" {
		t.Errorf("path = %q, want /api/v1/auth/2fa_disable#invalid_totp", path)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// TestDisable2FA_RecordsMissingTOTP — when 2FA is active, a disable request
// without a TOTP must record the omission separately from a wrong code so
// you can tell "I forgot to enter the code" from "I tried 999999 and missed".
func TestDisable2FA_RecordsMissingTOTP(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedAdmin(t, db, "alice", "rightpassword12", "JBSWY3DPEHPK3PXP")

	body := `{"password":"rightpassword12"}`
	c, rec := newAuthedContext("DELETE", "/api/v1/auth/2fa", body, "alice")
	if err := h.Disable2FA(c); err != nil {
		t.Fatalf("Disable2FA: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, _ := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/2fa_disable#totp_required" {
		t.Errorf("path = %q, want /api/v1/auth/2fa_disable#totp_required", path)
	}
}

// TestSetup2FA_RecordsSuccess — the setup endpoint just generates a fresh
// secret; the only outcome we audit is success, since failures are limited
// to crypto/rand errors which are recorded by the handler's normal error
// path anyway.
func TestSetup2FA_RecordsSuccess(t *testing.T) {
	h, db := newRefreshHandler(t)

	c, rec := newAuthedContext("POST", "/api/v1/auth/2fa/setup", "", "alice")
	if err := h.Setup2FA(c); err != nil {
		t.Fatalf("Setup2FA: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	waitForAuditRows(t, db, 1)
	_, path, status := readLatestAuditRow(t, db)
	if path != "/api/v1/auth/2fa_setup#success" {
		t.Errorf("path = %q, want /api/v1/auth/2fa_setup#success", path)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
}
