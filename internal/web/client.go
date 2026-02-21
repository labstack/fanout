package web

import (
	"embed"
	"io"
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed all:dist
var clientDist embed.FS

// ClientDist returns the embedded React app filesystem, rooted at the dist directory.
func ClientDist() (fs.FS, error) {
	return fs.Sub(clientDist, "dist")
}

// RegisterSPARoutes adds a catch-all route that serves the React SPA.
// Static files (JS, CSS, etc.) are served directly from the embedded filesystem.
// Any other path falls back to index.html for client-side routing.
// This must be called after all API routes are registered so they take priority.
func RegisterSPARoutes(e *echo.Echo, spaFS fs.FS) {
	httpFS := http.FS(spaFS)

	e.GET("/*", func(c *echo.Context) error {
		path := c.Param("*")
		if path == "" {
			path = "index.html"
		}

		// Try to serve the requested static file
		if served, err := tryServeFile(c, httpFS, path); served {
			return err
		}

		// Fall back to index.html for client-side routing
		return serveIndex(c, httpFS)
	})
}

// tryServeFile attempts to open and serve a file. Returns (true, nil) if served,
// (true, err) if served with error, or (false, nil) if file not found/is directory.
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
