package telemetry

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
	"golang.org/x/sync/errgroup"
)

// Merge the narrow sort keys of the signal's order once, then apply the same
// input-row schedule to every physical column. A scalar leaf holds one value
// per row, nulls included; a list leaf holds each row's elements as a run of
// values, which mergeColumn copies by whole rows. No VARIANT reconstruction
// or wide-row conversion is needed.
// Columns own their input cursors and output buffers; only footer publication
// is serial. At most eight columns decode/encode at once, sharing the existing
// compression pool and preserving the ingest writer's row-group ceiling, and
// the columns' open readers are bounded (mergeReaderBudget).
func mergeParquet(ctx context.Context, s *Signal, inputs []string, output string, stats *ParquetMergeStats) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(inputs) == 0 {
		return errors.New("merge Parquet requires at least one input")
	}
	var files []*os.File
	defer func() {
		for _, f := range files {
			err = errors.Join(err, f.Close())
		}
	}()
	schema := s.schema
	var spilled spills
	var groups []parquet.RowGroup
	var groupFiles []io.ReaderAt
	var chunkBytes []format.RowGroup // by group: its footer metadata
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, e := os.Open(input)
		if e != nil {
			return e
		}
		files = append(files, f)
		info, e := f.Stat()
		if e != nil {
			return e
		}
		pf, e := parquet.OpenFile(f, info.Size(), parquet.ReadBufferSize(chunkReadBuffer), parquet.OptimisticRead(true))
		if e != nil {
			return e
		}
		if !parquet.EqualNodes(schema, pf.Schema()) {
			return fmt.Errorf("%w: compaction input schema", ErrUnsupportedBatchFormat)
		}
		counts, e := readSpills(pf, s)
		if e != nil {
			return e
		}
		for bit, n := range counts {
			spilled[bit] += n
		}
		groups = append(groups, pf.RowGroups()...)
		for range pf.RowGroups() {
			groupFiles = append(groupFiles, f)
		}
		chunkBytes = append(chunkBytes, pf.Metadata().RowGroups...)
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(output)
		}
	}()
	pageSize := parquetPageSize
	// Logs with payloads benefit from larger pages in the groups that still
	// need merging. Numeric/list signals retain the ingest page target.
	if s == Logs {
		pageSize = 128 << 10
	}
	bufferedOutput := bufio.NewWriterSize(out, 256<<10)
	assembler, err := newMergeAssembler(bufferedOutput, s, spilled)
	if err != nil {
		return err
	}
	var index *traceIndexWriter
	if s.Traces {
		index, err = newTraceIndexWriter(filepath.Join(filepath.Dir(output), "trace.fidx"))
		if err != nil {
			return err
		}
		defer func() {
			if !ok {
				index.Abort()
			}
		}()
	}
	cursors := make([]*keyCursor, len(groups))
	for i, group := range groups {
		cursors[i] = newKeyCursor(s, i, group)
	}
	defer func() {
		for _, c := range cursors {
			err = errors.Join(err, c.close())
		}
	}()
	keys := make(keyHeap, 0, len(cursors))
	for _, c := range cursors {
		if err := ctx.Err(); err != nil {
			return err
		}
		has, e := c.next()
		if e != nil {
			return e
		}
		if has {
			keys = append(keys, c)
		}
	}
	heap.Init(&keys)
	columns := make([]mergeColumn, len(schema.Columns()))
	for i, path := range schema.Columns() {
		leaf, _ := schema.Lookup(path...)
		columns[i] = mergeColumn{column: i, repeated: leaf.MaxRepetitionLevel > 0, maxDefinition: leaf.MaxDefinitionLevel, flatNull: leaf.MaxRepetitionLevel == 0 && leaf.MaxDefinitionLevel == 1, groups: groups, files: groupFiles, meta: chunkBytes,
			readers: make([]parquet.ColumnChunkValueReader, len(groups)), remaining: make([]int64, len(groups)), started: make([]bool, len(groups))}
		if columns[i].repeated {
			columns[i].pending = make([][]parquet.Value, len(groups))
			columns[i].buffers = make([][]parquet.Value, len(groups))
			columns[i].eof = make([]bool, len(groups))
		}
	}
	defer func() {
		for i := range columns {
			err = errors.Join(err, columns[i].close())
		}
	}()
	// Shredded VARIANT groups are merged as units, re-encoding their values
	// with shared metadata (variant_merge.go).
	var variants []*variantMerge
	for _, c := range s.Columns {
		if c.Stored() && c.Type == Variant && !c.List {
			v, e := newVariantMerge(schema, c.Name, columns, len(groups))
			if e != nil {
				return e
			}
			variants = append(variants, v)
		}
	}
	// Identical canonical metadata gives every input the same field IDs.
	// Their leaves can follow the ordinary column schedule without rebuilding
	// the VARIANT objects, including sparse leaves that are constant.
	rewriting := variants[:0]
	for _, v := range variants {
		keep, e := mergeVariantKeepsMetadata(groups, v)
		if e != nil {
			return e
		}
		if !keep {
			rewriting = append(rewriting, v)
		}
	}
	variants = rewriting
	owned := make([]bool, len(columns))
	// rowBytes[group][variant] is a row's share of the variant's uncompressed
	// input bytes, which bounds an output row group's buffered variant state.
	rowBytes := make([][]int64, len(groups))
	for g := range groups {
		rowBytes[g] = make([]int64, len(variants))
		rows := groups[g].NumRows()
		for i, v := range variants {
			var total int64
			for _, c := range v.columns {
				total += chunkBytes[g].Columns[c.column].MetaData.TotalUncompressedSize
			}
			if rows > 0 {
				rowBytes[g][i] = (total + rows - 1) / rows
			}
		}
	}
	for _, v := range variants {
		for _, c := range v.columns {
			owned[c.column] = true
		}
	}
	buffered := make([]int64, len(variants))
	// Work units in schema order: a VARIANT group is one unit.
	var units []mergeUnit
	for i := range columns {
		if !owned[i] {
			units = append(units, mergeUnit{column: i, width: 1})
			continue
		}
		for _, v := range variants {
			if v.columns[0].column == i {
				units = append(units, mergeUnit{variant: v, width: len(v.columns)})
			}
		}
	}
	// Columns certified constant across the complete merge are encoded once
	// per output size. They do not need a value reader or an output buffer for
	// every row group. VARIANT leaves remain owned by their group rewriter.
	constants := make([]*mergeConstant, len(columns))
	allRuns := make([]mergeRun, len(groups))
	for i, g := range groups {
		allRuns[i] = mergeRun{i, int(g.NumRows())}
	}
	for i := range columns {
		if !owned[i] {
			if value, ok := columns[i].constantValue(allRuns); ok {
				constants[i] = newMergeConstant(schema, i, value)
				continue
			}
		}
	}
	// Price only columns that actually open readers. Counting the sparse
	// constants would needlessly split wide format-5 merges into extra windows.
	activeUnits := units[:0]
	for _, u := range units {
		if u.variant != nil || constants[u.column] == nil {
			activeUnits = append(activeUnits, u)
		}
	}
	units = activeUnits
	var rowGroups []*mergeOutput
	defer func() {
		for _, o := range rowGroups {
			if o != nil && o.writer != nil {
				_ = o.writer.Close()
			}
		}
	}()

	active := make([]bool, len(groups))
	// groupRowBytes is a row's share of its input's compressed bytes.
	groupRowBytes := make([]int64, len(groups))
	for g := range groups {
		if rows := groups[g].NumRows(); rows > 0 {
			groupRowBytes[g] = (chunkBytes[g].TotalCompressedSize + rows - 1) / rows
		}
	}
	// Give workers a standard, concurrency-safe context even when a caller wraps
	// Context.Err for instrumentation. Cancellation never leaves workers behind.
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	width := 0
	for _, u := range units {
		width += u.width
	}
	// A merge that can batch its columns writes a window of row groups per
	// batch, so that a batch's readers are reopened once per window rather
	// than once per row group. The window's encoded row groups are held until
	// they are committed in order.
	window := 1
	if len(groups)*width > mergeReaderBudget {
		window = mergeWindowRowGroups
		if pageSize > parquetPageSize {
			// A window retains every output group's column buffers. Scale its
			// group count with the page target so larger log pages do not grow
			// that memory budget, including merges smaller than a full window.
			var rows int64
			for _, group := range groups {
				rows += group.NumRows()
			}
			window = mergeLogWindowLimit(rows, pageSize)
			// Assembly also retains the compressed bytes of each slot. Keep
			// that scratch within the original window's memory allowance.
			window = max(1, window/2)
		}
	}
	copyable := make([]int8, len(groups)) // unknown, -1 requires rewrite, +1 copyable
	// Preserve a large input group as a unit when its complete key range is
	// before every competing cursor. This also works when a preceding overlap
	// consumed only part of an output group: finish that group first.
	canCopyNext := func() (bool, error) {
		if len(keys) == 0 || s == Logs {
			return false, nil
		}
		c := keys[0]
		g := groups[c.input]
		rows := g.NumRows()
		if c.remaining != rows-1 || rows < parquetRowGroupRows/2 || rows > parquetRowGroupRows {
			return false, nil
		}
		for _, n := range rowBytes[c.input] {
			if n*rows > maxVariantRowGroupBytes {
				return false, nil
			}
		}
		if copyable[c.input] == 0 {
			keep, e := mergeGroupKeepsVariantMetadata(g, variants)
			if e != nil {
				return false, e
			}
			copyable[c.input] = -1
			if keep {
				copyable[c.input] = 1
			}
		}
		if copyable[c.input] < 0 {
			return false, nil
		}
		if len(keys) == 1 {
			return true, nil
		}
		other := keys[1]
		if len(keys) > 2 && keys.Less(2, 1) {
			other = keys[2]
		}
		last, e := mergeLastKey(s, g)
		if e != nil {
			return false, e
		}
		cmp := s.order.compare(&last, &other.key)
		return cmp < 0 || cmp == 0 && c.input < other.input, nil
	}
	// Wide metric VARIANT rewrites retain row-local metadata. Smaller rewrite
	// groups offset the assembly scratch without changing the ingest ceiling or
	// splitting the already-ordered groups copied below. Small merges retain
	// ingest-sized groups to avoid spending their savings on extra footers.
	rowTarget := parquetRowGroupRows
	var totalRows int64
	for _, group := range groups {
		totalRows += group.NumRows()
	}
	if s == Metrics && totalRows >= 1_000_000 && len(inputs) > 1 && len(variants) > 0 {
		rowTarget = 30_000
		// A lower reader allowance must not multiply buffered output groups
		// on wide, overlapping compacted inputs. Their indexed pages can
		// resume per group; tiny ingest inputs still share a bounded window.
		window = 1
	}
	// Small merges amortize less decoding work over each projected schema.
	// Reuse one projection there; large merges also elide local constants.
	localConstants := totalRows >= 1_000_000
	var pending []mergeRowGroup
	for done := false; !done; {
		pending = pending[:0]
		var inputBytes int64
		for len(pending) < window && inputBytes < mergeWindowBytes {
			if err := ctx.Err(); err != nil {
				return err
			}
			var runs []mergeRun
			if len(pending) < cap(pending) {
				runs = pending[:len(pending)+1][len(pending)].runs[:0]
			}
			count := 0
			clear(buffered)
			full := false
			for count < rowTarget && len(keys) > 0 && !full {
				copyNext, e := canCopyNext()
				if e != nil {
					return e
				}
				if copyNext && count > 0 {
					break
				}
				c := keys[0]
				if copyNext && !s.Traces {
					count = int(groups[c.input].NumRows())
					runs = appendMergeRun(runs, c.input, count)
					inputBytes += int64(count) * groupRowBytes[c.input]
					if e := c.close(); e != nil {
						return e
					}
					heap.Pop(&keys)
					break
				}
				if s.Traces {
					// The span order leads with the trace hash, then start time.
					hash, start := c.key.n[0], int64(c.key.n[1]^1<<63)
					if start > 0 && (stats.MinSpanStartNanos == 0 || start < stats.MinSpanStartNanos) {
						stats.MinSpanStartNanos = start
					}
					stats.MaxSpanStartNanos = max(stats.MaxSpanStartNanos, start)
					if e := index.Append(hash); e != nil {
						return e
					}
				}
				runs = appendMergeRun(runs, c.input, 1)
				count++
				inputBytes += groupRowBytes[c.input]
				for i, n := range rowBytes[c.input] {
					buffered[i] += n
					full = full || buffered[i] >= maxVariantRowGroupBytes
				}
				has, e := c.next()
				if e != nil {
					return e
				}
				if has {
					heap.Fix(&keys, 0)
				} else {
					heap.Pop(&keys)
					if len(runs) == 1 && int64(count) == groups[c.input].NumRows() && copyable[c.input] > 0 {
						break
					}
				}
			}
			if count == 0 {
				done = true
				break
			}
			rg := mergeRowGroup{runs: runs, rows: count, chunks: make([]mergeChunk, len(columns)), constant: make([]bool, len(columns)), values: make([]parquet.Value, len(columns))}
			var paths [][]string
			for i := range columns {
				if !owned[i] {
					var value parquet.Value
					var constant bool
					if localConstants {
						value, constant = columns[i].constantValue(runs)
					} else if c := constants[i]; c != nil {
						value, constant = c.value, true
					}
					if constant {
						rg.constant[i] = true
						rg.values[i] = value
						continue
					}
				}
				paths = append(paths, schema.Columns()[i])
			}
			slot := len(pending)
			if slot == len(rowGroups) {
				rowGroups = append(rowGroups, nil)
			}
			output := rowGroups[slot]
			if len(paths) > 0 {
				if output == nil || !slices.Equal(output.constant, rg.constant) {
					if output != nil && output.writer != nil {
						_ = output.writer.Close()
					}
					projected := parquet.NewSchema(schema.Name(), mergeProjectedNode{schema, mergeProjection(schema, paths)})
					output = &mergeOutput{constant: slices.Clone(rg.constant), schema: projected}
					rowGroups[slot] = output
				}
				// Each committed group gets a fresh writer. Resetting a closed
				// parquet-go v0.32.0 writer clears the column paths its footer
				// metadata aliases, so later groups would lose PathInSchema.
				if output.writer == nil {
					output.encoded.Reset()
					output.writer = parquet.NewGenericWriter[any](&output.encoded, parquetWriterOptions(pageSize, output.schema)...)
					output.group = output.writer.BeginRowGroup()
				}
				rg.output = output
			}
			rg.writers = make([]*parquet.ColumnWriter, len(columns))
			j := 0
			for i := range columns {
				if !rg.constant[i] {
					rg.writers[i] = output.group.ColumnWriters()[j]
					j++
				}
			}
			// A whole input row group already in output order needs neither
			// decoding nor VARIANT dictionary rewriting. Assembly retains its
			// compressed chunks and relocates their page indexes.
			if len(runs) == 1 && int64(count) == groups[runs[0].input].NumRows() {
				input := runs[0].input
				if copyable[input] == 0 {
					canCopy, e := mergeGroupKeepsVariantMetadata(groups[input], variants)
					if e != nil {
						return e
					}
					copyable[input] = -1
					if canCopy {
						copyable[input] = 1
					}
				}
				if copyable[input] > 0 {
					rg.source = groups[input]
				}
			}
			pending = append(pending, rg)
			if len(pending) == 1 && window > 1 {
				clear(active)
				if mergeInputs(runs, active)*width <= mergeReaderBudget {
					break // the row group's readers fit: no batches to share
				}
			}
		}
		if len(pending) == 0 {
			break
		}
		clear(active)
		inputs := 0
		for _, rg := range pending {
			inputs += mergeInputs(rg.runs, active)
		}
		for _, batch := range mergeBatches(units, inputs) {
			var work errgroup.Group
			work.SetLimit(min(8, runtime.GOMAXPROCS(0)))
			for _, u := range batch {
				work.Go(func() error {
					for _, rg := range pending {
						if rg.source != nil {
							continue
						}
						var err error
						if u.variant != nil {
							err = u.variant.write(workerCtx, rg.writers, rg.runs)
						} else if !rg.constant[u.column] {
							err = columns[u.column].write(workerCtx, rg.writers[u.column], rg.runs)
						} else {
							err = columns[u.column].skip(rg.runs)
						}
						if err != nil {
							return err
						}
					}
					return nil
				})
			}
			if e := work.Wait(); e != nil {
				return e
			}
			if len(batch) < len(units) {
				// Batched: close this batch's readers before the next opens its
				// own; each resumes at its row in the next window.
				for _, u := range batch {
					for _, c := range u.leaves(columns) {
						if e := c.release(); e != nil {
							return e
						}
					}
				}
			}
		}
		for _, rg := range pending {
			if e := ctx.Err(); e != nil {
				return e
			}
			if rg.source == nil {
				output := rg.output
				var pf *parquet.File
				if output != nil {
					if _, e := output.group.Commit(); e != nil {
						return e
					}
					e := output.writer.Close()
					output.writer, output.group = nil, nil
					if e != nil {
						return e
					}
					pf, e = parquet.OpenFile(bytes.NewReader(output.encoded.Bytes()), int64(output.encoded.Len()))
					if e != nil {
						return e
					}
				}
				j := 0
				for i := range columns {
					if rg.constant[i] {
						if constants[i] == nil {
							constants[i] = newMergeConstant(schema, i, rg.values[i])
						}
						c := constants[i]
						value := rg.values[i].Level(0, rg.values[i].DefinitionLevel(), 0)
						if !parquet.Equal(c.value, value) || c.value.DefinitionLevel() != value.DefinitionLevel() {
							c.value = value
							c.rows = 0
						}
						chunk, e := c.chunk(ctx, rg.rows)
						if e != nil {
							return e
						}
						rg.chunks[i] = chunk
					} else {
						rg.chunks[i] = encodedMergeChunk(pf, output.encoded.Bytes(), j)
						j++
					}
				}
			}
			e := assembler.append(rg)
			if e != nil {
				return e
			}
			stats.Rows += int64(rg.rows)
		}
	}
	if err := assembler.close(); err != nil {
		return err
	}
	if err := bufferedOutput.Flush(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if index != nil {
		if err := index.Close(); err != nil {
			return err
		}
	}
	ok = true
	return nil
}

func mergeLogWindowLimit(rows int64, pageSize int) int {
	groups := min(mergeWindowRowGroups, int((rows+parquetRowGroupRows-1)/parquetRowGroupRows))
	return max(1, groups*parquetPageSize/pageSize)
}

// Ingest dictionaries can be unsorted or churn within a group. Keep their
// normal rewrite; only already shared, sorted, bounded dictionaries qualify
// for copying. Inspect the dictionary pages without decoding the row values.
func mergeGroupKeepsVariantMetadata(group parquet.RowGroup, variants []*variantMerge) (bool, error) {
	source, ok := group.(*parquet.FileRowGroup)
	if !ok {
		return false, nil
	}
	pf := source.File()
	ordinal := 0
	for pf.RowGroups()[ordinal] != group {
		ordinal++
	}
	for _, c := range pf.Metadata().RowGroups[ordinal].Columns {
		if c.ColumnIndexLength <= 0 || c.OffsetIndexLength <= 0 || c.FilePath != "" || len(c.EncryptedColumnMetadata) > 0 {
			return false, nil
		}
	}

	for _, v := range variants {
		_, keep, err := mergeMetadataBlob(group.ColumnChunks()[v.columns[v.metadata].column])
		if err != nil || !keep {
			return false, err
		}
	}
	return true, nil
}

// mergeReaderBudget bounds the column chunk readers a merge holds open. A
// reader holds its current page, decoded, and its read-ahead bytes, about 70
// KB measured on a merge of 95 metric ingest files, which opened one reader
// per input and leaf column: 7,500 readers and a 556 MB heap peak, most of a
// compaction's memory under ingest. When an output row group reads more
// inputs than the budget allows for every column, a window of row groups is
// written in column batches, each batch's readers closed before the next
// opens; a reader reopened for the next window seeks to its row through the
// offset index, decoding its page again. On that merge the heap peak fell to
// about 300 MB for 6% more CPU time. Merges of a few compacted files stay
// within the budget and never reopen.
// Assembly and projected schemas share the original live-memory envelope
// with decoded pages. Reserve most of the former reader allowance for them.
var mergeReaderBudget = 256

// mergeUnit is a column, or a VARIANT group, written by one worker.
type mergeUnit struct {
	variant *variantMerge
	column  int
	width   int // leaf columns
}

func (u mergeUnit) leaves(columns []mergeColumn) []*mergeColumn {
	if u.variant != nil {
		return u.variant.columns
	}
	return []*mergeColumn{&columns[u.column]}
}

// mergeBatches splits units into consecutive batches whose readers, one per
// input and leaf, fit mergeReaderBudget; a unit too wide for it is a batch of
// its own.
func mergeBatches(units []mergeUnit, inputs int) [][]mergeUnit {
	var batches [][]mergeUnit
	start, readers := 0, 0
	for i, u := range units {
		n := u.width * inputs
		if i > start && readers+n > mergeReaderBudget {
			batches = append(batches, units[start:i])
			start, readers = i, 0
		}
		readers += n
	}
	return append(batches, units[start:])
}

// mergeInputs marks the inputs of runs in active and returns how many were
// not yet marked.
func mergeInputs(runs []mergeRun, active []bool) int {
	n := 0
	for _, run := range runs {
		if !active[run.input] {
			active[run.input] = true
			n++
		}
	}
	return n
}

// mergeWindowRowGroups and mergeWindowBytes bound the row groups a batched
// merge writes per window, by count and by their rows' input bytes.
var mergeWindowRowGroups = 16

const mergeWindowBytes = 64 << 20

// mergeRowGroup is an output row group of a window: its schedule and writer.
type mergeRowGroup struct {
	runs     []mergeRun
	rows     int
	chunks   []mergeChunk
	output   *mergeOutput
	constant []bool
	values   []parquet.Value
	writers  []*parquet.ColumnWriter
	source   parquet.RowGroup // a whole, already ordered input group
}

// maxVariantRowGroupBytes ends an output row group early once the input
// bytes of any one of its VARIANT columns reach it. variantMerge buffers a
// row group of each such column, its interned names and its rewritten values;
// with wide, unique attribute sets a full row group would hold gigabytes.
// The bound depends only on the inputs, so output stays deterministic.
const maxVariantRowGroupBytes = 64 << 20

type mergeRun struct{ input, rows int }

func appendMergeRun(runs []mergeRun, input, rows int) []mergeRun {
	if len(runs) > 0 && runs[len(runs)-1].input == input {
		runs[len(runs)-1].rows += rows
		return runs
	}
	return append(runs, mergeRun{input, rows})
}

// mergeColumn copies one leaf column's values by the merge's row schedule.
// A non-repeated leaf holds one value per row, nulls included. A list leaf
// holds a row's elements as consecutive values whose first has repetition
// level 0, so it advances by whole rows: values read past the rows a run
// needs wait in pending until that input's next run.
type mergeColumn struct {
	column        int
	repeated      bool
	flatNull      bool
	maxDefinition int
	groups        []parquet.RowGroup
	files         []io.ReaderAt     // by input: the file its group is read from
	meta          []format.RowGroup // by input: its footer metadata
	readers       []parquet.ColumnChunkValueReader
	remaining     []int64 // by input: rows not yet written, once started
	started       []bool  // by input: its reader has been opened
	values        [256]parquet.Value
	pending       [][]parquet.Value // repeated: read values not yet written, by input
	buffers       [][]parquet.Value // repeated: the storage pending slices, by input
	eof           []bool            // repeated: the input's reader is exhausted
}

func (c *mergeColumn) close() (err error) {
	for _, r := range c.readers {
		if r != nil {
			err = errors.Join(err, r.Close())
		}
	}
	return err
}

// A locally constant output group still consumes input rows. Release any
// earlier reader and preserve its resume position for a later mixed group.
func (c *mergeColumn) skip(runs []mergeRun) error {
	for _, run := range runs {
		i := run.input
		if c.readers[i] != nil {
			if err := c.readers[i].Close(); err != nil {
				return err
			}
			c.readers[i] = nil
		}
		if !c.started[i] {
			c.started[i] = true
			c.remaining[i] = c.groups[i].NumRows()
		}
		c.remaining[i] -= int64(run.rows)
		if c.repeated {
			c.pending[i], c.buffers[i], c.eof[i] = nil, nil, false
		}
	}
	return nil
}

func (c *mergeColumn) write(ctx context.Context, writer *parquet.ColumnWriter, runs []mergeRun) error {
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if c.repeated {
			if _, err = c.open(run.input); err == nil {
				err = c.copyRows(writer, run)
			}
			if err == nil {
				err = c.advance(run)
			}
		} else {
			err = c.read(run, func(values []parquet.Value) error {
				written, err := writer.WriteRowValues(values)
				if err == nil && written != len(values) {
					err = io.ErrShortWrite
				}
				return err
			})
		}
		if err != nil {
			return err
		}
	}
	// Flush the final partial page concurrently too. Flush on the parent is
	// called only after every column is finished, so footer statistics and row
	// counts are observed without racing any column writer.
	return writer.Flush()
}

// open starts reading an input's column chunk on its first run, or resumes
// at its next row after release.
func (c *mergeColumn) open(input int) (parquet.ColumnChunkValueReader, error) {
	r := c.readers[input]
	if r == nil {
		r = openChunk(c.files[input], c.groups[input].ColumnChunks()[c.column], &c.meta[input].Columns[c.column].MetaData)
		c.readers[input] = r
		if !c.started[input] {
			c.started[input] = true
			c.remaining[input] = c.groups[input].NumRows()
		} else if err := r.SeekToRow(c.groups[input].NumRows() - c.remaining[input]); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// release closes the column's open readers; open resumes each at the row
// after the last written. Values a list column read past that row are
// dropped and read again.
func (c *mergeColumn) release() error {
	err := c.close()
	clear(c.readers)
	if c.repeated {
		clear(c.pending)
		clear(c.buffers)
		clear(c.eof)
	}
	return err
}

// advance accounts for a run's rows, closing the input's reader after its
// last.
func (c *mergeColumn) advance(run mergeRun) error {
	c.remaining[run.input] -= int64(run.rows)
	if c.remaining[run.input] > 0 {
		return nil
	}
	err := c.readers[run.input].Close()
	c.readers[run.input] = nil
	if c.repeated {
		c.pending[run.input], c.buffers[run.input] = nil, nil
	}
	return err
}

// readSome reads at most len(dst) values of an input's non-repeated leaf in
// one read; the caller accounts for them with advance.
func (c *mergeColumn) readSome(input int, dst []parquet.Value) (int, error) {
	r, err := c.open(input)
	if err != nil {
		return 0, err
	}
	n, err := r.ReadValues(dst)
	if n > 0 && (err == nil || err == io.EOF) {
		return n, nil
	}
	if err == io.EOF {
		return 0, io.ErrUnexpectedEOF
	}
	if err != nil {
		return 0, err
	}
	return 0, io.ErrNoProgress
}

// read passes a run's values of a non-repeated leaf, one per row, to fn in
// order. The values borrow the reader's page and must not be retained.
func (c *mergeColumn) read(run mergeRun, fn func([]parquet.Value) error) error {
	r, err := c.open(run.input)
	if err != nil {
		return err
	}
	for remaining := run.rows; remaining > 0; {
		n, e := r.ReadValues(c.values[:min(remaining, len(c.values))])
		if n > 0 {
			err := fn(c.values[:n])
			clear(c.values[:n])
			if err != nil {
				return err
			}
			remaining -= n
		}
		if e != nil {
			if e == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return e
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return c.advance(run)
}

// copyRows writes a run's rows of a repeated leaf. Values borrow their
// reader's page; before the reader advances, the only values still pending
// are the start of an incomplete row, which are cloned.
func (c *mergeColumn) copyRows(writer *parquet.ColumnWriter, run mergeRun) error {
	pending := c.pending[run.input]
	for need := run.rows; need > 0; {
		// Count the complete rows pending, up to need. A row is complete when
		// the next row starts or the column chunk has ended.
		end, rows := 0, 0
		for i := 1; i <= len(pending) && rows < need; i++ {
			if i < len(pending) && pending[i].RepetitionLevel() != 0 {
				continue
			}
			if i == len(pending) && !c.eof[run.input] {
				break
			}
			end, rows = i, rows+1
		}
		if rows > 0 {
			written, err := writer.WriteRowValues(pending[:end])
			if err != nil {
				return err
			}
			if written != rows {
				return io.ErrShortWrite
			}
			clear(pending[:end])
			pending = pending[end:]
			need -= rows
			continue
		}
		if c.eof[run.input] {
			return io.ErrUnexpectedEOF
		}
		// pending is a suffix of the input's buffer, so moving it to the
		// front copies forward over values already written.
		partial := c.buffers[run.input][:len(pending)]
		for i, v := range pending {
			partial[i] = v.Clone()
		}
		n, e := c.readers[run.input].ReadValues(c.values[:])
		if e == io.EOF {
			c.eof[run.input] = true
		} else if e != nil {
			return e
		} else if n == 0 {
			return io.ErrNoProgress
		}
		if n > 0 && len(partial) == 0 && c.values[0].RepetitionLevel() != 0 {
			return fmt.Errorf("column %d continues a row past its end", c.column)
		}
		pending = append(partial, c.values[:n]...)
		c.buffers[run.input] = pending[:cap(pending)]
		clear(c.values[:n])
	}
	c.pending[run.input] = pending
	return nil
}

// Read the last complete tuple, rather than combining independent footer
// bounds (which need not belong to the same row for compound metric keys).
func mergeLastKey(s *Signal, group parquet.RowGroup) (sortKey, error) {
	c := newKeyCursor(s, 0, group)
	defer c.close()
	for _, r := range c.readers {
		if err := r.SeekToRow(group.NumRows() - 1); err != nil {
			return sortKey{}, err
		}
	}
	c.remaining = 1
	if _, err := c.next(); err != nil {
		return sortKey{}, err
	}
	key := c.key
	for i := range key.s {
		key.s[i] = strings.Clone(key.s[i])
	}
	return key, nil
}

// A dictionary alone does not prove all data pages use it: a writer may
// fall back to plain encoding. Require page encoding statistics as well.
func mergeMetadataBlob(chunk parquet.ColumnChunk) (blob []byte, keep bool, err error) {
	fc, ok := chunk.(*parquet.FileColumnChunk)
	if !ok {
		return nil, false, nil
	}
	if fc.NumValues() > 0 && fc.NullCount() == fc.NumValues() {
		return nil, true, nil
	}
	pf := fc.File()
	var meta *format.ColumnMetaData
	for g, rg := range pf.RowGroups() {
		if rg.ColumnChunks()[fc.Column()] == chunk {
			meta = &pf.Metadata().RowGroups[g].Columns[fc.Column()].MetaData
			break
		}
	}
	if meta == nil || len(meta.EncodingStats) == 0 {
		return nil, false, nil
	}
	pagesCount := int32(0)
	for _, stat := range meta.EncodingStats {
		if stat.PageType == format.DataPage || stat.PageType == format.DataPageV2 {
			if stat.Encoding != format.RLEDictionary {
				return nil, false, nil
			}
			pagesCount += stat.Count
		}
	}
	if pagesCount == 0 {
		return nil, false, nil
	}
	pages := chunk.Pages()
	defer func() { err = errors.Join(err, pages.Close()) }()
	reader, ok := pages.(interface {
		ReadDictionary() (parquet.Dictionary, error)
	})
	if !ok {
		return nil, false, nil
	}
	dictionary, err := reader.ReadDictionary()
	if err != nil {
		return nil, false, err
	}
	if dictionary == nil || dictionary.Len() != 1 {
		return nil, false, nil
	}
	blob = bytes.Clone(dictionary.Index(0).ByteArray())
	names, sorted, e := variantMetadataNames(blob)
	size := 0
	for _, name := range names {
		size += len(name)
	}
	return blob, e == nil && sorted && len(names) <= 256 && size <= variantMetadataBytes, nil
}

func mergeVariantKeepsMetadata(groups []parquet.RowGroup, v *variantMerge) (bool, error) {
	var shared []byte
	for _, g := range groups {
		blob, keep, err := mergeMetadataBlob(g.ColumnChunks()[v.columns[v.metadata].column])
		if err != nil || !keep {
			return false, err
		}
		if blob == nil {
			continue
		}
		if shared == nil {
			shared = blob
		} else if !bytes.Equal(shared, blob) {
			return false, nil
		}
	}
	return true, nil
}
