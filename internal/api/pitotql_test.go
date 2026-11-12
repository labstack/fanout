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

	"github.com/labstack/pitot/internal/auth"
	"github.com/labstack/pitot/internal/pitotql"
	"github.com/labstack/pitot/internal/query"
)

type fakePitotQLParser struct {
	query.Parser
	err error
}

func (p *fakePitotQLParser) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
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

func (*fakePitotQLParser) RenderSQL(context.Context, map[string]any) (string, error) {
	return "SELECT 1 FROM spans WHERE service = 'api'", nil
}

type fakePitotQLPanels struct{ fakePanels }

func (*fakePitotQLPanels) Schema(_ context.Context, req query.SchemaRequest) (*query.Schema, error) {
	if req.Cached {
		return nil, query.ErrSchemaNotCached
	}
	return &query.Schema{Window: req.Window, Signals: map[string]query.SignalSchema{
		"logs": {Columns: []query.ColumnSchema{{Field: query.Field{Name: "severity", Type: query.TypeString}, Values: []query.Option{{Value: "ERROR"}, {Value: "INFO"}, {Value: "WARN"}}}}},
	}}, nil
}

func servePitotQL(t *testing.T, parser query.Parser, engine QueryEngine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterPitotQLRoutes(s.e, parser, engine)
	req := sessionRequest(method, path, strings.NewReader(body), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func pitotqlResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, target any) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

func TestPitotQLParseReturnsQueryCanonicalAndVizzes(t *testing.T) {
	text := "spans where status:error | count() by service"
	rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"`+text+`"}`)
	var body struct {
		Query     query.Spec `json:"query"`
		Canonical string     `json:"canonical"`
		Viz       struct {
			Compatible []query.VizCompat `json:"compatible"`
			Edits      []pitotql.VizEdit `json:"edits"`
		} `json:"viz"`
		Notices []pitotql.Notice `json:"notices"`
	}
	pitotqlResponse(t, rec, http.StatusOK, &body)
	want := query.Spec{From: "spans", Where: []string{"status = 'STATUS_CODE_ERROR'"}, Measures: []string{"count()"}, By: []string{"service"}}
	if !reflect.DeepEqual(body.Query, want) || body.Canonical != text || body.Notices == nil || len(body.Notices) != 0 {
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

func TestPitotQLParseProblemsHaveNoQuery(t *testing.T) {
	for _, preview := range []bool{false, true} {
		rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", fmt.Sprintf(`{"text":"spans where a:1 b:2 or c:3 | p99(duration) by http.route every 1m","preview":%t}`, preview))
		var body struct {
			Code     string         `json:"code"`
			Message  string         `json:"message"`
			Problems query.Problems `json:"problems"`
			Preview  *struct {
				Text string `json:"text"`
				Note string `json:"note"`
			} `json:"preview"`
		}
		pitotqlResponse(t, rec, http.StatusBadRequest, &body)
		if body.Code != "invalid_pitotql" || body.Message != "The query has problems." || len(body.Problems) == 0 || body.Problems[0].Message != "mixed and/or needs parentheses" || body.Problems[0].Span == nil || len(body.Problems[0].FixIts) != 2 {
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

func TestPitotQLParseSearchBarAndViz(t *testing.T) {
	rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"service:api timeout","source":"logs"}`)
	var body struct {
		Query query.Spec `json:"query"`
		Viz   struct {
			Compatible []query.VizCompat `json:"compatible"`
			Requested  string            `json:"requested"`
		} `json:"viz"`
		Notices []pitotql.Notice `json:"notices"`
	}
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if len(body.Query.Where) != 2 || len(body.Viz.Compatible) != 1 || body.Viz.Compatible[0].Viz != "logs" || body.Viz.Compatible[0].Requires == nil {
		t.Fatalf("search bar: %s", rec.Body)
	}
	rec = servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"where service:api","source":"logs"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "the page supplies where") {
		t.Fatalf("where: %d %s", rec.Code, rec.Body)
	}
	rec = servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"spans | count() every 1m sort count","viz":"timeseries"}`)
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if body.Viz.Requested != "timeseries" || len(body.Notices) != 1 || !strings.Contains(body.Notices[0].Message, "sort does not apply to bucketed series") {
		t.Fatalf("bucketed sort: %s", rec.Body)
	}
}

func TestPitotQLFormat(t *testing.T) {
	rec := servePitotQL(t, &fakePitotQLParser{}, &fakePanels{}, http.MethodPost, "/api/pitotql/format", `{"query":{"from":"spans","where":["service='api'"],"measures":["p95( duration_ms )"]},"panel":{"id":"a","title":"A","viz":"stat"}}`)
	var body struct {
		pitotql.Formatted
	}
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if body.Text != "spans where service:api | p95(duration)" || body.Exactness != "normalized" || !body.Editable {
		t.Fatalf("format: %s", rec.Body)
	}
	if len(body.Notices) == 0 || body.Notices[0].Message != `where[0] is shown as service:api; stored text is "service='api'"` {
		t.Fatalf("notices: %s", rec.Body)
	}
	rec = servePitotQL(t, &fakePitotQLParser{}, &fakePanels{}, http.MethodPost, "/api/pitotql/format", `{"query":{"from":"spans","measures":["count()"],"by":["a","b","c","d"]}}`)
	var failure map[string]json.RawMessage
	pitotqlResponse(t, rec, http.StatusBadRequest, &failure)
	if string(failure["code"]) != `"invalid_spec"` || failure["problems"] == nil {
		t.Fatalf("shape: %s", rec.Body)
	}
	rec = servePitotQL(t, &fakePitotQLParser{err: context.DeadlineExceeded}, &fakePanels{}, http.MethodPost, "/api/pitotql/format", `{"query":{"from":"logs","where":["coalesce(body, '') = ''"]}}`)
	failure = nil
	pitotqlResponse(t, rec, http.StatusServiceUnavailable, &failure)
	if string(failure["code"]) != `"unavailable"` || failure["problems"] != nil {
		t.Fatalf("operational: %s", rec.Body)
	}
	q := query.Spec{From: "logs"}
	for i := range 16 {
		q.Where = append(q.Where, "body = '"+strings.Repeat("x", 480)+fmt.Sprint(i)+"'")
	}
	input, err := json.Marshal(map[string]any{"query": q})
	if err != nil {
		t.Fatal(err)
	}
	rec = servePitotQL(t, &fakePitotQLParser{}, &fakePanels{}, http.MethodPost, "/api/pitotql/format", string(input))
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if body.Editable || !strings.HasPrefix(body.Reason, "formatted text is") {
		t.Fatalf("oversized character format: %s", rec.Body)
	}
	for i := range 16 {
		if !strings.Contains(body.Text, strings.Repeat("x", 480)+fmt.Sprint(i)) {
			t.Fatal("format truncated a body literal")
		}
	}
	q = query.Spec{From: "logs"}
	for i := range 16 {
		q.Where = append(q.Where, "coalesce(body, '"+strings.Repeat("𝔘", 110)+strings.Repeat("`", 30)+fmt.Sprint(i)+"') = ''")
	}
	input, err = json.Marshal(map[string]any{"query": q})
	if err != nil {
		t.Fatal(err)
	}
	rec = servePitotQL(t, &fakePitotQLParser{}, &fakePanels{}, http.MethodPost, "/api/pitotql/format", string(input))
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if body.Editable || !strings.HasPrefix(body.Reason, "formatted text is") || len(body.Text) <= 8192 {
		t.Fatalf("oversized format: %s", rec.Body)
	}
	for _, entry := range q.Where {
		if !strings.Contains(body.Text, strings.ReplaceAll(entry, "`", "``")) {
			t.Fatal("format truncated an entry")
		}
	}
}

func TestPitotQLComplete(t *testing.T) {
	rec := servePitotQL(t, nil, &fakePitotQLPanels{}, http.MethodPost, "/api/pitotql/complete", `{"text":"logs where sev","cursor":14}`)
	var body struct {
		Replace query.TextSpan `json:"replace"`
		Items   []struct {
			Label string `json:"label"`
		} `json:"items"`
		Schema struct {
			Window string `json:"window"`
			Warm   bool   `json:"warm"`
			Recent bool   `json:"recent"`
		} `json:"schema"`
		Notices []pitotql.Notice `json:"notices"`
	}
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if body.Replace != (query.TextSpan{Start: 11, End: 14}) || len(body.Items) == 0 || body.Items[0].Label != "severity" || body.Schema.Window != "24h" || body.Schema.Warm || !body.Schema.Recent {
		t.Fatalf("complete: %s", rec.Body)
	}
	rec = servePitotQL(t, nil, &fakePitotQLPanels{}, http.MethodPost, "/api/pitotql/complete", `{"text":"logs where sev","cursor":14,"window":"","namespace":"prod"}`)
	pitotqlResponse(t, rec, http.StatusOK, &body)
	if body.Schema.Window != "24h" || !body.Schema.Recent || len(body.Notices) != 0 {
		t.Fatalf("absolute completion: %s", rec.Body)
	}
}

func TestPitotQLReference(t *testing.T) {
	for _, path := range []string{"/api/pitotql/reference"} {
		rec := servePitotQL(t, nil, &fakePanels{}, http.MethodGet, path, "")
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(rec.Body.String(), "# PitotQL") || !strings.Contains(rec.Body.String(), "Preview") {
			t.Fatalf("reference: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestPitotQLRoutesAreClassified(t *testing.T) {
	for _, path := range []string{"parse", "format", "complete", "reference"} {
		method, other := http.MethodPost, http.MethodGet
		if path == "reference" {
			method, other = other, method
		}
		policy, ok := classifyRoute(method, "/api/pitotql/"+path)
		if !ok || policy.kind != routePolicyCapability || policy.capability != ReadTelemetry {
			t.Errorf("%s not telemetry:read", path)
		}
		if _, ok := classifyRoute(other, "/api/pitotql/"+path); ok {
			t.Errorf("%s %s classified", other, path)
		}
	}
	s := newTestAuthServer(t)
	RegisterPitotQLRoutes(s.e, nil, &fakePanels{})
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(http.MethodPost, "/api/pitotql/parse", strings.NewReader(`{"text":"spans"}`), nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}
	rec = servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"spans"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer: %d %s", rec.Code, rec.Body)
	}
}

func TestPitotQLBodyLimitsAndUnknownFields(t *testing.T) {
	for _, path := range []string{"parse", "format", "complete"} {
		valid := `{"text":"spans"}`
		if path == "format" {
			valid = `{"query":{"from":"spans"}}`
		}
		for _, input := range []string{`{"unknown":1}`, `{"pitotql_version":1}`, valid + strings.Repeat(" ", (512<<10)+1), `{} {}`} {
			rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/"+path, input)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s bad body: %d %s", path, rec.Code, rec.Body)
			}
		}
	}
	rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"`+strings.Repeat("a", 4001)+`"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_pitotql"`) || !strings.Contains(rec.Body.String(), "a query is at most 4000 characters (8192 bytes)") {
		t.Fatalf("interactive bound: %d %s", rec.Code, rec.Body)
	}
}

func TestPitotQLResponsesHaveNoLanguageVersion(t *testing.T) {
	for _, tc := range []struct{ path, input string }{
		{"parse", `{"text":"spans"}`},
		{"format", `{"query":{"from":"spans"}}`},
		{"complete", `{"text":"spans","cursor":5}`},
	} {
		rec := servePitotQL(t, &fakePitotQLParser{}, &fakePitotQLPanels{}, http.MethodPost, "/api/pitotql/"+tc.path, tc.input)
		var body map[string]json.RawMessage
		pitotqlResponse(t, rec, http.StatusOK, &body)
		if body["pitotql_version"] != nil || body["version"] != nil {
			t.Fatalf("%s version contract: %s", tc.path, rec.Body)
		}
		var notices []pitotql.Notice
		if raw := body["notices"]; raw != nil {
			if err := json.Unmarshal(raw, &notices); err != nil {
				t.Fatal(err)
			}
		}
		if len(notices) != 0 {
			t.Fatalf("%s unexpected notices: %+v", tc.path, notices)
		}
	}
	rec := servePitotQL(t, nil, &fakePanels{}, http.MethodGet, "/api/pitotql/reference", "")
	if rec.Code != http.StatusOK || rec.Body.String() != pitotql.Reference() || rec.Header().Get("PitotQL-Notice") != "" || rec.Header().Get("PitotQL-Version") != "" {
		t.Fatalf("reference metadata: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

func TestPitotQLCompletionAbsoluteAndLongWindowsDiscloseRecentSchema(t *testing.T) {
	for _, tt := range []struct{ window, want string }{
		// A past absolute range keeps its length; schema windows end now.
		{`"from":"2026-10-20T10:00:00Z","to":"2026-10-20T11:00:00Z"`, "1h"},
		{`"window":"7d"`, "24h"},
	} {
		rec := servePitotQL(t, nil, &fakePitotQLPanels{}, http.MethodPost, "/api/pitotql/complete", `{"text":"logs where sev","cursor":14,`+tt.window+`}`)
		var body struct {
			Schema struct {
				Window string `json:"window"`
				Recent bool   `json:"recent"`
				Notice string `json:"notice"`
			} `json:"schema"`
		}
		pitotqlResponse(t, rec, http.StatusOK, &body)
		if body.Schema.Window != tt.want || !body.Schema.Recent || body.Schema.Notice != "keys seen in the last "+tt.want+" (recent), not in the selected interval" {
			t.Fatalf("disclosure: %s", rec.Body)
		}
	}
}

func TestPitotQLCompletionRejectsBadCursorAndWindow(t *testing.T) {
	for _, body := range []string{
		`{"text":"spans","cursor":999}`,
		`{"text":"spans","cursor":-1}`,
		`{"text":"spans","cursor":5,"window":"bogus"}`,
	} {
		rec := servePitotQL(t, nil, &fakePitotQLPanels{}, http.MethodPost, "/api/pitotql/complete", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_input") {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestPitotQLFormatReportsPanelRequirements(t *testing.T) {
	for _, tt := range []struct {
		panel, query, name string
		satisfied          bool
	}{
		{`{"viz":"gauge"}`, `{"from":"spans","measures":["count()"]}`, "min_max", false},
		{`{"viz":"gauge","min":0,"max":100}`, `{"from":"spans","measures":["count()"]}`, "min_max", true},
		{`{"viz":"state_timeline"}`, `{"from":"spans","measures":["count()"],"by":["service"],"bucket":"1m"}`, "thresholds", false},
		{`{"viz":"state_timeline","thresholds":[{"value":10,"status":"warn"}]}`, `{"from":"spans","measures":["count()"],"by":["service"],"bucket":"1m"}`, "thresholds", true},
	} {
		input := `{"query":` + tt.query + `,"panel":` + tt.panel + `}`
		rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/format", input)
		var body struct {
			Requires []struct {
				Name      string `json:"name"`
				Satisfied bool   `json:"satisfied"`
			} `json:"requires"`
		}
		pitotqlResponse(t, rec, http.StatusOK, &body)
		if len(body.Requires) != 1 || body.Requires[0].Name != tt.name || body.Requires[0].Satisfied != tt.satisfied {
			t.Fatalf("requirements: %s", rec.Body)
		}
	}
}

// PitotQL problems quote the user's own text, which is often a URL path; the
// engine-error scrubber must not mistake them for filesystem paths.
func TestPitotQLParseProblemsKeepURLPaths(t *testing.T) {
	for _, tc := range []struct{ text, message, hint string }{
		{"spans where http.target:/a:b", "unexpected : after /a", `quote the value: http.target:"/a:b"`},
		{"logs where http://x:8080/y", "unexpected : after http", `quote the value: "http://x:8080/y"`},
		{"spans where url.path:/a/b:c", "unexpected : after /a/b", `quote the value: url.path:"/a/b:c"`},
	} {
		body, _ := json.Marshal(map[string]string{"text": tc.text})
		rec := servePitotQL(t, nil, &fakePanels{}, http.MethodPost, "/api/pitotql/parse", string(body))
		var got struct {
			Problems query.Problems `json:"problems"`
		}
		pitotqlResponse(t, rec, http.StatusBadRequest, &got)
		if len(got.Problems) == 0 || got.Problems[0].Message != tc.message || got.Problems[0].Hint != tc.hint {
			t.Errorf("%s: %s", tc.text, rec.Body)
		}
	}
}

type fakeCachedSchemaPanels struct{ fakePanels }

func (*fakeCachedSchemaPanels) Schema(_ context.Context, req query.SchemaRequest) (*query.Schema, error) {
	if req.Window == "7d" {
		return nil, query.Problems{{Path: "window", Message: "window must be one of 5m, 15m, 1h, 3h, 6h, 12h or 24h"}}
	}
	if !req.Cached || req.Window != "1h" || req.Namespace != "" {
		return nil, query.ErrSchemaNotCached
	}
	return &query.Schema{Window: req.Window, Signals: map[string]query.SignalSchema{
		"spans": {Attributes: []query.AttributeSchema{{Key: "http.route", Scope: "attributes"}}},
	}}, nil
}

// Parse compares keys with a schema already cached, never waiting for one.
// It offers the seen key as a fix only when that schema covers the query's
// window and namespace; otherwise it only names the key.
func TestPitotQLParseOffersSeenDottedKey(t *testing.T) {
	for _, tt := range []struct {
		scope string
		fix   bool
	}{{`,"window":"1h"`, true}, {`,"window":"15m"`, false}, {``, false}, {`,"window":"7d"`, false}, {`,"window":"1h","namespace":"shop"`, false}} {
		rec := servePitotQL(t, nil, &fakeCachedSchemaPanels{}, http.MethodPost, "/api/pitotql/parse", `{"text":"spans where http.rout:/api/cart | count()"`+tt.scope+`}`)
		var body struct {
			Notices []pitotql.Notice `json:"notices"`
		}
		pitotqlResponse(t, rec, http.StatusOK, &body)
		if len(body.Notices) != 1 || (len(body.Notices[0].FixIts) == 1) != tt.fix || !strings.Contains(body.Notices[0].Message, "did you mean http.route?") {
			t.Errorf("%s: %s", tt.scope, rec.Body)
		}
	}
}

func TestRetiredQueryRoutesAreNotRegistered(t *testing.T) {
	for _, action := range []string{"parse", "format", "complete", "reference"} {
		method := http.MethodPost
		if action == "reference" {
			method = http.MethodGet
		}
		path := "/api/fanql/" + action
		if _, ok := classifyRoute(method, path); ok {
			t.Fatalf("retired route classified: %s", path)
		}
		response := servePitotQL(t, &fakePitotQLParser{}, &fakePitotQLPanels{}, method, path, `{}`)
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired route: %d %s", response.Code, response.Body)
		}
	}
}
