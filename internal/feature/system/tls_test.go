package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

func tlsHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := paneltls.New(dir).Ensure(); err != nil {
		t.Fatalf("prepare certificate material: %v", err)
	}
	return &Handler{TLSEnabled: true, TLSManaged: true, TLSDir: dir}, dir
}

// The one property this route must never lose: it hands out the trust anchor,
// and nothing else. A regression that widened it to the directory, or swapped
// the filename, would ship private keys to anyone who can log in.
func TestDownloadCACertServesOnlyTheCertificate(t *testing.T) {
	h, dir := tlsHandler(t)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/tls/ca.crt", nil), rec)

	if err := h.DownloadCACert(c); err != nil {
		t.Fatalf("DownloadCACert: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "BEGIN CERTIFICATE") {
		t.Fatal("response is not a certificate")
	}
	for _, forbidden := range []string{"PRIVATE KEY", "BEGIN EC PRIVATE"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response contains %q", forbidden)
		}
	}
	// It must be the CA, not the server certificate: installing the leaf as a
	// trust anchor would make every renewal break trust again.
	caPEM, err := os.ReadFile(filepath.Join(dir, paneltls.CACertFile))
	if err != nil {
		t.Fatal(err)
	}
	if body != string(caPEM) {
		t.Error("response is not byte-identical to ca.crt")
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}
}

func TestDownloadCACertWhenNotManaged(t *testing.T) {
	cases := map[string]*Handler{
		"TLS off":              {TLSEnabled: false},
		"operator certificate": {TLSEnabled: true, TLSManaged: false},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/tls/ca.crt", nil), rec)
			if err := h.DownloadCACert(c); err != nil {
				t.Fatalf("DownloadCACert: %v", err)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "CERTIFICATE") {
				t.Error("a panel with no managed CA still returned certificate material")
			}
		})
	}
}

func TestGetTLSStatus(t *testing.T) {
	decode := func(t *testing.T, rec *httptest.ResponseRecorder) TLSStatus {
		t.Helper()
		var body struct {
			Success bool      `json:"success"`
			Data    TLSStatus `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		if !body.Success {
			t.Fatalf("success=false: %s", rec.Body.String())
		}
		return body.Data
	}

	t.Run("managed panel reports its material", func(t *testing.T) {
		h, _ := tlsHandler(t)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/tls", nil), rec)
		if err := h.GetTLSStatus(c); err != nil {
			t.Fatal(err)
		}
		st := decode(t, rec)
		if !st.Enabled || !st.Managed {
			t.Fatalf("enabled=%v managed=%v, want both true", st.Enabled, st.Managed)
		}
		if st.CAFingerprint == "" {
			t.Error("no CA fingerprint — an operator cannot match what they installed")
		}
		// The whole point of the split lifetimes: the anchor outlives the leaf
		// by years, so installing it is a one-time chore.
		if !st.CANotAfter.After(st.NotAfter) {
			t.Errorf("CA expires %v, leaf %v — the CA must outlive the leaf", st.CANotAfter, st.NotAfter)
		}
		if len(st.DNSNames) == 0 {
			t.Error("no DNS names reported")
		}
	})

	t.Run("plain HTTP panel reports disabled without touching disk", func(t *testing.T) {
		h := &Handler{TLSEnabled: false, TLSDir: "/nonexistent"}
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/tls", nil), rec)
		if err := h.GetTLSStatus(c); err != nil {
			t.Fatal(err)
		}
		if st := decode(t, rec); st.Enabled {
			t.Error("a plain-HTTP panel reported TLS enabled")
		}
	})
}
