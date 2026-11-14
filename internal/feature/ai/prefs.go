package ai

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// The terminal page's preferences: the snippets the operator keeps and the key
// rows they arranged. They are stored here rather than in the browser so a
// phone and a desk find the same ones, each as one JSON value in the
// terminal_prefs table of the node the page is served from — per node, never
// replicated, and asked of that node whichever node's sessions are on screen.
//
// The module checks their shape and their size and nothing else: which keys
// exist is the page's knowledge (web lib/terminalKeysets), and an id a newer
// page saved is one an older page leaves out.

const (
	prefSnippets = "snippets"
	prefKeysets  = "keysets"

	snippetsMax    = 100
	snippetBodyMax = 2000 // runes
	keysetPagesMax = 6
	keysetPageKeys = 7
	keyLabelMax    = 8  // runes
	keyTextMax     = 64 // runes
)

// Snippet is a text the operator keeps to send again: a prompt for an AI CLI
// (kind "agent") or a command for a shell (kind "shell").
type Snippet struct {
	Kind string `json:"kind"`
	Body string `json:"body"`
}

// PrefKey is one key of a key row: either one the page knows by its ID, or one
// of the operator's own that types Text, and Enter after it when asked.
type PrefKey struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
	Text  string `json:"text,omitempty"`
	Enter bool   `json:"enter,omitempty"`
}

// Keysets is the key rows by session kind, a list of pages each. A kind that
// is absent has the page's own.
type Keysets map[string][][]PrefKey

var keyIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,24}$`)

func validPrefKind(kind string) bool { return kind == "agent" || kind == "shell" }

// hasControl reports a control character; newlines says whether a line break
// and a tab count as one (they do in a key, not in a snippet).
func hasControl(s string, newlines bool) bool {
	for _, r := range s {
		if (r == '\n' || r == '\t') && !newlines {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// checkSnippets returns why a list cannot be saved, or "".
func checkSnippets(list []Snippet) string {
	if len(list) > snippetsMax {
		return fmt.Sprintf("too many snippets (%d at most)", snippetsMax)
	}
	seen := map[Snippet]bool{}
	for i, s := range list {
		n := i + 1
		switch {
		case !validPrefKind(s.Kind):
			return fmt.Sprintf("snippet %d: unknown kind %q", n, s.Kind)
		case strings.TrimSpace(s.Body) == "":
			return fmt.Sprintf("snippet %d is empty", n)
		case utf8.RuneCountInString(s.Body) > snippetBodyMax:
			return fmt.Sprintf("snippet %d is too long (%d characters at most)", n, snippetBodyMax)
		case hasControl(s.Body, false):
			return fmt.Sprintf("snippet %d holds a control character", n)
		case seen[s]:
			return fmt.Sprintf("snippet %d is there twice", n)
		}
		seen[s] = true
	}
	return ""
}

// checkKey returns why a key cannot be saved, or "".
func checkKey(k PrefKey) string {
	own := k.Label != "" || k.Text != "" || k.Enter
	switch {
	case k.ID == "" && !own:
		return "has neither an id nor a text"
	case k.ID != "" && own:
		return "has both an id and a text of its own"
	case k.ID != "":
		if !keyIDPattern.MatchString(k.ID) {
			return "is not a key id"
		}
		return ""
	case strings.TrimSpace(k.Label) == "":
		return "has no label"
	case k.Text == "":
		return "has no text"
	case hasControl(k.Label, true) || hasControl(k.Text, true):
		return "holds a control character"
	case utf8.RuneCountInString(k.Label) > keyLabelMax:
		return fmt.Sprintf("label is too long (%d characters at most)", keyLabelMax)
	case utf8.RuneCountInString(k.Text) > keyTextMax:
		return fmt.Sprintf("text is too long (%d characters at most)", keyTextMax)
	}
	return ""
}

// checkKeysets returns why key rows cannot be saved, or "".
func checkKeysets(sets Keysets) string {
	for kind, pages := range sets {
		if !validPrefKind(kind) {
			return fmt.Sprintf("unknown kind %q", kind)
		}
		if len(pages) > keysetPagesMax {
			return fmt.Sprintf("%s: too many pages (%d at most)", kind, keysetPagesMax)
		}
		for p, page := range pages {
			if len(page) == 0 {
				return fmt.Sprintf("%s: page %d has no keys", kind, p+1)
			}
			if len(page) > keysetPageKeys {
				return fmt.Sprintf("%s: page %d has too many keys (%d at most)", kind, p+1, keysetPageKeys)
			}
			for i, k := range page {
				if why := checkKey(k); why != "" {
					return fmt.Sprintf("%s: page %d, key %d %s", kind, p+1, i+1, why)
				}
			}
		}
	}
	return ""
}

// readPref decodes one stored value into v. A row that is missing leaves v as
// it is; so does one that does not decode, which is logged: the page then has
// the panel's own keys and an empty list instead of no terminal at all.
func (h *Handler) readPref(key string, v any) error {
	var raw string
	err := h.DB.QueryRow(`SELECT value FROM terminal_prefs WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		slog.Warn("ai prefs row does not decode", "component", "ai", "key", key, "err", err)
	}
	return nil
}

func (h *Handler) writePref(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = h.DB.Exec(`INSERT INTO terminal_prefs (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, string(raw))
	return err
}

// Prefs — GET /ai/prefs: the snippets and the key rows.
func (h *Handler) Prefs(c echo.Context) error {
	var snippets []Snippet
	var sets Keysets
	if err := h.readPref(prefSnippets, &snippets); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the snippets")
	}
	if err := h.readPref(prefKeysets, &sets); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the keys")
	}
	// A row written by hand, or by a later version, is held to the same rules
	// as one that is saved: the page draws what it is given.
	if checkSnippets(snippets) != "" || snippets == nil {
		snippets = []Snippet{}
	}
	if checkKeysets(sets) != "" || sets == nil {
		sets = Keysets{}
	}
	return response.OK(c, map[string]any{"snippets": snippets, "keysets": sets})
}

// PutSnippets — PUT /ai/prefs/snippets {snippets: [...]}: the whole list, in
// the order it is shown. The last save wins.
func (h *Handler) PutSnippets(c echo.Context) error {
	var req struct {
		Snippets *[]Snippet `json:"snippets"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "the body is not JSON")
	}
	if req.Snippets == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "the body has no snippets")
	}
	list := *req.Snippets
	if why := checkSnippets(list); why != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, why)
	}
	if err := h.writePref(prefSnippets, list); err != nil {
		slog.Warn("ai prefs save", "component", "ai", "key", prefSnippets, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not save the snippets")
	}
	return response.OK(c, map[string]any{"snippets": list})
}

// PutKeysets — PUT /ai/prefs/keysets {keysets: {agent: [[...]], shell: [[...]]}}:
// the key rows of every kind at once. A kind left out, or sent with no pages,
// goes back to the page's own keys.
func (h *Handler) PutKeysets(c echo.Context) error {
	var req struct {
		Keysets *Keysets `json:"keysets"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "the body is not JSON")
	}
	if req.Keysets == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "the body has no keysets")
	}
	if why := checkKeysets(*req.Keysets); why != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, why)
	}
	sets := Keysets{}
	for kind, pages := range *req.Keysets {
		if len(pages) == 0 {
			continue
		}
		for _, page := range pages {
			for i := range page {
				page[i].Label = strings.TrimSpace(page[i].Label)
			}
		}
		sets[kind] = pages
	}
	if err := h.writePref(prefKeysets, sets); err != nil {
		slog.Warn("ai prefs save", "component", "ai", "key", prefKeysets, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not save the keys")
	}
	return response.OK(c, map[string]any{"keysets": sets})
}
