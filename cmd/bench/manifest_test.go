package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRunManifestIsDeterministicAndSecretFree(t *testing.T) {
	startedAt := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.FixedZone("test", -7*60*60))
	finishedAt := startedAt.Add(30 * time.Minute)
	cfg := config{
		endpoint:      "secret-ingest-host:4317",
		token:         "ingest-secret-sentinel",
		metricsURL:    "https://metrics-secret-host/-/metrics",
		metricsToken:  "metrics-secret-sentinel",
		queryURL:      "https://query-secret-host",
		rate:          2500,
		duration:      30 * time.Minute,
		workers:       12,
		services:      50,
		namespaces:    3,
		cardinality:   200,
		errorRate:     0.05,
		msgRatio:      0.2,
		sendLogs:      true,
		sendMetrics:   true,
		backfillHours: 6,
		queryWorkers:  4,
		queryRate:     20,
		seed:          42,
		fanoutVersion: "fanout-v1",
	}

	first := newRunManifest(cfg, startedAt, finishedAt)
	if second := newRunManifest(cfg, startedAt, finishedAt); !reflect.DeepEqual(first, second) {
		t.Fatalf("same inputs produced different manifests:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if len(first.WorkloadHash) != 64 {
		t.Fatalf("workload hash = %q, want 64 hex characters", first.WorkloadHash)
	}
	if first.StartedAt.Location() != time.UTC || first.FinishedAt.Location() != time.UTC {
		t.Fatalf("manifest times are not UTC: %v %v", first.StartedAt, first.FinishedAt)
	}

	// The manifest travels with shared evidence, so it must never carry the
	// endpoints or credentials the run was pointed at.
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cfg.endpoint, cfg.token, cfg.metricsURL, cfg.metricsToken, cfg.queryURL} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("manifest leaked %q: %s", secret, encoded)
		}
	}

	changed := cfg
	changed.seed++
	if got := newRunManifest(changed, startedAt, finishedAt).WorkloadHash; got == first.WorkloadHash {
		t.Fatalf("workload hash did not change with seed: %s", got)
	}
}

func TestWriteReportProducesDeterministicJSON(t *testing.T) {
	report := report{
		Manifest: runManifest{
			StartedAt:  time.Date(2026, time.August, 1, 19, 0, 0, 0, time.UTC),
			FinishedAt: time.Date(2026, time.August, 1, 19, 30, 0, 0, time.UTC),
		},
		Endpoint:        "localhost:4317",
		DurationSec:     1800,
		TargetRate:      1000,
		TracesSent:      1,
		AvgTracesPerSec: 1000,
		Server: &serverReport{
			WriteGateWaitMs: map[string]distributionReport{
				"rollup_service": {Count: 2, P95Ms: 50},
				"ingest_spans":   {Count: 5, P95Ms: 10},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "run.json")
	if err := writeReport(path, report); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Trailing newline matters: the report is appended to evidence logs and read
	// back line-wise by the harness scripts.
	if !json.Valid(written) || len(written) == 0 || written[len(written)-1] != '\n' {
		t.Fatalf("report is not valid newline-terminated JSON: %q", written)
	}
}
