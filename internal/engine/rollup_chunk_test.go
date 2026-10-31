package engine

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

// A backfill compresses days of event time into minutes of ingest, so one
// ingest chunk recomputed every affected bucket and ran out of memory on each
// retry, leaving the service rollup stuck for good. Out-of-memory failures
// halve the ingest window; successes grow it back.
func TestServiceRollupChunkShrinksOnOutOfMemory(t *testing.T) {
	oom := fmt.Errorf("service_rollup/insert: %w", &duckdb.Error{Type: duckdb.ErrorTypeOutOfMemory, Msg: "Out of Memory Error: could not allocate block"})
	chunk := int64(0)
	var seen []time.Duration
	for range 14 {
		chunk = nextServiceRollupChunk(chunk, oom)
		seen = append(seen, time.Duration(chunk))
	}
	if seen[0] != 30*time.Minute || seen[len(seen)-1] != time.Second {
		t.Fatalf("shrink sequence %v", seen)
	}
	if got := nextServiceRollupChunk(chunk, errors.New("write gate busy")); got != chunk {
		t.Fatalf("other failures changed the chunk to %v", time.Duration(got))
	}
	for chunk != 0 {
		next := nextServiceRollupChunk(chunk, nil)
		if next != 0 && next != 2*chunk {
			t.Fatalf("grew %v to %v", time.Duration(chunk), time.Duration(next))
		}
		chunk = next
	}
}
