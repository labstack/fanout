package engine

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
)

// Log and metric files sort by time bucket or series identity (proposal 0005),
// so a windowed read scoped to a service skips the row groups whose
// statistics exclude it, through the snapshot's clean views.
func TestScopedLogAndMetricReadsSkipRowGroups(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var logs []telemetry.Log
	var metrics []telemetry.Metric
	var inWindow, inService int64
	for i := range 160_000 {
		if i%120 < 10 {
			inWindow++
			if i%4 == 1 {
				inService++
			}
		}
		n := at.Add(time.Duration(i%120)*time.Minute).UnixNano() + int64(i)
		service := fmt.Sprint("svc", i%4)
		logs = append(logs, telemetry.Log{Namespace: "shop", ServiceName: service, Body: "x", TimeUnixNanos: n, IngestedAt: n})
		metrics = append(metrics, telemetry.Metric{Namespace: "shop", ServiceName: service, Name: "requests", Type: "gauge", Value: 1, TimeUnixNanos: n, IngestedAt: n})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "sorted", Logs: logs, Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	w := queryrows.Window{Start: at, End: at.Add(10 * time.Minute)}
	for _, test := range []struct {
		name, query string
		want        int64
	}{
		{"logs by time", `SELECT count(*) FROM logs WHERE time >= ? AND time < ?`, inWindow},
		{"logs by time and service", `SELECT count(*) FROM logs WHERE time >= ? AND time < ? AND service = 'svc1'`, inService},
		{"metrics by service", `SELECT count(*) FROM metrics WHERE time >= ? AND time < ? AND service = 'svc1' AND name = 'requests'`, inService},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, err := d.bindSnapshot(queryrows.WithWindow(t.Context(), w), d.DB, test.query, w)
			if err != nil {
				t.Fatal(err)
			}
			args := []any{at.Format(time.RFC3339Nano), w.End.Format(time.RFC3339Nano)}
			var count int64
			if err := d.DB.QueryRow(bound, args...).Scan(&count); err != nil || count != test.want {
				t.Fatalf("count %d, %v; want %d", count, err, test.want)
			}
			var key, plan string
			if err := d.DB.QueryRow("EXPLAIN (ANALYZE, FORMAT JSON) "+bound, args...).Scan(&key, &plan); err != nil {
				t.Fatal(err)
			}
			scanned, total := rowGroups(t, plan)
			t.Logf("%d of %d row groups scanned", scanned, total)
			if total < 4 || scanned*2 > total {
				t.Fatalf("scanned %d of %d row groups", scanned, total)
			}
		})
	}
}

// rowGroups sums the Parquet scans' row-group counts in a JSON profile.
func rowGroups(t *testing.T, plan string) (scanned, total int) {
	t.Helper()
	var profile any
	if err := json.Unmarshal([]byte(plan), &profile); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "TABLE_SCAN" {
				s, _ := x["row_groups_scanned"].(float64)
				n, _ := x["total_row_groups_to_scan"].(float64)
				scanned, total = scanned+int(s), total+int(n)
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(profile)
	return scanned, total
}
