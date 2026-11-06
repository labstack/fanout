package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/query"
)

type fakePanels struct {
	run     query.RunRequest
	problem bool
	err     error
	bypass  bool
}

func (f *fakePanels) Run(ctx context.Context, req query.RunRequest) ([]query.Result, error) {
	f.run = req
	f.bypass = query.BypassesCache(ctx)
	if f.err != nil {
		return nil, f.err
	}
	if f.problem {
		return nil, query.Problems{{Path: "panels[0].viz", Message: "unsupported viz"}}
	}
	return []query.Result{{ID: "a", Status: query.StatusOK}}, nil
}

func (f *fakePanels) Stream(ctx context.Context, req query.RunRequest, emit func(query.Result)) error {
	results, err := f.Run(ctx, req)
	for _, r := range results {
		emit(r)
	}
	return err
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

func TestPanelQueryRouteStreamsWhenAsked(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	engine := &fakePanels{}
	RegisterQueryRoutes(s.e, engine)
	post := func(body string) *httptest.ResponseRecorder {
		req := sessionRequest(http.MethodPost, "/api/queries/run", strings.NewReader(body), s.login(t, viewer))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/x-ndjson")
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	rec := post(`{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" || rec.Body.String() != `{"id":"a","status":"ok","elapsed_ms":0,"from_ms":0,"to_ms":0}`+"\n" || !rec.Flushed {
		t.Fatalf("stream %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	// Chrome cancels a fully read no-store fetch body when its end arrives
	// after the last chunk, reporting the stream as aborted; a POST response is
	// not cached anyway, so the stream only keeps proxies from transforming it.
	if got := rec.Header().Get("Cache-Control"); got != "no-cache, no-transform" {
		t.Fatalf("stream cache control %q", got)
	}
	// Spec problems come before any result, so they keep their status.
	engine.problem = true
	if rec := post(`{"dashboard":{"name":"x","panels":[]}}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "panels[0].viz") {
		t.Fatalf("stream problems %d %s", rec.Code, rec.Body)
	}
}

func TestPanelQueryOperationalErrors(t *testing.T) {
	rec := servePanels(t, &fakePanels{err: context.DeadlineExceeded}, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), `"code":"timeout"`) || !strings.Contains(rec.Body.String(), query.BatchTimeoutError) {
		t.Fatalf("deadline %d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{err: context.Canceled}, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Body.Len() != 0 {
		t.Fatalf("cancelled wrote body %s", rec.Body)
	}
}

// Every panel turned away at the queue bound is one retryable 503, streamed
// or not, like the schema's busy answer.
func TestPanelQueryBusyIs503(t *testing.T) {
	for _, accept := range []string{"application/json", "application/x-ndjson"} {
		s := newTestAuthServer(t)
		viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
		if err != nil {
			t.Fatal(err)
		}
		RegisterQueryRoutes(s.e, &fakePanels{err: query.ErrBusy})
		req := sessionRequest(http.MethodPost, "/api/queries/run", strings.NewReader(`{"dashboard":{"name":"x","panels":[]}}`), s.login(t, viewer))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "2" || !strings.Contains(rec.Body.String(), `"code":"busy"`) {
			t.Fatalf("%s: %d %q %s", accept, rec.Code, rec.Header().Get("Retry-After"), rec.Body)
		}
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
	return []query.Result{{ID: "fast", Status: query.StatusOK}, {ID: "slow", Status: query.StatusError, Error: query.BatchDeadlineError, Reason: query.ReasonBusy}}, nil
}
func TestPanelBatchHTTPKeepsPartialResults(t *testing.T) {
	rec := servePanels(t, &partialBatchPanels{}, http.MethodPost, "/api/queries/run", `{"dashboard":{"name":"x","panels":[]}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reason":"busy"`) {
		t.Fatalf("partial batch %d %s", rec.Code, rec.Body)
	}
}

func (f *fakePanels) Exemplars(ctx context.Context, _ query.ExemplarRequest) (query.ExemplarResponse, error) {
	f.bypass = query.BypassesCache(ctx)
	return query.ExemplarResponse{Traces: []query.Exemplar{}}, f.err
}
func TestExemplarRoutePolicy(t *testing.T) {
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/queries/exemplars", `{"dashboard":{"name":"Inline","panels":[]},"panel_id":"latency","from":"2026-10-01T12:00:00Z","to":"2026-10-01T12:05:00Z","dimensions":{}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"traces":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = servePanels(t, &fakePanels{err: query.ErrBusy}, http.MethodPost, "/api/queries/exemplars", `{"dashboard":{"name":"Inline","panels":[]},"panel_id":"latency","from":"2026-10-01T12:00:00Z","to":"2026-10-01T12:05:00Z"}`)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("busy exemplars: %d %s", rec.Code, rec.Body)
	}
	policy, ok := classifyRoute(http.MethodPost, "/api/queries/exemplars")
	if !ok || policy.capability != ReadTelemetry {
		t.Fatalf("policy: %+v %v", policy, ok)
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/queries/exemplars"); ok {
		t.Fatal("GET accepted")
	}
}

type stalledWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *stalledWriter) Write([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }

func (w *stalledWriter) SetWriteDeadline(t time.Time) error {
	w.deadlines = append(w.deadlines, t)
	return nil
}

type cancelWatch struct {
	fakePanels
	cancelled bool
}

func (f *cancelWatch) Stream(ctx context.Context, _ query.RunRequest, emit func(query.Result)) error {
	emit(query.Result{ID: "a", Status: query.StatusOK})
	f.cancelled = ctx.Err() != nil
	return ctx.Err()
}

// A client that stops reading must not hold a stream open: each write has a
// deadline, and a failed write cancels the request so its panels stop.
func TestPanelStreamWriteDeadline(t *testing.T) {
	s := newTestAuthServer(t)
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	engine := &cancelWatch{}
	RegisterQueryRoutes(s.e, engine)
	req := sessionRequest(http.MethodPost, "/api/queries/run", strings.NewReader(`{"dashboard":{"name":"x","panels":[]}}`), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	w := &stalledWriter{ResponseRecorder: httptest.NewRecorder()}
	s.e.ServeHTTP(w, req)
	if len(w.deadlines) == 0 || w.deadlines[0].IsZero() {
		t.Fatalf("write deadlines = %v", w.deadlines)
	}
	if !engine.cancelled {
		t.Fatal("a failed write did not cancel the stream")
	}
}

// Cache-Control: no-cache, in any case and among other directives, skips
// stored query results on every cached route; nothing else does.
func TestNoCacheSkipsStoredResults(t *testing.T) {
	for _, tc := range []struct {
		header []string
		want   bool
	}{
		{nil, false},
		{[]string{"no-cache"}, true},
		{[]string{"max-age=0, No-Cache"}, true},
		{[]string{"max-age=0", "no-cache"}, true},
		{[]string{"no-store"}, false},
		{[]string{"no-cache-please"}, false},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/queries/run", nil)
		for _, value := range tc.header {
			req.Header.Add("Cache-Control", value)
		}
		c := echo.New().NewContext(req, httptest.NewRecorder())
		if got := query.BypassesCache(resultContext(c)); got != tc.want {
			t.Errorf("Cache-Control %q: bypass=%v", tc.header, got)
		}
	}

	s := newTestAuthServer(t)
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	panels := &fakePanels{}
	traces := &traceProbe{}
	RegisterQueryRoutes(s.e, panels)
	RegisterTraceRoutes(s.e, traces, 7)
	cookie := s.login(t, viewer)
	for _, stream := range []bool{false, true} {
		req := sessionRequest(http.MethodPost, "/api/queries/run", strings.NewReader(`{"dashboard":{"name":"x","panels":[]}}`), cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cache-Control", "no-cache")
		if stream {
			req.Header.Set("Accept", streamType)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !panels.bypass {
			t.Fatalf("stream=%v: %d bypass=%v", stream, rec.Code, panels.bypass)
		}
	}
	panels.bypass = false
	req := sessionRequest(http.MethodPost, "/api/queries/exemplars", strings.NewReader(`{"dashboard":{"name":"Inline","panels":[]},"panel_id":"latency","from":"2026-10-01T12:00:00Z","to":"2026-10-01T12:05:00Z"}`), cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !panels.bypass {
		t.Fatalf("exemplars: %d bypass=%v", rec.Code, panels.bypass)
	}
	req = sessionRequest(http.MethodGet, "/api/traces/abc?namespace=shop&from=2026-10-01T12:00:00Z&to=2026-10-01T13:00:00Z", nil, cookie)
	req.Header.Set("Cache-Control", "no-cache")
	rec = httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !traces.bypass {
		t.Fatalf("trace: %d bypass=%v", rec.Code, traces.bypass)
	}
}

func TestPanelQueryIsolatesInvalidPanels(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	executor := query.NewExecutor(duck, 30)
	t.Cleanup(func() { executor.Close(); _ = duck.Close(); _ = repo.Close() })
	s := newTestAuthServer(t)
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterQueryRoutes(s.e, executor)
	spec := query.Dashboard{Name: "Stale", Panels: []query.Panel{
		{ID: "stale", Title: "Stale", Viz: "bar", Query: &query.Spec{From: "spans", Measures: []string{"count()"}, By: []string{"http_route"}}},
		{ID: "valid", Title: "Valid", Viz: "stat", Query: &query.Spec{From: "spans", Measures: []string{"count()"}}},
	}}
	for _, global := range []bool{false, true} {
		if global {
			spec.Variables = []query.Variable{{Name: "route", Kind: "query", From: "spans", Field: "http_route"}}
		}
		raw, err := json.Marshal(query.RunRequest{Dashboard: spec})
		if err != nil {
			t.Fatal(err)
		}
		for _, accept := range []string{"application/json", streamType} {
			req := sessionRequest(http.MethodPost, "/api/queries/run", strings.NewReader(string(raw)), s.login(t, viewer))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", accept)
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			if global {
				if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "variables[0].field") {
					t.Fatalf("global: %d %s", rec.Code, rec.Body)
				}
				continue
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			var results []query.Result
			if accept == streamType {
				decoder := json.NewDecoder(rec.Body)
				for decoder.More() {
					var result query.Result
					if err := decoder.Decode(&result); err != nil {
						t.Fatal(err)
					}
					results = append(results, result)
				}
			} else {
				var body struct {
					Results []query.Result `json:"results"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				results = body.Results
				if len(results) != 2 || results[0].ID != "stale" {
					t.Fatalf("order: %+v", results)
				}
			}
			got := map[string]query.Result{}
			for _, r := range results {
				got[r.ID] = r
			}
			bad := got["stale"]
			if len(results) != 2 || bad.Status != query.StatusInvalid || len(bad.Problems) == 0 || !strings.Contains(bad.Problems[0].Message, "http_route") || got["valid"].Status != query.StatusOK {
				t.Fatalf("results: %+v", results)
			}
		}
	}
	spec.Variables = nil
	service := dashboard.New(s.db.DB, executor)
	_, err = service.Create(t.Context(), viewer.ID, spec, dashboard.Author{Kind: "user", ID: viewer.ID})
	var problems query.Problems
	if !errors.As(err, &problems) || !strings.Contains(err.Error(), "http_route") {
		t.Fatalf("save = %v", err)
	}
}

func TestAuxiliaryQueryRoutesIgnoreInvalidSiblings(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	executor := query.NewExecutor(duck, 30)
	t.Cleanup(func() { executor.Close(); _ = duck.Close(); _ = repo.Close() })
	from := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	at := from.Add(time.Minute).UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "auxiliary", Spans: []telemetry.Span{{ServiceName: "checkout", TraceID: "trace", SpanID: "root", Name: "GET /", StartUnixNanos: at, EndUnixNanos: at + 1000000, DurationMS: 1}}}); err != nil {
		t.Fatal(err)
	}
	spec := query.Dashboard{Name: "Stale sibling", Time: query.Time{From: &from, To: &to}, Variables: []query.Variable{{Name: "service", Kind: "query", From: "spans", Field: "service", Default: "checkout"}}, Panels: []query.Panel{
		{ID: "healthy", Title: "Healthy", Viz: "stat", Query: &query.Spec{From: "spans", Measures: []string{"count()"}, Where: []string{"service = $service"}}},
		{ID: "stale", Title: "Stale", Viz: "bar", Query: &query.Spec{From: "spans", Measures: []string{"count()"}, By: []string{"http_route"}}},
	}}
	post := func(path string, input any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return servePanels(t, executor, http.MethodPost, path, string(raw))
	}
	rec := post("/api/queries/variables/resolve", query.ResolveRequest{Dashboard: spec})
	var options struct {
		Options map[string][]query.Option `json:"options"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &options) != nil || len(options.Options["service"]) != 1 || options.Options["service"][0].Value != "checkout" {
		t.Fatalf("variables: %d %s", rec.Code, rec.Body)
	}
	req := query.ExemplarRequest{Dashboard: spec, PanelID: "healthy", From: from, To: to}
	rec = post("/api/queries/exemplars", req)
	var exemplars query.ExemplarResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &exemplars) != nil || len(exemplars.Traces) != 1 {
		t.Fatalf("healthy exemplars: %d %s", rec.Code, rec.Body)
	}
	req.PanelID = "stale"
	rec = post("/api/queries/exemplars", req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "panels[1].query.by[0]") {
		t.Fatalf("invalid selected panel: %d %s", rec.Code, rec.Body)
	}
	spec.Variables[0].Where = []string{"http_route = '/cart'"}
	spec.Panels[1] = query.Panel{ID: "stale", Title: "Stale", Viz: "unknown"}
	for _, path := range []string{"/api/queries/variables/resolve", "/api/queries/exemplars", "/api/queries/run"} {
		var input any = query.RunRequest{Dashboard: spec}
		if path == "/api/queries/variables/resolve" {
			input = query.ResolveRequest{Dashboard: spec}
		}
		if path == "/api/queries/exemplars" {
			input = query.ExemplarRequest{Dashboard: spec, PanelID: "healthy", From: from, To: to}
		}
		rec = post(path, input)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "variables[0].where[0]") || !strings.Contains(rec.Body.String(), "panels[1].viz") {
			t.Fatalf("global failure %s: %d %s", path, rec.Code, rec.Body)
		}
	}
}
