package query

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsScrapeKeepsLastMemorySampleWhileSamplerBlocks(t *testing.T) {
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
	d.startMemorySampler(time.Millisecond, func(ctx context.Context) (map[string]int64, error) {
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
		for _, series := range []string{
			`fanout_duckdb_memory_bytes{tag="BASE_TABLE"} 4096`,
			`fanout_duckdb_memory_bytes{tag="HASH_TABLE"} 2048`,
		} {
			if !strings.Contains(body, series) {
				t.Fatalf("missing cached series %s", series)
			}
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
	d := &Duck{DB: db}
	if tags, err := d.sampleMemory(t.Context()); err == nil || tags != nil {
		t.Fatalf("partial query returned tags=%v, err=%v", tags, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
