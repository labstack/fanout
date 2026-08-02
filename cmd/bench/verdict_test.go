package main

import (
	"strings"
	"testing"
)

func TestEvaluateReportPreservesZeroToleranceAndThresholdFailures(t *testing.T) {
	queryLatency := latencyReport{Count: 10, P95Ms: 1600}
	cfg := config{
		metricsURL:   "https://metrics.example.test",
		queryURL:     "https://query.example.test",
		queryWorkers: 4,
		maxExportP95: 100,
		maxQueryP95:  1000,
	}
	report := report{
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 150},
		SendErrors:      1,
		QueriesRun:      10,
		QueryErrors:     2,
		QueryLatencyMs:  &queryLatency,
		Server: &serverReport{
			BaselineAvailable: true,
			RowsDroppedDelta:  1,
		},
	}

	zeroTolerance, thresholds := evaluateReport(cfg, report, []string{"baseline metrics scrape failed"})
	for _, expected := range []string{
		"baseline metrics scrape failed",
		"query errors=2",
		"query p95 1600ms > 1500ms release SLO",
		"rows dropped=1",
	} {
		if !containsSubstring(zeroTolerance, expected) {
			t.Fatalf("zero-tolerance failures %q do not contain %q", zeroTolerance, expected)
		}
	}
	if !containsSubstring(thresholds, "export p95 150ms > 100ms") || !containsSubstring(thresholds, "query p95 1600ms > 1000ms") {
		t.Fatalf("threshold failures = %q", thresholds)
	}
}

func TestEvaluateReportAcceptsHealthyRun(t *testing.T) {
	queryLatency := latencyReport{Count: 10, P95Ms: 100}
	zeroTolerance, thresholds := evaluateReport(config{queryURL: "http://query", queryWorkers: 1}, report{
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 20},
		QueriesRun:      10,
		QueryLatencyMs:  &queryLatency,
	}, nil)
	if len(zeroTolerance) != 0 || len(thresholds) != 0 {
		t.Fatalf("healthy run failed: zero=%s thresholds=%s", strings.Join(zeroTolerance, "; "), strings.Join(thresholds, "; "))
	}
}
