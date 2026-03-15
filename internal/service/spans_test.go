package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// spanCols is the column set returned by the ungrouped spans query.
var spanCols = []string{"trace_id", "span_id", "service", "operation", "kind", "start_time", "duration_ms", "status", "attributes_json"}

func TestSpans_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(spanCols))

	result, err := svc.Spans(context.Background(), SpanParams{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if result == nil {
		t.Fatal("Spans() returned nil")
	}
	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
	if result.TotalMatched != 0 {
		t.Errorf("TotalMatched = %d, want 0", result.TotalMatched)
	}
}

func TestSpans_DefaultParams(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(spanCols))

	result, err := svc.Spans(context.Background(), SpanParams{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if result == nil {
		t.Fatal("Spans() returned nil")
	}
}

func TestSpans_OneRow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(spanCols).AddRow(
			"trace-1", "span-1", "frontend", "GET /api", "SPAN_KIND_SERVER",
			"2026-03-14T10:00:00Z", 16.17, "STATUS_CODE_OK", `{"http.method":"GET"}`))

	result, err := svc.Spans(context.Background(), SpanParams{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if len(result.Spans) != 1 {
		t.Fatalf("Spans count = %d, want 1", len(result.Spans))
	}
	sp := result.Spans[0]
	if sp.TraceID != "trace-1" {
		t.Errorf("TraceID = %q, want trace-1", sp.TraceID)
	}
	if sp.Service != "frontend" {
		t.Errorf("Service = %q, want frontend", sp.Service)
	}
	if sp.DurationMs != 16.17 {
		t.Errorf("DurationMs = %f, want 16.17", sp.DurationMs)
	}
	if sp.Attributes["http.method"] != "GET" {
		t.Errorf("Attributes[http.method] = %q, want GET", sp.Attributes["http.method"])
	}
}

func TestSpans_NullAttributes(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(spanCols).AddRow(
			"trace-1", "span-1", "svc", "op", "SPAN_KIND_SERVER",
			"2026-03-14T10:00:00Z", 5.0, "STATUS_CODE_OK", nil))

	result, err := svc.Spans(context.Background(), SpanParams{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if len(result.Spans) != 1 {
		t.Fatalf("Spans count = %d, want 1", len(result.Spans))
	}
	if result.Spans[0].Attributes != nil {
		t.Errorf("Attributes should be nil when attributes_json is NULL")
	}
}

func TestSpans_HasMore(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Return 11 rows with limit=10 → hasMore
	rows := sqlmock.NewRows(spanCols)
	for i := 0; i < 11; i++ {
		rows.AddRow("trace", "span", "svc", "op", "SPAN_KIND_SERVER",
			"2026-03-14T10:00:00Z", 5.0, "STATUS_CODE_OK", nil)
	}
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Spans(context.Background(), SpanParams{Limit: 10})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if len(result.Spans) != 10 {
		t.Errorf("Spans count = %d, want 10", len(result.Spans))
	}
	if result.TotalMatched != 11 {
		t.Errorf("TotalMatched = %d, want 11", result.TotalMatched)
	}
}

func TestSpans_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	result, err := svc.Spans(context.Background(), SpanParams{})
	if err == nil {
		t.Fatal("Spans() should return error on query failure")
	}
	if !strings.Contains(err.Error(), "db error") {
		t.Errorf("error should contain 'db error', got: %v", err)
	}
	if result == nil {
		t.Fatal("Spans() should return non-nil result even on error")
	}
}

func TestSpans_InvalidGroupBy(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.Spans(context.Background(), SpanParams{
		GroupBy: []string{"invalid_field"},
	})
	if err == nil {
		t.Fatal("Spans() should return error for invalid group_by field")
	}
	if !strings.Contains(err.Error(), "invalid group_by field") {
		t.Errorf("error should mention invalid group_by field, got: %v", err)
	}
}

func TestSpans_KindMapping(t *testing.T) {
	// Verify kind map entries
	cases := []struct {
		in   string
		want string
	}{
		{"server", "SPAN_KIND_SERVER"},
		{"client", "SPAN_KIND_CLIENT"},
		{"producer", "SPAN_KIND_PRODUCER"},
		{"consumer", "SPAN_KIND_CONSUMER"},
		{"internal", "SPAN_KIND_INTERNAL"},
	}
	for _, c := range cases {
		got, ok := spanKindMap[c.in]
		if !ok {
			t.Errorf("spanKindMap[%q] not found", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("spanKindMap[%q] = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSpans_StatusFilters(t *testing.T) {
	// We just test that the filters are built without crashing, not SQL text.
	cases := []string{"error", "ok", "slow", "all", ""}
	for _, status := range cases {
		filters, _ := buildSpanFilters(SpanParams{Status: status})
		switch status {
		case "error":
			if len(filters) != 1 {
				t.Errorf("status=error: expected 1 filter, got %d", len(filters))
			}
		case "ok":
			if len(filters) != 1 {
				t.Errorf("status=ok: expected 1 filter, got %d", len(filters))
			}
		case "slow":
			if len(filters) != 1 {
				t.Errorf("status=slow: expected 1 filter, got %d", len(filters))
			}
		case "all", "":
			if len(filters) != 0 {
				t.Errorf("status=%q: expected 0 filters, got %d", status, len(filters))
			}
		}
	}
}

func TestSpans_AttrFilters(t *testing.T) {
	filters, args := buildSpanFilters(SpanParams{
		Attrs: map[string]string{"http.method": "GET", "http.status_code": "200"},
	})
	if len(filters) != 2 {
		t.Errorf("expected 2 attr filters, got %d", len(filters))
	}
	if len(args) != 4 {
		t.Errorf("expected 4 args (2 key + 2 val), got %d", len(args))
	}
}

func TestSpans_DurationFilters(t *testing.T) {
	minD := 10.0
	maxD := 500.0
	filters, args := buildSpanFilters(SpanParams{
		MinDurationMs: &minD,
		MaxDurationMs: &maxD,
	})
	if len(filters) != 2 {
		t.Errorf("expected 2 duration filters, got %d", len(filters))
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
}

// Grouped path tests

var groupCols = []string{"service", "count", "error_count", "error_rate", "p50_ms", "p95_ms", "p99_ms", "exemplar_trace_ids"}

func TestSpans_GroupBy_Service(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(groupCols).AddRow(
			"frontend", int64(3200), int64(85), 0.0265625,
			2.1, 14.8, 22.3, `["trace-abc","trace-def"]`))

	result, err := svc.Spans(context.Background(), SpanParams{
		GroupBy:          []string{"service"},
		IncludeExemplars: true,
	})
	if err != nil {
		t.Fatalf("Spans() grouped error = %v", err)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups count = %d, want 1", len(result.Groups))
	}
	g := result.Groups[0]
	if g.Key["service"] != "frontend" {
		t.Errorf("Key[service] = %q, want frontend", g.Key["service"])
	}
	if g.Count != 3200 {
		t.Errorf("Count = %d, want 3200", g.Count)
	}
	if g.ErrorCount != 85 {
		t.Errorf("ErrorCount = %d, want 85", g.ErrorCount)
	}
	if len(g.ExemplarTraceIDs) != 2 {
		t.Errorf("ExemplarTraceIDs len = %d, want 2", len(g.ExemplarTraceIDs))
	}
	if g.ExemplarTraceIDs[0] != "trace-abc" {
		t.Errorf("ExemplarTraceIDs[0] = %q, want trace-abc", g.ExemplarTraceIDs[0])
	}
}

func TestSpans_GroupBy_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(groupCols))

	result, err := svc.Spans(context.Background(), SpanParams{
		GroupBy: []string{"service"},
	})
	if err != nil {
		t.Fatalf("Spans() grouped error = %v", err)
	}
	if len(result.Groups) != 0 {
		t.Errorf("Groups count = %d, want 0", len(result.Groups))
	}
}

func TestSpans_GroupBy_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("group error"))

	result, err := svc.Spans(context.Background(), SpanParams{
		GroupBy: []string{"service"},
	})
	if err == nil {
		t.Fatal("Spans() grouped should return error on query failure")
	}
	if !strings.Contains(err.Error(), "group error") {
		t.Errorf("error should contain 'group error', got: %v", err)
	}
	if result == nil {
		t.Fatal("Spans() should return non-nil result even on error")
	}
}

func TestGroupByExpr(t *testing.T) {
	cases := []struct {
		field string
		want  string
	}{
		{"service", "service"},
		{"operation", "operation"},
		{"status", "status"},
		{"kind", "kind"},
		{"http.method", `COALESCE(http_method, json_extract_string(attributes_json, '$.http.method'))`},
		{"http.status_code", `COALESCE(http_status_code, json_extract_string(attributes_json, '$.http.status_code'))`},
	}
	for _, c := range cases {
		got := groupByExpr(c.field)
		if got != c.want {
			t.Errorf("groupByExpr(%q) = %q, want %q", c.field, got, c.want)
		}
	}
}

func TestOrderByClause(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"time", "start_time DESC"},
		{"", "start_time DESC"},
		{"duration", "duration_ms DESC"},
		{"unknown", "start_time DESC"},
	}
	for _, c := range cases {
		got := orderByClause(c.in)
		if got != c.want {
			t.Errorf("orderByClause(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGroupOrderByClause(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"count", "count DESC"},
		{"", "count DESC"},
		{"error_rate", "error_rate DESC"},
		{"duration", "p95_ms DESC"},
	}
	for _, c := range cases {
		got := groupOrderByClause(c.in)
		if got != c.want {
			t.Errorf("groupOrderByClause(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseAttrsJSON(t *testing.T) {
	m := parseAttrsJSON(`{"http.method":"GET","http.status_code":"200"}`)
	if m == nil {
		t.Fatal("parseAttrsJSON returned nil")
	}
	if m["http.method"] != "GET" {
		t.Errorf("http.method = %q, want GET", m["http.method"])
	}
	if m["http.status_code"] != "200" {
		t.Errorf("http.status_code = %q, want 200", m["http.status_code"])
	}
}

func TestParseAttrsJSON_Invalid(t *testing.T) {
	m := parseAttrsJSON("not-json")
	if m != nil {
		t.Errorf("parseAttrsJSON(invalid) should return nil, got %v", m)
	}
}

func TestParseAttrsJSON_Empty(t *testing.T) {
	m := parseAttrsJSON("")
	if m != nil {
		t.Errorf("parseAttrsJSON('') should return nil, got %v", m)
	}
}

func TestParseStringArray(t *testing.T) {
	arr := parseStringArray(`["a","b","c"]`)
	if len(arr) != 3 {
		t.Fatalf("len = %d, want 3", len(arr))
	}
	if arr[0] != "a" || arr[1] != "b" || arr[2] != "c" {
		t.Errorf("arr = %v, want [a b c]", arr)
	}
}

func TestParseStringArray_Invalid(t *testing.T) {
	arr := parseStringArray("not-json")
	if arr != nil {
		t.Errorf("parseStringArray(invalid) should return nil, got %v", arr)
	}
}

func TestFilterClause_Empty(t *testing.T) {
	got := filterClause(nil)
	if got != "" {
		t.Errorf("filterClause(nil) = %q, want empty", got)
	}
}

func TestFilterClause_Single(t *testing.T) {
	got := filterClause([]string{"service = ?"})
	if got != "AND service = ?" {
		t.Errorf("filterClause = %q", got)
	}
}

func TestValidGroupByFields(t *testing.T) {
	valid := []string{"service", "operation", "status", "kind", "http.method", "http.status_code"}
	for _, f := range valid {
		if !validGroupByFields[f] {
			t.Errorf("validGroupByFields[%q] should be true", f)
		}
	}
	if validGroupByFields["invalid"] {
		t.Error("validGroupByFields[invalid] should be false")
	}
}

func TestSpans_ZeroWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(spanCols))

	result, err := svc.Spans(context.Background(), SpanParams{Window: 0})
	if err != nil {
		t.Fatalf("Spans(Window:0) error = %v", err)
	}
	if result == nil {
		t.Fatal("Spans(Window:0) returned nil")
	}
	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
}

func TestSpans_ZeroLimit(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(spanCols))

	result, err := svc.Spans(context.Background(), SpanParams{Limit: 0})
	if err != nil {
		t.Fatalf("Spans(Limit:0) error = %v", err)
	}
	if result == nil {
		t.Fatal("Spans(Limit:0) returned nil")
	}
	if len(result.Spans) != 0 {
		t.Errorf("Spans count = %d, want 0", len(result.Spans))
	}
}

func TestBuildSpanFilters_SpecialChars(t *testing.T) {
	p := SpanParams{Query: "50%_discount"}
	filters, args := buildSpanFilters(p)
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}
	// The args should contain escaped versions
	want := "%50\\%\\_discount%"
	if args[0] != want {
		t.Errorf("LIKE arg = %q, want %q", args[0], want)
	}
	// Second arg is same (for status_message ILIKE)
	if args[1] != want {
		t.Errorf("LIKE arg[1] = %q, want %q", args[1], want)
	}
}
