package query

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

type gatedEngine struct {
	Engine
	queries atomic.Int32
	gate    chan struct{}
}

func (c *gatedEngine) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	c.queries.Add(1)
	<-c.gate
	return c.Engine.QueryContext(ctx, query, args...)
}

// A cold burst for one window runs one discovery; callers that give up do not
// cancel it for the others.
func TestSchemaColdBurstSharesOneDiscovery(t *testing.T) {
	fixture := newFixtureExecutor(t)
	open := make(chan struct{})
	close(open)
	single := &gatedEngine{Engine: fixture.engine, gate: open}
	e := NewExecutor(single, 30)
	e.now = fixture.now
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"}); err != nil {
		t.Fatal(err)
	}
	perDiscovery := single.queries.Load()

	gate := make(chan struct{})
	burst := &gatedEngine{Engine: fixture.engine, gate: gate}
	e = NewExecutor(burst, 30)
	e.now = fixture.now
	quitter, quit := context.WithCancel(t.Context())
	quitDone := make(chan error, 1)
	go func() { _, err := e.Schema(quitter, SchemaRequest{Window: "1h"}); quitDone <- err }()
	results := make([]*Schema, 20)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			s, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
			if err != nil {
				t.Error(err)
			}
			results[i] = s
		})
	}
	quit()
	if err := <-quitDone; err == nil {
		t.Fatal("a caller that gave up got a schema")
	}
	close(gate)
	wg.Wait()
	for _, s := range results {
		if s == nil || s != results[0] {
			t.Fatal("callers got different discoveries")
		}
	}
	if n := burst.queries.Load(); n != perDiscovery {
		t.Fatalf("burst ran %d queries; one discovery runs %d", n, perDiscovery)
	}
}

// Attribute keys carry their common values for completion, and a row without a
// service does not add an empty name to a key's service list.
func TestSchemaAttributeValuesAndServices(t *testing.T) {
	d, repo := newTestEngine(t)
	spans := shopSpans()
	n := fixtureStart.UnixNano()
	spans = append(spans, telemetry.Span{Namespace: "shop", TraceID: "anon", SpanID: "anon", Name: "GET", Kind: "SPAN_KIND_SERVER",
		StartUnixNanos: n, EndUnixNanos: n + 1e6, DurationMS: 1, StatusCode: "STATUS_CODE_OK", Attributes: map[string]any{"customer.tier": "gold"}, IngestedAt: n})
	commit(t, repo, spans, nil)
	e := NewExecutor(d, 30)
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	schema, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range schema.Signals["spans"].Attributes {
		switch a.Key {
		case "http.route":
			if len(a.Values) != 3 || a.Values[0].Value != "/api/cart" || a.Values[0].Count != 60 {
				t.Errorf("http.route values = %+v", a.Values)
			}
		case "customer.tier":
			if a.Services != "checkout, frontend" {
				t.Errorf("customer.tier services = %q", a.Services)
			}
		}
	}
}

type activeEngine struct {
	Engine
	active, peak atomic.Int32
	gate         chan struct{}
}

func (a *activeEngine) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	n := a.active.Add(1)
	defer a.active.Add(-1)
	for old := a.peak.Load(); n > old && !a.peak.CompareAndSwap(old, n); old = a.peak.Load() {
	}
	select {
	case <-a.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return a.Engine.QueryContext(ctx, query, args...)
}

// Distinct keys start at most two detached discoveries at once, and a
// discovery whose callers all left is cancelled instead of running on.
func TestSchemaDetachedDiscoveriesAreBounded(t *testing.T) {
	fixture := newFixtureExecutor(t)
	engine := &activeEngine{Engine: fixture.engine, gate: make(chan struct{})}
	e := NewExecutor(engine, 30)
	e.now = fixture.now
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { _, _ = e.Schema(ctx, SchemaRequest{Window: "1h", Namespace: string(rune('a' + i))}) })
	}
	deadline := time.Now().Add(2 * time.Second)
	for engine.active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if peak := engine.peak.Load(); peak != 2 {
		t.Fatalf("peak concurrent discoveries %d, want 2", peak)
	}
	cancel()
	wg.Wait()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		e.mu.Lock()
		n := len(e.schemas)
		e.mu.Unlock()
		if n == 0 && engine.active.Load() == 0 {
			break
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.schemas) != 0 || engine.active.Load() != 0 || engine.peak.Load() != 2 {
		t.Fatalf("abandoned discoveries kept running: entries %d active %d peak %d", len(e.schemas), engine.active.Load(), engine.peak.Load())
	}
}
