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
