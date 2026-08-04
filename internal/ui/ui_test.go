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

func request(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}
