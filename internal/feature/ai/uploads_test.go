package ai

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// upload posts one file to the session's upload route, as the page does.
func upload(t *testing.T, h *Handler, id, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if filename != "" {
		part, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/ai/sessions/"+id+"/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	if err := h.SessionUpload(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

type uploaded struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func uploadedOf(t *testing.T, rec *httptest.ResponseRecorder) uploaded {
	t.Helper()
	var env struct {
		Success bool     `json:"success"`
		Data    uploaded `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || !env.Success {
		t.Fatalf("upload answered %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	return env.Data
}

// A handler whose panel account has a real home, and one session of its own.
func uploadHandler(t *testing.T) (*Handler, string, string) {
	t.Helper()
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	home := t.TempDir()
	h.panel.Home = home
	h.now = func() time.Time { return time.Date(2026, 11, 14, 9, 30, 5, 0, time.UTC) }
	id := "aaaaaaaaaaaa"
	if err := insertSession(h.DB, sessionRow{ID: id, Tool: ToolClaude, Title: "t", RunAs: "root", CWD: "/"}); err != nil {
		t.Fatal(err)
	}
	return h, home, id
}

func TestUploadName(t *testing.T) {
	for original, want := range map[string]string{
		"notes.txt":                  "notes.txt",
		"Screen Shot (1).png":        "Screen-Shot-1-.png",
		"스크린샷 2026-11-14.png":        "스크린샷-2026-11-14.png",
		"../../etc/passwd":           "passwd",
		"/root/.ssh/authorized_keys": "authorized_keys",
		".bashrc":                    "bashrc",
		"..":                         "file",
		"":                           "file",
		"a\nb\x00c.txt":              "a-b-c.txt",
		"`rm -rf ~`; $(x).sh":        "rm-rf-x-.sh",
		"C:\\Users\\me\\shot.png":    "C-Users-me-shot.png",
	} {
		if got := uploadName(original); got != want {
			t.Errorf("uploadName(%q) = %q, want %q", original, got, want)
		}
	}
	// A long name keeps its extension: the program reading it goes by that.
	long := uploadName(strings.Repeat("가", 200) + ".jpeg")
	if n := len([]rune(long)); n > 80 || !strings.HasSuffix(long, ".jpeg") {
		t.Errorf("a long name came out %d runes, ending %q", n, long[len(long)-8:])
	}
}

func TestSessionUpload_WritesIntoTheSessionsOwnDirectory(t *testing.T) {
	h, home, id := uploadHandler(t)
	got := uploadedOf(t, upload(t, h, id, "Screen Shot.png", []byte("PNGDATA")))
	dir := filepath.Join(home, profileRootName, "uploads", id)
	if want := filepath.Join(dir, "20261114-093005-Screen-Shot.png"); got.Path != want || got.Name != "20261114-093005-Screen-Shot.png" || got.Size != 7 {
		t.Fatalf("answered %+v, want the path %s and 7 bytes", got, want)
	}
	if b, err := os.ReadFile(got.Path); err != nil || string(b) != "PNGDATA" {
		t.Fatalf("the file holds %q (%v)", b, err)
	}
	// The account's alone: what a prompt points at may be private.
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Dir(dir): 0o700, got.Path: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s has mode %v (%v), want %v", path, info.Mode().Perm(), err, want)
		}
	}
}

func TestSessionUpload_NeverOverwrites(t *testing.T) {
	h, _, id := uploadHandler(t)
	first := uploadedOf(t, upload(t, h, id, "a.txt", []byte("one")))
	second := uploadedOf(t, upload(t, h, id, "a.txt", []byte("two")))
	if first.Path == second.Path {
		t.Fatalf("two uploads in one second share the path %s", first.Path)
	}
	if b, _ := os.ReadFile(first.Path); string(b) != "one" {
		t.Errorf("the first upload now holds %q", b)
	}
	if b, _ := os.ReadFile(second.Path); string(b) != "two" {
		t.Errorf("the second upload holds %q", b)
	}
}

// The directories are inside a home the account can rearrange: a link planted
// where one of them goes must not carry a root panel's write out of the home.
func TestSessionUpload_RefusesALinkPlantedOnTheWay(t *testing.T) {
	for _, level := range []string{"root", "uploads", "session"} {
		t.Run(level, func(t *testing.T) {
			h, home, id := uploadHandler(t)
			outside := t.TempDir()
			link := filepath.Join(home, profileRootName)
			switch level {
			case "uploads":
				_ = os.MkdirAll(link, 0o700)
				link = filepath.Join(link, "uploads")
			case "session":
				_ = os.MkdirAll(filepath.Join(link, "uploads"), 0o700)
				link = filepath.Join(link, "uploads", id)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			rec := upload(t, h, id, "a.txt", []byte("x"))
			if code, msg := failCode(t, rec); rec.Code != http.StatusBadRequest || code != response.ErrInvalidPath {
				t.Errorf("answered %d %s %q, want 400 %s", rec.Code, code, msg, response.ErrInvalidPath)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("the upload followed the link: %d entries outside the home", len(entries))
			}
		})
	}
}

func TestSessionUpload_AFileOverTheLimitIsRefusedWhole(t *testing.T) {
	h, home, id := uploadHandler(t)
	defer func(n int64) { uploadMaxBytes = n }(uploadMaxBytes)
	uploadMaxBytes = 1024
	rec := upload(t, h, id, "big.bin", bytes.Repeat([]byte("x"), 4096))
	if code, msg := failCode(t, rec); rec.Code != http.StatusRequestEntityTooLarge || code != response.ErrFileTooLarge {
		t.Errorf("answered %d %s %q, want 413 %s", rec.Code, code, msg, response.ErrFileTooLarge)
	}
	// Nothing of it stays behind for a prompt to point at.
	if entries, _ := os.ReadDir(filepath.Join(home, profileRootName, "uploads", id)); len(entries) != 0 {
		t.Errorf("%d entries left in the session's directory", len(entries))
	}
	// At the limit it goes through.
	if rec := upload(t, h, id, "fits.bin", bytes.Repeat([]byte("x"), 1024)); rec.Code != http.StatusOK {
		t.Errorf("a file of exactly the limit answered %d %s", rec.Code, rec.Body.String())
	}
}

func TestSessionUpload_NeedsASessionAndAFile(t *testing.T) {
	h, home, id := uploadHandler(t)
	for _, bad := range []string{"bbbbbbbbbbbb", "../../../etc", "nope"} {
		rec := upload(t, h, bad, "a.txt", []byte("x"))
		if code, msg := failCode(t, rec); rec.Code != http.StatusNotFound || code != response.ErrAISessionNotFound {
			t.Errorf("session %q: answered %d %s %q, want 404 %s", bad, rec.Code, code, msg, response.ErrAISessionNotFound)
		}
	}
	rec := upload(t, h, id, "", nil)
	if code, msg := failCode(t, rec); rec.Code != http.StatusBadRequest || code != response.ErrInvalidRequest {
		t.Errorf("no file: answered %d %s %q, want 400 %s", rec.Code, code, msg, response.ErrInvalidRequest)
	}
	// A session that is not there makes no directories in anyone's home.
	if _, err := os.Stat(filepath.Join(home, profileRootName)); !os.IsNotExist(err) {
		t.Errorf("a refused upload left %s behind (%v)", profileRootName, err)
	}
}

func TestDeleteSession_TakesItsUploadsWithIt(t *testing.T) {
	h, home, id := uploadHandler(t)
	other := "cccccccccccc"
	_ = insertSession(h.DB, sessionRow{ID: other, Tool: ToolShell, Title: "o", RunAs: "root", CWD: "/"})
	uploadedOf(t, upload(t, h, id, "a.txt", []byte("x")))
	kept := uploadedOf(t, upload(t, h, other, "b.txt", []byte("y")))
	if rec := call(t, h.DeleteSession, http.MethodDelete, "", id, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete answered %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, profileRootName, "uploads", id)); !os.IsNotExist(err) {
		t.Errorf("the deleted session's uploads are still there (%v)", err)
	}
	if _, err := os.Stat(kept.Path); err != nil {
		t.Errorf("another session's upload went with it: %v", err)
	}
}

// A session with no row — a database restored from before it — is still a
// session: its uploads go to the home of the account whose socket it is on,
// and leave with it.
func TestSessionUpload_ARowlessSessionUploadsAsTheAccountItRunsAs(t *testing.T) {
	h := newTestHandler(t, nil)
	h.DB = openTestDB(t)
	home := t.TempDir()
	if err := os.WriteFile(h.passwdPath, []byte(strings.ReplaceAll(passwdFixture, "/home/alice", home)), 0o600); err != nil {
		t.Fatal(err)
	}
	alice, ok := h.resolveAccount("alice")
	if !ok || alice.Home != home {
		t.Fatalf("alice resolved to %+v", alice)
	}
	if err := h.ensureSocketDir(alice); err != nil {
		t.Fatal(err)
	}
	const id = "969018a4f943"
	h.Cmd = socketsCommander(map[string]string{h.socketPath(alice): id + "\t1\tbash\t0\t1789348400\t0"}, "")
	h.now = func() time.Time { return time.Unix(1789348400, 0) }

	got := uploadedOf(t, upload(t, h, id, "a.txt", []byte("x")))
	dir := filepath.Join(home, profileRootName, "uploads", id)
	if filepath.Dir(got.Path) != dir {
		t.Fatalf("stored at %s, want it under alice's %s", got.Path, dir)
	}
	if rec := call(t, h.DeleteSession, http.MethodDelete, "", id, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete answered %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the deleted session's uploads are still there (%v)", err)
	}
}

// The account owns the directory the file is written into. Between the write
// and a chown of the file by its name it can move the file away and put a
// hard link to another inode under that name — a file it may write to but
// does not own — and a root panel would hand it that one. So the file is
// handed over through the descriptor its bytes went into.
func TestSessionUpload_OwnershipGoesToTheFileWrittenNotToItsName(t *testing.T) {
	h, home, id := uploadHandler(t)
	dir := filepath.Join(home, profileRootName, "uploads", id)
	victim := filepath.Join(home, "victim")
	if err := os.WriteFile(victim, []byte("not the account's"), 0o600); err != nil {
		t.Fatal(err)
	}
	// What the account does the moment before ownership is given.
	swap := func(name string) {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, "moved-away")); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(victim, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	var given os.FileInfo
	h.fchown = func(f *os.File, uid, gid int) error {
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			return err // a level of the path, handed over the same way
		}
		swap(filepath.Base(f.Name()))
		given = info
		return nil
	}
	if rec := upload(t, h, id, "a.txt", []byte("the upload")); rec.Code != http.StatusOK {
		t.Fatalf("upload answered %d %s", rec.Code, rec.Body.String())
	}
	if given == nil {
		t.Fatal("under a root panel the file was never handed to the account")
	}
	victimInfo, _ := os.Stat(victim)
	written, _ := os.Stat(filepath.Join(dir, "moved-away"))
	if os.SameFile(given, victimInfo) {
		t.Error("ownership went to whatever the name pointed at: the file the account linked in its place")
	}
	if !os.SameFile(given, written) {
		t.Error("ownership did not go to the file the upload was written into")
	}
}
