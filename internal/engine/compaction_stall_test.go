package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/engine/writegate"
)

// Both rollups and read-cache passes hold a pin while using background SQL.
// Optional cache transfers must not park compaction behind either reader.
func TestCompactionPublishesDuringBackgroundReads(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, cacheReader := range []bool{false, true} {
			t.Run(fmt.Sprintf("warm=%t/cache_reader=%t", warm, cacheReader), func(t *testing.T) {
				d, repo := newBatchCacheTest(t)
				commitCurrentBatches(t, repo, "input", 24, time.Now().UTC().Add(-2*time.Minute))
				if warm {
					if _, err := d.RefreshReadCaches(t.Context()); err != nil {
						t.Fatal(err)
					}
					if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				pin := d.pinParquet()
				released := false
				defer func() {
					if !released {
						d.unpinParquet(pin)
					}
				}()
				// Release the simulated reader before cleanup, even on a failed assertion.
				func() {
					unlock := d.writeGate.Lock(writegate.RollupEdge)
					if cacheReader {
						unlock()
						unlock = d.cacheGate.Lock(writegate.ReadCache)
					}
					defer unlock()
					background, err := d.lockBackground(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer background()
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					for pass := range 3 {
						if n, err := repo.CompactParquet(ctx, d, 8); err != nil || n != 8 {
							t.Fatalf("pass %d stopped publishing behind a background reader: compacted=%d err=%v", pass, n, err)
						}
					}
					if n, err := repo.CollectRetiredParquet(); err != nil || n != 0 {
						t.Fatalf("collected %d pinned inputs: %v", n, err)
					}
				}()
				// Skipping transfers leaves outputs cold. The normal worker must rebuild
				// them from Parquet and retire input contributions without counting twice.
				for {
					_, pending, err := d.refreshReadCaches(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if !pending {
						break
					}
				}
				if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
					t.Fatal(err)
				}
				var spans, logs, metrics int
				if err := d.QueryRowScan(t.Context(), []any{&spans, &logs, &metrics},
					`SELECT (SELECT count(*) FROM spans),(SELECT count(*) FROM logs),(SELECT count(*) FROM metrics)`); err != nil {
					t.Fatal(err)
				}
				if spans != 24 || logs != 24 || metrics != 24 {
					t.Fatalf("rows after publication: spans=%d logs=%d metrics=%d", spans, logs, metrics)
				}
				var markers int
				if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&markers); err != nil || markers != 3 {
					t.Fatalf("rebuilt output markers=%d, want 3: %v", markers, err)
				}
				var parts int
				if err := d.DB.QueryRow(`SELECT count(*) FROM read_trace_parts`).Scan(&parts); err != nil || parts != 24 {
					t.Fatalf("rebuilt trace contributions=%d, want 24: %v", parts, err)
				}
				d.unpinParquet(pin)
				released = true
				if n, err := repo.CollectRetiredParquet(); err != nil || n != 24 {
					t.Fatalf("collected %d after reader finished, want 24: %v", n, err)
				}
			})
		}
	}
}
