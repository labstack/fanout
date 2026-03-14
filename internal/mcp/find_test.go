package mcp

import "testing"

func TestFoundMetric(t *testing.T) {
	m := FoundMetric{
		Name:        "kafka.consumer.lag",
		Type:        "gauge",
		Service:     "kafka",
		Value:       42.0,
		Unit:        "messages",
		Time:        "2024-01-01T12:00:00Z",
		Description: "Consumer group lag",
	}

	if m.Name != "kafka.consumer.lag" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Value != 42.0 {
		t.Errorf("Value = %f, want 42.0", m.Value)
	}
	if m.Unit != "messages" {
		t.Errorf("Unit = %q, want %q", m.Unit, "messages")
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
