package store

import (
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestPressuredCompactionBoundsRowsAndPreservesNormalCeilings(t *testing.T) {
	for _, rows := range []int{50_000, telemetry.CompactionPressureMaxRows / 2, telemetry.CompactionPressureMaxRows/2 + 1} {
		small := genBatches(2, 0, 1151, rows)
		// Another day provides real reclaimable debt even when this day's inputs
		// are too large for the pressure ceiling. Selection must still progress.
		if rows > telemetry.CompactionPressureMaxRows/2 {
			small = append(small, genBatches(3, 0, 1151, 50_000)...)
		}
		selected := selectCompactionBatches(small, 128, telemetry.CompactionDebtLimit)
		var total int64
		for _, b := range selected {
			total += compactionBatchRows(b)
		}
		want := telemetry.CompactionPressureMaxRows / min(rows, telemetry.CompactionPressureMaxRows/2)
		if rows > telemetry.CompactionPressureMaxRows/2 {
			want = telemetry.CompactionPressureMaxRows / 50_000
		}
		if len(selected) != want || total > telemetry.CompactionPressureMaxRows {
			t.Fatalf("rows/input=%d selection=%d total=%d", rows, len(selected), total)
		}
	}
	group := genBatches(2, 0, 128, 50_000)
	if selected := selectCompactionBatches(group, 128, telemetry.CompactionDebtLimit/2-1); len(selected) != 128 {
		t.Fatalf("normal ceiling changed: %d", len(selected))
	}
	for i := range group {
		group[i].Generation = 1
	}
	if selected := selectCompactionBatches(group, 128, telemetry.CompactionDebtLimit); len(selected) != 128 {
		t.Fatalf("higher generation ceiling changed: %d", len(selected))
	}
}
