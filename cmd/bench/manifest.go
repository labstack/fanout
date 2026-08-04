package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// runManifest records what produced a report so a stored run.json is still
// readable months later. Everything here is either derived from the running
// process or copied from the flags that shape the workload — nothing describes
// the server under test beyond the version string the caller passes, because
// bench cannot verify server configuration.
type runManifest struct {
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Build      buildManifest    `json:"build"`
	Host       hostManifest     `json:"host"`
	Workload   workloadManifest `json:"workload"`
	// WorkloadHash identifies the requested workload: two runs with the same hash
	// asked for the same synthetic telemetry. It does not certify that they
	// produced the same data — the event count depends on the rate actually
	// achieved, which varies with the host.
	WorkloadHash string `json:"workload_sha256"`
}

type buildManifest struct {
	FanoutVersion string `json:"fanout_version"`
	BenchVersion  string `json:"bench_version"`
	GoVersion     string `json:"go_version"`
	GitCommit     string `json:"git_commit"`
	GitDirty      bool   `json:"git_dirty"`
}

type hostManifest struct {
	OS               string `json:"os"`
	Architecture     string `json:"architecture"`
	LogicalCPUs      int    `json:"logical_cpus"`
	GOMAXPROCS       int    `json:"gomaxprocs"`
	MemoryLimitBytes int64  `json:"memory_limit_bytes,omitempty"`
}

type workloadManifest struct {
	Seed uint64 `json:"seed"`
	// Requested, not achieved: report.duration_sec carries elapsed wall clock.
	RequestedDurationSec float64  `json:"requested_duration_sec"`
	TargetRate           float64  `json:"target_traces_per_sec"`
	Workers              int      `json:"workers"`
	Services             int      `json:"services"`
	Namespaces           int      `json:"namespaces"`
	Cardinality          int      `json:"attribute_cardinality"`
	ErrorRate            float64  `json:"error_rate"`
	MessagingRatio       float64  `json:"messaging_ratio"`
	SendLogs             bool     `json:"send_logs"`
	SendMetrics          bool     `json:"send_metrics"`
	BackfillHours        float64  `json:"backfill_hours"`
	QueryWorkers         int      `json:"query_workers"`
	TargetQueryRate      float64  `json:"target_queries_per_sec"`
	QueryOperations      []string `json:"query_operations"`
	QueryWindows         []string `json:"query_windows"`
}

func newRunManifest(cfg config, startedAt, finishedAt time.Time) runManifest {
	workload := workloadManifest{
		Seed:                 cfg.seed,
		RequestedDurationSec: round2(cfg.duration.Seconds()),
		TargetRate:           cfg.rate,
		Workers:              cfg.workers,
		Services:             cfg.services,
		Namespaces:           cfg.namespaces,
		Cardinality:          cfg.cardinality,
		ErrorRate:            cfg.errorRate,
		MessagingRatio:       cfg.msgRatio,
		SendLogs:             cfg.sendLogs,
		SendMetrics:          cfg.sendMetrics,
		BackfillHours:        cfg.backfillHours,
		QueryWorkers:         cfg.queryWorkers,
		TargetQueryRate:      cfg.queryRate,
		QueryOperations:      append([]string(nil), queryOperations...),
		QueryWindows:         append([]string(nil), queryWindows...),
	}
	hashInput, _ := json.Marshal(workload) // fixed value-only struct cannot fail
	hash := sha256.Sum256(hashInput)

	build := currentBuildManifest()
	build.FanoutVersion = strings.TrimSpace(cfg.fanoutVersion)
	build.BenchVersion = version

	return runManifest{
		StartedAt:  startedAt.UTC(),
		FinishedAt: finishedAt.UTC(),
		Build:      build,
		Host: hostManifest{
			OS:               runtime.GOOS,
			Architecture:     runtime.GOARCH,
			LogicalCPUs:      runtime.NumCPU(),
			GOMAXPROCS:       runtime.GOMAXPROCS(0),
			MemoryLimitBytes: detectMemoryLimit(),
		},
		Workload:     workload,
		WorkloadHash: hex.EncodeToString(hash[:]),
	}
}

func currentBuildManifest() buildManifest {
	manifest := buildManifest{
		GoVersion: runtime.Version(),
		GitCommit: "unknown",
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return manifest
	}
	if info.GoVersion != "" {
		manifest.GoVersion = info.GoVersion
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			manifest.GitCommit = setting.Value
		case "vcs.modified":
			manifest.GitDirty = setting.Value == "true"
		}
	}
	return manifest
}

// detectMemoryLimit reports the cgroup memory ceiling so a containerized run
// records what it was actually allowed to use rather than the host total.
// Returns 0 when unlimited or unavailable (including on non-Linux).
func detectMemoryLimit() int64 {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		value, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		raw := strings.TrimSpace(string(value))
		if raw == "" || raw == "max" {
			continue
		}
		if limit, err := strconv.ParseInt(raw, 10, 64); err == nil && limit > 0 {
			return limit
		}
	}
	return 0
}
