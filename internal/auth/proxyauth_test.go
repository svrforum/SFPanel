package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// resetForTest clears global state between tests.
func resetForTest(secret string) {
	SetClusterProxySecret(secret)
	nonceCacheMu.Lock()
	nonceCache = make(map[string]time.Time)
	nonceCacheMu.Unlock()
}

func TestSignAndValidateV2_RoundTrip(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	header := SignProxyRequestV2("GET", "/api/v1/system/info")
	if header == "" {
		t.Fatal("SignProxyRequestV2 returned empty")
	}
	parts := strings.Split(header, ":")
	if len(parts) != 3 {
		t.Errorf("expected 3 parts, got %d", len(parts))
	}

	r, _ := http.NewRequest("GET", "http://localhost/api/v1/system/info", nil)
	r.Header.Set(InternalProxyHeaderV2, header)
	if !IsInternalProxyRequest(r) {
		t.Error("freshly-signed v2 header should validate")
	}
}

func TestValidateV2_RejectsReplay(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	header := SignProxyRequestV2("POST", "/api/v1/system/update")
	r1, _ := http.NewRequest("POST", "http://localhost/api/v1/system/update", nil)
	r1.Header.Set(InternalProxyHeaderV2, header)
	if !IsInternalProxyRequest(r1) {
		t.Fatal("first use should validate")
	}

	// Wait past the sameRequestWindow grace period. Within the window a
	// repeated nonce is accepted as a same-request re-check (JWT then
	// CSRF middleware on the same inbound request); outside it must be
	// rejected as a genuine replay — a network replay attacker can't
	// fit a captured-then-replayed request inside the window.
	time.Sleep(sameRequestWindow + 10*time.Millisecond)

	r2, _ := http.NewRequest("POST", "http://localhost/api/v1/system/update", nil)
	r2.Header.Set(InternalProxyHeaderV2, header)
	if IsInternalProxyRequest(r2) {
		t.Error("replay (same nonce, same path) outside grace window should be rejected")
	}
}

func TestValidateV2_RejectsRebound(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	// MAC was computed for /api/v1/system/info; replaying it on /api/v1/cluster/disband
	// would let an attacker re-target a captured proxy header.
	header := SignProxyRequestV2("POST", "/api/v1/system/info")
	r, _ := http.NewRequest("POST", "http://localhost/api/v1/cluster/disband", nil)
	r.Header.Set(InternalProxyHeaderV2, header)
	if IsInternalProxyRequest(r) {
		t.Error("rebound request (different path) should be rejected")
	}
}

func TestValidateV2_RejectsStaleTimestamp(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	// Forge a header with a timestamp 5 minutes ago.
	stale := strconv.FormatInt(time.Now().Add(-5*time.Minute).UnixMilli(), 10)
	hdr := stale + ":" + "abcd1234ef567890" + ":" + "00" // bogus mac, won't matter

	r, _ := http.NewRequest("GET", "http://localhost/x", nil)
	r.Header.Set(InternalProxyHeaderV2, hdr)
	if IsInternalProxyRequest(r) {
		t.Error("timestamp 5m old should be rejected outright")
	}
}

// TestV1NoLongerAccepted pins the v0.57.0 removal: a request carrying the
// CORRECT secret in the legacy v1 static-secret header — the strongest v1
// credential possible — must be rejected. Only the v2 HMAC authenticates.
func TestV1NoLongerAccepted(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	r, _ := http.NewRequest("GET", "http://localhost/x", nil)
	r.Header.Set(InternalProxyHeader, "test-secret-32-bytes-long-enough!!")
	if IsInternalProxyRequest(r) {
		t.Error("v1 static-secret header must no longer authenticate (removed v0.57.0)")
	}
}

func TestSignProxyRequestV2_NoSecretReturnsEmpty(t *testing.T) {
	resetForTest("")
	if SignProxyRequestV2("GET", "/x") != "" {
		t.Error("no secret configured → empty signature")
	}
}

// TestSignAndValidateV2_WithQueryString — regression guard. Every cluster
// proxy signer (grpc_server.go:297, feature/cluster/handler.go:707) signs
// path-with-query so a captured header can't be re-targeted to different
// query params. The validator previously fed `r.URL.Path` (path component
// only) into the MAC check, producing a mismatch and silently rejecting
// every proxied request whose URL carried a query string. Dashboard's
// `/logs/read?source=syslog&lines=8` was the first user-visible casualty —
// after a cluster proxy hop those 401s pushed the SPA back to /login.
func TestSignAndValidateV2_WithQueryString(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	header := SignProxyRequestV2("GET", "/api/v1/logs/read?source=syslog&lines=8")
	if header == "" {
		t.Fatal("SignProxyRequestV2 returned empty")
	}

	r, _ := http.NewRequest("GET", "http://localhost/api/v1/logs/read?source=syslog&lines=8", nil)
	r.Header.Set(InternalProxyHeaderV2, header)
	if !IsInternalProxyRequest(r) {
		t.Error("freshly-signed v2 header should validate when URL carries a query string")
	}
}

// TestValidateV2_QueryParamRebinding — flipping a query param on the
// receiver side must invalidate the MAC. Same security goal as the
// path-rebinding test above, just for query string tampering.
func TestValidateV2_QueryParamRebinding(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	header := SignProxyRequestV2("GET", "/api/v1/logs/read?source=syslog&lines=8")
	r, _ := http.NewRequest("GET", "http://localhost/api/v1/logs/read?source=auth&lines=5000", nil)
	r.Header.Set(InternalProxyHeaderV2, header)
	if IsInternalProxyRequest(r) {
		t.Error("v2 header signed for one query must not validate against a different query")
	}
}

// TestIsInternalProxyRequest_RepeatedCallsWithinRequest pins the
// sameRequestWindow grace period. JWT middleware and CSRF middleware
// each call IsInternalProxyRequest on the same inbound request; before
// the grace window the second call hit the nonce-replay cache and
// reject, breaking every cross-node POST that relied on the
// internal-proxy bypass. Two middleware re-checks happen in microseconds
// — well under sameRequestWindow — so both should accept.
func TestIsInternalProxyRequest_RepeatedCallsWithinRequest(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	v2 := SignProxyRequestV2("POST", "/api/v1/system/update")
	if v2 == "" {
		t.Fatal("SignProxyRequestV2 returned empty")
	}
	req, _ := http.NewRequest("POST", "http://localhost/api/v1/system/update", nil)
	req.Header.Set(InternalProxyHeaderV2, v2)

	if !IsInternalProxyRequest(req) {
		t.Fatal("first call returned false")
	}
	if !IsInternalProxyRequest(req) {
		t.Errorf("second call returned false — same-request re-check by JWT+CSRF would fail (regression)")
	}
}

func signWith(t *testing.T, secret, method, path string) string {
	t.Helper()
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + ":" + nonce + ":" + method + ":" + path))
	return ts + ":" + nonce + ":" + hex.EncodeToString(mac.Sum(nil))
}

// While the cluster moves off the CA-derived secret, a node accepts both
// and signs with one.
func TestInternalProxyAcceptsEverySecretItWasGiven(t *testing.T) {
	SetProxySecrets("new", []string{"new", "old"})
	t.Cleanup(func() { SetClusterProxySecret("") })
	for _, secret := range []string{"new", "old"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
		r.Header.Set(InternalProxyHeaderV2, signWith(t, secret, http.MethodGet, "/api/v1/system/info"))
		if !IsInternalProxyRequest(r) {
			t.Errorf("header signed with %q refused", secret)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	r.Header.Set(InternalProxyHeaderV2, signWith(t, "stranger", http.MethodGet, "/api/v1/system/info"))
	if IsInternalProxyRequest(r) {
		t.Error("header signed with an unknown secret accepted")
	}
}

func TestSignProxyRequestUsesTheSigningSecret(t *testing.T) {
	SetProxySecrets("new", []string{"new", "old"})
	t.Cleanup(func() { SetClusterProxySecret("") })
	header := SignProxyRequestV2(http.MethodGet, "/x")
	SetProxySecrets("old", []string{"old"})
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set(InternalProxyHeaderV2, header)
	if IsInternalProxyRequest(r) {
		t.Fatal("signed with a secret other than the signing one")
	}
}

// A header made with an agent's key validates only where that key is
// accepted. A manager's secret must not validate it, and the agent's key must
// not validate one made with the manager's secret: either would let one side
// replay the other's credential.
func TestSignWithChosenKey(t *testing.T) {
	t.Cleanup(func() { SetProxySecrets("", nil) })
	path := "/api/v1/system/info"
	agentHdr := SignProxyRequestV2With("agent-key", http.MethodGet, path)
	if agentHdr == "" {
		t.Fatal("no header")
	}
	req := func(h string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set(InternalProxyHeaderV2, h)
		return r
	}
	SetProxySecrets("agent-key", []string{"agent-key"})
	if !IsInternalProxyRequest(req(agentHdr)) {
		t.Fatal("the agent refused a header made with its own key")
	}
	SetProxySecrets("manager-secret", []string{"manager-secret"})
	if IsInternalProxyRequest(req(SignProxyRequestV2With("agent-key", http.MethodGet, path))) {
		t.Fatal("a manager accepted a header made with an agent's key")
	}
	SetProxySecrets("agent-key", []string{"agent-key"})
	if IsInternalProxyRequest(req(SignProxyRequestV2With("manager-secret", http.MethodGet, path))) {
		t.Fatal("an agent accepted a header made with the manager secret")
	}
	if SignProxyRequestV2With("", http.MethodGet, path) != "" {
		t.Fatal("signed with an empty key")
	}
}
