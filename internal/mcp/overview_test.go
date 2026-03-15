package mcp

import (
	"testing"
)

func TestOverviewOut_Structure(t *testing.T) {
	// Verify OverviewOut can be constructed with all fields
	out := OverviewOut{
		Timestamp: "2026-03-14T17:05:00Z",
		Window:    "15m",
		Health: OverviewHealth{
			Score:            0.92,
			TotalServices:    20,
			ByStatus:         map[string]int{"healthy": 16, "degraded": 3, "unhealthy": 1},
			ThroughputPerMin: 4850,
			GlobalErrorRate:  0.031,
			GlobalP95Ms:      18.2,
		},
		Services: []OverviewService{
			{
				Service:   "frontend",
				Status:    "degraded",
				Requests:  4397,
				ErrorRate: 0.036,
				P50Ms:     2.62,
				P95Ms:     16.17,
			},
		},
		TopIssues: []OverviewIssue{
			{
				Service:   "accounting",
				Issue:     "p95_latency",
				Value:     9444.36,
				Threshold: 500,
				Since:     "2026-03-14T16:30:00Z",
			},
		},
	}

	if out.Health.Score != 0.92 {
		t.Errorf("Health.Score = %v, want 0.92", out.Health.Score)
	}
	if out.Health.TotalServices != 20 {
		t.Errorf("Health.TotalServices = %d, want 20", out.Health.TotalServices)
	}
	if out.Health.ByStatus["healthy"] != 16 {
		t.Errorf("ByStatus[healthy] = %d, want 16", out.Health.ByStatus["healthy"])
	}
	if len(out.Services) != 1 {
		t.Errorf("Services count = %d, want 1", len(out.Services))
	}
	if out.Services[0].Service != "frontend" {
		t.Errorf("Services[0].Service = %q, want %q", out.Services[0].Service, "frontend")
	}
	if len(out.TopIssues) != 1 {
		t.Errorf("TopIssues count = %d, want 1", len(out.TopIssues))
	}
	if out.TopIssues[0].Issue != "p95_latency" {
		t.Errorf("TopIssues[0].Issue = %q, want %q", out.TopIssues[0].Issue, "p95_latency")
	}
}

func TestTimeWindowString(t *testing.T) {
	tests := []struct {
		minutes int
		want    string
	}{
		{0, "15m"},
		{15, "15m"},
		{30, "30m"},
		{60, "1h"},
		{120, "2h"},
		{1440, "1d"},
		{2880, "2d"},
		{90, "90m"}, // 1.5 hours -> stays in minutes
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			tw := TimeWindow{Minutes: tc.minutes}
			got := tw.String()
			if got != tc.want {
				t.Errorf("TimeWindow{Minutes: %d}.String() = %q, want %q", tc.minutes, got, tc.want)
			}
		})
	}
}

func TestOverviewIn_Defaults(t *testing.T) {
	// Test zero-value OverviewIn
	in := OverviewIn{}
	if in.Window != "" {
		t.Errorf("default Window = %q, want empty", in.Window)
	}
	if len(in.Include) != 0 {
		t.Errorf("default Include = %v, want empty", in.Include)
	}
	if in.SortServicesBy != "" {
		t.Errorf("default SortServicesBy = %q, want empty", in.SortServicesBy)
	}
	if in.Limit != 0 {
		t.Errorf("default Limit = %d, want 0", in.Limit)
	}
}

func TestOverviewIssue_Fields(t *testing.T) {
	issue := OverviewIssue{
		Service:   "my-service",
		Issue:     "high_error_rate",
		Value:     0.12,
		Threshold: 0.05,
		Since:     "2026-03-14T16:00:00Z",
	}

	if issue.Service != "my-service" {
		t.Errorf("Service = %q, want %q", issue.Service, "my-service")
	}
	if issue.Threshold != 0.05 {
		t.Errorf("Threshold = %v, want 0.05", issue.Threshold)
	}
	if issue.Since == "" {
		t.Error("Since should not be empty")
	}
}
