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
	metric := Metric{Name: "net", Type: "counter", Temporality: "delta",
		StartTimeUnixNanos: 1790855940000000017, TimeUnixNanos: 1790856000000000009, IngestedAt: 1790856000000000010}
	metric.SeriesID = MetricSeriesID(metric)
	if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: "fidelity"}, nil, nil, []Metric{metric}); err != nil {
		t.Fatal(err)
	}
	got := fileRows(t, filepath.Join(store.BatchPath("fidelity"), "metrics.parquet"), Metrics)[0]
	if got["series_id"] != metric.SeriesID || got["type"] != "counter" || got["start_time_unix_nano"] != metric.StartTimeUnixNanos {
		t.Fatalf("physical=%+v", got)
	}
	if err := ValidatePublishedBatch(store.BatchPath("fidelity")); err != nil {
		t.Fatal(err)
	}
	if store.BatchMetadata()[0].Version != 5 {
		t.Fatal("metadata is not format 5")
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
			err = store.CommitBatch(t.Context(), &BatchMetadata{ID: "old"}, []Span{completeTestSpan()}, []Log{{Body: "ready", TimeUnixNanos: 1}},
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

func TestFormatFiveRequiresMetricPhysicalSchema(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(string) error
	}{
		{"format_four_columns", func(path string) error {
			kind := str("metric_type", func(r *Metric) string { return r.Type }).dict()
			layout := metricLayout(map[string]*Column{"type": &kind, "hist_bounds": nil, "hist_counts": nil})
			return writeParquet(path, layout, []Metric{{Name: "n", Type: "counter", TimeUnixNanos: 1, IngestedAt: 1}}, 1024)
		}},
		{"temporality_required", func(path string) error {
			temporality := str("temporality", func(r *Metric) string { return r.Temporality }).dict()
			layout := metricLayout(map[string]*Column{"temporality": &temporality})
			return writeParquet(path, layout, []Metric{{Name: "n", Type: "counter", Temporality: "delta", TimeUnixNanos: 1, IngestedAt: 1}}, 1024)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := OpenParquetStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: "schema"}, nil, nil,
				[]Metric{{Name: "n", Type: "counter", Temporality: "cumulative", TimeUnixNanos: 1, IngestedAt: 1}}); err != nil {
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
				if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: "partial"},
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
		m := Metric{Name: "net", Type: []string{"counter", "updown"}[i%2],
			Temporality:   []string{"cumulative", "delta"}[(i/2)%2],
			TimeUnixNanos: 1790856000000000009 + int64(i), IngestedAt: 1790856000000000010 + int64(i)}
		if (i/6)%2 == 0 {
			m.StartTimeUnixNanos = 1790855940000000017 + int64(i)
		}
		m.Attributes = map[string]any{"series": int64(i)}
		m.SeriesID = MetricSeriesID(m)
		metrics[i] = m
	}
	if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: "readback"}, nil, nil, metrics); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewBatchVerifier(t.Context(), store.BatchPath("readback"))
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	rows, err := verifier.db.QueryContext(t.Context(), `SELECT series_id, type, temporality, start_time_unix_nano, time_unix_nano
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
		var seriesID uint64
		var kind, temporality string
		var start, at int64
		if err := rows.Scan(&seriesID, &kind, &temporality, &start, &at); err != nil {
			t.Fatal(err)
		}
		want := metrics[count]
		if seriesID != want.SeriesID || kind != want.Type || temporality != want.Temporality || start != want.StartTimeUnixNanos || at != want.TimeUnixNanos {
			t.Fatalf("row %d: got (%q, %q, %d, %d), want (%q, %q, %d, %d)",
				count, kind, temporality, start, at, want.Type, want.Temporality, want.StartTimeUnixNanos, want.TimeUnixNanos)
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

// A gauge or sum point flagged as having no recorded value stores NULL, which
// verification accepts and aggregates skip; an unflagged point keeps its value.
func TestMetricNoRecordedValueStoresNull(t *testing.T) {
	store, err := OpenParquetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var metrics []Metric
	for i, flags := range []uint32{0, metricNoRecordedValue, 0, metricNoRecordedValue | 2} {
		m := Metric{Name: "level", Type: []string{"gauge", "counter"}[i/2], Flags: flags, Value: 4,
			TimeUnixNanos: 1790856000000000000 + int64(i), IngestedAt: 1790856000000000000 + int64(i)}
		if m.Type == "counter" {
			m.Temporality = "cumulative"
		}
		m.SeriesID = MetricSeriesID(m)
		metrics = append(metrics, m)
	}
	if err := store.CommitBatch(t.Context(), &BatchMetadata{ID: "flagged"}, nil, nil, metrics); err != nil {
		t.Fatal(err)
	}
	dir := store.BatchPath("flagged")
	if err := ValidatePublishedBatch(dir); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewBatchVerifier(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	var nulls, points int
	var avg float64
	if err := verifier.db.QueryRowContext(t.Context(), `SELECT count(*) FILTER (WHERE value IS NULL AND flags & 1 = 1), count(value), avg(value)
		FROM read_parquet(?, hive_partitioning=false, union_by_name=false)`,
		filepath.ToSlash(filepath.Join(dir, "metrics.parquet"))).Scan(&nulls, &points, &avg); err != nil {
		t.Fatal(err)
	}
	if nulls != 2 || points != 2 || avg != 4 {
		t.Fatalf("flagged NULL values=%d, values=%d, avg=%v", nulls, points, avg)
	}
}

// metricLayout is the metric layout with columns replaced or, where the
// replacement is nil, removed.
func metricLayout(replace map[string]*Column) *Signal {
	s := &Signal{Name: Metrics.Name, Time: Metrics.Time, Order: Metrics.Order}
	for _, c := range Metrics.Columns {
		if r, ok := replace[c.Name]; ok {
			if r == nil {
				continue
			}
			c = *r
		}
		s.Columns = append(s.Columns, c)
	}
	s.schema = buildSchema(s)
	s.order = compileOrder(s)
	return s
}
