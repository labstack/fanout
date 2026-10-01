package query

import (
	"context"
	"testing"
)

// Dots remain literal attribute-key characters, including after shredding.
func TestAttrMacroUsesLiteralKeys(t *testing.T) {
	db := openTestDuck(t)
	ctx := context.Background()
	if err := CreateTables(db); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatalf("CreateViews: %v", err)
	}

	// Bind JSON only to create a typed VARIANT test fixture.
	const attrs = `{"http.method":"GET","http.status_code":200,"messaging.system":"kafka"}`
	if _, err := db.ExecContext(ctx,
		`INSERT INTO telemetry.spans (namespace, service, attributes) VALUES ('default','svc',?::VARCHAR::JSON::VARIANT)`, attrs); err != nil {
		t.Fatalf("insert span: %v", err)
	}

	var method, msgSystem, statusCode, missing *string
	err := db.QueryRowContext(ctx, `
SELECT
  attr(attributes, 'http.method')::VARCHAR,
  attr(attributes, 'messaging.system')::VARCHAR,
  attr(attributes, 'http.status_code')::VARCHAR,
  attr(attributes, 'http')::VARCHAR
FROM spans`).Scan(&method, &msgSystem, &statusCode, &missing)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if method == nil || *method != "GET" {
		t.Errorf("attr(http.method) = %v, want GET", method)
	}
	if msgSystem == nil || *msgSystem != "kafka" {
		t.Errorf("literal key messaging.system = %v, want kafka", msgSystem)
	}
	if statusCode == nil || *statusCode != "200" {
		t.Errorf("attr(http.status_code) numeric coercion = %v, want \"200\"", statusCode)
	}
	if missing != nil {
		t.Errorf("missing key http resolved to %v; expected NULL", *missing)
	}
}
