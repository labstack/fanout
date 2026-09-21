package metrics

import (
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gatherDuckDBMemory returns the exported per-tag memory series, by tag.
func gatherDuckDBMemory(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "fanout_duckdb_memory_bytes" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "tag" {
					out[label.GetValue()] = metric.GetGauge().GetValue()
				}
			}
		}
	}
	return out
}

// The whole purpose of this series is to measure what DuckDB holds right now,
// so that the untracked gap against process RSS can be derived from it. A
// failed read must therefore export nothing. Reporting the last successful
// figures as if they were current is the one outcome that actively misleads:
// the gap is computed against a stale sum, and the number that was supposed to
// reveal an impending out-of-memory kill reads normal.
func TestDuckDBMemoryDropsSeriesWhenTheReadFails(t *testing.T) {
	release := SetDuckDBMemorySource(func() map[string]int64 {
		return map[string]int64{"BASE_TABLE": 4096}
	})
	if got := gatherDuckDBMemory(t); got["BASE_TABLE"] != 4096 {
		release()
		t.Fatalf("healthy read exported %v, want BASE_TABLE=4096", got)
	}
	release()

	// The source is installed but cannot read: the closure in internal/query
	// returns nil for a query error, a scan error, or a partial read.
	release = SetDuckDBMemorySource(func() map[string]int64 { return nil })
	defer release()
	if got := gatherDuckDBMemory(t); len(got) != 0 {
		t.Errorf("after a failed read the gauge still exports %v; stale values are reported as current", got)
	}
}

// Prometheus scrapes concurrently, and a GaugeVec that is Reset and refilled on
// every scrape is shared mutable state: two scrapes interleave, and one of them
// observes the other's half-finished refill -- missing tags, and a sum that
// understates what DuckDB holds.
func TestDuckDBMemoryIsSafeUnderConcurrentScrapes(t *testing.T) {
	release := SetDuckDBMemorySource(func() map[string]int64 {
		return map[string]int64{"BASE_TABLE": 1, "HASH_TABLE": 2, "ORDER_BY": 3}
	})
	defer release()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				families, err := prometheus.DefaultGatherer.Gather()
				if err != nil {
					t.Error(err)
					return
				}
				for _, family := range families {
					if family.GetName() != "fanout_duckdb_memory_bytes" {
						continue
					}
					if n := len(family.GetMetric()); n != 3 {
						t.Errorf("scrape saw %d tags, want 3: a concurrent refill was observed half-done", n)
						return
					}
					_ = dto.MetricType_GAUGE
				}
			}
		}()
	}
	wg.Wait()
}

// The gauge must not invent a series when nothing has installed a source,
// because an empty family reads exactly like "DuckDB is holding nothing".
func TestDuckDBMemoryExportsNothingWithoutASource(t *testing.T) {
	if got := gatherDuckDBMemory(t); len(got) != 0 {
		t.Errorf("with no source installed the gauge exports %v", got)
	}
	if families, err := prometheus.DefaultGatherer.Gather(); err == nil {
		for _, family := range families {
			if strings.Contains(family.GetName(), "duckdb_memory_scrape") {
				t.Errorf("%s exists only to pump a push gauge; a collector does not need it", family.GetName())
			}
		}
	}
}
