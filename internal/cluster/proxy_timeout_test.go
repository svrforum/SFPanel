package cluster

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// recordingPanel remembers the timeout the loopback hop asked for.
type recordingPanel struct {
	url  string
	mu   sync.Mutex
	asks []time.Duration
}

func (p *recordingPanel) URL(path string) string { return p.url + path }
func (p *recordingPanel) HTTPClient(timeout time.Duration) (*http.Client, error) {
	p.mu.Lock()
	p.asks = append(p.asks, timeout)
	p.mu.Unlock()
	return &http.Client{Timeout: timeout}, nil
}

// The sending node gives these operations five minutes; the receiving
// node's loopback hop gave all but compose thirty seconds, cut them off
// there and cancelled the subprocess with the request.
func TestLoopbackHopGrantsTheProxyTimeout(t *testing.T) {
	for _, path := range []string{
		"/api/v1/docker/compose/x/up", "/api/v1/appstore/apps/x/install", "/api/v1/packages/install",
		"/api/v1/packages/remove", "/api/v1/filesystems/format", "/api/v1/filesystems/resize",
		"/api/v1/swap", "/api/v1/system/restore", "/api/v1/disks/network-shares/tools/install",
	} {
		if ProxyTimeoutFor(path) != 5*time.Minute {
			t.Errorf("sender timeout for %s = %v", path, ProxyTimeoutFor(path))
		}
		panel := &recordingPanel{url: "http://127.0.0.1:1"}
		s := &GRPCServer{local: panel}
		_, _ = s.ProxyRequest(context.Background(), &pb.APIRequest{Method: http.MethodPost, Path: path})
		if len(panel.asks) == 0 || panel.asks[len(panel.asks)-1] != 5*time.Minute {
			t.Errorf("loopback timeout for %s = %v, want 5m", path, panel.asks)
		}
	}
	if ProxyTimeoutFor("/api/v1/system/info") != 30*time.Second {
		t.Error("an ordinary request got the long timeout")
	}
}

// A failed loopback hop answers with JSON the edge can parse, whatever the
// error text holds.
func TestLoopbackErrorIsValidJSON(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now: connection refused
	s := &GRPCServer{local: &recordingPanel{url: "http://" + addr}}
	resp, err := s.ProxyRequest(context.Background(), &pb.APIRequest{Method: http.MethodGet, Path: `/api/v1/files/read?path="quoted"`})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 502 || !json.Valid(resp.Body) {
		t.Fatalf("status %d, body %q", resp.StatusCode, resp.Body)
	}
	// And it still says what failed: valid JSON that lost the message would
	// leave the operator a 502 with no reason.
	var got struct {
		Success bool
		Error   struct{ Code, Message string }
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil || got.Success ||
		got.Error.Code != "PROXY_ERROR" || !strings.Contains(got.Error.Message, "quoted") {
		t.Fatalf("body %s decoded as %+v (%v)", resp.Body, got, err)
	}
}

// The proxy capped every ?node= request at thirty seconds while the handlers
// on the other side deliberately allow five minutes, so work that succeeds on
// the local node was cut off partway when aimed at another one — and the
// subprocess dies with the proxied request, leaving the remote host with a
// half-finished operation and nobody told.
func TestProxyAllowsTheOperationsTheHandlersAllow(t *testing.T) {
	long := []string{
		"/api/v1/docker/compose/myapp/up",
		"/api/v1/appstore/apps/immich/install",
		"/api/v1/packages/install",
		"/api/v1/packages/remove",
		"/api/v1/filesystems/format",
		"/api/v1/filesystems/resize",
		"/api/v1/swap",
		"/api/v1/system/restore",
		"/api/v1/system/restore/file",
		"/api/v1/disks/network-shares/tools/install",
	}
	for _, p := range long {
		if got := ProxyTimeoutFor(p); got != 5*time.Minute {
			t.Errorf("ProxyTimeoutFor(%q) = %v, want 5m", p, got)
		}
	}
}

// And the default stays short. A stuck request must not hold a proxy
// connection for five minutes because the list grew careless.
func TestProxyKeepsOrdinaryRequestsShort(t *testing.T) {
	short := []string{
		"/api/v1/system/info",
		"/api/v1/files?path=/etc",
		"/api/v1/docker/containers",
		"/api/v1/network/interfaces",
		"/api/v1/logs/read",
		"/api/v1/system/processes",
	}
	for _, p := range short {
		if got := ProxyTimeoutFor(p); got != 30*time.Second {
			t.Errorf("ProxyTimeoutFor(%q) = %v, want 30s", p, got)
		}
	}
}
