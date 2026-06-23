// Package wsorigin centralizes the WebSocket CSWSH (cross-site WebSocket
// hijacking) origin check shared by every /ws/* handler. The predicate and its
// Tauri allowlist used to be copy-pasted into the terminal, websocket and logs
// feature packages, where a future tightening could silently drift across the
// three copies on a security-sensitive check. Keep it here, once.
package wsorigin

import (
	"net/http"
	"net/url"
	"strings"
)

// tauriOrigins are the desktop wrapper's webview origins — the same three the
// CORS allowlist in router.go carries. Keys are lowercase. Webviews DO stamp an
// Origin on WS upgrades, so the empty-Origin allowance alone does not cover the
// desktop app.
var tauriOrigins = map[string]bool{
	"tauri://localhost":       true,
	"http://tauri.localhost":  true,
	"https://tauri.localhost": true,
}

// CheckOrigin allows a WS upgrade from the same Host as the request (the panel
// UI in a normal browser), from non-browser clients that omit the Origin header
// (curl, websocat), and from the Tauri desktop webview origins. Anything else —
// a foreign Origin set by a malicious page — is rejected, defending against
// CSWSH even though the ?ticket=/?token= path doesn't ride cookies. Suitable as
// websocket.Upgrader.CheckOrigin.
func CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if tauriOrigins[strings.ToLower(origin)] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	// Compare host:port; gorilla normalizes Request.Host the same way.
	return strings.EqualFold(u.Host, r.Host)
}
