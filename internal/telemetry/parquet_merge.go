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
	"golang.org/x/sync/errgroup"
)

// Format 4 has one physical value per leaf column per row, including nulls:
// nested attributes live in VARIANT, not repeated Parquet lists. Merge the
// narrow sort keys of the signal's order once, then apply the same input-row
// schedule to every physical column. No VARIANT reconstruction or wide-row
// conversion is needed.
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
	var groups []parquet.RowGroup
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
		groups = append(groups, pf.RowGroups()...)
	}
	for _, path := range schema.Columns() {
		leaf, _ := schema.Lookup(path...)
		if leaf.MaxRepetitionLevel != 0 {
			return errors.New("compaction requires non-repeated physical columns")
		}
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
	for i := range columns {
		columns[i] = mergeColumn{column: i, groups: groups, readers: make([]parquet.ColumnChunkValueReader, len(groups)), remaining: make([]int64, len(groups))}
	}
	defer func() {
		for i := range columns {
			err = errors.Join(err, columns[i].close())
		}
	}()
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
		for count < parquetRowGroupRows && len(keys) > 0 {
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
		for i := range columns {
			work.Go(func() error { return columns[i].write(workerCtx, writers[i], runs) })
		}
		if e := work.Wait(); e != nil {
			return e
		}
		if e := writer.Flush(); e != nil {
			return e
		}
		stats.Rows += int64(count)
	}
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

type mergeRun struct{ input, rows int }

func appendMergeRun(runs []mergeRun, input, rows int) []mergeRun {
	if len(runs) > 0 && runs[len(runs)-1].input == input {
		runs[len(runs)-1].rows += rows
		return runs
	}
	return append(runs, mergeRun{input, rows})
}

type mergeColumn struct {
	column    int
	groups    []parquet.RowGroup
	readers   []parquet.ColumnChunkValueReader
	remaining []int64
	values    [256]parquet.Value
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
		r := c.readers[run.input]
		if r == nil {
			r = parquet.NewColumnChunkValueReader(c.groups[run.input].ColumnChunks()[c.column])
			c.readers[run.input] = r
			c.remaining[run.input] = c.groups[run.input].NumRows()
		}
		for remaining := run.rows; remaining > 0; {
			n, e := r.ReadValues(c.values[:min(remaining, len(c.values))])
			if n > 0 {
				written, err := writer.WriteRowValues(c.values[:n])
				clear(c.values[:n])
				if err != nil {
					return err
				}
				if written != n {
					return io.ErrShortWrite
				}
				remaining -= n
				c.remaining[run.input] -= int64(n)
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
		if c.remaining[run.input] == 0 {
			if err := r.Close(); err != nil {
				return err
			}
			c.readers[run.input] = nil
		}
	}
	// Flush the final partial page concurrently too. Flush on the parent is
	// called only after every column is finished, so footer statistics and row
	// counts are observed without racing any column writer.
	return writer.Flush()
}
