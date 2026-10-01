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
	for _, name := range []string{"attributes", "resource"} {
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
	columns := writer.ColumnWriters()
	for start := 0; start < len(rows); {
		end := min(start+256, len(rows), (start/parquetRowGroupRows+1)*parquetRowGroupRows)
		for i := start; i < end; i++ {
			row := reflect.ValueOf(&rows[i]).Elem()
			for c := range scalars {
				column := &scalars[c]
				field := row.Field(column.field)
				var value parquet.Value
				switch column.kind {
				case reflect.String:
					value = parquet.ValueOf(field.String())
				case reflect.Int64:
					value = parquet.Int64Value(field.Int())
				case reflect.Uint64:
					value = parquet.Int64Value(int64(field.Uint()))
				case reflect.Float64:
					value = parquet.DoubleValue(field.Float())
				}
				column.values[i-start] = value.Level(0, 0, column.column)
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
