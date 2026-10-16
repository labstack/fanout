package metrics

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsScrapeKeepsCachedPoolStatsWhileSamplerBlocks(t *testing.T) {
	blocked, unblock := make(chan struct{}), make(chan struct{})
	calls := 0
	release := setDuckDBPoolSource(func() sql.DBStats {
		calls++
		if calls == 2 {
			close(blocked)
			<-unblock
		}
		return sql.DBStats{InUse: 1, Idle: 2, MaxOpenConnections: 3, WaitCount: 4, WaitDuration: 5 * time.Second}
	}, time.Millisecond)
	t.Cleanup(func() {
		close(unblock)
		release()
		release() // Release is idempotent.
	})
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("pool sampler did not reach the blocking call")
	}
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	handler := promhttp.Handler()
	go func() {
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("/metrics waited for pool bookkeeping")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status=%d", recorder.Code)
	}
	for _, series := range []string{
		"fanout_duckdb_pool_in_use 1\n",
		"fanout_duckdb_pool_idle 2\n",
		"fanout_duckdb_pool_max_open 3\n",
		"fanout_duckdb_pool_waits_total 4\n",
		"fanout_duckdb_pool_wait_seconds_total 5\n",
	} {
		if !strings.Contains(recorder.Body.String(), series) {
			t.Fatalf("missing cached series %q", series)
		}
	}
}

func TestDuckDBPoolReleaseOnlyClearsItsOwner(t *testing.T) {
	releaseOld := setDuckDBPoolSource(func() sql.DBStats { return sql.DBStats{InUse: 1} }, time.Hour)
	release := setDuckDBPoolSource(func() sql.DBStats { return sql.DBStats{InUse: 2} }, time.Hour)
	defer release()
	releaseOld()
	deadline := time.After(5 * time.Second)
	for poolStats().InUse != 2 {
		select {
		case <-deadline:
			t.Fatal("new owner's pool stats were cleared")
		case <-time.After(time.Millisecond):
		}
	}
	release()
	if got := poolStats(); got != (sql.DBStats{}) {
		t.Fatalf("released pool retained stats: %+v", got)
	}
}
