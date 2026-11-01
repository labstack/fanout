package telemetry

import (
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/telemetry/semconv"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/variant"
	"golang.org/x/sync/errgroup"
)

// writeColumns writes rows[perm[0]], rows[perm[1]] and so on. Scalar and list
// columns are filled per chunk of rows from the registry's accessors, and
// variant events stream straight into shredded columns: the generic map
// encoder builds a second object tree for every row, and keeping the
// canonical OTel maps as the event source bounds the ingest working set.
//
// Lanes write disjoint sets of columns in parallel, each walking a row group
// on its own and joining the others only at the row group's end. The first
// lane writes everything derived from a row's attributes and resource, which
// share one gathering of their entries; the others share the remaining
// columns. In the pinned parquet-go v0.32.0, a ColumnWriter's WriteRowValues
// and Flush (writer.go) touch only that column's buffers and dictionary, page
// buffers come from a BufferPool that must be safe for concurrent use
// (buffer_pool.go), and data page scratch and parquetCodec's encoders are
// sync.Pools. writer_test.go writes each column from its own goroutine in its
// "Parallel ColumnWriter.WriteRowValues" case.
func writeColumns[R any](writer *parquet.GenericWriter[any], s *Signal, rows []R, perm []int32, spilled *spills) error {
	type variantColumn struct {
		get        func(*R) map[string]any
		writer     *parquet.VariantColumnWriter
		attributes bool
	}
	leaves := map[string]int{}
	byColumn := map[string][]int{} // a stored column's leaves
	for i, path := range writer.Schema().Columns() {
		c, ok := s.Column(path[0])
		if !ok || !c.Stored() {
			return fmt.Errorf("parquet column %q is not a %s column", path[0], s.Name)
		}
		leaves[strings.Join(path, ".")] = i
		byColumn[path[0]] = append(byColumn[path[0]], i)
	}
	lanes := make([]*lane[R], min(maxWriteLanes, runtime.GOMAXPROCS(0)))
	for i := range lanes {
		lanes[i] = &lane[R]{chunk: make([]R, chunkRows)}
	}
	defer func() {
		for _, l := range lanes {
			clear(l.chunk)
		}
	}()
	shared := lanes[0]
	next := 0
	// other takes turns among the lanes after the shared one.
	other := func() *lane[R] {
		if len(lanes) == 1 {
			return shared
		}
		next++
		return lanes[1+(next-1)%(len(lanes)-1)]
	}
	own := func(l *lane[R], c Column) *lane[R] {
		l.columns = append(l.columns, byColumn[c.Name]...)
		return l
	}
	cell := func(l *lane[R], path string) *cellWriter {
		w := &cellWriter{column: leaves[path]}
		l.cells = append(l.cells, w)
		return w
	}
	keys := keyWriter[R]{byName: [2]map[string]int{{}, {}}, resources: map[reflect.Value]*keyRow{}, counts: spilled}
	var variants []variantColumn
	var resources *resourceColumns
	for _, c := range s.Columns {
		switch {
		case !c.Stored():
		case c.Key != nil:
			keys.add(c, cell(own(shared, c), c.Name))
		case c.Name == "_spill" && c.get == nil:
			keys.spill = cell(own(shared, c), c.Name)
		case c.List && c.Type == Variant:
			get, ok := c.get.(func(*R, []map[string]any) ([]map[string]any, bool))
			if !ok {
				return fmt.Errorf("%s column %q has no accessor for %T", s.Name, c.Name, *new(R))
			}
			l := own(other(), c)
			element := c.Name + ".list.element."
			l.fills = append(l.fills, variantList(&l.encoder, cell(l, element+"metadata"), cell(l, element+"value"), get))
		case c.List:
			l := own(other(), c)
			fill, err := listFill[R](c, cell(l, c.Name+".list.element"))
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
			l.fills = append(l.fills, fill)
		case c.Type == Variant:
			get, ok := c.get.(func(*R) map[string]any)
			if !ok {
				return fmt.Errorf("%s column %q has no accessor for %T", s.Name, c.Name, *new(R))
			}
			own(shared, c)
			switch c.Name {
			case "attributes":
				keys.attributes = get
			case "resource":
				keys.resource = get
			}
			stream, err := parquet.NewVariantColumnWriter(writer, c.Name)
			if err != nil {
				return err
			}
			if c.Name != "resource" {
				variants = append(variants, variantColumn{get: get, writer: stream, attributes: c.Name == "attributes"})
				continue
			}
			if resources, err = newResourceColumns(writer.Schema()); err != nil {
				return err
			}
			resources.stream = stream
			selectSharedResources(resources, rows, get)
			variants = append(variants, variantColumn{get: get})
		default:
			l := own(other(), c)
			fill, err := scalarFill[R](c, cell(l, c.Name))
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
			l.fills = append(l.fills, fill)
		}
	}
	if len(keys.columns) > 0 {
		if keys.attributes == nil || keys.resource == nil {
			return fmt.Errorf("%s has dedicated columns without attributes and resource", s.Name)
		}
		shared.fills = append(shared.fills, keys.fill)
	}

	columns := writer.ColumnWriters()
	encoder := &shared.encoder
	shared.rows = func(chunk []R) error {
		for i := range chunk {
			for _, column := range variants {
				value := column.get(&chunk[i])
				if column.writer == nil {
					if err := resources.append(encoder, value, columns); err != nil {
						return err
					}
					continue
				}
				if value == nil {
					if err := column.writer.WriteNullRow(); err != nil {
						return err
					}
					continue
				}
				if err := column.writer.BeginRow(); err != nil {
					return err
				}
				var err error
				// Attributes gathered for the dedicated columns are not
				// walked a second time.
				if fields, ok := keys.fields(i); column.attributes && ok {
					err = encoder.object(column.writer, fields, 0)
				} else {
					err = encoder.write(column.writer, value)
				}
				if err != nil {
					return err
				}
				if err := column.writer.EndRow(); err != nil {
					return err
				}
			}
		}
		if resources != nil {
			return resources.flush(columns)
		}
		return nil
	}
	for start := 0; start < len(perm); start += parquetRowGroupRows {
		group := perm[start:min(start+parquetRowGroupRows, len(perm))]
		var others errgroup.Group
		for _, l := range lanes[1:] {
			others.Go(func() error { return l.write(columns, rows, group) })
		}
		err := shared.write(columns, rows, group)
		if err := errors.Join(err, others.Wait()); err != nil {
			return err
		}
		if len(group) == parquetRowGroupRows {
			if err := writer.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// maxWriteLanes caps the lanes that write one file. Commit workers write
// files concurrently, so a few lanes each already cover the cores.
const maxWriteLanes = 4

// lane writes its columns for the rows of one row group at a time.
type lane[R any] struct {
	fills   []func([]R)
	rows    func([]R) error // writes the columns that stream row by row
	cells   []*cellWriter
	columns []int // every leaf the lane writes
	encoder variantEncoder
	chunk   []R
}

// write writes rows[group[0]], rows[group[1]] and so on to the lane's
// columns, then encodes their last pages so the row group's flush has only
// encoded pages left to copy.
func (l *lane[R]) write(columns []*parquet.ColumnWriter, rows []R, group []int32) error {
	for start := 0; start < len(group); start += chunkRows {
		// Each chunk's rows are gathered once into a contiguous buffer:
		// every column then reads them in sequence instead of chasing the
		// permutation across the whole batch once per column.
		chunk := l.chunk[:min(chunkRows, len(group)-start)]
		for i, row := range group[start : start+len(chunk)] {
			chunk[i] = rows[row]
		}
		// Dispatch once per column and chunk, rather than once per cell, so
		// the encoding loops are homogeneous and reuse their buffers.
		for _, fill := range l.fills {
			fill(chunk)
		}
		if l.rows != nil {
			if err := l.rows(chunk); err != nil {
				return err
			}
		}
		if err := l.encoder.err; err != nil {
			return err
		}
		for _, w := range l.cells {
			if err := w.flush(columns[w.column]); err != nil {
				return err
			}
		}
	}
	for _, column := range l.columns {
		if err := columns[column].Flush(); err != nil {
			return err
		}
	}
	return nil
}

// chunkRows is how many rows writeColumns fills at a time.
const chunkRows = 256

// cellWriter buffers one leaf column's values for a chunk of rows. Byte
// values reference its byte buffer, which is reused once they are written.
type cellWriter struct {
	column int
	values []parquet.Value
	bytes  []byte
	nulls  []parquet.Value // top-level NULLs levelled for this column
}

func (w *cellWriter) put(v parquet.Value, rep, def int) {
	w.values = append(w.values, v.Level(rep, def, w.column))
}

func (w *cellWriter) null(rep, def int) { w.put(parquet.NullValue(), rep, def) }

// skip appends n top-level NULLs, copied from a template levelled once.
func (w *cellWriter) skip(n int) {
	if n == 0 {
		return
	}
	if w.nulls == nil {
		w.nulls = make([]parquet.Value, chunkRows)
		for i := range w.nulls {
			w.nulls[i] = parquet.NullValue().Level(0, 0, w.column)
		}
	}
	w.values = append(w.values, w.nulls[:n]...)
}

func (w *cellWriter) text(v []byte, rep, def int) {
	offset := len(w.bytes)
	w.bytes = append(w.bytes, v...)
	w.put(parquet.ByteArrayValue(w.bytes[offset:]), rep, def)
}

func (w *cellWriter) flush(column *parquet.ColumnWriter) error {
	if len(w.values) == 0 {
		return nil
	}
	_, err := column.WriteRowValues(w.values)
	clear(w.values)
	w.values, w.bytes = w.values[:0], w.bytes[:0]
	return err
}

// Value writers by Go type: put one value at the given levels.
func putString(w *cellWriter, v string, rep, def int) {
	offset := len(w.bytes)
	w.bytes = append(w.bytes, v...)
	w.put(parquet.ByteArrayValue(w.bytes[offset:]), rep, def)
}
func putInt32(w *cellWriter, v int32, rep, def int)   { w.put(parquet.Int32Value(v), rep, def) }
func putUint32(w *cellWriter, v uint32, rep, def int) { w.put(parquet.Int32Value(int32(v)), rep, def) }
func putInt64(w *cellWriter, v int64, rep, def int)   { w.put(parquet.Int64Value(v), rep, def) }
func putUint64(w *cellWriter, v uint64, rep, def int) { w.put(parquet.Int64Value(int64(v)), rep, def) }
func putDouble(w *cellWriter, v float64, rep, def int) {
	w.put(parquet.DoubleValue(v), rep, def)
}
func putBool(w *cellWriter, v bool, rep, def int) { w.put(parquet.BooleanValue(v), rep, def) }

// scalarFill compiles a scalar column's accessor into a chunk filler.
func scalarFill[R any](c Column, w *cellWriter) (func([]R), error) {
	switch get := c.get.(type) {
	case func(*R) string:
		return required(w, get, putString), nil
	case func(*R) int32:
		return required(w, get, putInt32), nil
	case func(*R) uint32:
		return required(w, get, putUint32), nil
	case func(*R) int64:
		return required(w, get, putInt64), nil
	case func(*R) uint64:
		return required(w, get, putUint64), nil
	case func(*R) float64:
		return required(w, get, putDouble), nil
	case func(*R) bool:
		return required(w, get, putBool), nil
	case func(*R) (string, bool):
		return optional(w, get, putString), nil
	case func(*R) (int32, bool):
		return optional(w, get, putInt32), nil
	case func(*R) (uint64, bool):
		return optional(w, get, putUint64), nil
	case func(*R) (float64, bool):
		return optional(w, get, putDouble), nil
	}
	return nil, fmt.Errorf("column %q has no accessor for %T", c.Name, *new(R))
}

// listFill compiles a list column's accessor into a chunk filler.
func listFill[R any](c Column, w *cellWriter) (func([]R), error) {
	switch get := c.get.(type) {
	case func(*R, []string) ([]string, bool):
		return listed(w, get, putString), nil
	case func(*R, []int64) ([]int64, bool):
		return listed(w, get, putInt64), nil
	case func(*R, []uint64) ([]uint64, bool):
		return listed(w, get, putUint64), nil
	case func(*R, []float64) ([]float64, bool):
		return listed(w, get, putDouble), nil
	}
	return nil, fmt.Errorf("list column %q has no accessor for %T", c.Name, *new(R))
}

func required[R, T any](w *cellWriter, get func(*R) T, put func(*cellWriter, T, int, int)) func([]R) {
	return func(chunk []R) {
		for i := range chunk {
			put(w, get(&chunk[i]), 0, 0)
		}
	}
}

func optional[R, T any](w *cellWriter, get func(*R) (T, bool), put func(*cellWriter, T, int, int)) func([]R) {
	return func(chunk []R) {
		for i := range chunk {
			if v, ok := get(&chunk[i]); ok {
				put(w, v, 0, 1)
			} else {
				w.null(0, 0)
			}
		}
	}
}

// Lists are nullable with required elements: definition level 0 is a NULL
// list, 1 an empty one and 2 an element; repetition level 1 continues a row.
func listed[R, T any](w *cellWriter, get func(*R, []T) ([]T, bool), put func(*cellWriter, T, int, int)) func([]R) {
	var buffer []T
	return func(chunk []R) {
		for i := range chunk {
			values, ok := get(&chunk[i], buffer[:0])
			buffer = values
			switch {
			case !ok:
				w.null(0, 0)
			case len(values) == 0:
				w.null(0, 1)
			default:
				for j, v := range values {
					put(w, v, min(j, 1), 2)
				}
			}
		}
		clear(buffer)
	}
}

// variantList writes a list of unshredded VARIANT elements, each with its own
// metadata. A nil map is a NULL element at definition level 2.
func variantList[R any](encoder *variantEncoder, metadata, value *cellWriter, get func(*R, []map[string]any) ([]map[string]any, bool)) func([]R) {
	var buffer []map[string]any
	var names variant.MetadataBuilder
	var encoded []byte
	builder := variant.NewBuilderWithMetadata(&names)
	return func(chunk []R) {
		for i := range chunk {
			elements, ok := get(&chunk[i], buffer[:0])
			buffer = elements
			switch {
			case !ok:
				metadata.null(0, 0)
				value.null(0, 0)
			case len(elements) == 0:
				metadata.null(0, 1)
				value.null(0, 1)
			}
			if !ok {
				continue
			}
			for j, element := range elements {
				rep := min(j, 1)
				if element == nil {
					metadata.null(rep, 2)
					value.null(rep, 2)
					continue
				}
				// Each element carries only its own names.
				names.Reset()
				builder.Reset()
				if err := encoder.write(builder, element); err != nil {
					encoder.fail(err)
					return
				}
				v, err := builder.Bytes()
				if err != nil {
					encoder.fail(err)
					return
				}
				encoded = names.AppendTo(encoded[:0])
				metadata.text(encoded, rep, 3)
				value.text(v, rep, 3)
			}
		}
		clear(buffer)
	}
}

// keyWriter fills the dedicated columns and _spill from each row's
// attributes and resource. A resource map is shared by the rows of its OTLP
// block, so its part of the projection is computed once per map. A row's
// attributes are gathered once, in key order, for both its dedicated columns
// and its attributes variant. Most rows
// hold few of the keys: a row visits only the columns it fills, and each
// column's NULLs are appended as one run when its next value or the chunk's
// end arrives.
type keyWriter[R any] struct {
	attributes, resource func(*R) map[string]any
	columns              []*cellWriter
	filled               []int // by column position: chunk rows written
	keys                 []semconv.Key
	byName               [2]map[string]int // by scope: key name to column position
	spill                *cellWriter
	counts               *spills
	resources            map[reflect.Value]*keyRow
	entries              []field // the chunk's attribute entries, in key order by row
	ends                 []int   // by chunk row: the end of its entries
}

// fields returns chunk row i's attribute entries, when the chunk's attributes
// were gathered.
func (k *keyWriter[R]) fields(i int) ([]field, bool) {
	if i >= len(k.ends) {
		return nil, false
	}
	start := 0
	if i > 0 {
		start = k.ends[i-1]
	}
	return k.entries[start:k.ends[i]], true
}

// keyRow is a map's projection: the values of the keys it holds with their
// semconv types, and the spill bits of those it holds with other types.
type keyRow struct {
	values [64]any
	has    uint64 // by column position
	spill  uint64 // by key bit
}

func (k *keyWriter[R]) add(c Column, w *cellWriter) {
	k.byName[c.Key.Scope][c.Key.Name] = len(k.columns)
	k.columns = append(k.columns, w)
	k.filled = append(k.filled, 0)
	k.keys = append(k.keys, *c.Key)
}

func (k *keyWriter[R]) project(row *keyRow, scope semconv.Scope, fields []field) {
	names := k.byName[scope]
	for _, f := range fields {
		name, v := f.key, f.value
		position, ok := names[name]
		// A null value has no type: the column stays NULL without a spill.
		if !ok || v == nil {
			continue
		}
		if !typed(v, k.keys[position].Type) {
			row.spill |= 1 << k.keys[position].Bit
			continue
		}
		row.values[position] = v
		row.has |= 1 << position
	}
}

// typed reports whether an attribute value has the key's semconv type exactly.
func typed(v any, t semconv.Type) bool {
	switch v.(type) {
	case string:
		return t == semconv.String
	case int64:
		return t == semconv.Int
	}
	return false
}

func (k *keyWriter[R]) fill(chunk []R) {
	clear(k.entries)
	k.entries, k.ends = k.entries[:0], k.ends[:0]
	var row keyRow
	for i := range chunk {
		row = keyRow{}
		if resource := k.resource(&chunk[i]); resource != nil {
			id := reflect.ValueOf(resource)
			if cached, ok := k.resources[id]; ok {
				row = *cached
			} else {
				start := len(k.entries)
				k.entries = gather(k.entries, resource)
				k.project(&row, semconv.Resource, k.entries[start:])
				k.entries = k.entries[:start]
				if len(k.resources) < 64 {
					cached := row
					k.resources[id] = &cached
				}
			}
		}
		if len(k.byName[semconv.Span]) > 0 {
			start := len(k.entries)
			k.entries = gather(k.entries, k.attributes(&chunk[i]))
			k.project(&row, semconv.Span, k.entries[start:])
			k.ends = append(k.ends, len(k.entries))
		}
		for has := row.has; has != 0; has &= has - 1 {
			position := bits.TrailingZeros64(has)
			w := k.columns[position]
			w.skip(i - k.filled[position])
			k.filled[position] = i + 1
			switch v := row.values[position].(type) {
			case string:
				putString(w, v, 0, 1)
			case int64:
				putInt64(w, v, 0, 1)
			}
		}
		if k.spill != nil { // a layout may fill _spill from its own accessor
			putUint64(k.spill, row.spill, 0, 0)
		}
		for spill := row.spill; spill != 0; spill &= spill - 1 {
			k.counts[bits.TrailingZeros64(spill)]++
		}
	}
	for position, w := range k.columns {
		w.skip(len(chunk) - k.filled[position])
		k.filled[position] = 0
	}
}

// spills counts each key's spilled rows by key bit, across every write
// into one file.
type spills [64]uint64

// footer records each key's count of spilled rows in the file footer.
func (c *spills) footer(writer *parquet.GenericWriter[any], s *Signal) {
	for _, key := range s.Keys() {
		if n := c[key.Bit]; n > 0 {
			writer.SetKeyValueMetadata(spillPrefix+key.Name, strconv.FormatUint(n, 10))
		}
	}
}

// variantEncoder writes canonical maps with their keys in sorted order. A
// variant metadata dictionary interns names in first-use order, so sorting
// makes equal rows encode to equal bytes, whatever Go's map order.
type variantEncoder struct {
	fields [][]field // by depth: a map's entries, gathered in one pass
	err    error     // the first error of a list fill, which cannot return one
}

type field struct {
	key   string
	value any
}

func (e *variantEncoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

func (e *variantEncoder) write(writer variant.ValueBuilder, value any) error {
	return e.value(writer, value, 0)
}

// gather appends a map's entries to dst in key order.
func gather(dst []field, value map[string]any) []field {
	start := len(dst)
	for key, v := range value {
		dst = append(dst, field{key, v})
	}
	if len(dst)-start > 1 {
		slices.SortFunc(dst[start:], func(a, b field) int { return strings.Compare(a.key, b.key) })
	}
	return dst
}

// object writes an object at depth from its entries in key order.
func (e *variantEncoder) object(writer variant.ValueBuilder, fields []field, depth int) error {
	writer.BeginObject()
	for _, f := range fields {
		writer.Field(f.key)
		if err := e.value(writer, f.value, depth+1); err != nil {
			return err
		}
	}
	writer.EndObject()
	return nil
}

func (e *variantEncoder) value(writer variant.ValueBuilder, value any, depth int) error {
	switch value := value.(type) {
	case nil:
		writer.Null()
	case bool:
		writer.Bool(value)
	case int64:
		writer.Int64(value)
	case int:
		writer.Int64(int64(value))
	case float64:
		writer.Double(value)
	case string:
		writer.String(value)
	case []byte:
		writer.Binary(value)
	case map[string]any:
		for depth >= len(e.fields) {
			e.fields = append(e.fields, nil)
		}
		fields := gather(e.fields[depth][:0], value)
		e.fields[depth] = fields
		err := e.object(writer, fields, depth)
		clear(fields)
		if err != nil {
			return err
		}
	case []any:
		writer.BeginArray()
		for _, child := range value {
			if err := e.value(writer, child, depth+1); err != nil {
				return err
			}
		}
		writer.EndArray()
	default:
		return fmt.Errorf("unsupported telemetry attribute type %T", value)
	}
	return writer.Err()
}

// Resources are shared by all records in an OTLP resource block. Encode each
// canonical object once per file, then append its physical cells in chunks.
// Reflect values compare object identities and retain their Go objects. The
// cache owns cloned physical cells and is bounded to 64 canonical objects.
type resourceColumns struct {
	schema  *parquet.Schema
	columns []int
	values  [][]parquet.Value
	cache   map[reflect.Value]parquet.Row
	shared  map[reflect.Value]bool
	stream  *parquet.VariantColumnWriter
	pending int
}
type resourceRow struct {
	Resource any `parquet:"resource,variant"`
}

// rawVariant is an encoded variant value that Deconstruct shreds as is.
type rawVariant struct{ Metadata, Value []byte }

func newResourceColumns(schema *parquet.Schema) (*resourceColumns, error) {
	var node parquet.Node
	for _, f := range schema.Fields() {
		if f.Name() == "resource" {
			node = f
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("resource missing from physical schema")
	}
	r := &resourceColumns{schema: parquet.NewSchema("resource", parquet.Group{"resource": node}), cache: make(map[reflect.Value]parquet.Row)}
	for _, path := range r.schema.Columns() {
		column := -1
		for i, full := range schema.Columns() {
			if reflect.DeepEqual(path, full) {
				column = i
				break
			}
		}
		if column < 0 {
			return nil, fmt.Errorf("resource column missing: %v", path)
		}
		r.columns = append(r.columns, column)
		r.values = append(r.values, make([]parquet.Value, 0, 256))
	}
	return r, nil
}

// resourceKey is the object's identity; an absent resource has the zero key.
func resourceKey(value map[string]any) reflect.Value {
	if value == nil {
		return reflect.Value{}
	}
	return reflect.ValueOf(value)
}

func selectSharedResources[R any](r *resourceColumns, rows []R, get func(*R) map[string]any) {
	candidates := map[reflect.Value]int{}
	for i := range rows {
		key := resourceKey(get(&rows[i]))
		if count, ok := candidates[key]; ok {
			candidates[key] = count + 1
		} else if len(candidates) < 64 {
			candidates[key] = 1
		}
	}
	r.shared = map[reflect.Value]bool{}
	for key, count := range candidates {
		if count > 1 {
			r.shared[key] = true
		}
	}
}

func (r *resourceColumns) append(encoder *variantEncoder, value map[string]any, columns []*parquet.ColumnWriter) error {
	key := resourceKey(value)
	if !r.shared[key] {
		// Preserve row order when an uncached resource follows buffered cells.
		if err := r.flush(columns); err != nil {
			return err
		}
		if value == nil {
			return r.stream.WriteNullRow()
		}
		if err := r.stream.BeginRow(); err != nil {
			return err
		}
		if err := encoder.write(r.stream, value); err != nil {
			return err
		}
		return r.stream.EndRow()
	}
	encoded, ok := r.cache[key]
	if !ok {
		row := resourceRow{}
		if value != nil {
			// Deconstruct shreds a Go map in map order; pre-encoded variant
			// bytes keep the encoder's sorted order.
			var builder variant.Builder
			if err := encoder.write(&builder, value); err != nil {
				return err
			}
			metadata, data, err := builder.Finish()
			if err != nil {
				return err
			}
			row.Resource = rawVariant{Metadata: metadata, Value: data}
		}
		encoded = r.schema.Deconstruct(nil, row).Clone()
		r.cache[key] = encoded
	}
	if len(encoded) != len(r.columns) {
		return fmt.Errorf("resource physical row has %d cells, expected %d", len(encoded), len(r.columns))
	}
	for _, value := range encoded {
		column := value.Column()
		r.values[column] = append(r.values[column], value.Level(value.RepetitionLevel(), value.DefinitionLevel(), r.columns[column]))
	}
	r.pending++
	return nil
}

func (r *resourceColumns) flush(columns []*parquet.ColumnWriter) error {
	if r.pending == 0 {
		return nil
	}
	for i, values := range r.values {
		if _, err := columns[r.columns[i]].WriteRowValues(values); err != nil {
			return err
		}
		clear(values)
		r.values[i] = values[:0]
	}
	r.pending = 0
	return nil
}
