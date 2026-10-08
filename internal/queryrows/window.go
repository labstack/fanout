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
}

type windowKey struct{}

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
