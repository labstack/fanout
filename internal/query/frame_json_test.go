package query

import (
	"encoding/json"
	"testing"
)

// DuckDB writes nonfinite doubles inside a serialized column as bare NaN or
// Infinity, which no JSON parser accepts. A panel's JSON column must parse.
func TestJSONColumnsCarryNonfiniteNumbersAsStrings(t *testing.T) {
	got := convertDynamic(`{"nan":NaN,"up":Infinity,"down":[-Infinity],"text":"NaN"}`, "json")
	var value map[string]any
	if err := json.Unmarshal([]byte(got.(string)), &value); err != nil {
		t.Fatalf("%v: %v", got, err)
	}
	if value["nan"] != "NaN" || value["up"] != "Infinity" || value["text"] != "NaN" {
		t.Fatalf("value = %#v", value)
	}
}
