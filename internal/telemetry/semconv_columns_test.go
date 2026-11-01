package telemetry

import (
	"testing"

	"github.com/labstack/fanout/internal/telemetry/semconv"
)

func TestSemconvColumnsAvoidRowColumns(t *testing.T) {
	existing := map[string]string{}
	for _, s := range Signals {
		for _, c := range s.Columns {
			existing[c.Name] = s.Name
		}
	}
	for _, k := range semconv.Keys {
		if signal, ok := existing[k.Column]; ok {
			t.Errorf("%s column %s collides with %s", k.Name, k.Column, signal)
		}
	}
}
