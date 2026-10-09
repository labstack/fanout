package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestExamplesAndPreviewCoverNewTypes(t *testing.T) {
	s := newPanelServer(t)
	coverage := map[string]int{}
	for example, text := range []string{investigationExample, paymentExample} {
		var d panel.Dashboard
		if err := json.Unmarshal([]byte(text), &d); err != nil {
			t.Fatal(err)
		}
		wantPanels := []int{9, 3}[example]
		if len(d.Panels) != wantPanels {
			t.Errorf("example %d has %d panels, want %d", example+1, len(d.Panels), wantPanels)
		}
		if example == 0 {
			counts := map[string]int{}
			for _, p := range d.Panels {
				counts[p.Viz]++
			}
			for _, viz := range []string{"heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"} {
				if counts[viz] != 1 {
					t.Errorf("first example has %d %s panels, want one", counts[viz], viz)
				}
			}
		}
		for _, variable := range d.Variables {
			if variable.Kind != "query" {
				t.Errorf("example %d uses invented variable values: %+v", example+1, variable)
			}
		}
		if example == 1 {
			if len(d.Panels) != 3 {
				t.Fatal("second example must have three panels")
			}
			histogram, table, shifted := d.Panels[0], d.Panels[1], d.Panels[2]
			if histogram.Viz != "histogram" || histogram.Query == nil || histogram.Query.From != "metrics" || strings.Contains(histogram.Title, "by operation") {
				t.Error("second example must start with an accurately titled metric histogram")
			}
			formats := map[string]bool{}
			if table.Viz == "table" && table.Options != nil {
				for _, column := range table.Options.Columns {
					formats[column.Format] = true
					if column.Format == "service_link" && column.Variable == "" {
						t.Error("service_link omits variable")
					}
				}
			}
			if !formats["service_link"] || !formats["sparkline"] {
				t.Error("second example needs service_link and sparkline table columns")
			}
			if shifted.Click == nil || shifted.Click.SetVariable == "" || shifted.Time == nil || shifted.Time.Shift == "" {
				t.Error("second example needs click and a time shift on one panel")
			}
		}
		if err := s.panels.Validate(t.Context(), &d); err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		_, result, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: d.Panels, Time: &d.Time, Variables: d.Variables})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Panels) != len(d.Panels) {
			t.Fatalf("preview lost panels: %+v", result)
		}
		for i, preview := range result.Panels {
			if preview.Status != "ok" && preview.Status != "empty" {
				t.Fatalf("%s: %+v", d.Panels[i].Viz, preview)
			}
			if preview.Status == "empty" && preview.Diagnosis == "" {
				t.Fatalf("unexplained empty %s", preview.ID)
			}
			coverage[d.Panels[i].Viz]++
		}
	}
	for _, viz := range []string{"heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"} {
		if coverage[viz] < 1 {
			t.Fatalf("%s has %d examples", viz, coverage[viz])
		}
	}
}

func TestDescriptionsExposeAnalysisContract(t *testing.T) {
	docs, err := DescribeTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		fullGuide := doc.Name == "create_dashboard"
		if !fullGuide {
			if !strings.HasSuffix(doc.Name, "_dashboard") && !strings.HasSuffix(doc.Name, "_dashboards") && !strings.HasSuffix(doc.Name, "_panels") && !strings.HasSuffix(doc.Name, "_panel") {
				continue
			}
			seen[doc.Name] = true
			pointer := "See create_dashboard for the spec guide."
			if doc.Name == "preview_panels" {
				pointer = "Spec guide: see create_dashboard."
			}
			if !strings.Contains(doc.Description, pointer) {
				t.Errorf("%s omits the spec guide pointer", doc.Name)
			}
			for _, guide := range []string{baseSpecGuide, m2SpecGuide, investigationExample, paymentExample} {
				if strings.Contains(doc.Description, guide) {
					t.Errorf("%s duplicates the full guide or examples", doc.Name)
				}
			}
			continue
		}
		seen[doc.Name] = true
		for _, guide := range []string{baseSpecGuide, m2SpecGuide, investigationExample, paymentExample} {
			if !strings.Contains(doc.Description, guide) {
				t.Errorf("%s omits the full guide or examples", doc.Name)
			}
		}
		if strings.Count(doc.Description, "Reference shapes; use only the types the question needs.") != 2 {
			t.Error("both examples must carry the reference-shape label")
		}
		for _, term := range []string{"heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health", "set_variable", "drill", "annotations", "split", "columns"} {
			if !strings.Contains(doc.Description, term) {
				t.Errorf("%s omits %s", doc.Name, term)
			}
		}
	}
	for _, name := range []string{"preview_panels", "create_dashboard", "replace_dashboard", "edit_dashboard"} {
		if !seen[name] {
			t.Fatalf("missing description %s: %+v", name, seen)
		}
	}
	if RequiredToolScope("preview_panels") != "" || RequiredToolScope("create_dashboard") != "dashboard:manage" {
		t.Fatal("M1 authorization changed")
	}
}

func TestSpecGuidePinsAnalysisSemantics(t *testing.T) {
	for _, sentence := range []string{
		"state_timeline needs at least one threshold marked warn or bad, rather than only ok.",
		"Annotations attach only to timeseries, heatmap and state_timeline panels, scoped by service and namespace filters.",
		"drill: logs needs query.from=logs.",
		"Drill is not available on metrics.",
		"Every table format's field must exist in the returned frame; formatting cannot reference an absent column.",
		"A structured-table sparkline field must be a measure alias from that table's query.",
		"service_link needs variable to name the query or custom variable selected by the link.",
		"highlight applies to logs and log_patterns, at most 200 characters.",
		"split: deploy needs a service equality filter, as a literal or a single-value variable; other filters are allowed.",
		"When the service is All, a split: deploy panel shows the plain bar with a note explaining the missing service scope.",
		"Use service_map for dependency context, health for service health; neither proves causality.",
	} {
		if !strings.Contains(m2SpecGuide, sentence) {
			t.Errorf("guide omits %q", sentence)
		}
	}
}

func TestSpecGuideIsLimitedToCreate(t *testing.T) {
	docs, err := DescribeTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		want := 0
		if doc.Name == "create_dashboard" {
			want = 1
		}
		for _, guide := range []string{baseSpecGuide, m2SpecGuide, investigationExample, paymentExample} {
			if got := strings.Count(doc.Description, guide); got != want {
				t.Errorf("%s contains guidance %d times, want %d", doc.Name, got, want)
			}
		}
		if doc.Name == "get_telemetry_schema" && !strings.Contains(doc.Description, "See create_dashboard for the spec guide.") {
			t.Error("telemetry schema omits the spec guide pointer")
		}
	}
}

func TestGuidanceSize(t *testing.T) {
	if size := len([]rune(m2SpecGuide)); size > 1100 {
		t.Errorf("M2 guide has %d characters, want about 1000", size)
	}
	if size := len([]rune(investigationExample + paymentExample)); size > 2900 {
		t.Errorf("examples have %d characters, want about 2600", size)
	}
}
