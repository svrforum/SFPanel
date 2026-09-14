package ai

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// latestSources: Claude publishes a plain-text version at the URL its own
// install.sh reads; the npm registry answers JSON for the other two.
var latestSources = map[string]string{
	ToolClaude: "https://downloads.claude.ai/claude-code-releases/latest",
	ToolCodex:  "https://registry.npmjs.org/@openai/codex/latest",
	ToolGemini: "https://registry.npmjs.org/@google/gemini-cli/latest",
}

const (
	latestTTL = time.Hour
	// latestFailTTL: a lookup that failed is memoised too, or an offline host
	// would re-dial on every request — but for minutes, not an hour. A blip
	// while the panel boots (network-online.target is not the internet) must
	// not hide the update badge until the hour is up.
	latestFailTTL = 5 * time.Minute
)

var latestHTTP = &http.Client{Timeout: 10 * time.Second}

type latestMemoEntry struct {
	version string
	at      time.Time
}

func parseLatest(tool, body string) string {
	if tool == ToolClaude {
		return versionNumber(strings.TrimSpace(body))
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(body), &v) != nil {
		return ""
	}
	return versionNumber(v.Version)
}

func fetchLatest(tool string) string {
	url, ok := latestSources[tool]
	if !ok {
		return ""
	}
	resp, err := latestHTTP.Get(url)
	if err != nil {
		slog.Debug("latest version lookup failed", "component", "ai", "tool", tool, "err", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ""
	}
	return parseLatest(tool, string(body))
}

// latestVersion is a network call on a request path, so it is memoised — an
// hour for an answer, latestFailTTL for a failure — and a failure is simply ""
// so the card shows no badge. Never an error: an offline host must still get
// its page.
func (h *Handler) latestVersion(tool string) string {
	h.memoMu.Lock()
	e, ok := h.latestMemo[tool]
	h.memoMu.Unlock()
	if ok {
		ttl := latestTTL
		if e.version == "" {
			ttl = latestFailTTL
		}
		if h.now().Sub(e.at) < ttl {
			return e.version
		}
	}
	v := fetchLatest(tool)
	h.memoMu.Lock()
	h.latestMemo[tool] = latestMemoEntry{version: v, at: h.now()}
	h.memoMu.Unlock()
	return v
}
