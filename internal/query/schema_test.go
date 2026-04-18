package query

import (
	"strings"
	"testing"
)

func TestGetSchema(t *testing.T) {
	schema := GetSchema("/var/lib/fanout")

	if schema == "" {
		t.Error("GetSchema() returned empty string")
	}

	// Verify data dir substitution
	if !strings.Contains(schema, "/var/lib/fanout") {
		t.Error("GetSchema() did not substitute data dir")
	}
	if strings.Contains(schema, "{DATA_DIR}") {
		t.Error("GetSchema() has unsubstituted {DATA_DIR} placeholder")
	}

	// Check for key sections
	required := []string{
		"Fanout Data Schema",
		"Spans",
		"Logs",
		"Metrics",
		"service_rollup",
		"trace_id",
		"service",
		"lake.spans",
		"json_extract_string",
	}

	for _, section := range required {
		if !strings.Contains(schema, section) {
			t.Errorf("GetSchema() missing %q", section)
		}
	}
}
