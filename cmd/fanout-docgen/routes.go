package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/api"
)

// routeCount is set by collectRoutes so the summary line can report it.
var routeCount []api.RouteDoc

// collectRoutes finds every route the server registers and asks the middleware
// how it classifies each one.
//
// The two halves use deliberately different techniques, and the reason matters.
// The paths can only come from the source, because a route is an `e.GET("...")`
// call and nothing at runtime enumerates them. The authorization requirement
// must NOT come from the source: classifyRoute is a switch of prefix matches and
// method conditions, and a generator that re-implemented that reading would be a
// second authorization model, free to drift from the one that runs. So the path
// is parsed and the policy is asked.
//
// It scans several directories because the surface is registered in several
// places: the application handlers in internal/api, the agent runtime, and the
// operational, protocol and SPA routes wired up in cmd/fanout. Scanning only
// internal/api published a table that omitted /-/metrics, /debug/pprof/*, /mcp
// and every /api/agent route, while the page invited an operator to audit what
// an instance exposes (#188).
func collectRoutes(dirs []string) ([]api.RouteDoc, error) {
	type reg struct{ method, path string }
	var found []reg
	seen := map[reg]bool{}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}

		// Counted per directory rather than as growth of `found`, because a
		// route registered in two places would dedupe to nothing new and read
		// as a directory that registers none.
		registrations := 0

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
			}

			// Identifiers that are an *echo.Group parameter, mapped to the prefix
			// that group is mounted at, so a relative path can be completed.
			groupReceivers, err := groupParams(file, entry.Name())
			if err != nil {
				return nil, err
			}

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "GET", "POST", "PUT", "PATCH", "DELETE", "Any":
				default:
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				path, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}

				// Complete a group-relative path. groupParams has already refused
				// any group this generator cannot name, so an unresolved receiver
				// here is the root Echo and the path is already absolute.
				prefix, onGroup := "", false
				if recv, ok := sel.X.(*ast.Ident); ok {
					prefix, onGroup = groupReceivers[recv.Name]
				}
				switch {
				case onGroup:
					// `group.POST("", ...)` registers the group's own root, which
					// is a real route — POST /api/agent, the one that runs the
					// investigator, is registered exactly that way. Requiring a
					// leading slash here dropped it silently.
					path = prefix + path
				case !strings.HasPrefix(path, "/"):
					return true
				}

				registrations++
				r := reg{method: sel.Sel.Name, path: path}
				if !seen[r] {
					seen[r] = true
					found = append(found, r)
				}
				return true
			})
		}

		if registrations == 0 {
			return nil, fmt.Errorf(
				"found no route registrations under %s; has the registration style "+
					"changed, or did those routes move? A directory in the scan list "+
					"that registers nothing silently shrinks the published surface",
				dir,
			)
		}
	}

	docs := make([]api.RouteDoc, 0, len(found))
	for _, r := range found {
		doc, err := describe(r.method, r.path)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Path != docs[j].Path {
			return docs[i].Path < docs[j].Path
		}
		return docs[i].Method < docs[j].Method
	})
	routeCount = docs
	return docs, nil
}

// anyMethods are the methods checked before an `e.Any(...)` route is published
// as a single row.
//
// Echo v5 registers Any as a RouteAny sentinel that matches any method at all,
// so this is deliberately a subset: the six classifyRoute actually
// distinguishes. Checking those is what decides whether one row can state the
// requirement honestly, because a method the middleware does not distinguish
// cannot disagree with its neighbours.
var anyMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

// describe asks the middleware to classify one registration.
//
// A registered route the middleware does not classify is either unreachable or
// unprotected by accident. Neither is something to document quietly, so it is
// an error rather than an omitted row.
//
// `Any` needs every method to reach the same answer before the table can carry
// a single row for it. Both current Any routes, /mcp and /api/mcp, classify
// identically for every method by construction. If that stops being true,
// collapsing them into one row would state a requirement that is wrong for some
// verb, so this refuses rather than picking one.
func describe(method, path string) (api.RouteDoc, error) {
	if method != "Any" {
		doc, ok := api.DescribeRoute(method, path)
		if !ok {
			return api.RouteDoc{}, fmt.Errorf(
				"route %s %s is registered but the auth middleware does not classify it; "+
					"add a case to classifyRoute or remove the route",
				method, path,
			)
		}
		return doc, nil
	}

	var first api.RouteDoc
	for i, m := range anyMethods {
		doc, ok := api.DescribeRoute(m, path)
		if !ok {
			return api.RouteDoc{}, fmt.Errorf(
				"route Any %s is registered, so it answers %s as well, but the auth "+
					"middleware does not classify that pair; add a case to classifyRoute",
				path, m,
			)
		}
		if i == 0 {
			first = doc
			continue
		}
		if doc.Policy != first.Policy || doc.Capability != first.Capability {
			return api.RouteDoc{}, fmt.Errorf(
				"route Any %s classifies as %q for %s but %q for %s; it cannot be "+
					"published as one row without stating a requirement that is wrong "+
					"for one of them",
				path, first.Policy, anyMethods[0], doc.Policy, m,
			)
		}
	}
	first.Method = "Any"
	first.Path = path
	return first, nil
}

// requirement renders what a caller has to present, in a reader's terms rather
// than the middleware's.
func requirement(doc api.RouteDoc) string {
	switch doc.Policy {
	case api.RoutePolicyPublic:
		return "none"
	case api.RoutePolicyAuthenticated:
		return "any signed-in user"
	case api.RoutePolicyProtocol:
		return "protocol handshake"
	case api.RoutePolicyServiceCredential:
		return fmt.Sprintf("`%s`, or a service credential", doc.Capability)
	case api.RoutePolicyCapability:
		return fmt.Sprintf("`%s`", doc.Capability)
	default:
		return doc.Policy
	}
}

func renderRoutes(routes []api.RouteDoc) []byte {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("title: \"HTTP routes\"\n")
	b.WriteString("description: \"Every HTTP route the server registers and what guards each one. Mostly the browser client's backend rather than a public API.\"\n")
	b.WriteString("summary: \"Every route the server registers — application, operational, protocol and the SPA catch-all — with the authorization requirement for each, taken from the middleware that enforces them.\"\n")
	b.WriteString("read_when:\n")
	b.WriteString("  - \"You are auditing what an instance exposes and what guards each route.\"\n")
	b.WriteString("  - \"A request came back 401 or 403 and you want to know which capability it wanted.\"\n")
	b.WriteString("status: preview\n")
	b.WriteString("generated: true\n")
	b.WriteString("---\n\n")

	b.WriteString("{/* Generated by cmd/fanout-docgen from internal/api, internal/agent and cmd/fanout. Edit the generator, not this page. */}\n\n")

	b.WriteString("Every route below is served on `FANOUT_HTTP_ADDR` (`:7520` by default).\n")
	b.WriteString("Telemetry does not arrive here — OTLP has its own two listeners, described in\n")
	b.WriteString("[send your first telemetry](/start/send-telemetry).\n\n")

	b.WriteString(":::note[What this table is, and what it is not]\n")
	b.WriteString("Every route the binary registers, wherever it is registered — the application\n")
	b.WriteString("handlers, the agent runtime, the operational and protocol endpoints, and the\n")
	b.WriteString("SPA catch-all. A route the generator cannot get a classification for fails\n")
	b.WriteString("the build, so a new route cannot quietly go undocumented.\n\n")
	b.WriteString("It is the surface the binary *can* register, not the surface any particular\n")
	b.WriteString("instance exposes. Some of it is conditional, so check the instance before\n")
	b.WriteString("concluding a route is reachable — or that it is not:\n\n")
	b.WriteString("- `/debug/pprof/*` — only when `FANOUT_PPROF_ENABLED` is true, which is\n")
	b.WriteString("  **not** the default. Enabling it also turns on mutex and block sampling.\n")
	b.WriteString("- `/mcp`, `/api/mcp`, `/oauth/*` and `/.well-known/*` — only when\n")
	b.WriteString("  `FANOUT_MCP_ENABLED` is true, which **is** the default.\n")
	b.WriteString("- `/api/agent` and `/api/agent/*` — only when an AI provider key is\n")
	b.WriteString("  configured. Without one the investigator is not registered at all.\n\n")
	b.WriteString("Everything else is always registered. A path shown with `:name` or `*` is the\n")
	b.WriteString("pattern Echo matches on, not a literal URL.\n")
	b.WriteString(":::\n\n")

	b.WriteString(":::caution[Most of this is not a public API]\n")
	b.WriteString("The `/api/*` routes are the browser client's own backend. They are listed here\n")
	b.WriteString("so an operator can see the whole surface and what guards it — not as an\n")
	b.WriteString("interface to build against. They change with the client, without notice and\n")
	b.WriteString("without migration paths.\n\n")
	b.WriteString("The supported programmatic interface is **MCP**, at `/mcp` — see\n")
	b.WriteString("[MCP tools](/reference/mcp-tools). The stable non-MCP surfaces are OTLP\n")
	b.WriteString("ingest, and the operational endpoints in [endpoints](/reference/endpoints).\n\n")
	b.WriteString("Two exceptions worth knowing, because HTTP is currently the only way to reach\n")
	b.WriteString("them: alert rules (`/api/rules`, `/api/alerts`) and ingest-token rotation\n")
	b.WriteString("(`/api/settings/ingest`). Neither has a browser page yet.\n")
	b.WriteString(":::\n\n")

	b.WriteString("The **Requires** column is not a description of the rule. It is the answer the\n")
	b.WriteString("authorization middleware gives for that exact method and path, so a route\n")
	b.WriteString("cannot be documented as public while the server treats it otherwise.\n")
	b.WriteString("Capabilities map to roles in [roles](/reference/roles).\n\n")

	b.WriteString("| Method | Path | Requires |\n")
	b.WriteString("|---|---|---|\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.Method, r.Path, requirement(r))
	}

	b.WriteString("\n## What the requirements mean\n\n")
	b.WriteString("- **none** — reachable without a credential. The health endpoints are\n")
	b.WriteString("  deliberately here: a probe that needs a credential fails for the wrong\n")
	b.WriteString("  reason during an outage.\n")
	b.WriteString("- **any signed-in user** — a session, but no particular capability.\n")
	b.WriteString("- **protocol handshake** — the OAuth and MCP endpoints, which authenticate\n")
	b.WriteString("  as part of their own protocol rather than through the middleware.\n")
	b.WriteString("- **a named capability** — the caller's role must carry it.\n")
	b.WriteString("- **or a service credential** — additionally reachable without a browser\n")
	b.WriteString("  session, which is how a scraper reaches the metrics endpoint.\n\n")
	b.WriteString("A method of `Any` means the route answers every verb, and the middleware\n")
	b.WriteString("gives the same answer for all of them — where it would not, this page fails\n")
	b.WriteString("to build rather than pick one. `GET /*` is the single-page application:\n")
	b.WriteString("anything not matched above is served the client, which is why it is public.\n")

	return []byte(b.String())
}
