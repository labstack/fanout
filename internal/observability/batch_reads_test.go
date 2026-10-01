package observability

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func newCompletedReadTest(t *testing.T) (*query.Duck, *telemetrystore.Repository, *Service) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo, New(d, d, 30)
}
func warmCompletedReads(t *testing.T, d *query.Duck, want int) {
	t.Helper()
	for attempt := 0; attempt <= want; attempt++ {
		if _, err := d.RefreshReadCaches(t.Context()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == want {
			return
		}
		if count > want {
			t.Fatalf("cached %d batches, want %d", count, want)
		}
	}
	t.Fatal("cache backfill made no progress")
}
func TestCompletedDashboardReadsMatchColdAndLateData(t *testing.T) {
	d, repo, svc := newCompletedReadTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	commit := func(id string, offset int) {
		t.Helper()
		b := telemetrystore.Batch{ID: id}
		for i := range 8 {
			ns := "prod"
			if i%3 == 0 {
				ns = "other"
			}
			service := "api"
			if i%2 == 0 {
				service = "worker"
			}
			n := at.Add(time.Duration(offset+i*20) * time.Second).UnixNano()
			trace := fmt.Sprint("trace", i%3)
			status := "OK"
			if i == 5 {
				status = "ERROR"
			}
			b.Spans = append(b.Spans, telemetry.Span{Namespace: ns, ServiceName: service, TraceID: trace, SpanID: fmt.Sprint(id, i), StartUnixNanos: n, EndUnixNanos: n + int64((i+1)*1000000), DurationMS: float64(i + 1), StatusCode: status, HTTPMethod: "GET", HTTPRoute: "/test", IngestedAt: at.UnixNano()})
			sev := []string{"Error", "warn", "", "İ", "i"}[i%5]
			b.Logs = append(b.Logs, telemetry.Log{Namespace: ns, ServiceName: service, TimeUnixNanos: n, Severity: sev, Body: fmt.Sprint("event", i, " password=secret"), TraceID: trace, IngestedAt: at.UnixNano()})
			if i%3 == 1 {
				b.Logs = append(b.Logs, b.Logs[len(b.Logs)-1])
			}
		}
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	commit("first", 0)
	scope := Scope{Start: at.Add(10 * time.Second), End: at.Add(150 * time.Second), Namespace: "prod"}
	cold, _, err := svc.queryEndpoints(t.Context(), scope, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	logsCold, err := svc.Logs(t.Context(), scope, "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	warmCompletedReads(t, d, 1)
	warm, _, err := svc.queryEndpoints(t.Context(), scope, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	logsWarm, err := svc.Logs(t.Context(), scope, "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Fatalf("endpoints cold=%#v warm=%#v", cold, warm)
	}
	if !reflect.DeepEqual(logsCold.Data, logsWarm.Data) {
		t.Fatalf("logs cold=%#v warm=%#v", logsCold.Data, logsWarm.Data)
	}
	commit("late", 5)
	raw := New(SQLDB(d.DB), d, 30)
	for _, ns := range []string{"", "prod", "other"} {
		for _, service := range []string{"", "api", "worker"} {
			for _, window := range [][2]int{{-60, 300}, {10, 150}, {65, 95}} {
				selected := Scope{Namespace: ns, Start: at.Add(time.Duration(window[0]) * time.Second), End: at.Add(time.Duration(window[1]) * time.Second)}
				got, err := svc.Trace(t.Context(), selected, "", service, 100)
				if err != nil {
					t.Fatal(err)
				}
				want, err := raw.Trace(t.Context(), selected, "", service, 100)
				if err != nil {
					t.Fatal(err)
				}
				if got.Data.TraceID != want.Data.TraceID {
					t.Fatalf("candidate ns=%s service=%s window=%v: %s != %s", ns, service, window, got.Data.TraceID, want.Data.TraceID)
				}
				for _, severity := range []string{"", "error", "warn", "unspecified", "i", "İ"} {
					for _, search := range []string{"", "event5", "secret"} {
						got, err := svc.Logs(t.Context(), selected, service, severity, search, 100)
						if err != nil {
							t.Fatal(err)
						}
						want, err := raw.Logs(t.Context(), selected, service, severity, search, 100)
						if err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got.Data, want.Data) {
							t.Fatalf("logs ns=%s svc=%s window=%v severity=%s search=%s: %#v != %#v", ns, service, window, severity, search, got.Data, want.Data)
						}
					}
				}
			}
		}
	}
	warmCompletedReads(t, d, 2)
	got, err := svc.Trace(t.Context(), scope, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	want, err := raw.Trace(t.Context(), scope, "", "", 100)
	if err != nil || got.Data.TraceID != want.Data.TraceID {
		t.Fatalf("fully warm trace: %s != %s: %v", got.Data.TraceID, want.Data.TraceID, err)
	}
}

// When every cached file overlapping the window lies inside it, a trace whose
// other cached spans sit in a file outside the window is still a candidate:
// its in-window spans decide the ranking, as they do for the raw query.
func TestCompletedTraceKeepsTraceWithSpansOutsideWindow(t *testing.T) {
	d, repo, svc := newCompletedReadTest(t)
	raw := New(SQLDB(d.DB), d, 30)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	span := func(trace, id string, at time.Time, dur time.Duration, status string) telemetry.Span {
		return telemetry.Span{Namespace: "prod", ServiceName: "api", TraceID: trace, SpanID: id, StartUnixNanos: at.UnixNano(), EndUnixNanos: at.Add(dur).UnixNano(),
			DurationMS: float64(dur.Milliseconds()), StatusCode: status, HTTPMethod: "GET", HTTPRoute: "/x", IngestedAt: t0.UnixNano()}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "before", Spans: []telemetry.Span{span("T", "t1", t0.Add(-30*time.Second), time.Second, "OK")}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "inside", Spans: []telemetry.Span{
		span("T", "t2", t0.Add(10*time.Second), time.Second, "ERROR"),
		span("U", "u1", t0.Add(20*time.Second), 5*time.Second, "OK"),
	}}); err != nil {
		t.Fatal(err)
	}
	warmCompletedReads(t, d, 2)
	for _, service := range []string{"", "api"} {
		scope := Scope{Start: t0, End: t0.Add(time.Minute)}
		want, err := raw.Trace(t.Context(), scope, "", service, 100)
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.Trace(t.Context(), scope, "", service, 100)
		if err != nil {
			t.Fatal(err)
		}
		if want.Data.TraceID != "T" || got.Data.TraceID != want.Data.TraceID {
			t.Fatalf("service=%q: completed trace %q, raw %q, want T", service, got.Data.TraceID, want.Data.TraceID)
		}
	}
}

// Time-local batches put most window edges between files, so cached candidates
// must combine index rows with the in-window parts of partially covered traces.
func TestCompletedTraceMatchesRawAcrossTimeLocalBatches(t *testing.T) {
	d, repo, svc := newCompletedReadTest(t)
	raw := New(SQLDB(d.DB), d, 30)
	rng := rand.New(rand.NewPCG(11, 104729))
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	statuses := []string{"OK", "ERROR", "error", "UNSET", ""}
	commit := func(k int, ingest time.Time) {
		t.Helper()
		lo := t0.Add(-40*time.Minute + time.Duration(k)*37*time.Second)
		batch := telemetrystore.Batch{ID: fmt.Sprintf("local%03d", k)}
		for i := 0; i < 6; i++ {
			at := lo.Add(time.Duration(rng.Int64N(int64(37 * time.Second))))
			dur := time.Duration(rng.Int64N(int64(2 * time.Second)))
			batch.Spans = append(batch.Spans, telemetry.Span{Namespace: []string{"prod", "dev"}[rng.IntN(2)], ServiceName: []string{"api", "worker"}[rng.IntN(2)],
				TraceID: fmt.Sprintf("t%03d", k/2+rng.IntN(3)), SpanID: fmt.Sprintf("%s-%d", batch.ID, i), StartUnixNanos: at.UnixNano(), EndUnixNanos: at.Add(dur).UnixNano(),
				DurationMS: float64(dur) / 1e6, StatusCode: statuses[rng.IntN(len(statuses))], HTTPMethod: "GET", HTTPRoute: "/a", IngestedAt: ingest.UnixNano()})
		}
		if err := repo.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func() {
		t.Helper()
		warmCompletedReads(t, d, len(repo.Parquet.BatchMetadata()))
	}
	check := func(stage string) {
		t.Helper()
		var edges []time.Time
		for _, b := range repo.Parquet.BatchMetadata() {
			if b.SpanTime.Known {
				edges = append(edges, time.Unix(0, b.SpanTime.MinNanos).UTC(), time.Unix(0, b.SpanTime.MaxNanos).UTC())
			}
		}
		for i := 0; i < 8; i++ {
			start := edges[rng.IntN(len(edges))].Add(time.Duration(rng.Int64N(int64(2*time.Second))) - time.Second)
			if i%3 == 0 {
				start = t0.Add(-41*time.Minute + time.Duration(rng.Int64N(int64(42*time.Minute))))
			}
			end := start.Add(5*time.Second + time.Duration(rng.Int64N(int64(6*time.Minute))))
			for _, ns := range []string{"", "prod"} {
				for _, service := range []string{"", "api"} {
					scope := Scope{Namespace: ns, Start: start, End: end}
					want, err := raw.Trace(t.Context(), scope, "", service, 100)
					if err != nil {
						t.Fatal(err)
					}
					got, err := svc.Trace(t.Context(), scope, "", service, 100)
					if err != nil {
						t.Fatal(err)
					}
					if got.Data.TraceID != want.Data.TraceID {
						t.Fatalf("%s ns=%q service=%q [%s, %s): completed %q, raw %q", stage, ns, service,
							start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), got.Data.TraceID, want.Data.TraceID)
					}
				}
			}
		}
	}
	for k := 0; k < 24; k++ {
		commit(k, t0.Add(-2*time.Hour+time.Duration(k)*time.Second))
	}
	refresh()
	check("cached")
	for pass := 0; pass < 3; pass++ {
		if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n == 0 {
			t.Fatalf("compact pass %d: %d %v", pass, n, err)
		}
	}
	check("compacted before refresh")
	refresh()
	check("compacted")
	for k := 24; k < 28; k++ {
		commit(k, t0.Add(time.Duration(k)*time.Second))
	}
	refresh()
	if n, err := repo.PruneParquet(t.Context(), d, t0.Add(-time.Hour).UnixNano(), 100); err != nil || n == 0 {
		t.Fatalf("prune: %d %v", n, err)
	}
	check("pruned before refresh")
	refresh()
	check("pruned")
}
