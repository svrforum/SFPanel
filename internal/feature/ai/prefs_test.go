package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
)

type prefsAnswer struct {
	Snippets []Snippet `json:"snippets"`
	Keysets  Keysets   `json:"keysets"`
}

func prefsOf(t *testing.T, h *Handler) (prefsAnswer, string) {
	t.Helper()
	rec := call(t, h.Prefs, http.MethodGet, "", "", "")
	var env struct {
		Success bool        `json:"success"`
		Data    prefsAnswer `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || !env.Success {
		t.Fatalf("prefs answered %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	return env.Data, rec.Body.String()
}

func prefsHandler(t *testing.T) *Handler {
	t.Helper()
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	return h
}

// refused asserts a 400 INVALID_REQUEST whose message names the reason.
func refused(t *testing.T, name string, rec *httptest.ResponseRecorder, reason string) {
	t.Helper()
	code, msg := failCode(t, rec)
	if rec.Code != http.StatusBadRequest || code != response.ErrInvalidRequest || !strings.Contains(msg, reason) {
		t.Errorf("%s: answered %d %s %q, want 400 %s naming %q", name, rec.Code, code, msg, response.ErrInvalidRequest, reason)
	}
}

func TestPrefs_NothingStoredIsEmptyNotNull(t *testing.T) {
	h := prefsHandler(t)
	_, raw := prefsOf(t, h)
	// The page maps over these: an absent list must still be a list.
	if !strings.Contains(raw, `"snippets":[]`) || !strings.Contains(raw, `"keysets":{}`) {
		t.Errorf("prefs answered %s, want an empty list and an empty object", raw)
	}
}

func TestPrefs_SnippetsRoundTripInOrderAndReplaceWhole(t *testing.T) {
	h := prefsHandler(t)
	first := `{"snippets":[{"kind":"agent","body":"테스트를 돌리고 실패한 것만 고쳐줘"},{"kind":"shell","body":"docker compose logs -f --tail 100"},{"kind":"agent","body":"line one\nline two"}]}`
	if rec := call(t, h.PutSnippets, http.MethodPut, first, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("put answered %d %s", rec.Code, rec.Body.String())
	}
	got, _ := prefsOf(t, h)
	want := []Snippet{{"agent", "테스트를 돌리고 실패한 것만 고쳐줘"}, {"shell", "docker compose logs -f --tail 100"}, {"agent", "line one\nline two"}}
	if len(got.Snippets) != len(want) {
		t.Fatalf("stored %+v, want %+v", got.Snippets, want)
	}
	for i := range want {
		if got.Snippets[i] != want[i] {
			t.Errorf("snippet %d is %+v, want %+v", i, got.Snippets[i], want[i])
		}
	}
	// A second save is the whole list again, not an addition.
	if rec := call(t, h.PutSnippets, http.MethodPut, `{"snippets":[{"kind":"shell","body":"ls -la"}]}`, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("second put answered %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := prefsOf(t, h); len(got.Snippets) != 1 || got.Snippets[0].Body != "ls -la" {
		t.Errorf("after the second save: %+v", got.Snippets)
	}
	// And an empty list empties it.
	call(t, h.PutSnippets, http.MethodPut, `{"snippets":[]}`, "", "")
	if got, _ := prefsOf(t, h); len(got.Snippets) != 0 {
		t.Errorf("after saving none: %+v", got.Snippets)
	}
}

func TestPrefs_SnippetsAreValidated(t *testing.T) {
	h := prefsHandler(t)
	kept := `{"snippets":[{"kind":"agent","body":"kept"}]}`
	call(t, h.PutSnippets, http.MethodPut, kept, "", "")
	many := make([]string, snippetsMax+1)
	for i := range many {
		many[i] = `{"kind":"shell","body":"echo ` + strings.Repeat("x", i+1) + `"}`
	}
	for name, tc := range map[string]struct{ body, reason string }{
		"not json":       {`{"snippets":`, "not JSON"},
		"no list":        {`{}`, "no snippets"},
		"unknown kind":   {`{"snippets":[{"kind":"vim","body":"x"}]}`, "unknown kind"},
		"empty":          {`{"snippets":[{"kind":"agent","body":"  \n "}]}`, "empty"},
		"too long":       {`{"snippets":[{"kind":"agent","body":"` + strings.Repeat("가", snippetBodyMax+1) + `"}]}`, "too long"},
		"a NUL":          {`{"snippets":[{"kind":"agent","body":"a\u0000b"}]}`, "control"},
		"an escape":      {`{"snippets":[{"kind":"shell","body":"a\u001b[2Jb"}]}`, "control"},
		"twice the same": {`{"snippets":[{"kind":"agent","body":"x"},{"kind":"agent","body":"x"}]}`, "twice"},
		"too many":       {`{"snippets":[` + strings.Join(many, ",") + `]}`, "too many"},
	} {
		refused(t, name, call(t, h.PutSnippets, http.MethodPut, tc.body, "", ""), tc.reason)
	}
	// A refused save changes nothing.
	if got, _ := prefsOf(t, h); len(got.Snippets) != 1 || got.Snippets[0].Body != "kept" {
		t.Errorf("after the refused saves: %+v", got.Snippets)
	}
	// At the limits it goes through: the same words for another kind, a body of exactly the cap.
	ok := `{"snippets":[{"kind":"agent","body":"x"},{"kind":"shell","body":"x"},{"kind":"agent","body":"` + strings.Repeat("가", snippetBodyMax) + `"}]}`
	if rec := call(t, h.PutSnippets, http.MethodPut, ok, "", ""); rec.Code != http.StatusOK {
		t.Errorf("a list at the limits answered %d %s", rec.Code, rec.Body.String())
	}
}

func TestPrefs_KeysetsRoundTrip(t *testing.T) {
	h := prefsHandler(t)
	body := `{"keysets":{"agent":[[{"id":"esc"},{"id":"ctrl-b"},{"label":"/compact","text":"/compact","enter":true},{"label":"|","text":"|"}],[{"id":"up"}]]}}`
	if rec := call(t, h.PutKeysets, http.MethodPut, body, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("put answered %d %s", rec.Code, rec.Body.String())
	}
	got, raw := prefsOf(t, h)
	pages := got.Keysets["agent"]
	if len(pages) != 2 || len(pages[0]) != 4 || len(pages[1]) != 1 {
		t.Fatalf("stored %s", raw)
	}
	if pages[0][0] != (PrefKey{ID: "esc"}) || pages[0][2] != (PrefKey{Label: "/compact", Text: "/compact", Enter: true}) || pages[0][3] != (PrefKey{Label: "|", Text: "|"}) {
		t.Errorf("keys came back as %+v", pages[0])
	}
	// The kind that was not sent keeps the panel's own keys: it is simply absent.
	if _, there := got.Keysets["shell"]; there {
		t.Errorf("shell has pages nobody saved: %s", raw)
	}
	// Saving a kind with no pages gives it back to the defaults.
	call(t, h.PutKeysets, http.MethodPut, `{"keysets":{"agent":[]}}`, "", "")
	if got, raw := prefsOf(t, h); len(got.Keysets) != 0 {
		t.Errorf("after the reset: %s", raw)
	}
}

func TestPrefs_KeysetsAreValidated(t *testing.T) {
	h := prefsHandler(t)
	call(t, h.PutKeysets, http.MethodPut, `{"keysets":{"shell":[[{"id":"tab"}]]}}`, "", "")
	page := func(keys ...string) string { return `{"keysets":{"agent":[[` + strings.Join(keys, ",") + `]]}}` }
	eight := make([]string, keysetPageKeys+1)
	for i := range eight {
		eight[i] = `{"id":"esc"}`
	}
	pages := make([]string, keysetPagesMax+1)
	for i := range pages {
		pages[i] = `[{"id":"esc"}]`
	}
	for name, tc := range map[string]struct{ body, reason string }{
		"not json":            {`[`, "not JSON"},
		"no keysets":          {`{}`, "no keysets"},
		"unknown kind":        {`{"keysets":{"vim":[[{"id":"esc"}]]}}`, "unknown kind"},
		"too many pages":      {`{"keysets":{"agent":[` + strings.Join(pages, ",") + `]}}`, "too many pages"},
		"an empty page":       {`{"keysets":{"agent":[[]]}}`, "no keys"},
		"eight keys":          {page(eight...), "too many keys"},
		"neither id nor text": {page(`{}`), "neither"},
		"both id and text":    {page(`{"id":"esc","label":"x","text":"x"}`), "both"},
		"an id of no key":     {page(`{"id":"../etc"}`), "not a key id"},
		"an id with enter":    {page(`{"id":"esc","enter":true}`), "both"},
		"no label":            {page(`{"text":"ls"}`), "no label"},
		"a long label":        {page(`{"label":"123456789","text":"x"}`), "label is too long"},
		"no text":             {page(`{"label":"x"}`), "no text"},
		"a long text":         {page(`{"label":"x","text":"` + strings.Repeat("y", keyTextMax+1) + `"}`), "text is too long"},
		"an escape in text":   {page(`{"label":"x","text":"a\u001bb"}`), "control"},
		"a newline in text":   {page(`{"label":"x","text":"a\nb"}`), "control"},
		"a newline in label":  {page(`{"label":"a\nb","text":"x"}`), "control"},
	} {
		refused(t, name, call(t, h.PutKeysets, http.MethodPut, tc.body, "", ""), tc.reason)
	}
	// A refused save changes nothing.
	if got, raw := prefsOf(t, h); len(got.Keysets["shell"]) != 1 || len(got.Keysets["agent"]) != 0 {
		t.Errorf("after the refused saves: %s", raw)
	}
	// At the limits it goes through.
	full := make([]string, keysetPageKeys)
	for i := range full {
		full[i] = `{"label":"12345678","text":"` + strings.Repeat("가", keyTextMax) + `"}`
	}
	six := make([]string, keysetPagesMax)
	for i := range six {
		six[i] = `[` + strings.Join(full, ",") + `]`
	}
	if rec := call(t, h.PutKeysets, http.MethodPut, `{"keysets":{"agent":[`+strings.Join(six, ",")+`]}}`, "", ""); rec.Code != http.StatusOK {
		t.Errorf("pages at the limits answered %d %s", rec.Code, rec.Body.String())
	}
}

// A row this module cannot read must not take the terminal page down with it:
// the page falls back to the panel's own keys and an empty list.
func TestPrefs_AnUnreadableRowIsEmpty(t *testing.T) {
	h := prefsHandler(t)
	for _, key := range []string{prefSnippets, prefKeysets} {
		if _, err := h.DB.Exec(`INSERT INTO terminal_prefs (key, value) VALUES (?, ?)`, key, `{"not":"what it should be`); err != nil {
			t.Fatal(err)
		}
	}
	got, raw := prefsOf(t, h)
	if len(got.Snippets) != 0 || len(got.Keysets) != 0 || !strings.Contains(raw, `"snippets":[]`) {
		t.Errorf("prefs answered %s", raw)
	}
	// Nor does a row that decodes into something no save would have accepted.
	for key, value := range map[string]string{prefSnippets: `[{"kind":"vim","body":"x"}]`, prefKeysets: `{"agent":[[{"label":"x","text":"a\u001bb"}]]}`} {
		if _, err := h.DB.Exec(`UPDATE terminal_prefs SET value = ? WHERE key = ?`, value, key); err != nil {
			t.Fatal(err)
		}
	}
	if got, raw := prefsOf(t, h); len(got.Snippets) != 0 || len(got.Keysets) != 0 {
		t.Errorf("rows no save would accept were answered as they are: %s", raw)
	}
	// And saving over it works.
	if rec := call(t, h.PutSnippets, http.MethodPut, `{"snippets":[{"kind":"agent","body":"x"}]}`, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("put answered %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := prefsOf(t, h); len(got.Snippets) != 1 {
		t.Errorf("after saving over it: %+v", got.Snippets)
	}
}
