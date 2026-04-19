package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// isStreamingEndpoint checks if the request path is an SSE streaming endpoint
// that should be relayed via direct HTTP instead of gRPC.
func isStreamingEndpoint(path string) bool {
	return strings.HasSuffix(path, "/up-stream") ||
		strings.HasSuffix(path, "/update-stream") ||
		strings.HasSuffix(path, "/system/update") ||
		(strings.Contains(path, "/appstore/apps/") && strings.HasSuffix(path, "/install"))
}

// newRemoteHTTPClient creates an HTTP client for remote node communication.
func newRemoteHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// setAuthHeaders sets internal proxy or bearer auth headers for remote requests.
func setAuthHeaders(httpReq *http.Request, origReq *http.Request, mgr *cluster.Manager) {
	if secret := mgr.ProxySecret(); secret != "" {
		httpReq.Header.Set("X-SFPanel-Internal-Proxy", secret)
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
func relaySSE(c echo.Context, targetNode *cluster.Node, mgr *cluster.Manager) error {
	req := c.Request()
	baseURL := "http://" + targetNode.APIAddress
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

	client := newRemoteHTTPClient(5 * time.Minute)

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

	// Remote returned non-SSE response (e.g. JSON error from pre-flight check)
	// Pass it through to the client as-is
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	// If it's a JSON error response, forward directly
	if !strings.Contains(ct, "text/event-stream") {
		for k, v := range resp.Header {
			if len(v) > 0 {
				c.Response().Header().Set(k, v[0])
			}
		}
		return c.Blob(resp.StatusCode, ct, respBody)
	}

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
func ClusterProxyMiddleware(mgr *cluster.Manager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
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

			if targetNode.Status != cluster.StatusOnline {
				return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "Node is offline: "+targetNode.Name)
			}

			// Propagate the authenticated username for cluster-internal requests
			if username, ok := c.Get("username").(string); ok && username != "" {
				c.Request().Header.Set("X-SFPanel-Original-User", username)
			}

			// SSE streaming endpoints: relay via direct HTTP for real-time output
			if isStreamingEndpoint(c.Request().URL.Path) {
				return relaySSE(c, targetNode, mgr)
			}

			// Build gRPC request
			req := c.Request()
			var bodyBytes []byte
			if req.Body != nil {
				bodyBytes, _ = io.ReadAll(req.Body)
				req.Body.Close()
			}

			headers := make(map[string]string)
			for k, v := range req.Header {
				if len(v) > 0 {
					headers[k] = v[0]
				}
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
