package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResultProvenancePublishesOnlyWindowAndFreshness(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	service := &Service{now: func() time.Time { return now }}
	provenance := service.provenanceFor(Scope{Start: now.Add(-time.Hour), End: now})
	raw, err := json.Marshal(Result[TraceDetail]{Data: TraceDetail{Truncated: true}, Provenance: provenance})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema"`, `"query_id"`, `"data_source"`, "parquet_index", "edge_rollup"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("private provenance in result: %s", raw)
		}
	}
	if !provenance.Complete || provenance.Generated != now || provenance.Window != now.Add(-time.Hour).Format(time.RFC3339Nano)+"/"+now.Format(time.RFC3339Nano) || !strings.Contains(string(raw), `"truncated":true`) {
		t.Fatalf("lost window/freshness/truncation: %s", raw)
	}
}
