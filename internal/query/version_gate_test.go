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
