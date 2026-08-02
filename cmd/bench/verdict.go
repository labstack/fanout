package main

import "fmt"

// mixedQueryP95SLOMs is the shipped release SLO for mixed read queries. It is
// always enforced; -max-query-p95-ms can only tighten it.
const mixedQueryP95SLOMs = 1500

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
	if report.QueryLatencyMs != nil && report.QueryLatencyMs.P95Ms > mixedQueryP95SLOMs {
		fails = append(fails, fmt.Sprintf("query p95 %.0fms > %dms release SLO",
			report.QueryLatencyMs.P95Ms, mixedQueryP95SLOMs))
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
			if report.Server.IngestRowsDelta < 0 || report.Server.RowsDroppedDelta < 0 ||
				report.Server.CPUSecondsDelta < 0 || report.Server.AllocBytesDelta < 0 ||
				report.Server.GCPauseSecondsDelta < 0 {
				fails = append(fails, "server counter reset mid-run (fanout restarted?)")
			}
		}
	}

	if cfg.maxExportP95 > 0 && report.ExportLatencyMs.P95Ms > cfg.maxExportP95 {
		fails = append(fails, fmt.Sprintf("export p95 %.0fms > %.0fms", report.ExportLatencyMs.P95Ms, cfg.maxExportP95))
	}
	// Only report the caller's threshold when it is tighter than the release SLO;
	// at or above it the SLO check above has already fired for the same number.
	if cfg.maxQueryP95 > 0 && cfg.maxQueryP95 < mixedQueryP95SLOMs &&
		report.QueryLatencyMs != nil && report.QueryLatencyMs.P95Ms > cfg.maxQueryP95 {
		fails = append(fails, fmt.Sprintf("query p95 %.0fms > %.0fms", report.QueryLatencyMs.P95Ms, cfg.maxQueryP95))
	}
	return fails
}
