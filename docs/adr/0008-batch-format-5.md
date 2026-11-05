# 0008. Batch format 5

Status: Accepted
Date: 2026-11-01

## Context

Format 4 kept every OpenTelemetry attribute in a VARIANT map. Filters and
groupings on common keys such as `http.route` or `k8s.pod.name` decoded the
map on every row and could not use Parquet statistics. Some values were
stored as JSON strings, and log and metric files had no declared row order,
so their row groups covered every service and time. Proposal 0005 records
the analysis.

## Decision

Format 5 replaces format 4 and supersedes the format half of ADR 0003.

- A fixed rule over semantic conventions v1.44.0 selects 47 standard keys:
  18 span attribute keys and 29 resource keys. Each has a typed dedicated
  column. Spans carry all 47; logs and metrics carry the 29 resource
  columns. A different key list needs a new format version.
- Ingest converts each key's value to the column type. Integer keys take
  integers, integral floats within range and base-10 integer strings, so
  `"200"` is stored as 200. String keys take strings and the text of
  integers, booleans and floats. Any other value leaves the column NULL and
  is counted in the footer as a spill. The map keeps every value as sent.
- No storage column holds JSON. Span events and links, metric exemplars,
  histogram buckets and summary quantiles are typed lists.
- Every signal's files keep a declared sort order. Spans sort by trace
  hash, start time and span ID. Logs sort by five-minute bucket, namespace,
  service and time. Metrics sort by namespace, service, name, hour, series
  and time.
- Reads rewrite `attributes['k']` and `resource['k']` for a standard key to
  its column on the parsed SQL, so FanQL, structured panels and SQL share one
  rule and read the converted value. Users never name the columns, and
  result stars leave them out.
- SQL rejects `GROUP BY ALL`, because the pinned engine groups its keys
  without VARIANT collation and splits equal attribute values.

## Consequences

- Filters and groupings on standard keys read one typed column and skip
  row groups by statistics.
- A standard key has one type in schema discovery, completion and results,
  whatever type senders used.
- Format 4 directories are refused and preserved; operators delete them
  before running format 5.
- Adding a key, changing a type or changing a sort order is a format change.
