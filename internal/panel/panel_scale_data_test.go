//go:build readbench

package panel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	enginepolicy "github.com/labstack/fanout/internal/duckdb"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/parquet-go/parquet-go"
	"github.com/zeebo/xxh3"
)

type panelScaleOptions struct {
	SpansPerSecond           int64
	Hours, Hosts, Containers int
	OutputDir                string
}

type panelScaleCounts struct {
	Spans   int64 `json:"spans"`
	Logs    int64 `json:"logs"`
	Metrics int64 `json:"metrics"`
}

type panelScaleFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type panelScaleMetricCensus struct {
	Scope        string           `json:"scope"`
	ScopeVersion string           `json:"scope_version"`
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	Points       int64            `json:"points"`
	Series       int64            `json:"series"`
	Cadences     map[string]int64 `json:"cadence_seconds"`
	Gaps         int64            `json:"gaps"`
	BoundCounts  map[string]int64 `json:"histogram_bound_counts"`
}

type panelScaleManifest struct {
	Version              int                      `json:"version"`
	Complete             bool                     `json:"complete"`
	DatasetID            string                   `json:"dataset_id"`
	SourceHash           string                   `json:"source_hash"`
	BatchFormat          int                      `json:"batch_format"`
	SpansPerSecond       int64                    `json:"spans_per_second"`
	Hours                int                      `json:"hours"`
	Hosts                int                      `json:"hosts"`
	ContainersPerHost    int                      `json:"containers_per_host"`
	Start                time.Time                `json:"start"`
	End                  time.Time                `json:"end"`
	LookbackStart        time.Time                `json:"lookback_start"`
	Counts               panelScaleCounts         `json:"counts"`
	LookbackCounts       panelScaleCounts         `json:"lookback_counts"`
	Bytes                int64                    `json:"bytes"`
	Files                int                      `json:"files"`
	Services             []string                 `json:"services"`
	Namespaces           []string                 `json:"namespaces"`
	SourceStart          time.Time                `json:"source_start"`
	SourceEnd            time.Time                `json:"source_end"`
	RequestedSpans       int64                    `json:"requested_spans"`
	CountTolerance       int64                    `json:"count_tolerance"`
	ActualSpansPerSecond float64                  `json:"actual_spans_per_second"`
	EstimatedBytes       uint64                   `json:"estimated_bytes"`
	ProjectedCounts      panelScaleCounts         `json:"projected_counts"`
	Inventory            []panelScaleFile         `json:"inventory"`
	Census               []panelScaleMetricCensus `json:"metric_census"`
	Distribution         map[string]int64         `json:"span_distribution"`
	CounterSeams         string                   `json:"counter_seams"`
}

func panelScaleRewriteMetric(m telemetry.Metric, namespace string, host, container int) telemetry.Metric {
	resource := make(map[string]any, len(m.Resource)+4)
	for k, v := range m.Resource {
		resource[k] = v
	}
	m.Resource, m.Namespace = resource, namespace
	resource["service.namespace"] = namespace
	if host >= 0 {
		resource["host.name"] = fmt.Sprintf("bench-host-%02d", host)
	}
	if container >= 0 {
		resource["container.id"] = fmt.Sprintf("bench-h%02d-c%02d", host, container)
		resource["container.name"] = fmt.Sprintf("bench-container-%02d-%02d", host, container)
	}
	m.SeriesID = telemetry.MetricSeriesID(m)
	return m
}

func panelScaleShift(value, shift int64) (int64, error) {
	if shift > 0 && value > math.MaxInt64-shift || shift < 0 && value < math.MinInt64-shift {
		return 0, errors.New("signed timestamp shift overflow")
	}
	return value + shift, nil
}

func panelScaleDiskCheck(free, estimated uint64) error {
	if estimated > math.MaxUint64/2 || free < estimated*2 {
		return errors.New("insufficient disk: twice estimated output required")
	}
	return nil
}

func panelScaleAvailableDisk(dir string) (uint64, error) {
	parent, err := filepath.Abs(dir)
	if err != nil {
		return 0, errors.New("cannot resolve output parent")
	}
	for {
		_, err := os.Stat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parent) == parent {
			return 0, errors.New("cannot inspect output parent")
		}
		parent = filepath.Dir(parent)
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(parent, &stat); err != nil {
		return 0, errors.New("cannot check available output disk")
	}
	if stat.Bsize <= 0 || uint64(stat.Bavail) > math.MaxUint64/uint64(stat.Bsize) {
		return 0, errors.New("available disk overflow")
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

func panelScaleRequireDisk(dir string, estimated uint64) error {
	free, err := panelScaleAvailableDisk(dir)
	if err != nil {
		return err
	}
	return panelScaleDiskCheck(free, estimated)
}

func panelScaleOptionsFromEnv() (panelScaleOptions, error) {
	var o panelScaleOptions
	if os.Getenv("FANOUT_PANEL_BENCH_VOLUME") != "" {
		return o, errors.New("scale parameters cannot be combined with legacy volume")
	}
	parse := func(name string) (int64, error) {
		n, err := strconv.ParseInt(os.Getenv(name), 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", name)
		}
		return n, nil
	}
	var err error
	if o.SpansPerSecond, err = parse("FANOUT_PANEL_BENCH_SPANS_PER_SECOND"); err != nil {
		return o, err
	}
	for name, dst := range map[string]*int{"FANOUT_PANEL_BENCH_HOURS": &o.Hours, "FANOUT_PANEL_BENCH_HOSTS": &o.Hosts, "FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST": &o.Containers} {
		n, err := parse(name)
		if err != nil {
			return o, err
		}
		if n > int64(math.MaxInt) {
			return o, errors.New("scale option integer overflow")
		}
		*dst = int(n)
	}
	o.OutputDir = os.Getenv("FANOUT_PANEL_BENCH_OUTPUT_DIR")
	return o, panelScaleValidateOptions(o)
}

func panelScaleValidateOptions(o panelScaleOptions) error {
	if o.SpansPerSecond <= 0 || o.Hours <= 0 || o.Hosts <= 0 || o.Containers <= 0 || o.OutputDir == "" {
		return errors.New("all scale options and an absent output directory are required")
	}
	if int64(o.Hours) > (math.MaxInt64-int64(10*time.Minute))/int64(time.Hour) || o.SpansPerSecond > math.MaxInt64/(int64(o.Hours)*3600) || o.Hosts > math.MaxInt/o.Containers {
		return errors.New("scale duration or count overflow")
	}
	return nil
}

// Resolve existing ancestors, including symlinks, even for an absent output.
func panelScaleResolved(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(absolute)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(absolute) == absolute {
			return "", errors.New("cannot resolve dataset directory")
		}
		missing = append(missing, filepath.Base(absolute))
		absolute = filepath.Dir(absolute)
	}
}

func panelScalePaths(input, output string) error {
	a, err := panelScaleResolved(input)
	if err != nil {
		return err
	}
	b, err := panelScaleResolved(output)
	if err != nil {
		return err
	}
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if within(a, b) || within(b, a) {
		return errors.New("source and output directories must not overlap")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output already exists; choose an absent directory or inspect the interrupted dataset")
	}
	return nil
}

func (r panelScaleMetricRow) metric() telemetry.Metric {
	attributes, _ := r.Attributes.(map[string]any)
	resource, _ := r.Resource.(map[string]any)
	return telemetry.Metric{SeriesID: r.SeriesID, Namespace: r.Namespace, ServiceName: r.Service, Name: r.Name, Unit: r.Unit, ScopeName: r.ScopeName, ScopeVersion: r.ScopeVersion, Attributes: attributes, Resource: resource,
		Type: r.MetricType, Monotonic: r.Monotonic, Temporality: r.Temporality, StartTimeUnixNanos: r.StartTimeUnixNano, TimeUnixNanos: r.TimeUnixNano}
}

type panelScaleRowRewrite struct {
	Replica, Host          int
	Infrastructure         bool
	Containers             map[string]int
	Partial                bool
	TraceCutoff            string
	Fraction               float64
	Counts, LookbackCounts *panelScaleCounts
	WindowStart            time.Time
}

func (o *panelScaleRowRewrite) row(schema *parquet.Schema, row parquet.Row, signal string) (parquet.Row, bool, error) {
	rewrite := func(namespace string, resource any, container int) (string, map[string]any) {
		if o.Infrastructure {
			namespace += "_infra"
		} else {
			namespace = fmt.Sprintf("%s_replica%04d", namespace, o.Replica)
		}
		m, _ := resource.(map[string]any)
		got := panelScaleRewriteMetric(telemetry.Metric{Resource: m}, namespace, o.Host, container)
		return got.Namespace, got.Resource
	}

	switch signal {
	case "metrics":
		var r panelScaleMetricRow
		if err := schema.Reconstruct(&r, row); err != nil {
			return nil, false, err
		}
		infra := panelScaleInfrastructure(r.ScopeName)
		if infra != o.Infrastructure {
			return nil, false, nil
		}
		container := -1
		if strings.HasSuffix(r.ScopeName, "docker_stats") {
			resource, _ := r.Resource.(map[string]any)
			id, _ := resource["container.id"].(string)
			var ok bool
			container, ok = o.Containers[id]
			if !ok {
				return nil, false, errors.New("unvalidated source container tuple")
			}
		}
		if o.Partial && float64(r.SeriesID)/float64(math.MaxUint64) >= o.Fraction {
			return nil, false, nil
		}
		namespace, _ := rewrite(r.Namespace, r.Resource, container)
		m := panelScaleRewriteMetric(r.metric(), namespace, o.Host, container)
		r.Namespace, r.Resource, r.SeriesID = m.Namespace, m.Resource, m.SeriesID
		return schema.Deconstruct(nil, &r), true, nil
	case "spans":
		var r panelScaleSpanRow
		if err := schema.Reconstruct(&r, row); err != nil {
			return nil, false, err
		}
		if !o.keepTrace(r.TraceID, row) {
			return nil, false, nil
		}
		r.Namespace, r.Resource = rewrite(r.Namespace, r.Resource, -1)
		return schema.Deconstruct(nil, &r), true, nil
	case "logs":
		var r panelScaleLogRow
		if err := schema.Reconstruct(&r, row); err != nil {
			return nil, false, err
		}
		if !o.keepTrace(r.TraceID, row) {
			return nil, false, nil
		}
		r.Namespace, r.Resource = rewrite(r.Namespace, r.Resource, -1)
		return schema.Deconstruct(nil, &r), true, nil
	}
	return nil, false, errors.New("unknown scale signal")
}

func panelScaleInfrastructure(scope string) bool {
	return scope == "hostmetrics" || strings.HasSuffix(scope, "/hostmetrics") || scope == "docker_stats" || strings.HasSuffix(scope, "/docker_stats")
}

func panelScaleDB(ctx context.Context, root string) (*sql.DB, error) {
	db, err := sql.Open("duckdb", "?threads=2&memory_limit=256MB")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"SET TimeZone='UTC'", "SET temp_directory=''"} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			break
		}
	}
	if err == nil {
		err = enginepolicy.ConfigurePolicy(ctx, db, root)
	}
	if err != nil {
		db.Close()
		return nil, errors.New("cannot initialize bounded scale reader")
	}
	return db, nil
}

func panelScaleRelation(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "'" + strings.ReplaceAll(filepath.ToSlash(p), "'", "''") + "'"
	}
	return "read_parquet([" + strings.Join(quoted, ",") + "],union_by_name=false,hive_partitioning=false)"
}

// Footer statistics use signed nanoseconds. Unknown statistics force decoding.
func panelScaleBounds(ctx context.Context, db *sql.DB, path, event string) (low, high int64, err error) {
	in, err := os.Open(path)
	if err != nil {
		return 0, 0, errors.New("cannot read source footer")
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	stat, err := in.Stat()
	if err != nil {
		return 0, 0, err
	}
	file, err := parquet.OpenFile(in, stat.Size())
	if err != nil {
		return 0, 0, err
	}
	low, high = math.MaxInt64, math.MinInt64
	known := true
	for _, group := range file.Metadata().RowGroups {
		found := false
		for _, chunk := range group.Columns {
			if len(chunk.MetaData.PathInSchema) != 1 || chunk.MetaData.PathInSchema[0] != event {
				continue
			}
			stats := chunk.MetaData.Statistics
			if len(stats.MinValue) != 8 || len(stats.MaxValue) != 8 {
				known = false
				break
			}
			a, b := int64(binary.LittleEndian.Uint64(stats.MinValue)), int64(binary.LittleEndian.Uint64(stats.MaxValue))
			if a > b {
				known = false
				break
			}
			low, high = min(low, a), max(high, b)
			found = true
		}
		known = known && found
	}
	if !known || low > high {
		err = db.QueryRowContext(ctx, "SELECT min(epoch_ns("+event+")),max(epoch_ns("+event+")) FROM "+panelScaleRelation([]string{path})).Scan(&low, &high)
	}
	return
}

// Hash the inventory in a streaming read; no private source copy is made.
func panelScaleInventory(root string) ([]panelScaleFile, string, error) {
	return panelScaleInventoryFiles(root, false)
}

// Completion covers immutable batches. The engine owns disposable schema,
// staging and cache state, so opening the completed output can recreate them.
func panelScalePublishedInventory(root string) ([]panelScaleFile, string, error) {
	return panelScaleInventoryFiles(root, true)
}

func panelScaleInventoryFiles(root string, published bool) ([]panelScaleFile, string, error) {
	var out []panelScaleFile
	hash := sha256.New()
	walkRoot := root
	if published {
		walkRoot = filepath.Join(root, "parquet", "batches")
	}
	err := filepath.WalkDir(walkRoot, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return errors.New("cannot read dataset inventory")
		}
		if e.IsDir() {
			if published && e.Name() == telemetry.SchemaBatch {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("dataset inventory requires regular files")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return errors.New("cannot hash dataset file")
		}
		fileHash := sha256.New()
		_, readErr := io.Copy(fileHash, in)
		if err := errors.Join(readErr, in.Close()); err != nil {
			return errors.New("cannot hash dataset file")
		}
		entry := panelScaleFile{filepath.ToSlash(rel), info.Size(), hex.EncodeToString(fileHash.Sum(nil))}
		out = append(out, entry)
		fmt.Fprintf(hash, "%s\x00%d\x00%s\n", entry.Name, entry.Bytes, entry.SHA256)
		return nil
	})
	return out, hex.EncodeToString(hash.Sum(nil)), err
}

func TestPanelScaleMetricIdentities(t *testing.T) {
	m := telemetry.Metric{Namespace: "original", ServiceName: "collector", Name: "system.cpu.time",
		Unit: "s", ScopeName: "hostmetrics", ScopeVersion: "v1", Type: "sum", Monotonic: true,
		Temporality: "cumulative", StartTimeUnixNanos: -60000000017, TimeUnixNanos: -17,
		Attributes: map[string]any{"state": "idle", "integer": int64(7), "bytes": []byte{0, 255}},
		Resource:   map[string]any{"host.name": "source", "nested": map[string]any{"ok": true}}}
	m.SeriesID = telemetry.MetricSeriesID(m)
	seen := map[uint64]bool{}
	for h := range 2 {
		for c := range 2 {
			got := panelScaleRewriteMetric(m, "bench", h, c)
			if got.SeriesID == m.SeriesID || seen[got.SeriesID] || got.SeriesID != telemetry.MetricSeriesID(got) {
				t.Fatal("series identity was reused or not recomputed")
			}
			seen[got.SeriesID] = true
			if got.StartTimeUnixNanos != m.StartTimeUnixNanos || got.TimeUnixNanos != -17 ||
				got.Type != "sum" || !got.Monotonic || got.Temporality != "cumulative" ||
				!reflect.DeepEqual(got.Attributes, m.Attributes) || !reflect.DeepEqual(got.Resource["nested"], m.Resource["nested"]) {
				t.Fatal("rewrite changed fidelity, typed values or signed time")
			}
		}
	}
	namespaceOnly := panelScaleRewriteMetric(m, "other", -1, -1)
	if namespaceOnly.SeriesID == m.SeriesID || namespaceOnly.Resource["host.name"] != "source" || m.Resource["host.name"] != "source" {
		t.Fatal("namespace rewrite or source preservation failed")
	}
}

func TestPanelScaleDiskRefusal(t *testing.T) {
	if panelScaleDiskCheck(93, 47) == nil {
		t.Fatal("accepted insufficient disk")
	}
	if err := panelScaleDiskCheck(94, 47); err != nil {
		t.Fatal(err)
	}
	if panelScaleDiskCheck(math.MaxUint64, math.MaxUint64) == nil {
		t.Fatal("overflow bypassed refusal")
	}
	parent := filepath.Join(t.TempDir(), "absent", "output")
	if panelScaleRequireDisk(parent, math.MaxUint64) == nil {
		t.Fatal("real disk provider bypassed overflow refusal")
	}
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disk check created output")
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if panelScaleRequireDisk(filepath.Join(file, "child"), 1) == nil {
		t.Fatal("stat error did not fail closed")
	}
}

// Physical mirror only; every read/write uses the validated input schema.
type panelScaleSpanRow struct {
	Namespace        string  `parquet:"namespace"`
	TraceID          string  `parquet:"trace_id"`
	TraceHash        uint64  `parquet:"_trace_hash"`
	SpanID           string  `parquet:"span_id"`
	ParentSpanID     string  `parquet:"parent_span_id"`
	Service          string  `parquet:"service"`
	Operation        string  `parquet:"operation"`
	Kind             string  `parquet:"kind"`
	StartTime        int64   `parquet:"start_time,timestamp(nanosecond)"`
	EndTime          int64   `parquet:"end_time,timestamp(nanosecond)"`
	StartUnixNano    int64   `parquet:"start_unix_nano"`
	EndUnixNano      int64   `parquet:"end_unix_nano"`
	DurationMS       float64 `parquet:"duration_ms"`
	Status           string  `parquet:"status"`
	StatusMessage    string  `parquet:"status_message"`
	Resource         any     `parquet:"resource,variant"`
	Attributes       any     `parquet:"attributes,variant"`
	EventsJSON       string  `parquet:"events_json"`
	LinksJSON        string  `parquet:"links_json"`
	TraceState       string  `parquet:"trace_state,dict"`
	Flags            int64   `parquet:"flags"`
	ScopeName        string  `parquet:"scope_name,dict"`
	ScopeVersion     string  `parquet:"scope_version,dict"`
	IngestedAt       int64   `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano int64   `parquet:"ingested_unix_nano"`
	HTTPMethod       string  `parquet:"http_method,dict"`
	HTTPStatusCode   string  `parquet:"http_status_code,dict"`
	HTTPRoute        string  `parquet:"http_route,dict"`
	DBSystem         string  `parquet:"db_system,dict"`
	RPCMethod        string  `parquet:"rpc_method,dict"`
	RPCService       string  `parquet:"rpc_service,dict"`
	PeerService      string  `parquet:"peer_service,dict"`
	ServiceVersion   string  `parquet:"service_version,dict"`
	DeploymentEnv    string  `parquet:"deployment_env,dict"`
	ExceptionType    string  `parquet:"exception_type,dict"`
	ExceptionMessage string  `parquet:"exception_message"`
}

// Physical mirror only; every read/write uses the validated input schema.
type panelScaleLogRow struct {
	Namespace            string `parquet:"namespace,dict"`
	LogTime              int64  `parquet:"log_time,timestamp(nanosecond)"`
	ObservedTime         int64  `parquet:"observed_time,timestamp(nanosecond)"`
	TimeUnixNano         int64  `parquet:"time_unix_nano"`
	ObservedTimeUnixNano int64  `parquet:"observed_time_unix_nano"`
	Severity             string `parquet:"severity,dict"`
	SeverityNumber       int64  `parquet:"severity_number"`
	Body                 string `parquet:"body"`
	Service              string `parquet:"service,dict"`
	TraceID              string `parquet:"trace_id"`
	SpanID               string `parquet:"span_id"`
	Flags                int64  `parquet:"flags"`
	Resource             any    `parquet:"resource,variant"`
	Attributes           any    `parquet:"attributes,variant"`
	ScopeName            string `parquet:"scope_name,dict"`
	ScopeVersion         string `parquet:"scope_version,dict"`
	IngestedAt           int64  `parquet:"ingested_at,timestamp(nanosecond)"`
	IngestedUnixNano     int64  `parquet:"ingested_unix_nano"`
	BodyTemplate         string `parquet:"body_template,dict"`
}

// Physical mirror only; every read/write uses the validated input schema.
type panelScaleMetricRow struct {
	SeriesID          uint64  `parquet:"series_id"`
	Namespace         string  `parquet:"namespace,dict"`
	MetricTime        int64   `parquet:"metric_time,timestamp(nanosecond)"`
	TimeUnixNano      int64   `parquet:"time_unix_nano"`
	Name              string  `parquet:"name,dict"`
	Description       string  `parquet:"description,dict"`
	Unit              string  `parquet:"unit,dict"`
	MetricType        string  `parquet:"metric_type,dict"`
	Temporality       string  `parquet:"temporality,dict"`
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

type panelScaleSegment struct {
	SourceStart, SourceEnd, At time.Time
	PartialMode                int // 0 omit, 1 full temporal slice, 2 whole-trace cutoff
	PartialCutoff              string
	PartialFraction            float64
}
type panelScalePlan struct {
	Metadata     []telemetry.BatchMetadata
	Segments     []panelScaleSegment
	Manifest     panelScaleManifest
	Replicas     int
	Partial      bool
	Containers   map[string]int
	Distribution map[string]int64
}

func panelScaleCensus(ctx context.Context, db *sql.DB, start, end time.Time, containers, expectedHosts int, strict bool) ([]panelScaleMetricCensus, map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT scope_name,scope_version,name,
 CASE WHEN metric_type='sum' AND monotonic THEN 'counter' WHEN metric_type='sum' THEN 'updown' ELSE metric_type END,
 count(*),count(DISTINCT series_id)
 FROM scale_metrics WHERE metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
 GROUP BY ALL ORDER BY scope_name,scope_version,name,4`, start, end)
	if err != nil {
		return nil, nil, err
	}
	var census []panelScaleMetricCensus
	for rows.Next() {
		c := panelScaleMetricCensus{Cadences: map[string]int64{}, BoundCounts: map[string]int64{}}
		if err := rows.Scan(&c.Scope, &c.ScopeVersion, &c.Name, &c.Kind, &c.Points, &c.Series); err != nil {
			rows.Close()
			return nil, nil, err
		}
		census = append(census, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	if len(census) == 0 {
		return nil, nil, errors.New("source needs metric coverage")
	}
	// Bound native ordering to one metric/scope at a time, rather than sorting
	// the entire capture. No spill or configuration changes are permitted.
	for i := range census {
		c := &census[i]
		rows, err := db.QueryContext(ctx, `WITH points AS (
 SELECT series_id,epoch_ns(metric_time)-lag(epoch_ns(metric_time)) OVER(PARTITION BY series_id ORDER BY metric_time) AS delta
 FROM scale_metrics WHERE scope_name=? AND scope_version=? AND name=?
 AND metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 steps AS (SELECT *,min(delta) FILTER(WHERE delta>0) OVER(PARTITION BY series_id) AS cadence FROM points)
 SELECT delta,cadence,count(*) FROM steps WHERE delta IS NOT NULL GROUP BY ALL ORDER BY delta,cadence`, c.Scope, c.ScopeVersion, c.Name, start, end)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var delta, base, count int64
			if err := rows.Scan(&delta, &base, &count); err != nil {
				rows.Close()
				return nil, nil, err
			}
			if panelScaleInfrastructure(c.Scope) && (base != int64(10*time.Second) && base != int64(time.Minute) || delta <= 0 || delta%base != 0) {
				rows.Close()
				return nil, nil, errors.New("infrastructure series requires its observed 10s or 60s cadence")
			}
			c.Cadences[strconv.FormatFloat(float64(delta)/float64(time.Second), 'f', -1, 64)] += count
			if delta > base {
				c.Gaps += count
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, err
		}
		if panelScaleInfrastructure(c.Scope) && len(c.Cadences) == 0 {
			return nil, nil, errors.New("infrastructure cadence cannot be established")
		}
		if c.Kind == "histogram" || c.Kind == "exponential_histogram" {
			rows, err := db.QueryContext(ctx, `SELECT json_array_length(hist_bounds_json),count(*) FROM scale_metrics WHERE scope_name=? AND scope_version=? AND name=? AND metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY 1 ORDER BY 1`, c.Scope, c.ScopeVersion, c.Name, start, end)
			if err != nil {
				return nil, nil, err
			}
			for rows.Next() {
				var bounds, count int64
				if err := rows.Scan(&bounds, &count); err != nil {
					rows.Close()
					return nil, nil, err
				}
				c.BoundCounts[strconv.FormatInt(bounds, 10)] = count
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return nil, nil, err
			}
		}
	}
	hosts := map[string]bool{}
	tuples := map[string]string{}
	rows, err = db.QueryContext(ctx, `SELECT DISTINCT scope_name,resource['host.name']::VARCHAR,resource['container.id']::VARCHAR,resource['container.name']::VARCHAR FROM scale_metrics WHERE (scope_name='hostmetrics' OR ends_with(scope_name,'/hostmetrics') OR scope_name='docker_stats' OR ends_with(scope_name,'/docker_stats')) AND metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, start, end)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var scope string
		var host, id, name sql.NullString
		if err := rows.Scan(&scope, &host, &id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if !host.Valid || host.String == "" {
			rows.Close()
			return nil, nil, errors.New("infrastructure is missing a source host identity")
		}
		hosts[host.String] = true
		if strings.HasSuffix(scope, "docker_stats") {
			if !id.Valid || id.String == "" || !name.Valid || name.String == "" {
				rows.Close()
				return nil, nil, errors.New("container resource tuple is incomplete")
			}
			if old, ok := tuples[id.String]; ok && old != name.String {
				rows.Close()
				return nil, nil, errors.New("container resource tuple is inconsistent")
			}
			tuples[id.String] = name.String
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	if len(hosts) != expectedHosts || len(tuples) != containers {
		return nil, nil, errors.New("source requires one host and the requested distinct container sets")
	}
	ids := make([]string, 0, len(tuples))
	for id := range tuples {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	mapping := map[string]int{}
	for i, id := range ids {
		mapping[id] = i
	}
	if strict {
		required := map[string]string{"system.cpu.time": "counter", "system.network.io": "counter", "system.memory.usage": "updown", "system.filesystem.usage": "updown", "container.cpu.utilization": "gauge"}
		memory, explicit, exponential := false, false, false
		for _, c := range census {
			if kind, ok := required[c.Name]; ok && kind == c.Kind {
				delete(required, c.Name)
			}
			memory = memory || strings.HasPrefix(c.Name, "container.memory.usage") && c.Kind == "gauge"
			explicit = explicit || c.Kind == "histogram"
			exponential = exponential || c.Kind == "exponential_histogram"
		}
		if len(required) != 0 || !memory || !explicit || !exponential {
			return nil, nil, errors.New("S18 source lacks required metric names or semantic kinds; supply complete format-4 coverage")
		}
	}
	return census, mapping, nil
}

func panelScalePreparePlan(ctx context.Context, input string, o panelScaleOptions) (plan panelScalePlan, err error) {
	if err = panelScaleValidateOptions(o); err != nil {
		return
	}
	if err = panelScalePaths(input, o.OutputDir); err != nil {
		return
	}
	plan.Metadata, err = panelReplayMetadata(input)
	if err != nil {
		return
	}
	// Metadata and exact format-4 schemas are accepted before any output exists.
	issues, err := telemetrystore.VerifyBatches(input)
	if err != nil || len(issues) != 0 {
		return plan, errors.New("source format-4 verification failed")
	}
	_, sourceHash, err := panelScaleInventory(input)
	if err != nil {
		return plan, err
	}
	db, err := panelScaleDB(ctx, input)
	if err != nil {
		return plan, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	paths := map[string][]string{}
	low, high := int64(math.MaxInt64), int64(math.MinInt64)
	for i := range plan.Metadata {
		m := &plan.Metadata[i]
		for _, signal := range []string{"spans", "logs", "metrics"} {
			count := map[string]int{"spans": m.Spans, "logs": m.Logs, "metrics": m.Metrics}[signal]
			if count == 0 {
				continue
			}
			path := filepath.Join(input, "parquet", "batches", m.ID+telemetry.BatchSuffix, signal+".parquet")
			paths[signal] = append(paths[signal], path)
			event := map[string]string{"spans": "start_time", "logs": "log_time", "metrics": "metric_time"}[signal]
			a, b, e := panelScaleBounds(ctx, db, path, event)
			if e != nil {
				return plan, e
			}
			bounds := map[string]*telemetry.TimeRange{"spans": &m.SpanTime, "logs": &m.LogTime, "metrics": &m.MetricTime}[signal]
			*bounds = telemetry.TimeRange{MinNanos: a, MaxNanos: b, Known: true}
			if signal == "spans" {
				low, high = min(low, a), max(high, b)
			}
		}
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if len(paths[signal]) == 0 {
			return plan, errors.New("representative source requires all three signals")
		}
		if _, err := db.ExecContext(ctx, "CREATE TEMP VIEW scale_"+signal+" AS SELECT * FROM "+panelScaleRelation(paths[signal])); err != nil {
			return plan, err
		}
	}
	if low > high || high == math.MaxInt64 {
		return plan, errors.New("source has no usable span event bounds")
	}
	first := time.Unix(0, low).UTC().Truncate(time.Hour)
	end := time.Unix(0, high).UTC().Truncate(time.Hour)
	// Short captures retain their complete hour envelope, as the M4 harness does.
	if end.Sub(first) < time.Hour {
		end = end.Add(time.Hour)
	}
	period := min(time.Duration(o.Hours)*time.Hour, 24*time.Hour, end.Sub(first))
	if period <= 0 {
		return plan, errors.New("source window is empty")
	}
	start := end.Add(-period)
	if !end.Equal(time.Unix(0, end.UnixNano())) || !start.Equal(time.Unix(0, start.UnixNano())) {
		return plan, errors.New("source UTC window exceeds signed nanoseconds")
	}
	windowNanos, e := panelScaleShift(end.UnixNano(), -int64(time.Duration(o.Hours)*time.Hour))
	if e != nil {
		return plan, e
	}
	windowStart := time.Unix(0, windowNanos).UTC()
	if _, err := panelScaleShift(windowStart.UnixNano(), -int64(10*time.Minute)); err != nil {
		return plan, err
	}
	plan.Manifest = panelScaleManifest{Version: 1, BatchFormat: 4, SourceHash: sourceHash, SpansPerSecond: o.SpansPerSecond, Hours: o.Hours, Hosts: o.Hosts, ContainersPerHost: o.Containers, Start: windowStart, End: end, LookbackStart: windowStart.Add(-10 * time.Minute), SourceStart: start, SourceEnd: end, RequestedSpans: o.SpansPerSecond * int64(o.Hours) * 3600, CounterSeams: "Temporal tiles retain source cumulative values and start times; seams may reset or decrease. No values or missing points are fabricated. Partial replicas retain complete trace IDs and app series selected deterministically by source series ID; unlinked logs use a deterministic row hash."}
	plan.Manifest.Census, plan.Containers, err = panelScaleCensus(ctx, db, start, end, o.Containers, 1, o.Hosts == 20 && o.Containers == 24)
	if err != nil {
		return plan, err
	}
	var baseSpans int64
	// Align backwards so the newest complete source interval is always used.
	for at := end; at.After(windowStart); {
		duration := min(period, at.Sub(windowStart))
		seg := panelScaleSegment{SourceStart: end.Add(-duration), SourceEnd: end, At: at.Add(-duration)}
		plan.Segments = append(plan.Segments, seg)
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scale_spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, seg.SourceStart, seg.SourceEnd).Scan(&count); err != nil {
			return plan, err
		}
		baseSpans += count
		at = seg.At
	}
	if baseSpans == 0 {
		return plan, errors.New("selected source window contains no spans")
	}
	full := plan.Manifest.RequestedSpans / baseSpans
	if full > int64(math.MaxInt-o.Hosts-1) {
		return plan, errors.New("replica count overflow")
	}
	plan.Replicas = int(full)
	remaining := plan.Manifest.RequestedSpans % baseSpans
	plan.Partial = remaining != 0
	// Select output trace identities in (tile, source trace ID) order. Each
	// output trace is selected whole, so the final excess is at most one trace,
	// even when the capture repeats across multiple temporal tiles.
	for i := range plan.Segments {
		seg := &plan.Segments[i]
		traceSQL := `WITH traces AS (SELECT trace_id,count(*) AS n FROM scale_spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY trace_id) `
		var largest, total int64
		if err := db.QueryRowContext(ctx, traceSQL+`SELECT coalesce(max(n),0),coalesce(sum(n),0) FROM traces`, seg.SourceStart, seg.SourceEnd).Scan(&largest, &total); err != nil {
			return plan, err
		}
		plan.Manifest.CountTolerance = max(plan.Manifest.CountTolerance, largest)
		if remaining <= 0 || total == 0 {
			continue
		}
		if remaining >= total {
			seg.PartialMode = 1
			seg.PartialFraction = 1
			remaining -= total
			continue
		}
		seg.PartialMode = 2
		seg.PartialFraction = float64(remaining) / float64(total)
		if err := db.QueryRowContext(ctx, traceSQL+`,ordered AS (SELECT trace_id,n,sum(n) OVER(ORDER BY trace_id ROWS UNBOUNDED PRECEDING) AS total FROM traces) SELECT trace_id FROM ordered WHERE total>=? ORDER BY trace_id LIMIT 1`, seg.SourceStart, seg.SourceEnd, remaining).Scan(&seg.PartialCutoff); err != nil {
			return plan, err
		}
		remaining = 0
	}
	if plan.Manifest.RequestedSpans > math.MaxInt64-plan.Manifest.CountTolerance {
		return plan, errors.New("whole-trace count tolerance overflows target")
	}
	if plan.Partial {
		plan.Replicas++
	}
	// Compute source-derived projections by using the identical clipping and
	// whole-trace rule used by the writer. Infrastructure gets one clone/host.
	plan.Manifest.ProjectedCounts.Spans = baseSpans * full
	for _, seg := range plan.Segments {
		if plan.Partial && seg.PartialMode != 0 {
			var count int64
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scale_spans WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (? OR trace_id<=?)`, seg.SourceStart, seg.SourceEnd, seg.PartialMode == 1, seg.PartialCutoff).Scan(&count); err != nil {
				return plan, err
			}
			plan.Manifest.ProjectedCounts.Spans += count
		}
		var logs, app, infra int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scale_logs WHERE log_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND log_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, seg.SourceStart, seg.SourceEnd).Scan(&logs); err != nil {
			return plan, err
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE NOT (scope_name='hostmetrics' OR ends_with(scope_name,'/hostmetrics') OR scope_name='docker_stats' OR ends_with(scope_name,'/docker_stats'))),count(*) FILTER(WHERE scope_name='hostmetrics' OR ends_with(scope_name,'/hostmetrics') OR scope_name='docker_stats' OR ends_with(scope_name,'/docker_stats')) FROM scale_metrics WHERE metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, seg.SourceStart, seg.SourceEnd).Scan(&app, &infra); err != nil {
			return plan, err
		}
		if logs > math.MaxInt64/int64(plan.Replicas) || app > math.MaxInt64/int64(plan.Replicas) || infra > math.MaxInt64/int64(o.Hosts) {
			return plan, errors.New("projected signal count overflow")
		}
		logCount, appCount, infraCount := logs*full, app*full, infra*int64(o.Hosts)
		if plan.Partial && seg.PartialMode != 0 {
			rewrite := panelScaleRowRewrite{Partial: seg.PartialMode == 2, TraceCutoff: seg.PartialCutoff, Fraction: seg.PartialFraction}
			selectedLogs, e := panelScaleSelectedCount(input, plan.Metadata, seg, "logs", rewrite)
			if e != nil {
				return plan, e
			}
			selectedApp, e := panelScaleSelectedCount(input, plan.Metadata, seg, "metrics", rewrite)
			if e != nil {
				return plan, e
			}
			if logCount > math.MaxInt64-selectedLogs || appCount > math.MaxInt64-selectedApp {
				return plan, errors.New("projected selected count overflow")
			}
			logCount += selectedLogs
			appCount += selectedApp
		}
		if plan.Manifest.ProjectedCounts.Logs > math.MaxInt64-logCount || appCount > math.MaxInt64-infraCount || plan.Manifest.ProjectedCounts.Metrics > math.MaxInt64-appCount-infraCount {
			return plan, errors.New("projected signal count overflow")
		}
		plan.Manifest.ProjectedCounts.Logs += logCount
		plan.Manifest.ProjectedCounts.Metrics += appCount + infraCount
	}
	plan.Distribution = map[string]int64{}
	for _, seg := range plan.Segments {
		all, e := panelScaleTrafficDistribution(ctx, db, "scale_spans", seg.SourceStart, seg.SourceEnd, false, "")
		if e != nil {
			return plan, e
		}
		for key, n := range all {
			if n > math.MaxInt64/max(full, 1) || plan.Distribution[key] > math.MaxInt64-n*full {
				return plan, errors.New("traffic distribution count overflow")
			}
			plan.Distribution[key] += n * full
		}
		if plan.Partial && seg.PartialMode != 0 {
			selected, e := panelScaleTrafficDistribution(ctx, db, "scale_spans", seg.SourceStart, seg.SourceEnd, seg.PartialMode == 2, seg.PartialCutoff)
			if e != nil {
				return plan, e
			}
			for key, n := range selected {
				if plan.Distribution[key] > math.MaxInt64-n {
					return plan, errors.New("traffic distribution count overflow")
				}
				plan.Distribution[key] += n
			}
		}
	}
	// Price real predecessor points too. A temporal predecessor repeats actual
	// source points; it never invents a regular schedule in capture gaps.
	lookback := panelScaleSegment{SourceStart: end.Add(-10 * time.Minute), SourceEnd: end, At: windowStart.Add(-10 * time.Minute)}
	var predecessor int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scale_metrics WHERE metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, start.Add(-10*time.Minute), start).Scan(&predecessor); err != nil {
		return plan, err
	}
	if predecessor > 0 {
		lookback.SourceStart, lookback.SourceEnd = start.Add(-10*time.Minute), start
	}
	earliest := plan.Segments[len(plan.Segments)-1]
	lookback.PartialMode, lookback.PartialCutoff, lookback.PartialFraction = earliest.PartialMode, earliest.PartialCutoff, earliest.PartialFraction
	plan.Segments = append(plan.Segments, lookback)
	var lookbackPoints int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scale_metrics WHERE metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, lookback.SourceStart, lookback.SourceEnd).Scan(&lookbackPoints); err != nil {
		return plan, err
	}
	// Include metadata/sidecars, identity growth and compressed-file overhead.
	counts := plan.Manifest.ProjectedCounts
	estimate := float64(counts.Spans)*414 + float64(counts.Logs)*103 + float64(counts.Metrics)*45 + float64(lookbackPoints)*float64(plan.Replicas+o.Hosts)*45
	estimate = estimate*1.1 + float64(len(plan.Segments))*float64(len(plan.Metadata))*float64(plan.Replicas+o.Hosts)*4096
	if math.IsInf(estimate, 0) || estimate >= float64(math.MaxInt64) {
		return plan, errors.New("projected byte count overflow")
	}
	plan.Manifest.EstimatedBytes = uint64(math.Ceil(estimate))
	return plan, nil
}

func panelScaleWriteManifest(dir string, m panelScaleManifest, complete bool) error {
	m.Complete = complete
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 128<<20 {
		return errors.New("scale manifest exceeds bounded inventory limit")
	}
	name := "manifest.json"
	if complete {
		name = "manifest.complete.tmp"
	}
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errors.New("cannot create scale manifest; inspect interrupted output")
	}
	_, writeErr := file.Write(raw)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return errors.New("cannot flush scale manifest")
	}
	if complete {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, "manifest.json")); err != nil {
			return errors.New("cannot publish completed manifest")
		}
	}
	parent, err := os.Open(dir)
	if err != nil {
		return errors.New("cannot open manifest directory")
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func panelScaleManifestHash(m panelScaleManifest) string {
	m.DatasetID = ""
	raw, _ := json.Marshal(m)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// The injectable disk provider is used before creating any output at all.
// On failure the controller owns the incomplete directory; no retry or cleanup.
func panelScaleGenerate(ctx context.Context, input string, o panelScaleOptions, plan panelScalePlan, disk func(string, uint64) error) (panelScaleManifest, error) {
	m := plan.Manifest
	if err := panelScalePaths(input, o.OutputDir); err != nil {
		return m, err
	}
	if err := disk(o.OutputDir, m.EstimatedBytes); err != nil {
		return m, err
	}
	if err := os.MkdirAll(filepath.Dir(o.OutputDir), 0o755); err != nil {
		return m, errors.New("cannot create output parent")
	}
	if err := os.Mkdir(o.OutputDir, 0o755); err != nil {
		return m, errors.New("output must be absent")
	}
	if err := panelScaleWriteManifest(o.OutputDir, m, false); err != nil {
		return m, err
	}
	services, namespaces := map[string]bool{}, map[string]bool{}
	for tile, seg := range plan.Segments {
		lookback := tile == len(plan.Segments)-1
		for replica := 0; replica < plan.Replicas+o.Hosts; replica++ {
			infra := replica >= plan.Replicas
			partial := !infra && plan.Partial && replica == plan.Replicas-1
			if partial && seg.PartialMode == 0 {
				continue
			}
			host := replica % o.Hosts
			if infra {
				host = replica - plan.Replicas
			}
			rewrite := &panelScaleRowRewrite{Replica: replica, Host: host, Infrastructure: infra, Containers: plan.Containers, Partial: partial && seg.PartialMode == 2, TraceCutoff: seg.PartialCutoff, Fraction: seg.PartialFraction, Counts: &m.Counts, LookbackCounts: &m.LookbackCounts, WindowStart: m.Start}
			for batch, source := range plan.Metadata {
				if err := ctx.Err(); err != nil {
					return m, errors.New("scale generation interrupted; inspect incomplete output")
				}
				prefix := fmt.Sprintf("tile%04d_copy%04d_", tile, replica)
				id := fmt.Sprintf("%sbatch%06d", prefix, batch)
				dst := filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", id+telemetry.BatchSuffix)
				if err := os.MkdirAll(dst, 0o755); err != nil {
					return m, errors.New("cannot create output batch")
				}
				next := telemetry.BatchMetadata{Version: 4, ID: id, Generation: source.Generation}
				for _, signal := range []string{"spans", "logs", "metrics"} {
					if (lookback || infra) && signal != "metrics" {
						continue
					}
					count := map[string]int{"spans": source.Spans, "logs": source.Logs, "metrics": source.Metrics}[signal]
					bounds := map[string]telemetry.TimeRange{"spans": source.SpanTime, "logs": source.LogTime, "metrics": source.MetricTime}[signal]
					if count == 0 || bounds.Known && (bounds.MaxNanos < seg.SourceStart.UnixNano() || bounds.MinNanos >= seg.SourceEnd.UnixNano()) {
						continue
					}
					path := filepath.Join(input, "parquet", "batches", source.ID+telemetry.BatchSuffix, signal+".parquet")
					output := filepath.Join(dst, signal+".parquet")
					if err := tilePanelParquetRewrite(path, output, signal, prefix, seg.SourceStart, seg.SourceEnd, seg.At.Sub(seg.SourceStart), &next, services, namespaces, rewrite); err != nil {
						return m, errors.New("scale row transformation failed; inspect incomplete output")
					}
					written := map[string]int{"spans": next.Spans, "logs": next.Logs, "metrics": next.Metrics}[signal]
					if written == 0 {
						if err := os.Remove(output); err != nil {
							return m, errors.New("cannot remove empty output signal")
						}
					}
				}
				if next.Spans+next.Logs+next.Metrics == 0 {
					// Only an empty directory created by this invocation is removed.
					if err := os.Remove(dst); err != nil {
						return m, errors.New("cannot remove empty output batch")
					}
					continue
				}
				if next.Spans > 0 {
					if err := panelTraceIndex(dst); err != nil {
						return m, errors.New("cannot publish scale trace index")
					}
				}
				raw, err := json.Marshal(next)
				if err != nil {
					return m, err
				}
				if err := os.WriteFile(filepath.Join(dst, "metadata.json"), raw, 0o644); err != nil {
					return m, errors.New("cannot publish output metadata")
				}
			}
		}
	}
	if m.Counts != m.ProjectedCounts || m.Counts.Spans < m.RequestedSpans || m.Counts.Spans-m.RequestedSpans > m.CountTolerance {
		return m, errors.New("actual span rate exceeds whole-trace tolerance or differs from projection")
	}
	m.ActualSpansPerSecond = float64(m.Counts.Spans) / float64(m.Hours*3600)
	for s := range services {
		m.Services = append(m.Services, s)
	}
	sort.Strings(m.Services)
	for n := range namespaces {
		m.Namespaces = append(m.Namespaces, n)
	}
	sort.Strings(m.Namespaces)
	issues, err := telemetrystore.VerifyBatches(filepath.Join(o.OutputDir, "telemetry"))
	if err != nil || len(issues) != 0 {
		return m, errors.New("scale output schema, footer/count or trace-index verification failed")
	}
	db, err := panelScaleDB(ctx, o.OutputDir)
	if err != nil {
		return m, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE TEMP VIEW scale_metrics AS SELECT * FROM "+panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*"+telemetry.BatchSuffix, "metrics.parquet")})); err != nil {
		return m, errors.New("cannot census output metrics")
	}
	m.Census, _, err = panelScaleCensus(ctx, db, m.Start, m.End, o.Hosts*o.Containers, o.Hosts, o.Hosts == 20 && o.Containers == 24)
	if err != nil {
		return m, errors.New("output metric cadence or workload census failed")
	}
	var censusPoints int64
	for _, c := range m.Census {
		if censusPoints > math.MaxInt64-c.Points {
			return m, errors.New("metric census count overflow")
		}
		censusPoints += c.Points
	}
	if censusPoints != m.Counts.Metrics {
		return m, errors.New("metric census does not match written window counts")
	}
	m.Distribution, err = panelScaleTrafficDistribution(ctx, db, panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*.batch", "spans.parquet")}), m.Start, m.End, false, "")
	if err != nil || !reflect.DeepEqual(m.Distribution, plan.Distribution) {
		return m, errors.New("actual traffic distribution differs from selected source")
	}

	m.Inventory, _, err = panelScalePublishedInventory(filepath.Join(o.OutputDir, "telemetry"))
	if err != nil {
		return m, err
	}
	for _, f := range m.Inventory {
		if f.Bytes < 0 || m.Bytes > math.MaxInt64-f.Bytes {
			return m, errors.New("actual byte count overflow")
		}
		m.Bytes += f.Bytes
		file, e := os.Open(filepath.Join(o.OutputDir, "telemetry", filepath.FromSlash(f.Name)))
		if e != nil {
			return m, errors.New("cannot flush completed output")
		}
		if e := errors.Join(file.Sync(), file.Close()); e != nil {
			return m, errors.New("cannot flush completed output")
		}
	}
	if e := filepath.WalkDir(filepath.Join(o.OutputDir, "telemetry"), func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !entry.IsDir() {
			return nil
		}
		dir, e := os.Open(path)
		if e != nil {
			return e
		}
		return errors.Join(dir.Sync(), dir.Close())
	}); e != nil {
		return m, errors.New("cannot flush completed batch directories")
	}
	m.Files = len(m.Inventory)
	m.Complete = true
	m.DatasetID = panelScaleManifestHash(m)
	if err := panelScaleWriteManifest(o.OutputDir, m, true); err != nil {
		return m, err
	}
	return m, nil
}

func panelScaleData(dir string, m panelScaleManifest) panelBenchData {
	// Volume and PeriodHours describe only the legacy path and remain zero here.
	return panelBenchData{Dir: dir, SourceHash: m.SourceHash, SourceStart: m.SourceStart, SourceEnd: m.SourceEnd, Start: m.Start, End: m.End, Spans: m.Counts.Spans, Logs: m.Counts.Logs, Metrics: m.Counts.Metrics, Files: m.Files, Bytes: m.Bytes, Services: m.Services, Namespaces: m.Namespaces}
}

func preparePanelScale(tb testing.TB, input string, o panelScaleOptions) panelBenchData {
	tb.Helper()
	plan, err := panelScalePreparePlan(tb.Context(), input, o)
	if err != nil {
		tb.Fatal("scale preparation failed: source/options must pass format, coverage, count and path checks")
	}
	m, err := panelScaleGenerate(tb.Context(), input, o, plan, panelScaleRequireDisk)
	if err != nil {
		tb.Fatal(err)
	}
	return panelScaleData(o.OutputDir, m)
}

func panelScaleRead(ctx context.Context, dir string) (m panelScaleManifest, err error) {
	file, err := os.Open(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, errors.New("completed scale manifest is missing")
	}
	// The inventory may contain hundreds of thousands of small immutable files.
	// Keep decoding bounded while accommodating an uncompacted S18 source.
	info, statErr := file.Stat()
	if statErr != nil || info.Size() > 128<<20 {
		file.Close()
		return m, errors.New("scale manifest exceeds bounded inventory limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 128<<20))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&m)
	var extra any
	if err == nil && decoder.Decode(&extra) != io.EOF {
		err = errors.New("manifest has trailing data")
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return m, errors.New("invalid scale manifest")
	}
	validHash := func(s string) bool {
		raw, e := hex.DecodeString(s)
		return e == nil && len(raw) == sha256.Size && s == strings.ToLower(s)
	}
	if m.Version != 1 || !m.Complete || m.BatchFormat != 4 || !validHash(m.SourceHash) || !validHash(m.DatasetID) || m.DatasetID != panelScaleManifestHash(m) {
		return m, errors.New("incomplete or incompatible scale manifest; inspect output, do not regenerate")
	}
	var distributionTotal, metricTotal int64
	for key, count := range m.Distribution {
		if !validHash(key) || count < 0 || distributionTotal > math.MaxInt64-count {
			return m, errors.New("invalid traffic census")
		}
		distributionTotal += count
	}
	for _, c := range m.Census {
		if c.Points <= 0 || c.Series <= 0 || metricTotal > math.MaxInt64-c.Points {
			return m, errors.New("invalid metric census")
		}
		metricTotal += c.Points
	}
	if distributionTotal != m.Counts.Spans || metricTotal != m.Counts.Metrics || m.ProjectedCounts != m.Counts || m.ActualSpansPerSecond != float64(m.Counts.Spans)/float64(m.Hours*3600) {
		return m, errors.New("manifest workload census or rate is inconsistent")
	}
	o := panelScaleOptions{m.SpansPerSecond, m.Hours, m.Hosts, m.ContainersPerHost, dir}
	if err := panelScaleValidateOptions(o); err != nil {
		return m, err
	}
	if m.Start.Location() != time.UTC || m.End.Location() != time.UTC || m.LookbackStart.Location() != time.UTC {
		return m, errors.New("scale manifest times must use UTC")
	}
	if m.End.Sub(m.Start) != time.Duration(m.Hours)*time.Hour || m.LookbackStart != m.Start.Add(-10*time.Minute) || m.RequestedSpans != m.SpansPerSecond*int64(m.Hours)*3600 || m.CountTolerance < 0 || m.Counts.Spans < m.RequestedSpans || m.Counts.Spans-m.RequestedSpans > m.CountTolerance || m.Counts.Logs <= 0 || m.Counts.Metrics <= 0 || m.LookbackCounts.Spans != 0 || m.LookbackCounts.Logs != 0 || m.LookbackCounts.Metrics <= 0 || len(m.Services) == 0 || len(m.Census) == 0 {
		return m, errors.New("scale manifest window or workload is incompatible")
	}
	inventory, _, err := panelScalePublishedInventory(filepath.Join(dir, "telemetry"))
	if err != nil || !reflect.DeepEqual(inventory, m.Inventory) {
		return m, errors.New("scale inventory is missing or changed")
	}
	var bytes int64
	for _, f := range inventory {
		if !validHash(f.SHA256) || f.Bytes < 0 || bytes > math.MaxInt64-f.Bytes {
			return m, errors.New("invalid scale file inventory")
		}
		bytes += f.Bytes
	}
	if bytes != m.Bytes || len(inventory) != m.Files {
		return m, errors.New("scale byte/file totals changed")
	}
	metadata, err := panelReplayMetadata(filepath.Join(dir, "telemetry"))
	if err != nil {
		return m, errors.New("scale batch metadata is incompatible")
	}
	var counts panelScaleCounts
	for _, batch := range metadata {
		counts.Spans += int64(batch.Spans)
		counts.Logs += int64(batch.Logs)
		counts.Metrics += int64(batch.Metrics)
	}
	if counts.Spans != m.Counts.Spans+m.LookbackCounts.Spans || counts.Logs != m.Counts.Logs+m.LookbackCounts.Logs || counts.Metrics != m.Counts.Metrics+m.LookbackCounts.Metrics {
		return m, errors.New("scale metadata counts differ from manifest")
	}
	db, e := panelScaleDB(ctx, dir)
	if e != nil {
		return m, e
	}
	defer db.Close()
	for _, signal := range []string{"spans", "logs", "metrics"} {
		event := map[string]string{"spans": "start_time", "logs": "log_time", "metrics": "metric_time"}[signal]
		var window, lookback, outside int64
		q := `SELECT count(*) FILTER(WHERE ` + event + `>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND ` + event + `<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),count(*) FILTER(WHERE ` + event + `>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND ` + event + `<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),count(*) FILTER(WHERE ` + event + `<?::TIMESTAMP_NS::TIMESTAMPTZ_NS OR ` + event + `>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS) FROM ` + panelScaleRelation([]string{filepath.Join(dir, "telemetry", "parquet", "batches", "tile*.batch", signal+".parquet")})
		if e := db.QueryRowContext(ctx, q, m.Start, m.End, m.LookbackStart, m.Start, m.LookbackStart, m.End).Scan(&window, &lookback, &outside); e != nil {
			return m, errors.New("cannot validate scale window counts")
		}
		want := map[string]int64{"spans": m.Counts.Spans, "logs": m.Counts.Logs, "metrics": m.Counts.Metrics}[signal]
		wantLookback := map[string]int64{"spans": m.LookbackCounts.Spans, "logs": m.LookbackCounts.Logs, "metrics": m.LookbackCounts.Metrics}[signal]
		if window != want || lookback != wantLookback || outside != 0 {
			return m, errors.New("scale physical window counts differ from manifest")
		}
	}
	issues, err := telemetrystore.VerifyBatches(filepath.Join(dir, "telemetry"))
	if err != nil || len(issues) != 0 {
		return m, errors.New("scale batch verification failed")
	}
	return m, nil
}

func loadPanelScale(tb testing.TB, dir string) (panelBenchData, panelScaleManifest) {
	tb.Helper()
	if os.Getenv("FANOUT_PANEL_BENCH_VOLUME") != "" {
		tb.Fatal("scale output cannot be combined with legacy volume")
	}
	m, err := panelScaleRead(tb.Context(), dir)
	if err != nil {
		tb.Fatal(err)
	}
	if err := panelScaleMatchEnv(m); err != nil {
		tb.Fatal(err)
	}
	return panelScaleData(dir, m), m
}

func TestPanelBenchmarkScaleGenerate(t *testing.T) {
	if os.Getenv("FANOUT_PANEL_BENCH_GENERATE") != "1" {
		t.Skip("controller-only generation requires FANOUT_PANEL_BENCH_GENERATE=1")
	}
	input := os.Getenv("FANOUT_PANEL_BENCH_DATA_DIR")
	if input == "" {
		t.Fatal("generation requires source telemetry root")
	}
	o, err := panelScaleOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	data := preparePanelScale(t, input, o)
	_, m := loadPanelScale(t, data.Dir)
	t.Logf("counts=%+v lookback_counts=%+v bytes=%d estimated_bytes=%d dataset_id=%s", m.Counts, m.LookbackCounts, m.Bytes, m.EstimatedBytes, m.DatasetID)
}

func panelScaleTinySource(t *testing.T, signed bool) (string, time.Time) {
	t.Helper()
	input := t.TempDir()
	seed := input
	if signed {
		seed = t.TempDir()
	}
	start := time.Unix(2*3600, 17).UTC().Truncate(time.Hour)
	repo, err := telemetrystore.Open(seed)
	if err != nil {
		t.Fatal(err)
	}
	var spans []telemetry.Span
	var logs []telemetry.Log
	var metrics []telemetry.Metric
	resource := map[string]any{"service.name": "checkout", "service.namespace": "source", "host.name": "source-host", "nested": map[string]any{"ok": true}, "bytes": []byte{0, 255}, "integer": int64(7)}
	attributes := map[string]any{"literal.dot": int64(7), "bytes": []byte{0, 255}, "nested": map[string]any{"ok": true}, "boolean": true}
	for second := range 3600 {
		for step := range 4 {
			n := start.Add(time.Duration(second)*time.Second + time.Duration(step)*250*time.Millisecond).UnixNano()
			id := fmt.Sprintf("trace-%05d", second*4+step)
			spans = append(spans, telemetry.Span{Namespace: "source", TraceID: id, SpanID: id, ServiceName: "checkout", Name: "GET /checkout", Kind: "server", StatusCode: []string{"OK", "ERROR"}[step%2], StartUnixNanos: n, EndUnixNanos: n + 17, DurationMS: 0.000017, IngestedAt: n, Resource: resource, Attributes: attributes})
			if step%2 == 0 {
				logs = append(logs, telemetry.Log{Namespace: "source", TraceID: id, SpanID: id, ServiceName: "checkout", EventUnixNanos: n, TimeUnixNanos: n, ObservedTimeNanos: n + 19, IngestedAt: n, Body: "synthetic", Severity: "INFO", Resource: resource, Attributes: attributes})
			}
		}
	}
	for second := -600; second < 3600; second += 10 {
		at := start.Add(time.Duration(second) * time.Second)
		for container := range 2 {
			for _, name := range []string{"container.cpu.utilization", "container.memory.usage.total"} {
				r := map[string]any{}
				for k, v := range resource {
					r[k] = v
				}
				r["container.id"] = fmt.Sprintf("source-c%d", container)
				r["container.name"] = fmt.Sprintf("source-container%d", container)
				m := telemetry.Metric{Namespace: "source", ServiceName: "collector", ScopeName: "docker_stats", ScopeVersion: "v1", Name: name, Type: "gauge", TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano(), Resource: r, Attributes: attributes, Value: 0.5}
				m.SeriesID = telemetry.MetricSeriesID(m)
				metrics = append(metrics, m)
			}
		}
		if second%60 == 0 {
			m := telemetry.Metric{Namespace: "source", ServiceName: "collector", ScopeName: "hostmetrics", ScopeVersion: "v1", Name: "system.cpu.time", Unit: "s", Type: "sum", Monotonic: true, Temporality: "cumulative", StartTimeUnixNanos: start.Add(-time.Hour).UnixNano() + 17, TimeUnixNanos: at.UnixNano(), EventUnixNanos: at.UnixNano(), IngestedAt: at.UnixNano(), Resource: resource, Attributes: attributes, Value: float64(second + 601)}
			m.SeriesID = telemetry.MetricSeriesID(m)
			metrics = append(metrics, m)
			m.Name, m.ScopeName, m.ServiceName = "app.requests", "app", "checkout"
			m.StartTimeUnixNanos = 0
			m.SeriesID = telemetry.MetricSeriesID(m)
			metrics = append(metrics, m)
		}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "tiny", Spans: spans, Logs: logs, Metrics: metrics}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if signed {
		dst := filepath.Join(input, "parquet", "batches", "tiny.batch")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		m := telemetry.BatchMetadata{Version: 4, ID: "tiny"}
		for _, signal := range []string{"spans", "logs", "metrics"} {
			if err := tilePanelParquet(filepath.Join(seed, "parquet", "batches", "tiny.batch", signal+".parquet"), filepath.Join(dst, signal+".parquet"), signal, "signed_", start.Add(-10*time.Minute), start.Add(time.Hour), -4*time.Hour, &m, map[string]bool{}, map[string]bool{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := panelTraceIndex(dst); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dst, "metadata.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		// A schema canary is intentionally unrelated to the source's data batches.
		if err := os.MkdirAll(filepath.Join(input, "parquet", "batches", telemetry.SchemaBatch), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(input, "parquet", "batches", telemetry.SchemaBatch, "keep"), []byte("schema survives"), 0o644); err != nil {
			t.Fatal(err)
		}
		start = start.Add(-4 * time.Hour)
	}
	if err := os.MkdirAll(filepath.Join(input, "parquet", "staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "parquet", "staging", "keep"), []byte("immutable source"), 0o644); err != nil {
		t.Fatal(err)
	}
	return input, start
}

func TestPanelScaleTinyVolumeAndCadence(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(fmt.Sprintf("signed_%t", signed), func(t *testing.T) {
			input, start := panelScaleTinySource(t, signed)
			before, hash, err := panelScaleInventory(input)
			if err != nil {
				t.Fatal(err)
			}
			o := panelScaleOptions{SpansPerSecond: 8, Hours: 1, Hosts: 2, Containers: 2, OutputDir: filepath.Join(t.TempDir(), "persistent")}
			plan, err := panelScalePreparePlan(t.Context(), input, o)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Replicas != 2 || plan.Partial || plan.Manifest.RequestedSpans != 28800 {
				t.Fatalf("wrong source-derived replicas: %d", plan.Replicas)
			}
			m, err := panelScaleGenerate(t.Context(), input, o, plan, panelScaleRequireDisk)
			if err != nil {
				t.Fatal(err)
			}
			if m.Counts != (panelScaleCounts{Spans: 28800, Logs: 14400, Metrics: 3120}) || m.Start != start || m.End != start.Add(time.Hour) || m.LookbackCounts.Metrics != 520 {
				t.Fatalf("wrong window/lookback: %+v %+v %s %s", m.Counts, m.LookbackCounts, m.Start, m.End)
			}
			after, afterHash, err := panelScaleInventory(input)
			if err != nil || hash != afterHash || !reflect.DeepEqual(before, after) {
				t.Fatal("source/schema/staging changed")
			}
			db, err := panelScaleDB(t.Context(), o.OutputDir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			metrics := panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*.batch", "metrics.parquet")})
			var host, cpu, memory, app, tuples, wrongNamespace int64
			q := `SELECT count(*) FILTER(WHERE name='system.cpu.time'),count(*) FILTER(WHERE name='container.cpu.utilization'),count(*) FILTER(WHERE name='container.memory.usage.total'),count(*) FILTER(WHERE name='app.requests'),count(DISTINCT (resource['host.name'],resource['container.id'])) FILTER(WHERE scope_name='docker_stats'),count(*) FILTER(WHERE namespace<>resource['service.namespace']::VARCHAR OR (scope_name IN ('hostmetrics','docker_stats') AND namespace<>'source_infra')) FROM ` + metrics + ` WHERE metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`
			if err := db.QueryRowContext(t.Context(), q, m.Start, m.End).Scan(&host, &cpu, &memory, &app, &tuples, &wrongNamespace); err != nil || host != 120 || cpu != 1440 || memory != 1440 || app != 120 || tuples != 4 || wrongNamespace != 0 {
				t.Fatalf("host=%d cpu=%d memory=%d app=%d tuples=%d namespace=%d err=%v", host, cpu, memory, app, tuples, wrongNamespace, err)
			}
			// Validate typed identity and full metric meaning after actual Parquet IO.
			metadata, err := panelReplayMetadata(filepath.Join(o.OutputDir, "telemetry"))
			if err != nil {
				t.Fatal(err)
			}
			seen := map[uint64]int{}
			var absent, shifted int
			for _, batch := range metadata {
				if batch.Metrics == 0 {
					continue
				}
				path := filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", batch.ID+telemetry.BatchSuffix, "metrics.parquet")
				in, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				reader := parquet.NewReader(in)
				bound, err := panelScaleBindSchema(reader.Schema(), "metrics")
				if err != nil {
					t.Fatal(err)
				}
				rows := make([]parquet.Row, 256)
				for {
					n, e := reader.ReadRows(rows)
					for _, row := range rows[:n] {
						var r panelScaleMetricRow
						if err := bound.Reconstruct(&r, row); err != nil {
							t.Fatal(err)
						}
						m := r.metric()
						if r.SeriesID != telemetry.MetricSeriesID(m) {
							t.Fatal("unsigned series identity was not recomputed")
						}
						seen[r.SeriesID]++
						if !reflect.DeepEqual(m.Attributes["bytes"], []byte{0, 255}) || m.Attributes["literal.dot"] != int64(7) || m.Attributes["boolean"] != true || !reflect.DeepEqual(m.Resource["nested"], map[string]any{"ok": true}) || m.Resource["integer"] != int64(7) {
							t.Fatal("VARIANT typed values changed")
						}
						if r.Name == "app.requests" {
							if r.StartTimeUnixNano != 0 {
								t.Fatal("absent metric start changed")
							}
							absent++
						}
						if r.Name == "system.cpu.time" {
							if !r.Monotonic || r.Temporality != "cumulative" || r.MetricType != "sum" || r.StartTimeUnixNano != start.Add(-time.Hour).UnixNano()+17 {
								t.Fatal("counter meaning/start changed")
							}
							shifted++
						}
					}
					if errors.Is(e, io.EOF) {
						break
					}
					if e != nil {
						t.Fatal(e)
					}
				}
				if err := errors.Join(reader.Close(), in.Close()); err != nil {
					t.Fatal(err)
				}
			}
			if len(seen) != 12 || absent != 140 || shifted != 140 {
				t.Fatalf("series=%d absent=%d shifted=%d", len(seen), absent, shifted)
			}
			for _, points := range seen {
				if points != 70 && points != 420 {
					t.Fatalf("unstable metric series across tiles/lookback: %d", points)
				}
			}
			var unlinked int64
			spans := panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*.batch", "spans.parquet")})
			logs := panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*.batch", "logs.parquet")})
			if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+logs+` l LEFT JOIN `+spans+` s ON (l.namespace,l.trace_id,l.span_id)=(s.namespace,s.trace_id,s.span_id) WHERE s.trace_id IS NULL OR l.resource['host.name']<>s.resource['host.name']`).Scan(&unlinked); err != nil || unlinked != 0 {
				t.Fatalf("traffic links/hosts changed: %d %v", unlinked, err)
			}
		})
	}
}

func TestPanelScalePartialTracesAndTemporalIdentity(t *testing.T) {
	input, start := panelScaleTinySource(t, false)
	repo, err := telemetrystore.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	// Every trace has seven spans, deliberately split across two source batches.
	var spans []telemetry.Span
	for second := range 3600 {
		for step := range 4 {
			for child := range 6 {
				n := start.Add(time.Duration(second)*time.Second + time.Duration(step)*250*time.Millisecond).UnixNano()
				id := fmt.Sprintf("trace-%05d", second*4+step)
				spans = append(spans, telemetry.Span{Namespace: "source", TraceID: id, SpanID: fmt.Sprintf("%s-child%d", id, child), ParentSpanID: id, ServiceName: "checkout", Name: "child", Kind: "internal", StatusCode: "OK", StartUnixNanos: n, EndUnixNanos: n + 17, IngestedAt: n, Resource: map[string]any{"host.name": "source-host", "service.namespace": "source"}})
			}
		}
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "children", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	o := panelScaleOptions{SpansPerSecond: 30, Hours: 2, Hosts: 2, Containers: 2, OutputDir: filepath.Join(t.TempDir(), "persistent")}
	plan, err := panelScalePreparePlan(t.Context(), input, o)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Partial || plan.Replicas != 2 || plan.Manifest.CountTolerance != 7 {
		t.Fatalf("wrong partial plan: replicas=%d tolerance=%d", plan.Replicas, plan.Manifest.CountTolerance)
	}
	m, err := panelScaleGenerate(t.Context(), input, o, plan, panelScaleRequireDisk)
	if err != nil {
		t.Fatal(err)
	}
	if m.Counts.Spans != 216006 || m.Counts.Spans-m.RequestedSpans != 6 || m.CountTolerance != 7 {
		t.Fatalf("whole-trace tolerance: actual=%d requested=%d tolerance=%d", m.Counts.Spans, m.RequestedSpans, m.CountTolerance)
	}
	if _, err := panelScaleRead(t.Context(), o.OutputDir); err != nil {
		t.Fatal(err)
	}
	db, err := panelScaleDB(t.Context(), o.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	spanRelation := panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*.batch", "spans.parquet")})
	var broken int64
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM (SELECT trace_id,count(*) AS n FROM `+spanRelation+` GROUP BY trace_id HAVING n<>7)`).Scan(&broken); err != nil || broken != 0 {
		t.Fatalf("split or partial traces=%d err=%v", broken, err)
	}
	metricRelation := panelScaleRelation([]string{filepath.Join(o.OutputDir, "telemetry", "parquet", "batches", "*.batch", "metrics.parquet")})
	var host, cpu, identities int64
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FILTER(WHERE name='system.cpu.time'),count(*) FILTER(WHERE name='container.cpu.utilization'),count(DISTINCT series_id) FILTER(WHERE name='system.cpu.time') FROM `+metricRelation+` WHERE metric_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND metric_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`, m.Start, m.End).Scan(&host, &cpu, &identities); err != nil || host != 240 || cpu != 2880 || identities != 2 {
		t.Fatalf("temporal/infrastructure identities: host=%d cpu=%d series=%d err=%v", host, cpu, identities, err)
	}
}

func TestPanelScalePersistentReload(t *testing.T) {
	input, _ := panelScaleTinySource(t, false)
	o := panelScaleOptions{SpansPerSecond: 8, Hours: 1, Hosts: 2, Containers: 2, OutputDir: filepath.Join(t.TempDir(), "persistent")}
	data := preparePanelScale(t, input, o)
	for _, name := range []string{"FANOUT_PANEL_BENCH_DATA_DIR", "FANOUT_PANEL_BENCH_VOLUME", "FANOUT_PANEL_BENCH_SPANS_PER_SECOND", "FANOUT_PANEL_BENCH_HOURS", "FANOUT_PANEL_BENCH_HOSTS", "FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST"} {
		t.Setenv(name, "")
	}
	loaded, m := loadPanelScale(t, o.OutputDir)
	if !reflect.DeepEqual(loaded, data) || loaded.Volume != 0 || loaded.PeriodHours != 0 {
		t.Fatal("reload changed dataset or invented legacy multiplier")
	}
	panelBenchEngine(t, data)
	if _, err := panelScaleRead(t.Context(), o.OutputDir); err != nil {
		t.Fatalf("engine-created disposable state broke reload: %v", err)
	}
	t.Setenv("FANOUT_PANEL_BENCH_HOURS", "2")
	if panelScaleMatchEnv(m) == nil {
		t.Fatal("mismatched controller options accepted")
	}
	t.Setenv("FANOUT_PANEL_BENCH_HOURS", "")
	if _, err := panelScalePreparePlan(t.Context(), input, o); err == nil {
		t.Fatal("existing output was accepted")
	}
	for _, mutate := range []func(*panelScaleManifest){func(m *panelScaleManifest) { m.Complete = false }, func(m *panelScaleManifest) { m.Version = 2 }, func(m *panelScaleManifest) { m.Hours = 2 }, func(m *panelScaleManifest) { m.BatchFormat = 3 }, func(m *panelScaleManifest) { m.Inventory[0].SHA256 = strings.Repeat("0", 64) }} {
		copy := m
		copy.Inventory = append([]panelScaleFile(nil), m.Inventory...)
		mutate(&copy)
		copy.DatasetID = panelScaleManifestHash(copy)
		raw, _ := json.Marshal(copy)
		if err := os.WriteFile(filepath.Join(o.OutputDir, "manifest.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := panelScaleRead(t.Context(), o.OutputDir); err == nil {
			t.Fatal("incompatible/partial output accepted")
		}
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(o.OutputDir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(o.OutputDir, "telemetry", filepath.FromSlash(m.Inventory[0].Name))); err != nil {
		t.Fatal(err)
	}
	if _, err := panelScaleRead(t.Context(), o.OutputDir); err == nil {
		t.Fatal("missing inventory file accepted")
	}
	t.Run("explicit_options", func(t *testing.T) {
		for key, value := range map[string]string{"FANOUT_PANEL_BENCH_SPANS_PER_SECOND": "1000", "FANOUT_PANEL_BENCH_HOURS": "24", "FANOUT_PANEL_BENCH_HOSTS": "20", "FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST": "24", "FANOUT_PANEL_BENCH_OUTPUT_DIR": filepath.Join(t.TempDir(), "scale")} {
			t.Setenv(key, value)
		}
		if _, err := panelScaleOptionsFromEnv(); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"FANOUT_PANEL_BENCH_SPANS_PER_SECOND", "FANOUT_PANEL_BENCH_HOURS", "FANOUT_PANEL_BENCH_HOSTS", "FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST", "FANOUT_PANEL_BENCH_OUTPUT_DIR"} {
			old := os.Getenv(key)
			t.Setenv(key, "")
			if _, err := panelScaleOptionsFromEnv(); err == nil {
				t.Fatal("missing option accepted")
			}
			t.Setenv(key, old)
		}
		t.Setenv("FANOUT_PANEL_BENCH_VOLUME", "1")
		if _, err := panelScaleOptionsFromEnv(); err == nil {
			t.Fatal("scale/legacy combination accepted")
		}
		t.Setenv("FANOUT_PANEL_BENCH_VOLUME", "")
		for _, value := range []string{"0", "-1", "junk", "9223372036854775807"} {
			t.Setenv("FANOUT_PANEL_BENCH_HOURS", value)
			if _, err := panelScaleOptionsFromEnv(); err == nil {
				t.Fatal("invalid/overflow option accepted")
			}
		}
	})

	refused := filepath.Join(t.TempDir(), "refused")
	o.OutputDir = refused
	_, err := panelScaleGenerate(t.Context(), input, o, panelScalePlan{Manifest: panelScaleManifest{EstimatedBytes: 47}}, func(_ string, estimate uint64) error { return panelScaleDiskCheck(93, estimate) })
	if err == nil {
		t.Fatal("disk provider did not refuse generation")
	}
	if _, err := os.Lstat(refused); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disk refusal created output or a manifest")
	}
	// Symlinks must not bypass overlap protection; check both ancestry directions.
	link := filepath.Join(t.TempDir(), "source-link")
	if err := os.Symlink(input, link); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{input, filepath.Join(input, "child"), filepath.Dir(input), filepath.Join(link, "child")} {
		if panelScalePaths(input, output) == nil {
			t.Fatal("overlapping source/output accepted")
		}
	}
	if _, err := panelScaleShift(math.MaxInt64, 1); err == nil {
		t.Fatal("positive signed overflow accepted")
	}
	if _, err := panelScaleShift(math.MinInt64, -1); err == nil {
		t.Fatal("negative signed overflow accepted")
	}
}

// File-derived schemas address map fields. Bind only their top-level accessors
// to the full Go mirror; retain every physical node, including shredded VARIANT.
type panelScaleBoundNode struct {
	parquet.Node
	fields []parquet.Field
	typeOf reflect.Type
}

func (n panelScaleBoundNode) Fields() []parquet.Field { return n.fields }
func (n panelScaleBoundNode) GoType() reflect.Type    { return n.typeOf }

type panelScaleBoundField struct {
	parquet.Field
	index int
}

func (f panelScaleBoundField) Value(v reflect.Value) reflect.Value { return v.Field(f.index) }
func panelScaleBindSchema(input *parquet.Schema, signal string) (*parquet.Schema, error) {
	var value any
	switch signal {
	case "spans":
		value = panelScaleSpanRow{}
	case "logs":
		value = panelScaleLogRow{}
	case "metrics":
		value = panelScaleMetricRow{}
	default:
		return nil, errors.New("unknown physical row mirror")
	}
	t := reflect.TypeOf(value)
	indices := map[string]int{}
	for i := range t.NumField() {
		name := strings.Split(t.Field(i).Tag.Get("parquet"), ",")[0]
		indices[name] = i
	}
	fields := make([]parquet.Field, len(input.Fields()))
	if len(fields) != len(indices) {
		return nil, errors.New("physical row mirror field count mismatch")
	}
	for i, f := range input.Fields() {
		index, ok := indices[f.Name()]
		if !ok {
			return nil, errors.New("physical row mirror missing field")
		}
		fields[i] = panelScaleBoundField{f, index}
	}
	return parquet.NewSchema(input.Name(), panelScaleBoundNode{input, fields, t}), nil
}

func panelScaleMatchEnv(m panelScaleManifest) error {
	// Optional controller options must agree with the immutable completed data.
	for name, want := range map[string]int64{"FANOUT_PANEL_BENCH_SPANS_PER_SECOND": m.SpansPerSecond, "FANOUT_PANEL_BENCH_HOURS": int64(m.Hours), "FANOUT_PANEL_BENCH_HOSTS": int64(m.Hosts), "FANOUT_PANEL_BENCH_CONTAINERS_PER_HOST": int64(m.ContainersPerHost)} {
		if raw := os.Getenv(name); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || n != want {
				return errors.New("scale options differ from completed manifest")
			}
		}
	}
	return nil
}

// Projection uses the same raw selection as the writer. The full replicas use
// native counts; only a fractional replica requires this bounded streaming pass.
func panelScaleSelectedCount(input string, metadata []telemetry.BatchMetadata, seg panelScaleSegment, signal string, o panelScaleRowRewrite) (count int64, err error) {
	for _, m := range metadata {
		n := m.Logs
		bounds := m.LogTime
		if signal == "metrics" {
			n = m.Metrics
			bounds = m.MetricTime
		}
		if n == 0 || bounds.Known && (bounds.MaxNanos < seg.SourceStart.UnixNano() || bounds.MinNanos >= seg.SourceEnd.UnixNano()) {
			continue
		}
		in, e := os.Open(filepath.Join(input, "parquet", "batches", m.ID+telemetry.BatchSuffix, signal+".parquet"))
		if e != nil {
			return 0, errors.New("cannot project selected source rows")
		}
		reader := parquet.NewReader(in)
		cols := map[string]int{}
		for i, path := range reader.Schema().Columns() {
			if len(path) == 1 {
				cols[path[0]] = i
			}
		}
		event := "log_time"
		if signal == "metrics" {
			event = "metric_time"
		}
		rows := make([]parquet.Row, 256)
		for {
			n, e := reader.ReadRows(rows)
			for _, row := range rows[:n] {
				at := panelRowValue(row, cols[event]).Int64()
				if at < seg.SourceStart.UnixNano() || at >= seg.SourceEnd.UnixNano() {
					continue
				}
				if signal == "metrics" {
					if panelScaleInfrastructure(panelRowValue(row, cols["scope_name"]).String()) {
						continue
					}
					if o.Partial && float64(uint64(panelRowValue(row, cols["series_id"]).Int64()))/float64(math.MaxUint64) >= o.Fraction {
						continue
					}
				} else if !o.keepTrace(panelRowValue(row, cols["trace_id"]).String(), row) {
					continue
				}
				count++
			}
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				reader.Close()
				in.Close()
				return 0, errors.New("cannot project source rows")
			}
		}
		if e := errors.Join(reader.Close(), in.Close()); e != nil {
			return 0, e
		}
	}
	return count, nil
}

func (o panelScaleRowRewrite) keepTrace(trace string, row parquet.Row) bool {
	if !o.Partial {
		return true
	}
	if trace != "" {
		return trace <= o.TraceCutoff
	}
	return float64(xxh3.HashString(fmt.Sprint(row)))/float64(math.MaxUint64) < o.Fraction
}

func panelScaleTrafficDistribution(ctx context.Context, db *sql.DB, relation string, start, end time.Time, partial bool, cutoff string) (map[string]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT service,operation,kind,status,http_method,http_route,http_status_code,count(*) FROM `+relation+` WHERE start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (NOT ? OR trace_id<=?) GROUP BY ALL`, start, end, partial, cutoff)
	if err != nil {
		return nil, errors.New("cannot census traffic distribution")
	}
	out := map[string]int64{}
	for rows.Next() {
		var tuple [7]string
		var count int64
		if err := rows.Scan(&tuple[0], &tuple[1], &tuple[2], &tuple[3], &tuple[4], &tuple[5], &tuple[6], &count); err != nil {
			rows.Close()
			return nil, errors.New("cannot decode traffic distribution")
		}
		raw, _ := json.Marshal(tuple)
		hash := sha256.Sum256(raw)
		out[hex.EncodeToString(hash[:])] = count
	}
	return out, errors.Join(rows.Err(), rows.Close())
}
