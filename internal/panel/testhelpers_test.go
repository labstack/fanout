package panel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/query/querytest"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// fixtureStart is the start of every fixture window: twelve hours of data
// would be unnecessary; tests query 12:00–13:00 UTC on 2026-10-01.
var fixtureStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTestEngine(t *testing.T) (*query.Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo
}

var batchSeq int

func commit(t *testing.T, repo *telemetrystore.Repository, spans []telemetry.Span, logs []telemetry.Log) {
	t.Helper()
	batchSeq++
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("fixture-%d", batchSeq), Spans: spans, Logs: logs}); err != nil {
		t.Fatal(err)
	}
}

// shopSpans is one minute-spaced server span per (service, route) for an
// hour: checkout /cart (slow after 12:30, with errors), checkout /quote, and
// frontend /api/cart.
func shopSpans() []telemetry.Span {
	var out []telemetry.Span
	for minute := range 60 {
		at := fixtureStart.Add(time.Duration(minute) * time.Minute)
		add := func(service, route string, ms float64, status string) {
			n := at.UnixNano()
			out = append(out, telemetry.Span{
				Namespace: "shop", ServiceName: service, TraceID: fmt.Sprintf("%s-%s-%d", service, route, minute), SpanID: fmt.Sprintf("%s-%d", route, minute),
				Name: "GET " + route, Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + int64(ms*1e6), DurationMS: ms,
				StatusCode: status, HTTPMethod: "GET", HTTPRoute: route, ServiceVersion: "v1",
				Attributes: map[string]any{"http.route": route, "customer.tier": "gold"}, IngestedAt: n,
			})
		}
		slow, status := 100.0, "STATUS_CODE_OK"
		if minute >= 30 {
			slow = 900
			if minute%5 == 0 {
				status = "STATUS_CODE_ERROR"
			}
		}
		add("checkout", "/cart", slow, status)
		add("checkout", "/quote", 40, "STATUS_CODE_OK")
		add("frontend", "/api/cart", 20, "STATUS_CODE_OK")
	}
	return out
}

func metricFixture(t *testing.T) (*Executor, *sql.DB) {
	t.Helper()
	db, err := sql.Open("duckdb", "?threads=2&memory_limit=256MB")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("SET TimeZone='UTC'"); err != nil {
		t.Fatal(err)
	}
	if err := querytest.CreateTables(db, query.CreateCacheTables); err != nil {
		t.Fatal(err)
	}
	if err := query.CreateViews(db); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(&query.Duck{DB: db}, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	return e, db
}

func insertMetrics(t *testing.T, db *sql.DB, m telemetry.Metric) {
	t.Helper()
	encode := func(v map[string]any) string {
		if v == nil {
			return "{}"
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	_, err := db.Exec(`INSERT INTO telemetry.metrics
      (namespace,service,name,unit,metric_type,temporality,monotonic,start_time_unix_nano,
       metric_time,time_unix_nano,value,hist_bounds_json,hist_counts_json,hist_count,hist_sum,
       scope_name,scope_version,attributes,resource,ingested_at,ingested_unix_nano)
      VALUES (?,?,?,?,?,?,?,?,?::TIMESTAMP_NS,?,?,?,?,?,?,?, ?,?::JSON::VARIANT,?::JSON::VARIANT,?::TIMESTAMP_NS,?)`,
		m.Namespace, m.ServiceName, m.Name, m.Unit, m.Type, m.Temporality, m.Monotonic, m.StartTimeUnixNanos,
		time.Unix(0, m.TimeUnixNanos).UTC(), m.TimeUnixNanos, m.Value, m.HistBoundsJSON, m.HistCountsJSON, m.HistCount, m.HistSum,
		m.ScopeName, m.ScopeVersion, encode(m.Attributes), encode(m.Resource), time.Unix(0, m.IngestedAt).UTC(), m.IngestedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func metricCounter(at time.Time, value float64) telemetry.Metric {
	return telemetry.Metric{Namespace: "test", ServiceName: "svc", Name: "net", Unit: "By",
		Type: "sum", Monotonic: true, Temporality: "cumulative", Value: value,
		TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano(),
		ScopeName: "scope", ScopeVersion: "1"}
}

func runMetricPanel(t *testing.T, e *Executor, p Panel, start, end time.Time) Result {
	t.Helper()
	e.now = func() time.Time { return end }
	if p.ID == "" {
		p.ID = "metric"
	}
	if p.Title == "" {
		p.Title = "Metric"
	}
	results, err := e.Run(t.Context(), RunRequest{Dashboard: Dashboard{
		Name: "Metrics", Time: Time{From: &start, To: &end}, Panels: []Panel{p},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status == StatusError {
		t.Fatalf("result: %+v", results)
	}
	return results[0]
}

func metricValues(t *testing.T, r Result, column string) []any {
	t.Helper()
	if r.Frame == nil {
		t.Fatalf("missing frame: %+v", r)
	}
	for i, c := range r.Frame.Columns {
		if c.Name == column {
			return r.Frame.Values[i]
		}
	}
	t.Fatalf("missing column %s", column)
	return nil
}

func metricHistogram(at time.Time, bounds, counts string, count int64, sum float64, series string) telemetry.Metric {
	m := metricCounter(at, 0)
	m.Type, m.Monotonic = "histogram", false
	m.HistBoundsJSON, m.HistCountsJSON, m.HistCount, m.HistSum = bounds, counts, count, sum
	m.Attributes = map[string]any{"series": series}
	return m
}
