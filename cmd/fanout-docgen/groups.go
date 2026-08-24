package main

import (
	"fmt"
	"go/ast"
)

// groupPrefixes maps a function that registers onto an *echo.Group to the
// prefix that group is mounted at.
//
// Echo group registrations carry relative paths: `group.GET("/overview")` in a
// handler whose group was created as `e.Group("/api/observability", ...)`.
// Reading the registration alone yields `/overview`, which classifyRoute's SPA
// catch-all then reports as public — so the page published five telemetry
// endpoints as requiring no credential, which is the precise failure its own
// prose promises cannot happen. The prefix is therefore stated here rather than
// inferred from a call site in another package.
//
// Keyed by the registering function, so a handler that moves file or a second
// one mounted elsewhere is a deliberate edit. An unrecognised group
// registration is an error, never a guess.
var groupPrefixes = map[string]string{
	"ObservabilityHandler.Register": "/api/observability",
	"Runtime.Register":              "/api/agent",
}

// groupParams finds every `*echo.Group` parameter in a file and returns the
// prefix each one is mounted at, keyed by the parameter's identifier.
//
// A function taking an *echo.Group registers relative paths, and this generator
// cannot see the call site that created the group — it is in another package.
// Rather than guess, an unregistered group is an error: publishing a guarded
// route as requiring no credential is worse than failing the build.
func groupParams(file *ast.File, filename string) (map[string]string, error) {
	out := map[string]string{}
	var failure error

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Type.Params == nil {
			return true
		}

		for _, param := range fn.Type.Params.List {
			if !isEchoGroup(param.Type) {
				continue
			}

			name := qualifiedName(fn)
			prefix, known := groupPrefixes[name]
			if !known {
				failure = fmt.Errorf(
					"%s: %s registers routes on an *echo.Group, whose paths are relative to a "+
						"prefix declared at its call site in another package. Add %q to "+
						"groupPrefixes in cmd/fanout-docgen/groups.go with the prefix it is "+
						"mounted at — without it those routes publish as requiring no credential",
					filename, name, name,
				)
				return false
			}
			for _, ident := range param.Names {
				out[ident.Name] = prefix
			}
		}
		return true
	})

	return out, failure
}

func isEchoGroup(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "echo" && sel.Sel.Name == "Group"
}

// qualifiedName renders a function as `Receiver.Method`, or its bare name when
// it has no receiver, so the registry key survives a rename of either half
// being noticed rather than silently matching something else.
func qualifiedName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch recv := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := recv.X.(*ast.Ident); ok {
			return id.Name + "." + fn.Name.Name
		}
	case *ast.Ident:
		return recv.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
