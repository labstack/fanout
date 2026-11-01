package telemetry

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

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
// compression pool and preserving the ingest writer's row-group ceiling.
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
		pf, e := parquet.OpenFile(f, info.Size(), parquet.ReadBufferSize(16<<10), parquet.OptimisticRead(true))
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
	writer := parquet.NewGenericWriter[any](out, parquetWriterOptions(parquetPageSize, s.writerOptions()...)...)
	// Close on failure too: it returns the column page buffers to their pool.
	defer func() {
		if !ok {
			_ = writer.Close()
		}
	}()
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
		columns[i] = mergeColumn{column: i, repeated: leaf.MaxRepetitionLevel > 0, groups: groups,
			readers: make([]parquet.ColumnChunkValueReader, len(groups)), remaining: make([]int64, len(groups))}
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
	// Give workers a standard, concurrency-safe context even when a caller wraps
	// Context.Err for instrumentation. Cancellation never leaves workers behind.
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runs := make([]mergeRun, 0, parquetRowGroupRows)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		runs = runs[:0]
		count := 0
		clear(buffered)
		full := false
		for count < parquetRowGroupRows && len(keys) > 0 && !full {
			c := keys[0]
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
			}
		}
		if count == 0 {
			break
		}
		writers := writer.ColumnWriters()
		var work errgroup.Group
		work.SetLimit(min(8, runtime.GOMAXPROCS(0)))
		for _, v := range variants {
			work.Go(func() error { return v.write(workerCtx, writers, runs) })
		}
		for i := range columns {
			if !owned[i] {
				work.Go(func() error { return columns[i].write(workerCtx, writers[i], runs) })
			}
		}
		if e := work.Wait(); e != nil {
			return e
		}
		if e := writer.Flush(); e != nil {
			return e
		}
		stats.Rows += int64(count)
	}
	spilled.footer(writer, s)
	if err := writer.Close(); err != nil {
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
	column    int
	repeated  bool
	groups    []parquet.RowGroup
	readers   []parquet.ColumnChunkValueReader
	remaining []int64
	values    [256]parquet.Value
	pending   [][]parquet.Value // repeated: read values not yet written, by input
	buffers   [][]parquet.Value // repeated: the storage pending slices, by input
	eof       []bool            // repeated: the input's reader is exhausted
}

func (c *mergeColumn) close() (err error) {
	for _, r := range c.readers {
		if r != nil {
			err = errors.Join(err, r.Close())
		}
	}
	return err
}

func (c *mergeColumn) write(ctx context.Context, writer *parquet.ColumnWriter, runs []mergeRun) error {
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if c.repeated {
			c.open(run.input)
			if err = c.copyRows(writer, run); err == nil {
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

// open starts reading an input's column chunk on its first run.
func (c *mergeColumn) open(input int) parquet.ColumnChunkValueReader {
	r := c.readers[input]
	if r == nil {
		r = parquet.NewColumnChunkValueReader(c.groups[input].ColumnChunks()[c.column])
		c.readers[input] = r
		c.remaining[input] = c.groups[input].NumRows()
	}
	return r
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
	n, err := c.open(input).ReadValues(dst)
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
	r := c.open(run.input)
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
