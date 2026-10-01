package query

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

func TestParquetMessagingViewProjectsShreddedKeysIntoScan(t *testing.T) {
	store, err := telemetry.OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CommitBatch(context.Background(), telemetry.BatchMetadata{ID: "attrs"}, []telemetry.Span{
		{TraceID: "one", SpanID: "a", Resource: map[string]any{"service.name": "api"}, Attributes: map[string]any{"messaging.system": "kafka", "messaging.destination.name": "jobs"}},
		{TraceID: "two", SpanID: "b", Resource: map[string]any{"service.name": "worker"}, Attributes: map[string]any{"messaging.system": "nats", "messaging.destination.name": "events"}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	db := openTestDuck(t)
	if err := CreateParquetViews(db, store.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"SET enable_profiling='json'", "SET profiling_output=" + sqlLiteral(t.TempDir()+"/profile.json")} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []struct{ name, key, value string }{{"messaging_system", "messaging.system", "kafka"}, {"messaging_destination", "messaging.destination.name", "jobs"}} {
		q := "SELECT " + column.name + " FROM spans WHERE " + column.name + " = ?"
		var got string
		if err := db.QueryRow(q, column.value).Scan(&got); err != nil || got != column.value {
			t.Fatalf("projected key = %q, %v", got, err)
		}
		plan := explain(t, db, "ANALYZE "+q, column.value)
		var profile any
		if err := json.Unmarshal([]byte(plan), &profile); err != nil {
			t.Fatal(err)
		}
		var scan map[string]any
		var visit func(any)
		visit = func(value any) {
			switch x := value.(type) {
			case map[string]any:
				if x["type"] == "TABLE_SCAN" {
					scan = x
				}
				for _, v := range x {
					visit(v)
				}
			case []any:
				for _, v := range x {
					visit(v)
				}
			}
		}
		visit(profile)
		if scan == nil {
			t.Fatal("no scan")
		}
		projection, _ := scan["extra_info"].(map[string]any)["Projections"].(string)
		if projection != "attributes."+column.key {
			t.Fatalf("shredded key did not reach scan: %s", plan)
		}
	}
	// General attr() access still preserves literal keys and types through views;
	// this preview's optimizer does not push such extracts across their projection.
	var service string
	if err := db.QueryRow("SELECT attr(resource,'service.name')::VARCHAR FROM spans WHERE trace_id='one'").Scan(&service); err != nil || service != "api" {
		t.Fatalf("literal resource key=%q %v", service, err)
	}
	// Ensure the hot path did not request the whole attributes object.
	plan := explain(t, db, "SELECT messaging_system FROM spans")
	if strings.Contains(plan, "Projections: attributes ") {
		t.Fatal(plan)
	}
}
