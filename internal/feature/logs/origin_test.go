package logs

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

// The logs WS upgrader used to allow ALL origins; it now mirrors
// websocket/handler.go's policy (same-origin / empty / Tauri webview).
func TestSameOriginOrEmpty(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin (curl/websocat)", "panel.example.com:9443", "", true},
		{"matching origin", "panel.example.com:9443", "https://panel.example.com:9443", true},
		{"case-insensitive host", "Panel.Example.com:9443", "https://panel.example.com:9443", true},
		{"foreign origin", "panel.example.com:9443", "https://evil.example.com", false},
		{"matching host different port", "panel.example.com:9443", "https://panel.example.com:9444", false},
		{"malformed origin", "panel.example.com:9443", "not-a-url", false},
		{"tauri custom scheme", "panel.example.com:9443", "tauri://localhost", true},
		{"tauri http origin", "panel.example.com:9443", "http://tauri.localhost", true},
		{"tauri https origin", "panel.example.com:9443", "https://tauri.localhost", true},
		{"tauri lookalike subdomain", "panel.example.com:9443", "https://evil.tauri.localhost", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{
				Host:   tc.host,
				Header: make(http.Header),
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOriginOrEmpty(r); got != tc.want {
				t.Errorf("sameOriginOrEmpty(Host=%q, Origin=%q) = %v, want %v",
					tc.host, tc.origin, got, tc.want)
			}
		})
	}
}

// A log stream the check refuses stops at the 401. It used to go on behind
// it — authenticateWS answered with c.JSON's error, nil once written — to
// read the source and upgrade.
func TestALogStreamWithoutCredentialsStopsAtTheRefusal(t *testing.T) {
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/ws/logs", nil), rec)
	err := LogStreamWS(func() string { return strings.Repeat("k", 32) }, nil)(c)
	if !errors.Is(err, auth.ErrUnauthenticatedWS) || rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "missing source") {
		t.Fatalf("err %v, status %d, body %q", err, rec.Code, rec.Body.String())
	}
}
