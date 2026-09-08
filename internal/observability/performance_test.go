package observability

import (
	"strings"
	"testing"
	"time"
)

// The interior range is what the rollup cache can answer for. A window that
// starts mid-minute has a partial minute the cache does not hold, and the cache
// knows nothing past its own watermark — raw spans cover both edges.
func TestEndpointInteriorBounds(t *testing.T) {
	minute := func(s string) time.Time {
		at, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	for _, tc := range []struct {
		name                  string
		start, end, watermark time.Time
		wantStart, wantEnd    time.Time
	}{
		{
			name:  "a window on minute boundaries with a fresh cache is entirely interior",
			start: minute("2026-09-08T10:00:00Z"), end: minute("2026-09-08T11:00:00Z"), watermark: minute("2026-09-08T11:00:00Z"),
			wantStart: minute("2026-09-08T10:00:00Z"), wantEnd: minute("2026-09-08T11:00:00Z"),
		},
		{
			name:  "a mid-minute start leaves its partial minute to raw spans",
			start: minute("2026-09-08T10:00:30Z"), end: minute("2026-09-08T11:00:00Z"), watermark: minute("2026-09-08T11:00:00Z"),
			wantStart: minute("2026-09-08T10:01:00Z"), wantEnd: minute("2026-09-08T11:00:00Z"),
		},
		{
			name:  "a lagging watermark ends the interior early",
			start: minute("2026-09-08T10:00:00Z"), end: minute("2026-09-08T11:00:00Z"), watermark: minute("2026-09-08T10:42:30Z"),
			wantStart: minute("2026-09-08T10:00:00Z"), wantEnd: minute("2026-09-08T10:42:00Z"),
		},
		{
			name:  "a watermark behind the window leaves no interior at all",
			start: minute("2026-09-08T10:00:00Z"), end: minute("2026-09-08T11:00:00Z"), watermark: minute("2026-09-08T09:00:00Z"),
			wantStart: minute("2026-09-08T10:00:00Z"), wantEnd: minute("2026-09-08T09:00:00Z"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd := endpointInteriorBounds(tc.start, tc.end, tc.watermark)
			if !gotStart.Equal(tc.wantStart) || !gotEnd.Equal(tc.wantEnd) {
				t.Fatalf("bounds = %s..%s, want %s..%s", gotStart, gotEnd, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

// The boundary scan must bind its bounds directly: a bound derived from a
// joined row is not a constant DuckDB can push into the Parquet scan, which is
// what made a two-minute question read the whole window.
func TestEndpointRollupQueryBindsBoundsDirectly(t *testing.T) {
	if strings.Contains(endpointRollupQuery, "bounds b") || strings.Contains(endpointRollupQuery, "FROM params") {
		t.Fatal("the query still derives its bounds from a CTE join; the scan filter cannot be pushed")
	}
	if !strings.Contains(endpointRollupQuery, "s.start_time < ? OR s.start_time >= ?") {
		t.Fatal("the boundary exclusion is no longer expressed against bound parameters")
	}
}
