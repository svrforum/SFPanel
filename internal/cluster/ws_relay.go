package cluster

import (
	crypto_tls "crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

// RelayWebSocket connects to a remote node's WebSocket endpoint and
// bidirectionally relays messages between the client and the remote node.
// The caller must have already upgraded the client connection. `tlsCfg` is
// the cluster's mTLS client config; passing nil falls back to system roots
// (still hostname-verified) rather than InsecureSkipVerify.
//
// `username` is the JWT/ticket-authenticated operator on the relay side; it
// is stamped into X-SFPanel-Original-User on the outbound dial so the remote
// handler can bind per-user state (e.g. the terminal PTY session key). The
// HTTP gRPC proxy mirrors this header propagation — see proxy.go:451-462,
// where the same trust boundary is established for unary requests. Empty
// username is acceptable here on the dialing side; the receiving terminal
// handler's strict guard (handler.go:74-78) rejects empty after the
// X-SFPanel-Internal-Proxy validation passes.
func RelayWebSocket(clientWS *websocket.Conn, remoteNode *Node, originalURL *url.URL, proxySecret, username string, tlsCfg *crypto_tls.Config) error {
	// Build remote WS URL.
	//
	// SFPanel's HTTP API is plain HTTP by design (TLS is the reverse
	// proxy's job). Default to ws://. Only switch to wss:// when the stored
	// APIAddress explicitly carries the https:// prefix, which happens only
	// if an operator put the panel behind TLS and wrote that form into
	// config.Cluster.APIAddress. The previous default-to-wss behavior
	// produced "tls: first record does not look like a TLS handshake" for
	// every cross-node terminal/exec/logs/metrics relay.
	apiAddr := remoteNode.APIAddress
	scheme := "ws"
	switch {
	case strings.HasPrefix(apiAddr, "https://"):
		scheme = "wss"
		apiAddr = strings.TrimPrefix(apiAddr, "https://")
	case strings.HasPrefix(apiAddr, "http://"):
		apiAddr = strings.TrimPrefix(apiAddr, "http://")
	}
	if !strings.Contains(apiAddr, ":") {
		apiAddr += ":3628"
	}

	remotePath := originalURL.Path
	remoteQuery := stripNodeParam(originalURL.RawQuery)
	remoteURL := url.URL{
		Scheme:   scheme,
		Host:     apiAddr,
		Path:     remotePath,
		RawQuery: remoteQuery,
	}

	// Connect to remote node's WS endpoint with internal proxy auth.
	// Sign both v1 (static secret) and v2 (HMAC + nonce + timestamp) headers
	// so a captured loopback header can't be replayed. Mirrors the HTTP gRPC
	// proxy at grpc_server.go:328-332 and proxy.go:141.
	//
	// The v2 MAC binds method + request-URI (path + query); the receiving
	// IsInternalProxyRequest validates against r.URL.RequestURI(), which is
	// path+query as it arrives at the remote — i.e. after stripNodeParam
	// removed ?node=. We therefore sign over the same path+query we put on
	// the wire.
	headers := http.Header{}
	if proxySecret != "" {
		headers.Set("X-SFPanel-Internal-Proxy", proxySecret)
		signPath := remotePath
		if remoteQuery != "" {
			signPath = remotePath + "?" + remoteQuery
		}
		if v2 := auth.SignProxyRequestV2(http.MethodGet, signPath); v2 != "" {
			headers.Set(auth.InternalProxyHeaderV2, v2)
		}
	}
	if username != "" {
		headers.Set("X-SFPanel-Original-User", username)
	}
	dialCfg := &crypto_tls.Config{}
	if tlsCfg != nil {
		dialCfg = tlsCfg.Clone()
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  dialCfg,
	}
	remoteWS, resp, err := dialer.Dial(remoteURL.String(), headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("remote WS connect failed (HTTP %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("remote WS connect failed: %w", err)
	}
	defer remoteWS.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	const wsReadTimeout = 60 * time.Second

	// Mutexes to protect concurrent writes on each connection
	var clientMu, remoteMu sync.Mutex

	// Client → Remote
	go func() {
		defer wg.Done()
		for {
			clientWS.SetReadDeadline(time.Now().Add(wsReadTimeout))
			msgType, msg, err := clientWS.ReadMessage()
			if err != nil {
				remoteMu.Lock()
				// WriteDeadline so a hung peer can't pin this goroutine forever
				// and prevent wg.Wait() from returning.
				remoteWS.SetWriteDeadline(time.Now().Add(5 * time.Second))
				remoteWS.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				remoteMu.Unlock()
				return
			}
			remoteMu.Lock()
			remoteWS.SetWriteDeadline(time.Now().Add(10 * time.Second))
			writeErr := remoteWS.WriteMessage(msgType, msg)
			remoteMu.Unlock()
			if writeErr != nil {
				return
			}
		}
	}()

	// Remote → Client
	go func() {
		defer wg.Done()
		for {
			remoteWS.SetReadDeadline(time.Now().Add(wsReadTimeout))
			msgType, msg, err := remoteWS.ReadMessage()
			if err != nil {
				clientMu.Lock()
				clientWS.SetWriteDeadline(time.Now().Add(5 * time.Second))
				clientWS.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				clientMu.Unlock()
				return
			}
			clientMu.Lock()
			clientWS.SetWriteDeadline(time.Now().Add(10 * time.Second))
			writeErr := clientWS.WriteMessage(msgType, msg)
			clientMu.Unlock()
			if writeErr != nil {
				return
			}
		}
	}()

	wg.Wait()
	return nil
}

// stripNodeParam removes the "node" query parameter from a query string.
func stripNodeParam(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	values.Del("node")
	return values.Encode()
}

// WSRelayUpgrader is the WebSocket upgrader used for relay connections.
var WSRelayUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // relay trusts the original node's auth
	},
}


// WrapEchoWSHandler wraps an Echo WebSocket handler with cluster relay support.
// If the request contains a ?node=X parameter targeting a remote node,
// it relays the WebSocket connection to that node instead of running locally.
//
// Takes a getter so runtime cluster activation (init/join after a standalone
// start) takes effect without a process restart — capturing a nil *Manager
// at boot time would otherwise permanently disable WS relaying on this
// process, and every ?node=remote terminal/exec/logs/metrics request would
// silently fall through to the local handler.
func WrapEchoWSHandler(getMgr func() *Manager, handler func(c echo.Context) error) func(c echo.Context) error {
	return func(c echo.Context) error {
		mgr := getMgr()
		if mgr == nil {
			return handler(c)
		}

		nodeID := c.QueryParam("node")
		if nodeID == "" || nodeID == mgr.LocalNodeID() {
			return handler(c)
		}

		node := mgr.GetNode(nodeID)
		if node == nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "node not found"})
		}
		// Only the leader has authoritative health (it receives heartbeats
		// from every node). On a follower, sibling-follower status is always
		// offline from this view, so we'd reject WS relay to a healthy peer.
		// Let the actual TCP dial fail instead.
		if mgr.IsLeader() && node.Status == StatusOffline {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "node is offline"})
		}

		// Resolve the authenticated username before the upgrade so we can
		// stamp it into the outbound X-SFPanel-Original-User header. WS
		// routes are registered on root echo (no JWTMiddleware), so
		// c.Get("username") is only populated when this relay node is
		// itself receiving a chained internal-proxy request — handle that
		// first, then fall back to the ticket/JWT in the query string.
		//
		// Mirrors the type-assertion-safe lookup pattern in
		// proxy.go:455-462. Empty username flows through to the dial;
		// the remote terminal handler's strict guard refuses empty after
		// validating the internal-proxy headers.
		var username string
		if u, ok := c.Get("username").(string); ok && u != "" {
			username = u
		} else if auth.IsInternalProxyRequest(c.Request()) {
			username = c.Request().Header.Get("X-SFPanel-Original-User")
		} else {
			// Authenticate against the ticket/JWT in the query. This
			// consumes the single-use ticket on the relay side, so the
			// dialed URL below has ticket/token stripped — the remote
			// authenticates via X-SFPanel-Internal-Proxy(-V2) instead.
			//
			// Pull the JWT secret from the FSM (replicated cluster-wide)
			// for the loopback ?token= back-compat path. The ticket
			// branch in AuthenticateWSRequest does not consult the
			// secret, so callers without a JWT secret available (i.e.
			// pre-cluster boot) still authenticate fine on the modern
			// path.
			jwtSecret, _, _ := mgr.GetJWTAndAdmin()
			username = auth.AuthenticateWSRequest(c.Request(), jwtSecret)
		}

		// Upgrade client connection
		clientWS, err := WSRelayUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			slog.Warn("WS relay upgrade failed", "component", "cluster", "error", err)
			return nil
		}
		defer clientWS.Close()

		var tlsCfg *crypto_tls.Config
		if t := mgr.GetTLS(); t != nil {
			if cfg, cfgErr := t.ClientTLSConfig(); cfgErr == nil {
				tlsCfg = cfg
			}
		}
		// Strip ticket/token from the URL we relay. The local
		// AuthenticateWSRequest above already consumed the ticket
		// (single-use); leaving it on the relayed URL would either no-op
		// on a second consume attempt or, with ?token=, leak the JWT
		// across the cluster fabric for no reason — the remote trusts the
		// internal-proxy header.
		relayURL := *c.Request().URL
		relayURL.RawQuery = stripAuthParams(relayURL.RawQuery)
		if err := RelayWebSocket(clientWS, node, &relayURL, mgr.ProxySecret(), username, tlsCfg); err != nil {
			slog.Warn("WS relay to node failed", "component", "cluster", "node_id", nodeID, "error", err)
		}
		return nil
	}
}

// stripAuthParams removes ticket/token query parameters from a raw query.
// Used by WrapEchoWSHandler when relaying — the local side has already
// consumed the credential, so the remote authenticates via the internal
// proxy headers instead. (?node= is stripped separately by stripNodeParam
// inside RelayWebSocket.)
func stripAuthParams(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	values.Del("ticket")
	values.Del("token")
	return values.Encode()
}
