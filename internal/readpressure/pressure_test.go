package readpressure

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestYieldBoundAndRecovery(t *testing.T) {
	for _, n := range []int{0, 1, 2, 10} {
		checks, waits := 0, 0
		err := yield(t.Context(), func() bool { checks++; return checks <= n }, func(_ context.Context, d time.Duration) error {
			if d != 250*time.Millisecond {
				t.Fatal(d)
			}
			waits++
			return nil
		})
		if err != nil || waits != min(n, 4) {
			t.Fatalf("pressure=%d waits=%d err=%v", n, waits, err)
		}
	}
}
func TestYieldCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := &Signal{}
	s.Set(func() bool { return true })
	if err := Yield(WithSignal(ctx, s)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
