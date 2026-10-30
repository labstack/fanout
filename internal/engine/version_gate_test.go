package engine

import (
	"bytes"
	"context"
	"errors"
	"github.com/labstack/fanout/internal/engine/writegate"
	"github.com/labstack/fanout/internal/metrics"
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
	unlock := d.writeGate.Lock(writegate.RollupService)
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
	unlock := d.writeGate.Lock(writegate.RollupService)
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
	d, repo := versionEngine(t)
	commitVersionLogs(t, repo, "healthy", time.Now().UTC().Add(-time.Hour), 1)
	unlock := d.writeGate.Lock(writegate.RollupService)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(unlock) }
	defer release()
	watchdog := time.AfterFunc(time.Second, release)
	defer watchdog.Stop()
	counter := metrics.RollupComponentTotal.WithLabelValues("version", "error")
	before := testutil.ToFloat64(counter)
	var log bytes.Buffer
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	defer slog.SetDefault(logger)
	start := time.Now()
	_, err := d.refreshVersionRollup(context.Background(), 20*time.Millisecond)
	elapsed := time.Since(start)
	release()
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > 500*time.Millisecond {
		t.Fatalf("unbounded write wait: %v after %s", err, elapsed)
	}
	if after := testutil.ToFloat64(counter); after != before+1 {
		t.Fatalf("error counter=%v want %v", after, before+1)
	}
	if output := log.String(); !strings.Contains(output, "level=WARN") || !strings.Contains(output, "version rollup pass timed out") {
		t.Fatalf("missing timeout warning: %s", output)
	}
	if len(d.versionRollupFailures) != 0 {
		t.Fatal("gate wait penalized batches")
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatalf("gate leaked: %v", err)
	}
}

func TestVersionRollupWaitHonorsCallerCancellation(t *testing.T) {
	d, repo := versionEngine(t)
	commitVersionLogs(t, repo, "healthy", time.Now().UTC().Add(-time.Hour), 1)
	unlock := d.writeGate.Lock(writegate.RollupService)
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := d.refreshVersionRollup(ctx, time.Second); finished <- err }()
	blocked, stop := make(chan struct{}), make(chan struct{})
	defer close(stop)
	go signalVersionGateWait(blocked, stop)
	select {
	case <-blocked:
		cancel()
	case err := <-finished:
		t.Fatalf("returned before gate wait: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("never blocked on gate")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ignored cancellation")
	}
	release()
	if len(d.versionRollupFailures) != 0 {
		t.Fatal("cancellation penalized batch")
	}
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatalf("lock leaked: %v", err)
	}
}

// Observe a parked select, rather than guessing when a goroutine has reached
// the write gate.
// This test-only probe avoids adding instrumentation to the production gates.
func signalVersionGateWait(blocked chan<- struct{}, stop <-chan struct{}) {
	frame := "writegate.(*Gate).LockContext"
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	buffer := make([]byte, 1<<20)
	for {
		for stack := range strings.SplitSeq(string(buffer[:runtime.Stack(buffer, true)]), "\n\n") {
			if strings.Contains(stack, "[select]") && strings.Contains(stack, frame) && strings.Contains(stack, "engine.(*Duck).refreshVersionRollup") && strings.Contains(stack, "TestVersionRollupWaitHonorsCallerCancellation") {
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
