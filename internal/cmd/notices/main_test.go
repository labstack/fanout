package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectNPMUsesOnlyInstalledProductionGraph(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ui", "host")
	writeTestFile(t, filepath.Join(workspace, "package.json"), `{
  "name": "app",
  "version": "1.0.0",
  "dependencies": {"prod": "1.0.0"},
  "devDependencies": {"must-not-be-installed": "1.0.0"}
}`)
	prod := filepath.Join(workspace, "node_modules", "prod")
	writeTestFile(t, filepath.Join(prod, "package.json"), `{
  "name": "prod",
  "version": "1.0.0",
  "license": "MIT",
  "optionalDependencies": {"other-platform-only": "1.0.0"}
}`)
	writeTestFile(t, filepath.Join(prod, "LICENSE"), "test license\r\n")

	all := map[string]component{}
	if err := collectNPM(root, workspace, all); err != nil {
		t.Fatalf("collectNPM: %v", err)
	}
	item, ok := all["npm: prod 1.0.0"]
	if !ok || len(all) != 1 {
		t.Fatalf("components = %#v, want only prod", all)
	}
	if item.license != "MIT" || len(item.documents) != 1 || item.documents[0].text != "test license\n" {
		t.Fatalf("component = %#v", item)
	}
}

func TestRenderDeduplicatesLicenseTexts(t *testing.T) {
	generated := string(render([]component{
		{id: "Go: example/a v1.0.0", documents: []document{{name: "LICENSE", text: "same terms\n"}}},
		{id: "npm: b 1.0.0", license: "MIT", documents: []document{{name: "LICENSE", text: "same terms\n"}}},
	}))
	if strings.Count(generated, "same terms") != 1 {
		t.Fatalf("license text was not deduplicated:\n%s", generated)
	}
	for _, want := range []string{"Go: example/a v1.0.0 / LICENSE", "npm: b 1.0.0 / LICENSE"} {
		if !strings.Contains(generated, want) {
			t.Fatalf("generated notices do not contain %q", want)
		}
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
