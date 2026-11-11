package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
)

func TestSQLBoundaryPreservesResidualTypesUnderShreddedKeys(t *testing.T) {
	testResidualSerialization(t, false)
}
func TestSQLBoundaryResidualTypesWithoutShreddedStrings(t *testing.T) {
	testResidualSerialization(t, true)
}
func testResidualSerialization(t *testing.T, noStrings bool) {
	ctx := t.Context()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 1}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	values := []any{"5", int64(5), false, true, 1.25, map[string]any{"n": int64(9007199254740993), "b": false}, []any{int64(9), true, "s"}, nil, int64(9007199254740993)}
	if noStrings {
		values = values[1:]
	}
	attrs := make([]map[string]any, 4098)
	resources := make([]map[string]any, 4098)
	spans := make([]telemetry.Span, 4098)
	for i := range spans {
		attrs[i] = map[string]any{"messaging.system": values[i%len(values)], "messaging.destination.name": values[(i+1)%len(values)], "ordinary": values[i%len(values)]}
		resources[i] = map[string]any{"service.name": values[i%len(values)], "service.namespace": values[(i+1)%len(values)], "service.version": values[(i+2)%len(values)], "deployment.environment.name": values[(i+3)%len(values)]}
		spans[i] = telemetry.Span{TraceID: "trace", SpanID: fmt.Sprintf("%05d", i), Attributes: attrs[i], Resource: resources[i]}
	}
	if err := repo.Commit(ctx, telemetrystore.Batch{ID: "mismatch", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	window := queryrows.WithWindow(ctx, queryrows.Window{Start: time.Unix(0, 0), End: time.Now().Add(time.Hour)})
	norm := func(v any) any {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		var result any
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.UseNumber()
		if e = d.Decode(&result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	for _, surface := range []string{"spans", "telemetry.spans"} {
		for _, filter := range []string{"", " WHERE span_id='00001'"} {
			for _, shape := range []string{"*", "span_id,attributes,resource", "span_id,{'a':attributes,'r':resource} AS nested", "span_id,[attributes,resource] AS nested", "span_id,map(['a','r'],[attributes,resource]) AS nested"} {
				t.Run(surface+filter+shape, func(t *testing.T) {
					d, e := NewDuck(ctx, cfg, repo)
					if e != nil {
						t.Fatal(e)
					}
					defer d.Close()
					rows := mustRunTelemetrySQL(t, window, d, "SELECT "+shape+" FROM "+surface+filter+" ORDER BY span_id", 5000)
					count := len(spans)
					if filter != "" {
						count = 1
					}
					if len(rows) != count {
						t.Fatalf("rows %d want %d", len(rows), count)
					}
					for _, row := range rows {
						var i int
						if _, e := fmt.Sscanf(row["span_id"].(string), "%d", &i); e != nil {
							t.Fatal(e)
						}
						if strings.Contains(shape, " AS nested") {
							var want any = map[string]any{"a": attrs[i], "r": resources[i]}
							if strings.Contains(shape, "[attributes,resource] AS") {
								want = []any{attrs[i], resources[i]}
							}
							if !reflect.DeepEqual(jsonValue(t, row["nested"]), norm(want)) {
								t.Fatalf("row %d nested got %v want %#v", i, row["nested"], norm(want))
							}
						} else {
							for k, want := range map[string]any{"attributes": attrs[i], "resource": resources[i]} {
								if !reflect.DeepEqual(jsonValue(t, row[k]), norm(want)) {
									t.Fatalf("row %d %s got %#v want %#v", i, k, row[k], norm(want))
								}
							}
						}
					}
				})
			}
		}
	}
}
