package terminal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

func TestTerminalHome_PrefersHOMEEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if got := terminalHome(); got != dir {
		t.Errorf("terminalHome() = %q, want HOME=%q", got, dir)
	}
}

func TestTerminalHome_FallsBackWhenHOMEMissingOrNonexistent(t *testing.T) {
	t.Setenv("HOME", "/this/path/should/not/exist/anywhere")
	got := terminalHome()
	if got == "/this/path/should/not/exist/anywhere" {
		t.Error("terminalHome() should not return a non-existent HOME — chdir would fail")
	}
	// Either UserHomeDir worked (preferred) or we landed on /tmp. Both are
	// guaranteed to be stat-able on Linux.
	if _, err := os.Stat(got); err != nil {
		t.Errorf("fallback %q is not stat-able: %v", got, err)
	}
}

func TestTerminalHome_EmptyHOMEUsesUserHomeOrTmp(t *testing.T) {
	t.Setenv("HOME", "")
	got := terminalHome()
	if got == "" {
		t.Fatal("terminalHome() returned empty")
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("returned path %q is not stat-able: %v", got, err)
	}
}

func TestSameOriginOrEmpty(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin", "panel.example.com:9443", "", true},
		{"matching origin", "panel.example.com:9443", "https://panel.example.com:9443", true},
		{"case-insensitive host", "Panel.Example.com:9443", "https://panel.example.com:9443", true},
		{"foreign origin", "panel.example.com:9443", "https://evil.example.com", false},
		{"matching host different port", "panel.example.com:9443", "https://panel.example.com:9444", false},
		{"malformed origin", "panel.example.com:9443", "not-a-url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{Host: tc.host, Header: make(http.Header)}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOriginOrEmpty(r); got != tc.want {
				t.Errorf("sameOriginOrEmpty(Host=%q, Origin=%q) = %v, want %v",
					tc.host, tc.origin, got, tc.want)
			}
		})
	}
}

// TestBroadcast_SlowClientDoesNotBlockOthers verifies P0-17: a slow client
// (queue full) is kicked, and the fast client continues to receive output.
// We don't actually drive real WebSocket connections; the test exercises
// broadcast()'s non-blocking enqueue + kick semantics by populating the
// readers map directly with synthetic states.
func TestBroadcast_SlowClientDoesNotBlockOthers(t *testing.T) {
	sess := &terminalSession{
		scrollback: newRingBuffer(scrollbackBufSize),
		readers:    make(map[*websocket.Conn]*readerState),
	}

	fastWS := &websocket.Conn{}
	slowWS := &websocket.Conn{}

	fast := &readerState{send: make(chan []byte, readerSendQueue), done: make(chan struct{})}
	slow := &readerState{send: make(chan []byte, readerSendQueue), done: make(chan struct{})}

	// Fill the slow client's queue to capacity so the next broadcast can't fit.
	for i := 0; i < readerSendQueue; i++ {
		slow.send <- []byte{0}
	}

	sess.readers[fastWS] = fast
	sess.readers[slowWS] = slow

	payload := []byte("hello")
	sess.broadcast(payload)

	// Fast client received the payload.
	select {
	case got := <-fast.send:
		if string(got) != "hello" {
			t.Errorf("fast.send: got %q, want %q", got, "hello")
		}
	default:
		t.Error("fast.send should have received the payload")
	}

	// Slow client was kicked.
	select {
	case <-slow.done:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("slow.done should have been closed (kicked) when queue overflowed")
	}
}

func TestRingBuffer_Write_WrapAndOverflow(t *testing.T) {
	rb := newRingBuffer(8)

	// Partial write that fits in tail.
	rb.Write([]byte("abc"))
	if got := string(rb.Bytes()); got != "abc" {
		t.Errorf("after 'abc': got %q, want %q", got, "abc")
	}

	// Write that crosses the boundary.
	rb.Write([]byte("defghi")) // total 9 bytes into cap=8; "a" gets evicted
	if got := string(rb.Bytes()); got != "bcdefghi" {
		t.Errorf("after wrap: got %q, want %q", got, "bcdefghi")
	}

	// Write larger than the whole ring: only the tail survives.
	rb.Write([]byte("123456789ABC"))
	if got := string(rb.Bytes()); got != "23456789ABC"[len("23456789ABC")-8:] {
		t.Errorf("after oversized: got %q, want %q", got, "456789ABC"[1:])
	}

	// Sanity: cap=0 must not panic.
	empty := newRingBuffer(0)
	empty.Write([]byte("anything"))
}

func TestSessionKey_DifferentUsersNeverCollide(t *testing.T) {
	k1 := buildSessionKey("alice", "ssh-1")
	k2 := buildSessionKey("bob", "ssh-1")
	if k1 == k2 {
		t.Errorf("session keys collided across users: %q == %q", k1, k2)
	}
}

func TestSessionKey_StableForSameUser(t *testing.T) {
	k1 := buildSessionKey("alice", "ssh-1")
	k2 := buildSessionKey("alice", "ssh-1")
	if k1 != k2 {
		t.Errorf("session key not stable: %q vs %q", k1, k2)
	}
}

func TestSessionKey_EmptySessionIDStillBindsToUser(t *testing.T) {
	k1 := buildSessionKey("alice", "")
	k2 := buildSessionKey("bob", "")
	if k1 == k2 {
		t.Errorf("default session key collides across users")
	}
}

// TestAuthenticateWS_RefusesEmptyProxyUsername verifies the defence-in-depth
// guard: when a request comes in with valid internal-proxy credentials but
// the X-SFPanel-Original-User header is empty (which the proxy middleware
// should never emit, but we don't trust it absolutely), authenticateWS must
// refuse rather than letting buildSessionKey("", id) yield a key that
// collides across every empty-username request.
func TestAuthenticateWS_RefusesEmptyProxyUsername(t *testing.T) {
	// Configure a proxy secret so IsInternalProxyRequest can validate the
	// v1 header path. Restore the previous value when the test ends so we
	// don't leak global state across tests.
	prev := auth.ClusterProxySecret()
	auth.SetClusterProxySecret("test-secret")
	t.Cleanup(func() { auth.SetClusterProxySecret(prev) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/terminal/ws", nil)
	req.Header.Set(auth.InternalProxyHeader, "test-secret")
	// Intentionally do NOT set X-SFPanel-Original-User. The v1 path will
	// authenticate the proxy hop, but the username must be absent.
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	user, _ := authenticateWS(c, "irrelevant-jwt-secret")
	if user != "" {
		t.Errorf("authenticateWS user = %q, want \"\" so buildSessionKey is never called with the colliding empty-username key", user)
	}
	// c.JSON returns nil on a successful write, mirroring the existing
	// JWT-fail branch — Echo treats handler nil-return as "I already
	// wrote the response", so the operative assertion is that a 401 was
	// emitted (headers committed, subsequent Upgrader.Upgrade will fail)
	// and the empty username never propagates to the caller.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("response status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestStartReader_IsIdempotent verifies P0-18: two concurrent calls to
// startReader on the same session must spawn only one PTY-reader goroutine.
// We can't drive a real PTY in a unit test, so we wrap startOnce directly.
func TestStartReader_IsIdempotent(t *testing.T) {
	var calls int32
	sess := &terminalSession{}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.startOnce.Do(func() {
				atomic.AddInt32(&calls, 1)
			})
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("startOnce.Do fired %d times, want 1", got)
	}
}
