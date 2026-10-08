package agent

import (
	"strings"
	"testing"
)

func TestDashboardGuidancePreservesIntentAndRequiresPreview(t *testing.T) {
	for _, term := range []string{"answer a single factual question with a view instead", "preview_panels", "fix every invalid panel", "get_dashboard first", "edit_dashboard", "heatmap", "histogram", "scatter", "state_timeline", "log_patterns", "service_map", "health", "drill", "annotations", "split", "distinguish missing data from healthy behavior", "single fact"} {
		if !strings.Contains(systemPrompt, term) {
			t.Errorf("prompt omits %q", term)
		}
	}
	intent := "Build one whenever the user asks for an overview, asks why something is slow, failing or changing, asks to compare, break down or track telemetry, or asks for anything they would want to look at again; answer a single factual question with a view instead."
	if !strings.Contains(systemPrompt, intent) {
		t.Error("prompt omits M1 intent-preservation rule")
	}
}

func TestDashboardGuidancePinsEvidenceAndIntent(t *testing.T) {
	if !strings.HasSuffix(systemPrompt, dashboardAnalysisGuidance) {
		t.Fatal("analysis guidance is not appended to the system prompt")
	}
	for _, sentence := range []string{
		"Use drill for span or log evidence; keep checked filters.",
		"Include annotations for change investigations; deploys and detector findings do not prove causes.",
		"Use a deploy split for scoped before/since comparisons; retain missing-deploy explanations.",
		"A definition, explanation, or single fact is an answer intent; do not create or replace a dashboard for it.",
		"Preserve every requested facet and explain absent telemetry without inventing it.",
		"use only the types the question needs; do not fill a dashboard with all of them",
		"never name schema fields to the user",
		"Explain in chat is answer intent",
		"observed absolute panel window and resolved variables",
		"without creating, editing, replacing or restoring dashboards",
	} {
		if !strings.Contains(dashboardAnalysisGuidance, sentence) {
			t.Errorf("guidance omits %q", sentence)
		}
	}
}
