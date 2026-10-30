package websocket

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

// upgradeWithoutCredentials sends h a WebSocket upgrade with no ticket,
// cookie or token, and returns every byte the server sends back within a
// second.
func upgradeWithoutCredentials(t *testing.T, h echo.HandlerFunc) string {
	t.Helper()
	e := echo.New()
	e.GET("/ws", h)
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := "GET /ws HTTP/1.1\r\nHost: " + srv.Listener.Addr().String() +
		"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	got, _ := io.ReadAll(conn)
	return string(got)
}

// An upgrade the check refuses is answered 401 and goes no further. It used
// to be upgraded behind the 401 all the same: AuthenticateWS answered with
// c.JSON's error, which is nil once the 401 is written, so every caller went
// on to Upgrader.Upgrade, and a client that read past the 401 had the stream.
func TestAnUpgradeWithoutCredentialsIsRefusedNotUpgraded(t *testing.T) {
	got := upgradeWithoutCredentials(t, MetricsWS(func() string { return strings.Repeat("k", 32) }))
	if !strings.HasPrefix(got, "HTTP/1.1 401") || strings.Contains(got, "101 Switching Protocols") {
		t.Fatalf("answered %q", got)
	}
}

// The refusal is the caller's to propagate: AuthenticateWS says so with
// auth.ErrUnauthenticatedWS, as the PTY routes' check does.
func TestAuthenticateWSStopsItsCaller(t *testing.T) {
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/ws/metrics", nil), rec)
	user, err := AuthenticateWS(c, strings.Repeat("k", 32))
	if !errors.Is(err, auth.ErrUnauthenticatedWS) || user != "" || rec.Code != http.StatusUnauthorized {
		t.Fatalf("user %q, err %v, status %d", user, err, rec.Code)
	}
}
