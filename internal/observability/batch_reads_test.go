package observability

import (
	"fmt"
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
			sev := []string{"Error", "warn", ""}[i%3]
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
				for _, severity := range []string{"", "error", "warn", "unspecified"} {
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
