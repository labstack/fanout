package engine

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

var quotedNames = regexp.MustCompile(`"(?:[^"]|"")*"`)

func TestStandardKeySubscriptsReadTheirRelationsColumns(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	for _, test := range []struct{ name, query, want, absent string }{
		{"direct", `SELECT attributes['http.route'] FROM spans`, `_a_http_route AS "attributes['http.route']"`, "attributes["},
		{"qualified", `SELECT s.attributes['http.route'] AS r FROM spans AS s`, `s._a_http_route AS r`, "attributes["},
		{"raw relation", `SELECT resource['k8s.pod.name'] AS p FROM telemetry.logs`, `_r_k8s_pod_name AS p`, "resource["},
		{"nested name", `SELECT attributes['http.response.status_code']::INTEGER FROM spans`, `AS "CAST(attributes['http.response.status_code'] AS INTEGER)"`, "attributes["},
		{"filter", `SELECT count(*) AS n FROM spans WHERE attributes['http.response.status_code'] >= 500`, `_a_http_response_status_code >= 500`, "attributes["},
		{"star CTE", `WITH s AS (SELECT * FROM spans) SELECT attributes['http.route'] AS r FROM s`, `SELECT _a_http_route AS r FROM s`, "attributes["},
		{"map CTE", `WITH s AS (SELECT attributes FROM spans) SELECT attributes['http.route'] AS r FROM s`, `SELECT attributes['http.route'] AS r FROM s`, "_a_"},
		{"correlated", `SELECT count(*) AS n FROM logs l WHERE EXISTS (SELECT 1 FROM spans WHERE attributes['http.route'] = l.body)`, `WHERE (_a_http_route = l.body)`, "_a_http_route AS"},
		{"log attribute", `SELECT attributes['http.route'] AS r FROM logs`, `attributes['http.route'] AS r`, "_a_"},
		{"other key", `SELECT attributes['http.target'] AS r, attributes['http'] AS h FROM spans`, `attributes['http.target'] AS r`, "_a_"},
		{"shadowed", `WITH spans AS (SELECT {'http.route': 1}::VARIANT AS attributes) SELECT attributes['http.route'] AS r FROM spans`, `attributes['http.route'] AS r`, "_a_"},
		{"inventory", `SELECT resource['service.version'] AS v FROM resource_inventory`, `"resource"['service.version'] AS v`, "_r_"},
		{"union", `SELECT attributes['http.route'] AS r FROM (SELECT * FROM spans UNION ALL SELECT * FROM spans)`, `SELECT _a_http_route AS r`, "attributes["},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := compileRelations(t.Context(), d.DB, test.query, nil, map[string]string{})
			if err != nil {
				t.Fatal(err)
			}
			// Result names keep the original spelling; look for it elsewhere.
			if !strings.Contains(got, test.want) || strings.Contains(quotedNames.ReplaceAllString(got, ""), test.absent) {
				t.Fatalf("%s\nrewrote to %s\nwant %s without %s", test.query, got, test.want, test.absent)
			}
		})
	}
}

func TestStandardKeyRewriteKeepsResultNamesAndTypes(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	query := `SELECT attributes['http.route'], attributes['http.response.status_code'], resource['service.version']::VARCHAR, attributes['http.target'] FROM spans`
	prepared, encoded, err := d.PrepareTelemetrySQL(t.Context(), query, query, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{`"attributes['http.route']"`, `"attributes['http.response.status_code']"`, `"CAST(""resource""['service.version'] AS VARCHAR)"`, `"attributes['http.target']"`} {
		if !strings.Contains(prepared, name) {
			t.Fatalf("result name %s missing from %s", name, prepared)
		}
	}
	// Typed keys reach the driver as scalars; other keys stay VARIANT.
	if encoded[0] || encoded[1] || encoded[2] || !encoded[3] {
		t.Fatalf("encoded columns: %v", encoded)
	}
}

func TestTelemetrySQLRejectsInternalColumns(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	for query, want := range map[string]string{
		`SELECT _a_http_route FROM spans`:                   `column "_a_http_route" is internal; write attributes['http.route']`,
		`SELECT s._R_SERVICE_VERSION FROM spans s`:          `column "_R_SERVICE_VERSION" is internal; write resource['service.version']`,
		`SELECT _trace_hash FROM telemetry.spans`:           `column "_trace_hash" is internal`,
		`SELECT 1 FROM metrics WHERE _r_k8s_pod_name = 'a'`: `write resource['k8s.pod.name']`,
	} {
		if _, _, err := d.PrepareTelemetrySQL(t.Context(), query, query, 1); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", query, err, want)
		}
	}
}

// Ingest converts a standard key's value to the key's type, and the column is
// the read value: a status sent as text compares as the number it holds, and
// a version sent as a number reads as its text. A subquery that projects the
// map alone reads the value as sent.
func TestStandardKeysReadCoercedValues(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	commit := func(id string, statuses ...any) {
		t.Helper()
		var spans []telemetry.Span
		for i, status := range statuses {
			n := at.Add(time.Duration(i) * time.Second).UnixNano()
			spans = append(spans, telemetry.Span{
				Namespace: "shop", ServiceName: "checkout", TraceID: id, SpanID: id + string(rune('a'+i)), Name: "GET",
				StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n,
				Attributes: map[string]any{"http.response.status_code": status, "http.route": "/cart"},
				Resource:   map[string]any{"service.version": status},
			})
		}
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: spans}); err != nil {
			t.Fatal(err)
		}
	}
	commit("typed", int64(500), int64(200))
	commit("coerced", "200", 503.0, "abc", int64(504), 503.5, true)
	ctx := queryrows.WithWindow(t.Context(), queryrows.Window{Start: at, End: at.Add(time.Hour)})
	var errors, numeric, sent int64
	var versions string
	if err := d.QueryRowScan(ctx, []any{&errors, &numeric, &versions}, `SELECT
 count(*) FILTER (WHERE attributes['http.response.status_code'] >= 500),
 count(attributes['http.response.status_code']),
 string_agg(DISTINCT resource['service.version'], ',' ORDER BY resource['service.version'])
FROM spans`); err != nil {
		t.Fatal(err)
	}
	if errors != 3 || numeric != 5 || versions != "200,500,503,503.5,504,abc,true" {
		t.Fatalf("errors %d, numeric %d, versions %q", errors, numeric, versions)
	}
	if err := d.QueryRowScan(ctx, []any{&sent}, `WITH s AS (SELECT attributes FROM spans)
SELECT count(*) FROM s WHERE attributes['http.response.status_code']::VARCHAR IN ('503.5', 'abc', 'true')`); err != nil || sent != 3 {
		t.Fatalf("values as sent %d: %v", sent, err)
	}
	// Every file reads the column alone.
	compiled, err := d.compileSnapshot(t.Context(), d.DB, `SELECT count(*) FROM spans WHERE attributes['http.response.status_code'] >= 500`)
	if err != nil {
		t.Fatal(err)
	}
	source := d.snapshotSource("spans", repo.Parquet.BatchMetadata(), repo.Parquet.BatchMetadata())
	if strings.Contains(source, "UNION") || strings.Contains(source, "status_code") || !strings.Contains(compiled.query, "_a_http_response_status_code >= 500") {
		t.Fatalf("source:\n%s\nquery:\n%s", source, compiled.query)
	}
}

func TestVersionRollupReadsNumericVersions(t *testing.T) {
	d, repo := versionEngine(t)
	at := time.Now().UTC().Add(-time.Hour)
	for i, version := range []any{"v1", int64(2)} {
		n := at.Add(time.Duration(i) * time.Minute).UnixNano()
		id := string(rune('a' + i))
		if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: id, Spans: []telemetry.Span{{
			Namespace: "shop", ServiceName: "checkout", Resource: map[string]any{"service.version": version},
			TraceID: id, SpanID: id, Name: "checkout", StartUnixNanos: n, EndUnixNanos: n + 1, IngestedAt: n,
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var versions string
	if err := d.DB.QueryRow(`SELECT string_agg(service_version, ',' ORDER BY service_version) FROM version_rollup`).Scan(&versions); err != nil || versions != "2,v1" {
		t.Fatalf("versions %q, %v", versions, err)
	}
}

// Stars in the result leave the dedicated columns out; inner stars pass them
// on, so subscripts above still read the columns.
func TestStarsHideDedicatedColumns(t *testing.T) {
	d, _ := newBatchCacheTest(t)
	at := time.Now().UTC()
	ctx := queryrows.WithWindow(t.Context(), queryrows.Window{Start: at.Add(-time.Hour), End: at})
	for _, query := range []string{
		`SELECT * FROM spans`,
		`SELECT s.*, l.* EXCLUDE (body) FROM spans s JOIN logs l ON true`,
		`WITH s AS (SELECT * FROM spans) SELECT *, attributes['http.route'] AS r FROM s`,
		`SELECT * FROM (SELECT * FROM metrics) UNION ALL SELECT * FROM metrics`,
		`SELECT * FROM (SELECT * EXCLUDE (attributes) FROM spans)`,
		`SELECT * FROM (SELECT * FROM spans) AS t(a)`,
	} {
		rows, err := d.QueryContext(ctx, query+` LIMIT 0`)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		columns, err := rows.Columns()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(columns, "resource") {
			t.Fatalf("%s: %v", query, columns)
		}
		for _, c := range columns {
			if _, internal := internalColumn(c); internal {
				t.Fatalf("%s returns %s", query, c)
			}
		}
	}
	compiled, err := compileRelations(t.Context(), d.DB, `WITH s AS (SELECT * FROM spans) SELECT attributes['http.route'] AS r, * FROM s`, nil, map[string]string{})
	if err != nil || !strings.Contains(compiled, `SELECT _a_http_route AS r, * EXCLUDE (`) {
		t.Fatalf("%s: %v", compiled, err)
	}
}
