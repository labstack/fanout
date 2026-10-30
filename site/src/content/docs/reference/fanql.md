---
title: "FanQL"
description: "The preview text syntax for structured telemetry queries."
summary: "FanQL syntax, fields, functions, variables, null semantics and examples, generated from query registries."
status: preview
generated: true
---

# FanQL

Preview: the syntax may change before GA

FanQL is the text form of a structured telemetry query. It produces the same `panel.Query` that dashboards store and the panel engine executes. Panels store the query; text is derived. Problems prevent execution; notices never block it. Each diagnostic says what happened and what to write instead, using a valid example from that field. Diagnostics use display names without internal visualization identifiers and report all independent problems in one pass, in source order. Only follow-on cascades are suppressed.

## Shape and entry points

`source [where] filters | measures by fields every bucket sort key limit n`

Explore accepts a whole query. Logs and Traces search bars accept filters only; the page supplies source, `where` and visualization. The visualization picker accepts the query as written. Adding `| count() every auto` to make a timeseries is a visible edit. One aggregation stage is allowed; another pipe may introduce only `sort` or `limit`, and canonical text drops that pipe.

## Sources and words

Sources: `logs`, `metrics`, `spans`, recognized only as the first token. A bare source selects all rows in the chosen time range. `where` is optional; canonical text supplies it when filters exist.

Clause words: `by`, `every`, `limit`, `sort`, `where` and `|`, recognized at depth zero, outside quotes and brackets, at a term boundary. After operators they are values (`service:where`, `service in (sort, limit)`). Quote a clause word to search for it.

Expression words: `and`, `between`, `ilike`, `in`, `is`, `like`, `not`, `null`, `or`, recognized only in their grammatical positions. Modifiers: `as`, `asc`, `desc`. Keywords, columns, functions, units and vocabulary aliases ignore case. Values and attribute/resource keys preserve case.

A word is a field only before a field operator; otherwise it searches log `body` or span `operation`. Metrics require an explicit field. Quotes use doubling as the only escape; backslashes are literal. Quoted `api*` and embedded `$` stay quoted. Wildcards are unsupported except `k:*` and `count(*)`. A leading `-` is negation except when a numeric value is expected. `+` is legal in a sort key or exponent.

## Operators and missing values

| Operator | Meaning |
|---|---|
| `:` | Equality with vocabulary aliases; missing values are excluded. |
| `=` | Equality, with the same typing and alias rules as `:`. |
| `!=` | SQL spelling, SQL meaning: rows without `k` are excluded; `-k:v` includes them. |
| `<>` | SQL inequality, equivalent to `!=`; missing values are excluded. |
| `>` | Greater than; missing values are excluded. |
| `>=` | Greater than or equal; missing values are excluded. |
| `<` | Less than; missing values are excluded. |
| `<=` | Less than or equal; missing values are excluded. |
| `~` | Case-insensitive substring; `%` and `_` are ordinary characters. |
| `!~` | Null-inclusive substring exclusion, like `-k ~ v`. |
| `in` | Membership in a nonempty list or multi-value variable; missing values are excluded. |
| `not in` | SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them. |
| `between` | Inclusive lower and upper bounds separated by `and`; missing values are excluded. |
| `not between` | SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them. |
| `is null` | Matches missing values, canonical `-k:*`. |
| `is not null` | Matches present values, canonical `k:*`. |
| `like` | Case-sensitive SQL pattern; requires quoted text; `%` and `_` are wildcards. |
| `ilike` | Case-insensitive SQL pattern; requires quoted text. |
| `not like` | SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them. |
| `not ilike` | SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them. |

Prefix `-` or `not` excludes a term and includes rows where its predicate is NULL. Repeated negation cancels in pairs. SQL operators retain SQL null behavior. Compound negation follows De Morgan's law. TRY_CAST failures are NULL, so `-http_status_code between 400 and 599` includes uncastable codes.

Whitespace and `and` join filters. An `or` chain must occupy its whole filter list or be parenthesized: `a:1 (b:2 or c:3)` and `(a:1 b:2) or c:3` differ. This rule also applies inside groups and `count_if`. Mixing conjunction and `or` without parentheses is a problem with suggested repairs.

## Fields

A single identifier is a source column when one exists, otherwise an attribute key with a notice. Dotted `http.target` means `attributes['http.target']`; `resource.k8s.pod.name` means `resource['k8s.pod.name']`. Explicit `attributes.<path>` uses the attributes map. Bracket keys are literal nonempty strings, including spaces, Unicode, punctuation and doubled quotes. Keys beginning `resource.` or `attributes.` retain brackets where needed to preserve the map.

Canonical text brackets single-segment attribute keys, so new columns cannot reinterpret canonical or stored text. Map columns require a key. `duration` denotes `duration_ms` in filters, measure arguments and trace sorts. `duration_ms>500` takes a number; `duration>500ms` requires a unit. A measure alias named duration or duration_ms stays that alias in sort. Time columns accept range comparisons against quoted timestamps, not equality shorthand.

### spans

| Name | Type | Unit | Low cardinality | Description |
|---|---|---|---|---|
| `namespace` | `string` | `none` |  | OpenTelemetry service namespace |
| `service` | `string` | `none` | yes | Service name |
| `operation` | `string` | `none` |  | Span name |
| `kind` | `string` | `none` | yes | SPAN_KIND_CLIENT, SPAN_KIND_CONSUMER, SPAN_KIND_INTERNAL, SPAN_KIND_PRODUCER, SPAN_KIND_SERVER, SPAN_KIND_UNSPECIFIED |
| `status` | `string` | `none` | yes | STATUS_CODE_ERROR, STATUS_CODE_OK, STATUS_CODE_UNSET |
| `status_message` | `string` | `none` |  | Status description |
| `trace_id` | `string` | `none` |  | Trace identifier |
| `span_id` | `string` | `none` |  | Span identifier |
| `parent_span_id` | `string` | `none` |  | Parent span identifier; empty for roots |
| `start_time` | `time` | `none` |  | Span start |
| `end_time` | `time` | `none` |  | Span end |
| `duration_ms` | `number` | `ms` |  | Span duration in milliseconds |
| `http_method` | `string` | `none` | yes | HTTP method |
| `http_status_code` | `string` | `none` |  | HTTP response status code as text |
| `http_route` | `string` | `none` |  | HTTP route template |
| `db_system` | `string` | `none` | yes | Database system, e.g. postgresql, redis |
| `rpc_method` | `string` | `none` |  | RPC method |
| `rpc_service` | `string` | `none` |  | RPC service |
| `peer_service` | `string` | `none` |  | Remote service named by the caller |
| `service_version` | `string` | `none` | yes | service.version resource attribute |
| `deployment_env` | `string` | `none` | yes | deployment.environment resource attribute |
| `exception_type` | `string` | `none` |  | Exception type from the span's exception event |
| `exception_message` | `string` | `none` |  | Exception message |
| `messaging_system` | `string` | `none` | yes | messaging.system attribute |
| `messaging_destination` | `string` | `none` |  | messaging.destination.name attribute |
| `scope_name` | `string` | `none` |  | Instrumentation scope |
| `attributes` | `map` | `none` |  | Span attributes; use attributes['key'] |
| `resource` | `map` | `none` |  | Resource attributes; use resource['key'] |

### logs

| Name | Type | Unit | Low cardinality | Description |
|---|---|---|---|---|
| `namespace` | `string` | `none` |  | OpenTelemetry service namespace |
| `service` | `string` | `none` | yes | Service name |
| `time` | `time` | `none` |  | Log time |
| `severity` | `string` | `none` | yes | Severity text, e.g. ERROR, WARN, INFO |
| `severity_number` | `number` | `none` |  | OpenTelemetry severity number; 17 and above is an error |
| `body` | `string` | `none` |  | Redacted log body |
| `body_template` | `string` | `none` |  | Body with variable parts replaced by &lt;*&gt;; groups similar messages |
| `trace_id` | `string` | `none` |  | Trace identifier |
| `span_id` | `string` | `none` |  | Span identifier |
| `scope_name` | `string` | `none` |  | Instrumentation scope |
| `attributes` | `map` | `none` |  | Log attributes; use attributes['key'] |
| `resource` | `map` | `none` |  | Resource attributes; use resource['key'] |

### metrics

| Name | Type | Unit | Low cardinality | Description |
|---|---|---|---|---|
| `namespace` | `string` | `none` |  | OpenTelemetry service namespace |
| `service` | `string` | `none` | yes | Service name |
| `time` | `time` | `none` |  | Data point time |
| `name` | `string` | `none` |  | Metric name |
| `unit` | `string` | `none` |  | Metric unit as reported |
| `type` | `string` | `none` | yes | counter, gauge, histogram, summary, updown |
| `temporality` | `string` | `none` | yes | cumulative or delta; empty for gauge and summary |
| `start_time` | `time` | `none` |  | Data point start time; NULL when absent |
| `value` | `number` | `none` |  | Gauge or sum value |
| `hist_count` | `number` | `count` |  | Histogram point count |
| `hist_sum` | `number` | `none` |  | Histogram point sum |
| `attributes` | `map` | `none` |  | Data point attributes; use attributes['key'] |
| `resource` | `map` | `none` |  | Resource attributes; use resource['key'] |

`http_status_code` is text. `<`, `<=`, `>`, `>=` and `between` use `TRY_CAST(http_status_code AS INTEGER)` for literals and variables alike. Equality and membership stay text, so `http_status_code:500` means `'500'`.

## Vocabularies and severity

Aliases apply under equality, inequality and membership, including prefix negation. Quoted values, contains, SQL patterns and function arguments are literal. Stored spellings are also accepted bare.

### status (spans)

| Input | Stored |
|---|---|
| `error` | `STATUS_CODE_ERROR` |
| `ok` | `STATUS_CODE_OK` |
| `unset` | `STATUS_CODE_UNSET` |

### kind (spans)

| Input | Stored |
|---|---|
| `client` | `SPAN_KIND_CLIENT` |
| `consumer` | `SPAN_KIND_CONSUMER` |
| `internal` | `SPAN_KIND_INTERNAL` |
| `producer` | `SPAN_KIND_PRODUCER` |
| `server` | `SPAN_KIND_SERVER` |
| `unspecified` | `SPAN_KIND_UNSPECIFIED` |

### type (metrics)

| Input | Stored |
|---|---|
| `counter` | `counter` |
| `gauge` | `gauge` |
| `histogram` | `histogram` |
| `summary` | `summary` |
| `updown` | `updown` |

### severity (logs)

| Input | Stored |
|---|---|
| `alert` | `FATAL` |
| `crit` | `FATAL` |
| `critical` | `FATAL` |
| `dbg` | `DEBUG` |
| `debug` | `DEBUG` |
| `emerg` | `FATAL` |
| `emergency` | `FATAL` |
| `err` | `ERROR` |
| `error` | `ERROR` |
| `exception` | `ERROR` |
| `fatal` | `FATAL` |
| `fine` | `DEBUG` |
| `finest` | `TRACE` |
| `info` | `INFO` |
| `information` | `INFO` |
| `informational` | `INFO` |
| `notice` | `INFO` |
| `panic` | `FATAL` |
| `severe` | `ERROR` |
| `trace` | `TRACE` |
| `verbose` | `TRACE` |
| `warn` | `WARN` |
| `warning` | `WARN` |

Severity bands, lowest first: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`. `unspecified` maps to `UNSPECIFIED`; unknown names are uppercased. Trailing digits are dropped (`INFO2` becomes `INFO`). Unknown severity is legal for equality; sampled stored levels may provide a notice. Unknown status or kind aliases are problems.

Every stored log severity is normalized once: a positive severity number has authority (21+ FATAL, 17+ ERROR, 13+ WARN, 9+ INFO, 5+ DEBUG, 1+ TRACE); absent/nonpositive numbers fall back to text. Invariant: no stored log row bypasses normalization. Severity ranges expand to contiguous band membership, never CASE. `severity>=warn` means WARN, ERROR, FATAL; `severity<error` means TRACE, DEBUG, INFO, WARN; `between info and error` is inclusive. Unknown and UNSPECIFIED levels have no rank and never satisfy a range. Use `severity_number>=14` for sublevels. Empty or reversed ranges are problems.

On normalized log rows, `error_rate()` counts exactly `severity>=error`. Its backend test is `upper(severity) IN ('ERROR', 'FATAL', 'CRITICAL') OR severity_number >= 17`; normalization makes legacy CRITICAL redundant. Span `error_rate()` counts `status:error`.

## Literals and durations

| Literal | Rule |
|---|---|
| String | Bare words or single/double quotes; double a quote to escape it. Quoted values are strings and bypass aliases. |
| Number | Integers, decimals and exponents (`500`, `1.5`, `2e3`, `-3`); non-finite values are rejected. Numbers stay numeric on number columns and attributes, and stay text as typed on string columns. |
| Boolean | Bare `true`/`false`, case-insensitive; attributes retain boolean type, string columns compare text, number columns reject them. |
| Duration | `us`, `ms`, `s`, `m`, `h`, only on `duration`; converted to milliseconds. Examples: `500ms`, `1.5s`, `250us`, `2m`, `1h`. Negative, compound and overflowing durations are rejected. |
| Timestamp | Quoted RFC 3339 or `YYYY-MM-DD HH:MM:SS`, optional fraction/offset; no offset means UTC. Time comparisons retain nanoseconds through TIMESTAMPTZ_NS. |
| Existence | `k:*` is IS NOT NULL; `-k:*` is IS NULL. |
| Free text | Case-insensitive substring of log body or span operation; explicit field required on metrics. |

Contains emits `contains(lower(field), lower(value))`; map values first cast to VARCHAR. SQL LIKE/ILIKE require quoted patterns; ESCAPE is not expressible, so use `~` when `%` should be literal.

## Buckets

`auto`, `10s`, `30s`, `1m`, `5m`, `10m`, `15m`, `30m`, `1h`, `3h`, `6h`, `12h`, `1d`. `auto` lets the engine choose an interval. A bucket follows `every` and belongs to an aggregate query.

## Measures

The registry defines accepted functions and signal availability. Fields follow the filter/grouping resolution rules. Numeric attribute measures use TRY_CAST AS DOUBLE; nonnumeric values become NULL. `count(*)` canonicalizes to `count()`.

| Name | Form | Signals | Unit | Semantics |
|---|---|---|---|---|
| `avg` | `avg(f)` | spans, logs, metrics | field unit | Numeric average. Metrics: gauge/updown point average; histogram sum/count; counter has no value and reports a diagnosis. |
| `count` | `count()` | spans, logs, metrics | count | Rows; metric observation/point count according to kind. |
| `count_distinct` | `count_distinct(f)` | spans, logs, metrics | count | Distinct values of any field, including string attributes. |
| `error_rate` | `error_rate()` | spans, logs | percent | Spans: percent status:error. Logs: percent severity>=error on normalized rows. Unavailable for metrics. |
| `last` | `last(f)` | metrics | field unit | Metrics only: kind-aware last value. |
| `max` | `max(f)` | spans, logs, metrics | field unit | Maximum numeric value, retaining metric-kind semantics. |
| `min` | `min(f)` | spans, logs, metrics | field unit | Minimum numeric value, retaining metric-kind semantics. |
| `p50` | `p50(f)` | spans, logs, metrics | field unit | Registered percentile of numeric observations; metric distributions retain native semantics. |
| `p75` | `p75(f)` | spans, logs, metrics | field unit | Registered percentile of numeric observations; metric distributions retain native semantics. |
| `p90` | `p90(f)` | spans, logs, metrics | field unit | Registered percentile of numeric observations; metric distributions retain native semantics. |
| `p95` | `p95(f)` | spans, logs, metrics | field unit | Registered percentile of numeric observations; metric distributions retain native semantics. |
| `p99` | `p99(f)` | spans, logs, metrics | field unit | Registered percentile of numeric observations; metric distributions retain native semantics. |
| `quantile` | `quantile(f, q)` | spans, logs, metrics | field unit | Quantile q strictly between 0 and 1; numeric observations or native metric distribution semantics. |
| `rate` | `rate()` | spans, logs, metrics | per_second | Spans/logs: rows per second over bucket/window. Metrics: coverage-weighted counter increase per second; counters only. |
| `share` | `share()` | spans, logs, metrics | percent | Percent of scoped rows in the group; metrics use series population. Standalone only. |
| `sum` | `sum(f)` | spans, logs, metrics | field unit | Numeric total; metric counters use increases instead of summing cumulative points. |

Metric aggregates require a standalone `name:<metric>` equality entry. Counters use per-series increases and reset handling; gauge/updown use point values; histograms use observation counts, sums and buckets; summaries use available stored aggregates. Unsupported kind/function combinations report a diagnosis instead of treating counters as gauges.

`histogram(duration)` on spans uses count with log2 duration buckets; `histogram(value)` on metrics uses explicit histogram buckets. Distributions stand alone, with at most one `by` field for a histogram; `every` selects a heatmap.

## Expressions and ranking

`count_if(filters)` takes a FanQL filter list as one entry and compiles to checked `count(*) FILTER (WHERE ...)::DOUBLE`. Raw entries are allowed; variables are forbidden. Spans and logs support `+ - * /`, constants, unary minus and parentheses on aggregate vizzes. Metrics, distributions and log_patterns do not support arithmetic. Every expression needs at least one aggregate. Other than a single call, an expression requires `as alias`; aliases are 1–40 lowercase letters/digits/underscores and start with a letter. `share()` stands alone. Division emits `a / nullif(b, 0)`, so zero denominators yield NULL.

Count-family ratios use ratio units, `* 100` makes percent, addition/subtraction retains equal units, scaling retains the measure unit, other combinations use none. Expressions are not additive. Affine transformations inherit the underlying ranking and flip direction for a negative scale. Binomial count_if/count ratios use Wilson ranking with a 20-sample floor; other expressions use value ranking with leaf/denominator support. Ineligible series follow eligible ones by support, then dimension; result metadata discloses them.

Apdex uses disjoint predicates: `(count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex`. Exactly 500 ms counts once; NULL duration counts in the population and neither numerator.

Each measure allows 256 tokens, 64 nodes, depth 8 and 8 aggregates. A query allows 256 expression nodes and 24 aggregates across at most 6 measures. A count_if filter is at most 500 bytes. Whitespace is not a token. Budgets apply to structured inputs too.

## Filter functions and raw entries

| Function | Signature |
|---|---|
| `contains` | `contains(field, quoted_string)` |
| `length` | `length(field)` |
| `lower` | `lower(field)` |
| `regexp_matches` | `regexp_matches(field, quoted_string)` |
| `starts_with` | `starts_with(field, quoted_string)` |
| `upper` | `upper(field)` |

`lower`, `upper` and `length` need a comparison. `starts_with`, `contains` and `regexp_matches` are predicates alone. Calls have no space before `(`. Variables may compare call results, but remain forbidden inside count_if.

A backtick-delimited raw entry passes SQL to the existing filter gate; double a backtick to include it. A whole raw entry retains its stored spelling; raw fragments composed with other predicates are parenthesized. The gate accepts columns, literal `attributes['k']`/`resource['k']`, comparisons, boolean logic, IN, LIKE, IS NULL, BETWEEN, coalesce, variables, the filter functions above and casts to VARCHAR, BIGINT, INTEGER, DOUBLE, BOOLEAN, UBIGINT and TIMESTAMPTZ_NS. It rejects CASE, DISTINCT, ORDER BY and FILTER inside functions, qualified columns, subqueries and other functions. Raw cannot bypass the gate.

## Variables and entry boundaries

`$name` is a dashboard variable; `$__` names are reserved. It stays a parameter, never a string literal. `k in $multi` accepts multiple values; scalar operators reject multi variables. Without declarations, references produce notices so editing can continue; execution checks them.

All rule: a filter that mentions a variable set to All is removed; keep fixed conditions in their own terms. An empty variable list makes its entry FALSE. Top-level terms are separate entries; a parenthesized group, raw entry or OR chain is one entry. Stored order and boundaries survive formatting.

| Filters | Entries | `$service` = All |
|---|---|---|
| `service:$service status:error` | 2 | Keep status. |
| `(service:$service status:error)` | 1 | Drop both. |
| `service:$service or service:worker` | 1 | Drop whole entry. |
| `service:$service (status:error or status:unset)` | 2 | Keep status group. |
| `-service:$service` | 1 | Drop entry; nothing excluded. |
| Raw SQL mentioning `$service` | 1 | Drop whole entry. |
| `count_if(service:$service)` | n/a | Rejected. |

## Visualization and trace semantics

Logs/traces use logs/spans respectively and forbid measures, grouping, buckets and histogram. Log_patterns uses `count() by body_template`, optionally a bucket. Timeseries needs a bucket and at most one grouping field, with one measure when grouped. Tables allow three grouping fields; bars two, with one measure when grouped by two. Stat/gauge need one measure and no grouping/bucket; gauge needs min/max. Scatter needs two measures and one or two grouping fields. State_timeline needs one measure, one grouping field, a bucket and thresholds.

Traces are `(namespace, trace_id)` candidates with at least one matching span in the window, ranked by matching spans' max(duration_ms), or min(start_time) for `sort +start`. Limit applies to candidates; the engine reads at most 1,001 for truncation detection. Each returns one root span, or its earliest span in the window if the root is outside it, with error status if any span in the trace errored. Displayed service and duration are root service and root duration. A child can match while the returned root has different values.

Trace sorts: descending `duration` (stored duration_ms), `errors`, or ascending `+start`. `sort errors` is the legacy combined key: it also filters to error spans, then orders by duration. Prefer `status:error` with `sort duration`. Logs sort by `time` or `+time`; log_patterns by `count`. Aggregate sorts name a measure alias. Default/desc is descending; +/asc is ascending; choose one direction. Bucketed families use time order and options.top for series selection; sort/limit are accepted with a notice when ignored.

## Reserved words and foreign syntax

`compare`, `correlate`, `eval`, `extend`, `fields`, `having`, `join`, `let`, `over`, `rename`, `since`, `top`, `until`, `window` are reserved at clause positions. Quote them to search for the word. Foreign syntax gets a diagnostic and FanQL hint, never an alias; an ordinary foreign word in filter position stays free text.

Recognized foreign stages: `eval`, `extend`, `fields`, `head`, `order`, `project`, `rename`, `select`, `sort`, `stats`, `summarize`, `take`, `top`. Foreign call shapes: `bin`, `cardinality`, `count_over_time`, `dc`, `distinct_count`, `mean`, `median`, `pct`, `perc95`, `percentile`, `quantile_over_time`, `span`. `from`, `index=`, `source=`, first-token `search`, `ago(`, `now(`, `earliest=`, `_time:`, `order by`, `group by`, `facet`, `==`, `=~`, `|=`, `|~`, `::`, selectors and `@field:` get targeted hints. Time ranges belong to the time picker.

## Bounds and diagnostics

Interactive text allows 4,000 code points and 8,192 bytes, 1,024 tokens, 512 AST nodes, nesting depth 16 and lists of 100 values. Queries lower to 16 entries of 500 bytes each, 6 measures and 3 grouping fields, further constrained by the viz. Completion returns at most 50 items. Problems have code-point spans and optional fix-its, follow source order and are capped at 20 plus a final more-problems diagnostic. Any problem prevents execution; requested repair previews are labeled not executable.

Format returns complete text and exactness `exact` or `normalized`, with notices for normalization. Raw preserves whole SQL entries. Editable canonical text reparses under its tier without merging entries. Beyond any interactive bound, `editable: false` carries a reason and complete text; the envelope must preserve the original query and consumers must not parse the oversized text.

## Examples

```fanql
spans where kind:server | error_rate() by service every 1m
spans where service:checkout duration>500ms sort duration limit 50
spans where service:$service (status:error or status:unset) | count() by http_route
spans where kind:server | count_if(http_status_code>=500) / count() * 100 as pct_5xx by http_route
spans where kind:server | (count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex
spans | histogram(duration) by service
spans where service:checkout status:error -http.target:/healthz | count() by http_route
spans where user.id:* | count_distinct(user.id) by service
logs where service:api severity:error timeout sort time limit 200
logs where severity>=warn | count() by body_template sort count limit 10
metrics where name:http.server.requests | rate() by service every 1m
metrics where name:k8s.container.restarts | last(value) by resource.k8s.pod.name
```

## Transport

Machine text inputs carry visualization, time, comparison, declarations, resolved values, namespace and options as needed. The structured query is required when editable is false. Panels keep storing queries, never text.

Completion accepts a relative `window` duration or absolute `from` and `to` timestamps. Absolute windows and durations over 24h use recent 24h schema, disclosed by `schema.recent` and `schema.notice`. With no selected window, the same recent fallback applies.

HTTP exposes `/api/fanql/parse`, `/api/fanql/format`, `/api/fanql/complete` and `/api/fanql/reference`, requiring telemetry:read. MCP query_telemetry accepts optional text beside panel; agents should prefer panel.query, or panel.sql for complex work. This reference is also `fanout://fanql/reference`.
