package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

type fakePanels struct {
	run     panel.RunRequest
	problem bool
	err     error
}

func (f *fakePanels) Run(_ context.Context, req panel.RunRequest) ([]panel.Result, error) {
	f.run = req
	if f.err != nil {
		return nil, f.err
	}
	if f.problem {
		return nil, panel.Problems{{Path: "panels[0].viz", Message: "unsupported viz"}}
	}
	return []panel.Result{{ID: "a", Status: panel.StatusOK}}, nil
}

func (f *fakePanels) ResolveVariables(context.Context, panel.ResolveRequest) (map[string][]panel.Option, error) {
	return map[string][]panel.Option{"service": {{Value: "checkout", Count: 3}}}, nil
}

func (f *fakePanels) Schema(_ context.Context, req panel.SchemaRequest) (*panel.Schema, error) {
	return &panel.Schema{Window: req.Window}, nil
}

func servePanels(t *testing.T, engine PanelEngine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestAuthServer(t)
	if _, err := s.users.Create("admin@example.com", "", "admin"); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	RegisterPanelRoutes(s.e, engine)
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
	rec := servePanels(t, engine, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]},"vars":{"service":"checkout","route":"$__all"},"widths":{"a":640}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if !engine.run.Vars["route"].All || engine.run.Widths["a"] != 640 {
		t.Fatalf("request = %+v", engine.run)
	}
	engine.problem = true
	rec = servePanels(t, engine, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]}}`)
	var body struct {
		Problems []panel.Problem `json:"problems"`
	}
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Problems[0].Path != "panels[0].viz" {
		t.Fatalf("problems response %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, engine, http.MethodPost, "/api/panels/query", `{"dashboard":{},"unknown":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status %d", rec.Code)
	}
}

func TestPanelQueryOperationalErrors(t *testing.T) {
	rec := servePanels(t, &fakePanels{err: context.DeadlineExceeded}, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "20 seconds") {
		t.Fatalf("deadline %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{err: context.Canceled}, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Body.Len() != 0 {
		t.Fatalf("cancelled wrote body %s", rec.Body)
	}
}

func TestVariableAndSchemaRoutes(t *testing.T) {
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/variables/resolve", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"checkout"`) {
		t.Fatalf("resolve %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{}, http.MethodGet, "/api/telemetry/schema?window=6h", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"window":"6h"`) {
		t.Fatalf("schema %d %s", rec.Code, rec.Body)
	}
}

func TestPanelRoutesAreClassified(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/panels/query"},
		{http.MethodPost, "/api/variables/resolve"},
		{http.MethodGet, "/api/telemetry/schema"},
	} {
		policy, ok := classifyRoute(tc.method, tc.path)
		if !ok || policy.kind != routePolicyCapability || policy.capability != ReadTelemetry {
			t.Errorf("%s %s = %+v %v", tc.method, tc.path, policy, ok)
		}
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/panels/query"); ok {
		t.Error("GET /api/panels/query allowed")
	}
}

type partialBatchPanels struct{ fakePanels }

func (f *partialBatchPanels) Run(ctx context.Context, _ panel.RunRequest) ([]panel.Result, error) {
	// Executor owns the batch deadline; a transport-created deadline would be
	// indistinguishable from genuine caller cancellation inside Run.
	if _, ok := ctx.Deadline(); ok {
		return nil, context.DeadlineExceeded
	}
	return []panel.Result{{ID: "fast", Status: panel.StatusOK}, {ID: "slow", Status: panel.StatusError, Error: "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard."}}, nil
}
func TestFinalFixPanelBatchHTTPKeepsPartialResults(t *testing.T) {
	rec := servePanels(t, &partialBatchPanels{}, http.MethodPost, "/api/panels/query", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard.") {
		t.Fatalf("partial batch %d %s", rec.Code, rec.Body)
	}
}
