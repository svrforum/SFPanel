package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// ProxyToNode goes over gRPC, whose receiver discards any internal-proxy
// header and re-signs its own loopback hop. A header signed here would only
// be a manager credential handed to the target — an agent among them.
func TestProxyToNodeHeadersCarryNoSignature(t *testing.T) {
	// With a signing secret installed, so a signature added back here shows up
	// instead of coming out empty.
	auth.SetClusterProxySecret("managers-secret")
	t.Cleanup(func() { auth.SetClusterProxySecret("") })

	h := proxyToNodeHeaders("alice", []byte("{}"))
	for k := range h {
		if strings.Contains(strings.ToLower(k), "internal-proxy") {
			t.Fatalf("signed header %s on a gRPC proxy", k)
		}
	}
	if h["X-SFPanel-Original-User"] != "alice" || h["Content-Type"] != "application/json" {
		t.Fatalf("headers %v", h)
	}
}

// hopPanel is a LocalPanel whose loopback hop lands on an httptest server.
type hopPanel struct{ srv *httptest.Server }

func (p hopPanel) URL(path string) string { return p.srv.URL + path }
func (p hopPanel) HTTPClient(timeout time.Duration) (*http.Client, error) {
	return &http.Client{Timeout: timeout}, nil
}

// The receiving node authenticates its loopback hop with its own secret and
// with nothing the sender supplied. AuthToken carried the operator's access
// token: with no secret installed the hop goes out bare and is refused,
// rather than running on that token.
func TestLoopbackHopUsesOnlyItsOwnSecret(t *testing.T) {
	var seen http.Header
	var signed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		signed = auth.IsInternalProxyRequest(r)
	}))
	defer srv.Close()
	s := &GRPCServer{local: hopPanel{srv}}
	req := &pb.APIRequest{
		Method: http.MethodGet, Path: "/api/v1/system/info?x=1", AuthToken: "user-jwt",
		Headers: map[string]string{"Authorization": "Bearer user-jwt", auth.InternalProxyHeaderV2: "forged"},
	}
	t.Cleanup(func() { auth.SetClusterProxySecret("") })

	auth.SetClusterProxySecret("")
	if _, err := s.ProxyRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if seen.Get("Authorization") != "" || seen.Get(auth.InternalProxyHeaderV2) != "" {
		t.Fatalf("with no secret installed the hop carried Authorization=%q V2=%q",
			seen.Get("Authorization"), seen.Get(auth.InternalProxyHeaderV2))
	}

	auth.SetClusterProxySecret("this-node")
	if _, err := s.ProxyRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !signed || seen.Get("Authorization") != "" {
		t.Fatalf("hop signed with this node's secret: %v, Authorization=%q", signed, seen.Get("Authorization"))
	}
}

// A query that does not parse cleanly still loses its credentials. The strip
// used to hand such a query back untouched, ?token= included, and the WS
// relay dialled the target with it.
func TestStripAuthParamsDropsCredentialsFromAMalformedQuery(t *testing.T) {
	for _, q := range []string{"node=a1&token=jwt&x=%zz", "node=a1&ticket=tk&a;b=1"} {
		got := StripAuthParams(q)
		if strings.Contains(got, "token=") || strings.Contains(got, "ticket=") {
			t.Errorf("StripAuthParams(%q) = %q", q, got)
		}
		if !strings.Contains(got, "node=a1") {
			t.Errorf("StripAuthParams(%q) = %q dropped an ordinary parameter", q, got)
		}
	}
}
