package store

import (
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

// Rows are a poor proxy for what a batch costs. maxGroupBatchRows caps a group
// at 50,000 rows, but a row is whatever the sender put in it: 50,000 bare log
// lines and 50,000 spans carrying kilobytes of attributes each are the same
// number and differ by orders of magnitude in what the commit has to hold. The
// row ceiling alone therefore bounds nothing that matters on a fat-row stream.
func TestBatchBytesMeasuresThePayloadNotTheRowCount(t *testing.T) {
	thin := Batch{Spans: make([]telemetry.Span, 100)}
	fat := Batch{Spans: make([]telemetry.Span, 100)}
	for i := range fat.Spans {
		fat.Spans[i].AttributesJSON = strings.Repeat("x", 4096)
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
	huge.Spans[0].AttributesJSON = strings.Repeat("x", maxGroupBatchBytes*2)

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
