package files

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// trashRoot is where deleted entries wait before they are gone for good.
//
// Under /var/lib rather than a dot-directory beside the original: a trash that
// lives next to the file changes the directory the operator was cleaning up,
// shows up in their next listing, and rides along into any backup or archive
// they make of that tree.
const trashRoot = "/var/lib/sfpanel/trash"

// trashRootOverride redirects the trash during tests. Empty in production; the
// alternative is a test suite that moves real files into the real trash.
var trashRootOverride string

// trashDir is the directory the trash actually uses.
func trashDir() string {
	if trashRootOverride != "" {
		return trashRootOverride
	}
	return trashRoot
}

// trashRetention is how long an entry survives. Long enough to notice the
// mistake the next morning, short enough that a forgotten delete does not hold
// a filesystem's worth of bytes indefinitely.
const trashRetention = 7 * 24 * time.Hour

// TrashEntry describes one deleted item.
type TrashEntry struct {
	ID string `json:"id"`
	// OriginalPath is where it came from, which is the only thing that makes a
	// restore meaningful.
	OriginalPath string    `json:"originalPath"`
	Name         string    `json:"name"`
	DeletedAt    time.Time `json:"deletedAt"`
	Size         int64     `json:"size"`
	IsDir        bool      `json:"isDir"`
}

// trashMeta is the sidecar written next to each trashed item.
type trashMeta struct {
	OriginalPath string    `json:"originalPath"`
	DeletedAt    time.Time `json:"deletedAt"`
	IsDir        bool      `json:"isDir"`
}

// moveToTrash relocates a path into the trash and records where it came from.
//
// Returns false when the move is not possible — most often because the trash
// is on a different filesystem from the file, which os.Rename cannot cross.
// The caller then deletes outright rather than failing: refusing to delete
// something because it cannot be *undeleted* would be a worse trade than the
// missing safety net.
func moveToTrash(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(trashDir(), 0o700); err != nil {
		return false, err
	}

	// A timestamp plus the base name: sortable, and readable enough that the
	// directory can be understood without the panel.
	id := fmt.Sprintf("%d-%s", time.Now().UnixNano(), sanitizeTrashName(filepath.Base(path)))
	dest := filepath.Join(trashDir(), id)

	if err := os.Rename(path, dest); err != nil {
		// EXDEV and friends: the trash and the file are on different mounts.
		return false, nil
	}

	meta := trashMeta{OriginalPath: path, DeletedAt: time.Now().UTC(), IsDir: info.IsDir()}
	raw, err := json.Marshal(meta)
	if err != nil {
		return true, nil // the item is safe; only the label is missing
	}
	// Best effort: an entry with no sidecar still shows up in the listing and
	// can be restored by hand, which beats failing the delete.
	_ = os.WriteFile(dest+".meta.json", raw, 0o600)
	return true, nil
}

// sanitizeTrashName keeps the id filesystem-safe without losing readability.
func sanitizeTrashName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == 0:
			return '_'
		default:
			return r
		}
	}, name)
	if len(cleaned) > 100 {
		cleaned = cleaned[:100]
	}
	if cleaned == "" {
		return "entry"
	}
	return cleaned
}

// ListTrash returns what is recoverable, newest first.
func (h *Handler) ListTrash(c echo.Context) error {
	entries, err := os.ReadDir(trashDir())
	if err != nil {
		if os.IsNotExist(err) {
			return response.OK(c, map[string]interface{}{"entries": []TrashEntry{}})
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}

	out := []TrashEntry{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		item := TrashEntry{ID: e.Name(), Name: e.Name()}
		if info, ierr := e.Info(); ierr == nil {
			item.Size = info.Size()
			item.IsDir = info.IsDir()
			item.DeletedAt = info.ModTime()
		}
		if raw, rerr := os.ReadFile(filepath.Join(trashDir(), e.Name()+".meta.json")); rerr == nil {
			var meta trashMeta
			if json.Unmarshal(raw, &meta) == nil {
				item.OriginalPath = meta.OriginalPath
				item.Name = filepath.Base(meta.OriginalPath)
				item.DeletedAt = meta.DeletedAt
				item.IsDir = meta.IsDir
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return response.OK(c, map[string]interface{}{"entries": out, "retentionDays": int(trashRetention.Hours() / 24)})
}

// RestoreFromTrash puts an entry back where it came from.
func (h *Handler) RestoreFromTrash(c echo.Context) error {
	var req struct {
		ID string `json:"id"`
		// To overrides the recorded origin, for the case where the original
		// directory is gone or the operator wants it somewhere else.
		To string `json:"to,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	// The id names a file inside the trash directory and must not be able to
	// name anything else — a caller passing ../../etc would otherwise move a
	// system file back over whatever they chose.
	if req.ID == "" || strings.ContainsAny(req.ID, "/\\") || req.ID == "." || req.ID == ".." {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid trash id")
	}
	source := filepath.Join(trashDir(), req.ID)
	if _, err := os.Lstat(source); err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Not in the trash")
	}

	dest := req.To
	if dest == "" {
		raw, rerr := os.ReadFile(source + ".meta.json")
		if rerr != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
				"This entry has no record of where it came from; choose a destination")
		}
		var meta trashMeta
		if json.Unmarshal(raw, &meta) != nil || meta.OriginalPath == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
				"This entry has no record of where it came from; choose a destination")
		}
		dest = meta.OriginalPath
	}
	if err := validatePathForWrite(dest); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	dest = filepath.Clean(dest)

	// Never restore over something. The original name being free is the whole
	// reason a restore is safe, and clobbering a newer file to recover an older
	// one is the opposite of what was asked for.
	if _, err := os.Lstat(dest); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrDestinationExists,
			fmt.Sprintf("'%s' already exists", filepath.Base(dest)))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	if err := os.Rename(source, dest); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	_ = os.Remove(source + ".meta.json")
	return response.OK(c, map[string]interface{}{"restored": dest})
}

// PurgeTrash empties the trash, or removes one entry from it.
func (h *Handler) PurgeTrash(c echo.Context) error {
	id := c.QueryParam("id")
	if id == "" {
		if err := os.RemoveAll(trashDir()); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
		}
		return response.OK(c, map[string]interface{}{"purged": "all"})
	}
	if strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid trash id")
	}
	target := filepath.Join(trashDir(), id)
	if err := os.RemoveAll(target); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	_ = os.Remove(target + ".meta.json")
	return response.OK(c, map[string]interface{}{"purged": id})
}

// SweepTrash removes entries past the retention window.
//
// Called on a timer from the panel's background context. Without it the trash
// is not a safety net but a slow leak: every delete the operator ever made,
// held forever on the filesystem they were trying to clear.
func SweepTrash() (int, error) {
	entries, err := os.ReadDir(trashDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-trashRetention)
	var removed int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		deletedAt := time.Time{}
		if raw, rerr := os.ReadFile(filepath.Join(trashDir(), e.Name()+".meta.json")); rerr == nil {
			var meta trashMeta
			if json.Unmarshal(raw, &meta) == nil {
				deletedAt = meta.DeletedAt
			}
		}
		if deletedAt.IsZero() {
			// No sidecar: fall back to the entry's own mtime, which is when
			// the rename into the trash happened.
			if info, ierr := e.Info(); ierr == nil {
				deletedAt = info.ModTime()
			}
		}
		if deletedAt.IsZero() || deletedAt.After(cutoff) {
			continue
		}
		target := filepath.Join(trashDir(), e.Name())
		if err := os.RemoveAll(target); err == nil {
			_ = os.Remove(target + ".meta.json")
			removed++
		}
	}
	return removed, nil
}
