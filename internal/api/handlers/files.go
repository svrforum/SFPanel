package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// maxReadSize is the maximum file size (5 MB) that ReadFile will return.
const maxReadSize = 5 * 1024 * 1024


// criticalPaths are system directories that must never be deleted.
var criticalPaths = map[string]bool{
	"/":     true,
	"/etc":  true,
	"/usr":  true,
	"/bin":  true,
	"/sbin": true,
	"/var":  true,
	"/boot": true,
	"/proc": true,
	"/sys":  true,
	"/dev":  true,
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

// FilesHandler exposes REST handlers for server-side file management.
type FilesHandler struct {
	DB *sql.DB
}

// ---------- helpers ----------

// validatePath ensures the path is absolute and contains no traversal sequences.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute")
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("path must not contain '..'")
	}
	return nil
}

// isCriticalPath returns true if the cleaned path matches a protected system directory.
func isCriticalPath(p string) bool {
	cleaned := filepath.Clean(p)
	return criticalPaths[cleaned]
}

// ---------- ListDir ----------

// ListDir returns the contents of a directory.
// GET /files?path=/some/path
func (h *FilesHandler) ListDir(c echo.Context) error {
	dirPath := c.QueryParam("path")
	if dirPath == "" {
		dirPath = "/"
	}

	if err := validatePath(dirPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	dirPath = filepath.Clean(dirPath)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, "NOT_FOUND", "Directory not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	files := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			// Skip entries whose metadata cannot be read.
			continue
		}
		fullPath := filepath.Join(dirPath, entry.Name())
		files = append(files, FileEntry{
			Name:    entry.Name(),
			Path:    fullPath,
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime(),
			IsDir:   entry.IsDir(),
		})
	}

	// Sort: directories first, then alphabetical by name.
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	return response.OK(c, files)
}

// ---------- ReadFile ----------

// ReadFile returns the text content of a file (up to 5 MB).
// GET /files/read?path=/etc/hostname
func (h *FilesHandler) ReadFile(c echo.Context) error {
	filePath := c.QueryParam("path")

	if err := validatePath(filePath); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	filePath = filepath.Clean(filePath)

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, "NOT_FOUND", "File not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	if info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, "IS_DIRECTORY", "Path is a directory, not a file")
	}

	if info.Size() > maxReadSize {
		return response.Fail(c, http.StatusBadRequest, "FILE_TOO_LARGE",
			fmt.Sprintf("File size %d bytes exceeds the maximum of %d bytes", info.Size(), maxReadSize))
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"content": string(content),
		"size":    info.Size(),
	})
}

// ---------- WriteFile ----------

// WriteFile writes (or overwrites) a file with the provided content.
// POST /files/write  JSON body: { path: string, content: string }
func (h *FilesHandler) WriteFile(c echo.Context) error {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validatePath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	req.Path = filepath.Clean(req.Path)

	// Create parent directories if they do not exist.
	dir := filepath.Dir(req.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	// Determine file mode: preserve existing permissions or default to 0644.
	fileMode := os.FileMode(0644)
	if info, err := os.Stat(req.Path); err == nil {
		fileMode = info.Mode().Perm()

		// Create .bak backup of existing file.
		backupPath := req.Path + ".bak"
		_ = os.Remove(backupPath)
		if err := os.Rename(req.Path, backupPath); err != nil {
			// Cross-device fallback: copy content.
			data, readErr := os.ReadFile(req.Path)
			if readErr == nil {
				_ = os.WriteFile(backupPath, data, 0644)
			}
		}
	}

	// Atomic write: write to temp file then rename.
	tmpPath := req.Path + ".sfpanel.tmp"
	if err := os.WriteFile(tmpPath, []byte(req.Content), fileMode); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}
	if err := os.Rename(tmpPath, req.Path); err != nil {
		os.Remove(tmpPath)
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	return response.OK(c, map[string]string{"message": "file written", "path": req.Path})
}

// ---------- MkDir ----------

// MkDir creates a directory (and any missing parents).
// POST /files/mkdir  JSON body: { path: string }
func (h *FilesHandler) MkDir(c echo.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validatePath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	req.Path = filepath.Clean(req.Path)

	if err := os.MkdirAll(req.Path, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	return response.OK(c, map[string]string{"message": "directory created", "path": req.Path})
}

// ---------- DeletePath ----------

// DeletePath removes a file or directory (recursively for directories).
// DELETE /files?path=/some/file
func (h *FilesHandler) DeletePath(c echo.Context) error {
	targetPath := c.QueryParam("path")

	if err := validatePath(targetPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	targetPath = filepath.Clean(targetPath)

	if isCriticalPath(targetPath) {
		return response.Fail(c, http.StatusForbidden, "CRITICAL_PATH",
			fmt.Sprintf("Deleting '%s' is not allowed: critical system path", targetPath))
	}

	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, "NOT_FOUND", "Path not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	if err := os.RemoveAll(targetPath); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	return response.OK(c, map[string]string{"message": "path deleted", "path": targetPath})
}

// ---------- RenamePath ----------

// RenamePath renames (moves) a file or directory.
// POST /files/rename  JSON body: { old_path: string, new_path: string }
func (h *FilesHandler) RenamePath(c echo.Context) error {
	var req struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validatePath(req.OldPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", fmt.Sprintf("old_path: %s", err.Error()))
	}
	if err := validatePath(req.NewPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", fmt.Sprintf("new_path: %s", err.Error()))
	}

	req.OldPath = filepath.Clean(req.OldPath)
	req.NewPath = filepath.Clean(req.NewPath)

	if _, err := os.Stat(req.OldPath); err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, "NOT_FOUND", "Source path not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	// Ensure the parent directory of the new path exists.
	newDir := filepath.Dir(req.NewPath)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	if err := os.Rename(req.OldPath, req.NewPath); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	return response.OK(c, map[string]string{
		"message":  "path renamed",
		"old_path": req.OldPath,
		"new_path": req.NewPath,
	})
}

// ---------- DownloadFile ----------

// DownloadFile serves a file as an attachment download.
// GET /files/download?path=/some/file
func (h *FilesHandler) DownloadFile(c echo.Context) error {
	filePath := c.QueryParam("path")

	if err := validatePath(filePath); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	filePath = filepath.Clean(filePath)

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, "NOT_FOUND", "File not found")
		}
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	if info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, "IS_DIRECTORY", "Cannot download a directory")
	}

	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(filePath)))

	return c.File(filePath)
}

// ---------- UploadFile ----------

// UploadFile receives a multipart file upload and saves it to the specified directory.
// POST /files/upload  multipart form: file (uploaded file), path (destination directory)
func (h *FilesHandler) UploadFile(c echo.Context) error {
	// Enforce upload size limit from settings (default 1024 MB).
	maxMB, _ := strconv.ParseInt(GetSetting(h.DB, "max_upload_size"), 10, 64)
	if maxMB <= 0 {
		maxMB = 1024
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxMB*1024*1024)

	destDir := c.FormValue("path")
	if err := validatePath(destDir); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	destDir = filepath.Clean(destDir)

	// Ensure the destination directory exists.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FILE", "No file provided in the 'file' field")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}
	defer src.Close()

	// Sanitise the filename: use only the base name to prevent directory traversal
	// embedded in the uploaded filename.
	filename := filepath.Base(fileHeader.Filename)
	if filename == "." || filename == "/" {
		return response.Fail(c, http.StatusBadRequest, "INVALID_FILENAME", "Invalid file name")
	}

	destPath := filepath.Join(destDir, filename)

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
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	written, err := io.Copy(dst, src)
	dst.Close()
	if err != nil {
		os.Remove(tmpPath)
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	// Rename temp file to final destination (atomic on same filesystem).
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, "PERMISSION_DENIED", "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, "FILE_ERROR", err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message":  "file uploaded",
		"path":     destPath,
		"filename": filename,
		"size":     written,
	})
}
