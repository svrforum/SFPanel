package ai

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// A session's uploads: what the page's composer attaches — a screenshot, a
// log — lands on the node the session runs on, and its path goes into the
// prompt. They live at <home>/.sfpanel-ai/uploads/<session id>/ in the home of
// the account the session runs as: the program in the session has to be able
// to read them, and /tmp is not one place — a panel unit with PrivateTmp
// has its own.

const (
	uploadsLevel   = "uploads"
	uploadNameMax  = 80 // runes
	uploadTriesMax = 100
)

// uploadMaxBytes bounds one file. A var so a test can lower it.
var uploadMaxBytes int64 = 25 << 20

// uploadName is the name a client's file is stored under: its base name with
// everything but letters, digits, '.', '_' and '-' turned into '-'. It goes
// into a prompt and often into a shell command, so nothing in it may need
// quoting; and it is one path element, never ".." and never hidden.
func uploadName(original string) string {
	base := filepath.Base(strings.ReplaceAll(original, "\x00", "-"))
	var b strings.Builder
	dash := false
	for _, r := range base {
		keep := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'
		if !keep || r == '-' {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = true
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	name := strings.TrimLeft(b.String(), ".")
	if name == "" {
		return "file"
	}
	if utf8.RuneCountInString(name) <= uploadNameMax {
		return name
	}
	// Too long: the end goes, the extension stays — a program reads a file by it.
	ext := filepath.Ext(name)
	if utf8.RuneCountInString(ext) > 12 {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	return string(stem[:uploadNameMax-utf8.RuneCountInString(ext)]) + ext
}

// openSessionUploads opens <home>/.sfpanel-ai/uploads[/<id>] as an os.Root,
// level by level as the profile directories are opened (openProfileTool) and
// for the same reason: the home is the account's to rearrange, and a link at
// any level must not carry a root panel's write, chown or RemoveAll out of
// it. id == "" stops at the uploads directory.
func (h *Handler) openSessionUploads(acct Account, id string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(acct.Home) {
		return nil, fmt.Errorf("ai: account home %q is not absolute", acct.Home)
	}
	cur, err := os.OpenRoot(acct.Home)
	if err != nil {
		return nil, fmt.Errorf("ai: account home %q: %w", acct.Home, err)
	}
	levels := []string{profileRootName, uploadsLevel}
	if id != "" {
		levels = append(levels, id)
	}
	for _, level := range levels {
		next, err := h.descendProfileLevel(cur, acct, level, create)
		_ = cur.Close()
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// uploadAccount is the account a session's uploads belong to: the one it runs
// as. Unlike sessionAccount it refuses a session that is neither in the table
// nor live — that one falls back to the panel's account for a tmux command
// that then finds nothing, and here there is no such command to find nothing.
func (h *Handler) uploadAccount(c echo.Context, id string) (acct Account, failed bool, err error) {
	row, found, rerr := getSessionRow(h.DB, id)
	if rerr != nil {
		return Account{}, true, response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the session")
	}
	if found {
		a, ok := h.resolveAccount(row.RunAs)
		if !ok {
			return Account{}, true, response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "the session's account no longer exists")
		}
		return a, false, nil
	}
	a, live, lerr := h.rowlessAccount(id)
	if lerr != nil {
		return Account{}, true, response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list the sessions")
	}
	if !live {
		return Account{}, true, response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
	}
	return a, false, nil
}

// SessionUpload — POST /ai/sessions/:id/upload (multipart, field "file"): one
// file into the session's upload directory, under a name that is safe to put
// in a prompt. Answers with the absolute path, which is what the page inserts.
func (h *Handler) SessionUpload(c echo.Context) error {
	id := c.Param("id")
	if !validSessionID(id) {
		return response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
	}
	acct, failed, err := h.uploadAccount(c, id)
	if failed {
		return err
	}
	tooLarge := func() error {
		return response.Fail(c, http.StatusRequestEntityTooLarge, response.ErrFileTooLarge,
			fmt.Sprintf("the file is larger than %d MiB", uploadMaxBytes>>20))
	}
	// The form's own framing rides on top of the file.
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, uploadMaxBytes+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		var over *http.MaxBytesError
		if errors.As(err, &over) {
			return tooLarge()
		}
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "the request carries no file")
	}
	if fh.Size > uploadMaxBytes {
		return tooLarge()
	}
	src, err := fh.Open()
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "the file could not be read")
	}
	defer src.Close()

	dir, err := h.openSessionUploads(acct, id, true)
	if err != nil {
		if errors.Is(err, errProfilePath) || errors.Is(err, errProfileMissing) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "the account's upload directory is not a plain directory")
		}
		slog.Warn("ai upload directory", "component", "ai", "account", acct.Name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not open the upload directory")
	}
	defer dir.Close()

	// Never over a file that is there: two uploads in one second are two files.
	base := h.now().UTC().Format("20060102-150405") + "-" + uploadName(fh.Filename)
	name := base
	var dst *os.File
	for try := 2; ; try++ {
		dst, err = dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) || try > uploadTriesMax {
			slog.Warn("ai upload create", "component", "ai", "account", acct.Name, "err", err)
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not create the file")
		}
		ext := filepath.Ext(base)
		name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), try, ext)
	}
	size, cerr := io.Copy(dst, io.LimitReader(src, uploadMaxBytes+1))
	if err := dst.Close(); cerr == nil {
		cerr = err
	}
	if cerr == nil && h.isRoot() {
		cerr = h.lchownAt(dir, name, acct.UID, acct.GID)
	}
	if cerr != nil || size > uploadMaxBytes {
		_ = dir.Remove(name)
		if cerr == nil {
			return tooLarge()
		}
		slog.Warn("ai upload write", "component", "ai", "account", acct.Name, "err", cerr)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not write the file")
	}
	path := filepath.Join(acct.Home, profileRootName, uploadsLevel, id, name)
	slog.Info("ai session upload", "component", "ai", "id", id, "account", acct.Name, "name", name, "bytes", size)
	return response.OK(c, map[string]any{"path": path, "name": name, "size": size})
}

// removeSessionUploads deletes a session's upload directory with the session.
// Best effort: a session that never had one, or a home that is gone, is fine.
func (h *Handler) removeSessionUploads(acct Account, id string) {
	dir, err := h.openSessionUploads(acct, "", false)
	if err != nil {
		return
	}
	defer dir.Close()
	if err := dir.RemoveAll(id); err != nil {
		slog.Warn("ai upload cleanup", "component", "ai", "id", id, "account", acct.Name, "err", err)
	}
}
