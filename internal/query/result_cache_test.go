package query

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/queryrows"
	"golang.org/x/sync/semaphore"
)

// cacheEngine counts reads, and can hold them until release closes or
// fail them all.
type cacheEngine struct {
	Engine
	calls   atomic.Int64
	release chan struct{}
	fail    atomic.Bool
}

func (c *cacheEngine) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	c.calls.Add(1)
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.fail.Load() {
		return nil, errors.New("disk on fire")
	}
	return c.Engine.QueryContext(ctx, query, args...)
}

func cachedExecutor(t *testing.T) (*Executor, *cacheEngine, *atomic.Int64) {
	t.Helper()
	e := newFixtureExecutor(t)
	counting := &cacheEngine{Engine: e.engine}
	e.engine = counting
	e.SetCacheFreshness(10 * time.Second)
	var offset atomic.Int64
	base := fixtureStart.Add(time.Hour).Truncate(10 * time.Second)
	e.now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	t.Cleanup(e.Close)
	return e, counting, &offset
}

func cacheDashboard() Dashboard {
	return Dashboard{Name: "Cache", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "by_service", Title: "By service", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}}},
	}}
}

func runOne(t *testing.T, e *Executor, req RunRequest) Result {
	t.Helper()
	results, err := e.Run(t.Context(), req)
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	return results[0]
}

func TestResultCacheHitAndMiss(t *testing.T) {
	e, engine, offset := cachedExecutor(t)
	req := RunRequest{Dashboard: cacheDashboard()}
	first := runOne(t, e, req)
	if first.Status != StatusOK || first.Cached {
		t.Fatalf("first=%+v", first)
	}
	calls := engine.calls.Load()
	// Within the grain the clock rounds to the same instant: same key.
	offset.Store(int64(9 * time.Second))
	second := runOne(t, e, req)
	if !second.Cached || engine.calls.Load() != calls || second.FromMS != first.FromMS {
		t.Fatalf("second=%+v calls=%d->%d", second, calls, engine.calls.Load())
	}
	// Another width is another result.
	req.Widths = map[string]int{"by_service": 300}
	if r := runOne(t, e, req); r.Cached || engine.calls.Load() == calls {
		t.Fatalf("a different width was served from the cache: %+v", r)
	}
	// Past three grains the entry is gone.
	req.Widths = nil
	calls = engine.calls.Load()
	offset.Store(int64(40 * time.Second))
	if r := runOne(t, e, req); r.Cached || engine.calls.Load() == calls {
		t.Fatalf("an expired entry was served: %+v", r)
	}
}

func TestResultCacheQuantizesTheClock(t *testing.T) {
	e, _, offset := cachedExecutor(t)
	a := e.cacheNow()
	offset.Store(int64(9*time.Second + 999*time.Millisecond))
	if e.cacheNow() != a {
		t.Fatal("requests within one grain resolve different windows")
	}
	offset.Store(int64(10 * time.Second))
	if e.cacheNow() == a {
		t.Fatal("requests in different grains resolve the same window")
	}
	e.SetCacheFreshness(0)
	if e.cacheNow() != e.now() {
		t.Fatal("a disabled cache still rounds the clock")
	}
}

// On the next grain the last result is served stale while one background run
// replaces it, instead of every viewer waiting for a cold run at once.
func TestResultCacheServesStaleWhileRevalidating(t *testing.T) {
	e, engine, offset := cachedExecutor(t)
	req := RunRequest{Dashboard: cacheDashboard()}
	first := runOne(t, e, req)
	offset.Store(int64(15 * time.Second))
	calls := engine.calls.Load()
	r := runOne(t, e, req)
	if !r.Cached || r.ToMS != first.ToMS {
		t.Fatalf("stale entry not served: %+v", r)
	}
	e.running.Wait()
	if engine.calls.Load() == calls {
		t.Fatal("serving stale did not start a refresh")
	}
	r = runOne(t, e, req)
	if !r.Cached || r.ToMS != first.ToMS+10000 {
		t.Fatalf("refreshed entry not served: %+v (first to %d)", r, first.ToMS)
	}
}

func TestResultCacheCoalescesConcurrentRequests(t *testing.T) {
	e, engine, _ := cachedExecutor(t)
	e.SetCacheFreshness(0) // coalescing works without storing
	e.now = func() time.Time { return fixtureStart.Add(time.Hour) }
	engine.release = make(chan struct{})
	var wg sync.WaitGroup
	results := make([]Result, 5)
	for i := range results {
		// Each request decodes its own dashboard, as HTTP requests do.
		wg.Go(func() { results[i] = runOne(t, e, RunRequest{Dashboard: cacheDashboard()}) })
	}
	// Every request joins before the one run reads anything.
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.cache.mu.Lock()
		waiters := 0
		for _, f := range e.cache.flights {
			waiters += f.waiters
		}
		e.cache.mu.Unlock()
		if waiters == len(results) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(engine.release)
	wg.Wait()
	if n := engine.calls.Load(); n != 1 {
		t.Errorf("engine read %d times for five identical requests", n)
	}
	for _, r := range results {
		if r.Status != StatusOK || r.Cached {
			t.Errorf("result=%+v", r)
		}
	}
	if len(e.cache.entries) != 0 {
		t.Errorf("zero freshness stored %d entries", len(e.cache.entries))
	}
}

func TestResultCacheNeverStoresErrors(t *testing.T) {
	e, engine, _ := cachedExecutor(t)
	engine.fail.Store(true)
	req := RunRequest{Dashboard: cacheDashboard()}
	if r := runOne(t, e, req); r.Status != StatusError {
		t.Fatalf("result=%+v", r)
	}
	calls := engine.calls.Load()
	if r := runOne(t, e, req); r.Cached || engine.calls.Load() == calls {
		t.Fatalf("an error was cached: %+v", r)
	}
	if len(e.cache.entries) != 0 {
		t.Errorf("stored %d error entries", len(e.cache.entries))
	}
}

func TestResultCacheNeverStoresBusyResults(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	e.slots = semaphore.NewWeighted(1)
	if err := e.slots.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	e.batchTimeout = 30 * time.Millisecond
	r := runOne(t, e, RunRequest{Dashboard: cacheDashboard()})
	e.slots.Release(1)
	if r.Reason != ReasonBusy || len(e.cache.entries) != 0 {
		t.Fatalf("result=%+v entries=%d", r, len(e.cache.entries))
	}
}

// A run the executor cancels by closing is never stored, even when it
// produced a value.
func TestResultCacheNeverStoresCancelledRuns(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	_, _, err := loadCached(e, t.Context(), cacheKey{1}, func(run context.Context) (int, int64, bool, error) {
		go e.Close()
		<-run.Done()
		return 42, 8, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.cache.entries) != 0 {
		t.Fatal("a cancelled run was stored")
	}
}

// A departing caller does not cancel a run others still await, and its
// result is stored for the next request. A run whose every caller has left
// is cancelled and not stored.
func TestResultCacheOutlivesDepartingCallers(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	release := make(chan struct{})
	joined := make(chan struct{})
	fn := func(run context.Context) (int, int64, bool, error) {
		<-joined
		select {
		case <-release:
		case <-run.Done():
		}
		return 7, 8, true, nil
	}
	leaving, leave := context.WithCancel(t.Context())
	left := make(chan error, 1)
	go func() {
		_, _, err := loadCached(e, leaving, cacheKey{2}, fn)
		left <- err
	}()
	stayed := make(chan int, 1)
	go func() {
		for {
			e.cache.mu.Lock()
			f := e.cache.flights[cacheKey{2}]
			e.cache.mu.Unlock()
			if f != nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		v, outcome, err := loadCached(e, t.Context(), cacheKey{2}, fn)
		if err != nil || outcome != cacheCoalesced {
			t.Errorf("outcome=%s err=%v", outcome, err)
		}
		stayed <- v
	}()
	for {
		e.cache.mu.Lock()
		f := e.cache.flights[cacheKey{2}]
		waiters := 0
		if f != nil {
			waiters = f.waiters
		}
		e.cache.mu.Unlock()
		if waiters == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(joined)
	leave()
	if err := <-left; !errors.Is(err, context.Canceled) {
		t.Fatalf("departing caller err=%v", err)
	}
	close(release)
	if v := <-stayed; v != 7 {
		t.Fatalf("remaining caller got %d", v)
	}
	if v, outcome, err := loadCached(e, t.Context(), cacheKey{2}, func(context.Context) (int, int64, bool, error) {
		return 0, 0, false, errors.New("ran again")
	}); err != nil || v != 7 || outcome != cacheHit {
		t.Fatalf("v=%d outcome=%s err=%v", v, outcome, err)
	}

	alone, cancel := context.WithCancel(t.Context())
	cancelled := make(chan error, 1)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, _, _ = loadCached(e, alone, cacheKey{3}, func(run context.Context) (int, int64, bool, error) {
		<-run.Done()
		cancelled <- run.Err()
		return 1, 8, true, nil
	})
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a run kept going after its last caller left")
	}
	e.running.Wait()
	if _, ok := e.cache.entries[cacheKey{3}]; ok {
		t.Fatal("a run abandoned by every caller was stored")
	}
}

// limitBatchFrames truncates frames in place; a cached frame must survive it.
func TestResultCacheFramesAreCopiedOnUse(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	req := RunRequest{Dashboard: cacheDashboard()}
	var first Result
	if err := e.Stream(t.Context(), req, func(r Result) { first = r }); err != nil {
		t.Fatal(err)
	}
	if first.Frame == nil || first.Frame.Rows == 0 {
		t.Fatalf("first=%+v", first)
	}
	rows := first.Frame.Rows
	want := first.Frame.Values[0][0]
	first.Frame.Values[0][0] = "mutated"
	limitBatchFrames([]Result{{Frame: first.Frame}})
	first.Frame.Values[0] = first.Frame.Values[0][:0]
	first.Frame.Rows = 0
	second := runOne(t, e, req)
	if !second.Cached || second.Frame.Rows != rows || second.Frame.Values[0][0] != want {
		t.Fatalf("cached frame changed: %+v", second.Frame)
	}
}

// Every RunRequest field either feeds panelCacheKey or provably does not
// change a panel's result. Add a new field to one of the lists, and to the
// key when it changes results.
func TestPanelCacheKeyCoversRequest(t *testing.T) {
	keyed := map[string]string{
		"Dashboard": "Spec (normalized dashboard hash)",
		"Time":      "Time, Start, End",
		"Vars":      "Vars (resolved)",
		"Widths":    "Width",
		"Compare":   "Compare",
	}
	unkeyed := map[string]string{
		"Panels": "selects which panels run; a panel's result does not depend on its neighbours",
	}
	fields := reflect.TypeFor[RunRequest]()
	for i := range fields.NumField() {
		name := fields.Field(i).Name
		if keyed[name] == "" && unkeyed[name] == "" {
			t.Errorf("RunRequest.%s is new: add it to panelCacheKey if it changes a panel's result", name)
		}
	}
	// The spec hash is the dashboard's JSON: a field that JSON omits would
	// change results without changing the key.
	for _, typ := range []reflect.Type{reflect.TypeFor[Dashboard](), reflect.TypeFor[Panel](), reflect.TypeFor[Spec](), reflect.TypeFor[Time](), reflect.TypeFor[Variable]()} {
		for i := range typ.NumField() {
			field := typ.Field(i)
			if !field.IsExported() || strings.HasPrefix(field.Tag.Get("json"), "-") {
				t.Errorf("%s.%s is not in the dashboard JSON the panel cache key hashes", typ.Name(), field.Name)
			}
		}
	}
}

func TestResultCacheServesExemplars(t *testing.T) {
	e, engine, _ := cachedExecutor(t)
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", From: fixtureStart.Add(30 * time.Minute), To: fixtureStart.Add(31 * time.Minute), Dimensions: map[string]string{"attributes['http.route']": "/cart"}}
	first, err := e.Exemplars(t.Context(), req)
	if err != nil || len(first.Traces) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	calls := engine.calls.Load()
	req.Dashboard = shopDashboard()
	second, err := e.Exemplars(t.Context(), req)
	if err != nil || len(second.Traces) != 1 || engine.calls.Load() != calls {
		t.Fatalf("second=%+v err=%v calls=%d->%d", second, err, calls, engine.calls.Load())
	}
}

type fakeTraces struct {
	calls atomic.Int64
	err   error
}

func (f *fakeTraces) Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error) {
	f.calls.Add(1)
	return observability.Result[observability.TraceDetail]{Data: observability.TraceDetail{TraceID: "abc"}}, f.err
}

func TestResultCacheServesTraceDetail(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	reader := &fakeTraces{}
	traces := e.CachedTraces(reader)
	scope := observability.Scope{Namespace: "default", Start: fixtureStart, End: fixtureStart.Add(time.Hour)}
	for range 2 {
		if out, err := traces.Trace(t.Context(), scope, "abc", "", 200); err != nil || out.Data.TraceID != "abc" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("read %d times", reader.calls.Load())
	}
	if _, err := traces.Trace(t.Context(), scope, "abc", "", 100); err != nil || reader.calls.Load() != 2 {
		t.Fatalf("another limit shared the entry: err=%v calls=%d", err, reader.calls.Load())
	}
	reader.err = errors.New("boom")
	for range 2 {
		if _, err := traces.Trace(t.Context(), scope, "def", "", 200); err == nil {
			t.Fatal("error swallowed")
		}
	}
	if reader.calls.Load() != 4 {
		t.Fatalf("an error was cached: %d reads", reader.calls.Load())
	}
}
