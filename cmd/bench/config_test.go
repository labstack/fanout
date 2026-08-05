package main

import (
	"strings"
	"testing"
	"time"
)

func TestValidateTrialConfigRejectsUnusableInputs(t *testing.T) {
	valid := config{
		rate: 1000, workers: 8, services: 20, namespaces: 1, cardinality: 100,
		errorRate: 0.05, msgRatio: 0.2,
	}
	tests := []struct {
		name   string
		mutate func(*config)
	}{
		{name: "zero rate", mutate: func(cfg *config) { cfg.rate = 0 }},
		{name: "zero workers", mutate: func(cfg *config) { cfg.workers = 0 }},
		{name: "one service", mutate: func(cfg *config) { cfg.services = 1 }},
		{name: "invalid error rate", mutate: func(cfg *config) { cfg.errorRate = 1.1 }},
		{name: "invalid messaging ratio", mutate: func(cfg *config) { cfg.msgRatio = -0.1 }},
		{name: "query workers without URL", mutate: func(cfg *config) { cfg.queryWorkers = 1; cfg.queryRate = 10 }},
		{name: "query workers without rate", mutate: func(cfg *config) { cfg.queryWorkers = 1; cfg.queryURL = "http://x" }},
		{name: "negative backfill", mutate: func(cfg *config) { cfg.backfillHours = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := validateTrialConfig(cfg); err == nil {
				t.Fatalf("config unexpectedly valid: %#v", cfg)
			}
		})
	}
	if err := validateTrialConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateConfigRejectsNonPositiveAdaptiveStep(t *testing.T) {
	cfg := config{
		workers: 8, services: 20, namespaces: 1, cardinality: 100,
		errorRate: 0.05, msgRatio: 0.2, stepDuration: 0,
	}

	err := validateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "-step must be positive") {
		t.Fatalf("validateConfig error = %v, want a positive-step error", err)
	}
}

func TestValidateConfigAllowsPositiveAdaptiveStep(t *testing.T) {
	cfg := config{
		workers: 8, services: 20, namespaces: 1, cardinality: 100,
		errorRate: 0.05, msgRatio: 0.2, stepDuration: time.Second,
	}

	if err := validateConfig(cfg); err != nil {
		t.Fatalf("valid adaptive config rejected: %v", err)
	}
}

func TestValidateConfigDoesNotApplyStepToFixedRateRuns(t *testing.T) {
	cfg := config{
		rate: 1000, workers: 8, services: 20, namespaces: 1, cardinality: 100,
		errorRate: 0.05, msgRatio: 0.2, stepDuration: 0,
	}

	if err := validateConfig(cfg); err != nil {
		t.Fatalf("fixed-rate config rejected because of unused -step: %v", err)
	}
}
