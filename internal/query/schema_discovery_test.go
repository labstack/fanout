package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// A cold burst for one window runs one discovery; a caller that gives up
// does not cancel it, and later callers join it rather than start another.
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
	quitter, quit := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer quit()
	if _, err := e.Schema(quitter, SchemaRequest{Window: "1h"}); !errors.Is(err, ErrSchemaBusy) {
		t.Fatalf("a caller that gave up: %v", err)
	}
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

// A caller whose deadline passes while its discovery runs is told the schema
// is busy; the discovery finishes and the next lookup is served from cache.
func TestSchemaOutlivesItsCallers(t *testing.T) {
	fixture := newFixtureExecutor(t)
	gate := make(chan struct{})
	e := NewExecutor(&gatedEngine{Engine: fixture.engine, gate: gate}, 30)
	e.now = fixture.now
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := e.Schema(ctx, SchemaRequest{Window: "1h"}); !errors.Is(err, ErrSchemaBusy) {
		t.Fatalf("caller out of time: %v", err)
	}
	close(gate)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if s, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Cached: true}); err == nil && s != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the abandoned discovery never warmed the cache")
		}
	}
}

// A discovery that overruns schemaDeadline is cancelled and frees its slot
// and entry, so the next request starts afresh.
func TestSchemaDiscoveryHasItsOwnDeadline(t *testing.T) {
	saved := schemaDeadline
	schemaDeadline = 50 * time.Millisecond
	t.Cleanup(func() { schemaDeadline = saved })
	fixture := newFixtureExecutor(t)
	engine := &activeEngine{Engine: fixture.engine, gate: make(chan struct{})}
	e := NewExecutor(engine, 30)
	e.now = fixture.now
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("overrun discovery: %v", err)
	}
	e.mu.Lock()
	_, kept := e.schemas["1h|"]
	e.mu.Unlock()
	if kept || engine.active.Load() != 0 || len(schemaSlots) != 0 {
		t.Fatalf("overrun discovery kept entry=%v active=%d slots=%d", kept, engine.active.Load(), len(schemaSlots))
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

// Distinct keys start at most two detached discoveries at once, and the rest
// wait for a slot after their callers leave. A cached key is served at once
// while both slots are busy, stale entries included, and a new key past the
// cap is refused as busy without evicting.
func TestSchemaDetachedDiscoveriesAreBounded(t *testing.T) {
	fixture := newFixtureExecutor(t)
	engine := &activeEngine{Engine: fixture.engine, gate: make(chan struct{})}
	e := NewExecutor(engine, 30)
	now := fixture.now()
	var mu sync.Mutex
	e.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	cached := &Schema{Window: "5m"}
	e.schemas["5m|"] = schemaEntry{schema: cached, expires: now.Add(time.Minute), stale: now.Add(10 * time.Minute)}
	e.schemas[namespacesKey] = schemaEntry{namespaces: []string{"shop"}, expires: now.Add(time.Hour)}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	for _, window := range []string{"15m", "1h", "3h", "6h", "12h", "24h"} {
		wg.Go(func() { _, _ = e.Schema(ctx, SchemaRequest{Window: window}) })
	}
	deadline := time.Now().Add(2 * time.Second)
	for engine.active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if peak := engine.peak.Load(); peak != 2 {
		t.Fatalf("peak concurrent discoveries %d, want 2", peak)
	}
	hit := make(chan *Schema, 1)
	go func() { s, _ := e.Schema(t.Context(), SchemaRequest{Window: "5m"}); hit <- s }()
	select {
	case s := <-hit:
		if s != cached {
			t.Fatal("cached key not served")
		}
	case <-time.After(time.Second):
		t.Fatal("cached key blocked behind busy slots")
	}
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	if s, err := e.Schema(t.Context(), SchemaRequest{Window: "5m"}); err != nil || s != cached {
		t.Fatalf("stale key not served while busy: %v", err)
	}
	// Only a table of in-flight discoveries refuses a new key.
	e.mu.Lock()
	cachedEntry := e.schemas["5m|"]
	delete(e.schemas, "5m|")
	for i := len(e.schemas); i < maxSchemaEntries; i++ {
		e.schemas[fmt.Sprintf("full%d", i)] = schemaEntry{flight: &schemaFlight{done: make(chan struct{})}}
	}
	e.mu.Unlock()
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "1h", Namespace: "shop"}); !errors.Is(err, ErrSchemaBusy) {
		t.Fatalf("new key past the cap: %v", err)
	}
	e.mu.Lock()
	for k := range e.schemas {
		if strings.HasPrefix(k, "full") {
			delete(e.schemas, k)
		}
	}
	e.schemas["5m|"] = cachedEntry
	e.mu.Unlock()
	cancel()
	wg.Wait()
	if peak := engine.peak.Load(); peak != 2 {
		t.Fatalf("peak concurrent discoveries %d after callers left, want 2", peak)
	}
	close(engine.gate)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		e.mu.Lock()
		busy, cachedKeys := 0, 0
		for _, v := range e.schemas {
			if v.flight != nil {
				busy++
			}
			if v.schema != nil {
				cachedKeys++
			}
		}
		e.mu.Unlock()
		if busy == 0 && engine.active.Load() == 0 {
			// Every abandoned discovery ran to completion and was cached.
			if cachedKeys != 7 || engine.peak.Load() != 2 {
				t.Fatalf("cached %d keys with peak %d; want 7 with peak 2", cachedKeys, engine.peak.Load())
			}
			return
		}
	}
	t.Fatal("abandoned discoveries never finished")
}

// A full table of finished entries makes room by dropping the least recently
// used one, so new keys are never locked out by cached ones.
func TestSchemaRoomEvictsLeastRecentlyUsed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	e := &Executor{schemas: map[string]schemaEntry{}, now: func() time.Time { return now }}
	for i := 0; i < maxSchemaEntries; i++ {
		e.schemas[fmt.Sprintf("k%d", i)] = schemaEntry{schema: &Schema{}, expires: now.Add(time.Minute), stale: now.Add(time.Hour), used: now.Add(time.Duration(i) * time.Second)}
	}
	if !e.makeSchemaRoom() {
		t.Fatal("finished entries refused a new key")
	}
	if _, ok := e.schemas["k0"]; ok {
		t.Fatal("least recently used entry kept")
	}
	if _, ok := e.schemas["k1"]; !ok || len(e.schemas) != maxSchemaEntries-1 {
		t.Fatalf("evicted more than one entry: %d left", len(e.schemas))
	}
}

// A cached lookup answers at once: the cached schema, or ErrSchemaNotCached
// without starting a discovery.
func TestSchemaCachedLookupNeverWaits(t *testing.T) {
	e := newFixtureExecutor(t)
	if _, err := e.Schema(t.Context(), SchemaRequest{Window: "5m", Cached: true}); !errors.Is(err, ErrSchemaNotCached) {
		t.Fatalf("cold cached lookup err=%v", err)
	}
	e.mu.Lock()
	_, started := e.schemas["5m|"]
	e.mu.Unlock()
	if started {
		t.Fatal("cached lookup started a discovery")
	}
	want, err := e.Schema(t.Context(), SchemaRequest{Window: "5m"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.Schema(t.Context(), SchemaRequest{Window: "5m", Cached: true}); err != nil || got != want {
		t.Fatalf("warm cached lookup err=%v", err)
	}
}
