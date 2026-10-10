# Agent dashboards milestone 5, Phase B: system page Implementation Plan

> **For agentic workers:** Implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Proposed files and interfaces are explicitly identified below; existing paths and contracts were inspected in this checkout.

Tasks 1–2 are one backend dependency checkpoint: write Task 1's failing contract tests, implement its discovery/route and Task 2's fixed section builders, then run both tasks' green checks before starting the UI. Do not add temporary SQL, stub panels or accept a partially populated dashboard as completion of Task 1.

**Goal:** Ship S20's read-only system page at `/`: reachable, failing or slow, running out, then what changed. Preserve Phase A's metric correctness and close S22 with the existing agent tools and public host-metric prompts.
**Architecture:** One `panel.Executor.System` method discovers the selected window and namespace and builds an ordinary dashboard spec. One `GET /api/system` returns it. The browser executes that spec through the existing panel batch API and renders it through `PanelGrid`. Fixed server-written SQL supplies infrastructure and reachability rows; existing health, service map and annotation components supply service context.
**Tech Stack:** Existing Go, pinned DuckDB 2 through `scripts/with-duckdb.sh`, OTLP, React/TypeScript, Mantine, TanStack Query/Router, ECharts, Bun/Vitest, Playwright and `fanout-docgen`. No new dependencies or frameworks. Collector Contrib is a verification prerequisite, not a Fanout integration.
**Spec:** `docs/superpowers/specs/2026-10-10-agent-dashboards-m5-design.md`, Phase B, S20/S22 and Phase C. Binding dispatch: `.superpowers/sdd/m5/system-page-plan-brief.md`. Format precedents: the M5 Phase A and M4 plans.

## Global Constraints

- This dispatch is planning only and writes exactly this file. No implementation, extra reports, git writes, network, browser tools, CodeGraph initialization or questions. Commands and other file edits below are future implementation/verification instructions, not claims that they ran. The controller owns final commits and external verification.
- Binding direction: “Over engineering … move on to the features; performance later.” Keep the six tasks below small. No new renderer, panel type, framework, cache, summary table, metric accelerator, benchmark suite, configuration knob or per-source integration. Existing demo-volume performance tests remain a Phase C regression gate; scale benchmarks and tuning are deferred.
- Phase A is complete. Read and preserve `metric_series.go`, `metric_measures.go`, `metric_buckets.go` and `metric_histogram.go`: series identity, deduplication, reset/start-time handling, covered-elapsed rates, delta temporality, bucket spreading, five-minute carry, exclusive ends, NULL for undefined measures and histogram interpolation. SQL tables implement those rules where they need joins and ratios; they do not introduce a second metric engine or change structured measures.
- Follow `AGENTS.md`: sole batch format 4; native pinned engine; typed VARIANT maps and literal dotted keys; UTC connections; signed nanoseconds; native `TIMESTAMPTZ_NS` predicates and `?::TIMESTAMP_NS::TIMESTAMPTZ_NS` binds; immutable published migrations. No control-schema, telemetry-schema, engine-pin or read-cache changes are needed.
- SQL remains a single guarded read-only SELECT over approved telemetry relations, parsed/described/executed on the existing connection and pinned file snapshot. Generated SQL receives no privileged bypass. Its counter lookback must reach files preceding the selected range; public arbitrary SQL must not acquire dashboard-based file pruning. Task 2 supplies that necessary snapshot correction without a new engine path.
- Scope is the selected range and namespace. Keep identities separate across namespaces, hosts, containers and check vantages. No demo host names, telemetry-derived identifiers or service names become hard-coded filters. A missing section becomes exactly one text panel naming its receiver and linking to the setup guide. Partial infrastructure keeps the reporting table and explains the missing receiver; a missing value never becomes a healthy zero.
- Freshness is separate from value carry. Rows known in the selected range remain visible when they stop reporting, even after their numeric values become NULL at five minutes. At two reporting intervals they are bad: `no recent result` for checks, `not reporting` for hosts/containers. Assess age against the selected exclusive end, so historical windows have historical states.
- Use RTK for every command. Before future Go checks, create the prescribed temporary/cache directories, then use the environment prefix shown in each command. Do not run Go or create caches during this planning dispatch.

  ```bash
  rtk mkdir -p .superpowers/go-cache /private/tmp/claude-501/fanout-m5-gotmp
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/panel
  ```

- Execute every SQL-producing test on the pinned engine, including at least one format-4 Parquet snapshot test. Fake API engines test HTTP behavior only. Each task ends with relevant targeted checks and `rtk just fmt`; regenerate affected docs/UI before their freshness checks. The final gate is `just check` with the same Go environment. Dependency installation and browser/Collector runs belong to the controller when unavailable in a restricted dispatch; a skipped check is not a pass.
- Paid calls occur only in Task 6. The **entire M5 milestone shares one $5 ledger**. Task 6 runs only the public S22 host-metric prompts (estimated $1–2) unless the user approves a full S1–S5 rerun. Use existing admission, reserve and usage settlement; unknown usage stops further calls. Do not read or tune against the sealed holdout. Runtime secrets come from the existing private `.env` loading used by the eval harness and are never printed. Never expose secret values in prompts, output or committed files.
- No release, deployment, remote demo mutation, git writes or product alert/incident work. Documentation states that Fanout cannot report its own machine failing and needs an outside monitor.

## Review Focus

1. All four sections keep the question order, render through existing panels, and have receiver-specific empty states; fresh discovery must see new telemetry within two refreshes.
2. Check identity includes endpoint, method, namespace and reporting `host.name`; silence is bad, and the same-host vantage warning is based on observed service hosts.
3. Host/container rates and ratios follow Phase A; two-interval `not reporting` survives five-minute value expiry and ranks ahead of healthy rows.
4. `/` and System navigation work with or without an agent; one refresh has one panel batch, at most one annotation request, no saves or duplicate refresh loops, and deploy markers appear on the health trend.
5. Six OTLP scenarios, one real-Collector run and final S1–S16/S20/S22 evidence are explicit; public S22 prompts and the shared $5 cap do not touch sealed inputs or expand into performance work.

## Task 1: Build and expose the generated system dashboard

**Files**

- Create: `internal/panel/system.go`, `internal/panel/system_test.go`, `internal/api/system.go`, `internal/api/system_test.go`.
- Modify: `internal/panel/schema.go`, `internal/api/auth_middleware.go`, `internal/api/auth_test.go`, `cmd/fanout/main.go`, `cmd/fanout-docgen/routes_test.go`.
- Generate: `site/src/content/docs/reference/http-routes.mdx` and any other actual `docs-generate` outputs; do not hand-edit generated references.

**Interfaces**

Existing: `Executor.Schema(ctx, SchemaRequest)`, `Executor.Validate(ctx, *Dashboard)`, `resolveWindow`, `Schema.Signals`, `MetricSchema.Type`, `RequireCapability(ReadTelemetry)`, `panelError`, `DescribeRoute` and `RegisterPanelRoutes`.

New:

```go
type SystemRequest struct {
    Time      Time
    Namespace string
}
func (e *Executor) System(ctx context.Context, req SystemRequest) (*Dashboard, error)
func (e *Executor) discoverSchema(ctx context.Context, start, end time.Time, namespace string) (*Schema, error)
type SystemEngine interface {
    System(context.Context, panel.SystemRequest) (*panel.Dashboard, error)
}
func RegisterSystemRoutes(e *echo.Echo, engine SystemEngine)
```

HTTP: `GET /api/system?range=1h&namespace=shop`, or `from`/`to` RFC3339Nano instead of `range`. Defaults are `1h` and all namespaces. Return the dashboard directly; its `time` contains the resolved absolute `from`/`to` and `refresh: "30s"`. No saved-dashboard record, owner, version history or writable system resource. Reject malformed/conflicting bounds through `panel.Problems`; keep existing `{code,message}` errors. Preserve existing time/retention validation rather than inventing a second list of allowed ranges.

- [ ] **Write native fixture tests before the builder.** Use `metricFixture`, `insertMetrics`, `shopSpans` and existing rollup test helpers. Cover empty telemetry, each section alone, all sections, a partial infrastructure section, namespace exclusion and absolute/relative range boundaries including a point exactly at `to`. Assert panel order, known viz types, stable IDs, valid specs and executed results, not complete SQL strings. Proposed tests: `TestSystemSectionsFollowAvailableTelemetry`, `TestSystemDiscoveryUsesSelectedWindowAndNamespace`, `TestSystemDiscoverySeesNewTelemetryOnNextRefresh`.
- [ ] **Extract the existing uncached discovery body and reuse it.** `Schema` keeps its current public one-minute cache and response shape; `System` invokes `discoverSchema` afresh on every request. Set the exact `queryrows.Window` before discovery and close every result set. Do not call `Schema` and wait for its TTL, round absolute bounds to a relative range, or add another cache. Reuse the metric-name/kind and service discovery queries. Because public discovery caps metric names at 300 and samples resource keys, use one exact, window/namespace-scoped presence query for the small fixed metric family list and resource-version presence; sampled/capped absence cannot suppress a real section. Query existence/counts, not every metric point into Go.
- [ ] **Assemble one ordinary dashboard.** Resolve time once, use stable static IDs, pack panels in order, normalize and run `Executor.Validate`. Use fixed SQL and panel specs from Task 2. Resource values stay query data; only the namespace filter is safely quoted with existing `sqlString`. The following helpers are complete additions to `system.go`:

  ```go
  const systemSetupURL = "https://fanout.run/guides/watch-your-system"

  func systemMissing(id, title, receiver string) Panel {
      return Panel{ID: id, Title: title, Viz: "text", Width: 12, Height: "s",
          Content: "No telemetry for this section in the selected range and namespace. Add the Collector `" + receiver + "` receiver. [Watch your system](" + systemSetupURL + ")."}
  }

  func systemScopedSQL(text, namespace string) string {
      predicate := ""
      if namespace != "" {
          predicate = " AND namespace = " + sqlString(namespace)
      }
      return strings.ReplaceAll(text, "/* system namespace */", predicate)
  }

  func systemPack(d *Dashboard) {
      y := 0
      for i := range d.Panels {
          p := &d.Panels[i]
          h := 6
          if p.Height == "s" { h = 3 }
          if p.Height == "l" { h = 9 }
          p.Grid = &Grid{X: 0, Y: y, W: 12, H: h}
          y += h
      }
  }
  ```

  Keep layout deliberately simple: full-width panels in question order, with question headings in the first panel title of each section. A populated section has its specified panels; a wholly absent one has only its fallback panel. Receiver mapping: reachable → `httpcheck`; failing or slow → `otlp` traces; running out → `hostmetrics` and `docker_stats`; what changed → `otlp` with service resource attributes. Hosts-only or containers-only data keeps the available table and one text panel naming the other receiver. Operational discovery failures return errors, not receiver instructions.
- [ ] **Register one read route with existing authorization.** Add the registration beside `RegisterPanelRoutes` in `cmd/fanout/main.go`. Keep `PanelEngine` unchanged by using the small separate interface. Handler core:

  ```go
  type SystemHandler struct{ engine SystemEngine }

  func RegisterSystemRoutes(e *echo.Echo, engine SystemEngine) {
      h := &SystemHandler{engine: engine}
      e.GET("/api/system", h.get, RequireCapability(ReadTelemetry))
  }

  func (h *SystemHandler) get(c *echo.Context) error {
      req, err := parseSystemRequest(c)
      if err != nil { return panelError(c, err, "system unavailable") }
      ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
      defer cancel()
      spec, err := h.engine.System(ctx, req)
      if err != nil { return panelError(c, err, "system unavailable") }
      c.Response().Header().Set("Cache-Control", "no-store")
      return c.JSON(http.StatusOK, spec)
  }
  ```

  New `parseSystemRequest(c *echo.Context) (panel.SystemRequest, error)` parses timestamps with `time.RFC3339Nano`, requires both absolute endpoints, rejects `range` with absolute bounds, and defaults only a genuinely omitted range. In `classifyRoute`, classify exactly `/api/system` as `ReadTelemetry` for reads, following the existing GET/HEAD policy. Test unauthenticated, inactive/revoked session, viewer/operator/admin reads, rejected mutation methods, sanitized failures, cancellation and deadlines. Add the generated-route authorization assertion. No alternate routes or MCP tool.
- [ ] **Run targeted checks and regenerate references.**

  ```bash
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/panel ./internal/api ./cmd/fanout-docgen
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just docs-generate
  rtk just fmt
  ```

## Task 2: Supply the four sections with fixed telemetry queries

**Files**

- Create: `internal/panel/system_sql.go`, `internal/panel/system_sql_test.go`.
- Modify: `internal/panel/system.go`, `internal/panel/items.go`, `internal/panel/check.go`, `internal/panel/exec.go`, `internal/panel/frame.go`, `internal/panel/validate_test.go`, `internal/panel/annotation_scope.go`, `internal/panel/rollup_panels.go`, `internal/panel/rollup_panels_test.go`, `internal/panel/table_semantics_test.go`, `internal/panel/table_trends_test.go`, `internal/observability/health_trend.go`, `internal/observability/health_trend_test.go`.
- Reuse: `internal/panel/metric_*.go`, `internal/panel/rollup_panels.go`, `internal/panel/testhelpers_test.go`, `internal/observability/service.go`, `internal/annotations/service.go`.
- Update only affected SQL-capability guidance: `internal/mcp/panels.go`, `internal/mcp/panel_examples.go`; regenerate references in Task 5.

**Interfaces**

New fixed builders:

```go
func systemReachabilityPanels(namespace string) []Panel
func systemServicePanels(namespace string) []Panel
func systemCapacityPanels(namespace string, hosts, containers bool) []Panel
func systemVersionPanel(namespace string) Panel
```

They return existing `state_timeline`, `timeseries`, `health`, `service_map` and `table` panels. Ordinary `text` panels carry setup and vantage messages. SQL templates use `$__window(time_column)` and existing `$__from`/`$__to` bindings. `systemScopedSQL` substitutes only the static namespace comment; it does not rewrite authored SQL or introduce a macro language.

- [ ] **Establish native SQL and snapshot regressions.** Add `TestSystemCheckSilenceIsBadAfterTwoIntervals`, `TestSystemCapacityUsesRatesLatestValuesAndThresholds`, `TestSystemStoppedResourcesRemainVisible`, `TestSystemVersionsReadAllSignals`, `TestSystemSQLReadsCounterLookbackFromSeparateParquetFiles`. Execute `System` then `Run` against real tables; for the last test use `newTestEngine`, repository commits with `metricSeriesIDs`, and distinct files whose event-time footers lie before/inside the range. Use Phase A structured panels as an independent comparison for scalar rates/latest values. Include resets, delta points, duplicate/retried and out-of-order timestamps, mixed cadences, namespace collisions, exclusive ends, missing/zero denominators, a stopped container, and stale numeric values. Assert actual frame cells and status labels.
- [ ] **Keep arbitrary SQL's snapshot complete.** `runScope` currently attaches the dashboard read window to SQL as well as structured panels. Before `queryrows.WithWindow`, SQL panels instead use bounds covering all active signed-nanosecond files; SQL predicates still enforce their own selected range and ten-minute lookback. This preserves the existing pinned snapshot/lease and AST guard and adds no storage path or tuning option. Import `math` and use:

  ```go
  read := metricReadScope(p, scope)
  if p.SQL != "" {
      read.Start = time.Unix(0, math.MinInt64+1).UTC()
      read.End = time.Unix(0, math.MaxInt64-1).UTC()
  }
  ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: read.Start, End: read.End})
  ```

  Add an ordinary SQL-panel regression that reads an explicitly earlier interval as well as the required window. Do not add a “system” flag, infer trust from a panel ID, or widen public SQL validation permissions.
- [ ] **Define reporting intervals from actual observations, without a setting.** Standard OTLP points do not promise a collection-interval field. For this first implementation, use the latest positive gap between distinct event timestamps of the reporting stream. Check streams group by namespace + URL + method + reporter host across status-class series. Host witnesses prefer `system.cpu.time`, then memory, then filesystem; container witnesses prefer CPU utilization, then total memory. Infer cadence on the chosen metric family, not by interleaving unrelated scrapers. Deduplicate timestamps before lagging; retries never shorten the interval. A one-point stream says `reporting interval unknown` and has an unknown/bad state until cadence can be established; never invent a default interval or call silence healthy. This limitation and the selected-range inventory boundary belong in the guide. Use nanosecond integers for gaps/ages. For each identity, the following SQL is the concrete cadence stage after projecting its witness points into `witness(namespace, entity, time)`:

  ```sql
  WITH distinct_times AS (
    SELECT DISTINCT namespace, entity, time FROM witness
  ), gaps AS (
    SELECT *, epoch_ns(time::TIMESTAMP_NS)::HUGEINT -
      epoch_ns(lag(time) OVER (PARTITION BY namespace, entity ORDER BY time)::TIMESTAMP_NS)::HUGEINT AS gap_ns
    FROM distinct_times
  )
  SELECT namespace, entity, max(time) AS last_seen,
         arg_max(gap_ns, time) FILTER (WHERE gap_ns > 0) AS interval_ns
  FROM gaps GROUP BY namespace, entity
  ```

  Join this metadata back to the complete selected-range entity inventory. Age is `epoch_ns($__to::TIMESTAMP_NS)::HUGEINT - epoch_ns(last_seen::TIMESTAMP_NS)::HUGEINT`; `age >= 2 * interval_ns` crosses the bad boundary, including equality. Hosts/containers keep rows after five minutes, but expired measurements are NULL. Test one nanosecond below/at the boundary, first observation, interval changes and a resumed stream.
- [ ] **Reachable: timeline and latency with distinct vantages.** Recognize `httpcheck.status` and `httpcheck.duration` from discovered kinds/units. Read literal attributes `http.url`, `http.method`, `http.status_class`/`http.status_code`, and resource `host.name`. Fold zero/one status-class points into one check attempt: a positive 2xx/3xx result is reachable; an active failed attempt is down. Keep check identity independent of changing response codes. Collector fixture/real exports in Tasks 4/6 must confirm the receiver's actual kind, unit and attributes; unsupported/missing metadata is explained, never guessed into success.

  The timeline emits native timestamp `time`, first string dimension `endpoint_from`, numeric measure `failure`, and optional string `state`. Use `failure=0`/`state='reachable'`, `failure=1`/`state='down'`, `failure=2`/`state='no recent result'`; a lone observation without cadence uses `failure=2`/`state='reporting interval unknown'`. Set a bad threshold at 1 with `better: "lower"`. Generate minute buckets between the selected bounds, including the current partial bucket. For each bucket use the last attempt strictly before its clipped end and test silence against that end; do not forward-fill success indefinitely. Emit current trouble first under the existing SQL row cap, then order retained rows for display. Surface truncation rather than silently claiming every endpoint/history is shown.

  Minimal compatibility addition: allow **SQL on the existing `state_timeline` type** in `validateItems`, retaining thresholds, exactly-one query/SQL and all SQL guards. This avoids a new panel type or a second reachability renderer. SQL timelines use a fixed one-minute interval in `runPanel`, matching the generated rows; structured timelines keep their existing bucket behavior. Validate described SQL timeline columns (timestamp `time`, first item string and one numeric measure), and run `boundAnalysisFrame` after dynamic scanning. Test rejected shapes, retained newest bad states and normal structured timeline behavior. Keep the 8,000-character SQL limit, existing row/cell limits and `vizOrder` unchanged.

  Latency uses the `last(value)` rule for discovered gauge/updown duration or a percentile only if the discovered duration is a histogram. Use a SQL timeseries to project the combined endpoint/method/host item label: the existing structured timeseries supports only one grouping field and cannot concatenate these keys. Apply Phase A's per-series latest rule at each bucket end, and sum only series of that same check/duration metric. Never use point-count `rate()` as latency or select `avg` for counters. Convert the receiver's reported duration unit to the existing supported display unit without guessing. The scoped same-host warning is a text panel only when every identified reporter host also occurs on an observed application service's span resources in that same window/namespace. Obtain those identities with exact scoped distinct queries, not Schema's resource-key sample or capped service list. Unknown reporter identity explicitly says the vantage is unknown; one outside reporter removes the “every check is local” warning. No DNS/CDN/proxy coverage claim follows merely from successful local checks.
- [ ] **Failing or slow: reuse health and service map.** Generate these exact structured shapes with the namespace equality added to both only when selected:

  ```go
  func systemServicePanels(namespace string) []Panel {
      where := []string{}
      if namespace != "" { where = append(where, "namespace = " + sqlString(namespace)) }
      return []Panel{
          {ID: "service_health", Title: "Is anything failing or slow?", Viz: "health", Width: 12, Height: "m",
              Query: &Query{From: "spans", Where: where}},
          {ID: "service_map", Title: "Services and external dependencies", Viz: "service_map", Width: 12, Height: "l",
              Query: &Query{From: "spans", Where: where}},
      }
  }
  ```

  Set the executor's existing rollup reader in tests, as production already does. Seed a failing client span with its standard external-peer attributes and assert the existing dependency node/edge is bad. Preserve analytical watermark behavior; fixture readiness can wait for actual publication, but no new rollup or polling service is added.
- [ ] **Running out: two SQL tables, current values and worst-first order.** Host keys are namespace + resource `host.name`; container keys include namespace + host + `container.id`, with `container.name` as the label. If stable identity is absent, explain it instead of merging anonymous machines. Project only the required typed scalar keys. Do not combine different metric names before their individual value/rate stages.

  Host CPU: `system.cpu.time` is a counter. Order/deduplicate by `series_id`, honor reset/start changes and delta temporality, and compute a time-weighted rate over the selected window. For a covered interval `(previous,time]`, weight by its overlap with the selected range; gaps longer than ten minutes use Phase A's later-point attribution; rate alone carries the newest completed rate at most five minutes when no real coverage exists. Sum the per-series rates by CPU state; busy percent is `100 * sum(non-idle rates) / nullif(sum(all state rates),0)`, including all CPUs consistently. These are the scalar stages, not counts of export points:

  ```sql
  CASE WHEN temporality = 'delta' OR value < previous_value OR
       (start_time IS NOT NULL AND previous_start IS NOT NULL AND start_time <> previous_start)
       THEN value ELSE value - previous_value END AS increase
  ```

  ```sql
  SELECT series_id, sum(rate_share) FILTER (WHERE covered_s > 0) /
         nullif(sum(covered_s) FILTER (WHERE covered_s > 0), 0) AS cpu_rate
  FROM cpu_intervals GROUP BY series_id
  ```

  Here `previous_value`/`previous_start` come from the finite, deduplicated per-series lag stage; `rate_share=increase*overlap_s/elapsed_s` and `covered_s=overlap_s` for normal intervals. Delta elapsed starts at valid `start_time`, otherwise the previous point; cumulative first points are baselines. For a >10-minute gap attributed in-window, `rate_share=increase`, `covered_s=elapsed_s`. A carried rate supplies its value and weight 1 only if that series lacks real coverage. Test these stages against Phase A `rate()` with CPUs reporting at different cadences, reset and delta fixtures. Keep this as fixed scalar SQL inside the host query, not a new general compiler.

  Memory and filesystem: select the last finite point **per series** strictly before `to`, eligible for numeric display for at most five minutes. Host memory is used bytes / sum of all mutually exclusive state bytes; filesystem utilization is `(used + reserved) / total states` per host/device/mountpoint, then choose the fullest filesystem. Include its mountpoint and used/total bytes. Zero, absent or stale denominators yield NULL and a missing-measurement explanation. Do not sum values from all timestamps or sum filesystem percentages across mounts. Containers use reported `container.cpu.utilization` (convert percent versus ratio from its unit) and `100 * total memory / memory limit`, paired within the same container identity; an absent/zero limit is `memory limit not reported`, not 0%.

  Reuse a single small latest-point stage across the tables. This complete SQL demonstrates the required selected-range inventory and latest-value boundary for filesystem rows; incorporate it into the host query after the shared finite/retry projection:

  ```sql
  WITH inventory AS (
    SELECT DISTINCT namespace, resource['host.name']::VARCHAR AS host
    FROM metrics WHERE $__window(time) /* system namespace */
      AND name = 'system.filesystem.usage'
  ), latest AS (
    SELECT * FROM metrics
    WHERE time >= $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS - INTERVAL '5 minutes'
      AND time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS /* system namespace */
      AND name = 'system.filesystem.usage' AND type IN ('gauge', 'updown') AND isfinite(value)
    QUALIFY row_number() OVER (PARTITION BY series_id ORDER BY time DESC, ingested_unix_nano DESC, value DESC) = 1
  ), mounts AS (
    SELECT namespace, resource['host.name']::VARCHAR AS host,
      attributes['mountpoint']::VARCHAR AS mountpoint,
      attributes['device']::VARCHAR AS device,
      sum(value) FILTER (WHERE attributes['state']::VARCHAR IN ('used', 'reserved')) AS used_bytes,
      sum(value) AS total_bytes
    FROM latest GROUP BY namespace, host, mountpoint, device
  ), fullest AS (
    SELECT *, 100.0 * used_bytes / nullif(total_bytes, 0) AS filesystem_percent
    FROM mounts
    QUALIFY row_number() OVER (PARTITION BY namespace, host
      ORDER BY filesystem_percent DESC NULLS LAST, mountpoint, device) = 1
  )
  SELECT i.namespace, i.host, f.mountpoint, f.filesystem_percent, f.used_bytes, f.total_bytes
  FROM inventory i LEFT JOIN fullest f USING (namespace, host)
  ORDER BY f.filesystem_percent DESC NULLS LAST, i.namespace, i.host
  ```

  Final host columns: namespace, host, status, reporting, cpu_percent, memory_percent, filesystem_percent, mountpoint, used/total bytes and last_seen. Final container columns: namespace, host, container, status, reporting, cpu_percent, memory_percent, total/limit bytes and last_seen. `status` is numeric: current maximum percentage, or 100 for `not reporting`/unknown cadence, with the explicit `reporting` text beside it. Format percentage cells and numeric `status` using existing table formats; apply warning at **80 inclusive**, bad at **90 inclusive**, `better: "lower"`. Order not-reporting rows first, then descending worst available percentage, then stable identity. NULL measurements stay NULL; tests cover 79.99/80/89.99/90/91, a zero value and no denominator. Never use `greatest(...,0)` to imply missing measurements are healthy. Keep each generated SQL statement below the existing 8,000-character limit and execute validation on the complete all-section spec.
- [ ] **What changed: versions and deploy markers.** The versions SQL unions resource observations from `spans` (`start`), `logs` (`time`) and `metrics` (`time`), applying `$__window` and namespace to every branch. Project namespace, service, `resource['service.version']`, `process.runtime.name`, `process.runtime.version`, `telemetry.sdk.language` and `telemetry.sdk.version`. Group by the complete resource-version tuple and return its minimum native event time as `first_seen`, newest changes first. Label this “first seen in the selected range”; do not claim global first-ever history without reading it. Keep resource fields literal, typed and nullable; show two versions of the same service and distinguish namespaces. Version data on logs/metrics alone still makes this section present.

  Enable deploy annotations in the generated spec. Extend existing health annotation-scope projection in `Check`/`runPanel` to include `health` with the same namespace/service rules; Task 3 renders those markers on its trend. `HealthErrorTrend` currently discards the bucket timestamp it already selects: retain it rather than spacing sparse samples evenly across the window. Change that existing method and `RollupReader` signature to return this small internal value:

  ```go
  type HealthTrend struct {
      Times  []int64
      Values []float64
  }
  ```

  Populate `Times` with each scanned `point_time.UnixMilli()` and `Values` with the existing weighted error rate. Keep the existing frame's `error_trend` values and add required `error_trend_times` milliseconds of equal length. Update all consumers/fixtures together; no legacy method or invented-timestamp fallback. `limitBatchFrames` budgets two cells per trend point and slices both arrays identically. Native tests cover sparse/nonaligned buckets, empty scope, weighting, time/value alignment and response truncation. Existing `annotations.Service` remains the source of deploy times, not a second detector or another versions cache. Tests verify the v2 resource row and the corresponding scoped deploy annotation independently.
- [ ] **Run targeted native gates.**

  ```bash
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/panel ./internal/query ./internal/observability ./internal/annotations ./internal/mcp
  rtk just fmt
  ```

## Task 3: Open the read-only system view at `/`

**Files**

- Create: `ui/host/src/dashboards/system-page.tsx`, `ui/host/src/dashboards/system-page.test.tsx`.
- Modify: `ui/host/src/routes/index.tsx`, `ui/host/src/rail.tsx`, `ui/host/src/shell.tsx`, their existing tests, `ui/host/src/router.test.ts`, `ui/host/src/dashboards/api.ts`, `ui/host/src/dashboards/refresh.ts`, `ui/host/src/dashboards/toolbar.tsx`, `ui/host/src/dashboards/toolbar.test.tsx`, `ui/host/src/dashboards/annotation-refresh.test.tsx`, `ui/host/src/dashboards/viz/health.tsx`, `ui/host/src/dashboards/viz/health-pieces.tsx`, `ui/host/src/dashboards/viz/state-timeline.tsx`, `ui/panels/analysis.ts`, `ui/panels/annotations.ts`, `ui/panels/types.ts`, affected timeline/annotation tests and `ui/links.ts`.
- Update existing health-frame fixtures: `ui/host/src/dashboards/rollup-panels.test.tsx`, `panel-rendering.test.tsx`, `headline-panels.test.tsx`, `echarts-brush.test.tsx`, `viz-regressions.test.tsx`, `inspect-views.test.tsx`, `new-viz.test.tsx` in that same directory. Add aligned `error_trend_times`; generated Go-fragment/eval fixtures remain generator-owned.
- Reuse: `PanelGrid`, `PanelCard`, `Toolbar`, `DrillDrawer`, `parseSearch`, `effectiveTime`, existing chart palettes and annotation label/tooltip helpers. Modify `Toolbar` only to allow omitting edit/history controls in this read-only view; saved dashboards retain their current controls.
- Generate: `ui/host/src/routeTree.gen.ts` if its plugin changes it, `internal/ui/dist` and `internal/mcp/apps/panels.html` through `just ui`.

**Interfaces**

New `getSystem(time: DashboardTime, namespace: string, signal?: AbortSignal): Promise<DashboardSpec>`, `SystemPage` and `refreshSystem`. Existing `refreshDashboard` continues to return panel results and at most one annotation response. Add an optional namespace argument for the system caller's annotation request; saved-dashboard callers keep their existing behavior.

- [ ] **Write behavioral UI tests.** Assert `/` renders System regardless of `agent_available`, the rail has one active System entry at `/` on desktop/mobile, and saved dashboards/chat remain reachable. Assert loading/error/retry/empty states and URL-selected namespace/range. For a successful refresh, inspect calls: one GET spec, one POST panel batch containing all its panels, at most one POST annotations, and no dashboard mutations. When initial discovery is all text, the next refresh must still GET a fresh spec and discover newly arrived data. Assert `?edit=1` and edit/history shortcuts cannot enable mutations. New specs and their results appear together; cancellation and failed refresh do not put new headings above old frames.
- [ ] **Use one existing Query observer for the complete refresh.** Do not mount `usePanelResults`' independent timer beside a spec timer. `SystemPage` uses a single TanStack Query `queryFn` which fetches the generated spec then calls the existing `refreshDashboard`. Key it by selected view time and namespace, not by a timestamp embedded in the fetched spec. One refresh publishes `{spec, results, annotations}` atomically. The complete new function belongs beside `refreshDashboard`:

  ```ts
  export async function refreshSystem(time: DashboardTime, namespace: string, width: number, signal?: AbortSignal) {
    const spec = await getSystem(time, namespace, signal);
    const widths = Object.fromEntries(spec.panels.map(panel => [panel.id, Math.max(100, Math.round(width / 100) * 100)]));
    const data = await refreshDashboard({ dashboard: spec, time: spec.time, widths }, signal, namespace);
    return { spec, ...data };
  }
  ```

  `getSystem` uses the existing authorized request helper and `URLSearchParams` for `range` or `from`/`to` and namespace. The GET freezes absolute bounds for that refresh; the batch uses `spec.time` unchanged. Preserve the URL's relative selection for the *next* refresh. Default 30s refresh uses the existing refresh choices, manual refresh and cancellation; tests may select 10s. No new product refresh setting, custom scheduler or per-panel requests. Width changes affect the next request rather than launching a second initial batch. A failed refresh preserves the previous complete view with a visible stale/error indication and existing retry behavior. For an all-text spec, skip panel/annotation requests but keep the discovery refresh active. Use retry behavior that does not create a duplicate successful refresh cycle.
- [ ] **Render with `PanelGrid` and the existing read controls.** Title is `System`; use existing range/refresh controls and namespace selection through the existing variable/input controls, no new page chrome. Namespace is ordinary URL view state and is sent identically to discovery, every panel and annotations. Supply `editing={false}`, `canManage={false}`, `agentAvailable={false}` to the grid, and a fixed local rendering scope (not a persisted dashboard ID). Keep inspect, fullscreen and drill interactions; route health/map selections to traces/logs or existing chat investigation as appropriate without pretending metric SQL has structured drill lineage. Omit save, duplicate/remove/fix/history actions. Keep keyboard focus behavior. No saved spec, owner scope or new dashboard version is fabricated.

  Change the root route from `Navigate` to `SystemPage`, reuse dashboard search validation while dropping edit and comparison state, and wire rail `onSelectSystem`/`systemActive` from `Shell`. Use the existing `var-namespace` search representation and a `VariableBar` text input for namespace; it is page view state, not a generated query variable or product configuration setting. Make Toolbar edit/history/compare callbacks optional and omit those controls here; frozen SQL is never run with a previous-period override. Put System before the existing chat/dashboard lists. Add `docs.system = "https://fanout.run/guides/watch-your-system"` to the central outbound links map when used in UI.
- [ ] **Show check silence as text and bad state using the existing timeline.** In `analysisOption`, support the SQL timeline's optional `state` string without changing the existing numeric threshold coloring or structured frames. Include that label in escaped tooltips and prevent merging touching bad runs when one is `down` and the other is `no recent result`. `StateTimelineViz` exposes the latest item/state labels as accessible text beside its existing legend, so silence is visibly named without opening Data or relying on color. Tests assert down versus silence, endpoint+vantage labels, safe HTML, and preserved normal timeline runs. No new visualization type or reachability table is needed.
- [ ] **Put deploy markers on the health trend.** Include `health` in `refreshDashboard`'s annotation-eligible panels and pass the selected namespace. Extend `HealthTrend`'s existing ECharts option to use a time axis bounded by `Result.from_ms`/`to_ms` and data pairs from `error_trend_times`/`error_trend`. Reuse `withAnnotations` by extending its eligible-viz list for `health`, existing scope matching, mark lines and escaped deploy labels. Reserve annotation space with the existing layout helpers; pass `annotations`, `panel`, `result` and theme from `HealthViz`. A single trend point can still show a deploy marker; do not retain the current `length > 1` guard when annotations exist. Test exact marker time, sparse bucket positions, namespace/service exclusion, hover/accessible version labels, no duplicate requests and unchanged no-annotation health rendering. This is annotation support on the existing chart, not a second health panel.
- [ ] **Run host tests/lint and rebuild embedded assets.**

  ```bash
  rtk proxy sh -c 'cd ui/host && rtk bun run test && rtk bun run lint'
  rtk just ui
  rtk just ui-boundaries-check
  rtk just ui-deadcode
  rtk just fmt
  ```

## Task 4: Prove the six self-observation scenarios through OTLP

**Files**

- Create: `ui/host/e2e/system.spec.ts`, `ui/host/e2e/system-fixture.ts`, `internal/cmd/e2eseed/system.go`, `internal/cmd/e2eseed/system_test.go`.
- Modify only where needed: `internal/cmd/e2eseed/main.go`, `internal/cmd/e2eseed/main_test.go`, `ui/host/e2e/global-setup.ts`, `ui/host/e2e/setup-support.ts`, `ui/host/e2e/harness.test.ts`, `.github/workflows/ci.yml` for the required local Collector binary.
- Reuse: existing disposable server/session setup, protobuf OTLP seeder, private-token cleanup, readiness/diagnostic helpers, Playwright projects and the existing CI e2e job. No parallel test stack or model provider.

**Interfaces**

New fixture payload builder in package main:

```go
func systemPayloads(base time.Time, trouble bool) (map[string][]byte, error)
```

It uses existing OTLP protobufs and `proto.MarshalOptions{Deterministic:true}`. New TypeScript fixture lifecycle reuses the seeder subprocess/send path and has `startSystemFixture`, `trouble` and `close` operations; these are test helpers, not a product API. A preinstalled, exactly pinned Collector Contrib binary is supplied by CI/controller. Record its actual version/checksum and use that same pin for the real run; do not guess an unverified version or silently skip if absent.

- [ ] **Extend small fixture telemetry, not the product.** Keep the existing smoke topology/logs representing the demo's service/dependency context. Add only two hosts, two containers, two check endpoints and v1/v2 resource tuples. Use standard metric kinds/units, CPU states, memory states, filesystem mountpoint/state/device, container resource identity and an outside check reporter host. Add metric export to the existing seeder's signal loop and test its actual decoded OTLP kind, monotonicity, temporality, attributes and resource values. No thousand-host generator, copied private demo files or new metrics benchmark.
- [ ] **Send the fixture through one real local Collector forwarding to the disposable Fanout.** Extend the existing setup's process lifecycle to run a minimal Collector with an OTLP receiver on a loopback ephemeral port and the existing OTLP exporter to Fanout. Do not add hostmetrics/docker_stats live collection to deterministic CI fixtures; their standard payloads are generated through OTLP, and real receivers are checked in Task 6. Reuse temporary credential handling and redacted diagnostics; Collector/process/log/token cleanup runs on failure and termination. Configuration lives in the existing private temporary directory, not the worktree. Never print ingest credentials. If setup needs a CI acquisition step, pin/checksum it using the CI's established dependency-installation pattern; the restricted planning/implementation dispatch never downloads it.
- [ ] **Implement one browser scenario test with baseline then trouble.** Emit baseline points at a five-second fixture collection interval while the page loads and establish at least two distinct points per stream. The small fixture helper continues emitting active entities; its trouble phase changes the six observations below and stops emitting only the selected check/host. Stop/clean it in `finally`. Use the existing refresh control at 10s; the missing entities cross their two-interval threshold within two completed refreshes. Event time controls freshness; engine/UI clocks and panel API responses are not mocked.

  | Scenario | Injected observation | Required visible evidence |
  |---|---|---|
  | Filesystem filling | Used+reserved bytes produce 91% on a named mount | Hosts table shows 91%, bad threshold and that mount, sorted above healthy host |
  | External dependency fails | Error client spans to an external peer, with a matching parent service trace | Existing service map exposes the external dependency/error edge; health shows failing service context |
  | Reachability target down | Positive failed status-class result instead of 2xx/3xx | Endpoint and reporting host named; bad timeline state says `down` |
  | Check stops reporting | No further status attempts for the other endpoint | Same endpoint remains visible and says `no recent result`, bad |
  | Host stops reporting | No further hostmetrics for one baseline host | Host row remains visible and says `not reporting`, bad, ahead of reporting rows |
  | Version changes | New resources with `service.version` v2, runtime and SDK metadata | Versions table contains v2/first_seen; health trend exposes its deploy marker |

  Keep latest results/refresh counts from actual `/api/panels/query` responses, correlated to their request spec/time. Start counting after trouble emission is acknowledged; assert each requirement by the end of the **second completed** refresh, not an unbounded polling loop. Wait for normal flush/rollup readiness as part of those refreshes; do not exclude publication lag from the criterion. Verify both chart rendering and visible semantic text/cells, not only response JSON. Use the existing real ECharts rendering/annotation tooltip approach. Assert no console/CSP failures and no mutation requests. Run this one test in the existing four theme/width projects, not six independent expensive stacks. Native tests cover stopped containers and exact boundaries beyond these six required browser scenarios.
- [ ] **Prove isolation and CI coverage.** Unit-test fixture cleanup and failure diagnostics. Preserve all existing seeder window/performance fixtures and their cardinalities. The normal CI e2e job must select `system.spec.ts`; performance-only filtering must not be the only job containing it. Do not increase CI/global timeouts to hide refresh failures. Collector unavailability, missing scenarios or a missing deploy marker fails setup/test explicitly.
- [ ] **Run seeder checks; controller runs the browser fixture.**

  ```bash
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/cmd/e2eseed
  rtk proxy sh -c 'cd ui/host && rtk bun run test && rtk bun run lint'
  rtk just fmt
  ```

  Use this concrete helper for a manually triggered completed refresh in the scenario test; automatic-refresh counting uses the same successful response boundary. An assertion may inspect the response for corroboration, but the scenario's final assertions still target visible page text/cells and the real charts:

  ```ts
  import { expect, type Page } from "@playwright/test";

  async function completedSystemRefresh(page: Page) {
    const pending = page.waitForResponse(response =>
      new URL(response.url()).pathname === "/api/panels/query" &&
      response.request().method() === "POST" && response.status() === 200);
    await page.getByRole("button", { name: "Refresh now", exact: true }).click();
    const response = await pending;
    const body = await response.json();
    expect(Array.isArray(body.results)).toBe(true);
    expect(body.results.some((result: { status: string }) => result.status === "error")).toBe(false);
    await expect(page.getByRole("button", { name: "Refresh now", exact: true })).toBeEnabled();
    return body.results;
  }
  ```

  Controller, with installed Chrome/Collector and the prescribed Go environment:

  ```bash
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just e2e e2e/system.spec.ts
  ```

## Task 5: Document watching a system and complete existing metric guidance

**Files**

- Create: `site/src/content/docs/guides/watch-your-system.mdx`, `scripts/dashboard-eval/testdata/host-metrics.json`.
- Modify: `site/astro.config.mjs` Guides sidebar, `site/src/content/docs/start/send-telemetry.mdx` with a guide link, `site/src/content/docs/guides/build-dashboards.mdx` with a System link, `docs/testing.md` with the fixture/real-Collector procedure, `scripts/dashboard-eval/README.md` with the public S22 procedure.
- Modify **only the missing example**: `internal/mcp/panel_examples.go`, `internal/mcp/panel_examples_test.go`. Existing Phase A `metricMeasureGuide` already teaches gauge/updown, counters, histograms, stale carry and schema discovery; do not duplicate/rewrite it or add an agent tool/prompt subsystem.
- Generate: affected `site/src/content/docs/reference/*` and `scripts/dashboard-eval/testdata/server.json` only through their existing generators when source contracts/examples actually change.

**Interfaces**

Guide URL `/guides/watch-your-system`. Existing `get_telemetry_schema`, `preview_panels`, dashboard authoring and eval CLI remain the agent interfaces. Public S22 JSON uses the runner's existing `{id,prompt,expect,rationale}` shape; no new CLI mode or tool.

- [ ] **Write the working Collector guide.** Include frontmatter and explain the four questions, empty-section receiver instructions and how to set namespace/resource identity. This Collector Contrib configuration is the local-host/container portion; require the enabled metric names actually consumed by the page:

  ```yaml
  receivers:
    hostmetrics:
      collection_interval: 30s
      scrapers:
        cpu: {}
        memory: {}
        filesystem: {}
    docker_stats:
      endpoint: unix:///var/run/docker.sock
      collection_interval: 30s
      metrics:
        container.cpu.utilization: {enabled: true}
        container.memory.usage.total: {enabled: true}
        container.memory.usage.limit: {enabled: true}
    otlp:
      protocols:
        grpc: {}
        http: {}
  processors:
    resourcedetection:
      detectors: [system]
      override: false
      system:
        hostname_sources: [os]
    resource:
      attributes:
        - key: service.namespace
          value: "${env:SYSTEM_NAMESPACE}"
          action: upsert
    batch: {}
  exporters:
    otlp/fanout:
      endpoint: "https://fanout.example.com:443"
      headers:
        Authorization: "Bearer ${env:INGEST_TOKEN}"
  service:
    pipelines:
      metrics:
        receivers: [hostmetrics, docker_stats, otlp]
        processors: [resourcedetection, resource, batch]
        exporters: [otlp/fanout]
      traces:
        receivers: [otlp]
        processors: [resourcedetection, resource, batch]
        exporters: [otlp/fanout]
      logs:
        receivers: [otlp]
        processors: [resourcedetection, resource, batch]
        exporters: [otlp/fanout]
  ```

  Put the `httpcheck` portion on a **second machine outside the observed stack**, reusing the same exporters and resource processors but a distinct automatically detected `host.name`:

  ```yaml
  receivers:
    httpcheck:
      collection_interval: 30s
      targets:
        - endpoint: "https://app.example.com/healthz"
          method: GET
      metrics:
        httpcheck.status: {enabled: true}
        httpcheck.duration: {enabled: true}
  service:
    pipelines:
      metrics:
        receivers: [httpcheck]
        processors: [resourcedetection, resource, batch]
        exporters: [otlp/fanout]
  ```

  These snippets intentionally use environment substitutions, not real endpoints or secrets. Show the second machine's complete runnable configuration by including the shared processor/exporter blocks in the guide. State Collector Contrib is required, Docker socket/host visibility permissions are platform-dependent, and application services supply `service.version`, runtime and SDK resource attributes through their SDK/OTLP exports. Validate both complete configurations with the actual pinned Collector before calling them working. Correct any actual receiver metric-enablement differences from that validation; do not hide a missing memory-limit stream.
- [ ] **Explain scope and limits plainly.** Two-interval labels use the most recent observed positive gap; at least two observations are needed, and an entity absent throughout the selected range cannot be known to this page. Values may carry for five minutes but a stopped row goes bad earlier. Distinguish `not reporting`, explicit failure, missing memory limits and an empty section. Explain the same-host check warning, what an outside vantage can observe, and: “If the machine running Fanout fails, this page cannot report it. Monitor Fanout from outside that machine.” No alert-rule creation, incidents, notifications, vulnerability scoring or checks run by Fanout are advertised. Versions are first seen within the range; deploy correlation does not prove cause.
- [ ] **Add only the missing host updown/gauge example.** Phase A already has a host network counter example using `rate()` and a histogram using `p95(value)`. Add a memory example using latest values by state, with the existing metric-name rule, to `metricExample`:

  ```json
  {"id":"memory","title":"Host memory by state","viz":"table","query":{"from":"metrics","where":["name = 'system.memory.usage'"],"measures":["last(value) as bytes"],"by":["resource['host.name']","attributes['state']"]},"unit":"bytes"}
  ```

  Execute the example on fixture metrics through the existing native example tests, updating their metric-example panel count from 2 to 3. Preserve schema→preview→save and localized edit guidance. Update SQL timeline capability wording from Task 2 and generated tool/reference output only as necessary. No `get_system` MCP tool is added; S22 uses existing schema and metric panels.
- [ ] **Check in public S22 prompts, not sealed questions.** The complete new file is:

  ```json
  [
    {"id":"host_counter","prompt":"Build a dashboard of host network bytes per second by host and direction from system.network.io.","expect":"dashboard","rationale":"Public S22 counter prompt; use discovered counter kind and rate(), never export-point count rate."},
    {"id":"host_memory","prompt":"Build a dashboard showing current system.memory.usage by host and memory state.","expect":"dashboard","rationale":"Public S22 updown/gauge prompt; use last(value), or a clearly requested average, with a fixed metric name."},
    {"id":"host_histogram","prompt":"Build a dashboard of p95 latency for a reported histogram metric and explain which metric you found.","expect":"dashboard","rationale":"Public S22 histogram prompt; discover a real histogram and use bucket-based p95(value), not avg of exported points."}
  ]
  ```

  Native examples and mocked harness checks run now in implementation; paid public prompts run only in Task 6. The existing variable-length `--set holdout` interface can execute this **public** file with its own frozen SHA256; label its evidence “public S22”, never “sealed holdout”. Inspect saved panel specs and real preview values for correct kinds/measures in addition to the harness's structural scoring. No modifications to sealed files or tuning based on sealed artifacts.
- [ ] **Regenerate and validate docs/examples.**

  ```bash
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/mcp ./internal/agent ./cmd/fanout-docgen
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just docs-generate
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just dashboard-eval-generate
  rtk just script-tests
  rtk just fmt
  ```

  Controller validates the configurations using `rtk otelcol-contrib validate --config` with each private complete config path, and runs the installed site's build. No publication.

## Task 6: Record final-commit verification without adding performance work

**Files**

- Create: `docs/benchmarks/2026-10-agent-dashboards-m5.md`.
- Read/reuse: M5 spec, Phase A correctness/cross-check tests, existing M4 browser collector/eval commands, `ui/host/e2e/{smoke,accessibility,performance,system}.spec.ts`, `.superpowers/replay/m2-browser.mjs`, `scripts/dashboard-eval/README.md` and the guide from Task 5.
- No new benchmark, optimization or test framework. Private browser/eval/Collector artifacts stay in existing controller output locations; product fixes return to the owning task and require affected reruns.

**Interfaces**

Existing `just check`, `just e2e`, browser collector, `TestMetricDemoDataMatchesIndependentWindowSQL`, eval CLI's fixed ten-dashboard/five-edit benchmark, variable-length public-prompt interface, and shared cost ledger. Final results cover **S1–S16, S20 and S22** on one final source commit; there are no S17–S19/S21 requirements in this spec.

- [ ] **Run native and full deterministic gates on the final candidate.** Controller freezes/reviews the commit; the implementing agent performs no git writes. Record commit/build identity, platform, `SELECT version()` result from the existing engine evidence, dependencies, test commands and exit codes. UI, docs and eval goldens must be regenerated before freezing. Run:

  ```bash
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just check
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just e2e
  rtk proxy env GOCACHE="$PWD/.superpowers/go-cache" GOTMPDIR=/private/tmp/claude-501/fanout-m5-gotmp GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/panel -run TestMetricDemoDataMatchesIndependentWindowSQL -count=1
  ```

  Supply the existing `FANOUT_PANEL_BENCH_DATA_DIR` with local format-4 replay data for the cross-check. If missing, its skip is incomplete real-data evidence. Do not use a legacy reader or substitute synthetic CI data as a demo-data cross-check. Run existing demo-volume accessibility/performance tests and browser collector; report their actual targets/results without introducing scale benchmarks or tuning scope. Retain Phase A S14–S16 tests and S1–S13 guards.
- [ ] **Review the existing browser collector and the System page.** Use the established M4 collector invocation with controller-injected browser session/base/output values and both themes; review observed/passed rows, not just process exit 0. Existing dashboard/chat evidence remains required. System evidence comes from Task 4's Playwright test and controller review in both themes/widths: ordered questions, receiver fallbacks, visible silence/resource status, read-only controls, scoped data, one batch/at-most-one annotation request, and deploy markers on health. Record missing or inconclusive observations explicitly.
- [ ] **Run once with a real Collector on a development machine.** Use the validated Task 5 configurations and the pinned Collector version with real local containers, hostmetrics and a second outside check vantage. Observe actual exported metric kinds, units, names and resource keys before accepting calculated percentages. Fill a disposable filesystem/container fixture to >90%, produce an error dependency and failed HTTP target, stop one check exporter and one host exporter, and publish a new service version. Do not fill the development machine's root filesystem or stop Fanout's machine to simulate its self-monitoring limit. Record what each refresh visibly showed, the two-interval transitions and version/deploy evidence. A simulated failure and the actual host/container receiver observations retain their distinct provenance. No deployed-host access, demo reseed or production outage.
- [ ] **Run S1–S5 and public S22 within one retained $5 milestone ledger.** First verify remaining balance and conservative admission reserve; if earlier M5 verification consumed funds, subtract it. Use the existing benchmark command with its ten prompts and five edits, then public S22 through the existing variable-length interface. Example controller commands, using its existing private inputs:

  ```bash
  rtk proxy bun scripts/dashboard-eval/main.ts --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" --model-label "$FANOUT_EVAL_LABEL" --prompts-file "$FANOUT_EVAL_PROMPTS" --edits-file "$FANOUT_EVAL_EDITS" --snapshot-manifest "$FANOUT_EVAL_SNAPSHOT" --cost-ledger "$FANOUT_EVAL_LEDGER" --budget-usd 5 --out "$FANOUT_EVAL_OUT" --set benchmark
  rtk proxy bun scripts/dashboard-eval/main.ts --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" --model-label "$FANOUT_EVAL_LABEL" --prompts-file scripts/dashboard-eval/testdata/host-metrics.json --holdout-sha "$FANOUT_PUBLIC_HOST_PROMPTS_SHA256" --snapshot-manifest "$FANOUT_EVAL_SNAPSHOT" --cost-ledger "$FANOUT_EVAL_LEDGER" --budget-usd 5 --out "$FANOUT_PUBLIC_HOST_EVAL_OUT" --set holdout
  ```

  The second hash is SHA256 of the public prompt file, not a sealed holdout hash. The existing variable-length mode also requires `FANOUT_HOLDOUT_OUT_ROOT`: the controller supplies an external output root, places `FANOUT_PUBLIC_HOST_EVAL_OUT` under it, and keeps the **same ledger outside the worktree** for both invocations. Retain the mode's existing aggregate-output protections; the controller can inspect public prompt saved panels directly through their normal dashboard records to record kind/measure evidence. Same frozen candidate, configured model, real fresh telemetry snapshot and ledger for both invocations. The public host set needs actual host counter/updown-or-gauge data and a real histogram; absent required data makes S22 incomplete. Record the discovered kind, chosen measure, saved panel checks and value cross-check for each prompt. S1–S5 remain separately proven by the full benchmark, including five exact localized edits and median save time; public set success does not replace it. No paid calls before this task, budget reset, automatic retries beyond admission, or claim that a partially completed set passed. Optional judges/sealed aggregate verification run only if authorized and the same remaining budget fits; sealed contents/artifacts are never read by the implementing agent.
- [ ] **Publish evidence and limits, then hand off.** The results document contains one S1–S16/S20/S22 table with literal targets, final candidate identity, measured values, commands/artifact references and PASS/FAIL/PENDING/BLOCKED. Include six S20 scenario rows with observed refresh counts, real-Collector version/config validation and observations, public S22 kind/measure rows, total shared model spend, optional skips and remaining limitations. Existing results from earlier milestones/Phase A are references, not final-commit measurements. A missing Collector run, skipped real-data cross-check, unrun paid criterion or missing marker prevents claiming full M5 completion. End with the narrow next correction for each failure; do not initiate release/deploy work.

## Self-review coverage

- [x] **Binding scope and output:** this plan is the sole planning artifact; six tasks; no implementation, questions, git writes, network/browser use or CodeGraph initialization during drafting.
- [x] **Architecture reuse:** one server generation method and one GET read view; existing spec/executor, one batch, renderer, SQL panels, health/map, annotations and harness; no new panel type/cache/framework/configuration knob.
- [x] **Section order and absence:** reachable → failing or slow → running out → what changed; each absent section has one receiver/setup text panel; partial infrastructure and operational errors are distinguished.
- [x] **Selected scope and freshness:** fresh discovery avoids Schema's one-minute cache and sampled-absence trap; absolute/relative bounds and namespace agree across spec, panels and annotations; signed exclusive event-time bounds are preserved.
- [x] **Reachability:** endpoint/method/vantage identity, latency kind/unit, explicit down and two-interval no-recent-result bad states, unknown cadence, local-only warning and outside-vantage limit are covered.
- [x] **Resource correctness:** CPU counter increases/covered rates, per-series latest memory/filesystem, mount selection, container CPU/memory limit ratio, 80/90 thresholds, worst-first order, NULL denominators and namespace/identity isolation are covered.
- [x] **Stopped resources:** host and container inventory remains after two-interval bad status and five-minute measurement expiry; boundary, retry, cadence-change and resume tests are specified.
- [x] **Changes:** resource versions across all three signals, runtime/SDK fields, first-seen scope and scoped deploy markers on the health trend are required and tested.
- [x] **UI/read-only behavior:** `/`, rail, agent-independent access, existing controls/inspection/drill, disabled mutations, coherent refreshes, cancellation/stale errors, accessibility text and both themes/widths are covered.
- [x] **Native SQL and security:** actual engine execution, format-4 Parquet lookback, all-active arbitrary-SQL snapshot, guarded SELECTs, no privileged IDs/bypasses, authorization/error envelopes and generated docs are covered.
- [x] **S20/self-observation:** all six scenarios go through a real forwarding Collector and OTLP; visible evidence within two completed refreshes; existing CI harness and one real host/container Collector run are required.
- [x] **Guide and S22:** runnable host/container/outside-httpcheck configs, identity/limits/self-monitoring caveat; only missing memory example added; public kind-correct eval prompts use existing tools and leave sealed inputs untouched.
- [x] **Phase C and budget:** final-commit S1–S16/S20/S22, existing browser/smoke/a11y/demo-performance checks, demo metric cross-check, results document and one shared ≤$5 ledger; skipped evidence stays incomplete and performance expansion stays deferred.

These checks describe plan coverage, not implementation or verification PASS evidence.
