package engine

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

var errRollupOOM = fmt.Errorf("service_rollup/insert: %w", &duckdb.Error{Type: duckdb.ErrorTypeOutOfMemory, Msg: "Out of Memory Error: could not allocate block"})

// A backfill compresses days of event time into minutes of ingest, so one
// ingest chunk recomputed every affected bucket and ran out of memory on each
// retry, leaving the service rollup stuck for good. Out-of-memory failures
// halve the ingest window; committed passes grow it back.
func TestServiceRollupChunkShrinksOnOutOfMemory(t *testing.T) {
	var c rollupChunk
	var seen []time.Duration
	for range 14 {
		c.next(false, errRollupOOM)
		seen = append(seen, time.Duration(c.size))
	}
	if seen[0] != 30*time.Minute || seen[len(seen)-1] != time.Second {
		t.Fatalf("shrink sequence %v", seen)
	}
	before := c
	c.next(false, errors.New("write gate busy"))
	if c.size != before.size {
		t.Fatalf("other failures changed the chunk to %v", time.Duration(c.size))
	}
	for c.size != 0 {
		size := c.size
		for range rollupChunkWins {
			c.next(true, nil)
		}
		if c.size != 0 && c.size != 2*size {
			t.Fatalf("grew %v to %v", time.Duration(size), time.Duration(c.size))
		}
	}
}

// A pass that did no work, such as one skipped while compaction held the
// Parquet gate, is not evidence the window fits, and neither is one
// committed pass: growing on either made the window halve, grow and run out
// of memory again every other pass.
func TestServiceRollupChunkGrowsOnlyAfterCommittedPasses(t *testing.T) {
	var c rollupChunk
	c.next(false, errRollupOOM)
	c.next(false, errRollupOOM)
	narrow := c.size
	for range 10 {
		c.next(false, nil)
	}
	if c.size != narrow {
		t.Fatalf("noops grew the chunk to %v", time.Duration(c.size))
	}
	for i := range rollupChunkWins - 1 {
		c.next(true, nil)
		c.next(false, nil)
		if c.size != narrow {
			t.Fatalf("grew to %v after %d committed passes", time.Duration(c.size), i+1)
		}
	}
	c.next(false, errRollupOOM)
	narrow = c.size
	for range rollupChunkWins - 1 {
		c.next(true, nil)
	}
	if c.size != narrow {
		t.Fatal("a failure did not restart the count of committed passes")
	}
	c.next(true, nil)
	if c.size != 2*narrow {
		t.Fatalf("chunk %v after %d committed passes, want %v", time.Duration(c.size), rollupChunkWins, time.Duration(2*narrow))
	}

	// A workload whose window fits at 15 minutes but not 30: out of memory
	// at most once per growth attempt, never on alternate passes.
	c = rollupChunk{}
	ooms, worked := 0, 0
	for range 200 {
		if c.size == 0 || c.size > int64(20*time.Minute) {
			ooms++
			c.next(false, errRollupOOM)
			continue
		}
		worked++
		c.next(true, nil)
		c.next(false, nil) // idle tick between passes
	}
	if ooms*rollupChunkWins > worked+2*rollupChunkWins {
		t.Fatalf("%d out-of-memory passes against %d committed ones", ooms, worked)
	}
}
