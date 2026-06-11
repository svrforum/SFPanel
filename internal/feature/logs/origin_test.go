package logs

import (
	"net/http"
	"testing"
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
