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

			// Build gRPC request
			req := c.Request()
			var bodyBytes []byte
			if req.Body != nil {
				bodyBytes, _ = io.ReadAll(req.Body)
				req.Body.Close()
			}

			// Build the gRPC Headers map but exclude auth-sensitive keys from
			// the blanket copy. Authorization is sent separately via AuthToken
			// below, and X-SFPanel-Original-User must come from c.Get("username")
			// not from the inbound request — otherwise an attacker who reaches
			// any node directly could inject a claimed username and have it
			// fan out cluster-wide.
			headers := make(map[string]string)
			for k, v := range req.Header {
				if len(v) == 0 {
					continue
				}
				switch http.CanonicalHeaderKey(k) {
				case "Authorization", "X-Sfpanel-Original-User",
					"X-Sfpanel-Internal-Proxy", "X-Sfpanel-Internal-Proxy-V2":
					continue
				}
				headers[k] = v[0]
			}
			// Re-add the trusted username if present in echo context.
			if username, ok := c.Get("username").(string); ok && username != "" {
				headers["X-SFPanel-Original-User"] = username
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

			// Copy query params (except "node") to path
			query := req.URL.Query()
			query.Del("node")
			if encoded := query.Encode(); encoded != "" {
				apiReq.Path = req.URL.Path + "?" + encoded
			}

			// Forward via gRPC using connection pool
			// Use longer timeout for compose operations (pull/up can take minutes)
			proxyTimeout := 30 * time.Second
			if strings.Contains(req.URL.Path, "/docker/compose/") {
				proxyTimeout = 5 * time.Minute
			}
			ctx, cancel := context.WithTimeout(c.Request().Context(), proxyTimeout)
			defer cancel()

			pool := mgr.GetConnPool()
			client, err := pool.Get(targetNode.GRPCAddress)
			if err != nil {
				return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to connect to node: "+targetNode.Name)
			}
			// Do NOT close — connection is pooled

			resp, err := client.ProxyRequest(ctx, apiReq)
			if err != nil {
				// Connection may be stale, remove from pool and retry once
				pool.Remove(targetNode.GRPCAddress)
				client, err = pool.Get(targetNode.GRPCAddress)
				if err != nil {
					return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to reconnect to node: "+targetNode.Name)
				}
				resp, err = client.ProxyRequest(ctx, apiReq)
				if err != nil {
					pool.Remove(targetNode.GRPCAddress)
					return response.Fail(c, http.StatusGatewayTimeout, response.ErrInternalError, "Proxy request failed: "+err.Error())
				}
			}

			// Copy response headers
			for k, v := range resp.Headers {
				c.Response().Header().Set(k, v)
			}

			return c.Blob(int(resp.StatusCode), "application/json", resp.Body)
		}
	}
}
