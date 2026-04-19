package mcp

import (
	"testing"
)

func TestBuildSchemaResponse(t *testing.T) {
	schema := buildSchemaResponse()

	if schema == nil {
		t.Fatal("buildSchemaResponse() returned nil")
		return
	}

	// Views
	if len(schema.Views) != 3 {
		t.Errorf("Views count = %d, want 3", len(schema.Views))
	}
	viewNames := map[string]bool{}
	for _, v := range schema.Views {
		viewNames[v.Name] = true
		if len(v.Columns) == 0 {
			t.Errorf("view %q has no columns", v.Name)
		}
	}
	for _, name := range []string{"spans", "logs", "metrics"} {
		if !viewNames[name] {
			t.Errorf("missing view %q", name)
		}
	}

	// Rollup tables
	if len(schema.RollupTables) != 2 {
		t.Errorf("RollupTables count = %d, want 2", len(schema.RollupTables))
	}
	rollupNames := map[string]bool{}
	for _, rt := range schema.RollupTables {
		rollupNames[rt.Name] = true
		if len(rt.Columns) == 0 {
			t.Errorf("rollup table %q has no columns", rt.Name)
		}
	}
	for _, name := range []string{"service_rollup", "edge_rollup"} {
		if !rollupNames[name] {
			t.Errorf("missing rollup table %q", name)
		}
	}

	// Macros
	if len(schema.Macros) == 0 {
		t.Error("Macros should not be empty")
	}
	for _, m := range schema.Macros {
		if m.Name == "" {
			t.Error("macro Name should not be empty")
		}
		if m.Signature == "" {
			t.Errorf("macro %q Signature should not be empty", m.Name)
		}
		if m.Description == "" {
			t.Errorf("macro %q Description should not be empty", m.Name)
		}
	}

	// Examples
	if len(schema.Examples) == 0 {
		t.Error("Examples should not be empty")
	}
	for _, ex := range schema.Examples {
		if ex.Title == "" {
			t.Error("example Title should not be empty")
		}
		if ex.SQL == "" {
			t.Errorf("example %q SQL should not be empty", ex.Title)
		}
	}
}

func TestSchemaResponseSpansColumns(t *testing.T) {
	schema := buildSchemaResponse()

	var spansView *ViewSchema
	for i := range schema.Views {
		if schema.Views[i].Name == "spans" {
			spansView = &schema.Views[i]
			break
		}
	}
	if spansView == nil {
		t.Fatal("spans view not found")
		return
	}

	requiredCols := []string{
		"trace_id", "span_id", "service", "operation",
		"duration_ms", "start_time", "status",
		"attributes_json", "resource_json", "events_json",
	}
	colMap := map[string]bool{}
	for _, c := range spansView.Columns {
		colMap[c.Name] = true
		if c.Type == "" {
			t.Errorf("column %q missing type", c.Name)
		}
		if c.Description == "" {
			t.Errorf("column %q missing description", c.Name)
		}
	}
	for _, col := range requiredCols {
		if !colMap[col] {
			t.Errorf("spans view missing column %q", col)
		}
	}
}

func TestSchemaResponseServiceRollupColumns(t *testing.T) {
	schema := buildSchemaResponse()

	var rollup *ViewSchema
	for i := range schema.RollupTables {
		if schema.RollupTables[i].Name == "service_rollup" {
			rollup = &schema.RollupTables[i]
			break
		}
	}
	if rollup == nil {
		t.Fatal("service_rollup not found")
		return
	}

	requiredCols := []string{"namespace", "bucket", "service", "spans", "error_rate", "p50_ms", "p95_ms", "log_count", "metric_count"}
	colMap := map[string]bool{}
	for _, c := range rollup.Columns {
		colMap[c.Name] = true
	}
	for _, col := range requiredCols {
		if !colMap[col] {
			t.Errorf("service_rollup missing column %q", col)
		}
	}
}

func TestSchemaResponseEdgeRollupColumns(t *testing.T) {
	schema := buildSchemaResponse()

	var rollup *ViewSchema
	for i := range schema.RollupTables {
		if schema.RollupTables[i].Name == "edge_rollup" {
			rollup = &schema.RollupTables[i]
			break
		}
	}
	if rollup == nil {
		t.Fatal("edge_rollup not found")
		return
	}

	requiredCols := []string{"namespace", "bucket", "caller", "callee", "calls", "avg_ms", "error_rate", "edge_type"}
	colMap := map[string]bool{}
	for _, c := range rollup.Columns {
		colMap[c.Name] = true
	}
	for _, col := range requiredCols {
		if !colMap[col] {
			t.Errorf("edge_rollup missing column %q", col)
		}
	}
}

func TestQueryInFields(t *testing.T) {
	// Zero value
	in := QueryIn{}
	if in.SQL != "" {
		t.Errorf("SQL default should be empty, got %q", in.SQL)
	}
	if in.Explain {
		t.Error("Explain default should be false")
	}
	if in.MaxRows != 0 {
		t.Errorf("MaxRows default should be 0, got %d", in.MaxRows)
	}
	if in.TimeoutMs != 0 {
		t.Errorf("TimeoutMs default should be 0, got %d", in.TimeoutMs)
	}

	// Explicit values
	in2 := QueryIn{
		SQL:       "SELECT 1",
		Explain:   true,
		MaxRows:   500,
		TimeoutMs: 10000,
	}
	if in2.SQL != "SELECT 1" {
		t.Errorf("SQL = %q, want SELECT 1", in2.SQL)
	}
	if !in2.Explain {
		t.Error("Explain should be true")
	}
	if in2.MaxRows != 500 {
		t.Errorf("MaxRows = %d, want 500", in2.MaxRows)
	}
	if in2.TimeoutMs != 10000 {
		t.Errorf("TimeoutMs = %d, want 10000", in2.TimeoutMs)
	}
}

func TestQueryOutFields(t *testing.T) {
	// Schema-only response
	out := QueryOut{
		Schema: buildSchemaResponse(),
	}
	if out.Schema == nil {
		t.Error("Schema should not be nil")
	}
	if out.QueryPlan != "" {
		t.Errorf("QueryPlan should be empty, got %q", out.QueryPlan)
	}

	// Explain response
	out2 := QueryOut{
		QueryPlan:  "PhysicalTableScan",
		ExecTimeMs: 5,
	}
	if out2.QueryPlan == "" {
		t.Error("QueryPlan should not be empty")
	}
	if out2.Schema != nil {
		t.Error("Schema should be nil for explain response")
	}
}
