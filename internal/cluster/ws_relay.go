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
	"github.com/svrforum/SFPanel/internal/common/safe"
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
// where the same trust boundary is established for unary requests. Callers
// must pass a non-empty, locally-authenticated username: the remote trusts
// the internal-proxy headers, and most relayed WS handlers (logs, metrics,
// docker exec) do not re-validate the operator — only the terminal handler
// has its own non-empty guard. WrapEchoWSHandler rejects unauthenticated
// relays with 401 before calling this.
func RelayWebSocket(clientWS *websocket.Conn, remoteNode *Node, endpoint PanelEndpoint, originalURL *url.URL, proxySecret, username string, tlsCfg *crypto_tls.Config) error {
	// The ws/wss decision, and the peer's trust anchor, both come from the
	// caller-resolved endpoint. It used to be decided here from an https://
	// prefix on APIAddress — an escape hatch that never fired, because every
	// writer stores a bare ip:port and verifySelfAddress rewrites it back to
	// that form after each leader boot. Getting this wrong is loud in one
	// direction and silent in the other: dialling wss:// at a plaintext peer
	// produced "tls: first record does not look like a TLS handshake" on every
	// cross-node terminal/exec/logs/metrics relay.
	scheme, apiAddr, _ := strings.Cut(endpoint.WSBaseURL, "://")
	if apiAddr == "" {
		scheme, apiAddr = "ws", endpoint.WSBaseURL
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
	// Sign the v2 (HMAC + nonce + timestamp) header only — the v1
	// static-secret send was dropped (peers accept v2 on WS handlers since
	// v0.11.2). Mirrors the HTTP gRPC proxy and middleware setAuthHeaders.
	//
	// The v2 MAC binds method + request-URI (path + query); the receiving
	// IsInternalProxyRequest validates against r.URL.RequestURI(), which is
	// path+query as it arrives at the remote — i.e. after stripNodeParam
	// removed ?node=. We therefore sign over the same path+query we put on
	// the wire.
	headers := http.Header{}
	if proxySecret != "" {
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
	// A peer serving its panel over TLS presents a certificate from its own
	// local CA — a different trust domain from the shared cluster mTLS CA
	// above, which would reject it. Replace rather than merge, and clone:
	// mutating the cached cluster config in place would corrupt the one the
	// gRPC and Raft transports use.
	if endpoint.TLSConfig != nil {
		dialCfg = endpoint.TLSConfig.Clone()
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

	// Keepalive for the client leg. The relay forwards frames verbatim but
	// never re-emits the remote handler's 30s pings toward the browser
	// (gorilla auto-pongs them on the remote leg), and browser JS can't
	// originate WS pings. So a user watching streaming output without typing
	// would let the silent client→remote read deadline fire after wsReadTimeout
	// and tear down a live session. Ping the client ourselves and re-arm its
	// read deadline on the browser's auto-pong — mirroring the local handler.
	clientWS.SetPongHandler(func(string) error {
		clientWS.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})
	stopPing := make(chan struct{})
	safe.Go("ws-relay-ping", func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ticker.C:
				clientMu.Lock()
				clientWS.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := clientWS.WriteMessage(websocket.PingMessage, nil)
				clientMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	})

	// Client → Remote
	safe.Go("ws-relay-c2r", func() {
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
				// Close the peer conn so the Remote→Client goroutine's blocking
				// ReadMessage returns at once instead of waiting out its 60s
				// read deadline.
				remoteWS.Close()
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
	})

	// Remote → Client
	safe.Go("ws-relay-r2c", func() {
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
				// Close the client conn so the Client→Remote goroutine's blocking
				// ReadMessage returns at once instead of waiting out its 60s
				// read deadline.
				clientWS.Close()
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
	})

	wg.Wait()
	close(stopPing)
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
		// Don't pre-refuse on heartbeat status. A peer can read "offline" while
		// it's perfectly reachable — the 2-node heartbeat is racy right after a
		// restart, and a follower never sees a sibling as online at all. Attempt
		// the relay and let the TCP dial fail for a genuinely-down node (a fast
		// connection-refused), matching how the cluster-stacks aggregator always
		// tries the proxy rather than trusting a possibly-stale status.

		// Resolve the authenticated username before the upgrade so we can
		// stamp it into the outbound X-SFPanel-Original-User header.
		//
		// The JWT secret comes from the FSM (replicated cluster-wide) for
		// the loopback ?token= back-compat path. The ticket branch in
		// AuthenticateWSRequest does not consult the secret, so callers
		// without a JWT secret available (i.e. pre-cluster boot) still
		// authenticate fine on the modern path.
		jwtSecret, _, _ := mgr.GetJWTAndAdmin()
		username := resolveRelayUsername(c, jwtSecret)
		// A relay carries internal-proxy headers the remote trusts, and the
		// relayed logs/metrics/docker-exec handlers do not re-validate the
		// operator — an unauthenticated relay would hand a remote session
		// (including container exec) to anyone who knows a node UUID.
		// Reject before upgrading or dialing. Local (no ?node=) requests
		// returned earlier above; local handlers do their own auth.
		if username == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
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
		if err := RelayWebSocket(clientWS, node, mgr.PanelEndpointFor(node), &relayURL, mgr.ProxySecret(), username, tlsCfg); err != nil {
			slog.Warn("WS relay to node failed", "component", "cluster", "node_id", nodeID, "error", err)
		}
		return nil
	}
}

// resolveRelayUsername resolves the operator identity for a ?node= WS relay,
// in trust order: username already on the echo context (WS routes skip
// JWTMiddleware, so it's only set when this relay node is itself receiving a
// chained internal-proxy request), the X-SFPanel-Original-User header of a
// validated internal-proxy request, then the single-use ticket / loopback
// ?token= JWT in the query string. Returns "" when nothing authenticates.
// The ticket path consumes the ticket on this node, which is why the relayed
// URL is stripped of auth params (stripAuthParams) — the remote trusts the
// internal-proxy headers instead.
func resolveRelayUsername(c echo.Context, jwtSecret string) string {
	// Mirrors the type-assertion-safe lookup pattern in proxy.go:455-462.
	if u, ok := c.Get("username").(string); ok && u != "" {
		return u
	}
	if auth.IsInternalProxyRequest(c.Request()) {
		return c.Request().Header.Get("X-SFPanel-Original-User")
	}
	return auth.AuthenticateWSRequest(c.Request(), jwtSecret)
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
