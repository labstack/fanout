# Dedicated attribute columns and batch format 5

Status: Accepted
Date: 2026-10-31
Pull request: none yet

## Summary

Batch format 5 copies a fixed set of standard OpenTelemetry attributes into
typed Parquet columns. A mechanical rule over the stable semantic conventions
at v1.44.0 chooses the set of 47 keys, and the rule is fixed before any
measurement. Users keep writing `attributes['k']` or `k`. The compiler reads
the column instead of the VARIANT map, and the map stays complete.

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

Non-goals:

- Speeding up arbitrary custom keys.
- Changing the sort order or row-group size.
- Logs and metrics. They can follow the same rule later.

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

**Wrong types.** A column holds a value only if its OTLP type matches the
semconv type. For example, a string `"200"` for `http.response.status_code`
goes only to the map. A 64-bit `_spill` mask marks that row's bit for the key,
and the file footer records a count per key.

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

A query runs against a pinned file snapshot. If any file in the snapshot has
spilled values for a key, those files read `coalesce(column, map)`, which is
correct but slow. All other files read the column alone. Users cannot write
underscore-prefixed names. Catalog, suggestion and `get_telemetry_schema` show
the keys as attributes with an "indexed" mark.

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
- Offline verification also checks that column equals map value on every
  row without a spill bit.

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
2. **Use data the list was not derived from.**
   - The scratch generator emits a fixed attribute set. Extend it with a
     seeded mode that samples attribute sets from the v1.44.0 registry
     itself: stable, development and deprecated keys, keys outside the list,
     and values sent with the wrong type, such as its current string
     `"200"`.
   - Add a recorded corpus from an independent instrumentation set, such as
     Jaeger HotROD.
   - Do not use the OpenTelemetry Demo or the bench query set.
3. **Report regressions next to wins.** The report covers ingest CPU per span,
   allocation ratio, file size, compaction write amplification, startup and
   verification time, and spill-path query time, each with variance across
   seeds.

## Alternatives

- **Promote every key, as Honeycomb does.** That is VARIANT shredding plus
  extract pushdown, which DuckDB does not do across views today.
- **Bloom filters on map keys and values, as the ClickHouse exporter does.**
  Parquet bloom filters cover leaf columns, not VARIANT residuals, and do not
  help group-by.
- **Sort by time inside files.** This would break trace locality and the
  trace index.
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

- The column count grows by 47. Columns that are mostly null cost little,
  but the write path touches all of them.
- `rpc.method` loses its scalar column until RPC conventions are Stable, so
  RPC filters and group-bys get slower than on format 4.
- `attributes['k']` changes from VARIANT to a typed value, so
  `typeof(...)` results change.
- The ingest fallbacks for old spellings are removed, so producers that send
  old names lose fast paths until they convert upstream.

## Open questions

None of these blocks format 5.

1. Should `peer_service` and `rpc_service` stay as derived columns? Their
   sources, `peer.service` and `rpc.service`, are deprecated.
2. Should the messaging columns stay, given that the rule excludes them?
3. Should configurable columns be built later, and with how many slots?
