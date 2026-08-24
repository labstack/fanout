package mcp

import (
	"context"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
)

// DescribeTools must agree with what a real client sees, because that agreement
// is the only reason to generate the page from it rather than write it.
func TestDescribeToolsMatchesWhatAClientIsServed(t *testing.T) {
	docs, err := DescribeTools(context.Background())
	if err != nil {
		t.Fatalf("DescribeTools: %v", err)
	}

	server := New(nil, dashboard.New(nil), "test")
	session := connectTestClient(t, server, nil)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(docs) != len(listed.Tools) {
		t.Fatalf("DescribeTools reported %d tools, a client is served %d", len(docs), len(listed.Tools))
	}

	served := map[string]string{}
	for _, tool := range listed.Tools {
		served[tool.Name] = tool.Description
	}
	for _, doc := range docs {
		description, ok := served[doc.Name]
		if !ok {
			t.Errorf("DescribeTools reports %q, which no client is served", doc.Name)
			continue
		}
		if doc.Description != description {
			t.Errorf("%s description differs from the one served", doc.Name)
		}
	}
}

// The dashboard tools register only when a dashboard service is present. If
// DescribeTools ever passed nil, the reference would silently lose four tools —
// two of which mutate — while still reading as complete.
func TestDescribeToolsIncludesTheDashboardTools(t *testing.T) {
	docs, err := DescribeTools(context.Background())
	if err != nil {
		t.Fatalf("DescribeTools: %v", err)
	}

	found := map[string]ToolDoc{}
	for _, doc := range docs {
		found[doc.Name] = doc
	}

	for _, name := range []string{"dashboard_list", "dashboard_get", "dashboard_create", "dashboard_update"} {
		if _, ok := found[name]; !ok {
			t.Errorf("%s is registered but absent from DescribeTools", name)
		}
	}

	// dashboard_update replaces rather than merges, and that is the single most
	// important fact on the page.
	update, ok := found["dashboard_update"]
	if !ok {
		t.Fatal("dashboard_update missing")
	}
	if update.ReadOnly {
		t.Error("dashboard_update reported read-only")
	}
	if !update.Destructive {
		t.Error("dashboard_update reported non-destructive; it replaces a dashboard's design")
	}
}

// Every tool must arrive with the facts the page is built from. A blank cell
// reads as "nothing to say" rather than "not generated".
func TestDescribeToolsReportsCompleteMetadata(t *testing.T) {
	docs, err := DescribeTools(context.Background())
	if err != nil {
		t.Fatalf("DescribeTools: %v", err)
	}

	for _, doc := range docs {
		if doc.Name == "" || doc.Title == "" || doc.Description == "" {
			t.Errorf("%+v: incomplete metadata", doc)
		}
		if doc.OpenWorld {
			t.Errorf("%s reports open-world; every Fanout tool is closed-world", doc.Name)
		}
		for _, input := range doc.Inputs {
			if input.Type == "" {
				t.Errorf("%s input %q has no type", doc.Name, input.Name)
			}
		}
	}
}

// The observability tools share one scope input, and the page says so. If the
// shared struct gained or lost a field, the claim would go stale silently.
func TestDescribeToolsReportsTheSharedObservabilityScope(t *testing.T) {
	docs, err := DescribeTools(context.Background())
	if err != nil {
		t.Fatalf("DescribeTools: %v", err)
	}

	want := map[string]string{"window": "string", "namespace": "string", "limit": "integer"}
	for _, doc := range docs {
		if doc.Name != "observability_overview" && doc.Name != "service_topology" {
			continue
		}
		got := map[string]string{}
		for _, input := range doc.Inputs {
			got[input.Name] = input.Type
		}
		for name, typ := range want {
			if got[name] != typ {
				t.Errorf("%s input %q is %q, want %q", doc.Name, name, got[name], typ)
			}
		}
	}
}
