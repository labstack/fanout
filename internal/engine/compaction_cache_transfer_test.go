package engine

import (
	"testing"
	"time"

	"github.com/labstack/pitot/internal/engine/writegate"
)

// A busy background lease must defer reuse of complete input contributions,
// rather than make the refresh worker scan and aggregate the replacement again.
func TestCompactionDefersWarmCacheTransfer(t *testing.T) {
	for _, cacheReader := range []bool{false, true} {
		t.Run(map[bool]string{false: "analytical", true: "cache"}[cacheReader], func(t *testing.T) {
			d, repo := newBatchCacheTest(t)
			commitCurrentBatches(t, repo, "input", 24, time.Now().UTC().Add(-2*time.Minute))
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
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
				for range 3 {
					if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
						t.Fatalf("compacted %d: %v", n, err)
					}
				}
			}()
			n, err := d.RefreshReadCaches(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("rebuilt %d contributions from already cached inputs", n)
			}
			var parts, candidates, markers int
			if err := d.DB.QueryRow(`SELECT (SELECT count(*) FROM read_trace_parts),(SELECT count(*) FROM read_trace_candidates),(SELECT count(*) FROM read_batches)`).Scan(&parts, &candidates, &markers); err != nil {
				t.Fatal(err)
			}
			if parts != 24 || candidates != 24 || markers != 3 {
				t.Fatalf("parts=%d candidates=%d markers=%d", parts, candidates, markers)
			}
		})
	}
}

func TestDeferredCacheTransferFollowsCompactionGenerations(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	commitCurrentBatches(t, repo, "input", 64, time.Now().UTC().Add(-2*time.Minute))
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	func() {
		unlock := d.cacheGate.Lock(writegate.ReadCache)
		defer unlock()
		background, err := d.lockBackground(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer background()
		for range 9 {
			if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
				t.Fatalf("compacted %d: %v", n, err)
			}
		}
	}()
	n, err := d.RefreshReadCaches(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rebuilt %d contributions across warm generations", n)
	}
	var parts, candidates, markers int
	if err := d.DB.QueryRow(`SELECT (SELECT count(*) FROM read_trace_parts),(SELECT count(*) FROM read_trace_candidates),(SELECT count(*) FROM read_batches)`).Scan(&parts, &candidates, &markers); err != nil {
		t.Fatal(err)
	}
	if parts != 64 || candidates != 64 || markers != 1 {
		t.Fatalf("parts=%d candidates=%d markers=%d", parts, candidates, markers)
	}
}
