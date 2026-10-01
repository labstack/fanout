package query

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	engine "github.com/labstack/fanout/internal/duckdb"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestPinnedEngineReadsShreddedTelemetry(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 1}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var version string
	if err := d.DB.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != engine.Version {
		t.Fatalf("engine %s, expected %s", version, engine.Version)
	}
	nanos := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC).UnixNano()
	attrs := map[string]any{"dotted.key": int64(math.MaxInt64), "zero": int64(0), "false": false, "null": nil,
		"bytes": []byte{0, 255}, "nested": []any{int64(9), map[string]any{"ok": true}}, "messaging.system": "kafka", "nan": math.NaN()}
	resource := map[string]any{"service.name": "checkout", "service.namespace": "prod", "custom": int64(42)}
	if err := repository.Commit(ctx, telemetrystore.Batch{ID: "typed", Spans: []telemetry.Span{{Namespace: "prod", TraceID: "trace", SpanID: "span", StartUnixNanos: nanos, IngestedAt: nanos, Attributes: attrs, Resource: resource}},
		Logs:    []telemetry.Log{{Namespace: "prod", TimeUnixNanos: nanos, Attributes: attrs, Resource: resource}},
		Metrics: []telemetry.Metric{{Namespace: "prod", TimeUnixNanos: nanos, Attributes: attrs, Resource: resource}}}); err != nil {
		t.Fatal(err)
	}
	for _, relation := range []string{"spans", "logs", "metrics"} {
		resp := d.ExecuteSQL(ctx, SQLRequest{Query: "SELECT * FROM " + relation})
		if resp.Error != "" || len(resp.Results) != 1 {
			t.Fatalf("%s: %#v", relation, resp)
		}
		got := resp.Results[0]["attributes"].(map[string]any)
		if got["dotted.key"] != json.Number("9223372036854775807") || got["zero"] != json.Number("0") || got["false"] != false || got["null"] != nil {
			t.Fatalf("%s types: %#v", relation, got)
		}
		if _, err := json.Marshal(resp.Results); err != nil {
			t.Fatal(err)
		}
	}
	var extracted int64
	if err := d.DB.QueryRowContext(ctx, "SELECT attr(attributes,'dotted.key')::BIGINT FROM spans").Scan(&extracted); err != nil || extracted != math.MaxInt64 {
		t.Fatalf("integer extraction %d: %v", extracted, err)
	}
	var eventTime time.Time
	if err := d.DB.QueryRowContext(ctx, "SELECT start_time::TIMESTAMP_NS FROM spans").Scan(&eventTime); err != nil || eventTime.UnixNano() != nanos {
		t.Fatalf("nanosecond time %s: %v", eventTime, err)
	}
	indexed, _, err := d.Trace(ctx, telemetry.TraceQuery{TraceID: "trace", Namespace: "prod", StartNanos: nanos - 1, EndNanos: nanos + 1, Limit: 10})
	if err != nil || len(indexed) != 1 || indexed[0].StartUnixNanos != nanos {
		t.Fatalf("trace parity %#v: %v", indexed, err)
	}
	// Engine-level restrictions remain effective even outside public SQL AST
	// validation. Only telemetry and the rebuildable query directory are allowed.
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.ExecContext(ctx, "SELECT * FROM read_text("+sqlLiteral(secret)+")"); err == nil {
		t.Fatal("engine read outside allowed directories")
	}
	if _, err := d.DB.ExecContext(ctx, "SET enable_external_access=true"); err == nil {
		t.Fatal("engine policy was mutable")
	}
	if _, err := d.DB.ExecContext(ctx, "SET threads=1"); err != nil {
		t.Fatal("operator thread limit locked:", err)
	}
	// Nested nanosecond timezone types must never reach an unsupported Go vector.
	resp := d.ExecuteSQL(ctx, SQLRequest{Query: "SELECT [start_time::TIMESTAMPTZ_NS] AS times FROM spans"})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
}
