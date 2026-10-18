package dashboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func assertResourceFrame(t *testing.T, name string, frame *panel.Frame) {
	t.Helper()
	got, err := json.MarshalIndent(frame, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "resource-"+name+".golden.json")
	if os.Getenv("UPDATE_RESOURCE_GOLDENS") == "1" {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s frame differs from golden:\n%s", name, got)
	}
}

func resourcePanels() []panel.Panel {
	var panels []panel.Panel
	for _, p := range DefaultSpec().Panels {
		if p.ID == "versions" || p.ID == "check_states" {
			panels = append(panels, p)
		}
	}
	return panels
}

func executeResourcePanel(t *testing.T, e *panel.Executor, p panel.Panel, start, end time.Time) panel.Result {
	t.Helper()
	results, err := e.Run(t.Context(), panel.RunRequest{Dashboard: panel.Dashboard{Name: "Resource inventory", Time: panel.Time{From: &start, To: &end}, Panels: []panel.Panel{p}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != panel.StatusOK || results[0].Frame == nil {
		t.Fatalf("%s result: %+v", p.ID, results)
	}
	return results[0]
}

func TestDefaultResourcePanelsAcrossCacheStates(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := panel.NewExecutor(d, 30)
	start := time.Date(2026, 10, 1, 12, 0, 0, 1, time.UTC)
	end := start.Add(2 * time.Minute)
	for i := range 2 {
		b := telemetrystore.Batch{ID: []string{"first", "second"}[i]}
		for j, offset := range []time.Duration{-time.Minute, 0, 30 * time.Second, time.Minute, 2 * time.Minute} {
			n := start.Add(offset).UnixNano() + int64(i)
			r := map[string]any{"host.name": "same-host", "service.version": "v1", "process.runtime.name": []string{"go", "node"}[i], "process.runtime.version": "1", "telemetry.sdk.language": "go", "telemetry.sdk.version": "2"}
			// The version/host that exists only in lookback must stay absent.
			if j == 0 {
				r = map[string]any{"host.name": "lookback-only", "service.version": "lookback"}
			}
			b.Spans = append(b.Spans, telemetry.Span{Namespace: []string{"other", "prod"}[i], ServiceName: "api", StartUnixNanos: n, EndUnixNanos: n + 1, Resource: r})
			b.Logs = append(b.Logs, telemetry.Log{Namespace: "prod", ServiceName: "api", TimeUnixNanos: n, Resource: r})
			b.Metrics = append(b.Metrics, telemetry.Metric{Namespace: "prod", ServiceName: "api", Name: "httpcheck.status", Type: "gauge", TimeUnixNanos: n, Value: 1, Resource: r, Attributes: map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}})
		}
		if err := repo.Commit(t.Context(), b); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := d.RefreshReadCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, state := range []string{"partial", "cold", "full"} {
		t.Run(state, func(t *testing.T) {
			if state == "cold" {
				if _, err := d.DB.Exec(`DELETE FROM read_cache_version`); err != nil {
					t.Fatal(err)
				}
				if err := query.CreateCacheTables(d.DB); err != nil {
					t.Fatal(err)
				}
			}
			if state == "full" {
				if _, err := d.RefreshReadCaches(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range resourcePanels() {
				got := executeResourcePanel(t, e, p, start, end)
				assertResourceFrame(t, p.ID, got.Frame)
				// A shifted SQL execution carries its own logical inventory window.
				shifted := p
				shifted.Time = &panel.PanelTime{Shift: "1m"}
				got = executeResourcePanel(t, e, shifted, start, end)
				assertResourceFrame(t, p.ID+"-shifted", got.Frame)
			}
		})
	}
}

func TestDefaultResourceInventoryKeepsVantageNamespacesSeparate(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := panel.NewExecutor(d, 30)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	b := telemetrystore.Batch{ID: "namespaces", Spans: []telemetry.Span{{Namespace: "other", ServiceName: "api", StartUnixNanos: start.UnixNano(), Resource: map[string]any{"host.name": "same-host"}}}}
	for _, offset := range []time.Duration{-time.Minute, 0} {
		m := telemetry.Metric{Namespace: "prod", Name: "httpcheck.status", Type: "gauge", TimeUnixNanos: start.Add(offset).UnixNano(), Value: 1, Resource: map[string]any{"host.name": "same-host"}, Attributes: map[string]any{"http.url": "https://example.test", "http.method": "GET", "http.status_class": "2xx"}}
		b.Metrics = append(b.Metrics, m)
	}
	if err := repo.Commit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, p := range resourcePanels() {
			if p.ID != "check_states" {
				continue
			}
			got := executeResourcePanel(t, e, p, start, end)
			assertResourceFrame(t, "namespaces", got.Frame)
			for _, value := range defaultValues(t, got, "endpoint_from") {
				if strings.Contains(value.(string), "all checks run on service hosts") {
					t.Fatal("another namespace made a check local")
				}
			}
		}
		if _, err := d.RefreshReadCaches(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

// Opt-in evidence uses an extracted immutable snapshot and writes only to the
// supplied evidence directory and its separate disposable query cache.
func TestResourceInventoryFrozenSnapshotFrames(t *testing.T) {
	root := os.Getenv("FANOUT_TEST_INVENTORY_SNAPSHOT")
	if root == "" {
		t.Skip("set FANOUT_TEST_INVENTORY_SNAPSHOT to an extracted telemetry repository")
	}
	evidence := os.Getenv("FANOUT_TEST_INVENTORY_EVIDENCE")
	if evidence == "" {
		t.Fatal("FANOUT_TEST_INVENTORY_EVIDENCE is required")
	}
	if err := os.MkdirAll(evidence, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: filepath.Join(evidence, "runtime"), DuckDBMemory: "8GB", DuckDBThreads: 8, DuckDBMaxConns: 4}
	repo, err := telemetrystore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := panel.NewExecutor(d, 30)
	var engine string
	if err := d.DB.QueryRow(`SELECT version()`).Scan(&engine); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 16, 15, 47, 42, 261874001, time.UTC)
	end := start.Add(time.Hour)
	write := func(name string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidence, name), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stats := map[string]any{"engine": engine, "start": start, "end": end}
	sqlEvidence := map[string]string{}
	for _, p := range resourcePanels() {
		sqlEvidence[p.ID] = p.SQL
	}
	write("panel-sql.json", sqlEvidence)
	build := time.Now()
	passes := 0
	for {
		var markers int
		if err := d.DB.QueryRow(`SELECT count(*) FROM read_batches`).Scan(&markers); err != nil {
			t.Fatal(err)
		}
		if markers == len(repo.Parquet.BatchMetadata()) {
			break
		}
		if _, err := d.RefreshReadCaches(t.Context()); err != nil {
			t.Fatal(err)
		}
		passes++
		if passes > 10000 {
			t.Fatal("cache did not catch up")
		}
	}
	stats["cache_build_ms"] = float64(time.Since(build).Microseconds()) / 1000
	stats["cache_passes"] = passes
	var count int
	if err := d.DB.QueryRow(`SELECT count(*) FROM read_resource_inventory`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	stats["inventory_rows"] = count
	signalRows, err := d.DB.Query(`SELECT signal,count(*) FROM read_resource_inventory GROUP BY signal ORDER BY signal`)
	if err != nil {
		t.Fatal(err)
	}
	bySignal := map[string]int{}
	for signalRows.Next() {
		var signal string
		var count int
		if err := signalRows.Scan(&signal, &count); err != nil {
			t.Fatal(err)
		}
		bySignal[signal] = count
	}
	err = signalRows.Err()
	signalRows.Close()
	if err != nil {
		t.Fatal(err)
	}
	stats["inventory_rows_by_signal"] = bySignal
	for _, p := range resourcePanels() {
		var elapsed []int64
		var want *panel.Frame
		for i := range 4 {
			r := executeResourcePanel(t, e, p, start, end)
			elapsed = append(elapsed, r.ElapsedMS)
			if i == 0 {
				want = r.Frame
			} else if !reflect.DeepEqual(want, r.Frame) {
				t.Fatalf("unstable frozen %s frame", p.ID)
			}
		}
		write(p.ID+".json", want)
		stats[p.ID+"_ms"] = elapsed
		stats[p.ID+"_rows"] = want.Rows
	}
	stats["cache_wal_bytes_before_checkpoint"] = int64(0)
	if info, err := os.Stat(cfg.QueryDuckDBPath() + ".wal"); err == nil {
		stats["cache_wal_bytes_before_checkpoint"] = info.Size()
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := d.DB.Exec(`CHECKPOINT`); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(cfg.QueryDuckDBPath()); err == nil {
		stats["cache_database_bytes"] = info.Size()
	} else {
		t.Fatal(err)
	}
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	rss := usage.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	stats["peak_rss_bytes"] = rss
	write("measurements.json", stats)
	t.Logf("snapshot evidence: %+v", stats)
}
