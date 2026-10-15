package telemetry

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestGenerationZeroInventoryTracksCatalogChanges(t *testing.T) {
	p := &ParquetStore{batches: make(map[string]*storedBatch)}
	add := func(id string, generation int) {
		p.installBatch(&storedBatch{metadata: BatchMetadata{ID: id, Generation: uint32(generation), MaxIngestedNanos: 1, Spans: 1}})
	}
	add("a", 0)
	add("b", 0)
	add("a", 0)
	if p.Generation0BatchCount() != 2 {
		t.Fatal("idempotent install double counted")
	}
	add("a", 1)
	if p.Generation0BatchCount() != 1 {
		t.Fatal("replacement not counted")
	}
	p.mu.Lock()
	p.setBatchLocked("b", nil)
	p.mu.Unlock()
	if p.Generation0BatchCount() != 0 {
		t.Fatal("retirement not counted")
	}
	add("b", 0)
	if p.Generation0BatchCount() != 1 {
		t.Fatal("restoration not counted")
	}
}
func TestCompactionWaitWakesOnRetirementAndCancellation(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelWait), func(t *testing.T) {
			p := &ParquetStore{batches: make(map[string]*storedBatch)}
			for i := 0; i < CompactionDebtLimit+CompactionBatchLimit-1; i++ {
				p.installBatch(&storedBatch{metadata: BatchMetadata{ID: fmt.Sprint(i), Spans: 1, MaxIngestedNanos: 1}})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.WaitForCompaction(ctx); done <- err }()
			// Observe the registered wait under the same lock; no timing assumption.
			until := time.Now().Add(time.Second)
			for {
				p.mu.RLock()
				waiting := p.debtChanged != nil
				p.mu.RUnlock()
				if waiting {
					break
				}
				if time.Now().After(until) {
					t.Fatal("wait not registered")
				}
				time.Sleep(time.Millisecond)
			}
			select {
			case <-done:
				t.Fatal("wait did not block")
			default:
			}
			if cancelWait {
				cancel()
			} else {
				p.mu.Lock()
				p.setBatchLocked("0", nil)
				p.mu.Unlock()
			}
			select {
			case err := <-done:
				if cancelWait && err != context.Canceled || !cancelWait && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("wait did not wake")
			}
		})
	}
}
func TestIrreducibleGenerationZeroBatchesDoNotStall(t *testing.T) {
	p := &ParquetStore{batches: make(map[string]*storedBatch)}
	for i := 0; i < 2048; i++ {
		p.installBatch(&storedBatch{metadata: BatchMetadata{ID: fmt.Sprint(i), Spans: CompactionMaxRows, MaxIngestedNanos: 1}})
	}
	for day := 1; day < 200; day++ {
		for i := 0; i < 7; i++ {
			p.installBatch(&storedBatch{metadata: BatchMetadata{ID: fmt.Sprintf("%d-%d", day, i), Spans: 1, MaxIngestedNanos: int64(day) * int64(24*time.Hour)}})
		}
	}
	if p.Generation0BatchCount() < CompactionDebtLimit || p.compactionDebt != 0 {
		t.Fatalf("inventory=%d debt=%d", p.Generation0BatchCount(), p.compactionDebt)
	}
	if waited, err := p.WaitForCompaction(t.Context()); err != nil || waited != 0 {
		t.Fatalf("irreducible inputs stalled: %s %v", waited, err)
	}
}

func TestCompactionDebtExcludesUnreclaimableInventory(t *testing.T) {
	p := &ParquetStore{batches: make(map[string]*storedBatch)}
	for i := 0; i < CompactionBatchLimit-1+CompactionDebtLimit/2; i++ {
		p.installBatch(&storedBatch{metadata: BatchMetadata{ID: fmt.Sprint(i), Spans: 1, MaxIngestedNanos: 1}})
		want := max(0, i+1-(CompactionBatchLimit-1))
		if got := p.CompactionDebt(); got != want {
			t.Fatalf("after install %d debt=%d want=%d", i, got, want)
		}
	}
	p.installBatch(&storedBatch{metadata: BatchMetadata{ID: "oversized", Spans: CompactionMaxRows, MaxIngestedNanos: 1}})
	p.installBatch(&storedBatch{metadata: BatchMetadata{ID: "another-day", Spans: 1, MaxIngestedNanos: int64(24 * time.Hour)}})
	if got := p.CompactionDebt(); got != CompactionDebtLimit/2 {
		t.Fatalf("unreclaimable inputs raised debt: %d", got)
	}
	p.mu.Lock()
	p.setBatchLocked("0", nil)
	p.mu.Unlock()
	if got := p.CompactionDebt(); got != CompactionDebtLimit/2-1 {
		t.Fatalf("retirement debt=%d", got)
	}
}
