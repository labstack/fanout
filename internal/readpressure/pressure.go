// Package readpressure lets background work yield bounded time to queued reads.
package readpressure

import (
	"context"
	"sync/atomic"
	"time"
)

type Signal struct{ check atomic.Pointer[func() bool] }

func (s *Signal) Set(check func() bool)           { s.check.Store(&check) }
func (s *Signal) Pressured() bool                 { p := s.check.Load(); return p != nil && *p != nil && (*p)() }
func (s *Signal) Yield(ctx context.Context) error { return yield(ctx, s.Pressured, pause) }
func pause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Four quarter-second pauses cap each chunk's delay without skipping its work.
func yield(ctx context.Context, pressure func() bool, wait func(context.Context, time.Duration) error) error {
	for i := 0; i < 4 && pressure(); i++ {
		if err := wait(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type key struct{}

func WithSignal(ctx context.Context, s *Signal) context.Context {
	return context.WithValue(ctx, key{}, s)
}
func Yield(ctx context.Context) error {
	if s, ok := ctx.Value(key{}).(*Signal); ok {
		return s.Yield(ctx)
	}
	return ctx.Err()
}
