//go:build !race

package telemetry

import (
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// Converting a batch into Parquet rows must not duplicate the payload it was
// handed. The JSON columns are the bulk of a span, the originals stay live
// until the commit is durably acknowledged, and four commit workers run at
// once -- so a copy here is four copies of the batch resident at the moment
// the process is most likely to be killed.
//
// The end-to-end commit gate cannot see this on its own: parquet-go and zstd
// pool their buffers, so its figure swings between 3.2x and 5.3x payload run
// to run and a copy of this size hides inside that spread. Measuring the
// conversion alone is quiet enough to assert on.
func TestSpanConversionDoesNotCopyJSONPayloads(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	spans := make([]Span, 20_000)
	for i := range spans {
		spans[i] = syntheticSpan(i, base)
	}
	var jsonBytes int64
	for i := range spans {
		jsonBytes += int64(len(spans[i].ResourceJSON) + len(spans[i].AttributesJSON) +
			len(spans[i].EventsJSON) + len(spans[i].LinksJSON))
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	rows := make([]spanParquetRow, len(spans))
	for i := range spans {
		rows[i] = makeSpanParquetRow(spans[i])
	}

	runtime.ReadMemStats(&after)
	runtime.KeepAlive(rows)
	runtime.KeepAlive(spans)

	allocated := int64(after.TotalAlloc - before.TotalAlloc)
	headers := int64(len(spans)) * int64(spanParquetRowSize())
	t.Logf("json payload %.1f MiB, row headers %.1f MiB, allocated %.1f MiB",
		float64(jsonBytes)/(1<<20), float64(headers)/(1<<20), float64(allocated)/(1<<20))

	// Allowing the row slice plus a margin, and nothing like the payload again.
	limit := headers + jsonBytes/4
	if allocated > limit {
		t.Errorf("conversion allocated %.1f MiB against a %.1f MiB JSON payload; it is copying the payload rather than referencing it",
			float64(allocated)/(1<<20), float64(jsonBytes)/(1<<20))
	}
}

func spanParquetRowSize() uintptr { return unsafe.Sizeof(spanParquetRow{}) }
