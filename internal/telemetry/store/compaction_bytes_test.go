package store

import (
	"fmt"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

// A merge holds every input's dictionaries and cursors at once, so the live
// cost of a pass tracks the bytes it admits, not the number of files or the
// number of rows. Row counts cannot stand in for that: a batch of wide spans
// carrying large attribute payloads and a batch of bare log lines can hold the
// same row count and differ by an order of magnitude on disk.
func TestSelectBoundedCompactionGroupStopsAtTheByteBudget(t *testing.T) {
	const oneMiB = 1 << 20
	group := make([]telemetry.BatchMetadata, 0, 16)
	for i := range 16 {
		group = append(group, telemetry.BatchMetadata{
			ID:               string(rune('a' + i)),
			Spans:            1_000,
			MinIngestedNanos: int64(i),
			Bytes:            64 * oneMiB,
		})
	}

	candidate := selectBoundedCompactionGroup(group, 128, false)

	if len(candidate) == 0 {
		t.Fatal("no group selected; the byte budget must still admit a mergeable group")
	}
	var admitted int64
	for _, batch := range candidate {
		admitted += batch.Bytes
	}
	if admitted > maxCompactionBytes {
		t.Errorf("admitted %d bytes, budget %d: selection ignored the byte ceiling", admitted, maxCompactionBytes)
	}
	if len(candidate) == len(group) {
		t.Errorf("admitted all %d inputs at %d bytes each; the budget never bound", len(group), int64(64*oneMiB))
	}
}

// Batches whose size is unknown must not be treated as free. Nothing persists
// the byte count -- it is measured when a batch is loaded -- so a zero means
// "not measured". Charging those an estimate keeps the ceiling meaningful;
// treating them as weightless would reopen the hole the budget exists to
// close, and refusing them outright would stop compaction dead if any path
// ever failed to measure.
func TestSelectBoundedCompactionGroupChargesUnsizedBatchesAnEstimate(t *testing.T) {
	group := make([]telemetry.BatchMetadata, 0, 128)
	for i := range 128 {
		group = append(group, telemetry.BatchMetadata{
			ID:               fmt.Sprintf("batch-%03d", i),
			Spans:            50_000,
			MinIngestedNanos: int64(i),
			// Bytes deliberately left zero: never measured.
		})
	}

	candidate := selectBoundedCompactionGroup(group, 128, false)

	if len(candidate) == 0 {
		t.Fatal("no group selected; unsized batches must still be compactable")
	}
	if len(candidate) == len(group) {
		t.Fatalf("admitted all %d unsized inputs; an unmeasured batch must not count as zero bytes", len(group))
	}
	estimated := int64(len(candidate)) * 50_000 * assumedCompactionBytesPerRow
	if estimated > maxCompactionBytes {
		t.Errorf("estimated %d bytes across %d inputs, budget %d", estimated, len(candidate), int64(maxCompactionBytes))
	}
}
