package files

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func withTempThumbs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	thumbCacheDirOverride = dir
	t.Cleanup(func() { thumbCacheDirOverride = "" })
	return dir
}

func writePNG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestCanThumbnail(t *testing.T) {
	for _, name := range []string{"a.jpg", "A.JPEG", "b.png", "c.gif"} {
		if !CanThumbnail(name) {
			t.Errorf("CanThumbnail(%q) = false", name)
		}
	}
	// Unsupported formats fall back to an icon, which is the same outcome as
	// any other file the panel cannot render.
	for _, name := range []string{"a.webp", "a.avif", "a.svg", "a.txt", "a.tar.gz", "noext"} {
		if CanThumbnail(name) {
			t.Errorf("CanThumbnail(%q) = true", name)
		}
	}
}

func TestScaleToFit(t *testing.T) {
	cases := []struct{ w, h, box, wantW, wantH int }{
		{1000, 500, 200, 200, 100}, // landscape
		{500, 1000, 200, 100, 200}, // portrait
		{800, 800, 200, 200, 200},  // square
		// Never enlarge: a 64px icon rendered at 192 is only a blurrier 64px icon.
		{64, 64, 192, 64, 64},
		{10, 200, 100, 5, 100},
		// An extreme ratio must not round a dimension to zero.
		{10000, 3, 100, 100, 1},
	}
	for _, c := range cases {
		w, h := scaleToFit(c.w, c.h, c.box)
		if w != c.wantW || h != c.wantH {
			t.Errorf("scaleToFit(%d,%d,%d) = %dx%d, want %dx%d", c.w, c.h, c.box, w, h, c.wantW, c.wantH)
		}
		if w < 1 || h < 1 {
			t.Errorf("scaleToFit(%d,%d,%d) produced a zero dimension", c.w, c.h, c.box)
		}
	}
}

// The cache key carries the modification time, so an edited file gets a new
// thumbnail with no invalidation step to forget.
func TestCacheKeyChangesWithContentAndSize(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	a := cacheKey("/photos/x.jpg", base, 192)

	if cacheKey("/photos/x.jpg", base, 192) != a {
		t.Error("the same inputs produced two different keys")
	}
	if cacheKey("/photos/x.jpg", base.Add(time.Second), 192) == a {
		t.Error("an edited file reuses its old thumbnail")
	}
	if cacheKey("/photos/x.jpg", base, 384) == a {
		t.Error("two grid densities share one cache entry")
	}
	if cacheKey("/photos/y.jpg", base, 192) == a {
		t.Error("two files share one cache entry")
	}
}

func TestRenderThumbnailScalesDown(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.png")
	writePNG(t, src, 800, 400, color.RGBA{R: 200, G: 40, B: 40, A: 255})

	data, err := renderThumbnail(src, 100)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not a JPEG: %v", err)
	}
	if cfg.Width != 100 || cfg.Height != 50 {
		t.Errorf("thumbnail is %dx%d, want 100x50", cfg.Width, cfg.Height)
	}
	// A thumbnail that is larger than its source would defeat the purpose.
	srcInfo, _ := os.Stat(src)
	if int64(len(data)) > srcInfo.Size() {
		t.Errorf("thumbnail (%d bytes) is larger than the source (%d)", len(data), srcInfo.Size())
	}
}

// A transparent PNG has no alpha once flattened into JPEG. Compositing over
// white is what stops it becoming a black square.
func TestRenderThumbnailFlattensTransparencyToWhite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clear.png")
	writePNG(t, src, 40, 40, color.RGBA{}) // fully transparent

	data, err := renderThumbnail(src, 20)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(10, 10).RGBA()
	// JPEG is lossy, so allow a little drift rather than demanding pure white.
	if r < 0xF000 || g < 0xF000 || b < 0xF000 {
		t.Errorf("transparent pixel rendered as %04x%04x%04x, want near-white", r, g, b)
	}
}

func TestRenderThumbnailRejectsNonImages(t *testing.T) {
	dir := t.TempDir()
	notAnImage := filepath.Join(dir, "notes.png") // right name, wrong bytes
	if err := os.WriteFile(notAnImage, []byte("this is not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := renderThumbnail(notAnImage, 100); err == nil {
		t.Error("a file that is not an image produced a thumbnail")
	}
}

// The defence that matters. Decoding expands an image into raw memory: a
// declared 20000x20000 is 1.6 GB of RGBA, and a handful of those requests take
// the panel down. DecodeConfig reads the header only, so the refusal happens
// before a single row is allocated.
func TestRenderThumbnailRefusesAPixelBomb(t *testing.T) {
	dir := t.TempDir()
	bomb := filepath.Join(dir, "bomb.png")

	// A valid PNG header declaring enormous dimensions, with no image data
	// behind it — a few dozen bytes on disk. DecodeConfig only reads IHDR, so
	// this is exactly what the guard has to catch.
	if err := os.WriteFile(bomb, pngHeaderOnly(30000, 30000), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := renderThumbnail(bomb, 100)
	if err == nil {
		t.Fatal("a 30000x30000 declaration was accepted for decoding")
	}
	// Assert the REASON, not just that something failed.
	//
	// The crafted file also has a truncated image body, so with the guard
	// removed the decode fails anyway — for a completely different reason. An
	// earlier version of this test checked only that an error came back and
	// therefore passed with the defence deleted, which is worse than having no
	// test at all.
	if !strings.Contains(err.Error(), "larger than this panel will decode") {
		t.Fatalf("refused with %q — that is not the header-size check", err)
	}
	// The header check is microseconds; a decode that allocated first is not.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("refusal took %v — the pixels were probably allocated first", elapsed)
	}
}

// pngHeaderOnly builds a PNG signature and IHDR chunk and nothing else.
//
// Built from scratch rather than by patching an encoded image. The first
// version of this test rewrote the dimensions inside a real PNG and recomputed
// the CRC over the wrong span, so the header did not parse at all — the test
// then "passed" because DecodeConfig rejected it as unreadable, which is not
// the check being tested.
func pngHeaderOnly(width, height uint32) []byte {
	out := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

	ihdr := make([]byte, 0, 13)
	be := func(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }
	ihdr = append(ihdr, be(width)...)
	ihdr = append(ihdr, be(height)...)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit depth, RGBA, deflate, adaptive filter, non-interlaced

	chunk := append([]byte("IHDR"), ihdr...)
	out = append(out, be(uint32(len(ihdr)))...)
	out = append(out, chunk...)
	out = append(out, be(crc32.ChecksumIEEE(chunk))...)
	return out
}

func TestThumbnailHandler(t *testing.T) {
	withTempThumbs(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.png")
	writePNG(t, src, 300, 300, color.RGBA{G: 180, A: 255})

	call := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/files/thumbnail?path="+path, nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.QueryParams().Set("path", path)
		if err := (&Handler{}).Thumbnail(c); err != nil {
			t.Fatalf("Thumbnail: %v", err)
		}
		return rec.Code, rec.Header().Get("Content-Type")
	}

	status, ctype := call(src)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ctype != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ctype)
	}

	// A format with no decoder answers 415 rather than an error page, so the
	// UI can fall back to an icon.
	notImage := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notImage, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(notImage); status != http.StatusUnsupportedMediaType {
		t.Errorf("a non-image returned %d, want 415", status)
	}
}

// The cache is keyed on modification time, so an edited file orphans its old
// entry rather than replacing it. Without a sweep the directory only grows.
func TestSweepThumbnails(t *testing.T) {
	dir := withTempThumbs(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "fresh.jpg")
	stale := filepath.Join(dir, "stale.jpg")
	for _, p := range []string{fresh, stale} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepThumbnails(24 * time.Hour)
	if err != nil {
		t.Fatalf("SweepThumbnails: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d, want 1", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recently used thumbnail was swept")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("an unused thumbnail survived")
	}
}
