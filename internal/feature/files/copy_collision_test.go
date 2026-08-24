package files

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

func copyReq(t *testing.T, src, dst string, overwrite bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"src": src, "dst": dst, "overwrite": overwrite})
	req := httptest.NewRequest(http.MethodPost, "/files/copy", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if err := (&Handler{}).CopyPath(c); err != nil {
		t.Fatalf("CopyPath: %v", err)
	}
	return rec
}

// Rename and upload answer a collision with DESTINATION_EXISTS; copy answered
// INVALID_REQUEST. The frontend picks its message by code, so the collision an
// operator meets most often was the one it could not name.
func TestCopyCollisionUsesTheSameCodeAsRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("destination"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := copyReq(t, src, dst, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), response.ErrDestinationExists) {
		t.Errorf("body = %s, want code %s", rec.Body.String(), response.ErrDestinationExists)
	}
	// And the refusal has to have left the destination alone.
	if data, _ := os.ReadFile(dst); string(data) != "destination" {
		t.Errorf("destination was modified by a refused copy: %q", data)
	}
}

// Refusing with no way through left deleting the destination as the only
// route — a more dangerous operation than the one that was asked for.
func TestCopyOverwriteProceedsWhenAsked(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("destination"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := copyReq(t, src, dst, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if data, _ := os.ReadFile(dst); string(data) != "source" {
		t.Errorf("destination = %q, want the source contents", data)
	}
}
