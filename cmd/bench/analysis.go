package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	analysisSchemaVersion         = 2
	targetImprovementRequiredPct  = 10.0
	guardrailRegressionAllowedPct = 5.0
)

type metricDirection string

const (
	directionHigher metricDirection = "higher_is_better"
	directionLower  metricDirection = "lower_is_better"
)

type observedMetric struct {
	Direction metricDirection
	Value     float64
}

type metricComparison struct {
	Metric             string          `json:"metric"`
	Direction          metricDirection `json:"direction"`
	BaselineMedian     float64         `json:"baseline_median"`
	CandidateMedian    float64         `json:"candidate_median"`
	ImprovementPercent float64         `json:"improvement_percent"`
	PrimaryTarget      bool            `json:"primary_target"`
	Excluded           bool            `json:"excluded"`
	ExclusionRationale string          `json:"exclusion_rationale,omitempty"`
	Passed             bool            `json:"passed"`
	Reason             string          `json:"reason,omitempty"`
}

type runFailureEvidence struct {
	Group                 string   `json:"group"`
	Kind                  string   `json:"kind"`
	Ordinal               int      `json:"ordinal,omitempty"`
	ZeroToleranceFailures []string `json:"zero_tolerance_failures,omitempty"`
	ThresholdFailures     []string `json:"threshold_failures,omitempty"`
}

type analysisEvidence struct {
	SchemaVersion             int                  `json:"schema_version"`
	GeneratedAt               time.Time            `json:"generated_at"`
	Mode                      string               `json:"mode"`
	BaselineSuite             string               `json:"baseline_suite"`
	CandidateSuite            string               `json:"candidate_suite"`
	PrimaryTarget             string               `json:"primary_target"`
	TargetImprovementRequired float64              `json:"target_improvement_required_percent"`
	GuardrailRegressionLimit  float64              `json:"guardrail_regression_limit_percent"`
	ComparabilityFailures     []string             `json:"comparability_failures,omitempty"`
	RunFailures               []runFailureEvidence `json:"run_failures,omitempty"`
	Metrics                   []metricComparison   `json:"metrics"`
	Verdict                   string               `json:"verdict"`
	Passed                    bool                 `json:"passed"`
}

type suiteBundle struct {
	Path         string
	Suite        suiteEvidence
	WarmupReport *report
	Reports      []report
}

func analyzeCommand(args []string) error {
	flags := flag.NewFlagSet("bench analyze", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var baselinePath, candidatePath, outputPath string
	flags.StringVar(&baselinePath, "baseline", "", "baseline suite.json (required)")
	flags.StringVar(&candidatePath, "candidate", "", "candidate suite.json (required)")
	flags.StringVar(&outputPath, "output", "", "analysis JSON path (required; must not already exist)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if baselinePath == "" || candidatePath == "" || outputPath == "" {
		return errors.New("-baseline, -candidate, and -output are required")
	}
	if _, err := os.Stat(outputPath); err == nil {
		return fmt.Errorf("output already exists: %s", outputPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	baseline, err := loadSuiteBundle(baselinePath)
	if err != nil {
		return fmt.Errorf("load baseline: %w", err)
	}
	candidate, err := loadSuiteBundle(candidatePath)
	if err != nil {
		return fmt.Errorf("load candidate: %w", err)
	}
	analysis := analyzeSuites(baseline, candidate, time.Now().UTC())
	bytes, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, append(bytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write analysis: %w", err)
	}
	if !analysis.Passed {
		return fmt.Errorf("verdict %s; evidence written to %s", analysis.Verdict, outputPath)
	}
	fmt.Printf("analysis %s: %s\n", strings.ToUpper(analysis.Verdict), outputPath)
	return nil
}

func loadSuiteBundle(path string) (suiteBundle, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return suiteBundle{}, err
	}
	bundle := suiteBundle{Path: path}
	if err := json.Unmarshal(bytes, &bundle.Suite); err != nil {
		return suiteBundle{}, err
	}
	directory := filepath.Dir(path)
	if bundle.Suite.Warmup != nil {
		warmup, err := readSuiteReport(directory, bundle.Suite.Warmup.ReportFile)
		if err != nil {
			return suiteBundle{}, fmt.Errorf("read warm-up: %w", err)
		}
		bundle.WarmupReport = &warmup
	}
	for _, trial := range bundle.Suite.Trials {
		report, err := readSuiteReport(directory, trial.ReportFile)
		if err != nil {
			return suiteBundle{}, err
		}
		bundle.Reports = append(bundle.Reports, report)
	}
	return bundle, nil
}

func readSuiteReport(directory, name string) (report, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return report{}, fmt.Errorf("unsafe report path %q", name)
	}
	reportBytes, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		return report{}, fmt.Errorf("read %s: %w", name, err)
	}
	var decoded report
	if err := json.Unmarshal(reportBytes, &decoded); err != nil {
		return report{}, fmt.Errorf("decode %s: %w", name, err)
	}
	return decoded, nil
}

func analyzeSuites(baseline, candidate suiteBundle, generatedAt time.Time) analysisEvidence {
	mode := "candidate"
	verdict := "reject"
	if candidate.Suite.Screening {
		mode = "screening"
		verdict = "fail"
	}
	analysis := analysisEvidence{
		SchemaVersion:             analysisSchemaVersion,
		GeneratedAt:               generatedAt.UTC(),
		Mode:                      mode,
		BaselineSuite:             filepath.Base(baseline.Path),
		CandidateSuite:            filepath.Base(candidate.Path),
		PrimaryTarget:             candidate.Suite.PrimaryTarget,
		TargetImprovementRequired: targetImprovementRequiredPct,
		GuardrailRegressionLimit:  guardrailRegressionAllowedPct,
		Verdict:                   verdict,
	}
	analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, validateBundle("baseline", baseline)...)
	analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, validateBundle("candidate", candidate)...)
	analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, compareBundles(baseline, candidate)...)
	analysis.RunFailures = append(analysis.RunFailures, collectRunFailures("baseline", baseline.Suite)...)
	analysis.RunFailures = append(analysis.RunFailures, collectRunFailures("candidate", candidate.Suite)...)

	exclusions := make(map[string]string, len(candidate.Suite.GuardrailExclusions))
	for _, exclusion := range candidate.Suite.GuardrailExclusions {
		exclusions[exclusion.Metric] = exclusion.Rationale
	}
	if !candidate.Suite.Screening && candidate.Suite.PrimaryTarget == "" {
		analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, "candidate primary target is empty")
	}
	if !candidate.Suite.Screening {
		if _, excluded := exclusions[candidate.Suite.PrimaryTarget]; excluded {
			analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, "primary target cannot be excluded from guardrails")
		}
	}

	baselineMetrics := metricRuns(baseline.Reports)
	candidateMetrics := metricRuns(candidate.Reports)
	paths := make([]string, 0, len(baselineMetrics))
	for path := range baselineMetrics {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	knownExclusions := make(map[string]bool, len(exclusions))
	targetPassed := candidate.Suite.Screening
	guardrailsPassed := true
	for _, path := range paths {
		baseValues := baselineMetrics[path]
		candidateValues := candidateMetrics[path]
		direction := directionForRuns(baseValues)
		comparison := metricComparison{
			Metric:        path,
			Direction:     direction,
			PrimaryTarget: !candidate.Suite.Screening && path == candidate.Suite.PrimaryTarget,
		}
		if rationale, excluded := exclusions[path]; excluded {
			comparison.Excluded = true
			comparison.ExclusionRationale = rationale
			knownExclusions[path] = true
		}
		if len(baseValues) != 3 || len(candidateValues) != 3 || direction == "" || !directionsMatch(baseValues, candidateValues) {
			comparison.Reason = fmt.Sprintf("requires three values with one direction (baseline=%d candidate=%d)", len(baseValues), len(candidateValues))
			comparison.Passed = comparison.Excluded
			if !comparison.Passed {
				analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, path+": "+comparison.Reason)
				guardrailsPassed = false
			}
			analysis.Metrics = append(analysis.Metrics, comparison)
			continue
		}
		comparison.BaselineMedian = medianObservations(baseValues)
		comparison.CandidateMedian = medianObservations(candidateValues)
		improvement, comparable := normalizedImprovement(comparison.BaselineMedian, comparison.CandidateMedian, direction)
		if !comparable {
			comparison.Reason = "relative change is undefined because the baseline median is zero"
			comparison.Passed = comparison.Excluded
			if !comparison.Passed {
				analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, path+": "+comparison.Reason)
				guardrailsPassed = false
			}
			analysis.Metrics = append(analysis.Metrics, comparison)
			continue
		}
		comparison.ImprovementPercent = round4(improvement)
		switch {
		case comparison.PrimaryTarget:
			comparison.Passed = improvement >= targetImprovementRequiredPct
			if !comparison.Passed {
				comparison.Reason = fmt.Sprintf("target improvement %.2f%% is below %.2f%%", improvement, targetImprovementRequiredPct)
			}
			targetPassed = comparison.Passed
		case comparison.Excluded:
			comparison.Passed = true
			comparison.Reason = "excluded before execution with rationale"
		default:
			comparison.Passed = improvement >= -guardrailRegressionAllowedPct
			if !comparison.Passed {
				comparison.Reason = fmt.Sprintf("guardrail regressed %.2f%%, over %.2f%%", -improvement, guardrailRegressionAllowedPct)
				guardrailsPassed = false
			}
		}
		analysis.Metrics = append(analysis.Metrics, comparison)
	}
	for metric := range exclusions {
		if !knownExclusions[metric] {
			analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, "guardrail exclusion does not name a baseline metric: "+metric)
		}
	}
	if _, ok := baselineMetrics[candidate.Suite.PrimaryTarget]; !candidate.Suite.Screening && !ok && candidate.Suite.PrimaryTarget != "" {
		analysis.ComparabilityFailures = append(analysis.ComparabilityFailures, "primary target is not a baseline metric: "+candidate.Suite.PrimaryTarget)
	}
	analysis.ComparabilityFailures = sortedUnique(analysis.ComparabilityFailures)
	analysis.Passed = len(analysis.ComparabilityFailures) == 0 && len(analysis.RunFailures) == 0 && targetPassed && guardrailsPassed
	if analysis.Passed {
		if candidate.Suite.Screening {
			analysis.Verdict = "pass"
		} else {
			analysis.Verdict = "retain"
		}
	}
	return analysis
}

func validateBundle(group string, bundle suiteBundle) []string {
	var failures []string
	if bundle.Suite.SchemaVersion != suiteSchemaVersion {
		failures = append(failures, fmt.Sprintf("%s suite schema=%d, want %d", group, bundle.Suite.SchemaVersion, suiteSchemaVersion))
	}
	if len(bundle.Suite.Trials) != 3 || len(bundle.Reports) != 3 {
		failures = append(failures, fmt.Sprintf("%s must contain three measured reports", group))
	}
	if bundle.Suite.Warmup == nil || bundle.WarmupReport == nil {
		failures = append(failures, fmt.Sprintf("%s suite is missing separate warm-up evidence", group))
	} else if bundle.Suite.Warmup.Kind != "warmup" || bundle.Suite.Warmup.Manifest == nil {
		failures = append(failures, fmt.Sprintf("%s warm-up evidence is incomplete", group))
	} else {
		warmup := bundle.Suite.Warmup
		report := bundle.WarmupReport
		if !reflect.DeepEqual(*warmup.Manifest, report.Manifest) || warmup.Passed != report.Passed ||
			!reflect.DeepEqual(warmup.ZeroToleranceFailures, report.ZeroToleranceFailures) ||
			!reflect.DeepEqual(warmup.ThresholdFailures, report.ThresholdFailures) {
			failures = append(failures, fmt.Sprintf("%s warm-up suite/report evidence differs", group))
		}
		if report.Manifest.Stage != bundle.Suite.Stage+"-warmup" || report.Manifest.Candidate != bundle.Suite.Candidate {
			failures = append(failures, fmt.Sprintf("%s warm-up stage/candidate differs from suite", group))
		}
		if report.Manifest.Screening != bundle.Suite.Screening {
			failures = append(failures, fmt.Sprintf("%s warm-up screening declaration differs from suite", group))
		}
		if report.Manifest.PrimaryTarget != bundle.Suite.PrimaryTarget || !reflect.DeepEqual(report.Manifest.GuardrailExclusions, bundle.Suite.GuardrailExclusions) {
			failures = append(failures, fmt.Sprintf("%s warm-up declaration differs from suite", group))
		}
		if report.Manifest.Workload.DurationSec <= 0 || report.Manifest.Workload.WarmupSec != 0 {
			failures = append(failures, fmt.Sprintf("%s warm-up duration metadata is invalid", group))
		}
		if !durationMatches(report.DurationSec, report.Manifest.Workload.DurationSec) {
			failures = append(failures, fmt.Sprintf("%s warm-up ended outside its declared duration", group))
		}
	}
	if !bundle.Suite.Passed {
		failures = append(failures, fmt.Sprintf("%s suite did not pass: %s", group, strings.Join(bundle.Suite.Failures, "; ")))
	}
	for i := 0; i < len(bundle.Reports) && i < len(bundle.Suite.Trials); i++ {
		trial := bundle.Suite.Trials[i]
		report := bundle.Reports[i]
		ordinal := i + 1
		if trial.Kind != "measured" || trial.Ordinal != ordinal || report.Manifest.RunOrdinal != ordinal {
			failures = append(failures, fmt.Sprintf("%s run %d has inconsistent ordinal/kind", group, ordinal))
		}
		if trial.Manifest != nil && !reflect.DeepEqual(*trial.Manifest, report.Manifest) {
			failures = append(failures, fmt.Sprintf("%s run %d suite/report manifests differ", group, ordinal))
		}
		if trial.Passed != report.Passed ||
			!reflect.DeepEqual(trial.ZeroToleranceFailures, report.ZeroToleranceFailures) ||
			!reflect.DeepEqual(trial.ThresholdFailures, report.ThresholdFailures) {
			failures = append(failures, fmt.Sprintf("%s run %d suite/report verdicts differ", group, ordinal))
		}
		if report.Manifest.Stage != bundle.Suite.Stage || report.Manifest.Candidate != bundle.Suite.Candidate {
			failures = append(failures, fmt.Sprintf("%s run %d stage/candidate differs from suite", group, ordinal))
		}
		if report.Manifest.Screening != bundle.Suite.Screening {
			failures = append(failures, fmt.Sprintf("%s run %d screening declaration differs from suite", group, ordinal))
		}
		if report.Manifest.Workload.DurationSec <= 0 || report.Manifest.Workload.WarmupSec <= 0 {
			failures = append(failures, fmt.Sprintf("%s run %d did not record separate warm-up and measurement durations", group, ordinal))
		}
		if !durationMatches(report.DurationSec, report.Manifest.Workload.DurationSec) {
			failures = append(failures, fmt.Sprintf("%s run %d ended outside its declared duration", group, ordinal))
		}
		if report.Manifest.PrimaryTarget != bundle.Suite.PrimaryTarget || !reflect.DeepEqual(report.Manifest.GuardrailExclusions, bundle.Suite.GuardrailExclusions) {
			failures = append(failures, fmt.Sprintf("%s run %d declaration differs from suite", group, ordinal))
		}
		if i > 0 {
			failures = append(failures, compareRunManifests(group, bundle.Reports[0].Manifest, report.Manifest)...)
			if !reflect.DeepEqual(bundle.Reports[0].Manifest.Build, report.Manifest.Build) {
				failures = append(failures, fmt.Sprintf("%s build manifests differ between runs", group))
			}
		}
	}
	return failures
}

func compareBundles(baseline, candidate suiteBundle) []string {
	if len(baseline.Reports) == 0 || len(candidate.Reports) == 0 {
		return nil
	}
	leftManifest := baseline.Reports[0].Manifest
	rightManifest := candidate.Reports[0].Manifest
	if candidate.Suite.Screening {
		// Instrumentation mode is the one intentionally changed screening variable.
		leftManifest.Server.DataPlaneInstrumentation = ""
		rightManifest.Server.DataPlaneInstrumentation = ""
	}
	failures := compareRunManifests("baseline/candidate", leftManifest, rightManifest)
	if baseline.Suite.Screening != candidate.Suite.Screening {
		failures = append(failures, "baseline/candidate analysis modes differ")
	}
	leftBuild := baseline.Reports[0].Manifest.Build
	rightBuild := candidate.Reports[0].Manifest.Build
	if leftBuild.BenchVersion != rightBuild.BenchVersion || leftBuild.GoVersion != rightBuild.GoVersion {
		failures = append(failures, "baseline/candidate benchmark driver build differs")
	}
	if candidate.Suite.Screening {
		leftMode := baseline.Reports[0].Manifest.Server.DataPlaneInstrumentation
		rightMode := candidate.Reports[0].Manifest.Server.DataPlaneInstrumentation
		if leftMode != "disabled" || rightMode != "enabled" {
			failures = append(failures, "screening requires disabled control and enabled candidate instrumentation")
		}
		if leftBuild.FanoutSourceSHA256 == "" || leftBuild.FanoutSourceSHA256 != rightBuild.FanoutSourceSHA256 {
			failures = append(failures, "screening Fanout source digests differ or are missing")
		}
	}
	if baseline.WarmupReport != nil && candidate.WarmupReport != nil {
		leftWarmup := baseline.WarmupReport.Manifest
		rightWarmup := candidate.WarmupReport.Manifest
		if candidate.Suite.Screening {
			leftWarmup.Server.DataPlaneInstrumentation = ""
			rightWarmup.Server.DataPlaneInstrumentation = ""
		}
		failures = append(failures, compareRunManifests("baseline/candidate warm-up", leftWarmup, rightWarmup)...)
	}
	return failures
}

func durationMatches(actual, declared float64) bool {
	if actual <= 0 || declared <= 0 {
		return false
	}
	tolerance := math.Max(1, declared*0.01)
	return math.Abs(actual-declared) <= tolerance
}

func compareRunManifests(scope string, left, right runManifest) []string {
	var failures []string
	if !reflect.DeepEqual(left.Host, right.Host) {
		failures = append(failures, scope+" host manifests differ")
	}
	if !reflect.DeepEqual(left.Workload, right.Workload) {
		failures = append(failures, scope+" workload manifests differ")
	}
	if !reflect.DeepEqual(left.Dataset, right.Dataset) {
		failures = append(failures, scope+" dataset manifests differ")
	}
	if !reflect.DeepEqual(left.DuckDB, right.DuckDB) {
		failures = append(failures, scope+" DuckDB manifests differ")
	}
	if !reflect.DeepEqual(left.Server, right.Server) {
		failures = append(failures, scope+" server configuration manifests differ")
	}
	return failures
}

func collectRunFailures(group string, suite suiteEvidence) []runFailureEvidence {
	var failures []runFailureEvidence
	appendRun := func(run suiteRunEvidence) {
		if run.Passed && run.ExitCode == 0 && len(run.ZeroToleranceFailures) == 0 && len(run.ThresholdFailures) == 0 {
			return
		}
		zero := append([]string(nil), run.ZeroToleranceFailures...)
		if run.ExitCode != 0 && len(zero) == 0 {
			zero = append(zero, fmt.Sprintf("benchmark process exited with code %d", run.ExitCode))
		}
		failures = append(failures, runFailureEvidence{
			Group:                 group,
			Kind:                  run.Kind,
			Ordinal:               run.Ordinal,
			ZeroToleranceFailures: zero,
			ThresholdFailures:     append([]string(nil), run.ThresholdFailures...),
		})
	}
	if suite.Warmup != nil {
		appendRun(*suite.Warmup)
	}
	for _, trial := range suite.Trials {
		appendRun(trial)
	}
	return failures
}

func metricRuns(reports []report) map[string][]observedMetric {
	runs := make(map[string][]observedMetric)
	for _, report := range reports {
		for path, metric := range continuousMetrics(report) {
			runs[path] = append(runs[path], metric)
		}
	}
	return runs
}

func continuousMetrics(report report) map[string]observedMetric {
	metrics := make(map[string]observedMetric)
	add := func(path string, direction metricDirection, value float64) {
		metrics[path] = observedMetric{Direction: direction, Value: value}
	}
	addLatency := func(prefix string, latency latencyReport) {
		if latency.Count <= 0 {
			return
		}
		add(prefix+".mean_ms", directionLower, latency.MeanMs)
		add(prefix+".p50_ms", directionLower, latency.P50Ms)
		add(prefix+".p95_ms", directionLower, latency.P95Ms)
		add(prefix+".p99_ms", directionLower, latency.P99Ms)
		add(prefix+".over_30s_ratio", directionLower, float64(latency.Over30s)/float64(latency.Count))
	}
	addDistribution := func(prefix string, distribution distributionReport) {
		if distribution.Count <= 0 {
			return
		}
		add(prefix+".mean_ms", directionLower, distribution.MeanMs)
		add(prefix+".p50_ms", directionLower, distribution.P50Ms)
		add(prefix+".p95_ms", directionLower, distribution.P95Ms)
		add(prefix+".p99_ms", directionLower, distribution.P99Ms)
	}
	add("avg_traces_per_sec", directionHigher, report.AvgTracesPerSec)
	addLatency("export_latency_ms", report.ExportLatencyMs)
	if report.QueryLatencyMs != nil {
		addLatency("query_latency_ms", *report.QueryLatencyMs)
	}
	for operation, latency := range report.QueryLatencyByOperation {
		addLatency("query_latency_by_operation."+operation, latency)
	}
	if report.Server == nil {
		return metrics
	}
	server := report.Server
	add("server.lake_partitions", directionLower, server.LakePartitions)
	add("server.lake_partitions_delta", directionLower, server.LakePartitionsDelta)
	add("server.lake_size_bytes", directionLower, server.LakeSizeBytes)
	add("server.lake_size_bytes_delta", directionLower, server.LakeSizeBytesDelta)
	add("server.lake_growth_bytes_per_sec", directionLower, server.LakeGrowthBytesPerSec)
	add("server.ingest_queue_depth", directionLower, server.IngestQueueDepth)
	add("server.avg_rollup_ms", directionLower, server.AvgRollupMs)
	add("server.avg_flush_ms", directionLower, server.AvgFlushMs)
	add("server.avg_query_ms", directionLower, server.AvgQueryMs)
	add("server.cpu_cores", directionLower, server.CPUCores)
	add("server.rss_bytes", directionLower, server.RSSBytes)
	// HeapAllocBytes is retained in each raw report as a diagnostic snapshot,
	// but a single end-of-run gauge is GC-phase dependent and is not a continuous
	// guardrail. Allocation rate and RSS remain stable memory guardrails; heap
	// attribution comes from profiles in the allocation stage.
	add("server.alloc_bytes_per_sec", directionLower, server.AllocBytesPerSec)
	add("server.gc_pause_seconds_delta", directionLower, server.GCPauseSecondsDelta)
	for operation, distribution := range server.WriteGateWaitMs {
		addDistribution("server.write_gate_wait_ms."+operation, distribution)
	}
	for operation, distribution := range server.WriteGateHoldMs {
		addDistribution("server.write_gate_hold_ms."+operation, distribution)
	}
	for operation, background := range server.DuckLakeOperations {
		addDistribution("server.ducklake_operations."+operation+".duration_ms", background.DurationMs)
	}
	for rollup, progress := range server.Rollups {
		prefix := "server.rollups." + rollup
		add(prefix+".lag_seconds", directionLower, progress.LagSeconds)
		add(prefix+".backlog_chunks", directionLower, progress.BacklogChunks)
		addDistribution(prefix+".duration_ms", progress.DurationMs)
	}
	return metrics
}

func directionForRuns(values []observedMetric) metricDirection {
	if len(values) == 0 {
		return ""
	}
	direction := values[0].Direction
	for _, value := range values[1:] {
		if value.Direction != direction {
			return ""
		}
	}
	return direction
}

func directionsMatch(left, right []observedMetric) bool {
	leftDirection := directionForRuns(left)
	return leftDirection != "" && leftDirection == directionForRuns(right)
}

func medianObservations(values []observedMetric) float64 {
	numbers := make([]float64, len(values))
	for i, value := range values {
		numbers[i] = value.Value
	}
	sort.Float64s(numbers)
	return numbers[len(numbers)/2]
}

func normalizedImprovement(baseline, candidate float64, direction metricDirection) (float64, bool) {
	if baseline == 0 {
		return 0, candidate == 0
	}
	change := (candidate - baseline) / math.Abs(baseline) * 100
	if direction == directionLower {
		change = -change
	}
	return change, true
}

func sortedUnique(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			seen[item] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for item := range seen {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
