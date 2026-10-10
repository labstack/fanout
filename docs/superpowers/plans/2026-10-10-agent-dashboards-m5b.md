# Agent dashboards milestone 5, Phase B: scale benchmark Implementation Plan

> **For agentic workers:** Use checkbox (`- [ ]`) steps to implement this plan task-by-task. Codex implements measurement harnesses and runs only tiny-volume tests. The controller runs generation, performance measurements, ingest load, browser measurements and aggregate checks. This planning dispatch does not implement or run them.

**Goal:** Measure the format-4 raw path at S18 volume, establish the summaries-off ingest baseline for S19, and supply Phase C with measured failures and summary-key cardinality.
**Architecture:** Extend the M4 readbench tiler and `BenchmarkPanelQueries24Hours`. Generate one persistent dataset, then reuse its manifest and Parquet batches for query timing, cardinality and browser measurement. Measure real OTLP ingestion separately over in-memory gRPC with the production durable writer. All new Go measurement code is test-only, `readbench` tagged and explicitly opted in.
**Tech Stack:** Existing Go, pinned DuckDB 2 through `bash scripts/with-duckdb.sh`, parquet-go, OTLP protobufs, gRPC/bufconn, database/sql, syscall, Bun, Vitest and Playwright. No new dependencies.
**Spec:** `docs/superpowers/specs/2026-10-10-agent-dashboards-m5-design.md`, Phase B as amended by 96166ff9, S18, S19, metric-shape requirements and Risks/Metric query cost. S7 is ≤500 ms p95 per panel; S18's complete twelve-panel server request is ≤1,000 ms; S6 is ≤1,500 ms browser first render.

## Global Constraints

- Planning writes exactly `docs/superpowers/plans/2026-10-10-agent-dashboards-m5b.md`. No other file writes, git writes, network, browser tools, CodeGraph initialization or questions. All edits and commands below are future implementation instructions. No timings or gate passes are claimed here.
- Phase B changes measurement code and documentation only. No product behavior, query optimization, summary tables, routing, telemetry format, engine pin, control schema, public API, configuration setting or dependency changes. A missing S7 target is evidence for Phase C; fix no measured product problem in this phase. S19's summaries-on/≤10% extra CPU verdict belongs to Phase C.
- Extend, rather than replace, `tilePanelParquet`, `preparePanelReplay`, `panelBenchEngine`, `panelBenchmarkDashboard`, `panelBenchmarkMetricShapes`, `measurePanelBenchmark` and `BenchmarkPanelQueries24Hours`. Preserve `FANOUT_PANEL_BENCH_DATA_DIR` as a source **telemetry root**, and legacy `FANOUT_PANEL_BENCH_VOLUME=1|10`. Scale mode uses separate parameters; reject combinations with legacy volume instead of silently choosing one. Preserve the signed-time and source-preservation regressions.
- The supplied demo is approximately 2.2M spans, 14.3M metric points and 0.86M logs over 40.8 h, 21 services, one host and 24 containers. S18 is approximately 86.4M spans and 34M logs plus metrics for 20 hosts ×24 containers at their 10 s/60 s cadences and app metrics tiled with traffic. The measured costs are 414 B/span, 103 B/log and 45 B/metric point; approximately 47 GB is an estimate, not a generated-data result. Approximately 117 GB free exceeds the preliminary 94 GB refusal threshold; check actual free space and source-derived projected counts before writing.
- Keep format 4 only, exact physical schemas, typed VARIANT resources/attributes, literal dotted keys, signed nanoseconds, UTC, event-time footer bounds, native Parquet binding, `union_by_name=false` and `hive_partitioning=false`. All time predicates keep native `TIMESTAMPTZ_NS` and bind UTC Go times through `?::TIMESTAMP_NS::TIMESTAMPTZ_NS`. Shift nonzero metric start times; retain absent zero start times. Recompute `telemetry.MetricSeriesID` after **any** identity rewrite, including namespace-only rewrites.
- Never open the source with `telemetrystore.Open`: startup can clean staging and rewrite the schema batch. Read validated format-4 metadata and source files only. Derive source bounds directly from footers with a bounded local engine; preserve unknown statistics by decoding, never by pruning. Persistent output is controller-owned measurement state. No automatic removal, overwrite, format conversion or second full-size copy. Atomic completion manifests distinguish complete output from interrupted generation.
- Every new Go file begins `//go:build readbench`. Full generation, timings, cardinality and sustained ingest require explicit environment switches. Tiny tests have independent names and bounded synthetic fixtures. Gate the existing `TestMetricMeasuresExecuteAtReviewScale` with `FANOUT_METRIC_REVIEW_SCALE=1` when touching that harness. Do not make `just check` generate telemetry, run performance loops or launch a browser.
- Commands run at the repository root and use RTK. Before future Go commands, set the dispatch's required environment. Do not run Go during planning; the controller supplies the already cached native archive and installed dependencies, since the wrapper may download a missing archive.

  ```bash
  export GOCACHE="$PWD/.superpowers/go-cache"
  export GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp
  export GOFLAGS=-mod=readonly
  export GOPROXY=off
  rtk mkdir -p "$GOCACHE" "$GOTMPDIR"
  ```

- Controller-only long runs execute sequentially on an idle native macOS/Linux AMD64/ARM64 machine. Record platform, CPU count, Go/native-engine version, resolved memory/thread/read-pool settings, executor parallelism, dataset hash, interval and sample counts. Retain production sizing and engine asynchronous I/O defaults. No paid models. A blocked, failed, empty or undersampled measurement is not PASS.
- Result JSON and the committed results document contain bounded aggregate evidence, synthetic labels and hashes only: no machine hostnames, IPs, credentials, source paths, output paths, raw resources, raw telemetry or SQL containing private literals. Dataset/output paths remain environment values. Each task's final `rtk just check` is controller-owned; record failures without changing unrelated gates. No git commands are prescribed.

## Review Focus

1. Target volume comes from spans/s and hours, while infrastructure stays at 20×24 and its original 10 s/60 s cadences: Task 1, `TestPanelScaleTinyVolumeAndCadence`.
2. Host/container/namespace rewrites yield distinct typed identities and recomputed series IDs without changing metric meaning or signed time: Task 1, `TestPanelScaleMetricIdentities` and the existing signed replay regression.
3. S7 covers every M4 and requested metric shape, including stat totals/trends, ten-minute lookback, explicit 10 s counters and exponential histograms: Task 2, `TestPanelScaleShapeMatrix` and `TestPanelScaleTimingJSON`.
4. Summary sizing counts namespace/minute tuples without string concatenation, and reports every minute and every single-dimension omission against one twentieth: Task 3, `TestPanelScaleCardinalityCountsTuples`.
5. Ingest acknowledgements are durable and browser measurements actually read the S18 dataset rather than the smoke seed: Task 4, `TestIngestHeadroomTinyDurable`/`TestIngestHeadroomTinyFailure`; Task 6, external-dataset setup tests and controller paint evidence.

## Task 1: Tiler at S18 volume

**Files**

- Modify: `internal/panel/panel_bench_data_test.go`, `internal/panel/panel_bench_test.go`.
- Create/test: `internal/panel/panel_scale_data_test.go` (`readbench`).
- Consume unchanged: `internal/telemetry/metric_series.go`, `internal/telemetry/parquet_rows.go`, `internal/telemetry/parquet_schema.go`, `internal/telemetry/store/repair.go` and existing verification helpers. Do not export private production row types for the benchmark.

**Interfaces**

Consumes existing `panelReplayMetadata(input string) ([]telemetry.BatchMetadata, error)`, `panelTraceIndex(dir string) error`, `panelBenchEngine(testing.TB, panelBenchData) (*query.Duck, config.Config)`, `telemetry.MetricSeriesID(telemetry.Metric) uint64` and `telemetrystore.VerifyBatches(string)`. Retain the existing raw tiler signature:

```go
func tilePanelParquet(input, output, signal, prefix string, start, end time.Time,
    shift time.Duration, m *telemetry.BatchMetadata, services, namespaces map[string]bool) (err error)
```

Produces test-only helpers in `panel_scale_data_test.go`:

```go
type panelScaleOptions struct {
    SpansPerSecond int64
    Hours, Hosts, Containers int
    OutputDir string
}
type panelScaleCounts struct { Spans, Logs, Metrics int64 }
type panelScaleManifest struct {
    Version int `json:"version"` // manifest version 1, independent of batch format 4
    Complete bool `json:"complete"`
    DatasetID string `json:"dataset_id"`
    SourceHash string `json:"source_hash"`
    BatchFormat int `json:"batch_format"`
    SpansPerSecond int64 `json:"spans_per_second"`
    Hours int `json:"hours"`
    Hosts int `json:"hosts"`
    ContainersPerHost int `json:"containers_per_host"`
    Start, End time.Time
    LookbackStart time.Time `json:"lookback_start"`
    Counts panelScaleCounts `json:"counts"` // window counts, not lookback-inclusive totals
    LookbackCounts panelScaleCounts `json:"lookback_counts"`
    Bytes int64 `json:"bytes"`
    Files int `json:"files"`
    Services []string `json:"services"`
}
func panelScaleOptionsFromEnv() (panelScaleOptions, error)
func preparePanelScale(tb testing.TB, input string, opts panelScaleOptions) panelBenchData
func loadPanelScale(tb testing.TB, dir string) (panelBenchData, panelScaleManifest)
func panelScaleRequireDisk(dir string, estimated uint64) error
func panelScaleRewriteMetric(m telemetry.Metric, namespace string, host, container int) telemetry.Metric
func TestPanelBenchmarkScaleGenerate(t *testing.T)
```

Use explicit snake_case tags on **all** manifest fields, including `start`, `end` and nested `spans`, `logs`, `metrics`. `OutputDir` and `panelBenchData.Dir` never serialize. `preparePanelScale` returns the existing data type populated from the manifest; legacy `Volume`/`PeriodHours` retain their legacy meaning, not an invented scale multiplier.

- [ ] **Write tiny identity and disk-refusal tests first.** Import `errors`, `fmt`, `math`, `reflect`, `testing`, telemetry and time as required. The rewrite helper shallow-copies the resource map before changing keys; it never mutates a shared source resource. Negative host/container values mean leave that resource identity alone. Namespace always changes independently.

```go
func panelScaleRewriteMetric(m telemetry.Metric, namespace string, host, container int) telemetry.Metric {
    resource := make(map[string]any, len(m.Resource)+4)
    for k, v := range m.Resource { resource[k] = v }
    m.Resource, m.Namespace = resource, namespace
    resource["service.namespace"] = namespace
    if host >= 0 { resource["host.name"] = fmt.Sprintf("bench-host-%02d", host) }
    if container >= 0 {
        resource["container.id"] = fmt.Sprintf("bench-h%02d-c%02d", host, container)
        resource["container.name"] = fmt.Sprintf("bench-container-%02d-%02d", host, container)
    }
    m.SeriesID = telemetry.MetricSeriesID(m)
    return m
}

func TestPanelScaleMetricIdentities(t *testing.T) {
    m := telemetry.Metric{Namespace:"original", ServiceName:"collector", Name:"system.cpu.time",
        Unit:"s", ScopeName:"hostmetrics", ScopeVersion:"v1", Type:"sum", Monotonic:true,
        Temporality:"cumulative", StartTimeUnixNanos:-60000000017, TimeUnixNanos:-17,
        Attributes:map[string]any{"state":"idle", "integer":int64(7), "bytes":[]byte{0,255}},
        Resource:map[string]any{"host.name":"source", "nested":map[string]any{"ok":true}}}
    m.SeriesID = telemetry.MetricSeriesID(m)
    seen := map[uint64]bool{}
    for h := range 2 { for c := range 2 {
        got := panelScaleRewriteMetric(m, "bench", h, c)
        if got.SeriesID == m.SeriesID || seen[got.SeriesID] || got.SeriesID != telemetry.MetricSeriesID(got) {
            t.Fatal("series identity was reused or not recomputed")
        }
        seen[got.SeriesID] = true
        if got.StartTimeUnixNanos != m.StartTimeUnixNanos || got.TimeUnixNanos != -17 ||
            got.Type != "sum" || !got.Monotonic || got.Temporality != "cumulative" ||
            !reflect.DeepEqual(got.Attributes,m.Attributes) || !reflect.DeepEqual(got.Resource["nested"],m.Resource["nested"]) {
            t.Fatal("rewrite changed fidelity, typed values or signed time")
        }
    }}
    namespaceOnly := panelScaleRewriteMetric(m,"other",-1,-1)
    if namespaceOnly.SeriesID == m.SeriesID || namespaceOnly.Resource["host.name"] != "source" || m.Resource["host.name"] != "source" {
        t.Fatal("namespace rewrite or source preservation failed")
    }
}

func panelScaleDiskCheck(free, estimated uint64) error {
    if estimated > math.MaxUint64/2 || free < estimated*2 {
        return errors.New("insufficient disk: twice estimated output required")
    }
    return nil
}

func TestPanelScaleDiskRefusal(t *testing.T) {
    if panelScaleDiskCheck(93,47) == nil { t.Fatal("accepted insufficient disk") }
    if err := panelScaleDiskCheck(94,47); err != nil { t.Fatal(err) }
    if panelScaleDiskCheck(math.MaxUint64,math.MaxUint64) == nil { t.Fatal("overflow bypassed refusal") }
}
```

Define `panelScaleDiskCheck` in this owning task; `panelScaleRequireDisk` calls `syscall.Statfs` on the nearest existing output parent, uses available-to-user blocks (`Bavail × Bsize`), checks multiplication overflow, and fails closed on stat errors. This supports both native target OSes without a dependency. Add a disk-provider argument to the generation core for the refusal integration test; with a provider returning 93 and estimate 47, assert no output directory, Parquet or completion manifest was created. Check disk before copying or writing source data, not just before the final batch.

- [ ] **Implement explicit options and persistent publication.** `FANOUT_PANEL_BENCH_SPANS_PER_SECOND`, `FANOUT_PANEL_BENCH_HOURS`, `FANOUT_PANEL_BENCH_HOSTS`, `FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST` are positive integers. Require all scale parameters and `FANOUT_PANEL_BENCH_OUTPUT_DIR` for generation; production measurement values are 1000/24/20/24. Tiny tests pass options directly, so they do not inherit controller input. Reject duration/count overflow and an output equal to, below or above the source after resolving symlinks. Require an absent output directory; an incomplete directory fails with a bounded actionable error, never an automatic retry/cleanup. Write into the chosen directory with an incomplete manifest, then atomically rename a completed manifest after every writer closes, footer/count checks pass and sidecars validate. No persistent directory gets a `tb.Cleanup(RemoveAll)`.

`TestPanelBenchmarkScaleGenerate` skips unless `FANOUT_PANEL_BENCH_GENERATE=1`; then requires source and options, calls the generator and logs only counts, bytes and dataset hash. `loadPanelScale` needs no source env var, validates manifest version/completion/format, exact query-window duration, metadata/file inventories and bounded hashes, and fails if anything is absent or incompatible. Subsequent tasks call it and **never** call `preparePanelReplay` or regenerate when `OUTPUT_DIR` is present. Keep the old M4 entry path unchanged when scale mode is absent.

- [ ] **Implement bounded time/identity tiling in the existing raw-row writer.** Add a test-only rewrite-options form behind `tilePanelParquet`; retain its current signature as a wrapper for existing callers. Use the same raw schema, compression, row-group/page sizes, sorting buffers, footer bounds and `panelTraceIndex` contract. Source metadata validation precedes all writing. Hash the source inventory while reading rather than making a second multi-GB copy. Read the newest complete source window and extend it with a real ten-minute predecessor interval for metric lookback; the measured `[Start,End)` remains exactly 24 h (or 6 h). Track lookback counts separately and include their bytes in disk estimation.

Traffic replication uses the selected source's actual span count divided by its duration, not the approximate demo average or `VOLUME=10`. Repeat full replicas and a deterministic partial replica selected by complete trace IDs, retaining linked logs/app metrics consistently. Keep event spacing, duration, services, operations, routes, status/error distribution and histogram buckets unchanged. Target `spans_per_second × seconds`; whole-trace selection may exceed it by at most the largest included trace, which the manifest records as a count tolerance. Assert requested/actual rates and distribution counts, rather than claiming exactly 86M from a multiplier. A tiny fixture with four one-span traces per second and requested eight spans/s needs exactly two replicas.

Use stable copy namespaces across temporal tiles (source namespace + replica number); trace/span prefixes also include temporal tile number to avoid trace collisions. The same prefix rewrites linked trace/span IDs in logs. Assign traffic replicas across the requested hosts deterministically. Rewrite resource `service.namespace` with the scalar namespace; align `host.name` across spans, logs and associated app metrics. Keep resource service names and app metric attribute identities otherwise unchanged. Do not repeatedly create new metric series at each temporal tile boundary. Shift ingestion/event/end/observed times consistently, keep absent optional zero timestamps, guard signed int64 addition, and preserve sort/index tuples. Temporal counter/histogram seams retain the demo's reset semantics and are documented, not hidden by value fabrication.

Classify infrastructure by the demo's `hostmetrics` and `docker_stats` instrumentation scopes; discover and validate the observed scope names/versions and host/container resource tuples before expanding them. Require one source host and 24 distinct containers for the S18 controller run. Clone host series 20 times and each of the 24 container sets onto each host once. App metric series follow traffic replicas; never multiply host/container series by the ~traffic multiplier. Preserve the observed 10 s or 60 s schedule for each source series; fail if required metric names/kinds/cadences are missing. A source capture with gaps is recorded as gaps, not filled with invented points. Manifest workload census includes per-scope point counts, per-name semantic kinds, cadence distribution, series counts and histogram bound-count distribution; no private resource strings.

For metrics only, reconstruct a typed Go row from the input `parquet.Schema`, rewrite typed resources/namespace and recompute `series_id`, then deconstruct through the same input schema. Add a test-only full physical-row mirror of `metricParquetRow` (all fields/tags from the read file, including uint64 series ID and any/VARIANT maps); populate the canonical identity with namespace/service/name/unit/scope name/version/attributes/resource. Do not JSON round-trip or edit VARIANT byte fragments. Decode unsigned series IDs as uint64. Verify the output physical schema equals format 4 and check integers, bytes, nested maps, booleans and literal dotted keys after the actual Parquet round-trip. Spans/logs retain the raw-row path; when their resource identities change, use corresponding full test-only physical mirrors and the same schema reconstruction. No separate product schema source is introduced.

- [ ] **Add the tiny end-to-end tiling tests.** `TestPanelScaleTinyVolumeAndCadence` creates a positive-time, one-hour source repository in `t.TempDir()` containing 14,400 one-span traces (four/s), proportional linked logs, a host counter every 60 s, two container gauges every 10 s and one app series. Generate with options 8 spans/s, one hour, two hosts, two containers; expected window counts are 28,800 spans, twice the source logs, 120 host points and 1,440 container points per gauge name. Assert four distinct `(host.name,container.id)` tuples, stable series IDs across time, doubled app points, and no duplicated infrastructure namespace replicas. Explicit lookback rows are outside these window counts. A signed fixture is made with the existing raw transformation, then tiled/read/verified; retain `TestPanelBenchmarkReplayTilesSignedTimesAndDistinctIdentities` and add namespace/resource assertions. Snapshot every source file/hash including staging before and after; assert source schema and staging survive. Reload the completed output with source env unset; inventory/hash/counts stay identical. Partial output and mismatched manifest options fail rather than regenerate.

- [ ] **Run red/green tiny tests (Codex implementation step); run the gate (controller).** No demo generation in this command.

```bash
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^(TestPanelScaleMetricIdentities|TestPanelScaleDiskRefusal|TestPanelScaleTinyVolumeAndCadence|TestPanelScalePersistentReload|TestPanelBenchmarkReplayTilesSignedTimesAndDistinctIdentities)$' -count=1
rtk just check
```

Expected: new tests fail before the helpers exist; all pass after implementation with bounded synthetic data. The gate runs without generation/timing environment switches.

- [ ] **Generate S18 once (controller only).** The controller privately exports `FANOUT_PANEL_BENCH_DATA_DIR` and an absent `FANOUT_PANEL_BENCH_OUTPUT_DIR`; the latter remains set for Tasks 2, 3 and 6.

```bash
rtk proxy env -u FANOUT_PANEL_BENCH_VOLUME FANOUT_PANEL_BENCH_GENERATE=1 FANOUT_PANEL_BENCH_SPANS_PER_SECOND=1000 FANOUT_PANEL_BENCH_HOURS=24 FANOUT_PANEL_BENCH_HOSTS=20 FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST=24 bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^TestPanelBenchmarkScaleGenerate$' -count=1 -v -timeout=4h
```

Expected outputs: completed version-1 manifest, format-4 batches/trace indexes, source-derived counts near 86.4M spans/~34M logs, 20 hosts/480 container identities, both observed cadences, physical-schema and signed-time verification, projected/actual byte totals and source/dataset hashes. A refusal or unsupported source is a failed preparation; do not reduce the S18 target to obtain PASS. The four-hour timeout is a controller budget, not a latency target.

## Task 2: Query shapes at 24 h

**Files**

- Modify: `internal/panel/panel_bench_test.go`, `internal/panel/metric_measure_bench_test.go`.
- Create/test: `internal/panel/panel_scale_shapes_test.go`, `internal/panel/panel_scale_results_test.go` (`readbench`).

**Interfaces**

Consumes Task 1's completed manifest through `loadPanelScale`, existing dashboard/metric-shape builders, `NewExecutor`, `Check`, `Normalize`, `Executor.Run`, `metricFrameHasValue` and the existing `measurePanelBenchmark` machinery. New test-only helpers: `panelScaleMetricShapes(names map[string]string) []Panel`, `panelScaleTiming(samples []float64, target float64) (panelScaleTimingResult,error)` and `writePanelScaleResults(path string, result panelScaleResults) error`. `names` maps the required histogram/memory/filesystem logical roles to validated observed metric names; fixed CPU/network/container names are not replaced with whatever is fastest.

- [ ] **Lock the entire matrix with real panels and tiny validation tests.** Retain all fourteen M4 span/log shapes: `traces_all`, `traces_checkout`, `traces_selected`, `count`, `rate`, `logs`, `traffic`, `latency`, `errors`, `log_volume`, `service_counts`, `severity_counts`, `p95_by_service`, `error_rate_window`. Retain the four current generic metric shapes (gauge/updown average by service, counter rate, histogram p95) for historical comparison. Preserve `panelBenchmarkDashboard`'s exact twelve panels, variable resolution and executor parallelism four; do not replace its expensive panels with cheaper metrics. Add this system metric matrix:

| Role/name | Kind and measure | Grouping |
| --- | --- | --- |
| `system.cpu.time` | counter `rate()` | `resource['host.name']`; separately `attributes['state']` |
| `system.network.io` | counter `rate()` | `attributes['direction']` |
| observed `system.memory.usage` | updown `last(value)` | `resource['host.name']` |
| observed `system.filesystem.usage` | updown `last(value)` | `attributes['mountpoint']` |
| `container.cpu.utilization` | gauge `avg(value)` | `resource['container.id']` |
| observed container memory usage | gauge `last(value)` | `resource['container.id']` |
| observed explicit histogram | histogram `p95(value)` | `service` |
| observed exponential histogram | histogram `p95(value)` | `service` |
| each observed histogram above | heatmap, `count()`, `Histogram{Field:"value",Buckets:"explicit"}` | no grouping (existing heatmap contract) |

Resolve filesystem/memory/container-memory names from the source census by semantic name, not highest count. Common container memory names are validated from the supplied demo; require gauge semantics for the selected container metric. Select the most frequent explicit and exponential histogram deterministically (count then name) and record names/types/bound counts in JSON. Required missing names/kinds fail S18 measurement preparation; never silently skip a shape. If the demo lacks a separate explicit or exponential example, record that coverage gap and require controller-provided format-4 demo coverage; synthetic review data is not representative evidence.

Generate each scalar role in **stat, timeseries and table** where the existing contract supports it. Timeseries gets **auto and 1 m** buckets with the listed grouping; heatmap gets auto/1 m and no grouping. Stat has **no grouping and no authored bucket**: its counterpart aggregates the same metric over the whole fleet/window and includes its normal automatic trend/total behavior. Table keeps the listed grouping but **no authored bucket**, retaining normal totals and automatic trends. Record the resolved automatic intervals for both. The existing validator rejects stat grouping and stat/table buckets; do not change product validation, duplicate invalid variants or pretend their internal intervals were explicitly authored at 1 m. Add `10s` timeseries variants for both CPU groupings and network rate. Do not clamp explicit 10 s buckets. These probes expose ten-minute lookback, double stat/trend series-building, gauge/counter histogram scans and 160-bound exponential bucket decoding called out in Risks.

```go
func panelScaleMetricShapes(names map[string]string) []Panel {
    rows := []struct { id, name, kind, measure, by string }{
        {"cpu_host","system.cpu.time","counter","rate()","resource['host.name']"},
        {"cpu_state","system.cpu.time","counter","rate()","attributes['state']"},
        {"network_direction","system.network.io","counter","rate()","attributes['direction']"},
        {"memory_host",names["memory"],"updown","last(value)","resource['host.name']"},
        {"filesystem_mountpoint",names["filesystem"],"updown","last(value)","attributes['mountpoint']"},
        {"container_cpu","container.cpu.utilization","gauge","avg(value)","resource['container.id']"},
        {"container_memory",names["container_memory"],"gauge","last(value)","resource['container.id']"},
        {"explicit_p95",names["explicit_histogram"],"histogram","p95(value)","service"},
        {"exponential_p95",names["exponential_histogram"],"histogram","p95(value)","service"},
    }
    var out []Panel
    for _, row := range rows {
        buckets := []string{"auto","1m"}
        if row.kind == "counter" { buckets = append(buckets,"10s") }
        for _, bucket := range buckets {
            id := row.id+"_timeseries_"+bucket
            out = append(out,Panel{ID:id,Title:id,Viz:"timeseries",Query:&Query{From:"metrics",
                Where:[]string{"name = "+sqlString(row.name),"type = "+sqlString(row.kind)},
                Measures:[]string{row.measure},By:[]string{row.by},Bucket:bucket}})
        }
        for _, viz := range []string{"stat","table"} {
            id := row.id+"_"+viz
            q := &Query{From:"metrics",Where:[]string{"name = "+sqlString(row.name),"type = "+sqlString(row.kind)},
                Measures:[]string{row.measure}}
            if viz=="table" { q.By=[]string{row.by} }
            out=append(out,Panel{ID:id,Title:id,Viz:viz,Query:q})
        }
    }
    for _, role := range []string{"explicit_histogram","exponential_histogram"} { for _, bucket := range []string{"auto","1m"} {
        id := role+"_heatmap_"+bucket
        out = append(out,Panel{ID:id,Title:id,Viz:"heatmap",Query:&Query{From:"metrics",
            Where:[]string{"name = "+sqlString(names[role]),"type = 'histogram'"},
            Measures:[]string{"count()"},Bucket:bucket,Histogram:&Histogram{Field:"value",Buckets:"explicit"}}})
    }}
    return out
}
```

`TestPanelScaleShapeMatrix` supplies role names `system.memory.usage`, `system.filesystem.usage`, `container.memory.usage`, `request.duration`, `exponential.duration`; asserts exactly 43 added shapes (18 auto/1 m scalar timeseries, three explicit 10 s timeseries, nine stats, nine tables, four heatmaps), unique IDs, every name fixed literally, correct kind/measure/grouping and non-nil histogram config. Assert stat has no By/Bucket and table has no Bucket; label stat grouping as whole fleet in results. Insert tiny finite counter/updown/gauge/histogram points with two observations and one lookback point into `metricFixture`; run `Check` and `Executor.Run` for every shape. Require populated numeric measures or positive heatmap weights; retain NULL gaps rather than asserting every cell non-NULL. This catches syntax/name-rule/type mismatches with the real engine, not only serialized panel equality.

- [ ] **Return timings and produce one final JSON report.** Refactor the existing measuring function to return both p50/p95 and raw repeated samples to its caller, preserving Go benchmark metrics. Use the same executor path as M4: normalization, validation, variable scans for `traces_selected`, stat trends/totals, frame conversion and all twelve panels. Ordinary single-panel shapes still omit query variables. Warm each shape with at least one complete untimed request and require at least 100 timed requests. A failed request is recorded as a failure and prevents a valid verdict; never discard errors, slow trials or outliers. Keep calibration `N=1` records out of the final report; only the completed `N>=100` subbench results count.

```go
type panelScaleTimingResult struct {
    Samples int `json:"samples"`
    P50MS float64 `json:"p50_ms"`
    P95MS float64 `json:"p95_ms"`
    TargetMS float64 `json:"target_ms"`
    MeetsTarget bool `json:"meets_target"`
}
func panelScaleTiming(samples []float64, target float64) (panelScaleTimingResult,error) {
    if len(samples)<100 { return panelScaleTimingResult{},errors.New("100 warm samples required") }
    ordered := append([]float64(nil),samples...)
    for _, v := range ordered { if math.IsNaN(v)||math.IsInf(v,0)||v<0 {
        return panelScaleTimingResult{},errors.New("invalid timing sample")
    }}
    sort.Float64s(ordered)
    percentile := func(p int) float64 { return ordered[(p*len(ordered)+99)/100-1] }
    p50,p95 := percentile(50),percentile(95)
    return panelScaleTimingResult{len(ordered),p50,p95,target,p95<=target},nil
}

func TestPanelScaleTimingJSON(t *testing.T) {
    samples := make([]float64,100)
    for i := range samples { samples[i]=float64(i+1) }
    got,err := panelScaleTiming(samples,94)
    if err!=nil || got.P50MS!=50 || got.P95MS!=95 || got.MeetsTarget { t.Fatalf("%+v %v",got,err) }
    if _,err:=panelScaleTiming(samples[:99],500);err==nil { t.Fatal("undersampled report accepted") }
    samples[0]=math.NaN()
    if _,err:=panelScaleTiming(samples,500);err==nil { t.Fatal("invalid sample accepted") }
    raw,err:=json.Marshal(got)
    if err!=nil || !bytes.Contains(raw,[]byte(`"p95_ms":95`)) { t.Fatal("timing JSON contract") }
}
```

Define `panelScaleResults` here: schema version, dataset ID/hash, requested and measured volume, exact window/lookback, sanitized runtime/configuration fields, an ordered `items` array containing shape ID/query role/kind/viz/group/bucket/resolved interval, raw `samples_ms`, the timing result and error count, plus `dashboard_twelve` and `s7_failing_shapes`. Include resolved top-N, frame rows/cells and truncation flags: 480 container identities may produce bounded frames under existing display caps, which must be visible in the method. Do not raise caps or mistake displayed series count for dataset series count. Give every field an explicit snake_case tag. Report p95 targets per item: 500 for individual panels, 1000 for `dashboard_twelve`. The full dashboard's browser result is separate. Serialize native version from `SELECT version()` rather than the driver version. `writePanelScaleResults` uses a same-directory temporary file with mode 0600 and atomic rename; write no partial PASS JSON. Test with `t.TempDir()` and decode the actual file; assert fields, failure-list derivation and absence of path/resource keys.

In scale mode, `BenchmarkPanelQueries24Hours` skips unless `FANOUT_PANEL_BENCH_TIMINGS=1`, requires `OUTPUT_DIR` and `FANOUT_PANEL_BENCH_RESULTS`, loads the completed manifest, and appends this matrix plus retained M4 shapes. Reject the default regression dataset as S18 evidence. A p95 miss is a reported verdict, not a fatal harness error that hides later shapes. Execution errors/timeouts mark the affected shape invalid, preserve the report and make the run fail; report its ID to Phase C. The JSON writer runs after all final subbench results, never overwrites results with a calibration report, and verifies complete shape coverage. Add manifest counts/shape list assertions to the tiny report test.

- [ ] **Run tiny red/green (Codex); gate and full timings (controller).** `TestPanelScaleTimingJSON` computes statistics, not benchmark performance.

```bash
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^(TestPanelScaleShapeMatrix|TestPanelScaleTimingJSON)$' -count=1
rtk just check
rtk proxy env -u FANOUT_PANEL_BENCH_VOLUME FANOUT_PANEL_BENCH_TIMINGS=1 bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^$' -bench '^BenchmarkPanelQueries24Hours$' -benchtime=100x -count=1 -timeout=4h
```

Before the last command, the controller privately exports `FANOUT_PANEL_BENCH_RESULTS` to a JSON destination and keeps the completed S18 `OUTPUT_DIR`. Expected: no generation, every shape measured with ≥100 warm samples, per-shape p50/p95 and failure status, complete twelve-panel p50/p95, raw-version/settings and source/dataset hashes in one JSON file. Record known 160-bound histogram costs and stat/trend duplication without proposing a Phase B fix.

## Task 3: Summary key cardinality

**Files**

- Create/test: `internal/panel/panel_scale_cardinality_test.go` (`readbench`).
- Consume unchanged: Task 1 manifest, `panelBenchEngine`, `internal/query/views.go` (including the pre-projected literal `messaging.system` field).

**Interfaces**

Produces `panelScaleCardinalitySQL(signal string, dimensions []string) (string,error)`, `TestPanelBenchmarkScaleCardinality(t *testing.T)` and test-only result structs with explicit snake_case tags. The full run requires `FANOUT_PANEL_BENCH_CARDINALITY=1`, completed `OUTPUT_DIR` and `FANOUT_PANEL_BENCH_CARDINALITY_RESULTS`. The loader pins the same files/window as Task 2; it does not seed or modify summary tables.

- [ ] **Implement typed tuple counting for the full and omission sets.** Fixed namespace and minute are never omitted. Span dimensions, in order: `service`, `operation`, `kind`, `status`, `http_method`, `http_status_code`, `http_route`, `rpc_service`, `rpc_method`, `db_system`, `messaging_system`, `peer_service`, `service_version`, `deployment_env`. Measure the full set and all fourteen sets with exactly one dimension removed. Logs use `service`, `severity`, `severity_number`: full and three omissions. Do not use resource-map hashes, concatenated keys, `count(DISTINCT ...)` that drops NULL tuples, approximate distinct counts or source averages multiplied by the replay factor.

```go
func panelScaleCardinalitySQL(signal string, dimensions []string) (string,error) {
    events := map[string]string{"spans":"start_time","logs":"time"}
    event,ok := events[signal]
    if !ok { return "",errors.New("unsupported cardinality signal") }
    allowed := map[string]bool{}
    if signal=="spans" {
        for _,d:=range []string{"service","operation","kind","status","http_method","http_status_code","http_route","rpc_service","rpc_method","db_system","messaging_system","peer_service","service_version","deployment_env"} { allowed[d]=true }
    } else { for _,d:=range []string{"service","severity","severity_number"} { allowed[d]=true } }
    seen:=map[string]bool{}
    for _,d:=range dimensions { if !allowed[d]||seen[d] { return "",errors.New("invalid cardinality dimension") };seen[d]=true }
    keys:=[]string{"namespace","minute"}
    keys=append(keys,dimensions...)
    cols:=strings.Join(keys,",")
    return fmt.Sprintf(`WITH scoped AS (
      SELECT *,epoch_ns(time_bucket(INTERVAL '1 minute',%s::TIMESTAMP_NS)) AS minute
      FROM %s WHERE %s>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND %s<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
    ), tuples AS (SELECT %s FROM scoped GROUP BY %s),
    key_counts AS (SELECT minute,count(*) AS summary_rows FROM tuples GROUP BY minute),
    row_counts AS (SELECT minute,count(*) AS raw_rows FROM scoped GROUP BY minute)
    SELECT r.minute,r.raw_rows,k.summary_rows FROM row_counts r JOIN key_counts k USING(minute)
    ORDER BY r.minute`,event,signal,event,event,cols,cols),nil
}
```

SQL input is the same completed, pinned public span/log views; binds are `(manifest.Start.UTC(),manifest.End.UTC())`. Minute output is signed Unix nanoseconds, not milliseconds. Read `sql.Rows` with int64 for minute/raw_rows/summary_rows, close it and check iteration errors. Include namespace in tuple keys while reporting global per-minute totals; additionally run per-namespace/minute diagnostics with anonymized namespace ordinal, so small namespaces are visible. Enumerate every minute in the exact window, explicitly report zero-data minutes without a divide-by-zero ratio; any edge minute is identified, not silently dropped. Avoid logging namespace values.

- [ ] **Apply the rule and produce bounded JSON.** For spans, `summary_rows*20 <= raw_span_rows` is the exact rule, with overflow-safe integer comparison `summary_rows <= raw_span_rows/20`. Report every minute, overall sum ratio, worst minute, violation count and omitted dimension. For logs report both log keys/log rows and log keys/spans for the same minute; the spec's hard one-twentieth bound is on span summary rows against spans, not a newly invented log threshold. Also report combined span+log summary counts against spans as sizing information, clearly labeled informational. No metric-value summary recommendation or metric cardinality rewrite. The report does not automatically delete dimensions or edit Phase C design; it names which omissions make the span rule hold and which panels would fall back to raw if those dimensions are dropped. Single omissions that still fail are an explicit Phase C decision input, not an inferred PASS.

- [ ] **Add a native tiny tuple regression.** `TestPanelScaleCardinalityCountsTuples` uses `metricFixture`'s native engine/database plus `querytest` span/log tables. Insert 40 spans in a signed minute with two namespaces: each namespace has one service and two operations, yielding four full `(namespace,minute,service,operation)` keys. Removing operation gives two keys. Assert full `4/40` fails and omit-operation `2/40` passes the one-twentieth rule. Add NULL versus empty operation and values containing separators to prove tuples are not concatenated or dropped; assert their distinct groups explicitly. Insert logs with same severity string and two severity numbers; assert removing severity number reduces log keys. Add another minute with no spans and logs present: log ratios against spans are null/undefined. Run the SQL above with signed UTC parameters; verify excluded lookback/end-bound rows and per-minute counts, not only the SQL text.

- [ ] **Run tiny tests (Codex), gate and full count (controller).**

```bash
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^TestPanelScaleCardinalityCountsTuples$' -count=1
rtk just check
rtk proxy env FANOUT_PANEL_BENCH_CARDINALITY=1 bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^TestPanelBenchmarkScaleCardinality$' -count=1 -v -timeout=2h
```

Expected final command output: aggregate-only JSON at the private results env destination; fifteen span sets and four log sets, 1,440 minute records per set including empty minutes, all ratios and violations, same dataset ID/window/hash as timings. No claim that a global average passing makes every minute pass.

## Task 4: Ingest headroom baseline

**Files**

- Create/test: `internal/ingest/ingest_headroom_bench_test.go`, `internal/ingest/ingest_headroom_data_test.go` (`readbench`).
- Consume unchanged: `internal/ingest/server.go`, `auth.go`, `http_test.go` request constructors and runtime settings helper; `internal/telemetry/store/writer.go`, `repository.go`; `internal/ingest/transport_bench_test.go` methodology; `internal/query/backlog_pressure_test.go` CPU methodology.
- Do not change either existing production ingest behavior or the network-based transport benchmark. Its `transportbench` helpers are unavailable under `readbench`; define local equivalents instead of referencing excluded symbols.

**Interfaces**

Existing `NewServer(config.Config,batchSubmitter) *Server`, `RegisterOTLP(grpc.ServiceRegistrar,*Server)`, `GRPCServerOptions(*settings.Store) []grpc.ServerOption`, `telemetrystore.NewWriter(*Repository,int) *Writer`, `Writer.SetInFlightBudget(int)`, `Writer.Run(context.Context) error`, `Writer.Submit(context.Context,Batch) error`, `Writer.Wait()`. Produces `runIngestHeadroom(tb testing.TB, opts ingestHeadroomOptions) ingestHeadroomResult`, `observedSubmitter`, `processCPUSeconds() (float64,error)`, `TestIngestHeadroomHeadroom` and bounded tiny tests. Result struct reports per-signal offered/acknowledged/durable counts and rates, actual elapsed time, lag/CPU and errors with explicit snake_case tags.

- [ ] **Build real durable gRPC in memory.** Use existing settings fixture/token generation/auth behavior; token stays in memory. The listener is `bufconn.Listen(1<<20)`, never `net.Listen`. Register the actual ingest server and downstream writer. Start writer.Run with a cancellable context, track its result channel, and close the client, gracefully stop gRPC, cancel/wait the writer, check its result and close the repository. Every stage's operational error fails. Use this concrete dial sequence:

```go
lis := bufconn.Listen(1 << 20)
g := grpc.NewServer(GRPCServerOptions(store)...)
RegisterOTLP(g,NewServer(cfg,observed))
served := make(chan error,1)
go func(){ served<-g.Serve(lis) }()
conn,err := grpc.NewClient("passthrough:///ingest-headroom",
    grpc.WithContextDialer(func(ctx context.Context,_ string)(net.Conn,error){return lis.DialContext(ctx)}),
    grpc.WithTransportCredentials(insecure.NewCredentials()))
if err!=nil { t.Fatal(err) }
ctx := metadata.AppendToOutgoingContext(t.Context(),"authorization","Bearer "+token)
traces := collectortrace.NewTraceServiceClient(conn)
logs := collectorlogs.NewLogsServiceClient(conn)
metrics := collectormetrics.NewMetricsServiceClient(conn)
```

This block is inside `TestIngestHeadroomHeadroom`/its setup helper with existing imports/locals `store`, `token`, `cfg`, `observed` and `t`. `cfg` is loaded with the same production ingest batch size and decoded in-flight budget as `cmd/fanout/main.go`; call `writer.SetInFlightBudget(cfg.IngestMaxInFlightBytes)`. Clients use bounded per-call deadlines, not a no-op submitter or direct `exportSpans` invocation. The run includes OTLP decoding, conversion, auth, admission, grouping, Parquet/index publication and acknowledgement. No TCP socket, HTTP server or network traffic is needed.

- [ ] **Record commit acknowledgement lag without production hooks.** The writer already acknowledges only after commit. Wrap `Submit` and measure from its admission to durable return; also timestamp scheduled client requests to record scheduled-to-durable acknowledgement lag, including sender queue delay. These are separately labeled; event-time delay is not commit lag. Use an in-process collector with bounded lag samples/histograms and per-second cumulative committed rows; no new product metrics.

```go
type observedSubmitter struct {
    next batchSubmitter
    mu sync.Mutex
    spans,logs,metrics int64
    lagMS []float64 // bounded by fixed run duration and bounded export cadence
    failures int64
}
func (s *observedSubmitter) Submit(ctx context.Context,b telemetrystore.Batch) error {
    began:=time.Now()
    err:=s.next.Submit(ctx,b)
    s.mu.Lock()
    defer s.mu.Unlock()
    if err!=nil { s.failures++;return err }
    s.spans+=int64(len(b.Spans));s.logs+=int64(len(b.Logs));s.metrics+=int64(len(b.Metrics))
    s.lagMS=append(s.lagMS,float64(time.Since(began))/float64(time.Millisecond))
    return nil
}
func processCPUSeconds() (float64,error) {
    var usage syscall.Rusage
    if err:=syscall.Getrusage(syscall.RUSAGE_SELF,&usage);err!=nil{return 0,err}
    return float64(usage.Utime.Sec+usage.Stime.Sec)+float64(usage.Utime.Usec+usage.Stime.Usec)/1e6,nil
}
```

Use the same `Getrusage` method as query backlog pressure; it measures process user+system CPU, including native engine, test clients, serialization and server. Report CPU seconds, seconds per million durable rows and CPU cores=`delta_cpu_seconds/elapsed_wall_seconds`; not CPU percent normalized against an unstated machine CPU count. Sample beginning/end of the fixed steady-load interval separately from warmup and final drain. Preserve the same client/harness settings for Phase C's comparison. CPU diagnostics failing makes CPU evidence inconclusive; do not report zero CPU.

- [ ] **Implement a bounded, paced workload and fixed duration.** `TestIngestHeadroomHeadroom` skips unless `FANOUT_INGEST_BENCH=1`; require `FANOUT_INGEST_BENCH_RESULTS`, `FANOUT_PANEL_BENCH_DATA_DIR` (source root) and the completed scale manifest's workload census. Default controller duration is 10 m (`FANOUT_INGEST_BENCH_DURATION`); short unit tests pass an options struct and never enable the heavy test.

The target is 2,000 spans/s, proportional demo logs (actual source log/span ratio, approximately 775 logs/s), proportional **app** metric traffic, and the **20-host/480-container** metric workload at the census's 10 s/60 s cadences. Do not double infrastructure hosts or halve their export intervals when doubling span load. Report exact offered metric points/s by scope/cadence, not a hard-coded guessed metric rate. Use bounded templates streamed from the valid source Parquet; map their canonical fields into genuine OTLP protobufs with typed AnyValue conversion for int64, bool, double, bytes, nested lists/maps, original kind/temporality/monotonicity/start times and histogram bounds/counts. Exponential-source canonical rows have explicit converted bounds; export them as explicit histograms with preserved cumulative/delta/reset semantics, documenting this wire representation. Required template sources and names must be present; no channel submitter is sustained-load evidence.

For traces/logs, use deterministic valid 16-byte/8-byte IDs and current positive OTLP timestamps per export. Keep service/route/error distribution, rewrite resource namespace/host/container as in Task 1, and ensure successive metrics of each synthetic series keep identity while cumulative values progress/reset as the source does. Do not repeatedly send identical IDs/timestamps or create new series each export. Convert telemetry canonical fields to protobufs in `ingest_headroom_data_test.go` using a local recursive typed converter, with golden tiny round-trip tests for bytes/int64/nested maps. No JSON/float64 conversion of integer resources. Prebuild only a bounded cycle of templates; CPU measurements still include per-export protobuf construction/serialization.

Use fixed 100 ms traffic ticks with 200 spans per tick and an integer remainder accumulator for proportional logs/app metrics. Infrastructure exporters use exact 10 s/60 s schedules with deterministic phase offsets across hosts; keep per-host intervals intact. Feed a bounded worker queue (four workers, fixed queue capacity); record scheduled, offered, acknowledged, failed and pending counts. Do not slow the offered schedule after an acknowledgement or silently drop when the queue fills. A full queue is an inability to sustain the target, recorded as a run failure. Record each one-second interval and the whole steady interval. After the fixed load duration, stop scheduling, drain with a fixed bounded deadline and verify footer/metadata durable row counts equal acknowledged rows, including zero failed exports. Report load-interval throughput separately from post-load drain, commit-lag p50/p95/max and whether pending work grows over time.

This is the writer/server baseline with summaries absent; document exactly which existing background loops run. Keep the initial baseline focused on the production ingest server/writer/repository, without starting analytical/query-maintenance loops (the amended Phase B scope asks for ingest separately). Phase C must repeat that baseline and add summary upkeep under identical scheduling/configuration; it must also validate summary lifecycle under the full product separately. No Phase B claim of ≤10% summary overhead is possible.

- [ ] **Add tiny durable/failure and scheduler tests before sustained load.** `TestIngestHeadroomTinyDurable` runs bufconn with the real writer/repository and three bounded exports using existing `testTraceRequest`, `testLogsRequest`, `testMetricsRequest` expanded to small distinct IDs/times. Require exactly the submitted counts, acknowledged=durable per signal and zero pending after shutdown; inspect actual metadata/Parquet. `TestIngestHeadroomTinyFailure` wraps a submitter that returns a sentinel error and asserts zero acknowledged/durable rows and one failure. No channel count may substitute for durability.

```go
type rejectingSubmitter struct{}
func (rejectingSubmitter) Submit(context.Context,telemetrystore.Batch) error {
    return errors.New("tiny injected commit failure")
}
func TestIngestHeadroomTinyFailure(t *testing.T) {
    observed:=&observedSubmitter{next:rejectingSubmitter{}}
    err:=observed.Submit(t.Context(),telemetrystore.Batch{Spans:[]telemetry.Span{{TraceID:"tiny"}}})
    if err==nil || observed.spans!=0 || observed.failures!=1 || len(observed.lagMS)!=0 {
        t.Fatal("failed commit was counted as accepted")
    }
}
```

`TestIngestHeadroomTinySchedule` advances a synthetic clock across 60 s without sleeping or committing: assert 120,000 scheduled spans, exact rational logs/app count, six 10 s exports/host and one 60 s export/host (half-open schedule), and failure on queue exhaustion. CPU numeric assertions only check monotonicity/finite values, never a performance target on a tiny test.

- [ ] **Run tiny tests (Codex), gate and sustained baseline (controller).**

```bash
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/ingest -run '^(TestIngestHeadroomTinyDurable|TestIngestHeadroomTinyFailure|TestIngestHeadroomTinySchedule|TestIngestHeadroomTinyTypedTemplates)$' -count=1
rtk just check
rtk proxy env FANOUT_INGEST_BENCH=1 FANOUT_INGEST_BENCH_DURATION=10m bash scripts/with-duckdb.sh go test -tags=readbench ./internal/ingest -run '^TestIngestHeadroomHeadroom$' -count=1 -v -timeout=20m
```

Expected final command output: sanitized JSON with 10-minute workload definition, actual per-second/per-signal offered and durable rates, approximately 1.2M spans scheduled, lag distribution, no silent drops, bounded drain, verified durable totals and measured process CPU. Throughput failure is documented as baseline failure, not resolved by changing the input rate or summary code. Disk refusal applies to this independent scratch workload as well; no scale dataset regeneration occurs.

## Task 5: Informational 5,000 spans/s ×6 h run

**Files**

- Consume unchanged: Tasks 1–3 generation/timing/cardinality implementations.
- Modify tiny parameter assertions only: `internal/panel/panel_scale_data_test.go`, `internal/panel/panel_scale_shapes_test.go`.
- Modify later: `docs/benchmarks/2026-10-agent-dashboards-m5.md` in Task 7. No second tiler, benchmark or new product path.

**Interfaces**

The same scale environment contract, completed manifest and timing JSON schema. Only spans/s and hours change: 5000/6, still 20 hosts ×24 containers with unchanged cadences. Approximately 108M spans and 42M logs; estimate metric/app bytes from the actual census. The query window is 6 h and is labeled informational throughout, not “24 h” merely because the benchmark function's historical name is retained.

- [ ] **Add a tiny parameter regression (Codex).** In `TestPanelScalePersistentReload`, parameterize one-hour fixtures with two different requested rates and assert target span counts scale with rate while infrastructure point counts do not. `TestPanelScaleShapeMatrix` builds dashboards with six-hour start/end values and confirms the loader and query requests retain those bounds. No six-hour physical dataset is generated by Codex.
- [ ] **Check disk independently and generate only when affordable (controller).** Keep S18 output and its JSON evidence intact. Set `FANOUT_PANEL_BENCH_OUTPUT_DIR` to a distinct, absent informational destination and `FANOUT_PANEL_BENCH_RESULTS`/cardinality results to distinct destinations. Recheck free space after S18: the initial 117 GB cannot be reused as the current free amount. With ~47 GB already retained, free space may fail the informational run's twice-output threshold. Record that as blocked evidence; the controller supplies another volume with sufficient space. Never delete the S18 dataset or lower the run's rate to evade the guard.

```bash
rtk proxy env -u FANOUT_PANEL_BENCH_VOLUME FANOUT_PANEL_BENCH_GENERATE=1 FANOUT_PANEL_BENCH_SPANS_PER_SECOND=5000 FANOUT_PANEL_BENCH_HOURS=6 FANOUT_PANEL_BENCH_HOSTS=20 FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST=24 bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^TestPanelBenchmarkScaleGenerate$' -count=1 -v -timeout=4h
rtk proxy env -u FANOUT_PANEL_BENCH_VOLUME FANOUT_PANEL_BENCH_TIMINGS=1 bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run '^$' -bench '^BenchmarkPanelQueries24Hours$' -benchtime=100x -count=1 -timeout=4h
rtk just check
```

Expected: a different dataset ID with a six-hour window, same full shape registry and ≥100 warm trials per shape, p50/p95 and twelve-panel time. All threshold comparisons are informational; no informational result changes the S18 verdict. Restore the S18 `OUTPUT_DIR` before Task 6.

## Task 6: Browser first render at S18 volume (controller task)

**Files**

- Modify measurement setup only: `ui/host/e2e/global-setup.ts`, `ui/host/e2e/performance-support.ts`, `ui/host/e2e/performance-support.test.ts`.
- Create/test: `ui/host/e2e/scale-setup-support.ts`, `ui/host/e2e/scale-setup-support.test.ts`.
- Consume unchanged: `ui/host/e2e/performance.spec.ts`, `ui/host/e2e/fixtures/performance.json`, `ui/host/playwright.config.ts`.
- No product UI edit, browser launch by Codex, private credential export or replacement of the existing paint measurement.

**Interfaces**

New opt-in **test-only** input `FANOUT_E2E_SCALE_DATA_DIR` is the persistent **state root** (not the original source telemetry root). It contains Task 1's manifest and `telemetry/parquet/batches`. Existing `FANOUT_E2E_BINARY`, `FANOUT_E2E_BASE_URL`, `FANOUT_E2E_STORAGE_STATE`, `FANOUT_E2E_PERFORMANCE_ID` and `FANOUT_E2E_PERFORMANCE_WINDOW` retain their contracts. The controller command invokes global setup, which boots/stops the instance; no second manual server or undocumented external setup is required.

- [ ] **Implement a narrow scale branch in global setup.** Read/validate the version-1 completed format-4 manifest and require 1000/24/20/24 with measured counts/tolerance and lookback. Resolve the external state path privately. Boot the same binary via existing `spawn` with `FANOUT_DATA_DIR` set to this generated root. Retain the temporary private authentication/log/storage-state directory and normal auth setup; never remove external data in cleanup. Reject a pre-existing control DB requiring reused credentials: the measurement dataset is initially telemetry-only. This first browser run creates disposable control state in the controller's dataset; later timing/report runs ignore that derived state and retain Parquet hashes.

In the scale branch, **skip smoke seeding** and all-panels/service-map readiness/dashboard creation. Use the actual twelve-panel performance fixture, freeze its `from`/`to` to manifest bounds and validate its exact panel IDs. Keep the fixture's variable/annotation/paint/refresh behavior. Readiness checks require populated frames and `count`/`logs` totals equal manifest window counts, replacing only the default fixture's hardcoded 8640 count in this branch. Name-based checkout filtering remains fixed; source census must contain checkout. Boot with `FANOUT_RETENTION_DAYS=0`, `FANOUT_MAINTENANCE_INTERVAL=24h` for historical measurement data; these are existing settings and benchmark-only deviations, recorded in method. Keep production DuckDB defaults and initial background rollup behavior; record any backlog/setup delay. Do not wait for summaries, which do not exist.

Extend readiness helper with an optional expected-count map, preserving the existing default smoke test's exact 8640 expectations. Use these real fixture-mode selection/count rules:

```ts
export function scaleWindow(manifest: {
  version: number; complete: boolean; batch_format: number;
  spans_per_second: number; hours: number; hosts: number; containers_per_host: number;
  start: string; end: string; counts: { spans: number; logs: number };
}) {
  if (manifest.version !== 1 || !manifest.complete || manifest.batch_format !== 4 ||
      manifest.spans_per_second !== 1000 || manifest.hours !== 24 || manifest.hosts !== 20 ||
      manifest.containers_per_host !== 24 || Date.parse(manifest.end)-Date.parse(manifest.start) !== 86_400_000 ||
      !Number.isSafeInteger(manifest.counts.spans) || manifest.counts.spans <= 0 ||
      !Number.isSafeInteger(manifest.counts.logs) || manifest.counts.logs <= 0) {
    throw new Error("Complete S18 format-4 dataset required");
  }
  return { time: { from: manifest.start, to: manifest.end, refresh: "off" },
    expected: { count: manifest.counts.spans, logs: manifest.counts.logs } };
}
```

The input's `spans_per_second` and `counts` are the manifest fields defined by Task 1, not a separate schema. Use the existing authenticated POST `/api/dashboards` and response ID checks; store state privately. No secrets in argv/output. Cleanup stops only the child server, disposes the API context and deletes only its temporary private directory. Signal handlers must not call `rm` on external data. Attach only dataset ID/hash/counts/window, not the data path, to performance metadata.

- [ ] **Add tiny setup/unit tests (Codex, no browser).** `scale-setup-support.test.ts` tests valid manifest window/counts, rejection of incomplete/version-3/6-hour manifests and invalid counts, selection of skip-seed mode, and cleanup ownership. `performance-support.test.ts` tests external count expectations against populated twelve-panel results and verifies defaults still demand 8640. A mode-selection helper must be consumed by global setup, so tests actually guard the selected branch. No generated-file snapshot asserting only its own implementation.

```bash
rtk bun run --cwd ui/host test e2e/scale-setup-support.test.ts e2e/performance-support.test.ts
rtk bun run --cwd ui/host lint
```

Expected: tiny unit tests/TypeScript checks pass; no server or browser is launched. The controller runs `rtk just check` after this harness change.

- [ ] **Build and execute the existing performance spec (controller only).** The controller restores the S18 dataset environment and supplies installed Chrome, Bun dependencies, embedded UI and cached DuckDB. These commands are exact; global setup boots the instance and performs authenticated setup. No browser command is run by Codex.

```bash
rtk bash scripts/with-duckdb.sh go build -o .superpowers/fanout-m5b ./cmd/fanout
export FANOUT_E2E_BINARY="$PWD/.superpowers/fanout-m5b"
export FANOUT_E2E_SCALE_DATA_DIR="$FANOUT_PANEL_BENCH_OUTPUT_DIR"
rtk proxy env -u FANOUT_E2E_SKIP_PERFORMANCE -u CI bun run --cwd ui/host e2e e2e/performance.spec.ts --project=1440-light --project=1440-dark
rtk just check
```

The binary uses the already verified embedded UI; if UI embeds are stale, the controller completes the repository's existing UI build/verification before measuring and records that separately. Expected output: `FANOUT_PERFORMANCE` compact lines for both themes; `performance.json`, paint-sample attachments and refresh-cycle evidence under the existing Playwright `test-results` tree. Preserve 20 fresh-context samples per theme, 1440×3000 all-visible paint viewport, no retries/discarded outliers/page warmup, RAF nonblank-canvas/font checks, unique populated result IDs, no faults and existing refresh request budgets. The record's `render_p95_ms <= 1500` determines S6; the existing calibrated 1800 ms CI ceiling is **not** S6. Record both theme p50/p95 from attachments, use the worse p95 for the S18 browser verdict and keep server ≤1000 ms evidence separate. Readiness/server startup timeout failure at S18 is a failed/unmeasured browser step; do not increase the performance spec's timeouts to mask it.

## Task 7: Results document and Phase C handoff

**Files**

- Create: `docs/benchmarks/2026-10-agent-dashboards-m5.md`.
- Consume: Task 1 manifests, Task 2 timing JSON, Task 3 cardinality JSON, Task 4 ingest JSON, Task 5 informational JSON and Task 6 Playwright attachments. This plan does not create that results file during planning.

**Interfaces**

The results document joins evidence by schema version, dataset ID/hash, window, native engine and resolved settings, not an absolute file path. No new runtime interface. Controller measurements are required to populate numeric results; absent evidence is explicitly unmeasured/blocked and prevents a Phase B completion claim.

- [ ] **Check result completeness before writing conclusions.** Require all fourteen M4 span/log IDs, four historical metric IDs, all 43 supported system-metric variants and the complete twelve-panel request at S18; every valid timing record has ≥100 warm samples, finite p50/p95 and error counts. Document the stat/table authored-bucket/grouping exclusions as existing contract limits, while preserving their actual automatic trend/total cost. Compare manifest row counts with query/window counts and browser totals; never merge calibration records or information from different data windows. Preserve actual failures and quantify incomplete coverage. Join spans/log keys to exactly the S18 minutes; verify nineteen cardinality sets and one-dimension omissions. Ingest has its distinct workload ID and summaries-off label, not a fictitious S18 directory throughput derived from tiling.

- [ ] **Write method and results from evidence, with these explicit tables.**

  1. Requested/actual S18 volume and lookback counts, host/container series/cadence census, format/native version, source/dataset hashes, bytes/files, generation disk estimate/check and measured machine class (OS/architecture/CPU count, no hostname).
  2. Every shape's semantic role, metric name/kind, grouping, visualization, requested/resolved bucket, samples, p50/p95, target and PASS/MISS/INVALID. Include auto/1 m and 10 s variants independently, explicit/exponential histogram layout sizes and heatmaps. State the raw path and normal executor inclusion of lookback, validation, totals/trends/frame conversion.
  3. Exact twelve-panel request p50/p95 versus ≤1000 ms and both browser themes' p50/p95 versus ≤1500 ms, with the worse-theme S6 verdict, correctness/refresh evidence and any setup limitation.
  4. Ten-minute summaries-off ingest: offered and accepted/durable rows/s by signal, cadence/workload details, scheduled-to-ack and submit-to-commit lag, one-second backlog/throughput behavior, drain counts, process CPU seconds/cores and CPU-per-row. State that S19's ≤10% summary CPU overhead is unmeasured until Phase C repeats this same baseline with upkeep on.
  5. Summary cardinality full/omit-one tables: per-minute minimum/median/worst and total counts/ratios, violations and span one-twentieth verdict; log/log and log/span diagnostics distinguished. The full private JSON retains every minute without publishing namespace/host resource labels.
  6. Informational 5000×6 h volume, disk availability, every same shape and twelve-panel timings, or a concrete blocked/unmeasured result if adequate disk was unavailable. Keep it separate from S18 acceptance.
  7. The complete list of S18 shapes exceeding 500 ms or lacking valid measurements, plus twelve-panel/browser misses separately. For metric misses include the name/kind/layout, grouping/viz/bucket, observed cost and known lookback/histogram/totals risks. This list is the input to Phase C's measured metric fix before Phase D; no new Phase B optimizer, metric summary or speculative routing design is attached.

- [ ] **Validate publication privacy and verdicts with read-only commands (controller).** Audit all pasted evidence manually for local paths, source hostname/IP strings and credentials; use known input/output values privately to search for accidental inclusion without printing them. Prefer an explicit allowlist of fields from JSON rather than pasting process logs or SQL. Run the bounded structural checks below; zero S7 misses is an allowed empty list, not missing coverage.

```bash
rtk rg -n 'S7|S18|S19|S6|p50|p95|one twentieth|one-twentieth|summaries.off|informational|Phase C' docs/benchmarks/2026-10-agent-dashboards-m5.md
rtk rg -n '/Users/|/private/|/home/|[0-9]{1,3}(\.[0-9]{1,3}){3}|Bearer |setup_token|ingest_token' docs/benchmarks/2026-10-agent-dashboards-m5.md
rtk just check
```

Expected: first scan shows all required measurement topics; privacy scan has no matches (exit 1). Interpret numeric-version false positives manually without removing engine versions. Aggregate gate PASS plus complete controller evidence is required before marking Phase B complete. A real latency miss is still a completed measurement and a Phase C input; missing required measurements is incomplete work. The controller reviews results; this plan prescribes no git writes.

## Self-review

### Spec coverage list

- Phase B amended tiling method, existing M4 harness extension and persistent reuse → Task 1; no ingest used to generate the scale query dataset and no per-run regeneration.
- S18 1000 spans/s ×24 h, representative services/operations/routes/errors, namespace/host copies and proportional logs/app metrics → Task 1 count-driven traffic/whole-trace selection, linked identities and census verification.
- Hostmetrics/docker_stats 20 hosts ×24 containers at 10 s/60 s, distinct resource identities and recomputed uint64 series_id → Task 1 infrastructure classification, typed rewrite and native tiny round-trip tests; infrastructure volume stays independent of spans/s.
- Format 4, signed nanoseconds, optional start times, UTC native predicates, typed VARIANT/dotted keys, source preservation, physical schema and trace indexes → Task 1; existing signed-time/source regressions retained and extended. No legacy reader, engine/configuration or telemetry behavior changes.
- Twice-estimated-output refusal, ~47 GB estimate/~117 GB initial space, bounded memory and no second full-size copy → Task 1 disk/overflow/integration tests; Task 5 independently checks current free space and retains S18 data.
- Every M4 Task 6 shape, historical metric comparisons and unchanged twelve-panel request → Task 2 registry, measurement reuse and JSON completeness checks; Task 7 report.
- Required CPU host/state rate, network direction rate, updown memory/filesystem last, container gauge avg/last, histogram service p95 and heatmaps, auto/1 m and meaningful stat/timeseries/table variants → Task 2's explicit 43-shape matrix and native fixture execution. Stat/table variants obey existing grouping/bucket limits and still include automatic trends/totals.
- Risks/Lookback cost and Metric query cost, explicit 10 s counters, exponential bucket decoding, gauge/counter histogram scans and stat totals/trends → Task 2 extra 10 s probes, explicit/exponential census and full Executor.Run measurement; no premature product optimization.
- Warm repeated p50/p95, ≥100 trials, S7 ≤500 ms, complete request ≤1000 ms, sanitized JSON env destination, native settings/version and exact dataset/window → Task 2 timing/result tests and controller run; invalid/undersampled runs never PASS.
- Candidate fourteen span dimensions, full/each omission, log service/severity/severity_number, fixed namespace/minute keys and per-minute one-twentieth verdict → Task 3 nineteen sets and native tuple tests; average/combined/log ratios are distinguished from the spec's span rule.
- S19 baseline through the real OTLP server over in-process gRPC with durable writer at 2000 spans/s, proportional logs/app metrics and 20-host cadences for fixed ten minutes → Task 4 bufconn/auth/writer/scheduler; accepted rows/s, commit acknowledgement lag, pending growth, CPU and durable counts are explicit. Summaries-on/≤10% overhead is deferred to Phase C with reproducible baseline settings.
- Informational 5000 spans/s ×6 h using the same generator/matrix and still 20×24 infrastructure → Task 5; independent disk guard, distinct dataset/results and separate acceptance labeling.
- Browser S6 at actual S18 volume, 12-panel 24 h first render, existing Playwright spec, controller-only execution with exact commands/outputs → Task 6 scale setup skips the 8640-row seed, freezes the manifest window, checks real totals, preserves paint/refresh evidence and uses 1500 ms rather than the 1800 ms CI ceiling.
- Required M5 results document, complete methods/volumes/shapes/server/browser/ingest/cardinality/verdicts and Phase C failing-shape input before Phase D → Task 7 completeness/privacy checks; no numbers fabricated and no hostnames/IPs/local paths published.
- No dependencies or product changes; readbench and explicit heavy-run opt-ins; Codex tiny tests/controller long runs and gates → Global Constraints and each task's ownership labels. No git operations, network/browser execution or additional planning artifacts.

### Placeholder scan

- Seven task boundaries match the brief. Every task has Files, Interfaces and checkbox steps; reusable helper signatures are owned by their introducing task. Code blocks contain concrete identity rewriting, disk guard, panels, timing statistics/assertions, cardinality SQL, bufconn registration/dialing, durable observer/failure assertions and browser manifest validation. Longer generator/scheduler integration is specified by actual inputs, limits, transforms, counts and tests rather than an empty implementation marker.
- All consumed production/harness signatures and existing paths were read locally; create paths are new files in existing directories. `transportbench`-only helpers are not referenced by `readbench`; local observation/CPU helpers are declared in Task 4. The browser setup's smoke seeding and hardcoded readiness counts are explicitly addressed.
- Commands use RTK and the pinned native wrapper with the requested GOCACHE/GOTMPDIR. Planning ran no Go, gate, generation, timing, network, browser or git mutation. Future long commands are controller-only; skip/failure/unmeasured states are explicit and never represented as performance evidence.

### Type consistency

- OTLP wire timestamps are positive uint64; format-4 replay/raw timestamps and minute bounds are signed int64 nanoseconds. Window binds are UTC time.Time through nanosecond casts; datetime-function arguments alone cast to TIMESTAMP_NS. Manifest/browser RFC3339Nano windows describe the same 24-hour interval; browser millisecond comparison only validates duration, not data truncation.
- Metric series IDs remain uint64; resource/attribute maps keep typed int64/bool/bytes/nested values through schema reconstruction and protobuf AnyValue conversion. Resource/namespace changes precede the one canonical MetricSeriesID hash call. Stored OTLP kind and semantic view kind are distinguished in the metric census/shape filters.
- Counts/ratios use int64/overflow-safe integer comparisons; disk uses overflow-checked uint64 bytes. Percentiles use finite float64 milliseconds and nearest-rank indexing. Process CPU uses seconds and cores, never an unspecified percentage. Window and lookback rows, offered/acknowledged/durable rows and p95 MISS versus invalid measurements are separate fields.
- Manifest version 1, batch format 4, product CalVer and benchmark result schema versions are independent. Product routes/JSON contracts, SQLite/Goose/sqlc, read-cache schema and existing engine/Parquet semantics stay unchanged. Benchmark artifacts use explicit snake_case fields and omit private paths/resources.
