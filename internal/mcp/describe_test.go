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
			// `any` would read as a deliberate "accepts anything" rather than
			// "the generator could not tell", so it is refused upstream. Asserted
			// here too, because the difference matters to whoever calls the tool.
			if input.Type == "any" {
				t.Errorf("%s input %q published as `any`", doc.Name, input.Name)
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

	// Compared exactly, not as a subset: a field added to QueryInput would
	// otherwise pass while the page's claim that these two share one scope went
	// stale. And each tool must actually be seen — filtering by name and
	// asserting nothing when the filter matches nothing is a test that renaming
	// a tool would silently switch off.
	want := map[string]string{"window": "string", "namespace": "string", "limit": "integer"}

	for _, name := range []string{"observability_overview", "service_topology"} {
		var doc ToolDoc
		var found bool
		for _, candidate := range docs {
			if candidate.Name == name {
				doc, found = candidate, true
				break
			}
		}
		if !found {
			t.Errorf("%s is not among the served tools; was it renamed?", name)
			continue
		}

		got := map[string]string{}
		for _, input := range doc.Inputs {
			got[input.Name] = input.Type
		}
		if len(got) != len(want) {
			t.Errorf("%s takes %d inputs (%v), want exactly %v", name, len(got), got, want)
		}
		for input, typ := range want {
			if got[input] != typ {
				t.Errorf("%s input %q is %q, want %q", name, input, got[input], typ)
			}
		}
	}
}

// A schema the generator cannot reduce to a type must fail rather than publish
// `any`. The SDK emits $ref, anyOf or a bare enum for shapes like a pointer or
// an interface, and --check would not catch the result: a page carrying a wrong
// type is self-consistent with the generator that wrote it.
func TestSchemaTypeRefusesAnUntypedProperty(t *testing.T) {
	for name, property := range map[string]map[string]any{
		"a $ref":        {"$ref": "#/$defs/State"},
		"an anyOf":      {"anyOf": []any{map[string]any{"type": "string"}}},
		"a bare enum":   {"enum": []any{"a", "b"}},
		"only null":     {"type": []any{"null"}},
		"an empty type": {"type": ""},
	} {
		if got, err := schemaType(property); err == nil {
			t.Errorf("%s was accepted and published as %q", name, got)
		}
	}
}

func TestSchemaTypeRendersUnionsWithoutNull(t *testing.T) {
	got, err := schemaType(map[string]any{"type": []any{"null", "array"}})
	if err != nil {
		t.Fatalf("nullable array refused: %v", err)
	}
	if got != "array" {
		t.Errorf("nullable array rendered %q, want array", got)
	}

	got, err = schemaType(map[string]any{"type": []any{"string", "integer"}})
	if err != nil {
		t.Fatalf("union refused: %v", err)
	}
	if got != "string or integer" {
		t.Errorf("union rendered %q, want \"string or integer\"", got)
	}
}
