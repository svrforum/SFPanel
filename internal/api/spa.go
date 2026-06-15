package api

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// spaHandler serves the embedded React SPA with fallback to index.html
// for client-side routing. API (/api/*) and WebSocket (/ws/*) routes are
// registered before this catch-all so they take precedence.
func spaHandler(fsys fs.FS) echo.HandlerFunc {
	subFS, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		slog.Error("failed to create sub-filesystem for embedded SPA", "error", err)
		panic("embedded SPA filesystem unavailable")
	}
	fileServer := http.FileServer(http.FS(subFS))

	return func(c echo.Context) error {
		path := c.Request().URL.Path

		// Strip leading slash and try to open the file
		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		f, err := subFS.Open(cleanPath)
		if err != nil {
			// A request for a concrete build asset that doesn't exist must 404,
			// NOT fall back to index.html. Returning text/html for a missing
			// .js chunk trips the browser's strict MIME check and — after an
			// upgrade leaves a client referencing old chunk hashes — bricks the
			// whole SPA. Only extensionless paths are client-side routes.
			if isStaticAssetPath(cleanPath) {
				return c.String(http.StatusNotFound, "not found")
			}
			// SPA client-side route → serve index.html, marked no-cache so a
			// panel upgrade takes effect on the next load instead of a stale
			// shell pointing at chunk hashes that no longer exist.
			index, indexErr := subFS.Open("index.html")
			if indexErr != nil {
				return c.String(http.StatusNotFound, "index.html not found")
			}
			defer index.Close()
			content, readErr := io.ReadAll(index)
			if readErr != nil {
				return c.String(http.StatusInternalServerError, "failed to read index.html")
			}
			c.Response().Header().Set("Cache-Control", "no-cache")
			return c.HTMLBlob(http.StatusOK, content)
		}
		f.Close()

		// index.html must always revalidate; hashed build assets are immutable.
		switch {
		case cleanPath == "index.html":
			c.Response().Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(cleanPath, "assets/"):
			c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		// Serve the actual file
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

// isStaticAssetPath reports whether p names a concrete build asset (its last
// segment has a file extension, e.g. "assets/x.js", "favicon.ico") rather than
// a client-side SPA route ("docker", "disk/overview"). Missing assets get a
// 404; missing routes fall back to index.html.
func isStaticAssetPath(p string) bool {
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	return strings.Contains(base, ".")
}
