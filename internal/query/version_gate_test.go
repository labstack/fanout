package query

import (
	"bytes"
	"context"
	"errors"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVersionRollupBudgetStartsAfterGate(t *testing.T) {
	d, repo := versionEngine(t)
	commitVersionLogs(t, repo, "healthy", time.Now().UTC().Add(-time.Hour), 1)
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	timer := time.AfterFunc(10100*time.Millisecond, unlock)
	defer func() {
		if timer.Stop() {
			unlock()
		}
	}()
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 1 {
		t.Fatalf("gate wait consumed pass budget: %d %v", n, err)
	}
	if len(d.versionRollupFailures) != 0 {
		t.Fatalf("gate wait backoff: %+v", d.versionRollupFailures)
	}
}
func TestGateTimeoutDoesNotBackOff(t *testing.T) {
	d, repo := versionEngine(t)
	commitVersionLogs(t, repo, "healthy", time.Now().UTC().Add(-time.Hour), 1)
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := d.RefreshVersionRollup(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(d.versionRollupFailures) != 0 {
		t.Fatalf("gate-wait failure penalized batch: %+v", d.versionRollupFailures)
	}
}

func TestVersionRollupWaitIsBoundedWithoutCallerDeadline(t *testing.T) {
	for _, gate := range []string{"write", "parquet"} {
		t.Run(gate, func(t *testing.T) {
			d, repo := versionEngine(t)
			commitVersionLogs(t, repo, "healthy", time.Now().UTC().Add(-time.Hour), 1)
			acquired, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				if gate == "write" {
					unlock := d.writeGate.Lock(writegate.WriteRollupService)
					close(acquired)
					<-release
					unlock()
				} else {
					if err := d.parquetMu.LockContext(context.Background()); err != nil {
						panic(err)
					}
					close(acquired)
					<-release
					d.parquetMu.Unlock()
				}
				close(released)
			}()
			<-acquired
			// A watchdog releases the test fixture even if the implementation has no bound.
			finished := make(chan struct{})
			go func() {
				select {
				case <-finished:
				case <-time.After(time.Second):
				}
				close(release)
			}()
			counter := metrics.RollupComponentTotal.WithLabelValues("version", "error")
			before := testutil.ToFloat64(counter)
			var log bytes.Buffer
			logger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
			defer slog.SetDefault(logger)
			start := time.Now()
			_, err := d.refreshVersionRollup(context.Background(), 20*time.Millisecond)
			elapsed := time.Since(start)
			close(finished)
			<-released
			if !errors.Is(err, context.DeadlineExceeded) || elapsed > 500*time.Millisecond {
				t.Fatalf("unbounded %s wait: %v after %s", gate, err, elapsed)
			}
			if gate == "parquet" && !errors.Is(err, ErrParquetReadWait) {
				t.Fatalf("timeout never reached parquet gate: %v", err)
			}
			if after := testutil.ToFloat64(counter); after != before+1 {
				t.Fatalf("wait-budget error counter=%v want %v", after, before+1)
			}
			if output := log.String(); !strings.Contains(output, "level=WARN") || !strings.Contains(output, "version rollup pass timed out") {
				t.Fatalf("missing wait-budget timeout warning: %s", output)
			}
			if len(d.versionRollupFailures) != 0 {
				t.Fatalf("wait penalized batches: %+v", d.versionRollupFailures)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			// Taking both locks after the failed second acquisition proves the first was released.
			unlock, err := d.writeGate.LockContext(ctx, writegate.WriteRollupVersion)
			if err != nil {
				t.Fatalf("leaked write lock: %v", err)
			}
			defer unlock()
			if err := d.parquetMu.LockContext(ctx); err != nil {
				t.Fatalf("leaked parquet lock: %v", err)
			}
			d.parquetMu.Unlock()
		})
	}
}

func TestVersionRollupWaitHonorsCallerCancellation(t *testing.T) {
	for _, gate := range []string{"write", "parquet"} {
		t.Run(gate, func(t *testing.T) {
			d, _ := versionEngine(t)
			acquired, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				if gate == "write" {
					unlock := d.writeGate.Lock(writegate.WriteRollupService)
					close(acquired)
					<-release
					unlock()
				} else {
					if err := d.parquetMu.LockContext(context.Background()); err != nil {
						panic(err)
					}
					close(acquired)
					<-release
					d.parquetMu.Unlock()
				}
				close(released)
			}()
			<-acquired
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var releaseOnce sync.Once
			releaseFixture := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseFixture()
			watchdog := time.AfterFunc(2*time.Second, func() { cancel(); releaseFixture() })
			defer watchdog.Stop()
			finished := make(chan error, 1)
			go func() {
				_, err := d.refreshVersionRollup(ctx, time.Second)
				finished <- err
			}()
			blocked := make(chan struct{})
			stop := make(chan struct{})
			defer close(stop)
			go signalVersionGateWait(gate, blocked, stop)
			select {
			case <-blocked:
				cancel()
			case err := <-finished:
				t.Fatalf("pass returned before observed %s wait: %v", gate, err)
			case <-time.After(2 * time.Second):
				t.Fatalf("watchdog: pass never blocked on %s gate", gate)
			}
			var err error
			select {
			case err = <-finished:
			case <-time.After(2 * time.Second):
				t.Fatalf("watchdog: %s gate ignored caller cancellation", gate)
			}
			releaseFixture()
			<-released
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation: %v", err)
			}
			if gate == "parquet" && !errors.Is(err, ErrParquetReadWait) {
				t.Fatalf("cancellation never reached parquet gate: %v", err)
			}
			if len(d.versionRollupFailures) != 0 {
				t.Fatal("cancellation penalized a batch")
			}
			ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := d.RefreshVersionRollup(ctx); err != nil {
				t.Fatalf("lock leaked after cancellation: %v", err)
			}
		})
	}
}

// Observe a parked select, rather than guessing when a goroutine has reached
// a gate. The parquet stack also proves the free write gate was acquired.
// This test-only probe avoids adding instrumentation to the production gates.
func signalVersionGateWait(gate string, blocked chan<- struct{}, stop <-chan struct{}) {
	frame := "writegate.(*WriteGate).LockContext"
	if gate == "parquet" {
		frame = "query.(*parquetReadGate).RLockContext"
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	buffer := make([]byte, 1<<20)
	for {
		for stack := range strings.SplitSeq(string(buffer[:runtime.Stack(buffer, true)]), "\n\n") {
			if strings.Contains(stack, "[select]") && strings.Contains(stack, frame) && strings.Contains(stack, "query.(*Duck).refreshVersionRollup") && strings.Contains(stack, "TestVersionRollupWaitHonorsCallerCancellation") {
				close(blocked)
				return
			}
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}
