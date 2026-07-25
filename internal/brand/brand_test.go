package brand

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMarkSVGUsesCanonicalFaviconPaths(t *testing.T) {
	favicon, err := os.ReadFile(filepath.Join("..", "..", "site", "public", "favicon.svg"))
	if err != nil {
		t.Fatalf("read canonical favicon: %v", err)
	}

	pathPattern := regexp.MustCompile(`(?s)<path\s+d="([^"]+)"`)
	paths := pathPattern.FindAllStringSubmatch(string(favicon), -1)
	if len(paths) == 0 {
		t.Fatal("canonical favicon does not contain any paths")
	}
	renderedPaths := pathPattern.FindAllStringSubmatch(MarkSVG, -1)
	rendered := make(map[string]struct{}, len(renderedPaths))
	for _, match := range renderedPaths {
		rendered[strings.Join(strings.Fields(match[1]), "")] = struct{}{}
	}
	for _, match := range paths {
		path := strings.Join(strings.Fields(match[1]), "")
		if _, ok := rendered[path]; !ok {
			t.Errorf("MarkSVG is missing canonical favicon path %q", path)
		}
	}
}
