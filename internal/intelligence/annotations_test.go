package intelligence

import (
	"context"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/query/writegate"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"testing"
	"time"
)

func TestDetectorPublishesWhilePersistenceBlocked(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RetentionDays: 30}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	unlock := duck.WriteGate().Lock(writegate.WriteRollupService)
	defer unlock()
	d := NewDetector(duck, DefaultDetectorConfig())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.runCheck(ctx) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("persistence blocked snapshot publication")
	}
	if d.LatestSnapshot() == nil {
		t.Fatal("missing snapshot")
	}
}

func TestDetectorCoalescesPendingPersistence(t *testing.T) {
	d := NewDetector(&query.Duck{}, DefaultDetectorConfig())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan time.Time, 3)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.persistAnnotations(ctx, func(ctx context.Context, _ []annotations.Anomaly, at time.Time) error {
			entered <- at
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	defer func() { cancel(); <-done }()
	now := time.Now().UTC()
	publish := func(at time.Time) { d.publishSnapshot(IntelligenceSnapshot{GeneratedAt: at}) }
	publish(now)
	select {
	case at := <-entered:
		if !at.Equal(now) {
			t.Fatal(at)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	published := make(chan struct{})
	go func() { publish(now.Add(time.Minute)); publish(now.Add(2 * time.Minute)); close(published) }()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("blocked persistence delayed publication")
	}
	if got := d.LatestSnapshot(); got == nil || !got.GeneratedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("snapshot: %+v", got)
	}
	if len(d.annotationBatches) != 1 {
		t.Fatalf("pending batches=%d", len(d.annotationBatches))
	}
	close(release)
	select {
	case at := <-entered:
		if !at.Equal(now.Add(2 * time.Minute)) {
			t.Fatalf("pending batch was not replaced: %v", at)
		}
	case <-time.After(time.Second):
		t.Fatal("pending batch not persisted")
	}
	select {
	case at := <-entered:
		t.Fatalf("extra batch persisted: %v", at)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDetectorAnnotationMapping(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RetentionDays: 30}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	at := time.Now().UTC().Truncate(time.Minute)
	snapshot := IntelligenceSnapshot{GeneratedAt: at, Anomalies: []Anomaly{{ServiceName: "checkout", Type: AnomalyLatencyDegradation, ZScore: -4, DetectedAt: at, Description: "Slow"}, {ServiceName: "payment", Type: AnomalyVolumeChange, ZScore: 3, DetectedAt: at, Description: "Low volume"}}}
	findings := detectorAnnotations(snapshot, "shop", 5*time.Minute)
	if err := duck.RecordAnomalies(t.Context(), findings, at); err != nil {
		t.Fatal(err)
	}
	got, err := annotations.New(duck).Read(t.Context(), annotations.Request{From: at.Add(-time.Hour), To: at.Add(time.Minute), Namespace: "shop"})
	if err != nil || len(got.Anomalies) != 2 {
		t.Fatalf("persisted: %+v %v", got, err)
	}
	for _, a := range got.Anomalies {
		want := "warn"
		if a.Service == "checkout" {
			want = "bad"
		}
		if a.Namespace != "shop" || a.Severity != want || !a.From.Equal(at.Add(-5*time.Minute)) || !a.To.Equal(at) {
			t.Fatalf("mapping: %+v", a)
		}
	}
}

func TestDetectorWritesFailedFindingsWithTheNextBatch(t *testing.T) {
	d := NewDetector(&query.Duck{}, DefaultDetectorConfig())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := make(chan []annotations.Anomaly, 2)
	attempt := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.persistAnnotations(ctx, func(_ context.Context, findings []annotations.Anomaly, _ time.Time) error {
			calls <- append([]annotations.Anomaly(nil), findings...)
			attempt++
			if attempt == 1 {
				return context.DeadlineExceeded
			}
			return nil
		})
	}()
	defer func() { cancel(); <-done }()
	now := time.Now().UTC()
	spike := annotations.Anomaly{Namespace: "shop", Service: "cart", Kind: "latency", From: now.Add(-2 * time.Minute), To: now.Add(-time.Minute)}
	later := annotations.Anomaly{Namespace: "shop", Service: "cart", Kind: "error_rate", From: now.Add(-time.Minute), To: now}
	d.annotationBatches <- annotationBatch{findings: []annotations.Anomaly{spike}, at: now}
	if got := <-calls; len(got) != 1 || got[0] != spike {
		t.Fatalf("first write: %+v", got)
	}
	d.annotationBatches <- annotationBatch{findings: []annotations.Anomaly{later}, at: now.Add(time.Minute)}
	select {
	case got := <-calls:
		if len(got) != 2 || got[0] != spike || got[1] != later {
			t.Fatalf("failed findings were not carried: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second write did not happen")
	}
}
