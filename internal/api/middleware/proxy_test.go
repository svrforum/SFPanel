package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
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
		{"/api/v1/files/download", false},
		{"/api/v1/system/backup", false},
		{"/api/v1/cluster/nodes", false},
	}
	for _, tc := range cases {
		if got := isStreamingEndpoint(tc.path); got != tc.want {
			t.Errorf("isStreamingEndpoint(%q) = %v, want %v", tc.path, got, tc.want)
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

	got := buildRelayURL(req, target)
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

	got := buildRelayURL(req, target)
	want := "http://10.0.0.6:9443/api/v1/system/backup"
	if got != want {
		t.Errorf("buildRelayURL = %q, want %q", got, want)
	}
}

func TestBuildRelayURL_HonorsHTTPSPrefix(t *testing.T) {
	// Operators may store https:// in APIAddress for clusters fronted by TLS.
	u, _ := url.Parse("http://10.0.0.5:9443/api/v1/system/backup?node=x")
	req := &http.Request{Method: "GET", URL: u}
	target := &cluster.Node{APIAddress: "https://10.0.0.6:9443"}

	got := buildRelayURL(req, target)
	if !strings.HasPrefix(got, "https://") {
		t.Errorf("https prefix lost: %s", got)
	}
}
