package main

import "fmt"

const mixedQueryP95SLOMs = 1500

// evaluateReport separates correctness/SLO failures from caller-selected
// screening thresholds so orchestration can preserve every per-run invariant
// violation instead of averaging it into a median.
func evaluateReport(cfg config, report report, infrastructureFailures []string) (zeroTolerance, thresholds []string) {
	zeroTolerance = append(zeroTolerance, infrastructureFailures...)
	if report.ExportLatencyMs.Count == 0 {
		zeroTolerance = append(zeroTolerance, "no OTLP exports succeeded")
	}
	if attempted := report.ExportLatencyMs.Count + report.SendErrors; report.SendErrors > 0 && attempted > 0 {
		if rate := float64(report.SendErrors) / float64(attempted); rate > maxSendErrorRate {
			zeroTolerance = append(zeroTolerance, fmt.Sprintf("send error rate %.3f%% (%d/%d) > %.1f%%",
				rate*100, report.SendErrors, attempted, maxSendErrorRate*100))
		}
	}
	if cfg.queryURL != "" && cfg.queryWorkers > 0 && report.QueriesRun == 0 {
		zeroTolerance = append(zeroTolerance, "no mixed queries succeeded")
	}
	if report.QueryErrors > 0 {
		zeroTolerance = append(zeroTolerance, fmt.Sprintf("query errors=%d", report.QueryErrors))
	}
	if report.QueryLatencyMs != nil && report.QueryLatencyMs.P95Ms > mixedQueryP95SLOMs {
		zeroTolerance = append(zeroTolerance, fmt.Sprintf("query p95 %.0fms > %.0fms release SLO",
			report.QueryLatencyMs.P95Ms, float64(mixedQueryP95SLOMs)))
	}
	if cfg.metricsURL != "" && report.Server == nil {
		zeroTolerance = appendUnique(zeroTolerance, "server metrics evidence unavailable")
	}
	if report.Server != nil {
		if !report.Server.BaselineAvailable {
			zeroTolerance = appendUnique(zeroTolerance, "server metrics baseline unavailable")
		}
		if report.Server.IngestRowsDelta < 0 {
			zeroTolerance = append(zeroTolerance, "ingest counter reset during run")
		}
		if report.Server.RowsDroppedDelta > 0 {
			zeroTolerance = append(zeroTolerance, fmt.Sprintf("rows dropped=%.0f", report.Server.RowsDroppedDelta))
		} else if report.Server.RowsDroppedDelta < 0 {
			zeroTolerance = append(zeroTolerance, "rows-dropped counter reset during run")
		}
		if report.Server.CPUSecondsDelta < 0 || report.Server.AllocBytesDelta < 0 || report.Server.GCPauseSecondsDelta < 0 {
			zeroTolerance = append(zeroTolerance, "process runtime counter reset during run")
		}
	}
	if cfg.maxExportP95 > 0 && report.ExportLatencyMs.P95Ms > cfg.maxExportP95 {
		thresholds = append(thresholds, fmt.Sprintf("export p95 %.0fms > %.0fms", report.ExportLatencyMs.P95Ms, cfg.maxExportP95))
	}
	if cfg.maxQueryP95 > 0 && cfg.maxQueryP95 < mixedQueryP95SLOMs && report.QueryLatencyMs != nil && report.QueryLatencyMs.P95Ms > cfg.maxQueryP95 {
		thresholds = append(thresholds, fmt.Sprintf("query p95 %.0fms > %.0fms", report.QueryLatencyMs.P95Ms, cfg.maxQueryP95))
	}
	return zeroTolerance, thresholds
}

func appendUnique(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}
