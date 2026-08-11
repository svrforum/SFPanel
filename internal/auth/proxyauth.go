package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// InternalProxyHeader (v1) was the original static-secret proxy-bypass
// header — vulnerable to replay if an mTLS-trusted node ever leaked one.
//
// Retired, dead on the wire (deliberately NOT a formal "Deprecated:" marker:
// every remaining reference is intentional). Senders went v2-only in v0.56.0
// and the receive branch was removed in v0.57.0 — a request carrying only
// this header is rejected. The constant survives solely for the defensive
// strip lists (HTTP relay, gRPC receive) that must keep removing the header
// from forwarded requests, and for the tests pinning the rejection.
const InternalProxyHeader = "X-SFPanel-Internal-Proxy"

// InternalProxyHeaderV2 carries a timestamp + nonce + HMAC tuple instead of
// the raw secret. Format:
//
//	"<unix_millis>:<nonce_hex>:<hmac_hex>"
//
// where hmac = HMAC-SHA256(secret, "<unix_millis>:<nonce_hex>:<method>:<path>").
//
// Server-side validation enforces ±30s clock skew + a recent-nonce cache to
// reject replays. Method + path go into the MAC so a captured proxy header
// can't be re-bound to a different endpoint.
const InternalProxyHeaderV2 = "X-SFPanel-Internal-Proxy-V2"

// proxyClockSkew is how far in either direction a v2 proxy timestamp can
// drift from the server clock. 30s is generous enough for typical NTP-locked
// hosts; tightening would force inter-node clock sync that's already implicit
// in Raft's heartbeat math.
const proxyClockSkew = 30 * time.Second

// nonceCacheTTL is how long a seen nonce stays in the dedup cache. Must be
// ≥ 2 × proxyClockSkew so a request straddling the skew window can't be
// replayed by an attacker who held it back.
const nonceCacheTTL = 2 * proxyClockSkew

var (
	clusterProxySecret   string
	clusterProxySecretMu sync.RWMutex

	nonceCache   = make(map[string]time.Time)
	nonceCacheMu sync.Mutex
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
// proxy header — the replay-resistant v2 HMAC only. The legacy v1
// static-secret path was retired in two steps (send dropped in v0.56.0,
// receive dropped in v0.57.0); peers older than v0.56.0 can no longer relay
// to this node and must be upgraded locally first.
func IsInternalProxyRequest(r *http.Request) bool {
	secret := ClusterProxySecret()
	if secret == "" {
		return false
	}

	// Prefer v2. Bind the MAC against the full request-URI (path + query) —
	// every signer (cluster gRPC proxy, sub-handler relays in
	// feature/cluster/handler.go) feeds the path-with-query into
	// SignProxyRequestV2 so a captured header can't be re-targeted to a
	// different endpoint or different query params. Stripping the query
	// here would silently reject any forwarded request whose URL carries
	// one (e.g. /logs/read?source=syslog), leaving the receiving JWT
	// middleware to 401 the request because the proxy bypass never
	// activated.
	if v2 := r.Header.Get(InternalProxyHeaderV2); v2 != "" {
		return validateV2(secret, v2, r.Method, r.URL.RequestURI())
	}

	// The legacy v1 static-secret header is no longer accepted: senders went
	// v2-only in v0.56.0 and the receive branch was removed in v0.57.0. The
	// header name lives on only in the defensive strip lists (HTTP relay +
	// gRPC receive) so a forwarded request can never smuggle one.
	return false
}

// SignProxyRequestV2 returns the value to put in InternalProxyHeaderV2 for a
// fresh outgoing request. method + path bind the MAC to the specific
// endpoint so a captured header can't be re-targeted.
func SignProxyRequestV2(method, path string) string {
	secret := ClusterProxySecret()
	if secret == "" {
		return ""
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		// Returning empty leaves the outbound request without proxy auth (it
		// will 401 at the peer); a crypto/rand read failure is effectively
		// impossible on supported platforms.
		return ""
	}
	nonce := hex.EncodeToString(nonceBytes)
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + ":" + nonce + ":" + method + ":" + path))
	return ts + ":" + nonce + ":" + hex.EncodeToString(mac.Sum(nil))
}

// validateV2 returns true when (timestamp, nonce, mac) checks out for the
// given method + path. Side effect: records the nonce in the dedup cache
// so a replay of the same tuple within nonceCacheTTL fails.
func validateV2(secret, header, method, path string) bool {
	parts := strings.SplitN(header, ":", 3)
	if len(parts) != 3 {
		return false
	}
	tsStr, nonce, providedMAC := parts[0], parts[1], parts[2]
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now().UnixMilli()
	skewMs := proxyClockSkew.Milliseconds()
	if ts < now-skewMs || ts > now+skewMs {
		return false
	}

	expected := hmac.New(sha256.New, []byte(secret))
	expected.Write([]byte(tsStr + ":" + nonce + ":" + method + ":" + path))
	if subtle.ConstantTimeCompare([]byte(providedMAC), []byte(hex.EncodeToString(expected.Sum(nil)))) != 1 {
		return false
	}

	// Replay check + insert. Nonces survive in the cache for nonceCacheTTL;
	// concurrent valid requests with different nonces all proceed.
	if !registerNonce(nonce) {
		return false
	}
	return true
}

// sameRequestWindow is the grace period within which a repeated nonce is
// treated as a same-request re-check (e.g. JWT middleware then CSRF
// middleware on the same inbound request) rather than a replay attack.
// JWT middleware validates → registers nonce → returns control to echo;
// echo dispatches the next middleware (CSRF) which calls
// IsInternalProxyRequest again on the SAME request. Without this grace
// window the second call would see the nonce in the cache and reject
// the request as a replay, breaking every cross-node POST that wants
// the internal-proxy bypass. The window is small enough that a network
// replay attacker (with at least one RTT of delay between capturing and
// replaying the nonce) cannot fit inside it on any realistic link.
const sameRequestWindow = 50 * time.Millisecond

// registerNonce returns true when the nonce hasn't been seen recently and
// records it. Returns false on replay. Periodically GCs expired entries.
// A nonce seen again within sameRequestWindow is accepted as a same-
// request re-check; outside that window it's treated as a replay.
func registerNonce(nonce string) bool {
	nonceCacheMu.Lock()
	defer nonceCacheMu.Unlock()

	now := time.Now()
	if seen, exists := nonceCache[nonce]; exists {
		if now.Sub(seen) <= sameRequestWindow {
			// Same-request middleware re-check; allow.
			return true
		}
		if now.Sub(seen) <= nonceCacheTTL {
			return false // replay
		}
		// Expired entry — overwrite below.
	}
	nonceCache[nonce] = now

	// Best-effort GC: drop entries past their TTL whenever the cache grows
	// past a soft cap. Keeps memory bounded under a normal heartbeat load
	// (~1 unique nonce per second per cluster node).
	if len(nonceCache) > 2048 {
		for k, t := range nonceCache {
			if now.Sub(t) > nonceCacheTTL {
				delete(nonceCache, k)
			}
		}
	}
	return true
}
