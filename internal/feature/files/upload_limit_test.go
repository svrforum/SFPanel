package files

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/api/response"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := sfdb.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

// postUpload sends content as big.bin into dir through UploadFile, on a server
// whose max_upload_size is limitMB, and returns the status and error code.
func postUpload(t *testing.T, limitMB int, dir string, content []byte) (int, string) {
	t.Helper()
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('max_upload_size', ?)`, strconv.Itoa(limitMB)); err != nil {
		t.Fatalf("seed limit: %v", err)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("path", dir); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/files/upload", &body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if err := (&Handler{DB: db}).UploadFile(c); err != nil {
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

// uploadWithLimit uploads size bytes into a fresh directory under a limit of
// limitMB.
func uploadWithLimit(t *testing.T, limitMB int, size int) (int, string) {
	t.Helper()
	return postUpload(t, limitMB, t.TempDir(), bytes.Repeat([]byte("x"), size))
}

func TestUploadOverTheLimitIsRefusedAsTooLarge(t *testing.T) {
	// Assert the REASON: a refusal for size, not the INVALID_PATH a body cut short at the limit used to read as.
	status, code := uploadWithLimit(t, 1 /* MB */, 2<<20 /* bytes */)
	if status != http.StatusRequestEntityTooLarge || code != response.ErrFileTooLarge {
		t.Fatalf("upload of 2 MiB under a 1 MB limit returned %d/%s, want 413/%s", status, code, response.ErrFileTooLarge)
	}
}

func TestUploadUnderTheLimitStillLands(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("y"), 1<<10)
	if status, code := postUpload(t, 1, dir, content); status != http.StatusOK {
		t.Fatalf("upload of 1 KiB under a 1 MB limit returned %d/%s, want 200", status, code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatalf("read landed file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("landed file has %d bytes, want the %d sent", len(got), len(content))
	}
}
