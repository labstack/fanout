package telemetry

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// Compaction re-encodes each shredded VARIANT column with metadata shared
// across its output row group.
//
// A VARIANT value names object fields by their position in the row's
// metadata dictionary. The ingest writer shares one growing dictionary across
// a file's rows, resetting it past variantMetadataBytes of names, so an ingest
// file's rows hold few distinct metadata blobs, mostly equal to their
// neighbours'. Merging many inputs interleaves the blobs of all of them: the
// distinct blobs outgrow parquetDictionaryMaxBytes, and the column falls back
// to plain pages where neighbouring rows repeat different blobs of up to
// 4 KiB. A 125-input span merge stored 600 bytes a row of metadata.
//
// The merge instead assigns each row of an output row group to one of a few
// shared dictionaries: the first, in creation order, that holds the row's
// names or has room for its missing ones within variantMetadataBytes, the
// bound the ingest writer applies. A reader decodes a row's whole metadata
// (DuckDB parses every name of it), so no row's dictionary is larger than
// ingest could write. Names are sorted, so the sorted_strings flag is set and
// field IDs follow name order. Field IDs in every value of the group, the
// unshredded value and each shredded field's residual, are rewritten to the
// row's new dictionary; each object is laid out as the variant builder lays
// it out. Shredded field names stay in the dictionary, as the shredding
// specification requires.
//
// The dictionaries of a row group form the metadata column chunk's Parquet
// dictionary, so at most parquetDictionaryMaxBytes/variantMetadataBytes are
// shared, and only while the row group's distinct blobs fit that dictionary.
// Any other row carries only its own names: they are shared with too few rows
// to be worth a dictionary, and its metadata is no larger than the names it
// uses. Rows are buffered per output row group, which mergeParquet bounds by
// the inputs' variant bytes (maxVariantRowGroupBytes).
//
// Assignment depends only on the merged row order and the rows' names, so
// equal inputs give byte-identical output.

// variantMetadataBytes bounds the name bytes of one shared dictionary. It
// matches parquet-go's VariantColumnWriter reset bound, which ingest uses.
const variantMetadataBytes = 4 << 10

// variantSharedDictionaries is how many shared dictionaries fit one Parquet
// dictionary page.
const variantSharedDictionaries = parquetDictionaryMaxBytes / variantMetadataBytes

// variantMerge merges the leaves of one non-repeated shredded VARIANT group.
// Each output row group's rows are read first. An encoded value is
// rewritten as it is read, while its row's input metadata is still in the
// input page, so that its field IDs name the merge's interned names; the
// row's names are collected on the way. Assignment and the final rewrite
// follow once the row group is complete.
type variantMerge struct {
	columns  []*mergeColumn // the group's leaves, in schema order
	metadata int            // position of the metadata leaf
	encoded  []int          // positions of leaves holding variant binary values
	fields   []string       // by position: the shredded field of a leaf, or ""
	typed    []bool         // by position: a shredded field's typed value

	names   []string // interned names
	nameIDs map[string]int32
	member  [][4]uint64 // by name: the shared dictionaries holding it

	// By output row.
	null    []bool
	sets    [][]int32 // the row's names, by ID
	rowDict []int     // the row's shared dictionary d, or segment s as -2-s; -1 if NULL
	// By position, for the metadata and encoded leaves.
	defs [][]int8
	data [][]byte // encoded leaves: values with interned field IDs
	ends [][]int

	dicts     []variantDict   // shared dictionaries
	segments  []variantDict   // rows' own names, shared by neighbours they hold
	mark      []int32         // by name: the last segment holding it
	dictBytes int             // about the dictionaries' encoded size
	meta      []parquet.Value // the current chunk's input metadata
	known     [][]int32       // by input: names of its field IDs last seen, -1 if none
	rw        variantRewriter
	out       []byte
	slices    [][]byte
	values    []parquet.Value
}

type variantDict struct {
	names []int32 // sorted by name once the row group is assigned
	size  int     // name bytes
	pos   map[int32]int
	blob  []byte
}

// Dictionary membership is a four-word bitset; this fails to compile if more
// dictionaries fit.
var _ = [1]struct{}{}[variantSharedDictionaries/256-1]

func newVariantMerge(schema *parquet.Schema, name string, columns []mergeColumn, inputs int) (*variantMerge, error) {
	m := &variantMerge{metadata: -1, nameIDs: map[string]int32{}, meta: make([]parquet.Value, 256), known: make([][]int32, inputs)}
	m.rw.m = m
	for i, path := range schema.Columns() {
		if path[0] != name {
			continue
		}
		if columns[i].repeated {
			return nil, fmt.Errorf("variant column %s is repeated", name)
		}
		position := len(m.columns)
		m.columns = append(m.columns, &columns[i])
		field, typed := "", false
		switch {
		case len(path) == 2 && path[1] == "metadata":
			m.metadata = position
		case len(path) == 2 && path[1] == "value":
			m.encoded = append(m.encoded, position)
		case len(path) == 4 && path[1] == "typed_value" && path[3] == "value":
			field = path[2]
			m.encoded = append(m.encoded, position)
		case len(path) == 4 && path[1] == "typed_value" && path[3] == "typed_value":
			field, typed = path[2], true
		default:
			return nil, fmt.Errorf("variant column %s has an unexpected leaf %v", name, path)
		}
		m.fields = append(m.fields, field)
		m.typed = append(m.typed, typed)
	}
	if m.metadata < 0 {
		return nil, fmt.Errorf("variant column %s has no metadata leaf", name)
	}
	n := len(m.columns)
	m.defs, m.data, m.ends = make([][]int8, n), make([][]byte, n), make([][]int, n)
	return m, nil
}

func (m *variantMerge) reset() {
	for i := range m.columns {
		m.defs[i], m.data[i], m.ends[i] = m.defs[i][:0], m.data[i][:0], m.ends[i][:0]
	}
	clear(m.names)
	m.names = m.names[:0]
	clear(m.nameIDs)
	m.member = m.member[:0]
	m.null, m.sets, m.rowDict = m.null[:0], m.sets[:0], m.rowDict[:0]
	clear(m.dicts)
	clear(m.segments)
	m.dicts, m.segments, m.mark, m.dictBytes = m.dicts[:0], m.segments[:0], m.mark[:0], 0
	for i := range m.known {
		m.known[i] = m.known[i][:0]
	}
}

// write merges one output row group's rows of the group.
func (m *variantMerge) write(ctx context.Context, writers []*parquet.ColumnWriter, runs []mergeRun) error {
	m.reset()
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		for done := 0; done < run.rows; {
			n, err := m.read(run.input, run.rows-done, writers)
			if err != nil {
				return err
			}
			done += n
		}
	}
	m.assign()
	if err := m.emit(writers); err != nil {
		return err
	}
	for _, c := range m.columns {
		if err := writers[c.column].Flush(); err != nil {
			return err
		}
	}
	return nil
}

// read buffers up to rows rows of an input: as many as one read of the
// metadata leaf returns. Those values stay in their page while the other
// leaves are read. Other values borrow their page too, so only the bytes kept
// are copied; typed values are written through.
func (m *variantMerge) read(input, rows int, writers []*parquet.ColumnWriter) (int, error) {
	metadata := m.columns[m.metadata]
	n, err := metadata.readSome(input, m.meta[:min(rows, len(m.meta))])
	if err != nil {
		return 0, err
	}
	defer clear(m.meta[:n])
	base := len(m.null)
	for _, v := range m.meta[:n] {
		m.defs[m.metadata] = append(m.defs[m.metadata], int8(v.DefinitionLevel()))
		m.null = append(m.null, v.IsNull())
		if len(m.sets) < cap(m.sets) {
			m.sets = m.sets[:len(m.sets)+1]
			m.sets[len(m.sets)-1] = m.sets[len(m.sets)-1][:0]
		} else {
			m.sets = append(m.sets, nil)
		}
	}
	for position, c := range m.columns {
		if position == m.metadata {
			continue
		}
		row := base
		err := c.read(mergeRun{input, n}, func(values []parquet.Value) error {
			for _, v := range values {
				present := !v.IsNull()
				if present && m.null[row] {
					return fmt.Errorf("variant leaf %d holds a value of a NULL row", position)
				}
				if present && m.fields[position] != "" {
					m.sets[row] = append(m.sets[row], m.intern(m.fields[position]))
				}
				if !m.typed[position] {
					m.defs[position] = append(m.defs[position], int8(v.DefinitionLevel()))
					if present {
						if err := m.rw.input(m.meta[row-base].ByteArray(), &m.known[input], &m.sets[row]); err != nil {
							return err
						}
						var err error
						if m.data[position], err = m.rw.rewrite(m.data[position], v.ByteArray(), false); err != nil {
							return err
						}
					}
					m.ends[position] = append(m.ends[position], len(m.data[position]))
				}
				row++
			}
			if m.typed[position] {
				written, err := writers[c.column].WriteRowValues(values)
				if err == nil && written != len(values) {
					err = fmt.Errorf("variant leaf %d: short write", position)
				}
				return err
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	m.rw.meta, m.rw.known, m.rw.set = nil, nil, nil
	// Advancing past an input's last row closes its reader and releases the
	// page the metadata values borrow.
	if err := metadata.advance(mergeRun{input, n}); err != nil {
		return 0, err
	}
	for row := base; row < len(m.null); row++ {
		slices.Sort(m.sets[row])
		m.sets[row] = slices.Compact(m.sets[row])
	}
	return n, nil
}

func (m *variantMerge) intern(name string) int32 {
	if id, ok := m.nameIDs[name]; ok {
		return id
	}
	id := int32(len(m.names))
	name = strings.Clone(name)
	m.names = append(m.names, name)
	m.nameIDs[name] = id
	m.member = append(m.member, [4]uint64{})
	return id
}

// value returns a buffered encoded leaf's value of row, nil when NULL.
func (m *variantMerge) value(position, row int) []byte {
	start := 0
	if row > 0 {
		start = m.ends[position][row-1]
	}
	if m.ends[position][row] == start {
		return nil
	}
	return m.data[position][start:m.ends[position][row]]
}

func (m *variantMerge) byName(a, b int32) int { return strings.Compare(m.names[a], m.names[b]) }

// assign gives each row the first shared dictionary that holds its names or
// has room for the missing ones, opening a new one while the row group has
// fewer than variantSharedDictionaries.
//
// Every distinct blob is a value of the metadata column chunk's Parquet
// dictionary. Once they would outgrow parquetDictionaryMaxBytes the writer
// falls back to plain pages, where each row stores its blob again. From that
// row on, and for a row no shared dictionary takes, a row carries only its
// own names, the smallest blob that holds them; a neighbour whose names it
// holds reuses it. Measured on unique attribute names, plain pages of rows'
// own names compressed to about 130 bytes a row, and pages repeating shared
// 4 KiB dictionaries to about 600.
func (m *variantMerge) assign() {
	distinct := 0
	for row, null := range m.null {
		d := -1
		if !null {
			names := m.sets[row]
			before := m.dictBytes
			if distinct <= parquetDictionaryMaxBytes {
				d = m.fit(names)
			}
			if d < 0 {
				d = -2 - m.segment(names)
			}
			distinct += m.dictBytes - before
		}
		m.rowDict = append(m.rowDict, d)
	}
	for _, dicts := range [][]variantDict{m.dicts, m.segments} {
		for i := range dicts {
			d := &dicts[i]
			slices.SortFunc(d.names, m.byName)
			d.blob = m.appendMetadata(nil, d.names)
		}
	}
	for i := range m.dicts {
		d := &m.dicts[i]
		d.pos = make(map[int32]int, len(d.names))
		for j, name := range d.names {
			d.pos[name] = j
		}
	}
}

// segment returns the current segment if it holds a row's names, or a new
// one of the row's names.
func (m *variantMerge) segment(names []int32) int {
	for len(m.mark) < len(m.names) {
		m.mark = append(m.mark, -1)
	}
	current := len(m.segments) - 1
	missing := 0
	for _, name := range names {
		if m.mark[name] != int32(current) {
			missing += len(m.names[name])
		}
	}
	if current < 0 || missing > 0 {
		m.segments = append(m.segments, variantDict{})
		m.dictBytes += 4
		current++
	}
	d := &m.segments[current]
	for _, name := range names {
		if m.mark[name] != int32(current) {
			m.mark[name] = int32(current)
			d.names = append(d.names, name)
			d.size += len(m.names[name])
			m.dictBytes += len(m.names[name]) + 2
		}
	}
	return current
}

func (m *variantMerge) fit(names []int32) int {
	all := [4]uint64{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)} // dictionaries holding every name
	size, fresh := 0, 0                                              // the row's name bytes, and those in no dictionary
	for _, name := range names {
		member := m.member[name]
		for w := range all {
			all[w] &= member[w]
		}
		size += len(m.names[name])
		if member == [4]uint64{} {
			fresh += len(m.names[name])
		}
	}
	for w, word := range all {
		if i := w*64 + bits.TrailingZeros64(word); word != 0 && i < len(m.dicts) {
			return i
		}
	}
	for i := range m.dicts {
		d := &m.dicts[i]
		if d.size+fresh > variantMetadataBytes {
			continue
		}
		missing := 0
		for _, name := range names {
			if m.member[name][i/64]&(1<<(i%64)) == 0 {
				missing += len(m.names[name])
			}
		}
		if d.size+missing <= variantMetadataBytes {
			m.join(i, names)
			return i
		}
	}
	if len(m.dicts) < variantSharedDictionaries && size <= variantMetadataBytes {
		m.dicts = append(m.dicts, variantDict{})
		m.dictBytes += 4
		m.join(len(m.dicts)-1, names)
		return len(m.dicts) - 1
	}
	return -1
}

func (m *variantMerge) join(i int, names []int32) {
	d := &m.dicts[i]
	for _, name := range names {
		if m.member[name][i/64]&(1<<(i%64)) == 0 {
			m.member[name][i/64] |= 1 << (i % 64)
			d.names = append(d.names, name)
			d.size += len(m.names[name])
			m.dictBytes += len(m.names[name]) + 2
		}
	}
}

// appendMetadata encodes a dictionary of names sorted by name, as
// variant.MetadataBuilder encodes one (VariantEncoding.md, Metadata
// encoding): version 1 with sorted_strings set.
func (m *variantMerge) appendMetadata(dst []byte, names []int32) []byte {
	total := 0
	for _, name := range names {
		total += len(m.names[name])
	}
	size := variantWidth(max(total, len(names)))
	dst = append(dst, 1|1<<4|byte(size-1)<<6)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(names)))[:len(dst)+size]
	offset := 0
	for _, name := range names {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(offset))[:len(dst)+size]
		offset += len(m.names[name])
	}
	dst = binary.LittleEndian.AppendUint32(dst, uint32(offset))[:len(dst)+size]
	for _, name := range names {
		dst = append(dst, m.names[name]...)
	}
	return dst
}

// emit writes the metadata and encoded leaves, every row's values rewritten
// to its dictionary.
func (m *variantMerge) emit(writers []*parquet.ColumnWriter) error {
	rows := len(m.null)
	values := m.slices[:0]
	fresh := make([]bool, rows) // the row's metadata is new to the row group
	seen := make([]bool, len(m.dicts)+len(m.segments))
	for row := range rows {
		var blob []byte
		if d := m.rowDict[row]; d >= 0 {
			blob, fresh[row], seen[d] = m.dicts[d].blob, !seen[d], true
		} else if d <= -2 {
			s := -2 - d
			blob, fresh[row], seen[len(m.dicts)+s] = m.segments[s].blob, !seen[len(m.dicts)+s], true
		}
		values = append(values, blob)
	}
	if err := m.put(writers, m.metadata, values, fresh); err != nil {
		return err
	}
	ends := make([]int, rows)
	for _, position := range m.encoded {
		m.out = m.out[:0]
		for row := range rows {
			if data := m.value(position, row); data != nil {
				if d := m.rowDict[row]; d >= 0 {
					m.rw.pos, m.rw.own = m.dicts[d].pos, nil
				} else {
					m.rw.pos, m.rw.own = nil, m.segments[-2-d].names
				}
				var err error
				if m.out, err = m.rw.rewrite(m.out, data, true); err != nil {
					return err
				}
			}
			ends[row] = len(m.out)
		}
		start := 0
		for row := range rows {
			values[row] = nil
			if ends[row] > start {
				values[row] = m.out[start:ends[row]]
			}
			start = ends[row]
		}
		if err := m.put(writers, position, values, nil); err != nil {
			return err
		}
	}
	clear(values)
	m.slices = values[:0]
	m.rw.pos, m.rw.own = nil, nil
	return nil
}

// put writes a leaf's byte values of the row group, nil being NULL, at their
// buffered definition levels.
//
// The writer checks its page size only between calls, so each call holds
// about a page. A dictionary-encoded page's size counts its indices, not its
// dictionary, which the writer caps only when a page flushes: metadata pages
// are flushed once their values new to the dictionary reach a page, so a
// dictionary of unshared names falls back to plain pages near its cap.
func (m *variantMerge) put(writers []*parquet.ColumnWriter, position int, data [][]byte, fresh []bool) error {
	column := m.columns[position].column
	added := 0 // bytes new to the dictionary since the last flush
	for start := 0; start < len(data); {
		values, size := m.values[:0], 0
		for _, b := range data[start:] {
			if len(values) > 0 && (size+len(b) > parquetPageSize || added >= parquetPageSize) {
				break
			}
			v := parquet.NullValue()
			if b != nil {
				v = parquet.ByteArrayValue(b)
			}
			if fresh != nil && fresh[start+len(values)] {
				added += len(b)
			}
			values = append(values, v.Level(0, int(m.defs[position][start+len(values)]), column))
			size += len(b)
		}
		written, err := writers[column].WriteRowValues(values)
		clear(values)
		m.values = values[:0]
		if err == nil && written != len(values) {
			err = fmt.Errorf("variant leaf %d: short write", position)
		}
		if err == nil && added >= parquetPageSize {
			err = writers[column].Flush()
			added = 0
		}
		if err != nil {
			return err
		}
		start += written
	}
	return nil
}

// Variant binary walking. The spec (VariantEncoding.md) lays out a value as a
// header byte whose low two bits are the basic type: a primitive, a short
// string of up to 63 bytes, an object or an array.
const (
	variantBasicPrimitive   = 0
	variantBasicShortString = 1
	variantBasicObject      = 2
	variantBasicArray       = 3
)

// variantPrimitiveSizes is each primitive type's payload size; -1 marks the
// length-prefixed binary and string types.
var variantPrimitiveSizes = [...]int{0, 0, 0, 1, 2, 4, 8, 8, 5, 9, 17, 4, 8, 8, 4, -1, -1, 8, 8, 8, 16}

// variantContainer is a decoded object or array header.
type variantContainer struct {
	count      int
	idSize     int // objects only
	offsetSize int
	ids        int // offset of the field IDs
	offsets    int // offset of the field offsets
	values     int // offset of the first value
	end        int // the container's size
}

func parseVariantContainer(data []byte) (variantContainer, error) {
	header := data[0]
	var c variantContainer
	c.offsetSize = int(header>>2&3) + 1
	large := header>>4&1 == 1
	if header&3 == variantBasicObject {
		c.idSize = int(header>>4&3) + 1
		large = header>>6&1 == 1
	}
	pos := 2
	if large {
		if len(data) < 5 {
			return c, errors.New("variant container truncated")
		}
		c.count = int(binary.LittleEndian.Uint32(data[1:5]))
		pos = 5
	} else {
		if len(data) < 2 {
			return c, errors.New("variant container truncated")
		}
		c.count = int(data[1])
	}
	if c.count < 0 || c.count > len(data) {
		return c, errors.New("variant container count exceeds its value")
	}
	c.ids = pos
	c.offsets = c.ids + c.count*c.idSize
	c.values = c.offsets + (c.count+1)*c.offsetSize
	if c.values > len(data) {
		return c, errors.New("variant container header exceeds its value")
	}
	c.end = c.values + readVariantUint(data[c.offsets+c.count*c.offsetSize:], c.offsetSize)
	if c.end < c.values || c.end > len(data) {
		return c, errors.New("variant container values exceed its value")
	}
	return c, nil
}

func readVariantUint(data []byte, size int) int {
	v := 0
	for i := range size {
		v |= int(data[i]) << (8 * i)
	}
	return v
}

func putVariantUint(dst []byte, v, size int) {
	for i := range size {
		dst[i] = byte(v >> (8 * i))
	}
}

// variantChild returns the value at a container's i-th offset. Offsets need
// not increase, so the value's own encoding gives its size.
func variantChild(data []byte, c variantContainer, i int) ([]byte, error) {
	start := c.values + readVariantUint(data[c.offsets+i*c.offsetSize:], c.offsetSize)
	if start >= c.end {
		return nil, errors.New("variant child offset outside its container")
	}
	child := data[start:c.end]
	size, err := variantValueSize(child)
	if err != nil {
		return nil, err
	}
	return child[:size], nil
}

// variantValueSize returns the size of the value at the start of data.
func variantValueSize(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, errors.New("variant value is empty")
	}
	size := 0
	switch data[0] & 3 {
	case variantBasicPrimitive:
		t := int(data[0] >> 2)
		if t >= len(variantPrimitiveSizes) {
			return 0, fmt.Errorf("variant primitive type %d is unknown", t)
		}
		size = 1 + variantPrimitiveSizes[t]
		if variantPrimitiveSizes[t] < 0 {
			if len(data) < 5 {
				return 0, errors.New("variant value truncated")
			}
			size = 5 + int(binary.LittleEndian.Uint32(data[1:5]))
		}
	case variantBasicShortString:
		size = 1 + int(data[0]>>2)
	default:
		c, err := parseVariantContainer(data)
		if err != nil {
			return 0, err
		}
		size = c.end
	}
	if size < 1 || size > len(data) {
		return 0, errors.New("variant value truncated")
	}
	return size, nil
}

// variantRewriter rewrites the field IDs of encoded values. With input
// metadata, an input's IDs become interned names, which are added to set;
// otherwise interned names become positions in pos, or in own, a segment's
// names sorted by name.
type variantRewriter struct {
	m *variantMerge
	// The input metadata: its offsets' width, its size, and where its offsets
	// and names start.
	meta                []byte
	width, count, names int
	// The names of the input's IDs last seen. An input's writer keeps IDs
	// stable while its dictionary grows, so a known name is checked, not
	// looked up.
	known    *[]int32
	set      *[]int32
	pos      map[int32]int
	own      []int32
	entries  []variantEntry // open objects' fields, innermost last
	children []byte
}

type variantEntry struct{ id, start, end int }

// input reads an input metadata header (VariantEncoding.md, Metadata
// encoding); its names are read as values use them.
func (r *variantRewriter) input(meta []byte, known, set *[]int32) error {
	if len(meta) < 1 || meta[0]&0x0F != 1 {
		return errors.New("variant metadata has an unsupported version")
	}
	r.width = int(meta[0]>>6) + 1
	if len(meta) < 1+r.width {
		return errors.New("variant metadata truncated")
	}
	r.count = readVariantUint(meta[1:], r.width)
	r.names = 1 + r.width + (r.count+1)*r.width
	if r.count > len(meta) || r.names > len(meta) {
		return errors.New("variant metadata truncated")
	}
	for len(*known) < r.count {
		*known = append(*known, -1)
	}
	r.meta, r.known, r.set = meta, known, set
	return nil
}

func (r *variantRewriter) id(id int) (int, error) {
	if r.meta != nil {
		if id >= r.count {
			return 0, fmt.Errorf("variant field ID %d outside a dictionary of %d", id, r.count)
		}
		at := 1 + r.width + id*r.width
		start, end := r.names+readVariantUint(r.meta[at:], r.width), r.names+readVariantUint(r.meta[at+r.width:], r.width)
		if start > end || end > len(r.meta) {
			return 0, errors.New("variant metadata offsets are invalid")
		}
		text := r.meta[start:end]
		name := (*r.known)[id]
		if name < 0 || r.m.names[name] != string(text) {
			var ok bool
			if name, ok = r.m.nameIDs[string(text)]; !ok {
				name = r.m.intern(string(text))
			}
			(*r.known)[id] = name
		}
		*r.set = append(*r.set, name)
		return int(name), nil
	}
	if r.pos != nil {
		if i, ok := r.pos[int32(id)]; ok {
			return i, nil
		}
	} else if i, ok := slices.BinarySearchFunc(r.own, int32(id), r.m.byName); ok {
		return i, nil
	}
	return 0, errors.New("variant field missing from its merged dictionary")
}

// rewrite appends data to dst with every object's field IDs mapped. Objects
// lay out their values in field order with the narrowest ID and offset
// widths, as variant.Builder does; sorted lists fields in ID order, which
// with a sorted dictionary is name order.
func (r *variantRewriter) rewrite(dst, data []byte, sorted bool) ([]byte, error) {
	if len(data) == 0 {
		return dst, errors.New("variant value is empty")
	}
	basic := data[0] & 3
	if basic != variantBasicObject && basic != variantBasicArray {
		size, err := variantValueSize(data)
		if err != nil {
			return dst, err
		}
		return append(dst, data[:size]...), nil
	}
	c, err := parseVariantContainer(data)
	if err != nil {
		return dst, err
	}
	base := len(r.entries)
	defer func() { r.entries = r.entries[:base] }()
	maxID := 0
	start := len(dst)
	for i := range c.count {
		var e variantEntry
		if basic == variantBasicObject {
			if e.id, err = r.id(readVariantUint(data[c.ids+i*c.idSize:], c.idSize)); err != nil {
				return dst, err
			}
			maxID = max(maxID, e.id)
		}
		child, err := variantChild(data, c, i)
		if err != nil {
			return dst, err
		}
		e.start = len(dst) - start
		if dst, err = r.rewrite(dst, child, sorted); err != nil {
			return dst, err
		}
		e.end = len(dst) - start
		r.entries = append(r.entries, e)
	}
	entries := r.entries[base:]
	if basic == variantBasicObject && sorted && !slices.IsSortedFunc(entries, func(a, b variantEntry) int { return a.id - b.id }) {
		slices.SortFunc(entries, func(a, b variantEntry) int { return a.id - b.id })
		r.children = append(r.children[:0], dst[start:]...)
		dst = dst[:start]
		for i := range entries {
			e := &entries[i]
			from, to := e.start, e.end
			e.start = len(dst) - start
			dst = append(dst, r.children[from:to]...)
			e.end = len(dst) - start
		}
	}
	if basic == variantBasicObject {
		for i := 1; i < len(entries); i++ {
			if entries[i].id == entries[i-1].id {
				return dst, errors.New("variant object repeats a field")
			}
		}
	}
	content := len(dst) - start
	offsetSize, idSize := variantWidth(content), variantWidth(maxID)
	large := c.count > 255
	headerSize := 2 + (c.count+1)*offsetSize
	if large {
		headerSize += 3
	}
	if basic == variantBasicObject {
		headerSize += c.count * idSize
	}
	dst = slices.Grow(dst, headerSize)[:len(dst)+headerSize]
	copy(dst[start+headerSize:], dst[start:len(dst)-headerSize])
	h := dst[start : start+headerSize]
	pos := 2
	if large {
		binary.LittleEndian.PutUint32(h[1:], uint32(c.count))
		pos = 5
	} else {
		h[1] = byte(c.count)
	}
	if basic == variantBasicObject {
		h[0] = variantBasicObject | byte(offsetSize-1)<<2 | byte(idSize-1)<<4
		if large {
			h[0] |= 1 << 6
		}
		for _, e := range entries {
			putVariantUint(h[pos:], e.id, idSize)
			pos += idSize
		}
	} else {
		h[0] = variantBasicArray | byte(offsetSize-1)<<2
		if large {
			h[0] |= 1 << 4
		}
	}
	for _, e := range entries {
		putVariantUint(h[pos:], e.start, offsetSize)
		pos += offsetSize
	}
	putVariantUint(h[pos:], content, offsetSize)
	return dst, nil
}

// variantWidth is the bytes an unsigned value needs, as the variant builder
// sizes field IDs and offsets.
func variantWidth(v int) int {
	switch {
	case v <= 0xFF:
		return 1
	case v <= 0xFFFF:
		return 2
	case v <= 0xFFFFFF:
		return 3
	}
	return 4
}
