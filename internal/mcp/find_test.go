package mcp

import "testing"

func TestRenderFind(t *testing.T) {
	find := &FindOut{
		Spans: []FoundSpan{
			{TraceID: "trace-123", SpanID: "span-1", Service: "api", Operation: "GET /users", DurationMs: 50.0, Status: "ok", StartTime: "12:00"},
			{TraceID: "trace-123", SpanID: "span-2", Service: "db", Operation: "SELECT", DurationMs: 20.0, Status: "ok", StartTime: "12:00"},
		},
		Logs: []FoundLog{
			{Timestamp: "12:00:01", Service: "api", Severity: "INFO", Body: "Request received", TraceID: "trace-123"},
			{Timestamp: "12:00:02", Service: "api", Severity: "ERROR", Body: "Connection timeout", TraceID: "trace-456"},
		},
		SpanCount:  2,
		LogCount:   2,
		HasMore:    false,
		Suggestion: "Found spans. Use trace tool for details.",
	}

	output := renderFind(find)

	if output.ASCII == "" {
		t.Error("renderFind() should produce ASCII output")
	}
	if output.HTML == "" {
		t.Error("renderFind() should produce HTML output")
	}
}

func TestRenderFind_Empty(t *testing.T) {
	find := &FindOut{
		Spans:      []FoundSpan{},
		Logs:       []FoundLog{},
		SpanCount:  0,
		LogCount:   0,
		HasMore:    false,
		Suggestion: "No results. Try widening the time window.",
	}

	output := renderFind(find)

	if output.ASCII == "" {
		t.Error("renderFind() should produce ASCII output for empty results")
	}
}

func TestRenderFind_SpansOnly(t *testing.T) {
	find := &FindOut{
		Spans: []FoundSpan{
			{TraceID: "trace-123", SpanID: "span-1", Service: "api", Operation: "POST /orders", DurationMs: 100.0, Status: "error"},
		},
		Logs:       []FoundLog{},
		SpanCount:  1,
		LogCount:   0,
		HasMore:    false,
		Suggestion: "Found spans. Use trace tool with trace_id 'trace-123' for details.",
	}

	output := renderFind(find)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderFind() should produce output with spans only")
	}
}

func TestRenderFind_LogsOnly(t *testing.T) {
	find := &FindOut{
		Spans: []FoundSpan{},
		Logs: []FoundLog{
			{Timestamp: "12:00:00", Service: "worker", Severity: "WARN", Body: "Queue backlog increasing"},
		},
		SpanCount: 0,
		LogCount:  1,
		HasMore:   false,
	}

	output := renderFind(find)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderFind() should produce output with logs only")
	}
}

func TestRenderFind_HasMore(t *testing.T) {
	find := &FindOut{
		Spans:     make([]FoundSpan, 50),
		Logs:      []FoundLog{},
		SpanCount: 50,
		LogCount:  0,
		HasMore:   true,
	}

	output := renderFind(find)

	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderFind() should produce output when hasMore is true")
	}
}

func TestMoreLabel(t *testing.T) {
	tests := []struct {
		hasMore  bool
		expected string
	}{
		{true, "more available"},
		{false, "complete"},
	}

	for _, tc := range tests {
		result := moreLabel(tc.hasMore)
		if result != tc.expected {
			t.Errorf("moreLabel(%v) = %q, want %q", tc.hasMore, result, tc.expected)
		}
	}
}

func TestMoreStatus(t *testing.T) {
	tests := []struct {
		hasMore  bool
		expected string
	}{
		{true, "warning"},
		{false, "healthy"},
	}

	for _, tc := range tests {
		result := moreStatus(tc.hasMore)
		if result != tc.expected {
			t.Errorf("moreStatus(%v) = %q, want %q", tc.hasMore, result, tc.expected)
		}
	}
}

func TestFoundSpan(t *testing.T) {
	s := FoundSpan{
		TraceID:      "abc123",
		SpanID:       "span-1",
		Service:      "api-gateway",
		Operation:    "GET /health",
		DurationMs:   5.5,
		Status:       "ok",
		StartTime:    "2024-01-01T12:00:00Z",
		ScopeName:    "http",
		ScopeVersion: "1.0.0",
	}

	if s.TraceID != "abc123" {
		t.Errorf("TraceID = %q", s.TraceID)
	}
	if s.DurationMs != 5.5 {
		t.Errorf("DurationMs = %f, want 5.5", s.DurationMs)
	}
	if s.ScopeName != "http" {
		t.Errorf("ScopeName = %q, want %q", s.ScopeName, "http")
	}
}

func TestFoundLog(t *testing.T) {
	l := FoundLog{
		Timestamp:      "2024-01-01T12:00:00Z",
		ObservedTime:   "2024-01-01T12:00:01Z",
		Service:        "worker",
		Severity:       "ERROR",
		SeverityNumber: 17,
		Body:           "Failed to process job",
		TraceID:        "trace-123",
		SpanID:         "span-5",
		ScopeName:      "jobs",
		ScopeVersion:   "2.0.0",
	}

	if l.Severity != "ERROR" {
		t.Errorf("Severity = %q, want %q", l.Severity, "ERROR")
	}
	if l.SeverityNumber != 17 {
		t.Errorf("SeverityNumber = %d, want 17", l.SeverityNumber)
	}
	if l.Body != "Failed to process job" {
		t.Errorf("Body = %q", l.Body)
	}
}

func TestFindIn(t *testing.T) {
	in := FindIn{
		Query:     "error",
		Service:   "api",
		Operation: "GET /users",
		Type:      "both",
		Status:    "error",
		Window:    30,
		Namespace: "prod",
		TenantID:  "t1",
		Severity:  []string{"ERROR", "FATAL"},
		Attrs:     map[string]string{"http.status_code": "500"},
		Limit:     100,
		Format:    "html",
	}

	if in.Query != "error" {
		t.Errorf("Query = %q", in.Query)
	}
	if len(in.Severity) != 2 {
		t.Errorf("Severity length = %d, want 2", len(in.Severity))
	}
	if in.Attrs["http.status_code"] != "500" {
		t.Errorf("Attrs[http.status_code] = %q", in.Attrs["http.status_code"])
	}
}

func TestFindOut(t *testing.T) {
	out := FindOut{
		Spans:      []FoundSpan{{TraceID: "t1"}},
		Logs:       []FoundLog{{Timestamp: "12:00"}},
		SpanCount:  1,
		LogCount:   1,
		HasMore:    true,
		Suggestion: "Try narrowing the search",
	}

	if out.SpanCount != 1 {
		t.Errorf("SpanCount = %d, want 1", out.SpanCount)
	}
	if !out.HasMore {
		t.Error("HasMore should be true")
	}
}
