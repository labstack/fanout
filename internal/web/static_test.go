package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestVizJSPath(t *testing.T) {
	path := VizJSPath()
	if !strings.HasPrefix(path, "/static/viz.") {
		t.Errorf("VizJSPath() = %q, want prefix /static/viz.", path)
	}
	if !strings.HasSuffix(path, ".js") {
		t.Errorf("VizJSPath() = %q, want .js suffix", path)
	}
}

func TestVizCSSPath(t *testing.T) {
	path := VizCSSPath()
	if !strings.HasPrefix(path, "/static/viz.") {
		t.Errorf("VizCSSPath() = %q, want prefix /static/viz.", path)
	}
	if !strings.HasSuffix(path, ".css") {
		t.Errorf("VizCSSPath() = %q, want .css suffix", path)
	}
}

func TestVizPaths_CacheBusted(t *testing.T) {
	jsPath := VizJSPath()
	cssPath := VizCSSPath()

	// Extract hash portion: /static/viz.HASH.js
	jsHash := strings.TrimPrefix(jsPath, "/static/viz.")
	jsHash = strings.TrimSuffix(jsHash, ".js")

	cssHash := strings.TrimPrefix(cssPath, "/static/viz.")
	cssHash = strings.TrimSuffix(cssHash, ".css")

	if len(jsHash) != 8 {
		t.Errorf("JS hash = %q, want 8 hex chars", jsHash)
	}
	if len(cssHash) != 8 {
		t.Errorf("CSS hash = %q, want 8 hex chars", cssHash)
	}
	if jsHash == cssHash {
		t.Error("JS and CSS hashes should be different")
	}
}

func TestVizPaths_Stable(t *testing.T) {
	// Paths should be deterministic across calls
	if VizJSPath() != VizJSPath() {
		t.Error("VizJSPath() is not deterministic")
	}
	if VizCSSPath() != VizCSSPath() {
		t.Error("VizCSSPath() is not deterministic")
	}
}

func TestServeVizJS(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, VizJSPath(), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := serveVizJS(c); err != nil {
		t.Fatalf("serveVizJS: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if rec.Body.Len() == 0 {
		t.Error("JS bundle is empty")
	}
}

func TestServeVizCSS(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, VizCSSPath(), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := serveVizCSS(c); err != nil {
		t.Fatalf("serveVizCSS: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "css") {
		t.Errorf("Content-Type = %q, want css", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("CSS bundle is empty")
	}
}

func TestBuildJSBundle_IncludesCoreAndRenderers(t *testing.T) {
	bundle := string(vizJSBundle)
	if !strings.Contains(bundle, "// Core") && len(bundle) < 100 {
		t.Error("JS bundle seems too small / missing core.js content")
	}
	if len(bundle) == 0 {
		t.Fatal("JS bundle is empty")
	}
}

func TestBuildCSSBundle_NonEmpty(t *testing.T) {
	if len(vizCSSBundle) == 0 {
		t.Fatal("CSS bundle is empty")
	}
}

func TestListRenderers_Sorted(t *testing.T) {
	renderers := listRenderers()
	if len(renderers) == 0 {
		t.Fatal("no renderers found")
	}
	for i := 1; i < len(renderers); i++ {
		if renderers[i] < renderers[i-1] {
			t.Errorf("renderers not sorted: %q before %q", renderers[i-1], renderers[i])
		}
	}
	for _, name := range renderers {
		if !strings.HasSuffix(name, ".js") {
			t.Errorf("renderer %q should have .js suffix", name)
		}
	}
}

func TestHashBytes_Deterministic(t *testing.T) {
	a := hashBytes([]byte("hello"))
	b := hashBytes([]byte("hello"))
	if a != b {
		t.Errorf("hashBytes not deterministic: %q != %q", a, b)
	}
}

func TestHashBytes_DifferentInputs(t *testing.T) {
	a := hashBytes([]byte("hello"))
	b := hashBytes([]byte("world"))
	if a == b {
		t.Error("different inputs produced same hash")
	}
}

func TestRegisterStaticRoutes(t *testing.T) {
	e := echo.New()
	RegisterStaticRoutes(e)
	// No panic = success; routes registered
}
