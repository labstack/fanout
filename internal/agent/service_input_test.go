package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	controlstore "github.com/labstack/fanout/internal/store"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/labstack/fanout/internal/toolerror"
)

func TestToolRegistryUnknownServiceBoundsOversizedAliases(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	batch := telemetrystore.Batch{ID: "oversized-aliases"}
	for i := range 30 {
		batch.Spans = append(batch.Spans, telemetry.Span{
			Namespace: "shop", ServiceName: fmt.Sprintf("user%02d-", i) + strings.Repeat("<\\\"\n界", 512),
			TraceID: fmt.Sprint(i), SpanID: fmt.Sprint(i),
			StartUnixNanos: from.Add(time.Minute).UnixNano(), EndUnixNanos: from.Add(2 * time.Minute).UnixNano(),
			Attributes: map[string]any{"messaging.system": "kafka"},
		})
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(duck, 7)
	server := fanoutmcp.NewWithIntelligence(observability.New(duck, duck, 7), nil, executor, nil, "test")
	registry, err := NewToolRegistry(t.Context(), server.MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	// Keep the service filter executable; the alias list above supplies the
	// oversized guidance, and this name also exercises JSON/UTF-8 escaping.
	for _, service := range []string{"kafka", strings.Repeat("<\\\"\n界", 64)} {
		input, err := json.Marshal(map[string]any{"service": service, "namespace": "shop", "from": from, "to": to})
		if err != nil {
			t.Fatal(err)
		}
		result, err := registry.Execute(t.Context(), ToolCall{Name: "get_service_performance", Input: string(input)})
		if err != nil || !result.IsError || result.Structured != nil {
			t.Fatalf("unknown service result: %+v %v", result, err)
		}
		var failure toolerror.Result
		if len(result.Content) > 16*1024 || !utf8.ValidString(result.Content) || json.Unmarshal([]byte(result.Content), &failure) != nil || failure.Error == nil || failure.Error.Code != "unknown_service" || failure.Error.Message == "" {
			t.Fatalf("invalid error contract: bytes=%d valid_json=%v", len(result.Content), json.Valid([]byte(result.Content)))
		}
		if service == "kafka" && !strings.Contains(failure.Error.Message, "kafka is a messaging system used by user00-") {
			t.Fatalf("alias guidance missing: %q", failure.Error.Message)
		}
	}
}

func TestToolRegistryNoDataAndDashboardPanelErrors(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "signals", Metrics: []telemetry.Metric{{ServiceName: "collector", Name: "load", Type: "gauge", Value: 1, TimeUnixNanos: from.Add(time.Minute).UnixNano()}}, Logs: []telemetry.Log{{ServiceName: "logger", Severity: "INFO", Body: "hello", TimeUnixNanos: from.Add(time.Minute).UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	db, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test')`); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(duck, 7)
	server := fanoutmcp.NewWithIntelligence(observability.New(duck, duck, 7), dashboard.New(db.DB, executor), executor, nil, "test")
	registry, err := NewToolRegistry(t.Context(), server.MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	spec := panel.Dashboard{Name: "Empty", Time: panel.Time{From: &from, To: &to}, Panels: []panel.Panel{{ID: "p", Title: "Collector requests", Viz: "stat", Query: &panel.Query{From: "spans", Where: []string{"service = 'collector'"}, Measures: []string{"count()"}}}}}
	for _, tc := range []struct {
		name, code, want string
		args             any
	}{
		{"get_service_performance", "no_data", "spans=0, logs=0, metrics=1", map[string]any{"service": "collector", "from": from, "to": to}},
		{"search_logs", "no_data", "INFO (1)", map[string]any{"service": "logger", "severity": "ERROR", "from": from, "to": to}},
		{"create_dashboard", "invalid_spec", "panels[0]: Collector requests", map[string]any{"dashboard": spec}},
	} {
		input, _ := json.Marshal(tc.args)
		result, err := registry.Execute(dashboard.WithOwner(t.Context(), "owner"), ToolCall{Name: tc.name, Input: string(input)})
		var failure toolerror.Result
		if err != nil || !result.IsError || (result.Structured != nil) != (tc.name == "create_dashboard") || json.Unmarshal([]byte(result.Content), &failure) != nil || failure.Error == nil || failure.Error.Code != tc.code || !strings.Contains(failure.Error.Message, tc.want) {
			t.Fatalf("%s: %+v %v", tc.name, result, err)
		}
	}
}
