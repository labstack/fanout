package main

import (
	"errors"
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
	}
	return nil
}
