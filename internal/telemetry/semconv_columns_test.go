package telemetry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry/semconv"
)

func TestSemconvColumnsAvoidRowColumns(t *testing.T) {
	existing := map[string]string{}
	for _, row := range []reflect.Type{
		reflect.TypeFor[spanParquetRow](),
		reflect.TypeFor[logParquetRow](),
		reflect.TypeFor[metricParquetRow](),
	} {
		for field := range row.Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("parquet"), ",")
			if name != "" && name != "-" {
				existing[name] = row.Name()
			}
		}
	}
	if len(existing) == 0 {
		t.Fatal("no row columns found")
	}
	for _, k := range semconv.Keys {
		if row, ok := existing[k.Column]; ok {
			t.Errorf("%s column %s collides with %s", k.Name, k.Column, row)
		}
	}
}
