package engine

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/config"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestDuckDBMemoryDropsSeriesWhenTheReadFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &Duck{}
		defer d.Close()
		calls := 0
		d.startMemorySampler(time.Second, func(context.Context) (map[string]int64, error) {
			calls++
			if calls == 1 {
				return map[string]int64{"BASE_TABLE": 4096}, nil
			}
			return nil, errors.New("memory query failed")
		})
		synctest.Wait()
		if memory, timestamp := memoryMetrics(t); memory != 4096 || timestamp <= 0 {
			t.Fatalf("first sample: memory=%g, timestamp=%g", memory, timestamp)
		}
		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()
		if memory, timestamp := memoryMetrics(t); memory != -1 || timestamp <= 0 {
			t.Fatalf("failed reads: memory=%g, timestamp=%g", memory, timestamp)
		}
	})
}

func TestMemorySamplerBlockedSampleAgesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &Duck{}
		unblock := make(chan struct{})
		defer func() {
			close(unblock)
			d.Close()
		}()
		calls := 0
		d.startMemorySampler(time.Second, func(ctx context.Context) (map[string]int64, error) {
			calls++
			if calls == 1 {
				return map[string]int64{"BASE_TABLE": 4096}, nil
			}
			<-unblock
			return nil, ctx.Err()
		})
		time.Sleep(time.Second)
		synctest.Wait()
		memory, timestamp := memoryMetrics(t)
		if memory != 4096 || timestamp <= 0 {
			t.Fatalf("fresh sample: memory=%g, timestamp=%g", memory, timestamp)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if memory, _ := memoryMetrics(t); memory != 4096 {
			t.Fatalf("sample exactly two intervals old: memory=%g", memory)
		}
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if memory, lastSuccess := memoryMetrics(t); memory != -1 || lastSuccess != timestamp {
			t.Fatalf("hung sampler: memory=%g, timestamp=%g, want absent memory and timestamp=%g", memory, lastSuccess, timestamp)
		}
		if calls != 2 {
			t.Fatalf("blocked sampler made %d calls, want 2", calls)
		}
	})
}

func TestMemorySamplerUsesIntervalDeadlineAndPacesFromCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &Duck{}
		defer d.Close()
		const interval = time.Second
		var calls atomic.Int32
		var finished time.Time
		d.startMemorySampler(interval, func(ctx context.Context) (map[string]int64, error) {
			if calls.Add(1) > 1 {
				if gap := time.Since(finished); gap != interval {
					t.Errorf("next sample started after %s, want %s", gap, interval)
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != interval {
				t.Errorf("read deadline=%v, want one interval from now", deadline)
			}
			// A successful query may return after its deadline; keep its result.
			<-ctx.Done()
			time.Sleep(interval / 2)
			finished = time.Now()
			return map[string]int64{"BASE_TABLE": 4096}, nil
		})
		time.Sleep(2 * interval)
		synctest.Wait()
		if memory, _ := memoryMetrics(t); memory != 4096 {
			t.Fatalf("successful overdue sample discarded: memory=%g", memory)
		}
		if calls.Load() != 1 {
			t.Fatalf("sampler made %d calls before the next interval", calls.Load())
		}
		time.Sleep(interval / 2)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("sampler made %d calls, want 2", calls.Load())
		}
	})
}

func TestDuckCloseBoundsWaitAndDefersHandleCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatal(err)
		}
		d := &Duck{DB: db}
		unblock := make(chan struct{})
		d.startMemorySampler(time.Hour, func(ctx context.Context) (map[string]int64, error) {
			<-unblock // Ignore cancellation, then use the handle before returning.
			if err := db.PingContext(context.Background()); err != nil {
				t.Errorf("sampler's handle closed while still in use: %v", err)
			}
			return nil, ctx.Err()
		})
		synctest.Wait()
		start := time.Now()
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Fatalf("Close waited %s, want 1s", elapsed)
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		mock.ExpectPing()
		mock.ExpectClose()
		close(unblock)
		synctest.Wait()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

// A missing memory series is distinct from a real zero-byte sample.
func memoryMetrics(t *testing.T) (memory, timestamp float64) {
	t.Helper()
	memory, timestamp = -1, -1
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		switch family.GetName() {
		case "fanout_duckdb_memory_bytes":
			memory = family.Metric[0].GetGauge().GetValue()
		case "fanout_duckdb_memory_last_success_timestamp_seconds":
			timestamp = family.Metric[0].GetGauge().GetValue()
		}
	}
	return
}

func TestMetricsScrapeDropsStaleMemoryWhileSamplerBlocks(t *testing.T) {
	var log bytes.Buffer
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	defer slog.SetDefault(logger)

	d := &Duck{}
	blocked := make(chan struct{})
	unblock := make(chan struct{})
	t.Cleanup(func() {
		d.stopMemorySampler()
		close(unblock)
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	calls := 0
	d.startMemorySampler(10*time.Millisecond, func(ctx context.Context) (map[string]int64, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("missing sampling deadline")
		}
		calls++
		switch {
		case calls == 1:
			return map[string]int64{"BASE_TABLE": 4096, "HASH_TABLE": 2048}, nil
		case calls <= 5:
			return nil, errors.New("memory query failed")
		default:
			close(blocked)
			// Model an engine call that ignores cancellation, as observed in
			// the benchmark. Scrapes must not wait for it or spawn more calls.
			<-unblock
			return nil, ctx.Err()
		}
	})
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("sampler did not reach the blocking query")
	}
	if n := strings.Count(log.String(), "memory sampling failed repeatedly"); n != 1 {
		t.Fatalf("repeated failures logged %d warnings, want 1: %s", n, log.String())
	}

	handler := promhttp.Handler()
	var timestamp string
	for range 3 {
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		start := time.Now()
		go func() {
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("/metrics waited for the blocked memory query")
		}
		t.Logf("/metrics returned in %s with the sampler blocked", time.Since(start))
		if recorder.Code != http.StatusOK {
			t.Fatalf("/metrics status=%d: %s", recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		if strings.Contains(body, "fanout_duckdb_memory_bytes") {
			t.Fatal("blocked sampler still exports stale memory series")
		}
		const prefix = "fanout_duckdb_memory_last_success_timestamp_seconds "
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, prefix) {
				value := strings.TrimPrefix(line, prefix)
				at, err := strconv.ParseFloat(value, 64)
				if err != nil || at <= 0 {
					t.Fatalf("invalid last-success timestamp: %s", line)
				}
				if timestamp != "" && value != timestamp {
					t.Fatal("blocked sampler advanced its last-success timestamp")
				}
				timestamp = value
			}
		}
		if timestamp == "" {
			t.Fatal("/metrics did not expose sample freshness")
		}
	}
}

func TestDuckCloseCancelsAndJoinsMemorySampler(t *testing.T) {
	d := &Duck{}
	started := make(chan struct{})
	stopped := make(chan struct{})
	d.startMemorySampler(time.Hour, func(ctx context.Context) (map[string]int64, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	<-started
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before the sampler stopped")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemorySamplingDoesNotNeedReadOrWritePoolCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "128MB", DuckDBMaxConns: 1}
	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	d, err := NewDuck(ctx, cfg, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
		if open := d.memoryDB.Stats().OpenConnections; open != 0 {
			t.Errorf("Close left %d memory connections open", open)
		}
	}()
	// Sample synchronously below so no timer determines which query runs.
	d.stopMemorySampler()
	<-d.memorySamplerDone
	if got := d.memoryDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("memory handle MaxOpenConnections = %d, want 1", got)
	}

	// Exhaust both application pools. Sampling must still reach the engine.
	read, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	for range 2 {
		write, err := d.writeDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
	}
	tags, err := d.sampleMemory(ctx)
	if err != nil {
		t.Fatalf("sample with read and write pools exhausted: %v", err)
	}
	if len(tags) == 0 {
		t.Fatal("memory sample contains no tags")
	}
	if inUse := d.memoryDB.Stats().InUse; inUse != 0 {
		t.Fatalf("memory query retained %d connections after reading rows", inUse)
	}
}

func TestMemorySamplerRejectsPartialQueryResults(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT tag, sum").WillReturnRows(
		sqlmock.NewRows([]string{"tag", "bytes"}).
			AddRow("BASE_TABLE", 4096).
			AddRow("HASH_TABLE", 2048).
			RowError(1, errors.New("read failed")),
	)
	d := &Duck{memoryDB: db}
	if tags, err := d.sampleMemory(t.Context()); err == nil || tags != nil {
		t.Fatalf("partial query returned tags=%v, err=%v", tags, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
