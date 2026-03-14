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
		Window:      "24h",
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

func TestTraceIn_WithCompareTo(t *testing.T) {
	in := TraceIn{
		TraceID:   "abc123",
		CompareTo: "def456",
	}

	if in.TraceID != "abc123" {
		t.Errorf("TraceID = %q", in.TraceID)
	}
	if in.CompareTo != "def456" {
		t.Errorf("CompareTo = %q, want %q", in.CompareTo, "def456")
	}
}

func TestTraceIn_WithIncludeMetrics(t *testing.T) {
	trueVal := true
	in := TraceIn{
		TraceID:        "abc123",
		IncludeMetrics: &trueVal,
	}

	if in.IncludeMetrics == nil {
		t.Fatal("IncludeMetrics should not be nil")
	}
	if !*in.IncludeMetrics {
		t.Error("IncludeMetrics should be true")
	}
}

func TestTraceIn_IncludeMetrics_DefaultFalse(t *testing.T) {
	in := TraceIn{
		TraceID: "abc123",
	}

	// When nil, should default to false
	includeMetrics := false
	if in.IncludeMetrics != nil {
		includeMetrics = *in.IncludeMetrics
	}
	if includeMetrics {
		t.Error("IncludeMetrics should default to false when nil")
	}
}

func TestTraceComparison(t *testing.T) {
	cmp := TraceComparison{
		OtherTraceID:    "def456",
		OtherDurationMs: 85.2,
		DurationDeltaMs: 57.3,
		SpanDiffs: []TraceSpanDiff{
			{
				Operation: "process_payment",
				Service:   "payment",
				ThisMs:    89.3,
				OtherMs:   12.1,
				DeltaMs:   77.2,
			},
		},
	}

	if cmp.OtherTraceID != "def456" {
		t.Errorf("OtherTraceID = %q", cmp.OtherTraceID)
	}
	if cmp.OtherDurationMs != 85.2 {
		t.Errorf("OtherDurationMs = %f, want 85.2", cmp.OtherDurationMs)
	}
	if cmp.DurationDeltaMs != 57.3 {
		t.Errorf("DurationDeltaMs = %f, want 57.3", cmp.DurationDeltaMs)
	}
	if len(cmp.SpanDiffs) != 1 {
		t.Fatalf("SpanDiffs len = %d, want 1", len(cmp.SpanDiffs))
	}
	d := cmp.SpanDiffs[0]
	if d.Operation != "process_payment" {
		t.Errorf("SpanDiffs[0].Operation = %q", d.Operation)
	}
	if d.DeltaMs != 77.2 {
		t.Errorf("SpanDiffs[0].DeltaMs = %f, want 77.2", d.DeltaMs)
	}
}

func TestTraceSpanDiff(t *testing.T) {
	d := TraceSpanDiff{
		Operation: "db.query",
		Service:   "postgres",
		ThisMs:    200.0,
		OtherMs:   50.0,
		DeltaMs:   150.0,
	}

	if d.Service != "postgres" {
		t.Errorf("Service = %q", d.Service)
	}
	if d.DeltaMs != 150.0 {
		t.Errorf("DeltaMs = %f, want 150.0", d.DeltaMs)
	}
}

func TestTraceMetricContext(t *testing.T) {
	mc := TraceMetricContext{
		Service: "payment",
		AtTraceTime: TraceMetricSnapshot{
			P50Ms:       5200,
			P95Ms:       9444,
			ErrorRate:   0.0,
			SpansPerMin: 12,
		},
	}

	if mc.Service != "payment" {
		t.Errorf("Service = %q", mc.Service)
	}
	if mc.AtTraceTime.P50Ms != 5200 {
		t.Errorf("AtTraceTime.P50Ms = %f, want 5200", mc.AtTraceTime.P50Ms)
	}
	if mc.AtTraceTime.P95Ms != 9444 {
		t.Errorf("AtTraceTime.P95Ms = %f, want 9444", mc.AtTraceTime.P95Ms)
	}
	if mc.AtTraceTime.ErrorRate != 0.0 {
		t.Errorf("AtTraceTime.ErrorRate = %f, want 0.0", mc.AtTraceTime.ErrorRate)
	}
	if mc.AtTraceTime.SpansPerMin != 12 {
		t.Errorf("AtTraceTime.SpansPerMin = %f, want 12", mc.AtTraceTime.SpansPerMin)
	}
}

func TestTraceOut_WithComparison(t *testing.T) {
	out := TraceOut{
		TraceID:       "abc123",
		TotalDuration: 142.5,
		SpanCount:     3,
		Services:      []string{"api", "payment"},
		Comparison: &TraceComparison{
			OtherTraceID:    "def456",
			OtherDurationMs: 85.2,
			DurationDeltaMs: 57.3,
			SpanDiffs: []TraceSpanDiff{
				{Operation: "process_payment", Service: "payment", ThisMs: 89.3, OtherMs: 12.1, DeltaMs: 77.2},
			},
		},
	}

	if out.Comparison == nil {
		t.Fatal("Comparison should not be nil")
	}
	if out.Comparison.OtherTraceID != "def456" {
		t.Errorf("Comparison.OtherTraceID = %q", out.Comparison.OtherTraceID)
	}
	if len(out.Comparison.SpanDiffs) != 1 {
		t.Errorf("Comparison.SpanDiffs len = %d, want 1", len(out.Comparison.SpanDiffs))
	}
}

func TestTraceOut_WithMetricContext(t *testing.T) {
	out := TraceOut{
		TraceID: "abc123",
		MetricContext: []TraceMetricContext{
			{Service: "payment", AtTraceTime: TraceMetricSnapshot{P50Ms: 5200, P95Ms: 9444, ErrorRate: 0.0, SpansPerMin: 12}},
			{Service: "api", AtTraceTime: TraceMetricSnapshot{P50Ms: 10.0, P95Ms: 45.0, ErrorRate: 0.01, SpansPerMin: 100}},
		},
	}

	if len(out.MetricContext) != 2 {
		t.Fatalf("MetricContext len = %d, want 2", len(out.MetricContext))
	}
	if out.MetricContext[0].Service != "payment" {
		t.Errorf("MetricContext[0].Service = %q", out.MetricContext[0].Service)
	}
	if out.MetricContext[1].AtTraceTime.ErrorRate != 0.01 {
		t.Errorf("MetricContext[1].AtTraceTime.ErrorRate = %f, want 0.01", out.MetricContext[1].AtTraceTime.ErrorRate)
	}
}

func TestTraceOut_NilComparison(t *testing.T) {
	out := TraceOut{
		TraceID:    "abc123",
		Comparison: nil,
	}
	// nil comparison is fine when compare_to not provided
	if out.Comparison != nil {
		t.Error("Comparison should be nil when not requested")
	}
}
