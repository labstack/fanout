package telemetry

import (
	"fmt"
	"reflect"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/variant"
)

// writeColumns streams variant events straight into shredded columns.
// The generic map encoder builds a second object tree for every row; keeping
// the canonical OTel maps as the event source bounds the ingest working set.
func writeColumns[R any](writer *parquet.GenericWriter[any], s *Signal, rows []R) error {
	type scalarColumn struct {
		get    any
		column int
		values []parquet.Value
		bytes  []byte
	}
	type variantColumn struct {
		get    func(*R) map[string]any
		writer *parquet.VariantColumnWriter
	}
	var scalars []scalarColumn
	for column, path := range writer.Schema().Columns() {
		c, ok := s.Column(path[0])
		if !ok || !c.Stored() {
			return fmt.Errorf("parquet column %q is not a %s column", path[0], s.Name)
		}
		if len(path) != 1 {
			if c.Type != Variant {
				return fmt.Errorf("unsupported nested Parquet column %q", path)
			}
			continue
		}
		switch c.get.(type) {
		case func(*R) string, func(*R) int64, func(*R) uint64, func(*R) float64, func(*R) bool:
		default:
			return fmt.Errorf("%s column %q has no accessor for %T", s.Name, c.Name, *new(R))
		}
		scalars = append(scalars, scalarColumn{get: c.get, column: column, values: make([]parquet.Value, 256)})
	}
	var variants []variantColumn
	var resources *resourceColumns
	for _, c := range s.Columns {
		if c.Type != Variant {
			continue
		}
		get, ok := c.get.(func(*R) map[string]any)
		if !ok {
			return fmt.Errorf("%s column %q has no accessor for %T", s.Name, c.Name, *new(R))
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
	}

	columns := writer.ColumnWriters()
	for start := 0; start < len(rows); {
		end := min(start+256, len(rows), (start/parquetRowGroupRows+1)*parquetRowGroupRows)
		// Dispatch once per column and chunk, rather than once per cell, so
		// the scalar encoding loops are homogeneous and reuse their byte and
		// value buffers.
		chunk := rows[start:end]
		for c := range scalars {
			column := &scalars[c]
			switch get := column.get.(type) {
			case func(*R) string:
				for i := range chunk {
					offset := len(column.bytes)
					column.bytes = append(column.bytes, get(&chunk[i])...)
					column.values[i] = parquet.ByteArrayValue(column.bytes[offset:]).Level(0, 0, column.column)
				}
			case func(*R) int64:
				for i := range chunk {
					column.values[i] = parquet.Int64Value(get(&chunk[i])).Level(0, 0, column.column)
				}
			case func(*R) uint64:
				for i := range chunk {
					column.values[i] = parquet.Int64Value(int64(get(&chunk[i]))).Level(0, 0, column.column)
				}
			case func(*R) float64:
				for i := range chunk {
					column.values[i] = parquet.DoubleValue(get(&chunk[i])).Level(0, 0, column.column)
				}
			case func(*R) bool:
				for i := range chunk {
					column.values[i] = parquet.BooleanValue(get(&chunk[i])).Level(0, 0, column.column)
				}
			}
		}
		for i := range chunk {
			for _, column := range variants {
				value := column.get(&chunk[i])
				if column.writer == nil {
					if err := resources.append(value, columns); err != nil {
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
				if err := writeVariantValue(column.writer, value); err != nil {
					return err
				}
				if err := column.writer.EndRow(); err != nil {
					return err
				}
			}
		}
		for c := range scalars {
			column := &scalars[c]
			if _, err := columns[column.column].WriteRowValues(column.values[:len(chunk)]); err != nil {
				return err
			}
			clear(column.values[:len(chunk)])
			column.bytes = column.bytes[:0]
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

func writeVariantValue(writer variant.ValueBuilder, value any) error {
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
		writer.BeginObject()
		for key, child := range value {
			writer.Field(key)
			if err := writeVariantValue(writer, child); err != nil {
				return err
			}
		}
		writer.EndObject()
	case []any:
		writer.BeginArray()
		for _, child := range value {
			if err := writeVariantValue(writer, child); err != nil {
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

func (r *resourceColumns) append(value map[string]any, columns []*parquet.ColumnWriter) error {
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
		if err := writeVariantValue(r.stream, value); err != nil {
			return err
		}
		return r.stream.EndRow()
	}
	encoded, ok := r.cache[key]
	if !ok {
		row := resourceRow{}
		if value != nil {
			row.Resource = value
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
