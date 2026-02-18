package web

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
)

//go:embed static
var staticFS embed.FS

var (
	vizJSBundle  []byte
	vizCSSBundle []byte
	vizJSPath    string
	vizCSSPath   string
)

func init() {
	vizJSBundle = buildJSBundle()
	vizCSSBundle = buildCSSBundle()
	vizJSPath = "/static/viz." + hashBytes(vizJSBundle)[:8] + ".js"
	vizCSSPath = "/static/viz." + hashBytes(vizCSSBundle)[:8] + ".css"
}

// VizJSPath returns the cache-busted URL for the viz JS bundle.
func VizJSPath() string { return vizJSPath }

// VizCSSPath returns the cache-busted URL for the viz CSS bundle.
func VizCSSPath() string { return vizCSSPath }

// RegisterStaticRoutes registers the cache-busted static asset routes.
func RegisterStaticRoutes(e *echo.Echo) {
	e.GET(vizJSPath, serveVizJS)
	e.GET(vizCSSPath, serveVizCSS)
}

func serveVizJS(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "application/javascript; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, "application/javascript", vizJSBundle)
}

func serveVizCSS(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/css; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, "text/css", vizCSSBundle)
}

func buildJSBundle() []byte {
	var sb strings.Builder

	// Core first
	core, err := staticFS.ReadFile("static/core.js")
	if err != nil {
		panic("web: failed to read core.js: " + err.Error())
	}
	sb.Write(core)
	sb.WriteByte('\n')

	// Then renderers in sorted order for determinism
	rendererFiles := listRenderers()
	for _, name := range rendererFiles {
		data, err := staticFS.ReadFile("static/renderers/" + name)
		if err != nil {
			panic("web: failed to read renderer " + name + ": " + err.Error())
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}

	return []byte(sb.String())
}

func buildCSSBundle() []byte {
	data, err := staticFS.ReadFile("static/viz.css")
	if err != nil {
		panic("web: failed to read viz.css: " + err.Error())
	}
	return data
}

func listRenderers() []string {
	entries, err := fs.ReadDir(staticFS, "static/renderers")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".js") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:])
}
