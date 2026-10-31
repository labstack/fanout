package ui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/gzip"
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
		panic("internal/ui/dist is missing index.html — run `just build` (it builds the host and MCP app before the Go binary)")
	}
	files, err := load(dist)
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		f, ok := files[name]
		if !ok && name == "robots.txt" {
			// A robots.txt shipped in the build wins: this default is for a
			// binary that has none, not an override of one that does.
			serveRobots(response)
			return
		}
		if !ok {
			if looksLikeAFile(name) {
				http.NotFound(response, request)
				return
			}
			f = files["index.html"]
		}
		f.serve(response, request)
	})
}

// asset is one embedded file with its validator and, made on first use, a
// gzip copy. Vite fingerprints every file under assets/, so those never change
// under a name and are cached for a year; anything else (index.html, the
// favicon) is revalidated on every use, so a new build is picked up by the
// next navigation.
type asset struct {
	name      string
	body      []byte
	etag      string
	immutable bool
	zip       func() []byte // nil when the format does not compress
}

func load(dist fs.FS) (map[string]*asset, error) {
	files := map[string]*asset{}
	err := fs.WalkDir(dist, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		f := &asset{name: name, body: body, etag: hex.EncodeToString(sum[:12]), immutable: strings.HasPrefix(name, "assets/")}
		if compressible[path.Ext(name)] {
			// Compressing the whole build at startup costs about a second of
			// CPU before the listener opens; each file is compressed once,
			// when it is first requested.
			f.zip = sync.OnceValue(func() []byte {
				var buf bytes.Buffer
				w, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
				_, _ = w.Write(body)
				_ = w.Close()
				return buf.Bytes()
			})
		}
		files[name] = f
		return nil
	})
	return files, err
}

// Fonts and images are compressed formats already.
var compressible = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true, ".txt": true, ".map": true}

func (f *asset) serve(response http.ResponseWriter, request *http.Request) {
	header := response.Header()
	if f.immutable {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "no-cache")
	}
	if ctype := mime.TypeByExtension(path.Ext(f.name)); ctype != "" {
		header.Set("Content-Type", ctype)
	}
	body, etag := f.body, f.etag
	if f.zip != nil {
		header.Add("Vary", "Accept-Encoding")
		if acceptsGzip(request.Header.Get("Accept-Encoding")) {
			// Each representation has its own validator, so a cache never
			// revalidates one encoding against the other.
			body, etag = f.zip(), f.etag+"-gzip"
			header.Set("Content-Encoding", "gzip")
		}
	}
	header.Set("ETag", `"`+etag+`"`)
	// ServeContent answers conditional, HEAD and range requests; with a zero
	// modification time it sends no Last-Modified, and the ETag decides.
	http.ServeContent(response, request, f.name, time.Time{}, bytes.NewReader(body))
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip: listed
// with a nonzero quality, or covered by a nonzero "*".
func acceptsGzip(header string) bool {
	accepted := false
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		if coding != "gzip" && coding != "*" {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		zero := strings.HasPrefix(q, "q=0") && strings.Trim(strings.TrimPrefix(q, "q="), "0.") == ""
		if coding == "gzip" {
			return !zero
		}
		accepted = !zero
	}
	return accepted
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
// Client routes have no extension (/dashboards/<id>, /chat/<id>) — both
// parameters are UUIDs — so the last segment carrying a dot is the signal. A
// route parameter that can hold a dot (a service name, an email, a filename)
// would arrive here as a 404 instead of the application, so a new route of
// that shape needs this to become an allow-list of static paths.
//
// A trailing dot is not an extension: "/checkout." is a client route with a
// typo, not a request for a file.
func looksLikeAFile(name string) bool {
	return path.Ext(name) != "" && !strings.HasSuffix(name, ".")
}
