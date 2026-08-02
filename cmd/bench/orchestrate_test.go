package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteSuiteRunPreservesProcessFailureWithoutArguments(t *testing.T) {
	directory := t.TempDir()
	run := executeSuiteRun(
		context.Background(),
		filepath.Join(directory, "missing-bench"),
		directory,
		"measured",
		2,
		[]string{"-token", "secret-sentinel"},
		nil,
	)
	if run.Passed || run.ExitCode == 0 || len(run.ZeroToleranceFailures) < 2 {
		t.Fatalf("run evidence = %#v", run)
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-sentinel") {
		t.Fatalf("suite evidence leaked forwarded arguments: %s", encoded)
	}
}

func TestWriteSuiteEvidenceIsDeterministicAndSeparatesWarmup(t *testing.T) {
	directory := t.TempDir()
	manifest := runManifest{SchemaVersion: 1, Stage: "baseline-warmup", Candidate: "control"}
	suite := suiteEvidence{
		SchemaVersion: suiteSchemaVersion,
		StartedAt:     time.Date(2026, time.August, 1, 19, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, time.August, 1, 19, 5, 0, 0, time.UTC),
		Stage:         "baseline",
		Candidate:     "control",
		Warmup:        &suiteRunEvidence{Kind: "warmup", ReportFile: "warmup.json", Passed: true, Manifest: &manifest},
		Trials:        []suiteRunEvidence{},
		Passed:        true,
	}
	if err := writeSuiteEvidence(directory, suite); err != nil {
		t.Fatal(err)
	}
	first := mustReadFile(t, filepath.Join(directory, "suite.json"))
	if err := writeSuiteEvidence(directory, suite); err != nil {
		t.Fatal(err)
	}
	second := mustReadFile(t, filepath.Join(directory, "suite.json"))
	if string(first) != string(second) {
		t.Fatalf("suite JSON is not deterministic:\n%s\n%s", first, second)
	}
	if !strings.Contains(string(first), `"kind": "warmup"`) {
		t.Fatalf("warm-up evidence missing: %s", first)
	}
}

func TestOrchestrateRequiresExactlyThreeRunsBeforeCreatingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence")
	err := orchestrate([]string{"-output-dir", path, "-runs", "2"})
	if err == nil || !strings.Contains(err.Error(), "must be 3") {
		t.Fatalf("error = %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}
