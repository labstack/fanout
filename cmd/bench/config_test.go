package main

import "testing"

func TestValidateTrialConfigRejectsNonReproducibleInputs(t *testing.T) {
	valid := config{
		rate: 1000, workers: 8, services: 20, namespaces: 1, cardinality: 100,
		errorRate: 0.05, msgRatio: 0.2, stage: "baseline", candidate: "control",
	}
	tests := []struct {
		name   string
		mutate func(*config)
	}{
		{name: "zero rate", mutate: func(cfg *config) { cfg.rate = 0 }},
		{name: "zero workers", mutate: func(cfg *config) { cfg.workers = 0 }},
		{name: "invalid error rate", mutate: func(cfg *config) { cfg.errorRate = 1.1 }},
		{name: "query workers without URL", mutate: func(cfg *config) { cfg.queryWorkers = 1; cfg.queryRate = 10 }},
		{name: "candidate without target", mutate: func(cfg *config) { cfg.candidate = "candidate" }},
		{name: "exclusion without rationale", mutate: func(cfg *config) { cfg.guardrailExclusions = "server.cpu_cores" }},
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
	screening := valid
	screening.candidate = "instrumented"
	screening.screening = true
	if err := validateTrialConfig(screening); err != nil {
		t.Fatalf("valid screening config rejected: %v", err)
	}
}
