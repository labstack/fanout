package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestTelemetrySQLRejectsCatalogMacrosAndSideEffects(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	for name, args := range map[string]string{
		"current_query": "", "current_query_id": "", "pg_get_viewdef": "22636",
		"pg_get_constraintdef": "1", "format_type": "1, NULL",
		"get_block_size": "current_database()", "write_log": "'boundary probe'",
	} {
		for _, spelling := range []string{name, strings.ToUpper(name), `"` + name + `"`, "main." + name, `"main"."` + strings.ToUpper(name) + `"`, "telemetry." + name, "memory.main." + name} {
			t.Run(spelling, func(t *testing.T) {
				statement := fmt.Sprintf("SELECT %s(%s)", spelling, args)
				_, _, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10)
				if err == nil || !strings.Contains(err.Error(), "not available to telemetry SQL") {
					t.Fatalf("function boundary for %s: %v", statement, err)
				}
				response := d.ExecuteSQL(t.Context(), SQLRequest{Query: statement})
				if !strings.Contains(response.Error, "not available to telemetry SQL") {
					t.Fatalf("direct SQL function boundary: %+v", response)
				}
			})
		}
	}
}

func TestSQLRenderingPreservesExactNumbersAndSamples(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	for _, test := range []struct {
		name, statement string
		want            int64
	}{
		{"integer", "SELECT 9007199254740993 AS n", 9007199254740993},
		{"nanoseconds", "SELECT epoch_ns(make_timestamp_ns(1791370800000000001)) AS n", 1791370800000000001},
		{"query_sample", "SELECT count(*) AS n FROM range(10) USING SAMPLE 100 PERCENT (bernoulli)", 10},
		{"table_sample", "SELECT count(*) AS n FROM range(10) TABLESAMPLE 100 PERCENT (bernoulli)", 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			node, err := d.ParseSQL(t.Context(), test.statement)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := d.RenderSQL(t.Context(), node)
			if err != nil {
				t.Fatal(err)
			}
			prepared, _, err := d.PrepareTelemetrySQL(t.Context(), rendered, rendered, 10)
			if err != nil {
				t.Fatal(err)
			}
			var got int64
			if err := d.QueryRowScan(t.Context(), []any{&got}, prepared); err != nil || got != test.want {
				t.Fatalf("executed %s: %d, %v; want %d", rendered, got, err, test.want)
			}
		})
	}
}

func TestSnapshotExecutionMatchesDescribedColumnBindings(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "bindings", Logs: []telemetry.Log{{Body: "fixture body", TimeUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	ctx := queryrows.WithWindow(t.Context(), queryrows.Window{Start: at, End: at.Add(time.Hour)})
	t.Run("struct", func(t *testing.T) {
		statement := "SELECT telemetry.logs.body AS b FROM (SELECT {'body': 42} AS logs) AS telemetry, logs"
		var name, logicalType string
		var rest [4]any
		if err := d.DB.QueryRowContext(t.Context(), "DESCRIBE "+statement).Scan(&name, &logicalType, &rest[0], &rest[1], &rest[2], &rest[3]); err != nil {
			t.Fatal(err)
		}
		if logicalType != "INTEGER" {
			t.Fatalf("described type = %s", logicalType)
		}
		prepared, _, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := d.QueryContext(ctx, prepared)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got any
		if !rows.Next() {
			t.Fatalf("missing row: %v", rows.Err())
		}
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != int32(42) {
			t.Fatalf("described INTEGER 42, executed %T %v", got, got)
		}
	})
	for _, statement := range []string{
		"SELECT main.logs.body AS b FROM main.logs",
		"SELECT main.logs.body AS b FROM logs",
		"SELECT telemetry.logs.body AS b FROM telemetry.logs",
	} {
		t.Run(statement, func(t *testing.T) {
			response := d.ExecuteSQL(t.Context(), SQLRequest{Query: statement})
			if response.Error != "" || len(response.Results) != 1 || response.Results[0]["b"] != "fixture body" {
				t.Fatalf("direct SQL: %+v", response)
			}
			prepared, _, err := d.PrepareTelemetrySQL(t.Context(), statement, statement, 10)
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{statement, prepared} {
				var body string
				if err := d.QueryRowScan(ctx, []any{&body}, query); err != nil || body != "fixture body" {
					t.Fatalf("snapshot SQL: %q %v", body, err)
				}
			}
		})
	}
}

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
