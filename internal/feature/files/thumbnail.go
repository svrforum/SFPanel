package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "image/gif" // registered for image.Decode
	_ "image/png" // registered for image.Decode

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// Thumbnail limits.
//
// The pixel cap is the one that matters, and it is not the same thing as the
// byte cap. Decoding expands an image into raw memory: a 20 MB JPEG that is
// 20000x20000 is 1.6 GB of RGBA once decoded, and a handful of those requests
// take the panel down. So the header is read first — DecodeConfig parses
// dimensions without decoding pixels — and the image is rejected on its
// declared size before a single row is allocated.
const (
	maxThumbSourceBytes = 25 * 1024 * 1024
	maxThumbPixels      = 80_000_000
	defaultThumbSize    = 192
	maxThumbSize        = 512
)

// thumbCacheDir holds rendered thumbnails. Beside the trash rather than next to
// the originals, for the same reason: a cache written into the operator's
// directories would show up in their listings and their backups.
const thumbCacheDir = "/var/lib/sfpanel/thumbs"

// thumbCacheDirOverride redirects the cache during tests.
var thumbCacheDirOverride string

func thumbDir() string {
	if thumbCacheDirOverride != "" {
		return thumbCacheDirOverride
	}
	return thumbCacheDir
}

// thumbnailable reports whether this name is a format the panel can decode.
//
// WebP and AVIF are deliberately absent. golang.org/x/image decodes WebP but
// not AVIF, so adding the dependency would cover half the gap; both fall back
// to an icon, which is the same outcome as any other unsupported file.
var thumbnailable = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
}

// CanThumbnail reports whether a name is worth asking the thumbnail endpoint
// about, so the UI can skip the request instead of collecting a 415 per tile.
func CanThumbnail(name string) bool {
	return thumbnailable[strings.ToLower(filepath.Ext(name))]
}

// cacheKey identifies a rendered thumbnail.
//
// The modification time is part of the key on purpose: an edited file gets a
// new key and therefore a new thumbnail, with no invalidation step to forget.
// The size is in the key too, so two grid densities do not fight over one file.
func cacheKey(path string, modTime time.Time, size int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", path, modTime.UnixNano(), size)))
	return hex.EncodeToString(sum[:]) + ".jpg"
}

// Thumbnail renders a scaled-down copy of an image.
//
// GET /files/thumbnail?path=/photos/a.jpg&size=192
func (h *Handler) Thumbnail(c echo.Context) error {
	filePath := c.QueryParam("path")
	if err := validatePath(filePath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	filePath = filepath.Clean(filePath)

	if isReadProtectedPath(filePath) {
		return response.Fail(c, http.StatusForbidden, response.ErrReadProtected, "Access to this file is not allowed")
	}
	if !CanThumbnail(filePath) {
		return response.Fail(c, http.StatusUnsupportedMediaType, response.ErrUnsupportedFormat,
			"No thumbnail is available for this format")
	}

	size := defaultThumbSize
	if raw := c.QueryParam("size"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			size = n
		}
	}
	if size > maxThumbSize {
		size = maxThumbSize
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
	if !info.Mode().IsRegular() {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "Not a regular file")
	}
	if info.Size() > maxThumbSourceBytes {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileTooLarge, "Image is too large to thumbnail")
	}

	cached := filepath.Join(thumbDir(), cacheKey(filePath, info.ModTime(), size))
	if data, err := os.ReadFile(cached); err == nil {
		return serveThumb(c, data)
	}

	data, err := renderThumbnail(filePath, size)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}

	// Best effort: a cache that cannot be written is a performance problem, not
	// a reason to withhold the thumbnail that was already rendered.
	if mkErr := os.MkdirAll(thumbDir(), 0o700); mkErr == nil {
		tmp := cached + ".tmp"
		if os.WriteFile(tmp, data, 0o600) == nil {
			_ = os.Rename(tmp, cached)
		}
	}
	return serveThumb(c, data)
}

func serveThumb(c echo.Context, data []byte) error {
	// The key includes the file's mtime, so a URL's content never changes.
	// Telling the browser that is what keeps a grid of two hundred tiles from
	// re-requesting everything on every navigation.
	c.Response().Header().Set("Cache-Control", "private, max-age=86400")
	return c.Blob(http.StatusOK, "image/jpeg", data)
}

// renderThumbnail decodes, scales and re-encodes an image.
func renderThumbnail(path string, size int) ([]byte, error) {
	// O_NOFOLLOW: the read path refuses a leaf symlink at kernel level rather
	// than trusting a check that a local process could race.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open image")
	}
	defer f.Close()

	// Header first. DecodeConfig reads dimensions without allocating pixels,
	// which is the whole defence: a declared 20000x20000 is refused here rather
	// than after 1.6 GB has already been committed.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("not a readable image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("image has no dimensions")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxThumbPixels {
		return nil, fmt.Errorf("image is %dx%d, larger than this panel will decode", cfg.Width, cfg.Height)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("cannot rewind image")
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("could not decode the image")
	}

	dst := downscale(src, size)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, fmt.Errorf("could not encode the thumbnail")
	}
	return out.Bytes(), nil
}

// downscale reduces an image by averaging over each destination pixel's source
// area.
//
// Written out rather than pulled from golang.org/x/image, which is the usual
// home for a CatmullRom scaler. For a thumbnail the dependency buys nothing:
// area averaging is what you want at large reduction ratios anyway — it is
// exactly a box filter over the pixels being discarded, so it neither aliases
// the way nearest-neighbour does nor rings the way a cubic kernel does on a
// hard edge. The whole of it is thirty lines.
func downscale(src image.Image, box int) *image.RGBA {
	bounds := src.Bounds()
	w, h := scaleToFit(bounds.Dx(), bounds.Dy(), box)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// A white ground, so a transparent PNG does not become a black square once
	// it is flattened into JPEG, which has no alpha channel.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)

	for y := 0; y < h; y++ {
		// The source rows this destination row covers. Computed from the edges
		// rather than a centre and a radius, so every source pixel belongs to
		// exactly one destination pixel and none is counted twice or skipped.
		y0 := bounds.Min.Y + y*bounds.Dy()/h
		y1 := bounds.Min.Y + (y+1)*bounds.Dy()/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0 := bounds.Min.X + x*bounds.Dx()/w
			x1 := bounds.Min.X + (x+1)*bounds.Dx()/w
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var rSum, gSum, bSum, aSum, count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					// RGBA() returns 16-bit alpha-premultiplied values, so the
					// average is taken in premultiplied space and stays
					// correct for partially transparent source pixels.
					r, g, b, a := src.At(sx, sy).RGBA()
					rSum += uint64(r)
					gSum += uint64(g)
					bSum += uint64(b)
					aSum += uint64(a)
					count++
				}
			}
			if count == 0 {
				continue
			}
			// Composite over the white ground rather than replacing it: an
			// alpha of zero must leave white showing, not a black pixel.
			alpha := float64(aSum/count) / 65535
			toByte := func(sum uint64) uint8 {
				channel := float64(sum/count) / 257 // 16-bit -> 8-bit
				return uint8(channel + 255*(1-alpha))
			}
			dst.SetRGBA(x, y, color.RGBA{R: toByte(rSum), G: toByte(gSum), B: toByte(bSum), A: 255})
		}
	}
	return dst
}

// scaleToFit returns the largest size within a square box that keeps the
// aspect ratio, never enlarging: a 64px icon rendered at 192 would only be a
// blurrier 64px icon.
func scaleToFit(width, height, box int) (int, int) {
	if width <= box && height <= box {
		return width, height
	}
	if width >= height {
		h := height * box / width
		if h < 1 {
			h = 1
		}
		return box, h
	}
	w := width * box / height
	if w < 1 {
		w = 1
	}
	return w, box
}

// SweepThumbnails removes cached thumbnails that have not been read recently.
//
// The cache is keyed on modification time, so an edited file simply orphans its
// old entry rather than replacing it. Without a sweep the directory only ever
// grows.
func SweepThumbnails(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(thumbDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	var removed int
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(thumbDir(), e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}
