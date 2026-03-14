package mcp

import "testing"

func TestBuildTraceTree(t *testing.T) {
	spans := []TraceSpan{
		{SpanID: "root", ParentSpanID: "", Service: "api", Operation: "GET /users", DurationMs: 100.0, Status: "ok"},
		{SpanID: "child1", ParentSpanID: "root", Service: "db", Operation: "SELECT", DurationMs: 50.0, Status: "ok"},
		{SpanID: "child2", ParentSpanID: "root", Service: "cache", Operation: "GET", DurationMs: 10.0, Status: "ok"},
	}

	tree := buildTraceTree(spans)

	if tree == nil {
		t.Fatal("buildTraceTree() returned nil")
	}
	if tree.Root == nil {
		t.Fatal("buildTraceTree() returned tree with nil root")
	}
	if len(tree.Root.Children) != 2 {
		t.Errorf("Root should have 2 children, got %d", len(tree.Root.Children))
	}
}

func TestBuildTraceTree_Empty(t *testing.T) {
	tree := buildTraceTree([]TraceSpan{})

	if tree == nil {
		t.Fatal("buildTraceTree() returned nil for empty spans")
	}
	if tree.Root == nil {
		t.Fatal("buildTraceTree() should return tree with 'No spans' node")
	}
	if tree.Root.Label != "No spans" {
		t.Errorf("Empty tree root label = %q, want %q", tree.Root.Label, "No spans")
	}
}

func TestBuildTraceTree_SingleSpan(t *testing.T) {
	spans := []TraceSpan{
		{SpanID: "only", ParentSpanID: "", Service: "api", Operation: "ping", DurationMs: 5.0, Status: "ok"},
	}

	tree := buildTraceTree(spans)

	if tree.Root == nil {
		t.Fatal("buildTraceTree() should have root for single span")
	}
	if len(tree.Root.Children) != 0 {
		t.Error("Single span should have no children")
	}
}

func TestBuildTraceTree_ErrorSpan(t *testing.T) {
	spans := []TraceSpan{
		{SpanID: "root", ParentSpanID: "", Service: "api", Operation: "POST /orders", DurationMs: 100.0, Status: "error"},
	}

	tree := buildTraceTree(spans)

	if tree.Root == nil {
		t.Fatal("buildTraceTree() should have root")
	}
	// Error spans should have error icon
	if tree.Root.Meta["status"] != "error" {
		t.Errorf("Error span status = %q, want %q", tree.Root.Meta["status"], "error")
	}
}

func TestBuildTraceTree_CriticalSpan(t *testing.T) {
	spans := []TraceSpan{
		{SpanID: "root", ParentSpanID: "", Service: "api", Operation: "GET /users", DurationMs: 100.0, Status: "ok", IsCritical: true},
	}

	tree := buildTraceTree(spans)

	if tree.Root == nil {
		t.Fatal("buildTraceTree() should have root")
	}
}

func TestBuildTraceTree_OrphanSpan(t *testing.T) {
	// Span with parent ID that doesn't exist
	spans := []TraceSpan{
		{SpanID: "orphan", ParentSpanID: "nonexistent", Service: "api", Operation: "orphan-op", DurationMs: 50.0},
	}

	tree := buildTraceTree(spans)

	// Should still produce a tree, using first span as fallback root
	if tree == nil || tree.Root == nil {
		t.Fatal("buildTraceTree() should handle orphan spans")
	}
}

func TestTraceSpan(t *testing.T) {
	s := TraceSpan{
		SpanID:       "span-123",
		ParentSpanID: "parent-456",
		Service:      "api-gateway",
		Operation:    "authenticate",
		Kind:         "SERVER",
		StartTime:    "2024-01-01T12:00:00Z",
		DurationMs:   75.5,
		Status:       "ok",
		StatusMsg:    "",
		SelfTimeMs:   25.0,
		IsCritical:   true,
		Events: []SpanEventOut{
			{Time: 1704110400000000000, Name: "auth_started", Attributes: map[string]string{"user": "test"}},
		},
		Links: []SpanLinkOut{
			{TraceID: "other-trace", SpanID: "other-span"},
		},
		TraceState:   "vendor=value",
		ScopeName:    "auth",
		ScopeVersion: "1.0.0",
		Attributes:   map[string]any{"user.id": "123"},
	}

	if s.SpanID != "span-123" {
		t.Errorf("SpanID = %q", s.SpanID)
	}
	if s.DurationMs != 75.5 {
		t.Errorf("DurationMs = %f, want 75.5", s.DurationMs)
	}
	if !s.IsCritical {
		t.Error("IsCritical should be true")
	}
	if len(s.Events) != 1 {
		t.Errorf("Events count = %d, want 1", len(s.Events))
	}
	if len(s.Links) != 1 {
		t.Errorf("Links count = %d, want 1", len(s.Links))
	}
}

func TestCorrelatedLog(t *testing.T) {
	l := CorrelatedLog{
		Timestamp:      "2024-01-01T12:00:00Z",
		ObservedTime:   "2024-01-01T12:00:01Z",
		Service:        "api",
		Severity:       "ERROR",
		SeverityNumber: 17,
		Body:           "Connection refused",
		SpanID:         "span-123",
		ScopeName:      "http",
		ScopeVersion:   "1.0.0",
	}

	if l.Severity != "ERROR" {
		t.Errorf("Severity = %q", l.Severity)
	}
	if l.SpanID != "span-123" {
		t.Errorf("SpanID = %q", l.SpanID)
	}
}

func TestRootCause(t *testing.T) {
	rc := RootCause{
		Type:        "timeout",
		Description: "Database query exceeded 5s timeout",
		SpanID:      "db-span",
		Service:     "postgres",
	}

	if rc.Type != "timeout" {
		t.Errorf("Type = %q", rc.Type)
	}
	if rc.Service != "postgres" {
		t.Errorf("Service = %q", rc.Service)
	}
}

func TestTraceIn(t *testing.T) {
	includeLogs := false
	in := TraceIn{
		TraceID:     "abc123",
		IncludeLogs: &includeLogs,
		Window:      1440,
	}

	if in.TraceID != "abc123" {
		t.Errorf("TraceID = %q", in.TraceID)
	}
	if *in.IncludeLogs != false {
		t.Error("IncludeLogs should be false")
	}
}

func TestSpanEventOut(t *testing.T) {
	e := SpanEventOut{
		Time:       1704110400000000000,
		Name:       "exception",
		Attributes: map[string]string{"message": "null pointer"},
	}

	if e.Name != "exception" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.Attributes["message"] != "null pointer" {
		t.Errorf("Attributes[message] = %q", e.Attributes["message"])
	}
}

func TestSpanLinkOut(t *testing.T) {
	l := SpanLinkOut{
		TraceID:    "linked-trace",
		SpanID:     "linked-span",
		TraceState: "vendor=value",
		Attributes: map[string]string{"link.type": "parent"},
	}

	if l.TraceID != "linked-trace" {
		t.Errorf("TraceID = %q", l.TraceID)
	}
}
