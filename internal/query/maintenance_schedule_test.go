package query

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestMaintenanceCompactionDrainsBacklogThenIdles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := time.Now()
		var cycles []time.Duration
		var maintenance []time.Duration
		done := make(chan struct{})
		go func() {
			defer close(done)
			runMaintenanceSchedule(ctx, time.Hour, func() { maintenance = append(maintenance, time.Since(start)) }, func() int {
				cycles = append(cycles, time.Since(start))
				if len(cycles) <= 2 {
					return 1
				}
				return 0
			})
		}()
		time.Sleep(21 * time.Second)
		synctest.Wait()
		want := []time.Duration{10 * time.Second, 10100 * time.Millisecond, 10200 * time.Millisecond, 20200 * time.Millisecond}
		if !reflect.DeepEqual(cycles, want) {
			t.Fatalf("cycles=%v want=%v", cycles, want)
		}
		t.Logf("backlogged cycle gaps: %s, %s; idle gap: %s", cycles[1]-cycles[0], cycles[2]-cycles[1], cycles[3]-cycles[2])
		time.Sleep(time.Hour - 21*time.Second)
		synctest.Wait()
		if !reflect.DeepEqual(maintenance, []time.Duration{0, time.Hour}) {
			t.Fatalf("maintenance=%v", maintenance)
		}
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("cancellation did not stop scheduler")
		}
	})
}
func TestMaintenanceShortIntervalNeedsNoCompactionTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		passes := 0
		go runMaintenanceSchedule(ctx, time.Second, func() { passes++ }, func() int { t.Error("unexpected independent compaction"); return 1 })
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if passes != 4 {
			t.Fatalf("passes=%d", passes)
		}
		cancel()
		synctest.Wait()
	})
}
