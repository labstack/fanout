# Dedicated attribute columns and batch format 5

Status: Accepted (amended 2026-11-01)
Date: 2026-10-31
Pull request: none yet

## Summary

Batch format 5 copies a fixed set of standard OpenTelemetry attributes into
typed Parquet columns. A mechanical rule over the stable semantic conventions
at v1.44.0 chooses the set of 47 keys, and the rule is fixed before any
measurement. Users keep writing `attributes['k']` or `k`. The compiler reads
the column instead of the VARIANT map, and the map stays complete.

The same format also changes log and metric storage, so there is one format
break and one data wipe. Metric files store the user-visible metric type as a
column and keep histogram buckets and summary quantiles as typed lists instead
of JSON strings. Exponential histograms keep their native scale and offsets.
Log and metric files gain a declared sort order, taken from peer practice, so
their row-group statistics can skip work. The 29 rule C resource columns
appear in span, log and metric files alike. Span events and links and metric
exemplars become typed lists, so no storage column holds JSON.

## Decisions

Recorded on 2026-10-31:

1. Existing format-4 data is deleted on the lab and production deployments.
   There is no converter and no legacy reader.
2. The seven merged format-4 columns are removed with no aliases.
3. The key rule is A and C below. An earlier draft had a rule B for required
   span attributes, such as `url.full`. It is dropped.
4. The rule reads semantic conventions v1.44.0. The evaluation thresholds
   stand as written.
5. No compatibility path is kept for keys the rule excludes. `rpc_method` is
   removed although `rpc.method` is still Release Candidate; RPC keys gain
   columns in the format that follows their Stable release.

Recorded on 2026-11-01:

6. Format 5 also carries the log and metric storage changes in the section
   "Logs, metrics, events and links" below, so there is one format break and one wipe. There
   is no backward compatibility: the old metric columns go without aliases.
   Sort keys and layouts come from standards and peer practice, thresholds
   are fixed before measuring, and the evaluation uses independent data,
   never the OpenTelemetry Demo.
7. Rule C resource columns apply to every signal: spans, logs and metrics
   get the same 29 keys under the same `_r_<key>` names. Each signal's file
   has its own footer spill counts. Rule A stays on spans.
8. Exemplars become typed list columns, following the ClickHouse exporter's
   exemplar layout.
9. The L1 thresholds stand as written: at most half of format 4's time at
   100M log rows a day, and within ±10% at 10M.
10. The new metric columns keep the `hist_` and `exp_` prefixes.
11. Span events and links become typed list columns, following the
    ClickHouse exporter's layout, and `events_json` and `links_json` are
    removed with no aliases. With decision 8, no storage column of any
    signal holds JSON.
12. The L4 and M4 resource-filter queries join the evaluation with the same
    target as M1 to M3: at most half of format 4's time.

The rule comes from the published standard alone. No key is added or removed
because of what Fanout's own test or lab data contains.

## Motivation

Span attributes live in the VARIANT `attributes` map and resource attributes
in `resource`. DuckDB decodes every row of each row group it reads. Span
files sort by trace hash, so each 50,000-row group covers the file's whole
time range. A filter on `attributes['http.target']` therefore costs time in
proportion to file size, not to the query window. At 2 threads and 1 GB it
took 0.17 s on 54k rows, 3.4 s on 2.1M rows and 10 s on 8.3M rows. The scalar
`http_route` column took under 0.07 s. Hourly compaction bounds narrow
windows, but 24-hour filters still time out.

Format 4 already has scalar columns: `http_method`, `http_status_code`,
`http_route`, `db_system`, `rpc_method`, `rpc_service`, `peer_service`,
`service_version`, `deployment_env`, `exception_type` and `exception_message`
(`internal/telemetry/parquet_rows.go`). Most of them merge a stable key with
its deprecated spelling at ingest (`internal/ingest/server.go`). The read
views project `messaging_system` and `messaging_destination` from shredded
VARIANT fields (`internal/engine/views.go`).

## Goals and non-goals

Goals:

- Filters and group-bys on standard keys cost in proportion to the window.
- One user-visible name per key.
- A key list that anyone can rederive from public sources.
- Metric type filters and histogram quantiles read typed columns, with no
  per-query JSON parsing.
- Service, metric name and time filters on logs and metrics skip row groups
  where the data allows it.

Non-goals:

- Speeding up arbitrary custom keys.
- Changing the span sort order, or the row-group size of any signal.
- Applying rule A to log record or metric point attributes. Rule A is
  derived from span duration metrics; rule C resource columns cover every
  signal.

## Peer survey

| System | Columns | How keys are chosen |
|---|---|---|
| Tempo vParquet4 | Fixed: `http.method`, `http.url`, `http.status_code`, `service.name`, `k8s.{cluster,namespace,pod,container}.name` and unprefixed `cluster`, `namespace`, `pod`, `container`. Also 10 configurable string slots for each of span and resource. [schema.go](https://github.com/grafana/tempo/blob/main/tempodb/encoding/vparquet4/schema.go) | A fixed list, plus user configuration |
| Tempo vParquet5 | No fixed span keys; only `service.name` keeps a resource column. 20 string and 5 integer slots for each of span, resource and event. Tempo 3.1 writes vParquet5 by default. [schema.go](https://github.com/grafana/tempo/blob/main/tempodb/encoding/vparquet5/schema.go) | The user configures each tenant. The docs advise choosing the attributes that contribute most to block size, and `tempo-cli analyse block` reports candidates. [docs](https://grafana.com/docs/tempo/latest/operations/dedicated_columns/) |
| OTel Collector ClickHouse exporter | `Map(LowCardinality(String), String)` columns for span and resource attributes, with bloom-filter skip indexes on their keys and values. Only `service.name` has its own column, `ServiceName`. [traces_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/traces_table.sql) | None |
| SigNoz | `LowCardinality` columns filled by a `DEFAULT` expression from the attribute maps, each with an `_exists` flag: `service.name`, `http.route`, `messaging.system`, `messaging.operation`, `db.system`, `rpc.system`, `rpc.service`, `rpc.method`, `peer.service`, and later `gen_ai.request.model`, `gen_ai.provider.name`, `gen_ai.agent.name` and `gen_ai.tool.name`. [traces_migrations.go](https://github.com/SigNoz/signoz-otel-collector/blob/main/cmd/signozschemamigrator/schema_migrator/traces_migrations.go). For logs, marking a field as selected adds the same kind of column and a skip index. [reader.go](https://github.com/SigNoz/signoz/blob/main/pkg/query-service/app/clickhouseReader/reader.go) | A fixed list, plus user selection for logs |
| Jaeger v2 ClickHouse | Typed nested key/value arrays. No dedicated columns. [create_spans_table.sql](https://github.com/jaegertracing/jaeger/blob/main/internal/storage/v2/clickhouse/sql/create_spans_table.sql) | None |
| Honeycomb | Every field is its own column, and new fields add columns. [blog](https://www.honeycomb.io/blog/why-observability-requires-distributed-column-store) | Automatic, all keys |
| OTel-Arrow | Separate attribute record batches with typed value columns. No promoted keys. [data_model.md](https://github.com/open-telemetry/otel-arrow/blob/main/docs/data_model.md) | None |

Peers that promote keys use a fixed list, a configurable one, or both. None
of them builds its fixed list from observed frequency. Tempo leaves the choice
to the operator and advises sizing it from their own blocks. Fixed lists age:
Tempo vParquet4 hard-coded pre-1.21 names such as `http.method`, and
vParquet5 dropped them. SigNoz still uses the deprecated `db.system`,
`rpc.service` and `peer.service`. A fixed list must therefore follow semconv
versions.

## Key selection rule (fixed before measuring)

The input is the semantic-conventions YAML model at tag
[v1.44.0](https://github.com/open-telemetry/semantic-conventions/tree/v1.44.0).
A committed script applies the rule, so the list cannot be edited by hand.

- **Span rule A.** Take every attribute at Required, Conditionally Required
  or Recommended level on a Stable duration metric among
  `http.server.request.duration`, `http.client.request.duration`,
  `db.client.operation.duration`, `rpc.server.call.duration` and
  `rpc.client.call.duration`. Semconv bounds the cardinality of metric
  attributes, so these are its own common group-by dimensions.
- **Resource rule C.** Take every Stable attribute in the `service`,
  `deployment`, `k8s`, `container`, `host` and `cloud` namespaces.
- **All rules.** Each key must be Stable and scalar. `<key>` templates are
  excluded. `service.name` and `service.namespace` are skipped because the
  existing `service` and `namespace` identity columns already store them.

Applied to v1.44.0, the rules give 47 keys. Every key is Stable at that tag.
Keys marked int are integers, and all others are strings.

- **Rule A, 18 keys.** From
  [model/http/metrics.yaml](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/model/http/metrics.yaml):
  `http.request.method`, `http.response.status_code` (int), `http.route`,
  `url.scheme`, `server.address`, `server.port` (int), `error.type`,
  `network.protocol.name` and `network.protocol.version`. Added by
  [model/db/metrics.yaml](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/model/db/metrics.yaml):
  `db.system.name`, `db.namespace`, `db.collection.name`,
  `db.operation.name`, `db.response.status_code`, `db.query.summary`,
  `db.stored_procedure.name`, `network.peer.address` and
  `network.peer.port` (int).
- **Rule C, 29 keys.** From the `registry.yaml` file of each namespace under
  [model/](https://github.com/open-telemetry/semantic-conventions/tree/v1.44.0/model):
  `service.version`, `service.instance.id`, `deployment.environment.name`,
  `container.id`, `container.image.name` and 24 `k8s.*` keys. These are the
  `name` and `uid` of cluster, node, deployment, replicaset, statefulset,
  daemonset, job and cronjob, plus `namespace.name`,
  `pod.{name,uid,hostname,ip,start_time}`, `container.name` and
  `container.restart_count` (int).

The generated tables in
[http-metrics](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-metrics.md),
[database-metrics](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/db/database-metrics.md),
[rpc-metrics](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/rpc/rpc-metrics.md)
and
[docs/registry/attributes](https://github.com/open-telemetry/semantic-conventions/tree/v1.44.0/docs/registry/attributes)
show the same levels and stability.

Excluded by the rule at v1.44.0:

- RPC. Both RPC duration metrics and every `rpc.*` attribute are Release
  Candidate. A later semconv release that makes them Stable adds
  `rpc.system.name`, `rpc.method` and `rpc.response.status_code` through a
  new format version.
- Messaging, because none of its keys is Stable.
- `db.query.text`, `url.template` and `user_agent.synthetic.type`, which are
  Opt-In on every rule A metric.
- All `host.*` and `cloud.*` keys, `container.name` and the other
  non-Stable keys in the rule C namespaces.
- `container.image.tags` and `container.image.repo_digests`, which are
  string arrays.
- The `k8s.<kind>.label.<key>` and `k8s.<kind>.annotation.<key>` templates.

The `exception.*` keys sit on span events and remain the existing
`exception_type` column.

**Renamed keys.** The v1.44.0 registry lists these renames:

- `http.method` → `http.request.method`
- `http.status_code` → `http.response.status_code`
- `http.target` → `url.path` + `url.query`
- `http.url` → `url.full`
- `http.scheme` → `url.scheme`
- `db.system` → `db.system.name`
- `db.name` → `db.namespace`
- `db.operation` → `db.operation.name`
- `db.statement` → `db.query.text`
- `rpc.system` → `rpc.system.name`
- `rpc.service`: folded into a fully qualified `rpc.method`
- `rpc.grpc.status_code` → `rpc.response.status_code`, as a string
- `deployment.environment` → `deployment.environment.name`
- `peer.service` → `service.peer.name`, which is Development

Fanout stores keys exactly as they are sent. Old spellings stay in the map
and can still be queried, but slowly. Only keys in the list get a column.
Producers that still send old names convert them upstream, either with the
Collector's
[schema processor](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/schemaprocessor)
(alpha for traces) or with a transform processor recipe in the docs. Fanout
itself carries no alias table. The ingest fallbacks for old spellings in
`internal/ingest/server.go` are removed together with the columns they fill.
A semconv upgrade reruns the script. A changed list is a schema change and
needs a new format version.

## Design

**Format 5.** Each key gets a nullable, typed column, with NULL meaning the
key is absent. Strings use dictionary encoding. Physical names are hidden:
`_a_<key>` for span keys and `_r_<key>` for resource keys, with dots turned
into underscores. The VARIANT maps still contain every key, so the canonical
map rule in AGENTS.md holds and trace detail is unchanged. Writing a copy
instead of moving the value avoids rebuilding maps in the read views.

Span files carry all 47 columns. Log and metric files carry the 29 rule C
columns under the same `_r_<key>` names, so `resource['k8s.pod.name']`
resolves the same way on every signal. Metric `series_id` still hashes the
complete resource map, so series identity does not change.

**Other types.** Ingest converts a value of another OTLP type to the key's
semconv type, and the column holds the result. An integer key takes an
integer, a double that is integral and within int64 range, and a string that
Go's `strconv.ParseInt` reads in base 10 exactly as sent, with no trimming.
For example, the string `"200"` for `http.response.status_code` becomes 200
and `503.0` becomes 503. A string key takes a string and the text of an
integer, a boolean or a double, written by Go's `strconv` (`503.0` becomes
`"503"`). Any other value, such as `503.5` or `true` for an integer key or
bytes, maps and lists for any key, has no conversion: the column is NULL and
the file footer counts it for the key as a spill. The map always keeps the
value as sent.

The following format-4 columns are removed with no aliases: `http_method`,
`http_status_code`, `http_route`, `db_system`, `rpc_method`,
`service_version` and `deployment_env`. Six of them map to keys that now have
dedicated columns. `rpc_method` does not, because `rpc.method` is Release
Candidate at v1.44.0. Filters on `rpc.method` read the VARIANT map until a
later format adds the key. `peer_service` and `rpc_service` contain Fanout
logic and stay as derived columns, which is open question 1.

**Resolution.** FanQL already reads `http.route` as `attributes['http.route']`
(0004, section 0.5). The compiler maps a key in the list to its hidden column.
The SQL guard does the same on the parsed AST for every
`attributes['<literal>']` and `resource['<literal>']` subscript. The
rewritten expression has the column's type, not VARIANT, so the existing
`::INTEGER` casts still work.

The column is the key's read value everywhere, so queries never read the map
for a key in the list and every file keeps its statistics and filter
pushdown. Users cannot write underscore-prefixed names. Catalog, suggestion
and `get_telemetry_schema` show the keys as attributes with an "indexed"
mark.

The read path implements this as follows (`internal/engine/sql_keys.go`):

- Snapshot binding rewrites every query, so FanQL, structured panels, panel
  SQL and product SQL share one rule. A subscript becomes the column when its
  map binds to a signal relation directly, or through subqueries and CTEs
  that pass it on with `*`. Rewritten select items keep their original
  result names.
- Every other subscript reads the map unchanged, which is the value as sent:
  keys outside the list, log and metric attributes, subscripts whose relation
  the AST does not determine, and subscripts of a map that a subquery or CTE
  projects without its columns. In that last case a value that ingest
  converted reads in its original type, as any key outside the list does.
- Unpinned views read the files' columns as they are.
- Variables compared with an integer key bind as numbers. The SQL guard and
  the filter gate reject the `_a_*`, `_r_*` and `_trace_hash` names and name
  the key to write instead.

**Shredding.** Format 4 shreds `messaging.*` and four `service.*` and
`deployment.*` resource keys. Format 5 stops shredding the keys that now have
dedicated columns, since shredding them would store a third copy, and keeps
shredding `messaging.*`.

**Views, rollups, compaction and verification.**

- The spans view exposes the hidden columns.
- Rollups, default dashboards and FanQL reference examples move from the old
  columns to keys.
- The read-cache schema version changes, so caches rebuild.
- Compaction merges format-5 inputs column for column and sums the spill
  counts. Its existing schema check already refuses mixed formats.
- Offline verification also checks, on every row, that each column equals
  the conversion of its map value, restated in SQL, and that the footer
  counts each key's values without a conversion.

**Existing format-4 data.** AGENTS.md and ADR 0004 forbid a legacy reader.
Fanout keeps refusing format-4 directories and preserves them, as it does
today. The operator deletes them before starting format 5. There is a
precedent: the data reset on 2026-10-11.

**Configurable columns.** Tempo-style per-install slots would help with
`app.*` and tenant keys. They would also make the physical schema vary per
install, which conflicts with the rule that a schema change needs a format
version. Footers would have to record the mapping, and compaction and query
rewriting would depend on that per-file mapping. They are deferred until
format 5 has been measured, which is open question 3.

## Logs, metrics, events and links

### Metric points today

Format 4 writes one row per OTLP data point
(`internal/ingest/server.go`, `internal/telemetry/parquet_rows.go`):

| OTLP point | Format 4 storage |
|---|---|
| Gauge | `metric_type` `gauge` and `value`. Integer points become DOUBLE. |
| Sum | `metric_type` `sum`, a `monotonic` flag, `temporality` and `value`. |
| Histogram | `hist_bounds_json` and `hist_counts_json` as JSON strings, `hist_count`, and `hist_sum`, which is 0 when the optional sum is absent. `min`, `max` and `flags` are dropped. |
| Exponential histogram | Converted at ingest to explicit bounds and counts in the same JSON strings. `scale`, the offsets and the zero threshold are not kept. `min`, `max` and `flags` are dropped. |
| Summary | Quantiles in `hist_bounds_json` and their values in `hist_counts_json`, with `hist_count` and `hist_sum`. |

Gauges, sums and both histogram kinds also store their exemplars as one JSON
string, `exemplars_json`.

Two costs follow. The metrics view computes the user-visible `type` with a
CASE over `metric_type` and `monotonic` (`internal/engine/views.go`), so a
`type` filter cannot reach the Parquet scan. Every histogram query parses
both JSON strings on every row with `from_json`
(`internal/query/metric_series.go`) before it can compute a quantile.

### Peer storage of metric points

| System | Type | Histogram buckets | Exponential histogram | Summary |
|---|---|---|---|---|
| OTel Collector ClickHouse exporter | One table per point kind; sums keep `IsMonotonic` and `AggregationTemporality` | `ExplicitBounds Array(Float64)`, `BucketCounts Array(UInt64)`, plus `Count`, `Sum`, `Min`, `Max`, `Flags`. [metrics_histogram_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/metrics_histogram_table.sql) | Native: `Scale`, `ZeroCount`, `PositiveOffset`, `PositiveBucketCounts Array(UInt64)`, `NegativeOffset`, `NegativeBucketCounts`. [metrics_exp_histogram_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/metrics_exp_histogram_table.sql) | `ValueAtQuantiles Nested(Quantile Float64, Value Float64)`, two parallel arrays. [metrics_summary_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/metrics_summary_table.sql) |
| Prometheus native histograms, also stored by Mimir | Series metadata | Classic histograms are one `_bucket` series per `le` bound | `schema`, `zero_threshold`, `zero_count`, and positive and negative bucket spans with deltas. The OTLP translator maps `scale` to `schema` and merges buckets above schema 8. [types.proto](https://github.com/prometheus/prometheus/blob/main/prompb/types.proto), [histograms.go](https://github.com/prometheus/prometheus/blob/main/storage/remote/otlptranslator/prometheusremotewrite/histograms.go), [spec](https://prometheus.io/docs/specs/native_histograms/), [Mimir](https://grafana.com/docs/mimir/latest/send/native-histograms/) | Separate `quantile`-labelled series |
| SigNoz | `LowCardinality` `type`, `temporality` and `is_monotonic` columns on the series table | One `.bucket` series per bound with an `le` label | A DDSketch aggregate in `exp_hist`, with `count`, `sum`, `min` and `max` | One `.quantile` series per quantile. [squashed_metrics_migrations.go](https://github.com/SigNoz/signoz-otel-collector/blob/main/cmd/signozschemamigrator/schema_migrator/squashed_metrics_migrations.go), [exporter.go](https://github.com/SigNoz/signoz-otel-collector/blob/main/exporter/signozclickhousemetrics/exporter.go) |

Tempo and Pyroscope store neither logs nor metrics. The ClickHouse exporter
is the only peer that stores OTLP points in their own shape, with typed
arrays and the exponential layout as sent. Format 5 follows it.

### Metric points in format 5

The physical `type` column holds Fanout's vocabulary, `gauge`, `counter`,
`updown`, `histogram` and `summary` (`internal/query/vocabulary.go`), as a
dictionary-encoded string. Each value names one OTLP point kind, except that
`histogram` covers both histogram kinds; a non-null `exp_scale` marks the
exponential one. The view projects `type` unchanged, so a `type` filter
reaches the scan. `metric_type` and `monotonic` are removed, since `type`
carries both. `temporality` is NULL for gauges and summaries.

| OTLP point | Format 5 columns |
|---|---|
| Gauge | `value DOUBLE` |
| Sum | `value DOUBLE`, `temporality` |
| Histogram | `hist_bounds LIST<DOUBLE>`, `hist_counts LIST<UBIGINT>` with one more entry than the bounds, `hist_count UBIGINT`, and nullable `hist_sum`, `hist_min` and `hist_max` DOUBLE |
| Exponential histogram | `exp_scale INTEGER`, `exp_zero_count UBIGINT`, `exp_zero_threshold DOUBLE`, `exp_positive_offset INTEGER`, `exp_positive_counts LIST<UBIGINT>`, `exp_negative_offset INTEGER`, `exp_negative_counts LIST<UBIGINT>`, and the four `hist_` scalars |
| Summary | `quantiles LIST<DOUBLE>`, `quantile_values LIST<DOUBLE>`, `hist_count`, `hist_sum` |

Every kind also gains `flags UINTEGER` from the OTLP `DataPointFlags`
([metrics.proto](https://github.com/open-telemetry/opentelemetry-proto/blob/main/opentelemetry/proto/metrics/v1/metrics.proto)).
Columns that do not apply to a point are NULL. An absent histogram sum is
NULL, not 0. Gauges and sums keep DOUBLE values for integer points, as the
ClickHouse exporter does.

**Exemplars.** The ClickHouse exporter stores exemplars as
`Exemplars Nested(FilteredAttributes Map(LowCardinality(String), String),
TimeUnix DateTime, Value Float64, SpanId String, TraceId String)`, which
ClickHouse keeps as five parallel arrays
([metrics_histogram_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/metrics_histogram_table.sql)).
Format 5 uses the same five parallel lists, one entry per exemplar:
`exemplar_times LIST<TIMESTAMP_NS>`, `exemplar_values LIST<DOUBLE>`,
`exemplar_trace_ids LIST<VARCHAR>`, `exemplar_span_ids LIST<VARCHAR>` and
`exemplar_attributes LIST<VARIANT>`. Filtered attributes are typed VARIANT
maps like `attributes`, not string maps, so the canonical map rule holds.
Integer exemplar values become DOUBLE, as point values do. Summaries have no
exemplars, and their lists are NULL. `exemplars_json` is removed.

### Span events and links

Format 4 stores span events and links as the JSON strings `events_json` and
`links_json` (`eventsToJSON` and `linksToJSON` in `internal/ingest/server.go`).
Event and link attribute values are converted to strings there, so their
types are lost. Dropped attribute, event and link counts are not stored.

The ClickHouse exporter stores `Events Nested(Timestamp DateTime64(9), Name
LowCardinality(String), Attributes Map(...))` and `Links Nested(TraceId
String, SpanId String, TraceState String, Attributes Map(...))`
([traces_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/traces_table.sql)).
Format 5 uses the same parallel lists, one entry per event or link:

- `event_times LIST<TIMESTAMP_NS>`, `event_names LIST<VARCHAR>` and
  `event_attributes LIST<VARIANT>`.
- `link_trace_ids LIST<VARCHAR>`, `link_span_ids LIST<VARCHAR>`,
  `link_trace_states LIST<VARCHAR>` and `link_attributes LIST<VARIANT>`.

Attributes are typed VARIANT maps, as for exemplars, so event and link
values keep their OTLP types. Format 5 adds no dropped counts, since format 4
stores none. The span's own `trace_state` stays a scalar column, and
`exception_type` and `exception_message` are still derived from events.
`events_json` and `links_json` are removed with no aliases. Verification
checks that the event lists of a row have equal lengths, and so do the link
lists.

**No JSON remains.** After these changes no column in span, log or metric
files holds JSON. The remaining strings are scalar text such as `body`,
`status_message` and `trace_state`; maps are VARIANT and repeated values are
typed lists. The query layer still converts nested results to JSON at the
client boundary, as AGENTS.md requires.

Ingest no longer converts exponential histograms. The quantile path derives
the upper bound of each bucket from `exp_scale` and the bucket index with the
formula `explicitExpHistogram` uses today, including its merge of bounds that
overflow, and then runs the existing union-of-bounds quantile. The lists are
native, so this is list arithmetic, not parsing. The checks on bound order and
list lengths in `metric_series.go` move to ingest, which rejects a malformed
point with `InvalidArgument` as it does for a bad zero threshold.

The format-4 columns `metric_type`, `monotonic`, `hist_bounds_json` and
`hist_counts_json` are removed with no aliases. Public SQL that reads them
fails and must move to the new columns.

### Sort order

Spans keep `_trace_hash, start_unix_nano, span_id`. Trace locality and the
trace index depend on it, and the alternative is rejected below.

Logs and metrics have no declared order today. Ingest writes them in arrival
order (`internal/telemetry/parquet.go`), and compaction concatenates inputs
(`internal/telemetry/parquet_merge.go`), so every row group spans every
service and metric of its file. Peers order these tables as follows:

| System | Logs | Metrics |
|---|---|---|
| OTel Collector ClickHouse exporter | `ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)`, daily partitions. [logs_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/logs_table.sql) | `ORDER BY (ServiceName, MetricName, toStartOfHour(TimeUnix), cityHash64(Attributes), TimeUnix)`, daily partitions, for every point kind. [metrics_sum_table.sql](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/clickhouseexporter/internal/sqltemplates/metrics_sum_table.sql) |
| SigNoz | `logs_v2`: `ORDER BY (ts_bucket_start, resource_fingerprint, severity_text, timestamp, id)` with 30-minute buckets. [squashed_logs_migrations.go](https://github.com/SigNoz/signoz-otel-collector/blob/main/cmd/signozschemamigrator/schema_migrator/squashed_logs_migrations.go), [exporter.go](https://github.com/SigNoz/signoz-otel-collector/blob/main/exporter/clickhouselogsexporter/exporter.go) | `samples_v4`: `ORDER BY (env, temporality, metric_name, fingerprint, unix_milli)` |
| Loki, Prometheus, Mimir | Loki compresses each stream, one label set, into its own time-ordered chunks. [architecture](https://grafana.com/docs/loki/latest/get-started/architecture/) | Prometheus TSDB and Mimir store each series' samples in time-ordered chunks inside time-bounded blocks. [storage](https://prometheus.io/docs/prometheus/latest/storage/) |

All of them put either a coarse time bucket or the stream or series identity
first, and time last. Format 5 adopts the ClickHouse exporter's keys, the
OTel-native peer, with Fanout's `namespace` placed before `service`:

- **Logs:** five-minute bucket of `log_time`, `namespace`, `service`,
  `log_time`.
- **Metrics:** `namespace`, `service`, `name`, hour of `metric_time`,
  `series_id`, `metric_time`. `series_id` already hashes the series identity
  (`internal/telemetry/metric_series.go`), as `cityHash64(Attributes)` does
  for the exporter.

The bucket values are computed from the time column and not stored. The
Parquet footer's `sorting_columns` can name only physical columns, so log
and metric files leave it empty. The order is a format-5 invariant that the
writer, compaction and offline verification enforce.

**Row groups.** The ceiling stays at 50,000 rows for every signal. DuckDB
skips a row group whose min and max statistics exclude a filter, and the sort
makes those ranges narrow. Metric files then separate services and names.
Log files separate time buckets, and separate services inside a bucket only
when a bucket holds more rows than a row group. This is the expected result
of the peer order, not a tuning target, and the evaluation reports it.

**Interactions.**

- *Compaction.* The log and metric merges become k-way merges on these keys,
  like the span merge, reading only the key columns to build the input-row
  schedule. Hour merges and day merges both take sorted inputs and produce a
  sorted output. The merge refuses repeated columns today, so the column
  copier must advance list columns by whole rows.
- *Late data.* A late row sorts by its event time within its ingest-hour
  file. It lands in its own bucket at the front of a log file, so the other
  row groups keep tight time ranges.
- *Footer bounds.* Dashboard file snapshots keep reading event-time bounds
  from row-group statistics (`parquetTimeRange`). Sorting changes no file's
  bounds, and an unknown statistic still includes the file.
- *Read cache.* For logs and metrics it holds only per-batch resource
  inventory, an aggregate that does not depend on row order. The cache
  version changes with the schema, and caches rebuild.
- *Verification.* Offline verification checks the complete log and metric
  sort tuples, as it does for spans, that list lengths match their kind,
  and that the five exemplar lists of a row have equal lengths.

## Evaluation plan

1. **Fix the criteria first.** Commit the rule, the key list, the queries and
   these thresholds before any run:
   - Rule A and C equality filters and group-bys over 24 hours on 8.3M-row
     files complete within 2× of today's `http_route` time.
   - Keys outside the list are no slower than format 4, within ±10%.
   - Ingest throughput regresses by no more than 10%.
   - Commit memory regresses by no more than 10%.
   - Bytes per span increase by no more than 15%.
   - Compaction time and bytes written increase by no more than 15%.
   - Every query returns rows identical to the same query on format 4.
2. **Fix the log and metric queries and criteria first.** Each query runs
   over 24 hours against format 4 on the same data. Logs run at 10M and
   100M rows a day; only the larger volume puts more than 50,000 rows in a
   five-minute bucket. Metrics run at 10M points a day.
   - L1, service filter: log count for one service per 5-minute bucket.
   - L2, severity filter: `severity_number >= 17`, counted by service.
   - L3, body search: a substring match on `body`, over 1 hour and 24 hours.
   - M1, metric by name: one counter's rate per service.
   - M2, type filter: every `type = 'histogram'` series, counted by name.
   - M3, histogram quantile: p95 of one duration histogram per service, once
     with explicit and once with exponential buckets.
   - L4 and M4, resource filter: an equality filter on
     `resource['k8s.namespace.name']`, counted by service, on logs and on
     metrics.

   The criteria:
   - M1 to M4, L4, and L1 at 100M rows, complete in at most half of
     format 4's time.
   - L1 at 10M rows, L2 and L3 stay within ±10% of format 4. Below a row
     group per bucket the log sort cannot separate services, and neither
     severity nor body is a sort key.
   - Log and metric ingest throughput and commit memory regress by no more
     than 10%, sort included.
   - Bytes per log row and per metric point, and compaction time and bytes
     written, increase by no more than 15%.
   - Every query returns rows identical to format 4, except where format 4
     was wrong: an absent histogram sum reads NULL, not 0, and event and
     link attribute values keep their OTLP types instead of strings.
3. **Use data the list was not derived from.**
   - The scratch generator emits a fixed attribute set. Extend it with a
     seeded mode that samples attribute sets from the v1.44.0 registry
     itself: stable, development and deprecated keys, keys outside the list,
     and values sent with the wrong type, such as its current string
     `"200"`.
   - Add a recorded corpus from an independent instrumentation set, such as
     Jaeger HotROD.
   - The same seeded mode emits metrics from the v1.44.0 metric definitions:
     each Stable metric with its instrument kind, unit and attribute list,
     histograms with the explicit bucket boundaries semconv advises and,
     with the same seed, as exponential histograms at several scales,
     and a fixed share of summaries, which semconv does not define. It
     covers both temporalities, integer and double points, absent sums and
     malformed points.
   - It emits logs with every severity number, including unset, and with
     resources sampled from the rule C namespaces. Bodies come from a
     public log corpus such as LogHub, so body search runs on real text.
     A fixed share arrives late, with event times hours before ingest.
   - Add a recorded metric corpus from the Collector's hostmetrics receiver.
   - Do not use the OpenTelemetry Demo or the bench query set.
4. **Report regressions next to wins.** The report covers ingest CPU per span,
   allocation ratio, file size, compaction write amplification, startup and
   verification time, and query time on wrong-typed data, each with variance across
   seeds. For logs and metrics it adds sort time at ingest and the share of
   row groups each query skipped.

## Alternatives

- **Promote every key, as Honeycomb does.** That is VARIANT shredding plus
  extract pushdown, which DuckDB does not do across views today.
- **Bloom filters on map keys and values, as the ClickHouse exporter does.**
  Parquet bloom filters cover leaf columns, not VARIANT residuals, and do not
  help group-by.
- **Sort spans by time inside files.** This would break trace locality and
  the trace index.
- **Keep converting exponential histograms at ingest.** It keeps one bucket
  layout for queries, but it loses `scale` and the offsets, which the
  ClickHouse exporter and Prometheus both keep. Rejected.
- **Prometheus spans and deltas for buckets.** They compress sparse buckets
  in a row store. Parquet's dictionary, run-length and delta encodings
  already do that for dense lists, and OTLP sends dense lists. Rejected.
- **One row per bucket, as SigNoz and classic Prometheus histograms do.** It
  multiplies metric rows by the bucket count and moves quantiles into a
  join. Rejected.
- **Lists inside VARIANT.** The merge would keep only non-repeated columns,
  but every query would cast VARIANT to a list on every row. Rejected.
- **Rule B: required span attributes.** It added `url.path`, `url.query`,
  `url.full`, `http.request.method_original` and `rpc.method_original`. These
  are unbounded strings that roughly double their bytes when copied, and
  semconv does not use them as metric dimensions. Rejected.
- **Convert format-4 data instead of deleting it.** A one-time rewrite at
  startup is a format-4 reader inside the binary, which AGENTS.md forbids. A
  converter kept outside the release must reproduce the exact format-5
  schema and footer. Either way, conversion decodes every row and blocks
  ingest while it runs. Rejected.

## Risks

- Span files grow by 47 columns, and log and metric files by 29 resource
  columns that are mostly null, plus the new metric and exemplar columns.
  Columns that are mostly null cost little, but the write path touches all
  of them on every signal.
- `rpc.method` loses its scalar column until RPC conventions are Stable, so
  RPC filters and group-bys get slower than on format 4.
- `attributes['k']` changes from VARIANT to a typed value, so
  `typeof(...)` results change.
- The ingest fallbacks for old spellings are removed, so producers that send
  old names lose fast paths until they convert upstream.
- Exemplar, event and link attributes need a list of VARIANT values, a nesting the writer
  and DuckDB's reader have not yet carried in Fanout. It must be proven
  before the format ships.
- List columns break the merge's rule of one physical value per row. The
  column copier must handle repetition levels, and a mistake there corrupts
  histograms silently. Verification checks list lengths for this reason.
- Sorting logs and metrics costs ingest CPU and memory, and turns their
  compaction from concatenation into a k-way merge.
- Service filters on logs skip row groups only when a five-minute bucket
  holds more than 50,000 rows. Small installs gain little on L1.
- Exponential quantiles compute bounds per query instead of once at ingest.
- Public SQL and saved dashboards that read `metric_type`, `monotonic`,
  `exemplars_json`, `events_json`, `links_json` or the `hist_*_json`
  columns break. The trace detail view reads `events_json` for span details
  (`ui/host/src/dashboards/trace/span-extras.ts`) and moves to the event
  lists in the same change.

## Open questions

None of these blocks format 5.

1. Should `peer_service` and `rpc_service` stay as derived columns? Their
   sources, `peer.service` and `rpc.service`, are deprecated.
2. Should the messaging columns stay, given that the rule excludes them?
3. Should configurable columns be built later, and with how many slots?
