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

// prunedFile marks a batch retention removed from the catalog. It is written
// and synced before the swap, so a crash before the directory is deleted
// cannot republish rows retention already hid; startup retires it instead.
const prunedFile = "pruned"

// syncRetiredDirectory is the batch-directory fsync CollectRetired issues.
var syncRetiredDirectory = syncDirectory

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
	return pin.pinned && len(p.retiring) > 0 && p.retiring[len(p.retiring)-1].epoch >= pin.epoch
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
// replaces list retires. Removals are made durable before a batch carrying a
// replaces list is removed, so a filesystem that reorders directory operations
// cannot keep an input after losing the output that retired it. Each directory
// leaves the batch namespace with one rename before it is removed, so a partial
// removal is never mistaken for a published batch. Callers serialize this with
// compaction and retention.
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
	unsynced := false
	var err error
	for _, r := range due {
		for _, b := range r.batches {
			if unsynced && p.listsReplaced(b.ID) {
				if err = syncRetiredDirectory(p.batchesDir); err != nil {
					break
				}
				unsynced = false
			}
			if err = p.removeRetiredBatch(b.ID); err != nil {
				break
			}
			p.forgetReplaces(b.ID)
			removed++
			unsynced = true
		}
		if err != nil {
			break
		}
		done++
	}
	if unsynced {
		err = errors.Join(err, syncRetiredDirectory(p.batchesDir))
	}
	p.pinMu.Lock()
	p.retiring = slices.Delete(p.retiring, 0, done)
	p.retiringIDs = nil
	p.pinMu.Unlock()
	return removed, err
}

// recordReplacesLocked notes that lister's directory carries a replaces list.
// While that directory is in the namespace its inputs' rows live in it or in
// a later replacement, so a retried commit of an input is already published.
// Called with mu held.
func (p *ParquetStore) recordReplacesLocked(lister string, inputs []string) {
	if len(inputs) == 0 {
		return
	}
	if p.replaces == nil {
		p.replaces = map[string][]string{}
		p.replacedBy = map[string]string{}
	}
	p.forgetReplacesLocked(lister)
	p.replaces[lister] = slices.Clone(inputs)
	for _, id := range inputs {
		p.replacedBy[id] = lister
	}
}

// forgetReplacesLocked drops lister's record once its directory, and with it
// the durable replaces list, has left the namespace. Called with mu held.
func (p *ParquetStore) forgetReplacesLocked(lister string) {
	for _, id := range p.replaces[lister] {
		if p.replacedBy[id] == lister {
			delete(p.replacedBy, id)
		}
	}
	delete(p.replaces, lister)
}

func (p *ParquetStore) forgetReplaces(lister string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forgetReplacesLocked(lister)
}

func (p *ParquetStore) listsReplaced(id string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.replaces[id]
	return ok
}

// published reports whether a batch ID has already been published: it is in
// the catalog, a present replacement lists it, or it is retired but not yet
// deleted. Metadata is zero when only a replaces list still names the batch.
func (p *ParquetStore) published(id string) (BatchMetadata, bool) {
	p.mu.RLock()
	batch, live := p.batches[id]
	_, replaced := p.replacedBy[id]
	p.mu.RUnlock()
	if live {
		return batch.metadata, true
	}
	if retired, ok := p.retiredMetadata(id); ok {
		return retired, true
	}
	return BatchMetadata{}, replaced
}

// writePruned durably marks a batch directory as removed by retention.
func writePruned(dir string) error {
	path := filepath.Join(dir, prunedFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeBytesFile(path, nil); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func hasPruned(dir string) (bool, error) {
	_, err := os.Lstat(filepath.Join(dir, prunedFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
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
// batch replaced or that retention pruned, ordered so that a batch comes after
// every retired batch it replaced itself. Deleting in that order never orphans
// a replaced batch whose only retirement record is the directory being deleted.
func retiredAtLoad(present map[string]bool, replaces map[string][]string, pruned map[string]bool) []string {
	retired := map[string]bool{}
	for id, marked := range pruned {
		if marked && present[id] {
			retired[id] = true
		}
	}
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
