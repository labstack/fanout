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

func TestPanelAdmissionLabelsBoundUserInput(t *testing.T) {
	count := func(status string) uint64 {
		m := &dto.Metric{}
		if err := panelAdmissionDuration.WithLabelValues("custom", status).(prometheus.Metric).Write(m); err != nil {
			t.Fatal(err)
		}
		return m.Histogram.GetSampleCount()
	}
	before := count("other")
	RecordPanelAdmission("user panel", "user outcome", .1)
	RecordPanelAdmission("another panel", "another outcome", .2)
	if got := count("other") - before; got != 2 {
		t.Fatalf("unbounded inputs did not share one label set: %d", got)
	}
	for _, status := range []string{"admitted", "rejected", "canceled", "timeout"} {
		before := count(status)
		RecordPanelAdmission("user panel", status, .1)
		if count(status)-before != 1 {
			t.Fatalf("missing admission outcome %s", status)
		}
	}
}
