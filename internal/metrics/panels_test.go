package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestPanelQueryLabelsBoundUserInput(t *testing.T) {
	count := func() uint64 {
		m := &dto.Metric{}
		if err := panelQueryDuration.WithLabelValues("custom", "other").(prometheus.Metric).Write(m); err != nil {
			t.Fatal(err)
		}
		return m.Histogram.GetSampleCount()
	}
	before := count()
	RecordPanelQuery("arbitrary user panel", "arbitrary status", .1)
	RecordPanelQuery("another user panel", "another status", .2)
	if got := count() - before; got != 2 {
		t.Fatalf("unbounded inputs did not share one label set: %d", got)
	}
}
