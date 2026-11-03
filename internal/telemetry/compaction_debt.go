package telemetry

import (
	"context"
	"time"
)

const (
	CompactionMaxRows = 25_000_000
	// The slowest measured merge with six busy cores sustained over 400k
	// rows/s. Four million rows retire pressured debt in about ten seconds;
	// unpressured and higher-generation merges retain the normal ceiling.
	CompactionPressureMaxRows = 4_000_000
	CompactionBatchLimit      = 128
	CompactionDebtLimit       = 1024
	// CompactionWindow bounds what one merge may combine by ingest time. Span
	// files sort by trace, so each row group covers its file's whole time
	// range and a narrow query decodes the entire file: file size, not window
	// size, sets its cost. An hour of pico-rate ingest is about 325,000 spans.
	CompactionWindow = time.Hour
)

// Generation0BatchCount is the exact live ingest-batch inventory, including
// terminal oversized batches and underfilled day groups. The cached count
// lets metrics read it without waiting for the catalog lock or a metadata scan.
func (p *ParquetStore) Generation0BatchCount() int {
	return int(p.generation0Snapshot.Load())
}

// CompactionDebt returns the reclaimable generation-0 backlog, excluding
// batches that the compaction policy cannot yet merge. No metadata scan.
func (p *ParquetStore) CompactionDebt() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.compactionDebt
}

// changeGeneration0Locked also maintains conservative, reclaimable debt. A raw
// generation-0 limit can deadlock on arbitrary oversized requests or on many
// finished windows that cannot form a merge. Exempt batches above half the
// pressured merge row ceiling, and reserve one underfilled group per window.
// Positive debt then guarantees a selectable group within the pressured ceiling. Larger
// indivisible inputs can still compact below pressure, but cannot block ingest
// behind debt that the pressured policy is unable to retire.
func (p *ParquetStore) changeGeneration0Locked(b BatchMetadata, delta int) {
	if b.Generation != 0 {
		return
	}
	p.generation0Batches += delta
	rows := int64(b.Spans) + int64(b.Logs) + int64(b.Metrics)
	if b.MaxIngestedNanos <= 0 || rows <= 0 || rows > CompactionPressureMaxRows/2 {
		return
	}
	if p.generation0ByWindow == nil {
		p.generation0ByWindow = make(map[int64]int)
	}
	window := b.MaxIngestedNanos / int64(CompactionWindow)
	before := p.generation0ByWindow[window]
	after := before + delta
	p.compactionDebt += max(0, after-(CompactionBatchLimit-1)) - max(0, before-(CompactionBatchLimit-1))
	if after == 0 {
		delete(p.generation0ByWindow, window)
	} else {
		p.generation0ByWindow[window] = after
	}
}

// setBatchLocked is the sole live-catalog mutation. Replacement, restoration,
// rollback, startup and retention all use the same inventory accounting.
func (p *ParquetStore) setBatchLocked(id string, batch *storedBatch) {
	if old := p.batches[id]; old != nil {
		p.changeGeneration0Locked(old.metadata, -1)
	}
	if batch == nil {
		delete(p.batches, id)
	} else {
		p.batches[id] = batch
		p.changeGeneration0Locked(batch.metadata, 1)
	}
	p.generation0Snapshot.Store(int64(p.generation0Batches))
	if p.compactionDebt >= CompactionDebtLimit && p.debtLimitReached != nil {
		close(p.debtLimitReached)
		p.debtLimitReached = nil
	}
}

// WaitForCompactionLimit wakes higher-generation work when ingest needs the
// compactor. Registration and the threshold check share the catalog lock, so
// debt cannot cross the limit between the check and subscribing. No lock is
// held while waiting, and caller cancellation releases the waiter.
func (p *ParquetStore) WaitForCompactionLimit(ctx context.Context) error {
	p.mu.Lock()
	if p.compactionDebt >= CompactionDebtLimit {
		p.mu.Unlock()
		return nil
	}
	if p.debtLimitReached == nil {
		p.debtLimitReached = make(chan struct{})
	}
	reached := p.debtLimitReached
	p.mu.Unlock()
	select {
	case <-reached:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
