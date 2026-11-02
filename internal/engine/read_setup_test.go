package engine

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Panic at the first setup operation after readConn has acquired its pin.
type panicReadWindowContext struct{ context.Context }

func (c panicReadWindowContext) Value(key any) any {
	if fmt.Sprintf("%T", key) == "queryrows.windowKey" {
		panic("read setup")
	}
	return c.Context.Value(key)
}

func TestQuerySetupPanicReleasesPinAndConnection(t *testing.T) {
	d, repo := newBatchCacheTest(t)
	at := time.Now().UTC().Add(-time.Minute)
	commitCurrentBatches(t, repo, "panic", 8, at)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("setup did not panic")
			}
		}()
		_, _ = d.QueryContext(panicReadWindowContext{t.Context()}, "SELECT 1")
	}()
	if n := d.DB.Stats().InUse; n != 0 {
		t.Errorf("panic leaked %d read connections", n)
	}
	if n, err := repo.CompactParquet(t.Context(), d, 8); err != nil || n != 8 {
		t.Fatalf("compact: %d %v", n, err)
	}
	if n, err := repo.CollectRetiredParquet(); err != nil || n != 8 {
		t.Fatalf("panic left retired inputs pinned: collected=%d err=%v", n, err)
	}
}
