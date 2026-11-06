package engine

import (
	"fmt"
	"testing"
)

func TestEmptyCatalogAlwaysBound(t *testing.T) {
	for _, rollup := range []bool{false, true} {
		t.Run(fmt.Sprint(rollup), func(t *testing.T) {
			d, _ := newBatchCacheTest(t)
			// Simulate a file appearing in the global glob after the empty-catalog
			// check, before the read executes. Only the captured schema batch is valid.
			if _, err := d.writer().Exec("CREATE OR REPLACE VIEW spans AS SELECT 'unpublished' AS service"); err != nil {
				t.Fatal(err)
			}
			var count int
			if rollup {
				tx, err := d.writer().BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				unbind, err := d.bindRollupSnapshot(t.Context(), tx)
				if err != nil {
					t.Fatal(err)
				}
				if err := tx.QueryRow("SELECT count(*) FROM spans").Scan(&count); err != nil {
					t.Fatal(err)
				}
				if err := unbind(); err != nil {
					t.Fatal(err)
				}
			} else if err := d.QueryRowScan(t.Context(), []any{&count}, "SELECT count(*) FROM spans"); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("empty catalog read %d unpublished rows", count)
			}
		})
	}
}
