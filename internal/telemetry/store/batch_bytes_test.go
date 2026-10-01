package store

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestBatchBytesChargesResourceIdentityOnceAcrossSignals(t *testing.T) {
	resource := map[string]any{"service.name": "checkout", "nested": []any{true, int64(7)}}
	batch := Batch{Spans: []telemetry.Span{{Resource: resource}, {Resource: resource}}, Logs: []telemetry.Log{{Resource: resource}}, Metrics: []telemetry.Metric{{Resource: resource}}}
	if got, want := batchBytes(batch), telemetry.ValueBytes(resource)+4*telemetry.ValueBytes(map[string]any(nil)); got != want {
		t.Fatalf("shared resource: got %d, want %d", got, want)
	}
	batch.Spans[1].Resource = maps.Clone(resource)
	if got, want := batchBytes(batch), 2*telemetry.ValueBytes(resource)+4*telemetry.ValueBytes(map[string]any(nil)); got != want {
		t.Fatalf("distinct equal resource maps: got %d, want %d", got, want)
	}
	batch.Spans[0].Attributes = map[string]any{"same": "value"}
	batch.Spans[1].Attributes = maps.Clone(batch.Spans[0].Attributes)
	if got, want := batchBytes(batch), 2*telemetry.ValueBytes(resource)+2*telemetry.ValueBytes(batch.Spans[0].Attributes)+2*telemetry.ValueBytes(map[string]any(nil)); got != want {
		t.Fatalf("row-owned attributes: got %d, want %d", got, want)
	}
}

func TestRichSharedResourcesReachRowTargetAndUseActualAdmissionBytes(t *testing.T) {
	newExport := func() Batch {
		resource := make(map[string]any, 24)
		for i := range 24 {
			resource[fmt.Sprintf("kubernetes.resource.%02d", i)] = strings.Repeat("r", 32)
		}
		batch := Batch{Spans: make([]telemetry.Span, maxGroupBatchRows/2)}
		for i := range batch.Spans {
			attrs := make(map[string]any, 5)
			for k := range 5 {
				attrs[fmt.Sprintf("span.attribute.%d", k)] = strings.Repeat("a", 64)
			}
			batch.Spans[i] = telemetry.Span{Resource: resource, Attributes: attrs, Name: strings.Repeat("n", 128)}
		}
		return batch
	}
	first, second := newExport(), newExport()
	if !groupBatchFits(batchBytes(first), second, 1) {
		t.Fatal("24-key shared resources prematurely cut a 50k-row group")
	}
	w := NewWriter(nil, maxGroupBatchRows)
	w.SetInFlightBudget(maxGroupBatchBytes)
	for _, batch := range []Batch{first, second} {
		if err := w.reserve(batch); err != nil {
			t.Fatalf("actual retained exports fit the budget: %v", err)
		}
	}
	if err := w.reserve(newExport()); err == nil {
		t.Fatal("distinct requests must each charge their own resource and row payloads")
	}
	w.release(first)
	w.release(second)
	if got := w.inFlightBytes.Load(); got != 0 {
		t.Fatalf("reservation leak: %d", got)
	}
	t.Logf("50k rows: %d charged bytes; full per-row resources would charge %d", batchBytes(first)+batchBytes(second), batchBytes(first)+batchBytes(second)+(maxGroupBatchRows-2)*telemetry.ValueBytes(first.Spans[0].Resource))
}

// Rows are a poor proxy for what a batch costs. maxGroupBatchRows caps a group
// at 50,000 rows, but a row is whatever the sender put in it: 50,000 bare log
// lines and 50,000 spans carrying kilobytes of attributes each are the same
// number and differ by orders of magnitude in what the commit has to hold. The
// row ceiling alone therefore bounds nothing that matters on a fat-row stream.
func TestBatchBytesMeasuresThePayloadNotTheRowCount(t *testing.T) {
	thin := Batch{Spans: make([]telemetry.Span, 100)}
	fat := Batch{Spans: make([]telemetry.Span, 100)}
	for i := range fat.Spans {
		fat.Spans[i].Attributes = map[string]any{"payload": strings.Repeat("x", 4096)}
	}

	if batchRows(thin) != batchRows(fat) {
		t.Fatalf("fixture is wrong: row counts must match to make the point")
	}
	thinBytes, fatBytes := batchBytes(thin), batchBytes(fat)
	if fatBytes <= thinBytes {
		t.Errorf("batchBytes: fat %d, thin %d; the payload must be what is counted", fatBytes, thinBytes)
	}
	if fatBytes < 100*4096 {
		t.Errorf("batchBytes = %d, want at least the %d bytes of attributes", fatBytes, 100*4096)
	}
}

// The byte ceiling has to admit a single oversized request, for the same
// reason the row ceiling does: one request is already one atomic directory,
// and refusing it would drop data rather than batch it more carefully.
func TestGroupBatchBytesAdmitsALoneOversizedRequest(t *testing.T) {
	huge := Batch{Spans: make([]telemetry.Span, 1)}
	huge.Spans[0].Attributes = map[string]any{"payload": strings.Repeat("x", maxGroupBatchBytes*2)}

	if got := batchBytes(huge); got <= maxGroupBatchBytes {
		t.Fatalf("fixture is wrong: batchBytes = %d, want more than the %d ceiling", got, maxGroupBatchBytes)
	}
	if !groupBatchFits(0, huge, 0) {
		t.Error("an empty group must admit any single request, however large")
	}
	if groupBatchFits(maxGroupBatchBytes, huge, 1) {
		t.Error("a non-empty group must not accept a request that takes it past the ceiling")
	}
}
