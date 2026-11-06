package telemetry

import (
	"testing"

	"github.com/labstack/fanout/internal/telemetry/semconv"
)

// Spans carry every standard key's column and logs and metrics every
// resource key's, under the key's own column name, which no other column
// takes.
func TestSemconvKeysHaveDedicatedColumns(t *testing.T) {
	for _, s := range Signals {
		keys := map[string]bool{}
		for _, c := range s.Columns {
			if c.Key != nil {
				if c.Name != c.Key.Column {
					t.Errorf("%s column %s holds %s", s.Name, c.Name, c.Key.Name)
				}
				keys[c.Name] = true
			}
		}
		for _, k := range semconv.Keys {
			if want := k.Scope == semconv.Resource || s == Spans; keys[k.Column] != want {
				t.Errorf("%s has column %s = %v, want %v", s.Name, k.Column, keys[k.Column], want)
			}
		}
		if got := len(s.Keys()); got != len(keys) {
			t.Errorf("%s declares %s twice", s.Name, "a key column")
		}
	}
}
