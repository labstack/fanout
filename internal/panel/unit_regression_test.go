package panel

import (
	"encoding/json"
	"testing"
)

func TestCompileMeasureUnitsOverridePanelUnit(t *testing.T) {
	p := &Panel{ID: "mixed", Viz: "table", Unit: "ms", Query: &Query{From: "spans"}}
	texts := []string{"p95(duration_ms)", "count_distinct(service)", "count()", "rate()", "error_rate()", "share()", "avg(duration_ms)", "min(duration_ms)", "max(duration_ms)", "sum(duration_ms)", "sum(attributes['unknown'])"}
	want := []string{"ms", "count", "count", "per_second", "percent", "percent", "ms", "ms", "ms", "ms", ""}
	c, err := compileQuery(p, measuresOf(t, "spans", texts...), nil, Scope{Start: compileStart, End: compileEnd})
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range c.Columns {
		if column.Unit != want[i] {
			t.Errorf("%s unit = %q, want %q", column.Name, column.Unit, want[i])
		}
	}
	// The frame must preserve the authoritative units on the wire, including
	// omitting unknown units so the client can use its panel fallback.
	data, err := json.Marshal(newFrame(c.Columns))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Columns []map[string]any `json:"columns"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Columns[1]["unit"] != "count" {
		t.Errorf("count column JSON = %v", wire.Columns[1])
	}
	if _, ok := wire.Columns[len(wire.Columns)-1]["unit"]; ok {
		t.Error("unknown unit should be omitted")
	}
}
