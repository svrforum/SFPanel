package wsorigin

import (
	"net/http"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin", "panel.example.com:9443", "", true},
		{"matching origin", "panel.example.com:9443", "https://panel.example.com:9443", true},
		{"case-insensitive host", "Panel.Example.com:9443", "https://panel.example.com:9443", true},
		{"tauri webview", "panel.example.com:9443", "tauri://localhost", true},
		{"tauri https webview", "panel.example.com:9443", "https://tauri.localhost", true},
		{"foreign origin", "panel.example.com:9443", "https://evil.example.com", false},
		{"matching host different port", "panel.example.com:9443", "https://panel.example.com:9444", false},
		{"malformed origin", "panel.example.com:9443", "not-a-url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{Host: tc.host, Header: make(http.Header)}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := CheckOrigin(r); got != tc.want {
				t.Errorf("CheckOrigin(Host=%q, Origin=%q) = %v, want %v",
					tc.host, tc.origin, got, tc.want)
			}
		})
	}
}
