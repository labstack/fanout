package ui

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHandlerServesEmbeddedSPAAndFallback(t *testing.T) {
	handler := Handler()
	index := request(t, handler, "/")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("GET / = %d %q", index.Code, index.Body.String())
	}

	fallback := request(t, handler, "/dashboards/incident-command")
	if fallback.Code != http.StatusOK || fallback.Body.String() != index.Body.String() {
		t.Fatalf("SPA fallback did not return index.html")
	}

	assetPath := regexp.MustCompile(`src="([^"]+\.js)"`).FindStringSubmatch(index.Body.String())
	if len(assetPath) != 2 {
		t.Fatalf("index.html has no JavaScript asset: %q", index.Body.String())
	}
	asset := request(t, handler, assetPath[1])
	if asset.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", assetPath[1], asset.Code)
	}
	assetBody, err := io.ReadAll(asset.Result().Body)
	if err != nil || len(assetBody) == 0 {
		t.Fatalf("embedded JavaScript asset is empty: %v", err)
	}
}

func TestHandlerServesRobotsAndRefusesMissingAssets(t *testing.T) {
	handler := Handler()

	robots := request(t, handler, "/robots.txt")
	if robots.Code != http.StatusOK {
		t.Fatalf("GET /robots.txt = %d", robots.Code)
	}
	if got := robots.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("robots.txt content type = %q", got)
	}
	if body := robots.Body.String(); !strings.Contains(body, "Disallow: /") {
		t.Fatalf("robots.txt = %q", body)
	}

	// A missing asset used to answer with the application shell and a 200, so
	// nothing in the response said it was missing.
	missing := request(t, handler, "/assets/does-not-exist.js")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET a missing asset = %d, want 404", missing.Code)
	}
	// dist ships favicon.svg, so the .ico a browser asks for first is simply
	// not there and has to say so.
	if icon := request(t, handler, "/favicon.ico"); icon.Code != http.StatusNotFound {
		t.Fatalf("GET /favicon.ico = %d, want 404", icon.Code)
	}
	if trailing := request(t, handler, "/checkout."); trailing.Code != http.StatusOK {
		t.Fatalf("a trailing dot is a client route, not a file: got %d", trailing.Code)
	}

	// A client route still resolves to the shell, extension or not.
	if route := request(t, handler, "/chat/01a07a17-b2e2-7334-b77d-64e9ed69fa8e"); route.Code != http.StatusOK {
		t.Fatalf("GET a client route = %d", route.Code)
	}
}

// Hashed bundles were served uncompressed with no validator or lifetime, so
// every navigation downloaded about 2.4 MB of JavaScript again.
func TestHandlerCachesHashedAssetsAndCompressesThem(t *testing.T) {
	handler := Handler()
	index := request(t, handler, "/")
	if got := index.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index.html Cache-Control = %q, want no-cache", got)
	}
	if index.Header().Get("ETag") == "" {
		t.Fatal("index.html has no ETag")
	}
	if got := request(t, handler, "/dashboards/x").Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("SPA fallback Cache-Control = %q, want no-cache", got)
	}
	script := regexp.MustCompile(`src="([^"]+\.js)"`).FindStringSubmatch(index.Body.String())[1]

	plain := request(t, handler, script)
	if got := plain.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("hashed asset Cache-Control = %q", got)
	}
	if plain.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(plain.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("identity response headers = %v", plain.Header())
	}

	zipped := request(t, handler, script, "Accept-Encoding", "gzip, deflate, br")
	if zipped.Header().Get("Content-Encoding") != "gzip" || zipped.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzip response headers = %v", zipped.Header())
	}
	if zipped.Body.Len() >= plain.Body.Len()/2 {
		t.Fatalf("gzip body is %d bytes for %d", zipped.Body.Len(), plain.Body.Len())
	}
	reader, err := gzip.NewReader(zipped.Body)
	if err != nil {
		t.Fatal(err)
	}
	unzipped, err := io.ReadAll(reader)
	if err != nil || string(unzipped) != plain.Body.String() {
		t.Fatalf("gzip body does not decode to the asset: %v", err)
	}
	if zipped.Header().Get("ETag") == plain.Header().Get("ETag") {
		t.Fatal("both encodings share one ETag")
	}
	if refused := request(t, handler, script, "Accept-Encoding", "gzip;q=0"); refused.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 still got gzip")
	}

	revalidated := request(t, handler, script, "Accept-Encoding", "gzip", "If-None-Match", zipped.Header().Get("ETag"))
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 {
		t.Fatalf("If-None-Match = %d with %d bytes, want 304", revalidated.Code, revalidated.Body.Len())
	}
}

func request(t *testing.T, handler http.Handler, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	handler.ServeHTTP(recorder, req)
	return recorder
}
