package main

import (
	"strings"
	"testing"
)

func hasFailure(failures []string, want string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, want) {
			return true
		}
	}
	return false
}

func TestEvaluateReportCollectsEveryFailure(t *testing.T) {
	queryLatency := latencyReport{Count: 10, P95Ms: 1600}
	cfg := config{
		metricsURL:   "https://metrics.example.test",
		queryURL:     "https://query.example.test",
		queryWorkers: 4,
		maxExportP95: 100,
		maxQueryP95:  1000,
	}
	report := report{
		// 5 send errors against 100 successes is a 4.76% drop rate, far above
		// the 0.1% guardrail: a run that silently loses that much telemetry
		// cannot support a throughput comparison.
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 150},
		SendErrors:      5,
		QueriesRun:      10,
		QueryErrors:     2,
		QueryLatencyMs:  &queryLatency,
		Server: &serverReport{
			BaselineAvailable: true,
			RowsDroppedDelta:  1,
		},
	}

	failures := evaluateReport(cfg, report, []string{"final metrics scrape failed"})
	for _, expected := range []string{
		"final metrics scrape failed",
		"query errors=2",
		"rows dropped=1",
		"export p95 150ms > 100ms",
		"query p95 1600ms > 1000ms",
		"send error rate 4.762% (5/105) > 0.1%",
	} {
		if !hasFailure(failures, expected) {
			t.Fatalf("failures %q do not contain %q", failures, expected)
		}
	}
}

// Latency alone must not fail a run. A built-in millisecond threshold encodes
// the machine it was calibrated on, so the same binary would "fail" purely for
// running on a smaller computer.
func TestEvaluateReportDoesNotJudgeLatencyWithoutACallerThreshold(t *testing.T) {
	queryLatency := latencyReport{Count: 100, P95Ms: 9000}
	cfg := config{queryURL: "https://query.example.test", queryWorkers: 4}
	report := report{
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 8000},
		QueriesRun:      100,
		QueryLatencyMs:  &queryLatency,
	}

	if failures := evaluateReport(cfg, report, nil); len(failures) != 0 {
		t.Fatalf("slow but healthy run failed: %q", failures)
	}
}

// A run that produced no usable evidence must fail rather than report
// percentiles computed over whatever survived.
func TestEvaluateReportRejectsUnusableRuns(t *testing.T) {
	metricsCfg := config{metricsURL: "https://metrics.example.test"}
	tests := []struct {
		name string
		cfg  config
		rep  report
		want string
	}{
		{
			name: "no exports",
			rep:  report{},
			want: "no OTLP exports succeeded",
		},
		{
			name: "query load configured but no queries ran",
			cfg:  config{queryURL: "http://query", queryWorkers: 1},
			rep:  report{ExportLatencyMs: latencyReport{Count: 10}},
			want: "no mixed queries succeeded",
		},
		{
			name: "metrics requested but absent",
			cfg:  metricsCfg,
			rep:  report{ExportLatencyMs: latencyReport{Count: 10}},
			want: "server metrics evidence unavailable",
		},
		{
			name: "baseline scrape missing",
			cfg:  metricsCfg,
			rep: report{
				ExportLatencyMs: latencyReport{Count: 10},
				Server:          &serverReport{BaselineAvailable: false},
			},
			want: "server metrics baseline unavailable",
		},
		{
			name: "counter went backwards",
			cfg:  metricsCfg,
			rep: report{
				ExportLatencyMs: latencyReport{Count: 10},
				Server:          &serverReport{BaselineAvailable: true, IngestRowsDelta: -1},
			},
			want: "server counter reset mid-run",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if failures := evaluateReport(test.cfg, test.rep, nil); !hasFailure(failures, test.want) {
				t.Fatalf("failures %q do not contain %q", failures, test.want)
			}
		})
	}
}

func TestEvaluateReportAcceptsHealthyRun(t *testing.T) {
	queryLatency := latencyReport{Count: 10, P95Ms: 100}
	failures := evaluateReport(config{queryURL: "http://query", queryWorkers: 1}, report{
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 20},
		QueriesRun:      10,
		QueryLatencyMs:  &queryLatency,
	}, nil)
	if len(failures) != 0 {
		t.Fatalf("healthy run failed: %s", strings.Join(failures, "; "))
	}
}

// Rollup, merge, and maintenance are what this harness exists to measure. If
// they fail every cycle the run must fail, even though raw-fallback queries stay
// fast and every other signal looks healthy.
func TestEvaluateReportFailsOnBackgroundWorkErrors(t *testing.T) {
	queryLatency := latencyReport{Count: 10, P95Ms: 100}
	failures := evaluateReport(config{metricsURL: "https://metrics.example.test"}, report{
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 20},
		QueriesRun:      10,
		QueryLatencyMs:  &queryLatency,
		Server: &serverReport{
			BaselineAvailable: true,
			Rollups: map[string]rollupReport{
				"service": {Outcomes: map[string]float64{"success": 30}},
				"edge":    {Outcomes: map[string]float64{"error": 12}},
			},
			TelemetryOperations: map[string]backgroundOperationReport{
				"maintenance": {Outcomes: map[string]float64{"error": 3}},
				"compaction":  {Outcomes: map[string]float64{"success": 9}},
			},
		},
	}, nil)
	for _, want := range []string{"edge rollup errors=12", "telemetry maintenance errors=3"} {
		if !hasFailure(failures, want) {
			t.Fatalf("failures %q do not contain %q", failures, want)
		}
	}
	if hasFailure(failures, "service rollup") || hasFailure(failures, "telemetry merge") {
		t.Errorf("healthy components reported as failures: %q", failures)
	}
}

// A restart is caught from the process start time. Deltas alone miss it: a
// server that restarts early can out-count its own baseline, leaving every
// delta positive while the report describes two different processes.
func TestEvaluateReportDetectsRestartWithPositiveDeltas(t *testing.T) {
	queryLatency := latencyReport{Count: 10, P95Ms: 100}
	failures := evaluateReport(config{metricsURL: "https://metrics.example.test"}, report{
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 20},
		QueriesRun:      10,
		QueryLatencyMs:  &queryLatency,
		Server: &serverReport{
			BaselineAvailable: true,
			ProcessRestarted:  true,
			// Every delta is positive — the old sign check sees nothing wrong.
			IngestRowsDelta: 5000,
			CPUSecondsDelta: 42,
			AllocBytesDelta: 1 << 30,
		},
	}, nil)
	if !hasFailure(failures, "fanout restarted mid-run") {
		t.Fatalf("failures %q do not report the restart", failures)
	}
}
