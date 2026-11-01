package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

// fixtureStart is the start of every fixture window: twelve hours of data
// would be unnecessary; tests query 12:00–13:00 UTC on 2026-10-01.
var fixtureStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTestEngine(t *testing.T) (*engine.Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := engine.NewDuck(t.Context(), cfg, repo)
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
				StatusCode: status, Resource: map[string]any{"service.version": "v1"},
				Attributes: map[string]any{"http.route": route, "http.request.method": "GET", "customer.tier": "gold"}, IngestedAt: n,
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

// metricStore commits fixture metrics as real telemetry batches. Inserts
// buffer until the next read, so each read sees one batch per write burst.
type metricStore struct {
	t       *testing.T
	duck    *engine.Duck
	repo    *telemetrystore.Repository
	pending []telemetry.Metric
	batches int
}

func (s *metricStore) flush() {
	s.t.Helper()
	if len(s.pending) == 0 {
		return
	}
	s.batches++
	if err := s.repo.Commit(s.t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("metrics-%d", s.batches), Metrics: s.pending}); err != nil {
		s.t.Fatal(err)
	}
	s.pending = nil
}

// db is the engine's own handle, for oracle reads over every committed row.
func (s *metricStore) db() *sql.DB {
	s.flush()
	return s.duck.DB
}

// clear retires every committed batch through the engine's publication.
func (s *metricStore) clear() {
	s.t.Helper()
	s.pending = nil
	for {
		n, err := s.repo.PruneParquet(s.t.Context(), s.duck, math.MaxInt64, 1000)
		if err != nil {
			s.t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

// flushingEngine commits buffered fixture metrics before every read.
type flushingEngine struct {
	*engine.Duck
	store *metricStore
}

func (f flushingEngine) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	f.store.flush()
	return f.Duck.QueryContext(ctx, query, args...)
}

func metricFixture(t *testing.T) (*Executor, *metricStore) {
	t.Helper()
	d, repo := newTestEngine(t)
	store := &metricStore{t: t, duck: d, repo: repo}
	e := NewExecutor(flushingEngine{Duck: d, store: store}, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	return e, store
}

func insertMetrics(t *testing.T, s *metricStore, m telemetry.Metric) {
	t.Helper()
	m.SeriesID = telemetry.MetricSeriesID(m)
	s.pending = append(s.pending, m)
}

func metricCounter(at time.Time, value float64) telemetry.Metric {
	m := telemetry.Metric{Namespace: "test", ServiceName: "svc", Name: "net", Unit: "By",
		Type: "counter", Temporality: "cumulative", Value: value,
		TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano(),
		ScopeName: "scope", ScopeVersion: "1"}
	m.SeriesID = telemetry.MetricSeriesID(m)
	return m
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

// metricHistogram is an explicit histogram point; bounds and counts are JSON
// arrays so fixtures read like the buckets they describe.
func metricHistogram(at time.Time, bounds, counts string, count int64, sum float64, series string) telemetry.Metric {
	m := metricCounter(at, 0)
	m.Type, m.Value = "histogram", 0
	m.Histogram = telemetry.Histogram{Count: uint64(count), Sum: sum, HasSum: true, Bounds: []float64{}, Counts: []uint64{}}
	if err := json.Unmarshal([]byte(bounds), &m.Bounds); err != nil {
		panic(err)
	}
	if err := json.Unmarshal([]byte(counts), &m.Counts); err != nil {
		panic(err)
	}
	m.Attributes = map[string]any{"series": series}
	m.SeriesID = telemetry.MetricSeriesID(m)
	return m
}

// metricExponential is a delta exponential histogram point; its count is the
// sum of its buckets.
func metricExponential(at time.Time, h telemetry.Histogram, sum float64) telemetry.Metric {
	m := metricCounter(at, 0)
	m.Type, m.Temporality, m.Value = "histogram", "delta", 0
	h.Exponential, h.Sum, h.HasSum = true, sum, true
	h.PositiveCounts = append([]uint64{}, h.PositiveCounts...)
	h.NegativeCounts = append([]uint64{}, h.NegativeCounts...)
	h.Count = h.ZeroCount
	for _, n := range slices.Concat(h.PositiveCounts, h.NegativeCounts) {
		h.Count += n
	}
	m.Histogram = h
	m.Attributes = map[string]any{"series": "a"}
	return m
}

// Direct Parquet fixtures finalize their identity just like OTLP ingest.
func metricSeriesIDs(metrics []telemetry.Metric) []telemetry.Metric {
	for i := range metrics {
		metrics[i].SeriesID = telemetry.MetricSeriesID(metrics[i])
	}
	return metrics
}
