package panel

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExportedRegistriesMatchInternalTables(t *testing.T) {
	specs := MeasureSpecs()
	if len(specs) != len(measureFuncs) {
		t.Fatalf("measure specs: %d, want %d", len(specs), len(measureFuncs))
	}
	for i, m := range specs {
		if i > 0 && specs[i-1].Name >= m.Name {
			t.Fatal("measure specs not sorted")
		}
		s := measureFuncs[m.Name]
		if m.Args != s.args || m.Numeric != s.numeric || m.Additive != s.additive || m.Unit != s.unit || m.Quantile != s.quantile || !reflect.DeepEqual(m.Signals, s.signals) {
			t.Errorf("spec mismatch: %+v", m)
		}
	}
	wantBuckets := []string{"auto", "10s", "30s", "1m", "5m", "10m", "15m", "30m", "1h", "3h", "6h", "12h", "1d"}
	if got := BucketNames(); !slices.Equal(got, wantBuckets) {
		t.Errorf("buckets = %v", got)
	}
	for _, name := range wantBuckets[1:] {
		if _, ok := buckets[name]; !ok {
			t.Errorf("unknown bucket %s", name)
		}
	}
	if got := FilterFunctionNames(); !slices.Equal(got, []string{"contains", "length", "lower", "regexp_matches", "starts_with", "upper"}) {
		t.Errorf("filter functions = %v", got)
	}
	cols, ok := SignalColumns("spans")
	if !ok || !slices.ContainsFunc(cols, func(f Field) bool { return f.Name == "http_status_code" && f.Type == TypeString }) {
		t.Fatal("missing string http_status_code")
	}
	if !slices.ContainsFunc(cols, func(f Field) bool { return f.Name == "attributes" && f.Type == TypeMap }) {
		t.Fatal("missing attributes map")
	}
	if !reflect.DeepEqual(cols, signals["spans"].fields) {
		t.Fatal("columns differ from display order")
	}
	cols[0].Name = "BROKEN"
	if again, _ := SignalColumns("spans"); again[0].Name == "BROKEN" {
		t.Fatal("columns are mutable")
	}
	if got := LowCardinalityColumns("spans"); !slices.Equal(got, signals["spans"].lowCard) {
		t.Errorf("low cardinality = %v", got)
	}
	if cols, ok := SignalColumns("invalid"); ok || cols != nil {
		t.Fatal("invalid signal accepted")
	}
}

func TestColumnNamesContainNoDots(t *testing.T) {
	for _, signal := range SignalNames() {
		cols, _ := SignalColumns(signal)
		for _, col := range cols {
			if strings.Contains(col.Name, ".") {
				t.Errorf("%s column %q contains a dot", signal, col.Name)
			}
		}
	}
}
