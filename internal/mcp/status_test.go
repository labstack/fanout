package mcp

import (
	"testing"

	"github.com/labstack/fanout/internal/render"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected render.Format
	}{
		{"ascii", render.ASCII},
		{"html", render.HTML},
		{"both", render.Both},
		{"data", render.Data},
		{"", render.ASCII},           // default
		{"invalid", render.ASCII},    // invalid defaults to ASCII
		{"ASCII", render.ASCII},      // case sensitive - wrong case defaults
		{"HTML", render.ASCII},       // case sensitive
		{"  html  ", render.ASCII},   // no trimming - wrong input defaults
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := parseFormat(tc.input)
			if result != tc.expected {
				t.Errorf("parseFormat(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestRenderStatus(t *testing.T) {
	status := &StatusOut{
		Healthy: true,
		Summary: "All systems operational",
		Services: ServiceSummary{
			Total:     5,
			Healthy:   4,
			Degraded:  1,
			Unhealthy: 0,
		},
		TopIssues: []TopIssue{
			{Service: "api", Issue: "high_latency", Value: 1500.0},
		},
		ThroughputPerMin: 1000,
		P95Ms:            50.5,
		ErrorRate:        0.01,
	}

	output := renderStatus(status)

	// Should produce ASCII output
	if output.ASCII == "" {
		t.Error("renderStatus() should produce ASCII output")
	}

	// Should produce HTML output
	if output.HTML == "" {
		t.Error("renderStatus() should produce HTML output")
	}
}

func TestRenderStatus_Unhealthy(t *testing.T) {
	status := &StatusOut{
		Healthy: false,
		Summary: "System degraded",
		Services: ServiceSummary{
			Total:     3,
			Healthy:   1,
			Degraded:  1,
			Unhealthy: 1,
		},
		TopIssues:        []TopIssue{},
		ThroughputPerMin: 500,
		P95Ms:            200.0,
		ErrorRate:        0.15,
	}

	output := renderStatus(status)

	// Should produce output
	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderStatus() should produce output for unhealthy status")
	}
}

func TestRenderStatus_Empty(t *testing.T) {
	status := &StatusOut{
		Healthy:          true,
		Summary:          "No data",
		Services:         ServiceSummary{},
		TopIssues:        []TopIssue{},
		ThroughputPerMin: 0,
		P95Ms:            0,
		ErrorRate:        0,
	}

	output := renderStatus(status)

	// Should produce output even with empty data
	if output.ASCII == "" || output.HTML == "" {
		t.Error("renderStatus() should produce output for empty status")
	}
}

func TestServiceSummary(t *testing.T) {
	// Test that ServiceSummary can be created and used
	s := ServiceSummary{
		Total:     10,
		Healthy:   7,
		Degraded:  2,
		Unhealthy: 1,
	}

	if s.Total != 10 {
		t.Errorf("Total = %d, want 10", s.Total)
	}
	if s.Healthy != 7 {
		t.Errorf("Healthy = %d, want 7", s.Healthy)
	}
	if s.Degraded != 2 {
		t.Errorf("Degraded = %d, want 2", s.Degraded)
	}
	if s.Unhealthy != 1 {
		t.Errorf("Unhealthy = %d, want 1", s.Unhealthy)
	}
}

func TestTopIssue(t *testing.T) {
	issue := TopIssue{
		Service: "api-gateway",
		Issue:   "high_error_rate",
		Value:   0.15,
		Detail:  "15% errors in last 15 minutes",
	}

	if issue.Service != "api-gateway" {
		t.Errorf("Service = %q, want %q", issue.Service, "api-gateway")
	}
	if issue.Issue != "high_error_rate" {
		t.Errorf("Issue = %q, want %q", issue.Issue, "high_error_rate")
	}
}
