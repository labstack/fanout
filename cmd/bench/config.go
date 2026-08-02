package main

import (
	"errors"
	"fmt"
	"strings"
)

func validateTrialConfig(cfg config) error {
	switch {
	case cfg.rate <= 0:
		return errors.New("-rate must be positive")
	case cfg.workers <= 0:
		return errors.New("-workers must be positive")
	case cfg.services < 2:
		return errors.New("-services must be >= 2 (cross-service edges need a caller and a callee)")
	case cfg.namespaces <= 0:
		return errors.New("-namespaces must be positive")
	case cfg.cardinality <= 0:
		return errors.New("-attr-cardinality must be positive")
	case cfg.errorRate < 0 || cfg.errorRate > 1:
		return errors.New("-error-rate must be between 0 and 1")
	case cfg.msgRatio < 0 || cfg.msgRatio > 1:
		return errors.New("-messaging-ratio must be between 0 and 1")
	case cfg.duration < 0:
		return errors.New("-duration cannot be negative")
	case cfg.warmupDuration < 0:
		return errors.New("-warmup-duration cannot be negative")
	case cfg.queryWorkers < 0:
		return errors.New("-query-workers cannot be negative")
	case cfg.queryWorkers > 0 && strings.TrimSpace(cfg.queryURL) == "":
		return errors.New("-query-url is required when -query-workers is positive")
	case cfg.queryWorkers > 0 && cfg.queryRate <= 0:
		return errors.New("-query-rate must be positive when query load is enabled")
	case cfg.maxExportP95 < 0 || cfg.maxQueryP95 < 0:
		return errors.New("latency thresholds cannot be negative")
	case cfg.backfillHours < 0:
		return errors.New("-backfill-hours cannot be negative")
	case cfg.runOrdinal < 0:
		return errors.New("-run-ordinal cannot be negative")
	case strings.TrimSpace(cfg.stage) == "":
		return errors.New("-stage cannot be empty")
	case strings.TrimSpace(cfg.candidate) == "":
		return errors.New("-candidate cannot be empty")
	case cfg.measurementMode != "" && cfg.measurementMode != "unspecified" && cfg.measurementMode != "enabled" && cfg.measurementMode != "disabled":
		return errors.New("-measurement-instrumentation must be enabled, disabled, or unspecified")
	case strings.TrimSpace(cfg.fanoutSourceSHA256) != "" && len(strings.TrimSpace(cfg.fanoutSourceSHA256)) != 64:
		return errors.New("-fanout-source-sha256 must be a 64-character SHA-256 digest")
	case !cfg.screening && cfg.candidate != "control" && strings.TrimSpace(cfg.primaryTarget) == "":
		return errors.New("-primary-target is required for a non-control candidate")
	case cfg.memoryLimitBytes < 0:
		return errors.New("-memory-limit-bytes cannot be negative")
	case cfg.duckDBMaxConns < 0 || cfg.duckDBThreads < 0:
		return errors.New("DuckDB connection/thread settings cannot be negative")
	case cfg.flushSeconds < 0 || cfg.flushBatchSize < 0 || cfg.rollupEverySeconds < 0 || cfg.mergeEverySeconds < 0 || cfg.maintenanceSeconds < 0 || cfg.retentionDays < 0:
		return errors.New("fanout background-work settings cannot be negative")
	}
	if _, err := parseGuardrailExclusions(cfg.guardrailExclusions); err != nil {
		return fmt.Errorf("-guardrail-exclude: %w", err)
	}
	return nil
}
