package telemetry

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// commitAllocRatioLimit is how many bytes one CommitBatch may allocate
// relative to the payload it was handed. The number that matters is the
// multiplier, not an absolute byte count: it scales with batch size, and batch
// size scales with ingest rate, so a process sized for a quiet hour is sized
// wrong for a busy one.
//
// The assertion is on total bytes allocated rather than peak live heap.
// Peak heap carries whatever slack the collector happened to be holding when
// the sampler looked, which varies with GOMAXPROCS and machine speed: the same
// code measured 2.79x locally and 3.83x on a CI runner, and the two encodings'
// peak-heap distributions overlap. Total allocation does not overlap and barely
// varies -- it answers "how many copies does this make", which is the question.
// Peak heap is still reported, because it is what the kernel kills on.
//
// This is a ratchet, not a target, and it is placed between two measured
// distributions rather than guessed. Dictionary-encoded near-unique JSON
// columns allocate 10.68x-10.90x; writing them plain allocates 4.40x-5.69x.
// The limit sits in that gap.
//
// What remains above 1x is a conversion copy of every JSON column
// (makeSpanParquetRow) plus row-struct headers and the writer's own buffers.
// Lower this constant as each is addressed; see the ordered plan in the
// ingest-memory issue.
const commitAllocRatioLimit = 7.0

// syntheticSpan builds a span whose JSON columns look like production: a
// resource block shared across the batch, and attributes/events/links that are
// near-unique per row. That distinction is the whole point — a dictionary over
// a near-unique column stores every value twice.
func syntheticSpan(i int, base time.Time) Span {
	start := base.Add(time.Duration(i) * time.Microsecond)
	return Span{
		Namespace:      "prod",
		TraceID:        fmt.Sprintf("%032x", i),
		SpanID:         fmt.Sprintf("%016x", i),
		ServiceName:    "checkout",
		Name:           "POST /api/orders",
		Kind:           "SERVER",
		StartUnixNanos: start.UnixNano(),
		EndUnixNanos:   start.Add(3 * time.Millisecond).UnixNano(),
		DurationMS:     3,
		StatusCode:     "OK",
		ResourceJSON:   []byte(`{"service.name":"checkout","deployment.environment":"prod","host.name":"cube-10"}`),
		AttributesJSON: []byte(fmt.Sprintf(
			`{"http.method":"POST","http.route":"/api/orders","http.status_code":200,"order.id":"%d","user.id":"u-%d","session":"%032x"}`, i, i*7, i)),
		EventsJSON: []byte(fmt.Sprintf(
			`[{"name":"validated","time":%d},{"name":"charged","time":%d,"amount":%d}]`, start.UnixNano(), start.UnixNano()+1000, i%9999)),
		LinksJSON: []byte(fmt.Sprintf(`[{"trace_id":"%032x","span_id":"%016x"}]`, i+1, i+1)),
	}
}

func payloadBytes(spans []Span) int64 {
	var total int64
	for i := range spans {
		total += int64(len(spans[i].ResourceJSON) + len(spans[i].AttributesJSON) +
			len(spans[i].EventsJSON) + len(spans[i].LinksJSON) +
			len(spans[i].TraceID) + len(spans[i].SpanID) + len(spans[i].ServiceName) + len(spans[i].Name))
	}
	return total
}

// samplePeakHeapInuse runs fn while polling HeapInuse, and reports the highest
// value observed above the pre-run baseline. GOGC is pinned low for the
// duration so the figure approximates live bytes rather than live bytes plus
// whatever slack the collector happened to be carrying; production GOGC roughly
// doubles it.
func samplePeakHeapInuse(fn func()) (peakBytes, allocated int64) {
	previousGC := debug.SetGCPercent(5)
	defer debug.SetGCPercent(previousGC)

	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	baseline := int64(stats.HeapInuse)
	allocatedBefore := int64(stats.TotalAlloc)

	var peak atomic.Int64
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		var sample runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&sample)
			if delta := int64(sample.HeapInuse) - baseline; delta > peak.Load() {
				peak.Store(delta)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	fn()
	close(stop)
	sampler.Wait()

	runtime.ReadMemStats(&stats)
	allocated = int64(stats.TotalAlloc) - allocatedBefore
	return peak.Load(), allocated
}

// One commit must not allocate several times the payload it was given. Four
// commit workers run concurrently, each queued behind more batches, so a
// multiplier here is a multiplier on the whole process.
func TestCommitBatchAllocationStaysNearPayloadSize(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~50k spans")
	}
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	spans := make([]Span, 50_000)
	for i := range spans {
		spans[i] = syntheticSpan(i, base)
	}
	raw := payloadBytes(spans)

	const attempts = 3
	peak, totalAlloc := int64(math.MaxInt64), int64(math.MaxInt64)
	for attempt := range attempts {
		store, err := OpenParquetStore(t.TempDir())
		if err != nil {
			t.Fatalf("open parquet store: %v", err)
		}
		observed, allocated := samplePeakHeapInuse(func() {
			// A distinct ID each time: CommitBatch is idempotent, and a repeat
			// would return early and measure nothing.
			id := fmt.Sprintf("peak-heap-%d", attempt)
			if err := store.CommitBatch(context.Background(), BatchMetadata{ID: id}, spans, nil, nil); err != nil {
				t.Errorf("commit batch: %v", err)
			}
		})
		t.Logf("attempt %d: peak heap %.1f MiB (%.2fx), allocated %.1f MiB (%.2fx)",
			attempt, float64(observed)/(1<<20), float64(observed)/float64(raw),
			float64(allocated)/(1<<20), float64(allocated)/float64(raw))
		peak = min(peak, observed)
		totalAlloc = min(totalAlloc, allocated)
	}
	runtime.KeepAlive(spans)

	ratio := float64(totalAlloc) / float64(raw)
	t.Logf("payload %.1f MiB, minimum allocated %.1f MiB (%.2fx), minimum peak heap %.1f MiB (%.2fx)",
		float64(raw)/(1<<20), float64(totalAlloc)/(1<<20), ratio,
		float64(peak)/(1<<20), float64(peak)/float64(raw))
	if ratio > commitAllocRatioLimit {
		t.Errorf("CommitBatch allocated %.2fx payload, limit %.2fx: one batch makes several copies of what it was handed",
			ratio, commitAllocRatioLimit)
	}
}

// BenchmarkCommitBatch reports peak heap alongside time so a regression shows
// up as a number rather than as an out-of-memory kill in production.
func BenchmarkCommitBatch(b *testing.B) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	spans := make([]Span, 50_000)
	for i := range spans {
		spans[i] = syntheticSpan(i, base)
	}
	raw := payloadBytes(spans)

	var peak int64
	for i := 0; b.Loop(); i++ {
		store, err := OpenParquetStore(b.TempDir())
		if err != nil {
			b.Fatalf("open parquet store: %v", err)
		}
		observed, _ := samplePeakHeapInuse(func() {
			if err := store.CommitBatch(context.Background(), BatchMetadata{ID: fmt.Sprintf("bench-%d", i)}, spans, nil, nil); err != nil {
				b.Fatalf("commit batch: %v", err)
			}
		})
		if observed > peak {
			peak = observed
		}
	}
	b.ReportMetric(float64(peak)/(1<<20), "peak-heap-MiB")
	b.ReportMetric(float64(peak)/float64(raw), "peak/payload")
}
