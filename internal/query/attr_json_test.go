package query

import (
	"context"
	"testing"
)

// TestAttrMacroAndQuotedPath verifies that the attr() macro and a double-quoted
// JSON path resolve flat, dotted attribute keys — the shape ingest now writes.
// An unquoted path must NOT resolve (it would silently return NULL), which is the
// regression this guards against.
func TestAttrMacroAndQuotedPath(t *testing.T) {
	db := openTestDuck(t)
	ctx := context.Background()
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}

	// Flat object with dotted keys and a numeric value, as attrsJSON produces.
	const attrs = `{"http.method":"GET","http.status_code":200,"messaging.system":"kafka"}`
	if _, err := db.ExecContext(ctx,
		`INSERT INTO telemetry.spans (namespace, service, attributes_json) VALUES ('default','svc',?)`, attrs); err != nil {
		t.Fatalf("insert span: %v", err)
	}

	var method, msgSystem, statusCode, unquoted *string
	err := db.QueryRowContext(ctx, `
SELECT
  attr(attributes_json, 'http.method'),
  json_extract_string(attributes_json, '$."messaging.system"'),
  attr(attributes_json, 'http.status_code'),
  json_extract_string(attributes_json, '$.http.method')
FROM spans`).Scan(&method, &msgSystem, &statusCode, &unquoted)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if method == nil || *method != "GET" {
		t.Errorf("attr(http.method) = %v, want GET", method)
	}
	if msgSystem == nil || *msgSystem != "kafka" {
		t.Errorf("quoted path messaging.system = %v, want kafka", msgSystem)
	}
	if statusCode == nil || *statusCode != "200" {
		t.Errorf("attr(http.status_code) numeric coercion = %v, want \"200\"", statusCode)
	}
	if unquoted != nil {
		t.Errorf("unquoted '$.http.method' resolved to %v; expected NULL (dotted keys need quoting)", *unquoted)
	}
}
