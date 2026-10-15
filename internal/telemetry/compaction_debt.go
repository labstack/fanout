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
)

// Generation0BatchCount is the exact live ingest-batch inventory, including
// terminal oversized batches and underfilled day groups. No metadata scan.
func (p *ParquetStore) Generation0BatchCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.generation0Batches
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
// finished days with fewer than eight inputs. Exempt batches above half the
// pressured merge row ceiling, and reserve one underfilled group per day.
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
	if p.generation0ByDay == nil {
		p.generation0ByDay = make(map[int64]int)
	}
	day := b.MaxIngestedNanos / int64(24*time.Hour)
	before := p.generation0ByDay[day]
	after := before + delta
	p.compactionDebt += max(0, after-(CompactionBatchLimit-1)) - max(0, before-(CompactionBatchLimit-1))
	if after == 0 {
		delete(p.generation0ByDay, day)
	} else {
		p.generation0ByDay[day] = after
	}
}

// setBatchLocked is the sole live-catalog mutation. Replacement, restoration,
// rollback, startup and retention all use the same inventory accounting.
func (p *ParquetStore) setBatchLocked(id string, batch *storedBatch) {
	debtBefore := p.compactionDebt
	if old := p.batches[id]; old != nil {
		p.changeGeneration0Locked(old.metadata, -1)
	}
	if batch == nil {
		delete(p.batches, id)
	} else {
		p.batches[id] = batch
		p.changeGeneration0Locked(batch.metadata, 1)
	}
	if p.compactionDebt < debtBefore && p.debtChanged != nil {
		close(p.debtChanged)
		p.debtChanged = nil
	}
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

// WaitForCompaction releases the catalog lock before sleeping and wakes on a
// debt retirement or cancellation. No storage lock or query gate is held.
func (p *ParquetStore) WaitForCompaction(ctx context.Context) (time.Duration, error) {
	var began time.Time
	for {
		p.mu.Lock()
		if p.compactionDebt < CompactionDebtLimit {
			p.mu.Unlock()
			if began.IsZero() {
				return 0, ctx.Err()
			}
			return time.Since(began), ctx.Err()
		}
		if p.debtChanged == nil {
			p.debtChanged = make(chan struct{})
		}
		changed := p.debtChanged
		p.mu.Unlock()
		if began.IsZero() {
			began = time.Now()
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return time.Since(began), ctx.Err()
		}
	}
}
