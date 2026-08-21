package middleware

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"regexp"
)

// CSP hashes for the SPA's inline scripts.
//
// index.html carries one inline script: the pre-paint theme applier, which has
// to run before the app bundle so a dark-mode user does not get a white flash.
// It cannot be deferred into the bundle (that runs after parse, which is the
// flash) and it cannot use a nonce (the page is a static embedded asset, not
// rendered per request).
//
// So the CSP allows it by hash. The hash is computed at startup from the very
// bytes that get served, rather than being written down as a constant: a
// constant goes stale the moment anyone edits the script, and the failure is
// silent — the browser blocks the script and the flash quietly returns. That
// is exactly what happened before this existed. Deriving it from the embedded
// file makes drift impossible rather than merely tested-for.

// inlineScriptRe matches a <script> element with no src attribute, capturing
// its body. Browsers hash the raw bytes between the tags — no trimming, no
// normalisation — so the capture is used verbatim.
var inlineScriptRe = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`)

// srcAttrRe detects a src attribute, which makes a script external and thus
// covered by 'self' rather than needing a hash.
var srcAttrRe = regexp.MustCompile(`(?is)^<script[^>]*\ssrc\s*=`)

// InlineScriptHashes returns a CSP source expression ("'sha256-…'") for every
// inline script in the embedded index.html. Returns nil when the file is
// missing or has none, in which case the policy stays as it was.
func InlineScriptHashes(webFS embed.FS) []string {
	data, err := webFS.ReadFile("web/dist/index.html")
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range inlineScriptRe.FindAllSubmatch(data, -1) {
		if srcAttrRe.Match(m[0]) {
			continue
		}
		if len(m[1]) == 0 {
			continue
		}
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}
