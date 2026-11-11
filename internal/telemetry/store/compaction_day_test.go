package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/telemetry"
)

// hourFiles returns one settled file per ingest hour in [from, to), as hour
// merges leave them.
func hourFiles(from, to int64, generation uint32, rows int) []telemetry.BatchMetadata {
	var out []telemetry.BatchMetadata
	for hour := from; hour < to; hour++ {
		at := hour * int64(time.Hour)
		out = append(out, telemetry.BatchMetadata{
			ID: fmt.Sprintf("h%04d", hour), Generation: generation, Spans: rows, Bytes: int64(rows) * 75,
			MinIngestedNanos: at + int64(time.Second), MaxIngestedNanos: at + int64(59*time.Minute),
		})
	}
	return out
}

// compactToFixedPoint applies selection and merges metadata until nothing
// is selected, as repeated compaction passes do.
func compactToFixedPoint(t *testing.T, batches []telemetry.BatchMetadata) []telemetry.BatchMetadata {
	t.Helper()
	for pass := 0; ; pass++ {
		if pass > 10_000 {
			t.Fatal("compaction never reached a fixed point")
		}
		selected := selectCompactionBatchesWithHigherCap(batches, telemetry.CompactionBatchLimit, telemetry.CompactionBatchLimit, 0)
		if len(selected) < 2 {
			return batches
		}
		out := telemetry.BatchMetadata{ID: fmt.Sprintf("c%05d", pass), MinIngestedNanos: selected[0].MinIngestedNanos}
		inputs := map[string]bool{}
		for _, b := range selected {
			inputs[b.ID] = true
			out.Generation = max(out.Generation, b.Generation+1)
			out.MinIngestedNanos = min(out.MinIngestedNanos, b.MinIngestedNanos)
			out.MaxIngestedNanos = max(out.MaxIngestedNanos, b.MaxIngestedNanos)
			out.Spans += b.Spans
			out.Bytes += b.Bytes
		}
		kept := []telemetry.BatchMetadata{out}
		for _, b := range batches {
			if !inputs[b.ID] {
				kept = append(kept, b)
			}
		}
		batches = kept
	}
}

// Hourly files keep a narrow query on the last day cheap, but a quiet
// install held one small file per hour per signal for its whole retention.
// Hours more than a day behind the newest merge across their day, in time
// order, into files bounded by rows, so a narrow query on old data still
// decodes a bounded file.
func TestCompactionMergesOldHoursWithinTheirDay(t *testing.T) {
	batches := hourFiles(0, 24*7, 1, int(maxDayCompactionRows/10))
	final := compactToFixedPoint(t, batches)
	perDay := map[int64]int{}
	for _, b := range final {
		day := b.MinIngestedNanos / int64(24*time.Hour)
		if b.MaxIngestedNanos/int64(24*time.Hour) != day {
			t.Fatalf("%s spans days %d to %d", b.ID, day, b.MaxIngestedNanos/int64(24*time.Hour))
		}
		if int64(b.Spans) > maxDayCompactionRows {
			t.Fatalf("%s holds %d rows, over %d", b.ID, b.Spans, maxDayCompactionRows)
		}
		perDay[day]++
	}
	newest := int64(24*7 - 1)
	for day, n := range perDay {
		lastHour := day*24 + 23
		switch {
		case lastHour >= newest-compactionSettledHours:
			// The last day and the hours before it within a day stay hourly.
			if day == 6 && n != 24 {
				t.Errorf("recent day %d holds %d files, want 24 hourly", day, n)
			}
		case n > 3:
			// 24 hours of a tenth of the bound each.
			t.Errorf("old day %d holds %d files, want at most 3", day, n)
		}
	}
	if len(final) >= len(batches)/3 {
		t.Fatalf("%d files after compaction from %d", len(final), len(batches))
	}
}

// A day group merges contiguous hours: a file already at the row bound, or
// one too large to join its neighbour, splits the run instead of being
// skipped over, so every output covers one stretch of time.
func TestDayCompactionMergesContiguousHours(t *testing.T) {
	third := int(maxDayCompactionRows / 3)
	group := hourFiles(0, 6, 1, third)
	group[2].Spans = int(maxDayCompactionRows)
	got := selectDayCompactionGroup(group, 128, maxDayCompactionRows)
	if len(got) != 2 || got[0].ID != "h0000" || got[1].ID != "h0001" {
		t.Fatalf("selected %v, want the first two hours", ids(got))
	}
	group = append(group[3:4], group[0], group[1])
	got = selectDayCompactionGroup(group, 128, maxDayCompactionRows)
	if len(got) != 3 || got[0].ID != "h0000" || got[2].ID != "h0003" {
		t.Fatalf("selected %v, want hours 0, 1 and 3 in time order", ids(got))
	}
	if got := selectDayCompactionGroup(hourFiles(0, 2, 1, 2*third), 128, maxDayCompactionRows); len(got) != 0 {
		t.Fatalf("selected %v past the row bound", ids(got))
	}
}

// A file that already spans hours came from a day merge. It never rejoins an
// hour group, where the hour ceiling would let it grow past the day bound.
func TestMultiHourFilesStayOutOfHourGroups(t *testing.T) {
	merged := telemetry.BatchMetadata{ID: "day", Generation: 2, Spans: int(maxDayCompactionRows * 3 / 4), MinIngestedNanos: int64(time.Second), MaxIngestedNanos: int64(5*time.Hour + 30*time.Minute)}
	hour := hourFiles(5, 6, 2, int(maxDayCompactionRows/2))
	batches := append([]telemetry.BatchMetadata{merged}, hour...)
	batches = append(batches, hourFiles(40, 41, 0, 1)...)
	if got := selectCompactionBatchesWithHigherCap(batches, 128, 128, 0); len(got) != 0 {
		t.Fatalf("selected %v; the pair exceeds the day bound", ids(got))
	}
}

// A day merge takes files of any generation, and the output's generation is
// above all of them. Rows survive the merge.
func TestRepositoryMergesAnOldDayAcrossGenerations(t *testing.T) {
	dir := t.TempDir()
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	commit := func(id string, hour int64) {
		batch := testBatch()
		batch.ID = id
		at := hour*int64(time.Hour) + int64(time.Minute)
		batch.Spans[0].SpanID = id
		batch.Spans[0].StartUnixNanos, batch.Spans[0].EndUnixNanos, batch.Spans[0].IngestedAt = at, at+100, at
		batch.Logs[0].TimeUnixNanos, batch.Logs[0].IngestedAt = at, at
		batch.Metrics[0].TimeUnixNanos, batch.Metrics[0].IngestedAt = at, at
		if err := commitRestoredFixture(repository, ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	commit("old-a", 480_001)
	commit("old-b", 480_002)
	commit("old-c", 480_003)
	commit("new", 480_040)
	compactor := &testParquetCompactor{}
	// The first pass has nothing hourly to merge; the day merge takes all
	// three old hours.
	if n, err := repository.CompactParquet(ctx, compactor, minCompactionInputs); err != nil || n != 3 {
		t.Fatalf("compacted %d, %v; want the three old hours", n, err)
	}
	commit("old-d", 480_004)
	if n, err := repository.CompactParquet(ctx, compactor, minCompactionInputs); err != nil || n != 2 {
		t.Fatalf("compacted %d, %v; want the day file and the later hour", n, err)
	}
	var day telemetry.BatchMetadata
	for _, b := range repository.Parquet.BatchMetadata() {
		if b.ID != "new" {
			day = b
		}
	}
	if len(repository.Parquet.BatchMetadata()) != 2 || day.Generation != 2 || day.Spans != 4 || day.Logs != 4 || day.Metrics != 4 {
		t.Fatalf("batches after day merges: %+v", repository.Parquet.BatchMetadata())
	}
	spans, err := traceAll(repository, "trace-1")
	if err != nil || len(spans) != 5 {
		t.Fatalf("trace spans after day merges = %d, %v", len(spans), err)
	}
}

func ids(batches []telemetry.BatchMetadata) []string {
	out := make([]string, len(batches))
	for i, b := range batches {
		out[i] = b.ID
	}
	return out
}
