package query

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestSchemaDiscoversColumnsAttributesAndValues(t *testing.T) {
	e := newFixtureExecutor(t)
	schema, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	spans := schema.Signals["spans"]
	if len(schema.Signals) != 3 || len(schema.SQLRelations) != 3 || len(schema.SQLRelations["resource_inventory"].Columns) != 6 {
		t.Fatalf("SQL relation discovery: %+v", schema)
	}
	if got := schema.SQLRelations["resource_inventory"]; got.Columns[3].SQLType != "VARIANT" || got.Columns[4].SQLType != "TIMESTAMPTZ_NS" {
		t.Fatalf("inventory types: %+v", got)
	}
	d := Dashboard{Name: "Invalid structured inventory", Panels: []Panel{{ID: "resources", Title: "Resources", Viz: "stat", Query: &Spec{From: "resource_inventory", Measures: []string{"count()"}}}}}
	if err := e.Validate(t.Context(), &d); err == nil {
		t.Fatal("SQL-only relation accepted as structured signal")
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
	if !slices.Contains(schema.Measures, "p95") || !slices.Contains(schema.Units, "ms") {
		t.Fatalf("measures %v units %v", schema.Measures, schema.Units)
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
		rows, err := db.QueryContext(t.Context(), "SELECT * FROM "+name+" LIMIT 0")
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
	now := e.now()
	for i := range 70 {
		if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Namespace: fmt.Sprintf("ns%d", i)}); err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		n := len(e.schemas)
		e.mu.Unlock()
		if n > maxSchemaEntries {
			t.Fatalf("cache holds %d entries", n)
		}
	}
	e.now = func() time.Time { return now.Add(schemaTTL + time.Second) }
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Namespace: "fresh"}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.schemas) != 1 {
		t.Fatalf("expired entries kept: %d", len(e.schemas))
	}
}
