package auth

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// AuthenticateWSRequest validates a WebSocket upgrade request and returns the
// authenticated username, or "" if neither credential checked out. Tries the
// single-use ?ticket= first (preferred, consumed atomically) then falls back
// to ?token= JWT (back-compat for clients still using the older auth method).
//
// The legacy ?token= path is now loopback-only: putting a long-lived JWT in
// a URL leaks it into access logs, proxy logs, browser history, and shell
// history. Modern callers mint a single-use ticket via POST /auth/ws-ticket
// and connect with ?ticket=. The fallback survives so the operator can still
// `wscat ws://localhost:.../ws/...?token=$JWT` during incident response, but
// remote callers MUST use the ticket flow.
//
// Callers (terminal/, logs/, websocket/, ContainerLogs/Shell handlers) should
// reject the upgrade with HTTP 401 when "" is returned.
func AuthenticateWSRequest(r *http.Request, jwtSecret string) string {
	if ticket := r.URL.Query().Get("ticket"); ticket != "" {
		if username, ok := ConsumeWSTicket(ticket); ok {
			return username
		}
		// Bad/expired ticket: don't fall through to JWT — the client is
		// already on the new auth path and a stale ticket means they need
		// to mint a fresh one (or re-authenticate).
		return ""
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		return ""
	}
	if !isLoopbackRequest(r) {
		slog.Warn("rejecting WS upgrade via legacy ?token= from non-loopback",
			"component", "auth", "remote", r.RemoteAddr, "path", r.URL.Path)
		return ""
	}
	claims, err := ParseToken(token, jwtSecret)
	if err != nil {
		return ""
	}
	slog.Info("WS upgrade via legacy ?token= (loopback)",
		"component", "auth", "username", claims.Username, "path", r.URL.Path)
	return claims.Username
}

// isLoopbackRequest reports whether the request's source address is on the
// loopback interface. r.RemoteAddr is of the form "host:port" — extract host
// and check net.ParseIP. We deliberately don't trust X-Forwarded-For here:
// the request came from whatever socket we accepted on, and a reverse proxy
// presenting a remote client as 127.0.0.1 is operator misconfiguration, not
// our concern to second-guess.
func isLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
