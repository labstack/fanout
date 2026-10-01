package telemetry

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/variant"
)

// writeTelemetryColumns streams variant events straight into shredded columns.
// The generic map encoder builds a second object tree for every row; keeping
// the canonical OTel maps as the event source bounds the ingest working set.
func writeTelemetryColumns[T any](writer *parquet.GenericWriter[T], rows []T) error {
	type scalarColumn struct {
		field, column int
		kind          reflect.Kind
		values        []parquet.Value
		bytes         []byte
	}
	type variantColumn struct {
		field  int
		writer *parquet.VariantColumnWriter
	}
	typ := reflect.TypeFor[T]()
	fields := make(map[string]int, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		fields[strings.Split(typ.Field(i).Tag.Get("parquet"), ",")[0]] = i
	}
	var scalars []scalarColumn
	var variants []variantColumn
	for column, path := range writer.Schema().Columns() {
		if len(path) != 1 {
			if path[0] != "attributes" && path[0] != "resource" {
				return fmt.Errorf("unsupported nested Parquet column %q", path)
			}
			continue
		}
		field, ok := fields[path[0]]
		if !ok {
			return fmt.Errorf("parquet column %q has no row field", path[0])
		}
		kind := typ.Field(field).Type.Kind()
		switch kind {
		case reflect.String, reflect.Int64, reflect.Uint64, reflect.Float64:
		default:
			return fmt.Errorf("unsupported scalar Parquet field %q", path[0])
		}
		scalars = append(scalars, scalarColumn{field: field, column: column, kind: kind, values: make([]parquet.Value, 256)})
	}
	for _, name := range []string{"attributes"} {
		field, ok := fields[name]
		if !ok {
			continue
		}
		column, err := parquet.NewVariantColumnWriter(writer, name)
		if err != nil {
			return err
		}
		variants = append(variants, variantColumn{field: field, writer: column})
	}
	resources, err := newResourceColumns(writer.Schema(), fields)
	if err != nil {
		return err
	}
	if resources != nil {
		resources.stream, err = parquet.NewVariantColumnWriter(writer, "resource")
		if err != nil {
			return err
		}
		if err := selectSharedResources(resources, rows); err != nil {
			return err
		}
	}

	columns := writer.ColumnWriters()
	for start := 0; start < len(rows); {
		end := min(start+256, len(rows), (start/parquetRowGroupRows+1)*parquetRowGroupRows)
		// Dispatch once per column and chunk, rather than once per cell. Keep
		// reflection at the schema boundary while the scalar encoding loops are
		// homogeneous and reuse their byte/value buffers.
		var reflected [256]reflect.Value
		for i := start; i < end; i++ {
			reflected[i-start] = reflect.ValueOf(&rows[i]).Elem()
		}
		for c := range scalars {
			column := &scalars[c]
			switch column.kind {
			case reflect.String:
				for i := start; i < end; i++ {
					offset := len(column.bytes)
					column.bytes = append(column.bytes, reflected[i-start].Field(column.field).String()...)
					column.values[i-start] = parquet.ByteArrayValue(column.bytes[offset:]).Level(0, 0, column.column)
				}
			case reflect.Int64:
				for i := start; i < end; i++ {
					column.values[i-start] = parquet.Int64Value(reflected[i-start].Field(column.field).Int()).Level(0, 0, column.column)
				}
			case reflect.Uint64:
				for i := start; i < end; i++ {
					column.values[i-start] = parquet.Int64Value(int64(reflected[i-start].Field(column.field).Uint())).Level(0, 0, column.column)
				}
			case reflect.Float64:
				for i := start; i < end; i++ {
					column.values[i-start] = parquet.DoubleValue(reflected[i-start].Field(column.field).Float()).Level(0, 0, column.column)
				}
			}
		}
		for i := start; i < end; i++ {
			row := reflected[i-start]
			if resources != nil {
				if err := resources.append(row.Field(resources.field).Interface(), columns); err != nil {
					return err
				}
			}
			for _, column := range variants {
				value := row.Field(column.field).Interface()
				if value == nil || (reflect.ValueOf(value).Kind() == reflect.Map && reflect.ValueOf(value).IsNil()) {
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
			if _, err := columns[column.column].WriteRowValues(column.values[:end-start]); err != nil {
				return err
			}
			clear(column.values[:end-start])
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
	field   int
	schema  *parquet.Schema
	columns []int
	values  [][]parquet.Value
	cache   map[reflect.Value]parquet.Row
	shared  map[reflect.Value]bool
	stream  *parquet.VariantColumnWriter
	pending int
}
type resourceParquetRow struct {
	Resource any `parquet:"resource,variant"`
}

func newResourceColumns(schema *parquet.Schema, fields map[string]int) (*resourceColumns, error) {
	field, ok := fields["resource"]
	if !ok {
		return nil, nil
	}
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
	r := &resourceColumns{field: field, schema: parquet.NewSchema("resource", parquet.Group{"resource": node}), cache: make(map[reflect.Value]parquet.Row)}
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
func resourceKey(value any) (reflect.Value, any, error) {
	var key reflect.Value
	if value != nil {
		object, ok := value.(map[string]any)
		if !ok {
			return key, nil, fmt.Errorf("resource must be a canonical object, got %T", value)
		}
		if object != nil {
			key = reflect.ValueOf(object)
		} else {
			value = nil
		}
	}
	return key, value, nil
}
func selectSharedResources[T any](r *resourceColumns, rows []T) error {
	candidates := map[reflect.Value]int{}
	for i := range rows {
		key, _, err := resourceKey(reflect.ValueOf(&rows[i]).Elem().Field(r.field).Interface())
		if err != nil {
			return err
		}
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
	return nil
}
func (r *resourceColumns) append(value any, columns []*parquet.ColumnWriter) error {
	key, value, err := resourceKey(value)
	if err != nil {
		return err
	}
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
		encoded = r.schema.Deconstruct(nil, resourceParquetRow{Resource: value}).Clone()
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
