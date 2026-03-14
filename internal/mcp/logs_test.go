package mcp

import (
	"testing"
)

func TestLogsIn_Fields(t *testing.T) {
	in := LogsIn{
		Query:     "connection pool",
		Severity:  []string{"ERROR", "WARN"},
		TraceID:   "abc123",
		Service:   "payment",
		Attrs:     map[string]string{"db.system": "postgresql"},
		GroupBy:   []string{"service", "severity"},
		OrderBy:   "count",
		Window:    "1h",
		Namespace: "prod",
		Tenant:    "t1",
		Limit:     50,
	}

	if in.Query != "connection pool" {
		t.Errorf("Query = %q, want 'connection pool'", in.Query)
	}
	if len(in.Severity) != 2 {
		t.Errorf("Severity len = %d, want 2", len(in.Severity))
	}
	if in.Severity[0] != "ERROR" {
		t.Errorf("Severity[0] = %q, want ERROR", in.Severity[0])
	}
	if in.TraceID != "abc123" {
		t.Errorf("TraceID = %q, want abc123", in.TraceID)
	}
	if in.Service != "payment" {
		t.Errorf("Service = %q, want payment", in.Service)
	}
	if in.Attrs["db.system"] != "postgresql" {
		t.Errorf("Attrs[db.system] = %q, want postgresql", in.Attrs["db.system"])
	}
	if len(in.GroupBy) != 2 {
		t.Errorf("GroupBy len = %d, want 2", len(in.GroupBy))
	}
	if in.Limit != 50 {
		t.Errorf("Limit = %d, want 50", in.Limit)
	}
}

func TestLogRowOut_Fields(t *testing.T) {
	l := LogRowOut{
		Time:       "2026-03-14T17:01:23Z",
		Service:    "payment",
		Severity:   "ERROR",
		Body:       "Connection pool exhausted, all 10 connections in use",
		TraceID:    "abc123",
		SpanID:     "span-012",
		Attributes: map[string]string{"db.system": "postgresql"},
	}

	if l.Time != "2026-03-14T17:01:23Z" {
		t.Errorf("Time = %q", l.Time)
	}
	if l.Service != "payment" {
		t.Errorf("Service = %q, want payment", l.Service)
	}
	if l.Severity != "ERROR" {
		t.Errorf("Severity = %q, want ERROR", l.Severity)
	}
	if l.TraceID != "abc123" {
		t.Errorf("TraceID = %q, want abc123", l.TraceID)
	}
	if l.Attributes["db.system"] != "postgresql" {
		t.Errorf("Attributes[db.system] = %q, want postgresql", l.Attributes["db.system"])
	}
}

func TestLogGroupOut_Fields(t *testing.T) {
	g := LogGroupOut{
		Key:            map[string]string{"service": "payment", "severity": "ERROR"},
		Count:          23,
		SampleBodies:   []string{"Connection pool exhausted...", "DB timeout"},
		SampleTraceIDs: []string{"abc123", "def456"},
	}

	if g.Key["service"] != "payment" {
		t.Errorf("Key[service] = %q, want payment", g.Key["service"])
	}
	if g.Key["severity"] != "ERROR" {
		t.Errorf("Key[severity] = %q, want ERROR", g.Key["severity"])
	}
	if g.Count != 23 {
		t.Errorf("Count = %d, want 23", g.Count)
	}
	if len(g.SampleBodies) != 2 {
		t.Errorf("SampleBodies len = %d, want 2", len(g.SampleBodies))
	}
	if len(g.SampleTraceIDs) != 2 {
		t.Errorf("SampleTraceIDs len = %d, want 2", len(g.SampleTraceIDs))
	}
}

func TestLogsOut_Ungrouped(t *testing.T) {
	out := LogsOut{
		Logs: []LogRowOut{
			{
				Time:     "2026-03-14T17:01:23Z",
				Service:  "payment",
				Severity: "ERROR",
				Body:     "Connection pool exhausted",
				TraceID:  "abc123",
			},
		},
		TotalMatched: 1200,
		Returned:     1,
		Suggestion:   "Use trace tool with trace_id 'abc123' for full request context.",
	}

	if len(out.Logs) != 1 {
		t.Errorf("Logs len = %d, want 1", len(out.Logs))
	}
	if out.TotalMatched != 1200 {
		t.Errorf("TotalMatched = %d, want 1200", out.TotalMatched)
	}
	if out.Returned != 1 {
		t.Errorf("Returned = %d, want 1", out.Returned)
	}
}

func TestLogsOut_Grouped(t *testing.T) {
	out := LogsOut{
		Groups: []LogGroupOut{
			{
				Key:            map[string]string{"service": "payment"},
				Count:          23,
				SampleBodies:   []string{"Connection pool exhausted..."},
				SampleTraceIDs: []string{"abc123"},
			},
		},
		TotalGroups: 5,
	}

	if len(out.Groups) != 1 {
		t.Errorf("Groups len = %d, want 1", len(out.Groups))
	}
	if out.TotalGroups != 5 {
		t.Errorf("TotalGroups = %d, want 5", out.TotalGroups)
	}
}

func TestLogsIn_WindowDefault(t *testing.T) {
	in := LogsIn{}
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

func TestLogsIn_NoSeverity(t *testing.T) {
	in := LogsIn{}
	if in.Severity != nil {
		t.Error("Severity should default to nil")
	}
}

func TestLogsIn_NoGroupBy(t *testing.T) {
	in := LogsIn{}
	if in.GroupBy != nil {
		t.Error("GroupBy should default to nil")
	}
}

func TestLogGroupOut_EmptySamples(t *testing.T) {
	g := LogGroupOut{
		Key:            map[string]string{"service": "api"},
		Count:          10,
		SampleBodies:   []string{},
		SampleTraceIDs: []string{},
	}
	if g.SampleBodies == nil {
		t.Error("SampleBodies should not be nil")
	}
	if g.SampleTraceIDs == nil {
		t.Error("SampleTraceIDs should not be nil")
	}
	if len(g.SampleBodies) != 0 {
		t.Errorf("SampleBodies len = %d, want 0", len(g.SampleBodies))
	}
}
