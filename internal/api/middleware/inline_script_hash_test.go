package middleware

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// hashOf mirrors what a browser computes for a CSP script hash: sha256 over
// the raw bytes between the tags, no trimming.
func hashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// extractInlineHashes is InlineScriptHashes minus the embed.FS plumbing, so
// the parsing rules can be exercised against arbitrary documents.
func extractInlineHashes(html string) []string {
	var out []string
	for _, m := range inlineScriptRe.FindAllStringSubmatch(html, -1) {
		if srcAttrRe.MatchString(m[0]) || len(m[1]) == 0 {
			continue
		}
		out = append(out, hashOf(m[1]))
	}
	return out
}

func TestInlineScriptExtraction(t *testing.T) {
	const body = `console.log("hi")`
	cases := []struct {
		name string
		html string
		want []string
	}{
		{"plain inline script", "<head><script>" + body + "</script></head>", []string{hashOf(body)}},
		{"attributes before the body", `<script type="text/javascript">` + body + `</script>`, []string{hashOf(body)}},
		{"external script needs no hash", `<script type="module" src="/assets/app.js"></script>`, nil},
		{"external with src last", `<script defer src="/a.js"></script>`, nil},
		{"empty script contributes nothing", `<script></script>`, nil},
		{"no scripts at all", `<html><body>hi</body></html>`, nil},
		{"mixed document", `<script src="/a.js"></script><script>` + body + `</script>`, []string{hashOf(body)}},
		{"uppercase tag", `<SCRIPT>` + body + `</SCRIPT>`, []string{hashOf(body)}},
	}
	for _, tc := range cases {
		got := extractInlineHashes(tc.html)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: hash %d = %s, want %s", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// The body is hashed byte for byte — a browser will not match a policy built
// from trimmed content, and the failure mode is a silently blocked script.
func TestInlineScriptHashIsByteExact(t *testing.T) {
	const padded = "\n      var x = 1\n    "
	got := extractInlineHashes("<script>" + padded + "</script>")
	if len(got) != 1 || got[0] != hashOf(padded) {
		t.Fatalf("whitespace was normalised away: got %v", got)
	}
	if got[0] == hashOf(strings.TrimSpace(padded)) {
		t.Error("hash matched the trimmed body, so surrounding whitespace was dropped")
	}
}

func TestSecurityHeadersCSP(t *testing.T) {
	run := func(hashes ...string) string {
		e := echo.New()
		h := SecurityHeaders(hashes...)(func(c echo.Context) error { return c.NoContent(http.StatusOK) })
		rec := httptest.NewRecorder()
		if err := h(e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)); err != nil {
			t.Fatal(err)
		}
		return rec.Header().Get("Content-Security-Policy")
	}

	// Scoped to the script-src directive: style-src legitimately carries
	// 'unsafe-inline' (the SPA sets element styles), and a whole-policy
	// substring check would trip over it.
	scriptSrcOf := func(csp string) string {
		for _, d := range strings.Split(csp, ";") {
			if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src ") {
				return d
			}
		}
		return ""
	}

	bare := scriptSrcOf(run())
	if bare != "script-src 'self'" {
		t.Errorf("without hashes script-src should stay 'self' only, got %q", bare)
	}

	want := hashOf("var a=1")
	withHash := scriptSrcOf(run(want))
	if withHash != "script-src 'self' "+want {
		t.Errorf("script-src = %q, want it to carry the hash", withHash)
	}
	for _, csp := range []string{bare, withHash} {
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("script-src must never relax to unsafe-*: %q", csp)
		}
	}
	// Every other directive stays put.
	full := run(want)
	for _, d := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'self'", "form-action 'self'"} {
		if !strings.Contains(full, d) {
			t.Errorf("directive %q missing from %q", d, full)
		}
	}
}
