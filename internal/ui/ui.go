package ui

import (
	"embed"
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
		if _, err := fs.Stat(dist, name); err != nil {
			clone := request.Clone(request.Context())
			clone.URL.Path = "/"
			files.ServeHTTP(response, clone)
			return
		}
		files.ServeHTTP(response, request)
	})
}
