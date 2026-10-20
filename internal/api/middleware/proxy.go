package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
		strings.HasSuffix(path, "/install-stream"),
		strings.HasSuffix(path, "/system/update"):
		return true
	case strings.Contains(path, "/appstore/apps/") && strings.HasSuffix(path, "/install"):
		return true
	// Long-running package installs (SSE, may take minutes). A package
	// transaction's stream (/packages/upgrade, /packages/install-stream, and
	// /packages/transaction/stream, which follows one already running) is
	// cut by the relay's window like any other, and the page follows it
	// again: apt runs in a unit of its own and does not notice.
	case strings.HasSuffix(path, "/packages/upgrade"),
		strings.HasSuffix(path, "/packages/install-docker"),
		strings.HasSuffix(path, "/packages/install-node"),
		strings.HasSuffix(path, "/packages/node-install-version"),
		strings.HasSuffix(path, "/packages/transaction/stream"):
		return true
	// Docker image pull (SSE progress events from the daemon).
	case strings.HasSuffix(path, "/docker/images/pull"):
		return true
	// Tailscale's upstream installer can take 30s-2min depending on
	// network and is SSE-streamed for live feedback.
	case strings.HasSuffix(path, "/network/tailscale/install"):
		return true
	// /cluster/update runs on the leader and is itself SSE — proxying it
	// via gRPC would buffer the rolling-update event stream into one
	// 4 MB unary response.
	case strings.HasSuffix(path, "/cluster/update"):
		return true
	// Stack migrate orchestrator is SSE (cold-migration phase stream, can
	// run for minutes). The exact "/migrate" suffix deliberately excludes
	// "/migrate-import" (binary relay) and "/migrate/preflight" (unary JSON).
	case strings.HasSuffix(path, "/migrate"):
		return true
	}
	return false
}

// relayEventStream copies a remote node's event stream to the client as it
// arrives. The upstream request runs under the client request's context, so
// it ends when the client leaves or the panel shuts down (Streams) as well as
// when the remote breaks — and only the last is the remote's failure.
func relayEventStream(c echo.Context, upstream io.Reader) {
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	buf := make([]byte, 4096)
	flusher, _ := w.Writer.(http.Flusher)
	for {
		n, readErr := upstream.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF && c.Request().Context().Err() == nil {
				// The remote stream broke mid-flight (node crash, network
				// reset). Headers (200) are already sent, so we can't change
				// the status — but surface a terminal error event and log it
				// so a half-finished install/update isn't read as success.
				slog.Warn("SSE relay: remote stream ended abnormally",
					"component", "cluster-proxy",
					"path", c.Request().URL.Path, "error", readErr)
				fmt.Fprint(w, "event: error\ndata: {\"message\":\"upstream stream interrupted\"}\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
			return
		}
	}
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
		strings.HasSuffix(path, "/system/restore") ||
		strings.HasSuffix(path, "/compose/migrate-import")
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
// request, with the query relayQuery leaves. endpoint is the one the relay's
// client trusts (newRemoteHTTPClient): URL and trust anchor come from a single
// resolution, so a CA that vanished in between cannot turn the URL into
// http:// behind a check that passed.
func buildRelayURL(req *http.Request, endpoint cluster.PanelEndpoint) string {
	url := endpoint.BaseURL + req.URL.Path
	if encoded := relayQuery(req.URL.Query()); encoded != "" {
		url += "?" + encoded
	}
	return url
}

// relayQuery is the query string a forwarded request carries: without the
// routing "node=" param, so we don't loop, and without the ?token=/?ticket=
// the browser authenticated to this node with (the thumbnail and download
// URLs carry the access token) — the target is authenticated by the
// internal-proxy header, and the operator's credentials are not its to hold.
func relayQuery(q url.Values) string {
	q.Del("node")
	return cluster.StripAuthParams(q.Encode())
}

// newRemoteHTTPClient creates an HTTP client for remote node communication.
// It uses the cluster's mTLS configuration so remote traffic is authenticated
// via the shared CA instead of being blanket-trusted. If the TLS manager
// isn't ready yet (e.g. very early in Init), falls back to a config that
// still verifies hostnames against whatever system roots are available;
// this is strictly safer than InsecureSkipVerify while not crashing during
// bootstrap.
func newRemoteHTTPClient(timeout time.Duration, mgr *cluster.Manager, endpoint cluster.PanelEndpoint) *http.Client {
	tlsCfg := &tls.Config{}
	if mgr != nil {
		if cfg, err := mgr.GetTLS().ClientTLSConfig(); err == nil && cfg != nil {
			tlsCfg = cfg.Clone()
		}
	}
	// A peer serving its panel over TLS presents a certificate from ITS
	// OWN local CA, not the shared cluster mTLS CA — different trust
	// domain, so the mTLS pool above would reject it. Replace, don't merge:
	// each peer is anchored to exactly one authority.
	if endpoint.TLSConfig != nil {
		tlsCfg = endpoint.TLSConfig.Clone()
	}
	// Each relay builds its own client and sends one request with it (two for
	// the SSE fallback): a connection kept alive after that would sit idle
	// with nothing to reuse it, open on both nodes until one restarts.
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:   tlsCfg,
			DisableKeepAlives: true,
		},
	}
}

// setAuthHeaders signs a relayed request for target and names the operator.
//
// The key depends on the target (SignProxyRequestFor): a header signed with the
// managers' secret is accepted once by every manager, so it goes to managers
// only, and an agent gets one made with its own key. When target cannot be
// signed for, nothing is added and the error says why; the caller must not
// send. The operator's bearer token used to go in its place, and to an agent
// that is a login valid on every manager for a day.
func setAuthHeaders(httpReq *http.Request, origReq *http.Request, mgr *cluster.Manager, target *cluster.Node) error {
	// v2-only send: the v1 static-secret header is no longer attached, and
	// no receiver has accepted it since v0.57.0 (internal/auth/proxyauth.go).
	//
	// Sign the OUTBOUND request-URI (httpReq), not the inbound one. Callers
	// build httpReq pointing at the peer with the routing ?node= stripped, so
	// signing httpReq.URL.RequestURI() binds the MAC to exactly what the peer
	// receives and validates. Signing origReq instead would cover the
	// with-node URI; the peer validates the node-stripped one and v2 fails,
	// 401-ing every ?node-routed relay (e.g. a cross-node-initiated stack
	// migration over SSE).
	v2, err := mgr.SignProxyRequestFor(target, httpReq.Method, httpReq.URL.RequestURI())
	if err != nil {
		return err
	}
	httpReq.Header.Set(authpkg.InternalProxyHeaderV2, v2)
	// Forward the original username so the target node knows who initiated the request
	if user := origReq.Header.Get("X-SFPanel-Original-User"); user != "" {
		httpReq.Header.Set("X-SFPanel-Original-User", user)
	}
	return nil
}

// failUnsigned answers a relay that could not be signed for its target, before
// anything was sent to it. An agent with no key in replicated state has a code
// of its own: the operator can act on that one.
func failUnsigned(c echo.Context, err error) error {
	code := response.ErrInternalError
	if errors.Is(err, cluster.ErrNoAgentKey) {
		code = response.ErrClusterAgentAuthFailed
	}
	return response.Fail(c, http.StatusBadGateway, code, "Relay not sent: "+err.Error())
}

// failAgentRefusedSignature answers an agent's 401 to a relay this manager
// signed with that agent's key. Whatever the agent refused, it was not the
// operator's session here — which is how the browser reads a 401, logging
// them out.
func failAgentRefusedSignature(c echo.Context) error {
	return response.Fail(c, http.StatusBadGateway, response.ErrClusterAgentAuthFailed, "the agent refused the manager's signature")
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

// relayHTTP forwards an HTTP request to a remote node and streams the
// response back to the original client. Both request body and response
// body are streamed via io.Copy so multi-GB transfers don't sit in
// memory. Used for large binary endpoints (file download/upload, system
// backup/restore) where the default gRPC proxy can't carry the payload.
func relayHTTP(c echo.Context, targetNode *cluster.Node, mgr *cluster.Manager) error {
	endpoint, err := mgr.PanelEndpointChecked(targetNode)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrClusterAgentInsecure, err.Error())
	}
	// 30-minute window covers downloads up to a few GB on a slow LAN and
	// matches the upper bound for the rest of the cluster relay paths.
	client := newRemoteHTTPClient(30*time.Minute, mgr, endpoint)
	addAuth := func(httpReq *http.Request) error {
		return setAuthHeaders(httpReq, c.Request(), mgr, targetNode)
	}
	return executeHTTPRelay(c, buildRelayURL(c.Request(), endpoint), client, addAuth, 30*time.Minute, !targetNode.IsManager())
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
//   - An authMutator error ends the relay before anything is sent.
//   - agentTarget says the target is an agent, whose 401 is answered as a 502
//     (failAgentRefusedSignature) rather than handed to the browser.
//   - Any error that surfaces *after* WriteHeader can no longer flip the
//     status code — we log it so partial transfers don't fail silently.
func executeHTTPRelay(
	c echo.Context,
	targetURL string,
	client *http.Client,
	authMutator func(*http.Request) error,
	timeout time.Duration,
	agentTarget bool,
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
	// Nor the rest of the browser's session with this node: the refresh and
	// CSRF cookies and the CSRF token are this node's credentials, and the
	// relay target has no reason to receive them. Original-Node is the
	// forwarding node's to state, never a client's.
	httpReq.Header.Del("Cookie")
	httpReq.Header.Del("X-CSRF-Token")
	httpReq.Header.Del("X-SFPanel-Original-Node")
	// Never forward client-supplied internal-proxy headers. Before the v1
	// send was dropped, setAuthHeaders overwrote the v1 slot on every relay;
	// now nothing does, so without this Del a JWT-authenticated client could
	// smuggle its own X-SFPanel-Internal-Proxy value to the peer. Not
	// exploitable (the peer prefers the v2 header we attach, and a valid v1
	// requires the secret), but the relay must uphold the same strip rule as
	// the gRPC path.
	httpReq.Header.Del(authpkg.InternalProxyHeader)
	httpReq.Header.Del(authpkg.InternalProxyHeaderV2)
	// Ask the target for PLAIN — this edge node compresses once for the browser.
	// Forwarding Accept-Encoding would gzip on the target AND here → a
	// double-gzipped body the browser can't decode.
	httpReq.Header.Del("Accept-Encoding")
	if authMutator != nil {
		if err := authMutator(httpReq); err != nil {
			return failUnsigned(c, err)
		}
	}
	if req.ContentLength > 0 {
		httpReq.ContentLength = req.ContentLength
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Relay failed: "+err.Error())
	}
	defer resp.Body.Close()

	if agentTarget && resp.StatusCode == http.StatusUnauthorized {
		return failAgentRefusedSignature(c)
	}

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

// relayWindowFor is how long an SSE relay may run: http.Client.Timeout caps
// the whole request, streamed body included. A stack migration (quiesce →
// transfer → restore → healthcheck) and a cluster update (every agent wave,
// then every manager) routinely outlive the five minutes an install or a
// node's own update needs. Two hours is the migration's own limit.
func relayWindowFor(path string) time.Duration {
	if strings.HasSuffix(path, "/migrate") || path == "/api/v1/cluster/update" {
		return 2 * time.Hour
	}
	return 5 * time.Minute
}

// relaySSE opens a direct HTTP connection to the remote node's SSE endpoint
// and relays the event stream to the client in real-time.
// If the remote node doesn't support SSE streaming (old version), falls back
// to the regular non-streaming endpoint and synthesizes SSE events.
func relaySSE(c echo.Context, targetNode *cluster.Node, mgr *cluster.Manager) error {
	req := c.Request()
	endpoint, err := mgr.PanelEndpointChecked(targetNode)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrClusterAgentInsecure, err.Error())
	}
	baseURL := endpoint.BaseURL
	queryStr := ""
	if encoded := relayQuery(req.URL.Query()); encoded != "" {
		queryStr = "?" + encoded
	}

	// Read body
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	// The window bounds the whole relay, streamed body included; see
	// relayWindowFor.
	window := relayWindowFor(req.URL.Path)

	ctx, cancel := context.WithTimeout(req.Context(), window)
	defer cancel()

	client := newRemoteHTTPClient(window, mgr, endpoint)

	// Try SSE streaming endpoint first
	sseURL := baseURL + req.URL.Path + queryStr
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, sseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to create relay request")
	}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		httpReq.Header.Set("Content-Type", ct)
	}
	if err := setAuthHeaders(httpReq, req, mgr, targetNode); err != nil {
		return failUnsigned(c, err)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, "Failed to connect to node: "+err.Error())
	}

	// Check if remote supports SSE streaming
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode == http.StatusOK && strings.Contains(ct, "text/event-stream") {
		defer resp.Body.Close()
		relayEventStream(c, resp.Body)
		return nil
	}

	// Remote returned non-SSE response (e.g. JSON error from pre-flight
	// check, or someone routed a binary-shaped path here). Stream it
	// through directly — io.Copy avoids buffering an entire response
	// body in memory if the payload turned out to be larger than we
	// expected for an "SSE" route.
	defer resp.Body.Close()

	if !strings.Contains(ct, "text/event-stream") {
		if !targetNode.IsManager() && resp.StatusCode == http.StatusUnauthorized {
			return failAgentRefusedSignature(c)
		}
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
		if err := setAuthHeaders(httpReq, req, mgr, targetNode); err != nil {
			return failUnsigned(c, err)
		}

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

			// No node param or self → local execution. proxyTargetFor says
			// the same; answering here first keeps the common case from
			// copying the replicated node table on every request.
			if nodeID == "" || nodeID == mgr.LocalNodeID() {
				return next(c)
			}

			targetNode, local, err := proxyTargetFor(c.Request().URL.Path, nodeID, mgr.LocalNodeID(), mgr.GetNodes())
			if err != nil {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Node not found: "+nodeID)
			}
			if local {
				return next(c)
			}
			// An agent has no cluster routes; cluster management runs on
			// managers. Running the request here instead would be no answer:
			// it would act on this manager, and /cluster/leave would take it
			// out of the cluster.
			if !targetNode.IsManager() && agentRouteRefused(c.Request().URL.Path) {
				return response.Fail(c, http.StatusConflict, response.ErrClusterRouteNotOnAgent,
					"cluster management runs on managers; an agent has no cluster routes")
			}

			// Don't pre-refuse on heartbeat status. Even the leader's view goes
			// stale right after a peer restart (the 2-node heartbeat is racy) and
			// a follower never sees a sibling as online at all — so a 503 here
			// would block a perfectly reachable peer's dashboard/API. Let the
			// gRPC call be the source of truth: a genuine outage fails fast
			// (connection refused), matching the WS relay and the cluster-stacks
			// aggregator, which both try rather than trust a possibly-stale status.

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

// agentRouteRefused reports whether path is a cluster route, which a request
// whose ?node= names an agent may not reach: the agent does not serve it, and
// this manager must not run it in the agent's place.
func agentRouteRefused(path string) bool {
	return strings.HasPrefix(path, "/api/v1/cluster/")
}

// errProxyNodeNotFound is proxyTargetFor's answer for a ?node= that names no
// member; the middleware turns it into its 400.
var errProxyNodeNotFound = errors.New("node not found")

// proxyTargetFor decides where a request carrying ?node=nodeParam runs:
// locally, or on target.
//
// An exact node ID wins over a name. The node list comes out of a map, so a
// single search over IDs and names together was answered by iteration order:
// a node named after another node's ID could take that node's requests.
func proxyTargetFor(path, nodeParam, localID string, nodes []*cluster.Node) (target *cluster.Node, local bool, err error) {
	if nodeParam == "" || nodeParam == localID {
		return nil, true, nil
	}
	// The session, refresh row and cookies belong to the node the browser
	// talks to; /auth/logout forwarded elsewhere left the issuing node's
	// refresh row alive.
	if strings.HasPrefix(path, "/api/v1/auth/") {
		return nil, true, nil
	}
	for _, n := range nodes {
		if n.ID == nodeParam {
			return n, false, nil
		}
	}
	for _, n := range nodes {
		if n.Name == nodeParam {
			return n, false, nil
		}
	}
	return nil, false, errProxyNodeNotFound
}

// buildProxyAPIRequest is the gRPC request that forwards c to another node.
//
// It carries the operator's identity and nothing they authenticate with. The
// receiving gRPC server signs its own loopback hop, so the far side needs no
// credential, and whatever went along was handed to the target: the access
// token (Authorization, which also used to ride in AuthToken — accepted by
// every manager for a day by default), the refresh and CSRF cookies, the CSRF
// token, and ?token=/?ticket= (relayQuery). Original-User and Original-Node
// are re-set from this node's own state, never copied from the request.
func buildProxyAPIRequest(c echo.Context, localNodeID string, body []byte) *pb.APIRequest {
	req := c.Request()
	headers := make(map[string]string)
	for k, v := range req.Header {
		if len(v) == 0 {
			continue
		}
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Cookie", "X-Csrf-Token",
			"X-Sfpanel-Original-User", "X-Sfpanel-Original-Node",
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
	// leader where it landed.
	if localNodeID != "" {
		headers["X-SFPanel-Original-Node"] = localNodeID
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

	path := req.URL.Path
	if encoded := relayQuery(req.URL.Query()); encoded != "" {
		path += "?" + encoded
	}
	return &pb.APIRequest{
		Method:  req.Method,
		Path:    path,
		Body:    body,
		Headers: headers,
	}
}

// proxyToNodeGRPC carries the gRPC-unary proxy body so it can be invoked
// outside the middleware (e.g. from ProxyToLeader). The middleware's
// ClusterProxyMiddleware delegates here after deciding routing; FSM-write
// handlers call ProxyToLeader which in turn calls here with the leader as
// target. Both get their request from buildProxyAPIRequest, so the header
// trust rules are uniform across the call sites; see middleware/CLAUDE.md
// for the rationale.
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
	// mgr is non-nil at every call site (the middleware's getMgr nil-check
	// and ProxyToLeader's mgr-nil check both gate this code), so
	// dereferencing is safe.
	apiReq := buildProxyAPIRequest(c, mgr.LocalNodeID(), bodyBytes)

	proxyTimeout := cluster.ProxyTimeoutFor(req.URL.Path)
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

	// An agent's 401 is the agent refusing the request, not the operator's
	// session here ending — which is how the browser reads a 401, logging
	// them out. Unlike the HTTP relays, this path carries no signature of
	// this manager's (the agent signs its own loopback hop), so the answer
	// does not blame one.
	if !targetNode.IsManager() && resp.StatusCode == http.StatusUnauthorized {
		return response.Fail(c, http.StatusBadGateway, response.ErrClusterAgentAuthFailed, "the agent refused the request as unauthenticated")
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
