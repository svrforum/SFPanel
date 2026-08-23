package files

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

func postJSON(t *testing.T, fn func(echo.Context) error, path string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	if err := fn(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("handler returned err: %v", err)
	}
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded.Error.Code
}

// Two tabs open on the same file used to end with the second save silently
// discarding the first, and the operator who lost the edit never finding out.
func TestWriteRefusesAStaleSave(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	target := filepath.Join(dir, "conf.yml")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	readAt := info.ModTime()

	// Somebody else writes. Push the mtime forward explicitly so the test does
	// not depend on filesystem timestamp resolution.
	if err := os.WriteFile(target, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := readAt.Add(2 * time.Second)
	if err := os.Chtimes(target, later, later); err != nil {
		t.Fatal(err)
	}

	status, code := postJSON(t, h.WriteFile, "/files/write", map[string]any{
		"path": target, "content": "mine\n", "expect_mod_time": readAt.UTC(),
	})
	if status != http.StatusConflict || code != response.ErrStaleWrite {
		t.Fatalf("stale save returned %d/%s, want 409/%s", status, code, response.ErrStaleWrite)
	}
	if got, _ := os.ReadFile(target); string(got) != "theirs\n" {
		t.Errorf("file content = %q — the stale save was applied anyway", got)
	}
}

// The same save with a current timestamp must go through, or the guard would
// make the editor unusable.
func TestWriteAcceptsACurrentSave(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	target := filepath.Join(dir, "conf.yml")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(target)

	status, code := postJSON(t, h.WriteFile, "/files/write", map[string]any{
		"path": target, "content": "mine\n", "expect_mod_time": info.ModTime().UTC(),
	})
	if status != http.StatusOK {
		t.Fatalf("current save returned %d/%s, want 200", status, code)
	}
	if got, _ := os.ReadFile(target); string(got) != "mine\n" {
		t.Errorf("content = %q, want the new text", got)
	}
}

// "New file" shared the write route with Save, so typing the name of an
// existing file emptied it — the dialog said create, the effect was erase.
func TestCreateOnlyRefusesAnExistingFile(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	target := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(target, []byte("valuable\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, code := postJSON(t, h.WriteFile, "/files/write", map[string]any{
		"path": target, "content": "", "create_only": true,
	})
	if status != http.StatusConflict || code != response.ErrDestinationExists {
		t.Fatalf("create over an existing file returned %d/%s, want 409/%s", status, code, response.ErrDestinationExists)
	}
	if got, _ := os.ReadFile(target); string(got) != "valuable\n" {
		t.Errorf("content = %q — the existing file was truncated", got)
	}
}

// Rename clobbered silently while Copy, on the same screen, refused the same
// collision with a 409.
func TestRenameRefusesAnExistingDestination(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src, []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, code := postJSON(t, h.RenamePath, "/files/rename", map[string]any{
		"old_path": src, "new_path": dst,
	})
	if status != http.StatusConflict || code != response.ErrDestinationExists {
		t.Fatalf("rename onto an existing file returned %d/%s, want 409/%s", status, code, response.ErrDestinationExists)
	}
	if got, _ := os.ReadFile(dst); string(got) != "victim\n" {
		t.Errorf("destination = %q — it was overwritten", got)
	}

	// With the flag, it proceeds: the operator was told and chose.
	if status, code := postJSON(t, h.RenamePath, "/files/rename", map[string]any{
		"old_path": src, "new_path": dst, "overwrite": true,
	}); status != http.StatusOK {
		t.Fatalf("rename with overwrite returned %d/%s, want 200", status, code)
	}
	if got, _ := os.ReadFile(dst); string(got) != "source\n" {
		t.Errorf("destination = %q, want the source content", got)
	}
}

func uploadWithOverwrite(t *testing.T, dir, name, content, overwrite string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("path", dir)
	if overwrite != "" {
		_ = w.WriteField("overwrite", overwrite)
	}
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/files/upload", &body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	rec := httptest.NewRecorder()
	if err := (&Handler{}).UploadFile(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("UploadFile returned err: %v", err)
	}
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded.Error.Code
}

// Upload was the one write path with no safety net: no prompt, no 409, no .bak.
func TestUploadRefusesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(target, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, code := uploadWithOverwrite(t, dir, "compose.yml", "uploaded\n", "")
	if status != http.StatusConflict || code != response.ErrDestinationExists {
		t.Fatalf("upload over an existing file returned %d/%s, want 409/%s", status, code, response.ErrDestinationExists)
	}
	if got, _ := os.ReadFile(target); string(got) != "existing\n" {
		t.Errorf("file = %q — it was overwritten without asking", got)
	}

	if status, code := uploadWithOverwrite(t, dir, "compose.yml", "uploaded\n", "true"); status != http.StatusOK {
		t.Fatalf("upload with overwrite returned %d/%s, want 200", status, code)
	}
	if got, _ := os.ReadFile(target); string(got) != "uploaded\n" {
		t.Errorf("file = %q, want the uploaded content", got)
	}
}
