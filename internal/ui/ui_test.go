package ui

import (
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
	if icon := request(t, handler, "/favicon.ico"); icon.Code == http.StatusOK && strings.Contains(icon.Body.String(), "<div id=\"root\">") {
		t.Fatal("/favicon.ico answered with the application shell")
	}

	// A client route still resolves to the shell, extension or not.
	if route := request(t, handler, "/chat/01a07a17-b2e2-7334-b77d-64e9ed69fa8e"); route.Code != http.StatusOK {
		t.Fatalf("GET a client route = %d", route.Code)
	}
}

func request(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}
