package release

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withLatestURL(t *testing.T, u string) {
	t.Helper()
	prev := LatestURL
	LatestURL = u
	t.Cleanup(func() { LatestURL = prev })
}

func TestFetchLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.81.0", "assets": []Asset{{Name: "checksums.txt", BrowserDownloadURL: "https://github.com/x/checksums.txt"}}})
	}))
	defer srv.Close()
	withLatestURL(t, srv.URL)
	rel, err := FetchLatest(context.Background())
	if err != nil || rel.TagName != "v0.81.0" || len(rel.Assets) != 1 || rel.Assets[0].Name != "checksums.txt" {
		t.Fatalf("%+v %v", rel, err)
	}
}

// GitHub refuses with 403 once the address has used its sixty requests an
// hour; the bare "GitHub API error" said nothing about why.
func TestFetchLatestNamesTheRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	withLatestURL(t, srv.URL)
	if _, err := FetchLatest(context.Background()); err == nil || !strings.Contains(err.Error(), "sixty") {
		t.Fatalf("got %v", err)
	}
}

// 429 is GitHub's other rate-limit answer; any other status is not the rate
// limit, and waiting an hour would not fix it. A release without a tag would
// send every node an update it refuses. The answer is read up to 1 MiB; the
// real one, with twenty assets, is under 40 KB.
func TestFetchLatestRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, body, want, notWant string
		status                    int
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, want: "sixty"},
		{name: "server error", status: http.StatusBadGateway, want: "HTTP 502", notWant: "sixty"},
		{name: "no tag", status: http.StatusOK, body: `{"assets":[]}`, want: "no tag"},
		{name: "too large", status: http.StatusOK, body: `{"tag_name":"v0.81.0","body":"` + strings.Repeat("x", 2<<20) + `"}`, want: "read the latest release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			withLatestURL(t, srv.URL)
			_, err := FetchLatest(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) || (tc.notWant != "" && strings.Contains(err.Error(), tc.notWant)) {
				t.Fatalf("got %v; want %q, not %q", err, tc.want, tc.notWant)
			}
		})
	}
}
