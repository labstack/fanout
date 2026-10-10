package telemetry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMetricPhysicalRowKeepsFidelityColumns(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	metric := Metric{Name: "net", Type: "sum", Temporality: "delta", Monotonic: true,
		StartTimeUnixNanos: 1790855940000000017, TimeUnixNanos: 1790856000000000009, IngestedAt: 1790856000000000010}
	if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "fidelity"}, nil, nil, []Metric{metric}); err != nil {
		t.Fatal(err)
	}
	got := readOneParquetRow[metricParquetRow](t, filepath.Join(store.BatchPath("fidelity"), "metrics.parquet"))
	if got.MetricType != "sum" || got.Temporality != "delta" || !got.Monotonic || got.StartTimeUnixNano != metric.StartTimeUnixNanos {
		t.Fatalf("physical=%+v", got)
	}
	if err := ValidatePublishedBatch(store.BatchPath("fidelity")); err != nil {
		t.Fatal(err)
	}
	if store.BatchMetadata()[0].Version != 4 {
		t.Fatal("metadata is not format 4")
	}
}

func TestFormatThreeIsRefusedBeforeCleanup(t *testing.T) {
	for _, suffix := range []string{BatchSuffix, ".retired", ".retired-replacement"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			store, err := OpenParquetStore(root)
			if err != nil {
				t.Fatal(err)
			}
			err = store.CommitBatch(t.Context(), BatchMetadata{ID: "old"}, []Span{completeTestSpan()}, []Log{{Body: "ready", TimeUnixNanos: 1}},
				[]Metric{{Name: "n", Type: "gauge", TimeUnixNanos: 1, IngestedAt: 1}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			dir := store.BatchPath("old")
			if suffix != BatchSuffix {
				retired := filepath.Join(root, "batches", "old"+suffix)
				if err := os.Rename(dir, retired); err != nil {
					t.Fatal(err)
				}
				dir = retired
			}
			metaPath := filepath.Join(dir, "metadata.json")
			raw, err := os.ReadFile(metaPath)
			if err != nil {
				t.Fatal(err)
			}
			var meta BatchMetadata
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			meta.Version = 3
			old, err := json.Marshal(meta)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(metaPath, old, 0o644); err != nil {
				t.Fatal(err)
			}
			staging := filepath.Join(root, "staging", "keep")
			if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(staging, []byte("preserve"), 0o644); err != nil {
				t.Fatal(err)
			}
			snapshot := func() map[string][]byte {
				files := map[string][]byte{}
				if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					raw, err := os.ReadFile(path)
					files[path] = raw
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return files
			}
			before := snapshot()
			if _, err := OpenParquetStore(root); !errors.Is(err, ErrUnsupportedBatchFormat) {
				t.Fatalf("open: %v", err)
			}
			if err := ValidatePublishedBatch(filepath.Dir(metaPath)); !errors.Is(err, ErrUnsupportedBatchFormat) || IsBatchCorrupt(err) {
				t.Fatalf("verify: %v", err)
			}
			if !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("files changed after rejecting format 3 metadata")
			}
		})
	}
}

func TestFormatFourRequiresNewMetricPhysicalSchema(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(string) error
	}{
		{"format_three_columns", func(path string) error {
			return writeTypedParquet(path, []formatThreeMetricParquetRow{{Name: "n", MetricType: "sum", MetricTime: 1, TimeUnixNano: 1, IngestedAt: 1, IngestedUnixNano: 1}}, 1024)
		}},
		{"temporality_int64", func(path string) error {
			return writeTypedParquet(path, []wrongTemporalityMetricParquetRow{{Name: "n", MetricType: "sum", Temporality: 1, Monotonic: true, MetricTime: 1, TimeUnixNano: 1, IngestedAt: 1, IngestedUnixNano: 1}}, 1024)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := OpenParquetStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "schema"}, nil, nil,
				[]Metric{{Name: "n", Type: "sum", Monotonic: true, Temporality: "cumulative", TimeUnixNanos: 1, IngestedAt: 1}}); err != nil {
				t.Fatal(err)
			}
			dir := store.BatchPath("schema")
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "metrics.parquet")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := test.write(path); err != nil {
				t.Fatal(err)
			}
			if opened, err := OpenParquetStore(root); !errors.Is(err, ErrUnsupportedBatchFormat) || IsBatchCorrupt(err) {
				if opened != nil {
					_ = opened.Close()
				}
				t.Fatalf("open: %v", err)
			}
			if err := ValidatePublishedBatch(dir); !errors.Is(err, ErrUnsupportedBatchFormat) || IsBatchCorrupt(err) {
				t.Fatalf("schema: %v", err)
			}
		})
	}
}

func TestPartiallyRemovedRetiredBatchDoesNotStopOpen(t *testing.T) {
	for _, suffix := range []string{".retired", ".retired-replacement"} {
		for _, missing := range []string{"spans.parquet", "logs.parquet", "metrics.parquet", "trace.fidx", "metadata.json"} {
			t.Run(suffix+"/"+missing, func(t *testing.T) {
				root := t.TempDir()
				store, err := OpenParquetStore(root)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "partial"},
					[]Span{completeTestSpan()}, []Log{{Body: "ready", TimeUnixNanos: 1}},
					[]Metric{{Name: "n", Type: "gauge", TimeUnixNanos: 1}}); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				retired := filepath.Join(store.BatchesDir(), "partial"+suffix)
				if err := os.Rename(store.BatchPath("partial"), retired); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(retired, missing)); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenParquetStore(root)
				if err != nil {
					t.Fatalf("open with partially removed retired batch: %v", err)
				}
				defer reopened.Close()
				if reopened.RowCount() != 0 || len(reopened.BatchMetadata()) != 0 {
					t.Fatal("retired batch was registered as active")
				}
			})
		}
	}
}

func TestMetricPhysicalRowsReadBackThroughDuckDB(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	metrics := make([]Metric, 777) // Cross multiple 256-row writer chunks, including a partial chunk.
	for i := range metrics {
		m := Metric{Name: "net", Type: "sum", Monotonic: i%2 == 0,
			Temporality:   []string{"cumulative", "delta", ""}[(i/2)%3],
			TimeUnixNanos: 1790856000000000009 + int64(i), IngestedAt: 1790856000000000010 + int64(i)}
		if (i/6)%2 == 0 {
			m.StartTimeUnixNanos = 1790855940000000017 + int64(i)
		}
		metrics[i] = m
	}
	if err := store.CommitBatch(t.Context(), BatchMetadata{ID: "readback"}, nil, nil, metrics); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewBatchVerifier(t.Context(), store.BatchPath("readback"))
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	rows, err := verifier.db.QueryContext(t.Context(), `SELECT metric_type, temporality, monotonic, start_time_unix_nano, time_unix_nano
		FROM read_parquet(?, hive_partitioning=false, union_by_name=false) ORDER BY time_unix_nano`,
		filepath.ToSlash(filepath.Join(store.BatchPath("readback"), "metrics.parquet")))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count >= len(metrics) {
			t.Fatal("unexpected extra metric row")
		}
		var kind, temporality string
		var monotonic bool
		var start, at int64
		if err := rows.Scan(&kind, &temporality, &monotonic, &start, &at); err != nil {
			t.Fatal(err)
		}
		want := metrics[count]
		if kind != want.Type || temporality != want.Temporality || monotonic != want.Monotonic || start != want.StartTimeUnixNanos || at != want.TimeUnixNanos {
			t.Fatalf("row %d: got (%q, %q, %t, %d, %d), want (%q, %q, %t, %d, %d)",
				count, kind, temporality, monotonic, start, at, want.Type, want.Temporality, want.Monotonic, want.StartTimeUnixNanos, want.TimeUnixNanos)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(metrics) {
		t.Fatalf("read %d metrics, want %d", count, len(metrics))
	}
}

// Format 3 had every metric column except the three fidelity columns.
type formatThreeMetricParquetRow struct {
	Namespace        string  `parquet:"namespace,dict"`
	MetricTime       int64   `parquet:"metric_time,timestamp(nanosecond)"`
	TimeUnixNano     int64   `parquet:"time_unix_nano"`
	Name             string  `parquet:"name,dict"`
	Description      string  `parquet:"description,dict"`
	Unit             string  `parquet:"unit,dict"`
	MetricType       string  `parquet:"metric_type,dict"`
	Service          string  `parquet:"service,dict"`
	Value            float64 `parquet:"value"`
	HistBoundsJSON   string  `parquet:"hist_bounds_json,dict"`
	HistCountsJSON   string  `parquet:"hist_counts_json,dict"`
	HistCount        int64   `parquet:"hist_count"`
	HistSum          float64 `parquet:"hist_sum"`
	ExemplarsJSON    string  `parquet:"exemplars_json"`
	Attributes       any     `parquet:"attributes,variant"`
	Resource         any     `parquet:"resource,variant"`
	ScopeName        string  `parquet:"scope_name,dict"`
	ScopeVersion     string  `parquet:"scope_version,dict"`
	IngestedAt       int64   `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano int64   `parquet:"ingested_unix_nano"`
}

// Keep all format-4 columns and change only temporality's physical type.
type wrongTemporalityMetricParquetRow struct {
	Namespace         string  `parquet:"namespace,dict"`
	MetricTime        int64   `parquet:"metric_time,timestamp(nanosecond)"`
	TimeUnixNano      int64   `parquet:"time_unix_nano"`
	Name              string  `parquet:"name,dict"`
	Description       string  `parquet:"description,dict"`
	Unit              string  `parquet:"unit,dict"`
	MetricType        string  `parquet:"metric_type,dict"`
	Temporality       int64   `parquet:"temporality,dict"`
	Monotonic         bool    `parquet:"monotonic"`
	StartTimeUnixNano int64   `parquet:"start_time_unix_nano"`
	Service           string  `parquet:"service,dict"`
	Value             float64 `parquet:"value"`
	HistBoundsJSON    string  `parquet:"hist_bounds_json,dict"`
	HistCountsJSON    string  `parquet:"hist_counts_json,dict"`
	HistCount         int64   `parquet:"hist_count"`
	HistSum           float64 `parquet:"hist_sum"`
	ExemplarsJSON     string  `parquet:"exemplars_json"`
	Attributes        any     `parquet:"attributes,variant"`
	Resource          any     `parquet:"resource,variant"`
	ScopeName         string  `parquet:"scope_name,dict"`
	ScopeVersion      string  `parquet:"scope_version,dict"`
	IngestedAt        int64   `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano  int64   `parquet:"ingested_unix_nano"`
}
