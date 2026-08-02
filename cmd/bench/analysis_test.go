package main

import (
	"strings"
	"testing"
	"time"
)

const testPrimaryTarget = "server.write_gate_wait_ms.ingest_spans.p95_ms"

func TestAnalyzeSuitesRetainsOnlyMaterialTargetWithoutGuardrailRegression(t *testing.T) {
	baseline := benchmarkFixture("baseline", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "shorter-gate", []float64{80, 85, 90}, []float64{1.03, 1.04, 1.02}, nil)

	analysis := analyzeSuites(baseline, candidate, time.Date(2026, time.August, 1, 20, 0, 0, 0, time.UTC))
	if !analysis.Passed || analysis.Verdict != "retain" {
		t.Fatalf("analysis = %#v", analysis)
	}
	target := findMetricComparison(t, analysis, testPrimaryTarget)
	assertFloat(t, "target baseline median", target.BaselineMedian, 100)
	assertFloat(t, "target candidate median", target.CandidateMedian, 85)
	assertFloat(t, "target improvement", target.ImprovementPercent, 15)
	cpu := findMetricComparison(t, analysis, "server.cpu_cores")
	assertFloat(t, "CPU normalized improvement", cpu.ImprovementPercent, -3)
}

func TestAnalyzeSuitesScreeningModeAppliesFivePercentGuardrailWithoutTarget(t *testing.T) {
	baseline := benchmarkFixture("overhead-control", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("overhead-instrumented", "instrumented", []float64{96, 98, 100}, []float64{1.03, 1.04, 1.02}, nil)
	setScreening(&baseline)
	setScreening(&candidate)

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if !analysis.Passed || analysis.Mode != "screening" || analysis.Verdict != "pass" {
		t.Fatalf("analysis = %#v", analysis)
	}
	for _, metric := range analysis.Metrics {
		if metric.PrimaryTarget {
			t.Fatalf("screening metric was marked primary: %#v", metric)
		}
	}
}

func TestAnalyzeSuitesScreeningModeRejectsOverFivePercentRegression(t *testing.T) {
	baseline := benchmarkFixture("overhead-control", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("overhead-instrumented", "instrumented", []float64{96, 98, 100}, []float64{1.06, 1.07, 1.08}, nil)
	setScreening(&baseline)
	setScreening(&candidate)

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed || analysis.Verdict != "fail" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestAnalyzeSuitesScreeningRejectsDifferentFanoutSource(t *testing.T) {
	baseline := benchmarkFixture("overhead-control", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("overhead-instrumented", "instrumented", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	setScreening(&baseline)
	setScreening(&candidate)
	for index := range candidate.Reports {
		candidate.Reports[index].Manifest.Build.FanoutSourceSHA256 = strings.Repeat("b", 64)
		candidate.Suite.Trials[index].Manifest = &candidate.Reports[index].Manifest
	}

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed || !containsSubstring(analysis.ComparabilityFailures, "source digests differ") {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestContinuousMetricsTreatsEndpointHeapAsDiagnostic(t *testing.T) {
	report := report{Server: &serverReport{HeapAllocBytes: 123, RSSBytes: 456, AllocBytesPerSec: 789}}
	metrics := continuousMetrics(report)
	if _, ok := metrics["server.heap_alloc_bytes"]; ok {
		t.Fatal("GC-phase-dependent endpoint heap gauge must not be a guardrail")
	}
	if _, ok := metrics["server.rss_bytes"]; !ok {
		t.Fatal("RSS guardrail missing")
	}
	if _, ok := metrics["server.alloc_bytes_per_sec"]; !ok {
		t.Fatal("allocation-rate guardrail missing")
	}
}

func TestAnalyzeSuitesRejectsTargetBelowTenPercent(t *testing.T) {
	baseline := benchmarkFixture("baseline", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "small-win", []float64{93, 95, 97}, []float64{1, 1, 1}, nil)

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed || analysis.Verdict != "reject" {
		t.Fatalf("analysis = %#v", analysis)
	}
	target := findMetricComparison(t, analysis, testPrimaryTarget)
	if target.Passed || !strings.Contains(target.Reason, "below 10.00%") {
		t.Fatalf("target comparison = %#v", target)
	}
}

func TestAnalyzeSuitesRejectsGuardrailRegressionOverFivePercent(t *testing.T) {
	baseline := benchmarkFixture("baseline", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "cpu-regression", []float64{80, 85, 90}, []float64{1.06, 1.07, 1.08}, nil)

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed {
		t.Fatalf("analysis unexpectedly passed: %#v", analysis)
	}
	cpu := findMetricComparison(t, analysis, "server.cpu_cores")
	if cpu.Passed || !strings.Contains(cpu.Reason, "over 5.00%") {
		t.Fatalf("CPU comparison = %#v", cpu)
	}
}

func TestAnalyzeSuitesHonorsPredeclaredGuardrailExclusionWithRationale(t *testing.T) {
	exclusions := []guardrailExclusionManifest{{Metric: "server.cpu_cores", Rationale: "shared host screening only"}}
	baseline := benchmarkFixture("baseline", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "cpu-excluded", []float64{80, 85, 90}, []float64{1.10, 1.11, 1.12}, exclusions)

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if !analysis.Passed {
		t.Fatalf("analysis = %#v", analysis)
	}
	cpu := findMetricComparison(t, analysis, "server.cpu_cores")
	if !cpu.Excluded || !cpu.Passed || cpu.ExclusionRationale == "" {
		t.Fatalf("CPU comparison = %#v", cpu)
	}
}

func TestAnalyzeSuitesPreservesPerRunZeroToleranceFailure(t *testing.T) {
	baseline := benchmarkFixture("baseline", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "dropped-rows", []float64{80, 85, 90}, []float64{1, 1, 1}, nil)
	candidate.Suite.Passed = false
	candidate.Suite.Failures = []string{"measured run 2 failed"}
	candidate.Suite.Trials[1].Passed = false
	candidate.Suite.Trials[1].ZeroToleranceFailures = []string{"rows dropped=1"}
	candidate.Reports[1].Passed = false
	candidate.Reports[1].ZeroToleranceFailures = []string{"rows dropped=1"}

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed || len(analysis.RunFailures) != 1 {
		t.Fatalf("analysis = %#v", analysis)
	}
	failure := analysis.RunFailures[0]
	if failure.Group != "candidate" || failure.Ordinal != 2 || len(failure.ZeroToleranceFailures) != 1 || failure.ZeroToleranceFailures[0] != "rows dropped=1" {
		t.Fatalf("run failure = %#v", failure)
	}
}

func TestAnalyzeSuitesRejectsIncomparableWorkload(t *testing.T) {
	baseline := benchmarkFixture("baseline", "control", []float64{90, 100, 110}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "different-seed", []float64{80, 85, 90}, []float64{1, 1, 1}, nil)
	for i := range candidate.Reports {
		candidate.Reports[i].Manifest.Workload.Seed++
		candidate.Suite.Trials[i].Manifest = &candidate.Reports[i].Manifest
	}

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed || !containsSubstring(analysis.ComparabilityFailures, "workload manifests differ") {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestAnalyzeSuitesRejectsIncomparableServerConfiguration(t *testing.T) {
	baseline := benchmarkFixture("baseline", "control", []float64{100, 100, 100}, []float64{1, 1, 1}, nil)
	candidate := benchmarkFixture("candidate", "optimized", []float64{90, 90, 90}, []float64{1, 1, 1}, nil)
	for index := range candidate.Reports {
		candidate.Reports[index].Manifest.Server.RollupEverySeconds = 30
		candidate.Suite.Trials[index].Manifest = &candidate.Reports[index].Manifest
	}

	analysis := analyzeSuites(baseline, candidate, time.Time{})
	if analysis.Passed || !containsSubstring(analysis.ComparabilityFailures, "server configuration manifests differ") {
		t.Fatalf("comparability failures = %q", analysis.ComparabilityFailures)
	}
}

func TestNormalizedImprovementAccountsForDirection(t *testing.T) {
	if got, ok := normalizedImprovement(100, 115, directionHigher); !ok || got != 15 {
		t.Fatalf("higher-is-better improvement = %v, %v", got, ok)
	}
	if got, ok := normalizedImprovement(100, 85, directionLower); !ok || got != 15 {
		t.Fatalf("lower-is-better improvement = %v, %v", got, ok)
	}
	if _, ok := normalizedImprovement(0, 1, directionLower); ok {
		t.Fatal("nonzero candidate over zero baseline must be non-comparable")
	}
}

func benchmarkFixture(stage, candidate string, targetValues, cpuValues []float64, exclusions []guardrailExclusionManifest) suiteBundle {
	host := hostManifest{OS: "linux", Architecture: "amd64", LogicalCPUs: 8, GOMAXPROCS: 8, MemoryLimitBytes: 8 << 30}
	workload := workloadManifest{
		Seed: 42, DurationSec: 1800, WarmupSec: 300, TargetRate: 1000, Workers: 8,
		Services: 20, Namespaces: 1, Cardinality: 100, ErrorRate: 0.05, MessagingRatio: 0.2,
		SendLogs: true, SendMetrics: true, QueryWorkers: 4, TargetQueryRate: 20,
		QueryOperations: append([]string(nil), queryOperations...), QueryWindows: append([]string(nil), queryWindows...), QueryLimit: 100,
	}
	dataset := datasetManifest{ID: "fixture-dataset", ConfigHash: "fixture-hash"}
	duckDB := duckDBManifest{MaxConnections: 8, Memory: "6GB", Threads: 4}
	build := buildManifest{FanoutVersion: candidate, BenchVersion: "bench-v1", GoVersion: "go1.25", GitCommit: candidate + "-commit", GitMetadataAvailable: true}
	suite := suiteEvidence{
		SchemaVersion:       suiteSchemaVersion,
		Stage:               stage,
		Candidate:           candidate,
		PrimaryTarget:       testPrimaryTarget,
		GuardrailExclusions: append([]guardrailExclusionManifest(nil), exclusions...),
		Passed:              true,
	}
	warmupWorkload := workload
	warmupWorkload.DurationSec = 300
	warmupWorkload.WarmupSec = 0
	warmupManifest := runManifest{
		SchemaVersion: manifestSchemaVersion, Stage: stage + "-warmup", Candidate: candidate, PrimaryTarget: testPrimaryTarget,
		GuardrailExclusions: append([]guardrailExclusionManifest(nil), exclusions...),
		Build:               build, Host: host, Workload: warmupWorkload, Dataset: dataset, DuckDB: duckDB,
	}
	suite.Warmup = &suiteRunEvidence{Kind: "warmup", ReportFile: "warmup.json", Passed: true, Manifest: &warmupManifest}
	bundle := suiteBundle{Path: stage + "-suite.json", Suite: suite, WarmupReport: &report{Manifest: warmupManifest, Passed: true, DurationSec: 300}}
	for i := 0; i < 3; i++ {
		manifest := runManifest{
			SchemaVersion:       manifestSchemaVersion,
			Stage:               stage,
			Candidate:           candidate,
			PrimaryTarget:       testPrimaryTarget,
			GuardrailExclusions: append([]guardrailExclusionManifest(nil), exclusions...),
			RunOrdinal:          i + 1,
			Build:               build,
			Host:                host,
			Workload:            workload,
			Dataset:             dataset,
			DuckDB:              duckDB,
		}
		queryLatency := latencyReport{Count: 100, MeanMs: 20, P50Ms: 20, P95Ms: 30, P99Ms: 40}
		report := report{
			Manifest:        manifest,
			Passed:          true,
			DurationSec:     1800,
			AvgTracesPerSec: 1000,
			ExportLatencyMs: latencyReport{Count: 100, MeanMs: 10, P50Ms: 10, P95Ms: 20, P99Ms: 30},
			QueryLatencyMs:  &queryLatency,
			Server: &serverReport{
				BaselineAvailable: true,
				LakePartitions:    10, LakePartitionsDelta: 1,
				LakeSizeBytes: 1000, LakeSizeBytesDelta: 100, LakeGrowthBytesPerSec: 10,
				IngestQueueDepth: 1, AvgRollupMs: 10, AvgFlushMs: 10, AvgQueryMs: 10,
				CPUCores: cpuValues[i], RSSBytes: 1000, HeapAllocBytes: 500, AllocBytesPerSec: 100, GCPauseSecondsDelta: 0.1,
				WriteGateWaitMs: map[string]distributionReport{
					"ingest_spans": {Count: 100, MeanMs: targetValues[i] / 2, P50Ms: targetValues[i] * 0.7, P95Ms: targetValues[i], P99Ms: targetValues[i] * 1.1},
				},
			},
		}
		bundle.Reports = append(bundle.Reports, report)
		manifestCopy := manifest
		bundle.Suite.Trials = append(bundle.Suite.Trials, suiteRunEvidence{
			Kind: "measured", Ordinal: i + 1, ReportFile: "run.json", Passed: true, Manifest: &manifestCopy,
		})
	}
	return bundle
}

func setScreening(bundle *suiteBundle) {
	const fixtureSourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bundle.Suite.Screening = true
	bundle.Suite.PrimaryTarget = ""
	bundle.WarmupReport.Manifest.Screening = true
	bundle.WarmupReport.Manifest.PrimaryTarget = ""
	bundle.WarmupReport.Manifest.Build.FanoutSourceSHA256 = fixtureSourceSHA
	bundle.WarmupReport.Manifest.Server.DataPlaneInstrumentation = screeningMode(bundle.Suite.Candidate)
	bundle.Suite.Warmup.Manifest = &bundle.WarmupReport.Manifest
	for i := range bundle.Reports {
		bundle.Reports[i].Manifest.Screening = true
		bundle.Reports[i].Manifest.PrimaryTarget = ""
		bundle.Reports[i].Manifest.Build.FanoutSourceSHA256 = fixtureSourceSHA
		bundle.Reports[i].Manifest.Server.DataPlaneInstrumentation = screeningMode(bundle.Suite.Candidate)
		bundle.Suite.Trials[i].Manifest = &bundle.Reports[i].Manifest
	}
}

func screeningMode(candidate string) string {
	if candidate == "control" {
		return "disabled"
	}
	return "enabled"
}

func findMetricComparison(t *testing.T, analysis analysisEvidence, path string) metricComparison {
	t.Helper()
	for _, metric := range analysis.Metrics {
		if metric.Metric == path {
			return metric
		}
	}
	t.Fatalf("metric %q not found in %#v", path, analysis.Metrics)
	return metricComparison{}
}

func containsSubstring(items []string, substring string) bool {
	for _, item := range items {
		if strings.Contains(item, substring) {
			return true
		}
	}
	return false
}
