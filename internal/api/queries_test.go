package api

import (
	"context"
	"encoding/json"
	"github.com/labstack/fanout/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/query"
)

type fakePanels struct {
	run     query.RunRequest
	problem bool
	err     error
}

func (f *fakePanels) Run(_ context.Context, req query.RunRequest) ([]query.Result, error) {
	f.run = req
	if f.err != nil {
		return nil, f.err
	}
	if f.problem {
		return nil, query.Problems{{Path: "panels[0].viz", Message: "unsupported viz"}}
	}
	return []query.Result{{ID: "a", Status: query.StatusOK}}, nil
}

func (f *fakePanels) ResolveVariables(context.Context, query.ResolveRequest) (map[string][]query.Option, error) {
	return map[string][]query.Option{"service": {{Value: "checkout", Count: 3}}}, nil
}

func (f *fakePanels) Schema(_ context.Context, req query.SchemaRequest) (*query.Schema, error) {
	return &query.Schema{Window: req.Window, SQLRelations: query.SQLRelations()}, nil
}

func servePanels(t *testing.T, engine QueryEngine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterQueryRoutes(s.e, engine)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := sessionRequest(method, path, reader, s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func TestPanelQueryRoute(t *testing.T) {
	engine := &fakePanels{}
	rec := servePanels(t, engine, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]},"vars":{"service":"checkout","route":"$__all"},"widths":{"a":640}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if !engine.run.Vars["route"].All || engine.run.Widths["a"] != 640 {
		t.Fatalf("request = %+v", engine.run)
	}
	engine.problem = true
	rec = servePanels(t, engine, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	var body struct {
		Problems []query.Problem `json:"problems"`
	}
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Problems[0].Path != "panels[0].viz" {
		t.Fatalf("problems response %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, engine, http.MethodPost, "/api/queries/run", `{"dashboard":{},"unknown":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status %d", rec.Code)
	}
}

func TestPanelQueryOperationalErrors(t *testing.T) {
	rec := servePanels(t, &fakePanels{err: context.DeadlineExceeded}, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), `"code":"timeout"`) {
		t.Fatalf("deadline %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{err: context.Canceled}, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Body.Len() != 0 {
		t.Fatalf("cancelled wrote body %s", rec.Body)
	}
}

func TestPanelQueryRejectsUnknownHistogramTemporality(t *testing.T) {
	engine := &fakePanels{}
	rec := servePanels(t, engine, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"Hist","panels":[{"id":"h","title":"Hist","viz":"histogram","query":{"from":"metrics","measures":["count()"],"histogram":{"field":"value","buckets":"explicit","temporality":"delta"}}}]}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "temporality") || engine.run.Dashboard.Name != "" {
		t.Fatalf("removed option reached executor: %d %s", rec.Code, rec.Body)
	}
}

func TestVariableAndSchemaRoutes(t *testing.T) {
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/queries/variables/resolve", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"checkout"`) {
		t.Fatalf("resolve %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{}, http.MethodGet, "/api/telemetry/schema?window=6h", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"window":"6h"`) || !strings.Contains(rec.Body.String(), `"sql_relations":{`) || !strings.Contains(rec.Body.String(), `"resource_inventory":`) || !strings.Contains(rec.Body.String(), `"service_rollup":`) || !strings.Contains(rec.Body.String(), `"edge_rollup":`) || !strings.Contains(rec.Body.String(), `"sql_type":"TIMESTAMPTZ_NS"`) {
		t.Fatalf("schema %d %s", rec.Code, rec.Body)
	}
}

func TestPanelRoutesAreClassified(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/queries/run"},
		{http.MethodPost, "/api/queries/variables/resolve"},
		{http.MethodGet, "/api/telemetry/schema"},
	} {
		policy, ok := classifyRoute(tc.method, tc.path)
		if !ok || policy.kind != routePolicyCapability || policy.capability != ReadTelemetry {
			t.Errorf("%s %s = %+v %v", tc.method, tc.path, policy, ok)
		}
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/queries/run"); ok {
		t.Error("GET /api/queries/run allowed")
	}
}

type partialBatchPanels struct{ fakePanels }

func (f *partialBatchPanels) Run(ctx context.Context, _ query.RunRequest) ([]query.Result, error) {
	// Executor owns the batch deadline; a transport-created deadline would be
	// indistinguishable from genuine caller cancellation inside Run.
	if _, ok := ctx.Deadline(); ok {
		return nil, context.DeadlineExceeded
	}
	return []query.Result{{ID: "fast", Status: query.StatusOK}, {ID: "slow", Status: query.StatusError, Error: "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard."}}, nil
}
func TestPanelBatchHTTPKeepsPartialResults(t *testing.T) {
	rec := servePanels(t, &partialBatchPanels{}, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard.") {
		t.Fatalf("partial batch %d %s", rec.Code, rec.Body)
	}
}

func (f *fakePanels) Exemplars(context.Context, query.ExemplarRequest) (query.ExemplarResponse, error) {
	return query.ExemplarResponse{Traces: []query.Exemplar{}}, nil
}
func TestExemplarRoutePolicy(t *testing.T) {
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/queries/exemplars", `{"dashboard":{"name":"Inline","panels":[]},"panel_id":"latency","from":"2026-10-01T12:00:00Z","to":"2026-10-01T12:05:00Z","dimensions":{}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"traces":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	policy, ok := classifyRoute(http.MethodPost, "/api/queries/exemplars")
	if !ok || policy.capability != ReadTelemetry {
		t.Fatalf("policy: %+v %v", policy, ok)
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/queries/exemplars"); ok {
		t.Fatal("GET accepted")
	}
}
