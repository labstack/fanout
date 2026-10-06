# Agent dashboards — milestone 2 (Analysis) implementation plan

> **Revision 1 — 2026-10-05:** Applied every binding ruling in `.superpowers/sdd/2026-10-05-agent-dashboards-m2/plan-fix-1.md`. Split chart/row and server/browser/format work into fifteen sequential tasks, and moved eval baseline capture before prompt changes. Added independent scatter units, temporality-aware histogram increases, the historical overview-widget port and host-local trace copies. Fixed pinned-engine timestamp projections, router drill round trips, captured windows, brush activation, rollup drain/compaction, anomaly coalescing, annotation memoization/scope, deploy-All fallback, health shapes and table trends/status. Replaced vacuous tests and supplied missing integration/regression coverage. Removed the extra 1Password global constraint, deferred S6 to M4, added M2 S13 comparisons, and required formatting, docs generation and final `just check`.
> **Revision 2 — 2026-10-05:** Applied all ten binding edits in `.superpowers/sdd/2026-10-05-agent-dashboards-m2/plan-fix-2.md`: corrected rollup arithmetic, moved early-used TypeScript mirrors into Task 7, fixed test selection and quoting, isolated cursor counts, added annotation-range coverage, balanced fences, limited full tool guidance to create/preview, used the controller's secret store, and ordered Task 15 build/commit/check/follow-up. Verified task-level TypeScript ordering and balanced code fences.

Revision verification: throwaway probes on the pinned `v2.0.0-alpha43763` engine executed the timestamp casts, cumulative/delta histogram and heatmap SQL, annotation table/upsert/coalescing/retention reads, resource-version derivation, service-key union, real overview/topology service predicates, bounded nullable trace candidates, redacted log/pattern reads, per-bucket share and table-trend tuple predicates. The probes were deleted; implementation tests below remain execution work.


> **For agentic workers:** Execute the fifteen tasks in order, with one reviewer gate per task. Steps use checkbox (`- [ ]`) syntax. Commit commands are controller instructions.

**Goal:** Complete the fifteen panel types and the analysis interactions: exemplars and trace drill-down, linked time charts, brush zoom, panel time, deploys and anomalies, deploy-split bars, sparklines and formatted tables.

**Architecture:** Extend the merged `internal/panel` checker, compiler and bounded executor. DuckDB owns incremental annotation history; `internal/annotations` exposes it without scanning telemetry. The detector persists its findings. Topology and health adapt `observability.Service` results to frames. HTTP remains a thin authenticated telemetry adapter; saved dashboards remain owner-scoped SQLite records. Pure `ui/panels` compilers drive canvas charts and table models. The dashboard shell owns refresh, annotations and URL interactions; the existing trace components are copied and adapted into the host workspace, following M1’s echart precedent; consolidation belongs to M3.

**Tech Stack:** Go 1.26, Echo v5, pinned DuckDB 2 through `scripts/with-duckdb.sh`, SQLite (modernc) + Goose + sqlc, MCP Go SDK, React 19, Mantine 9, TanStack Router/Query, ECharts 6.1 canvas, `react-grid-layout` 2.2.4, `@tanstack/react-table` 9.2.6, Vitest 5.

**Spec:** `docs/superpowers/specs/2026-10-04-agent-dashboards-design.md`; binding task outline: `.superpowers/m2-outline.md`. Grounded against branch `feat/agent-dashboards-m2`, HEAD `2c208c85`, with milestone 1 merged.

## Global Constraints

- Work only in `/Users/v/Projects/labstack/fanout-dashboards`, branch `feat/agent-dashboards-m2`. Never push, merge or deploy.
- **the controller commits; Codex leaves changes uncommitted**. Every task retains its commit step, executed by the controller after its review gate.
- Codex execution is file edits only, no git writes and no network. Controller-owned test/build/replay/eval/browser commands below are explicit handoffs. Before any Go inspection or gate, the controller sets `GOCACHE` and `GOTMPDIR` under `.superpowers/`, `GOFLAGS=-mod=readonly`, and `GOPROXY=off`. Use `rtk` for every shell command.
- Run Go tests through `just test` with real package paths. A `-run` expression containing `|` is single-quoted around the double-quoted expression, for example `-run '"TestM2A|TestM2B"'`, because Just's recipe evaluates it again.
- Browser gates are `cd ui/host && bun run test` and `bun run lint`, followed by `just ui`. Browser commits include `internal/ui/dist`; shared trace changes also include `internal/mcp/apps` because `just ui` builds both workspaces.
- Every Go task runs `rtk just fmt` before its controller commit. Every task adding or changing an HTTP route, MCP tool contract or setting runs `rtk just docs-generate` and `rtk just docs-generate-check`, and includes the resulting reference docs in its controller commit.
- No control-schema change is needed. Annotation tables are DuckDB analytical tables, never SQLite migrations. Keep both published migration checksums unchanged.
- Preserve the milestone 1 fixes: batch deadlines return finished panels; `better` is inferred at run time; `add_panel` places only the new panel; compaction is bounded; error text never shows paths.
- Shared `ui/*.ts` and `ui/panels/*.ts` import siblings only, with no package imports. React components belong to the workspaces. Every visualization retains PanelCard loading/empty/error/stale/truncated states, Inspect and an aria summary.

The following applicable constraints are copied verbatim from **AGENTS.md**:

## Control database

- Keep SQLite with `modernc.org/sqlite` through `database/sql`, sqlc-generated
  query bindings, and Goose SQL migrations. DuckDB owns the telemetry query
  schema separately. Do not introduce pgx or PostgreSQL for control state.
- `internal/db/migrations` is the control schema source for both Goose and
  sqlc. Do not add a separate schema copy or another migration framework.
- Use timestamped Goose SQL migrations with `-- +goose Up`. Published
  migrations are immutable; add a new migration for subsequent changes and
  extend the checksum ledger in `internal/db/migrations_test.go`. Never replace
  a published checksum.
- Run `just db-gen` after schema or query changes and verify the storage tests.
- No Atlas conversion, legacy migration tracker, or schema fallback. A database
  with application tables must have a positive applied Goose version before
  initialization. An empty or zero-only version table is not sufficient.

## Telemetry engine and format

- Use the pinned DuckDB 2 engine in `internal/duckdb`, built with
  `scripts/with-duckdb.sh`. A Go driver version does not identify its bundled
  native engine; verify `SELECT version()` when changing the pin.
- Build and test on native Linux or macOS, AMD64 or ARM64. The wrapper verifies
  each platform archive's checksum and uses its matching headers and statically
  linked core, JSON, Parquet, and time-zone extensions. Do not add dynamic
  extension downloads or a second engine path.
- Store OTLP attributes and resource attributes as typed maps in canonical rows
  and shredded Parquet VARIANT columns. Preserve integers, booleans, bytes,
  nested values, and nanosecond UTC timestamps. Convert nested SQL results to
  JSON only at the client boundary; attribute keys containing dots are literal.
- Dashboard file snapshots use each signal's actual event-time footer bounds,
  including signed nanoseconds. Unknown statistics include the file. Never
  substitute ingestion time or prune public arbitrary SQL from a dashboard scope.
- Endpoint histograms, log counts, and notable-trace candidates acknowledge
  complete immutable batch IDs transactionally. Query cache markers and rows in
  one read transaction under the pinned file snapshot; uncached active files
  remain immediately visible. Compaction and retention must invalidate retired
  contributions without double counting or losing late publications.
- Version disposable read-cache schema and semantics together; mismatches rebuild
  all private read tables. Cache minute aggregates, per-batch trace bounds,
  and one incremental candidate per trace, never individual event copies or
  four globally rewritten scope indexes. Mixed trace scopes use batch parts. Exact
  clipped minutes read footer-pruned Parquet, and compaction derives complete
  output contributions from cached inputs. Body search matches redacted text.
  Analytical service/edge watermark lag is separate. Completed-batch writes
  have their own gate and write-pool slot; analytical writes use disjoint tables.
  Maintenance must hold both gates for checkpointing.
- Batch format 3, including its physical schema, is the only accepted telemetry
  format. Schema changes require a format version change. Disable schema unioning
  and Hive partition inference; use native Parquet binding so VARIANT extracts
  can reach the scan. Pre-project hot messaging fields into scalar read-view
  columns; this preview does not push general VARIANT extracts across views.
  Reject unsupported metadata before cleanup or schema rewriting, preserve those files, and add
  no legacy reader or format fallback.
- Keep native `TIMESTAMPTZ_NS` columns in time predicates. Bind Go window
  parameters as `?::TIMESTAMP_NS::TIMESTAMPTZ_NS` to preserve nanoseconds;
  cast returned timestamps and datetime-function arguments only. Every engine
  connection uses UTC. Do not change Parquet UTC-instant semantics for speed.
- Offline verification reuses one bounded, non-spilling native engine to decode
  all Parquet columns and checks physical schemas, complete span sort tuples,
  and exact index ranges. Operational errors and unsupported formats never
  authorize quarantine. Discarded hashes force decoding; they are not checksums.
- Keep DuckDB 2's memory-governed asynchronous I/O defaults unless measurements
  justify tuning them. Do not force an unbounded read-ahead depth.
- Arbitrary SQL is a single read-only SELECT over approved telemetry relations.
  Parse its AST and describe/project results on the same connection and pinned
  Parquet snapshot. Keep engine file access and configuration locked down.
- Rooted service traversal uses keyed recursion over the complete scoped edge
  rollup, keeps namespaces separate, and enforces hop, accumulated-node, and
  execution-time limits. Report truncation explicitly.


## API and MCP naming

Use the same naming conventions as Monk, Goal, Cipher, and ResponseKeeper
where the operations have the same meaning. Product-specific resources and
protocol-defined contracts keep their own semantics.

- Application HTTP routes use `/api`, lowercase words, meaningful slash-separated
  domains/resources/actions, and plural collections. Do not use hyphens in
  product-defined static path segments. Opaque resource IDs may contain hyphens.
  Singular paths are appropriate for genuine
  singleton resources or read views. Protocol-defined paths keep their required
  spelling, including `/.well-known/oauth-authorization-server`.
- GET reads, POST creates or invokes an action, PUT replaces the writable
  representation, PATCH partially modifies it, and DELETE removes it.
  Choose the method from the handler's behavior, not its current name.
- Custom actions use POST and a slash-separated action, for example
  `/api/users/{id}/access/revoke` or `/api/settings/ingest/token/rotate`.
  Each segment should have a domain, resource, or action meaning; do not
  mechanically split compound words. Colon actions are conventions in other
  API ecosystems.
- Equivalent authentication flows use `/api/auth/me`, `/api/auth/code/send`,
  `/api/auth/code/verify`, and `POST /api/auth/logout`. Flow selection, passkeys,
  account linking, and sign-in link verification are distinct operations.
- Operational routes use `/healthz` for liveness, `/readyz` for readiness,
  and `/metrics` for Prometheus exposition. Preserve authorization requirements.
- MCP tool names use verb-first snake_case and describe their behavior:
  `get`, `list`, `search`, `inspect`, `create`, `replace`, and domain actions.
  Full replacement uses `replace`, not `update`. Do not add product prefixes
  solely to avoid collisions in clients that aggregate servers.
- New product-owned JSON fields and query parameters use snake_case. Preserve
  protocol-defined names, including MCP, OAuth, OTLP, and AG-UI fields. Existing
  client-contract exceptions require a deliberate contract change.
- Product CalVer, client-contract versions, and protocol versions are separate.
  Do not put the product release or maturity suffix in API paths or tool names.
- Breaking naming changes update registrations, authorization classification,
  clients, tests, metrics configuration, and generated documentation together.
  Remove superseded routes and handlers; add no aliases or fallback behavior.

HTTP method semantics follow [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html)
and [RFC 5789](https://www.rfc-editor.org/rfc/rfc5789.html). The path and casing
rules above are project conventions, not a universal yearly API standard.
MCP names follow the published
[2026-07-28 tool-name guidance](https://modelcontextprotocol.io/specification/2026-07-28/server/tools#tool-names).

The following query, annotation and rendering constraints are copied verbatim from the **spec**:

## Execution

1. Validate the spec, resolve variables in dependency order, and choose each
   panel's window and bucket from the dashboard time, the panel override and
   the client's panel width.
2. Compile structured queries to SQL. Check SQL panels with the guard.
3. Bind the Parquet snapshot for the window through `bindSnapshot`, so file
   pruning applies, then run with named parameters on one connection under the
   read gate. Each panel has a 10-second timeout. Tables, bars and SQL panels
   cap at 1000 rows; time series cap at 2000 points per series, and a series
   beyond `top` folds into Other. One request holds at most 200,000 cells;
   past that, time series drop their oldest buckets and say so. When the
   whole request runs out of time, finished panels still return and the rest
   report that they did not run.
4. With comparison on, run the same query shifted by the range and return it
   alongside.
5. Return a frame per panel: columns with `name`, `type`, `role` (`time`,
   `dimension`, `measure`) and `unit`, values in columnar arrays, time as
   integer Unix milliseconds, plus `status` (`ok`, `empty`, `error`), timing,
   and the SQL that ran.
6. When a panel is empty, the executor reruns its count with each filter
   removed in turn, bounded to eight reruns, and reports the filter that empties
   it ("no spans match `http_route = '/cart'` for service frontend; seen routes
   include /api/cart"). This is what lets the agent fix or drop a panel before
   saving (#232 item 9).

A dashboard refresh sends one `POST /api/panels/query` with every visible
panel. Panels scrolled out of view, and all panels in a background tab, do not
refresh (#232 item 30).


## Annotations

- **Deploys.** The rollup cycle maintains `version_rollup(namespace, service,
  service_version, first_seen, last_seen)` from new batches, in its own
  analytical table as AGENTS.md requires. A deploy is the `first_seen` of a
  version other than the service's first observed version.
- **Anomalies.** The detector's findings become time ranges with service,
  title and severity. If the current snapshot does not retain history, the
  detector writes each finding to a bounded `anomaly_log` table.

Both render as a dashed vertical marker or a shaded band, with a tooltip, on
every time panel whose filters include the affected service, and on all time
panels when no service filter applies.


- **Rendering.** ECharts with the canvas renderer for dashboard panels, LTTB
  sampling as a guard behind server bucketing, and the chart theme read from
  `ui/tokens.ts`. Tables use `@tanstack/react-table` v9 drawn with Mantine.
- **Accessibility.** Every chart has the Inspect data table, an `aria-label`
  summary, keyboard-reachable menus, and status conveyed by Fanout's health
  shapes as well as color. Categorical colors use the first four series slots,
  which pass the CVD validator; panels with more series rely on the legend and
  direct labels.

## Recorded design choices

1. Use **POST `/api/annotations`**, as the binding outline requires, in place of the spec API table's GET spelling. The body has `from`, `to`, `services` and optional `namespace`; no GET alias and no annotation MCP tool (none is requested by the spec).
2. Version history uses an independent immutable-batch watermark plus `rollup_state.version_rollup_v1` for progress. Process at most 64 batches/64,000 source rows per transaction, admitting one indivisible oversized batch. Drain passes under a two-second phase budget in the read-cache worker and rollup cycle, yielding the analytical gate between passes. Transfer processed markers to compaction outputs only when every input is marked, and clear the backlog-limited flag when drained; disclose history caps separately. Min/max upserts are idempotent across compaction and late publication. Retain at most 100,000 version records and 30 days of inactive history; preserve the initial version for each retained service. Report caps explicitly.
3. Detector findings have the detector's existing default namespace scope; an empty scope is a global finding and is returned only for an unscoped annotation request. Coalesce overlapping lookbacks into one open row per (namespace, service, kind), extending its end_time; retain separate episodes after a gap, 30 days and 10,000 rows; `abs(z_score) >= 4` is bad, otherwise warn. The shaded range is the detection lookback ending at detection time.
4. Scatter uses one point per item (`query.by[0]`), exactly two aggregate measures, and optional `by[1]` colour. Each axis has its own `x_scale`/`y_scale`; each measure retains its own unit. `x_unit` overrides the x axis and `unit` overrides the y axis; both must be known units. Scatter and tables are exempt from the single-axis unit-family rule. State timeline requires one item dimension, one measure and thresholds.
5. Logs and traces use fixed server projections and no aggregate measures; patterns require `count()`, group by `body_template`, and return a JSON-encoded dense trend with at most 240 points. Structured row panels reject SQL, arbitrary projections and invalid sort values.
6. Topology/health accept only guarded equality filters on namespace/service; reject other expressions instead of dropping them. They call the existing observability services. No duplicate rollup SQL.
7. Drill URL payload is bounded JSON in `drill=` containing panel, kind, absolute range, dimension values and optional trace ID. SQL panels cannot promise filter lineage and reject drill/exemplar requests. Logs drill reuses the checked log panel with the clipped range and dimensions; trace selection calls the existing observability trace endpoint and renders its embedded correlated logs.
8. Deploy split uses the latest matching deploy **inside the panel's effective window**, one split instant for the whole bar. Without one, return a normal unsplit frame and a diagnosis. Before/since rates use each subwindow's duration; labels and comparison values remain exact. Annotation data is fetched once with the union of visible panel windows and shared with rendering.

## Review Focus

1. Incremental annotations never scan all batches: Task 1 `TestM2VersionIncrementalLateBatch`, `TestM2VersionPassAndHistoryCaps`, `TestM2AnnotationHistoryBounds` and `TestM2AnnotationSourcesNoRawScan` execute against the native engine and bound each pass/history table.
2. Exemplars/drill cannot bypass filters or owners: Task 2 `TestM2ExemplarsCheckedScope`, `TestM2ExemplarsRejectForgedDimensions`, `TestM2ExemplarRoutePolicy`; Task 10 `drill.test.tsx` covers URL reproduction, cancellation and trace logs. Inline reads use ReadTelemetry, never saved-dashboard lookup.
3. 24-hour heatmap/timeline frames stay within the cell budget: Task 3 `TestM2DistributionBudget` and Task 4 `TestM2TimelineBudget` execute real fixtures, assert truncation and the 200,000-cell batch budget.
4. Crosshair/brush do not trigger per-pixel URL writes or recreate charts: Task 9 `interaction.test.tsx` and the extended `echart-canvas.test.tsx` exercise group cleanup, stable options and one brush-end history push; Task 12 `annotation-refresh.test.tsx` and `use-panel-results.test.tsx` pins S8.
5. All nine new types show empty/error/loading/partial states: Tasks 7–8 `new-viz.test.tsx` iterate the nine types through PanelCard; Task 15 repeats them in both themes on replayed data. Registry test pins exactly fifteen types (S9).

## Execution setup (controller)

```bash
rtk proxy mkdir -p .superpowers/gocache .superpowers/gotmp
rtk proxy env GOCACHE="$PWD/.superpowers/gocache" GOTMPDIR="$PWD/.superpowers/gotmp" GOFLAGS=-mod=readonly GOPROXY=off just test ./internal/panel/...
```

For later Go commands, export those four values in the controller's shell first. Do not run a dependency install or regenerate module files to work around an offline cache failure. Record the exact blocked gate if a dependency is absent.

---

### Task 1: Annotation sources and the classified read route

**Files:**
- Create: `internal/annotations/service.go`, `internal/query/annotations.go`, `internal/query/annotations_test.go`, `internal/api/annotations.go`, `internal/api/annotations_test.go`
- Modify: `internal/query/views.go`, `internal/query/duck.go`, `internal/query/batch_cache.go`, `internal/query/writegate/write_gate.go`, `internal/intelligence/detector.go`, `internal/api/auth_middleware.go`, `cmd/fanout/main.go`

**Interfaces:**
- Consumes existing: `query.NewDuck(ctx context.Context, cfg config.Config, repository *telemetrystore.Repository) (*query.Duck, error)`; `(*Duck).writer() *sql.DB`; `(*Duck).lockRollupParquetRead(ctx context.Context) error`; `(*Duck).physicalSource(signal string, batches []telemetry.BatchMetadata) string`; `cleanSource(signal, physical string) string`; `storeRollupWatermark(ctx context.Context, tx *sql.Tx, key string, watermark int64) error`; `(*Duck).rollupOnce(ctx context.Context) (int, error)`; `(*Detector).runCheck(ctx context.Context)`; `(*Detector).GenerateSnapshot(ctx context.Context) IntelligenceSnapshot`; `(*Duck).DefaultNamespace() string`.
- Produces: `annotations.Request`, `Deploy`, `Anomaly`, `Response`; `annotations.New(db queryrows.Queryer) *annotations.Service`; `(*Service).Read(ctx context.Context, req Request) (Response, error)`; `(*Duck).RefreshVersionRollup(ctx context.Context) (int64, error)`; `(*Duck).RecordAnomalies(ctx context.Context, findings []annotations.Anomaly, now time.Time) error`; `api.RegisterAnnotationRoutes(e *echo.Echo, service AnnotationReader)`.
- New DuckDB tables: `version_rollup(namespace, service, service_version, first_seen, last_seen)`; `version_rollup_batches(batch_id, max_ingested)`; `anomaly_log(namespace, service, kind, start_time, end_time, title, severity)`; watermark keys `version_rollup_v1`, `version_rollup_v1_limited`, `version_rollup_v1_history_limited`. No Go/TypeScript control-storage interface changes.
- HTTP: POST `/api/annotations`, ReadTelemetry, body `annotations.Request`, response `annotations.Response`. No owner ID or MCP tool.

- [ ] **Step 1: Write the failing engine and route tests**

Create `internal/query/annotations_test.go`:

```go
package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func versionEngine(t *testing.T) (*Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RetentionDays: 30}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo
}

func TestM2VersionIncrementalLateBatch(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	add := func(id, version string, offset time.Duration) {
		t.Helper()
		n := at.Add(offset).UnixNano()
		err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{{
			Namespace: "shop", ServiceName: "checkout", ServiceVersion: version,
			TraceID: id, SpanID: id, Name: "checkout", Kind: "SPAN_KIND_SERVER",
			StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1,
			IngestedAt: at.UnixNano(),
		}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("v-first", "v1", 0)
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	add("v-late", "v2", 10*time.Minute) // Same ingestion tip; published later.
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 0 {
		t.Fatalf("noop: %d %v", n, err)
	}
	got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: at.Add(-time.Minute), To: at.Add(time.Hour), Namespace: "shop", Services: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deploys) != 1 || got.Deploys[0].Version != "v2" || !got.Deploys[0].At.Equal(at.Add(10*time.Minute)) {
		t.Fatalf("deploys: %+v", got)
	}
	var watermark int64
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1'`).Scan(&watermark); err != nil || watermark != at.UnixNano() {
		t.Fatalf("watermark: %d %v", watermark, err)
	}
}

func TestM2AnnotationHistoryBounds(t *testing.T) {
	d, _ := versionEngine(t)
	now := time.Now().UTC()
	findings := make([]annotations.Anomaly, 10020)
	for i := range findings {
		findings[i] = annotations.Anomaly{Namespace: "shop", Service: fmt.Sprint("service-", i), Kind: "latency", From: now.Add(-time.Duration(i+1) * time.Minute), To: now.Add(-time.Duration(i) * time.Minute), Title: "Slow", Severity: "warn"}
	}
	if err := d.RecordAnomalies(t.Context(), findings, now); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordAnomalies(t.Context(), findings[:1], now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log`).Scan(&n); err != nil || n != 10000 {
		t.Fatalf("cap: %d %v", n, err)
	}
	if err := d.RecordAnomalies(t.Context(), nil, now.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("age: %d %v", n, err)
	}
}

func TestM2AnnotationSourcesNoRawScan(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i, version := range []string{"v1", "v2"} {
		n := at.Add(time.Duration(i) * time.Minute).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("scan-", i), Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", ServiceVersion: version, TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n}}, Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", Body: "real log", EventUnixNanos: n, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	counter := &annotationCountingEngine{Duck: d}
	got, err := annotations.New(counter).Read(t.Context(), annotations.Request{From: at, To: at.Add(time.Hour), Namespace: "shop"})
	if err != nil || len(got.Deploys) != 1 || counter.calls != 3 || counter.raw != 0 {
		t.Fatalf("cache reads: %+v calls=%d raw=%d err=%v", got, counter.calls, counter.raw, err)
	}
}

type annotationCountingEngine struct {
	*Duck
	calls, raw int
}

func (e *annotationCountingEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	e.calls++
	for _, source := range []string{"FROM spans", "FROM logs", "FROM metrics", "read_parquet"} {
		if strings.Contains(text, source) {
			e.raw++
			return nil, fmt.Errorf("raw telemetry read: %s", source)
		}
	}
	return e.Duck.QueryContext(ctx, text, args...)
}

func TestM2VersionPassAndHistoryCaps(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("version-%03d", i)
		n := at.Add(time.Duration(i) * time.Millisecond).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", ServiceVersion: "v1", TraceID: id, SpanID: id, Name: "root", StartUnixNanos: n, EndUnixNanos: n + 1000, DurationMS: .001, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 64 {
		t.Fatalf("first pass %d %v", n, err)
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 1 {
		t.Fatalf("remaining pass %d %v", n, err)
	}
	_, err := d.DB.Exec(`DELETE FROM version_rollup`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.DB.Exec(`INSERT INTO version_rollup SELECT 'shop','checkout','v-'||i,(?::TIMESTAMP_NS + i*INTERVAL '1 millisecond')::TIMESTAMPTZ_NS,(?::TIMESTAMP_NS + i*INTERVAL '1 millisecond')::TIMESTAMPTZ_NS FROM generate_series(1,100005) t(i)`, at, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count, initial int
	if err := d.DB.QueryRow(`SELECT count(*),count(*) FILTER(WHERE service_version='v-1') FROM version_rollup`).Scan(&count, &initial); err != nil || count != 100000 || initial != 1 {
		t.Fatalf("bounded history: %d initial %d %v", count, initial, err)
	}
	got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: at, To: at.Add(time.Hour), Namespace: "shop"})
	if err != nil || !got.Truncated || len(got.Deploys) != 1000 {
		t.Fatalf("disclosed cap: %+v %v", got, err)
	}
}
func TestM2VersionDrainAndLimitedReset(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i := range 70 {
		n := at.UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprintf("drain-%03d", i), Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", Resource: map[string]any{"service.version": "v2"}, EventUnixNanos: n, IngestedAt: n}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var limited int
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 1 {
		t.Fatalf("backlog flag=%d %v", limited, err)
	}
	if n, err := d.DrainVersionRollup(t.Context()); err != nil || n != 6 {
		t.Fatalf("drain=%d %v", n, err)
	}
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 0 {
		t.Fatalf("stuck backlog=%d %v", limited, err)
	}
	var marked int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&marked); err != nil || marked != 70 {
		t.Fatalf("markers=%d %v", marked, err)
	}
}
func TestM2VersionResourceLogsAndMetrics(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	n := at.UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "resources", Logs: []telemetry.Log{{Namespace: "shop", ServiceName: "logsvc", Resource: map[string]any{"service.version": "log-v"}, EventUnixNanos: n, IngestedAt: n}}, Metrics: []telemetry.Metric{{Namespace: "shop", ServiceName: "metricsvc", Name: "n", Type: "gauge", Resource: map[string]any{"service.version": "metric-v"}, EventUnixNanos: n, IngestedAt: n}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := d.DB.Query(`SELECT service,service_version,first_seen::TIMESTAMP_NS FROM version_rollup ORDER BY service`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, want := range [][2]string{{"logsvc", "log-v"}, {"metricsvc", "metric-v"}} {
		var service, version string
		var first time.Time
		if !rows.Next() {
			t.Fatal("missing resource row")
		}
		if err := rows.Scan(&service, &version, &first); err != nil || service != want[0] || version != want[1] || !first.Equal(at) {
			t.Fatalf("%s %s %v %v", service, version, first, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("extra rows or error: %v", rows.Err())
	}
}
func TestM2VersionMarkersCompactionRetention(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Truncate(time.Hour)
	for i := range 8 {
		n := at.Add(time.Duration(i) * time.Second).UnixNano()
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: fmt.Sprint("part-", i), Spans: []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", ServiceVersion: "v1", TraceID: "trace", SpanID: fmt.Sprint(i), StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: at.UnixNano()}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.DrainVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
		t.Fatalf("compact=%d %v", n, err)
	}
	var n int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 9 {
		t.Fatalf("output marker not transferred=%d %v", n, err)
	}
	if count, err := d.RefreshVersionRollup(t.Context()); err != nil || count != 0 {
		t.Fatalf("rescanned processed output=%d %v", count, err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retired inputs=%d %v", n, err)
	}
	if count, err := repo.PruneParquet(t.Context(), d, at.Add(time.Hour).UnixNano(), 8); err != nil || count != 1 {
		t.Fatalf("prune=%d %v", count, err)
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("retention markers=%d %v", n, err)
	}
}
func TestM2AnomalyCoalescesOpenEpisode(t *testing.T) {
	d, _ := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	a := annotations.Anomaly{Namespace: "shop", Service: "checkout", Kind: "latency", From: at, To: at.Add(5 * time.Minute), Title: "Slow", Severity: "warn"}
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{a}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a.From = at.Add(time.Minute)
	a.To = at.Add(6 * time.Minute)
	a.Severity = "bad"
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{a}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	var first, last time.Time
	var severity string
	if err := d.DB.QueryRow(`SELECT count(*),min(start_time)::TIMESTAMP_NS,max(end_time)::TIMESTAMP_NS,max(severity) FROM anomaly_log`).Scan(&count, &first, &last, &severity); err != nil || count != 1 || !first.Equal(at) || !last.Equal(a.To) || severity != "bad" {
		t.Fatalf("episode=%d %v %v %s %v", count, first, last, severity, err)
	}
	a.From = at.Add(20 * time.Minute)
	a.To = at.Add(25 * time.Minute)
	if err := d.RecordAnomalies(t.Context(), []annotations.Anomaly{a}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("separate episodes=%d %v", count, err)
	}
}
```

Create `internal/api/annotations_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/annotations"
)

type annotationFake struct{}

func (annotationFake) Read(context.Context, annotations.Request) (annotations.Response, error) {
	return annotations.Response{Deploys: []annotations.Deploy{}, Anomalies: []annotations.Anomaly{}}, nil
}
func TestM2AnnotationRoute(t *testing.T) {
	s := newTestAuthServer(t)
	_, err := s.users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	RegisterAnnotationRoutes(s.e, annotationFake{})
	req := sessionRequest(http.MethodPost, "/api/annotations", strings.NewReader(`{"from":"2026-10-01T12:00:00Z","to":"2026-10-01T13:00:00Z","services":["checkout"]}`), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deploys":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	policy, ok := classifyRoute(http.MethodPost, "/api/annotations")
	if !ok || policy.capability != ReadTelemetry {
		t.Fatalf("policy: %+v %v", policy, ok)
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/annotations"); ok {
		t.Fatal("GET alias allowed")
	}
}

type invalidAnnotationFake struct{}

func (invalidAnnotationFake) Read(context.Context, annotations.Request) (annotations.Response, error) {
	return annotations.Response{}, annotations.ErrRequest
}
func TestM2AnnotationErrRequest400(t *testing.T) {
	s := newTestAuthServer(t)
	_, err := s.users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	RegisterAnnotationRoutes(s.e, invalidAnnotationFake{})
	req := sessionRequest(http.MethodPost, "/api/annotations", strings.NewReader(`{"from":"2026-10-01T13:00:00Z","to":"2026-10-01T12:00:00Z"}`), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), annotations.ErrRequest.Error()) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/query/... ./internal/api/... -run '"TestM2Version|TestM2Annotation|TestM2Anomaly"'
```

Expected: compile failure for missing annotation types and methods.

- [ ] **Step 3: Implement the analytical tables and bounded incremental writer**

Create `internal/query/annotations.go`:

```go
package query

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/telemetry"
)

const createVersionRollupTable = `CREATE TABLE version_rollup (
 namespace VARCHAR, service VARCHAR, service_version VARCHAR,
 first_seen TIMESTAMPTZ_NS, last_seen TIMESTAMPTZ_NS,
 PRIMARY KEY(namespace,service,service_version))`
const createVersionBatchesTable = `CREATE TABLE version_rollup_batches (batch_id VARCHAR PRIMARY KEY,max_ingested BIGINT)`
const createAnomalyLogTable = `CREATE TABLE anomaly_log (
 namespace VARCHAR,service VARCHAR,kind VARCHAR,start_time TIMESTAMPTZ_NS,end_time TIMESTAMPTZ_NS,
 title VARCHAR,severity VARCHAR,PRIMARY KEY(namespace,service,kind,start_time))`

func createAnnotationTables(db *sql.DB) error {
	if err := ensureCacheTable(db, "version_rollup", createVersionRollupTable,
		"namespace", "service", "service_version", "first_seen", "last_seen"); err != nil {
		return err
	}
	if err := ensureCacheTable(db, "version_rollup_batches", createVersionBatchesTable,
		"batch_id", "max_ingested"); err != nil {
		return err
	}
	return ensureCacheTable(db, "anomaly_log", createAnomalyLogTable,
		"namespace", "service", "kind", "start_time", "end_time", "title", "severity")
}

func (d *Duck) RefreshVersionRollup(ctx context.Context) (int64, error) {
	if d.repository == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteRollupVersion)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if err := d.lockRollupParquetRead(ctx); err != nil {
		return 0, err
	}
	defer d.parquetMu.RUnlock()
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT batch_id FROM version_rollup_batches`)
	if err != nil {
		return 0, err
	}
	marked := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		marked[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	batches := d.repository.Parquet.BatchMetadata()
	active := map[string]bool{}
	pending := []telemetry.BatchMetadata{}
	count := 0
	for _, b := range batches {
		active[b.ID] = true
		size := b.Spans + b.Logs + b.Metrics
		if marked[b.ID] || len(pending) >= 64 || (len(pending) > 0 && count+size > 64000) {
			continue
		}
		pending = append(pending, b)
		count += size
	}
	for id := range marked {
		d.cachePublishMu.Lock()
		publishing := d.cachePublishing[id]
		d.cachePublishMu.Unlock()
		if !active[id] && !publishing {
			if _, err := tx.ExecContext(ctx, `DELETE FROM version_rollup_batches WHERE batch_id=?`, id); err != nil {
				return 0, err
			}
		}
	}
	parts := []string{}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		selected := []telemetry.BatchMetadata{}
		for _, b := range pending {
			if _, n := batchTime(b, signal); n > 0 {
				selected = append(selected, b)
			}
		}
		if len(selected) == 0 {
			continue
		}
		version, column := `TRY_CAST(resource['service.version'] AS VARCHAR)`, "time"
		if signal == "spans" {
			version = "service_version"
			column = "start_time"
		}
		parts = append(parts, fmt.Sprintf(`SELECT coalesce(namespace,'') AS namespace,coalesce(service,'') AS service,%s AS service_version,%s AS t FROM (%s)`, version, column, cleanSource(signal, d.physicalSource(signal, selected))))
	}
	if len(parts) > 0 {
		statement := `INSERT INTO version_rollup SELECT namespace,service,service_version,min(t),max(t) FROM (` + strings.Join(parts, " UNION ALL ") + `) WHERE service<>'' AND service_version IS NOT NULL AND service_version<>'' GROUP BY 1,2,3
ON CONFLICT(namespace,service,service_version) DO UPDATE SET first_seen=least(version_rollup.first_seen,excluded.first_seen),last_seen=greatest(version_rollup.last_seen,excluded.last_seen)`
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return 0, err
		}
	}
	for _, b := range pending {
		if _, err := tx.ExecContext(ctx, `INSERT INTO version_rollup_batches VALUES (?,?)`, b.ID, b.MaxIngestedNanos); err != nil {
			return 0, err
		}
	}
	// Whole inactive services retire together, including their initial version.
	if _, err := tx.ExecContext(ctx, `DELETE FROM version_rollup WHERE (namespace,service) IN (SELECT namespace,service FROM version_rollup GROUP BY 1,2 HAVING max(last_seen)<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, time.Now().UTC().Add(-30*24*time.Hour)); err != nil {
		return 0, err
	}
	// Keep initial observations before recent versions; bound pathological cardinality.
	res, err := tx.ExecContext(ctx, `DELETE FROM version_rollup WHERE (namespace,service,service_version) NOT IN (
 SELECT namespace,service,service_version FROM (
 SELECT *,row_number() OVER(PARTITION BY namespace,service ORDER BY first_seen,service_version) AS initial_rank FROM version_rollup)
 ORDER BY (initial_rank=1) DESC,last_seen DESC,namespace,service,service_version LIMIT 100000)`)
	if err != nil {
		return 0, err
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if removed > 0 {
		if err := storeRollupWatermark(ctx, tx, "version_rollup_v1_history_limited", 1); err != nil {
			return 0, err
		}
	}
	remaining := 0
	for _, b := range batches {
		if !marked[b.ID] {
			remaining++
		}
	}
	limited := int64(0)
	if remaining > len(pending) {
		limited = 1
	}
	if err := storeRollupWatermark(ctx, tx, "version_rollup_v1_limited", limited); err != nil {
		return 0, err
	}
	var tip int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(max_ingested),0) FROM version_rollup_batches`).Scan(&tip); err != nil {
		return 0, err
	}
	var previous int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(last_ingested_unix_nano),0) FROM rollup_state WHERE cache_key='version_rollup_v1'`).Scan(&previous); err != nil {
		return 0, err
	}
	if err := storeRollupWatermark(ctx, tx, "version_rollup_v1", max(tip, previous)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(pending)), nil
}

func (d *Duck) RecordAnomalies(ctx context.Context, findings []annotations.Anomaly, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteAnomalyLog)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	findings = append([]annotations.Anomaly(nil), findings...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].To.Before(findings[j].To) })
	if len(findings) > 10000 {
		findings = findings[len(findings)-10000:]
	}
	for _, a := range findings {
		if !a.From.Before(a.To) {
			continue
		}
		// Delete and reinsert the merged episode inside this transaction. The
		// previous end must overlap the new lookback; disconnected episodes survive.
		var first time.Time
		err := tx.QueryRowContext(ctx, `SELECT min(start_time)::TIMESTAMP_NS FROM anomaly_log
WHERE namespace=? AND service=? AND kind=? AND end_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<=?::TIMESTAMP_NS::TIMESTAMPTZ_NS
HAVING count(*)>0`, a.Namespace, a.Service, a.Kind, a.From.UTC(), a.To.UTC()).Scan(&first)
		from := a.From.UTC()
		to := a.To.UTC()
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if first.Before(from) {
				from = first.UTC()
			}
			var last time.Time
			if err := tx.QueryRowContext(ctx, `SELECT max(end_time)::TIMESTAMP_NS FROM anomaly_log
WHERE namespace=? AND service=? AND kind=? AND end_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<=?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, a.Namespace, a.Service, a.Kind, a.From.UTC(), a.To.UTC()).Scan(&last); err != nil {
				return err
			}
			if last.After(to) {
				to = last.UTC()
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM anomaly_log WHERE namespace=? AND service=? AND kind=? AND end_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<=?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, a.Namespace, a.Service, a.Kind, a.From.UTC(), a.To.UTC()); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anomaly_log VALUES (?,?,?,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?,?)`, a.Namespace, a.Service, a.Kind, from, to, a.Title, a.Severity); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM anomaly_log WHERE end_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, now.UTC().Add(-30*24*time.Hour)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM anomaly_log WHERE (namespace,service,kind,start_time) NOT IN (SELECT namespace,service,kind,start_time FROM anomaly_log ORDER BY end_time DESC,namespace,service,kind,start_time LIMIT 10000)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Duck) DrainVersionRollup(ctx context.Context) (int64, error) {
	phase, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var total int64
	for phase.Err() == nil {
		n, err := d.RefreshVersionRollup(phase)
		if err != nil {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			if phase.Err() != nil {
				return total, nil
			}
			return total, err
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
	return total, ctx.Err()
}

func (d *Duck) transferVersionMarker(ctx context.Context, output telemetry.BatchMetadata, inputs []string) error {
	unlock, err := d.writeGate.LockContext(ctx, writegate.WriteRollupVersion)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := d.writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range inputs {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM version_rollup_batches WHERE batch_id=?`, id).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		} // The full output must be read when any input is cold.
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO version_rollup_batches VALUES (?,?) ON CONFLICT(batch_id) DO UPDATE SET max_ingested=greatest(version_rollup_batches.max_ingested,excluded.max_ingested)`, output.ID, output.MaxIngestedNanos); err != nil {
		return err
	}
	return tx.Commit()
}
```

In `internal/query/views.go`, immediately before `return createBatchCaches(db)` in `CreateCacheTables`, insert:

```go
if err := createAnnotationTables(db); err != nil {
	return err
}

```

In `internal/query/duck.go`, immediately before the final return in `rollupOnce`, insert:

```go
if n, err := d.DrainVersionRollup(ctx); err != nil {
	errs = append(errs, fmt.Errorf("version rollup: %w", err))
} else {
	affected += n
}

```

Extend `internal/query/writegate/write_gate.go`'s exhaustive constants:

```go
WriteRollupVersion WriteOperation = "rollup_version"
WriteAnomalyLog    WriteOperation = "anomaly_log"
```

In `runReadCacheLoop`, after the `RefreshReadCaches` result is handled, call `DrainVersionRollup(ctx)`; log its error when ctx is active and shorten the next delay to 100ms when it processes batches, using the existing worker's scheduling. In `PublishParquetReplacement` (`internal/query/batch_cache.go`), after `unlock()` and before `return d.PublishParquet(ctx, publish)`, insert:

```go
if err := d.transferVersionMarker(ctx, output, inputs); err != nil {
	slog.Warn("compaction version marker transfer failed; output will be read", "err", err)
}

```

The existing cachePublishing guard stays active through publication and protects the prepared output marker from cleanup. Never acquire the analytical gate while holding the completed-batch gate. Do not derive compaction ancestry from BatchMetadata: it has no input IDs; use this real publication callback.

- [ ] **Step 4: Implement the cache-only reader and detector persistence**

Create `internal/annotations/service.go`:

```go
package annotations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

var ErrRequest = errors.New("annotations need a positive range of at most 30 days and at most 100 services")

type Request struct {
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Services  []string  `json:"services,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
}
type Deploy struct {
	Namespace string    `json:"namespace"`
	Service   string    `json:"service"`
	Version   string    `json:"version"`
	At        time.Time `json:"at"`
}
type Anomaly struct {
	Namespace string    `json:"namespace"`
	Service   string    `json:"service"`
	Kind      string    `json:"kind"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Title     string    `json:"title"`
	Severity  string    `json:"severity"`
}
type Response struct {
	Deploys   []Deploy  `json:"deploys"`
	Anomalies []Anomaly `json:"anomalies"`
	Truncated bool      `json:"truncated,omitempty"`
}
type Service struct{ db queryrows.Queryer }

func New(db queryrows.Queryer) *Service { return &Service{db: db} }
func (s *Service) Read(ctx context.Context, req Request) (Response, error) {
	out := Response{Deploys: []Deploy{}, Anomalies: []Anomaly{}}
	if req.From.IsZero() || req.To.IsZero() || !req.From.Before(req.To) || req.To.Sub(req.From) > 30*24*time.Hour || len(req.Services) > 100 || len(req.Namespace) > 200 {
		return out, ErrRequest
	}
	suffix := ""
	args := []any{req.From.UTC(), req.To.UTC(), req.Namespace, req.Namespace}
	if len(req.Services) > 0 {
		slots := make([]string, len(req.Services))
		for i, v := range req.Services {
			if v == "" || len(v) > 200 {
				return out, ErrRequest
			}
			slots[i] = "?"
			args = append(args, v)
		}
		suffix = " AND service IN (" + strings.Join(slots, ",") + ")"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `WITH versions AS (SELECT *,row_number() OVER(PARTITION BY namespace,service ORDER BY first_seen,service_version) AS n FROM version_rollup)
SELECT namespace,service,service_version,first_seen::TIMESTAMP_NS FROM versions
WHERE first_seen>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND first_seen<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (?='' OR namespace=?) AND n>1`+suffix+` ORDER BY first_seen DESC,namespace,service,service_version LIMIT 1001`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a Deploy
		if err := rows.Scan(&a.Namespace, &a.Service, &a.Version, &a.At); err != nil {
			rows.Close()
			return out, err
		}
		a.At = a.At.UTC()
		out.Deploys = append(out.Deploys, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Deploys) > 1000 {
		out.Deploys = out.Deploys[:1000]
		out.Truncated = true
	}
	rows, err = s.db.QueryContext(ctx, `SELECT namespace,service,kind,start_time::TIMESTAMP_NS,end_time::TIMESTAMP_NS,title,severity FROM anomaly_log
WHERE end_time>?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (?='' OR namespace=?)`+suffix+` ORDER BY end_time DESC,namespace,service,kind LIMIT 1001`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a Anomaly
		if err := rows.Scan(&a.Namespace, &a.Service, &a.Kind, &a.From, &a.To, &a.Title, &a.Severity); err != nil {
			rows.Close()
			return out, err
		}
		a.From = a.From.UTC()
		a.To = a.To.UTC()
		out.Anomalies = append(out.Anomalies, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Anomalies) > 1000 {
		out.Anomalies = out.Anomalies[:1000]
		out.Truncated = true
	}
	rows, err = s.db.QueryContext(ctx, `SELECT coalesce(max(last_ingested_unix_nano),0) FROM rollup_state WHERE cache_key IN ('version_rollup_v1_limited','version_rollup_v1_history_limited')`)
	if err != nil {
		return out, err
	}
	if rows.Next() {
		var limited int64
		if err := rows.Scan(&limited); err != nil {
			rows.Close()
			return out, err
		}
		out.Truncated = out.Truncated || limited > 0
	}
	err = rows.Err()
	rows.Close()
	return out, err
}
```

In `internal/intelligence/detector.go`, import `internal/annotations`. Immediately after `snapshot := d.GenerateSnapshot(ctx)` in `runCheck`, insert:

```go
findings := detectorAnnotations(snapshot, d.duck.DefaultNamespace(), d.config.LookbackWindow)
if err := d.duck.RecordAnomalies(ctx, findings, snapshot.GeneratedAt); err != nil {
	slog.Error("persist detector annotations", "error", err)
}

```

Append the deterministic mapping helper to detector.go:

```go
func detectorAnnotations(snapshot IntelligenceSnapshot, namespace string, lookback time.Duration) []annotations.Anomaly {
	findings := make([]annotations.Anomaly, 0, len(snapshot.Anomalies))
	for _, a := range snapshot.Anomalies {
		severity := "warn"
		if math.Abs(a.ZScore) >= 4 {
			severity = "bad"
		}
		end := a.DetectedAt.UTC().Truncate(time.Minute)
		findings = append(findings, annotations.Anomaly{Namespace: namespace, Service: a.ServiceName, Kind: string(a.Type), From: end.Add(-lookback), To: end, Title: a.Description, Severity: severity})
	}
	return findings
}
```

Create `internal/intelligence/annotations_test.go`:

```go
package intelligence

import (
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"testing"
	"time"
)

func TestM2DetectorAnnotationMapping(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RetentionDays: 30}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	at := time.Now().UTC().Truncate(time.Minute)
	snapshot := IntelligenceSnapshot{GeneratedAt: at, Anomalies: []Anomaly{{ServiceName: "checkout", Type: AnomalyLatencyDegradation, ZScore: -4, DetectedAt: at, Description: "Slow"}, {ServiceName: "payment", Type: AnomalyVolumeChange, ZScore: 3, DetectedAt: at, Description: "Low volume"}}}
	findings := detectorAnnotations(snapshot, "shop", 5*time.Minute)
	if err := duck.RecordAnomalies(t.Context(), findings, at); err != nil {
		t.Fatal(err)
	}
	got, err := annotations.New(duck).Read(t.Context(), annotations.Request{From: at.Add(-time.Hour), To: at.Add(time.Minute), Namespace: "shop"})
	if err != nil || len(got.Anomalies) != 2 {
		t.Fatalf("persisted: %+v %v", got, err)
	}
	for _, a := range got.Anomalies {
		want := "warn"
		if a.Service == "checkout" {
			want = "bad"
		}
		if a.Namespace != "shop" || a.Severity != want || !a.From.Equal(at.Add(-5*time.Minute)) || !a.To.Equal(at) {
			t.Fatalf("mapping: %+v", a)
		}
	}
}
```

- [ ] **Step 5: Implement and classify the route**

Create `internal/api/annotations.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/annotations"
)

type AnnotationReader interface {
	Read(context.Context, annotations.Request) (annotations.Response, error)
}

func RegisterAnnotationRoutes(e *echo.Echo, service AnnotationReader) {
	e.POST("/api/annotations", func(c *echo.Context) error {
		var req annotations.Request
		if err := decodeStrict(c, &req, 32<<10); err != nil {
			return err
		}
		out, err := service.Read(c.Request().Context(), req)
		if errors.Is(err, annotations.ErrRequest) {
			return echo.NewHTTPError(400, err.Error())
		}
		if err != nil {
			return panelError(c, err, "annotations unavailable")
		}
		return c.JSON(http.StatusOK, out)
	}, RequireCapability(ReadTelemetry))
}
```

In `classifyRoute`, add `path == "/api/annotations"` to the existing POST ReadTelemetry case. In `cmd/fanout/main.go`, import `internal/annotations` and insert after `api.RegisterPanelRoutes(e, panels)`:

```go
api.RegisterAnnotationRoutes(e, annotations.New(q))

```

- [ ] **Step 6: Run to see pass and review**

```bash
rtk just test ./internal/query/... ./internal/api/... ./internal/intelligence/... -run '"TestM2Version|TestM2Annotation|TestM2Anomaly|TestM2DetectorAnnotation|TestVolume|TestErrorRate"'
rtk just test ./internal/query/... ./internal/api/... ./internal/intelligence/...
```

Expected: PASS. Inspect generated SQL in the fixture failures: version reads are from analytical tables and new-file work is an explicit bounded file list. No changes to the completed-batch write gate or SQLite.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

Regenerate route/tool/setting reference docs before this task’s commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
```

- [ ] **Step 7: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/annotations internal/query/annotations.go internal/query/annotations_test.go internal/query/views.go internal/query/duck.go internal/query/batch_cache.go internal/query/writegate/write_gate.go internal/intelligence/detector.go internal/intelligence/annotations_test.go internal/api/annotations.go internal/api/annotations_test.go internal/api/auth_middleware.go cmd/fanout/main.go
rtk git commit -m "feat(annotations): persist bounded deploy and detector history"
```

---

### Task 2: Checked exemplars behind a panel selection

**Files:**
- Create: `internal/panel/exemplars.go`, `internal/panel/exemplars_test.go`
- Create: `internal/panel/log_source.go`
- Modify: `internal/observability/redact.go`, `internal/api/panels.go`, `internal/api/panels_test.go`, `internal/api/auth_middleware.go`

**Interfaces:**
- Consumes existing: `(*Executor).check(ctx context.Context, d *Dashboard) (*Checked, error)`; `(*Executor).values(ctx context.Context, d *Dashboard, checked *Checked, start, end time.Time, supplied map[string]Value) (map[string]Value, error)`; `resolveWindow(t Time, override *PanelTime, now time.Time, maxWindow time.Duration) (time.Time, time.Time, error)`; `buildWhere(sig *signal, filters []Filter, scope Scope) (string, []any, error)`; `(*signal).field(text string) (FieldRef, error)`; `(FieldRef).stringSQL() string`; `queryrows.WithWindow(ctx context.Context, window queryrows.Window) context.Context`.
- Produces: `checkedTraceSQL(source,where string,limit int,order,startProjection string) string`; `ExemplarRequest`, `Exemplar`, `ExemplarResponse`; `(*Executor).Exemplars(ctx context.Context, req ExemplarRequest) (ExemplarResponse, error)`; `(*PanelHandler).exemplars(c *echo.Context) error`. Go signatures and JSON fields are defined in the complete code below. No TypeScript consumer until Task 10.
- Extends `api.PanelEngine` with `Exemplars(context.Context, panel.ExemplarRequest) (panel.ExemplarResponse, error)`; fake engine receives that method. POST `/api/panels/exemplars` uses ReadTelemetry, without saved-dashboard lookup or owner input.

- [ ] **Step 1: Write failing tests**

Create `internal/panel/exemplars_test.go`:

```go
package panel

import (
	"errors"
	"github.com/labstack/fanout/internal/telemetry"
	"math"
	"slices"
	"testing"
	"time"
)

func TestM2ExemplarsCheckedScope(t *testing.T) {
	e := newFixtureExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", From: fixtureStart.Add(30 * time.Minute), To: fixtureStart.Add(31 * time.Minute), Dimensions: map[string]string{"http_route": "/cart"}}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Traces) != 1 || got.Traces[0].Service != "checkout" || got.Traces[0].DurationMS != 900 || got.Traces[0].Status != "STATUS_CODE_ERROR" {
		t.Fatalf("traces: %+v", got)
	}
	req.Vars = map[string]Value{"service": {Values: []string{"frontend"}}}
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("filter escaped: %+v %v", got, err)
	}
	req.From = fixtureStart.Add(-time.Hour)
	req.To = fixtureStart.Add(2 * time.Hour)
	req.Dimensions = nil
	req.Vars = nil
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 20 || !got.Truncated {
		t.Fatalf("cap: %+v %v", got, err)
	}
}
func TestM2ExemplarsRejectForgedDimensions(t *testing.T) {
	e := newFixtureExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", From: fixtureStart, To: fixtureStart.Add(time.Hour), Dimensions: map[string]string{"service": "frontend"}}
	_, err := e.Exemplars(t.Context(), req)
	var problems Problems
	want := Problem{Path: "dimensions", Message: "dimensions must name query.by fields and values at most 500 characters", Hint: "select one of the panel grouping fields"}
	if !errors.As(err, &problems) || !slices.Contains(problems, want) {
		t.Fatalf("got %v want %+v", err, want)
	}
	req.Dimensions = map[string]string{"http_route": "x'); DROP TABLE spans; --"}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("hostile value: %+v %v", got, err)
	}
	req.Dashboard.Panels[2].Query.Where = []string{"service IN (SELECT service FROM spans)"}
	if _, err := e.Exemplars(t.Context(), req); err == nil {
		t.Fatal("guard bypass")
	}
}
func TestM2ExemplarBucketValidation(t *testing.T) {
	p := &Panel{Query: &Query{From: "spans", Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}
	scope := Scope{Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	bad := math.NaN()
	upper := 1.0
	cases := []struct {
		bucket *SelectionBucket
		want   Problem
	}{
		{&SelectionBucket{Lower: bad}, Problem{Path: "bucket", Message: "bucket needs a spans duration_ms histogram and a finite nonnegative lower bound", Hint: "select a finite duration bucket"}},
		{&SelectionBucket{Lower: 2, Upper: &upper}, Problem{Path: "bucket.upper", Message: "upper must be finite and greater than lower", Hint: "select a larger upper bound or omit it for overflow"}},
	}
	for _, tc := range cases {
		_, _, err := selectionWhere(p, nil, scope, nil, tc.bucket)
		var got Problems
		if !errors.As(err, &got) || !slices.Contains(got, tc.want) {
			t.Fatalf("got %v want %+v", err, tc.want)
		}
	}
}
func TestM2ExemplarsLogsCandidateUsesRedactedBody(t *testing.T) {
	engine, repo := newTestEngine(t)
	n := fixtureStart.UnixNano()
	spans := []telemetry.Span{{Namespace: "shop", ServiceName: "checkout", TraceID: "log-trace", SpanID: "root", Name: "root", DurationMS: 1, StartUnixNanos: n, EndUnixNanos: n + 1000000, IngestedAt: n}}
	logs := []telemetry.Log{{Namespace: "shop", ServiceName: "checkout", TraceID: "log-trace", Body: "token=secret failed", BodyTemplate: "token=secret failed", EventUnixNanos: n, IngestedAt: n}}
	commit(t, repo, spans, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Log lineage", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "bar", Query: &Query{From: "logs", Where: []string{"body LIKE '%failed%'"}, Measures: []string{"count()"}, By: []string{"severity"}}}}}
	req := ExemplarRequest{Dashboard: d, PanelID: "p", From: fixtureStart, To: fixtureStart.Add(time.Minute)}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 1 || got.Traces[0].TraceID != "log-trace" {
		t.Fatalf("logs candidate: %+v %v", got, err)
	}
	req.Dashboard.Panels[0].Query.Where = []string{"body LIKE '%secret%'"}
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 0 {
		t.Fatalf("searched unredacted source: %+v %v", got, err)
	}
}
```

Append to `internal/api/panels_test.go` (the existing imports already include context, HTTP, strings, testing):

```go
func (f *fakePanels) Exemplars(context.Context, panel.ExemplarRequest) (panel.ExemplarResponse, error) {
	return panel.ExemplarResponse{Traces: []panel.Exemplar{}}, nil
}
func TestM2ExemplarRoutePolicy(t *testing.T) {
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/panels/exemplars", `{"dashboard":{"name":"Inline","panels":[]},"panel_id":"latency","from":"2026-10-01T12:00:00Z","to":"2026-10-01T12:05:00Z","dimensions":{}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"traces":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	policy, ok := classifyRoute(http.MethodPost, "/api/panels/exemplars")
	if !ok || policy.capability != ReadTelemetry {
		t.Fatalf("policy: %+v %v", policy, ok)
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/panels/exemplars"); ok {
		t.Fatal("GET accepted")
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... ./internal/api/... -run '"TestM2Exemplar"'
```

Expected: missing Exemplar types/method.

- [ ] **Step 3: Implement exemplars**

Append to `internal/observability/redact.go`:

```go
// RedactLogBodySQL shares the existing read-path expression with panel projections.
func RedactLogBodySQL(column string) string { return redactLogBodySQL(column) }
```

Create `internal/panel/log_source.go`:

```go
package panel

import "github.com/labstack/fanout/internal/observability"

func redactedLogSource() string {
	return `SELECT * REPLACE (` + observability.RedactLogBodySQL("body") + ` AS body,` + observability.RedactLogBodySQL("body_template") + ` AS body_template) FROM logs`
}
```

Create `internal/panel/exemplars.go`:

```go
package panel

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type SelectionBucket struct {
	Lower float64  `json:"lower"`
	Upper *float64 `json:"upper,omitempty"`
}
type ExemplarRequest struct {
	Dashboard  Dashboard         `json:"dashboard"`
	PanelID    string            `json:"panel_id"`
	Time       *Time             `json:"time,omitempty"`
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Bucket     *SelectionBucket  `json:"bucket,omitempty"`
	Vars       map[string]Value  `json:"vars,omitempty"`
}
type Exemplar struct {
	TraceID    string    `json:"trace_id"`
	Namespace  string    `json:"namespace"`
	Service    string    `json:"service"`
	Operation  string    `json:"operation"`
	DurationMS float64   `json:"duration_ms"`
	Status     string    `json:"status"`
	Start      time.Time `json:"start"`
}
type ExemplarResponse struct {
	Traces    []Exemplar `json:"traces"`
	Truncated bool       `json:"truncated,omitempty"`
}

func selectionWhere(p *Panel, filters []Filter, scope Scope, dimensions map[string]string, bucket *SelectionBucket) (string, []any, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return "", nil, err
	}
	if len(dimensions) > 3 {
		return "", nil, Problems{{Path: "dimensions", Message: "at most 3 dimensions"}}
	}
	names := make([]string, 0, len(dimensions))
	for name := range dimensions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !slices.Contains(p.Query.By, name) || len(dimensions[name]) > 500 {
			return "", nil, Problems{{Path: "dimensions", Message: "dimensions must name query.by fields and values at most 500 characters", Hint: "select one of the panel grouping fields"}}
		}
		ref, err := sig.field(name)
		if err != nil {
			return "", nil, err
		}
		where += " AND coalesce(" + ref.stringSQL() + ",'')=?"
		args = append(args, dimensions[name])
	}
	if bucket != nil {
		if p.Query.From != "spans" || p.Query.Histogram == nil || p.Query.Histogram.Field != "duration_ms" || math.IsNaN(bucket.Lower) || math.IsInf(bucket.Lower, 0) || bucket.Lower < 0 {
			return "", nil, Problems{{Path: "bucket", Message: "bucket needs a spans duration_ms histogram and a finite nonnegative lower bound", Hint: "select a finite duration bucket"}}
		}
		where += " AND duration_ms>=?"
		args = append(args, bucket.Lower)
		if bucket.Upper != nil {
			if math.IsNaN(*bucket.Upper) || math.IsInf(*bucket.Upper, 0) || *bucket.Upper <= bucket.Lower {
				return "", nil, Problems{{Path: "bucket.upper", Message: "upper must be finite and greater than lower", Hint: "select a larger upper bound or omit it for overflow"}}
			}
			where += " AND duration_ms<?"
			args = append(args, *bucket.Upper)
		}
	}
	return where, args, nil
}

func (e *Executor) Exemplars(ctx context.Context, req ExemplarRequest) (ExemplarResponse, error) {
	out := ExemplarResponse{Traces: []Exemplar{}}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	d := req.Dashboard
	Normalize(&d)
	checked, err := e.check(ctx, &d)
	if err != nil {
		return out, err
	}
	index := slices.IndexFunc(d.Panels, func(p Panel) bool { return p.ID == req.PanelID })
	if index < 0 {
		return out, Problems{{Path: "panel_id", Message: "panel does not exist in dashboard"}}
	}
	p := &d.Panels[index]
	if p.Query == nil || p.Query.From == "metrics" {
		return out, Problems{{Path: "panel_id", Message: "trace lineage needs a structured spans or logs panel"}}
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
		if t.Refresh == "" {
			t.Refresh = d.Time.Refresh
		}
		var problems Problems
		validateTime(t, &problems)
		if len(problems) > 0 {
			return out, problems
		}
	}
	override := p.Time
	if req.Time != nil && req.Time.From != nil && req.Time.To != nil {
		override = nil
	} // The browser captured the effective window.
	start, end, err := resolveWindow(t, override, e.now(), e.maxWindow)
	if err != nil {
		return out, err
	}
	if req.From.IsZero() || req.To.IsZero() || !req.From.Before(req.To) {
		return out, Problems{{Path: "from", Message: "selection needs a positive absolute range"}}
	}
	lo, hi := req.From.UTC(), req.To.UTC()
	if lo.Before(start) {
		lo = start
	}
	if hi.After(end) {
		hi = end
	}
	if !lo.Before(hi) {
		return out, outsideSelection()
	}
	vars, err := e.values(ctx, &d, checked, start, end, req.Vars)
	if err != nil {
		return out, err
	}
	where, args, err := selectionWhere(p, checked.Filters[p.ID], Scope{Start: lo, End: hi, Vars: vars}, req.Dimensions, req.Bucket)
	if err != nil {
		return out, err
	}
	// Candidate membership is filtered; root metadata is selected within the full panel window.
	candidateSource := p.Query.From
	if candidateSource == "logs" {
		candidateSource = "(" + redactedLogSource() + ")"
	}
	text := checkedTraceSQL(candidateSource, where, 21, "duration_ms DESC,trace_id,namespace", "start_time::TIMESTAMP_NS")
	args = append(args, start, end)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end})
	rows, err := e.engine.QueryContext(ctx, text, args...)
	if err != nil {
		return out, fmt.Errorf("exemplars: %s", SafeError(err))
	}
	defer rows.Close()
	for rows.Next() {
		var trace Exemplar
		if err := rows.Scan(&trace.TraceID, &trace.Namespace, &trace.Service, &trace.Operation, &trace.DurationMS, &trace.Status, &trace.Start); err != nil {
			return out, err
		}
		trace.Start = trace.Start.UTC()
		out.Traces = append(out.Traces, trace)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Traces) > 20 {
		out.Traces = out.Traces[:20]
		out.Truncated = true
	}
	// Conservatively disclose the candidate cap even if fewer than 20 roots survive.
	if len(out.Traces) == 20 {
		out.Truncated = true
	}
	return out, nil
}
func checkedTraceSQL(source, where string, limit int, order, startProjection string) string {
	return fmt.Sprintf(`WITH candidates AS (SELECT DISTINCT namespace,trace_id FROM %s WHERE %s AND trace_id<>'' ORDER BY namespace,trace_id LIMIT 1000), roots AS (
SELECT s.*,max(CASE WHEN s.status IN ('STATUS_CODE_ERROR','ERROR') THEN 1 ELSE 0 END) OVER(PARTITION BY s.namespace,s.trace_id) AS trace_error,row_number() OVER(PARTITION BY s.namespace,s.trace_id ORDER BY (coalesce(s.parent_span_id,'')='') DESC,s.start_time,s.span_id) AS n
FROM spans s JOIN candidates c ON s.namespace=c.namespace AND s.trace_id=c.trace_id
WHERE s.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)
SELECT trace_id,coalesce(namespace,''),coalesce(service,''),coalesce(operation,''),duration_ms,CASE WHEN trace_error>0 THEN 'STATUS_CODE_ERROR' ELSE coalesce(status,'') END,%s FROM roots WHERE n=1 ORDER BY %s LIMIT %d`, source, where, startProjection, order, limit)
}
func outsideSelection() error {
	return Problems{{Path: "from", Message: "selection does not overlap the panel window"}}
}
```

In `internal/api/panels.go`, extend `PanelEngine`:

```go
Exemplars(context.Context, panel.ExemplarRequest)(panel.ExemplarResponse, error)

```

Register next to `/api/panels/query`:

```go
e.POST("/api/panels/exemplars", h.exemplars, read)

```

Append:

```go
func (h *PanelHandler) exemplars(c *echo.Context) error {
	var req panel.ExemplarRequest
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	out, err := h.engine.Exemplars(c.Request().Context(), req)
	if err != nil {
		return panelError(c, err, "exemplars unavailable")
	}
	return c.JSON(http.StatusOK, out)
}
```

In `classifyRoute`, add `path == "/api/panels/exemplars"` to the POST ReadTelemetry case. Keep dashboard CRUD on ManageOwnDashboards. The executor enforces its own timeout; the adapter adds no batch deadline.

- [ ] **Step 4: Run to see pass**

```bash
rtk just test ./internal/panel/... ./internal/api/... -run '"TestM2Exemplar|TestFinalFixPanelBatch"'
rtk just test ./internal/panel/... ./internal/api/...
```

Expected: PASS, 20 trace maximum, hostile dimension value treated literally, forged field rejected.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

Regenerate route/tool/setting reference docs before this task’s commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
```

- [ ] **Step 5: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel/log_source.go internal/observability/redact.go internal/panel/exemplars.go internal/panel/exemplars_test.go internal/api/panels.go internal/api/panels_test.go internal/api/auth_middleware.go
rtk git commit -m "feat(panels): query checked and window-bounded trace exemplars"
```

---

### Task 3: Distribution queries, histogram frames and heatmap bounds

**Files:**
- Create: `internal/panel/distribution.go`, `internal/panel/distribution_test.go`
- Modify: `internal/panel/compile.go`, `internal/panel/exec.go`, `internal/panel/validate.go`, `internal/panel/spec.go`

**Interfaces:**
- Consumes existing: `compileQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error)`; `scanFrame(rows queryrows.Rows, columns []Column, maxRows int) (*Frame, error)`; `Histogram{Field string, Buckets string}`; `telemetry.Metric` with `HistBoundsJSON`, `HistCountsJSON`, `HistCount`; `newTestEngine(t *testing.T) (*query.Duck, *telemetrystore.Repository)`.
- Produces: `compileDistribution(p *Panel, filters []Filter, scope Scope) (Compiled, error)`; `distributionRows(p *Panel, columns int) int`; `const analysisCellLimit = 20000`; `boundAnalysisFrame(f *Frame)`.
- Frame: optional `time` (Unix ms), optional `query.by[0]` dimension, `bucket_lower`/`bucket_upper` numeric dimensions (null represents an infinite endpoint), `count` number measure. Heatmap has time, no split; histogram has optional one split and no time. Metric histogram field is `value`, buckets `explicit`; spans field is `duration_ms`, buckets `log2`. Counts are observation weights, not point counts. `telemetry.Metric` (rows.go:77), `metricParquetRow` (parquet_rows.go:124), `createMetricsTable` and `viewMetrics` have no temporality field. Missing/unknown temporality therefore means cumulative (OTel default). Add optional `Histogram.Temporality` (`temporality`, cumulative|delta) so a caller with known delta provenance can request summation without changing format 3; omitted is cumulative. Cumulative counts use each series’ last minus first per bound, clamped at zero on reset; never sum cumulative scrape values. Series identity includes namespace, service, name, unit, scope, attributes and resource. For heatmap, apply the same reduction within each displayed time bucket.
- No TypeScript code is changed here; Task 7 introduces the exact mirrors.

- [ ] **Step 1: Write failing fixture tests**

Create `internal/panel/distribution_test.go`:

```go
package panel

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestM2DistributionCounts(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{{ID: "h", Title: "Latency distribution", Viz: "histogram", Query: &Query{From: "spans", Where: []string{"service = $service"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if got[0].Status != "ok" || f == nil || f.Rows != 3 {
		t.Fatalf("distribution: %+v", got)
	}
	var n float64
	for _, v := range f.Values[2] {
		n += v.(float64)
	}
	if n != 120 {
		t.Fatalf("count=%v", n)
	}
	engine, repo := newTestEngine(t)
	at := fixtureStart.UnixNano()
	err = repo.Commit(t.Context(), telemetrystore.Batch{ID: "hist-metric", Metrics: []telemetry.Metric{{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at, HistBoundsJSON: "[10,20]", HistCountsJSON: "[2,3,5]", HistCount: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	e = NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d.Variables = nil
	d.Panels[0].Query = &Query{From: "metrics", Where: []string{"name = 'latency'"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit", Temporality: "delta"}}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f = got[0].Frame
	if got[0].Status != "ok" || f.Rows != 3 || f.Values[2][2].(float64) != 5 || f.Values[1][2] != nil {
		t.Fatalf("weighted overflow: %+v", got)
	}
}
func TestM2MetricHistogramTemporality(t *testing.T) {
	engine, repo := newTestEngine(t)
	metrics := []telemetry.Metric{}
	for scrape, counts := range []string{"[10,20,30]", "[12,23,34]", "[15,27,40]"} {
		n := fixtureStart.Add(time.Duration(scrape) * time.Minute).UnixNano()
		metrics = append(metrics, telemetry.Metric{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", Unit: "ms", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10,20]", HistCountsJSON: counts, HistCount: 100, Attributes: map[string]any{"series": "a"}})
	}
	for scrape, counts := range []string{"[100,100,100]", "[101,101,101]", "[102,102,102]"} {
		n := fixtureStart.Add(time.Duration(scrape) * time.Minute).UnixNano()
		metrics = append(metrics, telemetry.Metric{Namespace: "shop", ServiceName: "checkout", Name: "latency", Type: "histogram", Unit: "ms", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10,20]", HistCountsJSON: counts, Attributes: map[string]any{"series": "b"}})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "scrapes", Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Hist", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "h", Title: "H", Viz: "histogram", Unit: "ms", Query: &Query{From: "metrics", Where: []string{"name = 'latency'"}, Measures: []string{"count()"}, Histogram: &Histogram{Field: "value", Buckets: "explicit"}}}}}
	for _, temporality := range []string{"", "cumulative", "delta"} {
		d.Panels[0].Query.Histogram.Temporality = temporality
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || got[0].Status != "ok" {
			t.Fatalf("hist: %+v %v", got, err)
		}
		want := []float64{7, 9, 12}
		if temporality == "delta" {
			want = []float64{340, 373, 407}
		}
		f := got[0].Frame
		if f.Rows != 3 {
			t.Fatalf("rows=%d", f.Rows)
		}
		for i, n := range want {
			if f.Values[2][i] != n {
				t.Fatalf("%q bucket %d=%v want %v", temporality, i, f.Values[2][i], n)
			}
		}
	}
	// A resetting series contributes max(last-first,0), not unsigned underflow.
	d.Panels[0].Query.Histogram.Temporality = ""
	n := fixtureStart.UnixNano()
	reset := []telemetry.Metric{{ServiceName: "reset", Name: "reset", Type: "histogram", EventUnixNanos: n, IngestedAt: n, HistBoundsJSON: "[10]", HistCountsJSON: "[20,30]"}, {ServiceName: "reset", Name: "reset", Type: "histogram", EventUnixNanos: n + int64(time.Minute), IngestedAt: n + 1, HistBoundsJSON: "[10]", HistCountsJSON: "[1,2]"}}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "reset", Metrics: reset}); err != nil {
		t.Fatal(err)
	}
	d.Panels[0].Query.Where = []string{"name = 'reset'"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("reset: %+v %v", got, err)
	}
}
func TestM2DistributionBudget(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := []telemetry.Span{}
	for m := range 1440 {
		for b := range 32 {
			n := fixtureStart.Add(time.Duration(m) * time.Minute).UnixNano()
			spans = append(spans, telemetry.Span{TraceID: "t", SpanID: "s", ServiceName: "checkout", Kind: "SPAN_KIND_SERVER", StartUnixNanos: n, EndUnixNanos: n + 1000, DurationMS: float64(uint64(1) << b), IngestedAt: n})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(24 * time.Hour) }
	d := Dashboard{Name: "Heat", Time: Time{Range: "24h"}, Panels: []Panel{{ID: "heat", Title: "Heat", Viz: "heatmap", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "1m", Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if f == nil || !f.Truncated || f.Rows*len(f.Columns) > analysisCellLimit {
		t.Fatalf("unbounded heat: %+v", got)
	}
	original := d.Panels[0]
	d.Panels = nil
	for i := 0; i < 11; i++ {
		copy := original
		copy.ID = fmt.Sprintf("bounded_%d", i)
		d.Panels = append(d.Panels, copy)
	}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	for _, r := range got {
		if r.Frame != nil {
			cells += r.Frame.Rows * len(r.Frame.Columns)
		}
	}
	if cells > 200000 {
		t.Fatalf("batch has %d cells", cells)
	}

}
func TestM2DistributionValidation(t *testing.T) {
	cases := []struct {
		p    Panel
		want Problem
	}{
		{Panel{ID: "h", Title: "H", Viz: "heatmap", Query: &Query{From: "spans", Measures: []string{"count()"}}}, Problem{Path: "panels[0].query.histogram", Message: "distribution panels require a histogram", Hint: "spans: duration_ms/log2; metrics: value/explicit"}},
		{Panel{ID: "h", Title: "H", Viz: "histogram", Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, Histogram: &Histogram{Field: "duration_ms", Buckets: "log2"}}}, Problem{Path: "panels[0].query.measures", Message: "distribution counts require count()", Hint: "use count() as the observation weight"}},
	}
	for _, tc := range cases {
		d := Dashboard{Name: "Rule", Panels: []Panel{tc.p}}
		Normalize(&d)
		got := Validate(&d)
		if !slices.Contains(got, tc.want) {
			t.Fatalf("got %+v want %+v", got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... -run '"TestM2Distribution|TestM2MetricHistogram"'
```

Expected: new viz rejected; missing budget constant.

- [ ] **Step 3: Implement compilation and bounded frames**

Create `internal/panel/distribution.go`:

```go
package panel

import (
	"fmt"
	"sort"
	"strings"
)

const analysisCellLimit = 20000

func distributionRows(p *Panel, columns int) int {
	n := analysisCellLimit / max(columns, 1)
	if p.Viz == "histogram" {
		n = min(n, 1000)
	}
	if p.Query.Limit > 0 {
		n = min(n, p.Query.Limit)
	}
	return n
}
func compileDistribution(p *Panel, filters []Filter, scope Scope) (Compiled, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	columns := []Column{}
	prefix := []string{}
	groups := []string{}
	if p.Viz == "heatmap" {
		prefix = append(prefix, fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',t::TIMESTAMP_NS))::BIGINT AS time", int64(scope.Interval.Seconds())))
		groups = append(groups, "time")
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	dimSelect := "' AS dim"
	if len(p.Query.By) > 0 {
		ref, err := sig.field(p.Query.By[0])
		if err != nil {
			return Compiled{}, err
		}
		dimSelect = "coalesce(" + ref.stringSQL() + ",'') AS dim"
		prefix = append(prefix, "dim")
		groups = append(groups, "dim")
		columns = append(columns, Column{Name: ref.alias(), Type: "string", Role: "dimension"})
	}
	unit := "ms"
	raw := ""
	if sig.name == "spans" {
		raw = fmt.Sprintf(`SELECT %s AS t,%s,
CASE WHEN duration_ms<1 THEN 0 ELSE pow(2,least(32,floor(log2(duration_ms)))) END AS lo,
CASE WHEN duration_ms<pow(2,32) THEN CASE WHEN duration_ms<1 THEN 1 ELSE pow(2,floor(log2(duration_ms))+1) END END AS hi,
1::DOUBLE AS n FROM spans WHERE %s AND duration_ms>=0 AND isfinite(duration_ms)`, quoteIdent(sig.time), dimSelect, where)
	} else {
		unit = p.Unit
		raw = metricHistogramRaw(dimSelect, where, p.Query.Histogram.Temporality, scope.Interval, p.Viz == "heatmap")
	}
	columns = append(columns, Column{Name: "bucket_lower", Type: "number", Role: "dimension", Unit: unit}, Column{Name: "bucket_upper", Type: "number", Role: "dimension", Unit: unit}, Column{Name: "count", Type: "number", Role: "measure", Unit: "count"})
	prefix = append(prefix, "lo AS bucket_lower", "hi AS bucket_upper", "sum(n)::DOUBLE AS count")
	groups = append(groups, "lo", "hi")
	source := "raw"
	cte := "WITH raw AS (" + raw + ")"
	if len(p.Query.By) > 0 {
		cte += fmt.Sprintf(",top AS (SELECT dim FROM raw GROUP BY dim ORDER BY sum(n) DESC,dim LIMIT %d),folded AS (SELECT * REPLACE (CASE WHEN dim IN (SELECT dim FROM top) THEN dim ELSE 'Other' END AS dim) FROM raw)", p.Top())
		source = "folded"
	}
	text := cte + " SELECT " + strings.Join(prefix, ",") + " FROM " + source + " GROUP BY " + strings.Join(groups, ",")
	order := "bucket_lower NULLS FIRST,bucket_upper NULLS LAST"
	if p.Viz == "heatmap" {
		order = "time DESC," + order
	}
	if len(p.Query.By) > 0 {
		order = "dim," + order
	}
	n := distributionRows(p, len(columns))
	text += " ORDER BY " + order + fmt.Sprintf(" LIMIT %d", n+1)
	return Compiled{SQL: text, Args: args, Columns: columns}, nil
}
```

Append to distribution.go (the temporality expression is selected by validated enum, never interpolated from client SQL; typed VARIANT identity stays in the engine):

```go
func metricHistogramRaw(dimSelect, where, temporality string, interval time.Duration, heat bool) string {
	period := "0::BIGINT"
	if heat {
		period = fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',time::TIMESTAMP_NS))", int64(interval/time.Second))
	}
	delta := "FALSE"
	if temporality == "delta" {
		delta = "TRUE"
	}
	return fmt.Sprintf(`WITH points AS (
 SELECT *,%s,%s AS period,
 from_json(hist_bounds_json,'["DOUBLE"]') AS bounds,
 from_json(hist_counts_json,'["UBIGINT"]') AS counts
 FROM metrics WHERE %s AND type='histogram'), expanded AS (
 SELECT namespace,service,name,unit,scope_name,scope_version,attributes,resource,dim,period,time AS t,ingested_unix_nano,
 CASE WHEN i=1 THEN NULL ELSE bounds[i-1] END AS lo,
 CASE WHEN i<=len(bounds) THEN bounds[i] END AS hi,n::DOUBLE AS n
 FROM points,UNNEST(counts) WITH ORDINALITY AS u(n,i)
 WHERE len(counts)=len(bounds)+1), increases AS (
 SELECT dim,period,lo,hi,max(t) AS t,
 CASE WHEN %s THEN sum(n) ELSE greatest(0,arg_max(n,(t,ingested_unix_nano))-arg_min(n,(t,ingested_unix_nano))) END::DOUBLE AS n
 FROM expanded GROUP BY namespace,service,name,unit,scope_name,scope_version,attributes,resource,dim,period,lo,hi)
 SELECT t,dim,lo,hi,n FROM increases WHERE n>0`, dimSelect, period, where, delta)
}
```

Add `"time"` to distribution.go imports. Extend the existing Histogram struct in spec.go:

```go
Temporality string `json:"temporality,omitempty" jsonschema:"metric histograms: cumulative (also unknown/default) or delta"`
```

In the histogram validation branch, also reject a nonempty temporality on spans and unknown strings with `addHint(path+".histogram.temporality", "temporality applies to metric histograms and must be cumulative or delta", "omit for the OTel cumulative default")`.

At the beginning of `compileQuery` (before `q := p.Query`), insert:

```go
if p.Viz == "heatmap" || p.Viz == "histogram" {
	return compileDistribution(p, filters, scope)
}

```

In `runScope`, after the existing `if scope.Interval > 0 { rowLimit = 0 }` and before `scanFrame`, insert:

```go
if p.Viz == "heatmap" || p.Viz == "histogram" {
	rowLimit = distributionRows(p, len(compiled.Columns))
}

```

Set the scanned frame’s private `bucketed` flag to `scope.Interval > 0 && len(compiled.Columns)>0 && compiled.Columns[0].Role=="time"`; log-pattern JSON trends are not a time-first frame. Keep `limitBatchFrames` intact. The SQL chooses newest heatmap data before sorting for rendering; the scanner bounds transfer and marks truncation.

Append the complete frame bound helper to distribution.go:

```go
func boundAnalysisFrame(f *Frame) {
	if f == nil || len(f.Columns) == 0 || f.Columns[0].Role != "time" || f.Rows == 0 {
		return
	}
	order := make([]int, f.Rows)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return f.Values[0][order[i]].(int64) < f.Values[0][order[j]].(int64) })
	for c := range f.Values {
		values := make([]any, len(order))
		for r, index := range order {
			values[r] = f.Values[c][index]
		}
		f.Values[c] = values
	}
	cut := 0
	if f.Truncated {
		oldest := f.Values[0][0]
		for cut < f.Rows && f.Values[0][cut] == oldest {
			cut++
		}
	}
	times := []int{}
	for r := cut; r < f.Rows; r++ {
		if r == cut || f.Values[0][r] != f.Values[0][r-1] {
			times = append(times, r)
		}
	}
	if len(times) > maxSeriesPoints {
		cut = times[len(times)-maxSeriesPoints]
		f.Truncated = true
	}
	if cut > 0 {
		for c := range f.Values {
			f.Values[c] = f.Values[c][cut:]
		}
		f.Rows -= cut
	}
}
```

Immediately after scanFrame succeeds in runScope, call `if p.Viz=="heatmap"{boundAnalysisFrame(frame)}`. This sorts the newest-first bounded transfer and discards the potentially clipped oldest bucket as a whole. It also caps distinct times at maxSeriesPoints (2000). In Task 4 extend that same condition to state_timeline. After the Task 4 extension, use this complete block before returning the structured frame:

```go
if frame != nil && (p.Viz == "heatmap" || p.Viz == "state_timeline") {
	boundAnalysisFrame(frame)
	if p.Viz == "state_timeline" {
		items := map[any]bool{}
		for _, item := range frame.Values[1] {
			items[item] = true
		}
		if len(items) == p.Top() {
			frame.Truncated = true
		}
	}
}

```

The timeline cap is disclosed after clipping so it does not remove another time bucket. The shared batch cell budget still runs last.

- [ ] **Step 4: Implement structural rules**

In `vizSpecs`, add:

```go
"heatmap":   {width: 6, height: "m", query: true, bucket: true},
"histogram": {width: 6, height: "m", query: true, maxBy: 1},
```

Append both names to `vizOrder`. Change the `Panel.Viz` schema description to include them. Replace the existing `if q.Histogram != nil` rejection in `validateQuery` with:

```go
if p.Viz == "heatmap" || p.Viz == "histogram" {
	if q.Histogram == nil {
		problems.addHint(path+".histogram", "distribution panels require a histogram", "spans: duration_ms/log2; metrics: value/explicit")
	} else if (q.From != "spans" || q.Histogram.Field != "duration_ms" || q.Histogram.Buckets != "log2") &&
		(q.From != "metrics" || q.Histogram.Field != "value" || q.Histogram.Buckets != "explicit") {
		problems.add(path+".histogram", "use spans duration_ms/log2 or metrics value/explicit")
	}
	if len(q.Measures) != 1 || q.Measures[0] != "count()" {
		problems.addHint(path+".measures", "distribution counts require count()", "use count() as the observation weight")
	}
	if p.SQL != "" {
		problems.add(path+".sql", "distribution panels use a structured histogram")
	}
} else if q.Histogram != nil {
	problems.add(path+".histogram", "histogram applies only to heatmap and histogram")
}

```

In `validatePanel`, immediately before its `if p.Query != nil { validateQuery(...) }` branch, insert:

```go
if (p.Viz == "heatmap" || p.Viz == "histogram") && p.Query == nil {
	problems.add(path+".query", "distribution panels require a structured query")
}

```

- [ ] **Step 5: Run to see pass**

```bash
rtk just test ./internal/panel/... -run '"TestM2Distribution|TestM2MetricHistogram|TestRunProducesFrames|TestRunReportsSQLTruncation"'
rtk just test ./internal/panel/...
```

Expected: count conservation for spans and weighted metric buckets, explicit overflow, truncated 24-hour heatmap, no engine syntax error.

Regenerate the changed panel/tool contract references, then format all Go changes before the controller commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

- [ ] **Step 6: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel/distribution.go internal/panel/distribution_test.go internal/panel/compile.go internal/panel/exec.go internal/panel/validate.go internal/panel/spec.go
rtk git commit -m "feat(panels): compile bounded latency and metric distributions"
```

---

### Task 4: Scatter and threshold-graded item timelines

**Files:**
- Create: `internal/panel/items.go`, `internal/panel/items_test.go`
- Modify: `internal/panel/spec.go`, `internal/panel/validate.go`, `internal/panel/compile.go`, `internal/panel/exec.go`

**Interfaces:**
- Consumes existing: `Measure{Text, Func, Alias, Unit string; Field *FieldRef; Q float64; Additive bool}`; `measureSQL(m Measure, sig *signal, seconds float64, partition string) string`; `Column`, `Scope`, `Compiled` and `buildWhere`.
- Produces: `compileItems(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error)`; `validateItems(p *Panel, path string, problems *Problems)`; Options fields `XScale string json:"x_scale,omitempty"`, `YScale string json:"y_scale,omitempty"`.
- Scatter frame: item dimension, optional colour dimension, two measure columns in query order. Timeline frame: time, item dimension, one numeric measure; browser computes threshold status using the same `statusFor` as existing stats. Null and absent buckets remain unknown, not healthy.
- TypeScript mirrors are introduced in Task 7. Add `Panel.XUnit` (`x_unit`) for scatter’s x axis, with `unit` describing y. Infer each axis from its respective measure when no override is set.

- [ ] **Step 1: Write failing tests**

Create `internal/panel/items_test.go`:

```go
package panel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestM2ItemsExecute(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = []Panel{
		{ID: "scatter", Title: "Mean and tail latency", Viz: "scatter", Query: &Query{From: "spans", Measures: []string{"avg(duration_ms)", "p95(duration_ms)"}, By: []string{"http_route", "service"}}, Options: &Options{XScale: "log", YScale: "linear"}},
		{ID: "states", Title: "Latency states", Viz: "state_timeline", Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "5m"}, Thresholds: []Threshold{{Value: 500, Status: "bad"}}},
	}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != "ok" || got[0].Frame.Rows != 3 || len(got[0].Frame.Columns) != 4 {
		t.Fatalf("scatter: %+v", got[0])
	}
	if got[1].Status != "ok" || got[1].Frame.Rows != 24 || got[1].Better != "lower" {
		t.Fatalf("timeline: %+v", got[1])
	}
}
func TestM2TimelineBudget(t *testing.T) {
	engine, repo := newTestEngine(t)
	spans := []telemetry.Span{}
	for m := range 1440 {
		for s := range 20 {
			n := fixtureStart.Add(time.Duration(m) * time.Minute).UnixNano()
			spans = append(spans, telemetry.Span{TraceID: fmt.Sprint(m, s), SpanID: "s", ServiceName: fmt.Sprint(s), StartUnixNanos: n, EndUnixNanos: n + 1000000, DurationMS: 1, IngestedAt: n})
		}
	}
	commit(t, repo, spans, nil)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(24 * time.Hour) }
	d := Dashboard{Name: "States", Time: Time{Range: "24h"}, Panels: []Panel{{ID: "states", Title: "States", Viz: "state_timeline", Options: &Options{Top: 20}, Thresholds: []Threshold{{Value: 5, Status: "bad"}}, Query: &Query{From: "spans", Measures: []string{"p95(duration_ms)"}, By: []string{"service"}, Bucket: "1m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if f == nil || !f.Truncated || f.Rows*len(f.Columns) > analysisCellLimit {
		t.Fatalf("budget: %+v", got)
	}
	for _, v := range f.Values[1] {
		if v == "Other" {
			t.Fatal("item identities merged")
		}
	}
	original := d.Panels[0]
	d.Panels = nil
	for i := 0; i < 11; i++ {
		copy := original
		copy.ID = fmt.Sprintf("bounded_%d", i)
		d.Panels = append(d.Panels, copy)
	}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	for _, r := range got {
		if r.Frame != nil {
			cells += r.Frame.Rows * len(r.Frame.Columns)
		}
	}
	if cells > 200000 {
		t.Fatalf("batch has %d cells", cells)
	}

}
func TestM2ItemsValidation(t *testing.T) {
	mixed := Panel{ID: "s", Title: "S", Viz: "scatter", XUnit: "count", Unit: "ms", Query: &Query{From: "spans", Measures: []string{"count()", "p95(duration_ms)"}, By: []string{"service"}}}
	d := Dashboard{Name: "Mixed", Panels: []Panel{mixed}}
	Normalize(&d)
	if got := Validate(&d); len(got) != 0 {
		t.Fatalf("mixed-axis scatter rejected: %+v", got)
	}
	cases := []struct {
		panel Panel
		want  Problem
	}{
		{Panel{ID: "s", Title: "S", Viz: "state_timeline", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, Problem{Path: "panels[0].thresholds", Message: "state_timeline requires thresholds", Hint: "set a warn or bad boundary for the selected measure"}},
		{Panel{ID: "s", Title: "S", Viz: "scatter", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, Problem{Path: "panels[0].query.measures", Message: "scatter requires exactly two measures", Hint: "select x and y measures in that order"}},
		{Panel{ID: "s", Title: "S", Viz: "bar", XUnit: "count", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}}, Problem{Path: "panels[0].x_unit", Message: "x_unit applies only to scatter", Hint: "remove x_unit or use scatter"}},
	}
	unknown := mixed
	unknown.XUnit = "rows"
	cases = append(cases, struct {
		panel Panel
		want  Problem
	}{unknown, Problem{Path: "panels[0].x_unit", Message: "unknown x unit", Hint: "use one of " + strings.Join(unitNames(), ", ")}})
	for _, tc := range cases {
		d := Dashboard{Name: "Rule", Panels: []Panel{tc.panel}}
		Normalize(&d)
		got := Validate(&d)
		if !slices.Contains(got, tc.want) {
			t.Fatalf("got %+v want %+v", got, tc.want)
		}
	}
}
func TestM2TimelineSharePartitionsByBucket(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Share", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "s", Title: "S", Viz: "state_timeline", Thresholds: []Threshold{{Value: 50, Status: "warn"}}, Query: &Query{From: "spans", Measures: []string{"share()"}, By: []string{"service"}, Bucket: "5m"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame == nil {
		t.Fatalf("share: %+v %v", got, err)
	}
	sums := map[any]float64{}
	f := got[0].Frame
	for i, bucket := range f.Values[0] {
		sums[bucket] += f.Values[2][i].(float64)
	}
	for bucket, sum := range sums {
		if math.Abs(sum-100) > 1e-9 {
			t.Fatalf("bucket %v share=%v", bucket, sum)
		}
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... -run '"TestM2Items|TestM2Timeline"'
```

Expected: missing Options fields and unsupported types.

- [ ] **Step 3: Implement item compilation**

Create `internal/panel/items.go`:

```go
package panel

import (
	"fmt"
	"strings"
)

func compileItems(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	item, err := sig.field(p.Query.By[0])
	if err != nil {
		return Compiled{}, err
	}
	itemSQL := "coalesce(" + item.stringSQL() + ",'')"
	selects, groups := []string{}, []string{}
	columns := []Column{}
	if p.Viz == "state_timeline" {
		expr := fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',%s::TIMESTAMP_NS))::BIGINT", int64(scope.Interval.Seconds()), quoteIdent(sig.time))
		selects = append(selects, expr+" AS time")
		groups = append(groups, expr)
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	selects = append(selects, itemSQL+" AS "+quoteIdent(item.alias()))
	groups = append(groups, itemSQL)
	columns = append(columns, Column{Name: item.alias(), Type: "string", Role: "dimension"})
	if len(p.Query.By) == 2 {
		ref, err := sig.field(p.Query.By[1])
		if err != nil {
			return Compiled{}, err
		}
		expr := "coalesce(" + ref.stringSQL() + ",'')"
		selects = append(selects, expr+" AS "+quoteIdent(ref.alias()))
		groups = append(groups, expr)
		columns = append(columns, Column{Name: ref.alias(), Type: "string", Role: "dimension"})
	}
	seconds := scope.End.Sub(scope.Start).Seconds()
	if p.Viz == "state_timeline" {
		seconds = scope.Interval.Seconds()
	}
	partition := ""
	if p.Viz == "state_timeline" {
		partition = "PARTITION BY " + groups[0]
	}
	for i, m := range measures {
		unit := m.Unit
		if p.Viz == "scatter" {
			if i == 0 && p.XUnit != "" {
				unit = p.XUnit
			}
			if i == 1 && p.Unit != "" {
				unit = p.Unit
			}
		}
		selects = append(selects, measureSQL(m, sig, seconds, partition)+" AS "+quoteIdent(m.Alias))
		columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: unit})
	}
	text := "WITH base AS (SELECT * FROM " + sig.name + " WHERE " + where + ")"
	if p.Viz == "state_timeline" {
		text += fmt.Sprintf(", items AS (SELECT %s AS item FROM base GROUP BY 1 ORDER BY count(*) DESC,item LIMIT %d)", itemSQL, p.Top())
	}
	text += " SELECT " + strings.Join(selects, ",") + " FROM base"
	if p.Viz == "state_timeline" {
		text += " WHERE " + itemSQL + " IN (SELECT item FROM items)"
	}
	text += " GROUP BY " + strings.Join(groups, ",")
	n := distributionRows(p, len(columns))
	if p.Viz == "scatter" {
		n = min(n, 1000)
	}
	if p.Viz == "state_timeline" {
		text += fmt.Sprintf(" ORDER BY time DESC,2 LIMIT %d", n+1)
	} else {
		name, direction := measures[0].Alias, "DESC"
		if p.Query.Sort != "" {
			name = strings.TrimPrefix(p.Query.Sort, "+")
			if strings.HasPrefix(p.Query.Sort, "+") {
				direction = "ASC"
			}
		}
		text += fmt.Sprintf(" ORDER BY %s %s,1 LIMIT %d", quoteIdent(name), direction, n+1)
	}
	return Compiled{SQL: text, Args: args, Columns: columns}, nil
}
func validateItems(p *Panel, path string, problems *Problems) {
	if p.Options != nil {
		for name, value := range map[string]string{"x_scale": p.Options.XScale, "y_scale": p.Options.YScale} {
			if value != "" && (p.Viz != "scatter" || (value != "linear" && value != "log")) {
				problems.add(path+".options."+name, "axis scales apply only to scatter and must be linear or log")
			}
		}
	}
	if p.Viz != "scatter" && p.Viz != "state_timeline" {
		return
	}
	if p.Query == nil {
		problems.add(path+".query", "item panels require structured queries")
		return
	}
	if p.Viz == "scatter" && len(p.Query.Measures) != 2 {
		problems.addHint(path+".query.measures", "scatter requires exactly two measures", "select x and y measures in that order")
	}
	if p.Viz == "state_timeline" {
		if p.Query.Sort != "" {
			problems.add(path+".query.sort", "state_timeline has fixed chronological ordering")
		}
		if len(p.Query.Measures) != 1 {
			problems.add(path+".query.measures", "state_timeline requires one measure")
		}
		if len(p.Thresholds) == 0 {
			problems.addHint(path+".thresholds", "state_timeline requires thresholds", "set a warn or bad boundary for the selected measure")
		}
	}
	if p.Options != nil && p.Options.Style != "" {
		problems.add(path+".options.style", "item panels cannot stack or set line styles")
	}
}
```

Append these fields to `Options`:

```go
XScale string `json:"x_scale,omitempty" jsonschema:"scatter x axis: linear or log"`
YScale string `json:"y_scale,omitempty" jsonschema:"scatter y axis: linear or log"`
```

Add to `vizSpecs`, append names to `vizOrder`, and update `Panel.Viz` schema description:

```go
"scatter":        {width: 6, height: "m", query: true, minBy: 1, maxBy: 2},
"state_timeline": {width: 6, height: "m", query: true, minBy: 1, maxBy: 1, bucket: true},
```

Call `validateItems(p, path, problems)` in `validatePanel` immediately before `if p.Query != nil { validateQuery(...) }`. Keep the non-additive stacking check. Change the one-unit-family condition in validateQuery (currently validate.go:443) to `len(families)>1 && p.Viz!="table" && p.Viz!="scatter"`. Add `Panel.XUnit` to spec.go and validate it in validateItems before the visualization-specific early return:

```go
// Panel field:
XUnit string `json:"x_unit,omitempty" jsonschema:"scatter x axis unit; unit describes y"`
```

```go
if p.XUnit != "" {
	if p.Viz != "scatter" {
		problems.addHint(path+".x_unit", "x_unit applies only to scatter", "remove x_unit or use scatter")
	}
	if _, ok := unitFamilies[p.XUnit]; !ok {
		problems.addHint(path+".x_unit", "unknown x unit", "use one of "+strings.Join(unitNames(), ", "))
	}
}

```

Immediately after the distribution branch at the start of `compileQuery`, insert:

```go
if p.Viz == "scatter" || p.Viz == "state_timeline" {
	return compileItems(p, measures, filters, scope)
}

```

Extend the special row-limit condition in `runScope` to include `scatter` and `state_timeline`; for scatter use `min(distributionRows(p, len(compiled.Columns)), 1000)`, otherwise `distributionRows`. Keep `limitBatchFrames` as the final shared budget.

- [ ] **Step 4: Run to see pass**

```bash
rtk just test ./internal/panel/... -run '"TestM2Items|TestM2Timeline|TestM2Distribution"'
rtk just test ./internal/panel/...
```

Expected: valid independent scatter units; required thresholds; no merged Other item; bounded 24-hour timeline.

Regenerate the changed panel/tool contract references, then format all Go changes before the controller commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

- [ ] **Step 5: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel/items.go internal/panel/items_test.go internal/panel/spec.go internal/panel/validate.go internal/panel/compile.go internal/panel/exec.go
rtk git commit -m "feat(panels): add item scatter and threshold state timelines"
```

---

### Task 5: Guarded log streams, patterns and trace lists

**Files:**
- Create: `internal/panel/rowpanels.go`, `internal/panel/rowpanels_test.go`
- Modify: `internal/panel/spec.go`, `internal/panel/validate.go`, `internal/panel/compile.go`, `internal/panel/exec.go`, `internal/panel/exemplars.go`, `internal/observability/redact.go`

**Interfaces:**
- Consumes existing: `observability.redactLogBodySQL(column string) string`, exposed by the wrapper below; `telemetry.Log{Namespace, Body, BodyTemplate, ServiceName, TraceID string; EventUnixNanos, TimeUnixNanos, IngestedAt int64; Severity string; SeverityNumber int32}`; checked `Filter` and `Scope`.
- Produces: `observability.RedactLogBodySQL(column string) string`; `compileRows(p *Panel, filters []Filter, scope Scope) (Compiled, error)`; `validateRows(p *Panel, path string, problems *Problems)`; Options `Highlight string json:"highlight,omitempty"` (literal redacted-body search, ≤ 200 characters).
- Logs frame: `time`, `severity`, `service`, `body`, `trace_id`, `namespace`; traces frame: `trace_id`, `namespace`, `service`, `operation`, `duration_ms`, `status`, `start`; patterns frame: `body_template`, `count`, `trend` (JSON string, role dimension). These fixed rows consume zero measures; patterns consume exactly `count()`. Logs/traces accept no `by` or `bucket`. Query `sort` is fixed per row type, never client SQL.
- All rows cap at `min(query.limit or 1000, 1000)` with a sentinel for `Frame.Truncated`; patterns default to 20, honour query.limit up to a hard cap of 50, and have a 240-point dense trend. No UI code until Task 8.

- [ ] **Step 1: Write failing native-engine tests**

Create `internal/panel/rowpanels_test.go`:

```go
package panel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestM2RowsExecuteAndRedact(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart.UnixNano()
	logs := []telemetry.Log{
		{Namespace: "shop", ServiceName: "checkout", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at, Body: "payment token=hidden failed", BodyTemplate: "payment token=hidden failed", Severity: "ERROR", SeverityNumber: 17, TraceID: "t"},
		{Namespace: "shop", ServiceName: "checkout", TimeUnixNanos: at + int64(time.Minute), EventUnixNanos: at + int64(time.Minute), IngestedAt: at, Body: "payment token=hidden failed", BodyTemplate: "payment token=hidden failed", Severity: "ERROR", SeverityNumber: 17, TraceID: "t"},
	}
	commit(t, repo, shopSpans(), logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Rows", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "logs", Title: "Errors", Viz: "logs", Options: &Options{Highlight: "failed"}, Query: &Query{From: "logs", Where: []string{"service = 'checkout'"}}},
		{ID: "patterns", Title: "Patterns", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: "auto"}},
		{ID: "traces", Title: "Slow traces", Viz: "traces", Query: &Query{From: "spans", Where: []string{"service = 'checkout'"}, Sort: "duration_ms", Limit: 20}},
	}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Status != "ok" {
			t.Fatalf("row panel: %+v", r)
		}
		raw, _ := json.Marshal(r.Frame)
		if strings.Contains(string(raw), "hidden") {
			t.Fatal("secret in rows")
		}
	}
	if got[0].Frame.Rows != 2 || got[1].Frame.Rows != 1 || got[1].Frame.Values[1][0].(float64) != 2 || got[2].Frame.Rows != 20 {
		t.Fatalf("frames: %+v", got)
	}
	var trend []float64
	if err := json.Unmarshal([]byte(got[1].Frame.Values[2][0].(string)), &trend); err != nil || len(trend) > 240 {
		t.Fatalf("trend %v %v", trend, err)
	}
	var total float64
	for _, n := range trend {
		total += n
	}
	if total != 2 {
		t.Fatalf("trend count %v", total)
	}
	d.Panels = d.Panels[:1]
	d.Panels[0].Options.Highlight = "hidden"
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("unredacted search: %+v %v", got, err)
	}
}
func TestM2RowsCap(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := make([]telemetry.Log, 1002)
	for i := range logs {
		n := fixtureStart.Add(time.Duration(i) * time.Millisecond).UnixNano()
		logs[i] = telemetry.Log{ServiceName: "s", TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, Body: fmt.Sprint(i)}
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Rows", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "logs", Title: "Logs", Viz: "logs", Query: &Query{From: "logs"}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Frame.Rows != 1000 || !got[0].Frame.Truncated {
		t.Fatalf("cap: %+v", got)
	}
	d.Panels[0].Query.Where = []string{"body IN (SELECT body FROM logs)"}
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: d}); err == nil {
		t.Fatal("row guard bypass")
	}
}
```

Add to rowpanels_test.go:

```go
func TestM2LogPatternsHonoursLimit50(t *testing.T) {
	engine, repo := newTestEngine(t)
	logs := []telemetry.Log{}
	for i := range 60 {
		n := fixtureStart.UnixNano()
		logs = append(logs, telemetry.Log{ServiceName: "s", Body: fmt.Sprintf("pattern-%d", i), BodyTemplate: fmt.Sprintf("pattern-%d", i), EventUnixNanos: n, IngestedAt: n})
	}
	commit(t, repo, nil, logs)
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Patterns", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "log_patterns", Query: &Query{From: "logs", Measures: []string{"count()"}, By: []string{"body_template"}, Bucket: "auto"}}}}
	for _, tc := range []struct{ limit, want int }{{0, 20}, {30, 30}, {50, 50}, {80, 50}} {
		d.Panels[0].Query.Limit = tc.limit
		got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
		if err != nil || got[0].Frame == nil || got[0].Frame.Rows != tc.want {
			t.Fatalf("limit=%d: %+v %v", tc.limit, got, err)
		}
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... -run '"TestM2Rows|TestM2LogPatterns"'
```

Expected: missing Highlight and row viz support.

- [ ] **Step 3: Implement fixed projections and redacted search**

Reuse Task 2’s `redactedLogSource` and `observability.RedactLogBodySQL`; do not redefine either.

Create `internal/panel/rowpanels.go`:

```go
package panel

import (
	"fmt"
	"strings"
	"time"
)

func patternLimit(p *Panel) int {
	if p.Query.Limit > 0 {
		return min(50, p.Query.Limit)
	}
	return min(50, p.Top())
}
func rowLimitForPanel(p *Panel) int {
	if p.Query.Limit > 0 {
		return min(1000, p.Query.Limit)
	}
	return 1000
}
func compileRows(p *Panel, filters []Filter, scope Scope) (Compiled, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	if p.Viz == "traces" {
		order := "duration_ms DESC,trace_id,namespace"
		if p.Query.Sort == "errors" {
			where += " AND status IN ('STATUS_CODE_ERROR','ERROR')"
		}
		if p.Query.Sort == "+start" {
			order = "start ASC,trace_id,namespace"
		}
		text := checkedTraceSQL("spans", where, rowLimitForPanel(p)+1, order, "epoch_ms(start_time::TIMESTAMP_NS)::BIGINT AS start")
		args = append(args, scope.Start.UTC(), scope.End.UTC())
		return Compiled{SQL: text, Args: args, Columns: []Column{{Name: "trace_id", Type: "string", Role: "dimension"}, {Name: "namespace", Type: "string", Role: "dimension"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "operation", Type: "string", Role: "dimension"}, {Name: "duration_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "status", Type: "string", Role: "dimension"}, {Name: "start", Type: "time", Role: "time"}}}, nil
	}
	redacted := redactedLogSource()
	if p.Options != nil && p.Options.Highlight != "" {
		where += " AND contains(lower(body),lower(?))"
		args = append(args, p.Options.Highlight)
	}
	base := "WITH redacted AS (" + redacted + "),base AS (SELECT * FROM redacted WHERE " + where + ")"
	if p.Viz == "logs" {
		order := "DESC"
		if p.Query.Sort == "+time" {
			order = "ASC"
		}
		return Compiled{SQL: base + ` SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,coalesce(severity,''),coalesce(service,''),coalesce(body,''),coalesce(trace_id,''),coalesce(namespace,'') FROM base ORDER BY time ` + order + fmt.Sprintf(" LIMIT %d", rowLimitForPanel(p)+1), Args: args, Columns: []Column{{Name: "time", Type: "time", Role: "time"}, {Name: "severity", Type: "string", Role: "dimension"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "body", Type: "string", Role: "dimension"}, {Name: "trace_id", Type: "string", Role: "dimension"}, {Name: "namespace", Type: "string", Role: "dimension"}}}, nil
	}
	interval := max(scope.Interval, AutoInterval(scope.End.Sub(scope.Start), 960))
	seconds := int64(interval / time.Second)
	lo := alignedBucketMillis(scope.Start, interval)
	points := int((scope.End.UnixMilli() - lo + interval.Milliseconds() - 1) / interval.Milliseconds())
	for points > 240 {
		interval *= 2
		seconds = int64(interval / time.Second)
		lo = alignedBucketMillis(scope.Start, interval)
		points = int((scope.End.UnixMilli() - lo + interval.Milliseconds() - 1) / interval.Milliseconds())
	}
	limit := patternLimit(p)
	text := base + fmt.Sprintf(`,
patterns AS (SELECT coalesce(body_template,'') AS pattern,count(*)::DOUBLE AS total FROM base GROUP BY 1 ORDER BY total DESC,pattern LIMIT %d),
buckets AS (SELECT coalesce(body_template,'') AS pattern,epoch_ms(time_bucket(INTERVAL '%d seconds',time::TIMESTAMP_NS,'1970-01-01'::TIMESTAMP_NS))::BIGINT AS point,count(*)::DOUBLE AS n FROM base GROUP BY 1,2),
dense AS (SELECT p.pattern,p.total,%d+r.i*%d AS point FROM patterns p CROSS JOIN range(%d) r(i))
SELECT d.pattern,d.total,CAST(to_json(list(coalesce(b.n,0) ORDER BY d.point)) AS VARCHAR) AS trend
FROM dense d LEFT JOIN buckets b ON d.pattern=b.pattern AND d.point=b.point
GROUP BY d.pattern,d.total ORDER BY d.total DESC,d.pattern`, limit+1, seconds, lo, interval.Milliseconds(), points)
	return Compiled{SQL: text, Args: args, Columns: []Column{{Name: "body_template", Type: "string", Role: "dimension"}, {Name: "count", Type: "number", Role: "measure", Unit: "count"}, {Name: "trend", Type: "json", Role: "dimension", Unit: "count"}}}, nil
}
func alignedBucketMillis(t time.Time, interval time.Duration) int64 {
	n, width := t.UnixMilli(), interval.Milliseconds()
	q := n / width
	if n%width < 0 {
		q--
	}
	return q * width
}

func validateRows(p *Panel, path string, problems *Problems) {
	isRow := p.Viz == "logs" || p.Viz == "traces" || p.Viz == "log_patterns"
	if p.Options != nil && p.Options.Highlight != "" && (!isRow || p.Viz == "traces" || len(p.Options.Highlight) > 200) {
		problems.add(path+".options.highlight", "highlight is a log search of at most 200 characters")
	}
	if !isRow {
		return
	}
	if p.Query == nil {
		problems.add(path+".query", "row panels require a structured query")
		return
	}
	q := p.Query
	if p.Viz == "traces" {
		if q.From != "spans" {
			problems.add(path+".query.from", "traces reads spans")
		}
		if q.Sort != "" && q.Sort != "duration_ms" && q.Sort != "errors" && q.Sort != "+start" {
			problems.add(path+".query.sort", "traces sort is duration_ms, errors or +start")
		}
	} else {
		if q.From != "logs" {
			problems.add(path+".query.from", "logs and log_patterns read logs")
		}
	}
	if p.Viz == "logs" && q.Sort != "" && q.Sort != "time" && q.Sort != "+time" {
		problems.add(path+".query.sort", "logs sort is time or +time")
	}
	if p.Viz == "log_patterns" && (len(q.By) != 1 || q.By[0] != "body_template" || len(q.Measures) != 1 || q.Measures[0] != "count()" || q.Sort != "" && q.Sort != "count") {
		problems.add(path+".query", "log_patterns requires count(), by body_template, and count sort")
	}
	if (p.Viz == "logs" || p.Viz == "traces") && len(q.Measures) > 0 {
		problems.add(path+".query.measures", "fixed row projections do not take aggregate measures")
	}
	if p.Options != nil && strings.TrimSpace(p.Options.Style) != "" {
		problems.add(path+".options.style", "row panels cannot set chart styles")
	}
}
```

In `spec.go`, change `Query.Measures` JSON tag to `json:"measures,omitempty"` and describe fixed rows as taking no measures. Add `Options.Highlight`:

```go
Highlight string `json:"highlight,omitempty" jsonschema:"logs and log_patterns: literal redacted-body search, at most 200 characters"`
```

Add to `vizSpecs`, append to `vizOrder`, and update `Panel.Viz` description:

```go
"logs":         {width: 12, height: "l", query: true},
"log_patterns": {width: 12, height: "m", query: true, minBy: 1, maxBy: 1, bucket: true},
"traces":       {width: 12, height: "m", query: true},
```

Call `validateRows(p, path, problems)` beside `validateItems`. Change the measure-count condition in `validateQuery` to:

```go
if (len(q.Measures) == 0 && p.Viz != "logs" && p.Viz != "traces") || len(q.Measures) > 6 {
	problems.add(path+".measures", "a query has 1 to 6 measures; fixed logs and traces take none")
}

```

Skip the generic measure-alias sort check for logs/traces (their validator above checks fixed sort names): change its condition to `q.Sort != "" && p.Viz != "logs" && p.Viz != "traces"`.

At the start of `compileQuery`, insert:

```go
if p.Viz == "logs" || p.Viz == "log_patterns" || p.Viz == "traces" {
	return compileRows(p, filters, scope)
}

```

Exemplars already use the redacted source from Task 2, so log filter and body_template selection semantics remain identical to row panels.

Before scanning in `runScope`, set rowLimit for row panels explicitly:

```go
if p.Viz == "logs" || p.Viz == "traces" {
	rowLimit = rowLimitForPanel(p)
}
if p.Viz == "log_patterns" {
	rowLimit = patternLimit(p)
}

```


- [ ] **Step 4: Run to see pass**

```bash
rtk just test ./internal/panel/... ./internal/observability/... -run '"TestM2Rows|TestM2LogPatterns|TestRedact"'
rtk just test ./internal/panel/... ./internal/observability/...
```

Expected: redacted text is both displayed and searched, patterns conserve counts, all fixed rows are capped, and guarded filters still reject subqueries.

Regenerate the changed panel/tool contract references, then format all Go changes before the controller commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

- [ ] **Step 5: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel/rowpanels.go internal/panel/rowpanels_test.go internal/panel/spec.go internal/panel/validate.go internal/panel/compile.go internal/panel/exec.go internal/panel/exemplars.go internal/observability/redact.go
rtk git commit -m "feat(panels): add guarded log, pattern and trace row frames"
```

---

### Task 6: Reuse topology and overview on panel frames

**Files:**
- Create: `internal/panel/rollup_panels.go`, `internal/panel/rollup_panels_test.go`
- Modify: `internal/panel/exec.go`, `internal/panel/check.go`, `internal/panel/validate.go`, `internal/panel/spec.go`, `internal/panel/exemplars.go`, `cmd/fanout/main.go`
- Modify existing shared service: `internal/observability/contracts.go`, `internal/observability/topology.go`, `internal/observability/overview.go`; reuse `internal/observability/service.go` unchanged

**Interfaces:**
- Consumes existing: `(*observability.Service).Topology(ctx context.Context, scope observability.Scope, limit int) (observability.Result[observability.Topology], error)`; `(*observability.Service).Overview(ctx context.Context, scope observability.Scope, limit int) (observability.Result[observability.Overview], error)`; `observability.New(db observability.DB, repository traceReader, retentionDays int) *observability.Service`; `Filter.Source`, `Filter.EqField`; `constantText(n map[string]any) string`; `fieldText(n any) string`.
- Produces `observability.Scope.Service string` (optional internal scope), and `type RollupReader interface` with those exact methods; `(*Executor).SetRollupReader(reader RollupReader)`; `(*Executor).runRollupPanel(ctx context.Context, p *Panel, filters []Filter, scope Scope) (*Frame, string, error)`; `rollupFilterValue(ctx context.Context, parser Parser, f Filter, scope Scope) (string, string, error)`.
- Health frame: service, health, spans, error_rate (percent), p50_ms, p95_ms, log_count, metric_count. Service-map frame: kind, service, caller, callee, edge_type, calls (edges only), average_ms (edges only), error_rate (percent), health, p95_ms (nodes only), spans (nodes only). Nodes and edges use the same frame with `kind=node|edge`. No SQLite, raw-query or public SQL allowlist additions.
- `service_map` and `health` require `query.from=spans`, no measures, no grouping/time buckets, only equality namespace/service filters. Limits are at most 400 per observability read. Extra filter semantics are rejected, never silently discarded.

- [ ] **Step 1: Write failing engine and guard tests**

Create `internal/panel/rollup_panels_test.go`:

```go
package panel

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/observability"
)

func TestM2RollupPanelsReuseServices(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO service_rollup(namespace,bucket,service,spans,served_spans,p50_ms,p95_ms,error_rate,log_count,metric_count) VALUES ('shop',?,'checkout',10,10,50,100,.1,2,3),('shop',?,'payment',20,20,20,40,0,1,1)`, at, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO edge_rollup VALUES ('shop',?,'checkout','payment',10,20,.1,'call')`, at)
	if err != nil {
		t.Fatal(err)
	}
	reader := observability.New(engine, engine, 30)
	e := NewExecutor(engine, 30)
	e.SetRollupReader(reader)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Overview", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "health", Title: "Health", Viz: "health", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'"}}},
		{ID: "map", Title: "Map", Viz: "service_map", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'"}}},
	}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := reader.Overview(t.Context(), observability.Scope{Namespace: "shop", Start: at, End: at.Add(time.Hour)}, 400)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != "ok" || got[0].Frame.Rows != len(overview.Data.Services) || got[0].Frame.Values[3][0].(float64) != 10 {
		t.Fatalf("overview frame: %+v", got[0])
	}
	if got[1].Status != "ok" || got[1].Frame.Rows != 3 || got[1].Frame.Values[7][2].(float64) != 10 {
		t.Fatalf("edges: %+v", got[1])
	}
	d.Panels[0].Query.Where = []string{"namespace = 'missing'"}
	d.Panels = d.Panels[:1]
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "empty" {
		t.Fatalf("empty health: %+v %v", got, err)
	}
}
func TestM2RollupPanelsApplyServiceBeforeLimit(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO service_rollup SELECT 'shop',?,'noisy-'||i,100,100,3000,4000,.5,0,0 FROM generate_series(1,500) AS t(i)`, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'quiet',1,1,1,2,0,0,0)`, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO edge_rollup SELECT 'shop',?,'noisy-'||i,'sink',100,100,.5,'call' FROM generate_series(1,500) AS t(i)`, at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.DB.Exec(`INSERT INTO edge_rollup VALUES ('shop',?,'quiet','sink',1,2,0,'call')`, at)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.SetRollupReader(observability.New(engine, engine, 30))
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := Dashboard{Name: "Quiet", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "h", Title: "H", Viz: "health", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'", "service = 'quiet'"}}}, {ID: "m", Title: "M", Viz: "service_map", Query: &Query{From: "spans", Where: []string{"namespace = 'shop'", "service = 'quiet'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Frame.Rows != 1 || got[0].Frame.Values[0][0] != "quiet" || got[1].Frame.Rows != 2 {
		t.Fatalf("post-limit scope loss: %+v", got)
	}
}
func TestM2RollupPanelsRejectUnrepresentableFilters(t *testing.T) {
	e := newFixtureExecutor(t)
	cases := []struct{ where, message string }{
		{"http_route = '/cart'", "rollup panels support equality filters on namespace or service"},
		{"service <> 'checkout'", "rollup panels support equality filters on namespace or service"},
		{"service = operation", "rollup equality needs a literal or variable"},
	}
	for _, tc := range cases {
		d := Dashboard{Name: "Map", Panels: []Panel{{ID: "map", Title: "Map", Viz: "service_map", Query: &Query{From: "spans", Where: []string{tc.where}}}}}
		err := e.Validate(t.Context(), &d)
		var got Problems
		want := Problem{Path: "panels[0].query.where[0]", Message: tc.message, Hint: "use namespace or service equality with a literal or single-value variable"}
		if !errors.As(err, &got) || !slices.Contains(got, want) {
			t.Fatalf("%s: got %v want %+v", tc.where, err, want)
		}
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... -run '"TestM2RollupPanels|TestM2VizOrder"'
```

Expected: missing SetRollupReader and unsupported types.

- [ ] **Step 3: Implement the adapter without copying observability SQL**

Create `internal/panel/rollup_panels.go`:

```go
package panel

import (
	"context"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/observability"
)

type RollupReader interface {
	Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error)
	Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error)
	Performance(context.Context, observability.Scope, observability.PerformanceOptions) (observability.Result[observability.Performance], error)
}

func (e *Executor) SetRollupReader(reader RollupReader) { e.rollups = reader }
func unwrapFilter(n any) map[string]any {
	v, _ := n.(map[string]any)
	for v != nil && v["class"] == "CAST" {
		v, _ = v["child"].(map[string]any)
	}
	return v
}
func rollupFilterValue(ctx context.Context, parser Parser, f Filter, scope Scope) (string, string, error) {
	tree, err := parser.ParseSQL(ctx, "SELECT 1 FROM spans WHERE ("+f.Source+")")
	if err != nil {
		return "", "", err
	}
	node := unwrapFilter(tree["where_clause"])
	if node == nil || node["class"] != "COMPARISON" || node["type"] != "COMPARE_EQUAL" {
		return "", "", fmt.Errorf("rollup panels support equality filters on namespace or service")
	}
	left, right := unwrapFilter(node["left"]), unwrapFilter(node["right"])
	if left["class"] != "COLUMN_REF" {
		left, right = right, left
	}
	field := fieldText(left)
	if left["class"] != "COLUMN_REF" || (field != "service" && field != "namespace") {
		return "", "", fmt.Errorf("rollup panels support equality filters on namespace or service")
	}
	switch right["class"] {
	case "CONSTANT":
		value := constantText(right)
		if value == "" {
			return "", "", fmt.Errorf("scope equality requires a nonempty string")
		}
		return field, value, nil
	case "PARAMETER":
		name, _ := right["identifier"].(string)
		v, ok := scope.Vars[name]
		if !ok || v.All || len(v.Values) != 1 || v.Values[0] == "" {
			return "", "", fmt.Errorf("rollup equality needs one selected value")
		}
		return field, v.Values[0], nil
	default:
		return "", "", fmt.Errorf("rollup equality needs a literal or variable")
	}
}
func (e *Executor) runRollupPanel(ctx context.Context, p *Panel, filters []Filter, scope Scope) (*Frame, string, error) {
	if e.rollups == nil {
		return nil, "", fmt.Errorf("rollup reader unavailable")
	}
	request := observability.Scope{Start: scope.Start, End: scope.End}
	seen := map[string]string{}
	for _, f := range filters {
		if scope.dropped(f) {
			continue
		}
		field, value, err := rollupFilterValue(ctx, e.engine, f, scope)
		if err != nil {
			return nil, "", err
		}
		if prior, ok := seen[field]; ok && prior != value {
			columns := healthColumns()
			if p.Viz == "service_map" {
				columns = mapColumns()
			}
			return newFrame(columns), "observability: conflicting scope filters", nil
		}
		seen[field] = value
		if field == "namespace" {
			request.Namespace = value
		} else {
			request.Service = value
		}
	}
	limit := 400
	if p.Query.Limit > 0 {
		limit = min(400, p.Query.Limit)
	}
	if p.Viz == "health" {
		result, err := e.rollups.Overview(ctx, request, limit)
		if err != nil {
			return nil, "", err
		}
		f := newFrame(healthColumns())
		f.Truncated = len(result.Data.Services) == limit
		performance, err := e.rollups.Performance(ctx, request, observability.PerformanceOptions{Service: request.Service, Limit: 1})
		if err != nil {
			return nil, "", err
		}
		f.Health = &HealthFrame{Health: string(result.Data.Health), Counts: result.Data.Counts, TotalSpans: result.Data.TotalSpans, ErrorRate: result.Data.ErrorRate * 100, ServiceCount: result.Data.ServiceCount, ErrorTrend: []float64{}}
		for _, point := range performance.Data.Points {
			f.Health.ErrorTrend = append(f.Health.ErrorTrend, point.ErrorRate*100)
		}
		for _, s := range result.Data.Services {
			appendPanelRow(f, s.Service, string(s.Health), float64(s.Spans), s.ErrorRate*100, s.P50MS, s.P95MS, float64(s.LogCount), float64(s.MetricCount))
		}
		return f, "observability.Service.Overview", nil
	}
	result, err := e.rollups.Topology(ctx, request, limit)
	if err != nil {
		return nil, "", err
	}
	f := newFrame(mapColumns())
	f.Truncated = len(result.Data.Nodes) == limit || len(result.Data.Edges) == limit
	for _, n := range result.Data.Nodes {
		appendPanelRow(f, "node", n.Service, "", "", "", nil, nil, n.ErrorRate*100, string(n.Health), n.P95MS, float64(n.Spans))
	}
	for _, edge := range result.Data.Edges {
		appendPanelRow(f, "edge", "", edge.Caller, edge.Callee, edge.Type, float64(edge.Calls), edge.AverageMS, edge.ErrorRate*100, "", nil, nil)
	}
	return f, "observability.Service.Topology", nil
}
func mapColumns() []Column {
	return []Column{{Name: "kind", Type: "string", Role: "dimension"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "caller", Type: "string", Role: "dimension"}, {Name: "callee", Type: "string", Role: "dimension"}, {Name: "edge_type", Type: "string", Role: "dimension"}, {Name: "calls", Type: "number", Role: "measure", Unit: "count"}, {Name: "average_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "error_rate", Type: "number", Role: "measure", Unit: "percent"}, {Name: "health", Type: "string", Role: "dimension"}, {Name: "p95_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "spans", Type: "number", Role: "measure", Unit: "count"}}
}
func healthColumns() []Column {
	return []Column{{Name: "service", Type: "string", Role: "dimension"}, {Name: "health", Type: "string", Role: "dimension"}, {Name: "spans", Type: "number", Role: "measure", Unit: "count"}, {Name: "error_rate", Type: "number", Role: "measure", Unit: "percent"}, {Name: "p50_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "p95_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "log_count", Type: "number", Role: "measure", Unit: "count"}, {Name: "metric_count", Type: "number", Role: "measure", Unit: "count"}}
}
func appendPanelRow(f *Frame, values ...any) {
	for i, v := range values {
		f.Values[i] = append(f.Values[i], v)
	}
	f.Rows++
}
func validateRollupPanel(p *Panel, path string, problems *Problems) {
	if p.Viz != "health" && p.Viz != "service_map" {
		return
	}
	if p.Query == nil || p.Query.From != "spans" {
		problems.add(path+".query", "health and service_map require a structured spans scope")
		return
	}
	if len(p.Query.Measures) > 0 || len(p.Query.By) > 0 || p.Query.Bucket != "" {
		problems.add(path+".query", "rollup panels use scope filters and a limit, without measures, dimensions or buckets")
	}
	if p.Query.Sort != "" || p.Options != nil && strings.TrimSpace(p.Options.Style) != "" {
		problems.add(path+".options", "rollup panels have fixed health and graph encodings")
	}
}
```

Add `Health *HealthFrame json:"health,omitempty"` to Frame in frame.go and define this type there (import `internal/observability`):

```go
type HealthFrame struct {
	Health       string                     `json:"health"`
	Counts       observability.HealthCounts `json:"counts"`
	TotalSpans   int64                      `json:"total_spans"`
	ErrorRate    float64                    `json:"error_rate"`
	ServiceCount int                        `json:"service_count"`
	ErrorTrend   []float64                  `json:"error_trend"`
}
```

This carries Overview’s exact aggregate metrics and Performance’s error-rate trend to the widget without an extra browser request. The service rows remain available to Inspect. Port the widget from `2c208c85^:ui/host/src/widgets/overview.tsx` and `pieces.tsx`; its health/error-rate metric tiles, operation/service counts, error trend and distribution are required, rather than a row summary/table.

Extend the existing observability Scope in contracts.go with this field (no public HTTP/MCP input changes):

```go
Service string `json:"service,omitempty"`
```

Modify the existing `overviewQuery` WHERE line in overview.go, preserving its aggregations:

```sql
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR service = ?)
```

Replace its QueryContext call with:

```go
rows, err := s.db.QueryContext(ctx, overviewQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Service, scope.Service, limit)

```

Modify the existing `topologyEdgesQuery` WHERE line in topology.go:

```sql
WHERE bucket >= ? AND bucket < ? AND (? = '' OR namespace = ?) AND (? = '' OR caller = ? OR callee = ?)
```

Replace its QueryContext call with:

```go
rows, err := s.db.QueryContext(ctx, topologyEdgesQuery, scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Service, scope.Service, scope.Service, limit)

```

Empty Scope.Service preserves existing callers. This applies the checked service restriction before GROUP BY/LIMIT, including low-volume matching edges. It extends the shared services; panel code contains no copy of their SQL.

Add `rollups RollupReader` to Executor. At the beginning of `runScope`, after applying `queryrows.WithWindow`, insert:

```go
if p.Viz == "health" || p.Viz == "service_map" {
	return e.runRollupPanel(ctx, p, checked.Filters[p.ID], scope)
}

```

In `Check`, after adding each checked filter to `checked.Filters[p.ID]`, insert:

```go
if p.Viz == "health" || p.Viz == "service_map" {
	sample := Scope{Vars: map[string]Value{}}
	for name := range earlier {
		sample.Vars[name] = Value{Values: []string{"check"}}
	}
	if _, _, err := rollupFilterValue(ctx, parser, f, sample); err != nil {
		if isOperational(err) {
			return nil, nil, err
		}
		problems.addHint(fmt.Sprintf("%s.query.where[%d]", path, j), SafeError(err), "use namespace or service equality with a literal or single-value variable")
	}
}

```

Add the two viz specs (`width:12,height:"m",query:true`), append names to vizOrder, and include them in the measure-count exception for fixed rows. Call `validateRollupPanel(p,path,problems)` in validatePanel. They have no measure families and therefore do not loosen the shared-axis rule.

In selectionWhere (exemplars.go, created in Task 2), replace its dimension allowlist condition with the complete condition below so a service node can drill through the same guard:

```go
if (!slices.Contains(p.Query.By, name) && !((p.Viz == "health" || p.Viz == "service_map") && name == "service")) || len(dimensions[name]) > 500 {
	return "", nil, Problems{{Path: "dimensions", Message: "dimensions must name query.by fields and values at most 500 characters", Hint: "select one of the panel grouping fields"}}
}

```

In `cmd/fanout/main.go`, after constructing `panels`, insert:

```go
panels.SetRollupReader(queries)

```

Add `TestM2VizOrder` to `internal/panel/validate_test.go` in Task 6:

```go
func TestM2VizOrder(t *testing.T) {
	if len(vizOrder) != 15 {
		t.Fatalf("vizOrder has %d types, want 15", len(vizOrder))
	}
	seen := map[string]bool{}
	for _, viz := range vizOrder {
		if seen[viz] {
			t.Fatalf("duplicate %s", viz)
		}
		seen[viz] = true
		if _, ok := vizSpecs[viz]; !ok {
			t.Fatalf("unregistered %s", viz)
		}
	}
}
```


- [ ] **Step 4: Run to see pass**

```bash
rtk just test ./internal/panel/... ./internal/observability/... -run '"TestM2RollupPanels|TestM2VizOrder|TestM2Rows|TestM2LogPatterns|TestM2Items"'
rtk just test ./internal/panel/... ./internal/observability/...
```

Expected: frames equal existing service results, empty scope is empty, unsupported filters fail during Check, no duplicated analytical SQL.

Regenerate the changed panel/tool contract references, then format all Go changes before the controller commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

- [ ] **Step 5: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel/rollup_panels.go internal/panel/rollup_panels_test.go internal/panel/exec.go internal/panel/check.go internal/panel/validate.go internal/panel/spec.go internal/panel/frame.go internal/panel/validate_test.go internal/panel/exemplars.go cmd/fanout/main.go internal/observability/contracts.go internal/observability/overview.go internal/observability/topology.go
rtk git commit -m "feat(panels): reuse observability topology and health services"
```

---

### Task 7: Chart visualizations: heatmap, histogram, scatter, state timeline, service map and health

**Files:**
- Create: `ui/panels/analysis.ts`, `ui/panels/rows.ts`
- Create: `ui/host/src/dashboards/viz/analysis-chart.tsx`, `ui/host/src/dashboards/viz/heatmap.tsx`, `histogram.tsx`, `scatter.tsx`, `state-timeline.tsx`, `service-map.tsx`, `health.tsx` (all six chart files under that viz directory)
- Create: `ui/host/src/dashboards/new-viz.test.tsx`
- Modify: `ui/panels/types.ts`, `ui/host/src/dashboards/viz/index.tsx`, `ui/host/src/dashboards/echart-canvas.tsx`, `ui/host/src/dashboards/echart-canvas.test.tsx`, `ui/host/src/dashboards/viz/table.tsx`, `ui/host/src/dashboards/panel-card.tsx`
- Generated: `internal/ui/dist/**`, `internal/mcp/apps/**` through `just ui`

**Interfaces:**
- Consumes existing: `chartThemeFor(dark: boolean): ChartTheme`, `EChartCanvas({option,height,label,onClick,group})`, `statusFor(value: number | null, thresholds?: Threshold[], better?: "lower" | "higher"): Status | null`, `formatValue(unit: string | undefined, value: number | null): string`, `PanelCard` and `InspectDrawer`.
- Produces pure TS: `analysisOption(panel: Panel, result: PanelResult, theme: ChartTheme): Record<string, unknown>`; `frameRows(frame: Frame): Record<string, Cell>[]`; `rowModel(panel: Panel, result: PanelResult): RowModel`; `analysisSummary(panel: Panel, result: PanelResult): string`; `Selection = {time?:number; dimensions:Record<string,string>; trace_id?:string; namespace?:string; bucket?:{lower:number;upper?:number}}`.
- Produces React: `AnalysisChart(props: AnalysisProps)`, six chart Viz exports accepting `AnalysisProps = {panel:Panel;title?:string;result:PanelResult;dark:boolean;height:number;group?:string;onSelect?:(value:string)=>void;onPoint?:(selection:Selection)=>void;onZoom?:(from:number,to:number)=>void}`. `RowPanel` uses the existing TableViz for TanStack sorting and a cell renderer hook introduced here.
- TS mirrors: fifteen-value `Viz`, Query optional measures and histogram, Options `x_scale`, `y_scale`, `highlight` and `columns` with exported `ColumnFormat`; PanelResult `from_ms`/`to_ms`. Frames retain their M1 types. Inspect is unchanged and remains reachable for every type through PanelCard.

- [ ] **Step 1: Write failing compile and chrome tests**

Create `ui/host/src/dashboards/new-viz.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { analysisOption, analysisSummary } from "../../../panels/analysis";
import { chartThemeFor } from "../../../panels/compile";
import {visualizations,type Panel,type PanelResult,type Viz as VizType} from "../../../panels/types";
import { PanelCard } from "./panel-card";
vi.mock("./echart-canvas",()=>({EChartCanvas:({label}:{label:string})=><div role="img" aria-label={label}/> }));
const types:VizType[]=["heatmap","histogram","scatter","state_timeline","service_map","health"];
const col=(name:string,type:"time"|"number"|"string"|"json",role:"time"|"dimension"|"measure",unit?:string)=>({name,type,role,unit});
const fixtures:Record<string,PanelResult["frame"]>={
 heatmap:{columns:[col("time","time","time"),col("bucket_lower","number","dimension","ms"),col("bucket_upper","number","dimension","ms"),col("count","number","measure","count")],values:[[1000],[1],[2],[3]],rows:1},
 histogram:{columns:[col("bucket_lower","number","dimension","ms"),col("bucket_upper","number","dimension","ms"),col("count","number","measure","count")],values:[[1],[2],[3]],rows:1},
 scatter:{columns:[col("service","string","dimension"),col("count","number","measure","count"),col("p95","number","measure","ms")],values:[["checkout"],[2],[50]],rows:1},
 state_timeline:{columns:[col("time","time","time"),col("service","string","dimension"),col("p95","number","measure","ms")],values:[[1000],["checkout"],[50]],rows:1},
 logs:{columns:[col("time","time","time"),col("severity","string","dimension"),col("service","string","dimension"),col("body","string","dimension"),col("trace_id","string","dimension"),col("namespace","string","dimension")],values:[[1000],["ERROR"],["checkout"],["failed"],["abc"],["shop"]],rows:1},
 log_patterns:{columns:[col("body_template","string","dimension"),col("count","number","measure","count"),col("trend","json","dimension","count")],values:[["failed <*>"],[2],["[1,1]"]],rows:1},
 traces:{columns:[col("trace_id","string","dimension"),col("namespace","string","dimension"),col("service","string","dimension"),col("operation","string","dimension"),col("duration_ms","number","measure","ms"),col("status","string","dimension"),col("start","time","time")],values:[["abc"],["shop"],["checkout"],["cart"],[50],["STATUS_CODE_ERROR"],[1000]],rows:1},
 service_map:{columns:[col("kind","string","dimension"),col("service","string","dimension"),col("caller","string","dimension"),col("callee","string","dimension"),col("edge_type","string","dimension"),col("calls","number","measure","count"),col("average_ms","number","measure","ms"),col("error_rate","number","measure","percent"),col("health","string","dimension"),col("p95_ms","number","measure","ms"),col("spans","number","measure","count")],values:[["node","edge"],["checkout",""],["","checkout"],["","payment"],["","call"],[null,3],[null,20],[10,1],["unhealthy",""],[50,null],[2,null]],rows:2},
 health:{columns:[col("service","string","dimension"),col("health","string","dimension"),col("spans","number","measure","count"),col("error_rate","number","measure","percent"),col("p50_ms","number","measure","ms"),col("p95_ms","number","measure","ms"),col("log_count","number","measure","count"),col("metric_count","number","measure","count")],values:[["checkout"],["unhealthy"],[2],[10],[20],[50],[1],[1]],rows:1,health:{health:"unhealthy",counts:{healthy:0,degraded:0,unhealthy:1},service_count:1,total_spans:2,error_rate:10,error_trend:[1,10]}},
};
const resultFor=(viz:string):PanelResult=>({id:"p",status:"ok",elapsed_ms:1,interval:"1m",from_ms:0,to_ms:10000,frame:fixtures[viz]});
const assertFinite=(value:unknown):void=>{if(typeof value==="number")expect(Number.isFinite(value)).toBe(true);else if(Array.isArray(value))value.forEach(assertFinite);else if(value&&typeof value==="object")Object.values(value).forEach(assertFinite);};

describe("M2 visualizations",()=>{
  it("has exactly fifteen registry entries",()=>{
    expect(visualizations).toHaveLength(15);expect(new Set(visualizations).size).toBe(15);
  });
  it("compiles empty chart frames in both themes without nonfinite values",()=>{
    for(const dark of [false,true])for(const viz of types){
      const panel:Panel={id:"p",title:viz,viz,thresholds:[{value:1,status:"bad"}]};
      const result=resultFor(viz);const empty={...result,frame:{...result.frame!,values:result.frame!.columns.map(()=>[]),rows:0,health:undefined}} as PanelResult;
      assertFinite(analysisOption(panel,empty,chartThemeFor(dark)));
      const nonfinite={...result,frame:{...result.frame!,values:result.frame!.values.map(values=>values.map(v=>typeof v==="number"?Infinity:v))}};assertFinite(analysisOption(panel,nonfinite,chartThemeFor(dark)));
      expect(analysisSummary(panel,empty)).toContain("0");
    }
  });
  it("renders loading, empty, error and partial states for all types",async()=>{
    const container=document.createElement("div");document.body.append(container);const root=createRoot(container);
    const noop=()=>undefined;
    for(const viz of types){
      const result=resultFor(viz);
      const panel:Panel={id:"p",title:viz,viz};
      const props={panel,title:viz,height:300,group:"g",editing:false,agentAvailable:false,onView:noop,onInspect:noop,onCopyLink:noop,onExplain:noop};
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading result={undefined}/></MantineProvider>));
      expect(container.querySelector('[aria-label="Loading panel"]')).not.toBeNull();
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={{...result,status:"empty",diagnosis:"No matching events"}}/></MantineProvider>));
      expect(container.textContent).toContain("No matching events");
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={{...result,status:"error",error:"Query failed"}}/></MantineProvider>));
      expect(container.textContent).toContain("Query failed");
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={{...result,frame:{...result.frame!,truncated:true}}}/></MantineProvider>));
      expect(container.textContent).toContain("Truncated");
      const zero={...result,frame:{...result.frame!,values:result.frame!.columns.map(()=>[]),rows:0,health:undefined}} as PanelResult;
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={zero}/></MantineProvider>));
      expect(container.textContent).not.toMatch(/NaN|Infinity/);
      const nonfinite={...result,frame:{...result.frame!,values:result.frame!.values.map(values=>values.map(value=>typeof value==="number"?Infinity:value)),health:result.frame!.health?{...result.frame!.health,error_rate:Infinity,total_spans:Infinity,error_trend:[Infinity,1]}:undefined}};
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={nonfinite}/></MantineProvider>));
      expect(container.textContent).not.toMatch(/NaN|Infinity/);
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={result}/></MantineProvider>));
      expect(container.textContent).not.toMatch(/NaN|Infinity/);
    }
    await act(async()=>root.unmount());container.remove();
  });
});
```

- [ ] **Step 2: Run to see failure**

```bash
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/new-viz.test.tsx'
```

Expected: missing compilers and new type union members.

- [ ] **Step 3: Mirror the Go contract and compile options**

In `ui/panels/types.ts`, replace Viz with:

```ts
export const visualizations = ["stat","gauge","timeseries","bar","table","text","heatmap","histogram","scatter","state_timeline","logs","log_patterns","traces","service_map","health"] as const;
export type Viz = typeof visualizations[number];
export type Selection = { time?: number; dimensions: Record<string,string>; trace_id?: string; namespace?: string; bucket?: {lower:number;upper?:number} };
export type ColumnFormat = {field:string;format:"unit"|"bar"|"status"|"sparkline"|"trace_link"|"service_link"|"log_template";unit?:Unit;variable?:string};
```

Replace Query with:

```ts
export type Query = { from: "spans"|"logs"|"metrics"; where?: string[]; measures?: string[]; by?: string[]; bucket?: string; histogram?: {field:string;buckets:"log2"|"explicit";temporality?:"cumulative"|"delta"}; sort?: string; limit?: number };
```

Add `x_unit?:Unit` beside Panel.unit, and `health?:{health:string;counts:{healthy:number;degraded:number;unhealthy:number};total_spans:number;error_rate:number;service_count:number;error_trend:number[]}` to Frame. Add `from_ms?:number; to_ms?:number` to PanelResult. Extend the existing inline Panel.options type with `x_scale?: "linear"|"log"; y_scale?: "linear"|"log"; highlight?: string; columns?:ColumnFormat[]`. Where existing pure compilers access `query.measures`, use `query.measures ?? []` (the fixed-row query may omit it).

Create `ui/panels/rows.ts`:

```ts
import type { Cell, Frame, Panel, PanelResult, Selection } from "./types";
export type RowModel = { columns: string[]; rows: Record<string,Cell>[]; selection(row:Record<string,Cell>):Selection };
export function frameRows(frame:Frame):Record<string,Cell>[] {
  return Array.from({length:frame.rows},(_,r)=>Object.fromEntries(frame.columns.map((c,i)=>[c.name,typeof frame.values[i]?.[r]==="number"&&!Number.isFinite(frame.values[i][r])?null:frame.values[i]?.[r]??null])));
}
export function rowModel(panel:Panel,result:PanelResult):RowModel {
  const frame=result.frame??{columns:[],values:[],rows:0};
  return {columns:frame.columns.map(c=>c.name),rows:frameRows(frame),selection:(row)=>{
    const dimensions:Record<string,string>={};
    for(const by of panel.query?.by??[]) {const column=by.startsWith("attributes[")||by.startsWith("resource[")?by.slice(by.indexOf("'")+1,by.lastIndexOf("'")):by;if(row[column]!==undefined&&row[column]!==null)dimensions[by]=String(row[column]);}
    return {time:typeof row.time==="number"?row.time:typeof row.start==="number"?row.start:undefined,dimensions,trace_id:typeof row.trace_id==="string"?row.trace_id:undefined,namespace:typeof row.namespace==="string"?row.namespace:undefined};
  }};
}
```

Create `ui/panels/analysis.ts`:

```ts
import { seriesColor, healthSymbol, healthBorderType, healthSymbolScale } from "../chart";
import { formatAxis } from "./units";
import type { ChartTheme } from "./compile";
import { frameRows } from "./rows";
import { statusFor } from "./thresholds";
import type { Panel, PanelResult } from "./types";
const spanMs=(s?:string)=>{const m=/^(\d+)(s|m|h|d)$/.exec(s??"");return m?Number(m[1])*({s:1000,m:60000,h:3600000,d:86400000}[m[2] as "s"|"m"|"h"|"d"]):60000;};
export function analysisSummary(panel:Panel,result:PanelResult):string{return `${panel.title}: ${result.frame?.rows??0} rows; ${(result.frame?.columns??[]).map(c=>c.name+(c.unit?` (${c.unit})`:"")).join(", ")}${result.frame?.truncated?", limited data":""}`;}
export function analysisOption(panel:Panel,result:PanelResult,theme:ChartTheme):Record<string,unknown>{
  const frame=result.frame??{columns:[],values:[],rows:0};const rows=frameRows(frame);
  const dims=frame.columns.filter(c=>c.role==="dimension").map(c=>c.name);const measures=frame.columns.filter(c=>c.role==="measure");
  const base={animation:false,aria:{enabled:true,description:analysisSummary(panel,result)},textStyle:{fontFamily:theme.font,color:theme.text},grid:{left:70,right:24,top:30,bottom:40,containLabel:true},tooltip:{trigger:"item",backgroundColor:theme.surface,borderColor:theme.border,textStyle:{color:theme.text},renderMode:"richText"},legend:{show:panel.options?.legend!=="hidden",textStyle:{color:theme.muted}},xAxis:{type:"value"},yAxis:{type:"value"},series:[] as unknown[]};
  if(panel.viz==="scatter"){
    const item=dims[0];const colour=dims[1];const groups=[...new Set(rows.map(r=>colour?String(r[colour]??""):"Items"))];
    return {...base,xAxis:{type:(panel.options?.x_scale??panel.options?.scale)==="log"?"log":"value",name:panel.x_unit??measures[0]?.unit,axisLabel:{formatter:formatAxis(panel.x_unit??measures[0]?.unit)}},yAxis:{type:(panel.options?.y_scale??panel.options?.scale)==="log"?"log":"value",name:panel.unit??measures[1]?.unit,axisLabel:{formatter:formatAxis(panel.unit??measures[1]?.unit)}},series:groups.map(name=>({type:"scatter",name,itemStyle:{color:seriesColor(name,theme.dark)},data:rows.filter(r=>!colour||String(r[colour]??"")===name).filter(r=>typeof r[measures[0]?.name]==="number"&&typeof r[measures[1]?.name]==="number").filter(r=>(panel.options?.x_scale!=="log"||Number(r[measures[0].name])>0)&&(panel.options?.y_scale!=="log"||Number(r[measures[1].name])>0)).map(r=>({name:String(r[item]??""),value:[r[measures[0].name],r[measures[1].name]],selection:{dimensions:Object.fromEntries((panel.query?.by??[]).map((key,i)=>[key,String(r[dims[i]]??"")]))}}))}))};
  }
  if(panel.viz==="histogram"){
    const split=dims.find(d=>d!=="bucket_lower"&&d!=="bucket_upper");
    const ordered=[...rows].sort((a,b)=>(a.bucket_lower===null?-Infinity:Number(a.bucket_lower))-(b.bucket_lower===null?-Infinity:Number(b.bucket_lower))||(a.bucket_upper===null?Infinity:Number(a.bucket_upper))-(b.bucket_upper===null?Infinity:Number(b.bucket_upper)));
    const labels=[...new Set(ordered.map(r=>`${r.bucket_lower??"−∞"}–${r.bucket_upper??"∞"}`))];const names=[...new Set(rows.map(r=>split?String(r[split]??""):"Count"))];
    return {...base,xAxis:{type:"category",data:labels,name:panel.unit??frame.columns.find(c=>c.name==="bucket_lower")?.unit},yAxis:{type:"value",name:"count"},series:names.map(name=>({type:"bar",name,itemStyle:{color:seriesColor(name,theme.dark)},data:labels.map(label=>{const r=rows.find(r=>(!split||String(r[split]??"")===name)&&`${r.bucket_lower??"−∞"}–${r.bucket_upper??"∞"}`===label);return {value:r?.count??0,selection:{dimensions:split?{[panel.query?.by?.[0]??split]:name}:{},bucket:typeof r?.bucket_lower==="number"?{lower:r.bucket_lower,upper:typeof r.bucket_upper==="number"?r.bucket_upper:undefined}:undefined}};})}))};
  }
  if(panel.viz==="service_map"){
    const nodes=new Map(rows.filter(r=>r.kind==="node").map(r=>[String(r.service),r]));const edges=rows.filter(r=>r.kind==="edge");for(const e of edges)for(const name of [String(e.caller),String(e.callee)])if(!nodes.has(name))nodes.set(name,{service:name,health:"unknown"});
    return {...base,xAxis:undefined,yAxis:undefined,legend:undefined,series:[{type:"graph",layout:"force",roam:true,force:{repulsion:180,edgeLength:100},label:{show:true,color:theme.text},data:[...nodes].map(([name,r])=>({id:name,name,symbol:healthSymbol(String(r.health??"unknown")),symbolSize:24*healthSymbolScale(String(r.health??"unknown")),value:{spans:r.spans,p95_ms:r.p95_ms,error_rate:r.error_rate},itemStyle:{borderType:healthBorderType(String(r.health??"unknown")),borderWidth:2,color:r.health==="unhealthy"?theme.status.bad:r.health==="degraded"?theme.status.warn:theme.muted},selection:{dimensions:{service:name}}})),links:edges.map(r=>({source:r.caller,target:r.callee,value:r.calls,lineStyle:{color:Number(r.error_rate)>0?theme.status.bad:theme.border,width:Number(r.error_rate)>0?3+Math.min(5,Number(r.error_rate)/10):1,opacity:Number(r.error_rate)>0?1:.6}}))}]};
  }
  if(panel.viz==="heatmap"||panel.viz==="state_timeline"){
    const heat=panel.viz==="heatmap";const item=dims[0];const ordered=heat?[...rows].sort((a,b)=>(a.bucket_lower===null?-Infinity:Number(a.bucket_lower))-(b.bucket_lower===null?-Infinity:Number(b.bucket_lower))):rows;const names=[...new Set(ordered.map(r=>heat?`${r.bucket_lower??"−∞"}–${r.bucket_upper??"∞"}`:String(r[item]??"")))];
    const interval=spanMs(result.interval);const value=measures[0]?.name??"count";
    const data=rows.map(r=>({value:[r.time,names.indexOf(heat?`${r.bucket_lower??"−∞"}–${r.bucket_upper??"∞"}`:String(r[item]??"")),r[value],Number(r.time)+interval,heat?0:typeof r[value]!=="number"?0:(statusFor(r[value],panel.thresholds,panel.better??result.better)==="bad"?3:statusFor(r[value],panel.thresholds,panel.better??result.better)==="warn"?2:1)],selection:{time:r.time,dimensions:heat?{}:{[panel.query?.by?.[0]??item]:String(r[item]??"")},bucket:heat&&typeof r.bucket_lower==="number"?{lower:r.bucket_lower,upper:typeof r.bucket_upper==="number"?r.bucket_upper:undefined}:undefined},itemStyle:heat?undefined:{color:typeof r[value]==="number"?theme.status[statusFor(r[value],panel.thresholds,panel.better??result.better)??"ok"]:theme.muted}}));
    const maxCount=Math.max(1,...rows.map(r=>Number(r[value])||0));
    return {...base,tooltip:{...base.tooltip,trigger:"axis"},axisPointer:{link:[{xAxisIndex:"all"}]},xAxis:{type:"time",axisPointer:{show:true}},yAxis:{type:"category",data:names},visualMap:heat?{show:false,min:0,max:maxCount,dimension:2,inRange:{color:[theme.surface,seriesColor("distribution",theme.dark)]}}:undefined,series:[{type:"custom",name:panel.title,encode:{x:[0,3],y:1,tooltip:2},data,renderItem:(_params:unknown,api:{value:(i:number)=>number;coord:(v:number[])=>number[];size:(v:number[])=>number[];style:()=>Record<string,unknown>})=>{const left=api.coord([api.value(0),api.value(1)]);const right=api.coord([api.value(3),api.value(1)]);const height=Math.abs(api.size([0,1])[1])*.85;const cell={type:"rect",shape:{x:left[0],y:left[1]-height/2,width:Math.max(1,right[0]-left[0]),height},style:api.style()};
      if(heat)return cell;
      const health=["unknown","healthy","degraded","unhealthy"][api.value(4)]??"unknown";
      const symbol=healthSymbol(health);const x=left[0]+Math.min(8,Math.max(1,(right[0]-left[0])/2)),y=left[1],r=Math.min(4,height/3);
      const shape=symbol==="diamond"?{type:"polygon",shape:{points:[[x,y-r],[x+r,y],[x,y+r],[x-r,y]]}}:symbol==="roundRect"?{type:"rect",shape:{x:x-r,y:y-r,width:2*r,height:2*r,r:2}}:{type:"circle",shape:{cx:x,cy:y,r}};
      return {type:"group",children:[cell,{...shape,style:{fill:health==="unknown"?theme.surface:theme.text,stroke:theme.text,lineDash:healthBorderType(health)==="dashed"?[2,2]:undefined}}]};}}]};
  }
  return base;
}
```


- [ ] **Step 4: Render through memoized workspace components**

Create `ui/host/src/dashboards/viz/analysis-chart.tsx`:

```tsx
import { useMemo } from "react";
import { analysisOption, analysisSummary } from "../../../../panels/analysis";
import { chartThemeFor } from "../../../../panels/compile";
import type { Panel, PanelResult, Selection } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";
export type AnalysisProps={panel:Panel;title?:string;result:PanelResult;dark:boolean;height:number;group?:string;onSelect?:(value:string)=>void;onPoint?:(selection:Selection)=>void;onZoom?:(from:number,to:number)=>void};
export function AnalysisChart({panel,title,result,dark,height,group,onSelect,onPoint}:AnalysisProps){
  const option=useMemo(()=>analysisOption(panel,result,chartThemeFor(dark)),[panel,result,dark]);
  const label=title?`${title}: ${result.frame?.rows??0} rows`:analysisSummary(panel,result);
  const time=panel.viz==="heatmap"||panel.viz==="state_timeline";
  return <EChartCanvas option={option} height={height} label={label} group={time?group:undefined} onClick={onPoint||onSelect?(event)=>{const selection=(event.data as {selection?:Selection}|undefined)?.selection;if(selection){onPoint?.(selection);const value=Object.values(selection.dimensions)[0];if(value!==undefined)onSelect?.(value);}}:undefined}/>;
}
```

Extend EChartCanvas's `onClick` event type with `data?: unknown`, in both its props and its event cast. Add `CustomChart`, `GraphChart`, `HeatmapChart`, `ScatterChart` from `echarts/charts`, and `VisualMapComponent` from components to the registration and the existing test mocks. Keep canvas, group cleanup, legend persistence and the lifetime effects intact.

Create the six chart wrappers with these complete contents:

`heatmap.tsx`:
```tsx
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";
export function HeatmapViz(props:AnalysisProps){return <AnalysisChart {...props}/>;}
```
`histogram.tsx`:
```tsx
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";
export function HistogramViz(props:AnalysisProps){return <AnalysisChart {...props}/>;}
```
`scatter.tsx`:
```tsx
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";
export function ScatterViz(props:AnalysisProps){return <AnalysisChart {...props}/>;}
```
`state-timeline.tsx`:
```tsx
import {Text} from "@mantine/core";
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";
export function StateTimelineViz(props:AnalysisProps){return <><AnalysisChart {...props}/><Text size="xs" c="dimmed">Gaps and gray cells mean unknown.</Text></>;}
```
`service-map.tsx`:
```tsx
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";
export function ServiceMapViz(props:AnalysisProps){return <AnalysisChart {...props}/>;}
```
`health.tsx` (adapted from `2c208c85^:ui/host/src/widgets/overview.tsx`; M1 removed it):
```tsx
import {Box,Group,Progress,SimpleGrid,Stack,Text} from "@mantine/core";
import {healthColor,statusHex} from "../../../../chart";
import {integer} from "../../../../format";
import {Metric,HealthShape,HealthTrend} from "./health-pieces";
import type {AnalysisProps} from "./analysis-chart";
export function HealthViz({result,dark}:AnalysisProps){
 const source=result.frame?.health;const finite=(value:number)=>Number.isFinite(value)?value:0;
 const data=source?{...source,total_spans:finite(source.total_spans),service_count:finite(source.service_count),counts:{healthy:finite(source.counts.healthy),degraded:finite(source.counts.degraded),unhealthy:finite(source.counts.unhealthy)}}:undefined;
 const empty=!data||data.health==="unknown"||data.service_count===0||!Number.isFinite(data.error_rate);
 const health=empty?"unknown":data.health;const total=Math.max(data?.service_count??0,1);const status=statusHex(dark);
 return <Stack gap="sm" role="region" aria-label={`Service health: ${empty?"No data":health}; ${data?.service_count??0} services`}>
  <SimpleGrid cols={2} spacing="sm">
   <Metric label="Health" value={<Group gap={6}><HealthShape health={health}/>{empty?"No data":health.charAt(0).toUpperCase()+health.slice(1)}</Group>} color={healthColor(health)} hint={empty?"No telemetry in this window":`${integer.format(data.service_count)} services`}/>
   <Metric label="Error rate" value={empty?"—":`${data.error_rate.toFixed(2)}%`} color={!empty&&data.error_rate>=1?"bad":undefined} hint={empty?undefined:`${integer.format(data.total_spans)} operations`}>
    {!empty&&data.error_trend.length>1&&<HealthTrend values={data.error_trend} color={status.bad}/>}</Metric>
  </SimpleGrid>
  {!empty&&<Box><Progress.Root size="md" aria-label="Service health distribution">
   <Progress.Section value={data.counts.healthy/total*100} color="ok"/>
   <Progress.Section value={data.counts.degraded/total*100} color="warn"/>
   <Progress.Section value={data.counts.unhealthy/total*100} color="bad"/>
  </Progress.Root><Group mt={6} gap="md">{(["healthy","degraded","unhealthy"] as const).map(health=><Group key={health} gap={6}><HealthShape health={health}/><Text c="dimmed" size="xs">{data.counts[health]} {health}</Text></Group>)}</Group></Box>}
 </Stack>;
}
```

Create `ui/host/src/dashboards/viz/health-pieces.tsx` (adapted from the Metric/Sparkline in `2c208c85^:ui/host/src/widgets/pieces.tsx`; uses the host canvas):

```tsx
import {Box,Paper,Text} from "@mantine/core";
import {useMemo,type ReactNode} from "react";
import {healthColor,healthSymbol,healthBorderType} from "../../../../chart";
import {EChartCanvas} from "../echart-canvas";
export function Metric({label,value,color,hint,children}:{label:string;value:ReactNode;color?:string;hint?:string;children?:ReactNode}){
 return <Paper withBorder radius="md" p="sm" bg="var(--mantine-color-default)" miw={0} h="100%"><Text c="dimmed" size="xs" truncate>{label}</Text><Box fw={600} fz="xl" c={color} mt={2} lh={1.2}>{value}</Box>{hint&&<Text c="dimmed" size="xs" mt={2}>{hint}</Text>}{children&&<Box mt={4}>{children}</Box>}</Paper>;
}
export function HealthShape({health}:{health:string}){
 const symbol=healthSymbol(health);const color=`var(--mantine-color-${healthColor(health)}-filled)`;
 return <svg aria-hidden width={12} height={12} viewBox="0 0 12 12" fill={health==="unknown"?"none":color} stroke={color} strokeDasharray={healthBorderType(health)==="dashed"?"2 2":undefined}>{symbol==="diamond"?<polygon points="6,0 12,6 6,12 0,6"/>:symbol==="roundRect"?<rect x={1} y={1} width={10} height={10} rx={2}/>:<circle cx={6} cy={6} r={5}/>}</svg>;
}
export function HealthTrend({values,color}:{values:number[];color:string}){
 const finite=useMemo(()=>values.map(v=>Number.isFinite(v)?v:null),[values]);const peak=Math.max(0,...finite.filter((v):v is number=>v!==null));
 const option=useMemo(()=>({animation:false,grid:{left:0,right:0,top:2,bottom:2},xAxis:{type:"category",show:false,data:finite.map((_,i)=>i)},yAxis:{type:"value",show:false,min:0},tooltip:{show:false},series:[{type:"line",data:finite,showSymbol:false,smooth:.3,lineStyle:{width:1.5,color},areaStyle:{opacity:.12,color}}]}),[finite,color]);
 return <Box><EChartCanvas option={option} height={28} label="Error rate trend"/>{peak>0&&<Text c="dimmed" size="xs" ta="right" mt={2}>peak {peak.toFixed(2)}%</Text>}</Box>;
}
```


Replace `viz/index.tsx` with:

```tsx
import type { Panel, PanelResult, Selection } from "../../../../panels/types";
import { BarViz } from "./bar";
import { GaugeViz } from "./gauge";
import { StatViz } from "./stat";
import { TableViz } from "./table";
import { TextViz } from "./text";
import { TimeseriesViz } from "./timeseries";
import { HeatmapViz } from "./heatmap";
import { HistogramViz } from "./histogram";
import { ScatterViz } from "./scatter";
import { StateTimelineViz } from "./state-timeline";
import { ServiceMapViz } from "./service-map";
import { HealthViz } from "./health";
export function Viz(props:{panel:Panel;title?:string;result?:PanelResult;dark:boolean;height:number;group:string;onSelect?:(value:string)=>void;onPoint?:(selection:Selection)=>void;onZoom?:(from:number,to:number)=>void}){
  const {panel,title,result,dark,height,group,onSelect}=props;
  if(panel.viz==="text")return <TextViz panel={panel}/>;
  if(!result?.frame)return null;
  const next={...props,result};
  switch(panel.viz){
    case "stat":return <StatViz panel={panel} result={result}/>;
    case "gauge":return <GaugeViz panel={panel} title={title} result={result} dark={dark} height={height}/>;
    case "timeseries":return <TimeseriesViz panel={panel} title={title} result={result} dark={dark} height={height} group={group} onSelect={onSelect}/>;
    case "bar":return <BarViz panel={panel} title={title} result={result} dark={dark} height={height} onSelect={onSelect}/>;
    case "table":return <TableViz panel={panel} result={result} height={height} onSelect={onSelect}/>;
    case "heatmap":return <HeatmapViz {...next}/>;
    case "histogram":return <HistogramViz {...next}/>;
    case "scatter":return <ScatterViz {...next}/>;
    case "state_timeline":return <StateTimelineViz {...next}/>;
    case "service_map":return <ServiceMapViz {...next}/>;
    case "health":return <HealthViz {...next}/>;
    case "logs":case "log_patterns":case "traces":return null; // Task 8 installs row renderers.
  }
}
```

The registry test imports the production visualizations constant and therefore pins its fifteen entries.

Add to new-viz.test.tsx:

```tsx
it("formats distinct scatter axis units and numeric bucket order",()=>{
 const panel:Panel={id:"p",title:"Rows versus latency",viz:"scatter",x_unit:"count",unit:"ms",query:{from:"spans",by:["service"],measures:["count()","p95(duration_ms)"]}};
 const option=analysisOption(panel,resultFor("scatter"),chartThemeFor(false)) as {xAxis:{name:string;axisLabel:{formatter:(n:number)=>string}};yAxis:{name:string;axisLabel:{formatter:(n:number)=>string}}};
 expect(option.xAxis.name).toBe("count");expect(option.yAxis.name).toBe("ms");expect(option.xAxis.axisLabel.formatter(2)).toBe("2");expect(option.yAxis.axisLabel.formatter(50)).toContain("ms");
 const frame={columns:fixtures.histogram!.columns,values:[[10,null,2],[20,1,10],[1,2,3]],rows:3};
 for(const viz of ["histogram","heatmap"] as const){
  const f=viz==="heatmap"?{columns:[col("time","time","time"),...frame.columns],values:[[3000,1000,2000],...frame.values],rows:3}:frame;
  const got=analysisOption({...panel,viz}, {id:"p",status:"ok",elapsed_ms:1,frame:f},chartThemeFor(false)) as {xAxis?:{data?:string[]};yAxis?:{data?:string[]}};
  expect(viz==="heatmap"?got.yAxis?.data:got.xAxis?.data).toEqual(["−∞–1","2–10","10–20"]);
 }
});
```

- [ ] **Step 5: Run to see pass and build the embedded bundle**

```bash
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

Sanitize numeric table cells when deriving TableViz rows: replace each non-finite numeric value with null. Filter maxima with Number.isFinite as well, so RowPanel’s table fallback follows frameRows’s chart behavior. Keep original column/row indexes for link targets and trends.

In PanelCard’s initial loading branch, replace `<Loader size="sm" />` with `<Loader size="sm" aria-label="Loading panel" />`. Loading must have an accessible name.

Expected: all tests and TypeScript pass; both embedded workspaces build. Keep loading/empty/error behavior in PanelCard and data Inspect rather than duplicating it in each renderer.

- [ ] **Step 6: Commit (controller)**

```bash
rtk git add ui/panels ui/host/src/dashboards internal/ui/dist internal/mcp/apps
rtk git commit -m "feat(ui): render dashboard charts and port overview health"
```

---

### Task 8: Row panels: logs, log patterns and traces

**Files:** Create `ui/host/src/dashboards/viz/row-panel.tsx`, `logs.tsx`, `log-patterns.tsx`, `traces.tsx`; modify `viz/table.tsx`, `viz/index.tsx`, `new-viz.test.tsx` (relative to dashboards); generated `internal/ui/dist/**`, `internal/mcp/apps/**`.

**Interfaces:** Reuse Task 7’s rows.ts, rowModel, Selection and AnalysisProps. RowPanel uses TableViz’s memoized columns and a useCallback cell renderer.

- [ ] **Step 1: Write failing row-rendering tests**

Extend new-viz.test.tsx’s types array to all nine new types. Keep its exact Go-shaped fixtures, populated/ok-zero-row/non-finite checks and chrome tests. Add content assertions after populated render: logs contains failed; log_patterns contains failed <*> and a Pattern count trend SVG; traces contains cart and abc. These fail while Viz returns null for rows.

```ts
const types:VizType[]=["heatmap","histogram","scatter","state_timeline","logs","log_patterns","traces","service_map","health"];
```

```bash
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/new-viz.test.tsx'
```

- [ ] **Step 2: Implement fixed row renderers**

Create `ui/host/src/dashboards/viz/row-panel.tsx`:

```tsx
import { Anchor, Badge, Highlight, Text } from "@mantine/core";
import { useMemo, useCallback } from "react";
import { rowModel } from "../../../../panels/rows";
import type { Cell } from "../../../../panels/types";
import { TableViz } from "./table";
import type { AnalysisProps } from "./analysis-chart";
export function RowPanel(props:AnalysisProps){
  const model=useMemo(()=>rowModel(props.panel,props.result),[props.panel,props.result]);
  const cell=useCallback((name:string,value:Cell,rowIndex:number)=>{
    const row=model.rows[rowIndex];const selection=model.selection(row);
    if(name==="trace_id"&&value)return <Anchor href={`?drill=${encodeURIComponent(JSON.stringify({panel_id:props.panel.id,kind:"traces",trace_id:String(value),namespace:String(row.namespace??""),dimensions:selection.dimensions}))}`} onClick={event=>{if(props.onPoint){event.preventDefault();props.onPoint(selection);}}}>{String(value)}</Anchor>;
    if(name==="service"&&value&&props.onSelect)return <Anchor component="button" type="button" onClick={()=>props.onSelect?.(String(value))}>{String(value)}</Anchor>;
    if(name==="health"||name==="severity"||name==="status")return <Badge color={String(value).includes("ERROR")||value==="unhealthy"?"bad":value==="degraded"||value==="WARN"?"warn":"gray"}>{String(value??"Unknown")}</Badge>;
    if(name==="body"&&props.panel.options?.highlight)return <Highlight highlight={props.panel.options.highlight}>{String(value??"")}</Highlight>;
    if(name==="trend") {let points:number[]=[];try{const parsed:unknown=JSON.parse(String(value));if(Array.isArray(parsed))points=parsed.filter((v):v is number=>typeof v==="number"&&Number.isFinite(v));}catch{points=[];}const max=Math.max(1,...points);return <svg role="img" aria-label="Pattern count trend" width={100} height={24} viewBox="0 0 100 24"><polyline fill="none" stroke="var(--mantine-primary-color-filled)" points={points.map((n,i)=>`${i*100/Math.max(points.length-1,1)},${23-n/max*22}`).join(" ")}/></svg>;}
    if(name==="body_template")return <Text size="sm" ff="monospace">{String(value??"")}</Text>;
    return undefined;
  },[model,props.panel,props.result,props.onPoint,props.onSelect]);
  return <div role="region" aria-label={`${props.title??props.panel.title}: ${model.rows.length} rows`}><TableViz panel={props.panel} result={props.result} height={props.height} onSelect={props.onSelect} renderCell={cell}/></div>;
}
```

In TableViz's props add `renderCell?: (name: string, value: Cell, rowIndex: number) => ReactNode` and import `type ReactNode` from react. At the start of each column's cell callback, immediately after `const value = info.getValue() as Cell`, insert:

```tsx
const custom = renderCell?.(column.name, value, info.row.index);
if (custom !== undefined) return custom;
```

Include `renderCell` in the column useMemo dependencies. Preserve its TanStack table implementation and sorting.

`logs.tsx`:
```tsx
import { RowPanel } from "./row-panel";
import type { AnalysisProps } from "./analysis-chart";
export function LogsViz(props:AnalysisProps){return <RowPanel {...props}/>;}
```
`log-patterns.tsx`:
```tsx
import { RowPanel } from "./row-panel";
import type { AnalysisProps } from "./analysis-chart";
export function LogPatternsViz(props:AnalysisProps){return <RowPanel {...props}/>;}
```
`traces.tsx`:
```tsx
import { RowPanel } from "./row-panel";
import type { AnalysisProps } from "./analysis-chart";
export function TracesViz(props:AnalysisProps){return <RowPanel {...props}/>;}
```
In viz/index.tsx import LogsViz, LogPatternsViz and TracesViz from their sibling modules and replace the combined null case with:

```tsx
case "logs":return <LogsViz {...next}/>;
case "log_patterns":return <LogPatternsViz {...next}/>;
case "traces":return <TracesViz {...next}/>;
```

The memoized cell callback includes its real model/panel/result/interaction dependencies. Service cells activate only with onSelect. Task 10 replaces the preliminary trace URLs with captured absolute targets.

- [ ] **Step 3: Run to see pass and review**

```bash
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

Expected: all owning regression and integration tests pass.

- [ ] **Step 4: Commit (controller)**

```bash
rtk git add ui/host/src/dashboards internal/ui/dist internal/mcp/apps
rtk git commit -m "feat(ui): render dashboard log, pattern and trace rows"
```

---

### Task 9: Filter chips, linked crosshair, brush zoom and panel time

**Files:**
- Create: `ui/panels/interaction.ts`, `ui/host/src/dashboards/use-brush-zoom.ts`, `ui/host/src/dashboards/interaction.test.tsx`, `internal/panel/interaction_test.go`
- Modify: `internal/panel/exec.go`, `ui/host/src/dashboards/page.tsx`, `grid.tsx`, `panel-card.tsx`, `echart-canvas.tsx`, `echart-canvas.test.tsx`, `viz/timeseries.tsx`, `viz/bar.tsx`, `viz/analysis-chart.tsx`, `viz/index.tsx` (paths relative to `ui/host/src/dashboards`)
- Generated: `internal/ui/dist/**`, `internal/mcp/apps/**`

**Interfaces:**
- Consumes existing: `DashboardPage({dashboardId,search,onSearch,onOpen})`; `onSearch(next: DashboardSearch, replace?: boolean): void`; `GridProps.onVariable(name:string,value:VarValue):void`; `Panel.time?:{range?:string;shift?:string}`; `echarts.connect(group:string)` and `disconnect(group:string)` already maintained by EChartCanvas.
- Produces Go Result `FromMS int64 json:"from_ms"`, `ToMS int64 json:"to_ms"`, populated from resolveWindow; consumes the TS `from_ms`/`to_ms` mirrors introduced in Task 7.
- Produces host `useBrushZoom(search:DashboardSearch,onSearch:(next:DashboardSearch,replace?:boolean)=>void):{zoom:(from:number,to:number)=>void;reset:()=>void}` and pure TS `brushRange(from:number,to:number):{from:string;to:string}|undefined`; `pointSelection(panel: Panel, result: PanelResult, event: ChartEvent): Selection | undefined`; `panelTimeLabel(panel: Panel): string | undefined`; `ChartEvent = {name?:string;seriesName?:string;value?:unknown;data?:unknown}`. `DashboardSearch` stays in the host; pure range decoding returns absolute timestamp strings.
- Extends GridProps/PanelCard/Viz with `onPoint?(panel:Panel,selection:Selection):void` at the grid boundary, then `(selection:Selection)=>void`; `onZoom?(from:number,to:number):void`. Existing onSelect still selects variables. Only timeseries/heatmap/state_timeline join the time group.

- [ ] **Step 1: Write failing interaction and effective-window tests**

Create `internal/panel/interaction_test.go`:

```go
package panel

import (
	"testing"
	"time"
)

func TestM2PanelTimeWindow(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[:1]
	d.Panels[0].Time = &PanelTime{Range: "15m"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FromMS != fixtureStart.Add(45*time.Minute).UnixMilli() || got[0].ToMS != fixtureStart.Add(time.Hour).UnixMilli() {
		t.Fatalf("window: %+v", got[0])
	}
	d.Panels[0].Time = &PanelTime{Shift: "1h"}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FromMS != fixtureStart.Add(-time.Hour).UnixMilli() || got[0].ToMS != fixtureStart.UnixMilli() {
		t.Fatalf("shift: %+v", got[0])
	}
}
```

Create `ui/host/src/dashboards/interaction.test.tsx`:

```tsx
import { describe,expect,it } from "vitest";
import { brushRange,panelTimeLabel,pointSelection } from "../../../panels/interaction";
import { parseSearch,toSearchParams } from "./search";
import type { Panel,PanelResult } from "../../../panels/types";
const panel:Panel={id:"p",title:"P",viz:"timeseries",query:{from:"spans",measures:["count()"],by:["service"]},time:{shift:"1d"}};
const result:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:1000,to_ms:10000,interval:"1m"};
describe("M2 interactions",()=>{
  it("writes one valid absolute range, retaining comparison and variables",()=>{
    const selected=brushRange(1000,9000);expect(selected).toEqual({from:new Date(1000).toISOString(),to:new Date(9000).toISOString()});
    const state={...parseSearch({range:"1h","var-service":"checkout",compare:"1"}),...selected,range:undefined};
    expect(parseSearch(toSearchParams(state))).toEqual({from:selected?.from,to:selected?.to,vars:{service:"checkout"},compare:"1"});
    expect(brushRange(2,2)).toBeUndefined();expect(brushRange(NaN,3)).toBeUndefined();
  });
  it("selects real dimensions and never Other or comparison overlays",()=>{
    expect(pointSelection(panel,result,{seriesName:"checkout",value:[2000,3]})).toEqual({time:2000,dimensions:{service:"checkout"}});
    expect(pointSelection(panel,result,{seriesName:"Other",value:[2000,3]})).toBeUndefined();
    expect(pointSelection(panel,result,{seriesName:"checkout · previous",value:[2000,3]})).toBeUndefined();
    expect(panelTimeLabel(panel)).toBe("Shifted 1d");
  });
});
```

Append these imports and test to interaction.test.tsx:

```tsx
import {act} from "react";
import {createRoot} from "react-dom/client";
import {vi} from "vitest";
import {useBrushZoom} from "./use-brush-zoom";
import type {DashboardSearch} from "./search";
it("pushes once per selection, preserves variables, and permits the same selection after Back",async()=>{
 const node=document.createElement("div");document.body.append(node);const root=createRoot(node);const navigate=vi.fn();
 let actions:ReturnType<typeof useBrushZoom>;
 function Host({search}:{search:DashboardSearch}){actions=useBrushZoom(search,navigate);return null;}
 const initial:DashboardSearch={range:"1h",vars:{service:"checkout"},compare:"1"};
 await act(async()=>root.render(<Host search={initial}/>));
 for(let i=0;i<20;i++)actions!.zoom(1000,9000);
 expect(navigate).toHaveBeenCalledTimes(1);expect(navigate.mock.calls[0][1]).toBe(false);expect(navigate.mock.calls[0][0].vars).toEqual(initial.vars);
 const brushed=navigate.mock.calls[0][0] as DashboardSearch;
 await act(async()=>root.render(<Host search={brushed}/>));actions!.zoom(1000,9000);expect(navigate).toHaveBeenCalledTimes(1);
 await act(async()=>root.render(<Host search={initial}/>));actions!.zoom(1000,9000);expect(navigate).toHaveBeenCalledTimes(2);
 await act(async()=>root.unmount());node.remove();
});
```

Extend the existing `fresh()` instance with `dispatchAction:vi.fn()` and append to `echart-canvas.test.tsx` inside its describe block (uses existing `fresh` and mocks):

```tsx
it("commits only brushEnd, keeps one chart on callback changes, and clears the brush",async()=>{
  const instance=fresh();
  const container=document.createElement("div");document.body.append(container);const root=createRoot(container);const zoom=vi.fn();const option={series:[]};
  await act(async()=>root.render(<EChartCanvas option={option} height={100} label="P" onZoom={zoom}/>));
  const end=instance.on.mock.calls.find(([name])=>name==="brushEnd")?.[1] as ((payload:unknown)=>void);
  expect(end).toBeTypeOf("function");expect(instance.on.mock.calls.some(([name])=>name==="brushselected")).toBe(false);
  end({areas:[{coordRange:[1000,5000]}]});expect(zoom).toHaveBeenCalledOnce();expect(zoom).toHaveBeenCalledWith(1000,5000);
  expect(instance.dispatchAction).toHaveBeenCalledWith({type:"brush",areas:[]},{silent:true});
  const calls=mocks.init.mock.calls.length;
  await act(async()=>root.render(<EChartCanvas option={option} height={100} label="P" onZoom={()=>undefined}/>));
  expect(mocks.init.mock.calls.length).toBe(calls);
  await act(async()=>root.unmount());
});
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... -run '"TestM2PanelTimeWindow"'
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/interaction.test.tsx src/dashboards/echart-canvas.test.tsx'
```

Expected: absent helpers, window metadata and brush callback.

- [ ] **Step 3: Implement precise windows and pure event decoding**

Add the two window fields to Go Result; Task 7 already introduced their TS mirror. In runPanel, immediately after successful `resolveWindow`, set:

```go
res.FromMS, res.ToMS = start.UnixMilli(), end.UnixMilli()

```

Create `ui/panels/interaction.ts`:

```ts
import type {Panel,PanelResult,Selection} from "./types";
export type ChartEvent={name?:string;seriesName?:string;value?:unknown;data?:unknown};
export function brushRange(from:number,to:number):{from:string;to:string}|undefined{
  if(!Number.isFinite(from)||!Number.isFinite(to)||from>=to||Math.abs(from)>8640000000000000||Math.abs(to)>8640000000000000)return undefined;
  return {from:new Date(Math.floor(from)).toISOString(),to:new Date(Math.ceil(to)).toISOString()};
}
export function panelTimeLabel(panel:Panel):string|undefined{const parts=[panel.time?.range?`Range ${panel.time.range}`:undefined,panel.time?.shift?`Shifted ${panel.time.shift}`:undefined].filter(Boolean);return parts.length?parts.join(" · "):undefined;}
export function pointSelection(panel:Panel,_result:PanelResult,event:ChartEvent):Selection|undefined{
  if(event.seriesName?.endsWith(" · previous")||event.seriesName==="Other"||event.name==="Other")return undefined;
  const embedded=(event.data as {selection?:Selection}|undefined)?.selection;
  if(embedded)return embedded;
  const dimension=panel.query?.by?.[0];const name=panel.viz==="timeseries"?event.seriesName:event.name;
  const value=Array.isArray(event.value)?event.value:[];
  const time=panel.viz==="timeseries"&&typeof value[0]==="number"?value[0]:undefined;
  return {time,dimensions:dimension&&name?{[dimension]:name}:{}};
}
```

- [ ] **Step 4: Wire callbacks without replacing chart instances**

In EChartCanvas's props/destructuring, add `onZoom?: (from:number,to:number)=>void`. Add `BrushComponent`, `ToolboxComponent` and `DataZoomComponent` to ECharts registration and its test mocks. Add a current ref `const zoom=useRef(onZoom); zoom.current=onZoom`. In the instance creation effect, register:

```ts
instance.on("brushEnd",(payload)=>{
  const range=(payload as {areas?:{coordRange?:number[]}[]}).areas?.[0]?.coordRange;
  if(range?.length===2&&Number.isFinite(range[0])&&Number.isFinite(range[1])&&range[0]<range[1]){
    zoom.current?.(range[0],range[1]);
    instance.dispatchAction({type:"brush",areas:[]},{silent:true});
  }
});
```

In the option application effect, extend the setOption payload with:

```ts
...(zoom.current ? {brush:{toolbox:["lineX","clear"],xAxisIndex:0,brushMode:"single",removeOnClick:true}} : {})
```

Immediately after every `chart.current?.setOption` while zoom is enabled, dispatch:

```ts
if(zoom.current)chart.current?.dispatchAction({type:"takeGlobalCursor",key:"brush",brushOption:{brushType:"lineX",brushMode:"single"}});
```

Use `const zoomEnabled=Boolean(onZoom)` in the option-effect dependencies, so enabling/disabling zoom updates the option without changing chart lifetime. After clearing a completed selection, dispatch the same cursor action again. Add a separate test in echart-canvas.test.tsx with a fresh instance and exact counts, independent of the brushEnd test:

```tsx
it("activates the brush cursor on initial render and option update",async()=>{
 const instance=fresh();
 const container=document.createElement("div");document.body.append(container);const root=createRoot(container);const zoom=vi.fn();const option={series:[]};
 await act(async()=>root.render(<EChartCanvas option={option} height={100} label="P" onZoom={zoom}/>));
 const cursorCalls=()=>instance.dispatchAction.mock.calls.filter(([a])=>a.type==="takeGlobalCursor");
 expect(cursorCalls()).toHaveLength(1);
 expect(instance.dispatchAction).toHaveBeenCalledWith({type:"takeGlobalCursor",key:"brush",brushOption:{brushType:"lineX",brushMode:"single"}});
 const nextOption={series:[{type:"line",data:[[1,2]]}]};
 await act(async()=>root.render(<EChartCanvas option={nextOption} height={100} label="P" onZoom={zoom}/>));
 expect(instance.setOption).toHaveBeenCalledTimes(2);expect(cursorCalls()).toHaveLength(2);
 await act(async()=>root.unmount());container.remove();
});
```

Callbacks remain ref-backed; do not add onZoom/onClick to the chart-lifetime effect dependencies. Include a minimal toolbox description in the chart aria label when zoom is available.

Replace TimeseriesViz with:

```tsx
import {useMemo} from "react";
import {chartThemeFor,timeseriesOption} from "../../../../panels/compile";
import {pointSelection} from "../../../../panels/interaction";
import {EChartCanvas} from "../echart-canvas";
import type {AnalysisProps} from "./analysis-chart";
export function TimeseriesViz({panel,title=panel.title,result,dark,height,group,onSelect,onPoint,onZoom}:AnalysisProps){
  const option=useMemo(()=>timeseriesOption(panel,result,chartThemeFor(dark)),[panel,result.frame,result.previous,result.shift_ms,dark]);
  return <EChartCanvas option={option} height={height} label={`${title}: time series. Brush to zoom.`} group={group} onZoom={onZoom} onClick={onPoint||onSelect?event=>{const selection=pointSelection(panel,result,event);if(!selection)return;onPoint?.(selection);const first=Object.values(selection.dimensions)[0];if(first!==undefined)onSelect?.(first);}:undefined}/>;
}
```

Replace BarViz with:

```tsx
import {useMemo} from "react";
import {barOption,chartThemeFor} from "../../../../panels/compile";
import {pointSelection} from "../../../../panels/interaction";
import {EChartCanvas} from "../echart-canvas";
import type {AnalysisProps} from "./analysis-chart";
export function BarViz({panel,title=panel.title,result,dark,height,onSelect,onPoint}:AnalysisProps){
  const option=useMemo(()=>barOption(panel,result.frame!,chartThemeFor(dark)),[panel,result.frame,dark]);
  return <EChartCanvas option={option} height={height} label={`${title}: bar chart`} onClick={onPoint||onSelect?event=>{const selection=pointSelection(panel,result,event);if(!selection)return;onPoint?.(selection);const first=Object.values(selection.dimensions)[0];if(first!==undefined)onSelect?.(first);}:undefined}/>;
}
```

AnalysisChart accepts onZoom from its props and passes it to EChartCanvas only when `time` is true. `viz/index.tsx` passes `onPoint`/`onZoom` to timeseries and `onPoint` to bar. Other analysis renderers already accept those props.

In PanelCard add onPoint/onZoom props and pass them to Viz; import Selection and `panelTimeLabel`. Add next to the title:

```tsx
{panelTimeLabel(panel)&&<Text size="xs" c="dimmed" role="status">{panelTimeLabel(panel)}</Text>}
```

In GridProps add `onPoint?(panel:Panel,selection:Selection):void; onZoom?(from:number,to:number):void`. Destructure them in PanelGrid; the card factory passes:

```tsx
onPoint={onPoint&&(panel.click||panel.drill||results.get(panel.id)?.frame?.columns.some((c,i)=>c.name==="trace_id"&&results.get(panel.id)!.frame!.values[i].some(v=>typeof v==="string"&&v!==""))||panel.options?.columns?.some(c=>c.format==="trace_link"&&results.get(panel.id)?.frame?.columns.some((column,i)=>column.name===c.field&&results.get(panel.id)!.frame!.values[i].some(v=>typeof v==="string"&&v!=="")))) ? selection=>onPoint(panel,selection) : undefined} onZoom={onZoom}
```

Create `ui/host/src/dashboards/use-brush-zoom.ts`:

```ts
import {useCallback,useRef} from "react";
import {brushRange} from "../../../panels/interaction";
import type {DashboardSearch} from "./search";
export function useBrushZoom(search:DashboardSearch,onSearch:(next:DashboardSearch,replace?:boolean)=>void){
 const scope=JSON.stringify([search.range,search.from,search.to]);
 const current=useRef({search,onSearch,scope});const last=useRef("");
 if(current.current.scope!==scope)last.current="";
 current.current={search,onSearch,scope};
 const reset=useCallback(()=>{last.current="";},[]);
 const zoom=useCallback((from:number,to:number)=>{
  const absolute=brushRange(from,to);if(!absolute)return;
  const active=current.current;const key=`${absolute.from}/${absolute.to}`;
  if(active.search.from===absolute.from&&active.search.to===absolute.to||last.current===key)return;
  last.current=key;
  active.onSearch({...active.search,range:undefined,...absolute},false);
 },[]);
 return {zoom,reset};
}
```

In Loaded, call `const {zoom,reset:resetBrush}=useBrushZoom(search,onSearch)` and pass `onZoom={zoom}` to PanelGrid. Call resetBrush in toolbar onRange/onAbsolute callbacks. The callback is stable across renders; the hook clears deduplication on actual URL window changes, including Back. Existing filter chips already remove URL overrides; give each button `aria-label={`Remove filter ${name}`}` and retain setVar's replace-history behavior. Click-to-filter uses click.set_variable through the existing grid onSelect.

- [ ] **Step 5: Run to see pass and build**

```bash
rtk just test ./internal/panel/... -run '"TestM2PanelTimeWindow|TestRunComparesWithThePreviousPeriod"'
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

Expected: stable chart lifetime, group disposal still correct, one history push per completed selection, comparison/vars survive zoom, exact panel override badge and server window.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

- [ ] **Step 6: Commit (controller)**

```bash
rtk git add internal/panel/exec.go internal/panel/interaction_test.go ui/panels ui/host/src/dashboards internal/ui/dist internal/mcp/apps
rtk git commit -m "feat(ui): connect dashboard time and selection interactions"
```

---

### Task 10: URL-addressed drill drawer and adapted trace waterfall

**Files:**
- Create: `ui/host/src/dashboards/trace/trace-components.tsx`, `ui/host/src/dashboards/trace/components.tsx`, `ui/host/src/dashboards/drill.tsx`, `ui/host/src/dashboards/drill-state.ts`, `ui/host/src/dashboards/drill.test.tsx`, `internal/panel/drill_logs_test.go`
- Modify: `internal/api/observability.go`, `internal/api/observability_test.go`, `cmd/fanout/main.go`, `ui/host/src/dashboards/api.ts`, `search.ts`, `page.tsx`, `viz/row-panel.tsx`, `viz/table.tsx`, `viz/index.tsx`; `internal/panel/exemplars.go`, `internal/panel/rowpanels.go`, `internal/panel/validate.go`
- Generated: `internal/ui/dist/**`, `internal/mcp/apps/**`

**Interfaces:**
- Consumes existing: GET `/api/observability/trace?trace_id=...&namespace=...&from=...&to=...&limit=...` via `observability.Trace(ctx,scope,traceID,service,limit)`; `ui/contracts.ts` exports `Result<T>`, `TraceDetail`, `TraceSpan`, `LogEntry`; Task 2 Exemplars; Task 9 Result.from_ms/to_ms and Selection.
- Copies and adapts within the host workspace: `Waterfall({spans,dark,onSpan}: {spans:TraceSpan[];dark:boolean;onSpan:(span:TraceSpan)=>void})`; `TraceLogs({entries}: {entries:LogEntry[]})`. The shared file has no createRoot call, CSS app import, MCP bridge or app mount.
- Produces: `DrillTarget = {panel_id:string;kind:"traces"|"logs";from:string;to:string;window_from:string;window_to:string;dimensions:Record<string,string>;trace_id?:string;namespace?:string;bucket?:{lower:number;upper?:number}}`; `parseDrill(raw: unknown): DrillTarget | undefined`; `makeDrill(panel: Panel, result: PanelResult, selection: Selection): DrillTarget | undefined`; `DrillDrawer({spec,time,vars,target,onChange}: DrillProps)`.
- Browser client: `queryExemplars(body: ExemplarBody, signal?: AbortSignal): Promise<ExemplarResponse>` and `getTrace(target: DrillTarget, signal?: AbortSignal): Promise<Result<TraceDetail>>`.
- Server extends ExemplarRequest with `Kind string json:"kind,omitempty"` and ExemplarResponse with `Logs *Frame json:"logs,omitempty"`; `kind=logs` is allowed only for a structured logs source, returns at most 200 checked log rows. Traces remain capped at 20. No new route.

- [ ] **Step 1: Write failing guarded-log and URL tests**

Create `internal/panel/drill_logs_test.go`:

```go
package panel

import (
	"github.com/labstack/fanout/internal/telemetry"
	"testing"
	"time"
)

func TestM2DrillLogsKeepCheckedSelection(t *testing.T) {
	engine, repo := newTestEngine(t)
	at := fixtureStart.UnixNano()
	commit(t, repo, nil, []telemetry.Log{{ServiceName: "checkout", Severity: "ERROR", Body: "failed", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at}, {ServiceName: "frontend", Severity: "INFO", Body: "ok", TimeUnixNanos: at, EventUnixNanos: at, IngestedAt: at}})
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	d := Dashboard{Name: "Logs", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "levels", Title: "Levels", Viz: "bar", Query: &Query{From: "logs", Where: []string{"service = 'checkout'"}, Measures: []string{"count()"}, By: []string{"severity"}}}}}
	req := ExemplarRequest{Dashboard: d, PanelID: "levels", Kind: "logs", From: fixtureStart, To: fixtureStart.Add(time.Minute), Dimensions: map[string]string{"severity": "ERROR"}}
	got, err := e.Exemplars(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Logs == nil || got.Logs.Rows != 1 || got.Logs.Values[2][0] != "checkout" {
		t.Fatalf("logs: %+v", got)
	}
	req.Dimensions = map[string]string{"severity": "INFO"}
	got, err = e.Exemplars(t.Context(), req)
	if err != nil || got.Logs.Rows != 0 {
		t.Fatalf("guard escaped: %+v %v", got, err)
	}
}
```

Create `ui/host/src/dashboards/drill.test.tsx`:

```tsx
import {describe,expect,it,vi} from "vitest";
import {makeDrill,parseDrill} from "./drill-state";
import {router} from "../router";
import {parseSearch,toSearchParams} from "./search";
import type {Panel,PanelResult} from "../../../panels/types";
const panel:Panel={id:"p",title:"P",viz:"timeseries",drill:"traces",query:{from:"spans",measures:["count()"],by:["service"]}};
const result:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:100000,to_ms:500000,interval:"1m"};
describe("M2 drill URL",()=>{
 it("reproduces bucket, dimensions, trace and variables",()=>{
  const target=makeDrill(panel,result,{time:200000,dimensions:{service:"checkout"},trace_id:"abc",namespace:"shop"});
  const search=toSearchParams({drill:JSON.stringify(target),vars:{service:"checkout"},compare:"1"});
  const url=router.buildLocation({to:"/dashboards/$dashboardId",params:{dashboardId:"d"},search});
  const decoded=parseSearch(router.options.parseSearch!(url.searchStr));
  expect(parseSearch(router.options.parseSearch!(router.options.stringifySearch!(toSearchParams(decoded))))).toEqual(decoded);
  expect(parseDrill(decoded.drill)).toEqual(target);expect(decoded.vars).toEqual({service:"checkout"});
  expect(target?.from).toBe(new Date(100000).toISOString()); // A specific trace retains the whole panel window.
 });
 it("rejects malformed, oversized and backwards payloads",()=>{
  expect(parseDrill("{" )).toBeUndefined();expect(parseDrill("x".repeat(4097))).toBeUndefined();
  expect(parseDrill(JSON.stringify({panel_id:"p",kind:"traces",from:"2026-10-01T13:00:00Z",to:"2026-10-01T12:00:00Z",dimensions:{}}))).toBeUndefined();
 });
});
```

Append to drill.test.tsx, importing the real shared components through DrillDrawer:

```tsx
import {MantineProvider} from "@mantine/core";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {act} from "react";
import {createRoot} from "react-dom/client";
import {DrillDrawer} from "./drill";
import type {DashboardSpec} from "../../../panels/types";
import type {DrillTarget} from "./drill-state";
const wire=vi.hoisted(()=>({exemplars:vi.fn(),trace:vi.fn()}));
vi.mock("./api",()=>({queryExemplars:wire.exemplars,getTrace:wire.trace}));
const spec:DashboardSpec={version:1,name:"D",time:{range:"1h"},panels:[panel]};
const target:DrillTarget={panel_id:"p",kind:"traces",from:"2026-10-01T12:00:00Z",to:"2026-10-01T13:00:00Z",window_from:"2026-10-01T12:00:00Z",window_to:"2026-10-01T13:00:00Z",dimensions:{service:"checkout"}};
const tick=()=>new Promise(resolve=>setTimeout(resolve,20));
it("aborts an obsolete selection and keeps the next selection visible",async()=>{
 let signal:AbortSignal|undefined;let finish:(value:{traces:[]})=>void=()=>undefined;
 wire.exemplars.mockImplementation((_body:unknown,incoming:AbortSignal)=>{signal=incoming;return new Promise((resolve,reject)=>{finish=resolve;incoming.addEventListener("abort",()=>reject(new DOMException("Aborted","AbortError")),{once:true});});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const node=document.createElement("div");document.body.append(node);const root=createRoot(node);const change=vi.fn();
 const render=(active?:DrillTarget)=><MantineProvider><QueryClientProvider client={client}><DrillDrawer spec={spec} time={spec.time} vars={{}} target={active} onChange={change}/></QueryClientProvider></MantineProvider>;
 await act(async()=>{root.render(render(target));await tick();});
 expect(signal).toBeDefined();expect(signal?.aborted).toBe(false);
 await act(async()=>{root.render(render());await tick();});expect(signal?.aborted).toBe(true);
 wire.exemplars.mockResolvedValueOnce({traces:[{trace_id:"next-trace",namespace:"shop",service:"next-service",operation:"next-operation",duration_ms:1,status:"OK",start:target.from}]});
 const next={...target,dimensions:{service:"next-service"}};
 await act(async()=>{root.render(render(next));await tick();});expect(document.body.textContent).toContain("next-operation");
 await act(async()=>{finish({traces:[]});await tick();});expect(document.body.textContent).toContain("next-operation");expect(document.body.textContent).not.toContain("No exemplar traces match");
 await act(async()=>root.unmount());client.clear();node.remove();
});
it("renders the existing waterfall and correlated logs with visible truncation",async()=>{
 wire.trace.mockResolvedValue({schema:"fanout.trace.v1",summary:"Trace",provenance:{query_id:"q",window:"w",generated_at:target.to,complete:true,data_source:"spans"},data:{trace_id:"abc",duration_ms:10,has_error:true,services:["checkout"],spans:[{span_id:"root",service:"checkout",operation:"cart",kind:"SPAN_KIND_SERVER",start:target.from,duration_ms:10,status:"STATUS_CODE_ERROR"}],logs:[{time:target.from,severity:"ERROR",service:"checkout",body:"correlated failure",trace_id:"abc"}],span_count:3,service_count:2,truncated:true}});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const node=document.createElement("div");document.body.append(node);const root=createRoot(node);
 await act(async()=>{root.render(<MantineProvider><QueryClientProvider client={client}><DrillDrawer spec={spec} time={spec.time} vars={{}} target={{...target,trace_id:"abc",namespace:"shop"}} onChange={()=>undefined}/></QueryClientProvider></MantineProvider>);await tick();});
 expect(document.body.textContent).toContain("correlated failure");expect(document.body.textContent).toContain("1 of 3 spans");expect(document.body.querySelector('[aria-label^="cart on checkout took"]')).not.toBeNull();
 await act(async()=>root.unmount());client.clear();node.remove();
});
```

The provenance/schema strings above are fixture labels; the rendering assertions use the adapted Waterfall and TraceLogs implementations, not copied markup.

Add to drill_logs_test.go (import errors and slices):

```go
func TestM2ExemplarKindValidation(t *testing.T) {
	e := newFixtureExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", Kind: "other", From: fixtureStart, To: fixtureStart.Add(time.Minute)}
	_, err := e.Exemplars(t.Context(), req)
	var got Problems
	want := Problem{Path: "kind", Message: "kind must be traces or logs", Hint: "choose traces or logs"}
	if !errors.As(err, &got) || !slices.Contains(got, want) {
		t.Fatalf("got %v want %+v", err, want)
	}
}
func TestM2ExemplarCapturedWindowDoesNotMove(t *testing.T) {
	e := newFixtureExecutor(t)
	from, to := fixtureStart, fixtureStart.Add(time.Hour)
	d := shopDashboard()
	d.Panels[2].Time = &PanelTime{Shift: "1h"}
	req := ExemplarRequest{Dashboard: d, PanelID: "by_route", Time: &Time{From: &from, To: &to, Refresh: "off"}, From: from, To: to}
	e.now = func() time.Time { return fixtureStart.Add(10 * time.Hour) }
	got, err := e.Exemplars(t.Context(), req)
	if err != nil || len(got.Traces) != 20 {
		t.Fatalf("request clock or double shift changed selection: %+v %v", got, err)
	}
}
```

- [ ] **Step 2: Run to see failure**

```bash
rtk just test ./internal/panel/... ./internal/observability/... -run '"TestM2DrillLogs|TestM2Exemplar|TestM2TraceAbsoluteWindow"'
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/drill.test.tsx'
```

Expected: missing request kind and URL state.

- [ ] **Step 3: Reuse row compilation for checked log selections**

In rowpanels.go factor the existing compiler prologue into a wrapper; the remainder of the complete compileRows body starts at `if p.Viz=="traces"` and becomes `compileRowsWhere`:

```go
func compileRows(p *Panel, filters []Filter, scope Scope) (Compiled, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	return compileRowsWhere(p, where, args, scope)
}
```

Use `func compileRowsWhere(p *Panel,where string,args []any,scope Scope)(Compiled,error)` for the extracted body.

The existing body and closing brace follow that signature unchanged; this is an extraction, not a second SQL implementation. Add the Kind/Logs fields above to the exemplar types. Reject any nonempty Kind other than `traces` or `logs` immediately after constructing `out` in Exemplars, returning `Problems{{Path:"kind",Message:"kind must be traces or logs",Hint:"choose traces or logs"}}`. Immediately after `selectionWhere` succeeds and before building the trace SQL, insert:

```go
if req.Kind == "logs" {
	if p.Query.From != "logs" {
		return out, Problems{{Path: "kind", Message: "log selections need a logs-source panel"}}
	}
	copyPanel := *p
	copyQuery := *p.Query
	copyQuery.Limit = 200
	copyQuery.Bucket = ""
	copyQuery.Sort = "time"
	copyPanel.Query = &copyQuery
	copyPanel.Viz = "logs"
	scope := Scope{Start: lo, End: hi, Vars: vars}
	compiled, err := compileRowsWhere(&copyPanel, where, args, scope)
	if err != nil {
		return out, err
	}
	logCtx := queryrows.WithWindow(ctx, queryrows.Window{Start: lo, End: hi})
	rows, err := e.engine.QueryContext(logCtx, compiled.SQL, compiled.Args...)
	if err != nil {
		return out, fmt.Errorf("drill logs: %s", SafeError(err))
	}
	out.Logs, err = scanFrame(rows, compiled.Columns, 200)
	return out, err
}

```

In validatePanel, immediately after the existing drill enum check, insert:

```go
if p.Drill != "" && (p.Query == nil || p.Query.From == "metrics" || p.Drill == "logs" && p.Query.From != "logs") {
	problems.add(path+".drill", "drill requires structured span/log lineage; logs drill requires a logs source")
}

```

- [ ] **Step 4: Extend absolute trace lookups and copy the trace components**

Create `ui/host/src/dashboards/trace/trace-components.tsx` with the following complete source:

```tsx
import {Badge,Box,Group,Table,Text,Tooltip} from "@mantine/core";
import {ListBullets} from "@phosphor-icons/react";
// Adapted copy of ui/apps/src/trace.tsx (M1 echart.tsx precedent). Consolidate in M3.
import type {LogEntry,TraceSpan} from "../../../../contracts";
import {seriesColor,severityColor} from "../../../../chart";
import {duration,exactTimestamp,timeZoneLabel} from "../../../../format";
import {EmptyState,PageControls,usePagedItems} from "./components";
export function Waterfall({ spans, dark, onSpan }: { spans: TraceSpan[]; dark: boolean; onSpan: (span: TraceSpan) => void }) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  const visibleSpans = usePagedItems(spans, 8);
  return <><Table.ScrollContainer minWidth={680}><Table highlightOnHover verticalSpacing="sm">
    <Table.Thead><Table.Tr><Table.Th w={230}>Operation</Table.Th><Table.Th>Timeline</Table.Th><Table.Th w={90} ta="right">Duration</Table.Th></Table.Tr></Table.Thead>
    <Table.Tbody>{visibleSpans.pageItems.map((span) => {
      const offset = (new Date(span.start).valueOf() - start) / total * 100;
      const width = Math.max(span.duration_ms / total * 100, .6);
      const failed = span.status.toUpperCase().includes("ERROR");
      return <Table.Tr key={span.span_id} tabIndex={0} onClick={() => onSpan(span)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onSpan(span); }} style={{ cursor: "pointer" }}>
        <Table.Td><Group gap="xs" wrap="nowrap"><Box w={8} h={8} bg={seriesColor(span.service, dark)} style={{ borderRadius: "50%", flex: "0 0 auto" }} /><Box miw={0}><Text fw={600} size="sm" truncate>{span.operation}</Text><Text c="dimmed" size="xs" truncate>{span.service}</Text></Box></Group></Table.Td>
        <Table.Td><Tooltip label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow><Box pos="relative" h={14} bg="var(--mantine-color-default-hover)" role="img" aria-label={`${span.operation} on ${span.service} took ${duration(span.duration_ms)}`} style={{ borderRadius: "var(--mantine-radius-sm)" }}><Box pos="absolute" left={`${offset}%`} w={`${Math.min(width, 100 - offset)}%`} h="100%" bg={failed ? "bad" : seriesColor(span.service, dark)} style={{ borderRadius: "var(--mantine-radius-sm)", minWidth: 3 }} /></Box></Tooltip></Table.Td>
        <Table.Td ta="right"><Text size="sm" ff="monospace">{duration(span.duration_ms)}</Text></Table.Td>
      </Table.Tr>;
    })}</Table.Tbody>
  </Table></Table.ScrollContainer><PageControls {...visibleSpans} onChange={visibleSpans.setPage} /></>;
}

export function TraceLogs({ entries }: { entries: LogEntry[] }) {
  const logs = usePagedItems(entries, 6);
  if (entries.length === 0) return <EmptyState tall icon={<ListBullets size={20} weight="duotone" />} title="No correlated logs">No logs in this window carry the selected trace ID.</EmptyState>;
  return <><Table.ScrollContainer minWidth={620}><Table striped verticalSpacing="xs"><Table.Thead><Table.Tr><Table.Th>Time ({timeZoneLabel(logs.pageItems[0]?.time)})</Table.Th><Table.Th>Level</Table.Th><Table.Th>Service</Table.Th><Table.Th>Message</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{logs.pageItems.map((entry, index) => <Table.Tr key={`${entry.time}-${logs.from + index}`}><Table.Td style={{ whiteSpace: "nowrap" }}><Text size="xs" ff="monospace" title={exactTimestamp(entry.time)}>{new Date(entry.time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })}</Text></Table.Td><Table.Td><Badge size="sm" color={severityColor(entry.severity)} variant="light">{entry.severity || "LOG"}</Badge></Table.Td><Table.Td><Text fw={600} size="sm">{entry.service}</Text></Table.Td><Table.Td><Text size="sm" lineClamp={2} title={entry.body}>{entry.body}</Text></Table.Td></Table.Tr>)}</Table.Tbody></Table></Table.ScrollContainer><PageControls {...logs} onChange={logs.setPage} /></>;
}


```

Create `ui/host/src/dashboards/trace/components.tsx` as the adapted host copy of the three dependencies from `ui/apps/src/components.tsx`:

```tsx
// Adapted from ui/apps/src/components.tsx. Keep Bun workspace boundaries; consolidate in M3.
import {Button,Center,Group,Text,ThemeIcon,Box} from "@mantine/core";
import {useEffect,useState,type ReactNode} from "react";
export function EmptyState({icon,title,children,tall=false}:{icon:ReactNode;title:string;children:ReactNode;tall?:boolean}){
 return <Center mih={tall?220:130} p="xl"><Group wrap="nowrap"><ThemeIcon variant="light" size="xl" radius="md">{icon}</ThemeIcon><Box><Text fw={700} size="sm">{title}</Text><Text c="dimmed" size="xs" mt={3}>{children}</Text></Box></Group></Center>;
}
export function usePagedItems<T>(items:T[],pageSize=8){
 const [page,setPage]=useState(1);const totalPages=Math.max(1,Math.ceil(items.length/pageSize));
 useEffect(()=>{if(page>totalPages)setPage(totalPages);},[page,totalPages]);const start=(page-1)*pageSize;
 return {page,setPage,totalPages,pageItems:items.slice(start,start+pageSize),from:items.length?start+1:0,to:Math.min(start+pageSize,items.length),total:items.length};
}
export function PageControls({page,totalPages,from,to,total,onChange}:{page:number;totalPages:number;from:number;to:number;total:number;onChange:(page:number)=>void}){
 if(totalPages<=1)return null;
 return <Group justify="space-between" mt="xs"><Text c="dimmed" size="xs">{from}–{to} of {total}</Text><Group gap={4}>
 {([{label:"First page",page:1,disabled:page===1},{label:"Previous page",page:page-1,disabled:page===1},{label:"Next page",page:page+1,disabled:page===totalPages},{label:"Last page",page:totalPages,disabled:page===totalPages}]).map(item=><Button key={item.label} size="compact-xs" variant="subtle" aria-label={item.label} disabled={item.disabled} onClick={()=>onChange(item.page)}>{item.label}</Button>)}
 </Group></Group>;
}
```

Keep the chat workspace’s trace.tsx unchanged. These copies contain no app mount or MCP bridge. Both embedded bundles build; consolidation is M3 work.

Extend `ObservabilityHandler` with `maxWindow time.Duration`; change its constructor to:

```go
func NewObservabilityHandler(queries ObservabilityQueries, retentionDays int) *ObservabilityHandler {
	maximum := 30 * 24 * time.Hour
	if retentionDays > 0 {
		maximum = time.Duration(retentionDays) * 24 * time.Hour
	}
	return &ObservabilityHandler{queries: queries, now: time.Now, maxWindow: maximum}
}
```

Update `cmd/fanout/main.go:270` to `api.NewObservabilityHandler(queries,cfg.RetentionDays)` and the constructor calls in observability_test.go to pass 30. Existing struct-literal deadline tests retain the 30-day default. In `ObservabilityHandler.request`, keep window and limit parsing unchanged; replace the block starting `end := h.now().UTC()` with:

```go
end := h.now().UTC()
start := end.Add(-window)
from, to := strings.TrimSpace(c.QueryParam("from")), strings.TrimSpace(c.QueryParam("to"))
if from != "" || to != "" {
	if from == "" || to == "" {
		return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "from and to must both be RFC 3339 timestamps")
	}
	var err error
	start, err = time.Parse(time.RFC3339Nano, from)
	if err != nil {
		return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "from must be RFC 3339")
	}
	end, err = time.Parse(time.RFC3339Nano, to)
	if err != nil {
		return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "to must be RFC 3339")
	}
	maximum := h.maxWindow
	if maximum <= 0 {
		maximum = 30 * 24 * time.Hour
	}
	if !start.Before(end) || end.Sub(start) > maximum {
		return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "absolute window must be positive and bounded by retention")
	}
}
return observability.Scope{Namespace: c.QueryParam("namespace"), Start: start.UTC(), End: end.UTC()}, limit, nil

```

`window` keeps its duration meaning; absolute pairs take precedence and obey the configured retention duration, matching observability.Service.normalizeScope. Do not clip the captured range to request-time now.

In fakeQueries add `traceScope observability.Scope` and have its existing Trace method record the scope. Add to observability_test.go:

```go
func TestM2TraceAbsoluteWindow(t *testing.T) {
	queries := &fakeQueries{}
	h := NewObservabilityHandler(queries, 7)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	e := echo.New()
	h.Register(e.Group("/api/observability"))
	cases := []struct {
		query string
		code  int
	}{
		{"?trace_id=abc&from=2026-10-01T12:00:00.123456789Z&to=2026-10-01T13:00:00.123456789Z&window=15m", 200},
		{"?from=2026-10-01T12:00:00Z", 400},
		{"?from=bad&to=2026-10-01T13:00:00Z", 400},
		{"?from=2026-10-01T13:00:00Z&to=2026-10-01T12:00:00Z", 400},
		{"?from=2026-09-01T12:00:00Z&to=2026-10-01T12:00:00Z", 400},
		{"?window=15m", 200},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/observability/trace"+tc.query, nil))
		if rec.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.query, rec.Code, rec.Body)
		}
		if tc.code == 200 {
			if tc.query == "?window=15m" {
				if !queries.traceScope.End.Equal(now) || queries.traceScope.End.Sub(queries.traceScope.Start) != 15*time.Minute {
					t.Fatalf("window changed: %+v", queries.traceScope)
				}
			} else {
				want := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
				if !queries.traceScope.Start.Equal(want) || queries.traceScope.End.Sub(want) != time.Hour {
					t.Fatalf("captured window lost: %+v", queries.traceScope)
				}
			}
		}
	}
}
```

- [ ] **Step 5: Implement bounded URL state and client adapters**

Create `ui/host/src/dashboards/drill-state.ts`:

```ts
import type {Panel,PanelResult,Selection} from "../../../panels/types";
export type DrillTarget={panel_id:string;kind:"traces"|"logs";from:string;to:string;window_from:string;window_to:string;dimensions:Record<string,string>;trace_id?:string;namespace?:string;bucket?:{lower:number;upper?:number}};
export function parseDrill(raw:unknown):DrillTarget|undefined{
 try{
  const encoded=typeof raw==="string"?raw:JSON.stringify(raw);if(!encoded||encoded.length>4096)return undefined;
  const v:unknown=typeof raw==="string"?JSON.parse(raw):raw;if(!v||typeof v!=="object")return undefined;const x=v as Partial<DrillTarget>;
  if(typeof x.panel_id!=="string"||!/^([a-z][a-z0-9_]{0,39})$/.test(x.panel_id)||(x.kind!=="traces"&&x.kind!=="logs")||typeof x.from!=="string"||typeof x.to!=="string"||!Number.isFinite(Date.parse(x.from))||!Number.isFinite(Date.parse(x.to))||Date.parse(x.from)>=Date.parse(x.to)||Date.parse(x.to)-Date.parse(x.from)>30*86400000)return undefined;
  if(typeof x.window_from!=="string"||typeof x.window_to!=="string"||!Number.isFinite(Date.parse(x.window_from))||!Number.isFinite(Date.parse(x.window_to))||Date.parse(x.window_from)>=Date.parse(x.window_to)||Date.parse(x.window_to)-Date.parse(x.window_from)>30*86400000||Date.parse(x.from)<Date.parse(x.window_from)||Date.parse(x.to)>Date.parse(x.window_to))return undefined;
  if(!x.dimensions||Array.isArray(x.dimensions)||typeof x.dimensions!=="object"||Object.keys(x.dimensions).length>3||Object.values(x.dimensions).some(v=>typeof v!=="string"||v.length>500))return undefined;
  if(x.trace_id!==undefined&&(typeof x.trace_id!=="string"||x.trace_id.length>128))return undefined;
  if(x.namespace!==undefined&&(typeof x.namespace!=="string"||x.namespace.length>200))return undefined;
  if(x.bucket&&(!Number.isFinite(x.bucket.lower)||x.bucket.lower<0||(x.bucket.upper!==undefined&&(!Number.isFinite(x.bucket.upper)||x.bucket.upper<=x.bucket.lower))))return undefined;
  return {panel_id:x.panel_id,kind:x.kind,from:x.from,to:x.to,window_from:x.window_from,window_to:x.window_to,dimensions:x.dimensions,trace_id:x.trace_id,namespace:x.namespace,bucket:x.bucket};
 }catch{return undefined;}
}
export function makeDrill(panel:Panel,result:PanelResult,selection:Selection):DrillTarget|undefined{
 if(!panel.drill&&!selection.trace_id)return undefined;
 if(result.from_ms===undefined||result.to_ms===undefined)return undefined;
 let from=result.from_ms,to=result.to_ms;
 if(selection.time!==undefined&&!selection.trace_id){const m=/^(\d+)(s|m|h|d)$/.exec(result.interval??"");const width=m?Number(m[1])*({s:1000,m:60000,h:3600000,d:86400000}[m[2] as "s"|"m"|"h"|"d"]):60000;from=Math.max(from,selection.time);to=Math.min(to,selection.time+width);}
 return parseDrill(JSON.stringify({panel_id:panel.id,kind:selection.trace_id?"traces":panel.drill,from:new Date(from).toISOString(),to:new Date(to).toISOString(),window_from:new Date(result.from_ms).toISOString(),window_to:new Date(result.to_ms).toISOString(),dimensions:selection.dimensions,trace_id:selection.trace_id,namespace:selection.namespace,bucket:selection.bucket}));
}
```

Extend DashboardSearch with `drill?:string`. In parseSearch, import parseDrill and insert `const drill=parseDrill(raw.drill);if(drill) out.drill=JSON.stringify(drill);` before returning. In toSearchParams insert `if(search.drill) out.drill=search.drill;`. URL links retain every other search value.

Append to api.ts (imports for Frame, Selection, Result, TraceDetail and DrillTarget are type-only):

```ts
export type ExemplarBody={dashboard:DashboardSpec;panel_id:string;kind?:"traces"|"logs";time?:DashboardTime;from:string;to:string;dimensions?:Record<string,string>;bucket?:Selection["bucket"];vars?:Record<string,VarValue>};
export type Exemplar={trace_id:string;namespace:string;service:string;operation:string;duration_ms:number;status:string;start:string};
export type ExemplarResponse={traces:Exemplar[];logs?:Frame;truncated?:boolean};
export const queryExemplars=(body:ExemplarBody,signal?:AbortSignal)=>request<ExemplarResponse>("/api/panels/exemplars",{method:"POST",json:body,signal});
export function getTrace(target:DrillTarget,signal?:AbortSignal):Promise<Result<TraceDetail>>{
 const params=new URLSearchParams({trace_id:target.trace_id??"",namespace:target.namespace??"",from:target.window_from,to:target.window_to,limit:"200"});
 return request<Result<TraceDetail>>(`/api/observability/trace?${params}`,{signal});
}
```

- [ ] **Step 6: Implement the drawer and hook selections into the URL**

Create `ui/host/src/dashboards/drill.tsx`:

```tsx
import {Alert,Button,Drawer,Group,Loader,Stack,Text,useComputedColorScheme} from "@mantine/core";
import {useQuery} from "@tanstack/react-query";
import {TraceLogs,Waterfall} from "./trace/trace-components";
import type {DashboardSpec,DashboardTime,VarValue} from "../../../panels/types";
import {getTrace,queryExemplars} from "./api";
import type {DrillTarget} from "./drill-state";
import {LogsViz} from "./viz/logs";
export type DrillProps={spec:DashboardSpec;time:DashboardTime;vars:Record<string,VarValue>;target?:DrillTarget;onChange(target?:DrillTarget):void};
export function DrillDrawer({spec,time,vars,target,onChange}:DrillProps){
 const dark=useComputedColorScheme("light")==="dark";const panel=spec.panels.find(p=>p.id===target?.panel_id);
 const directLogs=target?.kind==="logs"&&panel?.query?.from==="logs";
 const exemplars=useQuery({queryKey:["exemplars",spec,target,vars],queryFn:({signal})=>queryExemplars({dashboard:spec,panel_id:target!.panel_id,kind:directLogs?"logs":"traces",time:{from:target!.window_from,to:target!.window_to,refresh:"off"},from:target!.from,to:target!.to,dimensions:target!.dimensions,bucket:target!.bucket,vars},signal),enabled:Boolean(target&&panel&&!target.trace_id),retry:false});
 const trace=useQuery({queryKey:["drill-trace",target],queryFn:({signal})=>getTrace(target!,signal),enabled:Boolean(target?.trace_id),retry:false});
 return <Drawer opened={Boolean(target)} onClose={()=>onChange(undefined)} title={panel?`${panel.title} · ${target?.kind}`:"Panel selection unavailable"} position="right" size="xl">
  {!panel&&<Alert color="bad">This panel is no longer in the dashboard.</Alert>}
  {(exemplars.isFetching||trace.isFetching)&&<Loader aria-label="Loading drill data" size="sm"/>}
  {(exemplars.error||trace.error)&&<Alert color="bad">{(exemplars.error??trace.error)?.message}</Alert>}
  {directLogs&&exemplars.data?.logs&&panel&&<LogsViz panel={{...panel,viz:"logs"}} result={{id:panel.id,status:exemplars.data.logs.rows?"ok":"empty",frame:exemplars.data.logs,elapsed_ms:0,from_ms:Date.parse(target!.from),to_ms:Date.parse(target!.to)}} dark={dark} height={500}/>}
  {directLogs&&exemplars.data?.logs?.rows===0&&<Text c="dimmed">No logs match this selection.</Text>}
  {!directLogs&&!target?.trace_id&&exemplars.data&&<Stack gap="xs">
   {exemplars.data.traces.length===0&&<Text c="dimmed">No exemplar traces match this selection.</Text>}
   {exemplars.data.traces.map(t=><Button key={`${t.namespace}/${t.trace_id}`} variant="default" justify="space-between" onClick={()=>onChange({...target!,trace_id:t.trace_id,namespace:t.namespace})}>{t.service} · {t.operation} · {t.duration_ms.toFixed(1)} ms</Button>)}
   {exemplars.data.truncated&&<Text size="xs" c="dimmed">Showing at most 20 exemplar traces.</Text>}
  </Stack>}
  {trace.data&&<Stack gap="sm"><Group><Text fw={600}>{trace.data.data.trace_id}</Text><Text c={trace.data.data.has_error?"bad":"dimmed"}>{trace.data.data.has_error?"■ Error":"● OK"}</Text></Group>
   {trace.data.data.spans.length?<Waterfall spans={trace.data.data.spans} dark={dark} onSpan={()=>undefined}/>:<Text c="dimmed">No spans were found for this trace.</Text>}
   <Text fw={600}>Correlated logs</Text><TraceLogs entries={trace.data.data.logs}/>
   {trace.data.data.truncated&&<Alert color="warn">This trace is truncated: {trace.data.data.spans.length} of {trace.data.data.span_count} spans, {trace.data.data.services.length} of {trace.data.data.service_count} services.</Alert>}
  </Stack>}
 </Drawer>;
}
```

In page.tsx import DrillDrawer, makeDrill and parseDrill. Pass to PanelGrid:

```tsx
onPoint={(panel,selection)=>{
 const result=data.results.get(panel.id);if(!result)return;
 const target=makeDrill(panel,result,selection);if(!target)return;
 const first=Object.values(selection.dimensions)[0];const variable=panel.click?.set_variable;
 const vars=variable&&first!==undefined?{...search.vars,[variable]:first}:search.vars;
 onSearch({...search,vars,drill:JSON.stringify(target)},false);
}}
```

Render before the closing main Box:

```tsx
<DrillDrawer spec={spec} time={time} vars={resolvedVars} target={parseDrill(search.drill)} onChange={target=>onSearch({...search,drill:target?JSON.stringify(target):undefined},false)}/>
```

In RowPanel import makeDrill and replace its trace_id branch with:

```tsx
if(name==="trace_id"&&value){
 const target=makeDrill(props.panel,props.result,selection);if(!target)return <Text size="sm" ff="monospace">{String(value)}</Text>;
 const url=new URL(window.location.href);url.searchParams.set("drill",JSON.stringify(target));
 return <Anchor href={url.toString()} onClick={event=>{if(props.onPoint&&event.button===0&&!event.ctrlKey&&!event.metaKey&&!event.shiftKey&&!event.altKey){event.preventDefault();event.stopPropagation();props.onPoint(selection);}}}>{String(value)}</Anchor>;
}
```

In Grid's card factory, when a panel has drill and an onPoint handler, omit its onSelect callback; the page's onPoint performs click-variable and drill updates atomically. For other panels retain the existing click onSelect. Replace the factory's onSelect expression with:

```tsx
onSelect={panel.click && !(panel.drill && onPoint) ? value=>onVariable(panel.click!.set_variable,value) : undefined}
```

Service row links use onSelect only when provided. Table-format service_link receives the explicit variable callback in Task 13.

For table-wide drill, add `onPoint?: (selection:Selection)=>void` to TableViz and memoize `const model=useMemo(()=>rowModel(panel,result),[panel,result])`. Import rowModel from the pure rows module. Replace the firstDimension calculation with:

```tsx
const firstDimension=panel.query?.by?.length?frame.columns.findIndex(c=>c.role==="dimension"):panel.query&&frame.columns.some(c=>c.name==="service")?frame.columns.findIndex(c=>c.name==="service"):frame.columns.findIndex(c=>c.role==="dimension");
```

Define rowInteractive and activate beside that calculation:

```tsx
const rowInteractive=(index:number)=>Boolean(onSelect||onPoint&&(panel.click||panel.drill||model.selection(model.rows[index]).trace_id));
const activate=(index:number,original:Cell[])=>{
 if(!rowInteractive(index))return;
 onPoint?.(model.selection(model.rows[index]));
 const value=firstDimension>=0?String(original[firstDimension]??""):undefined;
 if(value!==undefined&&value!=="Other")onSelect?.(value);
};
```

Replace the complete Table.Tr expression in the existing row map with:

```tsx
<Table.Tr key={row.id} tabIndex={rowInteractive(row.index)?0:undefined} style={{cursor:rowInteractive(row.index)?"pointer":undefined}}
 onClick={event=>{if((event.target as Element).closest("a,button"))return;activate(row.index,row.original);}}
 onKeyDown={event=>{if(!rowInteractive(row.index)||(event.target as Element).closest("a,button"))return;if(event.key==="Enter"||event.key===" "){event.preventDefault();activate(row.index,row.original);}}}>
 {row.getAllCells().map((cell)=><Table.Td key={cell.id}><table.FlexRender cell={cell}/></Table.Td>)}
</Table.Tr>
```

Include onPoint in TableViz's props/destructuring. Pass `onPoint={onPoint}` from Viz's table case and `onPoint={props.onPoint}` from RowPanel. Trace links already stop propagation; row handlers additionally ignore nested links/buttons. Non-drill click panels retain their existing onSelect path.

- [ ] **Step 7: Run to see pass and build**

```bash
rtk just test ./internal/panel/... ./internal/api/... ./internal/observability/... -run '"TestM2DrillLogs|TestM2Exemplar|TestM2TraceAbsoluteWindow"'
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

Expected: the dashboard uses adapted workspace-local copies of the chat components; deep links reproduce selection/trace; changing/closing a drawer aborts its obsolete requests via React Query signals; zero-row and capped log/trace results remain explicit.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

Regenerate route/tool/setting reference docs before this task’s commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
```

- [ ] **Step 8: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel/exemplars.go internal/panel/rowpanels.go internal/panel/validate.go internal/panel/drill_logs_test.go internal/api/observability.go internal/api/observability_test.go cmd/fanout/main.go ui/host/src/dashboards internal/ui/dist internal/mcp/apps
rtk git commit -m "feat(ui): drill from panel selections into shared traces and logs"
```

---

### Task 11: Server deploy split and memoized annotation scope

**Files:** Create `internal/panel/deploy_split.go`, `deploy_split_test.go`, `annotation_scope.go`, `annotation_scope_test.go`; modify `spec.go`, `frame.go`, `validate.go`, `check.go`, `exec.go` (under internal/panel).

**Interfaces:** Define ColumnFormat, Options.Split/Columns, Frame.Note, AnnotationService/AnnotationMatch, AnnotationFilter, Checked.AnnotationFilters and Result.AnnotationScope/AnnotationError. The complete server code below produces runDeploySplit and annotationScope; ColumnFormat/Options.Columns TS mirrors were introduced in Task 7; the remaining UI mirrors follow in Task 12.

- [ ] **Step 1: Write failing engine tests**

Create `internal/panel/deploy_split_test.go`:

```go
package panel

import (
	"testing"
	"time"
)

func TestM2DeploySplitUsesEffectiveWindowAndRates(t *testing.T) {
	engine, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO version_rollup VALUES ('shop','checkout','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS),('shop','checkout','v2',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, at.Add(-time.Hour), at, at.Add(15*time.Minute), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Query.Measures = []string{"rate()"}
	d.Panels[0].Options = &Options{Split: "deploy"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Frame
	if got[0].Status != "ok" || f.Rows != 4 || f.Columns[1].Name != "period" {
		t.Fatalf("split: %+v", got)
	}
	for _, v := range f.Values[2] {
		if v.(float64) != 1.0/60 {
			t.Fatalf("rate used full window: %v", f.Values)
		}
	}
	d.Panels[0].Time = &PanelTime{Range: "15m"}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Frame.Note == "" || len(got[0].Frame.Columns) != 2 {
		t.Fatalf("no deploy: %+v %v", got, err)
	}
}
```

Create `internal/panel/annotation_scope_test.go`:

```go
package panel

import (
	"testing"
	"time"
)

func TestM2AnnotationScopeUsesCheckedAST(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	_, err := engine.DB.Exec(`INSERT INTO version_rollup VALUES ('shop','checkout','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS),('shop','payment','v1',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`, at, at, at, at)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	sig, _ := lookupSignal("spans")
	vars := map[string]Variable{"services": {Name: "services", Kind: "custom", Options: []string{"checkout", "payment"}, Multi: true, IncludeAll: true}}
	scope := Scope{Start: at, End: at.Add(time.Hour), Vars: map[string]Value{"services": {Values: []string{"checkout"}}}}
	p := &Panel{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans"}}
	for _, expr := range []string{"service IN $services", "service LIKE '%out'", "namespace = 'shop' AND service = 'checkout' AND http_route = '/cart'"} {
		f, err := checkFilter(t.Context(), engine, sig, vars, expr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got.Services) != 1 || got.Services[0].Service != "checkout" {
			t.Fatalf("%s: %+v", expr, got)
		}
	}
	f, err := checkFilter(t.Context(), engine, sig, vars, "service = 'checkout' OR http_route = '/cart'")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
	if err != nil || got != nil {
		t.Fatalf("mixed OR must remain unscoped for annotation service matching: %+v %v", got, err)
	}
	f, err = checkFilter(t.Context(), engine, sig, vars, "service IN $services")
	if err != nil {
		t.Fatal(err)
	}
	scope.Vars["services"] = Value{All: true}
	got, err = e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
	if err != nil || got != nil {
		t.Fatalf("All: %+v %v", got, err)
	}
	scope.Vars["services"] = Value{Values: []string{}}
	got, err = e.annotationScope(t.Context(), p, projectedFilters(t, engine, sig, vars, f), scope)
	if err != nil || got == nil || len(got.Services) != 0 {
		t.Fatalf("empty list: %+v %v", got, err)
	}
}
func projectedFilters(t *testing.T, parser Parser, sig *signal, vars map[string]Variable, f Filter) []AnnotationFilter {
	t.Helper()
	projected, err := projectAnnotationFilter(t.Context(), parser, sig, vars, f)
	if err != nil {
		t.Fatal(err)
	}
	if projected == nil {
		return nil
	}
	return []AnnotationFilter{*projected}
}
func TestM2AnnotationScopeRunPanelAndAllSources(t *testing.T) {
	engine, _ := newTestEngine(t)
	at := fixtureStart
	if _, err := engine.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'checkout',1,1,1,2,0,0,0)`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DB.Exec(`INSERT INTO anomaly_log VALUES ('shop','anomalyonly','latency',?::TIMESTAMP_NS::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,'Slow','warn')`, at, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return at.Add(time.Hour) }
	d := Dashboard{Name: "Scope", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "5m", Where: []string{"namespace = 'shop'"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	scope := got[0].AnnotationScope
	if scope == nil || !scope.NamespaceScoped || len(scope.Services) != 2 || scope.Services[0].Service != "anomalyonly" || scope.Services[1].Service != "checkout" {
		t.Fatalf("runPanel not wired or keys incomplete: %+v", got)
	}
}
```

```bash
rtk just test ./internal/panel/... -run '"TestM2DeploySplit|TestM2AnnotationScope|TestM2CheckOperational|TestM2AnnotationProjectionMemoized"'
```

- [ ] **Step 2: Implement split and checked annotation projection**

Add to Go Options and Frame:

```go
// Options fields:
Split   string         `json:"split,omitempty" jsonschema:"bar: deploy, comparing before and since the latest deploy"`
Columns []ColumnFormat `json:"columns,omitempty" jsonschema:"table column formats"`
// Frame field:
Note string `json:"note,omitempty"`
```

Define alongside Options:

```go
type ColumnFormat struct {
	Field    string `json:"field"`
	Format   string `json:"format" jsonschema:"unit, bar, status, sparkline, trace_link, service_link or log_template"`
	Unit     string `json:"unit,omitempty"`
	Variable string `json:"variable,omitempty" jsonschema:"service_link: query or custom variable to select"`
}
```

Create `internal/panel/deploy_split.go`:

```go
package panel

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/annotations"
)

func (e *Executor) runDeploySplit(ctx context.Context, p *Panel, checked *Checked, scope Scope) (*Frame, string, error) {
	copyPanel := *p
	copyOptions := *p.Options
	copyOptions.Split = ""
	copyPanel.Options = &copyOptions
	request := annotations.Request{From: scope.Start, To: scope.End}
	seen := map[string]string{}
	for _, f := range checked.Filters[p.ID] {
		if scope.dropped(f) {
			if f.EqField == "service" {
				frame, text, err := e.runScope(ctx, &copyPanel, checked, scope)
				if frame != nil {
					frame.Note = "Service is All; showing the unsplit whole-window frame."
				}
				return frame, text, err
			}
			continue
		}
		if f.EqField == "service" || f.EqField == "namespace" {
			field, value, err := rollupFilterValue(ctx, e.engine, f, scope)
			if err != nil {
				return nil, "", err
			}
			if old, ok := seen[field]; ok && old != value {
				return nil, "", fmt.Errorf("deploy split scope equalities disagree")
			}
			seen[field] = value
			if field == "namespace" {
				request.Namespace = value
			} else {
				request.Services = []string{value}
			}
		}
	}
	if len(request.Services) != 1 {
		return nil, "", fmt.Errorf("deploy split requires one active service equality")
	}
	history, err := annotations.New(e.engine).Read(ctx, request)
	if err != nil {
		return nil, "", err
	}
	var latest *annotations.Deploy
	for i := range history.Deploys {
		at := history.Deploys[i].At
		if at.After(scope.Start) && at.Before(scope.End) && (latest == nil || at.After(latest.At)) {
			latest = &history.Deploys[i]
		}
	}
	if latest == nil {
		f, text, err := e.runScope(ctx, &copyPanel, checked, scope)
		if f != nil {
			f.Note = "No deploy in this panel's time range; showing the whole window."
		}
		return f, text, err
	}
	before, after := scope, scope
	before.End = latest.At
	after.Start = latest.At
	a, sqlA, err := e.runScope(ctx, &copyPanel, checked, before)
	if err != nil {
		return nil, sqlA, err
	}
	b, sqlB, err := e.runScope(ctx, &copyPanel, checked, after)
	if err != nil {
		return nil, sqlB, err
	}
	columns := append([]Column{a.Columns[0], {Name: "period", Type: "string", Role: "dimension"}}, a.Columns[1:]...)
	f := newFrame(columns)
	f.Truncated = a.Truncated || b.Truncated || history.Truncated
	for _, part := range []struct {
		frame *Frame
		label string
	}{{a, "Before deploy"}, {b, "Since deploy"}} {
		for r := 0; r < part.frame.Rows; r++ {
			row := []any{part.frame.Values[0][r], part.label}
			for i := 1; i < len(part.frame.Columns); i++ {
				row = append(row, part.frame.Values[i][r])
			}
			appendPanelRow(f, row...)
		}
	}
	f.Note = "Split at " + latest.At.Format("2006-01-02T15:04:05.999999999Z07:00") + " · " + latest.Service + " " + latest.Version
	return f, sqlA + "; " + sqlB, nil
}
func validateDisplayOptions(p *Panel, path string, vars map[string]Variable, problems *Problems) {
	if p.Options == nil {
		return
	}
	if p.Options.Split != "" && (p.Options.Split != "deploy" || p.Viz != "bar" || p.Query == nil || len(p.Query.By) != 1 || len(p.Query.Measures) != 1) {
		problems.addHint(path+".options.split", "deploy split needs a structured bar with one category and one measure", "use a separate panel per measure")
	}
	if len(p.Options.Columns) > 30 {
		problems.add(path+".options.columns", "at most 30 column formats")
	}
	if len(p.Options.Columns) > 0 && p.Viz != "table" {
		problems.add(path+".options.columns", "column formats apply to table panels")
	}
	seen := map[string]bool{}
	for _, c := range p.Options.Columns {
		if c.Field == "" || seen[c.Field] {
			problems.add(path+".options.columns", "column fields must be nonempty and unique")
		}
		seen[c.Field] = true
		switch c.Format {
		case "unit", "bar", "status", "sparkline", "trace_link", "service_link", "log_template":
		default:
			problems.add(path+".options.columns", "unknown column format")
		}
		if c.Unit != "" {
			if _, ok := unitFamilies[c.Unit]; !ok {
				problems.add(path+".options.columns", "unknown column unit")
			}
		}
		if c.Format == "service_link" {
			v, ok := vars[c.Variable]
			if !ok || (v.Kind != "query" && v.Kind != "custom") {
				problems.add(path+".options.columns", "service_link requires a query or custom variable")
			}
		}
		if p.Query != nil {
			sig, ok := lookupSignal(p.Query.From)
			if !ok {
				continue
			}
			columns := map[string]bool{}
			for _, by := range p.Query.By {
				ref, err := sig.field(by)
				if err == nil {
					columns[ref.alias()] = true
				}
			}
			var ignored Problems
			for _, m := range parseMeasures(sig, p.Query.Measures, "", &ignored) {
				columns[m.Alias] = true
			}
			if !columns[c.Field] {
				problems.add(path+".options.columns", "formatted field does not exist in the structured frame")
			}
		}
	}
}
```

At the start of runScope after applying the read window and before structured dispatch:

```go
if p.Options != nil && p.Options.Split == "deploy" {
	return e.runDeploySplit(ctx, p, checked, scope)
}

```

Call `validateDisplayOptions(p,path,vars,problems)` beside the other viz validators. In Check, for split panels reject a filter referencing service/namespace unless it is a representable equality (use the existing parsed AST's `fieldText` walker to identify references, not a substring): add the following helper in deploy_split.go and call it after every checked filter is appended:

```go
func checkDeployScope(ctx context.Context, parser Parser, p *Panel, f Filter) error {
	if p.Options == nil || p.Options.Split != "deploy" {
		return nil
	}
	tree, err := parser.ParseSQL(ctx, "SELECT 1 FROM spans WHERE ("+f.Source+")")
	if err != nil {
		return err
	}
	scoped := false
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if v["class"] == "COLUMN_REF" && (fieldText(v) == "service" || fieldText(v) == "namespace") {
				scoped = true
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(tree["where_clause"])
	if !scoped {
		return nil
	}
	if f.EqField != "service" && f.EqField != "namespace" {
		return Problems{{Path: "options.split", Message: "deploy split requires standalone service/namespace equalities"}}
	}
	sample := Scope{Vars: map[string]Value{}}
	for _, name := range f.Params {
		sample.Vars[name] = Value{Values: []string{"check"}}
	}
	_, _, err = rollupFilterValue(ctx, parser, f, sample)
	return err
}
```

Call site in Check:

```go
if err := checkDeployScope(ctx, parser, p, f); err != nil {
	if isOperational(err) {
		return nil, nil, err
	}
	problems.addHint(path+".options.split", SafeError(err), "use standalone service and optional namespace equalities")
}

```

After the panel’s entire filter loop in Check, require the service equality even when query.where is absent:

```go
if p.Options != nil && p.Options.Split == "deploy" {
	found := false
	for _, f := range checked.Filters[p.ID] {
		if f.EqField == "service" {
			found = true
		}
	}
	if !found {
		problems.addHint(path+".options.split", "deploy split requires a service equality filter", "add service = 'name' or service = $service")
	}
}

```

Add to deploy_split_test.go (import errors and slices):

```go
func TestM2DeploySplitAllFallsBackAndRequiresEquality(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Options = &Options{Split: "deploy"}
	d.Variables[0].IncludeAll = true
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d, Vars: map[string]Value{"service": {All: true}}})
	if err != nil || got[0].Frame == nil || got[0].Frame.Note != "Service is All; showing the unsplit whole-window frame." || len(got[0].Frame.Columns) != 2 {
		t.Fatalf("All: %+v %v", got, err)
	}
	d.Panels[0].Query.Where = nil
	err = e.Validate(t.Context(), &d)
	var problems Problems
	want := Problem{Path: "panels[0].options.split", Message: "deploy split requires a service equality filter", Hint: "add service = 'name' or service = $service"}
	if !errors.As(err, &problems) || !slices.Contains(problems, want) {
		t.Fatalf("got %v want %+v", err, want)
	}
}
```

Create `internal/panel/annotation_scope.go`. Derive service/namespace restrictions from the already checked AST, including IN, LIKE, casts, variables and conjunctions; never use a browser regex to interpret SQL:

```go
package panel

import (
	"context"
	"fmt"
	"strings"
)

type AnnotationService struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
}
type AnnotationMatch struct {
	Services        []AnnotationService `json:"services"`
	NamespaceScoped bool                `json:"namespace_scoped,omitempty"`
	Limited         bool                `json:"limited,omitempty"`
}

func annotationOnly(n any) bool {
	switch v := n.(type) {
	case map[string]any:
		if v["class"] == "COLUMN_REF" {
			name := fieldText(v)
			return name == "service" || name == "namespace"
		}
		for _, child := range v {
			if !annotationOnly(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !annotationOnly(child) {
				return false
			}
		}
	}
	return true
}
func annotationNamespace(n any) bool {
	switch v := n.(type) {
	case map[string]any:
		if v["class"] == "COLUMN_REF" && fieldText(v) == "namespace" {
			return true
		}
		for _, child := range v {
			if annotationNamespace(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if annotationNamespace(child) {
				return true
			}
		}
	}
	return false
}

// exact=false means another signal field was omitted. AND can retain known
// necessary restrictions; OR and NOT containing unknown fields cannot.
func annotationPredicate(n any) (any, bool) {
	if annotationOnly(n) {
		return n, true
	}
	v, ok := n.(map[string]any)
	if !ok || v["class"] != "CONJUNCTION" || v["type"] != "CONJUNCTION_AND" {
		return nil, false
	}
	children, _ := v["children"].([]any)
	kept := []any{}
	exact := true
	for _, child := range children {
		projected, complete := annotationPredicate(child)
		exact = exact && complete
		if projected != nil {
			kept = append(kept, projected)
		}
	}
	if len(kept) == 0 {
		return nil, false
	}
	if len(kept) == 1 {
		return kept[0], exact
	}
	clone := map[string]any{}
	for key, value := range v {
		clone[key] = value
	}
	clone["children"] = kept
	return clone, exact
}

type AnnotationFilter struct {
	Filter          Filter
	NamespaceScoped bool
}

func projectAnnotationFilter(ctx context.Context, parser Parser, sig *signal, vars map[string]Variable, f Filter) (*AnnotationFilter, error) {
	tree, err := parser.ParseSQL(ctx, "SELECT 1 FROM "+sig.name+" WHERE ("+f.Source+")")
	if err != nil {
		return nil, err
	}
	projected, _ := annotationPredicate(tree["where_clause"])
	if projected == nil {
		return nil, nil
	}
	namespaceScoped := annotationNamespace(projected)
	tree["where_clause"] = projected
	rendered, err := parser.RenderSQL(ctx, tree)
	if err != nil {
		return nil, err
	}
	prefix := "SELECT 1 FROM " + sig.name + " WHERE "
	if !strings.HasPrefix(rendered, prefix) {
		return nil, fmt.Errorf("annotation scope could not be rendered")
	}
	checked, err := checkFilter(ctx, parser, sig, vars, strings.TrimPrefix(rendered, prefix))
	if err != nil {
		return nil, err
	}
	return &AnnotationFilter{Filter: checked, NamespaceScoped: namespaceScoped}, nil
}
func (e *Executor) annotationScope(ctx context.Context, p *Panel, filters []AnnotationFilter, scope Scope) (*AnnotationMatch, error) {
	clauses := []string{}
	args := []any{}
	namespaceScoped := false
	for _, projected := range filters {
		f := projected.Filter
		if scope.dropped(f) {
			continue
		}
		empty := false
		for _, name := range f.Params {
			if v := scope.Vars[name]; !v.All && len(v.Values) == 0 {
				empty = true
			}
		}
		if empty {
			clauses = append(clauses, "FALSE")
			continue
		}
		predicate, _, bound, err := bindParams(f.Expr, scope.lookup(f))
		if err != nil {
			return nil, err
		}
		namespaceScoped = namespaceScoped || projected.NamespaceScoped
		clauses = append(clauses, "("+predicate+")")
		args = append(args, bound...)
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	out := &AnnotationMatch{Services: []AnnotationService{}, NamespaceScoped: namespaceScoped}
	rows, err := e.engine.QueryContext(ctx, `WITH keys AS (
 SELECT namespace,service FROM version_rollup UNION SELECT namespace,service FROM anomaly_log UNION SELECT namespace,service FROM service_rollup)
SELECT DISTINCT namespace,service FROM keys WHERE `+strings.Join(clauses, " AND ")+` ORDER BY namespace,service LIMIT 1001`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item AnnotationService
		if err := rows.Scan(&item.Namespace, &item.Service); err != nil {
			return nil, err
		}
		out.Services = append(out.Services, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Services) > 1000 {
		out.Services = out.Services[:1000]
		out.Limited = true
	}
	return out, nil
}
```

Add `AnnotationFilters map[string][]AnnotationFilter` to Checked and initialize it in Check’s existing composite literal. In Check, once per checked time-panel filter, compute its projected predicate and store it inside this memoized Checked:

```go
if p.Viz == "timeseries" || p.Viz == "heatmap" || p.Viz == "state_timeline" {
	projected, err := projectAnnotationFilter(ctx, parser, sig, earlier, f)
	if err != nil {
		if isOperational(err) {
			return nil, nil, err
		}
		problems.addHint(fmt.Sprintf("%s.query.where[%d]", path, j), SafeError(err), "use a supported service or namespace predicate")
	} else if projected != nil {
		checked.AnnotationFilters[p.ID] = append(checked.AnnotationFilters[p.ID], *projected)
	}
}

```

`annotationScope` performs no ParseSQL or RenderSQL on refresh. Each projected filter has its own Params/InParams, so an omitted route variable cannot drop a retained service restriction.

Add the two Result fields specified in Interfaces and their TS mirrors. Immediately after `res.Frame, res.SQL = frame, sqlText` in runPanel, insert:

```go
if p.Query != nil && (p.Viz == "timeseries" || p.Viz == "heatmap" || p.Viz == "state_timeline") {
	res.AnnotationScope, err = e.annotationScope(ctx, p, checked.AnnotationFilters[p.ID], scope)
	if err != nil {
		res.AnnotationError = "Annotation scope is unavailable."
	}
}

```

This supplementary read resolves the union of version_rollup, anomaly_log and service_rollup keys under the panel deadline. It never scans telemetry and never discards the completed frame. SQL time panels have no structured service filter and receive unscoped markers. A limited scope is explicitly disclosed by PanelCard, along with AnnotationError; a scope error disables markers on that panel rather than drawing incorrect service events.

Add to annotation_scope_test.go (import context and errors):

```go
type failExtraParse struct {
	Parser
	parses  int
	failure error
}

func (p *failExtraParse) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	p.parses++
	if p.parses == 3 {
		return nil, p.failure
	}
	return p.Parser.ParseSQL(ctx, text)
}
func TestM2CheckOperationalFailures(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, p := range []Panel{
			{ID: "p", Title: "P", Viz: "health", Query: &Query{From: "spans", Where: []string{"service = 'checkout'"}}},
			{ID: "p", Title: "P", Viz: "bar", Options: &Options{Split: "deploy"}, Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"operation"}, Where: []string{"service = 'checkout'"}}},
			{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto", Where: []string{"service = 'checkout'"}}},
		} {
			d := Dashboard{Name: "Operational", Panels: []Panel{p}}
			Normalize(&d)
			parser := &failExtraParse{Parser: engine, failure: failure}
			_, problems, err := Check(t.Context(), parser, &d)
			if !errors.Is(err, failure) || len(problems) != 0 || parser.parses != 3 {
				t.Fatalf("%s converted operational error to Problems: %+v %v calls=%d", p.Viz, problems, err, parser.parses)
			}
		}
	}
}
```

Also pin annotation projection memoization with the actual Executor.check cache (no runtime parse):

```go
type annotationParseCounter struct {
	Engine
	parses int
}

func (p *annotationParseCounter) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	p.parses++
	return p.Engine.ParseSQL(ctx, text)
}
func TestM2AnnotationProjectionMemoized(t *testing.T) {
	e := newFixtureExecutor(t)
	counter := &annotationParseCounter{Engine: e.engine}
	e.engine = counter
	d := Dashboard{Name: "Memoized", Panels: []Panel{{ID: "p", Title: "P", Viz: "timeseries", Query: &Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto", Where: []string{"service = 'checkout'"}}}}}
	Normalize(&d)
	first, err := e.check(t.Context(), &d)
	if err != nil {
		t.Fatal(err)
	}
	count := counter.parses
	second, err := e.check(t.Context(), &d)
	if err != nil || first != second || count == 0 || counter.parses != count {
		t.Fatalf("memoization: %v count=%d now=%d", err, count, counter.parses)
	}
	scope := Scope{Start: e.now().Add(-time.Hour), End: e.now(), Vars: map[string]Value{}}
	for i := 0; i < 2; i++ {
		if _, err := e.annotationScope(t.Context(), &d.Panels[0], first.AnnotationFilters["p"], scope); err != nil {
			t.Fatal(err)
		}
	}
	if counter.parses != count {
		t.Fatalf("runtime reparsed annotation predicate: %d -> %d", count, counter.parses)
	}
}
```

The existing checkFilter performs two ParseSQL calls; the third is each new rollup/split/projection path. This test injects the error only into that new path.

- [ ] **Step 3: Run to see pass and review**

```bash
rtk just test ./internal/panel/...
```

Expected: all owning regression and integration tests pass.

Regenerate the changed panel/tool contract references, then format all Go changes before the controller commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

- [ ] **Step 4: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel
rtk git commit -m "feat(panels): add deploy split and memoized annotation scopes"
```

---

### Task 12: Browser annotation markers and the S8 refresh

**Files:** Create `ui/panels/annotations.ts`, `ui/host/src/dashboards/refresh.ts`, `annotation-refresh.test.tsx`, `annotation-markers.test.tsx`, `use-panel-results.test.tsx`; modify `internal/annotations/service.go`, `internal/query/annotations_test.go`, `ui/panels/types.ts`, dashboard api/use-panel-results/query-policy/page/grid/panel-card/echart-canvas and time renderers; generated embedded bundles.

**Interfaces:** Define AnnotationBody, AnnotationsResponse, queryAnnotations, refreshDashboard and withAnnotations. One batch-plus-annotation bundle feeds all time panels; Task 11’s checked scope is authoritative.

- [ ] **Step 1: Write failing marker and refresh tests**

Create `ui/host/src/dashboards/annotation-refresh.test.tsx`:

```tsx
import {describe,expect,it,vi} from "vitest";
const mock=vi.hoisted(()=>({panels:vi.fn(),annotations:vi.fn()}));
vi.mock("./api",()=>({queryPanels:mock.panels,queryAnnotations:mock.annotations}));
import {refreshDashboard} from "./refresh";
import type {QueryBody} from "./api";
const body:QueryBody={dashboard:{version:1,name:"D",time:{range:"1h"},panels:[{id:"a",title:"A",viz:"timeseries",query:{from:"spans",measures:["count()"]}},{id:"b",title:"B",viz:"timeseries",time:{shift:"1d"},query:{from:"spans",measures:["count()"]}}]},time:{range:"1h"},vars:{service:"checkout"}};
describe("S8 annotations",()=>{
 it("makes one panel batch plus one annotation request for a refresh",async()=>{
  mock.panels.mockResolvedValue([{id:"a",status:"ok",elapsed_ms:1,from_ms:100000,to_ms:200000},{id:"b",status:"ok",elapsed_ms:1,from_ms:0,to_ms:100000}]);mock.annotations.mockResolvedValue({deploys:[],anomalies:[]});
  await refreshDashboard(body);expect(mock.panels).toHaveBeenCalledTimes(1);expect(mock.annotations).toHaveBeenCalledTimes(1);
  expect(mock.annotations.mock.calls[0][0].from).toBe(new Date(0).toISOString());expect(mock.annotations.mock.calls[0][0].to).toBe(new Date(200000).toISOString());
 });
 it("keeps successful panels if annotations fail",async()=>{
  mock.panels.mockResolvedValue([{id:"a",status:"ok",elapsed_ms:1,from_ms:1,to_ms:2}]);mock.annotations.mockRejectedValue(new Error("Unavailable"));
  const got=await refreshDashboard(body);expect(got.results[0].status).toBe("ok");expect(got.annotation_error).toBe("Unavailable");
 });
});
```

Create `ui/host/src/dashboards/annotation-markers.test.tsx`:

```tsx
import {describe,expect,it} from "vitest";
import {withAnnotations} from "../../../panels/annotations";
import {chartThemeFor} from "../../../panels/compile";
import type {Panel,PanelResult} from "../../../panels/types";
const panel:Panel={id:"p",title:"P",viz:"timeseries",query:{from:"spans",where:["service = $service"],measures:["count()"]}};
const result:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:0,to_ms:10000,annotation_scope:{services:[{namespace:"shop",service:"checkout"}]}};
describe("annotations and formats",()=>{
 it("draws matching deploy lines and anomaly bands, retaining thresholds",()=>{
  const option={series:[{type:"line",markLine:{data:[{yAxis:3}]}}]};
  const annotations={deploys:[{namespace:"shop",service:"checkout",version:"v2",at:new Date(1000).toISOString()},{namespace:"shop",service:"frontend",version:"v3",at:new Date(2000).toISOString()}],anomalies:[{namespace:"shop",service:"checkout",kind:"latency",from:new Date(3000).toISOString(),to:new Date(5000).toISOString(),title:"Slow",severity:"bad"}]};
  const got=withAnnotations(option,panel,result,annotations,{service:"checkout"},chartThemeFor(false));
  const series=(got.series as {markLine:{data:unknown[]};markArea:{data:unknown[]}}[])[0];expect(series.markLine.data).toHaveLength(2);expect(series.markArea.data).toHaveLength(1);
 });
});
```
```bash
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/annotation-refresh.test.tsx src/dashboards/annotation-markers.test.tsx'
```

- [ ] **Step 2: Implement one refresh bundle, hook integration and matching markers**

Use Task 7's existing ColumnFormat and options.columns mirrors. Add TS mirror fields (`options.split?:"deploy"`, `frame.note?:string`, `annotation_scope?:{services:{namespace:string;service:string}[];namespace_scoped?:boolean;limited?:boolean}`, `annotation_error?:string`). Define these pure types in ui/panels/annotations.ts and import them in api.ts:

```ts
export type AnnotationBody={from:string;to:string;services?:string[];namespace?:string};
export type Deploy={namespace:string;service:string;version:string;at:string};
export type Anomaly={namespace:string;service:string;kind:string;from:string;to:string;title:string;severity:string};
export type AnnotationsResponse={deploys:Deploy[];anomalies:Anomaly[];truncated?:boolean};
```

Append the API client:

```ts
export const queryAnnotations=(body:AnnotationBody,signal?:AbortSignal)=>request<AnnotationsResponse>("/api/annotations",{method:"POST",json:body,signal});
```

Create refresh.ts:

```ts
import {queryAnnotations,queryPanels,type QueryBody} from "./api";
import type {AnnotationsResponse} from "../../../panels/annotations";
export async function refreshDashboard(body:QueryBody,signal?:AbortSignal){
 const results=await queryPanels(body,signal);
 const windows=results.filter(r=>r.from_ms!==undefined&&r.to_ms!==undefined&&r.from_ms<r.to_ms);
 if(windows.length===0||body.dashboard.annotations?.deploys===false&&body.dashboard.annotations?.anomalies===false)return {results};
 const from=Math.min(...windows.map(r=>r.from_ms!)),to=Math.max(...windows.map(r=>r.to_ms!));
 try{
  const annotations:AnnotationsResponse=await queryAnnotations({from:new Date(from).toISOString(),to:new Date(to).toISOString()},signal);
  return {results,annotations};
 }catch(error){if(signal?.aborted)throw error;return {results,annotation_error:error instanceof Error?error.message:"Annotations unavailable"};}
}
```

Use the union of windows once, with no service restriction: every panel still filters the returned records to its own scope. This avoids a global service variable incorrectly suppressing annotations for panels that use a different service. Request returns at most 1000 deploys and 1000 anomalies. Extend Task 1's Request range validation to `430*24*time.Hour` (400-day maximum shift plus the 30-day maximum panel range); the retained history and output limits remain unchanged. Change its ErrRequest message accordingly. This permits shifted windows in one S8 request without extending stored history. Set ErrRequest to `annotations need a positive range of at most 430 days and at most 100 services`.

Append these native-engine Go tests to `internal/query/annotations_test.go` (add `errors` to its imports; reuse Task 1's versionEngine):

```go
func TestM2AnnotationWidenedRangeAccepted(t *testing.T) {
	d, _ := versionEngine(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, days := range []int{31, 430} {
		got, err := annotations.New(d).Read(t.Context(), annotations.Request{From: from, To: from.Add(time.Duration(days)*24*time.Hour)})
		if err != nil || len(got.Deploys) != 0 || len(got.Anomalies) != 0 {
			t.Fatalf("%d-day range: %+v %v", days, got, err)
		}
	}
}

func TestM2AnnotationWidenedRangeRejected(t *testing.T) {
	d, _ := versionEngine(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	_, err := annotations.New(d).Read(t.Context(), annotations.Request{From: from, To: from.Add(430*24*time.Hour + time.Nanosecond)})
	const want = "annotations need a positive range of at most 430 days and at most 100 services"
	if !errors.Is(err, annotations.ErrRequest) || err.Error() != want {
		t.Fatalf("range just over 430 days: want ErrRequest %q, got %v", want, err)
	}
}
```

Run `rtk just test ./internal/query/... -run '"TestM2AnnotationWidenedRange"'` before widening the validation to prove failure, then rerun it after the change to prove pass.

In use-panel-results.ts import refreshDashboard. Replace queryPanels in queryFn with refreshDashboard. Keep query's payload as a bundle; in every snapshot `merge` invocation and in panelError lookup use `query.data?.results` in place of query.data. Return `annotations: query.isPlaceholderData ? undefined : query.data?.annotations` and `annotationError: query.isPlaceholderData ? undefined : query.data?.annotation_error` beside its existing results. Do not change query keys, visibility or background polling. Add `annotations:spec.annotations` to panelContent so switching the flags changes refresh input. Manual refetch and interval use the same queryFn; annotation failure never discards finished panels.

Create `ui/host/src/dashboards/use-panel-results.test.tsx`, an integration test of the real hook with React Query and the real refreshDashboard (only HTTP clients are mocked):

```tsx
import {act} from "react";
import {createRoot} from "react-dom/client";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {expect,it,vi} from "vitest";
import {usePanelResults} from "./use-panel-results";
import type {DashboardSpec} from "../../../panels/types";
const wire=vi.hoisted(()=>({panels:vi.fn(),annotations:vi.fn()}));
vi.mock("./api",()=>({queryPanels:wire.panels,queryAnnotations:wire.annotations}));
it("S8 integrates one annotation request into every 20-panel manual refresh",async()=>{
 const spec:DashboardSpec={version:1,name:"S8",time:{range:"1h"},panels:Array.from({length:20},(_,i)=>({id:`p_${i}`,title:`P ${i}`,viz:"timeseries",query:{from:"spans",measures:["count()"]}}))};
 wire.panels.mockResolvedValue(spec.panels.map(p=>({id:p.id,status:"ok",elapsed_ms:1,from_ms:1000,to_ms:2000,frame:{columns:[{name:"time",type:"time",role:"time"},{name:"count",type:"number",role:"measure"}],values:[[1000],[1]],rows:1}})));wire.annotations.mockResolvedValue({deploys:[],anomalies:[]});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const node=document.createElement("div");document.body.append(node);const root=createRoot(node);let current:ReturnType<typeof usePanelResults>;
 function Host(){current=usePanelResults({dashboardId:"d",version:1,spec,time:spec.time,vars:{},compare:false,widths:{},visible:spec.panels.map(p=>p.id),refresh:"off"});return null;}
 await act(async()=>{root.render(<QueryClientProvider client={client}><Host/></QueryClientProvider>);});
 async function settled(expected:number){const until=Date.now()+2000;while((wire.annotations.mock.calls.length<expected||current!.fetching)&&Date.now()<until){await act(async()=>{await new Promise(resolve=>setTimeout(resolve,10));});}expect(current!.fetching).toBe(false);expect(wire.panels).toHaveBeenCalledTimes(expected);expect(wire.annotations).toHaveBeenCalledTimes(expected);expect(current!.results.size).toBe(20);}
 await settled(1);for(let i=2;i<=4;i++){await act(async()=>current!.refetch());await settled(i);}
 wire.annotations.mockRejectedValueOnce(new Error("Unavailable"));await act(async()=>current!.refetch());await settled(5);expect(current!.annotationError).toBe("Unavailable");expect(current!.results.get("p_0")?.status).toBe("ok");
 await act(async()=>root.unmount());client.clear();node.remove();
});
```

Complete ui/panels/annotations.ts after its types:

```ts
import type {ChartTheme} from "./compile";
import type {Panel,PanelResult,VarValue} from "./types";
export function withAnnotations(option:Record<string,unknown>,panel:Panel,result:PanelResult,annotations:AnnotationsResponse,_vars:Record<string,VarValue>,theme:ChartTheme):Record<string,unknown>{
 if(!["timeseries","heatmap","state_timeline"].includes(panel.viz))return option;
 const scope=result.annotation_scope;
 if(result.annotation_error)return option;
 const matches=(a:{service:string;namespace:string})=>!scope||scope.services.some(s=>s.service===a.service&&(s.namespace===a.namespace||a.namespace===""&&!scope.namespace_scoped));
 const from=result.from_ms??-Infinity,to=result.to_ms??Infinity;
 const deploys=annotations.deploys.filter(matches).filter(a=>Date.parse(a.at)>=from&&Date.parse(a.at)<to).map(a=>({xAxis:Date.parse(a.at),name:`${a.service} ${a.version}`,label:{formatter:`${a.service} ${a.version}`,color:theme.muted},lineStyle:{type:"dashed",color:theme.muted,width:1},tooltip:{formatter:()=>`${a.service} · ${a.version} · ${a.at}`}}));
 const anomalies=annotations.anomalies.filter(matches).filter(a=>Date.parse(a.to)>from&&Date.parse(a.from)<to).map(a=>[{xAxis:Math.max(from,Date.parse(a.from)),name:a.title,itemStyle:{color:a.severity==="bad"?theme.status.bad:theme.status.warn,opacity:.08},label:{show:false},tooltip:{formatter:()=>`${a.service} · ${a.title} · ${a.severity}`}}, {xAxis:Math.min(to,Date.parse(a.to))}]);
 const series=(option.series??[]) as Record<string,unknown>[];
 if(!series.length)return option;
 return {...option,tooltip:{...(option.tooltip as Record<string,unknown>??{}),renderMode:"richText"},series:series.map((s,i)=>{
  if(i!==0)return s;const markLine=(s.markLine??{}) as {data?:unknown[]};const markArea=(s.markArea??{}) as {data?:unknown[]};
  return {...s,markLine:{...markLine,silent:false,symbol:["none","none"],data:[...(markLine.data??[]),...deploys]},markArea:{...markArea,silent:false,data:[...(markArea.data??[]),...anomalies]}};
 })};
}
```

Add MarkAreaComponent to EChartCanvas registration and mocks. Extend AnalysisProps, Viz, PanelCard and GridProps with `annotations?:AnnotationsResponse; vars?:Record<string,VarValue>` where needed; GridProps already has vars. Thread `data.annotations` from Loaded through PanelGrid/card/Viz to the time compilers. In TimeseriesViz and AnalysisChart's useMemo, wrap their options with withAnnotations when supplied, and include annotations/vars in their dependencies. Respect dashboard flags by passing `{...data.annotations,deploys:spec.annotations?.deploys===false?[]:data.annotations.deploys,anomalies:spec.annotations?.anomalies===false?[]:data.annotations.anomalies}` from Loaded, memoized on those inputs.

Render `data.annotationError` as an Alert with title "Annotations unavailable", and `data.annotations?.truncated` as "Annotation history is limited." in the page header. Add this exact footer after the existing stale/truncated footer in PanelCard:

```tsx
{result?.frame?.note&&<Text size="xs" c="dimmed" role="status">{result.frame.note}</Text>}
{result?.annotation_error&&<Text size="xs" c="warn" role="status">{result.annotation_error}</Text>}
{result?.annotation_scope?.limited&&<Text size="xs" c="warn" role="status">Annotation service scope is limited.</Text>}
```

This exposes missing-deploy fallback and bounded matching.

- [ ] **Step 3: Run to see pass and review**

```bash
rtk just test ./internal/panel/... ./internal/query/... ./internal/annotations/... ./internal/api/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

Expected: all owning regression and integration tests pass.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

- [ ] **Step 4: Commit (controller)**

```bash
rtk git add internal/annotations/service.go internal/query/annotations_test.go ui/panels ui/host/src/dashboards internal/ui/dist internal/mcp/apps
rtk git commit -m "feat(ui): share annotations across dashboard refreshes"
```

---

### Task 13: Table column formats, executor trends and stat sparkline

**Files:** Create `internal/panel/table_trends.go`, `table_trends_test.go`, `ui/panels/column-formats.ts`, `ui/host/src/dashboards/table-formats.test.tsx`; modify `internal/panel/frame.go`, `exec.go`, `deploy_split.go`; `ui/panels/types.ts` and dashboard table/stat/index/grid/panel-card; generated embedded bundles.

**Interfaces:** Frame.Trends and attachTableTrends produce bounded per-row numeric-measure series. The seven formats consume ColumnFormat from Task 7; numeric statuses use thresholds and direction. Stat totals and trends preserve the same panel window.

- [ ] **Step 1: Write failing format and executor tests**

Create `ui/host/src/dashboards/table-formats.test.tsx`:

```tsx
import {describe,expect,it} from "vitest";
import {columnDisplay} from "../../../panels/column-formats";
import {chartThemeFor} from "../../../panels/compile";
import {sparkline,statValue} from "../../../panels/frame";
import type {Panel,PanelResult} from "../../../panels/types";
const panel:Panel={id:"p",title:"P",viz:"timeseries",query:{from:"spans",where:["service = $service"],measures:["count()"]}};
const result:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:0,to_ms:10000,annotation_scope:{services:[{namespace:"shop",service:"checkout"}]}};
describe("annotations and formats",()=>{
 it("formats every column type and preserves whole-window stats",()=>{
  expect(columnDisplay({field:"n",format:"bar"},3,6)).toEqual({kind:"bar",text:"3",fraction:.5});
  expect(columnDisplay({field:"state",format:"status"},"bad",0).kind).toBe("status");
  const graded:Panel={id:"p",title:"P",viz:"table",thresholds:[{value:5,status:"warn"},{value:10,status:"bad"}],better:"lower"};
  expect(columnDisplay({field:"n",format:"status"},12,0,graded).status).toBe("bad");
  expect(columnDisplay({field:"n",format:"status"},2,0,graded).status).toBe("ok");
  expect(columnDisplay({field:"n",format:"status"},2,0,{...graded,better:"higher"}).status).toBe("bad");
  expect(columnDisplay({field:"n",format:"sparkline"},42,0,graded,undefined,[1,null,3]).points).toEqual([1,null,3]);
  expect(columnDisplay({field:"trend",format:"sparkline"},"[1,null,3]",0).points).toEqual([1,null,3]);
  expect(columnDisplay({field:"n",format:"sparkline"},42,0).unsupported).toBe("Sparkline requires an array column");
  expect(columnDisplay({field:"trace_id",format:"trace_link"},"abc",0).kind).toBe("trace_link");
  expect(columnDisplay({field:"service",format:"service_link",variable:"service"},"checkout",0).kind).toBe("service_link");
  expect(columnDisplay({field:"body_template",format:"log_template"},"failed <*>",0).text).toBe("failed <*>");
  const frame={columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[[0,1],[40,2]],rows:2,totals:[null,42]};
  expect(statValue({...panel,viz:"stat"},frame)).toBe(42);expect(sparkline(frame)).toEqual([40,2]);
 });
});
```
Create the complete table_trends_test.go shown in Step 2 before its implementation.

```bash
rtk just test ./internal/panel/... -run '"TestM2StructuredTableSparkline"'
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/table-formats.test.tsx'
```

- [ ] **Step 2: Attach bounded measure trends and render formats**

Add `Trends map[string][][]any json:"trends,omitempty"` to Go Frame, with TypeScript mirror `trends?:Record<string,(number|null)[][]>`. Structured table sparklines refer to numeric measure columns. In validateDisplayOptions, build a separate measure alias set from parseMeasures and reject a sparkline format not in that set:

```go
if c.Format == "sparkline" {
	measureAliases := map[string]bool{}
	for _, m := range parseMeasures(sig, p.Query.Measures, "", &ignored) {
		measureAliases[m.Alias] = true
	}
	if !measureAliases[c.Field] {
		problems.addHint(path+".options.columns", "structured sparklines require a measure column", "choose a measure field; the executor attaches its trend")
	}
}

```

Insert this inside the existing p.Query branch after parseMeasures and before the column-existence check. Do not require the main cell to contain an array. SQL tables may use described JSON array columns; scalar SQL cells show an explicit unsupported-format note.

Reuse Task 5’s alignedBucketMillis helper and matching Unix epoch SQL origin.

Create `internal/panel/table_trends.go`:

```go
package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/labstack/fanout/internal/queryrows"
	"strings"
	"time"
)

func trendKey(values []string) string { raw, _ := json.Marshal(values); return string(raw) }
func (e *Executor) attachTableTrends(ctx context.Context, p *Panel, checked *Checked, scope Scope, f *Frame) error {
	if p.Viz != "table" || p.Query == nil || p.Options == nil || f == nil || f.Rows == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, format := range p.Options.Columns {
		if format.Format == "sparkline" {
			wanted[format.Field] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, checked.Filters[p.ID], scope)
	if err != nil {
		return err
	}
	dimensions := []FieldRef{}
	dimensionSQL := []string{}
	indices := []int{}
	for _, by := range p.Query.By {
		ref, err := sig.field(by)
		if err != nil {
			return err
		}
		dimensions = append(dimensions, ref)
		dimensionSQL = append(dimensionSQL, "coalesce("+ref.stringSQL()+",'')")
		index := -1
		for i, c := range f.Columns {
			if c.Name == ref.alias() {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("trend dimension missing")
		}
		indices = append(indices, index)
	}
	rowKeys := map[string]int{}
	tuples := []string{}
	for r := 0; r < f.Rows; r++ {
		values := []string{}
		slots := []string{}
		for _, index := range indices {
			value, _ := f.Values[index][r].(string)
			values = append(values, value)
			slots = append(slots, "?")
			args = append(args, value)
		}
		rowKeys[trendKey(values)] = r
		if len(slots) > 0 {
			tuples = append(tuples, "("+strings.Join(slots, ",")+")")
		}
	}
	if len(dimensionSQL) > 0 {
		where += " AND (" + strings.Join(dimensionSQL, ",") + ") IN (" + strings.Join(tuples, ",") + ")"
	}
	interval := max(time.Minute, AutoInterval(scope.End.Sub(scope.Start), 960))
	for scope.End.Sub(scope.Start)/interval > 239 {
		interval *= 2
	}
	bucket := fmt.Sprintf("time_bucket(INTERVAL '%d seconds',%s::TIMESTAMP_NS,'1970-01-01'::TIMESTAMP_NS)", int64(interval/time.Second), quoteIdent(sig.time))
	selects := []string{"epoch_ms(" + bucket + ")::BIGINT AS _t"}
	groups := []string{bucket}
	columns := []Column{{Name: "time", Type: "time", Role: "time"}}
	for i, ref := range dimensions {
		selects = append(selects, dimensionSQL[i]+" AS "+quoteIdent(ref.alias()))
		groups = append(groups, dimensionSQL[i])
		columns = append(columns, Column{Name: ref.alias(), Type: "string", Role: "dimension"})
	}
	names := []string{}
	for _, m := range checked.Measures[p.ID] {
		if wanted[m.Alias] {
			selects = append(selects, measureSQL(m, sig, interval.Seconds(), "PARTITION BY "+bucket)+" AS "+quoteIdent(m.Alias))
			columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: m.Unit})
			names = append(names, m.Alias)
		}
	}
	if len(names) == 0 {
		return nil
	}
	limit := analysisCellLimit / len(columns)
	text := "SELECT " + strings.Join(selects, ",") + " FROM " + sig.name + " WHERE " + where + " GROUP BY " + strings.Join(groups, ",") + fmt.Sprintf(" ORDER BY _t DESC LIMIT %d", limit+1)
	rows, err := e.engine.QueryContext(queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End}), text, args...)
	if err != nil {
		return err
	}
	trend, err := scanFrame(rows, columns, limit)
	if err != nil {
		return err
	}
	boundAnalysisFrame(trend)
	f.Trends = map[string][][]any{}
	lo := alignedBucketMillis(scope.Start, interval)
	points := int((scope.End.UnixMilli() - lo + interval.Milliseconds() - 1) / interval.Milliseconds())
	points = min(points, 240)
	budget := analysisCellLimit
	for _, name := range names {
		series := make([][]any, f.Rows)
		for r := range series {
			if budget < points {
				f.Truncated = true
				continue
			}
			series[r] = make([]any, points)
			budget -= points
		}
		f.Trends[name] = series
	}
	for r := 0; r < trend.Rows; r++ {
		values := []string{}
		for i := range dimensions {
			value, _ := trend.Values[i+1][r].(string)
			values = append(values, value)
		}
		row, ok := rowKeys[trendKey(values)]
		if !ok {
			continue
		}
		point := int((trend.Values[0][r].(int64) - lo) / interval.Milliseconds())
		if point < 0 || point >= points {
			continue
		}
		for i, name := range names {
			if len(f.Trends[name][row]) > point {
				f.Trends[name][row][point] = trend.Values[len(dimensions)+1+i][r]
			}
		}
	}
	if trend.Truncated {
		f.Truncated = true
		f.Note = "Table trends are limited by the cell budget."
	}
	return nil
}
```

Call attachTableTrends after the main structured table frame is stored in runPanel, under that panel’s existing deadline. An error leaves the table intact and sets `frame.Note="Table trends unavailable."`. Replace limitBatchFrames in exec.go: row-aligned trends follow main-row truncation, and health/trend points consume the same deterministic request budget. Import slices for sorting the map keys (exec.go already uses slices).

```go
func limitBatchFrames(results []Result) {
	remaining := 200000
	for i := range results {
		for _, f := range []*Frame{results[i].Frame, results[i].Previous} {
			if f == nil {
				continue
			}
			if len(f.Totals) > remaining {
				f.Totals = nil
				f.Truncated = true
			}
			remaining -= len(f.Totals)
			rows := f.Rows
			if !f.bucketed {
				rows = min(rows, maxFrameRows)
			}
			if len(f.Columns) > 0 {
				rows = min(rows, remaining/len(f.Columns))
			}
			if rows < f.Rows {
				start := 0
				if f.bucketed {
					start = f.Rows - rows
					for start > 0 && start < f.Rows && f.Values[0][start] == f.Values[0][start-1] {
						start++
					}
					rows = f.Rows - start
				}
				for j := range f.Values {
					f.Values[j] = f.Values[j][start : start+rows]
				}
				for name, series := range f.Trends {
					f.Trends[name] = series[start : start+rows]
				}
				f.Rows = rows
				f.Truncated = true
			}
			remaining -= rows * len(f.Columns)
			if f.Health != nil {
				points := min(len(f.Health.ErrorTrend), remaining)
				if points < len(f.Health.ErrorTrend) {
					f.Health.ErrorTrend = f.Health.ErrorTrend[len(f.Health.ErrorTrend)-points:]
					f.Truncated = true
				}
				remaining -= points
			}
			names := make([]string, 0, len(f.Trends))
			for name := range f.Trends {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				for row, series := range f.Trends[name] {
					if len(series) > remaining {
						f.Trends[name][row] = nil
						f.Truncated = true
						continue
					}
					remaining -= len(series)
				}
			}
		}
	}
}
```

The per-table 20,000-point allowance is additional to the main query’s bounded scalar cells and remains subject to the shared request cap.

Create `internal/panel/table_trends_test.go`:

```go
package panel

import (
	"fmt"
	"testing"
)

func TestM2StructuredTableSparklineRejectsDimension(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Invalid trend", Panels: []Panel{{ID: "p", Title: "P", Viz: "table", Query: &Query{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}, Options: &Options{Columns: []ColumnFormat{{Field: "service", Format: "sparkline"}}}}}}
	Normalize(&d)
	_, problems, err := Check(t.Context(), e.engine, &d)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, problem := range problems {
		if problem.Path == "panels[0].options.columns" && problem.Message == "structured sparklines require a measure column" && problem.Hint == "choose a measure field; the executor attaches its trend" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing dimension trend validation: %+v", problems)
	}
}

func TestM2StructuredTableSparklineMeasure(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[2:3]
	d.Panels[0].Viz = "table"
	d.Panels[0].Options = &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil || got[0].Status != "ok" {
		t.Fatalf("table: %+v %v", got, err)
	}
	f := got[0].Frame
	series := f.Trends["count"]
	if len(series) != f.Rows {
		t.Fatalf("row trends: %+v", f)
	}
	points := 0
	for r, trend := range series {
		var total float64
		for _, v := range trend {
			points++
			if v != nil {
				total += v.(float64)
			}
		}
		if total != f.Values[1][r].(float64) {
			t.Fatalf("row %d trend=%v main=%v", r, total, f.Values[1][r])
		}
	}
	if points > analysisCellLimit {
		t.Fatalf("unbounded points=%d", points)
	}
	results := make([]Result, 20)
	for i := range results {
		f := newFrame([]Column{{Name: "service", Type: "string", Role: "dimension"}, {Name: "count", Type: "number", Role: "measure"}})
		f.Trends = map[string][][]any{"count": make([][]any, 100)}
		for row := 0; row < 100; row++ {
			appendPanelRow(f, fmt.Sprintf("service-%d", row), float64(200))
			f.Trends["count"][row] = make([]any, 200)
		}
		results[i] = Result{ID: fmt.Sprintf("table-%d", i), Status: StatusOK, Frame: f}
	}
	limitBatchFrames(results)
	cells := 0
	for _, r := range results {
		if r.Frame == nil {
			continue
		}
		cells += r.Frame.Rows * len(r.Frame.Columns)
		for _, rows := range r.Frame.Trends {
			for _, trend := range rows {
				cells += len(trend)
			}
		}
	}
	if cells > 200000 {
		t.Fatalf("batch cells=%d", cells)
	}
}
```



Create ui/panels/column-formats.ts:

```ts
import type {Cell,Unit,Panel,Status} from "./types";
import {statusFor} from "./thresholds";
import {formatValue} from "./units";
import type {ColumnFormat} from "./types";
export type {ColumnFormat} from "./types";
export type ColumnDisplay={unsupported?:string;kind:ColumnFormat["format"];text:string;fraction?:number;points?:(number|null)[];variable?:string;status?:Status|null};
export function columnDisplay(format:ColumnFormat,value:Cell,max:number,panel?:Panel,better?:"lower"|"higher",trend?:(number|null)[]):ColumnDisplay{
 const text=value===null?"—":String(value);const result:ColumnDisplay={kind:format.format,text};
 if(format.format==="unit"||format.format==="bar")result.text=typeof value==="number"?formatValue(format.unit,value):text;
 if(format.format==="bar")result.fraction=typeof value==="number"&&max>0?Math.max(0,Math.min(1,value/max)):0;
 if(format.format==="status"&&typeof value==="number")result.status=statusFor(Number.isFinite(value)?value:null,panel?.thresholds,panel?.better??better);
 if(format.format==="service_link")result.variable=format.variable;
 if(format.format==="sparkline"&&trend){result.points=trend.slice(0,240);return result;}
 if(format.format==="sparkline"){try{const parsed:unknown=JSON.parse(text);if(Array.isArray(parsed))result.points=parsed.slice(0,240).map(v=>typeof v==="number"&&Number.isFinite(v)?v:null);else result.unsupported="Sparkline requires an array column";}catch{result.unsupported="Sparkline requires an array column";}}
 return result;
}
```

Export the existing `Sparkline` from stat.tsx and change its SVG from aria-hidden to `role="img" aria-label="Value over this panel's time range"`; retain its gap-preserving path and existing totals/delta handling. Reuse it for table sparkline cells.

Add a pure `columnDisplay` call to TableViz's cell callback after RowPanel's custom override and before the default branch. Add `onVariable?: (name:string,value:string)=>void` to TableViz and thread it from PanelGrid through PanelCard/Viz. Use this complete rendering helper in table.tsx (imports: Anchor, Badge, Code from Mantine; ReactNode from React; Unit from ui/panels/types; Sparkline from ./stat; columnDisplay/ColumnFormat from ui/panels/column-formats; makeDrill from ../drill-state; all installed dependencies or files created above):

```tsx
function FormattedCell({format,value,max,panel,better,trend,traceHref,onTrace,onService}:{format:ColumnFormat;value:Cell;max:number;panel:Panel;better?:"lower"|"higher";trend?:(number|null)[];traceHref?:string;onTrace:()=>void;onService:(name:string,value:string)=>void}):ReactNode{
 const display=columnDisplay(format,value,max,panel,better,trend);
 switch(display.kind){
  case "unit":return <Text size="sm" ff="monospace">{display.text}</Text>;
  case "bar":return <Box><Text size="xs" ff="monospace">{display.text}</Text><Box h={5} bg="var(--mantine-color-default-border)"><Box h={5} w={`${(display.fraction??0)*100}%`} bg="var(--mantine-primary-color-filled)"/></Box></Box>;
  case "status":{
   const bad=display.status==="bad"||display.text==="bad"||display.text==="unhealthy"||display.text.includes("ERROR");const warn=display.status==="warn"||display.text==="warn"||display.text==="degraded"||display.text==="WARN";
   return <Badge color={bad?"bad":warn?"warn":"gray"}>{bad?"◆ ":warn?"■ ":display.status===null||display.text==="—"||display.text==="unknown"?"○ ":"● "}{display.text}</Badge>;
  }
  case "sparkline":return display.unsupported?<Text size="xs" c="warn" role="status">{display.unsupported}</Text>:display.points && display.points.filter(p=>p!==null).length>=2?<Sparkline points={display.points}/>:<Text c="dimmed">No trend</Text>;
  case "trace_link":return traceHref?<Anchor href={traceHref} onClick={event=>{if(event.button===0&&!event.ctrlKey&&!event.metaKey&&!event.shiftKey&&!event.altKey){event.preventDefault();event.stopPropagation();onTrace();}}}>{display.text}</Anchor>:<Text size="sm" ff="monospace">{display.text}</Text>;
  case "service_link":return <Anchor component="button" type="button" onClick={event=>{event.stopPropagation();if(display.variable&&value!==null)onService(display.variable,display.text);}}>{display.text}</Anchor>;
  case "log_template":return <Code style={{whiteSpace:"pre-wrap"}}>{display.text}</Code>;
 }
}
```

Cell call site:

```tsx
const format=panel.options?.columns?.find(c=>c.field===column.name);
if(format){
 if(format.format==="sparkline"&&!panel.query&&column.type!=="json")return <Text size="xs" c="warn" role="status">Sparkline requires an array column</Text>;
 const selection={...model.selection(model.rows[info.row.index]),trace_id:value===null?undefined:String(value)};
 const target=format.format==="trace_link"?makeDrill(panel,result,selection):undefined;
 const url=target?new URL(window.location.href):undefined;if(url&&target)url.searchParams.set("drill",JSON.stringify(target));
 return <FormattedCell format={{...format,unit:format.unit??column.unit as Unit|undefined}} value={value} max={maxima[index]} panel={panel} better={result.better} trend={frame.trends?.[column.name]?.[info.row.index]} traceHref={url?.toString()} onTrace={()=>onPoint?.(selection)} onService={(name,value)=>onVariable?.(name,value)}/>;
}
```

Keep unknown/null units readable. For SQL sparkline cells, validate the described column is json and the parsed value is an array; otherwise return a status text `Sparkline requires an array column` and show a panel unsupported-format note. Do not silently render scalar SQL values as No trend. Add this complete note immediately below Table in TableViz, before the truncation note:

```tsx
{!panel.query&&(panel.options?.columns??[]).some(c=>c.format==="sparkline"&&frame.columns.some((column,i)=>column.name===c.field&&(column.type!=="json"||frame.values[i].some(value=>Boolean(columnDisplay(c,value,0).unsupported)))))&&<Text size="xs" c="warn" role="status">Sparkline requires an array column</Text>}
{(panel.options?.columns??[]).some(c=>!frame.columns.some(column=>column.name===c.field))&&<Text size="xs" c="warn" role="status">Column formats unavailable: {(panel.options?.columns??[]).filter(c=>!frame.columns.some(column=>column.name===c.field)).map(c=>c.field).join(", ")}</Text>}
```

Include onPoint, onVariable, panel.options.columns and model in the cell-column useMemo dependencies. `trace_link` uses the same onPoint/makeDrill URL path as trace rows; no invented HTTP route. Keep aria summaries and Inspect available.

- [ ] **Step 3: Run to see pass and review**

```bash
rtk just test ./internal/panel/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

Expected: all owning regression and integration tests pass.

Regenerate the changed panel/tool contract references, then format all Go changes before the controller commit:

```bash
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

- [ ] **Step 4: Commit (controller)**

```bash
rtk git add site/src/content/docs/reference internal/panel ui/panels ui/host/src/dashboards internal/ui/dist internal/mcp/apps
rtk git commit -m "feat(panels): attach table trends and render formats"
```

---

### Task 14: Agent guidance, complete examples and independent evaluation

**Files:**
- Create: `internal/mcp/panel_examples.go`, `internal/mcp/panel_examples_test.go`, `internal/agent/dashboard_guidance_test.go`
- Modify: `internal/mcp/panels.go`, `internal/mcp/dashboards.go`, `internal/mcp/panels_test.go`, `internal/agent/runtime.go`
- Regenerate: `site/src/content/docs/reference/mcp-tools.mdx` and `site/src/content/docs/reference/http-routes.mdx` with the existing doc generator (the latter includes Task 1/2's routes).
- Controller-only evidence: fresh label directories under `.superpowers/eval/m2-baseline/`, `.superpowers/eval/m2-candidate/`; final report is published by Task 15. No change to `.superpowers/eval/holdout.json` or the harness.

**Interfaces:**
- Consumes `(*Server).previewPanels(ctx context.Context, request *mcp.CallToolRequest, input PreviewInput) (*mcp.CallToolResult, PreviewOutput, error)`; `PreviewInput{Panels []panel.Panel; Time *panel.Time; Variables []panel.Variable; Vars map[string]string}`; `DescribeTools(ctx context.Context) ([]ToolDoc,error)`; `dashboardTools`' existing create/replace/edit descriptions; `systemPrompt`.
- Produces `const m2SpecGuide string`, `const investigationExample string`, `const paymentExample string`, `const dashboardAnalysisGuidance string`. The two complete dashboards supply two independently executable examples of each of the nine new types. Example 1 additionally exercises all six M1 types, so preview has fifteen-type coverage.
- No new HTTP, MCP or TypeScript interface. Model selection uses the real `FANOUT_AI_PROVIDER=anthropic`, `FANOUT_AI_MODEL=claude-sonnet-5-5` configuration; `--model-label` only labels artifacts.

- [ ] **Step 1: Controller captures the baseline before any prompt or guide edit**

Capture the preceding build on an isolated account and fixed replay snapshot before Step 4 edits guidance. Configure FANOUT_AI_PROVIDER=anthropic and FANOUT_AI_MODEL=claude-sonnet-5-5. Inject FANOUT_AI_API_KEY through the controller's secret store, never a command argument or file. Use the harness’s existing cookie jar without printing it. Freeze telemetry and disable refresh for comparisons. Capture the baseline while the existing prompt/guide is still byte-for-byte unchanged:

```bash
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-r1 --out .superpowers/eval/m2-baseline --prompts all
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-r2 --out .superpowers/eval/m2-baseline --prompts all
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-holdout-r1 --out .superpowers/eval/m2-baseline --prompts-file .superpowers/eval/holdout.json --set holdout
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-holdout-r2 --out .superpowers/eval/m2-baseline --prompts-file .superpowers/eval/holdout.json --set holdout
```

Keep holdout prompts sealed and untouched. These four baseline artifacts must exist before Step 4; missing offline prerequisites block this gate rather than moving it after the prompt edit.

- [ ] **Step 2: Write failing guide, example and preview tests**

Create `internal/mcp/panel_examples_test.go`:

```go
package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestM2ExamplesAndPreviewCoverFifteenTypes(t *testing.T) {
	s := newPanelServer(t)
	coverage := map[string]int{}
	for _, text := range []string{investigationExample, paymentExample} {
		var d panel.Dashboard
		if err := json.Unmarshal([]byte(text), &d); err != nil {
			t.Fatal(err)
		}
		if err := s.panels.Validate(t.Context(), &d); err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		_, result, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: d.Panels, Time: &d.Time, Variables: d.Variables})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Panels) != len(d.Panels) {
			t.Fatalf("preview lost panels: %+v", result)
		}
		for i, preview := range result.Panels {
			if preview.Status != "ok" && preview.Status != "empty" {
				t.Fatalf("%s: %+v", d.Panels[i].Viz, preview)
			}
			if preview.Status == "empty" && preview.Diagnosis == "" {
				t.Fatalf("unexplained empty %s", preview.ID)
			}
			coverage[d.Panels[i].Viz]++
		}
	}
	if len(coverage) != 15 {
		t.Fatalf("type coverage: %+v", coverage)
	}
	for _, viz := range []string{"heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"} {
		if coverage[viz] != 2 {
			t.Fatalf("%s has %d examples", viz, coverage[viz])
		}
	}
}

func TestM2DescriptionsExposeAnalysisContract(t *testing.T) {
	docs, err := DescribeTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		fullGuide := doc.Name == "preview_panels" || doc.Name == "create_dashboard"
		if !fullGuide {
			if !strings.HasSuffix(doc.Name, "_dashboard") && !strings.HasSuffix(doc.Name, "_dashboards") && !strings.HasSuffix(doc.Name, "_panels") && !strings.HasSuffix(doc.Name, "_panel") {
				continue
			}
			seen[doc.Name] = true
			if !strings.Contains(doc.Description, "See create_dashboard for the spec guide.") {
				t.Errorf("%s omits the spec guide pointer", doc.Name)
			}
			for _, guide := range []string{baseSpecGuide, m2SpecGuide, investigationExample, paymentExample} {
				if strings.Contains(doc.Description, guide) {
					t.Errorf("%s duplicates the full guide or examples", doc.Name)
				}
			}
			continue
		}
		seen[doc.Name] = true
		for _, guide := range []string{baseSpecGuide, m2SpecGuide, investigationExample, paymentExample} {
			if !strings.Contains(doc.Description, guide) {
				t.Errorf("%s omits the full guide or examples", doc.Name)
			}
		}
		for _, term := range []string{"heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health", "histogram", "set_variable", "drill", "annotations", "split", "columns", "Checkout investigation", "Payment investigation"} {
			if !strings.Contains(doc.Description, term) {
				t.Errorf("%s omits %s", doc.Name, term)
			}
		}
	}
	for _, name := range []string{"preview_panels", "create_dashboard", "replace_dashboard", "edit_dashboard"} {
		if !seen[name] {
			t.Fatalf("missing description %s: %+v", name, seen)
		}
	}
	if RequiredToolScope("preview_panels") != "" || RequiredToolScope("create_dashboard") != "dashboard:manage" {
		t.Fatal("M1 authorization changed")
	}
}
```

Create `internal/agent/dashboard_guidance_test.go`:

```go
package agent

import (
	"strings"
	"testing"
)

func TestM2DashboardGuidancePreservesIntentAndRequiresPreview(t *testing.T) {
	for _, term := range []string{"answer a single factual question with a view instead", "preview_panels", "fix every invalid panel", "get_dashboard first", "edit_dashboard", "heatmap", "histogram", "scatter", "state_timeline", "log_patterns", "service_map", "health", "drill", "annotations", "split", "No data", "single fact"} {
		if !strings.Contains(systemPrompt, term) {
			t.Errorf("prompt omits %q", term)
		}
	}
}
```

- [ ] **Step 3: Run to see failure**

```bash
rtk just test ./internal/mcp/... ./internal/agent/... -run '"TestM2Examples|TestM2Descriptions|TestM2DashboardGuidance"'
```

Expected: undefined example/guidance constants and missing descriptions.

- [ ] **Step 4: Implement the full guide and both executable examples**

Create `internal/mcp/panel_examples.go`:

```go
package mcp

const m2SpecGuide = `Analysis types: heatmap uses bucket:auto and histogram:{field:duration_ms,buckets:log2} on spans, or {field:value,buckets:explicit} on metrics with type histogram; histogram has the same distribution input without time bucket and may split by one dimension. Both use count() as the bucket observation weight. Metric histograms default unknown temporality to cumulative: sum per-series last-minus-first counts per bound, clamped at zero on reset. A known delta source declares histogram.temporality:delta and sums its points; format 3 does not store temporality. Scatter requires exactly two aggregate measures with independent units; x_unit describes x and unit describes y and one item by, plus an optional colour by; options.x_scale and options.y_scale are linear or log. State_timeline requires one item by, one measure, bucket:auto and warn/bad thresholds; missing buckets mean unknown. Logs reads logs with no measures/by/bucket, shows redacted bodies and supports options.highlight as a literal search; sort is time or +time. Log_patterns reads logs, measures:[count()], by:[body_template], bucket:auto, limit up to a cap of 50 (default 20); trend sparklines are dense and bounded. Traces reads spans with no measures/by/bucket, sort duration_ms or errors or +start, limit at most 1000. Health and service_map read spans with no measures/by/bucket; their guarded filters support only namespace and service equality, including a single-value variable. They reuse existing service/edge rollups. Use service_map for dependency context, health for service health; neither proves causality. Panel click:{set_variable:service} chooses a declared query/custom variable. Panel drill is traces or logs; structured span panels drill to checked exemplars, structured log panels drill to checked logs. SQL panels do not drill. Panel time:{range:6h,shift:1d} overrides the dashboard window. Dashboard annotations:{deploys:true,anomalies:true} show version transitions and detector findings with one shared request per refresh; a first observed version is not a deploy and an anomaly is not a causal claim. Bar options.split:deploy requires one measure, one by and a single service equality; it compares separately normalized before/since windows at the latest deploy and says when none exists. Table options.columns is an array of {field,format,unit?,variable?}; formats are unit, bar, status, sparkline (executor-attached per-row measure trend for structured tables; JSON numeric arrays for SQL tables), trace_link, service_link (names a declared query/custom variable), log_template. Rows and cells are capped; truncation and No data must be visible. Preview every type and retain explained empty panels when the requested signal is absent. Complete examples follow.`

const investigationExample = `{"name":"Checkout investigation","description":"A complete fifteen-type overview with checked drills and deploy context.","time":{"range":"1h","refresh":"off"},"annotations":{"deploys":true,"anomalies":true},"variables":[{"name":"service","kind":"custom","options":["checkout","payment"],"default":"checkout"}],"panels":[{"id":"latency_heat","title":"Latency distribution over time","viz":"heatmap","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["count()"],"bucket":"auto","histogram":{"field":"duration_ms","buckets":"log2"}},"unit":"ms","drill":"traces"},{"id":"latency_hist","title":"Latency distribution by operation","viz":"histogram","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["count()"],"by":["operation"],"histogram":{"field":"duration_ms","buckets":"log2"}},"unit":"ms","drill":"traces"},{"id":"items","title":"Operation rows versus tail latency","viz":"scatter","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["count() as rows","p95(duration_ms) as tail_latency"],"by":["operation"]},"options":{"x_scale":"log","y_scale":"linear"},"drill":"traces","x_unit":"count","unit":"ms"},{"id":"states","title":"Operation error states","viz":"state_timeline","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["error_rate()"],"by":["operation"],"bucket":"auto"},"unit":"percent","thresholds":[{"value":1,"status":"warn"},{"value":5,"status":"bad"}],"drill":"traces"},{"id":"log_rows","title":"Recent checkout logs","viz":"logs","width":12,"query":{"from":"logs","where":["service = $service"],"sort":"time","limit":100},"options":{"highlight":"timeout"},"drill":"logs"},{"id":"patterns","title":"Repeated checkout log patterns","viz":"log_patterns","width":6,"query":{"from":"logs","where":["service = $service"],"measures":["count()"],"by":["body_template"],"bucket":"auto","limit":50},"drill":"logs"},{"id":"trace_rows","title":"Slow checkout traces","viz":"traces","width":6,"query":{"from":"spans","where":["service = $service"],"sort":"duration_ms","limit":20},"drill":"traces"},{"id":"map","title":"Shop service calls","viz":"service_map","width":8,"query":{"from":"spans","where":["namespace = 'shop'"]}},{"id":"health","title":"Shop service health","viz":"health","width":4,"query":{"from":"spans","where":["namespace = 'shop'"]}},{"id":"calls","title":"Checkout calls","viz":"stat","width":3,"query":{"from":"spans","where":["service = $service"],"measures":["count()"]},"unit":"count"},{"id":"error_gauge","title":"Checkout error rate","viz":"gauge","width":3,"query":{"from":"spans","where":["service = $service"],"measures":["error_rate()"]},"unit":"percent","min":0,"max":100},{"id":"latency","title":"Checkout p95 latency","viz":"timeseries","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["p95(duration_ms)"],"bucket":"auto"},"unit":"ms","drill":"traces"},{"id":"routes","title":"Calls before and since deploy","viz":"bar","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["rate()"],"by":["operation"]},"options":{"split":"deploy"},"unit":"per_second"},{"id":"operations","title":"Checkout operations","viz":"table","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["count() as calls"],"by":["operation"]},"options":{"columns":[{"field":"calls","format":"bar","unit":"count"}]}},{"id":"notes","title":"How to investigate","viz":"text","width":12,"content":"Select an operation to inspect traces. Deploy markers show observed version changes; shaded intervals show detector findings, not proven causes."}]}`

const paymentExample = `{"name":"Payment investigation","description":"Independent examples of every new type for the payment service.","time":{"range":"24h","refresh":"1m"},"annotations":{"deploys":true,"anomalies":true},"variables":[{"name":"service","kind":"custom","options":["checkout","payment"],"default":"payment"}],"panels":[{"id":"latency_heat","title":"Payment latency distribution over time","viz":"heatmap","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["count()"],"bucket":"auto","histogram":{"field":"duration_ms","buckets":"log2"}},"unit":"ms","drill":"traces"},{"id":"latency_hist","title":"Payment latency distribution by operation","viz":"histogram","width":6,"query":{"from":"metrics","where":["service = $service","name = 'http.server.request.duration'"],"measures":["count()"],"histogram":{"field":"value","buckets":"explicit"}},"unit":"ms"},{"id":"items","title":"Payment mean versus tail latency","viz":"scatter","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["avg(duration_ms) as mean_latency","p95(duration_ms) as tail_latency"],"by":["operation"]},"options":{"x_scale":"linear","y_scale":"log"},"drill":"traces","x_unit":"ms","unit":"ms"},{"id":"states","title":"Payment operation error states","viz":"state_timeline","width":6,"query":{"from":"spans","where":["service = $service"],"measures":["error_rate()"],"by":["operation"],"bucket":"auto"},"unit":"percent","thresholds":[{"value":1,"status":"warn"},{"value":5,"status":"bad"}],"drill":"traces","time":{"range":"6h"}},{"id":"log_rows","title":"Recent payment logs","viz":"logs","width":12,"query":{"from":"logs","where":["service = $service"],"sort":"time","limit":100},"options":{"highlight":"error"},"drill":"logs"},{"id":"patterns","title":"Repeated payment log patterns","viz":"log_patterns","width":6,"query":{"from":"logs","where":["service = $service"],"measures":["count()"],"by":["body_template"],"bucket":"auto","limit":50},"drill":"logs"},{"id":"trace_rows","title":"Slow payment traces","viz":"traces","width":6,"query":{"from":"spans","where":["service = $service"],"sort":"errors","limit":20},"drill":"traces"},{"id":"map","title":"Shop service calls","viz":"service_map","width":8,"query":{"from":"spans","where":["namespace = 'shop'"]}},{"id":"health","title":"Shop service health","viz":"health","width":4,"query":{"from":"spans","where":["namespace = 'shop'"]}}]}`
```

Rename the existing `specGuide` constant in panels.go to `baseSpecGuide` without altering its M1 measure/filter/SQL text. Define this exact combined constant in panel_examples.go:

```go
const specGuide = baseSpecGuide + " " + m2SpecGuide + " Complete example 1: " + investigationExample + " Complete example 2: " + paymentExample
```

Keep the full specGuide and both examples only on `create_dashboard` and `preview_panels`, which already append it. Every other dashboard/panel tool description carries only the one-line pointer `See create_dashboard for the spec guide.` for guidance; preserve its tool-specific description. In particular, append that pointer to `replace_dashboard` and `edit_dashboard` in dashboards.go, never the full specGuide or examples. Keep tool names, annotations, owner propagation, warning collection, and preview's invalid/not_run behavior unchanged.

In newPanelServer (panels_test.go), add the observability import and call `executor.SetRollupReader(observability.New(duck,duck,30))` after NewExecutor. This is a real reader over fixture tables, not a fake health success.

In runtime.go append the new constant to the existing raw-string systemPrompt expression:

```go
const dashboardAnalysisGuidance = ` For analysis dashboards use heatmap for latency changes, histogram for a distribution, scatter for relationships between two item measures, state_timeline for threshold states, logs for individual redacted events, log_patterns for repeated messages and their trends, traces for slow or erroring trace candidates, service_map for dependency context, and health for service health. Use drill on a structured span or log panel when a user needs evidence behind a point; keep its checked filters. Include annotations for change investigations, but distinguish observed deploys and detector findings from proven causes. Use a deploy split for an explicitly scoped before/since comparison and retain its missing-deploy explanation. Keep totals and sparklines on the same panel window. Use table columns formats for readable values and links. No data is missing evidence, not healthy behavior. A definition, explanation, or single fact is an answer intent; do not create or replace a dashboard for it. Preserve every requested facet, preview every new type, and explain absent telemetry without inventing it.`
```

The resulting declaration ends with the original raw string followed by ` + dashboardAnalysisGuidance`. Retain all existing factual-question, edit-scope and attached-view guidance.

- [ ] **Step 5: Run to see pass and regenerate registered documentation**

```bash
rtk just test ./internal/mcp/... ./internal/agent/... -run '"TestM2Examples|TestM2Descriptions|TestM2DashboardGuidance|TestPreviewPanels|TestDashboardToolScopes"'
rtk just test ./internal/mcp/... ./internal/agent/... ./internal/dashboard/...
rtk just docs-generate
rtk just docs-generate-check
rtk proxy bun .superpowers/eval/mock2.ts --out .superpowers/eval/m2-mock
```

A fresh mock output path is mandatory; an existing output is a refusal, not permission to overwrite. Generated API/MCP docs must agree with route classification and fifteen-type schemas.

- [ ] **Step 6: Controller runs candidate evaluation and compares the captured baseline**

Run against the candidate after Step 5, using the same model, isolated account, frozen telemetry snapshot and secret runtime injection established in Step 1:

```bash
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-r1 --out .superpowers/eval/m2-candidate --prompts all
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-r2 --out .superpowers/eval/m2-candidate --prompts all
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-holdout-r1 --out .superpowers/eval/m2-candidate --prompts-file .superpowers/eval/holdout.json --set holdout
rtk proxy bun .superpowers/eval/eval2.ts --base http://127.0.0.1:7520 --cookies .superpowers/eval/cookies.txt --model-label sonnet-holdout-r2 --out .superpowers/eval/m2-candidate --prompts-file .superpowers/eval/holdout.json --set holdout
rtk proxy bun .superpowers/eval/judge.ts --in .superpowers/eval/m2-baseline/sonnet-r1 --in .superpowers/eval/m2-baseline/sonnet-r2 --in .superpowers/eval/m2-baseline/sonnet-holdout-r1 --in .superpowers/eval/m2-baseline/sonnet-holdout-r2
rtk proxy bun .superpowers/eval/judge.ts --in .superpowers/eval/m2-candidate/sonnet-r1 --in .superpowers/eval/m2-candidate/sonnet-r2 --in .superpowers/eval/m2-candidate/sonnet-holdout-r1 --in .superpowers/eval/m2-candidate/sonnet-holdout-r2
rtk proxy bun .superpowers/eval/report.ts --in .superpowers/eval/m2-baseline/sonnet-r1 --in .superpowers/eval/m2-baseline/sonnet-r2 --in .superpowers/eval/m2-baseline/sonnet-holdout-r1 --in .superpowers/eval/m2-baseline/sonnet-holdout-r2 --out .superpowers/eval/m2-baseline/comparison.md
rtk proxy bun .superpowers/eval/report.ts --in .superpowers/eval/m2-candidate/sonnet-r1 --in .superpowers/eval/m2-candidate/sonnet-r2 --in .superpowers/eval/m2-candidate/sonnet-holdout-r1 --in .superpowers/eval/m2-candidate/sonnet-holdout-r2 --out .superpowers/eval/m2-candidate/comparison.md
```

Inject the harness's existing JUDGE_ANTHROPIC_KEY and JUDGE_OPENAI_KEY only into the judge process through the same secret service. Do not change judge models/rubric. Compare matching sets and repeats: candidate mean S1 and mean intent_accuracy must each be at least their baseline for benchmark and holdout separately; require complete evidence, no unknown validation, and no execution/provider errors. Built-in prompts are the development set. Freeze the candidate before the holdout run; never read holdout prompts to revise the guide. A holdout regression blocks release and is reported; it is not a tuning target. The controller freezes/replays the same snapshot and disables refresh for model comparisons to prevent wall-clock aging from becoming a confounder.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

- [ ] **Step 7: Commit (controller)**

```bash
rtk git add internal/mcp/panel_examples.go internal/mcp/panel_examples_test.go internal/mcp/panels.go internal/mcp/dashboards.go internal/mcp/panels_test.go internal/agent/runtime.go internal/agent/dashboard_guidance_test.go site/src/content/docs/reference/mcp-tools.mdx site/src/content/docs/reference/http-routes.mdx
rtk git commit -m "feat(agent): teach dashboard analysis types with checked examples"
```

---

### Task 15: Demo replay, browser evidence and fixes through the owning tests

**Files:**
- Create: `ui/panels/acceptance.ts`, `ui/host/src/dashboards/m2-acceptance.test.ts`, `docs/benchmarks/2026-10-agent-dashboards-m2.md`
- Create ignored controller helpers: `.superpowers/replay/m2-report.ts`, `.superpowers/replay/m2-browser-evidence.json` (the latter is measured browser output, never a checked-in fixture).
- Modify only when a recorded defect demonstrates the need: the owning implementation/test files from Tasks 1–14, with the exact regression reproduced in those tests before the fix.
- Generated: `internal/ui/dist/**`, `internal/mcp/apps/**` through `just ui`.
- Reuse: `.superpowers/replay/main.go`, `.superpowers/replay/README.md`, `.superpowers/eval/eval2.ts`, `judge.ts`, `report.ts`. No production format-2 reader or fallback.

**Interfaces:**
- Consumes all fifteen Viz values, Task 12's one-refresh bundle, Task 14's benchmark/holdout summaries, existing replay flags `-dry-run`, `-in`, `-endpoint`, `-signals`, environment `FANOUT_REPLAY_TOKEN`.
- Produces pure TS `BrowserEvidence`, `checkBrowserEvidence(e:BrowserEvidence):string[]`, `browserReport(e:BrowserEvidence):string` with the complete definitions below. Evidence has one row per theme × type, one annotation-call count per completed refresh, and explicit interaction/state results. Missing evidence is a failed gate.
- Browser helper contract: it operates the controller's disposable local instance, uses real demo data and both themes, writes the complete BrowserEvidence JSON plus screenshots under `.superpowers/replay/m2-screenshots/`, and reports defects with a reproducing URL, action, expected/actual result and network/console evidence. It changes no production files and prints no session or ingest secret.

- [ ] **Step 1: Write failing acceptance tests**

Create `ui/host/src/dashboards/m2-acceptance.test.ts`:

```ts
import {describe,expect,it} from "vitest";
import {browserReport,checkBrowserEvidence,type BrowserEvidence} from "../../../panels/acceptance";

const types=["stat","gauge","timeseries","bar","table","text","heatmap","histogram","scatter","state_timeline","logs","log_patterns","traces","service_map","health"];
const checks=["loading","empty","error","partial","inspect","filter_chip","linked_crosshair","brush_history","panel_time","drill_url","waterfall_logs","cancel_drill","deploy_markers","anomaly_areas","deploy_split","column_formats","stat_sparkline"];
function fixture():BrowserEvidence{return {
 head:"fixture",model:"claude-sonnet-5-5",source:"test fixture only",replay:{spans:60,logs:10,metrics:10,shift_ns:"1000000000"},
 panels:["light","dark"].flatMap(theme=>types.map(viz=>({theme:theme as "light"|"dark",viz,visible:true,aria:true,inspect:true,console_errors:0}))),
 refreshes:[{theme:"light",panels:20,panel_calls:1,annotation_calls:1,cells:180000,elapsed_ms:1000},{theme:"dark",panels:20,panel_calls:1,annotation_calls:1,cells:180000,elapsed_ms:1000}],
 interactions:checks.map(name=>({name,passed:true,evidence:"isolated test fixture"})),
 parity:["heatmap","log_patterns","deploy_markers","drill_drawer","split_bars","table_formats"].map(capability=>({capability,preview_evidence:"isolated preview fixture",m2_evidence:"isolated M2 fixture",gap:"none"})),
 screenshots:["light.png","dark.png"],defects:[],
 evaluations:["benchmark","holdout"].map(set=>({set: set as "benchmark"|"holdout",baseline_s1:1,candidate_s1:1,baseline_intent:1,candidate_intent:1,errors:0,unchecked:0})),
};}
describe("M2 acceptance evidence",()=>{
 it("accepts complete evidence and renders measured counts",()=>{const e=fixture();expect(checkBrowserEvidence(e)).toEqual([]);const report=browserReport(e);expect(report).toContain("60 spans");expect(report).toContain("S8");expect(report).toContain("holdout");});
 it("rejects annotation fanout, missing themes, oversized frames and regressed intent",()=>{
  const e=fixture();e.refreshes[0].annotation_calls=20;e.refreshes[0].cells=200001;e.panels=e.panels.filter(p=>!(p.theme==="dark"&&p.viz==="health"));e.evaluations[1].candidate_intent=.9;
  const errors=checkBrowserEvidence(e).join(" ");expect(errors).toContain("S8");expect(errors).toContain("200000");expect(errors).toContain("dark/health");expect(errors).toContain("holdout intent");
 });
 it("requires S13 comparisons and records capability gaps",()=>{const e=fixture();e.parity[0].gap="heatmap lacks numeric bucket ordering";expect(checkBrowserEvidence(e).join(" ")).toContain("S13 heatmap");expect(browserReport(e)).toContain("heatmap lacks numeric bucket ordering");e.parity=[];expect(checkBrowserEvidence(e).join(" ")).toContain("missing comparison");});
 it("rejects silent empty/error/loading omissions and unresolved defects",()=>{const e=fixture();e.interactions=e.interactions.filter(x=>x.name!=="empty");e.defects=["heatmap did not clear an empty refresh"];expect(checkBrowserEvidence(e).join(" ")).toContain("empty");expect(checkBrowserEvidence(e).join(" ")).toContain("unresolved");});
});
```

- [ ] **Step 2: Run to see failure**

```bash
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/m2-acceptance.test.ts'
```

Expected: missing acceptance module. These tests certify the evidence gate, not actual browser performance; actual S8 evidence must come from Step 5.

- [ ] **Step 3: Implement the evidence gate and report renderer**

Create `ui/panels/acceptance.ts`:

```ts
export type BrowserEvidence={
 head:string;model:string;source:string;replay:{spans:number;logs:number;metrics:number;shift_ns:string};
 panels:{theme:"light"|"dark";viz:string;visible:boolean;aria:boolean;inspect:boolean;console_errors:number}[];
 refreshes:{theme:"light"|"dark";panels:number;panel_calls:number;annotation_calls:number;cells:number;elapsed_ms:number}[];
 interactions:{name:string;passed:boolean;evidence:string}[];
 parity:{capability:string;preview_evidence:string;m2_evidence:string;gap:string}[];
 screenshots:string[];defects:string[];
 evaluations:{set:"benchmark"|"holdout";baseline_s1:number;candidate_s1:number;baseline_intent:number;candidate_intent:number;errors:number;unchecked:number}[];
};
const types=["stat","gauge","timeseries","bar","table","text","heatmap","histogram","scatter","state_timeline","logs","log_patterns","traces","service_map","health"];
const checks=["loading","empty","error","partial","inspect","filter_chip","linked_crosshair","brush_history","panel_time","drill_url","waterfall_logs","cancel_drill","deploy_markers","anomaly_areas","deploy_split","column_formats","stat_sparkline"];
export function checkBrowserEvidence(e:BrowserEvidence):string[]{
 const errors:string[]=[];
 if(!e.head||!e.source||e.model!=="claude-sonnet-5-5")errors.push("missing build/source or incorrect model");
 if(!/^[-]?\d+$/.test(e.replay.shift_ns)||[e.replay.spans,e.replay.logs,e.replay.metrics].some(n=>!Number.isFinite(n)||n<=0))errors.push("replay must contain all three signals and a signed nanosecond shift");
 for(const theme of ["light","dark"] as const)for(const viz of types){const row=e.panels.find(p=>p.theme===theme&&p.viz===viz);if(!row||!row.visible||(viz!=="text"&&(!row.aria||!row.inspect))||row.console_errors!==0)errors.push(`${theme}/${viz}: incomplete rendering/aria/Inspect evidence`);}
 for(const theme of ["light","dark"] as const){if(!e.refreshes.some(r=>r.theme===theme&&r.panels>=20))errors.push(`${theme}: no 20-panel refresh`);}
 for(const r of e.refreshes){if(r.panel_calls!==1||r.annotation_calls!==1)errors.push(`S8 ${r.theme}: expected one panel call and one annotations call`);if(!Number.isFinite(r.cells)||r.cells<0||r.cells>200000)errors.push("frame exceeds 200000 cells");if(!Number.isFinite(r.elapsed_ms)||r.elapsed_ms<0)errors.push("missing refresh timing evidence");}
 for(const name of checks){const row=e.interactions.find(i=>i.name===name);if(!row?.passed||!row.evidence.trim())errors.push(`${name}: missing or failed interaction evidence`);}
 for(const set of ["benchmark","holdout"] as const){const row=e.evaluations.find(i=>i.set===set);if(!row){errors.push(`${set}: missing evaluation`);continue;}for(const n of [row.baseline_s1,row.candidate_s1,row.baseline_intent,row.candidate_intent])if(!Number.isFinite(n)||n<0||n>1)errors.push(`${set}: unknown rate`);if(row.candidate_s1<row.baseline_s1)errors.push(`${set} S1 regressed`);if(row.candidate_intent<row.baseline_intent)errors.push(`${set} intent regressed`);if(row.errors||row.unchecked)errors.push(`${set}: incomplete execution/validation evidence`);}
 for(const capability of ["heatmap","log_patterns","deploy_markers","drill_drawer","split_bars","table_formats"]){const row=e.parity.find(p=>p.capability===capability);if(!row?.preview_evidence.trim()||!row.m2_evidence.trim()||!row.gap.trim())errors.push(`S13 ${capability}: missing comparison or gap record`);else if(row.gap!=="none")errors.push(`S13 ${capability}: ${row.gap}`);}
 if(e.defects.length)errors.push(`${e.defects.length} unresolved defects`);
 if(e.screenshots.length<2)errors.push("missing screenshots");
 return errors;
}
function cell(s:unknown):string{return String(s).replaceAll("|","\\|").replaceAll("\n"," ");}
export function browserReport(e:BrowserEvidence):string{
 const errors=checkBrowserEvidence(e);
 return ["# Agent dashboards Milestone 2 verification","",`Build: ${cell(e.head)}. Model: ${cell(e.model)}. Source: ${cell(e.source)}.`,`Status: ${errors.length?"BLOCKED":"PASS"}.`,"",`Replay: ${e.replay.spans} spans, ${e.replay.logs} logs, ${e.replay.metrics} metric points; shift_ns=${e.replay.shift_ns}.`,"","## Browser type coverage","","| Theme | Type | Visible | Aria | Inspect | Console errors |","|---|---|---|---|---|---|",...e.panels.map(p=>`| ${p.theme} | ${cell(p.viz)} | ${p.visible} | ${p.aria} | ${p.inspect} | ${p.console_errors} |`),"","## S8 and bounded refresh","","| Theme | Panels | Panel requests | Annotation requests | Cells | ms |","|---|---:|---:|---:|---:|---:|",...e.refreshes.map(r=>`| ${r.theme} | ${r.panels} | ${r.panel_calls} | ${r.annotation_calls} | ${r.cells} | ${r.elapsed_ms} |`),"","## Interaction evidence","",...e.interactions.map(i=>`- ${cell(i.name)}: ${i.passed?"PASS":"FAIL"}; ${cell(i.evidence)}`),"","## Agent evaluation","","Holdout is never used for tuning. Matching repeats use the same frozen telemetry and account isolation.","","| Set | Baseline S1 | Candidate S1 | Baseline intent | Candidate intent | Errors | Unchecked |","|---|---:|---:|---:|---:|---:|---:|",...e.evaluations.map(r=>`| ${r.set} | ${r.baseline_s1} | ${r.candidate_s1} | ${r.baseline_intent} | ${r.candidate_intent} | ${r.errors} | ${r.unchecked} |`),"","## S13 M2 capability preview comparison","","| Capability | Preview evidence | M2 evidence | Gap |","|---|---|---|---|",...e.parity.map(p=>`| ${cell(p.capability)} | ${cell(p.preview_evidence)} | ${cell(p.m2_evidence)} | ${cell(p.gap)} |`),"","## Screenshots","",...e.screenshots.map(s=>`- ${cell(s)}`),"","## Remaining defects and gates","",...(errors.length?errors.map(s=>`- ${cell(s)}`):["All gates passed against measured evidence."]),""].join("\n");
}
```

Create `.superpowers/replay/m2-report.ts` (Bun's existing runtime, no added package):

```ts
import {browserReport,checkBrowserEvidence,type BrowserEvidence} from "../../ui/panels/acceptance";
const input=".superpowers/replay/m2-browser-evidence.json";
const output="docs/benchmarks/2026-10-agent-dashboards-m2.md";
const evidence=await Bun.file(input).json() as BrowserEvidence;
await Bun.write(output,browserReport(evidence));
const errors=checkBrowserEvidence(evidence);
if(errors.length){for(const error of errors)console.error(error);process.exit(1);}
console.log(`Wrote ${output}`);
```

This writes the final benchmark document from measured values; do not check in invented successful numbers. A failed gate still writes a reviewable BLOCKED report and exits nonzero.

- [ ] **Step 4: Controller builds and replays the existing conversion tool**

The real input directory exists at the replay README's default path. The README currently records missing offline transitive modules (grpc-gateway/v2 v2.29.0 and grpc v1.82.1); the controller must first make those exact modules available in its local cache. Codex does not download dependencies or mutate replay's go.mod/go.sum. Do not accept an unverified prebuilt binary as a successful build. Run from repository root with the sandbox cache settings from Global Constraints:

```bash
rtk proxy env GOWORK=off go -C .superpowers/replay vet ./...
rtk proxy env GOWORK=off go -C .superpowers/replay build -o replay .
rtk proxy .superpowers/replay/replay -dry-run -in /private/tmp/claude-501/-Users-v-Projects-labstack-fanout/d2138802-fe1e-498f-9f11-dc2d731f6191/scratchpad/demo-v2 -signals traces,logs,metrics
rtk proxy .superpowers/replay/replay -in /private/tmp/claude-501/-Users-v-Projects-labstack-fanout/d2138802-fe1e-498f-9f11-dc2d731f6191/scratchpad/demo-v2 -endpoint http://127.0.0.1:7520 -signals traces,logs,metrics
```

Controller starts a disposable Fanout instance at its actual unified HTTP/OTLP address :7520 and injects FANOUT_REPLAY_TOKEN through the controller's secret store. Capture dry-run counts, successful replay counts, source maximum, target instant and signed shift; ensure counts agree and all three signals exist. A partial replay is a failed run: start another disposable data directory before retrying because the converter has no deduplication. Production still accepts only batch format 3; the independent tool converts format 2 into OTLP. Wait for the rollup cycle to publish service, edge and version contributions before the browser check; log the observed watermark/readiness, not a guessed sleep.

- [ ] **Step 5: Controller delegates the browser check and collects actual evidence**

Give a browser helper this exact work order after the implementation tests pass:

> Use the controller's disposable, authenticated http://127.0.0.1:7520 instance. Create the two Task 14 example dashboards through the existing dashboard UI/API. Inspect the fifteen-type dashboard with a 24h window in light and dark themes, including all nine new types and all six M1 types. Create a 20-panel copy by adding five independent timeseries panels through edit_dashboard; keep IDs unique and keep all fifteen originals. Capture screenshots under .superpowers/replay/m2-screenshots/ and record the actual saved spec and build HEAD. For each non-text type open Inspect and verify the visible frame and accessible summary; verify text content directly. Capture console errors for all types. Intercept/delay the existing panels response to see loading, replace individual results with empty/error/partial frames using browser network interception, then remove interception and prove recovery; no production data mutation. Check visible empty diagnosis, safe error text, retained partial/truncated data and loading indicators for every new type. Click a real grouped value and remove its chip; confirm $vars and panels change together. Move the linked crosshair across timeseries/heatmap/state_timeline, unmount/remount panels and confirm no leaked group. Brush once and verify exactly one pushed URL range and one Back operation restoring the old range; a pixel drag must not create a history entry per mousemove. Verify the per-panel range/shift badge and effective window without changing other panels. Open traces and logs drill drawers, reload the drill URL, use Back, select a trace and confirm the shared waterfall and correlated logs. Cancel an in-flight drill by closing and prove it cannot reopen/replace the next drill. Show deploy dashed lines and anomaly shaded areas with matching service filters and safe tooltips; confirm unrelated-service events are absent and unscoped panels include them. Verify before/since rate normalization and the visible no-deploy fallback. Exercise all seven table column formats and stat sparkline gaps/totals. Count requests during at least three settled 20-panel refreshes per theme: exactly one POST /api/panels/query and one POST /api/annotations per refresh (exclude variables/schema/drill traffic, initial StrictMode aborted requests and navigation). Sum returned frame rows × columns and record elapsed time from refresh initiation to visible results. Record every check in BrowserEvidence, with refresh rows for every observed refresh. Write .superpowers/replay/m2-browser-evidence.json only from actual observations; do not mark a blocked check passed. Compare M2 heatmap, log patterns, deploy markers, drill drawer, split bars and table formats with the actual capability preview in light and dark themes (S13). For each, record preview evidence, M2 evidence and a concrete gap or none in BrowserEvidence.parity. Reference the preview source the controller supplies; missing preview access is missing evidence. Record gaps in the results document, fixing in-scope gaps before PASS. Full board recreation and render-time S6 remain M4. Return reproducible defects with URLs, actions and screenshots. Do not alter implementation files or display credentials.

The controller supplies evaluation aggregates from Task 14's actual summary/report artifacts to the helper's evidence object: matching two-repeat means for S1 and intent_accuracy, per set, plus execution-error and validation_unchecked counts. `source` records the exact replay path, counts and timestamp bounds. Evidence includes where empty/error/partial interception was used so simulated states cannot be mistaken for demo-derived success. S8's denominator is complete refreshes, not individual panels. Refresh timing is descriptive in M2; the S6 render-time gate belongs to M4.

- [ ] **Step 6: Fix each demonstrated defect and rerun its owning checks**

For each browser/helper defect, first add the concrete reproducer to the owning test file: annotation query/rollup defects → Task 1 tests; scope/owner bypass → Tasks 2/10; oversized distribution/timeline → Tasks 3/4; empty/partial rendering → Tasks 7–8; duplicate history/group leaks → Task 9; stale drills → Task 10; refresh fanout/event matching → Task 12; deploy math → Task 11; formats → Task 13. Use the same complete fixture forms already supplied in those tasks, with the failing observed value and exact expected result. Run the owning test command to prove failure, make the smallest fix in that owning implementation, prove pass, then have the browser helper repeat the failed scenario and both-theme refresh sampling. Do not replace a measured failure with a narrative explanation. Keep a BLOCKED report if prerequisites or sealed holdout results prevent acceptance.

Run final checks after recorded defects are resolved:

```bash
rtk just test ./internal/panel/... ./internal/query/... ./internal/annotations/... ./internal/api/... ./internal/dashboard/... ./internal/observability/... ./internal/intelligence/... ./internal/mcp/... ./internal/agent/... ./internal/db/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk proxy sh -c 'cd ui/apps && bun run lint'
rtk just docs-generate
rtk just docs-generate-check
rtk just ui
rtk proxy bun .superpowers/replay/m2-report.ts
rtk just fmt
```

The final `just check` runs after Step 7 commits the task changes and both embedded asset directories; ui-check requires committed embedded assets.

Expected: all checks pass; report has actual fifteen-type coverage in both themes, bounded 24h payloads, one annotation request per refresh, reproduced drill/history behavior, and separate benchmark/holdout no-regression evidence. No checks are claimed here as executed results; this document is the implementation plan.

Before the controller commit, format all Go changes:

```bash
rtk just fmt
```

- [ ] **Step 7: Commit (controller)**

```bash
rtk git add ui/panels/acceptance.ts ui/host/src/dashboards/m2-acceptance.test.ts docs/benchmarks/2026-10-agent-dashboards-m2.md internal/panel internal/query internal/annotations internal/api internal/dashboard internal/observability internal/intelligence internal/mcp internal/agent ui/panels ui/host/src/dashboards internal/api/observability.go internal/api/observability_test.go cmd/fanout/main.go internal/ui/dist internal/mcp/apps site/src/content/docs/reference
rtk git commit -m "test(dashboards): verify milestone two against replay and browser evidence"
```

- [ ] **Step 8: Run the final check against committed assets (controller)**

After Step 6 runs `rtk just ui` and Step 7 commits the task changes, including `internal/ui/dist` and `internal/mcp/apps`, run:

```bash
rtk just check
```

If `just check` forces a fix, make the fix, rerun its owning checks, run `rtk just fmt` and `rtk just ui` as applicable, and make a follow-up controller commit containing the fix and any regenerated assets. Rerun `rtk just check` after that commit and repeat until it passes. Do not run ui-check while Codex leaves assets uncommitted: that recipe requires committed embedded assets and may restore them through git. Every browser task above ends with just ui and includes internal/ui/dist in its commit.
