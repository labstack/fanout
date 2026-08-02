package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunManifestIsDeterministicAndSecretFree(t *testing.T) {
	startedAt := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.FixedZone("test", -7*60*60))
	finishedAt := startedAt.Add(30 * time.Minute)
	cfg := config{
		endpoint:            "secret-ingest-host:4317",
		token:               "ingest-secret-sentinel",
		metricsURL:          "https://metrics-secret-host/-/metrics",
		metricsToken:        "metrics-secret-sentinel",
		queryURL:            "https://query-secret-host",
		rate:                2500,
		duration:            30 * time.Minute,
		warmupDuration:      5 * time.Minute,
		workers:             12,
		services:            50,
		namespaces:          3,
		cardinality:         200,
		errorRate:           0.05,
		msgRatio:            0.2,
		sendLogs:            true,
		sendMetrics:         true,
		backfillHours:       6,
		queryWorkers:        4,
		queryRate:           20,
		seed:                42,
		stage:               "baseline",
		candidate:           "control",
		primaryTarget:       "write_gate_wait_ms.ingest_spans.p95_ms",
		guardrailExclusions: " rss_bytes=host noise, cpu_cores=shared host, rss_bytes=host noise ",
		runOrdinal:          2,
		fanoutVersion:       "fanout-v1",
		memoryLimitBytes:    8 << 30,
		duckDBMaxConns:      8,
		duckDBMemory:        "6GB",
		duckDBThreads:       4,
		flushSeconds:        15,
		flushBatchSize:      50000,
		rollupEverySeconds:  60,
		mergeEverySeconds:   60,
		maintenanceSeconds:  3600,
		retentionDays:       30,
	}

	first := newRunManifest(cfg, startedAt, finishedAt)
	second := newRunManifest(cfg, startedAt, finishedAt)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same inputs produced different manifests:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if got, want := first.GuardrailExclusions, []guardrailExclusionManifest{
		{Metric: "cpu_cores", Rationale: "shared host"},
		{Metric: "rss_bytes", Rationale: "host noise"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("guardrail exclusions = %#v, want %#v", got, want)
	}
	if !strings.HasPrefix(first.Dataset.ID, "synthetic:") || len(first.Dataset.ConfigHash) != 64 {
		t.Fatalf("dataset identity = %#v", first.Dataset)
	}
	if first.StartedAt.Location() != time.UTC || first.FinishedAt.Location() != time.UTC {
		t.Fatalf("manifest times are not UTC: %v %v", first.StartedAt, first.FinishedAt)
	}
	if first.Server.RollupEverySeconds != 60 || first.Server.MergeEverySeconds != 60 || first.Server.FlushSeconds != 15 {
		t.Fatalf("server configuration manifest = %#v", first.Server)
	}

	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		cfg.endpoint,
		cfg.token,
		cfg.metricsURL,
		cfg.metricsToken,
		cfg.queryURL,
	} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("manifest leaked secret/configured endpoint %q: %s", secret, encoded)
		}
	}

	changed := cfg
	changed.seed++
	if got := newRunManifest(changed, startedAt, finishedAt).Dataset.ConfigHash; got == first.Dataset.ConfigHash {
		t.Fatalf("dataset hash did not change with seed: %s", got)
	}
}

func TestGuardrailExclusionsRequireRationales(t *testing.T) {
	if _, err := parseGuardrailExclusions("server.cpu_cores"); err == nil {
		t.Fatal("expected exclusion without rationale to fail")
	}
	if _, err := parseGuardrailExclusions("server.cpu_cores=host noise,server.cpu_cores=different"); err == nil {
		t.Fatal("expected conflicting exclusion rationales to fail")
	}
}

func TestWriteReportProducesDeterministicJSON(t *testing.T) {
	manifest := runManifest{
		SchemaVersion: 1,
		Stage:         "baseline",
		Candidate:     "control",
		StartedAt:     time.Date(2026, time.August, 1, 19, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, time.August, 1, 19, 30, 0, 0, time.UTC),
	}
	report := report{
		Manifest:        manifest,
		Endpoint:        "localhost:4317",
		DurationSec:     1800,
		TargetRate:      1000,
		Workers:         8,
		Services:        20,
		TracesSent:      1,
		AvgTracesPerSec: 1000,
		Server: &serverReport{
			WriteGateWaitMs: map[string]distributionReport{
				"rollup_service": {Count: 2, P95Ms: 50},
				"ingest_spans":   {Count: 5, P95Ms: 10},
			},
		},
	}
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.json")
	secondPath := filepath.Join(directory, "second.json")
	if err := writeReport(firstPath, report); err != nil {
		t.Fatal(err)
	}
	if err := writeReport(secondPath, report); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("report JSON is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !json.Valid(first) || len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatalf("report is not valid newline-terminated JSON: %q", first)
	}
}
