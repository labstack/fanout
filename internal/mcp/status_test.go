package mcp

import "testing"

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
