package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// replacesFile names, inside a compacted batch, the batch IDs it replaced. The
// output's rename into the batch namespace commits the compaction, and this
// file travels with it: once the output is present, every batch it lists is
// retired, whether or not its directory has been deleted yet. Startup applies
// the same rule, so a crash between publication and deletion cannot expose an
// output together with its inputs.
const replacesFile = "replaces.json"

// SnapshotPin records the catalog epoch a reader entered at. A reader pins
// before it captures BatchMetadata and unpins when it no longer opens files.
type SnapshotPin struct {
	epoch  uint64
	pinned bool
}

// retirement is one publication's removed batches. Their directories stay in
// place, so readers that captured the catalog before the publication can still
// open them, until every reader that entered at or before epoch has left.
type retirement struct {
	epoch   uint64
	batches []BatchMetadata
}

// Pin admits a reader to the current catalog. It never waits: publication
// swaps the catalog atomically and defers deleting what it replaced.
func (p *ParquetStore) Pin() SnapshotPin {
	p.pinMu.Lock()
	defer p.pinMu.Unlock()
	if p.pinned == nil {
		p.pinned = map[uint64]int{}
	}
	p.pinned[p.epoch]++
	return SnapshotPin{epoch: p.epoch, pinned: true}
}

// Unpin releases a reader. Releasing a zero pin is a no-op.
func (p *ParquetStore) Unpin(pin SnapshotPin) {
	if !pin.pinned {
		return
	}
	p.pinMu.Lock()
	defer p.pinMu.Unlock()
	switch p.pinned[pin.epoch] {
	case 0:
		panic("telemetry: Unpin without Pin")
	case 1:
		delete(p.pinned, pin.epoch)
	default:
		p.pinned[pin.epoch]--
	}
}

// RetirementWaiting reports whether deleting a retired batch waits for pin,
// so a resumable reader can release its snapshot at a convenient boundary.
func (p *ParquetStore) RetirementWaiting(pin SnapshotPin) bool {
	p.pinMu.Lock()
	defer p.pinMu.Unlock()
	return pin.pinned && len(p.retiring) > 0 && p.retiring[0].epoch >= pin.epoch
}

// RetiringBatches is the number of retired batch directories not yet deleted.
func (p *ParquetStore) RetiringBatches() int {
	p.pinMu.Lock()
	defer p.pinMu.Unlock()
	n := 0
	for _, r := range p.retiring {
		n += len(r.batches)
	}
	return n
}

// retireLocked records batches the catalog no longer contains. The caller has
// already removed them from p.batches; readers that pin after this see the new
// catalog, and readers pinned at or before the recorded epoch may still use
// the files. Called with pinMu held.
func (p *ParquetStore) retireLocked(batches []BatchMetadata) {
	if len(batches) == 0 {
		return
	}
	p.retiring = append(p.retiring, retirement{epoch: p.epoch, batches: batches})
	p.retiringIDs = nil
	p.epoch++
}

func (p *ParquetStore) retire(batches []BatchMetadata) {
	p.pinMu.Lock()
	defer p.pinMu.Unlock()
	p.retireLocked(batches)
}

// retiredMetadata returns a retired batch that has not been deleted yet.
func (p *ParquetStore) retiredMetadata(id string) (BatchMetadata, bool) {
	p.pinMu.Lock()
	defer p.pinMu.Unlock()
	if p.retiringIDs == nil {
		p.retiringIDs = map[string]BatchMetadata{}
		for _, r := range p.retiring {
			for _, b := range r.batches {
				p.retiringIDs[b.ID] = b
			}
		}
	}
	b, ok := p.retiringIDs[id]
	return b, ok
}

// CollectRetired deletes retired batch directories no pinned reader can still
// open. Retirements are deleted in publication order and the pass stops at the
// first failure, so a compacted batch is never removed before the inputs its
// replaces list retires. Each directory leaves the batch namespace with one
// rename before it is removed, so a partial removal is never mistaken for a
// published batch. Callers serialize this with compaction and retention.
func (p *ParquetStore) CollectRetired() (int, error) {
	p.pinMu.Lock()
	floor := p.epoch
	for epoch := range p.pinned {
		floor = min(floor, epoch)
	}
	ready := 0
	for ready < len(p.retiring) && p.retiring[ready].epoch < floor {
		ready++
	}
	due := slices.Clone(p.retiring[:ready])
	p.pinMu.Unlock()

	removed, done := 0, 0
	var err error
	for _, r := range due {
		for _, b := range r.batches {
			if err = p.removeRetiredBatch(b.ID); err != nil {
				break
			}
			removed++
		}
		if err != nil {
			break
		}
		done++
	}
	if removed > 0 {
		err = errors.Join(err, syncDirectory(p.batchesDir))
	}
	p.pinMu.Lock()
	p.retiring = slices.Delete(p.retiring, 0, done)
	p.retiringIDs = nil
	p.pinMu.Unlock()
	return removed, err
}

func (p *ParquetStore) removeRetiredBatch(id string) error {
	active := p.BatchPath(id)
	// Retired directories reuse retention's suffix: startup cleanup reclaims a
	// removal that was interrupted after the rename.
	retired := filepath.Join(p.batchesDir, id+".retired")
	if err := os.RemoveAll(retired); err != nil {
		return err
	}
	if err := os.Rename(active, retired); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("retire Parquet batch %s: %w", id, err)
	}
	return os.RemoveAll(retired)
}

// writeReplaces durably records, in a staged compacted batch, the inputs its
// publication retires.
func writeReplaces(dir string, inputs []string) error {
	data, err := json.Marshal(inputs)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, replacesFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeBytesFile(path, data); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func readReplaces(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, replacesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, replacesFile), err)
	}
	for _, id := range ids {
		if err := ValidateBatchID(id); err != nil {
			return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, replacesFile), err)
		}
	}
	return ids, nil
}

// retiredAtLoad returns the present batch directories that a present compacted
// batch replaced, ordered so that a batch comes after every retired batch it
// replaced itself. Deleting in that order never orphans a replaced batch whose
// only retirement record is the directory being deleted.
func retiredAtLoad(present map[string]bool, replaces map[string][]string) []string {
	retired := map[string]bool{}
	for _, ids := range replaces {
		for _, id := range ids {
			if present[id] {
				retired[id] = true
			}
		}
	}
	ordered := make([]string, 0, len(retired))
	visited := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, child := range replaces[id] {
			if retired[child] {
				visit(child)
			}
		}
		ordered = append(ordered, id)
	}
	ids := make([]string, 0, len(retired))
	for id := range retired {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		visit(id)
	}
	return ordered
}
