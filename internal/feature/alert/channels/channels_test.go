package channels

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeBrokenServer returns a URL whose Post will fail with a transport-level
// error, which net/http wraps with the URL.
func makeBrokenServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close() // force net/http to surface "connection broken"
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTelegram_ErrorDoesNotLeakToken(t *testing.T) {
	const token = "1234567890:SECRETTOKEN_DONOTLEAK"
	err := sendTelegramTo(makeBrokenServer(t)+"/bot"+token+"/sendMessage", "x", "y")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error message leaks token: %q", err.Error())
	}
	if !errors.Is(err, ErrChannelDelivery) {
		t.Errorf("error is not classified as delivery: %v", err)
	}
}

func TestDiscord_ErrorDoesNotLeakWebhook(t *testing.T) {
	err := sendDiscordTo(makeBrokenServer(t)+"/webhooks/123/SECRETPATH", []byte(`{"content":"msg"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRETPATH") {
		t.Errorf("error leaks webhook secret: %q", err.Error())
	}
	if !errors.Is(err, ErrChannelDelivery) {
		t.Errorf("error is not classified as delivery: %v", err)
	}
}
