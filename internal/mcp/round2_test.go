package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestRound2DashboardScopeDocsMatchCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../site/src/content/docs/guides/connect-over-mcp.mdx")
	if err != nil {
		t.Fatal(err)
	}
	var scope string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "| `dashboard:manage`") {
			scope = line
		}
	}
	if scope == "" || strings.Contains(strings.ToLower(scope), "delete") {
		t.Fatalf("incorrect scope: %s", scope)
	}
	for _, tool := range dashboardTools {
		if !strings.Contains(scope, "`"+tool.Name+"`") {
			t.Errorf("scope missing %s", tool.Name)
		}
	}
}
