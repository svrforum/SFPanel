package websocket

import (
	"net/http"
	"testing"
)

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
		{"matching origin without port (rare)", "panel.example.com", "https://panel.example.com", true},
		{"foreign origin", "panel.example.com:9443", "https://evil.example.com", false},
		{"matching host different port", "panel.example.com:9443", "https://panel.example.com:9444", false},
		{"malformed origin", "panel.example.com:9443", "not-a-url", false},
		{"protocol-relative scheme empty", "panel.example.com:9443", "//panel.example.com:9443", true},
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
			got := sameOriginOrEmpty(r)
			if got != tc.want {
				t.Errorf("sameOriginOrEmpty(Host=%q, Origin=%q) = %v, want %v",
					tc.host, tc.origin, got, tc.want)
			}
		})
	}
}
