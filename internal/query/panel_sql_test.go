package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestParseSQLReturnsTheStatementNode(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	node, err := d.ParseSQL(t.Context(), "SELECT 1 FROM spans WHERE service = $service")
	if err != nil {
		t.Fatal(err)
	}
	where, _ := node["where_clause"].(map[string]any)
	if where["class"] != "COMPARISON" {
		t.Fatalf("where = %v", where)
	}
	if _, err := d.ParseSQL(t.Context(), "SELECT 1; SELECT 2"); err == nil {
		t.Fatal("two statements accepted")
	}
	if _, err := d.ParseSQL(t.Context(), "SELECT * FROM"); err == nil || !strings.Contains(err.Error(), "parsing failed") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestPrepareTelemetrySQLGuardsAndProjects(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	batch := telemetrystore.Batch{ID: "panel-sql"}
	for i := range 4 {
		n := at.Add(time.Duration(i) * time.Minute).UnixNano()
		batch.Spans = append(batch.Spans, telemetry.Span{Namespace: "shop", ServiceName: "checkout", TraceID: "t", SpanID: string(rune('a' + i)), Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + 1e6, DurationMS: float64(i + 1), StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"http.route": "/cart"}, IngestedAt: n})
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		"SELECT * FROM read_csv('/etc/passwd')",
		"SELECT * FROM duckdb_settings()",
		"INSERT INTO spans SELECT * FROM spans",
	} {
		if _, _, err := d.PrepareTelemetrySQL(t.Context(), bad, bad, 10); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}

	okQuery := "SELECT 1 FROM spans WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS"
	for describe, want := range map[string]string{
		"SELECT * FROM read_csv('/etc/passwd')": "not available to telemetry SQL",
		"SELECT 1; SELECT 2":                    "exactly one SELECT statement is required",
	} {
		_, _, err := d.PrepareTelemetrySQL(t.Context(), okQuery, describe, 10)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("describe %q error = %v, want %q", describe, err, want)
		}
	}

	query := "SELECT service, attributes, duration_ms FROM spans WHERE start_time >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS ORDER BY duration_ms"
	describe := strings.ReplaceAll(query, "?", "NULL")
	prepared, serialized, err := d.PrepareTelemetrySQL(t.Context(), query, describe, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(serialized) != 3 || serialized[0] || !serialized[1] || serialized[2] {
		t.Fatalf("serialized = %v", serialized)
	}
	ctx := queryrows.WithWindow(context.Background(), queryrows.Window{Start: at, End: at.Add(time.Hour)})
	rows, err := d.QueryContext(ctx, prepared, at, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var service, attributes string
	var duration float64
	if !rows.Next() {
		t.Fatal("no rows")
	}
	if err := rows.Scan(&service, &attributes, &duration); err != nil {
		t.Fatal(err)
	}
	if service != "checkout" || duration != 1 || !strings.Contains(attributes, `"http.route"`) {
		t.Fatalf("row = %q %q %v", service, attributes, duration)
	}
}

func TestRenderSQLReturnsCanonicalText(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	node, err := d.ParseSQL(t.Context(), "SELECT 1 FROM spans WHERE body = $$it's$$ AND service = $service -- c")
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.RenderSQL(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "'it''s'") || !strings.Contains(out, "$service") || strings.Contains(out, "--") {
		t.Fatalf("rendered %q", out)
	}
	if _, err := d.ParseSQL(t.Context(), out); err != nil {
		t.Fatalf("rendering does not re-parse: %v", err)
	}
}
