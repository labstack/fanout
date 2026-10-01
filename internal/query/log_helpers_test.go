package query

import (
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestLogHelpersReadNativeParquetTimestamps(t *testing.T) {
	ctx := t.Context()
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
	recent := time.Now().UTC().Add(-time.Minute)
	batch := telemetrystore.Batch{ID: "log-helpers"}
	for _, at := range []time.Time{recent, recent.Add(time.Second), recent.Add(-time.Hour)} {
		batch.Logs = append(batch.Logs, telemetry.Log{Namespace: "prod", ServiceName: "checkout", Severity: "ERROR", Body: "checkout failed", TimeUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano()})
	}
	if err := repository.Commit(ctx, batch); err != nil {
		t.Fatal(err)
	}
	samples, err := d.LogsSamples(ctx, 5, 10, ".*failed.*")
	if err != nil || len(samples) != 2 {
		t.Fatalf("samples %#v: %v", samples, err)
	}
	if samples[0].TS != recent.Add(time.Second).Format("2006-01-02T15:04:05Z") || samples[0].Svc != "checkout" {
		t.Fatalf("sample: %#v", samples[0])
	}
	routes, err := d.ErrorRoutes(ctx, 5, 10)
	if err != nil || len(routes) != 1 || routes[0].Count != 2 {
		t.Fatalf("error routes %#v: %v", routes, err)
	}
}
