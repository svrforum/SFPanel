package files

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// maxReadSize is the maximum file size (5 MB) that ReadFile will return.
const maxReadSize = 5 * 1024 * 1024

// maxWriteSize is the maximum body size (10 MB) for WriteFile.
const maxWriteSize = 10 * 1024 * 1024

// maxDownloadSize is the maximum file size (2 GB) for DownloadFile.
const maxDownloadSize = 2 * 1024 * 1024 * 1024


// criticalPaths are system directories that must never be deleted.
var criticalPaths = map[string]bool{
	"/":      true,
	"/etc":   true,
	"/usr":   true,
	"/bin":   true,
	"/sbin":  true,
	"/var":   true,
	"/boot":  true,
	"/proc":  true,
	"/sys":   true,
	"/dev":   true,
	"/home":  true,
	"/root":  true,
	"/lib":   true,
	"/lib64": true,
	"/opt":   true,
	"/run":   true,
	"/srv":   true,
}

// FileEntry represents a single file or directory in a listing.
type FileEntry struct {
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Size    int64       `json:"size"`
	Mode    string      `json:"mode"`
	ModTime time.Time   `json:"modTime"`
	IsDir   bool        `json:"isDir"`
}

// Handler exposes REST handlers for server-side file management.
type Handler struct {
	DB *sql.DB
}

// ---------- helpers ----------

// validatePath ensures the path is absolute and free of traversal / redundant
// segments. The previous implementation used strings.Contains(p, "..") which
// produced false positives on legitimate filenames like "app..log" while
// missing edge cases like "/etc/./shadow" and "//etc/passwd".
//
// The rule we actually want is: the cleaned form of the path equals the input
// (modulo trailing slash). filepath.Clean normalizes "..", ".", and "//" but
// leaves filenames containing literal ".." intact, because path-segment
// processing operates on /-separated tokens.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute")
	}
	cleaned := filepath.Clean(p)
	// Allow a trailing slash on the input — Clean strips it, but operators
	// commonly type "/etc/" when listing a directory.
	trimmed := strings.TrimRight(p, "/")
	if trimmed == "" {
		trimmed = "/"
	}
	if cleaned != trimmed {
		return fmt.Errorf("path contains traversal or redundant segments")
	}
	return nil
}

// validatePathForWrite checks symlink resolution for write/delete operations.
func validatePathForWrite(p string) error {
	if err := validatePath(p); err != nil {
		return err
	}
	parentDir := filepath.Dir(p)
	realDir, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Parent doesn't exist yet — MkdirAll will create it; validate the literal path
			realDir = filepath.Clean(parentDir)
		} else {
			return fmt.Errorf("cannot resolve parent directory: %w", err)
		}
	}
	resolved := filepath.Join(realDir, filepath.Base(p))
	if isCriticalPath(resolved) {
		return fmt.Errorf("access to critical system path is not allowed")
	}
	if isCriticalPath(realDir) {
		return fmt.Errorf("writing inside critical system directory is not allowed")
	}
	// Leaf-symlink check: if p itself is a symlink (e.g. /tmp/sneaky ->
	// /etc/cron.d), MkdirAll/os.Create would follow it into a critical path
	// even though parent + literal-resolved checks above pass. Resolve the
	// symlink chain and re-check the final target.
	if info, lerr := os.Lstat(p); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, terr := filepath.EvalSymlinks(p)
		if terr != nil {
			return fmt.Errorf("cannot resolve symlink target: %w", terr)
		}
		if isCriticalPath(target) {
			return fmt.Errorf("path resolves to a critical system path via symlink")
		}
	}
	return nil
}

// isCriticalPath returns true if the cleaned path is a protected system
// directory *or is located anywhere under one*. Prefix-matching is required
// because the previous exact-match behaviour left /etc/cron.d, /etc/sudoers.d,
// /usr/local/bin, etc. writable even though their parents (/etc, /usr) were
// in the protected set.
func isCriticalPath(p string) bool {
	cleaned := filepath.Clean(p)
	if criticalPaths[cleaned] {
		return true
	}
	for critical := range criticalPaths {
		if critical == "/" {
			continue
		}
		if strings.HasPrefix(cleaned, critical+"/") {
			return true
		}
	}
	return false
}

// readProtectedPaths are files that must not be readable via the file API.
// These are exact-match entries; prefix-based rules live in isReadProtectedPath.
var readProtectedPaths = map[string]bool{
	"/etc/shadow":              true,
	"/etc/gshadow":             true,
	"/etc/sudoers":             true,
	"/etc/sfpanel/config.yaml": true,
	// SFPanel SQLite live DB + WAL/SHM — exposing these to /files/read would
	// leak admin password hashes, JWT secret, refresh tokens, and audit logs.
	"/var/lib/sfpanel/sfpanel.db":     true,
	"/var/lib/sfpanel/sfpanel.db-wal": true,
	"/var/lib/sfpanel/sfpanel.db-shm": true,
}

// readProtectedPrefixes block every file underneath one of these roots.
// Use when the protection applies to a whole subtree (TLS certs, sudoers
// fragments, root's home directory).
var readProtectedPrefixes = []string{
	"/etc/sfpanel/cluster/", // TLS CA + node certs and keys
	"/etc/sudoers.d/",       // sudoers fragments — same impact as /etc/sudoers
	"/root/.ssh/",           // root's SSH keys + authorized_keys
}

// isReadProtectedPath returns true if the (symlink-resolved) path is one we
// refuse to serve via the file API. Resolution prevents the classic bypass
// where an attacker who can write a symlink in a permissive directory
// (e.g. /tmp) points it at a sensitive target.
//
// Resolution is best-effort: if EvalSymlinks fails (broken symlink, ENOENT
// at intermediate component) we fall back to the literal cleaned path, which
// still blocks attempts to read sensitive paths directly. That is the more
// permissive direction — a non-existent path can't leak data anyway, so a
// false negative on EvalSymlinks is acceptable; the false-positive direction
// (blocking a legitimate read) is what we avoid.
func isReadProtectedPath(p string) bool {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		resolved = filepath.Clean(p)
	}

	if readProtectedPaths[resolved] {
		return true
	}
	for _, prefix := range readProtectedPrefixes {
		if strings.HasPrefix(resolved, prefix) {
			return true
		}
	}
	// Generic /etc/ssh/*_key (private host keys). The .pub variants are
	// public and remain readable so operators can inspect/copy them.
	if strings.HasPrefix(resolved, "/etc/ssh/") &&
		strings.HasSuffix(resolved, "_key") {
		return true
	}
	// Generic /home/<user>/.ssh/<anything>. We protect the whole subtree
	// the same way as /root/.ssh — id_*, authorized_keys, known_hosts all
	// have sensitive content (or footprints) we don't expose.
	if strings.HasPrefix(resolved, "/home/") {
		// "/home/<user>/.ssh/..." — the ".ssh" segment must follow the
		// username and be inside that user's home root.
		rest := strings.TrimPrefix(resolved, "/home/")
		if idx := strings.Index(rest, "/"); idx > 0 {
			if strings.HasPrefix(rest[idx:], "/.ssh/") || rest[idx:] == "/.ssh" {
				return true
			}
		}
	}
	return false
}

// ---------- ListDir ----------

// ListDir returns the contents of a directory.
// GET /files?path=/some/path
func (h *Handler) ListDir(c echo.Context) error {
	dirPath := c.QueryParam("path")
	if dirPath == "" {
		dirPath = "/"
	}

	if err := validatePath(dirPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	dirPath = filepath.Clean(dirPath)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Directory not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	// Cap the number of entries we materialise. /var/lib/docker/overlay2,
	// /tmp on a busy system, or accidentally-shared cache directories can
	// contain millions of entries; allocating them all into one JSON
	// response stalls the panel UI and OOM-risks the panel process.
	// 10000 covers every legitimate operator workflow (the UI paginates
	// after that anyway) without DoSing on pathological inputs.
	const listDirCap = 10000
	filesList := make([]FileEntry, 0, len(entries))
	truncated := false
	for _, entry := range entries {
		if len(filesList) >= listDirCap {
			truncated = true
			break
		}
		info, err := entry.Info()
		if err != nil {
			// Skip entries whose metadata cannot be read.
			continue
		}
		fullPath := filepath.Join(dirPath, entry.Name())
		// Hide read-protected files (TLS certs, /etc/shadow, SFPanel config) from
		// directory listings so their existence, size, and mtime aren't leaked
		// to clients that can't read them anyway.
		if isReadProtectedPath(fullPath) {
			continue
		}
		filesList = append(filesList, FileEntry{
			Name:    entry.Name(),
			Path:    fullPath,
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime(),
			IsDir:   entry.IsDir(),
		})
	}

	// Sort: directories first, then alphabetical by name.
	sort.Slice(filesList, func(i, j int) bool {
		if filesList[i].IsDir != filesList[j].IsDir {
			return filesList[i].IsDir
		}
		return strings.ToLower(filesList[i].Name) < strings.ToLower(filesList[j].Name)
	})

	if truncated {
		slog.Warn("ListDir truncated — directory exceeds entry cap",
			"component", "files", "path", dirPath, "limit", listDirCap)
	}
	return response.OK(c, filesList)
}

// ---------- ReadFile ----------

// ReadFile returns the text content of a file (up to 5 MB).
// GET /files/read?path=/etc/hostname
func (h *Handler) ReadFile(c echo.Context) error {
	filePath := c.QueryParam("path")

	if err := validatePath(filePath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	filePath = filepath.Clean(filePath)

	if isReadProtectedPath(filePath) {
		return response.Fail(c, http.StatusForbidden, response.ErrReadProtected, "Access to this file is not allowed")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "File not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	if info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrIsDirectory, "Path is a directory, not a file")
	}

	if info.Size() > maxReadSize {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileTooLarge,
			fmt.Sprintf("File size %d bytes exceeds the maximum of %d bytes", info.Size(), maxReadSize))
	}

	// Open with O_NOFOLLOW to refuse a leaf symlink. isReadProtectedPath
	// above resolves symlinks at *check* time, but a local attacker can swap
	// the symlink target between the check and the open (TOCTOU). O_NOFOLLOW
	// closes that race by refusing to traverse a final-component symlink at
	// kernel level. Note: only the LAST component is protected — intermediate
	// symlinks (e.g. /var/log being a symlink to /data/log) are still followed,
	// which matches the read-path's intent.
	f, err := os.OpenFile(filePath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath,
				"refusing to follow symlink")
		}
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "File not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, maxReadSize))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"content": string(content),
		"size":    info.Size(),
	})
}

// ---------- WriteFile ----------

// WriteFile writes (or overwrites) a file with the provided content.
// POST /files/write  JSON body: { path: string, content: string }
func (h *Handler) WriteFile(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxWriteSize)

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validatePathForWrite(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	req.Path = filepath.Clean(req.Path)

	// Create parent directories if they do not exist.
	dir := filepath.Dir(req.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	// Determine file mode: preserve existing permissions or default to 0644.
	fileMode := os.FileMode(0644)
	if info, err := os.Stat(req.Path); err == nil {
		fileMode = info.Mode().Perm()

		// Copy-first backup: read original and write to .bak, keeping the
		// original in place. The previous rename-based approach moved the
		// original away first, so any subsequent write failure left the user
		// with only .bak as recovery. With copy-first, a write failure leaves
		// both the original and the .bak intact.
		backupPath := req.Path + ".bak"
		// Stream the copy rather than ReadFile-into-memory: the incoming body is
		// bounded, but the existing on-disk file is not, so a multi-GB original
		// would OOM the panel. io.Copy uses a small fixed buffer.
		if src, openErr := os.Open(req.Path); openErr == nil {
			// Best-effort: a backup failure must not prevent the write itself.
			_ = os.Remove(backupPath)
			if dst, createErr := os.OpenFile(backupPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode); createErr == nil {
				_, _ = io.Copy(dst, src)
				_ = dst.Close()
			}
			_ = src.Close()
		}
	}

	// Atomic write: write to temp file then rename into place. The original
	// is untouched until the rename succeeds, so a failure in WriteFile
	// (e.g. ENOSPC, EISDIR on the tmp path) preserves the original.
	tmpPath := req.Path + ".sfpanel.tmp"
	if err := os.WriteFile(tmpPath, []byte(req.Content), fileMode); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}
	if err := os.Rename(tmpPath, req.Path); err != nil {
		_ = os.Remove(tmpPath)
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]string{"message": "file written", "path": req.Path})
}

// ---------- MkDir ----------

// MkDir creates a directory (and any missing parents).
// POST /files/mkdir  JSON body: { path: string }
func (h *Handler) MkDir(c echo.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validatePathForWrite(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	req.Path = filepath.Clean(req.Path)

	if err := os.MkdirAll(req.Path, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]string{"message": "directory created", "path": req.Path})
}

// ---------- DeletePath ----------

// DeletePath removes a file or directory (recursively for directories).
// DELETE /files?path=/some/file
func (h *Handler) DeletePath(c echo.Context) error {
	targetPath := c.QueryParam("path")

	if err := validatePathForWrite(targetPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	targetPath = filepath.Clean(targetPath)

	if isCriticalPath(targetPath) {
		return response.Fail(c, http.StatusForbidden, response.ErrCriticalPath,
			fmt.Sprintf("Deleting '%s' is not allowed: critical system path", targetPath))
	}

	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Path not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	if err := os.RemoveAll(targetPath); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]string{"message": "path deleted", "path": targetPath})
}

// ---------- RenamePath ----------

// RenamePath renames (moves) a file or directory.
// POST /files/rename  JSON body: { old_path: string, new_path: string }
func (h *Handler) RenamePath(c echo.Context) error {
	var req struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validatePathForWrite(req.OldPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, fmt.Sprintf("old_path: %s", err.Error()))
	}
	if err := validatePathForWrite(req.NewPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, fmt.Sprintf("new_path: %s", err.Error()))
	}

	req.OldPath = filepath.Clean(req.OldPath)
	req.NewPath = filepath.Clean(req.NewPath)

	if isCriticalPath(req.OldPath) {
		return response.Fail(c, http.StatusForbidden, response.ErrCriticalPath,
			fmt.Sprintf("Renaming '%s' is not allowed: critical system path", req.OldPath))
	}
	if isCriticalPath(req.NewPath) {
		return response.Fail(c, http.StatusForbidden, response.ErrCriticalPath,
			fmt.Sprintf("Renaming to '%s' is not allowed: critical system path", req.NewPath))
	}

	if _, err := os.Stat(req.OldPath); err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Source path not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	// Ensure the parent directory of the new path exists.
	newDir := filepath.Dir(req.NewPath)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	if err := os.Rename(req.OldPath, req.NewPath); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]string{
		"message":  "path renamed",
		"old_path": req.OldPath,
		"new_path": req.NewPath,
	})
}

// ---------- CopyPath ----------

// copyRecursive copies src to dst. Directories are recreated and their regular
// file contents copied; non-regular files (symlinks, devices, sockets) are
// skipped rather than dereferenced, so a copy can't be tricked into reading
// through a symlink to a critical path.
func copyRecursive(src, dst string, info os.FileInfo) error {
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			ci, err := e.Info()
			if err != nil {
				return err
			}
			if err := copyRecursive(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), ci); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil // skip symlinks/devices/etc.
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// CopyPath copies a file or directory tree to a new location.
// POST /files/copy  JSON body: { src: string, dst: string }
func (h *Handler) CopyPath(c echo.Context) error {
	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if err := validatePath(req.Src); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, fmt.Sprintf("src: %s", err.Error()))
	}
	if err := validatePathForWrite(req.Dst); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, fmt.Sprintf("dst: %s", err.Error()))
	}
	req.Src = filepath.Clean(req.Src)
	req.Dst = filepath.Clean(req.Dst)

	if isCriticalPath(req.Dst) {
		return response.Fail(c, http.StatusForbidden, response.ErrCriticalPath,
			fmt.Sprintf("Copying to '%s' is not allowed: critical system path", req.Dst))
	}

	srcInfo, err := os.Stat(req.Src)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Source path not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}
	// Refuse to clobber: copy must not overwrite an existing destination.
	if _, err := os.Stat(req.Dst); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, "Destination already exists")
	}
	// Block copying a directory into its own subtree (would recurse forever).
	if srcInfo.IsDir() && strings.HasPrefix(req.Dst+string(filepath.Separator), req.Src+string(filepath.Separator)) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cannot copy a directory into itself")
	}

	if err := os.MkdirAll(filepath.Dir(req.Dst), 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}
	if err := copyRecursive(req.Src, req.Dst, srcInfo); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]string{"message": "path copied", "src": req.Src, "dst": req.Dst})
}

// ---------- SearchFiles ----------

const maxSearchResults = 1000
const maxSearchDuration = 10 * time.Second

// SearchFiles recursively searches under a root directory for entries whose
// name contains the query (case-insensitive). Bounded by a result cap and a
// wall-clock deadline so a search rooted at a huge tree can't hang the request
// or exhaust memory; the response flags whether it was truncated.
// GET /files/search?path=/root&q=needle&limit=200
func (h *Handler) SearchFiles(c echo.Context) error {
	root := c.QueryParam("path")
	query := strings.TrimSpace(c.QueryParam("q"))
	if err := validatePath(root); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	if query == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "search query is required")
	}
	limit := 200
	if l, err := strconv.Atoi(c.QueryParam("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	root = filepath.Clean(root)

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Path not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}
	if !info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Search path must be a directory")
	}

	needle := strings.ToLower(query)
	deadline := time.Now().Add(maxSearchDuration)
	results := make([]FileEntry, 0, limit)
	truncated := false

	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip dirs/files we can't read rather than aborting
		}
		if len(results) >= limit || time.Now().After(deadline) {
			truncated = true
			return filepath.SkipAll
		}
		if p == root {
			return nil
		}
		if strings.Contains(strings.ToLower(d.Name()), needle) {
			if fi, ierr := d.Info(); ierr == nil {
				results = append(results, FileEntry{
					Name:    d.Name(),
					Path:    p,
					Size:    fi.Size(),
					Mode:    fi.Mode().String(),
					ModTime: fi.ModTime(),
					IsDir:   d.IsDir(),
				})
			}
		}
		return nil
	})

	return response.OK(c, map[string]interface{}{
		"results":   results,
		"count":     len(results),
		"truncated": truncated,
	})
}

// ---------- DownloadFile ----------

// DownloadFile serves a file as an attachment download.
// GET /files/download?path=/some/file
func (h *Handler) DownloadFile(c echo.Context) error {
	filePath := c.QueryParam("path")

	if err := validatePath(filePath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	filePath = filepath.Clean(filePath)

	if isReadProtectedPath(filePath) {
		return response.Fail(c, http.StatusForbidden, response.ErrReadProtected, "Access to this file is not allowed")
	}

	// Refuse a leaf symlink before handing off to c.File / http.ServeContent.
	// Same TOCTOU rationale as ReadFile: an attacker who can swap the link
	// target between isReadProtectedPath and the file open would otherwise
	// leak a protected path's content via the download endpoint.
	if linfo, err := os.Lstat(filePath); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath,
			"refusing to follow symlink")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "File not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	if info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrIsDirectory, "Cannot download a directory")
	}
	if !info.Mode().IsRegular() {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileError, "Cannot download special files")
	}
	if info.Size() > maxDownloadSize {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileTooLarge,
			fmt.Sprintf("File size %d bytes exceeds the download limit", info.Size()))
	}

	encoded := url.PathEscape(filepath.Base(filePath))
	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename*=UTF-8''%s`, encoded))

	return c.File(filePath)
}

// ---------- UploadFile ----------

// UploadFile receives a multipart file upload and saves it to the specified directory.
// POST /files/upload  multipart form: file (uploaded file), path (destination directory)
func (h *Handler) UploadFile(c echo.Context) error {
	// Enforce upload size limit from settings (default 1024 MB). Read the
	// row directly so this module doesn't have to import the settings
	// feature package — keeping the feature/* layer free of sibling imports.
	var maxMBStr string
	if h.DB != nil {
		_ = h.DB.QueryRow("SELECT value FROM settings WHERE key = ?", "max_upload_size").Scan(&maxMBStr)
	}
	maxMB, _ := strconv.ParseInt(maxMBStr, 10, 64)
	if maxMB <= 0 {
		maxMB = 1024
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxMB*1024*1024)

	destDir := c.FormValue("path")
	if err := validatePathForWrite(destDir); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	destDir = filepath.Clean(destDir)

	// Ensure the destination directory exists.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFile, "No file provided in the 'file' field")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}
	defer src.Close()

	// Sanitise the filename: use only the base name to prevent directory traversal
	// embedded in the uploaded filename.
	filename := filepath.Base(fileHeader.Filename)
	if filename == "." || filename == "/" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFilename, "Invalid file name")
	}

	destPath := filepath.Join(destDir, filename)

	if isCriticalPath(destPath) {
		return response.Fail(c, http.StatusForbidden, response.ErrCriticalPath,
			fmt.Sprintf("Uploading to '%s' is not allowed: critical system path", destPath))
	}

	// Per-extension blocklist for known web-serving directories. The intent
	// is to keep an operator from accidentally dropping an executable script
	// into a path the host serves (Apache/Nginx will then run it). Outside
	// these prefixes there's no restriction — operators frequently upload
	// .sh files into /opt/* or /home/* for legitimate use.
	if isWebServedPath(destPath) && hasWebExecutableExtension(filename) {
		return response.Fail(c, http.StatusForbidden, response.ErrCriticalPath,
			fmt.Sprintf("Refusing to upload server-executable file (%s) into a web-served directory", filename))
	}

	// Atomic upload: write to temp file then rename into place.
	tmpPath := destPath + ".sfpanel.tmp"

	// In sticky-bit directories (like /tmp), fs.protected_regular=2 may prevent
	// overwriting files owned by other users. Remove existing temp file first.
	if info, err := os.Lstat(tmpPath); err == nil && !info.IsDir() {
		os.Remove(tmpPath)
	}

	dst, err := os.Create(tmpPath)
	if err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	written, err := io.Copy(dst, src)
	dst.Close()
	if err != nil {
		os.Remove(tmpPath)
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	// Rename temp file to final destination (atomic on same filesystem).
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message":  "file uploaded",
		"path":     destPath,
		"filename": filename,
		"size":     written,
	})
}
