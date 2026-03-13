package middleware

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// ClusterProxyMiddleware intercepts requests with ?node=X and forwards them
// to the target node via gRPC. If no node param or local node, passes through.
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
			ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
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
