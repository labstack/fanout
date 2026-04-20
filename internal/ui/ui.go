// Package ui embeds the built React admin UI and serves it as a SPA.
package ui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded UI filesystem, rooted at the dist directory.
// Returns an error if the embedded dist has no index.html — which happens
// when the Go binary is built without running the frontend build first
// (the embed only has the .gitkeep placeholder). Callers should log and
// skip SPA registration rather than serve 404s on every route.
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("UI not built: index.html not in embedded dist (run `bun run build` in web/ then rebuild Go): %w", err)
	}
	return sub, nil
}

// RegisterSPARoutes adds a catch-all route that serves the React SPA.
// Static files (JS, CSS, etc.) are served directly from the embedded
// filesystem. Any other path falls back to index.html for client-side
// routing. Must be called after all API routes so they take priority.
func RegisterSPARoutes(e *echo.Echo, spaFS fs.FS) {
	httpFS := http.FS(spaFS)

	e.GET("/*", func(c *echo.Context) error {
		path := c.Param("*")
		if path == "" {
			path = "index.html"
		}

		if served, err := tryServeFile(c, httpFS, path); served {
			return err
		}
		return serveIndex(c, httpFS)
	})
}

// tryServeFile attempts to open and serve a file. Returns (true, nil) if
// served, (true, err) if served with error, or (false, nil) if not
// found / is a directory.
func tryServeFile(c *echo.Context, httpFS http.FileSystem, path string) (bool, error) {
	f, err := httpFS.Open(path)
	if err != nil {
		return false, nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		return false, nil
	}

	http.ServeContent(c.Response(), c.Request(), stat.Name(), stat.ModTime(), f.(io.ReadSeeker))
	return true, nil
}

// serveIndex serves index.html as the SPA entry point.
func serveIndex(c *echo.Context, httpFS http.FileSystem) error {
	f, err := httpFS.Open("index.html")
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "SPA index not found")
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to stat index.html")
	}

	http.ServeContent(c.Response(), c.Request(), stat.Name(), stat.ModTime(), f.(io.ReadSeeker))
	return nil
}
