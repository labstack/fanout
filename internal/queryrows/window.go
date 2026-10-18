package queryrows

import (
	"context"
	"time"
)

// Window is an explicit internal read scope. Only callers that apply these
// event-time predicates may use it to exclude immutable Parquet files.
type Window struct {
	Start, End         time.Time
	Namespace, Service string
	Kind               ReadKind
	// RetainedInventory ignores event time only for resource_inventory. Raw
	// relations still require the caller's explicit event-time predicates.
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

// BatchReader exposes the completed-batch accelerators to internal callers.
type BatchReader interface{ CompletedBatchReads() bool }
