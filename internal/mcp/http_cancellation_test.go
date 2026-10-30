package mcp

import (
	"context"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/queryrows"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type disconnectQueryEngine struct {
	query.Engine
	duck       *engine.Duck
	started    chan struct{}
	stopped    chan disconnectResult
	release    chan struct{}
	once       sync.Once
	resultType duckdb.TypeInfo
}

type disconnectResult struct{ contextErr, queryErr error }

func (e *disconnectQueryEngine) Config() duckdb.ScalarFuncConfig {
	return duckdb.ScalarFuncConfig{ResultTypeInfo: e.resultType, Volatile: true}
}

func (e *disconnectQueryEngine) Executor() duckdb.ScalarFuncExecutor {
	return duckdb.ScalarFuncExecutor{RowContextExecutor: func(ctx context.Context, _ []driver.Value) (any, error) {
		// Signal from inside native execution, not before QueryContext. The
		// channel handshake makes disconnect timing independent of scheduling.
		e.once.Do(func() { close(e.started) })
		select {
		case <-ctx.Done():
			return int64(1), nil
		case <-e.release:
			// Let failed tests tear down without waiting for the enormous range.
			return nil, errors.New("test query released")
		}
	}}
}

func (e *disconnectQueryEngine) QueryContext(ctx context.Context, _ string, _ ...any) (queryrows.Rows, error) {
	// Execute in the pinned native engine, so this proves the whole
	// HTTP -> tool -> panel -> DuckDB statement cancellation path.
	rows, err := e.duck.QueryContext(ctx, "SELECT sum(disconnect_probe()) FROM range(1000000000000)")
	if rows != nil {
		_ = rows.Close()
	}
	e.stopped <- disconnectResult{ctx.Err(), err}
	return nil, err
}

func TestHTTPDisconnectCancelsPanelDuckDBQuery(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	duck, err := engine.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	resultType, err := duckdb.NewTypeInfo(duckdb.TYPE_BIGINT)
	if err != nil {
		t.Fatal(err)
	}
	engine := &disconnectQueryEngine{Engine: duck, duck: duck, started: make(chan struct{}), stopped: make(chan disconnectResult, 1), release: make(chan struct{}), resultType: resultType}
	conn, err := duck.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = duckdb.RegisterScalarUDF(conn, "disconnect_probe", engine)
	_ = conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithIntelligence(nil, nil, query.NewExecutor(engine, 30), nil, "test")
	httpServer := httptest.NewServer(server.HTTPHandler())
	defer httpServer.Close()
	defer close(engine.release)
	client := mcp.NewClient(&mcp.Implementation{Name: "disconnect", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, &mcp.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "query_telemetry", Arguments: map[string]any{"panel": map[string]any{"id": "count", "title": "Count", "viz": "stat", "query": map[string]any{"from": "spans", "measures": []string{"count()"}}}}})
		done <- err
	}()
	select {
	case <-engine.started:
	case err := <-done:
		t.Fatalf("query never started: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("query never started")
	}
	cancel()
	select {
	case result := <-engine.stopped:
		if !errors.Is(result.contextErr, context.Canceled) || !errors.Is(result.queryErr, context.Canceled) {
			t.Fatalf("DuckDB cancellation=%+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not interrupt DuckDB")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled call succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client call did not return")
	}
	// Cancellation must release the native connection for subsequent queries.
	rows, err := duck.QueryContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
}
