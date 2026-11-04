package engine

import "context"

// Pending transfers protect input contributions even after their files retire.
// Entries follow publication order, so multiple compaction generations can be
// transferred without decoding intermediate Parquet files. They are disposable:
// after a restart the worker rebuilds active outputs from Parquet.
type batchCacheTransfer struct {
	output    string
	inputs    []string
	published bool // guarded by cachePublishMu
}

func (d *Duck) pendingCacheTransfer(id string, outputOnly bool) bool {
	d.cachePublishMu.Lock()
	defer d.cachePublishMu.Unlock()
	for _, p := range d.cacheTransfers {
		if p.output == id {
			return true
		}
		if !outputOnly {
			for _, input := range p.inputs {
				if input == id {
					return true
				}
			}
		}
	}
	return false
}

// removeCacheTransfer requires cachePublishMu.
func (d *Duck) removeCacheTransfer(p *batchCacheTransfer) {
	for i, entry := range d.cacheTransfers {
		if entry == p {
			copy(d.cacheTransfers[i:], d.cacheTransfers[i+1:])
			d.cacheTransfers[len(d.cacheTransfers)-1] = nil
			d.cacheTransfers = d.cacheTransfers[:len(d.cacheTransfers)-1]
			return
		}
	}
}

// Caller holds cacheGate and the background SQL lease. Never hold
// cachePublishMu across SQL: publication must remain independent of scans.
func (d *Duck) applyCacheTransfers(ctx context.Context) error {
	d.cachePublishMu.Lock()
	var ready []*batchCacheTransfer
	for _, p := range d.cacheTransfers {
		if !p.published || len(ready) == readCacheBatchLimit {
			break
		}
		ready = append(ready, p)
	}
	d.cachePublishMu.Unlock()
	for _, p := range ready {
		if d.cachePassStarted {
			if err := d.readPressure.Yield(ctx); err != nil {
				return err
			}
		}
		d.cachePassStarted = true
		err := d.transferBatchCacheLocked(ctx, p.output, p.inputs)
		d.cachePublishMu.Lock()
		d.removeCacheTransfer(p)
		d.cachePublishMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
