package files

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func postCopy(t *testing.T, h *Handler, src, dst string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"src": src, "dst": dst})
	req := httptest.NewRequest(http.MethodPost, "/files/copy", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	if err := h.CopyPath(c); err != nil {
		t.Fatalf("CopyPath err: %v", err)
	}
	return rec
}

func getSearch(t *testing.T, h *Handler, path, q string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/files/search?path="+url.QueryEscape(path)+"&q="+url.QueryEscape(q), nil)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	if err := h.SearchFiles(c); err != nil {
		t.Fatalf("SearchFiles err: %v", err)
	}
	return rec
}

func TestCopyPath(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()

	// Build a small tree: dir/srcdir/{a.txt, sub/b.txt}
	srcDir := filepath.Join(dir, "srcdir")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Happy path: recursive directory copy.
	dst := filepath.Join(dir, "dstdir")
	if rec := postCopy(t, h, srcDir, dst); rec.Code != http.StatusOK {
		t.Fatalf("copy dir: status %d, body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "sub", "b.txt")); string(got) != "world" {
		t.Errorf("nested file not copied, got %q", got)
	}

	// Refuse to clobber an existing destination.
	if rec := postCopy(t, h, srcDir, dst); rec.Code != http.StatusConflict {
		t.Errorf("clobber: status %d, want 409", rec.Code)
	}

	// Refuse to copy a directory into its own subtree.
	if rec := postCopy(t, h, srcDir, filepath.Join(srcDir, "inner")); rec.Code != http.StatusBadRequest {
		t.Errorf("copy-into-self: status %d, want 400 — body=%s", rec.Code, rec.Body.String())
	}

	// Traversal in src is rejected by validatePath. Use a raw string (not
	// filepath.Join, which would pre-clean the "..") so the unclean path
	// actually reaches the validator.
	if rec := postCopy(t, h, dir+"/../etc", filepath.Join(dir, "x")); rec.Code != http.StatusBadRequest {
		t.Errorf("traversal src: status %d, want 400", rec.Code)
	}
}

func TestSearchFiles(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.log", "error.log", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, "logs", name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	rec := getSearch(t, h, dir, "log")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Results   []FileEntry `json:"results"`
			Count     int         `json:"count"`
			Truncated bool        `json:"truncated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v — body=%s", err, rec.Body.String())
	}
	// "log" matches app.log, error.log, and the "logs" dir — 3 entries.
	if resp.Data.Count != 3 {
		names := make([]string, 0)
		for _, r := range resp.Data.Results {
			names = append(names, r.Name)
		}
		t.Errorf("count = %d (%v), want 3", resp.Data.Count, names)
	}

	// Empty query is rejected.
	if rec := getSearch(t, h, dir, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty query: status %d, want 400", rec.Code)
	}
}
