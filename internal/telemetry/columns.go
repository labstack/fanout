package telemetry

import (
	"fmt"
	"reflect"
	"time"

	"github.com/labstack/fanout/internal/telemetry/semconv"
	"github.com/parquet-go/parquet-go"
	"github.com/zeebo/xxh3"
)

// Type is a column's physical value type, or its element type in a list.
type Type uint8

const (
	String Type = iota
	Int32
	Int64
	Uint32
	Uint64
	Float64
	Bool
	Time    // INT64 nanoseconds with the TIMESTAMP(NANOS, UTC) logical type
	Variant // VARIANT; shredded as a column, unshredded as a list element
)

// Column is one column of a signal. The writer fills a stored column from the
// canonical row; a column with Expr is computed by the raw relation instead.
//
// Accessors take the row and return its value: func(*R) T for a required
// scalar, func(*R) (T, bool) for a nullable one, where false is NULL, and
// func(*R, []T) ([]T, bool) for a list, which appends the row's elements to
// the buffer it is given. A dedicated column has no accessor: the writer
// copies Key's value out of the attributes or resource map.
type Column struct {
	Name     string
	Type     Type
	Null     bool         // nullable; an absent value is NULL
	List     bool         // a nullable LIST of Type elements
	Dict     bool         // dictionary-encoded; see the encoding note below
	Delta    bool         // DELTA_BINARY_PACKED: an integer instant or count of nanoseconds
	Shred    []string     // VARIANT object keys also stored as typed columns
	Expr     string       // raw-relation expression over stored columns
	Private  bool         // part of the file layout only; no relation exposes it
	Key      *semconv.Key // dedicated column of a standard attribute key
	Parallel string       // lists of one group hold one element per item: events, links or exemplars
	View     Field        // clean-view column; the zero Field leaves it out
	get      any
}

// Field is a column of a signal's clean view.
type Field struct {
	Name string
	Type Type
	List bool
	Expr string // over the raw relation; empty reads the column itself
}

// Signal is the layout of one signal's Parquet files and its relations.
type Signal struct {
	Name    string // file stem and relation name
	Time    string // event-time column whose footer statistics bound a file
	Order   Order  // row order of every file
	Traces  bool   // files carry trace.fidx, built from an order led by _trace_hash
	Columns []Column
	schema  *parquet.Schema
	order   keyOrder
}

// Stored reports whether the files hold the column.
func (c Column) Stored() bool { return c.Expr == "" }

// Column returns the column with this physical or relation name.
func (s *Signal) Column(name string) (Column, bool) {
	for _, c := range s.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// Field returns the clean-view column with this name.
func (s *Signal) Field(name string) (Field, bool) {
	for _, c := range s.Columns {
		if c.View.Name == name {
			return c.View, true
		}
	}
	return Field{}, false
}

// Clock is the clean-view name of the event-time column.
func (s *Signal) Clock() string {
	c, _ := s.Column(s.Time)
	return c.View.Name
}

// Keys lists the signal's dedicated columns in spill-bit order.
func (s *Signal) Keys() []semconv.Key {
	var keys []semconv.Key
	for _, c := range s.Columns {
		if c.Key != nil {
			keys = append(keys, *c.Key)
		}
	}
	return keys
}

// Dedicated returns the standard key whose dedicated column holds the typed
// values of key in this signal's attributes or resource map.
func (s *Signal) Dedicated(column, key string) (semconv.Key, bool) {
	scope := semconv.Span
	switch column {
	case "attributes":
	case "resource":
		scope = semconv.Resource
	default:
		return semconv.Key{}, false
	}
	k, ok := semconv.Lookup(scope, key)
	if !ok {
		return semconv.Key{}, false
	}
	if _, ok := s.Column(k.Column); !ok {
		return semconv.Key{}, false
	}
	return k, true
}

// Signals lists every signal in file order.
var Signals = []*Signal{Spans, Logs, Metrics}

// LookupSignal returns the signal with this name.
func LookupSignal(name string) (*Signal, bool) {
	for _, s := range Signals {
		if s.Name == name {
			return s, true
		}
	}
	return nil, false
}

// Dictionary encoding is applied per column chunk and holds every distinct
// value for the whole row group, which ingest flushes only at
// parquetRowGroupRows. That pays for itself on a column whose values repeat —
// span kinds and statuses, HTTP methods, scope identifiers, the resource
// block shared by every span in a ResourceSpans — and costs a full second copy
// of the payload on a column whose values do not. Per-row IDs, event and link
// IDs and exemplars are near-unique by construction and are written plain.
// Dedicated attribute columns, event names, histogram bounds and summary
// quantiles repeat and are dictionary-encoded. Span
// namespaces, services and operation names stay plain too: clients bound
// neither their length nor their count, and parquetDictionaryMaxBytes is only
// checked when a page flushes. Attributes and resources use shredded VARIANT
// columns with a bounded shared metadata dictionary.
// Dictionary-encoding whole attribute payloads made one 50k-span commit hold
// several times the batch it was handed; commit_memory_test.go bounds the
// allocation ratio. Dashboard scans filter and group on the low-cardinality
// span columns, and the engine evaluates those predicates against a
// dictionary instead of decoding one string per row.
//
// Instants and nanosecond counts are DELTA_BINARY_PACKED. Log and metric rows
// are sorted by service before time, so neighbouring plain values share fewer
// leading bytes than in arrival order and compressed worse; deltas within a
// sorted run are small. On 50,000 sorted log rows the four time columns took
// 8 bytes a row plain and under 2 delta-encoded; span times, in trace order,
// shrank too.

// Dedicated columns copy a standard key's value out of its map when the value
// has the key's semconv type; the map keeps every key. A value of another
// type stays only in the map and sets the key's bit in _spill, and the file
// footer counts each key's spilled rows (spillPrefix). Keys with a column are
// not shredded, which would store a third copy. The read views project the
// messaging keys from their shredded fields.
var (
	attributeShred = []string{"messaging.system", "messaging.destination.name"}
	resourceShred  = []string{"service.name", "service.namespace"}
)

// spillPrefix starts the footer key whose value counts a key's spilled rows.
const spillPrefix = "fanout.spill."

// Spans keep trace locality, which the trace index depends on. Logs and
// metrics follow the OpenTelemetry Collector's ClickHouse exporter, with
// namespace before service: a coarse time bucket or the series identity
// first, and time last, so row-group statistics separate time ranges,
// services and metric names.
var Spans = &Signal{
	Name:   "spans",
	Time:   "start_time",
	Order:  Order{{Column: "_trace_hash"}, {Column: "start_unix_nano"}, {Column: "span_id"}},
	Traces: true,
	Columns: append([]Column{
		str("namespace", func(r *Span) string { return r.Namespace }),
		str("trace_id", func(r *Span) string { return r.TraceID }),
		u64("_trace_hash", func(r *Span) uint64 { return xxh3.HashString(r.TraceID) }).private(),
		str("span_id", func(r *Span) string { return r.SpanID }),
		str("parent_span_id", func(r *Span) string { return r.ParentSpanID }),
		str("service", func(r *Span) string { return r.ServiceName }),
		str("operation", func(r *Span) string { return r.Name }),
		str("kind", func(r *Span) string { return r.Kind }).dict(),
		ts("start_time", func(r *Span) int64 { return r.StartUnixNanos }),
		ts("end_time", func(r *Span) int64 { return r.EndUnixNanos }),
		nanos("start_unix_nano", func(r *Span) int64 { return r.StartUnixNanos }),
		nanos("end_unix_nano", func(r *Span) int64 { return r.EndUnixNanos }),
		f64("duration_ms", func(r *Span) float64 { return r.DurationMS }),
		str("status", func(r *Span) string { return r.StatusCode }).dict(),
		str("status_message", func(r *Span) string { return r.StatusMsg }),
		object("resource", func(r *Span) map[string]any { return r.Resource }, resourceShred),
		object("attributes", func(r *Span) map[string]any { return r.Attributes }, attributeShred),
		list("event_times", Time, func(r *Span, b []int64) ([]int64, bool) {
			for _, e := range r.Events {
				b = append(b, e.TimeUnixNanos)
			}
			return b, true
		}).parallel("events"),
		list("event_names", String, func(r *Span, b []string) ([]string, bool) {
			for _, e := range r.Events {
				b = append(b, e.Name)
			}
			return b, true
		}).parallel("events").dict(),
		list("event_attributes", Variant, func(r *Span, b []map[string]any) ([]map[string]any, bool) {
			for _, e := range r.Events {
				b = append(b, e.Attributes)
			}
			return b, true
		}).parallel("events"),
		list("link_trace_ids", String, func(r *Span, b []string) ([]string, bool) {
			for _, l := range r.Links {
				b = append(b, l.TraceID)
			}
			return b, true
		}).parallel("links"),
		list("link_span_ids", String, func(r *Span, b []string) ([]string, bool) {
			for _, l := range r.Links {
				b = append(b, l.SpanID)
			}
			return b, true
		}).parallel("links"),
		list("link_trace_states", String, func(r *Span, b []string) ([]string, bool) {
			for _, l := range r.Links {
				b = append(b, l.TraceState)
			}
			return b, true
		}).parallel("links").dict(),
		list("link_attributes", Variant, func(r *Span, b []map[string]any) ([]map[string]any, bool) {
			for _, l := range r.Links {
				b = append(b, l.Attributes)
			}
			return b, true
		}).parallel("links"),
		str("trace_state", func(r *Span) string { return r.TraceState }).dict(),
		i64("flags", func(r *Span) int64 { return int64(r.Flags) }),
		str("scope_name", func(r *Span) string { return r.ScopeName }).dict(),
		str("scope_version", func(r *Span) string { return r.ScopeVersion }).dict(),
		ts("ingested_at", func(r *Span) int64 { return r.IngestedAt }),
		nanos("ingested_unix_nano", func(r *Span) int64 { return r.IngestedAt }),
		str("rpc_service", func(r *Span) string { return r.RPCService }).dict(),
		str("peer_service", func(r *Span) string { return r.PeerService }).dict(),
		str("exception_type", func(r *Span) string { return r.ExceptionType }).dict(),
		str("exception_message", func(r *Span) string { return r.ExceptionMessage }),
		derived("messaging_system", String, "TRY_CAST(attributes['messaging.system'] AS VARCHAR)"),
		derived("messaging_destination", String, "TRY_CAST(attributes['messaging.destination.name'] AS VARCHAR)"),
		spill(),
	}, dedicated(semconv.Span, semconv.Resource)...),
}

var Logs = &Signal{
	Name:  "logs",
	Time:  "log_time",
	Order: Order{{Column: "log_time", Bucket: int64(5 * time.Minute)}, {Column: "namespace"}, {Column: "service"}, {Column: "log_time"}},
	Columns: append([]Column{
		str("namespace", func(r *Log) string { return r.Namespace }).dict(),
		ts("log_time", func(r *Log) int64 {
			return FirstPositiveNanos(r.EventUnixNanos, r.TimeUnixNanos, r.ObservedTimeNanos, r.IngestedAt)
		}).as("time"),
		ts("observed_time", func(r *Log) int64 {
			return FirstPositiveNanos(r.ObservedTimeNanos, r.EventUnixNanos, r.TimeUnixNanos, r.IngestedAt)
		}),
		nanos("time_unix_nano", func(r *Log) int64 { return r.TimeUnixNanos }),
		nanos("observed_time_unix_nano", func(r *Log) int64 { return r.ObservedTimeNanos }),
		str("severity", func(r *Log) string { return r.Severity }).dict(),
		i64("severity_number", func(r *Log) int64 { return int64(r.SeverityNumber) }),
		str("body", func(r *Log) string { return r.Body }),
		str("service", func(r *Log) string { return r.ServiceName }).dict(),
		str("trace_id", func(r *Log) string { return r.TraceID }),
		str("span_id", func(r *Log) string { return r.SpanID }),
		i64("flags", func(r *Log) int64 { return int64(r.Flags) }),
		object("resource", func(r *Log) map[string]any { return r.Resource }, resourceShred),
		object("attributes", func(r *Log) map[string]any { return r.Attributes }, attributeShred),
		str("scope_name", func(r *Log) string { return r.ScopeName }).dict(),
		str("scope_version", func(r *Log) string { return r.ScopeVersion }).dict(),
		ts("ingested_at", func(r *Log) int64 { return r.IngestedAt }),
		nanos("ingested_unix_nano", func(r *Log) int64 { return r.IngestedAt }),
		str("body_template", func(r *Log) string { return r.BodyTemplate }).dict(),
		spill(),
	}, dedicated(semconv.Resource)...),
}

// Metric columns follow the ClickHouse exporter's point layout. A column that
// does not apply to a point's kind is NULL.
var Metrics = &Signal{
	Name: "metrics",
	Time: "metric_time",
	Order: Order{{Column: "namespace"}, {Column: "service"}, {Column: "name"},
		{Column: "metric_time", Bucket: int64(time.Hour)}, {Column: "series_id"}, {Column: "metric_time"}},
	Columns: append([]Column{
		u64("series_id", func(r *Metric) uint64 { return r.SeriesID }),
		str("namespace", func(r *Metric) string { return r.Namespace }).dict(),
		ts("metric_time", func(r *Metric) int64 {
			return FirstPositiveNanos(r.EventUnixNanos, r.TimeUnixNanos, r.IngestedAt)
		}).as("time"),
		nanos("time_unix_nano", func(r *Metric) int64 { return r.TimeUnixNanos }),
		str("name", func(r *Metric) string { return r.Name }).dict(),
		str("description", func(r *Metric) string { return r.Description }).dict(),
		str("unit", func(r *Metric) string { return r.Unit }).dict(),
		str("type", func(r *Metric) string { return r.Type }).dict(),
		nullable("temporality", String, func(r *Metric) (string, bool) { return r.Temporality, r.Temporality != "" }).dict(),
		// The int64 extremes are the engine's infinity sentinels and do not
		// convert; TRY keeps one such client-supplied point from failing every
		// query.
		nanos("start_time_unix_nano", func(r *Metric) int64 { return r.StartTimeUnixNanos }).view(Field{Name: "start_time", Type: Time, Expr: `CASE WHEN start_time_unix_nano=0 THEN NULL
       ELSE TRY(make_timestamp_ns(start_time_unix_nano)::TIMESTAMPTZ_NS) END`}),
		str("service", func(r *Metric) string { return r.ServiceName }).dict(),
		column("flags", Uint32, func(r *Metric) uint32 { return r.Flags }),
		nullable("value", Float64, func(r *Metric) (float64, bool) { return r.Value, r.Type != "histogram" && r.Type != "summary" }),
		nullable("hist_count", Uint64, func(r *Metric) (uint64, bool) { return r.Count, r.Type == "histogram" || r.Type == "summary" }),
		nullable("hist_sum", Float64, func(r *Metric) (float64, bool) { return r.Sum, r.HasSum }),
		nullable("hist_min", Float64, func(r *Metric) (float64, bool) { return r.Min, r.HasMin }),
		nullable("hist_max", Float64, func(r *Metric) (float64, bool) { return r.Max, r.HasMax }),
		list("hist_bounds", Float64, func(r *Metric, b []float64) ([]float64, bool) {
			return append(b, r.Bounds...), r.Type == "histogram" && !r.Exponential
		}).dict(),
		list("hist_counts", Uint64, func(r *Metric, b []uint64) ([]uint64, bool) {
			return append(b, r.Counts...), r.Type == "histogram" && !r.Exponential
		}),
		nullable("exp_scale", Int32, func(r *Metric) (int32, bool) { return r.Scale, r.Exponential }),
		nullable("exp_zero_count", Uint64, func(r *Metric) (uint64, bool) { return r.ZeroCount, r.Exponential }),
		nullable("exp_zero_threshold", Float64, func(r *Metric) (float64, bool) { return r.ZeroThreshold, r.Exponential }),
		nullable("exp_positive_offset", Int32, func(r *Metric) (int32, bool) { return r.PositiveOffset, r.Exponential }),
		list("exp_positive_counts", Uint64, func(r *Metric, b []uint64) ([]uint64, bool) {
			return append(b, r.PositiveCounts...), r.Exponential
		}),
		nullable("exp_negative_offset", Int32, func(r *Metric) (int32, bool) { return r.NegativeOffset, r.Exponential }),
		list("exp_negative_counts", Uint64, func(r *Metric, b []uint64) ([]uint64, bool) {
			return append(b, r.NegativeCounts...), r.Exponential
		}),
		list("quantiles", Float64, func(r *Metric, b []float64) ([]float64, bool) {
			return append(b, r.Quantiles...), r.Type == "summary"
		}).parallel("quantiles").dict(),
		list("quantile_values", Float64, func(r *Metric, b []float64) ([]float64, bool) {
			return append(b, r.QuantileValues...), r.Type == "summary"
		}).parallel("quantiles"),
		list("exemplar_times", Time, func(r *Metric, b []int64) ([]int64, bool) {
			for _, e := range r.Exemplars {
				b = append(b, e.TimeUnixNanos)
			}
			return b, r.Type != "summary"
		}).parallel("exemplars"),
		list("exemplar_values", Float64, func(r *Metric, b []float64) ([]float64, bool) {
			for _, e := range r.Exemplars {
				b = append(b, e.Value)
			}
			return b, r.Type != "summary"
		}).parallel("exemplars"),
		list("exemplar_trace_ids", String, func(r *Metric, b []string) ([]string, bool) {
			for _, e := range r.Exemplars {
				b = append(b, e.TraceID)
			}
			return b, r.Type != "summary"
		}).parallel("exemplars"),
		list("exemplar_span_ids", String, func(r *Metric, b []string) ([]string, bool) {
			for _, e := range r.Exemplars {
				b = append(b, e.SpanID)
			}
			return b, r.Type != "summary"
		}).parallel("exemplars"),
		list("exemplar_attributes", Variant, func(r *Metric, b []map[string]any) ([]map[string]any, bool) {
			for _, e := range r.Exemplars {
				b = append(b, e.Attributes)
			}
			return b, r.Type != "summary"
		}).parallel("exemplars"),
		object("attributes", func(r *Metric) map[string]any { return r.Attributes }, attributeShred),
		object("resource", func(r *Metric) map[string]any { return r.Resource }, resourceShred),
		str("scope_name", func(r *Metric) string { return r.ScopeName }).dict(),
		str("scope_version", func(r *Metric) string { return r.ScopeVersion }).dict(),
		ts("ingested_at", func(r *Metric) int64 { return r.IngestedAt }),
		nanos("ingested_unix_nano", func(r *Metric) int64 { return r.IngestedAt }),
		spill(),
	}, dedicated(semconv.Resource)...),
}

func column[R, T any](name string, t Type, get func(*R) T) Column {
	return Column{Name: name, Type: t, View: Field{Name: name, Type: t}, get: get}
}

func str[R any](name string, get func(*R) string) Column  { return column(name, String, get) }
func i64[R any](name string, get func(*R) int64) Column   { return column(name, Int64, get) }
func u64[R any](name string, get func(*R) uint64) Column  { return column(name, Uint64, get) }
func f64[R any](name string, get func(*R) float64) Column { return column(name, Float64, get) }
func ts[R any](name string, get func(*R) int64) Column {
	return column(name, Time, get).delta().as(name)
}
func nanos[R any](name string, get func(*R) int64) Column { return column(name, Int64, get).delta() }

func nullable[R, T any](name string, t Type, get func(*R) (T, bool)) Column {
	return Column{Name: name, Type: t, Null: true, View: Field{Name: name, Type: t}, get: get}
}

func list[R, T any](name string, t Type, get func(*R, []T) ([]T, bool)) Column {
	return Column{Name: name, Type: t, List: true, View: Field{Name: name, Type: t, List: true}, get: get}
}

func derived(name string, t Type, expr string) Column {
	return Column{Name: name, Type: t, Expr: expr, View: Field{Name: name, Type: t}}
}

func object[R any](name string, get func(*R) map[string]any, shred []string) Column {
	c := column(name, Variant, get)
	c.Shred = shred
	return c
}

// dedicated declares the columns of the standard keys in these scopes.
func dedicated(scopes ...semconv.Scope) []Column {
	var columns []Column
	for i := range semconv.Keys {
		k := &semconv.Keys[i]
		for _, scope := range scopes {
			if k.Scope != scope {
				continue
			}
			t := String
			if k.Type == semconv.Int {
				t = Int64
			}
			columns = append(columns, Column{Name: k.Column, Type: t, Null: true, Dict: true, Key: k, View: Field{Name: k.Column, Type: t}})
		}
	}
	return columns
}

// spill is the per-row mask of keys whose value had another type than the
// key's dedicated column; bit i is semconv.Keys[i].
func spill() Column {
	return Column{Name: "_spill", Type: Uint64, Dict: true, Private: true}
}

func (c Column) dict() Column                 { c.Dict = true; return c }
func (c Column) parallel(group string) Column { c.Parallel = group; return c }
func (c Column) delta() Column                { c.Delta = true; return c }
func (c Column) private() Column              { c.Private, c.View = true, Field{}; return c }

func (c Column) view(f Field) Column { c.View = f; return c }

// as names a time column in the clean view, which reads it as an instant.
func (c Column) as(name string) Column {
	return c.view(Field{Name: name, Type: Time, Expr: c.Name + "::TIMESTAMPTZ_NS"})
}

func init() {
	for _, s := range Signals {
		s.schema = buildSchema(s)
		s.order = compileOrder(s)
	}
}

// Every writer and full-row reader uses this physical schema, including empty
// schema batches and compaction. A parquet.Group orders its fields by name, so
// the file's column order does not follow Columns. Dotted OTel names remain
// literal object keys.
func buildSchema(s *Signal) *parquet.Schema {
	fields := parquet.Group{}
	for _, c := range s.Columns {
		if !c.Stored() {
			continue
		}
		var node parquet.Node
		switch {
		case c.Type == Variant && c.List:
			node = parquet.Optional(dictMetadata{parquet.Variant()})
		case c.Type == Variant:
			shred := parquet.Group{}
			for _, key := range c.Shred {
				shred[key] = parquet.Optional(parquet.String())
			}
			variant, err := parquet.ShreddedVariant(shred)
			if err != nil {
				panic(fmt.Sprintf("%s.%s: %v", s.Name, c.Name, err))
			} // The fixed schema is a programmer-owned constant.
			node = dictMetadata{variant}
		default:
			node = leaf(c.Type)
			if c.Dict {
				node = parquet.Encoded(node, &parquet.RLEDictionary)
			}
			if c.Delta {
				node = parquet.Encoded(node, &parquet.DeltaBinaryPacked)
			}
		}
		switch {
		case c.List:
			node = parquet.Optional(parquet.List(node))
		case c.Null || c.Type == Variant:
			node = parquet.Optional(node)
		}
		fields[c.Name] = node
	}
	return parquet.NewSchema(s.Name, fields)
}

func leaf(t Type) parquet.Node {
	switch t {
	case String:
		return parquet.String()
	case Int32:
		return parquet.Int(32)
	case Int64:
		return parquet.Int(64)
	case Uint32:
		return parquet.Uint(32)
	case Uint64:
		return parquet.Uint(64)
	case Float64:
		return parquet.Leaf(parquet.DoubleType)
	case Bool:
		return parquet.Leaf(parquet.BooleanType)
	case Time:
		return parquet.Timestamp(parquet.Nanosecond)
	}
	panic(fmt.Sprintf("type %d has no Parquet leaf", t))
}

// dictMetadata dictionary-encodes a VARIANT group's metadata leaf. The variant
// writer shares one growing name dictionary across a file's rows, so a file
// holds a few distinct metadata blobs of up to 4 KiB each. Written plain, each
// row repeats its blob; after compaction interleaves the blobs of many input
// batches, the compressor no longer finds them adjacent. Measured on 50
// merged span batches with mixed attribute sets, plain metadata grew 3.3x in
// compaction and was the largest column; dictionary-encoded it is a few
// bytes a row.
type dictMetadata struct{ parquet.Node }

func (n dictMetadata) Fields() []parquet.Field {
	fields := n.Node.Fields()
	out := make([]parquet.Field, len(fields))
	for i, f := range fields {
		if f.Name() == "metadata" {
			out[i] = namedField{parquet.Encoded(f, &parquet.RLEDictionary), f}
		} else {
			out[i] = f
		}
	}
	return out
}

type namedField struct {
	parquet.Node
	field parquet.Field
}

func (f namedField) Name() string                           { return f.field.Name() }
func (f namedField) Value(base reflect.Value) reflect.Value { return f.field.Value(base) }
