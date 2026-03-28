# Log Template Normalization Design

**Date:** 2026-03-28
**Status:** Approved

## Overview

Add a `body_template` column to log rows that normalizes variable parts of log bodies into placeholders at ingest time. Enables pattern grouping ("top 10 error patterns in the last hour") without any new dependencies.

## Goals

- Group thousands of raw log lines into a handful of meaningful patterns
- Work on any log format — JSON, logfmt, plain text, stack traces
- Zero new dependencies
- Backward compatible with existing Parquet files
- Improve intelligence detector pattern quality

## Non-Goals

- Structured field extraction (OTel Collector or DuckDB query-time functions handle this)
- Full log parsing library (Grok, Drain, etc.)
- Replacing or modifying the raw `body` column

## What Changes

| Change | File | Lines |
|---|---|---|
| `normalizeTemplate()` + regexes | `internal/ingest/template.go` (new) | ~80 |
| Add `BodyTemplate` to `LogRow` | `internal/lake/writer.go` | ~2 |
| Populate at ingest | `internal/ingest/server.go` | ~1 |
| Add to DuckDB view + placeholder | `internal/query/views.go` | ~3 |
| Detector uses template | `internal/intelligence/detector.go` | ~3 |
| MCP group_by template | `internal/mcp/logs.go` + `internal/service/logs.go` | ~10 |

~100 lines of code. No new dependencies. Backward compatible.

## The Normalizer

### Two-Path Design

JSON and plain text need different treatment. JSON bodies must preserve keys (structural identity) while normalizing values. Plain text normalizes everything.

```go
func normalizeTemplate(body string) string {
    if len(body) > 500 {
        body = truncateUTF8(body, 500)
    }
    if len(body) > 0 && body[0] == '{' {
        return normalizeJSON(body)
    }
    return normalizeText(body)
}
```

### Plain Text Normalization

Replaces variable parts with typed placeholders via compiled regexes.

```go
var (
    // Order matters — match specific patterns before generic ones
    reUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
    reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
    reIPv4      = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`)
    reEmail     = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
    reHexLong   = regexp.MustCompile(`\b0x[0-9a-fA-F]{4,}\b|\b[0-9a-fA-F]{8,}\b`)
    rePath      = regexp.MustCompile(`(/[a-zA-Z0-9._-]+){2,}(\?[^\s"]*)?`)
    reQuoted    = regexp.MustCompile(`"[^"]{1,200}"`)
    reNumber    = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
)

func normalizeText(body string) string {
    s := body
    s = reUUID.ReplaceAllString(s, "<uuid>")
    s = reTimestamp.ReplaceAllString(s, "<time>")
    s = reIPv4.ReplaceAllString(s, "<ip>")
    s = reEmail.ReplaceAllString(s, "<email>")
    s = reHexLong.ReplaceAllString(s, "<hex>")
    s = rePath.ReplaceAllString(s, "<path>")
    s = reQuoted.ReplaceAllString(s, "<str>")
    s = reNumber.ReplaceAllString(s, "<num>")
    return s
}
```

**Regex ordering rationale:**
1. UUIDs — contain hex chars and numbers, would be mangled by later regexes
2. Timestamps — contain numbers and dashes
3. IPs — contain numbers and dots
4. Emails — contain alphanumeric before number replacement
5. Hex strings (8+ chars) — git SHAs, trace IDs, Docker short IDs, hashes
6. Paths (with optional query strings) — `/api/v1/search?q=foo` → `<path>`
7. Quoted strings — string literals
8. Numbers — catches everything remaining (HTTP status codes, retry counts, etc.)

### JSON Normalization

Preserves JSON keys, normalizes values only. Uses `encoding/json` to unmarshal, walk the structure, and normalize leaf values.

```go
func normalizeJSON(body string) string {
    var m map[string]interface{}
    if err := json.Unmarshal([]byte(body), &m); err != nil {
        // Not valid JSON despite starting with '{' — fall back to text
        return normalizeText(body)
    }
    m = normalizeJSONValues(m).(map[string]interface{})
    out, err := json.Marshal(m)
    if err != nil {
        return normalizeText(body)
    }
    return string(out)
}

func normalizeJSONValues(v interface{}) interface{} {
    switch val := v.(type) {
    case map[string]interface{}:
        for k, child := range val {
            val[k] = normalizeJSONValues(child)
        }
        return val
    case []interface{}:
        for i, child := range val {
            val[i] = normalizeJSONValues(child)
        }
        return val
    case string:
        return normalizeText(val) // normalize string values with text rules
    case float64:
        return "<num>"
    case bool:
        return val // preserve booleans — they're structural
    case nil:
        return nil
    default:
        return "<val>"
    }
}
```

**Examples:**

Input:
```json
{"level":"error","msg":"connection refused","host":"db-primary-01","port":5432,"trace_id":"abc123def456"}
```

Output:
```json
{"level":"error","msg":"connection refused","host":"<str>","port":"<num>","trace_id":"<hex>"}
```

Keys preserved. Structural identity maintained. Short string values that look like enum constants (e.g., `"error"`, `"connection refused"`) pass through `normalizeText` unchanged — no regex matches. Dynamic values like hostnames get normalized: `"db-primary-01"` → `"db-primary-<num>"`.

### UTF-8 Safe Truncation

```go
func truncateUTF8(s string, maxBytes int) string {
    if len(s) <= maxBytes {
        return s
    }
    // Walk back from the limit to find a valid rune boundary
    for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
        maxBytes--
    }
    return s[:maxBytes]
}
```

Prevents corrupt Parquet writes from split multi-byte characters. The `parquet-go` library validates UTF-8 in STRING columns — a split character would fail the entire batch flush.

### Truncation at 500 Bytes

Deliberate tradeoff. Templates longer than 500 bytes are diminishing returns for grouping — the first 500 bytes contain the message and initial context. Stack traces are the main case where meaningful content appears after 500 bytes, but the exception type and first frames (the most diagnostic parts) are typically in the first 500 bytes. Keeps Parquet compact.

## Storage

### LogRow Change

`internal/lake/writer.go`:

```go
type LogRow struct {
    // ... all existing fields unchanged
    BodyTemplate string  // NEW — normalized template
}
```

One new column in Parquet files. Same partitioning, same compression (zstd). `body_template` compresses exceptionally well due to high repetition — many log rows share the same template.

### Backward Compatibility

Old Parquet files lack the `body_template` column. DuckDB's `union_by_name=true` (already in use throughout Fanout) fills the missing column with NULL. No migration needed.

## DuckDB View

### Placeholder SQL Update

`internal/query/views.go` — add to the logs placeholder. This is required for both fresh installs AND schema evolution on running instances. Fanout rewrites `_schema.parquet` sentinels on every startup to propagate new columns via `union_by_name=true`:

```sql
NULL::VARCHAR AS "name=body_template",
```

### View Update

Add to the `viewLogs` definition:

```sql
"name=body_template" AS body_template,
```

## Intelligence Detector Update

`internal/intelligence/detector.go` — replace crude substring grouping with template grouping.

**Note:** The detector queries raw Parquet via `read_parquet()`, not through the `logs` view. Raw Parquet columns use the `"name=..."` prefix. The service layer queries go through the `logs` view and use clean aliases (`body_template`). These are different namespaces — do not confuse them.

```sql
-- Before:
GROUP BY SUBSTRING("name=body", 1, 100), "name=severity", "name=service_name"

-- After (raw Parquet column names):
GROUP BY COALESCE("name=body_template", SUBSTRING("name=body", 1, 100)),
         "name=severity", "name=service_name"
```

`COALESCE` handles the migration window: old files without `body_template` fall back to the existing substring approach. Once all data has rolled through retention, `COALESCE` can be simplified to just `"name=body_template"`.

## MCP Logs Tool Enhancement

### Service Layer

`internal/service/logs.go` — add `"template"` to the group-by allowlist:

```go
var validLogGroupByFields = map[string]bool{
    "service":  true,
    "severity": true,
    "template": true,  // NEW — maps to body_template column
}
```

**Column name mapping required.** The existing `logsGrouped` function passes group-by field names directly as SQL column references (`groupCols := strings.Join(p.GroupBy, ", ")`). Since the MCP-facing name is `"template"` but the view column is `body_template`, a mapping step must be added before building `groupCols`:

```go
// Map MCP-facing group-by names to actual SQL column names
var logGroupByColumnMap = map[string]string{
    "service":  "service",
    "severity": "severity",
    "template": "body_template",
}

// In logsGrouped, before building groupCols:
for i, field := range p.GroupBy {
    if col, ok := logGroupByColumnMap[field]; ok {
        p.GroupBy[i] = col
    }
}
```

The result scanning in `LogGroup.Key` should use the original MCP-facing name (`"template"`) as the map key so consumers see a clean interface. This means the SELECT should alias: `body_template AS template`.

Generated SQL when `group_by` includes `"template"`:

```sql
SELECT body_template AS template, count(*) AS count,
       list_slice(list(body ORDER BY random()), 1, 3) AS sample_bodies,
       list_slice(list(trace_id ORDER BY random()) FILTER (...), 1, 3) AS sample_trace_ids
FROM logs
WHERE ...
GROUP BY body_template
ORDER BY count DESC
```

### MCP Tool Description Update

Add `template` to the `group_by` parameter description in the logs tool:

```
group_by (service|severity|template)
```

## What This Enables

```
Claude: calls logs(group_by=["template"], severity=["ERROR","FATAL"], window="1h")

Returns:
  template: "User <num> failed auth from <ip>"                    count: 4,832
  template: {"level":"error","msg":"connection refused",...}       count: 891
  template: "Payment declined for order <uuid>"                   count: 234
  template: "OOM killed process <num>"                            count: 12

Claude: "4,832 auth failures from various IPs in the last hour.
         891 connection refused errors to the database.
         Should I investigate the auth failures?"
```

Combined with the alert system (separate spec), rules could reference template-based counts:
```
log_pattern_count("User <num> failed auth") > 100
```

## Performance

- **normalizeText**: 8 compiled regexes on ≤500 bytes. ~1-5µs per log. 8 string allocations per call (one per `ReplaceAllString`).
- **normalizeJSON**: `json.Unmarshal` + walk + `json.Marshal` + text normalization on values. ~5-20µs per log depending on JSON size.
- **At 10K logs/sec**: 10-200ms/sec of CPU for normalization. Negligible vs the Parquet flush cost.
- **Storage**: `body_template` is shorter than `body` and highly repetitive — excellent zstd compression. Minimal Parquet size impact.

## Ingest Pipeline Change

`internal/ingest/server.go` — one line added where `LogRow` is built:

```go
row := lake.LogRow{
    Body:         body,
    BodyTemplate: normalizeTemplate(body),  // NEW
    // ... rest unchanged
}
```

## File Summary

```
internal/ingest/template.go   (NEW)  — normalizeTemplate, normalizeText, normalizeJSON, truncateUTF8, regex vars
internal/lake/writer.go               — add BodyTemplate to LogRow struct
internal/ingest/server.go              — populate BodyTemplate at ingest
internal/query/views.go                — add body_template to view + placeholder
internal/intelligence/detector.go      — use COALESCE(body_template, SUBSTRING(...))
internal/service/logs.go               — add "template" to validLogGroupByFields, handle in query
internal/mcp/logs.go                   — update tool description for group_by
```
