package query

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
	"github.com/labstack/fanout/internal/telemetry/semconv"
)

func TestSchemaDiscoversColumnsAttributesAndValues(t *testing.T) {
	e := newFixtureExecutor(t)
	schema, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	spans := schema.Signals["spans"]
	if len(schema.Signals) != 3 || len(schema.SQLRelations) != 10 || len(schema.SQLRelations["resource_inventory"].Columns) != 6 {
		t.Fatalf("SQL relation discovery: %+v", schema)
	}
	if got := schema.SQLRelations["resource_inventory"]; got.Columns[3].SQLType != "VARIANT" || got.Columns[4].SQLType != "TIMESTAMPTZ_NS" {
		t.Fatalf("inventory types: %+v", got)
	}
	for _, name := range []string{"resource_inventory", "resource_rollup", "receiver_rollup", "receiver_points", "receiver_entity_rollup"} {
		if _, ok := schema.SQLRelations[name]; !ok {
			t.Fatalf("SQL relation %s missing", name)
		}
		d := Dashboard{Name: "Invalid structured summary", Panels: []Panel{{ID: "resources", Title: "Resources", Viz: "stat", Query: &Spec{From: name, Measures: []string{"count()"}}}}}
		if err := e.Validate(t.Context(), &d); err == nil {
			t.Fatalf("SQL-only relation %s accepted as a structured signal", name)
		}
	}
	if spans.TimeColumn != "start_time" {
		t.Fatalf("time column = %q", spans.TimeColumn)
	}
	keys := []string{}
	for _, a := range spans.Attributes {
		keys = append(keys, a.Scope+":"+a.Key+":"+a.Type)
	}
	if !slices.Contains(keys, "attributes:http.route:string") || !slices.Contains(keys, "attributes:customer.tier:string") {
		t.Fatalf("attributes = %v", keys)
	}
	indexed := map[string]bool{}
	for _, a := range spans.Attributes {
		if a.Indexed {
			indexed[a.Scope+":"+a.Key+":"+a.Type] = true
		}
	}
	if len(indexed) != len(semconv.Keys) || !indexed["attributes:http.route:string"] || !indexed["attributes:http.response.status_code:number"] || !indexed["resource:k8s.pod.name:string"] {
		t.Fatalf("indexed span keys = %v", indexed)
	}
	logKeys := 0
	for _, a := range schema.Signals["logs"].Attributes {
		if a.Indexed {
			logKeys++
			if a.Scope != "resource" {
				t.Fatalf("log attribute %s indexed", a.Key)
			}
		}
	}
	if logKeys != len(telemetry.Logs.Keys()) {
		t.Fatalf("indexed log keys = %d", logKeys)
	}
	var kinds []string
	for _, c := range spans.Columns {
		if c.Name == "kind" {
			for _, v := range c.Values {
				kinds = append(kinds, v.Value)
			}
		}
	}
	if !slices.Contains(kinds, "SPAN_KIND_SERVER") {
		t.Fatalf("kind values = %v", kinds)
	}
	if len(schema.Services) != 2 || schema.Services[0].Value != "checkout" {
		t.Fatalf("services = %+v", schema.Services)
	}
	if !slices.Contains(schema.Measures, "p95") || !slices.Contains(schema.Units, "ms") || !slices.Equal(schema.Buckets, BucketNames()) {
		t.Fatalf("measures %v units %v buckets %v", schema.Measures, schema.Units, schema.Buckets)
	}
	again, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil || again != schema {
		t.Fatal("schema was not cached")
	}
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "7d"}); err == nil {
		t.Fatal("window over 24h accepted")
	}
}

func TestSchemaRollupColumnsMatchEngine(t *testing.T) {
	_, db := metricFixture(t)
	for _, name := range []string{"service_rollup", "edge_rollup"} {
		rows, err := db.db().QueryContext(t.Context(), "SELECT * FROM "+name+" LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		catalog := SQLRelations()[name].Columns
		if len(columns) != len(catalog) {
			t.Fatalf("%s: engine has %d columns, catalog has %d", name, len(columns), len(catalog))
		}
		for i, column := range columns {
			if column.Name() != catalog[i].Name || column.DatabaseTypeName() != catalog[i].SQLType {
				t.Errorf("%s column %d: engine %s %s, catalog %+v", name, i, column.Name(), column.DatabaseTypeName(), catalog[i])
			}
		}
	}
}

func TestSchemaReturnsOperationalErrorsUnwrapped(t *testing.T) {
	e := newFixtureExecutor(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.Schema(ctx, SchemaRequest{Window: "1h"})
	if err == nil || !isOperational(err) {
		t.Fatalf("err = %v", err)
	}
	var problems Problems
	if errors.As(err, &problems) {
		t.Fatalf("operational error became Problems: %v", err)
	}
}

func TestSchemaCacheIsBounded(t *testing.T) {
	e := newFixtureExecutor(t)
	for i := range 70 {
		_, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Namespace: fmt.Sprintf("ns%d", i)})
		var problems Problems
		if !errors.As(err, &problems) || problems[0].Path != "namespace" {
			t.Fatalf("junk namespace: %v", err)
		}
	}
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "2h"}); err == nil {
		t.Fatal("unsupported window accepted")
	}
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Namespace: "shop"}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// The namespace listing and the one real key: junk took no entries.
	if len(e.schemas) != 2 {
		t.Fatalf("cache holds %d entries", len(e.schemas))
	}
}

// A standard key reads as its column's type whatever the map's sampled rows
// sent, so the schema reports no mixed types for it.
func TestSchemaReportsStandardKeysTyped(t *testing.T) {
	got := indexAttributes(telemetry.Spans, []AttributeSchema{
		{Key: "http.response.status_code", Scope: "attributes", Type: "string", Count: 9, Types: []string{"string", "number"}, Numbers: []Option{{Value: "200", Count: 4}}},
		{Key: "http.status_code", Scope: "attributes", Type: "string", Types: []string{"string", "number"}},
	})
	if a := got[0]; !a.Indexed || a.Type != "number" || a.Types != nil || a.Numbers != nil || a.Count != 9 {
		t.Fatalf("standard key: %+v", a)
	}
	if a := got[1]; a.Indexed || a.Types == nil {
		t.Fatalf("other key: %+v", a)
	}
	if len(got) != len(semconv.Keys)+1 {
		t.Fatalf("%d attributes", len(got))
	}
}
