package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
)

func TestAnalyticalRollupsReleaseGateWhenPublicationBlocksAdmission(t *testing.T) {
	for _, component := range []string{"service", "edge", "version"} {
		t.Run(component, func(t *testing.T) {
			d, repo := versionEngine(t)
			commitVersionLogs(t, repo, "pending", time.Now().UTC().Add(-time.Hour), 2)
			refresh := func() (int64, error) {
				switch component {
				case "service":
					return d.refreshServiceRollup(t.Context())
				case "edge":
					n, _, err := d.refreshEdgeRollupPass(t.Context())
					return n, err
				default:
					return d.RefreshVersionRollup(t.Context())
				}
			}
			if err := d.parquetMu.LockContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			locked := true
			defer func() {
				if locked {
					d.parquetMu.Unlock()
				}
			}()
			before := fmt.Sprint(d.rollupWatermarks)
			for range 3 {
				if n, err := refresh(); err != nil || n != 0 {
					t.Fatalf("skipped pass rows=%d error=%v", n, err)
				}
			}
			if fmt.Sprint(d.rollupWatermarks) != before || len(d.versionRollupFailures) != 0 {
				t.Fatal("skipped admission moved progress or penalized a batch")
			}
			var marked int
			if err := d.writer().QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&marked); err != nil || marked != 0 {
				t.Fatalf("skipped version markers=%d error=%v", marked, err)
			}
			// Allow slow race runs to finish the INSERT while still detecting a
			// gate parked for the former 15-second admission lease.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start := time.Now()
			if err := d.RecordAnomalies(ctx, []annotations.Anomaly{}, time.Now()); err != nil {
				t.Fatalf("anomaly persistence failed with %s admission blocked: %v", component, err)
			}
			t.Logf("anomaly persisted with publication blocking %s admission: %s", component, time.Since(start))
			d.parquetMu.Unlock()
			locked = false
			if n, err := refresh(); err != nil || component != "edge" && n == 0 {
				t.Fatalf("retry rows=%d error=%v", n, err)
			}
		})
	}
}
