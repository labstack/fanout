package store

import (
	"context"
	"fmt"
	"github.com/labstack/fanout/internal/telemetry"
	"testing"
	"time"
)

func TestCompactionPassCompletesMultipleGroups(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := range 16 {
		if err := r.Commit(t.Context(), Batch{ID: fmt.Sprint("batch-", i), Logs: []telemetry.Log{{TimeUnixNanos: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	publishes := 0
	publisher := testParquetPublisherFunc(func(ctx context.Context, publish func(context.Context) error) error {
		publishes++
		return publish(ctx)
	})
	n, err := r.CompactParquetPass(t.Context(), publisher, 8, 5*time.Second)
	if err != nil || n < 16 || publishes < 2 {
		t.Fatalf("inputs=%d groups=%d err=%v", n, publishes, err)
	}
}
