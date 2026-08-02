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
		ExportLatencyMs: latencyReport{Count: 100, P95Ms: 150},
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
		"query p95 1600ms > 1500ms release SLO",
		"rows dropped=1",
		"export p95 150ms > 100ms",
		"query p95 1600ms > 1000ms",
	} {
		if !hasFailure(failures, expected) {
			t.Fatalf("failures %q do not contain %q", failures, expected)
		}
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
