package auth

import (
	"crypto/subtle"
	"net/http"
	"sync"
)

// InternalProxyHeader is set by the gRPC ProxyRequest handler to bypass JWT
// authentication when a cluster node forwards a request that is already
// authenticated via mTLS at the transport layer.
const InternalProxyHeader = "X-SFPanel-Internal-Proxy"

// clusterProxySecret lives in the shared `auth` package instead of the HTTP
// middleware package so feature modules can consult the proxy check without
// creating a feature → api/middleware reverse dependency.
var (
	clusterProxySecret   string
	clusterProxySecretMu sync.RWMutex
)

// SetClusterProxySecret configures the secret used to validate internal
// proxy requests. Called at startup (and again when a node joins a cluster
// mid-process).
func SetClusterProxySecret(secret string) {
	clusterProxySecretMu.Lock()
	clusterProxySecret = secret
	clusterProxySecretMu.Unlock()
}

// ClusterProxySecret returns the current secret. Primarily useful for the
// JWT middleware, which has its own mutex-snapshot pattern.
func ClusterProxySecret() string {
	clusterProxySecretMu.RLock()
	defer clusterProxySecretMu.RUnlock()
	return clusterProxySecret
}

// IsInternalProxyRequest reports whether the request carries a valid internal
// proxy header. WebSocket handlers use this to bypass JWT validation for
// cluster-relayed connections.
func IsInternalProxyRequest(r *http.Request) bool {
	secret := ClusterProxySecret()
	if secret == "" {
		return false
	}
	proxyToken := r.Header.Get(InternalProxyHeader)
	if proxyToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(proxyToken), []byte(secret)) == 1
}
