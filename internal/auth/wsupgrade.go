package auth

import (
	"errors"
	"net/http"
)

// ErrUnauthenticatedWS is returned by AuthenticateWSUpgrade when the upgrade
// must be refused. The 401 has already been written; the caller only needs
// to propagate the error so the handler stops before Upgrader.Upgrade. A nil
// error never accompanies an empty username — that is what keeps a per-user
// session key from ever being built on "".
var ErrUnauthenticatedWS = errors.New("websocket upgrade not authenticated")

// AuthenticateWSUpgrade verifies a WebSocket upgrade request and returns the
// authenticated username. Shared by every PTY-style route (/ws/terminal,
// /ws/ai/attach).
//
// Direct requests carry a single-use ticket or, on loopback only, a JWT —
// AuthenticateWSRequest checks both. Cluster-internal forwards, gated by the
// HMAC-validated internal-proxy headers, carry the originating node's verified
// operator name in X-SFPanel-Original-User; the proxy middleware strips any
// caller-supplied copy before re-setting it, so it is authoritative here. An
// empty value is refused rather than defaulted, because a default would
// shadow the real admin's sessions on the target node.
//
// An echo caller passes c.Response(), never c.Response().Writer: the 401 has
// to go through echo's ResponseWriter so the status is recorded and the
// response counts as committed.
func AuthenticateWSUpgrade(w http.ResponseWriter, r *http.Request, jwtSecret string) (string, error) {
	if IsInternalProxyRequest(r) {
		if user := r.Header.Get("X-SFPanel-Original-User"); user != "" {
			return user, nil
		}
		writeWSUnauthorized(w)
		return "", ErrUnauthenticatedWS
	}
	if user := AuthenticateWSRequest(r, jwtSecret); user != "" {
		return user, nil
	}
	writeWSUnauthorized(w)
	return "", ErrUnauthenticatedWS
}

func writeWSUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
