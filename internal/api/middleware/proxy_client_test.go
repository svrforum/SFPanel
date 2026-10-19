package middleware

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// Every relay builds a client of its own and sends one request with it (two
// for the SSE fallback). A connection it kept alive would sit idle with
// nothing to reuse it, open on both nodes until one of them restarts.
func TestARelayClientLeavesNoConnectionOpen(t *testing.T) {
	var open atomic.Int32
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	target.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	target.Start()
	defer target.Close()

	const relays = 20
	for i := 0; i < relays; i++ {
		resp, err := newRemoteHTTPClient(time.Minute, nil, cluster.PanelEndpoint{}).Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	for deadline := time.Now().Add(2 * time.Second); open.Load() > 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := open.Load(); n > 0 {
		t.Fatalf("%d of %d relays left their connection open", n, relays)
	}
}
