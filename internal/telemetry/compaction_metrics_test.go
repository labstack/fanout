package telemetry

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCompactionRetirementMetricDoesNotCountPublicationRetries(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows := []Log{{Body: "input", TimeUnixNanos: 1}}
	for _, id := range []string{"input", "output"} {
		if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: id}, nil, rows, nil); err != nil {
			t.Fatal(err)
		}
	}
	before := testutil.ToFloat64(metrics.CompactionRetiredBatches)
	for range 2 {
		if err := store.PublishReplacement(filepath.Join(t.TempDir(), "missing"), BatchMetadata{ID: "output"}, []string{"input"}, func(f func(context.Context) error) error { return f(t.Context()) }); err != nil {
			t.Fatal(err)
		}
	}
	if delta := testutil.ToFloat64(metrics.CompactionRetiredBatches) - before; delta != 1 {
		t.Fatalf("retired=%v want 1", delta)
	}
}
