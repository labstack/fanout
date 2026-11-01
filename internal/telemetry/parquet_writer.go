package telemetry

import (
	"fmt"
	"math/bits"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/telemetry/semconv"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/variant"
)

// writeColumns writes rows[perm[0]], rows[perm[1]] and so on. Scalar and list
// columns are filled per chunk of rows from the registry's accessors, and
// variant events stream straight into shredded columns: the generic map
// encoder builds a second object tree for every row, and keeping the
// canonical OTel maps as the event source bounds the ingest working set.
func writeColumns[R any](writer *parquet.GenericWriter[any], s *Signal, rows []R, perm []int32, spilled *spills) error {
	type variantColumn struct {
		get    func(*R) map[string]any
		writer *parquet.VariantColumnWriter
	}
	leaves := map[string]int{}
	for i, path := range writer.Schema().Columns() {
		c, ok := s.Column(path[0])
		if !ok || !c.Stored() {
			return fmt.Errorf("parquet column %q is not a %s column", path[0], s.Name)
		}
		leaves[strings.Join(path, ".")] = i
	}
	var encoder variantEncoder
	var cells []*cellWriter
	var fills []func([]R)
	keys := keyWriter[R]{byName: [2]map[string]int{{}, {}}, resources: map[reflect.Value]*keyRow{}, counts: spilled}
	cell := func(path string) *cellWriter {
		w := &cellWriter{column: leaves[path]}
		cells = append(cells, w)
		return w
	}
	var variants []variantColumn
	var resources *resourceColumns
	for _, c := range s.Columns {
		switch {
		case !c.Stored():
		case c.Key != nil:
			keys.add(c, cell(c.Name))
		case c.Name == "_spill" && c.get == nil:
			keys.spill = cell(c.Name)
		case c.List && c.Type == Variant:
			get, ok := c.get.(func(*R, []map[string]any) ([]map[string]any, bool))
			if !ok {
				return fmt.Errorf("%s column %q has no accessor for %T", s.Name, c.Name, *new(R))
			}
			element := c.Name + ".list.element."
			fills = append(fills, variantList(&encoder, cell(element+"metadata"), cell(element+"value"), get))
		case c.List:
			fill, err := listFill[R](c, cell(c.Name+".list.element"))
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
			fills = append(fills, fill)
		case c.Type == Variant:
			get, ok := c.get.(func(*R) map[string]any)
			if !ok {
				return fmt.Errorf("%s column %q has no accessor for %T", s.Name, c.Name, *new(R))
			}
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
				variants = append(variants, variantColumn{get: get, writer: stream})
				continue
			}
			if resources, err = newResourceColumns(writer.Schema()); err != nil {
				return err
			}
			resources.stream = stream
			selectSharedResources(resources, rows, get)
			variants = append(variants, variantColumn{get: get})
		default:
			fill, err := scalarFill[R](c, cell(c.Name))
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
			fills = append(fills, fill)
		}
	}
	if len(keys.columns) > 0 {
		if keys.attributes == nil || keys.resource == nil {
			return fmt.Errorf("%s has dedicated columns without attributes and resource", s.Name)
		}
		fills = append(fills, keys.fill)
	}

	columns := writer.ColumnWriters()
	// Each chunk's rows are gathered once into a contiguous buffer: every
	// column then reads them in sequence instead of chasing the permutation
	// across the whole batch once per column.
	buffer := make([]R, 256)
	defer clear(buffer)
	for start := 0; start < len(perm); {
		end := min(start+256, len(perm), (start/parquetRowGroupRows+1)*parquetRowGroupRows)
		chunk := buffer[:end-start]
		for i, row := range perm[start:end] {
			chunk[i] = rows[row]
		}
		// Dispatch once per column and chunk, rather than once per cell, so
		// the encoding loops are homogeneous and reuse their buffers.
		for _, fill := range fills {
			fill(chunk)
		}
		for i := range chunk {
			for _, column := range variants {
				value := column.get(&chunk[i])
				if column.writer == nil {
					if err := resources.append(&encoder, value, columns); err != nil {
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
				if err := encoder.write(column.writer, value); err != nil {
					return err
				}
				if err := column.writer.EndRow(); err != nil {
					return err
				}
			}
		}
		if err := encoder.err; err != nil {
			return err
		}
		for _, w := range cells {
			if err := w.flush(columns[w.column]); err != nil {
				return err
			}
		}
		if resources != nil {
			if err := resources.flush(columns); err != nil {
				return err
			}
		}
		start = end
		if start%parquetRowGroupRows == 0 {
			if err := writer.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// cellWriter buffers one leaf column's values for a chunk of rows. Byte
// values reference its byte buffer, which is reused once they are written.
type cellWriter struct {
	column int
	values []parquet.Value
	bytes  []byte
}

func (w *cellWriter) put(v parquet.Value, rep, def int) {
	w.values = append(w.values, v.Level(rep, def, w.column))
}

func (w *cellWriter) null(rep, def int) { w.put(parquet.NullValue(), rep, def) }

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
// block, so its part of the projection is computed once per map.
type keyWriter[R any] struct {
	attributes, resource func(*R) map[string]any
	columns              []*cellWriter
	keys                 []semconv.Key
	byName               [2]map[string]int // by scope: key name to column position
	spill                *cellWriter
	counts               *spills
	resources            map[reflect.Value]*keyRow
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
	k.keys = append(k.keys, *c.Key)
}

func (k *keyWriter[R]) project(row *keyRow, scope semconv.Scope, values map[string]any) {
	names := k.byName[scope]
	for name, v := range values {
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
	var row keyRow
	for i := range chunk {
		row = keyRow{}
		if resource := k.resource(&chunk[i]); resource != nil {
			id := reflect.ValueOf(resource)
			if cached, ok := k.resources[id]; ok {
				row = *cached
			} else {
				k.project(&row, semconv.Resource, resource)
				if len(k.resources) < 64 {
					cached := row
					k.resources[id] = &cached
				}
			}
		}
		if len(k.byName[semconv.Span]) > 0 {
			k.project(&row, semconv.Span, k.attributes(&chunk[i]))
		}
		for position, w := range k.columns {
			if row.has&(1<<position) == 0 {
				w.null(0, 0)
				continue
			}
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
	keys [][]string
	err  error // the first error of a list fill, which cannot return one
}

func (e *variantEncoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

func (e *variantEncoder) write(writer variant.ValueBuilder, value any) error {
	return e.value(writer, value, 0)
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
		for depth >= len(e.keys) {
			e.keys = append(e.keys, nil)
		}
		keys := e.keys[depth][:0]
		for key := range value {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		e.keys[depth] = keys
		writer.BeginObject()
		for _, key := range keys {
			writer.Field(key)
			if err := e.value(writer, value[key], depth+1); err != nil {
				return err
			}
		}
		writer.EndObject()
		clear(keys)
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
