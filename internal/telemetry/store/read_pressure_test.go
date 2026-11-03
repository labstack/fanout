package store

import (
	"context"
	"fmt"
	"github.com/labstack/fanout/internal/telemetry"
	"testing"
	"time"
)

func TestCompactionYieldsBetweenGroupsWithoutSkipping(t *testing.T) {
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
	checks, publishes := 0, 0
	r.SetReadPressure(func() bool { checks++; return false })
	publisher := testParquetPublisherFunc(func(ctx context.Context, publish func(context.Context) error) error {
		publishes++
		if checks+1 < publishes {
			t.Error("group bypassed pressure check")
		}
		return publish(ctx)
	})
	n, err := r.CompactParquetPass(t.Context(), publisher, 8, 5*time.Second)
	if err != nil || n < 16 || publishes < 2 || checks+1 < publishes {
		t.Fatalf("inputs=%d groups=%d checks=%d err=%v", n, publishes, checks, err)
	}
}
