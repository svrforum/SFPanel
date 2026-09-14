package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A valid proxy hop whose original-user header is missing must be refused
// with a non-nil error: the callers key PTY sessions by username, and an
// empty name would make every such request share one key.
func TestAuthenticateWSUpgrade_RefusesEmptyProxyUsername(t *testing.T) {
	prev := ClusterProxySecret()
	SetClusterProxySecret("test-secret")
	t.Cleanup(func() { SetClusterProxySecret(prev) })

	req := httptest.NewRequest(http.MethodGet, "/ws/ai/attach", nil)
	req.Header.Set(InternalProxyHeaderV2, SignProxyRequestV2(http.MethodGet, "/ws/ai/attach"))
	rec := httptest.NewRecorder()

	user, err := AuthenticateWSUpgrade(rec, req, "irrelevant")
	if user != "" {
		t.Errorf("user = %q, want empty", user)
	}
	if !errors.Is(err, ErrUnauthenticatedWS) {
		t.Errorf("err = %v, want ErrUnauthenticatedWS", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAuthenticateWSUpgrade_AcceptsProxyUsername(t *testing.T) {
	prev := ClusterProxySecret()
	SetClusterProxySecret("test-secret")
	t.Cleanup(func() { SetClusterProxySecret(prev) })

	req := httptest.NewRequest(http.MethodGet, "/ws/ai/attach", nil)
	req.Header.Set(InternalProxyHeaderV2, SignProxyRequestV2(http.MethodGet, "/ws/ai/attach"))
	req.Header.Set("X-SFPanel-Original-User", "admin")
	rec := httptest.NewRecorder()

	user, err := AuthenticateWSUpgrade(rec, req, "irrelevant")
	if err != nil || user != "admin" {
		t.Fatalf("got (%q, %v), want (admin, nil)", user, err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("a successful check must write nothing; status = %d", rec.Code)
	}
}

func TestAuthenticateWSUpgrade_RefusesMissingCredentials(t *testing.T) {
	prev := ClusterProxySecret()
	SetClusterProxySecret("")
	t.Cleanup(func() { SetClusterProxySecret(prev) })

	req := httptest.NewRequest(http.MethodGet, "/ws/ai/attach", nil)
	rec := httptest.NewRecorder()

	user, err := AuthenticateWSUpgrade(rec, req, "any-secret")
	if user != "" || !errors.Is(err, ErrUnauthenticatedWS) {
		t.Fatalf("got (%q, %v), want (\"\", ErrUnauthenticatedWS)", user, err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
