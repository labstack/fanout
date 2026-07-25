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
	if len(renderedPaths) != len(paths) {
		t.Fatalf("MarkSVG has %d paths, canonical favicon has %d", len(renderedPaths), len(paths))
	}
	canonical := make(map[string]struct{}, len(paths))
	for _, match := range paths {
		canonical[strings.Join(strings.Fields(match[1]), "")] = struct{}{}
	}
	rendered := make(map[string]struct{}, len(renderedPaths))
	for _, match := range renderedPaths {
		rendered[strings.Join(strings.Fields(match[1]), "")] = struct{}{}
	}
	for path := range canonical {
		if _, ok := rendered[path]; !ok {
			t.Errorf("MarkSVG is missing canonical favicon path %q", path)
		}
	}
	for path := range rendered {
		if _, ok := canonical[path]; !ok {
			t.Errorf("MarkSVG contains non-canonical path %q", path)
		}
	}
}
