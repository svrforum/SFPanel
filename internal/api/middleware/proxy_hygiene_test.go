package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// What leaves for another node carries the operator's identity and nothing
// they authenticate with: no access token (valid on every manager for a day),
// no refresh or CSRF cookie, no ?token= or ?ticket=, no internal-proxy header
// signed here — the receiver re-signs its own loopback hop.
func TestProxyAPIRequestCarriesNoCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/read?path=/etc&node=n2&token=jwt&ticket=tk", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer user-jwt")
	req.Header.Set("Cookie", "sfpanel_refresh=r; sfpanel_csrf=c")
	req.Header.Set("X-CSRF-Token", "c")
	req.Header.Set("X-SFPanel-Original-Node", "spoofed")
	req.Header.Set("X-SFPanel-Internal-Proxy-V2", "forged")
	req.Header.Set("Content-Type", "application/json")
	c := echo.New().NewContext(req, httptest.NewRecorder())
	c.Set("username", "alice")

	got := buildProxyAPIRequest(c, "n1", []byte("{}"))
	if got.AuthToken != "" {
		t.Fatalf("access token forwarded: %q", got.AuthToken)
	}
	for k := range got.Headers {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Cookie", "X-Csrf-Token", "X-Sfpanel-Internal-Proxy", "X-Sfpanel-Internal-Proxy-V2":
			t.Fatalf("forwarded header %s", k)
		}
	}
	if got.Headers["X-SFPanel-Original-User"] != "alice" || got.Headers["X-SFPanel-Original-Node"] != "n1" {
		t.Fatalf("identity headers: %v", got.Headers)
	}
	if got.Headers["Content-Type"] != "application/json" {
		t.Fatal("an ordinary header was dropped")
	}
	if strings.Contains(got.Path, "token=") || strings.Contains(got.Path, "ticket=") || strings.Contains(got.Path, "node=") || !strings.Contains(got.Path, "path=%2Fetc") {
		t.Fatalf("forwarded path %q", got.Path)
	}
}

// The HTTP relays (SSE, binary) strip the same things.
func TestHTTPRelayCarriesNoCredentials(t *testing.T) {
	var seen *http.Request
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Clone(r.Context()) }))
	defer target.Close()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/download?path=/x&node=n2&token=jwt", nil)
	req.Header.Set("Cookie", "sfpanel_refresh=r")
	req.Header.Set("X-CSRF-Token", "c")
	req.Header.Set("X-SFPanel-Original-Node", "spoofed")
	c := echo.New().NewContext(req, httptest.NewRecorder())
	url := target.URL + req.URL.Path + "?" + relayQuery(req.URL.Query())
	if err := executeHTTPRelay(c, url, target.Client(), nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	if seen == nil {
		t.Fatal("target not reached")
	}
	for _, h := range []string{"Cookie", "X-Csrf-Token", "X-Sfpanel-Original-Node"} {
		if seen.Header.Get(h) != "" {
			t.Fatalf("relayed %s", h)
		}
	}
	if seen.URL.Query().Get("token") != "" || seen.URL.Query().Get("node") != "" || seen.URL.Query().Get("path") != "/x" {
		t.Fatalf("relayed query %q", seen.URL.RawQuery)
	}
}

func TestProxyTargetFor(t *testing.T) {
	nodes := []*cluster.Node{{ID: "n2", Name: "web"}, {ID: "web", Name: "db"}}
	if _, local, _ := proxyTargetFor("/api/v1/auth/logout", "n2", "n1", nodes); !local {
		t.Fatal("an /auth path was proxied: the session belongs to the node the browser talks to")
	}
	if _, local, _ := proxyTargetFor("/api/v1/system/info", "n1", "n1", nodes); !local {
		t.Fatal("self not local")
	}
	if tgt, _, _ := proxyTargetFor("/api/v1/system/info", "web", "n1", nodes); tgt == nil || tgt.ID != "web" {
		t.Fatalf("a node ID must win over another node's name, got %+v", tgt)
	}
	if tgt, _, _ := proxyTargetFor("/api/v1/system/info", "db", "n1", nodes); tgt == nil || tgt.ID != "web" {
		t.Fatalf("name lookup, got %+v", tgt)
	}
	if _, _, err := proxyTargetFor("/api/v1/system/info", "nope", "n1", nodes); err == nil {
		t.Fatal("unknown node accepted")
	}
}
