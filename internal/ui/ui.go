package ui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist contains the Vite production build. JavaScript executes in the browser;
// the Fanout process remains a single Go runtime.
//
//go:embed dist
var embedded embed.FS

func Handler() http.Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	// Fail at startup, not with blank pages at runtime, when the binary was
	// built without the frontend assets.
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		panic("internal/ui/dist is missing index.html — run `just build` (it builds ui/apps and ui/host before the Go binary)")
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		if name == "robots.txt" {
			serveRobots(response)
			return
		}
		if _, err := fs.Stat(dist, name); err != nil {
			if looksLikeAFile(name) {
				http.NotFound(response, request)
				return
			}
			clone := request.Clone(request.Context())
			clone.URL.Path = "/"
			files.ServeHTTP(response, clone)
			return
		}
		files.ServeHTTP(response, request)
	})
}

// robots is served rather than embedded because it is a fact about the
// application, not part of the build: every route behind it needs a session,
// so there is nothing here for a crawler to index, and a public demo should
// say so instead of answering /robots.txt with the application shell.
const robots = "User-agent: *\nDisallow: /\n"

func serveRobots(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(response, robots)
}

// looksLikeAFile reports whether a missing path was asking for an asset rather
// than a client route. The single-page fallback answered every unknown path
// with the application shell and a 200 — including /favicon.ico, a mistyped
// bundle and every probe a scanner makes — so a missing asset arrived at the
// browser as HTML that parsed as neither script nor image, and nothing in the
// response said it was missing.
//
// Client routes have no extension (/dashboards/<id>, /chat/<id>), so the last
// segment carrying a dot is the signal.
func looksLikeAFile(name string) bool {
	return path.Ext(name) != ""
}
