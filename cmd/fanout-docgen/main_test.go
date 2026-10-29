package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/fanql"
)

const configSource = "../../internal/config/config.go"

func TestDocgenWritesFanQLReference(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "reference")
	if err := run(configSource, "../../internal/alert/types.go", strings.Join(routeDirs, ","), out, filepath.Join(dir, ".env.example"), false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "fanql.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	if !strings.HasPrefix(page, "---\n") || !strings.Contains(page, "generated: true\n") || !strings.Contains(page, "summary: ") {
		t.Fatal("missing site front matter")
	}
	_, content, ok := strings.Cut(page, "\n---\n\n")
	if !ok || content != fanql.Reference() {
		t.Fatalf("page content differs from the API/MCP reference: %q", page)
	}
}

// The generator's value is that a page cannot describe a setting the binary
// does not accept. That holds only while reflection and the source parse agree,
// so the cross-check is the thing worth testing.
func TestCollectCrossChecksReflectionAgainstSource(t *testing.T) {
	fields, err := collect(configSource)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(fields) == 0 {
		t.Fatal("collected no settings; the configuration type is never empty")
	}

	for _, f := range fields {
		if f.Group == "" || f.Env == "" {
			t.Errorf("%s: group and env tags are both required", f.GoName)
		}
		if f.Type == "" {
			t.Errorf("%s: no rendered type", f.GoName)
		}
		if f.Doc == "" {
			t.Errorf("%s: setting purpose must be documented in the authoritative schema", f.GoName)
		}
	}
}

func TestEnvironmentTemplateMatchesSchema(t *testing.T) {
	fields, err := collect(configSource)
	if err != nil {
		t.Fatal(err)
	}
	body := renderEnvironment(fields)
	written, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(body) {
		t.Error(".env.example is stale; run just docs-generate")
	}
	var env []string
	seen := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			t.Fatalf("template must contain only comments: %q", line)
		}
		entry := strings.TrimPrefix(line, "# ")
		if !strings.HasSuffix(entry, "=") {
			continue
		}
		name, value, ok := strings.Cut(entry, "=")
		if _, duplicate := seen[name]; !ok || duplicate {
			t.Fatal("invalid or duplicate template entry")
		}
		seen[name] = value
	}
	if len(seen) != len(fields) {
		t.Fatalf("template has %d settings, schema has %d", len(seen), len(fields))
	}
	for _, f := range fields {
		if f.Secret && !strings.Contains(string(body), f.Env+"_FILE") {
			t.Errorf("missing file form for %s", f.Env)
		}
		value, exists := seen[f.Env]
		if !exists || value != "" {
			t.Errorf("template must leave %s empty so it preserves defaults", f.Env)
		}
		if f.Default != "" && !f.Secret && !strings.Contains(string(body), "# Default: "+f.Default+"\n# "+f.Env+"=\n") {
			t.Errorf("template must document the default for %s", f.Env)
		}
	}
	// A copied template contributes no overrides to the image environment.
	env = append(env, "FANOUT_ADDR=:7520", "FANOUT_DATA_DIR=/var/lib/fanout/data", "FANOUT_AUTH_CODE_SECRET="+strings.Repeat("x", 32))
	cfg, err := config.Load(config.LoadOptions{Environ: env})
	if err != nil {
		t.Fatalf("template rejected by the runtime loader: %v", err)
	}
	if cfg.Addr != ":7520" || cfg.DataDir != "/var/lib/fanout/data" {
		t.Fatal("template must preserve image environment values")
	}
}

func TestCollectRejectsASourceThatIsNotTheConfigType(t *testing.T) {
	// Pointing the parser at the wrong file is the failure the cross-check
	// exists to catch: reflection still finds every field, so without it the
	// reference would publish with no prose and no error.
	if _, err := collect("main.go"); err == nil {
		t.Fatal("collect accepted a file that declares no type Config")
	}
}

func TestEverySettingGroupIsRegistered(t *testing.T) {
	fields, err := collect(configSource)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, f := range fields {
		if _, ok := groups[f.group()]; !ok {
			t.Errorf(
				"setting %q is in group %q, which has no entry in the groups registry; "+
					"add a title and description for it",
				f.Env, f.group(),
			)
		}
	}
}

func TestRenderProducesOnePageWithFrontmatterPerGroup(t *testing.T) {
	fields, err := collect(configSource)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	pages, err := render(fields)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("rendered no pages")
	}

	for name, body := range pages {
		text := string(body)
		if strings.Contains(text, "Fanout also recognizes") != (name == "settings/ai.mdx") {
			t.Errorf("%s: vendor-variable paragraph belongs only on the AI settings page", name)
		}
		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s: does not open with frontmatter", name)
		}
		if !strings.Contains(text, "generated: true") {
			t.Errorf("%s: missing the generated marker, so the site cannot tell it apart from an authored page", name)
		}
		if !strings.Contains(text, "| Environment variable | Type | Default |") {
			t.Errorf("%s: missing the settings table", name)
		}
		// Every page is indexed in llms.txt, which fails the site build when a
		// page carries no summary. Catch that here rather than three steps later.
		if !strings.Contains(text, "summary: ") {
			t.Errorf("%s: no summary, which llms.txt refuses to index", name)
		}
	}
}

func TestSecretSettingsCarryTheirWarning(t *testing.T) {
	fields, err := collect(configSource)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	pages, err := render(fields)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	secretGroups := map[string]bool{}
	for _, f := range fields {
		if f.Secret {
			secretGroups[f.group()] = true
		}
	}
	if len(secretGroups) == 0 {
		t.Fatal("no setting is marked secret; the api key and smtp password are")
	}

	for group := range secretGroups {
		body := string(pages["settings/"+group+".mdx"])
		if !strings.Contains(body, "· secret") {
			t.Errorf("%s.mdx: holds a secret setting but does not mark it in the table", group)
		}
		if !strings.Contains(body, ":::caution[Secrets]") {
			t.Errorf("%s.mdx: holds a secret setting but carries no warning", group)
		}
	}
}

func TestFlowDropsTheGodocFieldNamePrefix(t *testing.T) {
	for _, tc := range []struct{ in, name, want string }{
		{"DuckDBMemory caps DuckDB's memory.", "DuckDBMemory", "Caps DuckDB's memory."},
		{"TLSEnabled reports whether a pair is set.", "TLSEnabled", "Whether a pair is set."},
		{"Wrapped\nover\nlines.", "Other", "Wrapped over lines."},
	} {
		if got := flow(tc.in, tc.name); got != tc.want {
			t.Errorf("flow(%q, %q) = %q, want %q", tc.in, tc.name, got, tc.want)
		}
	}
}

func TestOrphanedFindsGeneratedPagesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}

	generated := "---\ntitle: x\ngenerated: true\n---\n"
	authored := "---\ntitle: x\n---\n"

	files := map[string]string{
		"settings/server.mdx": generated, // still generated, keep
		"settings/gone.mdx":   generated, // no longer generated, remove
		"roles.mdx":           authored,  // hand-written, never touch
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := orphaned(dir, map[string][]byte{"settings/server.mdx": nil})
	if err != nil {
		t.Fatalf("orphaned: %v", err)
	}

	want := filepath.Join(dir, "settings/gone.mdx")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("orphaned = %v, want just %q", got, want)
	}

	// The one that matters: generated pages now sit beside authored ones, and
	// orphan detection deletes. An authored page must never be a candidate.
	for _, path := range got {
		if strings.HasSuffix(path, "roles.mdx") {
			t.Error("orphaned would delete a hand-written page")
		}
	}
}
