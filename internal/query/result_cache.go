package query

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/observability"
)

// The result cache keeps recent panel, exemplar and trace-detail results so a
// dashboard refreshed by many viewers runs its queries once per freshness
// grain, and joins identical requests already running instead of queueing a
// second copy behind them. Only complete results are kept: errors, busy
// answers and results of a cancelled run are never stored.
//
// An entry younger than the grain is served as is. One older than the grain
// but younger than three grains is served stale while one background run
// replaces it, like the schema cache. Anything older is a miss. With a zero
// grain nothing is stored, but concurrent identical requests still share one
// run.

type cacheKey [32]byte

// Lookup outcomes, also the result label of fanout_query_cache_requests_total.
const (
	cacheHit       = "hit"
	cacheStale     = "stale"
	cacheMiss      = "miss"
	cacheCoalesced = "coalesced"
)

type resultCache struct {
	freshness  time.Duration
	maxEntries int
	maxBytes   int64

	mu      sync.Mutex
	lru     list.List // of *cacheEntry, most recently used first
	entries map[cacheKey]*list.Element
	bytes   int64
	flights map[cacheKey]*cacheFlight
}

type cacheEntry struct {
	key    cacheKey
	value  any
	size   int64
	stored time.Time
}

// A cacheFlight is one run that every concurrent caller of its key awaits.
// It outlives the caller that started it: a departing client does not cancel
// work others wait for. Once every caller has gone the run is cancelled, so
// nobody's abandoned work holds the engine; a background refresh has no
// callers and runs to completion.
type cacheFlight struct {
	done       chan struct{}
	value      any
	err        error
	waiters    int
	background bool
	cancel     context.CancelFunc
}

// flightFunc runs one cached computation under ctx, which keeps the starting
// caller's values but ends only when every caller has left or the executor
// closes. It returns the value, its estimated size and whether the value may
// be stored.
type flightFunc[T any] func(ctx context.Context) (T, int64, bool, error)

func newResultCache() resultCache {
	return resultCache{maxEntries: config.QueryCacheMaxEntries, maxBytes: config.QueryCacheMaxBytes,
		entries: map[cacheKey]*list.Element{}, flights: map[cacheKey]*cacheFlight{}}
}

// SetCacheFreshness sets the result cache grain; zero disables storing
// results. Call it before serving requests.
func (e *Executor) SetCacheFreshness(d time.Duration) {
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	e.cache.freshness = max(d, 0)
	if d <= 0 {
		e.cache.lru.Init()
		clear(e.cache.entries)
		e.cache.setBytes(0)
	}
}

// cacheNow is the executor's clock rounded down to the freshness grain, so
// identical relative-window requests within one grain resolve the same
// window and share a cache key.
func (e *Executor) cacheNow() time.Time {
	now := e.now()
	e.cache.mu.Lock()
	grain := e.cache.freshness
	e.cache.mu.Unlock()
	if grain > 0 {
		now = now.Truncate(grain)
	}
	return now
}

// keyOf hashes the canonical JSON of v. Maps marshal with sorted keys.
func keyOf(v any) (cacheKey, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return cacheKey{}, err
	}
	return sha256.Sum256(raw), nil
}

// loadCached answers key from the cache or from one run of fn shared by every
// concurrent caller. The returned value is shared: callers must not mutate
// it. The outcome is one of the cache* lookup results.
func loadCached[T any](e *Executor, ctx context.Context, key cacheKey, fn flightFunc[T]) (T, string, error) {
	var zero T
	c := &e.cache
	run := func(ctx context.Context) (any, int64, bool, error) { return fn(ctx) }
	c.mu.Lock()
	if element := c.entries[key]; element != nil {
		entry := element.Value.(*cacheEntry)
		age := e.now().Sub(entry.stored)
		switch {
		case age < c.freshness:
			c.lru.MoveToFront(element)
			c.mu.Unlock()
			metrics.QueryCacheRequests.WithLabelValues(cacheHit).Inc()
			return entry.value.(T), cacheHit, nil
		case age < 3*c.freshness:
			c.lru.MoveToFront(element)
			if c.flights[key] == nil {
				e.startFlight(ctx, key, true, run)
			}
			c.mu.Unlock()
			metrics.QueryCacheRequests.WithLabelValues(cacheStale).Inc()
			return entry.value.(T), cacheStale, nil
		default:
			c.remove(element)
		}
	}
	outcome := cacheCoalesced
	flight := c.flights[key]
	if flight == nil {
		outcome = cacheMiss
		flight = e.startFlight(ctx, key, false, run)
	}
	flight.waiters++
	c.mu.Unlock()
	metrics.QueryCacheRequests.WithLabelValues(outcome).Inc()
	select {
	case <-flight.done:
		if flight.err != nil {
			return zero, outcome, flight.err
		}
		return flight.value.(T), outcome, nil
	case <-ctx.Done():
		c.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 && !flight.background {
			flight.cancel()
			if c.flights[key] == flight {
				delete(c.flights, key)
			}
		}
		c.mu.Unlock()
		return zero, outcome, ctx.Err()
	}
}

// startFlight registers and starts a run for key. c.mu is held. The run keeps
// the caller's values but not its cancellation, and ends with the executor.
func (e *Executor) startFlight(caller context.Context, key cacheKey, background bool, fn flightFunc[any]) *cacheFlight {
	c := &e.cache
	run, cancel := context.WithCancel(context.WithoutCancel(caller))
	flight := &cacheFlight{done: make(chan struct{}), background: background, cancel: cancel}
	c.flights[key] = flight
	e.mu.Lock()
	if err := e.life.Err(); err != nil {
		e.mu.Unlock()
		cancel()
		delete(c.flights, key)
		flight.err = err
		close(flight.done)
		return flight
	}
	e.running.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.running.Done()
		defer cancel()
		stop := context.AfterFunc(e.life, cancel)
		defer stop()
		value, size, keep, err := fn(run)
		// A run cut short, by its callers leaving or the executor closing,
		// is never kept.
		keep = keep && err == nil && run.Err() == nil
		c.mu.Lock()
		if c.flights[key] == flight {
			delete(c.flights, key)
		}
		if keep {
			c.store(key, value, size, e.now)
		}
		flight.value, flight.err = value, err
		c.mu.Unlock()
		close(flight.done)
	}()
	return flight
}

// store keeps value under key, evicting the least recently used entries past
// either bound. c.mu is held.
func (c *resultCache) store(key cacheKey, value any, size int64, now func() time.Time) {
	if c.freshness <= 0 || size > c.maxBytes {
		return
	}
	if element := c.entries[key]; element != nil {
		c.remove(element)
	}
	c.entries[key] = c.lru.PushFront(&cacheEntry{key: key, value: value, size: size, stored: now()})
	c.setBytes(c.bytes + size)
	for len(c.entries) > c.maxEntries || c.bytes > c.maxBytes {
		c.remove(c.lru.Back())
	}
}

func (c *resultCache) remove(element *list.Element) {
	entry := c.lru.Remove(element).(*cacheEntry)
	delete(c.entries, entry.key)
	c.setBytes(c.bytes - entry.size)
}

func (c *resultCache) setBytes(n int64) {
	c.bytes = n
	metrics.QueryCacheBytes.Set(float64(n))
}

// panelCacheKey is everything a panel result depends on except the clock.
// The spec hash covers the normalized dashboard, so every Panel and Dashboard
// field is included; the other fields are the request's resolved inputs. An
// absolute window is in Time; a relative one slides with the clock, and the
// entry's age decides whether it is still fresh, so the result of the last
// grain is served stale while the next one runs instead of every viewer
// missing at once on each grain boundary. Panel selection does not change a
// panel's result and is left out. TestPanelCacheKeyCoversRequest fails when
// RunRequest grows a field this key does not account for.
type panelCacheKey struct {
	Spec    cacheKey         `json:"spec"`
	Panel   string           `json:"panel"`
	Time    Time             `json:"time"`
	Vars    map[string]Value `json:"vars"`
	Width   int              `json:"width"`
	Compare bool             `json:"compare"`
}

// panelOutcome is a cached panel run. busy marks a panel that never started.
type panelOutcome struct {
	Result
	busy bool
}

// clone returns r with frames the caller may mutate: limitBatchFrames
// truncates a response's frames in place, and cached frames are shared.
func (r Result) clone() Result {
	r.Frame = r.Frame.clone()
	r.Previous = r.Previous.clone()
	return r
}

func (f *Frame) clone() *Frame {
	if f == nil {
		return nil
	}
	c := *f
	c.Values = make([][]any, len(f.Values))
	for i, column := range f.Values {
		c.Values[i] = slices.Clone(column)
	}
	c.Totals = slices.Clone(f.Totals)
	if f.Trends != nil {
		c.Trends = make(map[string][][]any, len(f.Trends))
		for name, rows := range f.Trends {
			c.Trends[name] = slices.Clone(rows)
		}
	}
	if f.Health != nil {
		health := *f.Health
		c.Health = &health
	}
	return &c
}

// size estimates the memory a result holds, from its contents.
func (r Result) size() int64 {
	n := int64(256 + len(r.ID) + len(r.Error) + len(r.Diagnosis) + len(r.SQL) + len(r.AnnotationError))
	return n + r.Frame.size() + r.Previous.size()
}

func (f *Frame) size() int64 {
	if f == nil {
		return 0
	}
	n := int64(256 + len(f.Note))
	for _, c := range f.Columns {
		n += int64(64 + len(c.Name))
	}
	for _, column := range f.Values {
		n += 24 + valuesSize(column)
	}
	n += valuesSize(f.Totals)
	for name, rows := range f.Trends {
		n += int64(48 + len(name))
		for _, series := range rows {
			n += 24 + valuesSize(series)
		}
	}
	if f.Health != nil {
		n += int64(128 + 16*len(f.Health.ErrorTrend))
	}
	return n
}

func valuesSize(values []any) int64 {
	n := int64(16 * len(values))
	for _, v := range values {
		if s, ok := v.(string); ok {
			n += int64(len(s))
		}
	}
	return n
}

// TraceReader reads one trace's detail, as observability.Service does.
type TraceReader interface {
	Trace(ctx context.Context, scope observability.Scope, traceID, service string, limit int) (observability.Result[observability.TraceDetail], error)
}

// CachedTraces answers trace-detail reads through the executor's result
// cache, keyed by the trace, its scope and the span limit.
func (e *Executor) CachedTraces(reader TraceReader) TraceReader {
	return cachedTraces{executor: e, reader: reader}
}

type cachedTraces struct {
	executor *Executor
	reader   TraceReader
}

// traceDetailTimeout matches the trace HTTP handler's own bound.
const traceDetailTimeout = 20 * time.Second

func (c cachedTraces) Trace(ctx context.Context, scope observability.Scope, traceID, service string, limit int) (observability.Result[observability.TraceDetail], error) {
	key, err := keyOf(struct {
		Kind    string              `json:"kind"`
		Scope   observability.Scope `json:"scope"`
		TraceID string              `json:"trace_id"`
		Service string              `json:"service"`
		Limit   int                 `json:"limit"`
	}{"trace", scope, traceID, service, limit})
	if err != nil {
		return c.reader.Trace(ctx, scope, traceID, service, limit)
	}
	out, _, err := loadCached(c.executor, ctx, key, func(run context.Context) (observability.Result[observability.TraceDetail], int64, bool, error) {
		run, cancel := context.WithTimeout(run, traceDetailTimeout)
		defer cancel()
		out, err := c.reader.Trace(run, scope, traceID, service, limit)
		if err != nil {
			return out, 0, false, err
		}
		raw, err := json.Marshal(out)
		return out, int64(len(raw)), err == nil, nil
	})
	return out, err
}
