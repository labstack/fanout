// Package uinext embeds the clean-rewrite React UI (web-next) and serves it
// as a SPA. Selected at runtime by FANOUT_UI_NEXT; the default remains
// internal/ui until web-next reaches parity and the dirs are swapped.
package uinext

import (
	"fmt"
	"io/fs"

	"embed"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/ui"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded web-next filesystem rooted at dist, or an error
// if it was built without running the web-next build first.
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("web-next UI not built: run `just build-next`: %w", err)
	}
	return sub, nil
}

// RegisterSPARoutes delegates to the shared SPA handler in internal/ui so the
// serve/catch-all behavior stays identical between the two UIs.
func RegisterSPARoutes(e *echo.Echo, spaFS fs.FS) {
	ui.RegisterSPARoutes(e, spaFS)
}
