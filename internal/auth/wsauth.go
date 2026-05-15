package auth

import (
	"net/http"
)

// AuthenticateWSRequest validates a WebSocket upgrade request and returns the
// authenticated username, or "" if neither credential checked out. Tries the
// single-use ?ticket= first (preferred, consumed atomically) then falls back
// to ?token= JWT (back-compat for clients still using the older auth method).
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
	claims, err := ParseToken(token, jwtSecret)
	if err != nil {
		return ""
	}
	return claims.Username
}
