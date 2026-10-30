package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/panel"
)

type fakeFanQLParser struct {
	panel.Parser
	err error
}

func (p *fakeFanQLParser) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("format parser requires a deadline")
	}
	if p.err != nil {
		return nil, p.err
	}
	if strings.Contains(text, "service") {
		return map[string]any{"where_clause": map[string]any{
			"class": "COMPARISON", "type": "COMPARE_EQUAL",
			"left":  map[string]any{"class": "COLUMN_REF", "column_names": []any{"service"}},
			"right": map[string]any{"class": "CONSTANT", "literal": map[string]any{"kind": "STRING", "value": "api"}},
		}}, nil
	}
	return map[string]any{}, nil
}

func (*fakeFanQLParser) RenderSQL(context.Context, map[string]any) (string, error) {
	return "SELECT 1 FROM spans WHERE service = 'api'", nil
}

type fakeFanQLPanels struct{ fakePanels }

func (*fakeFanQLPanels) Schema(_ context.Context, req panel.SchemaRequest) (*panel.Schema, error) {
	return &panel.Schema{Window: req.Window, Signals: map[string]panel.SignalSchema{
		"logs": {Columns: []panel.ColumnSchema{{Field: panel.Field{Name: "severity", Type: panel.TypeString}, Values: []panel.Option{{Value: "ERROR"}, {Value: "INFO"}, {Value: "WARN"}}}}},
	}}, nil
}

func serveFanQL(t *testing.T, parser panel.Parser, engine PanelEngine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterFanQLRoutes(s.e, parser, engine)
	req := sessionRequest(method, path, strings.NewReader(body), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func fanqlResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, target any) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

func TestFanQLParseReturnsQueryCanonicalAndVizzes(t *testing.T) {
	text := "spans where status:error | count() by service"
	rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"fanql_version":1,"text":"`+text+`"}`)
	var body struct {
		Version   int         `json:"fanql_version"`
		Query     panel.Query `json:"query"`
		Canonical string      `json:"canonical"`
		Viz       struct {
			Compatible []panel.VizCompat `json:"compatible"`
			Edits      []fanql.VizEdit   `json:"edits"`
		} `json:"viz"`
		Notices []fanql.Notice `json:"notices"`
	}
	fanqlResponse(t, rec, http.StatusOK, &body)
	want := panel.Query{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'"}, Measures: []string{"count()"}, By: []string{"service"}}
	if body.Version != 1 || !reflect.DeepEqual(body.Query, want) || body.Canonical != text || body.Notices == nil || len(body.Notices) != 0 {
		t.Fatalf("response: %s", rec.Body)
	}
	for _, viz := range []string{"bar", "table"} {
		found := false
		for _, compatible := range body.Viz.Compatible {
			if compatible.Viz == viz && compatible.Requires != nil && len(compatible.Requires) == 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("missing compatible %s: %+v", viz, body.Viz)
		}
	}
	found := false
	for _, edit := range body.Viz.Edits {
		if edit.Viz == "timeseries" && edit.Text == text+" every auto" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing timeseries edit: %+v", body.Viz.Edits)
	}
}

func TestFanQLParseProblemsHaveNoQuery(t *testing.T) {
	for _, preview := range []bool{false, true} {
		rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", fmt.Sprintf(`{"text":"spans where a:1 b:2 or c:3 | p99(duration) by http_route every 1m","preview":%t}`, preview))
		var body struct {
			Code     string         `json:"code"`
			Message  string         `json:"message"`
			Problems panel.Problems `json:"problems"`
			Preview  *struct {
				Text string `json:"text"`
				Note string `json:"note"`
			} `json:"preview"`
		}
		fanqlResponse(t, rec, http.StatusBadRequest, &body)
		if body.Code != "invalid_fanql" || body.Message != "The query has problems." || len(body.Problems) == 0 || body.Problems[0].Message != "mixed and/or needs parentheses" || body.Problems[0].Span == nil || len(body.Problems[0].FixIts) != 2 {
			t.Fatalf("problem response: %s", rec.Body)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
			t.Fatal(err)
		}
		if _, ok := keys["query"]; ok {
			t.Fatal("invalid query must not be executable")
		}
		if preview {
			if body.Preview == nil || body.Preview.Note != "not executable" || body.Preview.Text == "" {
				t.Fatalf("preview: %s", rec.Body)
			}
		} else if _, ok := keys["preview"]; ok {
			t.Fatal("unexpected preview")
		}
	}
}

func TestFanQLParseSearchBarAndViz(t *testing.T) {
	rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"fanql_version":1,"text":"service:api timeout","source":"logs"}`)
	var body struct {
		Query panel.Query `json:"query"`
		Viz   struct {
			Compatible []panel.VizCompat `json:"compatible"`
			Requested  string            `json:"requested"`
		} `json:"viz"`
		Notices []fanql.Notice `json:"notices"`
	}
	fanqlResponse(t, rec, http.StatusOK, &body)
	if len(body.Query.Where) != 2 || len(body.Viz.Compatible) != 1 || body.Viz.Compatible[0].Viz != "logs" || body.Viz.Compatible[0].Requires == nil {
		t.Fatalf("search bar: %s", rec.Body)
	}
	rec = serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"text":"where service:api","source":"logs"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "the page supplies where") {
		t.Fatalf("where: %d %s", rec.Code, rec.Body)
	}
	rec = serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"fanql_version":1,"text":"spans | count() every 1m sort count","viz":"timeseries"}`)
	fanqlResponse(t, rec, http.StatusOK, &body)
	if body.Viz.Requested != "timeseries" || len(body.Notices) != 1 || !strings.Contains(body.Notices[0].Message, "sort does not apply to bucketed series") {
		t.Fatalf("bucketed sort: %s", rec.Body)
	}
}

func TestFanQLVersionHandling(t *testing.T) {
	for _, path := range []string{"parse", "format", "complete"} {
		for _, version := range []string{"2", "0", "-1", `"1"`, "null"} {
			rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/"+path, `{"fanql_version":`+version+`}`)
			var body struct {
				Code string `json:"code"`
				Hint string `json:"hint"`
			}
			fanqlResponse(t, rec, http.StatusBadRequest, &body)
			if body.Code != "unsupported_fanql_version" || body.Hint != "send fanql_version: 1" {
				t.Fatalf("%s version %s: %s", path, version, rec.Body)
			}
		}
	}
	for _, path := range []string{"parse", "format"} {
		input := `{"text":"spans"}`
		if path == "format" {
			input = `{"query":{"from":"spans"}}`
		}
		rec := serveFanQL(t, &fakeFanQLParser{}, &fakePanels{}, http.MethodPost, "/api/fanql/"+path, input)
		var body struct {
			Notices []fanql.Notice `json:"notices"`
		}
		fanqlResponse(t, rec, http.StatusOK, &body)
		if len(body.Notices) != 1 || body.Notices[0].Message != "fanql_version missing; assuming 1" {
			t.Fatalf("missing version: %s", rec.Body)
		}
	}
	for _, version := range []string{"2", "bogus", ""} {
		rec := serveFanQL(t, nil, &fakePanels{}, http.MethodGet, "/api/fanql/reference?fanql_version="+version, "")
		var body struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		}
		fanqlResponse(t, rec, http.StatusBadRequest, &body)
		if body.Code != "unsupported_fanql_version" || body.Hint != "send fanql_version: 1" {
			t.Fatalf("reference version: %s", rec.Body)
		}
	}
}

func TestFanQLFormat(t *testing.T) {
	rec := serveFanQL(t, &fakeFanQLParser{}, &fakePanels{}, http.MethodPost, "/api/fanql/format", `{"fanql_version":1,"query":{"from":"spans","where":["service='api'"],"measures":["p95( duration_ms )"]},"panel":{"id":"a","title":"A","viz":"stat"}}`)
	var body struct {
		Version int `json:"fanql_version"`
		fanql.Formatted
	}
	fanqlResponse(t, rec, http.StatusOK, &body)
	if body.Version != 1 || body.Text != "spans where service:api | p95(duration)" || body.Exactness != "normalized" || !body.Editable {
		t.Fatalf("format: %s", rec.Body)
	}
	if len(body.Notices) == 0 || body.Notices[0].Message != `where[0] is shown as service:api; stored text is "service='api'"` {
		t.Fatalf("notices: %s", rec.Body)
	}
	rec = serveFanQL(t, &fakeFanQLParser{}, &fakePanels{}, http.MethodPost, "/api/fanql/format", `{"query":{"from":"spans","measures":["count()"],"by":["a","b","c","d"]}}`)
	var failure map[string]json.RawMessage
	fanqlResponse(t, rec, http.StatusBadRequest, &failure)
	if string(failure["code"]) != `"invalid_spec"` || failure["problems"] == nil {
		t.Fatalf("shape: %s", rec.Body)
	}
	rec = serveFanQL(t, &fakeFanQLParser{err: context.DeadlineExceeded}, &fakePanels{}, http.MethodPost, "/api/fanql/format", `{"query":{"from":"logs","where":["coalesce(body, '') = ''"]}}`)
	failure = nil
	fanqlResponse(t, rec, http.StatusServiceUnavailable, &failure)
	if string(failure["code"]) != `"unavailable"` || failure["problems"] != nil {
		t.Fatalf("operational: %s", rec.Body)
	}
	q := panel.Query{From: "logs"}
	for i := range 16 {
		q.Where = append(q.Where, "body = '"+strings.Repeat("x", 480)+fmt.Sprint(i)+"'")
	}
	input, err := json.Marshal(map[string]any{"fanql_version": 1, "query": q})
	if err != nil {
		t.Fatal(err)
	}
	rec = serveFanQL(t, &fakeFanQLParser{}, &fakePanels{}, http.MethodPost, "/api/fanql/format", string(input))
	fanqlResponse(t, rec, http.StatusOK, &body)
	if body.Editable || !strings.HasPrefix(body.Reason, "formatted text is") {
		t.Fatalf("oversized character format: %s", rec.Body)
	}
	for i := range 16 {
		if !strings.Contains(body.Text, strings.Repeat("x", 480)+fmt.Sprint(i)) {
			t.Fatal("format truncated a body literal")
		}
	}
	q = panel.Query{From: "logs"}
	for i := range 16 {
		q.Where = append(q.Where, "coalesce(body, '"+strings.Repeat("𝔘", 110)+strings.Repeat("`", 30)+fmt.Sprint(i)+"') = ''")
	}
	input, err = json.Marshal(map[string]any{"fanql_version": 1, "query": q})
	if err != nil {
		t.Fatal(err)
	}
	rec = serveFanQL(t, &fakeFanQLParser{}, &fakePanels{}, http.MethodPost, "/api/fanql/format", string(input))
	fanqlResponse(t, rec, http.StatusOK, &body)
	if body.Editable || !strings.HasPrefix(body.Reason, "formatted text is") || len(body.Text) <= 8192 {
		t.Fatalf("oversized format: %s", rec.Body)
	}
	for _, entry := range q.Where {
		if !strings.Contains(body.Text, strings.ReplaceAll(entry, "`", "``")) {
			t.Fatal("format truncated an entry")
		}
	}
}

func TestFanQLComplete(t *testing.T) {
	rec := serveFanQL(t, nil, &fakeFanQLPanels{}, http.MethodPost, "/api/fanql/complete", `{"fanql_version":1,"text":"logs where sev","cursor":14}`)
	var body struct {
		Version int            `json:"fanql_version"`
		Replace panel.TextSpan `json:"replace"`
		Items   []struct {
			Label string `json:"label"`
		} `json:"items"`
		Schema struct {
			Window string `json:"window"`
			Warm   bool   `json:"warm"`
			Recent bool   `json:"recent"`
		} `json:"schema"`
		Notices []fanql.Notice `json:"notices"`
	}
	fanqlResponse(t, rec, http.StatusOK, &body)
	if body.Version != 1 || body.Replace != (panel.TextSpan{Start: 11, End: 14}) || len(body.Items) == 0 || body.Items[0].Label != "severity" || body.Schema.Window != "24h" || body.Schema.Warm || !body.Schema.Recent {
		t.Fatalf("complete: %s", rec.Body)
	}
	rec = serveFanQL(t, nil, &fakeFanQLPanels{}, http.MethodPost, "/api/fanql/complete", `{"text":"logs where sev","cursor":14,"window":"","namespace":"prod"}`)
	fanqlResponse(t, rec, http.StatusOK, &body)
	if body.Schema.Window != "24h" || !body.Schema.Recent || len(body.Notices) != 1 || body.Notices[0].Message != "fanql_version missing; assuming 1" {
		t.Fatalf("absolute completion: %s", rec.Body)
	}
}

func TestFanQLReference(t *testing.T) {
	for _, path := range []string{"/api/fanql/reference", "/api/fanql/reference?fanql_version=1"} {
		rec := serveFanQL(t, nil, &fakePanels{}, http.MethodGet, path, "")
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(rec.Body.String(), "FanQL 1") || !strings.Contains(rec.Body.String(), "Preview") {
			t.Fatalf("reference: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestFanQLRoutesAreClassified(t *testing.T) {
	for _, path := range []string{"parse", "format", "complete", "reference"} {
		method, other := http.MethodPost, http.MethodGet
		if path == "reference" {
			method, other = other, method
		}
		policy, ok := classifyRoute(method, "/api/fanql/"+path)
		if !ok || policy.kind != routePolicyCapability || policy.capability != ReadTelemetry {
			t.Errorf("%s not telemetry:read", path)
		}
		if _, ok := classifyRoute(other, "/api/fanql/"+path); ok {
			t.Errorf("%s %s classified", other, path)
		}
	}
	s := newTestAuthServer(t)
	RegisterFanQLRoutes(s.e, nil, &fakePanels{})
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodPost, "/api/fanql/parse", strings.NewReader(`{"text":"spans"}`), nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}
	rec = serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"text":"spans"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer: %d %s", rec.Code, rec.Body)
	}
}

func TestFanQLBodyLimitsAndUnknownFields(t *testing.T) {
	for _, path := range []string{"parse", "format", "complete"} {
		valid := `{"text":"spans"}`
		if path == "format" {
			valid = `{"query":{"from":"spans"}}`
		}
		for _, input := range []string{`{"unknown":1}`, valid + strings.Repeat(" ", (512<<10)+1), `{} {}`} {
			rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/"+path, input)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s bad body: %d %s", path, rec.Code, rec.Body)
			}
		}
	}
	rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"text":"`+strings.Repeat("a", 4001)+`"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_fanql"`) || !strings.Contains(rec.Body.String(), "a query is at most 4000 characters (8192 bytes)") {
		t.Fatalf("interactive bound: %d %s", rec.Code, rec.Body)
	}
}

func TestFanQLVersionUsesSharedFieldAndReferenceHeaders(t *testing.T) {
	rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/parse", `{"fanql_version":1,"text":"spans"}`)
	var body map[string]json.RawMessage
	fanqlResponse(t, rec, http.StatusOK, &body)
	if string(body["fanql_version"]) != "1" || body["version"] != nil {
		t.Fatalf("version contract: %s", rec.Body)
	}
	rec = serveFanQL(t, nil, &fakePanels{}, http.MethodGet, "/api/fanql/reference", "")
	if rec.Code != http.StatusOK || rec.Body.String() != fanql.Reference() || rec.Header().Get("FanQL-Notice") == "" || rec.Header().Get("FanQL-Version") != "1" {
		t.Fatalf("reference metadata: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

func TestFanQLCompletionAbsoluteAndLongWindowsDiscloseRecentSchema(t *testing.T) {
	for _, window := range []string{
		`"from":"2026-10-20T10:00:00Z","to":"2026-10-20T11:00:00Z"`,
		`"window":"7d"`,
	} {
		rec := serveFanQL(t, nil, &fakeFanQLPanels{}, http.MethodPost, "/api/fanql/complete", `{"fanql_version":1,"text":"logs where sev","cursor":14,`+window+`}`)
		var body struct {
			Schema struct {
				Window string `json:"window"`
				Recent bool   `json:"recent"`
				Notice string `json:"notice"`
			} `json:"schema"`
		}
		fanqlResponse(t, rec, http.StatusOK, &body)
		if body.Schema.Window != "24h" || !body.Schema.Recent || body.Schema.Notice != "keys seen in the last 24h (recent), not in the selected interval" {
			t.Fatalf("disclosure: %s", rec.Body)
		}
	}
}

func TestFanQLFormatReportsPanelRequirements(t *testing.T) {
	for _, tt := range []struct {
		panel, query, name string
		satisfied          bool
	}{
		{`{"viz":"gauge"}`, `{"from":"spans","measures":["count()"]}`, "min_max", false},
		{`{"viz":"gauge","min":0,"max":100}`, `{"from":"spans","measures":["count()"]}`, "min_max", true},
		{`{"viz":"state_timeline"}`, `{"from":"spans","measures":["count()"],"by":["service"],"bucket":"1m"}`, "thresholds", false},
		{`{"viz":"state_timeline","thresholds":[{"value":10,"status":"warn"}]}`, `{"from":"spans","measures":["count()"],"by":["service"],"bucket":"1m"}`, "thresholds", true},
	} {
		input := `{"fanql_version":1,"query":` + tt.query + `,"panel":` + tt.panel + `}`
		rec := serveFanQL(t, nil, &fakePanels{}, http.MethodPost, "/api/fanql/format", input)
		var body struct {
			Requires []struct {
				Name      string `json:"name"`
				Satisfied bool   `json:"satisfied"`
			} `json:"requires"`
		}
		fanqlResponse(t, rec, http.StatusOK, &body)
		if len(body.Requires) != 1 || body.Requires[0].Name != tt.name || body.Requires[0].Satisfied != tt.satisfied {
			t.Fatalf("requirements: %s", rec.Body)
		}
	}
}
