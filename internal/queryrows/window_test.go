package queryrows

import (
	"sync"
	"testing"
	"time"
)

func TestCoverageKeepsOldestHorizon(t *testing.T) {
	ctx, c := WithCoverage(t.Context())
	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ {
		wg.Go(func() { RecordCoverage(ctx, time.Unix(int64(i), 0)) })
	}
	wg.Wait()
	if !c.FreshTo().Equal(time.Unix(1, 0)) {
		t.Fatal(c.FreshTo())
	}
}
