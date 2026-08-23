package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const configSource = "../../internal/config/config.go"

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
		if f.Key == "" || f.Env == "" {
			t.Errorf("%s: koanf and env tags are both required", f.GoName)
		}
		if !strings.HasPrefix(f.Env, "FANOUT_") {
			t.Errorf("%s: environment variable %q lacks the FANOUT_ prefix the loader requires", f.GoName, f.Env)
		}
		if f.Type == "" {
			t.Errorf("%s: no rendered type", f.GoName)
		}
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
				f.Key, f.group(),
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
		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s: does not open with frontmatter", name)
		}
		if !strings.Contains(text, "generated: true") {
			t.Errorf("%s: missing the generated marker, so the site cannot tell it apart from an authored page", name)
		}
		if !strings.Contains(text, "| Setting | Environment variable | Type | Default |") {
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
