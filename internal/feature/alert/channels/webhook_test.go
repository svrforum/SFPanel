package channels

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendWebhook_RejectsBadURL(t *testing.T) {
	for _, bad := range []string{"", "not a url", "ftp://example.com/hook", "://nohost", "example.com/hook"} {
		if err := SendWebhook(bad, "t", "m", "info"); !errors.Is(err, ErrChannelDelivery) {
			t.Errorf("SendWebhook(%q) err = %v, want ErrChannelDelivery", bad, err)
		}
	}
}

func TestSendWebhook_PostsPayload(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := SendWebhook(srv.URL, "Disk full", "root at 95%", "critical"); err != nil {
		t.Fatalf("SendWebhook: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	var p WebhookPayload
	if err := json.Unmarshal(gotBody, &p); err != nil {
		t.Fatalf("payload not JSON: %v — %s", err, gotBody)
	}
	if p.Title != "Disk full" || p.Severity != "critical" || p.Source != "SFPanel" {
		t.Errorf("payload fields wrong: %+v", p)
	}
	// The Slack/Mattermost-compatible text field carries the human summary.
	if !strings.Contains(p.Text, "Disk full") || !strings.Contains(p.Text, "root at 95%") {
		t.Errorf("text field missing summary: %q", p.Text)
	}
}

func TestSendWebhook_Non2xxFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := SendWebhook(srv.URL, "t", "m", "info"); !errors.Is(err, ErrChannelDelivery) {
		t.Errorf("err = %v, want ErrChannelDelivery on 500", err)
	}
}
