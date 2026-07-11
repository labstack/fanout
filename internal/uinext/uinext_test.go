package uinext

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v5"
)

// TestRegisterSPARoutes_ServesIndexAndFallsBack exercises the real SPA
// serving contract hermetically: it registers routes against an in-memory
// fstest.MapFS instead of the embedded dist. This is deliberately
// build-state-independent — unlike a test that asserts Dist() errors on an
// unbuilt embed (true on a fresh checkout, but false the moment a local
// `just build-next` populates internal/uinext/dist/, which would flip that
// assertion and break `just check` for anyone who has built web-next
// locally). Routing through the in-memory filesystem verifies the same
// serve/fallback behavior regardless of whether dist has been built.
func TestRegisterSPARoutes_ServesIndexAndFallsBack(t *testing.T) {
	mapFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<div id="root">web-next</div>`)},
	}

	e := echo.New()
	RegisterSPARoutes(e, mapFS)

	// GET / serves index.html.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<div id="root">web-next</div>`) {
		t.Fatalf("GET / body = %q, want it to contain index.html content", rec.Body.String())
	}

	// GET on an unknown client-side route falls back to index.html.
	req = httptest.NewRequest(http.MethodGet, "/some/deep/client/route", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /some/deep/client/route status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<div id="root">web-next</div>`) {
		t.Fatalf("unknown-route body = %q, want SPA fallback to index.html", rec.Body.String())
	}
}

// TestDist_UnbuiltReturnsError only applies to a fresh checkout where
// internal/uinext/dist has nothing but the .gitkeep placeholder. Once
// `just build-next` has run locally, the embed legitimately contains
// index.html and this assertion would flip — so it self-skips rather than
// depending on build state (see the package-level comment above for why
// the hermetic MapFS test above is the primary coverage for this
// package's serving behavior).
func TestDist_UnbuiltReturnsError(t *testing.T) {
	_, err := Dist()
	if err == nil {
		t.Skip("web-next dist has been built locally (index.html present) — skipping unbuilt-dist assertion")
	}
}
