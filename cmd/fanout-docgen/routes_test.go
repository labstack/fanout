package main

import (
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/api"
)

const apiDir = "../../internal/api"

// The bug this guards: routes registered on an *echo.Group carry relative
// paths, and classifyRoute's SPA catch-all reports any non-/api/ path as
// public — so five telemetry endpoints were published as requiring no
// credential, on a page whose prose promises that cannot happen.
func TestCollectRoutesResolvesGroupPrefixes(t *testing.T) {
	routes, err := collectRoutes(apiDir)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}

	relative := map[string]bool{
		"/overview": true, "/topology": true, "/logs": true,
		"/trace": true, "/performance": true,
	}
	for _, r := range routes {
		if relative[r.Path] {
			t.Errorf("%s %s: group-relative path published without its prefix", r.Method, r.Path)
		}
	}

	var found bool
	for _, r := range routes {
		if r.Path == "/api/observability/overview" {
			found = true
			if r.Capability != "telemetry:read" {
				t.Errorf("observability overview requires %q, want telemetry:read", r.Capability)
			}
		}
	}
	if !found {
		t.Error("the observability group's routes are missing from the reference entirely")
	}
}

// The roles matrix must agree with the middleware, which is why it is generated
// at all: the hand-written one claimed viewer could not run the agent.
func TestRenderRolesMatchesTheMiddleware(t *testing.T) {
	body, err := renderRoles()
	if err != nil {
		t.Fatalf("renderRoles: %v", err)
	}
	text := string(body)

	for role, caps := range api.RoleCapabilities() {
		for _, capability := range caps {
			if !strings.Contains(text, "`"+capability+"`") {
				t.Errorf("%s holds %q but the page never names it", role, capability)
			}
		}
	}
	if !strings.Contains(text, "generated: true") {
		t.Error("missing the generated marker")
	}
}

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
