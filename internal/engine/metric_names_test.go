package engine

import (
	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
	"testing"
	"time"
)

func TestMetricNamePruningProof(t *testing.T) {
	db := openTestDuck(t)
	for _, tc := range []struct {
		sql  string
		want int
	}{
		{`SELECT * FROM metrics WHERE name='wanted'`, 1},
		{`SELECT * FROM metrics WHERE time>now() AND name IN ('a','b')`, 2},
		{`SELECT * FROM metrics WHERE name='wanted' OR value>0`, 0},
		{`SELECT * FROM metrics WHERE name=$name`, 0},
		{`SELECT * FROM metrics UNION ALL SELECT * FROM metrics WHERE name='wanted'`, 0},
		{`SELECT * FROM metrics m JOIN spans s ON true WHERE name='wanted'`, 0},
	} {
		tree, err := parseSQLTree(t.Context(), db, tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		if got := singleMetricNames(tree); len(got) != tc.want {
			t.Fatalf("%s: %v want %d; tree=%+v", tc.sql, got, tc.want, tree)
		}
	}
}

func TestMetricNamePruningUncachedAndCompacted(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Add(-time.Minute)
	commit := func(id, name string) {
		t.Helper()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Metrics: []telemetry.Metric{{Namespace: "ns", ServiceName: "svc", Name: name, TimeUnixNanos: at.UnixNano(), Value: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	commit("other", "other")
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		tx, err := d.DB.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		selected, err := pruneMetricNames(t.Context(), tx, repo.Parquet.BatchMetadata(), []string{"wanted"})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, b := range selected {
			n += b.Metrics
		}
		if n != want {
			t.Fatalf("selected=%d want=%d", n, want)
		}
	}
	check(0)
	commit("fresh", "wanted")
	check(1)
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(1)
	for i := 0; i < 6; i++ {
		commit(string(rune('a'+i)), "other")
	}
	if _, err := d.RefreshReadCaches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CompactParquet(t.Context(), d, 8); err != nil {
		t.Fatal(err)
	}
	check(8) // Whole replacement remains eligible because it contains wanted.
	var count int
	ctx := queryrows.WithWindow(t.Context(), queryrows.Window{Start: at.Add(-time.Second), End: at.Add(time.Second)})
	if err := d.QueryRowScan(ctx, []any{&count}, `SELECT count(*) FROM metrics WHERE name='wanted'`); err != nil || count != 1 {
		t.Fatalf("scoped count=%d: %v", count, err)
	}
	if err := d.QueryRowScan(t.Context(), []any{&count}, `SELECT count(*) FROM metrics`); err != nil || count != 8 {
		t.Fatalf("unscoped count=%d: %v", count, err)
	}
}
