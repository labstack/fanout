package main

import (
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/api"
)

// The directories the generator scans by default, from the same list main.go
// ships. A test that scanned only internal/api would keep passing through
// exactly the regression #188 was about.
var routeDirs = []string{"../../internal/api", "../../internal/agent", "../../cmd/fanout"}

// The bug this guards: routes registered on an *echo.Group carry relative
// paths, and classifyRoute's SPA catch-all reports any non-/api/ path as
// public — so five telemetry endpoints were published as requiring no
// credential, on a page whose prose promises that cannot happen.
func TestCollectRoutesResolvesGroupPrefixes(t *testing.T) {
	routes, err := collectRoutes(routeDirs)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}

	relative := map[string]bool{
		"/overview": true, "/topology": true, "/logs": true,
		"/trace": true, "/performance": true,
		"/threads": true, "/threads/:threadID": true,
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

// A group's own root is registered as `group.POST("", ...)`. Requiring a
// leading slash dropped it silently, so the one route that actually runs the
// investigator was absent from a page an operator reads to see what an
// instance exposes.
func TestCollectRoutesIncludesGroupRootRegistrations(t *testing.T) {
	routes, err := collectRoutes(routeDirs)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}

	for _, r := range routes {
		if r.Method == "POST" && r.Path == "/api/agent" {
			if r.Capability != "agent:run" {
				t.Errorf("POST /api/agent requires %q, want agent:run", r.Capability)
			}
			return
		}
	}
	t.Error("POST /api/agent is registered as group.POST(\"\") but is absent from the reference")
}

// #188: the table was built from internal/api alone, so an operator auditing
// exposure would conclude the profiling and metrics endpoints were not served.
func TestCollectRoutesCoversRoutesRegisteredOutsideInternalAPI(t *testing.T) {
	routes, err := collectRoutes(routeDirs)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}

	seen := map[string]api.RouteDoc{}
	for _, r := range routes {
		seen[r.Method+" "+r.Path] = r
	}

	for _, want := range []struct{ key, capability string }{
		{"GET /-/metrics", "operations:read"},
		{"GET /debug/pprof/", "operations:read"},
		{"GET /debug/pprof/profile", "operations:read"},
		{"Any /api/mcp", "telemetry:read"},
		{"Any /mcp", ""},
		{"GET /", ""},
		{"GET /*", ""},
	} {
		got, ok := seen[want.key]
		if !ok {
			t.Errorf("%s is registered but absent from the reference", want.key)
			continue
		}
		if got.Capability != want.capability {
			t.Errorf("%s requires %q, want %q", want.key, got.Capability, want.capability)
		}
	}
}

// An `Any` registration answers every verb. Publishing one row for it is only
// honest while the middleware gives every verb the same answer.
func TestDescribeAnyCollapsesOnlyWhenEveryMethodAgrees(t *testing.T) {
	doc, err := describe("Any", "/mcp")
	if err != nil {
		t.Fatalf("describe Any /mcp: %v", err)
	}
	if doc.Method != "Any" {
		t.Errorf("method rendered as %q, want Any", doc.Method)
	}
	if doc.Policy != api.RoutePolicyProtocol {
		t.Errorf("/mcp classified as %q, want %q", doc.Policy, api.RoutePolicyProtocol)
	}

	// POST-only in the middleware, so an Any registration on it would publish a
	// requirement that is wrong for GET.
	if _, err := describe("Any", "/api/auth/setup"); err == nil {
		t.Error("describe accepted an Any route the middleware classifies for only some methods")
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
	routes, err := collectRoutes(routeDirs)
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
// table reads as "this server has no API". Now that several directories are
// scanned, the same has to hold per directory — one that stops registering
// routes would otherwise just shrink the table.
func TestCollectRoutesRejectsADirectoryWithNoRoutes(t *testing.T) {
	if _, err := collectRoutes([]string{"."}); err == nil {
		t.Fatal("collectRoutes accepted a directory that registers no routes")
	}
	if _, err := collectRoutes(append(append([]string{}, routeDirs...), ".")); err == nil {
		t.Fatal("collectRoutes accepted a scan list containing a directory that registers no routes")
	}
}

// The per-directory guard must count what a directory registers, not how much
// it adds to the total. Counting growth means a route registered in two places
// dedupes to nothing new and the second directory reads as registering none —
// failing the build over a duplicate rather than over a real problem.
func TestCollectRoutesGuardCountsRegistrationsNotNewness(t *testing.T) {
	dir := routeDirs[0]
	routes, err := collectRoutes([]string{dir, dir})
	if err != nil {
		t.Fatalf("collectRoutes refused a directory scanned twice: %v", err)
	}

	once, err := collectRoutes([]string{dir})
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}
	if len(routes) != len(once) {
		t.Errorf("scanning %s twice yielded %d routes, want %d — duplicates are not deduped",
			dir, len(routes), len(once))
	}
}

func TestRenderRoutesListsEveryRoute(t *testing.T) {
	routes, err := collectRoutes(routeDirs)
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
