package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/config"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
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
