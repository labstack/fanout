package api

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestHTTPProblemMessagesAndHintsUseAuthorPresentation(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)duckdb|parquet|sqlite|(?:binder|catalog|conversion|io|parser) error|variant|timestamptz_ns|timestamp_ns|telemetry\.|parquet_index|/srv/|cast\(\$`)
	for _, message := range []string{
		`Binder Error: Referenced column missing_column not found`,
		`Catalog Error: missing_function not found in telemetry.spans`,
		`Conversion Error: Cannot convert 'bad' to VARIANT`,
		`IO Error: DuckDB read /srv/data/a.parquet`,
		`Parser Error: syntax error at or near "oops"`,
		`read indexed Parquet trace: open /srv/data/a.parquet: failed`,
		`exemplars: SQLite read /srv/data/state.sqlite: failed`,
		`discover spans attributes: Catalog Error: resource_inventory not found`,
		`Binder Error: Cannot compare timestamp to CAST($x AS TIMESTAMPTZ_NS)`,
		`Binder Error: query_count in telemetry.service_rollup cannot compare DOUBLE with INTEGER` + "\nCandidate bindings: \"telemetry.edge_rollup.query_count\", \"resource_inventory.service\"\nLINE 1: SELECT query_count FROM telemetry.service_rollup",
	} {
		t.Run(message, func(t *testing.T) {
			rec := servePanels(t, &fakePanels{err: panel.Problems{{Path: "panels[0].sql", Message: message, Hint: message}}}, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]}}`)
			body := rec.Body.String()
			if rec.Code != 400 || forbidden.MatchString(body) {
				t.Fatalf("unsafe problems: %d %s", rec.Code, body)
			}
			for _, meaning := range []string{"missing_column", "missing_function", "oops"} {
				if strings.Contains(message, meaning) && !strings.Contains(body, meaning) {
					t.Fatalf("lost %s: %s", meaning, body)
				}
			}
			if strings.Contains(message, "query_count") {
				for _, detail := range []string{"query_count", "service_rollup", "DOUBLE", "INTEGER", "Candidate bindings:", "edge_rollup.query_count", "resource_inventory.service"} {
					if !strings.Contains(body, detail) {
						t.Errorf("lost author detail %q: %s", detail, body)
					}
				}
				if strings.Contains(body, "telemetry.") || strings.Contains(body, "_result.") || strings.Contains(body, "LINE 1:") {
					t.Fatalf("expanded SQL or internal qualifiers escaped: %s", body)
				}
			}
		})
	}
}
