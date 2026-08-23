package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// Archive limits.
//
// Extraction is the dangerous direction, so both a total size and an entry
// count are capped: a "zip bomb" is a few kilobytes on disk that expands to
// gigabytes, and without a ceiling the first one to arrive fills the
// filesystem the panel itself runs on.
const (
	maxArchiveEntries = 20000
	maxExtractedBytes = 4 * 1024 * 1024 * 1024
)

// CreateArchive packs paths into a .tar.gz or .zip.
//
// POST /files/archive  { paths: []string, dest: string, format: "tar.gz"|"zip" }
func (h *Handler) CreateArchive(c echo.Context) error {
	var req struct {
		Paths  []string `json:"paths"`
		Dest   string   `json:"dest"`
		Format string   `json:"format"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if len(req.Paths) == 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Nothing to archive")
	}
	if err := validatePathForWrite(req.Dest); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	req.Dest = filepath.Clean(req.Dest)
	for i, p := range req.Paths {
		if err := validatePath(p); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
		}
		req.Paths[i] = filepath.Clean(p)
	}
	if _, err := os.Stat(req.Dest); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrDestinationExists,
			fmt.Sprintf("'%s' already exists", filepath.Base(req.Dest)))
	}

	// Write to a temporary name and rename on success. A half-written archive
	// left under the final name looks like a finished backup, which is the
	// worst possible way for this to fail.
	tmp := req.Dest + ".sfpanel.tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	cleanup := func() {
		out.Close()
		os.Remove(tmp)
	}

	switch strings.ToLower(req.Format) {
	case "zip":
		err = writeZip(out, req.Paths)
	default:
		err = writeTarGz(out, req.Paths)
	}
	if err != nil {
		cleanup()
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	if err := os.Rename(tmp, req.Dest); err != nil {
		os.Remove(tmp)
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{"dest": req.Dest})
}

func writeTarGz(w io.Writer, paths []string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, root := range paths {
		base := filepath.Dir(root)
		err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			// Symlinks are stored as links rather than followed. Following
			// them would silently inline the target — and a link to / would
			// try to pack the whole filesystem.
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				if link, err = os.Readlink(p); err != nil {
					return err
				}
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func writeZip(w io.Writer, paths []string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, root := range paths {
		base := filepath.Dir(root)
		err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			// zip has no portable symlink representation that consumers agree
			// on, so links are skipped rather than silently dereferenced.
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(rel)
			if info.IsDir() {
				header.Name += "/"
			} else {
				header.Method = zip.Deflate
			}
			writer, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(writer, f)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ExtractArchive unpacks an archive into a directory.
//
// POST /files/extract  { path: string, dest: string }
func (h *Handler) ExtractArchive(c echo.Context) error {
	var req struct {
		Path string `json:"path"`
		Dest string `json:"dest"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if err := validatePath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	if err := validatePathForWrite(req.Dest); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	req.Path = filepath.Clean(req.Path)
	req.Dest = filepath.Clean(req.Dest)

	if err := os.MkdirAll(req.Dest, 0o755); err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}

	var count int
	var err error
	lower := strings.ToLower(req.Path)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		count, err = extractZip(req.Path, req.Dest)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar"):
		count, err = extractTar(req.Path, req.Dest)
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			"Only .tar, .tar.gz, .tgz and .zip can be extracted")
	}
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{"dest": req.Dest, "entries": count})
}

// safeJoin resolves an archive entry name against the destination and refuses
// anything that would land outside it.
//
// This is the Zip Slip defence, and it is the whole reason extraction needs
// care: an entry named ../../etc/cron.d/x writes outside the directory the
// operator chose, and an absolute name does the same. Checked with a separator
// appended so /tmp/dest-evil cannot pass as a child of /tmp/dest.
func safeJoin(dest, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive contains an entry with no name")
	}
	cleaned := filepath.Clean(filepath.Join(dest, name))
	if cleaned != dest && !strings.HasPrefix(cleaned, dest+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q would be written outside the destination", name)
	}
	return cleaned, nil
}

func extractTar(archivePath, dest string) (int, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var reader io.Reader = f
	if strings.HasSuffix(strings.ToLower(archivePath), ".gz") || strings.HasSuffix(strings.ToLower(archivePath), ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, err
		}
		defer gz.Close()
		reader = gz
	}

	tr := tar.NewReader(reader)
	var count int
	var written int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		count++
		if count > maxArchiveEntries {
			return count, fmt.Errorf("archive has more than %d entries", maxArchiveEntries)
		}
		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return count, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode).Perm()); err != nil {
				return count, err
			}
		case tar.TypeSymlink:
			// The link TARGET is not resolved here — creating the link is
			// harmless, and following it during extraction is what would let
			// an archive write through it.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, err
			}
			os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return count, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, err
			}
			n, err := writeExtracted(target, tr, os.FileMode(header.Mode).Perm(), maxExtractedBytes-written)
			written += n
			if err != nil {
				return count, err
			}
		default:
			// Devices, FIFOs and sockets are skipped: an archive should not be
			// able to create a device node through a file manager.
			continue
		}
	}
	return count, nil
}

func extractZip(archivePath, dest string) (int, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	if len(r.File) > maxArchiveEntries {
		return 0, fmt.Errorf("archive has more than %d entries", maxArchiveEntries)
	}

	var count int
	var written int64
	for _, entry := range r.File {
		count++
		target, err := safeJoin(dest, entry.Name)
		if err != nil {
			return count, err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, entry.Mode().Perm()); err != nil {
				return count, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		rc, err := entry.Open()
		if err != nil {
			return count, err
		}
		n, err := writeExtracted(target, rc, entry.Mode().Perm(), maxExtractedBytes-written)
		rc.Close()
		written += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

// writeExtracted copies one entry, bounded.
//
// The budget is what stops a zip bomb: a few kilobytes on disk can declare
// gigabytes of content, and without a ceiling the first one fills the
// filesystem the panel runs on. The limit is on the total across the archive,
// not per entry, so it cannot be evaded by splitting.
func writeExtracted(target string, src io.Reader, mode os.FileMode, budget int64) (int64, error) {
	if budget <= 0 {
		return 0, fmt.Errorf("archive expands beyond the %d byte extraction limit", int64(maxExtractedBytes))
	}
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	written, err := io.Copy(f, io.LimitReader(src, budget))
	if err != nil {
		return written, err
	}
	if written == budget {
		// Hit the ceiling exactly: treat it as an overrun rather than a
		// coincidence, since the alternative is a silently truncated file.
		return written, fmt.Errorf("archive expands beyond the %d byte extraction limit", int64(maxExtractedBytes))
	}
	return written, nil
}
