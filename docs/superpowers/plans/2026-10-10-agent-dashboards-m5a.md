# Agent dashboards milestone 5, Phase A: metric fidelity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish S14–S16: preserve OTLP metric meaning in format 4, compute increases and elapsed-time rates per series, and derive histogram percentiles from observation weights.
**Architecture:** Extend the canonical metric row, physical Parquet schema and public metrics view together. One private SQL builder supplies deduplicated, ordered series points and increases to structured aggregates and distribution panels. Aggregate within each series before combining series; retain the existing executor, frame contracts and snapshot binder.
**Tech Stack:** Go 1.27.2, the pinned DuckDB 2 native engine through `bash scripts/with-duckdb.sh`, parquet-go, OTLP protobufs, database/sql, existing querytest fixtures, React/TypeScript, Bun/Vitest, and the existing documentation generator. No new dependencies.
**Spec:** docs/superpowers/specs/2026-10-10-agent-dashboards-m5-design.md

## Global Constraints

- This is a plan, not implementation or execution evidence. The planning dispatch writes only this file. No git writes, network, browser tools, CodeGraph initialization, questions or extra reports. The seven implementation tasks below describe future edits. The controller reviews and commits; commit steps contain messages only.
- The M5 spec and the Phase A brief explicitly supersede AGENTS.md's format-3-only sentence: Task 1 changes the sole supported format to 4 and updates that sentence. No format-3 reader, schema union, migration, conversion in Fanout, alias or fallback. Reject unsupported active and retired batches before cleanup/schema-batch replacement, preserve their bytes, and never classify unsupported formats or operational failures as corruption.
- Preserve typed VARIANT attributes/resource, literal dotted keys, signed nanoseconds, UTC on every connection, native `TIMESTAMPTZ_NS` predicates, `?::TIMESTAMP_NS::TIMESTAMPTZ_NS` window binds, footer-derived event-time bounds, native Parquet binding and `hive_partitioning=false`. Do not change the engine pin or its I/O defaults.
- No control-schema changes, telemetry value summaries, new caches, summary routing, DDSketch, system page, benchmark generator, paid eval or Phase B–E work. Keep SQLite/modernc, Goose/sqlc and existing read-cache version/semantics unchanged. Existing lifecycle and S1–S13 guards remain required.
- Commands below run from the repository root and use RTK. Before future Go commands, set this environment; the planning dispatch does not run Go or create caches:

  ```bash
  rtk mkdir -p .superpowers/gocache .superpowers/gotmp
  export GOCACHE="$PWD/.superpowers/gocache"
  export GOTMPDIR="$PWD/.superpowers/gotmp"
  export GOFLAGS=-mod=readonly
  export GOPROXY=off
  ```

  The verified justfile uses `bash scripts/with-duckdb.sh go test`. Targeted commands below use that exact wrapper. **Every task ends with `rtk just check`.** The controller supplies installed dependencies and runs any gate recipe that needs network outside the restricted dispatch; a blocked or skipped gate is not a pass. Do not inherit M4's instruction to omit the aggregate gate.
- All modified/new files use repository-relative paths, snake_case product JSON, plain-English Problems with hints, and no machine names, IPs, credentials or absolute local paths. New file paths below are proposals in existing verified directories; existing modify/test paths were read or listed in this checkout.
- SQL behavior tests execute the actual pinned engine through querytest tables and `Executor.Run`; snapshot tests additionally use the existing Parquet repository helper. No generated-SQL equality assertion is evidence of correctness. Existing span/log compiler tests continue to run.
- Decisions resolving prose ambiguities: (1) cumulative first points are baselines; delta first points contribute their value to sums and get elapsed time only from a usable non-null start time; subsequent deltas use a valid start time when supplied, otherwise the preceding point; (2) elapsed must be positive for rate, and points without elapsed remain eligible for sums; (3) finite scalar values are selected before lagging, so invalid values never poison a baseline; (4) histogram reset applies once to all channels on count decrease, bounds change or two non-null differing start times; a hist_sum decrease alone is not a reset because negative observations are possible; (5) unspecified/unknown OTLP aggregation temporality on sums/histograms is rejected as InvalidArgument, rather than guessed; (6) summary permits point-count operations (`count`, `count_distinct`, `share`), but all value aggregates return NULL; (7) the amended spec exempts count_distinct from the name rule, while share requires a fixed name or name grouping.
- Undefined kind/measure contributions stay NULL, including all-summary or all-incompatible groups. A real zero increase stays zero. Keep NULLs in frames; when every measure cell is NULL, report empty and run bounded kind diagnosis. Never convert an undefined value to zero or retain an old series in buckets with no point.

## Review Focus

1. A counter exported every 60 s viewed with 10 s buckets reads the same per-second rate as with 1 m buckets: Task 4, `TestMetricRateUsesCoveredElapsedTime`.
2. A container restart creates a new series mid-window: no spike from its first point, and the old series stops contributing: Task 3, `TestMetricIncreaseOrdersDeduplicatesAndResets`; Task 4, `TestMetricRestartDoesNotCarryOldSeries`.
3. Two points of one series at the same timestamp (exporter retry) do not create a reset or double the increase: Task 3, `TestMetricIncreaseOrdersDeduplicatesAndResets`.
4. A histogram whose bounds change mid-window (Collector reconfigured) treats the change as a reset instead of subtracting mismatched buckets: Task 3, `TestHistogramIncreaseResetsAllChannels`; Task 3, `TestHistogramPanelsShareResetIncreases`.
5. A dashboard variable for the metric name that has an All option does not satisfy the name rule; selecting All never sums different metrics: Task 4, `TestMetricNameRuleUsesCheckedEqualities` and `TestMetricNameAllGroupsDifferentMetricsSeparately`.

## Task 1: Format 4 metric columns and ingest kinds

**Files**

- Modify: `AGENTS.md`; `internal/telemetry/rows.go`, `internal/telemetry/parquet_rows.go`, `internal/telemetry/parquet.go`, `internal/telemetry/verify.go`; `internal/ingest/server.go`, `internal/ingest/nesting_test.go`; `internal/query/querytest/tables.go`, `internal/query/views.go`; `internal/panel/testhelpers_test.go`, `internal/panel/panel_bench_data_test.go`, `internal/panel/panel_bench_test.go`; `site/src/content/docs/explanation/storage-model.mdx`.
- Create/test: `internal/ingest/metric_fidelity_test.go`, `internal/telemetry/metric_format_test.go`, `internal/panel/metric_view_test.go`.
- Existing regression tests: `internal/telemetry/parquet_test.go`, `internal/telemetry/schema_batch_test.go`, `internal/telemetry/verify_test.go`, `internal/telemetry/store/repair_test.go`, `internal/query/views_schema_test.go`, `internal/query/rollup_test.go`, `internal/panel/distribution_test.go`. Update metric fixtures with accurate stored kind/temporality/monotonicity; do not rename visualization `histogram` to an OTLP kind.

**Interfaces**

Consumes (existing):

```go
func NewServer(cfg config.Config, submitter batchSubmitter) *Server
func (s *Server) exportMetrics(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error)
func makeMetricParquetRow(r Metric) metricParquetRow
func (p *ParquetStore) CommitBatch(ctx context.Context, metadata BatchMetadata, spans []Span, logs []Log, metrics []Metric) error
func OpenParquetStore(dir string) (*ParquetStore, error)
func ValidatePublishedBatch(dir string) (err error)
func CreateTables(db *sql.DB, createCaches func(*sql.DB) error) error // querytest
func CreateViews(db *sql.DB) error // query
func NewExecutor(engine Engine, retentionDays int) *Executor
func (e *Executor) Run(ctx context.Context, req RunRequest) ([]Result, error)
```

Produces: `telemetry.Metric.Temporality string`, `Monotonic bool`, `StartTimeUnixNanos int64`; matching Parquet fields; `batchMetadataVersion = 4`; metrics `type` in gauge/counter/updown/histogram/summary, `temporality VARCHAR`, nullable `start_time TIMESTAMPTZ_NS`. New ingest helper `func metricTemporality(t metricspb.AggregationTemporality) (string, error)`. New test-only shared helpers (fully defined below): `metricFixture`, `insertMetrics`, `runMetricPanel`, `metricValues`, `metricCounter`.

- [ ] **Write the failing ingest fidelity test.** In package ingest, import testing, telemetry/config, collectormetrics and metricspb under the same aliases as server.go. Capture through existing `newTestSubmitter`; this is not a fake SQL engine.

```go
func TestMetricIngestKeepsKindTemporalityAndStart(t *testing.T) {
    at, start := uint64(1790856000000000017), uint64(1790855940000000009)
    point := func() *metricspb.NumberDataPoint {
        return &metricspb.NumberDataPoint{TimeUnixNano: at, StartTimeUnixNano: start,
            Value: &metricspb.NumberDataPoint_AsInt{AsInt: 12}}
    }
    metrics := []*metricspb.Metric{
        {Name: "g", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{point()}}}},
        {Name: "c", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.NumberDataPoint{point()}}}},
        {Name: "d", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, DataPoints: []*metricspb.NumberDataPoint{point()}}}},
        {Name: "u", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.NumberDataPoint{point()}}}},
        {Name: "h", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, DataPoints: []*metricspb.HistogramDataPoint{{TimeUnixNano: at, StartTimeUnixNano: start, Count: 2, ExplicitBounds: []float64{1}, BucketCounts: []uint64{1, 1}}}}}},
        {Name: "e", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, DataPoints: []*metricspb.ExponentialHistogramDataPoint{{TimeUnixNano: at, StartTimeUnixNano: start, Count: 1, Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{BucketCounts: []uint64{1}}}}}}},
        {Name: "s", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{{TimeUnixNano: at, StartTimeUnixNano: start, Count: 1}}}}},
    }
    ch := make(chan telemetry.Metric, len(metrics))
    srv := NewServer(config.Config{DefaultNamespace: "test"}, newTestSubmitter(nil, nil, ch))
    _, err := srv.exportMetrics(t.Context(), &collectormetrics.ExportMetricsServiceRequest{
        ResourceMetrics: []*metricspb.ResourceMetrics{{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}}}},
    })
    if err != nil { t.Fatal(err) }
    kinds := []string{"gauge", "sum", "sum", "sum", "histogram", "exponential_histogram", "summary"}
    temps := []string{"", "cumulative", "delta", "cumulative", "delta", "cumulative", ""}
    for i := range metrics {
        got := <-ch
        if got.Type != kinds[i] || got.Temporality != temps[i] || got.Monotonic != (i == 1 || i == 2) || got.StartTimeUnixNanos != int64(start) {
            t.Fatalf("point %d: %+v", i, got)
        }
    }
}
```

- [ ] **Add format rejection tests, including byte preservation.** Package telemetry imports encoding/json, errors, os, filepath, testing. These tests deliberately modify only temporary fixtures. The existing `readOneParquetRow[T any](t *testing.T, path string) T` helper in parquet_test.go is reused for the physical-row round-trip.

```go
func TestMetricPhysicalRowKeepsFidelityColumns(t *testing.T) {
    store,err:=OpenParquetStore(t.TempDir());if err!=nil {t.Fatal(err)}
    defer store.Close()
    metric:=Metric{Name:"net",Type:"sum",Temporality:"delta",Monotonic:true,
        StartTimeUnixNanos:1790855940000000017,TimeUnixNanos:1790856000000000009,IngestedAt:1790856000000000010}
    if err:=store.CommitBatch(t.Context(),BatchMetadata{ID:"fidelity"},nil,nil,[]Metric{metric});err!=nil {t.Fatal(err)}
    got:=readOneParquetRow[metricParquetRow](t,filepath.Join(store.BatchPath("fidelity"),"metrics.parquet"))
    if got.MetricType!="sum"||got.Temporality!="delta"||!got.Monotonic||got.StartTimeUnixNano!=metric.StartTimeUnixNanos {t.Fatalf("physical=%+v",got)}
    if err:=ValidatePublishedBatch(store.BatchPath("fidelity"));err!=nil {t.Fatal(err)}
    if store.BatchMetadata()[0].Version!=4 {t.Fatal("metadata is not format 4")}
}

func TestFormatThreeIsRefusedBeforeCleanup(t *testing.T) {
    root := t.TempDir()
    store, err := OpenParquetStore(root)
    if err != nil { t.Fatal(err) }
    err = store.CommitBatch(t.Context(), BatchMetadata{ID: "old"}, nil, nil,
        []Metric{{Name: "n", Type: "gauge", TimeUnixNanos: 1, IngestedAt: 1}})
    if err != nil { t.Fatal(err) }
    if err := store.Close(); err != nil { t.Fatal(err) }
    metaPath := filepath.Join(root, "batches", "old.batch", "metadata.json")
    raw, err := os.ReadFile(metaPath)
    if err != nil { t.Fatal(err) }
    var meta BatchMetadata
    if err := json.Unmarshal(raw, &meta); err != nil { t.Fatal(err) }
    meta.Version = 3
    old, err := json.Marshal(meta)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(metaPath, old, 0o644); err != nil { t.Fatal(err) }
    staging := filepath.Join(root, "staging", "keep")
    if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(staging, []byte("preserve"), 0o644); err != nil { t.Fatal(err) }
    schemaPath := filepath.Join(root, "batches", SchemaBatch, "metrics.parquet")
    schemaBefore, err := os.ReadFile(schemaPath)
    if err != nil { t.Fatal(err) }
    if _, err := OpenParquetStore(root); !errors.Is(err, ErrUnsupportedBatchFormat) { t.Fatalf("open: %v", err) }
    if err := ValidatePublishedBatch(filepath.Dir(metaPath)); !errors.Is(err, ErrUnsupportedBatchFormat) || IsBatchCorrupt(err) { t.Fatalf("verify: %v", err) }
    for path, want := range map[string]string{metaPath: string(old), staging: "preserve", schemaPath: string(schemaBefore)} {
        got, err := os.ReadFile(path)
        if err != nil || string(got) != want { t.Fatalf("changed rejected input: %v", err) }
    }
}

func TestFormatFourRequiresNewMetricPhysicalSchema(t *testing.T) {
    root := t.TempDir()
    store, err := OpenParquetStore(root)
    if err != nil { t.Fatal(err) }
    if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "schema"}, nil, nil,
        []Metric{{Name: "n", Type: "sum", Monotonic: true, Temporality: "cumulative", TimeUnixNanos: 1, IngestedAt: 1}}); err != nil { t.Fatal(err) }
    dir := store.BatchPath("schema")
    if err := store.Close(); err != nil { t.Fatal(err) }
    // A column with the wrong physical type must not be accepted even with version 4.
    if err := writeTypedParquet(filepath.Join(dir, "metrics.parquet"),
        []struct { Temporality int64 `parquet:"temporality"` }{{1}}, 1024); err != nil { t.Fatal(err) }
    if err := ValidatePublishedBatch(dir); !errors.Is(err, ErrUnsupportedBatchFormat) || IsBatchCorrupt(err) { t.Fatalf("schema: %v", err) }
}
```

- [ ] **Define the shared engine-executed fixture helpers and metrics view test.** Add helpers to existing `testhelpers_test.go`; imports needed are database/sql, encoding/json and querytest, in addition to its existing imports. `query.Duck{DB: db}` has no repository and therefore executes these tables without Parquet rebinding; production snapshot coverage is a separate Task 3 test. The duckdb driver is already registered through query's imports.

```go
func metricFixture(t *testing.T) (*Executor, *sql.DB) {
    t.Helper()
    db, err := sql.Open("duckdb", "?threads=2&memory_limit=256MB")
    if err != nil { t.Fatal(err) }
    db.SetMaxOpenConns(1)
    t.Cleanup(func() { _ = db.Close() })
    if _, err := db.Exec("SET TimeZone='UTC'"); err != nil { t.Fatal(err) }
    if err := querytest.CreateTables(db, query.CreateCacheTables); err != nil { t.Fatal(err) }
    if err := query.CreateViews(db); err != nil { t.Fatal(err) }
    e := NewExecutor(&query.Duck{DB: db}, 30)
    e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
    return e, db
}

func insertMetrics(t *testing.T, db *sql.DB, m telemetry.Metric) {
    t.Helper()
    encode := func(v map[string]any) string {
        if v == nil { return "{}" }
        b, err := json.Marshal(v)
        if err != nil { t.Fatal(err) }
        return string(b)
    }
    _, err := db.Exec(`INSERT INTO telemetry.metrics
      (namespace,service,name,unit,metric_type,temporality,monotonic,start_time_unix_nano,
       metric_time,time_unix_nano,value,hist_bounds_json,hist_counts_json,hist_count,hist_sum,
       scope_name,scope_version,attributes,resource,ingested_at,ingested_unix_nano)
      VALUES (?,?,?,?,?,?,?,?,?::TIMESTAMP_NS,?,?,?,?,?,?,?, ?,?::JSON::VARIANT,?::JSON::VARIANT,?::TIMESTAMP_NS,?)`,
        m.Namespace, m.ServiceName, m.Name, m.Unit, m.Type, m.Temporality, m.Monotonic, m.StartTimeUnixNanos,
        time.Unix(0, m.TimeUnixNanos).UTC(), m.TimeUnixNanos, m.Value, m.HistBoundsJSON, m.HistCountsJSON, m.HistCount, m.HistSum,
        m.ScopeName, m.ScopeVersion, encode(m.Attributes), encode(m.Resource), time.Unix(0, m.IngestedAt).UTC(), m.IngestedAt)
    if err != nil { t.Fatal(err) }
}

func metricCounter(at time.Time, value float64) telemetry.Metric {
    return telemetry.Metric{Namespace: "test", ServiceName: "svc", Name: "net", Unit: "By",
        Type: "sum", Monotonic: true, Temporality: "cumulative", Value: value,
        TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano(),
        ScopeName: "scope", ScopeVersion: "1"}
}

func runMetricPanel(t *testing.T, e *Executor, p Panel, start, end time.Time) Result {
    t.Helper()
    e.now = func() time.Time { return end }
    if p.ID == "" { p.ID = "metric" }
    if p.Title == "" { p.Title = "Metric" }
    results, err := e.Run(t.Context(), RunRequest{Dashboard: Dashboard{
        Name: "Metrics", Time: Time{From: &start, To: &end}, Panels: []Panel{p},
    }})
    if err != nil { t.Fatal(err) }
    if len(results) != 1 || results[0].Status == StatusError { t.Fatalf("result: %+v", results) }
    return results[0]
}

func metricValues(t *testing.T, r Result, column string) []any {
    t.Helper()
    if r.Frame == nil { t.Fatalf("missing frame: %+v", r) }
    for i, c := range r.Frame.Columns { if c.Name == column { return r.Frame.Values[i] } }
    t.Fatalf("missing column %s", column)
    return nil
}

func TestMetricViewMapsKindsAndKeepsNanosecondStart(t *testing.T) {
    e, db := metricFixture(t)
    for i, stored := range []string{"gauge", "sum", "sum", "histogram", "exponential_histogram", "summary"} {
        m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Second), 1)
        m.Type, m.Monotonic = stored, i == 1
        m.Name = fmt.Sprintf("metric%d", i)
        m.StartTimeUnixNanos = fixtureStart.Add(-time.Minute).UnixNano()+17
        if i == 0 { m.StartTimeUnixNanos = 0; m.Temporality = "" }
        if i == 5 { m.Temporality = "" }
        insertMetrics(t, db, m)
    }
    r := runMetricPanel(t, e, Panel{Viz: "table", SQL: `SELECT name,type,temporality,
        epoch_ns(start_time)::DOUBLE AS start_nanos,typeof(start_time) AS start_type
        FROM metrics WHERE $__window(time) ORDER BY name`}, fixtureStart, fixtureStart.Add(time.Minute))
    if got := metricValues(t, r, "type"); !reflect.DeepEqual(got, []any{"gauge","counter","updown","histogram","histogram","summary"}) { t.Fatalf("kinds: %v", got) }
    if metricValues(t, r, "start_nanos")[0] != nil { t.Fatal("zero start must be NULL") }
    for _, got := range metricValues(t, r, "start_type") { if got != "TIMESTAMPTZ_NS" { t.Fatalf("start type: %v", got) } }
    var exact int64
    if err := db.QueryRow("SELECT epoch_ns(start_time) FROM metrics WHERE name='metric1'").Scan(&exact); err != nil || exact != fixtureStart.Add(-time.Minute).UnixNano()+17 { t.Fatalf("nanoseconds=%d err=%v", exact, err) }
}
```

- [ ] **Run red.** The new fidelity tests initially fail to compile because the canonical fields do not exist. After adding the fields only, rerun to observe missing metadata/incorrect kinds; the legacy rejection test fails because version 3 is accepted.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/ingest ./internal/telemetry ./internal/panel -run 'TestMetricIngestKeepsKindTemporalityAndStart|TestMetricPhysicalRowKeepsFidelityColumns|TestFormatThreeIsRefusedBeforeCleanup|TestFormatFourRequiresNewMetricPhysicalSchema|TestMetricViewMapsKindsAndKeepsNanosecondStart' -count=1
```

- [ ] **Implement the format and mapping.** Add the three fields to Metric and the physical row, assign them in `makeMetricParquetRow`, and add these definitions to querytest's table and query's view respectively:

```go
// metricParquetRow additions:
Temporality string `parquet:"temporality,dict"`
Monotonic bool `parquet:"monotonic"`
StartTimeUnixNano int64 `parquet:"start_time_unix_nano"`
```

```sql
-- querytest table additions
temporality VARCHAR,
monotonic BOOLEAN,
start_time_unix_nano BIGINT,
-- replace metric_type AS type in viewMetrics; add temporality and start_time
CASE WHEN metric_type='sum' AND monotonic THEN 'counter'
     WHEN metric_type='sum' THEN 'updown'
     WHEN metric_type='exponential_histogram' THEN 'histogram'
     ELSE metric_type END AS type,
temporality,
CASE WHEN start_time_unix_nano=0 THEN NULL
     ELSE make_timestamp_ns(start_time_unix_nano)::TIMESTAMPTZ_NS END AS start_time,
```

Set `batchMetadataVersion` to 4. `loadStoredBatch` already compares the complete `telemetrySchema[metricParquetRow]()` and `BatchVerifier.Validate` calls `loadRegisteredBatch` before decoding: extend the row contract, retain this one verification path and the native all-column decode, rather than invent another schema copy. Update the comments in parquet.go/views.go, AGENTS.md and both batch-format references in `site/src/content/docs/explanation/storage-model.mdx` (near lines 110 and 165) to format 4.

```go
func metricTemporality(t metricspb.AggregationTemporality) (string, error) {
    switch t {
    case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:
        return "cumulative", nil
    case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:
        return "delta", nil
    default:
        return "", fmt.Errorf("sum and histogram metrics require cumulative or delta temporality")
    }
}
```

In all five point loops set `StartTimeUnixNanos: int64(dp.StartTimeUnixNano)`. Gauges and summaries leave temporality empty and monotonic false. For sums call metricTemporality once before the point loop, return `status.Error(codes.InvalidArgument, err.Error())` on error, store `Type: "sum"`, `Temporality: temporality`, `Monotonic: d.Sum.IsMonotonic`; delete the sum_delta branch. For explicit/exponential histograms call the helper on their own aggregation temporality, set Temporality and keep Monotonic false; exponential kind is exactly `exponential_histogram`.

Update `TestExportsBoundAttributeNestingBeforeSubmit` in `internal/ingest/nesting_test.go`: set `AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE` on its Sum, Histogram and ExponentialHistogram fixtures. These remain valid nesting tests; do not relax the `InvalidArgument` rejection of an export containing unspecified aggregate temporality.

Keep `TestPanelBenchmarkMetricNamesSelectMostCommonInWindow` green in this first type-changing task. In its `add` helper set sums to `Monotonic: true, Temporality: "cumulative"`, histograms to cumulative temporality, and emit at least two points per aggregate series with identical service/name/unit/scope/attributes/resource. Keep the first at `at` and the second at `at.Add(time.Minute)` with increased Value, HistCount, HistSum and bucket counts; assign all event/ingestion timestamps consistently. Gauges still emit one point. Change the expected selection map key from `sum` to `counter`; keep requests more frequent than rare, histogram-name tie ordering, outside-window exclusions and the final narrow-window assertions. The second points also supply histogram baselines when Task 3 switches distributions. Include this regression in Task 1's readbench command; it must not be deferred until Task 4.

Task 1's mechanical reader audit uses the brief's search, plus all stored-type/format literals. `metricHistogramRaw` still selects semantic `histogram`; that selector now includes exponential points. Change readbench metadata checks/outputs to 4, selectors/maps from `sum` to `counter` (include updown when choosing gauge-like measures), histogram benchmark from `p95(hist_sum)` to `p95(value)`, and shift `start_time_unix_nano` only when nonzero in `tilePanelParquet`. Do not infer monotonicity inside a production reader. Task 7 consumes data supplied by private tooling outside the repository; this plan implements no replay or lost-fidelity upgrade.

```bash
rtk proxy rg -n "sum_delta|'histogram'|\"histogram\"" internal ui
rtk proxy rg -n 'metric_type|exp_histogram|Version: 3|Version != 3|format-3|format 3' internal ui site/src/content/docs/explanation/storage-model.mdx AGENTS.md
```

Every stored/view type consumer found by these reads must use the new distinction in this task; negative tests and historical migration descriptions may name rejected versions. Keep existing test fixture kind and temporality truthful even while Task 3 still has to remove the old panel option.

- [ ] **Run green and the complete gate.** The targeted command above must pass; verify readbench setup also compiles/runs after the version change.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/telemetry/... ./internal/ingest ./internal/query ./internal/panel -count=1
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run 'TestPanelBenchmarkReplay|TestPanelBenchmarkMetricNamesSelectMostCommonInWindow' -count=1
rtk just check
```

- [ ] **Controller commit message:** `feat(telemetry): preserve metric semantics in batch format 4`

## Task 2: Exponential histogram conversion

**Files:** Modify `internal/ingest/server.go`, `internal/ingest/server_test.go`; create/test `internal/ingest/exponential_histogram_test.go`. Retain Task 1's ingest round-trip test.

**Interfaces**

Consumes (existing): `func expHistBuckets(dp *metricspb.ExponentialHistogramDataPoint) []float64`, `func expHistCounts(dp *metricspb.ExponentialHistogramDataPoint) []uint64`, and `func toJSON(v interface{}) string`.

Produces (new): `func explicitExpHistogram(dp *metricspb.ExponentialHistogramDataPoint) ([]float64, []uint64)`. Delete both old helpers and their callers after introducing this single paired conversion; exportMetrics calls it once per point and serializes both results.

- [ ] **Write the failing count-preservation test.** Package ingest; imports math, reflect, testing and metricspb.

```go
func TestExponentialHistogramRetainsEveryObservation(t *testing.T) {
    for _, tc := range []struct {
        name string; scale, positiveOffset, negativeOffset int32; positive, negative []uint64; zero uint64
        bounds []float64; counts []uint64
    }{
        {"scale_zero_negative_offset", 0, -1, 0, []uint64{2,3}, []uint64{4,1}, 6, []float64{-4,-2,-1,.5,1,2}, []uint64{0,1,4,6,2,3,0}},
        {"scale_three_positive_offset", 3, 2, -1, []uint64{2,3}, []uint64{4}, 1, []float64{-1,-math.Pow(math.Pow(2,math.Pow(2,-3)), -1),math.Pow(math.Pow(2,math.Pow(2,-3)),2),math.Pow(math.Pow(2,math.Pow(2,-3)),3),math.Pow(math.Pow(2,math.Pow(2,-3)),4)}, []uint64{0,4,1,2,3,0}},
        {"scale_negative_two", -2, 1, 0, []uint64{7,9}, nil, 0, []float64{16,256,4096}, []uint64{0,7,9,0}},
        {"zero_only", 0, 0, 0, nil, nil, 9, []float64{0}, []uint64{9,0}},
        {"negative_only", 0, 0, 1, nil, []uint64{2,6}, 0, []float64{-8,-4,-2,0}, []uint64{0,6,2,0,0}},
        {"negative_and_zero", 0, 0, 1, nil, []uint64{2,6}, 3, []float64{-8,-4,-2,0}, []uint64{0,6,2,3,0}},
        {"positive_only", 0, 0, 0, []uint64{2,3}, nil, 4, []float64{1,2,4}, []uint64{4,2,3,0}},
        {"empty", 0, 0, 0, nil, nil, 0, []float64{0}, []uint64{0,0}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            dp := &metricspb.ExponentialHistogramDataPoint{Scale: tc.scale, ZeroCount: tc.zero,
                Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: tc.positiveOffset, BucketCounts: tc.positive},
                Negative: &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: tc.negativeOffset, BucketCounts: tc.negative}}
            dp.Count = tc.zero
            for _, n := range tc.positive { dp.Count += n }
            for _, n := range tc.negative { dp.Count += n }
            bounds, counts := explicitExpHistogram(dp)
            if len(counts) != len(bounds)+1 || !reflect.DeepEqual(counts, tc.counts) { t.Fatalf("bounds=%v counts=%v", bounds, counts) }
            if len(bounds) != len(tc.bounds) { t.Fatalf("bounds=%v", bounds) }
            for i, want := range tc.bounds { if math.Abs(bounds[i]-want) > math.Abs(want)*1e-12 { t.Fatalf("bound %d=%g want %g", i, bounds[i], want) } }
            for i:=1;i<len(bounds);i++ { if bounds[i]<=bounds[i-1] {t.Fatalf("bounds are not strictly ascending: %v",bounds)} }
            var total uint64
            for _, n := range counts { total += n }
            if total != dp.Count { t.Fatalf("total=%d count=%d", total, dp.Count) }
        })
    }
}
```

- [ ] **Run red:** the new paired helper is undefined. Defining its signature with the previous conversion exposes wrong lengths and lost zero/negative counts.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/ingest -run TestExponentialHistogramRetainsEveryObservation -count=1
```

- [ ] **Implement the paired conversion and replace the nil-output tests.** Preserve the true negative grid from the amended spec: with base b, negative offset on and m counts, emit `-b^(on+m)` through `-b^on` in ascending order, with counts `[0,q[m-1],...,q[0]]`. Then emit the positive grid `b^op` through `b^(op+n)` with counts `[z,p[0],...,p[n-1],0]`. If n=0 the positive part has just bound 0 and counts `[z,0]`; if m=0 omit the negative prefix. Counts have one more entry than bounds and sum exactly to the point count. Never fold negative observations into the zero bucket.

```go
func explicitExpHistogram(dp *metricspb.ExponentialHistogramDataPoint) ([]float64, []uint64) {
    base := math.Pow(2, math.Pow(2, -float64(dp.Scale)))
    bounds := make([]float64, 0)
    counts := make([]uint64, 0)
    if dp.Negative != nil && len(dp.Negative.BucketCounts)>0 {
        negative := dp.Negative.BucketCounts
        counts = append(counts, 0)
        for i:=len(negative);i>=0;i-- {
            bounds = append(bounds, -math.Pow(base,float64(dp.Negative.Offset)+float64(i)))
            if i>0 { counts = append(counts, negative[i-1]) }
        }
    }
    counts = append(counts, dp.ZeroCount)
    if dp.Positive == nil || len(dp.Positive.BucketCounts)==0 {
        bounds = append(bounds, 0)
    } else {
        for i:=0;i<=len(dp.Positive.BucketCounts);i++ {
            bounds = append(bounds, math.Pow(base,float64(dp.Positive.Offset)+float64(i)))
        }
        counts = append(counts, dp.Positive.BucketCounts...)
    }
    counts = append(counts, 0)
    return bounds, counts
}
```
In the exponential point loop use `bounds, counts := explicitExpHistogram(dp)` and `toJSON(bounds)`, `toJSON(counts)`. Remove `TestExpHistBuckets`/`TestExpHistCounts` (which currently expect nil for zero-only data) in favor of the full conversion test. Task 3 executes the converted shape through distribution panels; Task 5 adds percentile execution, including an all-negative result.

- [ ] **Run green:** the conversion and full ingest tests pass; run the gate.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/ingest -run 'TestExponentialHistogramRetainsEveryObservation|TestMetricIngestKeepsKindTemporalityAndStart' -count=1
rtk just check
```

- [ ] **Controller commit message:** `fix(ingest): retain all exponential histogram counts`

## Task 3: Shared series-increase SQL

**Files:** Create `internal/panel/metric_series.go`, `internal/panel/metric_series_test.go`; modify `internal/panel/exec.go`, `internal/panel/table_trends.go`, `internal/panel/distribution.go`, `internal/panel/spec.go`, `internal/panel/validate.go`, `internal/panel/distribution_test.go`, `internal/panel/testhelpers_test.go`, `internal/mcp/panels.go`, `internal/mcp/panels_distribution_test.go`, `ui/panels/types.ts`; regenerate `site/src/content/docs/reference/mcp-tools.mdx` via `cmd/fanout-docgen` and refresh UI embeds only if their bytes change. Regenerate `scripts/dashboard-eval/testdata/server.json` through its recipe if the description guard requires it. Test strict decoding in `internal/api/panels.go`, the MCP examples, and existing `internal/panel/testhelpers_test.go` and `internal/query/file_snapshot_test.go` behavior through a real panel request.

**Interfaces**

Consumes: `func buildWhere(sig *signal, filters []Filter, scope Scope) (string, []any, error)`, `func sqlFloat(v float64) string`, existing `Scope`, `Compiled`, `Engine` and `queryrows.WithWindow`.

Produces (new):

```go
const metricLookback = 10 * time.Minute
const metricSeriesKey = "namespace,service,name,unit,scope_name,scope_version,attributes,resource"
func metricReadScope(p *Panel, scope Scope) Scope
func metricSeriesCTE(filters []Filter, scope Scope) (string, []any, error)
func metricHistogramRaw(dimSelect string, filters []Filter, scope Scope) (string, []any, error)
```

The CTE defines `metric_input` (raw lookback rows), `metric_points` (finite, valid, unique timestamp rows), and `metric_increases` (in-window increases). Output retains identity/grouping fields and exposes `_bucket BIGINT`, `increase DOUBLE`, `elapsed_s DOUBLE`, `count_increase DOUBLE`, `sum_increase DOUBLE`, `bound_increases HUGEINT[]`, `_bounds DOUBLE[]`. A cumulative first point stays in metric_points for latest-value operations but contributes no increase. Delta first points are retained with nullable elapsed. No public API or dependency is added. New test-only helpers are `func bindFixtureTimes(t *testing.T, text string, args []any) string` and `func metricIncreaseResult(t *testing.T, e *Executor, scope Scope, selectSQL string) Result`.

Replace the old `metricHistogramRaw(dimSelect, where, temporality string, interval time.Duration, heat bool) string` signature entirely in this task. `Histogram` keeps only `Field string` and `Buckets string`; remove `Temporality` and its authored option across Go, MCP, TS, generated contracts and tests here. The builder has a live production caller through `compileDistribution`, so `just check`'s `scripts/go-deadcode.mjs` (`deadcode -test=false`) does not reject a test-only builder. Task 5 adds histogram value measures.

- [ ] **Write the failing builder tests.** Package panel imports bytes, encoding/json, fmt, reflect, strings, testing, time, telemetry and telemetrystore. Execute builder results through SQL panels on the querytest engine; test SQL is the builder plus a projection, not an assertion about its text. Helpers are defined here.

```go
func bindFixtureTimes(t *testing.T, text string, args []any) string {
    t.Helper()
    // Builder tests have no authored filters; their four arguments are times.
    // Render only these trusted fixture instants into a guarded SQL panel.
    for _,arg:=range args {
        at,ok:=arg.(time.Time);if !ok {t.Fatalf("fixture argument is %T",arg)}
        text=strings.Replace(text,"?",fmt.Sprintf("make_timestamp_ns(%d)",at.UnixNano()),1)
    }
    if strings.Contains(text,"?") {t.Fatal("unbound fixture instant")}
    return text
}

func metricIncreaseResult(t *testing.T, e *Executor, scope Scope, selectSQL string) Result {
    t.Helper()
    cte, args, err := metricSeriesCTE(nil, scope)
    if err != nil { t.Fatal(err) }
    return runMetricPanel(t,e,Panel{Viz:"table",SQL:bindFixtureTimes(t,cte+selectSQL,args)},
        scope.Start.Add(-metricLookback),scope.End)
}

func TestMetricIncreaseOrdersDeduplicatesAndResets(t *testing.T) {
    e, db := metricFixture(t)
    at := fixtureStart
    for _, p := range []struct{ seconds int; value float64; ingest int64; start int64 }{
        {60,160,60,1}, {-60,40,-60,1}, {0,100,0,1},
        {60,10,59,1}, // earlier retry at the same timestamp must lose
        {120,5,120,1}, // value decrease: reset contributes 5
        {180,20,180,2}, // start change without decrease: reset contributes 20
        {240,50,240,0}, // absent start does not by itself reset
    } {
        m := metricCounter(at.Add(time.Duration(p.seconds)*time.Second),p.value)
        m.IngestedAt = at.Add(time.Duration(p.ingest)*time.Second).UnixNano()
        m.StartTimeUnixNanos = p.start
        insertMetrics(t,db,m)
    }
    // New resource identity is a new container series; its first point is not traffic.
    for i, value := range []float64{9000,9060} {
        m := metricCounter(at.Add(time.Duration(150+i*60)*time.Second),value)
        m.Resource = map[string]any{"container.id":"new"}
        insertMetrics(t,db,m)
    }
    r := metricIncreaseResult(t,e,Scope{Start:at,End:at.Add(5*time.Minute),Interval:time.Minute},
        " SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,increase,elapsed_s FROM metric_increases ORDER BY time")
    if got := metricValues(t,r,"increase"); !reflect.DeepEqual(got,[]any{60.0,60.0,5.0,20.0,60.0,30.0}) { t.Fatalf("increase=%v",got) }
    if got := metricValues(t,r,"elapsed_s"); !reflect.DeepEqual(got,[]any{60.0,60.0,60.0,60.0,60.0,60.0}) { t.Fatalf("elapsed=%v",got) }
}

func TestDeltaFirstPointNeedsElapsedOnlyForRate(t *testing.T) {
    e,db := metricFixture(t)
    for i := range 3 {
        m := metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),12)
        m.Temporality = "delta"
        if i == 2 { m.StartTimeUnixNanos = fixtureStart.Add(90*time.Second).UnixNano() }
        insertMetrics(t,db,m)
    }
    r := metricIncreaseResult(t,e,Scope{Start:fixtureStart,End:fixtureStart.Add(3*time.Minute)},
        " SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,increase,elapsed_s FROM metric_increases ORDER BY time")
    if !reflect.DeepEqual(metricValues(t,r,"increase"),[]any{12.0,12.0,12.0}) ||
       !reflect.DeepEqual(metricValues(t,r,"elapsed_s"),[]any{nil,60.0,30.0}) { t.Fatalf("delta=%+v",r.Frame) }
}

func TestMetricSeriesIdentitySeparatesEveryField(t *testing.T) {
    for _,field:=range []string{"namespace","service","name","unit","scope_name","scope_version","attributes","resource"} {
        t.Run(field,func(t *testing.T) {
            e,db:=metricFixture(t)
            insertMetrics(t,db,metricCounter(fixtureStart.Add(-time.Minute),0))
            for i,value:=range []float64{9000,9060} {
                m:=metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),value)
                switch field {
                case "namespace":m.Namespace="other"
                case "service":m.ServiceName="other"
                case "name":m.Name="other"
                case "unit":m.Unit="other"
                case "scope_name":m.ScopeName="other"
                case "scope_version":m.ScopeVersion="other"
                case "attributes":m.Attributes=map[string]any{"literal.dot":int64(1)}
                case "resource":m.Resource=map[string]any{"literal.dot":true}
                }
                insertMetrics(t,db,m)
            }
            r:=metricIncreaseResult(t,e,Scope{Start:fixtureStart,End:fixtureStart.Add(2*time.Minute)},
                " SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,increase,elapsed_s FROM metric_increases ORDER BY time")
            if !reflect.DeepEqual(metricValues(t,r,"increase"),[]any{60.0}) {t.Fatalf("identity=%+v",r.Frame)}
        })
    }
}

func TestHistogramIncreaseResetsAllChannels(t *testing.T) {
    e,db := metricFixture(t)
    for i,p := range []struct{ bounds,counts string; count int64; sum float64; start int64 }{
        {"[10,20]","[10,20,30]",60,100,1},
        {"[10,20]","[12,23,35]",70,130,1},
        {"[5,20]","[20,30,40]",90,200,1}, // bounds change while total increases
        {"[5,20]","[1,2,3]",6,12,1}, // count decrease
        {"[5,20]","[2,3,4]",9,18,2}, // start change
    } {
        m:=metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),0)
        m.Type,m.Monotonic="histogram",false
        m.HistBoundsJSON,m.HistCountsJSON,m.HistCount,m.HistSum,m.StartTimeUnixNanos=p.bounds,p.counts,p.count,p.sum,p.start
        insertMetrics(t,db,m)
    }
    cte,args,err:=metricSeriesCTE(nil,Scope{Start:fixtureStart,End:fixtureStart.Add(5*time.Minute),Interval:time.Minute})
    if err!=nil { t.Fatal(err) }
    r:=runMetricPanel(t,e,Panel{Viz:"table",SQL:bindFixtureTimes(t,cte+" SELECT count_increase,sum_increase,to_json(bound_increases) AS weights FROM metric_increases ORDER BY time",args)},fixtureStart.Add(-metricLookback),fixtureStart.Add(5*time.Minute))
    wantCounts:=[]float64{10,90,6,9}; wantSums:=[]float64{30,200,12,18}
    wantBounds:=[][]float64{{2,3,5},{20,30,40},{1,2,3},{2,3,4}}
    if r.Frame.Rows!=4 {t.Fatalf("rows=%d",r.Frame.Rows)}
    for i,weights:=range metricValues(t,r,"weights") {
        var got []float64;if err:=json.Unmarshal([]byte(weights.(string)),&got);err!=nil {t.Fatal(err)}
        if metricValues(t,r,"count_increase")[i]!=wantCounts[i]||metricValues(t,r,"sum_increase")[i]!=wantSums[i]||!reflect.DeepEqual(got,wantBounds[i]) {t.Fatalf("row %d: %+v",i,r.Frame)}
    }
}
```

The builder tests above execute real guarded SQL panels through Executor.Run on querytest tables. The integration test below additionally exercises the production file snapshot; Task 3 connects metric distributions to these CTEs; Tasks 4–5 add aggregate measures.

- [ ] **Add the lookback snapshot regression.** This test is written now and becomes green when Task 4 connects rate compilation; until then, its Task 3 assertion uses a SQL panel with an explicit extended input window and original output window, plus the `metricReadScope` result. Do not leave the test red across task commits.

```go
func TestMetricReadScopeIncludesSeparateLookbackBatch(t *testing.T) {
    engine,repo:=newTestEngine(t)
    at:=fixtureStart
    for i,p:=range []struct{offset time.Duration; value float64}{{-time.Minute,100},{0,160},{time.Minute,220}} {
        if err:=repo.Commit(t.Context(),telemetrystore.Batch{ID:fmt.Sprintf("lookback%d",i),Metrics:[]telemetry.Metric{metricCounter(at.Add(p.offset),p.value)}});err!=nil {t.Fatal(err)}
    }
    p:=Panel{ID:"rate",Title:"Rate",Viz:"timeseries",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate()"},Bucket:"1m"}}
    original:=Scope{Start:at,End:at.Add(2*time.Minute),Interval:time.Minute}
    read:=metricReadScope(&p,original)
    if !read.Start.Equal(at.Add(-10*time.Minute))||!original.Start.Equal(at) {t.Fatalf("read=%+v original=%+v",read,original)}
    e:=NewExecutor(engine,30)
    cte,args,err:=metricSeriesCTE(nil,original);if err!=nil {t.Fatal(err)}
    built:=runMetricPanel(t,e,Panel{Viz:"table",SQL:bindFixtureTimes(t,cte+" SELECT _bucket AS time,increase,elapsed_s FROM metric_increases ORDER BY time",args)},read.Start,read.End)
    if built.Frame.Rows!=2||metricValues(t,built,"increase")[0]!=60.0 {t.Fatalf("lookback frame=%+v",built.Frame)}
    // A real executor SQL panel independently confirms baseline visibility.
    r:=runMetricPanel(t,e,Panel{Viz:"table",SQL:`WITH prior AS (
        SELECT time,value,lag(value) OVER (ORDER BY time) AS previous FROM metrics
        WHERE time >= $__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS
        AND name='net') SELECT value-previous AS increase FROM prior
        WHERE time >= CAST('2026-10-01 12:00:00' AS TIMESTAMPTZ_NS) ORDER BY time`},read.Start,read.End)
    if !reflect.DeepEqual(metricValues(t,r,"increase"),[]any{60.0,60.0}) {t.Fatalf("snapshot=%+v",r.Frame)}
}
```

Once Task 4 lands, append a structured `runMetricPanel` with panel p and original bounds expecting rates `[1,1]`; retain the builder SQL panel snapshot test. queryrows is needed by production code, not by these test helpers; do not leave an unused import in the test file.

- [ ] **Run red:** builder/read-scope symbols do not exist; then run again with their signatures to observe missing dedup/reset/lookback behavior.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel -run 'TestMetricIncreaseOrdersDeduplicatesAndResets|TestMetricSeriesIdentitySeparatesEveryField|TestDeltaFirstPointNeedsElapsedOnlyForRate|TestHistogramIncreaseResetsAllChannels|TestMetricReadScopeIncludesSeparateLookbackBatch' -count=1
```

- [ ] **Implement the single builder.** Package panel imports fmt, strings and time. Use validated filters through buildWhere; do not concatenate author text. Only bucket expressions cast time for datetime functions; both input and output predicates remain nanosecond instants.

```go
const metricLookback = 10 * time.Minute
const metricSeriesKey = "namespace,service,name,unit,scope_name,scope_version,attributes,resource"

func metricReadScope(p *Panel, scope Scope) Scope {
    if p.Query != nil && p.Query.From == "metrics" { scope.Start = scope.Start.Add(-metricLookback) }
    return scope
}

func metricSeriesCTE(filters []Filter, scope Scope) (string, []any, error) {
    read := scope
    read.Start = read.Start.Add(-metricLookback)
    where,args,err := buildWhere(signals["metrics"],filters,read)
    if err != nil { return "",nil,err }
    bucket := "0::BIGINT"
    if scope.Interval > 0 {
        bucket = fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',time::TIMESTAMP_NS))::BIGINT",int64(scope.Interval/time.Second))
    }
    text := `WITH metric_input AS (SELECT * FROM metrics WHERE `+where+`),
      metric_decoded AS (SELECT *,
        TRY(from_json(hist_bounds_json,'["DOUBLE"]')) AS _bounds,
        TRY(from_json(hist_counts_json,'["UBIGINT"]')) AS _counts
        FROM metric_input),
      metric_valid AS (SELECT * FROM metric_decoded WHERE
        (type<>'histogram' AND isfinite(value)) OR
        (type='histogram' AND isfinite(hist_sum) AND hist_count>=0
         AND _bounds IS NOT NULL AND _counts IS NOT NULL
         AND len(_counts)=len(_bounds)+1
         AND coalesce(list_bool_and(list_transform(_bounds,lambda x: isfinite(x))),true))),
      metric_unique AS (SELECT * EXCLUDE (_retry) FROM (
        SELECT *,row_number() OVER (PARTITION BY `+metricSeriesKey+`,time ORDER BY
          ingested_unix_nano DESC,value DESC,hist_count DESC,hist_sum DESC,
          hist_counts_json DESC,hist_bounds_json DESC,start_time DESC NULLS LAST) AS _retry
        FROM metric_valid) WHERE _retry=1),
      metric_points AS (SELECT *,`+bucket+` AS _bucket FROM metric_unique),
      metric_previous AS (SELECT *,
        lag(time) OVER w AS _previous_time,
        lag(value) OVER w AS _previous_value,
        lag(start_time) OVER w AS _previous_start,
        lag(hist_count) OVER w AS _previous_count,
        lag(hist_sum) OVER w AS _previous_sum,
        lag(_bounds) OVER w AS _previous_bounds,
        lag(_counts) OVER w AS _previous_counts
        FROM metric_points WINDOW w AS (PARTITION BY `+metricSeriesKey+` ORDER BY time,ingested_unix_nano)),
      metric_reset AS (SELECT *,
        ((start_time IS NOT NULL AND _previous_start IS NOT NULL AND start_time<>_previous_start)
         OR CASE WHEN type='histogram' THEN hist_count<_previous_count OR _bounds<>_previous_bounds
                 ELSE value<_previous_value END) AS _reset,
        CASE WHEN temporality='delta' AND start_time IS NOT NULL AND start_time<time THEN start_time
             ELSE _previous_time END AS _elapsed_from
        FROM metric_previous),
      metric_increases AS (SELECT *,
        CASE WHEN type='counter' THEN CASE WHEN temporality='delta' OR _reset THEN value ELSE value-_previous_value END END::DOUBLE AS increase,
        CASE WHEN type='histogram' THEN CASE WHEN temporality='delta' OR _reset THEN hist_count ELSE hist_count-_previous_count END END::DOUBLE AS count_increase,
        CASE WHEN type='histogram' THEN CASE WHEN temporality='delta' OR _reset THEN hist_sum ELSE hist_sum-_previous_sum END END::DOUBLE AS sum_increase,
        CASE WHEN type='histogram' THEN list_transform(range(1,len(_counts)+1),lambda i:
          CASE WHEN temporality='delta' OR _reset THEN _counts[i]::HUGEINT
               ELSE _counts[i]::HUGEINT-_previous_counts[i]::HUGEINT END) END AS bound_increases,
        CASE WHEN _elapsed_from<time THEN (epoch_ns(time)::HUGEINT-epoch_ns(_elapsed_from)::HUGEINT)::DOUBLE/1e9 END AS elapsed_s
        FROM metric_reset
        WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
          AND type IN ('counter','histogram') AND temporality IN ('delta','cumulative')
          AND (temporality='delta' OR _previous_time IS NOT NULL))`
    args = append(args,scope.Start.UTC(),scope.End.UTC())
    return strings.TrimSpace(text),args,nil
}
```

Explicit histograms with no finite bounds (`[]`) remain valid; an empty bound list is not invalid data. Equal timestamps are deduplicated before any lag. Equal ingestion timestamps have the deterministic ordering shown in metric_unique rather than depending on file order; exact duplicates are indistinguishable.

Apply `metricReadScope` at `runScope`'s WithWindow call. Do not alter logical scope.Start/End passed to compilation, comparison, totals, annotation matching or response FromMS/ToMS. The metrics branch of table trends must use the same read scope when it starts running metric aggregates in Task 4. Diagnostic point counts, variable option discovery and schema discovery retain their visible windows because they do not compute increases. No time widening for arbitrary SQL panels.

- [ ] **Switch metric distribution panels to the builder now.** This is Task 3's production integration, with reset/first-delta and strict option-removal tests. Put the shared `metricHistogram` helper in `testhelpers_test.go` so Task 5 and readbench reuse it without milestone names. Add these tests to `metric_series_test.go` (bytes/json/reflect/strings imports are required); they do not call Task 4's scalar-measure helper.

```go
func metricHistogram(at time.Time, bounds,counts string, count int64, sum float64, series string) telemetry.Metric {
    m:=metricCounter(at,0)
    m.Type,m.Monotonic="histogram",false
    m.HistBoundsJSON,m.HistCountsJSON,m.HistCount,m.HistSum=bounds,counts,count,sum
    m.Attributes=map[string]any{"series":series}
    return m
}

func TestHistogramPanelsShareResetIncreases(t *testing.T) {
    e,db:=metricFixture(t)
    for _,m:=range []telemetry.Metric{
        metricHistogram(fixtureStart.Add(-time.Minute),"[10,20]","[10,20,30]",60,100,"a"),
        metricHistogram(fixtureStart,"[10,20]","[12,23,35]",70,130,"a"),
        metricHistogram(fixtureStart.Add(time.Minute),"[5,20]","[20,30,40]",90,200,"a"),
        metricHistogram(fixtureStart.Add(2*time.Minute),"[5,20]","[1,2,3]",6,12,"a"),
    } {insertMetrics(t,db,m)}
    for _,viz:=range []string{"histogram","heatmap"} {
        q:=&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"count()"},Histogram:&Histogram{Field:"value",Buckets:"explicit"}}
        if viz=="heatmap" {q.Bucket="1m"}
        r:=runMetricPanel(t,e,Panel{Viz:viz,Query:q},fixtureStart,fixtureStart.Add(3*time.Minute))
        total:=0.0;for _,n:=range metricValues(t,r,"count") {total+=n.(float64)}
        if total!=106 {t.Fatalf("%s count=%g want 106",viz,total)}
        if viz=="heatmap" {
            times:=metricValues(t,r,"time");counts:=metricValues(t,r,"count")
            buckets:=map[int64]float64{};for i,n:=range counts {buckets[times[i].(int64)]+=n.(float64)}
            if buckets[fixtureStart.UnixMilli()]!=10||buckets[fixtureStart.Add(time.Minute).UnixMilli()]!=90||buckets[fixtureStart.Add(2*time.Minute).UnixMilli()]!=6 {t.Fatalf("buckets=%v",buckets)}
        }
    }
    // Single delta without elapsed still contributes observations.
    if _,err:=db.Exec("DELETE FROM telemetry.metrics");err!=nil {t.Fatal(err)}
    m:=metricHistogram(fixtureStart,"[10]","[3,2]",5,30,"a");m.Temporality="delta";insertMetrics(t,db,m)
    h:=runMetricPanel(t,e,Panel{Viz:"histogram",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"count()"},Histogram:&Histogram{Field:"value",Buckets:"explicit"}}},fixtureStart,fixtureStart.Add(time.Minute))
    if !reflect.DeepEqual(metricValues(t,h,"count"),[]any{3.0,2.0}) {t.Fatalf("delta counts=%+v",h.Frame)}
}

func TestHistogramSpecRejectsRemovedTemporality(t *testing.T) {
    raw:=[]byte(`{"name":"Hist","panels":[{"id":"h","title":"Hist","viz":"histogram","query":{"from":"metrics","where":["name='net'"],"measures":["count()"],"histogram":{"field":"value","buckets":"explicit","temporality":"delta"}}}]}`)
    decoder:=json.NewDecoder(bytes.NewReader(raw));decoder.DisallowUnknownFields()
    var d Dashboard
    if err:=decoder.Decode(&d);err==nil||!strings.Contains(err.Error(),`unknown field "temporality"`) {t.Fatalf("decode=%v",err)}
}
```

- [ ] **Run the distribution/removed-option regressions red before switching production.** Run the new tests against the old renderer and option; reset weights and strict removal fail. Then make the production integration and contract changes below in this same task.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel ./internal/mcp -run 'TestHistogramPanelsShareResetIncreases|TestHistogramSpecRejectsRemovedTemporality|TestDistributionSpecGuide' -count=1
```

Expand the builder's bound_increases with ordinality after reset handling; do not subtract exploded mismatched bounds. This is the complete replacement raw renderer:

```go
func metricHistogramRaw(dimSelect string, filters []Filter, scope Scope) (string, []any, error) {
    cte,args,err:=metricSeriesCTE(filters,scope)
    if err!=nil {return "",nil,err}
    text:=cte+` SELECT time AS t,`+dimSelect+`,_bucket,
      CASE WHEN i=1 THEN NULL ELSE _bounds[i-1] END AS lo,
      CASE WHEN i<=len(_bounds) THEN _bounds[i] END AS hi,
      n AS n
      FROM metric_increases,UNNEST(bound_increases) WITH ORDINALITY AS u(n,i)
      WHERE type='histogram' AND n>=0`
    return text,args,nil
}
```

Update compileDistribution to request this renderer with filters/scope, accept its complete Args including output-window arguments, and wrap its result as existing raw. The heatmap uses later point time; histogram uses the whole window. Preserve group/top/Other/count/cell budgets and preserve Task 2's real negative bounds and zero observations. Distribution display groups use `HAVING sum(n)>0`; retain zero-count bounds in the raw quantile input because they determine interpolation neighbors and the highest finite overflow bound.


- [ ] **Remove the option everywhere and regenerate contracts.** Delete Histogram.Temporality, its validator branch, the TS property, old helper parameters, old reflection-tag tests and old panel-option tests. Move temporality fixtures in distribution_test.go onto telemetry.Metric.Temporality and expect reset current weights. Replace the distribution passage in `baseSpecGuide` with these exact sentences (the regression checks this text verbatim):

```text
Distribution panels use a structured query with one count() measure and histogram: {field: duration_ms, buckets: log2} for spans or {field: value, buckets: explicit} for metrics. Negative span durations clamp to [0,1); +Inf goes to overflow; NaN is excluded because it is not a measurement. Metric histograms use stored temporality and shared per-series increases; resets contribute current counts. Delta histograms sum observation weights.
```

Replace the old MCP distribution guide regression with the following code in its existing test file:

```go
func TestDistributionSpecGuide(t *testing.T) {
    const want = "Distribution panels use a structured query with one count() measure and histogram: {field: duration_ms, buckets: log2} for spans or {field: value, buckets: explicit} for metrics. Negative span durations clamp to [0,1); +Inf goes to overflow; NaN is excluded because it is not a measurement. Metric histograms use stored temporality and shared per-series increases; resets contribute current counts. Delta histograms sum observation weights."
    if !strings.Contains(specGuide,want) {t.Errorf("guide omits exact distribution passage %q",want)}
    for _,old:=range []string{"temporality: cumulative|delta","last minus first","counter resets mid-window contributes 0"} {
        if strings.Contains(specGuide,old) {t.Errorf("obsolete guide phrase %q",old)}
    }
}
```

No custom decoder may consume and silently discard the removed property. Existing API DisallowUnknownFields, dashboard edit decoding and SDK closed JSON schema handle unknown fields; exercise the API/MCP suites as well as the strict unit test. Generated references are produced by `rtk just docs-generate`; never hand-edit mcp-tools.mdx. Use `rtk just ui` then `rtk just ui-check` to verify the shared types and embeds. If changing descriptions affects server eval goldens, use `rtk just dashboard-eval-generate`, not hand-written fixture edits.

- [ ] **Run green:** all builder/identity/snapshot tests and the existing file-window tests pass; distribution panels now use the shared increases and stored temporality; scalar aggregate meaning changes in Task 4.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel -run 'TestMetricIncreaseOrdersDeduplicatesAndResets|TestMetricSeriesIdentitySeparatesEveryField|TestDeltaFirstPointNeedsElapsedOnlyForRate|TestHistogramIncreaseResetsAllChannels|TestMetricReadScopeIncludesSeparateLookbackBatch' -count=1
rtk bash scripts/with-duckdb.sh go test ./internal/query -run 'Test.*Snapshot|Test.*Window' -count=1
rtk bash scripts/with-duckdb.sh go test ./internal/panel ./internal/mcp ./internal/api ./internal/dashboard -run 'TestHistogramPanelsShareResetIncreases|TestHistogramSpecRejectsRemovedTemporality|TestDistribution|TestExamplesAndPreviewCoverNewTypes|Test.*Unknown' -count=1
rtk just docs-generate
rtk just ui
rtk just ui-check
rtk just check
```

- [ ] **Controller commit message:** `feat(panel): share metric series increase computation`

## Task 4: Series-aware metric measures

**Files:** Create `internal/panel/metric_measures.go`, `internal/panel/metric_measures_test.go`, `internal/panel/metric_name_test.go`; modify `internal/panel/compile.go`, `internal/panel/items.go`, `internal/panel/table_trends.go`, `internal/panel/exec.go`, `internal/panel/validate.go`, `internal/panel/check.go`, `internal/panel/filter.go`, `internal/panel/diagnose.go`, `internal/panel/metric_series_test.go`. Test current limits/ranking/frames with `internal/panel/confidence_ranking_test.go`, `internal/panel/frame_semantics_test.go`, `internal/panel/items_test.go`, `internal/panel/table_trends_test.go`, `internal/panel/diagnose_regression_test.go`.

**Interfaces**

Consumes (unchanged signatures):

```go
func compileQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error)
func compileItems(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error)
func measureSQL(m Measure, sig *signal, seconds float64, partition string) string
func Check(ctx context.Context, parser Parser, d *Dashboard) (*Checked, Problems, error)
func Validate(d *Dashboard) Problems
func (e *Executor) diagnose(ctx context.Context, p *Panel, filters []Filter, scope Scope) string
func (e *Executor) attachTableTrends(ctx context.Context, p *Panel, checked *Checked, scope Scope, f *Frame) (err error)
func AutoInterval(window time.Duration, widthPx int) time.Duration
```

Produces (new, private):

```go
func compileMetricQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error)
func metricMeasurePlan(m Measure) (source, perSeries, acrossSeries string)
func validateMetricName(p *Panel, filters []Filter, vars map[string]Variable, path string, problems *Problems)
func equalityValue(n any) (literal, parameter string, ok bool)
func metricFrameHasValue(f *Frame) bool
func (e *Executor) metricKindDiagnosis(ctx context.Context, p *Panel, filters []Filter, scope Scope) string
```

Extend Filter with internal fields `EqLiteral string`, `EqParam string`, `EqValue bool` (no JSON exposure). These are derived from the original AST only for a field-to-string-constant or field-to-scalar-parameter equality. `EqField` alone is insufficient: `name = service` must not satisfy the rule. Produces unchanged Compiled/Frame wire contracts and at least 1 m automatic buckets for metric queries.

Series-aware and kind-dependent semantics apply only to the `value` field. Other numeric fields (`hist_count`, `hist_sum`, numeric attributes) retain today's per-point aggregation for every kind, including summary and histogram: do not apply counter increases, per-series weighting, latest-value summation or histogram quantile interpolation to those fields. Field-less count/count_distinct/share retain point meanings and rate follows the kind-aware increase path. Task 5 must preserve this planner split.

- [ ] **Write rate and restart regressions with known answers.** Package panel imports errors, math, reflect, strings, testing, time, telemetry. New `runMetricMeasure` helper is defined here.

```go
func runMetricMeasure(t *testing.T, e *Executor, measures []string, bucket string, by []string, start,end time.Time) Result {
    t.Helper()
    viz:="table";if bucket!="" {viz="timeseries"}
    return runMetricPanel(t,e,Panel{Viz:viz,Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:measures,By:by,Bucket:bucket}},start,end)
}

func TestMetricRateUsesCoveredElapsedTime(t *testing.T) {
    e,db:=metricFixture(t)
    for i:= -1;i<3;i++ {insertMetrics(t,db,metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),float64(100+60*(i+1))))}
    for _,bucket:=range []string{"10s","1m","1h"} {
        r:=runMetricMeasure(t,e,[]string{"rate()"},bucket,nil,fixtureStart,fixtureStart.Add(3*time.Minute))
        values:=metricValues(t,r,"rate")
        if len(values)==0 {t.Fatalf("%s: no rates",bucket)}
        for _,v:=range values {if v!=1.0 {t.Fatalf("%s rate=%v want 1",bucket,v)}}
    }
    r:=runMetricPanel(t,e,Panel{Viz:"stat",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate()"}}},fixtureStart,fixtureStart.Add(3*time.Minute))
    if r.Frame.Totals[len(r.Frame.Totals)-1]!=1.0 {t.Fatalf("totals=%v",r.Frame.Totals)}
    auto:=runMetricMeasure(t,e,[]string{"rate()"},"auto",nil,fixtureStart,fixtureStart.Add(3*time.Minute))
    duration,ok:=ParseSpan(auto.Interval);if !ok||duration<time.Minute {t.Fatalf("auto=%q",auto.Interval)}
}

func TestMetricRestartDoesNotCarryOldSeries(t *testing.T) {
    e,db:=metricFixture(t)
    for _,p:=range []struct{minute int;value float64;id string}{{-1,100,"old"},{0,160,"old"},{1,10000,"new"},{2,10060,"new"}} {
        m:=metricCounter(fixtureStart.Add(time.Duration(p.minute)*time.Minute),p.value)
        m.Resource=map[string]any{"container.id":p.id};insertMetrics(t,db,m)
    }
    r:=runMetricMeasure(t,e,[]string{"rate()"},"1m",nil,fixtureStart,fixtureStart.Add(3*time.Minute))
    times,values:=metricValues(t,r,"time"),metricValues(t,r,"rate")
    got:=map[int64]any{};for i,v:=range values {got[times[i].(int64)]=v}
    if got[fixtureStart.UnixMilli()]!=1.0||got[fixtureStart.Add(2*time.Minute).UnixMilli()]!=1.0 {t.Fatalf("restart=%v",got)}
    if v,exists:=got[fixtureStart.Add(time.Minute).UnixMilli()];exists&&v!=nil {t.Fatalf("first new point=%v",v)}
}

func TestMetricRateSumsPerSeriesAndIgnoresMissingElapsed(t *testing.T) {
    e,db:=metricFixture(t)
    for _,p:=range []struct{seconds int;value float64;id string}{{-60,0,"a"},{0,60,"a"},{30,120,"a"},{-60,0,"b"},{0,120,"b"},{90,300,"b"}} {
        m:=metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second),p.value)
        m.Attributes=map[string]any{"series":p.id};insertMetrics(t,db,m)
    }
    r:=runMetricMeasure(t,e,[]string{"rate()"},"5m",nil,fixtureStart,fixtureStart.Add(2*time.Minute))
    a, b := 120.0/90.0, 300.0/150.0
    want := a + b
    if got:=metricValues(t,r,"rate");!reflect.DeepEqual(got,[]any{want}) {t.Fatalf("rates=%v",got)}
    if _,err:=db.Exec("DELETE FROM telemetry.metrics");err!=nil {t.Fatal(err)}
    for i:=range 2 {m:=metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),12);m.Temporality="delta";insertMetrics(t,db,m)}
    sums:=runMetricMeasure(t,e,[]string{"sum(value)"},"5m",nil,fixtureStart,fixtureStart.Add(2*time.Minute))
    rates:=runMetricMeasure(t,e,[]string{"rate()"},"5m",nil,fixtureStart,fixtureStart.Add(2*time.Minute))
    if metricValues(t,sums,"sum")[0]!=24.0||metricValues(t,rates,"rate")[0]!=.2 {t.Fatalf("sum=%+v rate=%+v",sums.Frame,rates.Frame)}
}

func TestMetricMeasuresRespectKindsAndSeriesWeight(t *testing.T) {
    for _,kind:=range []string{"gauge","updown","counter","summary"} {
        t.Run(kind,func(t *testing.T) {
            e,db:=metricFixture(t)
            for _,p:=range []struct{seconds int;value float64;id string}{{-60,0,"a"},{0,10,"a"},{30,30,"a"},{-60,0,"b"},{0,100,"b"}} {
                m:=metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second),p.value)
                if kind=="gauge"||kind=="summary" {m.Type=kind;m.Temporality="";m.Monotonic=false}
                if kind=="updown" {m.Monotonic=false}
                m.Attributes=map[string]any{"series":p.id};insertMetrics(t,db,m)
            }
            want:=map[string]any{"sum(value)":120.0,"avg(value)":60.0,"last(value)":130.0,"min(value)":10.0,"max(value)":100.0,"p75(value)":65.0,"quantile(value, 0.5)":30.0}
            if kind=="counter" {want["sum(value)"]=130.0;want["avg(value)"]=nil;want["p75(value)"]=nil;want["quantile(value, 0.5)"]=nil}
            if kind=="summary" {for measure:=range want {want[measure]=nil}}
            for text,value:=range want {
                r:=runMetricMeasure(t,e,[]string{text},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
                got:=r.Frame.Values[len(r.Frame.Columns)-1]
                if len(got)!=1||got[0]!=value {t.Fatalf("%s got=%v want=%v",text,got,value)}
            }
            r:=runMetricMeasure(t,e,[]string{"count()","count_distinct(attributes['series'])","share()"},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
            for i,value:=range []float64{3,2,100} {if r.Frame.Values[i][0]!=value {t.Fatalf("point operation=%+v",r.Frame)}}
        })
    }
}

func TestMetricNonValueFieldsKeepPerPointAggregates(t *testing.T) {
    for _,kind:=range []string{"gauge","updown","counter","histogram","summary"} {
        t.Run(kind,func(t *testing.T) {
            e,db:=metricFixture(t)
            for i,value:=range []float64{10,30,100} {
                m:=metricCounter(fixtureStart.Add(time.Duration(i)*time.Second),999)
                if kind=="updown" {m.Monotonic=false}
                if kind!="updown"&&kind!="counter" {m.Type=kind;m.Monotonic=false}
                if kind=="gauge"||kind=="summary" {m.Temporality=""}
                m.HistCount,m.HistSum=int64(value),value
                m.HistBoundsJSON,m.HistCountsJSON="[10]","[1,0]"
                series:="a";if i==2 {series="b"}
                m.Attributes=map[string]any{"series":series,"value":value}
                insertMetrics(t,db,m)
            }
            total, points := 140.0, 3.0
            wantAverage := total / points
            for _,field:=range []string{"hist_count","hist_sum","attributes['value']"} {
                for _,tc:=range []struct{fn string;want float64}{
                    {"sum",140},{"avg",wantAverage},{"min",10},{"max",100},{"last",100},{"p75",65},
                } {
                    r:=runMetricMeasure(t,e,[]string{tc.fn+"("+field+")"},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
                    got:=r.Frame.Values[len(r.Frame.Columns)-1]
                    if len(got)!=1||got[0]!=tc.want {t.Fatalf("%s(%s)=%v want %v",tc.fn,field,got,tc.want)}
                }
            }
        })
    }
}

func TestMetricFiniteValuesDoNotPoisonLag(t *testing.T) {
    e,db:=metricFixture(t)
    for i,v:=range []float64{100,math.NaN(),math.Inf(1),math.Inf(-1),160} {insertMetrics(t,db,metricCounter(fixtureStart.Add(time.Duration(i-1)*time.Minute),v))}
    r:=runMetricMeasure(t,e,[]string{"rate()"},"5m",nil,fixtureStart,fixtureStart.Add(4*time.Minute))
    if !reflect.DeepEqual(metricValues(t,r,"rate"),[]any{.25}) {t.Fatalf("finite=%+v",r.Frame)}
}
```

- [ ] **Test the name rule against DuckDB's parsed equalities, including All selection.** Use existing Check and Problems; a successful validation-only negative test is distinct from SQL-output correctness.

```go
func TestMetricNameRuleUsesCheckedEqualities(t *testing.T) {
    e,_:=metricFixture(t)
    for _,tc:=range []struct{label,where string;by []string;all,multi bool;measure string;valid bool}{
        {"literal","name='net'",nil,false,false,"rate()",true},
        {"single","name=$metric",nil,false,false,"rate()",true},
        {"all_option","name=$metric",nil,true,false,"rate()",false},
        {"all_grouped","name=$metric",[]string{"name"},true,false,"rate()",true},
        {"multi","name IN $metric",nil,false,true,"rate()",false},
        {"no_name","service='svc'",nil,false,false,"sum(value)",false},
        {"field_equality","name=service",nil,false,false,"sum(value)",false},
        {"or","name='net' OR name='cpu'",nil,false,false,"sum(value)",false},
        {"count","service='svc'",nil,false,false,"count()",true},
        {"distinct","service='svc'",nil,false,false,"count_distinct(name)",true},
        {"share","service='svc'",nil,false,false,"share()",false},
    } {
        t.Run(tc.label,func(t *testing.T) {
            d:=Dashboard{Name:"Metrics",Variables:[]Variable{{Name:"metric",Kind:"custom",Options:[]string{"net","cpu"},Default:"net",IncludeAll:tc.all,Multi:tc.multi}},
                Panels:[]Panel{{ID:"metric",Title:"Metric",Viz:"table",Query:&Query{From:"metrics",Where:[]string{tc.where},Measures:[]string{tc.measure},By:tc.by}}}}
            Normalize(&d)
            _,problems,err:=Check(t.Context(),e.engine,&d);if err!=nil {t.Fatal(err)}
            if (len(problems)==0)!=tc.valid {t.Fatalf("problems=%v",problems)}
            if !tc.valid {
                found:=false;for _,p:=range problems {if p.Path=="panels[0].query.where"&&strings.Contains(p.Hint,"name") {found=true}}
                if !found {t.Fatalf("missing name hint: %v",problems)}
            }
            if tc.label=="all_option" {
                _,err:=e.Run(t.Context(),RunRequest{Dashboard:d,Vars:map[string]Value{"metric":{All:true}}})
                var p Problems;if !errors.As(err,&p) {t.Fatalf("All accepted: %v",err)}
            }
        })
    }
}

func TestMetricKindMismatchHasActionableDiagnosis(t *testing.T) {
    e,db:=metricFixture(t)
    m:=metricCounter(fixtureStart,100);m.Monotonic=false;m.Name="system.memory.usage";insertMetrics(t,db,m)
    r:=runMetricPanel(t,e,Panel{Viz:"timeseries",Query:&Query{From:"metrics",Where:[]string{"name='system.memory.usage'"},Measures:[]string{"rate()"},Bucket:"1m"}},fixtureStart,fixtureStart.Add(time.Minute))
    want:="rate() needs a counter; system.memory.usage is an updown; use last(value) or avg(value)"
    if r.Status!=StatusEmpty||r.Diagnosis!=want {t.Fatalf("diagnosis=%+v",r)}
}

func TestMetricNameAllGroupsDifferentMetricsSeparately(t *testing.T) {
    e,db:=metricFixture(t)
    for _,p:=range []struct{name string;value float64}{{"net",10},{"cpu",100}} {
        m:=metricCounter(fixtureStart,p.value);m.Name=p.name;m.Type="gauge";m.Monotonic=false;m.Temporality=""
        insertMetrics(t,db,m)
    }
    start,end:=fixtureStart,fixtureStart.Add(time.Minute)
    d:=Dashboard{Name:"Metrics",Time:Time{From:&start,To:&end},Variables:[]Variable{{
        Name:"metric",Kind:"custom",Options:[]string{"net","cpu"},IncludeAll:true,Default:AllValue,
    }},Panels:[]Panel{{ID:"metrics",Title:"Metrics",Viz:"table",Query:&Query{
        From:"metrics",Where:[]string{"name=$metric"},Measures:[]string{"sum(value)"},By:[]string{"name"},
    }}}}
    results,err:=e.Run(t.Context(),RunRequest{Dashboard:d,Vars:map[string]Value{"metric":{All:true}}})
    if err!=nil {t.Fatal(err)}
    names,values:=metricValues(t,results[0],"name"),metricValues(t,results[0],"sum")
    got:=map[string]any{};for i,name:=range names {got[name.(string)]=values[i]}
    if len(got)!=2||got["net"]!=10.0||got["cpu"]!=100.0 {t.Fatalf("All groups=%v",got)}
}
```

- [ ] **Run red:** current rate is export frequency, gauge sums/means weight points, kind mismatches return values, auto buckets are 10 s, and All-capable variables pass the name rule.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel -run 'TestMetricRateUsesCoveredElapsedTime|TestMetricRestartDoesNotCarryOldSeries|TestMetricRateSumsPerSeriesAndIgnoresMissingElapsed|TestMetricMeasuresRespectKindsAndSeriesWeight|TestMetricFiniteValuesDoNotPoisonLag|TestMetricNonValueFieldsKeepPerPointAggregates|TestMetricNameRuleUsesCheckedEqualities|TestMetricNameAllGroupsDifferentMetricsSeparately|TestMetricKindMismatchHasActionableDiagnosis' -count=1
```

- [ ] **Implement per-series then across-series SQL.** The following is the scalar/count stage planner. It emits actual SQL expressions; Stage 1's `_value` is a finite numeric projection of m.Field; only `value` is NULL for unsupported kinds, and `_bucket` comes from Task 3. A rate's numerator includes only deltas with valid elapsed; it is not divided by the chart bucket width. Task 5 adds the histogram value-measure branches and histogram rate stages.

```go
func metricMeasurePlan(m Measure) (source, perSeries, acrossSeries string) {
    if m.Field != nil && (m.Field.Column != "value" || m.Field.Key != "") && m.Func != "count_distinct" {
        // _value is the finite FieldRef.numberSQL() projection over raw visible points.
        switch m.Func {
        case "sum", "avg", "min", "max":
            return "metric_raw_window", "list(_value) FILTER (WHERE _value IS NOT NULL)",
                "list_aggregate(flatten(list(_v)), '"+m.Func+"')::DOUBLE"
        case "last":
            return "metric_raw_window", "arg_max(_value,(time,ingested_unix_nano))", "arg_max(_v,_last_order)::DOUBLE"
        default:
            return "metric_raw_window", "list(_value) FILTER (WHERE _value IS NOT NULL)",
                "list_aggregate(flatten(list(_v)), 'quantile_cont', "+sqlFloat(m.Q)+")::DOUBLE"
        }
    }
    scalar := "CASE WHEN type IN ('gauge','updown','counter') THEN _value END"
    gauge := "CASE WHEN type IN ('gauge','updown') THEN _value END"
    switch m.Func {
    case "count":
        return "metric_raw_window", "count(*)::DOUBLE", "sum(_v)::DOUBLE"
    case "count_distinct":
        return "metric_raw_window", "list(DISTINCT _distinct) FILTER (WHERE _distinct IS NOT NULL)", "list_unique(flatten(list(_v)))::DOUBLE"
    case "share":
        return "metric_raw_window", "count(*)::DOUBLE", "100.0*sum(_v)/nullif(sum(sum(_v)) OVER (_partition),0)"
    case "rate":
        return "metric_increases", "sum(CASE WHEN type='counter' AND elapsed_s>0 THEN increase END ORDER BY time,ingested_unix_nano)/nullif(sum(CASE WHEN type='counter' AND elapsed_s>0 THEN elapsed_s END ORDER BY time,ingested_unix_nano),0)", "sum(_v ORDER BY "+metricSeriesKey+")::DOUBLE"
    case "sum":
        return "metric_value_rows", "CASE WHEN type IN ('gauge','updown') THEN avg(_value) WHEN type='counter' THEN sum(increase) END", "sum(_v)::DOUBLE"
    case "avg":
        return "metric_window_points", "avg("+gauge+")", "avg(_v)::DOUBLE"
    case "last":
        return "metric_window_points", "arg_max("+scalar+",(time,ingested_unix_nano))", "sum(_v)::DOUBLE"
    case "min", "max":
        return "metric_window_points", m.Func+"("+scalar+")", m.Func+"(_v)::DOUBLE"
    default:
        return "metric_window_points", "list("+gauge+") FILTER (WHERE "+gauge+" IS NOT NULL)",
            "list_aggregate(flatten(list(_v)), 'quantile_cont', "+sqlFloat(m.Q)+")::DOUBLE"
    }
}
```

Implement compileMetricQuery with these concrete stages:

1. Get Task 3's CTE/Args. Add `metric_raw_window` from metric_input with the original `[Start,End)` predicates and computed _bucket (raw rows preserve count semantics). Add `metric_window_points` from metric_points with the original predicates. Bind both windows with the required timestamp casts, appending UTC arguments in textual order. They do not emit lookback observations.
2. For each measure index i, project its `Field.numberSQL()` as `_value`, replacing nonfinite values with NULL; for count_distinct also project `Field.stringSQL()` as `_distinct`. Field-less functions do not dereference m.Field. For sum(value), `metric_value_rows` is gauge/updown window points UNION ALL counter metric_increases, projecting the same identity/type/time/_bucket/value/increase columns. Counter baselines are excluded from sum, but kept for last/min/max.
3. Render dimensions as existing FieldRef.stringSQL() with coalesce to empty text. Stage `metric_series_i` selects `_bucket`, the eight exact identity columns, type, dimension aliases `_d0`…`_d2`, and the planner's perSeries expression as `_v`; group by bucket, identity, type and dimensions. Stage `metric_group_i` groups by bucket/dimensions and evaluates acrossSeries. Replace `_partition` with `PARTITION BY _bucket` for bucketed queries, empty text otherwise. The non-value last branch also projects `max((time,ingested_unix_nano))` as `_last_order` for Stage 2; its last selects the globally latest point, never sums latest values per series. Non-value lists flatten all per-point samples across series before applying the requested aggregate, including recomputation for Other. Empty aggregate/quantile lists return NULL rather than raising an engine error (guard with `CASE WHEN len(flatten(list(_v)))>0 THEN ... END`). Count-distinct of an empty list is zero.
4. Build `metric_groups` from distinct bucket/dimension keys of raw window rows (or one `SELECT 0::BIGINT AS _bucket` for an ungrouped, unbucketed aggregate). Left-join each metric_group_i by bucket and dimension aliases using `IS NOT DISTINCT FROM`. An unsupported measure has a NULL column even when no increase row exists; do not drop an otherwise existing group. Only count/count_distinct coalesce absence to zero. Emit epoch-ms bucket as `_t`/frame time, dimension column names from FieldRef.alias(), and measures with their current aliases/units.
5. Full-window candidates for categorical top N use the same two stages with `_bucket=0` before ranking, not count-per-second or an average of displayed rates. Preserve existing confidence/sample-count eligibility for scalar percentiles/min/max/avg, count actual supported finite samples, and keep volume/first-measure direction/tie semantics. Determine top dimensions once per panel window; fold raw series identities into the chosen dimension/Other **only after Stage 1**, then run Stage 2 again for each folded group. Rates sum per-series rates; gauge means divide by number of series, not rows; count_distinct recomputes the union of values. Do not sum percentiles. Cap via existing orderAndLimit/distributionRows and retain NULL ordering.

Dispatch from compileQuery after the visualization-specific checks: when `q.From=="metrics"`, call compileMetricQuery. In compileItems, use this same metric compiler with item dimensions, then apply its existing scatter/state row ordering/limits and state top-item selection. No separate rate implementation in items.go. measureSQL remains the span/log and raw count-expression helper; explicitly route its metric callers away from generic value/rate branches so a future caller cannot silently reinstate export frequency. Metric histogram distribution compilation already uses Task 3's builder; Task 5 adds histogram value-measure branches.

For metrics table sparklines, clone the table panel/query, select only formatted measure aliases, assign a real time bucket at least 1 m and at most 240 points, run compileMetricQuery with its dimensions and normal scope, and execute with `metricReadScope`. Then retain attachTableTrends' existing row-key mapping/cell budget. Select displayed tuples **after** metric stage aggregation so share's denominator covers the complete checked scope. Comparison and stat/gauge totals already call runScope: they automatically inherit the shared path, including their independently shifted lookbacks.

In runPanel, clamp automatic metric intervals (`reduces`, `bucket=="auto"`, cell auto) with `max(interval,time.Minute)`. Do not clamp explicit 10 s/30 s buckets or widen unrelated signals. Guard optional p.Query before reading From.

- [ ] **Implement AST-backed metric-name validation and NULL diagnosis.** In filter.go, equalityValue unwraps CAST nodes, checks a root COMPARISON with type `COMPARE_EQUAL`, finds the operand opposite a direct `name` field, and accepts only a string CONSTANT's `literal.text` or a PARAMETER's `identifier`. Set EqLiteral/EqParam/EqValue before parameter casts. Reject field-to-field equality, OR roots and expression-valued right sides. Do not use regex or source-string prefix matching. Validate's syntactic measure check stays in validate.go; put validateMetricName there and call it from Check after checked.Filters have been populated (the parser is required).

```go
func equalityValue(n any) (literal, parameter string, ok bool) {
    v,_:=n.(map[string]any)
    if v["class"]!="COMPARISON"||v["type"]!="COMPARE_EQUAL" {return "","",false}
    var other any
    if fieldText(v["left"])=="name" {other=v["right"]
    } else if fieldText(v["right"])=="name" {other=v["left"]
    } else {return "","",false}
    for {
        node,_:=other.(map[string]any)
        if node["class"]=="CAST" {other=node["child"];continue}
        if node["class"]=="PARAMETER" {id,_:=node["identifier"].(string);return "",id,id!=""}
        if node["class"]=="CONSTANT" {
            value,_:=node["literal"].(map[string]any)
            if value["kind"]=="STRING" {text,_:=value["text"].(string);return text,"",true}
        }
        return "","",false
    }
}

func validateMetricName(p *Panel, filters []Filter, vars map[string]Variable, path string, problems *Problems) {
    if p.Query==nil||p.Query.From!="metrics" {return}
    for _,by:=range p.Query.By {if by=="name" {return}}
    sig:=signals["metrics"];var ignored Problems
    measures:=parseMeasures(sig,p.Query.Measures,"",&ignored)
    needs:=false
    for _,m:=range measures {if m.Func!="count"&&m.Func!="count_distinct" {needs=true}}
    if !needs {return}
    for _,f:=range filters {
        if f.EqField!="name"||!f.EqValue {continue}
        if f.EqParam=="" {return}
        v,ok:=vars[f.EqParam]
        if ok&&!v.Multi&&!v.IncludeAll {return}
    }
    problems.addHint(path+".query.where","metric aggregates need one metric per group",
        "filter name to one literal or a single-value variable without All, or group by name")
}

func metricFrameHasValue(f *Frame) bool {
    if f==nil {return false}
    for i,c:=range f.Columns {if c.Role=="measure" {for _,v:=range f.Values[i] {if v!=nil {return true}}}}
    return false
}
```

Call metricFrameHasValue from runPanel to treat a structured metric frame with no non-NULL measures as empty while retaining its NULL cells. Call metricKindDiagnosis before ordinary missing-filter diagnosis, querying the filtered visible metrics window for `(name,type,count(*))`, bounded to at most 6 rows. metricKindDiagnosis applies the explicit compatibility table to field-less rate and the actual value column only (Column=value, Key empty); other numeric fields cannot produce a kind mismatch: rate needs counter (or histogram once Task 5 lands), counter avg/quantile offers rate/last, histogram min/max/last offers avg/p95, summary offers count. Match the exact updown example in the regression; mixed compatible data must not be reported empty. Keep maxDiagnosedFilters and the existing error/operational-failure behavior.

- [ ] **Extend the lookback test to the structured executor, and cover auxiliary aggregates.** Append the two-rate `[1,1]` assertion described in Task 3. Add this actual regression for item panels, whole-window totals, comparisons and table trends:

```go
func TestMetricAuxiliaryAggregatesUseSeriesRates(t *testing.T) {
    e,db:=metricFixture(t)
    for i:=-4;i<4;i++ {insertMetrics(t,db,metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),float64(1000+60*(i+4))))}
    start,end:=fixtureStart,fixtureStart.Add(3*time.Minute)
    panels:=[]Panel{
        {ID:"stat",Title:"Rate",Viz:"stat",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate()"}}},
        {ID:"table",Title:"Rate",Viz:"table",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate()"},By:[]string{"service"}},Options:&Options{Columns:[]ColumnFormat{{Field:"rate",Format:"sparkline"}}}},
        {ID:"items",Title:"Rates",Viz:"scatter",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate() as x","last(value) as y"},By:[]string{"service"}}},
        {ID:"states",Title:"Rates",Viz:"state_timeline",Query:&Query{From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate()"},By:[]string{"service"},Bucket:"1m"},Thresholds:[]Threshold{{Value:2,Status:"bad"}}},
    }
    compare:=true
    results,err:=e.Run(t.Context(),RunRequest{Dashboard:Dashboard{Name:"Metrics",Time:Time{From:&start,To:&end},Panels:panels},Compare:&compare})
    if err!=nil {t.Fatal(err)};got:=byID(results)
    if got["stat"].Frame.Totals[len(got["stat"].Frame.Totals)-1]!=1.0||got["stat"].Previous==nil||got["stat"].Previous.Totals[len(got["stat"].Previous.Totals)-1]!=1.0 {t.Fatalf("stat=%+v",got["stat"])}
    if len(got["table"].Frame.Trends["rate"])!=1 {t.Fatal("missing table trend")}
    for _,v:=range got["table"].Frame.Trends["rate"][0] {if v!=nil&&v!=1.0 {t.Fatalf("trend=%v",v)}}
    if metricValues(t,got["items"],"x")[0]!=1.0 {t.Fatalf("items=%+v",got["items"])}
    for _,v:=range metricValues(t,got["states"],"rate") {if v!=1.0 {t.Fatalf("state=%v",v)}}
}
```

Task 5 extends this same compiler to histogram stages; Task 4 must already return NULL for histogram value aggregates rather than aggregating their stored value; non-value numeric measures remain per-point. The no-name histogram distribution case keeps count() as its observation-weight contract on Task 3's shared renderer; value aggregates require names. Add these concrete metric top-N and half-open-window tests:

```go
func TestMetricRankingRecomputesOtherFromSeriesRates(t *testing.T) {
    e,db:=metricFixture(t)
    for _,p:=range []struct{seconds int;value float64;id string}{{-60,0,"a"},{0,60,"a"},{30,120,"a"},{-60,0,"b"},{0,120,"b"},{90,300,"b"}} {
        m:=metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second),p.value)
        m.ServiceName=p.id;insertMetrics(t,db,m)
    }
    r:=runMetricPanel(t,e,Panel{Viz:"timeseries",Options:&Options{Top:1},Query:&Query{
        From:"metrics",Where:[]string{"name='net'"},Measures:[]string{"rate()"},By:[]string{"service"},Bucket:"5m",
    }},fixtureStart,fixtureStart.Add(2*time.Minute))
    dims,rates:=metricValues(t,r,"service"),metricValues(t,r,"rate")
    got:=map[string]any{};for i,d:=range dims {got[d.(string)]=rates[i]}
    increase, elapsed := 120.0, 90.0
    wantOther := increase / elapsed
    if got["b"]!=2.0||got["Other (1)"]!=wantOther {t.Fatalf("ranking=%v",got)}
}

func TestMetricWindowClipsPointsAfterComputingIncrease(t *testing.T) {
    e,db:=metricFixture(t)
    for _,p:=range []struct{offset time.Duration;value float64}{{-11*time.Minute,0},{-10*time.Minute,100},{0,160},{time.Minute,9999}} {
        insertMetrics(t,db,metricCounter(fixtureStart.Add(p.offset),p.value))
    }
    r:=runMetricMeasure(t,e,[]string{"sum(value)"},"1m",nil,fixtureStart,fixtureStart.Add(time.Minute))
    if !reflect.DeepEqual(metricValues(t,r,"sum"),[]any{60.0}) {t.Fatalf("clipped=%+v",r.Frame)}
    r=runMetricMeasure(t,e,[]string{"rate()"},"1m",nil,fixtureStart,fixtureStart.Add(time.Minute))
    if !reflect.DeepEqual(metricValues(t,r,"rate"),[]any{.1}) {t.Fatalf("elapsed lookback=%+v",r.Frame)}
}
```

Counter first samples older than the 10-minute lookback are not silently scanned; the lookback boundary point above is included and supplies exactly 600 elapsed seconds.

- [ ] **Run green and final gate.** All golden values, NULL diagnoses, name validation, comparisons, trends and existing limits/ranking must pass.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel -run 'TestMetric|TestDeltaFirst|TestHistogramIncrease|Test.*Ranking|Test.*Other|TestGaugeTotals|Test.*Trend|Test.*Items|Test.*State' -count=1
rtk just check
```

- [ ] **Controller commit message:** `fix(panel): aggregate metric measures per series`

## Task 5: Histogram measures

**Files:** Create `internal/panel/metric_histogram.go`, `internal/panel/metric_histogram_test.go`; modify `internal/panel/metric_measures.go`, `internal/panel/diagnose.go`. Reuse Task 3's distribution renderer, contract-removal tests and shared test helpers. No second renderer or authored temporality option is introduced.

**Interfaces**

Consumes: Task 3's metricSeriesCTE; Task 4's compileMetricQuery and metricMeasurePlan; existing `func compileDistribution(p *Panel, filters []Filter, scope Scope) (Compiled, error)` and `func distributionRows(p *Panel, columns int) int`.

Produces (new):

```go
func histogramQuantileSQL(source string, groupColumns []string, q float64) string
```

Task 3 already replaced metricHistogramRaw and removed Histogram.Temporality. This task adds only histogram measure stages and their compatibility diagnosis.

- [ ] **Write golden histogram measure tests through Executor.Run.** Package panel imports testing, time, telemetry. Reuse Task 3's metricHistogram helper and Task 4's runMetricMeasure; do not redeclare them.

```go
func TestHistogramMeasuresInterpolateSummedIncreases(t *testing.T) {
    e,db:=metricFixture(t)
    for _,m:=range []telemetry.Metric{
        metricHistogram(fixtureStart.Add(-time.Minute),"[10,20]","[1,1,0]",2,20,"a"),
        metricHistogram(fixtureStart,"[10,20]","[11,11,0]",22,320,"a"),
        metricHistogram(fixtureStart.Add(-time.Minute),"[10,20]","[0,1,0]",1,20,"b"),
        metricHistogram(fixtureStart,"[10,20]","[0,11,0]",11,220,"b"),
    } {insertMetrics(t,db,m)}
    sumIncrease, countIncrease := 500.0, 30.0
    wantAverage := sumIncrease / countIncrease
    for _,tc:=range []struct{measure string;want any}{
        {"rate()",.5},{"sum(value)",500.0},{"avg(value)",wantAverage},
        {"p50(value)",12.5},{"p75(value)",16.25},{"p95(value)",19.25},
        {"quantile(value, 0.95)",19.25},{"min(value)",nil},{"max(value)",nil},{"last(value)",nil},
    } {
        r:=runMetricMeasure(t,e,[]string{tc.measure},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
        got:=r.Frame.Values[len(r.Frame.Columns)-1]
        if len(got)!=1||got[0]!=tc.want {t.Fatalf("%s=%v want %v",tc.measure,got,tc.want)}
    }
}

func TestHistogramQuantileHandlesZeroAndOverflow(t *testing.T) {
    for _,tc:=range []struct{label,bounds,counts string;count int64;want any}{
        {"lowest_positive","[10,20]","[10,0,0]",10,9.5},
        {"overflow","[10,20]","[1,1,18]",20,20.0},
        {"overflow_with_empty_finite_buckets","[10,20]","[0,0,20]",20,20.0},
        {"zero_total","[10,20]","[0,0,0]",0,nil},
        {"no_finite_bound","[]","[10]",10,nil},
        {"nonpositive_first","[-10,0,10]","[10,0,0,0]",10,-10.0},
        {"exponential_shape","[0.5,1,2]","[4,2,4,0]",10,1.875},
    } {
        t.Run(tc.label,func(t *testing.T) {
            e,db:=metricFixture(t)
            m:=metricHistogram(fixtureStart,tc.bounds,tc.counts,tc.count,10,"a")
            m.Temporality="delta";m.StartTimeUnixNanos=fixtureStart.Add(-time.Minute).UnixNano()
            if tc.label=="exponential_shape" {m.Type="exponential_histogram"}
            insertMetrics(t,db,m)
            r:=runMetricMeasure(t,e,[]string{"p95(value)"},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
            if got:=metricValues(t,r,"p95");len(got)!=1||got[0]!=tc.want {t.Fatalf("p95=%v want=%v",got,tc.want)}
        })
    }
}

func TestHistogramNegativeOnlyQuantileKeepsSign(t *testing.T) {
    e,db:=metricFixture(t)
    // Task 2's scale=0, negative offset=1, counts=[2,6], no positive/zero observations.
    m:=metricHistogram(fixtureStart,"[-8,-4,-2,0]","[0,6,2,0,0]",8,-40,"a")
    m.Type,m.Temporality="exponential_histogram","delta"
    insertMetrics(t,db,m)
    r:=runMetricMeasure(t,e,[]string{"p75(value)"},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
    if got:=metricValues(t,r,"p75");len(got)!=1||got[0]!=-4.0 {t.Fatalf("negative quantile=%v want -4",got)}
}

func TestHistogramDeltaWithoutElapsedHasNoRate(t *testing.T) {
    e,db:=metricFixture(t)
    m:=metricHistogram(fixtureStart,"[10]","[3,2]",5,30,"a");m.Temporality="delta"
    insertMetrics(t,db,m)
    r:=runMetricMeasure(t,e,[]string{"rate()"},"",nil,fixtureStart,fixtureStart.Add(time.Minute))
    if got:=metricValues(t,r,"rate");len(got)!=1||got[0]!=nil {t.Fatalf("delta rate=%v",got)}
}
```

- [ ] **Run red:** histogram value percentiles, rate/sum/mean are still undefined after Task 4; distribution and removed-option regressions are already green from Task 3.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel -run 'TestHistogramMeasuresInterpolateSummedIncreases|TestHistogramQuantileHandlesZeroAndOverflow|TestHistogramNegativeOnlyQuantileKeepsSign|TestHistogramDeltaWithoutElapsedHasNoRate' -count=1
```

- [ ] **Implement histogram rate/sum/mean and interpolation over Task 3 increases.** For histogram metric rate, Stage 1 is `sum(count_increase ORDER BY time,ingested_unix_nano) FILTER (WHERE elapsed_s>0)/nullif(sum(elapsed_s ORDER BY time,ingested_unix_nano) FILTER (WHERE elapsed_s>0),0)`, then sum across series ordered by metricSeriesKey. Histogram sum(value) sums sum_increase; avg(value) divides total sum_increase by total count_increase **after** combining series, NULL on zero denominator. Keep scalar kinds on Task 4's path. Apply these new branches only when `m.Field != nil && m.Field.Column=="value" && m.Field.Key==""` (rate remains field-less); every other numeric field continues through Task 4's per-point planner without kind gating or series weighting. The name rule still applies to those aggregates.


For percentile queries aggregate n by group and finite upper bound (hi); NULL hi is the overflow bucket. Sort upper bounds numerically, overflow last; cumulative counts are derived from **per-bucket weights**, not from stored cumulative-count arrays. For differing bucket grids the union of upper bounds is the interpolation grid; assign each observation weight to its exported upper bound, without fabricating observations by splitting coarse buckets. Different grids within a series still trigger the Task 3 reset first. The following real SQL renderer uses only compiler-produced source/group identifiers:

```go
func histogramQuantileSQL(source string, groupColumns []string, q float64) string {
    keys:=strings.Join(groupColumns,",")
    prefix,group,partition:="","",""
    if keys!="" {prefix=keys+",";group=keys+",";partition="PARTITION BY "+keys+" "}
    return `WITH weights AS (SELECT `+prefix+`hi,sum(n)::DOUBLE AS n FROM `+source+` GROUP BY `+group+`hi),
      ranks AS (SELECT *,sum(n) OVER (`+partition+`ORDER BY hi NULLS LAST ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS total,
        sum(n) OVER (`+partition+`ORDER BY hi NULLS LAST ROWS UNBOUNDED PRECEDING) AS cumulative,
        lag(hi) OVER (`+partition+`ORDER BY hi NULLS LAST) AS previous_bound,
        max(hi) OVER (`+partition+`) AS highest_finite FROM weights),
      answers AS (SELECT *,CASE
        WHEN total<=0 THEN NULL
        WHEN hi IS NULL THEN highest_finite
        WHEN previous_bound IS NULL AND hi<=0 THEN hi
        ELSE coalesce(previous_bound,0.0)+
          (`+sqlFloat(q)+`*total-(cumulative-n))/nullif(n,0)*(hi-coalesce(previous_bound,0.0))
        END::DOUBLE AS quantile
        FROM ranks WHERE cumulative>=`+sqlFloat(q)+`*total AND n>0)
      SELECT `+prefix+`arg_min(quantile,cumulative)::DOUBLE AS quantile FROM answers`+
        func() string {if keys!="" {return " GROUP BY "+keys};return ""}()
}
```

Call the quantile renderer inside compileMetricQuery's per-measure branch on the summed per-bound rows, with `_bucket` and dimension aliases as its groupColumns. It must run before display caps/top folding. Keep all groups from Task 4's metric_groups even when zero total/no finite bounds yields no answer. Recompute Other's histogram weights before its quantile; averaging/summing quantiles is invalid. Kind diagnosis now considers histogram rate/sum/avg/percentiles compatible.


- [ ] **Run green:** native golden tests match interpolated percentiles (including a negative result) and histogram rates/sums/means; rerun Task 3 distribution and removed-option regressions.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel ./internal/mcp ./internal/api ./internal/dashboard -run 'TestHistogram|TestMetric|TestDistribution|TestExamplesAndPreviewCoverNewTypes|Test.*Unknown' -count=1
rtk just docs-generate
rtk just ui
rtk just check
```

- [ ] **Controller commit message:** `fix(panel): derive histogram measures from bucket increases`

## Task 6: Schema discovery, agent guide and docs

**Files:** Modify `internal/panel/schema.go`, `internal/panel/catalog.go`, `internal/panel/schema_test.go`, `internal/mcp/panels.go`, `internal/mcp/panel_examples.go`, `internal/mcp/panel_examples_test.go`, `site/src/content/docs/guides/build-dashboards.mdx`; create/test `internal/panel/metric_schema_test.go`; regenerate `site/src/content/docs/reference/mcp-tools.mdx` with the existing `cmd/fanout-docgen/main.go`/`mcptools.go` generator. Regenerate existing `scripts/dashboard-eval/testdata/server.json` only through the dashboard-eval recipe if its contract guard reports changed goldens.

**Interfaces**

Consumes: `func (e *Executor) Schema(ctx context.Context, req SchemaRequest) (*Schema, error)`, `func (e *Executor) metricNames(ctx context.Context, start, end time.Time, namespace string) ([]MetricSchema, error)`, `func DescribeTools(ctx context.Context) ([]ToolDoc, error)`, existing `baseSpecGuide`, `specGuide`, `investigationExample`, `paymentExample` and `func renderMCPTools() ([]byte, error)`.

Produces: MetricSchema keeps its existing `Type string json:"type"` carrying the semantic kind and adds only `Temporality string json:"temporality,omitempty"`; no `kind` JSON field or Kind member. Catalog lists semantic kinds and gains temporality/start_time. New constants `metricMeasureGuide`, `metricExample`; no new public function. Type never exposes old sum/sum_delta/exp_histogram values. Discovery groups by name/unit/type/temporality rather than picking arbitrary metadata when the same name appears with multiple kinds.

- [ ] **Write the discovery regression on querytest tables.** Package panel imports encoding/json, strings, testing and time.

```go
func TestMetricSchemaExposesKindsAndTemporality(t *testing.T) {
    e,db:=metricFixture(t)
    for _,p:=range []struct{name,kind,temp string;mono bool}{
        {"network","sum","cumulative",true},{"network","sum","delta",true},
        {"memory","sum","cumulative",false},{"temperature","gauge","",false},
        {"latency","exponential_histogram","delta",false},{"summary","summary","",false},
    } {
        m:=metricCounter(fixtureStart,1);m.Name,m.Type,m.Temporality,m.Monotonic=p.name,p.kind,p.temp,p.mono
        insertMetrics(t,db,m)
    }
    schema,err:=e.Schema(t.Context(),SchemaRequest{Window:"1h",Namespace:"test"});if err!=nil {t.Fatal(err)}
    seen:=map[string]bool{}
    for _,m:=range schema.Signals["metrics"].Metrics {
        seen[m.Name+"/"+m.Type+"/"+m.Temporality]=true
        raw,err:=json.Marshal(m);if err!=nil {t.Fatal(err)}
        if !strings.Contains(string(raw),`"type":`) {t.Fatalf("type missing: %s",raw)}
        if strings.Contains(string(raw),`"kind":`) {t.Fatalf("unexpected kind field: %s",raw)}
        if m.Temporality==""&&strings.Contains(string(raw),`"temporality":`) {t.Fatalf("empty temporality: %s",raw)}
    }
    for _,key:=range []string{"network/counter/cumulative","network/counter/delta","memory/updown/cumulative","temperature/gauge/","latency/histogram/delta","summary/summary/"} {
        if !seen[key] {t.Fatalf("missing %s: %v",key,seen)}
    }
    fields:=map[string]ColumnSchema{};for _,f:=range schema.Signals["metrics"].Columns {fields[f.Name]=f}
    if fields["start_time"].Type!=TypeTime||fields["temporality"].Type!=TypeString {t.Fatalf("columns=%v",fields)}
    for _,kind:=range []string{"gauge","counter","updown","histogram","summary"} {if !strings.Contains(fields["type"].Description,kind) {t.Fatalf("type description=%s",fields["type"].Description)}}
}
```

- [ ] **Write the guidance/example regressions.** In mcp's existing panel_examples_test.go import unicode/utf8 in addition to its current encoding/json, strings, testing, panel imports.

```go
func TestMetricGuidanceIsConciseAndExamplesValidate(t *testing.T) {
    if utf8.RuneCountInString(metricMeasureGuide)>900 {t.Fatalf("metric guide has %d characters",utf8.RuneCountInString(metricMeasureGuide))}
    for _,term:=range []string{"gauge/updown","counter","histogram","summary","rate()","sum(value)","avg(value)","last(value)","pNN","elapsed","All","count_distinct()"} {
        if !strings.Contains(metricMeasureGuide,term) {t.Fatalf("missing %q",term)}
    }
    s:=newPanelServer(t)
    var d panel.Dashboard
    if err:=json.Unmarshal([]byte(metricExample),&d);err!=nil {t.Fatal(err)}
    if len(d.Panels)!=2||d.Panels[0].Query.Measures[0]!="rate()"||d.Panels[1].Query.Measures[0]!="p95(value)" {t.Fatalf("examples=%+v",d)}
    if err:=s.panels.Validate(t.Context(),&d);err!=nil {t.Fatal(err)}
    _,preview,err:=s.previewPanels(t.Context(),nil,PreviewInput{Panels:d.Panels,Time:&d.Time,Variables:d.Variables})
    if err!=nil {t.Fatal(err)}
    for _,p:=range preview.Panels {if p.Status!="ok"&&(p.Status!="empty"||p.Diagnosis=="") {t.Fatalf("preview=%+v",p)}}
    tools,err:=DescribeTools(t.Context());if err!=nil {t.Fatal(err)}
    for _,tool:=range tools {
        if tool.Name=="create_dashboard"&&(!strings.Contains(tool.Description,metricMeasureGuide)||!strings.Contains(tool.Description,metricExample)) {t.Fatal("create omits metric guide/examples")}
        if tool.Name!="create_dashboard"&&strings.Contains(tool.Description,metricExample) {t.Fatalf("duplicate metric example in %s",tool.Name)}
    }
}
```

- [ ] **Run red:** MetricSchema temporality, catalog columns, guide and metric example are absent.

```bash
rtk bash scripts/with-duckdb.sh go test ./internal/panel ./internal/mcp -run 'TestMetricSchemaExposesKindsAndTemporality|TestMetricGuidanceIsConciseAndExamplesValidate' -count=1
```

- [ ] **Implement explicit discovery fields and catalog descriptions.** Add only Temporality to MetricSchema; keep Type as the semantic kind. Replace metricNames' any_value(type) query with the following SQL, and scan Name, Unit, Type, Temporality, Count directly. Keep current TTL, limits, namespace binds and operational error handling.

```go
rows, err := e.engine.QueryContext(ctx,
    "SELECT name,coalesce(unit,''),type,coalesce(temporality,''),count(*) FROM metrics WHERE "+where+
    " GROUP BY name,unit,type,temporality ORDER BY 5 DESC,1,3,4 LIMIT 300",args...)
```

Add `str("type", "gauge, counter, updown, histogram or summary")`, `str("temporality", "cumulative or delta; empty for gauge and summary")`, and `ts("start_time", "Data point start time; NULL when absent")` in the metrics catalog; include temporality among lowCard fields.

- [ ] **Publish the bounded guide and two valid examples.** Append this exact table text to baseSpecGuide using metricMeasureGuide and append metricExample once to specGuide in panel_examples.go. Preserve guide pointers on the other tools and the two existing reference-shape labels.

```go
const metricMeasureGuide = `Metrics: gauge/updown | counter | histogram. count(): points for all. rate(): undefined | sum of series increase/elapsed | sum of series count increase/elapsed. sum(value): sum of series averages | sum of increases | sum increase. avg(value): mean of series averages | undefined | total sum increase/total count increase. min/max(value): points | points | undefined. last(value): sum of series latest | same | undefined. pNN/quantile(value,q): points | undefined | interpolated summed bucket increases. summary: no value aggregates. Undefined is NULL. Rates divide by covered elapsed seconds, not bucket width; auto metrics buckets >=1m. Except count()/count_distinct(), fix name with one literal or a single-value variable without All, or group by name. Read kind and temporality from get_telemetry_schema.`

const metricExample = `{"name":"Metric measures","panels":[{"id":"network","title":"Network bytes per second","viz":"timeseries","query":{"from":"metrics","where":["name = 'system.network.io'"],"measures":["rate()"],"by":["attributes['direction']"],"bucket":"auto"}},{"id":"duration","title":"Request duration p95","viz":"timeseries","query":{"from":"metrics","where":["name = 'http.server.request.duration'"],"measures":["p95(value)"],"bucket":"auto"},"unit":"s"}]}`
```

The guide has 807 Unicode characters; its schema facts decide whether each example applies. Update existing guide-presence/uniqueness tests to include both new constants and keep all prior assertions. Add metricExample to the examples-validation loop with expected two panels; retain the specific checks for investigationExample and paymentExample at their current indices. The existing examples contain 2564 characters and the new one adds 443. Change the existing examples budget assertion to `if size := len([]rune(investigationExample + paymentExample + metricExample)); size > 3343 { t.Errorf("examples have %d characters, maximum 3343", size) }` (the prior ceiling of 2900 plus 443). Keep m2SpecGuide's 1100-character cap and the dedicated metricMeasureGuide cap above. Do not trim prior contracts just to pass a size assertion.

- [ ] **Write the metrics documentation section and regenerate references.** In build-dashboards.mdx include the exact Phase A table (all seven rows), semantic/stored kind distinction, summary NULLs, count_distinct/share meaning, the explicit count()/count_distinct() name-rule exemption, the name rule with an All-capable-variable rejection example, per-series sums/means/last, 10-minute lookback, resets, first cumulative/delta points, covered elapsed seconds, automatic >=1 m buckets and histogram interpolation/overflow. Include the two metricExample panels as readable JSON. State that histogram panels read stored temporality and reject an authored temporality option. No Phase C approximate span-quantile claims are introduced in Phase A.

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk bash scripts/with-duckdb.sh go test ./internal/panel ./internal/mcp -run 'TestMetricSchema|TestMetricGuidance|TestExamplesAndPreviewCoverNewTypes|TestDescriptionsExposeAnalysisContract|TestSpecGuide|TestGuidanceSize|TestDistributionSpecGuide' -count=1
rtk just dashboard-eval-generate-check
rtk just check
```

Expected green: discovery includes every observed kind/temporality pair, guide and examples are valid and uniquely attached to create_dashboard, generated MCP reference matches its source, and all gates pass. If the eval-golden check fails because the intentional tool descriptions changed, run `rtk just dashboard-eval-generate`, rerun its check, then rerun `rtk just check`.

- [ ] **Controller commit message:** `docs(metrics): teach series-aware measures and expose metric kinds`

## Task 7: Demo-data cross-check (readbench)

**Files:** Create/test `internal/panel/metric_crosscheck_test.go` with `//go:build readbench`; modify `internal/panel/panel_bench_data_test.go`, `internal/panel/panel_bench_test.go` only for a read-only copy helper and its input/metadata/start-time tests. No product reader, generator, dependency or deployed-data edits.

**Interfaces**

Consumes (existing): `func panelReplayMetadata(input string) ([]telemetry.BatchMetadata, error)`, `func panelBenchScratch(tb testing.TB) string`, `func panelBenchEngine(tb testing.TB, data panelBenchData) (*query.Duck, config.Config)`, existing panelBenchData, Executor.Run, queryrows.WithWindow, telemetry.TimeRange, SchemaBatch and BatchSuffix.

Produces (new, test-only):

```go
func copyMetricCrosscheckData(tb testing.TB, input string) panelBenchData
func metricOracleSQL(histogram bool) string
func metricCrosscheckDashboard(name string, histogram bool, start, end time.Time) Dashboard
func crosscheckMetricFrame(t *testing.T, engine Engine, d Dashboard, histogram bool, start, end time.Time)
```

`FANOUT_PANEL_BENCH_DATA_DIR` points to the controller's **telemetry root**, containing `parquet/batches/*.batch`; it is not a live application data directory. Only supplied format-4 batches are accepted. Without the variable, the demo test skips; a local synthetic oracle test still runs under readbench to verify that wrong results are detected. The oracle is authored directly against the metrics view; it calls no metricSeriesCTE, metricMeasurePlan, compileMetricQuery, metricHistogramRaw or histogramQuantileSQL.

- [ ] **Write the test and independent oracle.** Package panel imports encoding/json, math, os, reflect, strings, testing, time, telemetry, queryrows. Add io, filepath, fmt, errors and config/query only where the copy helper requires them.

```go
func metricCrosscheckDashboard(name string, histogram bool, start,end time.Time) Dashboard {
    measure:= "rate()";by:=[]string{"attributes['direction']"}
    if histogram {measure="p95(value)";by=nil}
    return Dashboard{Name:"Metric cross-check",Time:Time{From:&start,To:&end},Panels:[]Panel{{
        ID:"metric",Title:"Metric",Viz:"timeseries",Query:&Query{From:"metrics",
        Where:[]string{"name = "+sqlString(name)},Measures:[]string{measure},By:by,Bucket:"1m"},
    }}}
}

// This SQL is deliberately independent of the production builder. Four binds:
// extended read start, visible end, exact metric name, visible start.
func metricOracleSQL(histogram bool) string {
    base:=`WITH decoded AS (
      SELECT *,TRY(from_json(hist_bounds_json,'["DOUBLE"]')) AS b,
        TRY(from_json(hist_counts_json,'["UBIGINT"]')) AS c
      FROM metrics WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND name=?
        AND ((type='counter' AND isfinite(value)) OR (type='histogram' AND isfinite(hist_sum) AND hist_count>=0))),
      valid AS (SELECT * FROM decoded WHERE type='counter' OR
        (b IS NOT NULL AND c IS NOT NULL AND len(c)=len(b)+1
          AND coalesce(list_bool_and(list_transform(b,lambda x: isfinite(x))),true))),
      samples AS (SELECT *,
        row_number() OVER (PARTITION BY namespace,service,name,unit,scope_name,scope_version,attributes,resource,time
          ORDER BY ingested_unix_nano DESC,value DESC,hist_count DESC,hist_sum DESC,
            hist_counts_json DESC,hist_bounds_json DESC,start_time DESC NULLS LAST) AS retry
      FROM valid),
      ordered AS (
      SELECT *,lag(value) OVER series AS pv,lag(time) OVER series AS pt,
        lag(start_time) OVER series AS ps,lag(hist_count) OVER series AS pc,
        lag(b) OVER series AS pb,lag(c) OVER series AS previous_counts
      FROM samples WHERE retry=1
      WINDOW series AS (PARTITION BY namespace,service,name,unit,scope_name,scope_version,attributes,resource ORDER BY time,ingested_unix_nano)),
      steps AS (
      SELECT *,epoch_ms(time_bucket(INTERVAL '60 seconds',time::TIMESTAMP_NS))::BIGINT AS bucket,
        ((start_time IS NOT NULL AND ps IS NOT NULL AND start_time<>ps)
          OR CASE WHEN type='histogram' THEN hist_count<pc OR b<>pb ELSE value<pv END) AS reset,
        CASE WHEN temporality='delta' AND start_time IS NOT NULL AND start_time<time
          THEN (epoch_ns(time)::HUGEINT-epoch_ns(start_time)::HUGEINT)::DOUBLE/1e9
          WHEN pt<time THEN (epoch_ns(time)::HUGEINT-epoch_ns(pt)::HUGEINT)::DOUBLE/1e9 END AS elapsed
      FROM ordered WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS
        AND temporality IN ('delta','cumulative') AND (temporality='delta' OR pt IS NOT NULL))`
    if !histogram {
        return base+`, series_rates AS (
          SELECT bucket,namespace,service,name,unit,scope_name,scope_version,attributes,resource,
            coalesce(TRY_CAST(attributes['direction'] AS VARCHAR),'') AS direction,
            sum(CASE WHEN temporality='delta' OR reset THEN value ELSE value-pv END ORDER BY time,ingested_unix_nano)/sum(elapsed ORDER BY time,ingested_unix_nano) AS rate
          FROM steps WHERE type='counter' AND elapsed>0
          GROUP BY bucket,namespace,service,name,unit,scope_name,scope_version,attributes,resource)
          SELECT bucket,direction,sum(rate ORDER BY namespace,service,name,unit,scope_name,scope_version,attributes,resource)::DOUBLE AS value FROM series_rates GROUP BY bucket,direction ORDER BY 1,2`
    }
    return base+`, bound_steps AS (
        SELECT bucket,CASE WHEN i<=len(b) THEN b[i] END AS upper_bound,
          CASE WHEN temporality='delta' OR reset THEN c[i]::HUGEINT
            ELSE c[i]::HUGEINT-previous_counts[i]::HUGEINT END AS n
        FROM steps,UNNEST(c) WITH ORDINALITY AS points(unused,i) WHERE type='histogram'),
      merged AS (SELECT bucket,upper_bound,sum(n)::DOUBLE AS n FROM bound_steps WHERE n>=0 GROUP BY bucket,upper_bound),
      cumulative AS (SELECT *,sum(n) OVER (PARTITION BY bucket ORDER BY upper_bound NULLS LAST ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS total,
        sum(n) OVER (PARTITION BY bucket ORDER BY upper_bound NULLS LAST ROWS UNBOUNDED PRECEDING) AS seen,
        lag(upper_bound) OVER (PARTITION BY bucket ORDER BY upper_bound NULLS LAST) AS lower_bound,
        max(upper_bound) OVER (PARTITION BY bucket) AS largest FROM merged),
      picked AS (SELECT *,row_number() OVER (PARTITION BY bucket ORDER BY seen,upper_bound NULLS LAST) AS rank
        FROM cumulative WHERE n>0 AND seen>=0.95*total)
      SELECT bucket,CASE WHEN total<=0 THEN NULL WHEN upper_bound IS NULL THEN largest
        WHEN lower_bound IS NULL AND upper_bound<=0 THEN upper_bound
        ELSE coalesce(lower_bound,0.0)+(0.95*total-(seen-n))/n*(upper_bound-coalesce(lower_bound,0.0))
        END::DOUBLE AS value FROM picked WHERE rank=1 ORDER BY 1`
}

func crosscheckMetricFrame(t *testing.T, engine Engine, d Dashboard, histogram bool, start,end time.Time) {
    t.Helper()
    e:=NewExecutor(engine,30);e.now=func()time.Time{return end}
    results,err:=e.Run(t.Context(),RunRequest{Dashboard:d});if err!=nil {t.Fatal(err)}
    if len(results)!=1||results[0].Status==StatusError||results[0].Frame==nil {t.Fatalf("panel=%+v",results)}
    r:=results[0];alias:="rate";if histogram {alias="p95"}
    times,values:=metricValues(t,r,"time"),metricValues(t,r,alias)
    direction:=[]any(nil);if !histogram {direction=metricValues(t,r,"direction")}
    actual:=map[string]any{}
    key:=func(bucket int64,dir string)string{raw,_:=json.Marshal([]any{bucket,dir});return string(raw)}
    for i,v:=range values {
        dir:="";if !histogram {dir=direction[i].(string)}
        k:=key(times[i].(int64),dir)
        if _,exists:=actual[k];exists {t.Fatalf("duplicate panel bucket %s",k)}
        actual[k]=v
    }
    name:=strings.TrimPrefix(d.Panels[0].Query.Where[0],"name = '")
    name=strings.TrimSuffix(name,"'");name=strings.ReplaceAll(name,"''","'")
    rows,err:=engine.QueryContext(queryrows.WithWindow(t.Context(),queryrows.Window{
        Start:start.Add(-10*time.Minute),End:end}),metricOracleSQL(histogram),start.Add(-10*time.Minute),end,name,start)
    if err!=nil {t.Fatal(err)};defer rows.Close()
    expected:=map[string]any{}
    for rows.Next() {
        var bucket int64;dir:="";var value any
        if histogram {err=rows.Scan(&bucket,&value)} else {err=rows.Scan(&bucket,&dir,&value)}
        if err!=nil {t.Fatal(err)}
        expected[key(bucket,dir)]=value
    }
    if err:=rows.Err();err!=nil {t.Fatal(err)}
    // Empty/no-elapsed/zero-total buckets may be present as explicit NULLs.
    // Compare every numeric bucket and require every other cell to be NULL.
    for k,v:=range actual {if v==nil {if want,exists:=expected[k];exists&&want!=nil {t.Fatalf("lost value at %s",k)};delete(actual,k)}}
    for k,v:=range expected {if v==nil {delete(expected,k)}}
    if len(expected)==0 {t.Fatal("fixture has no comparable metric buckets")}
    if !reflect.DeepEqual(actual,expected) {t.Fatalf("panel differs from independent SQL: panel=%v oracle=%v",actual,expected)}
}

func TestMetricDemoDataMatchesIndependentWindowSQL(t *testing.T) {
    input:=os.Getenv("FANOUT_PANEL_BENCH_DATA_DIR")
    if input=="" {t.Skip("FANOUT_PANEL_BENCH_DATA_DIR is unset")}
    data:=copyMetricCrosscheckData(t,input)
    engine,_:=panelBenchEngine(t,data)
    rows,err:=engine.QueryContext(t.Context(),`SELECT name FROM metrics WHERE type='histogram'
        GROUP BY name ORDER BY count(*) DESC,name LIMIT 1`)
    if err!=nil {t.Fatal(err)}
    var histogram string
    if !rows.Next() {rows.Close();t.Fatal("format-4 fixture needs a histogram")}
    if err:=rows.Scan(&histogram);err!=nil {rows.Close();t.Fatal(err)}
    if err:=rows.Close();err!=nil {t.Fatal(err)}
    for _,tc:=range []struct{name string;hist bool}{{"system.network.io",false},{histogram,true}} {
        d:=metricCrosscheckDashboard(tc.name,tc.hist,data.Start,data.End)
        crosscheckMetricFrame(t,engine,d,tc.hist,data.Start,data.End)
    }
}

func TestMetricOracleMatchesKnownFixture(t *testing.T) {
    e,db:=metricFixture(t)
    for i:=-1;i<3;i++ {
        m:=metricCounter(fixtureStart.Add(time.Duration(i)*time.Minute),float64(100+60*(i+1)))
        m.Attributes=map[string]any{"direction":"receive"};insertMetrics(t,db,m)
        h:=metricHistogram(fixtureStart.Add(time.Duration(i)*time.Minute),"[10,20]",fmt.Sprintf("[%d,%d,0]",10*(i+2),10*(i+2)),int64(20*(i+2)),float64(300*(i+2)),"a")
        h.Name="latency";insertMetrics(t,db,h)
    }
    end:=fixtureStart.Add(3*time.Minute)
    for _,tc:=range []struct{name string;hist bool}{{"net",false},{"latency",true}} {
        crosscheckMetricFrame(t,e.engine,metricCrosscheckDashboard(tc.name,tc.hist,fixtureStart,end),tc.hist,fixtureStart,end)
    }
}
```

The oracle selects valid finite points before deduplicating and uses its independently written windows. Exact float equality is required; do not round, use tolerances, skip mismatching buckets or turn a missing metric into a skip. Use deterministic aggregate ordering in production and oracle: rate numerators/denominators accumulate in `(time,ingested_unix_nano)` order, and across-series rates accumulate in the eight-column identity order. This prevents execution scheduling from changing rounding. Histogram bucket weights subtract in HUGEINT and sum exact integer contributions before one DOUBLE conversion, so percentile math receives the same count totals on both paths. Log only metric names, bounded bucket/count summaries and assertion values; never the input path or raw telemetry.

- [ ] **Run red, then verify the oracle catches a wrong production result.** Before implementing copyMetricCrosscheckData the new readbench test fails to compile because that helper is absent. After adding the helper, the normal scalar/histogram paths may already be correct. The controller's temporary mutation rehearsal changes Task 4's rate denominator to bucket seconds, runs the 10-second regression below to observe failure, and restores it. It then temporarily changes histogram quantile to quantile_cont(value), runs the synthetic cross-check to observe failure, and restores it. No mutation enters the controller's commit. This validates independence without asserting that an already-correct implementation must fail its oracle.

```bash
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run TestMetricOracleMatchesKnownFixture -count=1
rtk bash scripts/with-duckdb.sh go test ./internal/panel -run TestMetricRateUsesCoveredElapsedTime -count=1
```

Expected under rate mutation: 10 s rate is 6 instead of 1. Under value-percentile mutation: histogram returns 0 instead of its interpolated 19. No mutation command or git operation is prescribed.

- [ ] **Implement the read-only copy and consume data supplied by private tooling.** This helper copies each batch after panelReplayMetadata has accepted all metadata as format 4, before opening a repository. It does no tiling, schema conversion or inferred-fidelity upgrade. Controller data is never opened by telemetrystore.Open in place.

```go
func copyMetricCrosscheckData(tb testing.TB, input string) panelBenchData {
    tb.Helper()
    metadata,err:=panelReplayMetadata(input);if err!=nil {tb.Fatal(err)}
    data:=panelBenchData{Dir:filepath.Join(panelBenchScratch(tb),"state")}
    low,high:=int64(math.MaxInt64),int64(math.MinInt64)
    for _,m:=range metadata {
        names:=[]string{"metadata.json"}
        if m.Spans>0 {names=append(names,"spans.parquet","trace.fidx")}
        if m.Logs>0 {names=append(names,"logs.parquet")}
        if m.Metrics>0 {names=append(names,"metrics.parquet")}
        dst:=filepath.Join(data.Dir,"telemetry","parquet","batches",m.ID+telemetry.BatchSuffix)
        if err:=os.MkdirAll(dst,0o755);err!=nil {tb.Fatal(err)}
        for _,name:=range names {
            in,err:=os.Open(filepath.Join(input,"parquet","batches",m.ID+telemetry.BatchSuffix,name));if err!=nil {tb.Fatal("cannot open cross-check input")}
            info,err:=in.Stat();if err!=nil||!info.Mode().IsRegular() {in.Close();tb.Fatal("cross-check files must be regular")}
            out,err:=os.Create(filepath.Join(dst,name));if err!=nil {in.Close();tb.Fatal(err)}
            _,copyErr:=io.Copy(out,in)
            if err:=errors.Join(copyErr,out.Close(),in.Close());err!=nil {tb.Fatal("cannot copy cross-check input")}
        }
        data.Spans+=int64(m.Spans);data.Logs+=int64(m.Logs);data.Metrics+=int64(m.Metrics)
    }
    // Only the private copy is opened; obtain the authoritative metric footer bounds.
    repo,err:=telemetrystore.Open(filepath.Join(data.Dir,"telemetry"));if err!=nil {tb.Fatal(err)}
    for _,m:=range repo.Parquet.BatchMetadata() {
        if m.Metrics>0&&m.MetricTime.Known {low=min(low,m.MetricTime.MinNanos);high=max(high,m.MetricTime.MaxNanos)}
    }
    if err:=repo.Close();err!=nil {tb.Fatal(err)}
    if low>high {tb.Fatal("cross-check fixture has no known metric event-time bounds")}
    data.Start=time.Unix(0,low).UTC()
    data.End=time.Unix(0,high).UTC().Add(time.Nanosecond)
    if data.End.Sub(data.Start)>24*time.Hour {data.Start=data.End.Add(-24*time.Hour)}
    return data
}
```

Retain bytes of all source batches and source staging. Outside this repository, private tooling generates format 4 through OTLP ingest; if its demo source lost sum monotonicity, that **external replay** assigns monotonicity using the source metric's OpenTelemetry semantic convention before ingest, keeps updown memory metrics non-monotonic, and supplies original temporality/start-time facts. No guessed monotonicity is added to Fanout or to the copy helper. This plan does not implement or modify that private replay. Missing source facts are recorded by the private tooling; they do not authorize relabelling existing format-3 Parquet as format 4.

The existing tilePanelParquet format-4 helper must preserve and shift nonzero start_time_unix_nano. Add this actual test to panel_bench_test.go (readbench imports already include filepath/os and telemetry/store):

```go
func TestMetricReplayCopyPreservesSourceAndStartTime(t *testing.T) {
    input:=t.TempDir();repo,err:=telemetrystore.Open(input);if err!=nil {t.Fatal(err)}
    m:=metricCounter(fixtureStart,100);m.StartTimeUnixNanos=fixtureStart.Add(-time.Minute).UnixNano()+17
    if err:=repo.Commit(t.Context(),telemetrystore.Batch{ID:"metric",Metrics:[]telemetry.Metric{m}});err!=nil {t.Fatal(err)}
    if err:=repo.Close();err!=nil {t.Fatal(err)}
    metaPath:=filepath.Join(input,"parquet","batches","metric.batch","metadata.json")
    before,err:=os.ReadFile(metaPath);if err!=nil {t.Fatal(err)}
    data:=copyMetricCrosscheckData(t,input);engine,cfg:=panelBenchEngine(t,data)
    var start int64
    if err:=engine.DB.QueryRow("SELECT epoch_ns(start_time) FROM metrics WHERE name='net'").Scan(&start);err!=nil||start!=m.StartTimeUnixNanos {t.Fatalf("start=%d err=%v",start,err)}
    after,err:=os.ReadFile(metaPath);if err!=nil||string(after)!=string(before) {t.Fatal("source metadata changed")}
    if _,err:=os.Stat(filepath.Join(input,"query"));!os.IsNotExist(err) {t.Fatal("source query directory created")}
    raw:=filepath.Join(cfg.QueryDir(),"shifted.parquet")
    metadata:=telemetry.BatchMetadata{Version:4,ID:"shifted"}
    if err:=tilePanelParquet(filepath.Join(input,"parquet","batches","metric.batch","metrics.parquet"),raw,"metrics","",fixtureStart,fixtureStart.Add(time.Minute),time.Hour,&metadata,map[string]bool{},map[string]bool{});err!=nil {t.Fatal(err)}
    var shifted int64
    if err:=engine.DB.QueryRow("SELECT start_time_unix_nano FROM read_parquet(?)",raw).Scan(&shifted);err!=nil||shifted!=m.StartTimeUnixNanos+int64(time.Hour) {t.Fatalf("shifted=%d err=%v",shifted,err)}
}
```

- [ ] **Run green with and without the environment variable and run the final gate.** The controller sets FANOUT_PANEL_BENCH_DATA_DIR privately to the telemetry root; the plan and output do not contain its value.

```bash
rtk proxy env -u FANOUT_PANEL_BENCH_DATA_DIR bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run 'TestMetricDemoDataMatchesIndependentWindowSQL|TestMetricOracleMatchesKnownFixture|TestMetricReplayCopyPreservesSourceAndStartTime' -count=1 -v
rtk bash scripts/with-duckdb.sh go test -tags=readbench ./internal/panel -run 'TestMetricDemoDataMatchesIndependentWindowSQL|TestMetricOracleMatchesKnownFixture|TestMetricReplayCopyPreservesSourceAndStartTime|TestPanelBenchmarkReplay' -count=1 -v
rtk just check
```

First command: demo SKIP, synthetic/copy PASS. Second command with controller data: both requested metrics have comparable buckets, every numeric rate and percentile equals the independently executed SQL, missing/extra numeric buckets fail, input remains unchanged. Without data that second demo run is still SKIP and S14's demo evidence remains unmeasured; synthetic success does not substitute for it. Every task, including this one, requires the final just check pass.

- [ ] **Controller commit message:** `test(panel): cross-check metric rates and quantiles on format 4 data`

## Self-review

### Spec coverage list

- Storage columns: temporality dict string, monotonic bool, start_time_unix_nano int64 → Task 1 canonical rows, Parquet, querytest and round-trip/view tests.
- OTLP-only stored kinds; delete sum_delta and exp_histogram; retain every point's start/monotonicity/temporality → Task 1 ingest and mechanical reader audit; unspecified aggregate temporality fails explicitly; Task 1 corrects the nesting fixture and readbench semantic/baseline fixtures.
- Format 4 only; reject/preserve format 3 before cleanup and schema rewrite; validate physical schema and decode all columns → Task 1 format/schema tests and existing verifier/repair guards; storage-model.mdx format references update to 4.
- Exponential explicit conversion has one more count than bounds and preserves zero/negative observations → Task 2 real ascending negative/positive bounds with independent offsets, zero-only/negative-only/empty goldens; Task 5 exact negative-only p75=-4 execution and exponential query shape.
- Metrics view semantic gauge/counter/updown/histogram/summary, temporality and nullable nanosecond start_time → Task 1 view execution test; Task 6 catalog/schema fields, preserving MetricSchema.type and adding only temporality (no kind field).
- Discover kind/temporality per observed metric metadata combination → Task 6 native schema discovery test, no arbitrary any_value selection.
- Eight-field series identity; two-stage per-series/per-bucket then per-group measures → Tasks 3–4; all eight fields are grouped directly, not encoded via a lossy string/hash.
- Ordered consecutive points, latest retry per timestamp, finite values, value/start/count/bounds resets → Task 3 builder/reset tests; Task 4 finite-value and restart executor tests; Task 3 shared distribution reset counts.
- Ten-minute previous-window read, snapshot pruning and matching executor windows; discard outputs outside original half-open scope → Task 3 separate-batch regression; Task 4 structured lookback, comparison/totals/trends.
- Cumulative first point baseline, delta sums/elapsed/start time and missing elapsed → Task 3 delta tests; Task 4 sum/rate test; Task 3 delta distribution observation weights; Task 5 NULL rate without elapsed.
- Counter rate sums each series' total increase/total covered elapsed; export interval independent of bucket width; no carry into empty buckets → Task 4 10 s/1 m/1 h, multiple-series/elapsed/restart tests; Task 7 independent demo oracle (S14).
- Automatic metric bucket at least 1 minute, fixed sub-minute buckets allowed → Task 4 interval caller clamp and rate test; existing point/cell caps remain exercised.
- Gauge/updown sum and avg over per-series averages; sum of latest; counter increase sum/latest; min/max/quantiles over supported raw points → Task 4 kind/series-weight goldens and auxiliary aggregate regression; non-value numeric fields keep per-point aggregation for every kind in Tasks 4–5, verified by TestMetricNonValueFieldsKeepPerPointAggregates (including the literal attribute key value).
- count/count_distinct/share keep point meanings; undefined pairs and summary value aggregates NULL, zero remains zero → Task 4 table goldens and NULL/empty diagnosis; Task 5 histogram incompatibility tests.
- Name fixed by literal/single variable without All or grouping by name; count/count_distinct exception; All/multi/OR/field equality cannot qualify → Task 4 parsed-equality validation and All-selection rejection (Review Focus 5).
- Empty kind mismatch names metric/kind and fitting measures → Task 4 exact updown diagnosis and Task 5 histogram compatibility update.
- Histogram rate=count increase/elapsed, sum=sum increase, avg=total sum/total count → Task 5 two-series goldens, same Task 3 reset builder.
- Histogram pNN/quantile linear interpolation over summed bound increases, first positive lower bound 0, overflow highest finite, zero total NULL → Task 5 known distributions/edge cases and Task 7 independent p95 oracle (S15).
- Heatmap/histogram panels share increases and stored temporality; remove authored option everywhere and reject old specs → Task 3 production distribution integration, strict-decode/MCP exact guide tests, TS contract and generated references; Task 5 retains histogram measures.
- Agent guide table <=900 characters and counter-rate/histogram-p95 examples kept valid → Task 6 character/validation/preview/uniqueness tests (Phase A groundwork for S22; paid S22 eval belongs to Phase E).
- Dashboard guide metric section and current generated reference → Tasks 3/6 repo generator/gate; no hand-edit of generated MCP/eval bytes.
- Demo monotonicity restoration belongs only to private tooling outside the repository; format-4 readbench cross-check skips only when variable unset → Tasks 1/7 metadata/start-time compatibility and read-only copy/oracle. No committed replay implementation, product legacy reader or inference; shifted test output stays under cfg.QueryDir() and the oracle filters per-bound n>=0 like production.
- S16 fidelity → Tasks 1–2 canonical ingest tests plus physical/view verification; S14 → Tasks 3–4/7; S15 → Tasks 3/5/7.
- Phase A-relevant Testing bullets (native-engine execution; resets, interval changes, delta/late/out-of-order/new series, empty/overflow buckets) → Tasks 1–5/7. Summary-route equality, sketch approximation, summary lifecycle, scale benchmark, fixture-stack/browser/real-Collector evidence and S17–S21 remain Phases B–E; no claim that this plan implements or verifies them.
- Review Focus 1 → Task 4; 2 → Tasks 3–4; 3 → Task 3; 4 → Task 3; 5 → Task 4. Every focus has an explicitly named behavior test with real fixture/assertion code.

### Placeholder scan

- No unfinished marker, deferred validation instruction, cross-task implementation shortcut or code-free test step. New private function/type signatures are declared in their owning tasks; Task 3 production distributions call metricHistogramRaw → metricSeriesCTE, Task 4 compiler/validation/diagnosis call their new helpers, Task 5 compiler calls histogramQuantileSQL, Task 6 guides attach to tool descriptions, and Task 7 adds test-only helpers. No non-test symbol waits for a later task to obtain a production caller under deadcode -test=false; test helpers are defined before downstream use. Existing signatures and file paths were verified locally. Create paths are explicitly new in verified directories.
- Red/green commands use the pinned native wrapper and behavioral test names. All seven tasks require `rtk just check`; controller commit steps contain only commit messages. No planning-time Go execution, git mutation, network or browser work is claimed.
- Every SQL code block states its actual input/output/bind contract. The implementation instructions specify integration into compiler, items, executor, totals/comparison, table trends, snapshot scope, diagnosis, discovery and generated contracts. Expected values are calculations for future tests, not reported execution results. Multi-operation floating-point expectations use typed runtime variables; exact quantile goldens use representable ranks/results (p75=65 over [10,30,100] and p75=16.25 for the weighted histogram), avoiding rounded p95/p99 constants.

### Type consistency

- Canonical StartTimeUnixNanos is int64; physical StartTimeUnixNano maps exactly to start_time_unix_nano BIGINT; 0 maps to NULL public start_time TIMESTAMPTZ_NS. Both input/output windows bind time.Time.UTC through nanosecond casts; frame buckets are int64 Unix milliseconds.
- Stored Type remains the OTLP kind, while view type/MetricSchema.Type are semantic strings. MetricSchema gains only Temporality, with no Kind member or kind JSON field. Temporality is data, never a panel option; Monotonic is bool in canonical/Parquet/querytest rows. Histogram counts parse as UBIGINT, subtract via HUGEINT, expose DOUBLE; hist_count remains existing int64 storage.
- Scalar/count/sum increases and elapsed_s are nullable DOUBLE; only positive elapsed participates in rate numerator/denominator. Bounds are DOUBLE[], stored counts UBIGINT[], and bound increases HUGEINT[] internally, summed before conversion to DOUBLE for interpolation/frame counts; nested results become JSON only at the client/test boundary. Empty lists and NULL aggregates are distinguished from a numerical zero.
- Measure aliases, Column/Frame roles/units and variable Value bindings retain existing contracts. Problems use JSON paths and hints; operational/protocol errors retain their existing envelopes. Existing generated docs and TS contracts are refreshed in the owning contract-change task, not through a second schema source.
