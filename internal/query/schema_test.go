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

	// Verify lake dir substitution
	if !strings.Contains(schema, "/var/lib/fanout/spans") {
		t.Error("GetSchema() did not substitute lake dir")
	}
	if strings.Contains(schema, "{LAKE}") {
		t.Error("GetSchema() has unsubstituted {LAKE} placeholder")
	}

	// Check for key sections
	required := []string{
		"Fanout Data Schema",
		"Spans (Traces)",
		"Logs",
		"Metrics",
		"service_rollup",
		"trace_id",
		"service_name",
		"read_parquet",
		"hive_partitioning",
	}

	for _, section := range required {
		if !strings.Contains(schema, section) {
			t.Errorf("GetSchema() missing %q", section)
		}
	}
}
