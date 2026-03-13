package cluster

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// RelayWebSocket connects to a remote node's WebSocket endpoint and
// bidirectionally relays messages between the client and the remote node.
// The caller must have already upgraded the client connection.
func RelayWebSocket(clientWS *websocket.Conn, remoteNode *Node, originalURL *url.URL, proxySecret string) error {
	// Build remote WS URL
	apiAddr := remoteNode.APIAddress
	if !strings.Contains(apiAddr, ":") {
		apiAddr += ":8443"
	}

	remoteURL := url.URL{
		Scheme:   "ws",
		Host:     apiAddr,
		Path:     originalURL.Path,
		RawQuery: stripNodeParam(originalURL.RawQuery),
	}

	// Connect to remote node's WS endpoint with internal proxy auth
	headers := http.Header{}
	if proxySecret != "" {
		headers.Set("X-SFPanel-Internal-Proxy", proxySecret)
	}
	dialer := websocket.Dialer{}
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

	// Client → Remote
	go func() {
		defer wg.Done()
		for {
			msgType, msg, err := clientWS.ReadMessage()
			if err != nil {
				remoteWS.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := remoteWS.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Remote → Client
	go func() {
		defer wg.Done()
		for {
			msgType, msg, err := remoteWS.ReadMessage()
			if err != nil {
				clientWS.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := clientWS.WriteMessage(msgType, msg); err != nil {
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
func WrapEchoWSHandler(mgr *Manager, handler func(c echo.Context) error) func(c echo.Context) error {
	return func(c echo.Context) error {
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
		if node.Status == StatusOffline {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "node is offline"})
		}

		// Upgrade client connection
		clientWS, err := WSRelayUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			log.Printf("WS relay upgrade failed: %v", err)
			return nil
		}
		defer clientWS.Close()

		if err := RelayWebSocket(clientWS, node, c.Request().URL, mgr.ProxySecret()); err != nil {
			log.Printf("WS relay to node %s failed: %v", nodeID, err)
		}
		return nil
	}
}
