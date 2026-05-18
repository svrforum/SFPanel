package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	authpkg "github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// isStreamingEndpoint checks if the request path is an SSE streaming endpoint
// that should be relayed via direct HTTP instead of gRPC. The default gRPC
// proxy buffers the whole response into one APIResponse with a 4 MB recv cap
// and a 30 s unary timeout — fine for JSON CRUD, fatal for multi-minute
// installs or megabyte-scale output. Add new long-running POSTs here.
func isStreamingEndpoint(path string) bool {
	switch {
	case strings.HasSuffix(path, "/up-stream"),
		strings.HasSuffix(path, "/update-stream"),
		strings.HasSuffix(path, "/system/update"):
		return true
	case strings.Contains(path, "/appstore/apps/") && strings.HasSuffix(path, "/install"):
		return true
	// Long-running package installs (SSE, may take minutes).
	case strings.HasSuffix(path, "/packages/upgrade"),
		strings.HasSuffix(path, "/packages/install-docker"),
		strings.HasSuffix(path, "/packages/install-node"),
		strings.HasSuffix(path, "/packages/install-claude"),
		strings.HasSuffix(path, "/packages/install-codex"),
		strings.HasSuffix(path, "/packages/install-gemini"),
		strings.HasSuffix(path, "/packages/node-install-version"):
		return true
	// Docker image pull (SSE progress events from the daemon).
	case strings.HasSuffix(path, "/docker/images/pull"):
		return true
	}
	return false
}

// isBinaryRelayEndpoint identifies routes that ship large binary payloads
// in either direction (request body, response body, or both). The default
// gRPC proxy buffers the whole body into one APIResponse message and
// fails past the 4 MB default MaxRecvMsgSize. These routes are forwarded
// via plain HTTP with io.Copy so the transfer streams without sitting in
// memory on the proxying node.
func isBinaryRelayEndpoint(path string) bool {
	return strings.HasSuffix(path, "/files/download") ||
		strings.HasSuffix(path, "/files/upload") ||
		strings.HasSuffix(path, "/system/backup") ||
		strings.HasSuffix(path, "/system/restore")
}

// hopByHopHeaders are headers that must not be forwarded across HTTP
// proxies per RFC 7230 §6.1. Forwarding them confuses the next hop's
// connection management and Transfer-Encoding handling.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// copyEndToEndHeaders copies headers from src to dst skipping hop-by-hop
// entries. Use for both request- and response-header forwarding.
func copyEndToEndHeaders(dst, src http.Header) {
	for k, v := range src {
		if hopByHopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, vv := range v {
			dst.Add(k, vv)
		}
	}
}

// buildRelayURL composes the absolute URL for a remote node forwarding
// request, dropping the local "node=" param so we don't loop.
func buildRelayURL(req *http.Request, targetNode *cluster.Node) string {
	baseURL := nodeBaseURL(targetNode.APIAddress)
	query := req.URL.Query()
	query.Del("node")
	url := baseURL + req.URL.Path
	if encoded := query.Encode(); encoded != "" {
		url += "?" + encoded
	}
	return url
}

// newRemoteHTTPClient creates an HTTP client for remote node communication.
// It uses the cluster's mTLS configuration so remote traffic is authenticated
// via the shared CA instead of being blanket-trusted. If the TLS manager
// isn't ready yet (e.g. very early in Init), falls back to a config that
// still verifies hostnames against whatever system roots are available;
// this is strictly safer than InsecureSkipVerify while not crashing during
// bootstrap.
func newRemoteHTTPClient(timeout time.Duration, mgr *cluster.Manager) *http.Client {
	tlsCfg := &tls.Config{}
	if mgr != nil {
		if cfg, err := mgr.GetTLS().ClientTLSConfig(); err == nil && cfg != nil {
			tlsCfg = cfg.Clone()
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
}

// setAuthHeaders sets internal proxy or bearer auth headers for remote requests.
func setAuthHeaders(httpReq *http.Request, origReq *http.Request, mgr *cluster.Manager) {
	if secret := mgr.ProxySecret(); secret != "" {
		// v1 stays for back-compat with not-yet-upgraded peers; v2 makes the
		// request replay-resistant when both sides support it.
		httpReq.Header.Set(authpkg.InternalProxyHeader, secret)
		if v2 := authpkg.SignProxyRequestV2(origReq.Method, origReq.URL.Path); v2 != "" {
			httpReq.Header.Set(authpkg.InternalProxyHeaderV2, v2)
		}
	} else if auth := origReq.Header.Get("Authorization"); auth != "" {
		httpReq.Header.Set("Authorization", auth)
	}
	// Forward the original username so the target node knows who initiated the request
	if user := origReq.Header.Get("X-SFPanel-Original-User"); user != "" {
		httpReq.Header.Set("X-SFPanel-Original-User", user)
	}
}

// writeSSEEvent writes a single SSE event to the response writer and flushes.
func writeSSEEvent(w *echo.Response, phase, line string) {
	evt := map[string]string{"phase": phase, "line": line}
	data, _ := json.Marshal(evt)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.Writer.(http.Flusher); ok {
		f.Flush()
	}
}

// relaySSE opens a direct HTTP connection to the remote node's SSE endpoint
// and relays the event stream to the client in real-time.
// If the remote node doesn't support SSE streaming (old version), falls back
// to the regular non-streaming endpoint and synthesizes SSE events.
// nodeBaseURL returns the scheme+host for HTTP requests to a cluster peer's
// panel API. Panels are plain HTTP by default (TLS is a reverse-proxy's
// job), so we default to http://; only honor an explicit https:// prefix
// if an operator stored one into APIAddress.
func nodeBaseURL(apiAddr string) string {
	if strings.HasPrefix(apiAddr, "http://") || strings.HasPrefix(apiAddr, "https://") {
		return apiAddr
	}
	return "http://" + apiAddr
}

// relayHTTP forwards an HTTP request to a remote node and streams the
// response back to the original client. Both request body and response
// body are streamed via io.Copy so multi-GB transfers don't sit in
// memory. Used for large binary endpoints (file download/upload, system
// backup/restore) where the default gRPC proxy can't carry the payload.
func relayHTTP(c echo.Context, targetNode *cluster.Node, mgr *cluster.Manager) error {
	// 30-minute window covers downloads up to a few GB on a slow LAN and
	// matches the upper bound for the rest of the cluster relay paths.
	client := newRemoteHTTPClient(30*time.Minute, mgr)
	addAuth := func(httpReq *http.Request) {
		setAuthHeaders(httpReq, c.Request(), mgr)
	}
	return executeHTTPRelay(c, buildRelayURL(c.Request(), targetNode), client, addAuth, 30*time.Minute)
}

// executeHTTPRelay carries the manager-free body of relayHTTP. Splitting it
// out lets tests drive the relay against an httptest.Server target with a
// stubbed authMutator, without having to construct a real cluster.Manager
// (which pulls in raft, mTLS, the FSM, etc.).
//
// Lifecycle expectations:
//   - The caller has already decided routing and target URL.
//   - Request body, response body, and connection lifetime are all bound to
//     c.Request().Context(); a client disconnect cancels the upstream call.
//   - Any error that surfaces *after* WriteHeader can no longer flip the
//     status code — we log it so partial transfers don't fail silently.
func executeHTTPRelay(
	c echo.Context,
	targetURL string,
	client *http.Client,
	authMutator func(*http.Request),
	timeout time.Duration,
) error {
	req := c.Request()
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, req.Body)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to create relay request")
	}
	// Forward original request headers so things like Content-Type, Range,
	// If-None-Match reach the target. Drop Authorization first — the auth
	// mutator re-adds the cluster-internal proxy secret instead, which the
	// target trusts more than a forwarded user JWT.
	copyEndToEndHeaders(httpReq.Header, req.Header)
	httpReq.Header.Del("Authorization")
	if authMutator != nil {
		authMutator(httpReq)
	}
	if req.ContentLength > 0 {
		httpReq.ContentLength = req.ContentLength
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Relay failed: "+err.Error())
	}
	defer resp.Body.Close()

	// Forward response headers verbatim — Content-Disposition (filename),
	// Content-Length, Content-Type all matter to the browser. Strip
	// hop-by-hop entries so we don't confuse our caller's connection.
	copyEndToEndHeaders(c.Response().Header(), resp.Header)
	c.Response().WriteHeader(resp.StatusCode)
	_, copyErr := io.Copy(c.Response().Writer, resp.Body)
	if copyErr != nil && ctx.Err() == nil {
		// ctx.Err() != nil means the client went away — that's normal and
		// not actionable. Anything else is an upstream truncation we need
		// in the logs to debug stalled or partial transfers.
		slog.Warn("relay copy interrupted",
			"component", "cluster",
			"path", req.URL.Path,
			"status", resp.StatusCode,
			"error", copyErr.Error())
	}
	return copyErr
}

func relaySSE(c echo.Context, targetNode *cluster.Node, mgr *cluster.Manager) error {
	req := c.Request()
	baseURL := nodeBaseURL(targetNode.APIAddress)
	query := req.URL.Query()
	query.Del("node")
	queryStr := ""
	if encoded := query.Encode(); encoded != "" {
		queryStr = "?" + encoded
	}

	// Read body
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Minute)
	defer cancel()

	client := newRemoteHTTPClient(5*time.Minute, mgr)

	// Try SSE streaming endpoint first
	sseURL := baseURL + req.URL.Path + queryStr
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, sseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to create relay request")
	}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		httpReq.Header.Set("Content-Type", ct)
	}
	setAuthHeaders(httpReq, req, mgr)

	resp, err := client.Do(httpReq)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to connect to node: "+err.Error())
	}

	// Check if remote supports SSE streaming
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode == http.StatusOK && strings.Contains(ct, "text/event-stream") {
		defer resp.Body.Close()
		// Relay SSE stream directly
		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		buf := make([]byte, 4096)
		flusher, _ := w.Writer.(http.Flusher)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}
		return nil
	}

	// Remote returned non-SSE response (e.g. JSON error from pre-flight
	// check, or someone routed a binary-shaped path here). Stream it
	// through directly — io.Copy avoids buffering an entire response
	// body in memory if the payload turned out to be larger than we
	// expected for an "SSE" route.
	defer resp.Body.Close()

	if !strings.Contains(ct, "text/event-stream") {
		copyEndToEndHeaders(c.Response().Header(), resp.Header)
		c.Response().WriteHeader(resp.StatusCode)
		_, _ = io.Copy(c.Response().Writer, resp.Body)
		return nil
	}

	// SSE-but-broken status (e.g. 4xx with content-type still set):
	// drain the body and fall through to the legacy non-streaming fallback
	// for compose deploy paths.
	_, _ = io.Copy(io.Discard, resp.Body)

	// Fallback: try non-streaming endpoint (/up-stream → /up, /update-stream → /update)
	if strings.Contains(req.URL.Path, "-stream") {
		fallbackPath := strings.Replace(req.URL.Path, "-stream", "", 1)
		fallbackURL := baseURL + fallbackPath + queryStr

		httpReq, err = http.NewRequestWithContext(ctx, req.Method, fallbackURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to create fallback request")
		}
		if fct := req.Header.Get("Content-Type"); fct != "" {
			httpReq.Header.Set("Content-Type", fct)
		}
		setAuthHeaders(httpReq, req, mgr)

		fallbackResp, err := client.Do(httpReq)
		if err != nil {
			return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Fallback request failed: "+err.Error())
		}
		defer fallbackResp.Body.Close()

		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		fallbackBody, _ := io.ReadAll(fallbackResp.Body)

		if fallbackResp.StatusCode != http.StatusOK {
			writeSSEEvent(w, "error", fmt.Sprintf("Remote node error (HTTP %d)", fallbackResp.StatusCode))
			return nil
		}

		var result struct {
			Success bool `json:"success"`
			Data    struct {
				Output string `json:"output"`
			} `json:"data"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(fallbackBody, &result); err == nil {
			if !result.Success && result.Error.Message != "" {
				writeSSEEvent(w, "error", result.Error.Message)
				return nil
			}
			if result.Data.Output != "" {
				for _, line := range strings.Split(result.Data.Output, "\n") {
					if line = strings.TrimSpace(line); line != "" {
						writeSSEEvent(w, "deploy", line)
					}
				}
			}
		}
		writeSSEEvent(w, "complete", "Deployment completed successfully")
	}
	return nil
}

// ClusterProxyMiddleware intercepts requests with ?node=X and forwards them
// to the target node via gRPC. If no node param or local node, passes through.
// SSE streaming endpoints are relayed via direct HTTP for real-time output.
//
// The manager is resolved dynamically via getMgr on every request so that
// late activation (a node that started standalone and later initialized
// or joined a cluster) takes effect without a restart — the middleware
// chain is built once at boot, so capturing a nil *Manager as a closure
// would permanently disable proxy routing for cluster-init-at-runtime.
func ClusterProxyMiddleware(getMgr func() *cluster.Manager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			mgr := getMgr()
			// Skip if cluster not active
			if mgr == nil {
				return next(c)
			}

			nodeID := c.QueryParam("node")

			// No node param or self → local execution
			if nodeID == "" || nodeID == mgr.LocalNodeID() {
				return next(c)
			}

			// Find target node
			var targetNode *cluster.Node
			for _, n := range mgr.GetNodes() {
				if n.ID == nodeID || n.Name == nodeID {
					targetNode = n
					break
				}
			}

			if targetNode == nil {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node not found: "+nodeID)
			}

			// Only fail-fast on the leader — its CheckHealth is authoritative
			// because the leader receives heartbeats from every node. On a
			// follower, CheckHealth only reflects the leader→this-node link,
			// so other followers always look offline from this view. Trusting
			// that here would 503 routes to a perfectly healthy peer.
			// On follower we let the gRPC call (30 s timeout) be the source of
			// truth; a real outage still surfaces, just one round-trip later.
			if mgr.IsLeader() && targetNode.Status != cluster.StatusOnline {
				return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "Node is offline: "+targetNode.Name)
			}

			// Propagate the authenticated username for cluster-internal requests.
			// .Set() replaces any attacker-supplied value, so the JWT-derived
			// username is authoritative. Defense-in-depth: the copy loop below
			// further skips this header before populating gRPC Headers.
			if username, ok := c.Get("username").(string); ok && username != "" {
				c.Request().Header.Set("X-SFPanel-Original-User", username)
			} else {
				// JWT middleware should have populated username, but if not,
				// strip any attacker-supplied header rather than letting it
				// flow through.
				c.Request().Header.Del("X-SFPanel-Original-User")
			}

			// SSE streaming endpoints: relay via direct HTTP for real-time output
			if isStreamingEndpoint(c.Request().URL.Path) {
				return relaySSE(c, targetNode, mgr)
			}

			// Binary file routes: io.Copy past the gRPC 4 MB ceiling in
			// both directions (request body and response body).
			if isBinaryRelayEndpoint(c.Request().URL.Path) {
				return relayHTTP(c, targetNode, mgr)
			}

			return proxyToNodeGRPC(c, targetNode, mgr)
		}
	}
}

// proxyToNodeGRPC carries the gRPC-unary proxy body so it can be invoked
// outside the middleware (e.g. from ProxyToLeader). The middleware's
// ClusterProxyMiddleware delegates here after deciding routing; FSM-write
// handlers call ProxyToLeader which in turn calls here with the leader as
// target. Header trust rules — strip Authorization / X-SFPanel-Original-User /
// X-SFPanel-Original-Node / X-SFPanel-Internal-Proxy* from the inbound copy,
// re-set them from this-node-authoritative sources — are uniform across
// both call sites; see middleware/CLAUDE.md for the rationale.
//
// Side effect: drains c.Request().Body into bodyBytes. Callers must NOT
// expect the body to be re-readable after this returns. The current call
// sites (ClusterProxyMiddleware, ProxyToLeader) all treat the proxy as
// terminal — they return the relay response directly and never fall back
// to a local handler that would need the body.
func proxyToNodeGRPC(c echo.Context, targetNode *cluster.Node, mgr *cluster.Manager) error {
	req := c.Request()
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	headers := make(map[string]string)
	for k, v := range req.Header {
		if len(v) == 0 {
			continue
		}
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "X-Sfpanel-Original-User", "X-Sfpanel-Original-Node",
			"X-Sfpanel-Internal-Proxy", "X-Sfpanel-Internal-Proxy-V2":
			continue
		}
		headers[k] = v[0]
	}
	if username, ok := c.Get("username").(string); ok && username != "" {
		headers["X-SFPanel-Original-User"] = username
	}
	// X-SFPanel-Original-Node carries the cluster ID of the node that
	// initiated the forward. The leader's audit / security-event writers
	// stamp the row with this ID so a forensic reviewer can attribute the
	// action to the node where the user actually authenticated, not the
	// leader where it landed. mgr is non-nil at every call site (the
	// middleware's getMgr nil-check and ProxyToLeader's mgr-nil check both
	// gate this code), so dereferencing is safe.
	if nid := mgr.LocalNodeID(); nid != "" {
		headers["X-SFPanel-Original-Node"] = nid
	}
	// X-Forwarded-For preserves the original client IP across the
	// follower→leader hop. Without this, the leader's c.RealIP() returns
	// 127.0.0.1 for every forwarded request (the gRPC→loopback HTTP hop's
	// source address), collapsing the per-IP rate limiter onto one bucket
	// and letting a single attacker on one follower lock out all admin
	// auth across the cluster. Echo's IPExtractor trusts loopback by
	// default (router.go ExtractIPFromXFFHeader with 127.0.0.0/8) so the
	// XFF chain we set here propagates to the handler's c.RealIP().
	if existing := req.Header.Get("X-Forwarded-For"); existing != "" {
		headers["X-Forwarded-For"] = existing + ", " + c.RealIP()
	} else {
		headers["X-Forwarded-For"] = c.RealIP()
	}

	authToken := ""
	if auth := req.Header.Get("Authorization"); len(auth) > 7 {
		authToken = auth[7:] // Strip "Bearer "
	}

	apiReq := &pb.APIRequest{
		Method:    req.Method,
		Path:      req.URL.Path,
		Body:      bodyBytes,
		Headers:   headers,
		AuthToken: authToken,
	}

	query := req.URL.Query()
	query.Del("node")
	if encoded := query.Encode(); encoded != "" {
		apiReq.Path = req.URL.Path + "?" + encoded
	}

	proxyTimeout := 30 * time.Second
	if strings.Contains(req.URL.Path, "/docker/compose/") {
		proxyTimeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), proxyTimeout)
	defer cancel()

	pool := mgr.GetConnPool()
	client, err := pool.Get(targetNode.GRPCAddress)
	if err != nil {
		slog.Error("cluster proxy: connect failed", "component", "cluster",
			"target", targetNode.Name, "addr", targetNode.GRPCAddress,
			"path", req.URL.Path, "error", err)
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to connect to node: "+targetNode.Name)
	}

	resp, err := client.ProxyRequest(ctx, apiReq)
	if err != nil {
		slog.Warn("cluster proxy: first attempt failed, retrying",
			"component", "cluster", "target", targetNode.Name,
			"addr", targetNode.GRPCAddress, "path", req.URL.Path, "error", err)
		pool.Remove(targetNode.GRPCAddress)
		client, err = pool.Get(targetNode.GRPCAddress)
		if err != nil {
			slog.Error("cluster proxy: reconnect failed", "component", "cluster",
				"target", targetNode.Name, "addr", targetNode.GRPCAddress,
				"path", req.URL.Path, "error", err)
			return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to reconnect to node: "+targetNode.Name)
		}
		resp, err = client.ProxyRequest(ctx, apiReq)
		if err != nil {
			slog.Error("cluster proxy: retry failed", "component", "cluster",
				"target", targetNode.Name, "addr", targetNode.GRPCAddress,
				"path", req.URL.Path, "error", err)
			pool.Remove(targetNode.GRPCAddress)
			return response.Fail(c, http.StatusGatewayTimeout, response.ErrInternalError, "Proxy request failed: "+err.Error())
		}
	}

	for k, v := range resp.Headers {
		c.Response().Header().Set(k, v)
	}
	// Propagate the target's Content-Type instead of hard-coding JSON.
	// Defaults to application/json when the target didn't set one, matching
	// the prior behaviour for the only call sites at the time of this
	// refactor (auth FSM endpoints).
	contentType := resp.Headers["Content-Type"]
	if contentType == "" {
		contentType = "application/json"
	}
	return c.Blob(int(resp.StatusCode), contentType, resp.Body)
}

// ProxyToLeader transparently forwards the current request to the cluster
// leader via gRPC and returns (true, err) once the response is written.
// Returns (false, nil) when the local node is itself the leader, when
// cluster mode is disabled, or when the request already arrived from
// another peer via the internal-proxy auth (anti-loop guard) — in all
// those cases the caller should proceed with normal in-process handling.
//
// FSM-write handlers (admin account changes, etc.) call this at the top so
// followers don't surface "must run on leader" errors to the user. If no
// leader is currently elected, returns (true, 503) so the user sees a
// retry-friendly message instead of a stale routing hint.
//
// The anti-loop signal is the cluster-internal proxy authentication
// (X-SFPanel-Internal-Proxy + V2 HMAC) — not a forge-able plain header.
// During a leadership flap the leader-as-of-the-forward may have lost
// leadership by the time the gRPC request lands; its handler then sees a
// proxy-authenticated request and skips re-forward, letting the local
// path surface ErrNotLeader. Strictly better than ping-ponging the request
// between two peers that each think the other is leader.
func ProxyToLeader(c echo.Context, mgr *cluster.Manager) (handled bool, err error) {
	if mgr == nil || mgr.IsLeader() {
		return false, nil
	}
	if authpkg.IsInternalProxyRequest(c.Request()) {
		return false, nil
	}
	leader := mgr.LeaderNode()
	if leader == nil {
		return true, response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError,
			"No cluster leader available — retry in a few seconds")
	}
	return true, proxyToNodeGRPC(c, leader, mgr)
}
