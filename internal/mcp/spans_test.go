package mcp

import (
	"testing"
)

func TestSpansIn_Fields(t *testing.T) {
	minD := 10.0
	maxD := 500.0
	in := SpansIn{
		Query:            "error",
		Operation:        "GET /api",
		Service:          "frontend",
		Status:           "error",
		Kind:             "server",
		MinDurationMs:    &minD,
		MaxDurationMs:    &maxD,
		Attrs:            map[string]string{"http.status_code": "500"},
		GroupBy:          []string{"service", "operation"},
		OrderBy:          "error_rate",
		IncludeExemplars: true,
		Window:           "15m",
		Namespace:        "prod",
		Limit:            100,
	}

	if in.Query != "error" {
		t.Errorf("Query = %q, want error", in.Query)
	}
	if in.Service != "frontend" {
		t.Errorf("Service = %q, want frontend", in.Service)
	}
	if *in.MinDurationMs != 10.0 {
		t.Errorf("MinDurationMs = %f, want 10.0", *in.MinDurationMs)
	}
	if *in.MaxDurationMs != 500.0 {
		t.Errorf("MaxDurationMs = %f, want 500.0", *in.MaxDurationMs)
	}
	if len(in.GroupBy) != 2 {
		t.Errorf("GroupBy len = %d, want 2", len(in.GroupBy))
	}
	if !in.IncludeExemplars {
		t.Error("IncludeExemplars should be true")
	}
	if in.Attrs["http.status_code"] != "500" {
		t.Errorf("Attrs[http.status_code] = %q, want 500", in.Attrs["http.status_code"])
	}
}

func TestSpanRowOut_Fields(t *testing.T) {
	s := SpanRowOut{
		TraceID:    "trace-abc",
		SpanID:     "span-def",
		Service:    "frontend",
		Operation:  "GET /api/products",
		Kind:       "server",
		StartTime:  "2026-03-14T17:01:23Z",
		DurationMs: 16.17,
		Status:     "ok",
		Attributes: map[string]string{"http.method": "GET"},
	}

	if s.TraceID != "trace-abc" {
		t.Errorf("TraceID = %q", s.TraceID)
	}
	if s.DurationMs != 16.17 {
		t.Errorf("DurationMs = %f, want 16.17", s.DurationMs)
	}
	if s.Attributes["http.method"] != "GET" {
		t.Errorf("Attributes[http.method] = %q, want GET", s.Attributes["http.method"])
	}
}

func TestSpanGroupOut_Fields(t *testing.T) {
	g := SpanGroupOut{
		Key:              map[string]string{"service": "frontend", "http.method": "GET"},
		Count:            3200,
		ErrorCount:       85,
		ErrorRate:        0.0266,
		P50Ms:            2.1,
		P95Ms:            14.8,
		P99Ms:            22.3,
		ExemplarTraceIDs: []string{"abc123", "def456"},
	}

	if g.Key["service"] != "frontend" {
		t.Errorf("Key[service] = %q, want frontend", g.Key["service"])
	}
	if g.Count != 3200 {
		t.Errorf("Count = %d, want 3200", g.Count)
	}
	if g.ErrorCount != 85 {
		t.Errorf("ErrorCount = %d, want 85", g.ErrorCount)
	}
	if g.ErrorRate != 0.0266 {
		t.Errorf("ErrorRate = %f, want 0.0266", g.ErrorRate)
	}
	if g.P50Ms != 2.1 {
		t.Errorf("P50Ms = %f, want 2.1", g.P50Ms)
	}
	if g.P95Ms != 14.8 {
		t.Errorf("P95Ms = %f, want 14.8", g.P95Ms)
	}
	if g.P99Ms != 22.3 {
		t.Errorf("P99Ms = %f, want 22.3", g.P99Ms)
	}
	if len(g.ExemplarTraceIDs) != 2 {
		t.Errorf("ExemplarTraceIDs len = %d, want 2", len(g.ExemplarTraceIDs))
	}
}

func TestSpansOut_Ungrouped(t *testing.T) {
	out := SpansOut{
		Spans: []SpanRowOut{
			{TraceID: "t1", Service: "api", DurationMs: 5.0},
		},
		TotalMatched: 4397,
		Returned:     1,
		Suggestion:   "Use trace tool with trace_id 't1' for full request context.",
	}

	if len(out.Spans) != 1 {
		t.Errorf("Spans len = %d, want 1", len(out.Spans))
	}
	if out.TotalMatched != 4397 {
		t.Errorf("TotalMatched = %d, want 4397", out.TotalMatched)
	}
	if out.Returned != 1 {
		t.Errorf("Returned = %d, want 1", out.Returned)
	}
}

func TestSpansOut_Grouped(t *testing.T) {
	out := SpansOut{
		Groups: []SpanGroupOut{
			{Key: map[string]string{"service": "frontend"}, Count: 100},
		},
		TotalGroups: 12,
	}

	if len(out.Groups) != 1 {
		t.Errorf("Groups len = %d, want 1", len(out.Groups))
	}
	if out.TotalGroups != 12 {
		t.Errorf("TotalGroups = %d, want 12", out.TotalGroups)
	}
}

func TestSpansIn_NilPointers(t *testing.T) {
	in := SpansIn{}
	if in.MinDurationMs != nil {
		t.Error("MinDurationMs should default to nil")
	}
	if in.MaxDurationMs != nil {
		t.Error("MaxDurationMs should default to nil")
	}
}

func TestSpansIn_WindowDefault(t *testing.T) {
	in := SpansIn{}
	if in.Window != "" {
		t.Errorf("Window default should be empty string, got %q", in.Window)
	}
	// parseWindow("") should return default 15m
	tw, err := parseWindow(in.Window)
	if err != nil {
		t.Fatalf("parseWindow('') error = %v", err)
	}
	if tw.Minutes != defWindow {
		t.Errorf("default window = %d minutes, want %d", tw.Minutes, defWindow)
	}
}

func TestSpanGroupOut_EmptyExemplars(t *testing.T) {
	g := SpanGroupOut{
		Key:              map[string]string{"service": "api"},
		Count:            10,
		ExemplarTraceIDs: []string{},
	}
	if g.ExemplarTraceIDs == nil {
		t.Error("ExemplarTraceIDs should not be nil")
	}
	if len(g.ExemplarTraceIDs) != 0 {
		t.Errorf("ExemplarTraceIDs len = %d, want 0", len(g.ExemplarTraceIDs))
	}
}
