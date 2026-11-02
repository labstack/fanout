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
	"github.com/labstack/fanout/internal/telemetry"
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
	e.SetCacheTTL(10 * time.Second)
	var offset atomic.Int64
	base := fixtureStart.Add(time.Hour)
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
	if first.Status != StatusOK || first.Cached || first.ToMS != e.now().UnixMilli() {
		t.Fatalf("first=%+v", first)
	}
	calls := engine.calls.Load()
	// Younger than half the TTL, counted from the window's end: a hit.
	offset.Store(int64(4 * time.Second))
	second := runOne(t, e, req)
	if !second.Cached || engine.calls.Load() != calls || second.ToMS != first.ToMS {
		t.Fatalf("second=%+v calls=%d->%d", second, calls, engine.calls.Load())
	}
	// Another width is another result.
	req.Widths = map[string]int{"by_service": 300}
	if r := runOne(t, e, req); r.Cached || engine.calls.Load() == calls {
		t.Fatalf("a different width was served from the cache: %+v", r)
	}
	// At the TTL the entry is gone.
	req.Widths = nil
	calls = engine.calls.Load()
	offset.Store(int64(10 * time.Second))
	if r := runOne(t, e, req); r.Cached || engine.calls.Load() == calls || r.ToMS != e.now().UnixMilli() {
		t.Fatalf("an expired entry was served: %+v", r)
	}
}

// Ages count from the end of the data window, not from when the run
// finished: a run that ended late is no fresher for it.
func TestResultCacheAgesFromTheWindowEnd(t *testing.T) {
	e, _, offset := cachedExecutor(t)
	start := e.now()
	value := 0
	load := func(stamp time.Time) (int, string) {
		t.Helper()
		v, outcome, err := loadCached(e, t.Context(), cacheKey{9}, stamp, func(context.Context) (int, int64, bool, error) {
			value++
			// The run finishes well after its window ended.
			offset.Add(int64(3 * time.Second))
			return value, 8, true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		e.running.Wait()
		return v, outcome
	}
	if v, outcome := load(start); v != 1 || outcome != cacheMiss {
		t.Fatalf("v=%d outcome=%s", v, outcome)
	}
	// Three seconds after the window's end, though the run just finished.
	if v, outcome := load(e.now()); v != 1 || outcome != cacheHit {
		t.Fatalf("v=%d outcome=%s", v, outcome)
	}
	offset.Store(int64(5 * time.Second))
	if v, outcome := load(e.now()); v != 1 || outcome != cacheStale {
		t.Fatalf("at half the TTL: v=%d outcome=%s", v, outcome)
	}
	// An answer already older than the TTL when its run ends is not kept.
	e.cache.mu.Lock()
	e.cache.store(cacheKey{10}, 1, 8, start, start.Add(10*time.Second))
	_, kept := e.cache.entries[cacheKey{10}]
	e.cache.mu.Unlock()
	if kept {
		t.Fatal("an answer older than the TTL was stored")
	}
}

// Past half the TTL the last result is served stale while exactly one
// background run replaces it, instead of every viewer waiting for a cold run
// at once.
func TestResultCacheServesStaleWhileRevalidating(t *testing.T) {
	e, engine, offset := cachedExecutor(t)
	req := RunRequest{Dashboard: cacheDashboard()}
	first := runOne(t, e, req)
	offset.Store(int64(7 * time.Second))
	calls := engine.calls.Load()
	engine.release = make(chan struct{})
	for range 3 {
		r := runOne(t, e, req)
		if !r.Cached || r.ToMS != first.ToMS {
			t.Fatalf("stale entry not served: %+v", r)
		}
	}
	e.cache.mu.Lock()
	flights := len(e.cache.flights)
	e.cache.mu.Unlock()
	if flights != 1 {
		t.Fatalf("%d refreshes for one stale entry", flights)
	}
	close(engine.release)
	e.running.Wait()
	if engine.calls.Load() != calls+1 {
		t.Fatalf("refresh read %d times", engine.calls.Load()-calls)
	}
	r := runOne(t, e, req)
	if !r.Cached || r.ToMS != first.ToMS+7000 {
		t.Fatalf("refreshed entry not served: %+v (first to %d)", r, first.ToMS)
	}
}

// A caller does not join a run whose answer is already older than the TTL
// at its arrival; it starts a fresh run, and the old run, finishing later,
// does not replace the fresh answer.
func TestResultCacheFollowersSkipOldRuns(t *testing.T) {
	e, _, offset := cachedExecutor(t)
	start := e.now()
	release := make(chan struct{})
	old := make(chan int, 1)
	go func() {
		v, _, _ := loadCached(e, t.Context(), cacheKey{4}, start, func(context.Context) (int, int64, bool, error) {
			<-release
			return 1, 8, true, nil
		})
		old <- v
	}()
	waitForFlight(t, e, cacheKey{4})
	// Nine seconds in, the run's answer may still be shared.
	offset.Store(int64(9 * time.Second))
	joined := make(chan string, 1)
	go func() {
		_, outcome, _ := loadCached(e, t.Context(), cacheKey{4}, e.now(), func(context.Context) (int, int64, bool, error) {
			return 0, 0, false, errors.New("ran again")
		})
		joined <- outcome
	}()
	waitForWaiters(t, e, cacheKey{4}, 2)
	// At the TTL it may not.
	offset.Store(int64(10 * time.Second))
	v, outcome, err := loadCached(e, t.Context(), cacheKey{4}, e.now(), func(context.Context) (int, int64, bool, error) {
		return 2, 8, true, nil
	})
	if err != nil || v != 2 || outcome != cacheMiss {
		t.Fatalf("v=%d outcome=%s err=%v", v, outcome, err)
	}
	close(release)
	if v := <-old; v != 1 {
		t.Fatalf("old run answered %d", v)
	}
	if outcome := <-joined; outcome != cacheCoalesced {
		t.Fatalf("follower within the TTL: %s", outcome)
	}
	e.running.Wait()
	if v, outcome, _ := loadCached(e, t.Context(), cacheKey{4}, e.now(), func(context.Context) (int, int64, bool, error) {
		return 0, 0, false, errors.New("ran again")
	}); v != 2 || outcome != cacheHit {
		t.Fatalf("an older run replaced a newer answer: v=%d outcome=%s", v, outcome)
	}
}

// A no-cache caller reads no stored entry and joins only a run that started
// after it arrived; its fresh answer is stored for others.
func TestResultCacheNoCacheBypassesStoredResults(t *testing.T) {
	e, engine, offset := cachedExecutor(t)
	req := RunRequest{Dashboard: cacheDashboard()}
	first := runOne(t, e, req)
	offset.Store(int64(time.Second))
	calls := engine.calls.Load()
	results, err := e.Run(WithoutCachedResults(t.Context()), req)
	if err != nil || results[0].Cached || engine.calls.Load() == calls || results[0].ToMS != first.ToMS+1000 {
		t.Fatalf("no-cache served a stored result: %+v err=%v", results, err)
	}
	if r := runOne(t, e, req); !r.Cached || r.ToMS != first.ToMS+1000 {
		t.Fatalf("the no-cache answer was not stored: %+v", r)
	}

	start := e.now()
	release := make(chan struct{})
	go func() {
		_, _, _ = loadCached(e, t.Context(), cacheKey{5}, start, func(context.Context) (int, int64, bool, error) {
			<-release
			return 1, 8, true, nil
		})
	}()
	waitForFlight(t, e, cacheKey{5})
	bypass := WithoutCachedResults(t.Context())
	// Arrived before the run started: it may share it.
	earlier := make(chan string, 1)
	go func() {
		_, outcome, _ := loadCached(e, bypass, cacheKey{5}, start.Add(-time.Millisecond), func(context.Context) (int, int64, bool, error) {
			return 0, 0, false, errors.New("ran again")
		})
		earlier <- outcome
	}()
	waitForWaiters(t, e, cacheKey{5}, 2)
	// Arrived after it started: it runs its own.
	v, outcome, err := loadCached(e, bypass, cacheKey{5}, start.Add(time.Millisecond), func(context.Context) (int, int64, bool, error) {
		return 2, 8, true, nil
	})
	if err != nil || v != 2 || outcome != cacheMiss {
		t.Fatalf("v=%d outcome=%s err=%v", v, outcome, err)
	}
	close(release)
	if outcome := <-earlier; outcome != cacheCoalesced {
		t.Fatalf("a no-cache caller did not join a newer run: %s", outcome)
	}
}

func waitForFlight(t *testing.T, e *Executor, key cacheKey) {
	t.Helper()
	waitForWaiters(t, e, key, 1)
}

func waitForWaiters(t *testing.T, e *Executor, key cacheKey, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.cache.mu.Lock()
		waiters := 0
		if f := e.cache.flights[key]; f != nil {
			waiters = f.waiters
		}
		e.cache.mu.Unlock()
		if waiters >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d callers joined", waiters, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// A run that panics answers every caller with an error instead of taking the
// process down, and leaves nothing behind.
func TestResultCacheRecoversPanickingRuns(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	release := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := loadCached(e, t.Context(), cacheKey{6}, e.now(), func(context.Context) (int, int64, bool, error) {
				<-release
				panic("boom")
			})
			errs <- err
		}()
	}
	waitForWaiters(t, e, cacheKey{6}, 2)
	close(release)
	for range 2 {
		if err := <-errs; !errors.Is(err, errRunPanicked) {
			t.Fatalf("err=%v", err)
		}
	}
	e.running.Wait()
	e.cache.mu.Lock()
	flights, entries := len(e.cache.flights), len(e.cache.entries)
	e.cache.mu.Unlock()
	if flights != 0 || entries != 0 {
		t.Fatalf("flights=%d entries=%d", flights, entries)
	}
	if v, outcome, err := loadCached(e, t.Context(), cacheKey{6}, e.now(), func(context.Context) (int, int64, bool, error) {
		return 3, 8, true, nil
	}); err != nil || v != 3 || outcome != cacheMiss {
		t.Fatalf("v=%d outcome=%s err=%v", v, outcome, err)
	}

	traces := e.CachedTraces(panickingTraces{})
	if _, err := traces.Trace(t.Context(), observability.Scope{}, "abc", "", 10); !errors.Is(err, errRunPanicked) {
		t.Fatalf("trace err=%v", err)
	}
}

type panickingTraces struct{}

func (panickingTraces) Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error) {
	panic("trace boom")
}

func TestResultCacheCoalescesConcurrentRequests(t *testing.T) {
	e, engine, _ := cachedExecutor(t)
	e.SetCacheTTL(0) // coalescing works without storing
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
	_, _, err := loadCached(e, t.Context(), cacheKey{1}, e.now(), func(run context.Context) (int, int64, bool, error) {
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
// result is stored for the next request.
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
		_, _, err := loadCached(e, leaving, cacheKey{2}, e.now(), fn)
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
		v, outcome, err := loadCached(e, t.Context(), cacheKey{2}, e.now(), fn)
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
	if v, outcome, err := loadCached(e, t.Context(), cacheKey{2}, e.now(), func(context.Context) (int, int64, bool, error) {
		return 0, 0, false, errors.New("ran again")
	}); err != nil || v != 7 || outcome != cacheHit {
		t.Fatalf("v=%d outcome=%s err=%v", v, outcome, err)
	}

}

// A run whose result will be stored keeps going after every caller has left,
// so a slow cold panel still fills the cache, and the next request reads it.
func TestResultCacheAbandonedRunsCompleteAndAreReused(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	release := make(chan struct{})
	gone, leave := context.WithCancel(t.Context())
	left := make(chan error, 1)
	go func() {
		_, _, err := loadCached(e, gone, cacheKey{3}, e.now(), func(run context.Context) (int, int64, bool, error) {
			markStarted(run) // the run holds a read slot
			select {
			case <-release:
				return 5, 8, true, nil
			case <-run.Done():
				return 0, 0, false, run.Err()
			}
		})
		left <- err
	}()
	waitForFlight(t, e, cacheKey{3})
	leave()
	if err := <-left; !errors.Is(err, context.Canceled) {
		t.Fatalf("departed caller err=%v", err)
	}
	close(release)
	e.running.Wait()
	if v, outcome, err := loadCached(e, t.Context(), cacheKey{3}, e.now(), func(context.Context) (int, int64, bool, error) {
		return 0, 0, false, errors.New("ran again")
	}); err != nil || v != 5 || outcome != cacheHit {
		t.Fatalf("v=%d outcome=%s err=%v", v, outcome, err)
	}
}

// A cacheable run abandoned while still waiting for a read slot is
// cancelled: only a started run outlives its callers, so requests that leave
// cannot fill the queue with work nobody waits for.
func TestResultCacheCancelsAbandonedQueuedRuns(t *testing.T) {
	e, _, _ := cachedExecutor(t)
	gone, leave := context.WithCancel(t.Context())
	cancelled := make(chan error, 1)
	go func() {
		_, _, _ = loadCached(e, gone, cacheKey{4}, e.now(), func(run context.Context) (int, int64, bool, error) {
			<-run.Done() // never gets a slot
			cancelled <- run.Err()
			return 0, 0, false, run.Err()
		})
	}()
	waitForFlight(t, e, cacheKey{4})
	leave()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued run ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an abandoned run kept its place in the queue")
	}
}

// A run nobody will read later, because the cache is off or only no-cache
// callers wait for it, is cancelled once its last caller leaves, and not
// stored.
func TestResultCacheCancelsAbandonedUncacheableRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ttl    time.Duration
		bypass bool
	}{{"disabled", 0, false}, {"no-cache", 10 * time.Second, true}} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := cachedExecutor(t)
			e.SetCacheTTL(tc.ttl)
			alone, cancel := context.WithCancel(t.Context())
			if tc.bypass {
				alone = WithoutCachedResults(alone)
			}
			cancelled := make(chan error, 1)
			go func() { time.Sleep(20 * time.Millisecond); cancel() }()
			_, _, _ = loadCached(e, alone, cacheKey{3}, e.now(), func(run context.Context) (int, int64, bool, error) {
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
		})
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

// subReadEngine fails or holds the reads fail and hold pick, by statement
// text and by how often that text has run.
type subReadEngine struct {
	Engine
	mu   sync.Mutex
	seen map[string]int
	fail func(text string, seen int) bool
	hold func(text string) bool
}

func (s *subReadEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[string]int{}
	}
	s.seen[text]++
	n := s.seen[text]
	s.mu.Unlock()
	if s.fail != nil && s.fail(text, n) {
		return nil, errors.New("sub-read failed")
	}
	if s.hold != nil && s.hold(text) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.Engine.QueryContext(ctx, text, args...)
}

// A result missing part of what it would show is answered but never cached,
// whichever sub-read failed, was skipped or ran out of time.
func TestResultCacheNeverStoresPartialResults(t *testing.T) {
	compare := true
	trends := shopDashboard()
	trends.Panels = trends.Panels[2:3]
	trends.Panels[0].Viz = "table"
	trends.Panels[0].Options = &Options{Columns: []ColumnFormat{{Field: "count", Format: "sparkline"}}}
	annotated := shopDashboard()
	annotated.Panels = annotated.Panels[:1]
	quiet := shopDashboard()
	quiet.Panels = []Panel{{ID: "none", Title: "None", Viz: "stat", Query: &Spec{From: "spans", Where: []string{"service = 'nowhere'"}, Measures: []string{"count()"}}}}
	for _, tc := range []struct {
		name   string
		req    RunRequest
		engine func(Engine) Engine
		check  func(Result) bool
	}{
		{"previous period", RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Compare: &compare},
			// The previous period reruns the panel's statement.
			func(e Engine) Engine {
				return &subReadEngine{Engine: e, fail: func(_ string, n int) bool { return n == 2 }}
			},
			func(r Result) bool { return r.Previous == nil }},
		{"annotations", RunRequest{Dashboard: annotated},
			func(e Engine) Engine { return &annotationQueryFailure{Engine: e} },
			func(r Result) bool { return r.AnnotationError != "" }},
		{"table trends", RunRequest{Dashboard: trends},
			func(e Engine) Engine { return failingTableTrendEngine{e} },
			func(r Result) bool { return r.Frame.Note == "Table trends unavailable." }},
		{"diagnosis timeout", RunRequest{Dashboard: quiet},
			func(e Engine) Engine {
				return &subReadEngine{Engine: e, hold: func(text string) bool { return strings.HasPrefix(text, "SELECT count(*)") }}
			},
			func(r Result) bool { return r.Diagnosis != "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := cachedExecutor(t)
			e.engine = tc.engine(e.engine)
			e.timeout = 300 * time.Millisecond
			r := runOne(t, e, tc.req)
			if r.Status != StatusOK && r.Status != StatusEmpty || !r.partial || !tc.check(r) {
				t.Fatalf("result=%+v partial=%v", r, r.partial)
			}
			if len(e.cache.entries) != 0 {
				t.Fatal("a partial result was stored")
			}
		})
	}

	t.Run("metric unit", func(t *testing.T) {
		e, db := metricFixture(t)
		e.SetCacheTTL(10 * time.Second)
		at := fixtureStart.Add(30 * time.Minute).UnixNano()
		insertMetrics(t, db, telemetry.Metric{ServiceName: "ad", Name: "jvm.memory.used", Unit: "By", Type: "gauge", TimeUnixNanos: at, Value: 1, IngestedAt: at})
		e.engine = &subReadEngine{Engine: e.engine, fail: func(text string, _ int) bool { return strings.HasPrefix(text, "SELECT DISTINCT coalesce(unit,'')") }}
		r := runMetricPanel(t, e, Panel{Viz: "stat", Query: &Spec{From: "metrics", Where: []string{"name = 'jvm.memory.used'"}, Measures: []string{"last(value)"}}}, fixtureStart, fixtureStart.Add(time.Hour))
		if r.Status != StatusOK || !r.partial || r.Frame.Columns[len(r.Frame.Columns)-1].Unit != "" {
			t.Fatalf("result=%+v partial=%v", r, r.partial)
		}
		if len(e.cache.entries) != 0 {
			t.Fatal("a result without its unit was stored")
		}
	})

	// The same panels complete are stored.
	e, _, _ := cachedExecutor(t)
	runOne(t, e, RunRequest{Dashboard: shopDashboard(), Panels: []string{"requests"}, Compare: &compare})
	if len(e.cache.entries) != 1 {
		t.Fatalf("a complete result was not stored: %d entries", len(e.cache.entries))
	}
}
