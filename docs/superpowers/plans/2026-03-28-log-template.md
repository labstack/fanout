# Log Template Normalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `body_template` column to log rows that normalizes variable parts into placeholders, enabling pattern grouping via MCP.

**Architecture:** At ingest time, detect body format (JSON vs plain text) and produce a normalized template. Store alongside raw body in Parquet. Expose in DuckDB view, intelligence detector, and MCP logs tool.

**Tech Stack:** Go standard library only (regexp, encoding/json, unicode/utf8). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-03-28-log-template-design.md`

---

### Task 1: Template Normalizer — Text Path

**Files:**
- Create: `internal/ingest/template.go`
- Create: `internal/ingest/template_test.go`

- [ ] **Step 1: Write the failing tests for text normalization**

```go
// internal/ingest/template_test.go
package ingest

import "testing"

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "uuid replacement",
			body: "request 550e8400-e29b-41d4-a716-446655440000 failed",
			want: "request <uuid> failed",
		},
		{
			name: "timestamp replacement",
			body: "error at 2026-03-28T10:15:03Z in handler",
			want: "error at <time> in handler",
		},
		{
			name: "timestamp with offset",
			body: "error at 2026-03-28T10:15:03.123+05:30 in handler",
			want: "error at <time> in handler",
		},
		{
			name: "ipv4 replacement",
			body: "connection from 192.168.1.100 refused",
			want: "connection from <ip> refused",
		},
		{
			name: "ipv4 with port",
			body: "listening on 0.0.0.0:8080",
			want: "listening on <ip>",
		},
		{
			name: "email replacement",
			body: "sent to user@example.com successfully",
			want: "sent to <email> successfully",
		},
		{
			name: "hex string replacement 8+ chars",
			body: "commit deadbeef pushed",
			want: "commit <hex> pushed",
		},
		{
			name: "hex 32 char trace id",
			body: "trace abc123def456789012345678abcdef00 found",
			want: "trace <hex> found",
		},
		{
			name: "path replacement",
			body: "GET /api/v1/users returned 200",
			want: "GET <path> returned <num>",
		},
		{
			name: "path with query string",
			body: "GET /api/v1/search?q=foo&limit=10 returned",
			want: "GET <path> returned",
		},
		{
			name: "quoted string replacement",
			body: `error: "connection refused" from host`,
			want: "error: <str> from host",
		},
		{
			name: "number replacement",
			body: "retried 3 times, took 12.5 seconds",
			want: "retried <num> times, took <num> seconds",
		},
		{
			name: "combined patterns",
			body: "User 482 failed auth from 10.0.0.51 at 2026-03-28T10:15:01Z",
			want: "User <num> failed auth from <ip> at <time>",
		},
		{
			name: "uuid before hex before number",
			body: "id=550e8400-e29b-41d4-a716-446655440000 hash=abcdef1234567890 count=42",
			want: "id=<uuid> hash=<hex> count=<num>",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
		{
			name: "no variables — passes through",
			body: "connection refused",
			want: "connection refused",
		},
		{
			name: "0x hex prefix",
			body: "address 0xdeadbeef allocated",
			want: "address <hex> allocated",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeText(tc.body)
			if got != tc.want {
				t.Errorf("normalizeText(%q)\n  got:  %q\n  want: %q", tc.body, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/ -run TestNormalizeText -v`
Expected: FAIL — `normalizeText` not defined

- [ ] **Step 3: Implement normalizeText and regex vars**

```go
// internal/ingest/template.go
package ingest

import (
	"regexp"
)

// Compiled regexes for template normalization.
// Order matters — specific patterns must match before generic ones.
var (
	reUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	reIPv4      = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`)
	reEmail     = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	reHexLong   = regexp.MustCompile(`\b0x[0-9a-fA-F]{4,}\b|\b[0-9a-fA-F]{8,}\b`)
	rePath      = regexp.MustCompile(`(/[a-zA-Z0-9._-]+){2,}(\?[^\s"]*)?`)
	reQuoted    = regexp.MustCompile(`"[^"]{1,200}"`)
	reNumber    = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
)

// normalizeText replaces variable parts of a plain text log body with typed placeholders.
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ingest/ -run TestNormalizeText -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/template.go internal/ingest/template_test.go
git commit -m "feat: add text log template normalizer with regex replacements"
```

---

### Task 2: Template Normalizer — JSON Path + truncateUTF8 + normalizeTemplate Entry Point

**Files:**
- Modify: `internal/ingest/template.go`
- Modify: `internal/ingest/template_test.go`

- [ ] **Step 1: Write the failing tests for JSON normalization, truncation, and entry point**

Add to `internal/ingest/template_test.go`:

```go
func TestNormalizeJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "preserves keys, normalizes string values",
			body: `{"level":"error","host":"db-primary-01","port":5432}`,
			want: `{"host":"db-primary-<num>","level":"error","port":"<num>"}`,
		},
		{
			name: "normalizes trace ids in values",
			body: `{"trace_id":"abc123def456789012345678abcdef00","msg":"failed"}`,
			want: `{"msg":"failed","trace_id":"<hex>"}`,
		},
		{
			name: "preserves booleans",
			body: `{"success":false,"retries":3}`,
			want: `{"retries":"<num>","success":false}`,
		},
		{
			name: "preserves nulls",
			body: `{"error":null,"code":500}`,
			want: `{"code":"<num>","error":null}`,
		},
		{
			name: "nested objects",
			body: `{"req":{"path":"/api/v1/users","id":123},"status":"ok"}`,
			want: `{"req":{"id":"<num>","path":"<path>"},"status":"ok"}`,
		},
		{
			name: "arrays",
			body: `{"ids":[1,2,3],"msg":"batch"}`,
			want: `{"ids":["<num>","<num>","<num>"],"msg":"batch"}`,
		},
		{
			name: "invalid json falls back to text",
			body: `{not json at all 12345`,
			want: `{not json at all <num>`,
		},
		{
			name: "enum-like string values preserved",
			body: `{"level":"error","msg":"connection refused"}`,
			want: `{"level":"error","msg":"connection refused"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeJSON(tc.body)
			if got != tc.want {
				t.Errorf("normalizeJSON(%q)\n  got:  %q\n  want: %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		wantLen  int
	}{
		{"ascii short", "hello", 10, 5},
		{"ascii exact", "hello", 5, 5},
		{"ascii truncate", "hello world", 5, 5},
		{"multibyte safe", "hello 世界!", 8, 6},  // "hello " = 6 bytes, 世 = 3 bytes, can't fit
		{"empty", "", 10, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateUTF8(tc.input, tc.max)
			if len(got) > tc.max {
				t.Errorf("truncateUTF8(%q, %d) = %q (len %d), exceeds max", tc.input, tc.max, got, len(got))
			}
			if len(got) != tc.wantLen {
				t.Errorf("truncateUTF8(%q, %d) len = %d, want %d", tc.input, tc.max, len(got), tc.wantLen)
			}
		})
	}
}

func TestNormalizeTemplate(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "json body detected by leading brace",
			body: `{"level":"error","code":500}`,
			want: `{"code":"<num>","level":"error"}`,
		},
		{
			name: "plain text",
			body: "User 123 failed auth from 10.0.0.1",
			want: "User <num> failed auth from <ip>",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeTemplate(tc.body)
			if got != tc.want {
				t.Errorf("normalizeTemplate(%q)\n  got:  %q\n  want: %q", tc.body, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/ -run "TestNormalizeJSON|TestTruncateUTF8|TestNormalizeTemplate" -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Implement normalizeJSON, truncateUTF8, normalizeTemplate**

Add to `internal/ingest/template.go`:

```go
import (
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

const maxTemplateBytes = 500

// normalizeTemplate produces a normalized template from a log body.
// JSON bodies preserve keys and normalize values.
// Plain text bodies normalize all variable parts.
func normalizeTemplate(body string) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) > maxTemplateBytes {
		body = truncateUTF8(body, maxTemplateBytes)
	}
	if body[0] == '{' {
		return normalizeJSON(body)
	}
	return normalizeText(body)
}

// normalizeJSON preserves JSON keys and normalizes leaf values.
// Falls back to normalizeText if the body is not valid JSON.
func normalizeJSON(body string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return normalizeText(body)
	}
	m = normalizeJSONValues(m).(map[string]interface{})
	out, err := json.Marshal(m)
	if err != nil {
		return normalizeText(body)
	}
	return string(out)
}

// normalizeJSONValues recursively normalizes JSON leaf values.
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
		return normalizeText(val)
	case float64:
		return "<num>"
	case bool:
		return val
	case nil:
		return nil
	default:
		return "<val>"
	}
}

// truncateUTF8 truncates a string to at most maxBytes, ensuring valid UTF-8.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
```

- [ ] **Step 4: Run all template tests to verify they pass**

Run: `go test ./internal/ingest/ -run "TestNormalizeText|TestNormalizeJSON|TestTruncateUTF8|TestNormalizeTemplate" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/template.go internal/ingest/template_test.go
git commit -m "feat: add JSON normalization, UTF-8 truncation, and template entry point"
```

---

### Task 3: Wire Template Into Ingest + Storage

**Files:**
- Modify: `internal/lake/writer.go:59-76` (LogRow struct)
- Modify: `internal/ingest/server.go:137-154` (log Export)

- [ ] **Step 1: Add BodyTemplate field to LogRow**

In `internal/lake/writer.go`, add after the `IngestedAt` field (line 75) in the `LogRow` struct:

```go
	BodyTemplate  string `parquet:"name=body_template, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
```

- [ ] **Step 2: Populate BodyTemplate at ingest time**

In `internal/ingest/server.go`, in the `Export` method for logs (around line 137), add `BodyTemplate` to the `LogRow` initialization. Change the block to:

```go
				body := bodyString(lr.Body)
				row := lake.LogRow{
					TenantID:          cfg.TenantID.String(),
					Namespace:         namespace,
					TimeUnixNanos:     int64(lr.TimeUnixNano),
					ObservedTimeNanos: int64(lr.ObservedTimeUnixNano),
					Severity:          normalizeSeverity(lr.SeverityText, int32(lr.SeverityNumber)),
					SeverityNumber:    int32(lr.SeverityNumber),
					Body:              body,
					BodyTemplate:      normalizeTemplate(body),
					ServiceName:       svc,
					TraceID:           hexOrEmpty(lr.TraceId),
					SpanID:            hexOrEmpty(lr.SpanId),
					Flags:             lr.Flags,
					ResourceJSON:      resourceJSON,
					AttributesJSON:    toJSON(lr.Attributes),
					ScopeName:         scopeName,
					ScopeVersion:      scopeVer,
					IngestedAt:        now,
				}
```

Note: `bodyString(lr.Body)` must be extracted to a local var `body` since it's used twice.

- [ ] **Step 3: Build to verify no compile errors**

Run: `go build ./...`
Expected: Success

- [ ] **Step 4: Run existing ingest tests to verify nothing broke**

Run: `go test ./internal/ingest/ -v`
Expected: All existing tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lake/writer.go internal/ingest/server.go
git commit -m "feat: wire body_template into log ingest and Parquet storage"
```

---

### Task 4: DuckDB View + Placeholder Schema

**Files:**
- Modify: `internal/query/views.go:48-61` (viewLogs constant)
- Modify: `internal/query/views.go:132-145` (logs placeholder)

- [ ] **Step 1: Add body_template to the logs placeholder**

In `internal/query/views.go`, in the `placeholders["logs"]` entry, add before the `namespace` line:

```sql
  NULL::VARCHAR AS "name=body_template",
```

The full logs placeholder becomes:

```go
	"logs": `COPY (
SELECT
  NULL::BIGINT  AS "name=time_unix_nano",
  NULL::VARCHAR AS "name=severity",
  NULL::VARCHAR AS "name=body",
  NULL::VARCHAR AS "name=service_name",
  NULL::VARCHAR AS "name=trace_id",
  NULL::VARCHAR AS "name=span_id",
  NULL::BLOB    AS "name=attributes_json",
  NULL::BLOB    AS "name=resource_json",
  NULL::VARCHAR AS "name=body_template",
  NULL::VARCHAR AS namespace,
  NULL::VARCHAR AS tenant
WHERE false
) TO '{path}' (FORMAT parquet);`,
```

- [ ] **Step 2: Add body_template to the viewLogs definition**

In `internal/query/views.go`, in the `viewLogs` constant, add after the `resource_json` line:

```sql
  "name=body_template" AS body_template,
```

The full viewLogs becomes:

```go
const viewLogs = `
CREATE OR REPLACE VIEW logs AS
SELECT
  to_timestamp("name=time_unix_nano" / 1e9) AS time,
  "name=severity" AS severity,
  "name=body" AS body,
  "name=service_name" AS service,
  "name=trace_id" AS trace_id,
  "name=span_id" AS span_id,
  decode("name=attributes_json") AS attributes_json,
  decode("name=resource_json") AS resource_json,
  "name=body_template" AS body_template,
  namespace, tenant
FROM read_parquet('{lake}/logs/**/*.parquet',
     hive_partitioning=true, union_by_name=true);`
```

- [ ] **Step 3: Build to verify no compile errors**

Run: `go build ./...`
Expected: Success

- [ ] **Step 4: Commit**

```bash
git add internal/query/views.go
git commit -m "feat: add body_template to DuckDB logs view and placeholder schema"
```

---

### Task 5: Intelligence Detector — Use body_template for Pattern Grouping

**Files:**
- Modify: `internal/intelligence/detector.go:386-402` (detectLogPatterns query)

- [ ] **Step 1: Update the SQL query to use COALESCE(body_template, SUBSTRING)**

In `internal/intelligence/detector.go`, replace the `detectLogPatterns` SQL query. Change the SELECT and GROUP BY to use `COALESCE`:

Replace:
```go
	sql := fmt.Sprintf(`
		SELECT
			SUBSTRING("name=body", 1, 100) AS template,
			"name=severity" as severity,
			"name=service_name" as service_name,
			COUNT(*) AS occurrence_count,
			MIN("name=time_unix_nano") AS first_seen_nano,
			MAX("name=time_unix_nano") AS last_seen_nano
		FROM read_parquet(%s, union_by_name=true)
		WHERE "name=time_unix_nano" >= %d AND "name=time_unix_nano" < %d
		AND "name=severity" IN ('WARN', 'ERROR', 'FATAL')
		GROUP BY SUBSTRING("name=body", 1, 100), "name=severity", "name=service_name"
		HAVING COUNT(*) >= 3
		ORDER BY occurrence_count DESC
		LIMIT 50
	`, logsGlob, startNano, endNano)
```

With:
```go
	sql := fmt.Sprintf(`
		SELECT
			COALESCE("name=body_template", SUBSTRING("name=body", 1, 100)) AS template,
			"name=severity" as severity,
			"name=service_name" as service_name,
			COUNT(*) AS occurrence_count,
			MIN("name=time_unix_nano") AS first_seen_nano,
			MAX("name=time_unix_nano") AS last_seen_nano
		FROM read_parquet(%s, union_by_name=true)
		WHERE "name=time_unix_nano" >= %d AND "name=time_unix_nano" < %d
		AND "name=severity" IN ('WARN', 'ERROR', 'FATAL')
		GROUP BY COALESCE("name=body_template", SUBSTRING("name=body", 1, 100)), "name=severity", "name=service_name"
		HAVING COUNT(*) >= 3
		ORDER BY occurrence_count DESC
		LIMIT 50
	`, logsGlob, startNano, endNano)
```

- [ ] **Step 2: Build to verify no compile errors**

Run: `go build ./...`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/intelligence/detector.go
git commit -m "feat: detector uses body_template for pattern grouping with COALESCE fallback"
```

---

### Task 6: Service Layer — Add Template Group-By with Column Mapping

**Files:**
- Modify: `internal/service/logs.go:11-15` (validLogGroupByFields)
- Modify: `internal/service/logs.go:168-189` (logsGrouped)

- [ ] **Step 1: Write a test for template group-by validation**

Add to `internal/service/logs_test.go`:

```go
func TestLogGroupByTemplateValidation(t *testing.T) {
	// Verify "template" is accepted in group_by validation
	for _, field := range []string{"service", "severity", "template"} {
		if !validLogGroupByFields[field] {
			t.Errorf("validLogGroupByFields[%q] = false, want true", field)
		}
	}
}

func TestLogGroupByColumnMap(t *testing.T) {
	tests := []struct {
		input []string
		want  []string
	}{
		{[]string{"service"}, []string{"service"}},
		{[]string{"severity"}, []string{"severity"}},
		{[]string{"template"}, []string{"body_template"}},
		{[]string{"service", "template"}, []string{"service", "body_template"}},
	}
	for _, tc := range tests {
		got := mapGroupByCols(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("mapGroupByCols(%v) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("mapGroupByCols(%v)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/ -run "TestLogGroupByTemplateValidation|TestLogGroupByColumnMap" -v`
Expected: FAIL

- [ ] **Step 3: Update validLogGroupByFields and add column mapping**

In `internal/service/logs.go`, update the allowlist:

```go
var validLogGroupByFields = map[string]bool{
	"service":  true,
	"severity": true,
	"template": true,
}
```

Add the column mapping function and update the error message:

```go
// logGroupByColumnMap maps MCP-facing group-by names to SQL column names.
var logGroupByColumnMap = map[string]string{
	"service":  "service",
	"severity": "severity",
	"template": "body_template",
}

// mapGroupByCols converts MCP group-by field names to SQL column names.
func mapGroupByCols(fields []string) []string {
	cols := make([]string, len(fields))
	for i, f := range fields {
		if col, ok := logGroupByColumnMap[f]; ok {
			cols[i] = col
		} else {
			cols[i] = f
		}
	}
	return cols
}
```

Update the validation error message in the `Logs` method:

```go
		if !validLogGroupByFields[field] {
			return nil, fmt.Errorf("invalid group_by field %q: allowed fields are service, severity, template", field)
		}
```

Update `logsGrouped` to use the column mapping. Replace line 174:

```go
	groupCols := strings.Join(p.GroupBy, ", ")
```

With:

```go
	sqlCols := mapGroupByCols(p.GroupBy)
	groupCols := strings.Join(sqlCols, ", ")
```

The `selectCols` assignment on the next line already uses `groupCols`, so the SQL will correctly reference `body_template` in both SELECT and GROUP BY. The scan loop at lines 220-225 still iterates `p.GroupBy` (the original MCP names) for the `Key` map, so the consumer sees `"template"` not `"body_template"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/ -run "TestLogGroupByTemplateValidation|TestLogGroupByColumnMap" -v`
Expected: PASS

- [ ] **Step 5: Build to verify no compile errors**

Run: `go build ./...`
Expected: Success

- [ ] **Step 6: Commit**

```bash
git add internal/service/logs.go internal/service/logs_test.go
git commit -m "feat: add template group-by to logs service with column name mapping"
```

---

### Task 7: MCP Tool Description Update

**Files:**
- Modify: `internal/mcp/logs.go:19` (GroupBy jsonschema)

- [ ] **Step 1: Update the GroupBy jsonschema tag**

In `internal/mcp/logs.go`, change line 19:

```go
	GroupBy   []string          `json:"group_by,omitempty"  jsonschema:"Aggregate by: service|severity"`
```

To:

```go
	GroupBy   []string          `json:"group_by,omitempty"  jsonschema:"Aggregate by: service|severity|template"`
```

- [ ] **Step 2: Build to verify no compile errors**

Run: `go build ./...`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/logs.go
git commit -m "feat: add template to MCP logs tool group_by description"
```

---

### Task 8: Final Verification

- [ ] **Step 1: Run all tests**

Run: `go test ./internal/ingest/ ./internal/service/ -v`
Expected: All PASS

- [ ] **Step 2: Build full binary**

Run: `go build ./cmd/fanout`
Expected: Success

- [ ] **Step 3: Run linter**

Run: `just lint`
Expected: No issues
