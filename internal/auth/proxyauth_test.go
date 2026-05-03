package auth

import (
	"net/http"
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

	r2, _ := http.NewRequest("POST", "http://localhost/api/v1/system/update", nil)
	r2.Header.Set(InternalProxyHeaderV2, header)
	if IsInternalProxyRequest(r2) {
		t.Error("replay (same nonce, same path) should be rejected")
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

func TestV1Compat_StillWorks(t *testing.T) {
	resetForTest("test-secret-32-bytes-long-enough!!")

	r, _ := http.NewRequest("GET", "http://localhost/x", nil)
	r.Header.Set(InternalProxyHeader, "test-secret-32-bytes-long-enough!!")
	if !IsInternalProxyRequest(r) {
		t.Error("v1 static-secret path should still validate (backward compat)")
	}
}

func TestV1Compat_RejectsWrongSecret(t *testing.T) {
	resetForTest("the-real-secret")

	r, _ := http.NewRequest("GET", "http://localhost/x", nil)
	r.Header.Set(InternalProxyHeader, "wrong-secret")
	if IsInternalProxyRequest(r) {
		t.Error("v1 with wrong secret should be rejected")
	}
}

func TestSignProxyRequestV2_NoSecretReturnsEmpty(t *testing.T) {
	resetForTest("")
	if SignProxyRequestV2("GET", "/x") != "" {
		t.Error("no secret configured → empty signature")
	}
}
