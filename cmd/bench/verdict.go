package main

import (
	"fmt"
	"sort"
)

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Latency is judged against -max-export-p95-ms / -max-query-p95-ms when the
// caller sets them, and not otherwise.
//
// There is deliberately no built-in latency SLO. A fixed millisecond threshold
// encodes the machine it was calibrated on: the same binary that clears it on
// four cores fails it on two, and the run reports a defect where there is only
// a smaller computer. Capacity is reported instead, and a gate that wants a
// threshold passes one in.
//
// Everything below this line stays a hard failure, because it makes the numbers
// themselves untrustworthy rather than merely unflattering: a run that ingested
// nothing, dropped rows, lost its baseline, or straddled a server restart reads
// as a clean p95-of-survivors if it is allowed to pass.

// evaluateReport lists everything that makes a run untrustworthy or failing.
// A run that ingested nothing, dropped rows, lost its metrics baseline, or saw
// the server restart mid-run must FAIL loudly: otherwise p95-of-survivors reads
// a healthy number and a broken run looks like a clean pass.
func evaluateReport(cfg config, report report, infrastructureFailures []string) []string {
	fails := append([]string(nil), infrastructureFailures...)

	if report.ExportLatencyMs.Count == 0 {
		fails = append(fails, "no OTLP exports succeeded")
	}
	if attempted := report.ExportLatencyMs.Count + report.SendErrors; report.SendErrors > 0 && attempted > 0 {
		if rate := float64(report.SendErrors) / float64(attempted); rate > maxSendErrorRate {
			fails = append(fails, fmt.Sprintf("send error rate %.3f%% (%d/%d) > %.1f%%",
				rate*100, report.SendErrors, attempted, maxSendErrorRate*100))
		}
	}
	if cfg.queryURL != "" && cfg.queryWorkers > 0 && report.QueriesRun == 0 {
		fails = append(fails, "no mixed queries succeeded")
	}
	if report.QueryErrors > 0 {
		fails = append(fails, fmt.Sprintf("query errors=%d", report.QueryErrors))
	}

	// Without a server-side baseline every delta below is meaningless, and a
	// negative delta means a counter reset — i.e. fanout restarted mid-run, so
	// the whole report describes two different processes.
	if cfg.metricsURL != "" {
		switch {
		case report.Server == nil:
			fails = append(fails, "server metrics evidence unavailable")
		case !report.Server.BaselineAvailable:
			fails = append(fails, "server metrics baseline unavailable")
		default:
			if report.Server.RowsDroppedDelta > 0 {
				fails = append(fails, fmt.Sprintf("rows dropped=%.0f", report.Server.RowsDroppedDelta))
			}
			// A restart is detected from the process start time, not from a
			// negative delta: if fanout restarts early enough, the new process
			// can out-count the old baseline and every delta stays positive
			// while the report silently describes two different processes.
			if report.Server.ProcessRestarted {
				fails = append(fails, "fanout restarted mid-run (process start time changed)")
			} else if report.Server.IngestRowsDelta < 0 || report.Server.RowsDroppedDelta < 0 ||
				report.Server.CPUSecondsDelta < 0 || report.Server.AllocBytesDelta < 0 ||
				report.Server.GCPauseSecondsDelta < 0 {
				fails = append(fails, "server counter reset mid-run (fanout restarted?)")
			}
			// Rollups, merge, and maintenance are what this harness exists to
			// measure. Left unchecked they can fail every cycle while queries
			// stay fast on raw fallback and the run reports PASS.
			// Sorted so the failure list — which is written to the report — does
			// not vary with Go's map iteration order between runs.
			for _, name := range sortedMapKeys(report.Server.Rollups) {
				if errors := report.Server.Rollups[name].Outcomes["error"]; errors > 0 {
					fails = append(fails, fmt.Sprintf("%s rollup errors=%.0f", name, errors))
				}
			}
			for _, name := range sortedMapKeys(report.Server.TelemetryOperations) {
				if errors := report.Server.TelemetryOperations[name].Outcomes["error"]; errors > 0 {
					fails = append(fails, fmt.Sprintf("telemetry %s errors=%.0f", name, errors))
				}
			}
		}
	}

	if cfg.maxExportP95 > 0 && report.ExportLatencyMs.P95Ms > cfg.maxExportP95 {
		fails = append(fails, fmt.Sprintf("export p95 %.0fms > %.0fms", report.ExportLatencyMs.P95Ms, cfg.maxExportP95))
	}
	if cfg.maxQueryP95 > 0 &&
		report.QueryLatencyMs != nil && report.QueryLatencyMs.P95Ms > cfg.maxQueryP95 {
		fails = append(fails, fmt.Sprintf("query p95 %.0fms > %.0fms", report.QueryLatencyMs.P95Ms, cfg.maxQueryP95))
	}
	return fails
}
