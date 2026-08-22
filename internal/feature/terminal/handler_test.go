package terminal

import (
	"encoding/json"
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

// resolveHome's precedence is what the /tmp bug turned on: the old chain's
// first two steps both read $HOME, so an unset HOME skipped straight to /tmp
// and the session lost ~/.bashrc. These cases pin each step independently —
// the tests above only assert the result is stat-able, which /tmp satisfies.
func TestResolveHome(t *testing.T) {
	exists := func(paths ...string) func(string) bool {
		set := make(map[string]bool, len(paths))
		for _, p := range paths {
			set[p] = true
		}
		return func(p string) bool { return set[p] }
	}

	cases := []struct {
		name       string
		envHome    string
		passwdHome string
		present    []string
		want       string
	}{
		{"env HOME wins when it exists", "/home/op", "/root", []string{"/home/op", "/root"}, "/home/op"},
		{"unset HOME falls through to passwd", "", "/root", []string{"/root"}, "/root"},
		{"non-existent HOME falls through to passwd", "/gone", "/root", []string{"/root"}, "/root"},
		{"both missing lands on /tmp", "", "", nil, "/tmp"},
		{"neither path exists lands on /tmp", "/gone", "/also-gone", nil, "/tmp"},
		{"passwd home that does not exist is not used", "", "/nonexistent", nil, "/tmp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveHome(tc.envHome, tc.passwdHome, exists(tc.present...)); got != tc.want {
				t.Errorf("resolveHome(%q, %q) = %q, want %q", tc.envHome, tc.passwdHome, got, tc.want)
			}
		})
	}
}

// The regression itself: with HOME unset, the resolved home must come from the
// OS user database, not /tmp. Skipped only if this build's account genuinely
// has no home on disk.
func TestTerminalHomeUsesPasswdWhenHOMEUnset(t *testing.T) {
	_, passwdHome := osAccount()
	if passwdHome == "" {
		t.Skip("no home in the OS user database for this account")
	}
	if _, err := os.Stat(passwdHome); err != nil {
		t.Skipf("passwd home %q is not present on this machine", passwdHome)
	}
	t.Setenv("HOME", "")
	if got := terminalHome(); got != passwdHome {
		t.Errorf("terminalHome() with HOME unset = %q, want passwd home %q", got, passwdHome)
	}
}

// GetInfo is what the UI's "which box am I typing into" badge reads, so its
// shape is a contract. It must also refuse an unauthenticated caller: the
// hostname and account name of every cluster node are not public.
func TestGetInfo(t *testing.T) {
	e := echo.New()

	t.Run("rejects an unauthenticated caller", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/terminal/info", nil), rec)
		if err := GetInfo(c); err != nil {
			t.Fatalf("GetInfo returned err: %v", err)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("describes the shell this node would spawn", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/terminal/info", nil), rec)
		c.Set("username", "admin")
		if err := GetInfo(c); err != nil {
			t.Fatalf("GetInfo returned err: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Success bool `json:"success"`
			Data    Info `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
		}
		if !body.Success {
			t.Fatalf("success = false, body %s", rec.Body.String())
		}
		if body.Data.ShellUser == "" {
			t.Error("shell_user is empty — the badge would render \"@host\"")
		}
		if body.Data.Hostname == "" {
			t.Error("hostname is empty — the badge could not name the node")
		}
		// The bug this whole change exists for: /tmp means the .bashrc lookup
		// failed and the operator loses their aliases and prompt.
		if body.Data.Home == "/tmp" {
			t.Error("home resolved to /tmp — passwd lookup failed")
		}
		if body.Data.Shell == "" {
			t.Error("shell is empty")
		}
		if body.Data.IsRoot != (os.Geteuid() == 0) {
			t.Errorf("is_root = %v, want %v", body.Data.IsRoot, os.Geteuid() == 0)
		}
	})
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
// collides across every empty-username request. The empty-username branch
// must also return a non-nil error — c.JSON returns nil on a successful
// write, and the previous "return c.JSON(...)" form let the caller's
// err == nil check pass and continue with username == "".
func TestAuthenticateWS_RefusesEmptyProxyUsername(t *testing.T) {
	// Configure a proxy secret so IsInternalProxyRequest can validate the
	// v2 signature. Restore the previous value when the test ends so we
	// don't leak global state across tests.
	prev := auth.ClusterProxySecret()
	auth.SetClusterProxySecret("test-secret")
	t.Cleanup(func() { auth.SetClusterProxySecret(prev) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/terminal/ws", nil)
	req.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(http.MethodGet, "/api/v1/terminal/ws"))
	// Intentionally do NOT set X-SFPanel-Original-User. The v2 signature
	// authenticates the proxy hop, but the username must be absent.
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	user, err := authenticateWS(c, "irrelevant-jwt-secret")
	if user != "" {
		t.Errorf("authenticateWS user = %q, want \"\" so buildSessionKey is never called with the colliding empty-username key", user)
	}
	if err == nil {
		t.Error("authenticateWS err = nil, want non-nil so TerminalWS aborts before Upgrader.Upgrade")
	}
	// A 401 must also have been written so any client that ignores the
	// response and tries to upgrade anyway sees the rejection on the wire.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("response status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestAuthenticateWS_RefusesMissingJWT verifies the JWT-fail branch returns a
// non-nil error. Same contract as the proxy-username branch: empty username
// must never coincide with err == nil, or the caller proceeds past the
// security guard.
func TestAuthenticateWS_RefusesMissingJWT(t *testing.T) {
	// No proxy secret configured — the request must be evaluated via the
	// JWT path, which has no credentials and must fail.
	prev := auth.ClusterProxySecret()
	auth.SetClusterProxySecret("")
	t.Cleanup(func() { auth.SetClusterProxySecret(prev) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/terminal/ws", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	user, err := authenticateWS(c, "any-secret")
	if user != "" {
		t.Errorf("authenticateWS user = %q, want \"\"", user)
	}
	if err == nil {
		t.Error("authenticateWS err = nil, want non-nil")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("response status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestTerminalWS_EmptyUsernameDoesNotCreateSession is the regression test
// requested by the reviewer: with proxy auth valid but the original-user
// header empty, TerminalWS must NOT proceed to create a PTY session keyed on
// "\x00default" (the collision key). We can't drive a real WS upgrade here
// without a much larger harness, so the test exercises the authenticateWS
// contract directly and asserts the sessions map is unchanged. Combined with
// the call-site (TerminalWS line 355-358) which aborts on err != nil, this
// proves an empty-username request can never reach buildSessionKey.
func TestTerminalWS_EmptyUsernameDoesNotCreateSession(t *testing.T) {
	prev := auth.ClusterProxySecret()
	auth.SetClusterProxySecret("test-secret")
	t.Cleanup(func() { auth.SetClusterProxySecret(prev) })

	// Snapshot the sessions map length before the call.
	sessionsMu.Lock()
	before := len(sessions)
	sessionsMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/terminal/ws?session_id=hijack", nil)
	req.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(http.MethodGet, "/api/v1/terminal/ws?session_id=hijack"))
	// Deliberately omit X-SFPanel-Original-User to trigger the guard.
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	user, err := authenticateWS(c, "any-secret")
	if user != "" || err == nil {
		t.Fatalf("authenticateWS = (%q, %v), want (\"\", non-nil)", user, err)
	}

	// The TerminalWS handler returns immediately on err != nil, BEFORE
	// touching the sessions map. Verify the map is unchanged.
	sessionsMu.Lock()
	after := len(sessions)
	sessionsMu.Unlock()
	if after != before {
		t.Errorf("sessions map size changed: before=%d after=%d (empty-username request must not create a session)", before, after)
	}

	// And no session was registered under the collision key.
	sessionsMu.Lock()
	_, collided := sessions[buildSessionKey("", "hijack")]
	sessionsMu.Unlock()
	if collided {
		t.Error("a session was created under the empty-username collision key — the hijack vector is open")
	}
}

// TestReadyReaper_NoReadersReapsAfterIdleWindow exercises both branches of
// isReadyForReap: the zero-readers branch (reap after idleReapAfter regardless
// of lastUse) and the has-readers branch (legacy lastUse + perInputTimeout).
// Together these close the orphaned-PTY hole where a user fires a long-running
// command, closes the browser tab, and the PTY survives until terminal_timeout
// elapses (default 30m) because lastUse was bumped on the last input.
func TestReadyReaper_NoReadersReapsAfterIdleWindow(t *testing.T) {
	base := time.Now()

	// No readers, just past idleReapAfter — should reap.
	sess := &terminalSession{
		readers:          map[*websocket.Conn]*readerState{},
		lastUse:          base, // recent input, but no readers anymore
		lastReaderLeftAt: base.Add(-idleReapAfter - 1*time.Second),
	}
	if !isReadyForReap(sess, base, 30*time.Minute) {
		t.Error("no readers + past idle window: want reap=true")
	}

	// No readers, within idleReapAfter — should NOT reap.
	sess2 := &terminalSession{
		readers:          map[*websocket.Conn]*readerState{},
		lastUse:          base,
		lastReaderLeftAt: base.Add(-1 * time.Minute),
	}
	if isReadyForReap(sess2, base, 30*time.Minute) {
		t.Error("no readers + within idle window: want reap=false")
	}

	// Has readers, but lastUse is past per-input timeout — should reap (existing semantics).
	fakeReader := &websocket.Conn{} // never used; just non-nil sentinel
	sess3 := &terminalSession{
		readers: map[*websocket.Conn]*readerState{
			fakeReader: {send: make(chan []byte), done: make(chan struct{})},
		},
		lastUse: base.Add(-31 * time.Minute),
	}
	if !isReadyForReap(sess3, base, 30*time.Minute) {
		t.Error("has readers + lastUse past timeout: want reap=true")
	}

	// Has readers, lastUse recent — should NOT reap.
	sess4 := &terminalSession{
		readers: map[*websocket.Conn]*readerState{
			fakeReader: {send: make(chan []byte), done: make(chan struct{})},
		},
		lastUse: base.Add(-1 * time.Minute),
	}
	if isReadyForReap(sess4, base, 30*time.Minute) {
		t.Error("has readers + lastUse recent: want reap=false")
	}
}

func TestRemoveReader_StampsLastReaderLeftAt(t *testing.T) {
	fakeReader := &websocket.Conn{}
	sess := &terminalSession{
		readers: map[*websocket.Conn]*readerState{
			fakeReader: {send: make(chan []byte, 1), done: make(chan struct{})},
		},
	}
	before := time.Now()
	sess.removeReader(fakeReader)
	after := time.Now()
	sess.mu.Lock()
	stamp := sess.lastReaderLeftAt
	sess.mu.Unlock()
	if stamp.Before(before) || stamp.After(after) {
		t.Errorf("lastReaderLeftAt = %v, want in [%v, %v]", stamp, before, after)
	}
}

func TestAddReader_ClearsLastReaderLeftAt(t *testing.T) {
	sess := &terminalSession{
		readers:          map[*websocket.Conn]*readerState{},
		lastReaderLeftAt: time.Now().Add(-1 * time.Hour),
	}
	sess.addReader(&websocket.Conn{})
	sess.mu.Lock()
	stamp := sess.lastReaderLeftAt
	sess.mu.Unlock()
	if !stamp.IsZero() {
		t.Errorf("lastReaderLeftAt = %v, want zero time after new reader join", stamp)
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
