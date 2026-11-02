package telemetry

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/encoding/thrift"
	"github.com/parquet-go/parquet-go/format"
)

type mergeProjectedField struct {
	parquet.Field
	fields []parquet.Field
}

func (f mergeProjectedField) Fields() []parquet.Field { return f.fields }

type mergeProjectedNode struct {
	parquet.Node
	fields []parquet.Field
}

func (n mergeProjectedNode) Fields() []parquet.Field { return n.fields }

func mergeProjection(node parquet.Node, paths [][]string) []parquet.Field {
	var fields []parquet.Field
	for _, field := range node.Fields() {
		var children [][]string
		for _, p := range paths {
			if len(p) > 0 && p[0] == field.Name() {
				children = append(children, p[1:])
			}
		}
		if len(children) == 0 {
			continue
		}
		if field.Leaf() {
			fields = append(fields, field)
		} else {
			fields = append(fields, mergeProjectedField{field, mergeProjection(field, children)})
		}
	}
	return fields
}

type mergeChunk struct {
	meta        format.ColumnChunk
	data        []byte
	reader      io.ReaderAt
	start       int64
	dictionary  bool // a dictionary can begin at relative offset zero
	columnIndex format.ColumnIndex
	offsetIndex format.OffsetIndex
}

// Nonconstant columns use the library encoder; constants use bounded RLE
// pages. Assembly relocates chunks and indexes under the original full schema.
type mergeAssembler struct {
	out     io.Writer
	offset  int64
	meta    format.FileMetaData
	indexes [][]format.ColumnIndex
	offsets [][]format.OffsetIndex
	sorting []format.SortingColumn
}

func (a *mergeAssembler) Write(p []byte) (int, error) {
	n, err := a.out.Write(p)
	a.offset += int64(n)
	return n, err
}

func newMergeAssembler(out io.Writer, s *Signal, spilled spills) (*mergeAssembler, error) {
	var prototype bytes.Buffer
	w := parquet.NewGenericWriter[any](&prototype, parquetWriterOptions(parquetPageSize, s.writerOptions()...)...)
	spilled.footer(w, s)
	if err := w.Close(); err != nil {
		return nil, err
	}
	pf, err := parquet.OpenFile(bytes.NewReader(prototype.Bytes()), int64(prototype.Len()))
	if err != nil {
		return nil, err
	}
	a := &mergeAssembler{out: out, meta: *pf.Metadata()}
	config, err := parquet.NewWriterConfig(s.writerOptions()...)
	if err != nil {
		return nil, err
	}
	for _, col := range config.Sorting.SortingColumns {
		leaf, ok := s.schema.Lookup(col.Path()...)
		if !ok {
			return nil, fmt.Errorf("merge sort column %v not found", col.Path())
		}
		a.sorting = append(a.sorting, format.SortingColumn{ColumnIdx: int32(leaf.ColumnIndex), Descending: col.Descending(), NullsFirst: col.NullsFirst()})
	}
	_, err = a.Write([]byte("PAR1"))
	return a, err
}

func (a *mergeAssembler) append(rg mergeRowGroup) error {
	if len(a.meta.RowGroups) >= 32767 {
		return parquet.ErrTooManyRowGroups
	}

	if rg.source != nil {
		// File-backed groups keep their compressed chunks. Read only their page
		// indexes here; assembly relocates offsets without decoding values.
		source := rg.source.(*parquet.FileRowGroup)
		pf := source.File()
		group := 0
		for pf.RowGroups()[group] != rg.source {
			group++
		}
		width := len(rg.chunks)
		for i, col := range rg.source.ColumnChunks() {
			fc := col.(*parquet.FileColumnChunk)
			meta := pf.Metadata().RowGroups[group].Columns[i]
			start := meta.MetaData.DataPageOffset
			dictionary := meta.MetaData.DictionaryPageOffset > 0
			if dictionary {
				start = min(start, meta.MetaData.DictionaryPageOffset)
			}
			c := mergeChunk{meta: meta, dictionary: dictionary, reader: fc.File(), start: start}
			c.columnIndex = pf.ColumnIndexes()[group*width+i]
			c.offsetIndex = pf.OffsetIndexes()[group*width+i]
			c.offsetIndex.PageLocations = append([]format.PageLocation(nil), c.offsetIndex.PageLocations...)
			c.meta.MetaData.DataPageOffset -= start
			if dictionary {
				c.meta.MetaData.DictionaryPageOffset -= start
			}
			for j := range c.offsetIndex.PageLocations {
				c.offsetIndex.PageLocations[j].Offset -= start
			}
			rg.chunks[i] = c
		}
	}

	group := format.RowGroup{NumRows: int64(rg.rows), Ordinal: int16(len(a.meta.RowGroups)), FileOffset: a.offset, SortingColumns: a.sorting, Columns: make([]format.ColumnChunk, len(rg.chunks))}
	indexes := make([]format.ColumnIndex, len(rg.chunks))
	offsets := make([]format.OffsetIndex, len(rg.chunks))
	for i, c := range rg.chunks {
		if len(c.data) == 0 && c.reader == nil {
			return fmt.Errorf("merge column %d has no encoded chunk", i)
		}
		meta := c.meta
		// Data is embedded in this file. Bloom filters are optional and are not
		// copied; never retain a pointer into the source file's filter section.
		meta.FilePath = ""
		meta.FileOffset = 0
		meta.MetaData.BloomFilterOffset = 0
		meta.MetaData.BloomFilterLength = 0
		meta.MetaData.IndexPageOffset = 0
		meta.MetaData.DataPageOffset += a.offset
		if c.dictionary {
			meta.MetaData.DictionaryPageOffset += a.offset
		}
		index := c.offsetIndex
		index.PageLocations = append(index.PageLocations[:0:0], index.PageLocations...)
		for j := range index.PageLocations {
			index.PageLocations[j].Offset += a.offset
		}
		if c.reader != nil {
			if _, err := io.Copy(a, io.NewSectionReader(c.reader, c.start, c.meta.MetaData.TotalCompressedSize)); err != nil {
				return err
			}
		} else if _, err := a.Write(c.data); err != nil {
			return err
		}
		group.Columns[i] = meta
		indexes[i] = c.columnIndex
		offsets[i] = index
		group.TotalByteSize += meta.MetaData.TotalUncompressedSize
		group.TotalCompressedSize += meta.MetaData.TotalCompressedSize
	}
	a.meta.NumRows += int64(rg.rows)
	a.meta.RowGroups = append(a.meta.RowGroups, group)
	a.indexes = append(a.indexes, indexes)
	a.offsets = append(a.offsets, offsets)
	return nil
}

func (a *mergeAssembler) close() error {
	var protocol thrift.CompactProtocol
	encoder := thrift.NewEncoder(protocol.NewWriter(a))
	for i, indexes := range a.indexes {
		for j := range indexes {
			c := &a.meta.RowGroups[i].Columns[j]
			c.ColumnIndexOffset = a.offset
			if err := encoder.Encode(&indexes[j]); err != nil {
				return err
			}
			c.ColumnIndexLength = int32(a.offset - c.ColumnIndexOffset)
		}
	}
	for i, indexes := range a.offsets {
		for j := range indexes {
			c := &a.meta.RowGroups[i].Columns[j]
			c.OffsetIndexOffset = a.offset
			if err := encoder.Encode(&indexes[j]); err != nil {
				return err
			}
			c.OffsetIndexLength = int32(a.offset - c.OffsetIndexOffset)
		}
	}
	start := a.offset
	if err := encoder.Encode(&a.meta); err != nil {
		return err
	}
	var footer [8]byte
	binary.LittleEndian.PutUint32(footer[:4], uint32(a.offset-start))
	copy(footer[4:], "PAR1")
	_, err := a.Write(footer[:])
	return err
}

// A constant is private to one merge. Its single cached size bounds memory
// even when VARIANT byte limits produce many different output row counts.
type mergeConstant struct {
	value                        parquet.Value
	rows                         int
	cached                       mergeChunk
	path                         []string
	typ                          format.Type
	maxDefinition, maxRepetition int
}

func newMergeConstant(schema *parquet.Schema, column int, value parquet.Value) *mergeConstant {
	path := schema.Columns()[column]
	leaf, _ := schema.Lookup(path...)
	return &mergeConstant{value: value.Level(0, value.DefinitionLevel(), 0), path: path, typ: format.Type(leaf.Node.Type().Kind()), maxDefinition: leaf.MaxDefinitionLevel, maxRepetition: leaf.MaxRepetitionLevel}
}

func (c *mergeConstant) chunk(ctx context.Context, rows int) (mergeChunk, error) {
	if err := ctx.Err(); err != nil {
		return mergeChunk{}, err
	}
	if c.rows != rows {
		chunk, err := c.constantChunk(rows)
		if err != nil {
			return mergeChunk{}, err
		}
		c.cached = chunk
		c.rows = rows
	}
	return c.cached, nil
}

func encodedMergeChunk(pf *parquet.File, data []byte, column int) mergeChunk {
	meta := pf.Metadata().RowGroups[0].Columns[column]
	start := meta.MetaData.DataPageOffset
	if d := meta.MetaData.DictionaryPageOffset; d > 0 && d < start {
		start = d
	}
	end := start + meta.MetaData.TotalCompressedSize
	chunk := mergeChunk{meta: meta, data: data[start:end], columnIndex: pf.ColumnIndexes()[column], offsetIndex: pf.OffsetIndexes()[column]}
	chunk.meta.MetaData.DataPageOffset -= start
	if chunk.meta.MetaData.DictionaryPageOffset > 0 {
		chunk.meta.MetaData.DictionaryPageOffset -= start
		chunk.dictionary = true
	}
	for j := range chunk.offsetIndex.PageLocations {
		chunk.offsetIndex.PageLocations[j].Offset -= start
	}
	return chunk
}

// Footer bounds can certify a constant scalar without decoding its pages.
// Floating point bounds cannot distinguish signed zero or exclude NaNs.
func (c *mergeColumn) constantValue(runs []mergeRun) (parquet.Value, bool) {
	if level, ok := c.nullLevel(runs); ok {
		return parquet.NullValue().Level(0, level, c.column), true
	}
	if c.repeated {
		return parquet.Value{}, false
	}
	var value parquet.Value
	for i, run := range runs {
		meta := &c.meta[run.input].Columns[c.column].MetaData
		stats := &meta.Statistics
		if meta.NumValues != c.groups[run.input].NumRows() || stats.NullCount != 0 || stats.MinValue == nil || !bytes.Equal(stats.MinValue, stats.MaxValue) {
			return parquet.Value{}, false
		}
		b := stats.MinValue
		var v parquet.Value
		switch {
		case meta.Type == format.Boolean && len(b) == 1:
			v = parquet.BooleanValue(b[0] != 0)
		case meta.Type == format.Int32 && len(b) == 4:
			v = parquet.Int32Value(int32(binary.LittleEndian.Uint32(b)))
		case meta.Type == format.Int64 && len(b) == 8:
			v = parquet.Int64Value(int64(binary.LittleEndian.Uint64(b)))
		case meta.Type == format.ByteArray:
			v = parquet.ByteArrayValue(b)
		default:
			return parquet.Value{}, false
		}
		v = v.Level(0, c.maxDefinition, c.column)
		if i > 0 && !parquet.Equal(value, v) {
			return parquet.Value{}, false
		}
		value = v
	}
	return value, len(runs) > 0
}

func (c *mergeColumn) nullLevel(runs []mergeRun) (int, bool) {
	level := -1
	for _, run := range runs {
		meta := &c.meta[run.input].Columns[c.column].MetaData
		if meta.NumValues != c.groups[run.input].NumRows() || meta.Statistics.NullCount != meta.NumValues {
			return 0, false
		}
		current := -1
		if c.flatNull {
			current = 0
		} else {
			for d, count := range meta.SizeStatistics.DefinitionLevelHistogram {
				if count == meta.NumValues && count > 0 {
					current = d
					break
				}
			}
		}
		if current < 0 || level >= 0 && current != level {
			return 0, false
		}
		level = current
	}
	return level, level >= 0
}

// Constant chunks have one RLE run per level stream and, for a non-NULL
// scalar, one dictionary entry. Work and scratch space do not scale with rows
// (booleans use their small PLAIN bitset). NULLs need no physical value data.
func (c *mergeConstant) constantChunk(rows int) (mergeChunk, error) {
	var rep, def []byte
	if c.maxRepetition > 0 {
		rep = binary.AppendUvarint(nil, uint64(rows)<<1)
		rep = append(rep, 0)
	}
	if c.maxDefinition > 0 {
		def = binary.AppendUvarint(nil, uint64(rows)<<1)
		def = append(def, byte(c.value.DefinitionLevel()))
	}
	var data bytes.Buffer
	var protocol thrift.CompactProtocol
	encoder := thrift.NewEncoder(protocol.NewWriter(&data))
	stats := format.Statistics{}
	encoding := format.Plain
	var values []byte
	var dictionary bool
	var uncompressed int64
	if c.value.IsNull() {
		stats.NullCount = int64(rows)
	} else {
		bounds := c.value.Bytes()
		stats.MinValue = bytes.Clone(bounds)
		stats.MaxValue = stats.MinValue
		if c.typ == format.Boolean {
			values = make([]byte, (rows+7)/8)
			if c.value.Boolean() {
				for i := range values {
					values[i] = 255
				}
			}
		} else {
			dictionary = true
			encoding = format.RLEDictionary
			plain := bounds
			if c.typ == format.ByteArray {
				plain = binary.LittleEndian.AppendUint32(nil, uint32(len(bounds)))
				plain = append(plain, bounds...)
			}
			compressed, err := parquetCodec.Encode(nil, plain)
			if err != nil {
				return mergeChunk{}, err
			}
			header := format.PageHeader{
				Type: format.DictionaryPage, UncompressedPageSize: int32(len(plain)),
				CompressedPageSize: int32(len(compressed)), CRC: int32(crc32.ChecksumIEEE(compressed)),
				DictionaryPageHeader: thrift.New(format.DictionaryPageHeader{
					NumValues: 1, Encoding: format.Plain, IsSorted: true,
				}),
			}
			if err := encoder.Encode(&header); err != nil {
				return mergeChunk{}, err
			}
			uncompressed = int64(data.Len() + len(plain))
			data.Write(compressed)
			// Bit width zero, followed by a zero-index RLE run.
			values = binary.AppendUvarint([]byte{0}, uint64(rows)<<1)
		}
	}
	dataOffset := int64(data.Len())
	compressed := values
	if len(values) > 0 {
		var err error
		compressed, err = parquetCodec.Encode(nil, values)
		if err != nil {
			return mergeChunk{}, err
		}
	}
	body := append(append(rep, def...), compressed...)
	header := format.PageHeader{
		Type: format.DataPageV2, UncompressedPageSize: int32(len(rep) + len(def) + len(values)),
		CompressedPageSize: int32(len(body)), CRC: int32(crc32.ChecksumIEEE(body)),
		DataPageHeaderV2: thrift.New(format.DataPageHeaderV2{
			NumValues: int32(rows), NumNulls: int32(stats.NullCount), NumRows: int32(rows),
			Encoding: encoding, DefinitionLevelsByteLength: int32(len(def)),
			RepetitionLevelsByteLength: int32(len(rep)),
			IsCompressed:               thrift.New(len(values) > 0), Statistics: stats,
		}),
	}
	if err := encoder.Encode(&header); err != nil {
		return mergeChunk{}, err
	}
	headerSize := int64(data.Len()) - dataOffset
	data.Write(body)
	size := int64(data.Len())
	uncompressed += headerSize + int64(len(rep)+len(def)+len(values))
	defs := make([]int64, c.maxDefinition+1)
	defs[c.value.DefinitionLevel()] = int64(rows)
	reps := make([]int64, c.maxRepetition+1)
	reps[0] = int64(rows)
	// These levels are implied by the row and NULL counts. Keep histograms
	// only where nested leaves need them to distinguish missing containers.
	if c.maxDefinition <= 1 {
		defs = nil
	}
	if c.maxRepetition == 0 {
		reps = nil
	}
	encodingStats := []format.PageEncodingStats{{PageType: format.DataPageV2, Encoding: encoding, Count: 1}}
	encodings := []format.Encoding{format.Plain, format.RLE}
	if dictionary {
		encodingStats = append(encodingStats, format.PageEncodingStats{PageType: format.DictionaryPage, Encoding: format.Plain, Count: 1})
		encodings = append(encodings, encoding)
	}
	sizes := format.SizeStatistics{DefinitionLevelHistogram: defs, RepetitionLevelHistogram: reps}
	if c.typ == format.ByteArray && !c.value.IsNull() {
		sizes.UnencodedByteArrayDataBytes = int64(rows) * int64(len(c.value.ByteArray()))
	}
	meta := format.ColumnMetaData{
		Type: c.typ, Encoding: encodings, PathInSchema: c.path, Codec: parquetCodec.CompressionCodec(),
		DataPageOffset: dataOffset, NumValues: int64(rows), TotalUncompressedSize: uncompressed,
		TotalCompressedSize: size, Statistics: stats, EncodingStats: encodingStats, SizeStatistics: sizes,
	}
	return mergeChunk{
		data: data.Bytes(), dictionary: dictionary, meta: format.ColumnChunk{MetaData: meta},
		columnIndex: format.ColumnIndex{
			NullPages: []bool{c.value.IsNull()}, MinValues: [][]byte{stats.MinValue},
			MaxValues: [][]byte{stats.MaxValue}, NullCounts: []int64{stats.NullCount},
			DefinitionLevelHistogram: defs, RepetitionLevelHistogram: reps,
		},
		offsetIndex: format.OffsetIndex{PageLocations: []format.PageLocation{{
			Offset: dataOffset, CompressedPageSize: int32(size - dataOffset),
		}}},
	}, nil
}

// One bounded window slot reuses its column buffers while its projection is
// unchanged. Constants are omitted before decoding and added during assembly.
type mergeOutput struct {
	constant []bool
	encoded  bytes.Buffer
	writer   *parquet.GenericWriter[any]
	group    *parquet.ConcurrentRowGroupWriter
}
