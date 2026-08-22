package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

func TestIsBinaryRelayEndpoint(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Routes that the gRPC proxy can't handle (>4 MB MaxRecvMsgSize).
		{"/api/v1/files/download", true},
		{"/api/v1/files/upload", true},
		{"/api/v1/system/backup", true},
		{"/api/v1/system/restore", true},
		// Sibling routes that are small/JSON; must not be redirected.
		{"/api/v1/files", false},
		{"/api/v1/files/read", false},
		{"/api/v1/files/list", false},
		{"/api/v1/system/info", false},
		// SSE-style endpoints handled by relaySSE, not relayHTTP.
		{"/api/v1/system/update", false},
		{"/api/v1/docker/compose/foo/up-stream", false},
	}
	for _, tc := range cases {
		if got := isBinaryRelayEndpoint(tc.path); got != tc.want {
			t.Errorf("isBinaryRelayEndpoint(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsStreamingEndpoint(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/v1/docker/compose/foo/up-stream", true},
		{"/api/v1/docker/compose/foo/update-stream", true},
		{"/api/v1/system/update", true},
		{"/api/v1/appstore/apps/jellyfin/install", true},
		// Long-running package installs — SSE handlers that would otherwise
		// fall through to gRPC unary (30s + 4MB cap) on remote-node calls.
		{"/api/v1/packages/install-docker", true},
		{"/api/v1/packages/install-node", true},
		{"/api/v1/packages/install-claude", true},
		{"/api/v1/packages/install-codex", true},
		{"/api/v1/packages/install-gemini", true},
		{"/api/v1/packages/node-install-version", true},
		{"/api/v1/packages/upgrade", true},
		// Long-running docker image pull (SSE).
		{"/api/v1/docker/images/pull", true},
		// Long-running Tailscale install (SSE).
		{"/api/v1/network/tailscale/install", true},
		// Cluster orchestrated rolling update (SSE on the leader).
		{"/api/v1/cluster/update", true},
		// Stack migrate orchestrator (SSE phase stream).
		{"/api/v1/docker/compose/myproj/migrate", true},
		// Sync POSTs that intentionally stay unary.
		{"/api/v1/packages/install", false},
		{"/api/v1/packages/remove", false},
		{"/api/v1/files/download", false},
		{"/api/v1/system/backup", false},
		{"/api/v1/cluster/nodes", false},
		// Migrate siblings that must NOT classify as SSE: the bundle import is
		// a binary relay and the preflight is unary JSON.
		{"/api/v1/docker/compose/migrate-import", false},
		{"/api/v1/docker/compose/myproj/migrate/preflight", false},
	}
	for _, tc := range cases {
		if got := isStreamingEndpoint(tc.path); got != tc.want {
			t.Errorf("isStreamingEndpoint(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestStreamingAllowlist_KnownSSEHandlers locks in the contract that every
// path which sets Content-Type: text/event-stream must classify as a
// streaming endpoint. If a new SSE handler ships without an allowlist
// entry, remote-node calls hit the 4 MB / 30 s unary gRPC ceiling and
// fail mid-stream; this test catches the omission at CI time.
//
// Update both this list and isStreamingEndpoint() when adding a new
// SSE route. If you intentionally serve an SSE response synchronously
// (never callable with ?node=remote), document the exception inline.
func TestStreamingAllowlist_KnownSSEHandlers(t *testing.T) {
	sseRoutes := []string{
		// system
		"/api/v1/system/update",
		// docker
		"/api/v1/docker/images/pull",
		// appstore (id-templated)
		"/api/v1/appstore/apps/jellyfin/install",
		"/api/v1/appstore/apps/foo-bar/install",
		// packages (every installer is SSE)
		"/api/v1/packages/upgrade",
		"/api/v1/packages/install-docker",
		"/api/v1/packages/install-node",
		"/api/v1/packages/install-claude",
		"/api/v1/packages/install-codex",
		"/api/v1/packages/install-gemini",
		"/api/v1/packages/node-install-version",
		// compose (project-templated)
		"/api/v1/docker/compose/myproj/up-stream",
		"/api/v1/docker/compose/myproj/update-stream",
		"/api/v1/docker/compose/myproj/migrate",
		// network
		"/api/v1/network/tailscale/install",
		// cluster orchestrator
		"/api/v1/cluster/update",
	}
	for _, p := range sseRoutes {
		if !isStreamingEndpoint(p) {
			t.Errorf("SSE handler %q is missing from isStreamingEndpoint allowlist — remote-node calls will fail at the 4MB gRPC ceiling", p)
		}
	}
}

func TestCopyEndToEndHeaders_StripsHopByHop(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/octet-stream")
	src.Set("Content-Length", "12345")
	src.Set("Content-Disposition", `attachment; filename="big.tar.gz"`)
	src.Set("Connection", "close")
	src.Set("Keep-Alive", "timeout=5")
	src.Set("Transfer-Encoding", "chunked")
	src.Set("Upgrade", "websocket")
	src.Set("Proxy-Authorization", "secret")
	src.Set("Te", "trailers")
	// Mixed case — must be detected case-insensitively because RFC 7230 doesn't
	// guarantee canonical casing on the wire.
	src.Set("trailer", "Expires")

	dst := http.Header{}
	copyEndToEndHeaders(dst, src)

	mustHave := []string{"Content-Type", "Content-Length", "Content-Disposition"}
	for _, h := range mustHave {
		if dst.Get(h) == "" {
			t.Errorf("end-to-end header %q was dropped", h)
		}
	}
	mustNotHave := []string{
		"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade",
		"Proxy-Authorization", "Te", "Trailer",
	}
	for _, h := range mustNotHave {
		if dst.Get(h) != "" {
			t.Errorf("hop-by-hop header %q leaked through (value=%q)", h, dst.Get(h))
		}
	}
}

func TestCopyEndToEndHeaders_PreservesMultivalue(t *testing.T) {
	src := http.Header{}
	src.Add("Set-Cookie", "a=1")
	src.Add("Set-Cookie", "b=2")
	src.Add("X-Forwarded-For", "10.0.0.1")
	src.Add("X-Forwarded-For", "10.0.0.2")

	dst := http.Header{}
	copyEndToEndHeaders(dst, src)

	if got := len(dst.Values("Set-Cookie")); got != 2 {
		t.Errorf("Set-Cookie multi-value lost: got %d entries, want 2", got)
	}
	if got := len(dst.Values("X-Forwarded-For")); got != 2 {
		t.Errorf("X-Forwarded-For multi-value lost: got %d entries, want 2", got)
	}
}

func TestBuildRelayURL_DropsNodeParam(t *testing.T) {
	// Without ?node= the URL must still preserve other query params verbatim.
	u, _ := url.Parse("http://10.0.0.5:9443/api/v1/files/download?path=/tmp/big&node=abc-123&foo=bar")
	req := &http.Request{Method: "GET", URL: u}
	target := &cluster.Node{APIAddress: "10.0.0.6:9443"}

	got := buildRelayURL(req, target, nil)
	if !strings.HasPrefix(got, "http://10.0.0.6:9443/api/v1/files/download?") {
		t.Fatalf("base URL wrong: %s", got)
	}
	if strings.Contains(got, "node=") {
		t.Errorf("node param leaked into relay URL — would loop: %s", got)
	}
	if !strings.Contains(got, "path=%2Ftmp%2Fbig") || !strings.Contains(got, "foo=bar") {
		t.Errorf("non-node query params dropped: %s", got)
	}
}

func TestBuildRelayURL_NoQuery(t *testing.T) {
	u, _ := url.Parse("http://10.0.0.5:9443/api/v1/system/backup")
	req := &http.Request{Method: "POST", URL: u}
	target := &cluster.Node{APIAddress: "10.0.0.6:9443"}

	got := buildRelayURL(req, target, nil)
	want := "http://10.0.0.6:9443/api/v1/system/backup"
	if got != want {
		t.Errorf("buildRelayURL = %q, want %q", got, want)
	}
}

func TestBuildRelayURL_IgnoresSchemeStoredInAPIAddress(t *testing.T) {
	// This used to assert the opposite: that an https:// prefix stored in
	// APIAddress selected the scheme. That escape hatch never worked. Every
	// writer of APIAddress stores a bare ip:port, and verifySelfAddress applies
	// a correction back to that bare form ten seconds after each leader boot —
	// so a prefix an operator managed to set was erased before it mattered.
	//
	// The scheme now comes from whether the peer's panel CA is in replicated
	// state, which nothing overwrites. A stale prefix on an old record must be
	// stripped rather than obeyed, or the relay would talk HTTPS to a peer that
	// never enabled it.
	u, _ := url.Parse("http://10.0.0.5:9443/api/v1/system/backup?node=x")
	req := &http.Request{Method: "GET", URL: u}
	target := &cluster.Node{ID: "peer", APIAddress: "https://10.0.0.6:9443"}

	got := buildRelayURL(req, target, nil)
	want := "http://10.0.0.6:9443/api/v1/system/backup"
	if got != want {
		t.Errorf("buildRelayURL = %q, want %q (scheme comes from replicated state, not the address)", got, want)
	}
}

// runRelay drives executeHTTPRelay against an httptest target server.
// Returns the recorded client-facing response.
func runRelay(t *testing.T, method, path string, body []byte, headers http.Header, target *httptest.Server, authMutator func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	client := target.Client()
	if err := executeHTTPRelay(c, target.URL+path, client, authMutator, 30*time.Second); err != nil {
		t.Logf("executeHTTPRelay returned non-fatal error: %v", err)
	}
	return rec
}

// TestRelayDownload_StreamsLargeBodyAndHeaders proves the response path
// — what /files/download and /system/backup ride on. The 8 MB body is
// chosen to exceed the gRPC unary 4 MB ceiling that motivated this
// whole code path; if the relay ever silently buffers, that's also where
// memory pressure first shows up.
func TestRelayDownload_StreamsLargeBodyAndHeaders(t *testing.T) {
	const size = 8 * 1024 * 1024
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	want := sha256.Sum256(payload)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="big.bin"`)
		w.Header().Set("Content-Length", "8388608")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer target.Close()

	rec := runRelay(t, "GET", "/api/v1/files/download?path=/tmp/big.bin", nil, http.Header{}, target, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="big.bin"` {
		t.Errorf("Content-Disposition not forwarded: %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "8388608" {
		t.Errorf("Content-Length not forwarded: %q", got)
	}
	gotSum := sha256.Sum256(rec.Body.Bytes())
	if gotSum != want {
		t.Errorf("body sha256 mismatch: got %s, want %s", hex.EncodeToString(gotSum[:]), hex.EncodeToString(want[:]))
	}
	if rec.Body.Len() != size {
		t.Errorf("relayed body size = %d, want %d", rec.Body.Len(), size)
	}
}

// TestRelayUpload_StreamsRequestBody proves the request path —
// /files/upload and /system/restore — actually streams the multipart
// body to the target. Pre-refactor this used the gRPC unary path which
// would have blown up at 4 MB.
func TestRelayUpload_StreamsRequestBody(t *testing.T) {
	const size = 6 * 1024 * 1024
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte((i * 31) % 251)
	}
	want := sha256.Sum256(payload)

	var got [32]byte
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "want POST", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		got = sha256.Sum256(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	headers := http.Header{}
	headers.Set("Content-Type", "application/octet-stream")
	rec := runRelay(t, "POST", "/api/v1/files/upload", payload, headers, target, nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got != want {
		t.Errorf("upstream did not receive intact body: got %s, want %s",
			hex.EncodeToString(got[:]), hex.EncodeToString(want[:]))
	}
}

// TestRelayHopByHopHeaderStripping verifies both directions: hop-by-hop
// headers from the client must not reach the target, and hop-by-hop
// headers from the target must not reach the client.
func TestRelayHopByHopHeaderStripping(t *testing.T) {
	var receivedHopByHop string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bug if our relay forwards the client's Connection: close header.
		if v := r.Header.Get("Connection"); v != "" {
			receivedHopByHop = "Connection: " + v
		}
		if v := r.Header.Get("Proxy-Authorization"); v != "" {
			receivedHopByHop = "Proxy-Authorization: " + v
		}
		// Send back a hop-by-hop header that we expect the relay to strip.
		w.Header().Set("Keep-Alive", "timeout=300")
		w.Header().Set("X-App-Version", "1.0")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	headers := http.Header{}
	headers.Set("Connection", "close")
	headers.Set("Proxy-Authorization", "leaked-secret")
	headers.Set("X-Custom-OK", "preserved")

	rec := runRelay(t, "GET", "/api/v1/files/download?path=/x", nil, headers, target, nil)

	if receivedHopByHop != "" {
		t.Errorf("hop-by-hop header reached target: %s", receivedHopByHop)
	}
	if got := rec.Header().Get("Keep-Alive"); got != "" {
		t.Errorf("hop-by-hop header reached client: Keep-Alive=%q", got)
	}
	if got := rec.Header().Get("X-App-Version"); got != "1.0" {
		t.Errorf("end-to-end header dropped: X-App-Version=%q", got)
	}
}

// TestRelayAuthRewrite verifies that the client's Authorization header is
// stripped and replaced with whatever the auth mutator decides — that's
// how the cluster avoids forwarding user JWTs to peer nodes.
func TestRelayAuthRewrite(t *testing.T) {
	var sawAuth, sawProxy string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawProxy = r.Header.Get("X-SFPanel-Internal-Proxy")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer user-jwt-do-not-forward")

	mutator := func(req *http.Request) {
		req.Header.Set("X-SFPanel-Internal-Proxy", "shared-cluster-secret")
	}
	runRelay(t, "GET", "/api/v1/files/download?path=/x", nil, headers, target, mutator)

	if sawAuth != "" {
		t.Errorf("user JWT leaked across cluster boundary: %q", sawAuth)
	}
	if sawProxy != "shared-cluster-secret" {
		t.Errorf("proxy secret not set: %q", sawProxy)
	}
}

// TestRelayStripsClientProxyHeaders: a client must not be able to smuggle its
// own internal-proxy headers through the relay. Since the v1 send was dropped
// (v0.56.0) nothing overwrites the v1 slot anymore, so the relay has to strip
// both proxy headers from the inbound request explicitly — same rule the gRPC
// receive path applies.
func TestRelayStripsClientProxyHeaders(t *testing.T) {
	var sawV1, sawV2 string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawV1 = r.Header.Get(auth.InternalProxyHeader)
		sawV2 = r.Header.Get(auth.InternalProxyHeaderV2)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	headers := http.Header{}
	headers.Set(auth.InternalProxyHeader, "client-forged-v1-secret")
	headers.Set(auth.InternalProxyHeaderV2, "client-forged-v2-mac")

	runRelay(t, "GET", "/api/v1/files/download?path=/x", nil, headers, target, nil)

	if sawV1 != "" {
		t.Errorf("client-supplied v1 proxy header reached the target: %q", sawV1)
	}
	if sawV2 != "" {
		t.Errorf("client-supplied v2 proxy header reached the target: %q", sawV2)
	}
}

// TestProxyToLeader_NilManagerPassesThrough confirms that when cluster mode
// is disabled (mgr == nil), the helper returns (false, nil) so the FSM-write
// handler proceeds normally on the single node. Otherwise every admin-account
// call on a non-cluster install would 503.
func TestProxyToLeader_NilManagerPassesThrough(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handled, err := ProxyToLeader(c, nil)
	if handled {
		t.Errorf("expected handled=false for nil manager, got true")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected no response body, got %q", rec.Body.String())
	}
}

// newTestManagerWithProxySecret builds a minimal cluster.Manager whose
// ProxySecret() returns non-empty by dropping a stub ca.crt into a tempdir
// (LoadCACert just reads bytes and sha256s them; no x509 parsing). Keeps
// setAuthHeaders' real gate intact instead of stubbing it.
func newTestManagerWithProxySecret(t *testing.T, secretSeed string) *cluster.Manager {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte(secretSeed), 0o600); err != nil {
		t.Fatalf("write fake ca.crt: %v", err)
	}
	return cluster.NewManager(&config.ClusterConfig{CertDir: dir, DataDir: dir})
}

// TestSetAuthHeaders_V2SignsPathPlusQuery locks in C1 from
// docs/superpowers/plans/2026-05-24-cluster-and-update-hardening.md.
//
// Bug: setAuthHeaders used to sign the v2 MAC over origReq.URL.Path (path
// only) while the receiver in internal/auth/proxyauth.go validates against
// r.URL.RequestURI() (path + query). Any cross-node HTTP relay carrying a
// query string ( /files/download?path=…, /logs/read?source=syslog&lines=8,
// etc.) failed v2 validation, and because v1 is short-circuited when v2 is
// present the request 401'd at the receiving JWT middleware.
//
// Fix: sign over RequestURI() so signer and verifier agree.
func TestSetAuthHeaders_V2SignsPathPlusQuery(t *testing.T) {
	// SignProxyRequestV2 reads the auth-package global; the gate inside
	// setAuthHeaders reads mgr.ProxySecret(). In production both derive from
	// the same CA hash and are set together at startup; the test mirrors
	// that by configuring both sides.
	mgr := newTestManagerWithProxySecret(t, "test-ca-bytes-for-v2-sig-roundtrip")
	auth.SetClusterProxySecret(mgr.ProxySecret())
	t.Cleanup(func() { auth.SetClusterProxySecret("") })

	// Inbound request carries a query string — typical of cross-node file
	// download, log read, etc.
	orig := httptest.NewRequest("GET", "/api/v1/logs/read?source=syslog&lines=8", nil)

	// setAuthHeaders mutates this outbound request.
	out, err := http.NewRequest("GET", "http://peer:3628/api/v1/logs/read?source=syslog&lines=8", nil)
	if err != nil {
		t.Fatalf("build outbound request: %v", err)
	}

	setAuthHeaders(out, orig, mgr)

	if v2 := out.Header.Get(auth.InternalProxyHeaderV2); v2 == "" {
		t.Fatal("v2 header not set — mgr.ProxySecret() gate did not fire")
	}
	// Replay the outbound on the receiver — IsInternalProxyRequest reads
	// r.URL.RequestURI() (path+query) for validation. If the signer used
	// path-only, validation fails.
	if !auth.IsInternalProxyRequest(out) {
		t.Errorf("receiver rejected v2 sig — signer used path-only but verifier uses path+query")
	}
}

// TestSetAuthHeaders_NoV1Outbound pins the v0.56.0 contract that outbound
// relay traffic is v2-only: the legacy v1 static-secret header must not be
// attached, and the v2 header alone must satisfy the receiver. This is the
// representative regression guard for the relay family — the other send
// sites (grpc_server ProxyRequest, ws_relay, manager.ProxyToNode, the
// feature/cluster relays, compose migration push) follow the same pattern.
func TestSetAuthHeaders_NoV1Outbound(t *testing.T) {
	mgr := newTestManagerWithProxySecret(t, "test-ca-bytes-for-v1-drop")
	auth.SetClusterProxySecret(mgr.ProxySecret())
	t.Cleanup(func() { auth.SetClusterProxySecret("") })

	orig := httptest.NewRequest("GET", "/api/v1/system/info", nil)
	out, err := http.NewRequest("GET", "http://peer:3628/api/v1/system/info", nil)
	if err != nil {
		t.Fatalf("build outbound request: %v", err)
	}
	setAuthHeaders(out, orig, mgr)

	if v1 := out.Header.Get(auth.InternalProxyHeader); v1 != "" {
		t.Errorf("v1 static-secret header must no longer be sent, got %q", v1)
	}
	if out.Header.Get(auth.InternalProxyHeaderV2) == "" {
		t.Fatal("v2 header missing — outbound request would be unauthenticated")
	}
	if !auth.IsInternalProxyRequest(out) {
		t.Error("receiver must accept the v2-only outbound request")
	}
}
