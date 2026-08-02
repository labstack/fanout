package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const suiteSchemaVersion = 1

type suiteEvidence struct {
	SchemaVersion       int                          `json:"schema_version"`
	StartedAt           time.Time                    `json:"started_at"`
	FinishedAt          time.Time                    `json:"finished_at"`
	Stage               string                       `json:"stage"`
	Candidate           string                       `json:"candidate"`
	Screening           bool                         `json:"screening"`
	PrimaryTarget       string                       `json:"primary_target,omitempty"`
	GuardrailExclusions []guardrailExclusionManifest `json:"guardrail_exclusions,omitempty"`
	Warmup              *suiteRunEvidence            `json:"warmup,omitempty"`
	Trials              []suiteRunEvidence           `json:"trials"`
	Passed              bool                         `json:"passed"`
	Failures            []string                     `json:"failures,omitempty"`
}

type suiteRunEvidence struct {
	Kind                  string       `json:"kind"`
	Ordinal               int          `json:"ordinal,omitempty"`
	ReportFile            string       `json:"report_file"`
	ExitCode              int          `json:"exit_code"`
	Passed                bool         `json:"passed"`
	Manifest              *runManifest `json:"manifest,omitempty"`
	ZeroToleranceFailures []string     `json:"zero_tolerance_failures,omitempty"`
	ThresholdFailures     []string     `json:"threshold_failures,omitempty"`
}

func runSubcommand(args []string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	var err error
	switch args[0] {
	case "orchestrate":
		err = orchestrate(args[1:])
	case "analyze":
		err = analyzeCommand(args[1:])
	default:
		return false, 0
	}
	if errors.Is(err, flag.ErrHelp) {
		return true, 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench %s: %v\n", args[0], err)
		return true, 1
	}
	return true, 0
}

func orchestrate(args []string) error {
	flags := flag.NewFlagSet("bench orchestrate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var (
		outputDir           string
		stage               string
		candidate           string
		primaryTarget       string
		exclusionConfig     string
		warmupDuration      time.Duration
		measurementDuration time.Duration
		runs                int
		screening           bool
	)
	flags.StringVar(&outputDir, "output-dir", "", "new directory for suite evidence (required)")
	flags.StringVar(&stage, "stage", "baseline", "evidence stage")
	flags.StringVar(&candidate, "candidate", "control", "candidate identifier")
	flags.StringVar(&primaryTarget, "primary-target", "", "predeclared report metric path")
	flags.StringVar(&exclusionConfig, "guardrail-exclude", "", "comma-separated metric=rationale guardrail exclusions")
	flags.DurationVar(&warmupDuration, "warmup", 5*time.Minute, "separate warm-up duration")
	flags.DurationVar(&measurementDuration, "duration", 30*time.Minute, "duration of each measured trial")
	flags.IntVar(&runs, "runs", 3, "number of measured trials (must be 3)")
	flags.BoolVar(&screening, "screening", false, "measurement-overhead screening (5% guardrail, no optimization target)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if outputDir == "" {
		return errors.New("-output-dir is required")
	}
	if warmupDuration <= 0 || measurementDuration <= 0 {
		return errors.New("-warmup and -duration must be positive")
	}
	if runs != 3 {
		return fmt.Errorf("-runs must be 3 for comparable evidence, got %d", runs)
	}
	if !screening && candidate != "control" && primaryTarget == "" {
		return errors.New("-primary-target is required for a non-control candidate")
	}
	exclusions, err := parseGuardrailExclusions(exclusionConfig)
	if err != nil {
		return fmt.Errorf("-guardrail-exclude: %w", err)
	}
	if err := os.Mkdir(outputDir, 0o755); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if parentErr := os.MkdirAll(filepath.Dir(outputDir), 0o755); parentErr == nil {
				err = os.Mkdir(outputDir, 0o755)
			}
		}
		if err != nil {
			return fmt.Errorf("create evidence directory (must not already exist): %w", err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve bench executable: %w", err)
	}
	startedAt := time.Now().UTC()
	suite := suiteEvidence{
		SchemaVersion:       suiteSchemaVersion,
		StartedAt:           startedAt,
		Stage:               stage,
		Candidate:           candidate,
		Screening:           screening,
		PrimaryTarget:       primaryTarget,
		GuardrailExclusions: exclusions,
		Trials:              make([]suiteRunEvidence, 0, runs),
	}
	context, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	forwardedArgs := flags.Args()

	warmup := executeSuiteRun(context, executable, outputDir, "warmup", 0, forwardedArgs, []string{
		"-duration", warmupDuration.String(),
		"-warmup-duration", "0s",
		"-stage", stage + "-warmup",
		"-candidate", candidate,
		"-primary-target", primaryTarget,
		"-guardrail-exclude", exclusionConfig,
		"-screening=" + fmt.Sprint(screening),
	})
	suite.Warmup = &warmup
	if !warmup.Passed {
		suite.Failures = append(suite.Failures, "warm-up failed")
		suite.FinishedAt = time.Now().UTC()
		if err := writeSuiteEvidence(outputDir, suite); err != nil {
			return err
		}
		return fmt.Errorf("warm-up failed; evidence written to %s", filepath.Join(outputDir, "suite.json"))
	}

	for ordinal := 1; ordinal <= runs; ordinal++ {
		trial := executeSuiteRun(context, executable, outputDir, "measured", ordinal, forwardedArgs, []string{
			"-duration", measurementDuration.String(),
			"-warmup-duration", warmupDuration.String(),
			"-stage", stage,
			"-candidate", candidate,
			"-primary-target", primaryTarget,
			"-guardrail-exclude", exclusionConfig,
			"-screening=" + fmt.Sprint(screening),
			"-run-ordinal", fmt.Sprint(ordinal),
		})
		suite.Trials = append(suite.Trials, trial)
		if !trial.Passed {
			suite.Failures = append(suite.Failures, fmt.Sprintf("measured run %d failed", ordinal))
		}
		suite.FinishedAt = time.Now().UTC()
		if err := writeSuiteEvidence(outputDir, suite); err != nil {
			return err
		}
		if context.Err() != nil {
			break
		}
	}
	if len(suite.Trials) != runs {
		suite.Failures = appendUnique(suite.Failures, fmt.Sprintf("completed %d/%d measured runs", len(suite.Trials), runs))
	}
	suite.Passed = len(suite.Failures) == 0
	suite.FinishedAt = time.Now().UTC()
	if err := writeSuiteEvidence(outputDir, suite); err != nil {
		return err
	}
	if !suite.Passed {
		return fmt.Errorf("suite failed; evidence written to %s", filepath.Join(outputDir, "suite.json"))
	}
	fmt.Printf("suite PASS: %s\n", filepath.Join(outputDir, "suite.json"))
	return nil
}

func executeSuiteRun(ctx context.Context, executable, outputDir, kind string, ordinal int, forwarded, controlled []string) suiteRunEvidence {
	name := "warmup.json"
	if kind == "measured" {
		name = fmt.Sprintf("run-%02d.json", ordinal)
	}
	reportPath := filepath.Join(outputDir, name)
	childArgs := append(append([]string(nil), forwarded...), controlled...)
	childArgs = append(childArgs, "-report", reportPath)
	command := exec.CommandContext(ctx, executable, childArgs...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	run := suiteRunEvidence{Kind: kind, Ordinal: ordinal, ReportFile: name}
	err := command.Run()
	if err != nil {
		run.ExitCode = 1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			run.ExitCode = exitError.ExitCode()
		}
		run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, fmt.Sprintf("benchmark process exited with code %d", run.ExitCode))
	}
	reportBytes, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, "benchmark report unavailable")
		return run
	}
	var report report
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, "benchmark report is invalid JSON")
		return run
	}
	run.Manifest = &report.Manifest
	run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, report.ZeroToleranceFailures...)
	run.ThresholdFailures = append(run.ThresholdFailures, report.ThresholdFailures...)
	if report.Server == nil {
		run.ZeroToleranceFailures = appendUnique(run.ZeroToleranceFailures, "server metrics evidence unavailable")
	}
	if report.QueryLatencyMs == nil {
		run.ZeroToleranceFailures = appendUnique(run.ZeroToleranceFailures, "mixed query evidence unavailable")
	}
	if report.Passed && run.ExitCode != 0 {
		run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, "report/process verdict mismatch")
	}
	if !report.Passed && run.ExitCode == 0 {
		run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, "report/process verdict mismatch")
	}
	if !report.Passed && len(run.ZeroToleranceFailures) == 0 && len(run.ThresholdFailures) == 0 {
		run.ZeroToleranceFailures = append(run.ZeroToleranceFailures, "report failed without a recorded reason")
	}
	run.Passed = report.Passed && run.ExitCode == 0 && len(run.ZeroToleranceFailures) == 0 && len(run.ThresholdFailures) == 0
	return run
}

func writeSuiteEvidence(outputDir string, suite suiteEvidence) error {
	bytes, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(outputDir, "suite.json")
	if err := os.WriteFile(path, append(bytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write suite evidence: %w", err)
	}
	return nil
}
