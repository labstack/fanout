package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

func TestDuckDBMemoryPublishesCompleteSamplesAndTimestamp(t *testing.T) {
	record, release := SetDuckDBMemorySource(time.Minute)
	defer release()
	if got := gatherDuckDBMemory(t); len(got) != 0 {
		t.Fatalf("before the first sample exported %v", got)
	}
	if got := gatherMemoryTimestamp(t); got != 0 {
		t.Fatalf("before the first sample timestamp=%g", got)
	}
	tags := map[string]int64{"BASE_TABLE": 4096}
	at := time.Now()
	record(tags, at)
	tags["BASE_TABLE"] = 0 // Publication owns an immutable copy.
	if got := gatherDuckDBMemory(t); got["BASE_TABLE"] != 4096 {
		t.Fatalf("healthy read exported %v, want BASE_TABLE=4096", got)
	}
	if got := gatherMemoryTimestamp(t); got != float64(at.UnixNano())/1e9 {
		t.Fatalf("timestamp=%g, want %v", got, at)
	}
	record(map[string]int64{"HASH_TABLE": 2048}, time.Now())
	if got := gatherDuckDBMemory(t); len(got) != 1 || got["HASH_TABLE"] != 2048 {
		t.Fatalf("replacement sample exported %v", got)
	}
}

func TestDuckDBMemoryReleaseOnlyClearsItsOwner(t *testing.T) {
	recordOld, releaseOld := SetDuckDBMemorySource(time.Minute)
	record, release := SetDuckDBMemorySource(time.Minute)
	defer release()
	record(map[string]int64{"BASE_TABLE": 2}, time.Now())
	releaseOld()
	recordOld(map[string]int64{"BASE_TABLE": 1}, time.Now())
	if got := gatherDuckDBMemory(t); got["BASE_TABLE"] != 2 {
		t.Fatalf("old owner changed the current sample: %v", got)
	}
	release()
	if got := gatherDuckDBMemory(t); len(got) != 0 {
		t.Fatalf("released source still exports %v", got)
	}
}

func TestDuckDBMemoryIsSafeUnderConcurrentScrapes(t *testing.T) {
	record, release := SetDuckDBMemorySource(time.Minute)
	defer release()
	record(map[string]int64{"BASE_TABLE": 1, "HASH_TABLE": 2, "ORDER_BY": 3}, time.Now())

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			record(map[string]int64{"BASE_TABLE": 1, "HASH_TABLE": 2, "ORDER_BY": 3}, time.Now())
		}
	})
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
	if got := gatherMemoryTimestamp(t); got != -1 {
		t.Errorf("without a source timestamp=%g, want absent", got)
	}
}

func gatherMemoryTimestamp(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "fanout_duckdb_memory_last_success_timestamp_seconds" {
			return family.Metric[0].GetGauge().GetValue()
		}
	}
	return -1
}

func TestDuckDBMemoryDropsStaleSeriesAndKeepsTimestamp(t *testing.T) {
	record, release := SetDuckDBMemorySource(time.Second)
	defer release()
	at := time.Now().Add(-3 * time.Second)
	record(map[string]int64{"BASE_TABLE": 4096}, at)
	if got := gatherDuckDBMemory(t); len(got) != 0 {
		t.Fatalf("stale sample exported %v", got)
	}
	if got := gatherMemoryTimestamp(t); got != float64(at.UnixNano())/1e9 {
		t.Fatalf("stale sample timestamp=%g, want %v", got, at)
	}
}
