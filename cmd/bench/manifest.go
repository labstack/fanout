package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

const manifestSchemaVersion = 2

type runManifest struct {
	SchemaVersion       int                          `json:"schema_version"`
	Stage               string                       `json:"stage"`
	Candidate           string                       `json:"candidate"`
	Screening           bool                         `json:"screening"`
	PrimaryTarget       string                       `json:"primary_target,omitempty"`
	GuardrailExclusions []guardrailExclusionManifest `json:"guardrail_exclusions,omitempty"`
	RunOrdinal          int                          `json:"run_ordinal,omitempty"`
	StartedAt           time.Time                    `json:"started_at"`
	FinishedAt          time.Time                    `json:"finished_at"`
	Build               buildManifest                `json:"build"`
	Host                hostManifest                 `json:"host"`
	Workload            workloadManifest             `json:"workload"`
	Dataset             datasetManifest              `json:"dataset"`
	DuckDB              duckDBManifest               `json:"duckdb"`
	Server              serverConfigManifest         `json:"server"`
}

type buildManifest struct {
	FanoutVersion        string `json:"fanout_version"`
	BenchVersion         string `json:"bench_version"`
	GoVersion            string `json:"go_version"`
	GitCommit            string `json:"git_commit"`
	GitDirty             bool   `json:"git_dirty"`
	GitMetadataAvailable bool   `json:"git_metadata_available"`
	FanoutSourceSHA256   string `json:"fanout_source_sha256,omitempty"`
}

type guardrailExclusionManifest struct {
	Metric    string `json:"metric"`
	Rationale string `json:"rationale"`
}

type hostManifest struct {
	OS               string `json:"os"`
	Architecture     string `json:"architecture"`
	LogicalCPUs      int    `json:"logical_cpus"`
	GOMAXPROCS       int    `json:"gomaxprocs"`
	MemoryLimitBytes int64  `json:"memory_limit_bytes,omitempty"`
}

type workloadManifest struct {
	Seed            uint64   `json:"seed"`
	DurationSec     float64  `json:"duration_sec"`
	WarmupSec       float64  `json:"warmup_sec"`
	TargetRate      float64  `json:"target_traces_per_sec"`
	Workers         int      `json:"workers"`
	Services        int      `json:"services"`
	Namespaces      int      `json:"namespaces"`
	Cardinality     int      `json:"attribute_cardinality"`
	ErrorRate       float64  `json:"error_rate"`
	MessagingRatio  float64  `json:"messaging_ratio"`
	SendLogs        bool     `json:"send_logs"`
	SendMetrics     bool     `json:"send_metrics"`
	BackfillHours   float64  `json:"backfill_hours"`
	QueryWorkers    int      `json:"query_workers"`
	TargetQueryRate float64  `json:"target_queries_per_sec"`
	QueryOperations []string `json:"query_operations"`
	QueryWindows    []string `json:"query_windows"`
	QueryLimit      int      `json:"query_limit"`
}

type datasetManifest struct {
	ID         string `json:"id"`
	ConfigHash string `json:"config_sha256"`
}

type duckDBManifest struct {
	MaxConnections int    `json:"max_connections,omitempty"`
	Memory         string `json:"memory,omitempty"`
	Threads        int    `json:"threads,omitempty"`
}

type serverConfigManifest struct {
	FlushSeconds             int    `json:"flush_seconds,omitempty"`
	FlushBatchSize           int    `json:"flush_batch_size,omitempty"`
	RollupEverySeconds       int    `json:"rollup_every_seconds,omitempty"`
	MergeEverySeconds        int    `json:"merge_every_seconds,omitempty"`
	MaintenanceSeconds       int    `json:"maintenance_every_seconds,omitempty"`
	RetentionDays            int    `json:"retention_days,omitempty"`
	RollupSkipToLatest       bool   `json:"rollup_skip_to_latest"`
	DataPlaneInstrumentation string `json:"data_plane_instrumentation"`
}

func newRunManifest(cfg config, startedAt, finishedAt time.Time) runManifest {
	workload := workloadManifest{
		Seed:            cfg.seed,
		DurationSec:     round2(cfg.duration.Seconds()),
		WarmupSec:       round2(cfg.warmupDuration.Seconds()),
		TargetRate:      cfg.rate,
		Workers:         cfg.workers,
		Services:        cfg.services,
		Namespaces:      cfg.namespaces,
		Cardinality:     cfg.cardinality,
		ErrorRate:       cfg.errorRate,
		MessagingRatio:  cfg.msgRatio,
		SendLogs:        cfg.sendLogs,
		SendMetrics:     cfg.sendMetrics,
		BackfillHours:   cfg.backfillHours,
		QueryWorkers:    cfg.queryWorkers,
		TargetQueryRate: cfg.queryRate,
		QueryOperations: append([]string(nil), queryOperations...),
		QueryWindows:    append([]string(nil), queryWindows...),
		QueryLimit:      100,
	}
	hashInput, _ := json.Marshal(workload) // fixed value-only struct cannot fail
	hash := sha256.Sum256(hashInput)
	datasetHash := hex.EncodeToString(hash[:])
	datasetID := strings.TrimSpace(cfg.datasetID)
	if datasetID == "" {
		datasetID = "synthetic:" + datasetHash[:16]
	}

	memoryLimit := cfg.memoryLimitBytes
	if memoryLimit <= 0 {
		memoryLimit = detectMemoryLimit()
	}
	build := currentBuildManifest()
	build.FanoutVersion = strings.TrimSpace(cfg.fanoutVersion)
	build.BenchVersion = version
	build.FanoutSourceSHA256 = strings.TrimSpace(cfg.fanoutSourceSHA256)

	exclusions, _ := parseGuardrailExclusions(cfg.guardrailExclusions) // validated before a live run
	measurementMode := strings.TrimSpace(cfg.measurementMode)
	if measurementMode == "" {
		measurementMode = "unspecified"
	}
	return runManifest{
		SchemaVersion:       manifestSchemaVersion,
		Stage:               strings.TrimSpace(cfg.stage),
		Candidate:           strings.TrimSpace(cfg.candidate),
		Screening:           cfg.screening,
		PrimaryTarget:       strings.TrimSpace(cfg.primaryTarget),
		GuardrailExclusions: exclusions,
		RunOrdinal:          cfg.runOrdinal,
		StartedAt:           startedAt.UTC(),
		FinishedAt:          finishedAt.UTC(),
		Build:               build,
		Host: hostManifest{
			OS:               runtime.GOOS,
			Architecture:     runtime.GOARCH,
			LogicalCPUs:      runtime.NumCPU(),
			GOMAXPROCS:       runtime.GOMAXPROCS(0),
			MemoryLimitBytes: memoryLimit,
		},
		Workload: workload,
		Dataset: datasetManifest{
			ID:         datasetID,
			ConfigHash: datasetHash,
		},
		DuckDB: duckDBManifest{
			MaxConnections: cfg.duckDBMaxConns,
			Memory:         strings.TrimSpace(cfg.duckDBMemory),
			Threads:        cfg.duckDBThreads,
		},
		Server: serverConfigManifest{
			FlushSeconds:             cfg.flushSeconds,
			FlushBatchSize:           cfg.flushBatchSize,
			RollupEverySeconds:       cfg.rollupEverySeconds,
			MergeEverySeconds:        cfg.mergeEverySeconds,
			MaintenanceSeconds:       cfg.maintenanceSeconds,
			RetentionDays:            cfg.retentionDays,
			RollupSkipToLatest:       cfg.rollupSkipToLatest,
			DataPlaneInstrumentation: measurementMode,
		},
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
			manifest.GitMetadataAvailable = setting.Value != ""
		case "vcs.modified":
			manifest.GitDirty = setting.Value == "true"
		}
	}
	return manifest
}

func parseGuardrailExclusions(raw string) ([]guardrailExclusionManifest, error) {
	seen := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("%q must use metric=rationale", item)
		}
		metric := strings.TrimSpace(parts[0])
		rationale := strings.TrimSpace(parts[1])
		if existing, ok := seen[metric]; ok && existing != rationale {
			return nil, fmt.Errorf("metric %q has conflicting rationales", metric)
		}
		seen[metric] = rationale
	}
	items := make([]guardrailExclusionManifest, 0, len(seen))
	for metric, rationale := range seen {
		items = append(items, guardrailExclusionManifest{Metric: metric, Rationale: rationale})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Metric < items[j].Metric })
	return items, nil
}

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
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && limit > 0 {
			return limit
		}
	}
	return 0
}
