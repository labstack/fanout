package queryrows

import (
	"context"
	"sync"
	"time"
)

// Window is an explicit internal read scope. Only callers that apply these
// event-time predicates may use it to exclude immutable Parquet files.
type Window struct {
	Start, End         time.Time
	Namespace, Service string
	Kind               ReadKind
	// RetainedInventory is exact retained-service membership, including uncached
	// files. It ignores event time only for resource_inventory; raw relations
	// still require the caller's explicit event-time predicates.
	RetainedInventory bool
}

type windowKey struct{}
type inventoryWindowKey struct{}

// WithInventoryWindow records the logical panel interval before file lookback
// expansion. It is independent of the raw-file read window and read kind.
func WithInventoryWindow(ctx context.Context, window Window) context.Context {
	return context.WithValue(ctx, inventoryWindowKey{}, window)
}

func ReadInventoryWindow(ctx context.Context) (Window, bool) {
	window, ok := ctx.Value(inventoryWindowKey{}).(Window)
	return window, ok && window.Start.Before(window.End)
}

func WithWindow(ctx context.Context, window Window) context.Context {
	return context.WithValue(ctx, windowKey{}, window)
}

func ReadWindow(ctx context.Context) (Window, bool) {
	window, ok := ctx.Value(windowKey{}).(Window)
	return window, ok && window.Start.Before(window.End)
}

// ReadKind identifies private aggregate relations used by trusted dashboard SQL.
type ReadKind uint8

const (
	RawRead ReadKind = iota
	TraceCandidateRead
)

// Coverage carries the oldest incomplete ingest horizon back from snapshot
// binding to the panel result. Comparisons and auxiliary reads share the value.
type Coverage struct {
	mu    sync.Mutex
	fresh time.Time
}
type coverageKey struct{}

func WithCoverage(ctx context.Context) (context.Context, *Coverage) {
	c := &Coverage{}
	return context.WithValue(ctx, coverageKey{}, c), c
}
func RecordCoverage(ctx context.Context, fresh time.Time) {
	if c, ok := ctx.Value(coverageKey{}).(*Coverage); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.fresh.IsZero() || fresh.Before(c.fresh) {
			c.fresh = fresh
		}
	}
}
func (c *Coverage) FreshTo() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.fresh }
