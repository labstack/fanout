package query

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/metrics"
	"github.com/labstack/pitot/internal/observability"
)

// The result cache keeps recent panel, exemplar and trace-detail results so a
// dashboard refreshed by many viewers runs its queries about once per half
// TTL, and joins identical requests already running instead of queueing a
// second copy behind them. Only complete results are kept: errors, busy and
// partial answers and results of a cancelled run are never stored.
//
// No stored answer is older than the TTL. Every entry is stamped with the instant
// its data window ends: a panel run's resolved now, or for traces and
// exemplars the moment their run started. Its age is measured from that
// stamp. Younger than half the TTL it is served as is; younger than the TTL
// it is served stale while exactly one background run replaces it, so
// viewers do not all miss at once; older it is a miss. Concurrent identical
// requests share one run regardless of its age. With a zero TTL nothing is
// stored, but concurrent identical requests still share one run.
//
// A caller marked by WithoutCachedResults (Cache-Control: no-cache) reads no
// stored entry and joins only a run that started after it arrived; its fresh
// result is stored for others.

type cacheKey [32]byte

// Lookup outcomes, also the result label of pitot_query_cache_requests_total.
const (
	cacheHit       = "hit"
	cacheStale     = "stale"
	cacheMiss      = "miss"
	cacheCoalesced = "coalesced"
)

type resultCache struct {
	ttl        time.Duration
	maxEntries int
	maxBytes   int64

	mu      sync.Mutex
	lru     list.List // of *cacheEntry, most recently used first
	entries map[cacheKey]*list.Element
	bytes   int64
	flights map[cacheKey]*cacheFlight
}

type cacheEntry struct {
	key   cacheKey
	value any
	size  int64
	// stamp is where the entry's data window ends; its age counts from here.
	stamp time.Time
}

// A cacheFlight is one run that every concurrent caller of its key awaits.
// It outlives the caller that started it: a departing client does not cancel
// work others wait for. A run whose result will be stored keeps going after
// every caller has gone, bounded by its own timeout, so a slow cold panel
// still fills the cache for the next refresh instead of every refresh
// starting over. Any other run, one with the cache disabled or one only
// no-cache callers wait for, is cancelled once its last caller leaves, so
// nobody's abandoned work holds the engine. A background refresh has no
// callers and runs to completion.
type cacheFlight struct {
	done    chan struct{}
	stamp   time.Time
	value   any
	err     error
	waiters int
	// detached runs outlive their callers once started, that is once they
	// hold a read slot. One still waiting in line when its last caller
	// leaves is cancelled, so abandoned requests cannot fill the queue.
	detached bool
	// background runs are revalidations no caller started; they always
	// complete.
	background bool
	started    atomic.Bool
	cancel     context.CancelFunc
}

type flightContextKey struct{}

// markStarted records that the run ctx belongs to, if any, holds a slot.
func markStarted(ctx context.Context) {
	if flight, ok := ctx.Value(flightContextKey{}).(*cacheFlight); ok {
		flight.started.Store(true)
	}
}

// flightFunc runs one cached computation under ctx, which keeps the starting
// caller's values but ends only when every caller has left or the executor
// closes. It returns the value, its estimated size and whether the value may
// be stored.
type flightFunc[T any] func(ctx context.Context) (T, int64, bool, error)

// errRunPanicked is the error every caller of a run that panicked receives.
var errRunPanicked = errors.New("query run panicked")

type noCacheKey struct{}

// WithoutCachedResults marks ctx as a request that must not be answered from
// a stored result, as Cache-Control: no-cache asks. It may still join a run
// that started after it arrived, and its result is stored for others.
func WithoutCachedResults(ctx context.Context) context.Context {
	return context.WithValue(ctx, noCacheKey{}, true)
}

// BypassesCache reports whether ctx was marked by WithoutCachedResults.
func BypassesCache(ctx context.Context) bool {
	bypass, _ := ctx.Value(noCacheKey{}).(bool)
	return bypass
}

func newResultCache() resultCache {
	return resultCache{maxEntries: config.QueryCacheMaxEntries, maxBytes: config.QueryCacheMaxBytes,
		entries: map[cacheKey]*list.Element{}, flights: map[cacheKey]*cacheFlight{}}
}

// SetCacheTTL sets the oldest answer the result cache may serve; zero
// disables storing results. Call it before serving requests.
func (e *Executor) SetCacheTTL(d time.Duration) {
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	e.cache.ttl = max(d, 0)
	if d <= 0 {
		e.cache.lru.Init()
		clear(e.cache.entries)
		e.cache.setBytes(0)
	}
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
// concurrent caller. stamp is where the caller's data window ends, normally
// its arrival; a run fn starts is stored under it. The returned value is
// shared: callers must not mutate it. The outcome is one of the cache* lookup
// results.
func loadCached[T any](e *Executor, ctx context.Context, key cacheKey, stamp time.Time, fn flightFunc[T]) (T, string, error) {
	var zero T
	c := &e.cache
	run := func(ctx context.Context) (any, int64, bool, error) { return fn(ctx) }
	bypass := BypassesCache(ctx)
retry:
	if err := ctx.Err(); err != nil {
		return zero, cacheCoalesced, err
	}
	c.mu.Lock()
	if element := c.entries[key]; element != nil && !bypass {
		entry := element.Value.(*cacheEntry)
		age := e.now().Sub(entry.stamp)
		switch {
		case age < c.ttl/2:
			c.lru.MoveToFront(element)
			c.mu.Unlock()
			metrics.QueryCacheRequests.WithLabelValues(cacheHit).Inc()
			return entry.value.(T), cacheHit, nil
		case age < c.ttl:
			c.lru.MoveToFront(element)
			// Never duplicate work already running or waiting for admission.
			if c.flights[key] == nil {
				e.startFlight(ctx, key, stamp, true, run)
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
		flight = e.startFlight(ctx, key, stamp, false, run)
	}
	// A bypass caller must wait for older work to finish before requesting
	// a fresh run. Keep its place as a waiter without accepting that answer.
	accept := e.joinable(flight, stamp, bypass)
	flight.waiters++
	// A caller that will read the stored result keeps the run going.
	flight.detached = flight.detached || c.ttl > 0 && !bypass
	c.mu.Unlock()
	if accept {
		metrics.QueryCacheRequests.WithLabelValues(outcome).Inc()
	}
	select {
	case <-flight.done:
		if !accept {
			goto retry
		}
		if flight.err != nil {
			return zero, outcome, flight.err
		}
		return flight.value.(T), outcome, nil
	case <-ctx.Done():
		if !accept {
			metrics.QueryCacheRequests.WithLabelValues(outcome).Inc()
		}
		c.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 && (!flight.detached || !flight.started.Load() && !flight.background) {
			flight.cancel()
			// Retain the flight until fn exits, even if cancellation is slow.
		}
		c.mu.Unlock()
		return zero, outcome, ctx.Err()
	}
}

type panelCacheLookup struct {
	key cacheKey
	id  string
}

// availablePanels pins a whole refresh's existing answers or flights. It adds
// no execution work, so neither cache hits nor coalesced waiters need a ticket
// in an already full queue. Missing even one panel makes this path ineligible.
func (e *Executor) availablePanels(ctx context.Context, lookups []panelCacheLookup, stamp time.Time) ([]Result, bool, error) {
	c := &e.cache
	c.mu.Lock()
	now := e.now()
	values := make([]panelOutcome, len(lookups))
	flights := make([]*cacheFlight, len(lookups))
	outcomes := make([]string, len(lookups))
	bypass := BypassesCache(ctx)
	for i, lookup := range lookups {
		if element := c.entries[lookup.key]; element != nil && !bypass {
			entry := element.Value.(*cacheEntry)
			if c.ttl > 0 && now.Sub(entry.stamp) < c.ttl {
				var ok bool
				values[i], ok = entry.value.(panelOutcome)
				if !ok {
					c.mu.Unlock()
					return nil, false, nil
				}
				outcomes[i] = cacheHit
				if now.Sub(entry.stamp) >= c.ttl/2 {
					outcomes[i] = cacheStale
				}
				continue
			}
		}
		flight := c.flights[lookup.key]
		if flight == nil || !e.joinable(flight, stamp, bypass) {
			c.mu.Unlock()
			return nil, false, nil
		}
		flights[i], outcomes[i] = flight, cacheCoalesced
	}
	for i, lookup := range lookups {
		if flight := flights[i]; flight != nil {
			flight.waiters++
			flight.detached = flight.detached || c.ttl > 0 && !bypass
		} else {
			c.lru.MoveToFront(c.entries[lookup.key])
		}
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, flight := range flights {
			if flight == nil {
				continue
			}
			flight.waiters--
			if flight.waiters == 0 && (!flight.detached || !flight.started.Load() && !flight.background) {
				flight.cancel()
			}
		}
	}()
	results := make([]Result, len(values))
	for i, lookup := range lookups {
		metrics.QueryCacheRequests.WithLabelValues(outcomes[i]).Inc()
		if flight := flights[i]; flight != nil {
			select {
			case <-flight.done:
				if flight.err != nil {
					results[i] = failed(Result{ID: lookup.id}, flight.err, ctx.Err())
					continue
				}
				values[i] = flight.value.(panelOutcome)
			case <-ctx.Done():
				return nil, true, ctx.Err()
			}
		}
		results[i] = values[i].Result.clone()
		results[i].Cached = outcomes[i] == cacheHit || outcomes[i] == cacheStale
	}
	return results, true, nil
}

// joinable reports whether a caller may take flight's answer. Non-bypass
// callers always share in-flight work; the TTL applies only to stored results.
// A no-cache caller accepts only a run whose window ends at or after its
// arrival. c.mu is held.
func (e *Executor) joinable(flight *cacheFlight, stamp time.Time, bypass bool) bool {
	return !bypass || !flight.stamp.Before(stamp)
}

// startFlight registers the only run for key and stores its result under
// stamp. c.mu is held and no run for key may be in flight. The run keeps the
// caller's values but not its cancellation, and ends with the executor.
func (e *Executor) startFlight(caller context.Context, key cacheKey, stamp time.Time, background bool, fn flightFunc[any]) *cacheFlight {
	releaseBatch := func() {}
	if batch, ok := caller.Value(panelBatchKey{}).(*panelBatchAdmission); ok {
		releaseBatch = batch.retainFlight()
	}
	c := &e.cache
	run, cancel := context.WithCancel(context.WithoutCancel(caller))
	flight := &cacheFlight{done: make(chan struct{}), stamp: stamp, detached: background, background: background, cancel: cancel}
	run = context.WithValue(run, flightContextKey{}, flight)
	c.flights[key] = flight
	e.mu.Lock()
	if err := e.life.Err(); err != nil {
		e.mu.Unlock()
		cancel()
		delete(c.flights, key)
		flight.err = err
		releaseBatch()
		close(flight.done)
		return flight
	}
	e.running.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.running.Done()
		defer releaseBatch()
		defer cancel()
		stop := context.AfterFunc(e.life, cancel)
		defer stop()
		value, size, keep, err := runFlight(run, fn)
		// A run cut short, by its callers leaving or the executor closing,
		// is never kept.
		keep = keep && err == nil && run.Err() == nil
		c.mu.Lock()
		if c.flights[key] == flight {
			delete(c.flights, key)
		}
		if keep && c.ttl > 0 {
			c.store(key, value, size, stamp, e.now())
		}
		flight.value, flight.err = value, err
		c.mu.Unlock()
		close(flight.done)
	}()
	return flight
}

// runFlight runs fn, turning a panic into errRunPanicked: a run has no
// request goroutine, so nothing else would recover it, and its callers must
// get an answer.
func runFlight(ctx context.Context, fn flightFunc[any]) (value any, size int64, keep bool, err error) {
	defer func() {
		if v := recover(); v != nil {
			slog.Error("query run panicked", "panic", v, "stack", string(debug.Stack()))
			value, size, keep, err = nil, 0, false, fmt.Errorf("%w: %v", errRunPanicked, v)
		}
	}()
	return fn(ctx)
}

// store keeps value under key, stamped where its window ends, evicting the
// least recently used entries past either bound. c.mu is held.
func (c *resultCache) store(key cacheKey, value any, size int64, stamp, now time.Time) {
	// An answer already too old to serve is not kept.
	if c.ttl <= 0 || size > c.maxBytes || now.Sub(stamp) >= c.ttl {
		return
	}
	if element := c.entries[key]; element != nil {
		// A slower, older run never replaces a newer answer.
		if element.Value.(*cacheEntry).stamp.After(stamp) {
			return
		}
		c.remove(element)
	}
	c.entries[key] = c.lru.PushFront(&cacheEntry{key: key, value: value, size: size, stamp: stamp})
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
// entry's age, from the end of its window, decides whether it may still be
// served. Panel selection does not change a
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

// panelOutcome is a shared panel run.
type panelOutcome struct {
	Result
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
	// A trace read has no relative window: its data is as of the run's start.
	out, _, err := loadCached(c.executor, ctx, key, c.executor.now(), func(run context.Context) (observability.Result[observability.TraceDetail], int64, bool, error) {
		var out observability.Result[observability.TraceDetail]
		release, err := c.executor.admit(run)
		if err != nil {
			return out, 0, false, err
		}
		defer release()
		run, cancel := context.WithTimeout(run, traceDetailTimeout)
		defer cancel()
		out, err = c.reader.Trace(run, scope, traceID, service, limit)
		if err != nil {
			return out, 0, false, err
		}
		raw, err := json.Marshal(out)
		return out, int64(len(raw)), err == nil, nil
	})
	return out, err
}
