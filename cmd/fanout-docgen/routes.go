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

// collectRoutes finds every route registered under apiDir and asks the
// middleware how it classifies each one.
//
// The two halves use deliberately different techniques, and the reason matters.
// The paths can only come from the source, because a route is an `e.GET("...")`
// call and nothing at runtime enumerates them. The authorization requirement
// must NOT come from the source: classifyRoute is a switch of prefix matches and
// method conditions, and a generator that re-implemented that reading would be a
// second authorization model, free to drift from the one that runs. So the path
// is parsed and the policy is asked.
func collectRoutes(apiDir string) ([]api.RouteDoc, error) {
	entries, err := os.ReadDir(apiDir)
	if err != nil {
		return nil, err
	}

	type reg struct{ method, path string }
	var found []reg
	seen := map[reg]bool{}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(apiDir, entry.Name()), nil, 0)
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
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			path, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.HasPrefix(path, "/") {
				return true
			}
			// Complete a group-relative path. groupParams has already refused
			// any group this generator cannot name, so an unresolved receiver
			// here is the root Echo and the path is already absolute.
			if recv, ok := sel.X.(*ast.Ident); ok {
				if prefix, isGroup := groupReceivers[recv.Name]; isGroup {
					path = prefix + path
				}
			}
			r := reg{method: sel.Sel.Name, path: path}
			if !seen[r] {
				seen[r] = true
				found = append(found, r)
			}
			return true
		})
	}

	if len(found) == 0 {
		return nil, fmt.Errorf(
			"found no route registrations under %s; has the registration style changed?",
			apiDir,
		)
	}

	docs := make([]api.RouteDoc, 0, len(found))
	for _, r := range found {
		doc, ok := api.DescribeRoute(r.method, r.path)
		if !ok {
			// A registered route the middleware does not classify is either
			// unreachable or unprotected by accident. Neither is something to
			// document quietly.
			return nil, fmt.Errorf(
				"route %s %s is registered but the auth middleware does not classify it; "+
					"add a case to classifyRoute or remove the route",
				r.method, r.path,
			)
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
	b.WriteString("description: \"The HTTP routes registered in internal/api and what guards each one. Mostly the browser client's backend rather than a public API.\"\n")
	b.WriteString("summary: \"The application routes and their authorization requirements, taken from the middleware that enforces them. Does not yet cover the operational and protocol routes registered outside internal/api.\"\n")
	b.WriteString("read_when:\n")
	b.WriteString("  - \"You are auditing what an instance exposes and what guards each route.\"\n")
	b.WriteString("  - \"A request came back 401 or 403 and you want to know which capability it wanted.\"\n")
	b.WriteString("status: preview\n")
	b.WriteString("generated: true\n")
	b.WriteString("---\n\n")

	b.WriteString("{/* Generated by cmd/fanout-docgen from internal/api. Edit the generator, not this page. */}\n\n")

	b.WriteString("Every route below is served on `FANOUT_HTTP_ADDR` (`:7520` by default).\n")
	b.WriteString("Telemetry does not arrive here — OTLP has its own two listeners, described in\n")
	b.WriteString("[send your first telemetry](/start/send-telemetry).\n\n")

	b.WriteString(":::note[Not the whole surface yet]\n")
	b.WriteString("This covers the routes registered in `internal/api`. The operational and\n")
	b.WriteString("protocol routes — `/-/metrics`, `/debug/pprof/*`, `/mcp` and `/api/mcp` — are\n")
	b.WriteString("registered elsewhere and are not in this table yet; they are described in\n")
	b.WriteString("[endpoints](/reference/endpoints). Tracked in\n")
	b.WriteString("[#188](https://github.com/labstack/fanout/issues/188).\n")
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
	b.WriteString("  session, which is how a scraper reaches the metrics endpoint.\n")

	return []byte(b.String())
}
