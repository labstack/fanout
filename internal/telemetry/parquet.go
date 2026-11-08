package telemetry

import (
	"container/heap"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/fanout/internal/metrics"
	"github.com/parquet-go/parquet-go"
	"github.com/zeebo/xxh3"
	"golang.org/x/sync/errgroup"
)

const (
	BatchSuffix = ".batch"
	SchemaBatch = "_schema" + BatchSuffix
	// scripts/release-notes.sh reads this declaration to mark format-changing
	// releases; keep it a single integer literal.
	batchMetadataVersion = 5
	parquetPageSize      = 64 << 10
	// A column whose distinct values outgrow this switches to plain pages for
	// the rest of its row group, so client-supplied text cannot grow a
	// dictionary without bound.
	parquetDictionaryMaxBytes = 1 << 20
	parquetRowGroupRows       = 50_000
	maxTraceQueryResults      = 500
)

// commitPublishWait caps how long one ingest commit waits for the publish
// gate. It sits above the publisher's own swap budget so it only fires when a
// publication is genuinely stuck rather than merely slow, and it keeps a stuck
// publication from being indistinguishable from a hung ingest path.
const commitPublishWait = 60 * time.Second

var syncPublishedDirectory = syncDirectory

type BatchMetadata struct {
	Version           uint32 `json:"version"`
	ID                string `json:"id"`
	MinIngestedNanos  int64  `json:"min_ingested_nanos"`
	MaxIngestedNanos  int64  `json:"max_ingested_nanos"`
	MinSpanStartNanos int64  `json:"min_span_start_nanos"`
	MaxSpanStartNanos int64  `json:"max_span_start_nanos"`
	Generation        uint32 `json:"generation"`
	Spans             int    `json:"spans"`
	Logs              int    `json:"logs"`
	Metrics           int    `json:"metrics"`
	// Bytes is the batch's on-disk size, derived rather than persisted: it is
	// measured when a batch is loaded, from stats the load already performs.
	// Zero means not measured.
	Bytes int64 `json:"-"`
	// Event times come from fixed-schema Parquet footer statistics, not ingest
	// timestamps. They are derived on publication/load and never change format 5.
	SpanTime   TimeRange `json:"-"`
	LogTime    TimeRange `json:"-"`
	MetricTime TimeRange `json:"-"`
}

// Signal returns the signal's event-time bounds and row count.
func (m BatchMetadata) Signal(s *Signal) (TimeRange, int) {
	f := m.signal(s)
	return *f.time, *f.rows
}

type signalMetadata struct {
	rows *int
	time *TimeRange
}

func (m *BatchMetadata) signal(s *Signal) signalMetadata {
	switch s {
	case Spans:
		return signalMetadata{&m.Spans, &m.SpanTime}
	case Logs:
		return signalMetadata{&m.Logs, &m.LogTime}
	case Metrics:
		return signalMetadata{&m.Metrics, &m.MetricTime}
	}
	panic("unknown signal " + s.Name)
}

type TimeRange struct {
	MinNanos, MaxNanos int64
	Known              bool
}

type TraceQuery struct {
	TraceID   string
	Namespace string
	// ExactNamespace matches Namespace exactly, including an empty namespace.
	ExactNamespace bool
	StartNanos     int64
	EndNanos       int64
	Limit          int
}

type storedBatch struct {
	metadata BatchMetadata
	dir      string
	traces   traceIndex
}

type ParquetStore struct {
	dir                string
	batchesDir         string
	stagingDir         string
	mu                 sync.RWMutex
	publishGate        chan struct{}
	batches            map[string]*storedBatch
	generation0Batches int
	// Published after each catalog mutation; scrapes never take mu.
	generation0Snapshot atomic.Int64
	generation0ByWindow map[int64]int
	compactionDebt      int
	debtLimitReached    chan struct{}
	// Guarded by mu: the replaces list of every compacted directory still in
	// the batch namespace, and the inverse from each listed input to it.
	replaces   map[string][]string
	replacedBy map[string]string

	// pinMu guards reader epochs and the batches retired at each epoch.
	pinMu       sync.Mutex
	epoch       uint64
	pinned      map[uint64]int
	retiring    []retirement
	retiringIDs map[string]BatchMetadata
}

type ParquetStats struct {
	Files int
	Bytes int64
}

func OpenParquetStore(dir string) (*ParquetStore, error) {
	p := &ParquetStore{
		dir: dir, batchesDir: filepath.Join(dir, "batches"), stagingDir: filepath.Join(dir, "staging"),
		publishGate: make(chan struct{}, 1), batches: make(map[string]*storedBatch),
	}
	p.publishGate <- struct{}{}
	// Reject incompatible published data before touching staging or schema files.
	if err := validateBatchFormats(p.batchesDir); err != nil {
		return nil, err
	}
	for _, path := range []string{p.dir, p.batchesDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, err
		}
	}
	// Staging is never acknowledged or queried. Removing it is the complete
	// recovery protocol for writes interrupted before atomic publication.
	if err := os.RemoveAll(p.stagingDir); err != nil {
		return nil, fmt.Errorf("clear incomplete Parquet batches: %w", err)
	}
	if err := os.Mkdir(p.stagingDir, 0o755); err != nil {
		return nil, err
	}
	if err := p.ensureSchemaBatch(); err != nil {
		return nil, err
	}
	if err := p.loadBatches(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *ParquetStore) Close() error       { return nil }
func (p *ParquetStore) Dir() string        { return p.dir }
func (p *ParquetStore) BatchesDir() string { return p.batchesDir }

func (p *ParquetStore) Pattern(signal string) string {
	return filepath.ToSlash(filepath.Join(p.batchesDir, "*"+BatchSuffix, signal+".parquet"))
}

func (p *ParquetStore) BatchPath(id string) string {
	return filepath.Join(p.batchesDir, id+BatchSuffix)
}

func (p *ParquetStore) StagingPath(id string) string {
	return filepath.Join(p.stagingDir, id)
}

// ParquetMergeStats records row counts and the existing span metadata bounds
// without decoding the completed output a second time.
type ParquetMergeStats struct {
	Rows              int64
	MinSpanStartNanos int64
	MaxSpanStartNanos int64
}

// MergeParquet rewrites immutable files into one native Parquet output. Span
// rows retain the index order required by trace.fidx and build that sidecar as
// they are copied; the other signals keep their input order. Re-encoding also
// consolidates tiny ingest row groups.
func (p *ParquetStore) MergeParquet(ctx context.Context, signal string, inputs []string, output string) (ParquetMergeStats, error) {
	var stats ParquetMergeStats
	s, ok := LookupSignal(signal)
	if !ok {
		return stats, fmt.Errorf("unsupported Parquet signal %q", signal)
	}
	err := mergeParquet(ctx, s, inputs, output, &stats)
	return stats, err
}

func (p *ParquetStore) BatchMetadata() []BatchMetadata {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]BatchMetadata, 0, len(p.batches))
	for _, batch := range p.batches {
		out = append(out, batch.metadata)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Generation != out[j].Generation {
			return out[i].Generation < out[j].Generation
		}
		if out[i].MinIngestedNanos != out[j].MinIngestedNanos {
			return out[i].MinIngestedNanos < out[j].MinIngestedNanos
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// batchMetadataByID returns one installed batch without scanning the catalog.
func (p *ParquetStore) batchMetadataByID(id string) (BatchMetadata, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	batch, ok := p.batches[id]
	if !ok {
		return BatchMetadata{}, false
	}
	return batch.metadata, true
}

func (p *ParquetStore) RowCount() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var count uint64
	for _, batch := range p.batches {
		count += uint64(batch.metadata.Spans + batch.metadata.Logs + batch.metadata.Metrics)
	}
	return count
}

// CommitBatch publishes one atomic batch directory. Preparation is allowed to
// finish; only the final publish-gate wait is capped. A stalled publication
// therefore cannot hang ingest, while a large valid encode does not consume
// the gate budget before it starts waiting. On success metadata is replaced
// with the actual published metadata, including an adopted earlier attempt.
func (p *ParquetStore) CommitBatch(ctx context.Context, metadata *BatchMetadata, spans []Span, logs []Log, metrics []Metric) error {
	if err := ValidateBatchID(metadata.ID); err != nil {
		return err
	}
	metadata.Version = batchMetadataVersion
	metadata.Spans, metadata.Logs, metadata.Metrics = len(spans), len(logs), len(metrics)
	final := p.BatchPath(metadata.ID)
	// A retried commit of a batch already compacted away is published: its
	// rows live in the replacement, whether or not its directory was deleted.
	// Adopting or rewriting it would count them twice.
	if p.alreadyPublished(metadata) {
		return nil
	}
	if info, err := os.Stat(final); err == nil && info.IsDir() {
		// Adopting a directory left by an interrupted commit means validating
		// it — Parquet footers plus a full trace-index walk. That is done
		// before taking the gate, so re-registering one batch cannot stall
		// every other publisher and ingest worker behind it.
		adopted, err := loadRegisteredBatch(final)
		if err != nil {
			return err
		}
		if err := p.lockCommitPublish(ctx); err != nil {
			return err
		}
		defer p.unlockPublish()
		// A replacement published while the directory was validated retires it.
		if p.alreadyPublished(metadata) {
			return nil
		}
		// The directory is already in the namespace, so the catalog includes
		// it even if the fsync fails; see the rename below.
		p.installBatch(adopted)
		*metadata = adopted.metadata
		return syncDirectory(p.batchesDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	stage := filepath.Join(p.stagingDir, metadata.ID)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()

	for i := range spans {
		start := spans[i].StartUnixNanos
		if start > 0 && (metadata.MinSpanStartNanos == 0 || start < metadata.MinSpanStartNanos) {
			metadata.MinSpanStartNanos = start
		}
		metadata.MaxSpanStartNanos = max(metadata.MaxSpanStartNanos, start)
	}
	var writes errgroup.Group
	if len(spans) > 0 {
		writes.Go(func() error { return commitSignal(stage, Spans, spans) })
	}
	if len(logs) > 0 {
		writes.Go(func() error { return commitSignal(stage, Logs, logs) })
	}
	if len(metrics) > 0 {
		writes.Go(func() error { return commitSignal(stage, Metrics, metrics) })
	}
	writes.Go(func() error { return writeJSONFile(filepath.Join(stage, "metadata.json"), metadata) })
	if err := writes.Wait(); err != nil {
		return err
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}
	prepared, err := loadStoredBatch(stage)
	if err != nil {
		return fmt.Errorf("validate staged Parquet batch: %w", err)
	}
	if prepared.metadata.ID != metadata.ID {
		return fmt.Errorf("staged Parquet metadata ID %q does not match batch ID %q", prepared.metadata.ID, metadata.ID)
	}
	prepared.dir = final
	if prepared.metadata.Spans > 0 {
		prepared.traces.path = filepath.Join(final, "trace.fidx")
	}

	if err := p.lockCommitPublish(ctx); err != nil {
		return err
	}
	defer p.unlockPublish()
	if p.alreadyPublished(metadata) {
		complete = true
		return os.RemoveAll(stage)
	}
	if err := os.Rename(stage, final); err != nil {
		if info, statErr := os.Stat(final); statErr == nil && info.IsDir() {
			complete = true
			// The staged copy lost the race and is now unreferenced; drop it
			// rather than leaving it to accumulate until the next open.
			removeErr := os.RemoveAll(stage)
			if err := syncDirectory(p.batchesDir); err != nil {
				return errors.Join(err, removeErr)
			}
			if err := p.registerBatch(final); err != nil {
				return errors.Join(err, removeErr)
			}
			*metadata, _ = p.batchMetadataByID(metadata.ID)
			return removeErr
		}
		return fmt.Errorf("publish Parquet batch: %w", err)
	}
	complete = true
	// A renamed directory is published even if the fsync below fails: it is
	// in the namespace now and loads at the next open if it survives. Readers
	// see exactly the catalog, so it joins the catalog; the failed commit's
	// stamp then cannot pass the rollup horizon unread, and a retry adopts it.
	p.mu.Lock()
	p.setBatchLocked(metadata.ID, prepared)
	p.mu.Unlock()
	*metadata = prepared.metadata
	return syncDirectory(p.batchesDir)
}

// alreadyPublished replaces metadata with the published batch's, when known,
// and reports whether the batch needs no further commit. A batch named only by
// a replaces list keeps the attempt's metadata: its rows are already counted
// in the replacement.
func (p *ParquetStore) alreadyPublished(metadata *BatchMetadata) bool {
	existing, ok := p.published(metadata.ID)
	if ok && existing.ID != "" {
		*metadata = existing
	}
	return ok
}

func (p *ParquetStore) lockPublish(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.publishGate:
		return nil
	}
}

func (p *ParquetStore) lockCommitPublish(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, commitPublishWait)
	defer cancel()
	return p.lockPublish(waitCtx)
}

func (p *ParquetStore) unlockPublish() { p.publishGate <- struct{}{} }

// RestoreRetiredInputs rolls back a compaction whose durable output vanished.
// Readers see the inputs only once the catalog installs them.
func (p *ParquetStore) RestoreRetiredInputs(inputs []string, replacementID string, publish func(func(context.Context) error) error) error {
	if err := ValidateBatchID(replacementID); err != nil {
		return err
	}
	for _, id := range inputs {
		if err := ValidateBatchID(id); err != nil {
			return err
		}
	}
	type restoredInput struct {
		id      string
		active  string
		retired string
		batch   *storedBatch
		move    bool
	}
	prepared := make([]restoredInput, 0, len(inputs))
	for _, id := range inputs {
		active := p.BatchPath(id)
		path := active
		move := false
		if _, err := os.Stat(active); errors.Is(err, os.ErrNotExist) {
			path = filepath.Join(p.batchesDir, id+".retired-"+replacementID)
			move = true
		} else if err != nil {
			return err
		}
		batch, err := loadStoredBatch(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("input %s is missing", id)
			}
			return fmt.Errorf("load input %s: %w", id, err)
		}
		batch.dir = active
		if batch.metadata.Spans > 0 {
			batch.traces.path = filepath.Join(active, "trace.fidx")
		}
		prepared = append(prepared, restoredInput{id: id, active: active, retired: path, batch: batch, move: move})
	}
	return publish(func(ctx context.Context) error {
		if err := p.lockPublish(ctx); err != nil {
			return err
		}
		defer p.unlockPublish()
		installActive := func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			for _, input := range prepared {
				if _, err := os.Stat(input.active); err == nil {
					// The replacement never reached the namespace, so its
					// replaces list no longer covers the restored input.
					if lister, ok := p.replacedBy[input.id]; ok && lister == replacementID {
						delete(p.replacedBy, input.id)
					}
					p.setBatchLocked(input.id, input.batch)
				} else {
					p.setBatchLocked(input.id, nil)
				}
			}
		}

		moved := make([]restoredInput, 0, len(prepared))
		for _, input := range prepared {
			if !input.move {
				continue
			}
			if err := os.Rename(input.retired, input.active); err != nil {
				var rollbackErr error
				for i := len(moved) - 1; i >= 0; i-- {
					rollbackErr = errors.Join(rollbackErr, os.Rename(moved[i].active, moved[i].retired))
				}
				installActive()
				return errors.Join(err, rollbackErr, syncDirectory(p.batchesDir))
			}
			moved = append(moved, input)
		}
		var syncErr error
		if len(moved) > 0 {
			syncErr = syncDirectory(p.batchesDir)
		}
		installActive()
		return syncErr
	})
}

// TraceTotals describes the whole trace, not the page a limit admitted.
//
// These come free: readIndexedTrace already decodes every row of the trace and
// applies the same predicates a separate aggregate would, and only the heap is
// bounded by Limit. Deriving them here removes a second pass over every batch
// file -- the thing that made trace_detail pay for its own correctness.
type TraceTotals struct {
	// Root prefers a parentless span, then start time and span ID, across all
	// matching indexed ranges, independently of the detail page limit.
	Root         *IndexedSpan
	Spans        int
	Services     int
	DurationMS   float64
	HasError     bool
	MinStartNano int64
	MaxEndNano   int64
}

// Trace reads only ranges selected by the persistent hash index. Scope filters
// and the limit are applied while decoding so a pathological trace cannot grow
// request memory without bound. The totals it returns describe the whole trace,
// not the page the limit admitted.
func (p *ParquetStore) Trace(ctx context.Context, query TraceQuery) ([]IndexedSpan, TraceTotals, error) {
	var totals TraceTotals
	services := make(map[string]struct{}, 8)
	if query.TraceID == "" {
		return nil, totals, nil
	}
	if query.Limit <= 0 {
		return nil, totals, errors.New("trace query limit must be positive")
	}
	if query.Limit > maxTraceQueryResults {
		return nil, totals, fmt.Errorf("trace query limit exceeds %d", maxTraceQueryResults)
	}
	if query.StartNanos >= query.EndNanos {
		return nil, totals, errors.New("trace query time range must be positive")
	}
	hash := xxh3.HashString(query.TraceID)
	p.mu.RLock()
	batches := make([]*storedBatch, 0, len(p.batches))
	for _, batch := range p.batches {
		batches = append(batches, batch)
	}
	p.mu.RUnlock()
	selected := make(indexedSpanHeap, 0, query.Limit)
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return nil, totals, err
		}
		if bounds := batch.metadata.SpanTime; bounds.Known &&
			(bounds.MaxNanos < query.StartNanos || bounds.MinNanos >= query.EndNanos) {
			continue
		}
		match, found, err := batch.traces.Lookup(hash)
		if err != nil {
			return nil, totals, err
		}
		if !found {
			continue
		}
		if err := readIndexedTrace(ctx, batch, match, query, &selected, &totals, services); err != nil {
			return nil, totals, err
		}
	}
	totals.Services = len(services)
	if totals.Spans > 0 && totals.MaxEndNano > totals.MinStartNano {
		totals.DurationMS = float64(totals.MaxEndNano-totals.MinStartNano) / float64(time.Millisecond)
	}
	out := []IndexedSpan(selected)
	sort.Slice(out, func(i, j int) bool {
		return indexedSpanEarlier(out[i], out[j])
	})
	return out, totals, nil
}

// indexedSpanRow reads the IndexedSpan columns of a span file.
type indexedSpanRow struct {
	Namespace     string  `parquet:"namespace"`
	TraceID       string  `parquet:"trace_id"`
	SpanID        string  `parquet:"span_id"`
	ParentSpanID  string  `parquet:"parent_span_id"`
	Service       string  `parquet:"service"`
	Operation     string  `parquet:"operation"`
	Kind          string  `parquet:"kind"`
	StartUnixNano int64   `parquet:"start_unix_nano"`
	DurationMS    float64 `parquet:"duration_ms"`
	Status        string  `parquet:"status"`
	StatusMessage string  `parquet:"status_message"`
}

func (r indexedSpanRow) span() IndexedSpan {
	return IndexedSpan{
		Namespace: r.Namespace, TraceID: r.TraceID, SpanID: r.SpanID, ParentSpanID: r.ParentSpanID,
		ServiceName: r.Service, Name: r.Operation, Kind: r.Kind, StartUnixNanos: r.StartUnixNano,
		DurationMS: r.DurationMS, StatusCode: r.Status, StatusMsg: r.StatusMessage,
	}
}

func readIndexedTrace(ctx context.Context, batch *storedBatch, match traceRange, query TraceQuery, selected *indexedSpanHeap, totals *TraceTotals, services map[string]struct{}) (err error) {
	file, err := os.Open(filepath.Join(batch.dir, "spans.parquet"))
	if err != nil {
		return err
	}
	reader := parquet.NewGenericReader[indexedSpanRow](file)
	defer func() { err = errors.Join(err, reader.Close(), file.Close()) }()
	return readIndexedRange(ctx, reader, match, query.EndNanos, func(row indexedSpanRow) {
		if row.TraceID != query.TraceID || row.StartUnixNano < query.StartNanos || row.StartUnixNano >= query.EndNanos || ((query.ExactNamespace || query.Namespace != "") && row.Namespace != query.Namespace) {
			return
		}
		accumulateTraceRow(row, totals, services)
		selected.Add(row.span(), query.Limit)
	})
}

func accumulateTraceRow(row indexedSpanRow, totals *TraceTotals, services map[string]struct{}) {
	// Counted before the heap, so the totals describe the trace while
	// the heap keeps describing the page.
	totals.Spans++
	if row.Service != "" {
		services[row.Service] = struct{}{}
	}
	if row.Status == "STATUS_CODE_ERROR" {
		totals.HasError = true
	}
	if totals.MinStartNano == 0 || row.StartUnixNano < totals.MinStartNano {
		totals.MinStartNano = row.StartUnixNano
	}
	if end := row.StartUnixNano + int64(row.DurationMS*float64(time.Millisecond)); end > totals.MaxEndNano {
		totals.MaxEndNano = end
	}
	span := row.span()
	if totals.Root == nil {
		totals.Root = new(IndexedSpan)
	}
	if totals.Spans == 1 || indexedRootEarlier(span, *totals.Root) {
		*totals.Root = span
	}
}

func readIndexedRange(ctx context.Context, reader *parquet.GenericReader[indexedSpanRow], match traceRange, end int64, visit func(indexedSpanRow)) error {
	if match.row > uint64(math.MaxInt64) {
		return errors.New("trace index row exceeds Parquet reader limit")
	}
	if err := reader.SeekToRow(int64(match.row)); err != nil {
		return err
	}
	remaining := match.count
	buffer := make([]indexedSpanRow, min(uint64(8192), remaining))
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := reader.Read(buffer[:int(min(uint64(len(buffer)), remaining))])
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		for _, row := range buffer[:n] {
			// Rows within one hash range are ordered by event time.
			if row.StartUnixNano >= end {
				return nil
			}
			visit(row)
		}
		remaining -= uint64(n)
	}
	return nil
}

// TraceIdentity keeps equal trace IDs in different namespaces separate.
type TraceIdentity struct{ Namespace, TraceID string }

// TraceSummaries reads bounded identities in one batch pass, opening each index
// and matching span file once. Hash collisions are resolved by exact identity.
func (p *ParquetStore) TraceSummaries(ctx context.Context, ids []TraceIdentity, start, end int64) ([]TraceTotals, error) {
	if len(ids) > 20 {
		return nil, errors.New("trace summaries limit exceeds 20")
	}
	if start >= end {
		return nil, errors.New("trace query time range must be positive")
	}
	totals := make([]TraceTotals, len(ids))
	positions := make(map[TraceIdentity]int, len(ids))
	services := make([]map[string]struct{}, len(ids))
	hashes := make([]uint64, 0, len(ids))
	for i, id := range ids {
		positions[id] = i
		services[i] = map[string]struct{}{}
		hashes = append(hashes, xxh3.HashString(id.TraceID))
	}
	slices.Sort(hashes)
	hashes = slices.Compact(hashes)
	p.mu.RLock()
	batches := make([]*storedBatch, 0, len(p.batches))
	for _, batch := range p.batches {
		batches = append(batches, batch)
	}
	p.mu.RUnlock()
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if bounds := batch.metadata.SpanTime; bounds.Known && (bounds.MaxNanos < start || bounds.MinNanos >= end) {
			continue
		}
		if err := readBatchTraceSummaries(ctx, batch, hashes, end, func(row indexedSpanRow) {
			if row.StartUnixNano < start || row.StartUnixNano >= end {
				return
			}
			if i, ok := positions[TraceIdentity{row.Namespace, row.TraceID}]; ok {
				accumulateTraceRow(row, &totals[i], services[i])
			}
		}); err != nil {
			return nil, err
		}
	}
	for i := range totals {
		totals[i].Services = len(services[i])
		if totals[i].Spans > 0 && totals[i].MaxEndNano > totals[i].MinStartNano {
			totals[i].DurationMS = float64(totals[i].MaxEndNano-totals[i].MinStartNano) / float64(time.Millisecond)
		}
	}
	return totals, nil
}

func readBatchTraceSummaries(ctx context.Context, batch *storedBatch, hashes []uint64, end int64, visit func(indexedSpanRow)) (err error) {
	if len(hashes) == 0 || batch.traces.entries == 0 {
		return nil
	}
	index, err := os.Open(batch.traces.path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, index.Close()) }()
	var matches []traceRange
	for _, hash := range hashes {
		match, found, err := batch.traces.lookup(index, hash)
		if err != nil {
			return err
		}
		if found {
			matches = append(matches, match)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	file, err := os.Open(filepath.Join(batch.dir, "spans.parquet"))
	if err != nil {
		return err
	}
	reader := parquet.NewGenericReader[indexedSpanRow](file)
	defer func() { err = errors.Join(err, reader.Close(), file.Close()) }()
	for _, match := range matches {
		if err := readIndexedRange(ctx, reader, match, end, visit); err != nil {
			return err
		}
	}
	return nil
}

type indexedSpanHeap []IndexedSpan

func (h indexedSpanHeap) Len() int { return len(h) }
func (h indexedSpanHeap) Less(i, j int) bool {
	return indexedSpanEarlier(h[j], h[i])
}
func (h indexedSpanHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *indexedSpanHeap) Push(value any) {
	*h = append(*h, value.(IndexedSpan))
}
func (h *indexedSpanHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func (h *indexedSpanHeap) Add(span IndexedSpan, limit int) {
	if len(*h) < limit {
		heap.Push(h, span)
		return
	}
	if indexedSpanEarlier(span, (*h)[0]) {
		(*h)[0] = span
		heap.Fix(h, 0)
	}
}

func indexedRootEarlier(left, right IndexedSpan) bool {
	if (left.ParentSpanID == "") != (right.ParentSpanID == "") {
		return left.ParentSpanID == ""
	}
	if left.StartUnixNanos != right.StartUnixNanos {
		return left.StartUnixNanos < right.StartUnixNanos
	}
	if left.SpanID != right.SpanID {
		return left.SpanID < right.SpanID
	}
	return left.Namespace < right.Namespace
}

func indexedSpanEarlier(left, right IndexedSpan) bool {
	if left.StartUnixNanos != right.StartUnixNanos {
		return left.StartUnixNanos < right.StartUnixNanos
	}
	if left.DurationMS != right.DurationMS {
		return left.DurationMS > right.DurationMS
	}
	return left.SpanID < right.SpanID
}

// PruneBefore removes complete batches from the catalog in one swap. Readers
// never wait: directories stay in place until CollectRetired finds no reader
// that could still open them. Each batch is durably marked pruned before the
// swap, so a crash before deletion retires it at the next open instead of
// republishing it. Callers serialize this with compaction, so the candidates
// stay in the catalog until the swap; if the swap fails, marked batches stay
// listed until the next pass prunes them.
func (p *ParquetStore) PruneBefore(cutoff int64, maxBatches int, publish func(func(context.Context) error) error) (int, error) {
	if maxBatches <= 0 {
		return 0, nil
	}
	type candidate struct {
		id  string
		max int64
	}
	p.mu.RLock()
	candidates := make([]candidate, 0, len(p.batches))
	for id, batch := range p.batches {
		if batch.metadata.MaxIngestedNanos > 0 && batch.metadata.MaxIngestedNanos < cutoff {
			candidates = append(candidates, candidate{id: id, max: batch.metadata.MaxIngestedNanos})
		}
	}
	p.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].max != candidates[j].max {
			return candidates[i].max < candidates[j].max
		}
		return candidates[i].id < candidates[j].id
	})
	if len(candidates) > maxBatches {
		candidates = candidates[:maxBatches]
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	for _, candidate := range candidates {
		if err := writePruned(p.BatchPath(candidate.id)); err != nil {
			return 0, fmt.Errorf("mark Parquet batch %s pruned: %w", candidate.id, err)
		}
	}
	pruned := 0
	err := publish(func(ctx context.Context) error {
		if err := p.lockPublish(ctx); err != nil {
			return err
		}
		defer p.unlockPublish()
		retired := make([]BatchMetadata, 0, len(candidates))
		p.mu.Lock()
		for _, candidate := range candidates {
			if batch, exists := p.batches[candidate.id]; exists {
				retired = append(retired, batch.metadata)
				p.setBatchLocked(candidate.id, nil)
			}
		}
		p.mu.Unlock()
		p.retire(retired)
		pruned = len(retired)
		return nil
	})
	return pruned, err
}

func (p *ParquetStore) Stats() (map[string]ParquetStats, error) {
	p.mu.RLock()
	dirs := make([]string, 0, len(p.batches))
	for _, batch := range p.batches {
		dirs = append(dirs, batch.dir)
	}
	p.mu.RUnlock()
	stats := map[string]ParquetStats{}
	for _, s := range Signals {
		stats[s.Name] = ParquetStats{}
	}
	for _, dir := range dirs {
		for signal := range stats {
			info, err := os.Stat(filepath.Join(dir, signal+".parquet"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			value := stats[signal]
			value.Files++
			value.Bytes += info.Size()
			stats[signal] = value
		}
	}
	return stats, nil
}

// PrepareReplacement durably records metadata after all signal merges and the
// streaming trace index have completed. Span bounds come from the merge.
func (p *ParquetStore) PrepareReplacement(dir string, metadata BatchMetadata) error {
	metadata.Version = batchMetadataVersion
	if err := writeJSONFile(filepath.Join(dir, "metadata.json"), metadata); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// PublishReplacement validates a prepared compacted batch and swaps it for its
// inputs. The output's rename commits the replacement: its replaces list
// retires the inputs, whose directories stay in place for readers that
// captured the old catalog until CollectRetired deletes them. No reader is
// excluded or waited for.
func (p *ParquetStore) PublishReplacement(stage string, metadata BatchMetadata, inputs []string, publish func(func(context.Context) error) error) error {
	if err := ValidateBatchID(metadata.ID); err != nil {
		return err
	}
	for _, id := range inputs {
		if err := ValidateBatchID(id); err != nil {
			return err
		}
	}
	final := p.BatchPath(metadata.ID)
	source := stage
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		source = final
	} else if err != nil {
		return err
	}
	if source == stage {
		if err := writeReplaces(stage, inputs); err != nil {
			return fmt.Errorf("record compaction inputs: %w", err)
		}
	}
	replacement, err := loadStoredBatch(source)
	if err != nil {
		return err
	}
	replacement.dir = final
	if replacement.metadata.Spans > 0 {
		replacement.traces.path = filepath.Join(final, "trace.fidx")
	}
	return publish(func(ctx context.Context) error {
		if err := p.lockPublish(ctx); err != nil {
			return err
		}
		defer p.unlockPublish()
		if source != final {
			if err := os.Rename(stage, final); err != nil {
				return fmt.Errorf("publish compacted Parquet batch: %w", err)
			}
			source = final
		}
		syncErr := syncPublishedDirectory(p.batchesDir)
		// The namespace already holds the replacement, and its replaces list
		// retires the inputs at the next open whatever happens here, so the
		// catalog follows it. Inputs are queued for deletion only once the
		// output is durable; until then marker recovery retries the fsync.
		retired := make([]BatchMetadata, 0, len(inputs))
		p.mu.Lock()
		for _, id := range inputs {
			if batch, exists := p.batches[id]; exists {
				retired = append(retired, batch.metadata)
			}
			p.setBatchLocked(id, nil)
		}
		p.setBatchLocked(metadata.ID, replacement)
		// Whether or not the fsync succeeded, the output's replaces list now
		// covers its inputs: a retried commit of one must neither adopt its
		// directory nor write it again once it is deleted.
		p.recordReplacesLocked(metadata.ID, inputs)
		p.mu.Unlock()
		// Count catalog retirements once, even if marker cleanup or fsync is
		// retried. Already absent inputs must not inflate retirement throughput.
		metrics.CompactionRetiredBatches.Add(float64(len(retired)))
		if syncErr != nil {
			return syncErr
		}
		for _, id := range inputs {
			if !slices.ContainsFunc(retired, func(b BatchMetadata) bool { return b.ID == id }) {
				if _, statErr := os.Stat(p.BatchPath(id)); statErr == nil {
					retired = append(retired, BatchMetadata{ID: id})
				}
			}
		}
		p.retire(retired)
		return nil
	})
}

// ensureSchemaBatch writes _schema.batch: an empty Parquet batch whose only
// purpose is to carry this binary's column list. internal/engine derives every
// view's explicit read_parquet(schema=MAP{...}) from it, so a column absent
// here is a column absent from the view.
//
// It is rewritten on every open rather than created once. Written once, it
// pinned the views to whichever build first created the data directory, for
// the life of that deployment -- and a later build that added a span column
// could not start at all: the view would not expose it,
// CreateViews would fail to bind it, NewDuck would return an error, and
// restarting would not help because the stale file was still there. The files
// are empty, so this is three small writes per boot.
func (p *ParquetStore) ensureSchemaBatch() error {
	final := filepath.Join(p.batchesDir, SchemaBatch)
	stage := filepath.Join(p.stagingDir, "_schema")
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		return err
	}
	if err := writeParquet(filepath.Join(stage, "spans.parquet"), Spans, []Span{}, parquetPageSize); err != nil {
		return err
	}
	if err := writeParquet(filepath.Join(stage, "logs.parquet"), Logs, []Log{}, parquetPageSize); err != nil {
		return err
	}
	if err := writeParquet(filepath.Join(stage, "metrics.parquet"), Metrics, []Metric{}, parquetPageSize); err != nil {
		return err
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}
	// Rename cannot replace a non-empty directory, so the old one is removed
	// first. A crash between the two leaves no _schema.batch at all, which the
	// next open recreates: the batch holds no rows, only the column list.
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	return syncDirectory(p.batchesDir)
}

func (p *ParquetStore) loadBatches() error {
	entries, err := os.ReadDir(p.batchesDir)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	replaces := map[string][]string{}
	pruned := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == SchemaBatch || !strings.HasSuffix(entry.Name(), BatchSuffix) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), BatchSuffix)
		present[id] = true
		dir := filepath.Join(p.batchesDir, entry.Name())
		ids, err := readReplaces(dir)
		if err != nil {
			return fmt.Errorf("load Parquet batch %s: %w", entry.Name(), err)
		}
		if len(ids) > 0 {
			replaces[id] = ids
		}
		marked, err := hasPruned(dir)
		if err != nil {
			return fmt.Errorf("load Parquet batch %s: %w", entry.Name(), err)
		}
		if marked {
			pruned[id] = true
		}
	}
	p.mu.Lock()
	for id, ids := range replaces {
		p.recordReplacesLocked(id, ids)
	}
	p.mu.Unlock()
	// A present compacted batch has committed its replacement, so the inputs
	// it lists are retired even if their directories survived a crash; a
	// batch marked pruned was already removed by retention. They are never
	// registered; the first collection deletes them.
	retiredIDs := retiredAtLoad(present, replaces, pruned)
	retired := make(map[string]bool, len(retiredIDs))
	for _, id := range retiredIDs {
		retired[id] = true
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), BatchSuffix)
		if !ok || !entry.IsDir() || !present[id] || retired[id] {
			continue
		}
		if err := p.registerBatch(filepath.Join(p.batchesDir, entry.Name())); err != nil {
			return fmt.Errorf("load Parquet batch %s: %w", entry.Name(), err)
		}
	}
	if len(retiredIDs) > 0 {
		batches := make([]BatchMetadata, len(retiredIDs))
		for i, id := range retiredIDs {
			metadata, err := readBatchMetadata(filepath.Join(p.BatchPath(id), "metadata.json"))
			if err != nil {
				return fmt.Errorf("load retired Parquet batch %s: %w", id, err)
			}
			batches[i] = metadata
		}
		p.retire(batches)
	}
	return nil
}

func (p *ParquetStore) registerBatch(dir string) error {
	batch, err := loadRegisteredBatch(dir)
	if err != nil {
		return err
	}
	p.installBatch(batch)
	return nil
}

// loadRegisteredBatch validates a published directory and is safe to call
// outside the publish gate: it only reads files that are already immutable.
func loadRegisteredBatch(dir string) (*storedBatch, error) {
	batch, err := loadStoredBatch(dir)
	if err != nil {
		return nil, err
	}
	if filepath.Base(dir) != batch.metadata.ID+BatchSuffix {
		return nil, fmt.Errorf("parquet batch directory %q does not match metadata ID %q", filepath.Base(dir), batch.metadata.ID)
	}
	return batch, nil
}

// verifyTraces checks every row's trace hash and that the trace index holds
// exactly the file's runs of equal hashes; verifyOrder checks their order.
func verifyTraces(path string, expected int, index traceIndex) (err error) {
	indexFile, err := os.Open(index.path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, indexFile.Close()) }()
	var current traceRange
	var have bool
	var entry uint64
	flush := func() error {
		if !have {
			return nil
		}
		indexed, err := readTraceRangeAt(indexFile, entry)
		if err != nil {
			return err
		}
		if indexed != current {
			return fmt.Errorf("trace index entry %d does not match span rows", entry)
		}
		entry++
		have = false
		return nil
	}
	err = verifyTypedParquet[verificationSpanRow](path, expected, func(row verificationSpanRow, position uint64) error {
		if row.TraceHash != xxh3.HashString(row.TraceID) {
			return fmt.Errorf("span row %d trace hash does not match trace ID", position)
		}
		if have && current.hash == row.TraceHash {
			current.count++
			return nil
		}
		if have && current.hash > row.TraceHash {
			return fmt.Errorf("span row %d is not sorted by trace hash", position)
		}
		if err := flush(); err != nil {
			return err
		}
		current = traceRange{hash: row.TraceHash, row: position, count: 1}
		have = true
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if entry != index.entries {
		return fmt.Errorf("trace index has %d entries; span rows produced %d", index.entries, entry)
	}
	return nil
}

func verifyTypedParquet[T any](path string, expected int, visit func(T, uint64) error) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	reader := parquet.NewGenericReader[T](file)
	defer func() { err = errors.Join(err, reader.Close()) }()
	buffer := make([]T, 256)
	var rows uint64
	for {
		n, readErr := reader.Read(buffer)
		for i := 0; i < n; i++ {
			if visit != nil {
				if err := visit(buffer[i], rows); err != nil {
					return err
				}
			}
			rows++
		}
		clear(buffer[:n])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if rows != uint64(expected) {
		return fmt.Errorf("decoded %d rows; metadata declares %d", rows, expected)
	}
	return nil
}

// installBatch publishes a validated batch into the queryable set.
func (p *ParquetStore) installBatch(batch *storedBatch) {
	p.mu.Lock()
	p.setBatchLocked(batch.metadata.ID, batch)
	p.mu.Unlock()
}

func loadStoredBatch(dir string) (*storedBatch, error) {
	metadata, err := readBatchMetadata(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	for _, signal := range Signals {
		fields := metadata.signal(signal)
		count := fields.rows
		path := filepath.Join(dir, signal.Name+".parquet")
		if *count == 0 {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("%s parquet exists but metadata declares zero rows", signal.Name)
			}
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s Parquet is not a regular file", signal.Name)
		}
		// Size the batch from the stat this validation already performs.
		// Compaction prices a merge by the bytes it admits, and measuring here
		// costs nothing over the estimate it would otherwise fall back to.
		metadata.Bytes += info.Size()
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		// The footer alone says what this validation checks: schema, row
		// count and the time column's chunk statistics. The page index
		// behind it holds a column index and offset index per leaf, and the
		// format has many leaves, so reading them cost as much as the
		// footer on every commit.
		parquetFile, openErr := parquet.OpenFile(file, info.Size(), parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
		closeErr := file.Close()
		if err := errors.Join(openErr, closeErr); err != nil {
			return nil, fmt.Errorf("open %s Parquet: %w", signal.Name, err)
		}
		if !parquet.EqualNodes(parquetFile.Schema(), signal.schema) {
			return nil, fmt.Errorf("%w: %s parquet schema does not match telemetry format %d", ErrUnsupportedBatchFormat, signal.Name, batchMetadataVersion)
		}
		if err := checkColumnPaths(parquetFile, signal); err != nil {
			return nil, fmt.Errorf("%s Parquet: %w", signal.Name, err)
		}
		if rows := parquetFile.NumRows(); rows != int64(*count) {
			return nil, fmt.Errorf("%s Parquet has %d rows; metadata declares %d", signal.Name, rows, *count)
		}
		*fields.time = parquetTimeRange(parquetFile, signal.Time)
	}
	var traces traceIndex
	if metadata.Spans > 0 {
		traces, err = loadTraceIndex(filepath.Join(dir, "trace.fidx"), metadata.Spans)
		if err != nil {
			return nil, err
		}
	}
	return &storedBatch{metadata: metadata, dir: dir, traces: traces}, nil
}

// checkColumnPaths requires every column chunk to name its schema leaf.
// Readers find a chunk's statistics by PathInSchema, so a wrong path silently
// loses the file's event-time bounds rather than failing.
func checkColumnPaths(file *parquet.File, s *Signal) error {
	leaves := s.schema.Columns()
	for g, group := range file.Metadata().RowGroups {
		if len(group.Columns) != len(leaves) {
			return fmt.Errorf("row group %d has %d column chunks; schema has %d", g, len(group.Columns), len(leaves))
		}
		for i, chunk := range group.Columns {
			if !slices.Equal(chunk.MetaData.PathInSchema, leaves[i]) {
				return fmt.Errorf("row group %d column %d path %q; schema path %q", g, i, chunk.MetaData.PathInSchema, leaves[i])
			}
		}
	}
	return nil
}

// readSpills reads the footer's count of spills for each of the signal's
// standard keys, by key bit.
func readSpills(file *parquet.File, s *Signal) (spills, error) {
	var counts spills
	keys := map[string]uint8{}
	for _, key := range s.Keys() {
		keys[key.Name] = key.Bit
	}
	for _, kv := range file.Metadata().KeyValueMetadata {
		name, ok := strings.CutPrefix(kv.Key, spillPrefix)
		if !ok {
			continue
		}
		bit, ok := keys[name]
		if !ok {
			return counts, fmt.Errorf("footer counts spills of %q, which has no dedicated column", name)
		}
		n, err := strconv.ParseUint(kv.Value, 10, 64)
		if err != nil || n == 0 {
			return counts, fmt.Errorf("footer spill count of %q is %q", name, kv.Value)
		}
		counts[bit] = n
	}
	return counts, nil
}

// Statistics are optional in Parquet. Without complete bounds, the file stays
// in every candidate snapshot. Ingest bounds never substitute for event time.
func parquetTimeRange(file *parquet.File, column string) TimeRange {
	var result TimeRange
	for _, group := range file.Metadata().RowGroups {
		found := false
		for _, chunk := range group.Columns {
			if len(chunk.MetaData.PathInSchema) != 1 || chunk.MetaData.PathInSchema[0] != column {
				continue
			}
			stats := chunk.MetaData.Statistics
			if len(stats.MinValue) != 8 || len(stats.MaxValue) != 8 {
				return TimeRange{}
			}
			low := int64(binary.LittleEndian.Uint64(stats.MinValue))
			high := int64(binary.LittleEndian.Uint64(stats.MaxValue))
			if low > high {
				return TimeRange{}
			}
			if !result.Known || low < result.MinNanos {
				result.MinNanos = low
			}
			if !result.Known || high > result.MaxNanos {
				result.MaxNanos = high
			}
			result.Known, found = true, true
		}
		if !found {
			return TimeRange{}
		}
	}
	return result
}

// writerOptions are the schema and footer sort order every file of the signal
// is written with.
func (s *Signal) writerOptions() []parquet.WriterOption {
	options := []parquet.WriterOption{s.schema}
	if claim := s.Order.claim(); len(claim) > 0 {
		sorting := make([]parquet.SortingColumn, len(claim))
		for i, name := range claim {
			sorting[i] = parquet.Ascending(name)
		}
		options = append(options, parquet.SortingWriterConfig(parquet.SortingColumns(sorting...)))
	}
	return options
}

// commitSignal writes one signal of a staged batch in the signal's order,
// with its trace index when the signal has one. The index needs only the
// sorted hashes, so it is written while the Parquet file is.
func commitSignal[R any](stage string, s *Signal, rows []R) error {
	perm, keys := sortRows(s, rows)
	var index errgroup.Group
	if s.Traces {
		hashes := make([]uint64, len(perm))
		for i, row := range perm {
			hashes[i] = keys.row(int(row))[0]
		}
		index.Go(func() error {
			if err := writeTraceIndex(filepath.Join(stage, "trace.fidx"), hashes); err != nil {
				return fmt.Errorf("write trace index: %w", err)
			}
			return nil
		})
	}
	err := writeOrdered(filepath.Join(stage, s.Name+".parquet"), s, rows, perm, parquetPageSize)
	if err != nil {
		err = fmt.Errorf("write %s Parquet: %w", s.Name, err)
	}
	return errors.Join(err, index.Wait())
}

func parquetWriterOptions(pageSize int, extra ...parquet.WriterOption) []parquet.WriterOption {
	options := []parquet.WriterOption{
		parquet.Compression(parquetCodec),
		parquet.MaxRowsPerRowGroup(parquetRowGroupRows),
		parquet.PageBufferSize(pageSize),
		parquet.DictionaryMaxBytes(parquetDictionaryMaxBytes),
	}
	return append(options, extra...)
}

// writeParquet writes rows to a new file in the signal's order.
func writeParquet[R any](path string, s *Signal, rows []R, pageSize int, options ...parquet.WriterOption) error {
	perm, _ := sortRows(s, rows)
	return writeOrdered(path, s, rows, perm, pageSize, options...)
}

// writeOrdered writes rows[perm[0]], rows[perm[1]] and so on to a new file.
func writeOrdered[R any](path string, s *Signal, rows []R, perm []int32, pageSize int, options ...parquet.WriterOption) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	writer := parquet.NewGenericWriter[any](f, parquetWriterOptions(pageSize, append(s.writerOptions(), options...)...)...)
	var spilled spills
	if err := writeColumns(writer, s, rows, perm, &spilled); err != nil {
		_ = writer.Close()
		return err
	}
	spilled.footer(writer, s)
	if err := writer.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func readBatchMetadata(path string) (BatchMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BatchMetadata{}, err
	}
	var metadata BatchMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return BatchMetadata{}, err
	}
	if metadata.Version != batchMetadataVersion {
		return BatchMetadata{}, fmt.Errorf("%w: version %d", ErrUnsupportedBatchFormat, metadata.Version)
	}
	if err := ValidateBatchID(metadata.ID); err != nil {
		return BatchMetadata{}, err
	}
	if metadata.Spans < 0 || metadata.Logs < 0 || metadata.Metrics < 0 {
		return BatchMetadata{}, errors.New("parquet batch metadata has a negative row count")
	}
	if metadata.MinIngestedNanos > 0 && metadata.MaxIngestedNanos > 0 && metadata.MinIngestedNanos > metadata.MaxIngestedNanos {
		return BatchMetadata{}, errors.New("parquet batch metadata has an inverted time range")
	}
	return metadata, nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeBytesFile(path, data)
}

func writeBytesFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// ValidateBatchID is the one grammar for identifiers that become directory
// names under the storage root. It is exported because the store package
// validates marker-supplied IDs against the same rule before joining them to a
// path; a second copy there could drift and leave the two gates disagreeing
// about what is a safe name.
func ValidateBatchID(id string) error {
	if id == "" || len(id) > 128 || id[0] == '.' || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("invalid telemetry batch ID %q", id)
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("invalid telemetry batch ID %q", id)
		}
	}
	return nil
}
