package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func genBatches(day int64, generation uint32, n int, rows int) []telemetry.BatchMetadata {
	out := make([]telemetry.BatchMetadata, 0, n)
	for i := range n {
		out = append(out, telemetry.BatchMetadata{
			ID:               fmt.Sprintf("g%d-%04d", generation, i),
			Generation:       generation,
			Spans:            rows,
			Bytes:            int64(rows) * 100,
			MaxIngestedNanos: day*int64(24*time.Hour) + int64(i),
			MinIngestedNanos: day*int64(24*time.Hour) + int64(i),
		})
	}
	return out
}

// Ingest replenishes generation 0 continuously, so a rule that always prefers
// the lowest generation never reaches the higher ones. Observed in production:
// generation 0 and 1 were compacting while 290 generation-2 batches sat
// untouched for hours, and the live file count -- which is what every query
// pays per-file overhead on -- settled at an equilibrium instead of falling.
func TestSelectCompactionBatchesDoesNotStarveHigherGenerations(t *testing.T) {
	// The shape of the stall, taken from the live box: generation 0 replenished
	// by ingest, generation 2 piled up above it. Both are past the count ceiling,
	// so both are valid candidates and the tie-break decides.
	batches := append(genBatches(1, 0, 508, 1_000), genBatches(1, 2, 290, 200_000)...)

	selected := selectCompactionBatches(batches, 128)
	if len(selected) == 0 {
		t.Fatal("selected nothing with two compactable groups available")
	}
	if selected[0].Generation != 2 {
		t.Errorf("selected generation %d when generation 2 has 290 batches waiting: "+
			"a rule that always takes the lowest generation lets ingest starve every generation above it",
			selected[0].Generation)
	}
}

// The older day still wins: reclaiming disk that retention will drop next
// matters more than consolidating today's files.
func TestSelectCompactionBatchesStillPrefersTheOlderDay(t *testing.T) {
	batches := append(genBatches(1, 0, 200, 1_000), genBatches(5, 2, 290, 200_000)...)

	selected := selectCompactionBatches(batches, 128)
	if len(selected) == 0 {
		t.Fatal("selected nothing")
	}
	if selected[0].MaxIngestedNanos/int64(24*time.Hour) != 1 {
		t.Errorf("selected day %d, want the older day 1 even though day 5 has more files",
			selected[0].MaxIngestedNanos/int64(24*time.Hour))
	}
}

// Generation 0 must still be served: it is refilled constantly, so once the
// higher generations no longer have a full group it should take every pass.
func TestSelectCompactionBatchesServesGenerationZeroWhenNothingIsPiledAbove(t *testing.T) {
	// Only generation 0 has enough to form a group.
	batches := append(genBatches(1, 0, 200, 1_000), genBatches(1, 2, 3, 1_000)...)

	selected := selectCompactionBatches(batches, 128)
	if len(selected) == 0 {
		t.Fatal("selected nothing with a full generation-0 group available")
	}
	if selected[0].Generation != 0 {
		t.Errorf("selected generation %d, want 0: with nothing compactable above it, the newest generation takes the pass",
			selected[0].Generation)
	}
}

// A day that has stopped being written still has to consolidate. Its group
// never reaches the count ceiling and never saturates a row or byte ceiling, so
// a rule that admits only full or saturated groups strands it forever.
//
// Measured on the live box: 1,243 batches spread over 69 (day, generation)
// groups covering 25 days, every one of them returning no candidate. Only the
// current day ever reached 128 files, so every compaction output was
// generation 1 and everything older sat untouched -- which is why the live file
// count held at an equilibrium no amount of reordering or budget-raising moved.
func TestSelectBoundedCompactionGroupCompactsAnUnderfilledDay(t *testing.T) {
	// A finished day: well under the count ceiling, nowhere near saturating.
	group := genBatches(3, 1, 56, 5_000)

	candidate := selectBoundedCompactionGroup(group, 128, true)

	if len(candidate) == 0 {
		t.Fatal("no candidate for a 56-batch day: a day that will never grow to 128 files can never consolidate")
	}
	if len(candidate) != 56 {
		t.Errorf("candidate = %d, want all 56: nothing here forces a smaller group", len(candidate))
	}
}

// Still not worth waking the merge for a couple of files.
func TestSelectBoundedCompactionGroupIgnoresATrivialDay(t *testing.T) {
	if got := selectBoundedCompactionGroup(genBatches(3, 1, 3, 5_000), 128, true); len(got) != 0 {
		t.Errorf("candidate = %d for a 3-batch day, want none: below the effort floor", len(got))
	}
}
