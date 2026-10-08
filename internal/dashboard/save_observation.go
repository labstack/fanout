package dashboard

import (
	"context"
	"sync/atomic"
)

type saveObservationKey struct{}

// TrackSave starts a per-call observation, shared with nested service work.
// It is never serialized or accepted as client-supplied metadata.
func TrackSave(ctx context.Context) context.Context {
	return context.WithValue(ctx, saveObservationKey{}, new(atomic.Bool))
}

// MarkSaveCommitted records a confirmed database commit or successful remote
// mutation result before any subsequent receipt/encoding work can panic.
func MarkSaveCommitted(ctx context.Context) {
	if committed, ok := ctx.Value(saveObservationKey{}).(*atomic.Bool); ok {
		committed.Store(true)
	}
}

func SaveCommitted(ctx context.Context) bool {
	committed, ok := ctx.Value(saveObservationKey{}).(*atomic.Bool)
	return ok && committed.Load()
}
