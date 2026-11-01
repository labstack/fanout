//go:build !race

package telemetry

import (
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// Ordering a batch for its span file must not duplicate the payload it was
// handed, nor even its rows: it sorts a permutation over per-row keys. The
// payload columns are the bulk of a span, the originals stay live until the
// commit is durably acknowledged, and four commit workers run at once -- so a
// copy here is four copies of the batch resident at the moment the process is
// most likely to be killed.
//
// The end-to-end commit gate cannot see this on its own: parquet-go and zstd
// pool their buffers, so its figure swings between 3.2x and 5.3x payload run
// to run and a copy of this size hides inside that spread. Measuring the
// ordering alone is quiet enough to assert on.
func TestSpanOrderingDoesNotCopyRows(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	spans := make([]Span, 20_000)
	for i := range spans {
		spans[i] = syntheticSpan(i, base)
	}
	var jsonBytes int64
	for i := range spans {
		jsonBytes += int64(ValueBytes(spans[i].Resource) + ValueBytes(spans[i].Attributes) +
			spans[i].ListBytes())
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	perm, keys := sortRows(Spans, spans)

	runtime.ReadMemStats(&after)
	runtime.KeepAlive(perm)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(spans)

	allocated := int64(after.TotalAlloc - before.TotalAlloc)
	order := int64(len(keys.n))*int64(unsafe.Sizeof(uint64(0))) + int64(len(perm))*int64(unsafe.Sizeof(int32(0)))
	for _, text := range keys.s {
		order += int64(len(text)) * int64(unsafe.Sizeof(""))
	}
	rows := int64(len(spans)) * int64(unsafe.Sizeof(Span{}))
	t.Logf("json payload %.1f MiB, row headers %.1f MiB, keys and permutation %.1f MiB, allocated %.1f MiB",
		float64(jsonBytes)/(1<<20), float64(rows)/(1<<20), float64(order)/(1<<20), float64(allocated)/(1<<20))

	// The keys and permutation plus a margin, and the span ID's rank map,
	// which stops at maxRanked values whatever the batch size: no copy of the
	// rows, and nothing like the payload again.
	limit := order + order/4 + 512<<10
	if allocated > limit {
		t.Errorf("ordering allocated %.1f MiB for %.1f MiB of keys; it is copying rows or payload rather than referencing them",
			float64(allocated)/(1<<20), float64(order)/(1<<20))
	}
}
