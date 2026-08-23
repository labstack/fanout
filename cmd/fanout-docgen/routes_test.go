package main

import (
	"strings"
	"testing"
)

const apiDir = "../../internal/api"

// The route reference's whole value is that the Requires column is the
// middleware's own answer rather than a description of it. So the test that
// matters is that every registered route gets one.
func TestCollectRoutesClassifiesEveryRegisteredRoute(t *testing.T) {
	routes, err := collectRoutes(apiDir)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("found no routes; the API always registers some")
	}

	for _, r := range routes {
		if r.Policy == "" {
			t.Errorf("%s %s: no policy, which would publish as an empty requirement", r.Method, r.Path)
		}
		if requirement(r) == "" {
			t.Errorf("%s %s: renders no requirement", r.Method, r.Path)
		}
	}
}

// Pointed at the generator's own package: it parses fine and registers nothing.
// Emitting an empty route table would be worse than failing, because an empty
// table reads as "this server has no API".
func TestCollectRoutesRejectsADirectoryWithNoRoutes(t *testing.T) {
	if _, err := collectRoutes("."); err == nil {
		t.Fatal("collectRoutes accepted a directory that registers no routes")
	}
}

func TestRenderRoutesListsEveryRoute(t *testing.T) {
	routes, err := collectRoutes(apiDir)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}
	body := string(renderRoutes(routes))

	if !strings.Contains(body, "generated: true") {
		t.Error("missing the generated marker")
	}
	if !strings.Contains(body, "summary: ") {
		t.Error("no summary, which llms.txt refuses to index")
	}
	for _, r := range routes {
		if !strings.Contains(body, "`"+r.Path+"`") {
			t.Errorf("route %s is registered but absent from the page", r.Path)
		}
	}
}
