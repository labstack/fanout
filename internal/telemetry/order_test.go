package telemetry

import (
	"cmp"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// The radix path must give exactly the comparison sort's permutation: the
// same order with equal keys in input order. Keys range from constants to
// full 64-bit values, so some sets pack and some fall back.
func TestRadixSortMatchesComparisonSort(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	var packed, fellBack int
	for trial := range 2000 {
		n := []int{0, 1, 2, 3, 257, r.Intn(3000)}[trial%6]
		parts := 1 + r.Intn(len(sortKey{}.n))
		keys := make([]uint64, n*parts)
		for p := range parts {
			part := keyPart{index: p, signed: r.Intn(2) == 0}
			if part.signed && r.Intn(2) == 0 {
				part.bucket = 1 + r.Int63n(1000)
			}
			spread := []int64{0, 3, 1 << 20, 1 << 40, -1}[r.Intn(5)]
			base := r.Int63() - 1<<62
			for i := range n {
				var v int64
				switch spread {
				case 0:
					v = base
				case -1:
					v = int64(r.Uint64())
				default:
					v = base + r.Int63n(spread) - spread/2
				}
				keys[i*parts+p] = part.integer(v)
			}
		}
		want := identity(n)
		slices.SortFunc(want, func(a, b int32) int {
			for p := range parts {
				if c := cmp.Compare(keys[int(a)*parts+p], keys[int(b)*parts+p]); c != 0 {
					return c
				}
			}
			return cmp.Compare(a, b)
		})
		got := identity(n)
		if !radixSort(keys, parts, got) {
			fellBack++
			continue
		}
		packed++
		if !slices.Equal(got, want) {
			t.Fatalf("trial %d: %d rows, %d parts: radix order differs", trial, n, parts)
		}
	}
	if packed < 1000 || fellBack == 0 {
		t.Fatalf("packed %d trials and fell back on %d; want both paths", packed, fellBack)
	}
}

// sortRows must order each signal's rows by its declared order whichever
// path it takes: ranked text, bucketed time, full-width series IDs and ties.
func TestSortRowsMatchesDeclaredOrder(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	base := int64(1_790_000_000_000_000_000)
	logs := make([]Log, 5000)
	for i := range logs {
		logs[i] = Log{Namespace: fmt.Sprint("ns-", r.Intn(3)), ServiceName: fmt.Sprint("svc-", r.Intn(5)), EventUnixNanos: base + r.Int63n(3600e9)/1e6*1e6, IngestedAt: 1}
		if i%50 == 0 {
			logs[i].EventUnixNanos = 0 // falls back to ingest time
		}
	}
	checkOrder(t, Logs, logs)
	metrics := make([]Metric, 5000)
	for i := range metrics {
		metrics[i] = Metric{Namespace: "prod", ServiceName: fmt.Sprint("svc-", r.Intn(4)), Name: fmt.Sprint("m.", r.Intn(6)),
			SeriesID: []uint64{r.Uint64(), 7, 1 << 63}[r.Intn(3)], EventUnixNanos: base + r.Int63n(3)*3600e9 + r.Int63n(10)*10e9}
	}
	checkOrder(t, Metrics, metrics)
	spans := make([]Span, 5000)
	for i := range spans {
		spans[i] = Span{Namespace: "prod", TraceID: fmt.Sprint("t-", r.Intn(300)), SpanID: fmt.Sprint("s-", r.Intn(10000)), StartUnixNanos: r.Int63n(5) - 2}
	}
	checkOrder(t, Spans, spans)
}

// checkOrder compares sortRows with a stable sort on the rows' own column
// values.
func checkOrder[R any](t *testing.T, s *Signal, rows []R) {
	t.Helper()
	compare := func(a, b *R) int {
		for _, p := range s.Order {
			c, _ := s.Column(p.Column)
			var d int
			switch get := c.get.(type) {
			case func(*R) string:
				d = strings.Compare(get(a), get(b))
			case func(*R) uint64:
				d = cmp.Compare(get(a), get(b))
			case func(*R) int64:
				x, y := get(a), get(b)
				if p.Bucket > 0 {
					x, y = floorDiv(x, p.Bucket), floorDiv(y, p.Bucket)
				}
				d = cmp.Compare(x, y)
			default:
				t.Fatalf("%s order column %s has no accessor", s.Name, p.Column)
			}
			if d != 0 {
				return d
			}
		}
		return 0
	}
	want := identity(len(rows))
	slices.SortStableFunc(want, func(a, b int32) int { return compare(&rows[a], &rows[b]) })
	got, _ := sortRows(s, rows)
	if !slices.Equal(got, want) {
		t.Fatalf("%s rows out of declared order", s.Name)
	}
}

func floorDiv(v, d int64) int64 {
	q := v / d
	if v%d < 0 {
		q--
	}
	return q
}

func identity(n int) []int32 {
	perm := make([]int32, n)
	for i := range perm {
		perm[i] = int32(i)
	}
	return perm
}
