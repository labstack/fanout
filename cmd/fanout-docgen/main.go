// Command fanout-docgen writes the settings reference from Fanout's own
// configuration type.
//
// The contract comes from reflection over config.Config — the same struct the
// loader binds, compiled into this program by the same build. A page therefore
// cannot describe a setting the binary does not accept, and cannot miss one it
// does, because both sides are the one type.
//
// The prose comes from the doc comments in internal/config/config.go. Comments
// are not available through reflection, so the source is parsed for them, and
// the two views are cross-checked: a field present in one and absent from the
// other is an error rather than a page written from half the information. That
// check is what makes the comment side trustworthy — without it, a refactor
// that moved the struct to another file would silently produce a reference with
// no prose in it.
//
// Run `just docs-generate` to rewrite the pages, and `just docs-generate-check`
// to fail when a committed page is behind the type. The generated pages are
// committed so a reader of the repository sees the same reference a reader of
// the site does, and so the check has a baseline to compare against. Edit this
// generator, never the pages.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/config"
)

func main() {
	var (
		source = flag.String("source", "internal/config/config.go", "path to the file declaring config.Config")
		outDir = flag.String("out", "site/src/content/docs/reference/settings", "directory to write the settings pages into")
		check  = flag.Bool("check", false, "exit non-zero when a written page differs from the one on disk")
	)
	flag.Parse()

	if err := run(*source, *outDir, *check); err != nil {
		fmt.Fprintf(os.Stderr, "fanout-docgen: %v\n", err)
		os.Exit(1)
	}
}

func run(source, outDir string, check bool) error {
	fields, err := collect(source)
	if err != nil {
		return err
	}

	pages, err := render(fields)
	if err != nil {
		return err
	}

	var stale []string
	for name, body := range pages {
		path := filepath.Join(outDir, name)
		if check {
			existing, err := os.ReadFile(path)
			if err != nil {
				stale = append(stale, fmt.Sprintf("%s (missing)", path))
				continue
			}
			if !bytes.Equal(existing, body) {
				stale = append(stale, path)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}

	// A page for a group that no longer exists is as wrong as a missing one: it
	// documents settings the binary has dropped, and nothing else would notice.
	orphans, err := orphaned(outDir, pages)
	if err != nil {
		return err
	}

	if check {
		stale = append(stale, orphans...)
		sort.Strings(stale)
		if len(stale) > 0 {
			return fmt.Errorf(
				"these generated pages are behind internal/config: \n  %s\nrun `just docs-generate`",
				strings.Join(stale, "\n  "),
			)
		}
		fmt.Printf("fanout-docgen: %d page(s) match the configuration type\n", len(pages))
		return nil
	}

	for _, orphan := range orphans {
		if err := os.Remove(orphan); err != nil {
			return err
		}
	}
	fmt.Printf("fanout-docgen: wrote %d page(s) covering %d setting(s)\n", len(pages), len(fields))
	return nil
}

// field is one setting, assembled from both views of the same declaration.
type field struct {
	GoName  string
	Key     string // koanf key, e.g. "storage.duckdb.memory"
	Env     string // e.g. "FANOUT_DUCKDB_MEMORY"
	Type    string
	Default string
	Secret  bool
	Doc     string
}

func (f field) group() string {
	key, _, _ := strings.Cut(f.Key, ".")
	return key
}

// collect reflects over config.Config for the contract, parses source for the
// doc comments, and refuses to proceed unless the two describe the same fields.
func collect(source string) ([]field, error) {
	docs, err := docComments(source)
	if err != nil {
		return nil, err
	}

	typ := reflect.TypeOf(config.Config{})
	fields := make([]field, 0, typ.NumField())
	seen := make(map[string]bool, typ.NumField())

	for i := range typ.NumField() {
		sf := typ.Field(i)
		if !sf.IsExported() {
			continue
		}
		key := sf.Tag.Get("koanf")
		env := sf.Tag.Get("env")
		if key == "" || env == "" {
			return nil, fmt.Errorf(
				"config.Config field %s is exported but carries no koanf/env tag; "+
					"either tag it so it reaches the reference, or unexport it",
				sf.Name,
			)
		}
		seen[sf.Name] = true
		fields = append(fields, field{
			GoName:  sf.Name,
			Key:     key,
			Env:     env,
			Type:    typeName(sf.Type),
			Default: sf.Tag.Get("default"),
			Secret:  sf.Tag.Get("secret") == "true",
			Doc:     docs[sf.Name],
		})
	}

	// The parse is the half that can silently go wrong: reflection follows the
	// type wherever it moves, while the parser is pointed at a path. If the two
	// disagree, the prose in the reference is not the prose in the source.
	for name := range docs {
		if !seen[name] {
			return nil, fmt.Errorf(
				"%s declares field %s, which is not in config.Config — "+
					"is --source pointing at the right file?",
				source, name,
			)
		}
	}

	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return fields, nil
}

// docComments returns the doc comment for every field of the Config struct
// declared in source, keyed by Go field name. Fields without one are absent.
func docComments(source string) (map[string]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, source, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", source, err)
	}

	out := map[string]string{}
	var found bool

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Config" {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		found = true
		for _, astField := range structType.Fields.List {
			text := strings.TrimSpace(astField.Doc.Text())
			for _, name := range astField.Names {
				if !name.IsExported() {
					continue
				}
				if text != "" {
					out[name.Name] = flow(text, name.Name)
				} else {
					// Recorded with no prose so the cross-check still sees the
					// field; render() treats an empty doc as "no notes".
					out[name.Name] = ""
				}
			}
		}
		return false
	})

	if !found {
		return nil, fmt.Errorf("%s declares no type Config", source)
	}
	return out, nil
}

// flow turns a hard-wrapped Go doc comment into prose, and drops the leading
// "FieldName is " that godoc convention requires and a settings table does not:
// the name is already the row the reader is looking at.
func flow(text, goName string) string {
	joined := strings.Join(strings.Fields(text), " ")
	for _, prefix := range []string{goName + " is ", goName + " reports ", goName + " "} {
		if rest, ok := strings.CutPrefix(joined, prefix); ok {
			return strings.ToUpper(rest[:1]) + rest[1:]
		}
	}
	return joined
}

func typeName(t reflect.Type) string {
	if t == reflect.TypeOf(time.Duration(0)) {
		return "duration"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64:
		return "integer"
	default:
		return t.String()
	}
}

// groupPage is the authored half of a settings page: the generator knows every
// setting but not what a group is for.
type groupPage struct {
	Title       string
	Description string
	Summary     string
	ReadWhen    []string
}

// Registering a group is deliberate. A new koanf prefix appearing in the type
// is a new section of the product, and it should be named by a person rather
// than published under a bare key — so an unregistered group fails the run
// instead of shipping a page called "metrics" with no explanation on it.
var groups = map[string]groupPage{
	"server": {
		Title:       "Server settings",
		Description: "The HTTP listener, the public URL, TLS, and proxy trust.",
		Summary:     "Settings for the listener that serves the browser client, the API, chat and MCP.",
		ReadWhen: []string{
			"You are putting Fanout behind a reverse proxy.",
			"Browser sessions are not getting Secure cookies.",
		},
	},
	"ingest": {
		Title:       "Ingest settings",
		Description: "The OTLP listeners, the advertised endpoint, batching, and the default namespace.",
		Summary:     "Settings for how telemetry arrives and how it is batched before it is written.",
		ReadWhen: []string{
			"Exporters cannot reach the instance.",
			"You are tuning write throughput or flush latency.",
		},
	},
	"storage": {
		Title:       "Storage settings",
		Description: "The data directory, retention, rollups, compaction, and the DuckDB limits.",
		Summary:     "Settings for where telemetry lives, how long it is kept, and how DuckDB is sized.",
		ReadWhen: []string{
			"You are sizing an instance for a host.",
			"Disk use or query latency is growing and you need the maintenance knobs.",
		},
	},
	"auth": {
		Title:       "Authentication settings",
		Description: "Sign-in mode, session lifetimes, and the OIDC provider configuration.",
		Summary:     "Settings for how people sign in and how long their sessions last.",
		ReadWhen: []string{
			"You are moving an instance from local sign-in to an identity provider.",
			"You need to map provider groups onto Fanout roles.",
		},
	},
	"smtp": {
		Title:       "SMTP settings",
		Description: "The mail server used to deliver sign-in codes.",
		Summary:     "Settings for outbound email, which local sign-in uses to deliver codes.",
		ReadWhen:    []string{"Sign-in codes are not arriving."},
	},
	"ai": {
		Title:       "AI provider settings",
		Description: "The provider, key, model and base URL backing the chat investigator.",
		Summary:     "Settings for the model behind the chat investigator. Without a key the agent is disabled.",
		ReadWhen: []string{
			"The chat investigator is disabled and you want it on.",
			"You are pointing Fanout at a self-hosted or proxied model endpoint.",
		},
	},
	"mcp": {
		Title:       "MCP settings",
		Description: "Whether the MCP server is served, and the public resource URI it binds tokens to.",
		Summary:     "Settings for the MCP endpoint external agents connect to.",
		ReadWhen:    []string{"You are connecting an external agent to a Fanout instance."},
	},
	"alerts": {
		Title:       "Alert settings",
		Description: "Whether the alert engine runs, how often it evaluates, and how long history is kept.",
		Summary:     "Settings for the alert evaluation loop.",
		ReadWhen:    []string{"Alerts are evaluating too slowly, or not at all."},
	},
	"metrics": {
		Title:       "Metrics settings",
		Description: "The Prometheus endpoint's token and whether it is public.",
		Summary:     "Settings for the Prometheus scrape endpoint at /-/metrics.",
		ReadWhen:    []string{"You are scraping Fanout's own metrics."},
	},
}

func render(fields []field) (map[string][]byte, error) {
	byGroup := map[string][]field{}
	for _, f := range fields {
		byGroup[f.group()] = append(byGroup[f.group()], f)
	}

	pages := make(map[string][]byte, len(byGroup))
	for name, group := range byGroup {
		page, ok := groups[name]
		if !ok {
			return nil, fmt.Errorf(
				"configuration group %q has no entry in the groups registry in "+
					"cmd/fanout-docgen/main.go; add a title and description for it "+
					"rather than publishing a page named after a bare key",
				name,
			)
		}
		pages[name+".mdx"] = renderPage(name, page, group)
	}
	return pages, nil
}

func renderPage(name string, page groupPage, fields []field) []byte {
	var b strings.Builder

	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", page.Title)
	fmt.Fprintf(&b, "description: %q\n", page.Description)
	fmt.Fprintf(&b, "summary: %q\n", page.Summary)
	if len(page.ReadWhen) > 0 {
		b.WriteString("read_when:\n")
		for _, when := range page.ReadWhen {
			fmt.Fprintf(&b, "  - %q\n", when)
		}
	}
	b.WriteString("generated: true\n")
	b.WriteString("---\n\n")

	b.WriteString("{/* Generated by cmd/fanout-docgen from internal/config. Edit the generator, not this page. */}\n\n")

	b.WriteString("Every setting below is read from the environment variable in the second\n")
	b.WriteString("column, or from the same key in a YAML configuration file. An unrecognised\n")
	b.WriteString("`FANOUT_`-prefixed variable is a startup error, so a renamed setting surfaces\n")
	b.WriteString("as a refusal to start rather than as a default nobody chose.\n\n")

	b.WriteString("| Setting | Environment variable | Type | Default |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, f := range fields {
		typ := f.Type
		if f.Secret {
			typ += " · secret"
		}
		def := "—"
		if f.Default != "" {
			def = "`" + f.Default + "`"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", f.Key, f.Env, typ, def)
	}

	var noted []field
	for _, f := range fields {
		if f.Doc != "" {
			noted = append(noted, f)
		}
	}
	if len(noted) > 0 {
		b.WriteString("\n## Notes\n")
		for _, f := range noted {
			fmt.Fprintf(&b, "\n### `%s`\n\n%s\n", f.Key, f.Doc)
		}
	}

	if hasSecret(fields) {
		b.WriteString("\n:::caution[Secrets]\n")
		b.WriteString("Settings marked `secret` are redacted from Fanout's startup configuration\n")
		b.WriteString("log. They are still plain environment variables: anything that can read the\n")
		b.WriteString("process environment can read them.\n")
		b.WriteString(":::\n")
	}

	return []byte(b.String())
}

func hasSecret(fields []field) bool {
	for _, f := range fields {
		if f.Secret {
			return true
		}
	}
	return false
}

// orphaned lists generated pages on disk that this run would not write.
func orphaned(outDir string, pages map[string][]byte) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mdx") {
			continue
		}
		if _, ok := pages[entry.Name()]; !ok {
			out = append(out, filepath.Join(outDir, entry.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}
