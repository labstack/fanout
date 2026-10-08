package query

import (
	"context"
	"errors"
	"github.com/labstack/fanout/internal/query/writegate"
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
			start := time.Now()
			_, err := d.refreshVersionRollup(context.Background(), 20*time.Millisecond)
			elapsed := time.Since(start)
			close(finished)
			<-released
			if !errors.Is(err, context.DeadlineExceeded) || elapsed > 500*time.Millisecond {
				t.Fatalf("unbounded %s wait: %v after %s", gate, err, elapsed)
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
			cancel()
			_, err := d.refreshVersionRollup(ctx, time.Second)
			close(release)
			<-released
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation: %v", err)
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
