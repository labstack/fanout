package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// logCols is the column set returned by the ungrouped logs query.
var logCols = []string{"time", "service", "severity", "body", "trace_id", "span_id", "attributes_json"}

func TestLogs_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(logCols))

	result, err := svc.Logs(context.Background(), LogParams{})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if result == nil {
		t.Fatal("Logs() returned nil")
		return
	}
	if len(result.Logs) != 0 {
		t.Errorf("Logs count = %d, want 0", len(result.Logs))
	}
	if result.TotalMatched != 0 {
		t.Errorf("TotalMatched = %d, want 0", result.TotalMatched)
	}
}

func TestLogs_DefaultParams(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(logCols))

	result, err := svc.Logs(context.Background(), LogParams{})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if result == nil {
		t.Fatal("Logs() returned nil")
	}
}

func TestLogs_OneRow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(logCols).AddRow(
			"2026-03-14T17:01:23Z", "payment", "ERROR",
			"Connection pool exhausted, all 10 connections in use",
			"abc123", "span-012", `{"db.system":"postgresql"}`))

	result, err := svc.Logs(context.Background(), LogParams{})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if len(result.Logs) != 1 {
		t.Fatalf("Logs count = %d, want 1", len(result.Logs))
	}
	l := result.Logs[0]
	if l.Time != "2026-03-14T17:01:23Z" {
		t.Errorf("Time = %q, want 2026-03-14T17:01:23Z", l.Time)
	}
	if l.Service != "payment" {
		t.Errorf("Service = %q, want payment", l.Service)
	}
	if l.Severity != "ERROR" {
		t.Errorf("Severity = %q, want ERROR", l.Severity)
	}
	if l.Body != "Connection pool exhausted, all 10 connections in use" {
		t.Errorf("Body = %q, unexpected", l.Body)
	}
	if l.TraceID != "abc123" {
		t.Errorf("TraceID = %q, want abc123", l.TraceID)
	}
	if l.SpanID != "span-012" {
		t.Errorf("SpanID = %q, want span-012", l.SpanID)
	}
	if l.Attributes["db.system"] != "postgresql" {
		t.Errorf("Attributes[db.system] = %q, want postgresql", l.Attributes["db.system"])
	}
}

func TestLogs_NullFields(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(logCols).AddRow(
			"2026-03-14T10:00:00Z", "svc", "INFO", "hello", nil, nil, nil))

	result, err := svc.Logs(context.Background(), LogParams{})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if len(result.Logs) != 1 {
		t.Fatalf("Logs count = %d, want 1", len(result.Logs))
	}
	l := result.Logs[0]
	if l.TraceID != "" {
		t.Errorf("TraceID should be empty, got %q", l.TraceID)
	}
	if l.SpanID != "" {
		t.Errorf("SpanID should be empty, got %q", l.SpanID)
	}
	if l.Attributes != nil {
		t.Errorf("Attributes should be nil when attributes_json is NULL")
	}
}

func TestLogs_HasMore(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	// Return 11 rows with limit=10 → hasMore
	rows := sqlmock.NewRows(logCols)
	for i := 0; i < 11; i++ {
		rows.AddRow("2026-03-14T10:00:00Z", "svc", "INFO", "msg", nil, nil, nil)
	}
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Logs(context.Background(), LogParams{Limit: 10})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if len(result.Logs) != 10 {
		t.Errorf("Logs count = %d, want 10", len(result.Logs))
	}
	if result.TotalMatched != 11 {
		t.Errorf("TotalMatched = %d, want 11", result.TotalMatched)
	}
}

func TestLogs_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	result, err := svc.Logs(context.Background(), LogParams{})
	if err == nil {
		t.Fatal("Logs() should return error on query failure")
	}
	if !strings.Contains(err.Error(), "db error") {
		t.Errorf("error should contain 'db error', got: %v", err)
	}
	if result == nil {
		t.Fatal("Logs() should return non-nil result even on error")
	}
}

func TestLogs_InvalidGroupBy(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.Logs(context.Background(), LogParams{
		GroupBy: []string{"invalid_field"},
	})
	if err == nil {
		t.Fatal("Logs() should return error for invalid group_by field")
	}
	if !strings.Contains(err.Error(), "invalid group_by field") {
		t.Errorf("error should mention invalid group_by field, got: %v", err)
	}
}

func TestBuildLogFilters_Query(t *testing.T) {
	filters, args := buildLogFilters(LogParams{Query: "error"})
	if len(filters) != 1 {
		t.Errorf("expected 1 filter, got %d", len(filters))
	}
	if len(args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(args))
	}
	if args[0] != "%error%" {
		t.Errorf("arg = %q, want %%error%%", args[0])
	}
}

func TestBuildLogFilters_Severity(t *testing.T) {
	filters, args := buildLogFilters(LogParams{Severity: []string{"ERROR", "WARN"}})
	if len(filters) != 1 {
		t.Errorf("expected 1 filter, got %d", len(filters))
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args (one per severity), got %d", len(args))
	}
}

func TestBuildLogFilters_TraceID(t *testing.T) {
	filters, args := buildLogFilters(LogParams{TraceID: "abc123"})
	if len(filters) != 1 {
		t.Errorf("expected 1 filter, got %d", len(filters))
	}
	if args[0] != "abc123" {
		t.Errorf("arg = %q, want abc123", args[0])
	}
}

func TestBuildLogFilters_Service(t *testing.T) {
	filters, args := buildLogFilters(LogParams{Service: "payment"})
	if len(filters) != 1 {
		t.Errorf("expected 1 filter, got %d", len(filters))
	}
	if args[0] != "payment" {
		t.Errorf("arg = %q, want payment", args[0])
	}
}

func TestBuildLogFilters_Attrs(t *testing.T) {
	filters, args := buildLogFilters(LogParams{
		Attrs: map[string]string{"db.system": "postgresql"},
	})
	if len(filters) != 1 {
		t.Errorf("expected 1 filter, got %d", len(filters))
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args (key + val), got %d", len(args))
	}
}

func TestBuildLogFilters_Empty(t *testing.T) {
	filters, args := buildLogFilters(LogParams{})
	if len(filters) != 0 {
		t.Errorf("expected 0 filters, got %d", len(filters))
	}
	if len(args) != 0 {
		t.Errorf("expected 0 args, got %d", len(args))
	}
}

func TestLogOrderByClause(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"time", "time DESC"},
		{"", "time DESC"},
		{"count", "time DESC"},
		{"severity", "severity ASC, time DESC"},
	}
	for _, c := range cases {
		got := logOrderByClause(c.in)
		if got != c.want {
			t.Errorf("logOrderByClause(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLogGroupOrderByClause(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"count", "count DESC"},
		{"", "count DESC"},
		{"severity", "severity ASC"},
	}
	for _, c := range cases {
		got := logGroupOrderByClause(c.in)
		if got != c.want {
			t.Errorf("logGroupOrderByClause(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidLogGroupByFields(t *testing.T) {
	valid := []string{"service", "severity"}
	for _, f := range valid {
		if !validLogGroupByFields[f] {
			t.Errorf("validLogGroupByFields[%q] should be true", f)
		}
	}
	if validLogGroupByFields["invalid"] {
		t.Error("validLogGroupByFields[invalid] should be false")
	}
	if validLogGroupByFields["operation"] {
		t.Error("validLogGroupByFields[operation] should be false for logs")
	}
}

// Grouped path tests

var logGroupCols = []string{"service", "count", "sample_bodies", "sample_trace_ids"}

func TestLogs_GroupBy_Service(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(logGroupCols).AddRow(
			"payment", int64(23),
			`["Connection pool exhausted...","DB timeout"]`,
			`["abc123","def456"]`))

	result, err := svc.Logs(context.Background(), LogParams{
		GroupBy: []string{"service"},
	})
	if err != nil {
		t.Fatalf("Logs() grouped error = %v", err)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups count = %d, want 1", len(result.Groups))
	}
	g := result.Groups[0]
	if g.Key["service"] != "payment" {
		t.Errorf("Key[service] = %q, want payment", g.Key["service"])
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
	if g.SampleTraceIDs[0] != "abc123" {
		t.Errorf("SampleTraceIDs[0] = %q, want abc123", g.SampleTraceIDs[0])
	}
}

func TestLogs_GroupBy_Severity(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"severity", "count", "sample_bodies", "sample_trace_ids"}).AddRow(
			"ERROR", int64(100),
			`["err1","err2"]`,
			`["t1"]`))

	result, err := svc.Logs(context.Background(), LogParams{
		GroupBy: []string{"severity"},
	})
	if err != nil {
		t.Fatalf("Logs() grouped error = %v", err)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups count = %d, want 1", len(result.Groups))
	}
	if result.Groups[0].Key["severity"] != "ERROR" {
		t.Errorf("Key[severity] = %q, want ERROR", result.Groups[0].Key["severity"])
	}
}

func TestLogs_GroupBy_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(logGroupCols))

	result, err := svc.Logs(context.Background(), LogParams{
		GroupBy: []string{"service"},
	})
	if err != nil {
		t.Fatalf("Logs() grouped error = %v", err)
	}
	if len(result.Groups) != 0 {
		t.Errorf("Groups count = %d, want 0", len(result.Groups))
	}
}

func TestLogs_GroupBy_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("group error"))

	result, err := svc.Logs(context.Background(), LogParams{
		GroupBy: []string{"service"},
	})
	if err == nil {
		t.Fatal("Logs() grouped should return error on query failure")
	}
	if !strings.Contains(err.Error(), "group error") {
		t.Errorf("error should contain 'group error', got: %v", err)
	}
	if result == nil {
		t.Fatal("Logs() should return non-nil result even on error")
	}
}

func TestLogs_GroupBy_NullArrays(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows(logGroupCols).AddRow(
			"api", int64(5), nil, nil))

	result, err := svc.Logs(context.Background(), LogParams{
		GroupBy: []string{"service"},
	})
	if err != nil {
		t.Fatalf("Logs() grouped error = %v", err)
	}
	if len(result.Groups) != 1 {
		t.Fatalf("Groups count = %d, want 1", len(result.Groups))
	}
	g := result.Groups[0]
	if g.SampleBodies == nil {
		t.Error("SampleBodies should not be nil (should default to empty slice)")
	}
	if g.SampleTraceIDs == nil {
		t.Error("SampleTraceIDs should not be nil (should default to empty slice)")
	}
}
