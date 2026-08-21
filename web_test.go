package sfpanel

import (
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/middleware"
)

// index.html carries one inline script — the pre-paint theme applier — and the
// CSP allows it by hash. If the extraction ever returns nothing, the browser
// blocks the script and dark mode flashes white on every page load, silently:
// a blocked inline script produces a console entry and no other symptom. That
// shipped undetected once, which is why this asserts against the real embedded
// asset rather than a fixture.
func TestEmbeddedIndexYieldsInlineScriptHash(t *testing.T) {
	hashes := middleware.InlineScriptHashes(WebDistFS)
	if len(hashes) == 0 {
		t.Fatal("no inline script hash derived from web/dist/index.html; the CSP would block the theme script")
	}
	for _, h := range hashes {
		if !strings.HasPrefix(h, "'sha256-") || !strings.HasSuffix(h, "'") {
			t.Errorf("malformed CSP source expression: %q", h)
		}
	}
	if !strings.Contains(string(webDistIndex), "<script") {
		t.Fatal("index.html has no <script> at all — the build output looks wrong")
	}
}
