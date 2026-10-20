package featureauth

import (
	"errors"
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// stepUpFrom runs VerifyStepUp for username as a request from ip would.
func stepUpFrom(h *Handler, ip, username, password string) error {
	c, _ := newAuthedContext(http.MethodPost, "/api/v1/appstore/apps/demo/install", "", username)
	c.Request().RemoteAddr = ip + ":1234"
	return h.VerifyStepUp(c, password)
}

// newStepUpHandler is a panel whose admin "alice" has password pw, hashed at
// bcrypt's lowest cost so a test can check it many times.
func newStepUpHandler(t *testing.T, pw string) *Handler {
	t.Helper()
	h := newAuthHandlerForTest(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.DB.Exec(`INSERT INTO admin (username, password) VALUES (?, ?)`, "alice", string(hash)); err != nil {
		t.Fatal(err)
	}
	return h
}

// Each refusal says why, and none is a 401: the browser reads that as its
// session ending and logs the operator out.
func TestVerifyStepUpSaysWhyItRefuses(t *testing.T) {
	h := newStepUpHandler(t, "correct-horse-9")
	const ip = "198.51.100.41"
	loginAttempts.Delete(ip)
	t.Cleanup(func() { loginAttempts.Delete(ip) })
	for _, tc := range []struct {
		name, username, password string
		status                   int
		code                     string
	}{
		{"no operator", "", "correct-horse-9", http.StatusForbidden, response.ErrInvalidCredentials},
		{"no password", "alice", "", http.StatusBadRequest, response.ErrMissingFields},
		{"no account", "bob", "correct-horse-9", http.StatusForbidden, response.ErrInvalidCredentials},
		{"a wrong password", "alice", "wrong", http.StatusForbidden, response.ErrInvalidPassword},
	} {
		var se *auth.StepUpError
		if err := stepUpFrom(h, ip, tc.username, tc.password); !errors.As(err, &se) || se.Status != tc.status || se.Code != tc.code {
			t.Errorf("%s: %v (%+v)", tc.name, err, se)
		}
	}
	if err := stepUpFrom(h, ip, "alice", "correct-horse-9"); err != nil {
		t.Fatalf("the right password: %v", err)
	}
}

// Guesses here count against the login limit of the caller's address — the
// re-auth's bcrypt had no limit at all — and the operator's own successes do
// not add up to a lockout: every retry of a refused compose file asks again.
func TestVerifyStepUpCountsAgainstTheLoginLimit(t *testing.T) {
	h := newStepUpHandler(t, "correct-horse-9")
	const ip = "198.51.100.42"
	loginAttempts.Delete(ip)
	t.Cleanup(func() { loginAttempts.Delete(ip) })
	for i := 0; i <= rateLimitMaxAttempts; i++ {
		if err := stepUpFrom(h, ip, "alice", "correct-horse-9"); err != nil {
			t.Fatalf("success %d: %v", i+1, err)
		}
	}
	for i := 0; i < rateLimitMaxAttempts; i++ {
		_ = stepUpFrom(h, ip, "alice", "wrong")
	}
	var se *auth.StepUpError
	if err := stepUpFrom(h, ip, "alice", "correct-horse-9"); !errors.As(err, &se) || se.Status != http.StatusTooManyRequests || se.Code != response.ErrRateLimited {
		t.Fatalf("after %d wrong passwords: %v", rateLimitMaxAttempts, err)
	}
}
