package mcp

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestSQLTimestampRuleAppearsOnceInAgentGuidance(t *testing.T) {
	field, ok := reflect.TypeOf(panel.Panel{}).FieldByName("SQL")
	if !ok || strings.Count(field.Tag.Get("jsonschema"), panel.SQLTimestampGuide) != 1 {
		t.Fatalf("SQL field guidance=%s", field.Tag.Get("jsonschema"))
	}
	docs, err := DescribeTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"create_dashboard", "get_telemetry_schema"} {
		found := false
		for _, doc := range docs {
			if doc.Name == name {
				found = true
				if strings.Count(doc.Description, panel.SQLTimestampGuide) != 1 {
					t.Fatalf("%s must state the timestamp rule once", name)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestExamplesAndPreviewCoverNewTypes(t *testing.T) {
	s := newPanelServer(t)
	coverage := map[string]int{}
	for example, text := range []string{investigationExample, paymentExample, metricExample} {
		var d panel.Dashboard
		if err := json.Unmarshal([]byte(text), &d); err != nil {
			t.Fatal(err)
		}
		wantPanels := []int{9, 3, 2}[example]
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
			for _, guide := range []string{baseSpecGuide, m2SpecGuide, metricMeasureGuide, investigationExample, paymentExample, metricExample} {
				if strings.Contains(doc.Description, guide) {
					t.Errorf("%s duplicates the full guide or examples", doc.Name)
				}
			}
			continue
		}
		seen[doc.Name] = true
		for _, guide := range []string{baseSpecGuide, m2SpecGuide, metricMeasureGuide, investigationExample, paymentExample, metricExample} {
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
		for _, guide := range []string{baseSpecGuide, m2SpecGuide, metricMeasureGuide, investigationExample, paymentExample, metricExample} {
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
	if size := len([]rune(investigationExample + paymentExample + metricExample)); size > 3343 {
		t.Errorf("examples have %d characters, maximum 3343", size)
	}
}

func TestMetricGuidanceIsConciseAndExamplesValidate(t *testing.T) {
	if utf8.RuneCountInString(metricMeasureGuide) > 900 {
		t.Fatalf("metric guide has %d characters", utf8.RuneCountInString(metricMeasureGuide))
	}
	for _, term := range []string{"gauge/updown", "counter", "histogram", "summary", "rate()", "sum(value)", "avg(value)", "last(value)", "pNN", "elapsed", "All", "multi-value", "count_distinct()", "own where entry", "name in by", "strictly before bucket/window end", "reported within 5m", "per-second increase over covered elapsed time", "interpolated bucket increases"} {
		if !strings.Contains(metricMeasureGuide, term) {
			t.Fatalf("missing %q", term)
		}
	}
	s := newPanelServer(t)
	var d panel.Dashboard
	if err := json.Unmarshal([]byte(metricExample), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Panels) != 2 || d.Panels[0].Query.Measures[0] != "rate()" || d.Panels[1].Query.Measures[0] != "p95(value)" {
		t.Fatalf("examples=%+v", d)
	}
	if err := s.panels.Validate(t.Context(), &d); err != nil {
		t.Fatal(err)
	}
	_, preview, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: d.Panels, Time: &d.Time, Variables: d.Variables})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range preview.Panels {
		if p.Status != "ok" && (p.Status != "empty" || p.Diagnosis == "") {
			t.Fatalf("preview=%+v", p)
		}
	}
	tools, err := DescribeTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name == "create_dashboard" && (!strings.Contains(tool.Description, metricMeasureGuide) || !strings.Contains(tool.Description, metricExample)) {
			t.Fatal("create omits metric guide/examples")
		}
		if tool.Name != "create_dashboard" && strings.Contains(tool.Description, metricExample) {
			t.Fatalf("duplicate metric example in %s", tool.Name)
		}
	}
}

func TestMetricExamplesExecuteWithObservations(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var metrics []telemetry.Metric
	for i := range 2 {
		// The completed interval must overlap the example's selected window.
		n := at.Add(time.Duration(i-1)*time.Minute + 30*time.Second).UnixNano()
		metrics = append(metrics,
			telemetry.Metric{Namespace: "test", ServiceName: "api", Name: "system.network.io", Unit: "By",
				Type: "sum", Monotonic: true, Temporality: "cumulative", Value: float64(i * 60),
				TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n, Attributes: map[string]any{"direction": "receive"}},
			telemetry.Metric{Namespace: "test", ServiceName: "api", Name: "http.server.request.duration", Unit: "s",
				Type: "histogram", Temporality: "cumulative", HistCount: int64(i * 100), HistSum: float64(i * 50),
				HistBoundsJSON: "[1]", HistCountsJSON: []string{"[0,0]", "[100,0]"}[i],
				TimeUnixNanos: n, EventUnixNanos: n, IngestedAt: n})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "metric-examples", Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = duck.Close() })
	executor := panel.NewExecutor(duck, 30)
	var d panel.Dashboard
	if err := json.Unmarshal([]byte(metricExample), &d); err != nil {
		t.Fatal(err)
	}
	end := at.Add(time.Minute)
	d.Time = panel.Time{From: &at, To: &end}
	results, err := executor.Run(t.Context(), panel.RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%+v", results)
	}
	for i, want := range []float64{1, 0.95} {
		r := results[i]
		if r.Status != panel.StatusOK || r.Frame == nil || r.Frame.Rows != 1 {
			t.Fatalf("%s: %+v", r.ID, r)
		}
		found := false
		for j, column := range r.Frame.Columns {
			if column.Name == []string{"rate", "p95"}[i] {
				found = true
				value, ok := r.Frame.Values[j][0].(float64)
				if !ok || math.Abs(value-want) > 1e-9 {
					t.Fatalf("%s: value=%v, want %v", r.ID, r.Frame.Values[j][0], want)
				}
			}
		}
		if !found {
			t.Fatalf("%s: measure column missing: %+v", r.ID, r.Frame)
		}
	}
}

func TestResourceInventoryExampleExecutesThroughPanelTools(t *testing.T) {
	now := time.Now().UTC()
	s := panelServerFixtureAt(t, true, now)
	var d panel.Dashboard
	if err := json.Unmarshal([]byte(resourceInventoryExample), &d); err != nil {
		t.Fatal(err)
	}
	if err := s.panels.Validate(t.Context(), &d); err != nil {
		t.Fatal(err)
	}
	_, preview, err := s.previewPanels(t.Context(), nil, PreviewInput{Panels: d.Panels, Time: &d.Time})
	if err != nil || len(preview.Panels) != 1 || preview.Panels[0].Status != "ok" || preview.Panels[0].Rows != 1 {
		t.Fatalf("inventory preview=%+v %v", preview, err)
	}
	_, fragment, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Panel: d.Panels[0]})
	if err != nil || len(fragment.Results) != 1 || fragment.Results[0].Status != "ok" {
		t.Fatalf("inventory query=%+v %v", fragment, err)
	}
	_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: d})
	if err != nil || created.Dashboard.Version != 1 {
		t.Fatalf("inventory create=%+v %v", created, err)
	}
	_, schema, err := s.telemetrySchema(t.Context(), nil, SchemaInput{})
	if err != nil || len(schema.Signals) != 3 || len(schema.SQLRelations["resource_inventory"].Columns) != 6 {
		t.Fatalf("inventory schema=%+v %v", schema, err)
	}
}
