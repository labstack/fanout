package panel

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
