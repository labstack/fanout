package query

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/query/writegate"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestM2FixVersionHistoryDeletesOnlyWhenNeeded(t *testing.T) {
	_, repo := versionEngine(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := &Duck{DB: db, repository: repo, versionRollupPasses: 1}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT batch_id FROM version_rollup_batches`).WillReturnRows(sqlmock.NewRows([]string{"batch_id"}))
	mock.ExpectQuery(`SELECT count\(\*\) FROM version_rollup`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`INSERT INTO rollup_state`).WithArgs("version_rollup_v1_limited", int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT coalesce\(max\(max_ingested\),0\) FROM version_rollup_batches`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(0))
	mock.ExpectQuery(`SELECT coalesce\(max\(last_ingested_unix_nano\),0\) FROM rollup_state`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(0))
	mock.ExpectExec(`INSERT INTO rollup_state`).WithArgs("version_rollup_v1", int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if _, err := d.RefreshVersionRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestM2FixVersionPassTimeoutWarnAndMetric(t *testing.T) {
	d, _ := versionEngine(t)
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	defer unlock()
	counter := metrics.RollupComponentTotal.WithLabelValues("version", "error")
	before := testutil.ToFloat64(counter)
	var log bytes.Buffer
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	defer slog.SetDefault(logger)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := d.RefreshVersionRollup(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	if after := testutil.ToFloat64(counter); after != before+1 {
		t.Fatalf("timeout counter=%v want %v", after, before+1)
	}
	if output := log.String(); !strings.Contains(output, "level=WARN") || !strings.Contains(output, "version rollup pass timed out") {
		t.Fatalf("missing timeout warning: %s", output)
	}
}

func TestM2FixVersionMetricsCountRows(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().UnixNano()
	logs := []telemetry.Log{}
	for _, version := range []string{"v1", "v2"} {
		logs = append(logs, telemetry.Log{ServiceName: "svc", Resource: map[string]any{"service.version": version}, EventUnixNanos: now, IngestedAt: now})
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "metrics", Logs: logs}); err != nil {
		t.Fatal(err)
	}
	counter := metrics.RollupComponentRows.WithLabelValues("version")
	before := testutil.ToFloat64(counter)
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 1 {
		t.Fatalf("pass=%d %v", n, err)
	}
	if after := testutil.ToFloat64(counter); after != before+2 {
		t.Fatalf("materialized rows=%v want %v", after-before, 2)
	}
}

func TestM2FixDrainBudgetBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	started := time.Now()
	var passes int
	n, err := drainVersionRollup(ctx, func(ctx context.Context) (int64, error) {
		passes++
		select {
		case <-time.After(25 * time.Millisecond):
			return 1, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	elapsed := time.Since(started)
	if err != nil || elapsed < 1900*time.Millisecond || elapsed > 2500*time.Millisecond || n != int64(passes) {
		t.Fatalf("unbounded/incorrect drain: passes=%d n=%d elapsed=%v err=%v", passes, n, elapsed, err)
	}
}

func TestM2FixDrainReleasesGateBetweenPasses(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "yield", Logs: []telemetry.Log{{ServiceName: "svc", EventUnixNanos: now, IngestedAt: now}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	serviceDone := make(chan error, 1)
	passes := 0
	n, err := drainVersionRollup(ctx, func(ctx context.Context) (int64, error) {
		passes++
		if passes == 2 {
			select {
			case err := <-serviceDone:
				if err != nil {
					return 0, err
				}
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		n, err := d.RefreshVersionRollup(ctx)
		if passes == 1 {
			go func() { _, err := d.refreshServiceRollup(ctx); serviceDone <- err }()
		}
		return n, err
	})
	if err != nil || n != 1 || passes != 2 {
		t.Fatalf("service could not acquire gate: %d passes=%d %v", n, passes, err)
	}
}

func TestM2FixSingleDrainRegistration(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "duck.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "rollupOnce" && fn.Name.Name != "runReadCacheLoop") {
			continue
		}
		count := 0
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "DrainVersionRollup" {
					count++
				}
			}
			return true
		})
		want := 0
		if fn.Name.Name == "runReadCacheLoop" {
			want = 1
		}
		if count != want {
			t.Fatalf("%s has %d drain registrations, want %d", fn.Name.Name, count, want)
		}
	}
}

func TestM2FixAnomalyInputTruncation(t *testing.T) {
	now := time.Now().UTC()
	findings := make([]annotations.Anomaly, 10002)
	for i := range findings {
		findings[i] = annotations.Anomaly{Service: fmt.Sprint(i), From: now.Add(-time.Minute), To: now.Add(time.Duration(10002-i) * time.Nanosecond)}
	}
	started := time.Now()
	got := boundedAnomalies(findings)
	if len(got) != 10000 || got[0].Service != "9999" || got[len(got)-1].Service != "0" {
		t.Fatalf("incorrect latest input selection: len=%d first=%s last=%s", len(got), got[0].Service, got[len(got)-1].Service)
	}
	if findings[0].Service != "0" {
		t.Fatal("input mutated")
	}
	if time.Since(started) >= time.Second {
		t.Fatal("truncation exceeded 1s")
	}
	t.Logf("input truncation took %v", time.Since(started))
}

func TestM2FixSlowVersionPassCommits(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().UnixNano()
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "slow", Logs: []telemetry.Log{{ServiceName: "svc", Resource: map[string]any{"service.version": "v1"}, EventUnixNanos: now, IngestedAt: now}}}); err != nil {
		t.Fatal(err)
	}
	unlock := d.writeGate.Lock(writegate.WriteRollupService)
	timer := time.AfterFunc(2200*time.Millisecond, unlock)
	defer func() {
		if timer.Stop() {
			unlock()
		}
	}()
	n, err := d.DrainVersionRollup(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("slow pass must commit: batches=%d err=%v", n, err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches WHERE batch_id='slow'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("marker=%d err=%v", count, err)
	}
}

func TestM2FixAnomalyMergePerformance(t *testing.T) {
	d, _ := versionEngine(t)
	now := time.Now().UTC()
	if _, err := d.DB.Exec(`INSERT INTO anomaly_log SELECT 'shop','svc-'||i,'latency',(?::TIMESTAMP_NS-INTERVAL '5 minutes')::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS,'old','warn' FROM generate_series(1,10000) t(i)`, now, now); err != nil {
		t.Fatal(err)
	}
	findings := make([]annotations.Anomaly, 10000)
	for i := range findings {
		findings[i] = annotations.Anomaly{Namespace: "shop", Service: fmt.Sprint("svc-", i+1), Kind: "latency", From: now.Add(-time.Minute), To: now.Add(time.Minute), Title: "new", Severity: "bad"}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := d.RecordAnomalies(ctx, findings, now); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("merge took %v", elapsed)
	}
	t.Logf("10,000 overlapping findings merged in %v", time.Since(start))
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM anomaly_log WHERE title='new' AND severity='bad' AND start_time=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND end_time=?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, now.Add(-5*time.Minute), now.Add(time.Minute)).Scan(&count); err != nil || count != 10000 {
		t.Fatalf("merged=%d %v", count, err)
	}
}

func TestM2FixOversizedVersionBatch(t *testing.T) {
	d, repo := versionEngine(t)
	now := time.Now().UTC().UnixNano()
	logs := make([]telemetry.Log, 64001)
	for i := range logs {
		logs[i] = telemetry.Log{ServiceName: "oversized", Resource: map[string]any{"service.version": "v1"}, EventUnixNanos: now, IngestedAt: now}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "oversized", Logs: logs}); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	defer slog.SetDefault(logger)
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 0 {
		t.Fatalf("unbounded batch read: %d %v", n, err)
	}
	if output := log.String(); !strings.Contains(output, "level=WARN") || !strings.Contains(output, "version rollup batch exceeds row budget") || !strings.Contains(output, "batch_id=oversized") {
		t.Fatalf("missing oversized warning: %s", output)
	}
	if n, err := d.RefreshVersionRollup(t.Context()); err != nil || n != 0 {
		t.Fatalf("oversized retry: %d %v", n, err)
	}
	var count, limited int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("oversized scanned=%d %v", count, err)
	}
	if err := d.DB.QueryRow(`SELECT last_ingested_unix_nano FROM rollup_state WHERE cache_key='version_rollup_v1_limited'`).Scan(&limited); err != nil || limited != 1 {
		t.Fatalf("limited=%d %v", limited, err)
	}
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches WHERE batch_id='oversized'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("oversized batch acknowledged=%d %v", count, err)
	}
}

func TestM2FixVersionMarkerColdInput(t *testing.T) {
	d, _ := versionEngine(t)
	if _, err := d.DB.Exec(`INSERT INTO version_rollup_batches VALUES ('warm',1)`); err != nil {
		t.Fatal(err)
	}
	if err := d.transferVersionMarker(t.Context(), telemetry.BatchMetadata{ID: "output", MaxIngestedNanos: 2}, []string{"warm", "cold"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM version_rollup_batches WHERE batch_id='output'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cold output marked=%d %v", count, err)
	}
}
