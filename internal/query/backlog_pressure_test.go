//go:build readbench

package query

import (
	"context"
	"errors"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus"
)

// The deadline includes seeding. All disposable files stay in the private workspace.
func pressureBatches(t *testing.T) int {
	t.Helper()
	if raw := os.Getenv("FANOUT_BACKLOG_BATCHES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("invalid FANOUT_BACKLOG_BATCHES %q", raw)
		}
		return n
	}
	return 4096
}
func pressureDuration(t *testing.T) time.Duration {
	t.Helper()
	if raw := os.Getenv("FANOUT_BACKLOG_DURATION"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			t.Fatalf("invalid FANOUT_BACKLOG_DURATION %q", raw)
		}
		return d
	}
	return 2 * time.Minute
}
func pressureBatch(batch int, base time.Time, live bool) telemetrystore.Batch {
	prefix := "pressure"
	if live {
		prefix = "live"
	}
	b := telemetrystore.Batch{ID: fmt.Sprintf("%s-%06d", prefix, batch)}
	for i := range 512 {
		n := base.Add(-24*time.Hour + time.Duration((batch*512+i)%86400)*time.Second).UnixNano()
		ingested := base.Add(time.Duration(batch) * time.Millisecond).UnixNano()
		if live {
			n = base.Add(-time.Second).UnixNano()
			if batch%2 == 0 {
				n = base.Add(-12 * time.Hour).UnixNano()
			}
			ingested = base.Add(time.Duration(batch+1) * time.Second).UnixNano()
		}
		version := "v1"
		if i%2 == 0 {
			version = "v2"
		}
		service := fmt.Sprintf("svc%d", i%6)
		trace := fmt.Sprintf("%s-trace-%d-%d", prefix, batch, i/4)
		b.Spans = append(b.Spans, telemetry.Span{Namespace: "pressure", ServiceName: service, TraceID: trace, SpanID: fmt.Sprintf("s%d", i), StartUnixNanos: n, EndUnixNanos: n + int64(i%1000+1)*1000000, DurationMS: float64(i%1000 + 1), Kind: "SPAN_KIND_SERVER", StatusCode: "STATUS_CODE_OK", ServiceVersion: version, IngestedAt: ingested})
		if i < 192 {
			b.Logs = append(b.Logs, telemetry.Log{Namespace: "pressure", ServiceName: service, TraceID: trace, EventUnixNanos: n, TimeUnixNanos: n, IngestedAt: ingested, Body: "request complete", Severity: "INFO"})
		}
	}
	return b
}
func pressureError(t *testing.T, label string, started time.Time, err error) {
	t.Helper()
	var native *duckdb.Error
	typed := errors.As(err, &native)
	kind := "none"
	if typed {
		kind = fmt.Sprint(native.Type)
	}
	t.Logf("statement=%s started=%s ended=%s duration=%s error=%v native_type=%s canceled=%t deadline=%t", label, started.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), time.Since(started), err, kind, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded))
}

type pressureLog struct{ t *testing.T }

func (h pressureLog) Enabled(context.Context, slog.Level) bool { return true }
func (h pressureLog) Handle(_ context.Context, r slog.Record) error {
	h.t.Logf("internal_log time=%s level=%s message=%q", r.Time.UTC().Format(time.RFC3339Nano), r.Level, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		if err, ok := a.Value.Any().(error); ok {
			pressureError(h.t, "internal_log/"+r.Message, r.Time, err)
		} else {
			h.t.Logf("internal_attr %s=%v", a.Key, a.Value)
		}
		return true
	})
	return nil
}
func (h pressureLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h pressureLog) WithGroup(string) slog.Handler      { return h }

// Same /proc + ps methodology as processRSSMiB, but unsupported diagnostics
// are reported as inconclusive rather than skipping a pressure test.
var pressureRSSState struct {
	sync.Mutex
	blocked error
}

func pressureRSS() (float64, error) {
	pressureRSSState.Lock()
	defer pressureRSSState.Unlock()
	if pressureRSSState.blocked != nil {
		return 0, pressureRSSState.blocked
	}
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		f := strings.Fields(string(raw))
		if len(f) > 1 {
			n, e := strconv.ParseFloat(f[1], 64)
			return n * float64(os.Getpagesize()) / (1 << 20), e
		}
	}
	raw, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		pressureRSSState.blocked = err
		return 0, err
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	return n / 1024, err
}
func pressureDiagnostics(ctx context.Context, t *testing.T, d *Duck, repo *telemetrystore.Repository, phase string) {
	t.Helper()
	stats := d.DB.Stats()
	t.Logf("write_pool phase=%s stats=%+v", phase, d.writeDB.Stats())
	var usage syscall.Rusage
	cpuErr := syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpuSeconds := float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
	t.Logf("cpu phase=%s seconds=%.6f error=%v", phase, cpuSeconds, cpuErr)
	rss, rssErr := pressureRSS()
	var goMem runtime.MemStats
	runtime.ReadMemStats(&goMem)
	t.Logf("diagnostics phase=%s platform=%s/%s cpus=%d rss_mib=%.2f rss_error=%v go_heap_bytes=%d go_total_alloc_bytes=%d read_pool=%+v", phase, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), rss, rssErr, goMem.HeapAlloc, goMem.TotalAlloc, stats)
	files, err := repo.Parquet.Stats()
	t.Logf("files phase=%s stats=%+v error=%v active_batches=%d rows=%d", phase, files, err, len(repo.Parquet.BatchMetadata()), repo.RowCount())
	// Never queue diagnostics behind the pressure workers. Five read slots are
	// fixed for all phases; busy samples are recorded and retried between phases.
	if stats.InUse >= stats.MaxOpenConnections {
		t.Logf("diagnostics phase=%s sql_sample=busy", phase)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, q := range []string{
		"SELECT version(),current_setting('memory_limit'),current_setting('threads'),current_setting('temp_directory')<>''",
		"SELECT tag,memory_usage_bytes,temporary_storage_bytes FROM duckdb_memory() ORDER BY tag",
		"SELECT coalesce(sum(size),0) FROM duckdb_temporary_files()",
		"SELECT cache_key,last_ingested_unix_nano FROM rollup_state ORDER BY cache_key",
		"SELECT (SELECT count(*) FROM read_batches),(SELECT count(*) FROM version_rollup_batches),(SELECT count(*) FROM version_rollup),(SELECT count(*) FROM anomaly_log)",
	} {
		rows, err := d.DB.QueryContext(ctx, q)
		if err != nil {
			pressureError(t, "diagnostics/"+q, time.Now(), err)
			continue
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Logf("diagnostic_columns error=%v", err)
			continue
		}
		for rows.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err := rows.Scan(p...); err != nil {
				t.Logf("diagnostic_scan error=%v", err)
				break
			}
			t.Logf("diagnostic phase=%s columns=%v values=%v", phase, cols, v)
		}
		if err := rows.Err(); err != nil {
			pressureError(t, "diagnostics/rows", time.Now(), err)
		}
		rows.Close()
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Logf("metrics error=%v", err)
		return
	}
	for _, f := range families {
		if f.GetName() == "fanout_write_gate_wait_seconds" || f.GetName() == "fanout_write_gate_hold_seconds" {
			for _, m := range f.Metric {
				t.Logf("gate phase=%s metric=%s labels=%v count=%d seconds_sum=%.6f", phase, f.GetName(), m.Label, m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum())
			}
		}
	}
}

func pressureDuck(ctx context.Context, t *testing.T, batches int) (*Duck, *telemetrystore.Repository, time.Time) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".superpowers", "sdd", "2026-10-09-agent-dashboards-m4", "task-2-artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "pressure-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scratch_state=%s", dir)
	base := time.Now().UTC().Truncate(time.Minute)
	cfg := config.Config{DataDir: dir, RetentionDays: 30, DuckDBMemory: "4GB", DuckDBThreads: 4, DuckDBMaxConns: 5, RollupInterval: time.Second}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("repository close: %v", err)
		}
	})
	started := time.Now()
	seeded := 0
	defer func() {
		if seeded != batches {
			t.Logf("seed_duration=%s seed_batches=%d requested=%d seed_incomplete=true", time.Since(started), seeded, batches)
		}
	}()
	for batch := range batches {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		if err := repo.Commit(ctx, pressureBatch(batch, base, false)); err != nil {
			t.Fatal(err)
		}
		seeded++
		if seeded%256 == 0 {
			t.Logf("seed_progress batches=%d duration=%s", seeded, time.Since(started))
		}
	}
	t.Logf("seed_complete duration=%s spans=%d logs=%d", time.Since(started), batches*512, batches*192)
	opened := time.Now()
	d, err := NewDuck(ctx, cfg, repo)
	pressureError(t, "NewDuck", opened, err)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("engine close: %v", err)
		}
	})
	return d, repo, base
}

// This controls statement attribution; the panel-package combined phase is
// the authoritative reproduction. Run each batch size in a separate process.
func TestBacklogPressureRecordsConcurrentStatementFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	d, repo, base := pressureDuck(ctx, t, pressureBatches(t))
	logger := slog.Default()
	slog.SetDefault(slog.New(pressureLog{t}))
	defer slog.SetDefault(logger)
	pressureDiagnostics(ctx, t, d, repo, "before_service_alone")
	started := time.Now()
	n, err := d.refreshServiceRollup(ctx)
	pressureError(t, "service_rollup/solo", started, err)
	t.Logf("service_solo rows=%d", n)
	pressureDiagnostics(ctx, t, d, repo, "after_service_alone")
	started = time.Now()
	n, err = d.refreshEdgeRollup(ctx)
	pressureError(t, "edge_rollup/solo", started, err)
	t.Logf("edge_solo rows=%d", n)
	pressureDiagnostics(ctx, t, d, repo, "after_edge_alone")
	ready := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-ready; d.RunRollups(ctx) }()
	go func() {
		defer wg.Done()
		<-ready
		for batch := range 32 {
			started := time.Now()
			err := repo.Commit(ctx, pressureBatch(batch, base, true))
			pressureError(t, "publication", started, err)
			if err != nil {
				return
			}
		}
	}()
	close(ready)
	timer := time.NewTimer(pressureDuration(t))
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	timer.Stop()
	cancel()
	wg.Wait()
	pressureDiagnostics(t.Context(), t, d, repo, "after_loop_publication")
	t.Log("outcome=diagnostic_only; query probe does not establish panel robustness")
}

// Existing rollupOnce only labels the component, losing which of its two
// expensive statements failed. Attribution must retain the native cause.
func TestBacklogServiceFailuresIdentifyStatementAndPreserveCause(t *testing.T) {
	for _, label := range []string{"service_rollup/delete", "service_rollup/insert"} {
		t.Run(label, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cause := &duckdb.Error{Type: duckdb.ErrorTypeOutOfMemory, Msg: "Out of Memory Error: attribution fixture"}
			mock.ExpectBegin()
			for _, key := range []string{serviceRollupStateKey, serviceRollupRawMaxKey} {
				mock.ExpectQuery("FROM rollup_state").WithArgs(key).WillReturnRows(sqlmock.NewRows([]string{"watermark"}).AddRow(1))
			}
			mock.ExpectQuery("FROM \\(").WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(2))
			if label == "service_rollup/delete" {
				mock.ExpectExec("DELETE FROM service_rollup").WillReturnError(cause)
			} else {
				mock.ExpectExec("DELETE FROM service_rollup").WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("WITH affected").WillReturnError(cause)
			}
			mock.ExpectRollback()
			d := &Duck{DB: db, writeDB: db}
			_, err = d.refreshServiceRollup(t.Context())
			var native *duckdb.Error
			if err == nil || !strings.Contains(err.Error(), label) || !errors.Is(err, cause) || !errors.As(err, &native) || native != cause {
				t.Fatalf("statement attribution/cause lost: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBacklogAnnotationFailuresIdentifyStatementAndPreserveCause(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	_, repo, _ := pressureDuck(ctx, t, 1)
	for _, label := range []string{"version_rollup/insert", "anomaly_log/bounds", "anomaly_log/insert"} {
		t.Run(label, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cause := &duckdb.Error{Type: duckdb.ErrorTypeOutOfMemory, Msg: "Out of Memory Error: attribution fixture"}
			d := &Duck{DB: db, writeDB: db, repository: repo}
			if label == "version_rollup/insert" {
				mock.ExpectQuery("SELECT batch_id FROM version_rollup_batches").WillReturnRows(sqlmock.NewRows([]string{"batch_id"}))
				mock.ExpectBegin()
				mock.ExpectExec("INSERT INTO version_rollup SELECT").WillReturnError(cause)
				mock.ExpectRollback()
				_, err = d.RefreshVersionRollup(ctx)
			} else {
				mock.ExpectBegin()
				if label == "anomaly_log/bounds" {
					mock.ExpectExec("CREATE TEMP TABLE annotation_bounds").WillReturnError(cause)
				} else {
					mock.ExpectExec("CREATE TEMP TABLE annotation_bounds").WillReturnResult(sqlmock.NewResult(0, 0))
					mock.ExpectQuery("SELECT a.namespace").WillReturnRows(sqlmock.NewRows([]string{"namespace", "service", "kind", "start", "end", "title", "severity"}))
					for _, q := range []string{"CREATE TEMP TABLE annotation_updates", "CREATE TEMP TABLE annotation_candidates"} {
						mock.ExpectExec(q).WillReturnResult(sqlmock.NewResult(0, 0))
					}
					mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
					mock.ExpectExec("DELETE FROM anomaly_log").WillReturnResult(sqlmock.NewResult(0, 0))
					mock.ExpectExec("INSERT INTO anomaly_log").WillReturnError(cause)
				}
				mock.ExpectRollback()
				now := time.Now().UTC()
				err = d.RecordAnomalies(ctx, []annotations.Anomaly{{Namespace: "pressure", Service: "svc0", Kind: "latency", From: now.Add(-time.Minute), To: now, Title: "Attribution", Severity: "warning"}}, now)
			}
			var native *duckdb.Error
			if err == nil || !strings.Contains(err.Error(), label) || !errors.Is(err, cause) || !errors.As(err, &native) || native != cause {
				t.Fatalf("statement attribution/cause lost: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBacklogAnnotationTimeoutsIdentifyWriteGateAndPreserveDeadline(t *testing.T) {
	for _, label := range []string{"version_rollup/write_gate", "anomaly_log/write_gate"} {
		t.Run(label, func(t *testing.T) {
			d := &Duck{repository: &telemetrystore.Repository{}}
			unlock := d.writeGate.Lock(writegate.WriteRollupService)
			defer unlock()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			var err error
			if label == "version_rollup/write_gate" {
				_, err = d.RefreshVersionRollup(ctx)
			} else {
				err = d.RecordAnomalies(ctx, nil, time.Now())
			}
			if err == nil || !strings.Contains(err.Error(), label) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("gate attribution/deadline lost: %v", err)
			}
		})
	}
}
