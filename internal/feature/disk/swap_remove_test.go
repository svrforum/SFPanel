package disk

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// RemoveSwap switches swap off. It must not delete the path.
//
// An earlier attempt at this had it remove the file so an operator could clear
// a half-written swap file without a terminal. validateDiskPath accepts any
// ordinary-looking path, so that turned the route into an unconfirmed file
// delete for anything the regex allows — /etc/passwd included.
func TestRemoveSwapDoesNotDeleteTheFile(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "not-a-swapfile")
	if err := os.WriteFile(victim, []byte("important"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"path": victim})
	req := httptest.NewRequest(http.MethodDelete, "/swap", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	h := &Handler{Cmd: &exec.MockCommander{}}
	if err := h.RemoveSwap(c); err != nil {
		t.Fatalf("RemoveSwap: %v", err)
	}

	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("RemoveSwap deleted %s — this route switches swap off, it does not delete files", victim)
	}
	data, _ := os.ReadFile(victim)
	if string(data) != "important" {
		t.Errorf("file contents changed to %q", string(data))
	}
}

// A path that was never switched on has nothing to switch off, and answering
// 500 for that told the operator something had gone wrong when nothing had.
func TestRemoveSwapToleratesAnInactivePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never-activated")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest(http.MethodDelete, "/swap", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	h := &Handler{Cmd: &exec.MockCommander{
		Fallback: exec.MockResult{Output: "swapoff: Invalid argument", Err: errors.New("exit status 255")},
	}}
	if err := h.RemoveSwap(c); err != nil {
		t.Fatalf("RemoveSwap: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — swapoff failing on an inactive path is not an error", rec.Code)
	}
}
