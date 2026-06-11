package middleware

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// captureSlog redirects the default slog output into a buffer for the
// duration of the test so log emission can be asserted.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func runRequestLogger(t *testing.T, path string, handler echo.HandlerFunc) (*bytes.Buffer, error) {
	t.Helper()
	buf := captureSlog(t)
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath(path)
	err := RequestLogger()(handler)(c)
	return buf, err
}

func TestRequestLoggerSkip_Classification(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/v1/health", true},
		{"/api/v1/system/info", true},
		{"/ws/terminal", true},
		// Dead route removed from the skip list; must classify as loggable.
		{"/api/v1/monitor/metrics", false},
		{"/api/v1/services", false},
	}
	for _, tc := range cases {
		if got := requestLoggerSkip(tc.path); got != tc.want {
			t.Errorf("requestLoggerSkip(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestRequestLogger_SkipPathSuccessNotLogged(t *testing.T) {
	buf, err := runRequestLogger(t, "/api/v1/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("successful skip-path request should not log, got: %s", buf.String())
	}
}

func TestRequestLogger_SkipPathErrorStatusLogged(t *testing.T) {
	buf, err := runRequestLogger(t, "/api/v1/health", func(c echo.Context) error {
		return c.String(http.StatusInternalServerError, "boom")
	})
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "status=500") {
		t.Errorf("5xx on skip path must log, got: %q", out)
	}
}

func TestRequestLogger_SkipPathHandlerErrorLogged(t *testing.T) {
	wantErr := errors.New("upgrade refused")
	buf, err := runRequestLogger(t, "/ws/terminal", func(c echo.Context) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("middleware must propagate handler error, got %v", err)
	}
	if buf.Len() == 0 {
		t.Error("handler error on skip path must log")
	}
}

func TestRequestLogger_NormalPathLogged(t *testing.T) {
	buf, err := runRequestLogger(t, "/api/v1/services", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "path=/api/v1/services") || !strings.Contains(out, "status=200") {
		t.Errorf("normal path must log method/path/status, got: %q", out)
	}
}
