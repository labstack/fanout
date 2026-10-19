package mcp

import (
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDistributionSpecGuide(t *testing.T) {
	const want = "Distribution panels use a structured query with one count() measure and histogram: {field: duration_ms, buckets: log2} for spans or {field: value, buckets: explicit} for metrics. Negative span durations clamp to [0,1); +Inf goes to overflow; NaN is excluded because it is not a measurement. Metric histograms use stored temporality and shared per-series increases; resets contribute current counts. Delta histograms sum observation weights."
	if !strings.Contains(specGuide, want) {
		t.Errorf("guide omits exact distribution passage %q", want)
	}
	for _, old := range []string{"temporality: cumulative|delta", "last minus first", "counter resets mid-window contributes 0"} {
		if strings.Contains(specGuide, old) {
			t.Errorf("obsolete guide phrase %q", old)
		}
	}
}

func TestDistributionRejectsUnknownHistogramTemporality(t *testing.T) {
	s := newPanelServer(t)
	session := connectTestClient(t, s, nil)
	query := map[string]any{"from": "metrics", "measures": []string{"count()"}, "histogram": map[string]any{"field": "value", "buckets": "explicit", "temporality": "delta"}}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "preview_panels", Arguments: map[string]any{"panels": []any{map[string]any{"id": "hist", "title": "Hist", "viz": "histogram", "query": query}}}})
	if err == nil && !result.IsError {
		t.Fatalf("MCP accepted removed option: %+v", result)
	}
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: textDashboard("Strict edits")})
	if err != nil {
		t.Fatal(err)
	}
	result, edited, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, BaseVersion: 1, Operations: []dashboard.Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"viz": "histogram", "content": nil, "query": query}}}})
	if err != nil || !result.IsError || edited.Error == nil || edited.Error.Code != "invalid_spec" || !strings.Contains(edited.Error.Message, "temporality") || edited.Receipt != nil {
		t.Fatalf("dashboard edit accepted removed option: %+v %v", edited, err)
	}
}
