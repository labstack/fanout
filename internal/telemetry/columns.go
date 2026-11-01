package telemetry

import (
	"fmt"
	"reflect"

	"github.com/parquet-go/parquet-go"
	"github.com/zeebo/xxh3"
)

// Type is a column's physical value type.
type Type uint8

const (
	String Type = iota
	Int64
	Uint64
	Float64
	Bool
	Time    // INT64 nanoseconds with the TIMESTAMP(NANOS, UTC) logical type
	Variant // shredded VARIANT object
)

// Column is one column of a signal. The writer fills a stored column from the
// canonical row; a column with Expr is computed by the raw relation instead.
type Column struct {
	Name    string
	Type    Type
	Dict    bool     // dictionary-encoded; see the encoding note below
	Shred   []string // VARIANT object keys also stored as typed columns
	Expr    string   // raw-relation expression over stored columns
	Private bool     // part of the file layout only; no relation exposes it
	View    Field    // clean-view column; the zero Field leaves it out
	get     any      // func(*R) for the signal's canonical row type R
}

// Field is a column of a signal's clean view.
type Field struct {
	Name string
	Type Type
	Expr string // over the raw relation; empty reads the column itself
}

// Signal is the layout of one signal's Parquet files and its relations.
type Signal struct {
	Name    string   // file stem and relation name
	Time    string   // event-time column whose footer statistics bound a file
	Sort    []string // footer sorting columns; empty keeps arrival order
	Columns []Column
	schema  *parquet.Schema
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
// of the payload on a column whose values do not. Per-row IDs, events, links
// and exemplars are near-unique by construction and are written plain. Span
// namespaces, services and operation names stay plain too: clients bound
// neither their length nor their count, and parquetDictionaryMaxBytes is only
// checked when a page flushes. Attributes and resources use shredded VARIANT
// columns with a bounded shared metadata dictionary.
// Dictionary-encoding whole attribute payloads made one 50k-span commit hold
// several times the batch it was handed; commit_memory_test.go bounds the
// allocation ratio. Dashboard scans filter and group on the low-cardinality
// span columns, and the engine evaluates those predicates against a
// dictionary instead of decoding one string per row.

// The read views project the messaging keys from these shredded fields.
var (
	attributeShred = []string{"messaging.system", "messaging.destination.name"}
	resourceShred  = []string{"service.name", "service.namespace", "service.version", "deployment.environment.name"}
)

var Spans = &Signal{
	Name: "spans",
	Time: "start_time",
	Sort: []string{"_trace_hash", "start_unix_nano", "span_id"},
	Columns: []Column{
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
		i64("start_unix_nano", func(r *Span) int64 { return r.StartUnixNanos }),
		i64("end_unix_nano", func(r *Span) int64 { return r.EndUnixNanos }),
		f64("duration_ms", func(r *Span) float64 { return r.DurationMS }),
		str("status", func(r *Span) string { return r.StatusCode }).dict(),
		str("status_message", func(r *Span) string { return r.StatusMsg }),
		object("resource", func(r *Span) map[string]any { return r.Resource }, resourceShred),
		object("attributes", func(r *Span) map[string]any { return r.Attributes }, attributeShred),
		str("events_json", func(r *Span) string { return r.EventsJSON }),
		str("links_json", func(r *Span) string { return r.LinksJSON }),
		str("trace_state", func(r *Span) string { return r.TraceState }).dict(),
		i64("flags", func(r *Span) int64 { return int64(r.Flags) }),
		str("scope_name", func(r *Span) string { return r.ScopeName }).dict(),
		str("scope_version", func(r *Span) string { return r.ScopeVersion }).dict(),
		ts("ingested_at", func(r *Span) int64 { return r.IngestedAt }),
		i64("ingested_unix_nano", func(r *Span) int64 { return r.IngestedAt }),
		str("http_method", func(r *Span) string { return r.HTTPMethod }).dict(),
		str("http_status_code", func(r *Span) string { return r.HTTPStatusCode }).dict(),
		str("http_route", func(r *Span) string { return r.HTTPRoute }).dict(),
		str("db_system", func(r *Span) string { return r.DBSystem }).dict(),
		str("rpc_method", func(r *Span) string { return r.RPCMethod }).dict(),
		str("rpc_service", func(r *Span) string { return r.RPCService }).dict(),
		str("peer_service", func(r *Span) string { return r.PeerService }).dict(),
		str("service_version", func(r *Span) string { return r.ServiceVersion }).dict(),
		str("deployment_env", func(r *Span) string { return r.DeploymentEnv }).dict(),
		str("exception_type", func(r *Span) string { return r.ExceptionType }).dict(),
		str("exception_message", func(r *Span) string { return r.ExceptionMessage }),
		derived("messaging_system", String, "TRY_CAST(attributes['messaging.system'] AS VARCHAR)"),
		derived("messaging_destination", String, "TRY_CAST(attributes['messaging.destination.name'] AS VARCHAR)"),
	},
}

var Logs = &Signal{
	Name: "logs",
	Time: "log_time",
	Columns: []Column{
		str("namespace", func(r *Log) string { return r.Namespace }).dict(),
		ts("log_time", func(r *Log) int64 {
			return FirstPositiveNanos(r.EventUnixNanos, r.TimeUnixNanos, r.ObservedTimeNanos, r.IngestedAt)
		}).as("time"),
		ts("observed_time", func(r *Log) int64 {
			return FirstPositiveNanos(r.ObservedTimeNanos, r.EventUnixNanos, r.TimeUnixNanos, r.IngestedAt)
		}),
		i64("time_unix_nano", func(r *Log) int64 { return r.TimeUnixNanos }),
		i64("observed_time_unix_nano", func(r *Log) int64 { return r.ObservedTimeNanos }),
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
		i64("ingested_unix_nano", func(r *Log) int64 { return r.IngestedAt }),
		str("body_template", func(r *Log) string { return r.BodyTemplate }).dict(),
	},
}

var Metrics = &Signal{
	Name: "metrics",
	Time: "metric_time",
	Columns: []Column{
		u64("series_id", func(r *Metric) uint64 { return r.SeriesID }),
		str("namespace", func(r *Metric) string { return r.Namespace }).dict(),
		ts("metric_time", func(r *Metric) int64 {
			return FirstPositiveNanos(r.EventUnixNanos, r.TimeUnixNanos, r.IngestedAt)
		}).as("time"),
		i64("time_unix_nano", func(r *Metric) int64 { return r.TimeUnixNanos }),
		str("name", func(r *Metric) string { return r.Name }).dict(),
		str("description", func(r *Metric) string { return r.Description }).dict(),
		str("unit", func(r *Metric) string { return r.Unit }).dict(),
		str("metric_type", func(r *Metric) string { return r.Type }).dict().view(Field{Name: "type", Type: String, Expr: `CASE WHEN metric_type='sum' AND monotonic THEN 'counter'
       WHEN metric_type='sum' THEN 'updown'
       WHEN metric_type='exponential_histogram' THEN 'histogram'
       ELSE metric_type END`}),
		str("temporality", func(r *Metric) string { return r.Temporality }).dict(),
		boolean("monotonic", func(r *Metric) bool { return r.Monotonic }).view(Field{}),
		// The int64 extremes are the engine's infinity sentinels and do not
		// convert; TRY keeps one such client-supplied point from failing every
		// query.
		i64("start_time_unix_nano", func(r *Metric) int64 { return r.StartTimeUnixNanos }).view(Field{Name: "start_time", Type: Time, Expr: `CASE WHEN start_time_unix_nano=0 THEN NULL
       ELSE TRY(make_timestamp_ns(start_time_unix_nano)::TIMESTAMPTZ_NS) END`}),
		str("service", func(r *Metric) string { return r.ServiceName }).dict(),
		f64("value", func(r *Metric) float64 { return r.Value }),
		str("hist_bounds_json", func(r *Metric) string { return r.HistBoundsJSON }).dict(),
		str("hist_counts_json", func(r *Metric) string { return r.HistCountsJSON }).dict(),
		i64("hist_count", func(r *Metric) int64 { return r.HistCount }),
		f64("hist_sum", func(r *Metric) float64 { return r.HistSum }),
		str("exemplars_json", func(r *Metric) string { return r.ExemplarsJSON }),
		object("attributes", func(r *Metric) map[string]any { return r.Attributes }, attributeShred),
		object("resource", func(r *Metric) map[string]any { return r.Resource }, resourceShred),
		str("scope_name", func(r *Metric) string { return r.ScopeName }).dict(),
		str("scope_version", func(r *Metric) string { return r.ScopeVersion }).dict(),
		ts("ingested_at", func(r *Metric) int64 { return r.IngestedAt }),
		i64("ingested_unix_nano", func(r *Metric) int64 { return r.IngestedAt }),
	},
}

func column(name string, t Type, get any) Column {
	return Column{Name: name, Type: t, View: Field{Name: name, Type: t}, get: get}
}

func str[R any](name string, get func(*R) string) Column   { return column(name, String, get) }
func i64[R any](name string, get func(*R) int64) Column    { return column(name, Int64, get) }
func u64[R any](name string, get func(*R) uint64) Column   { return column(name, Uint64, get) }
func f64[R any](name string, get func(*R) float64) Column  { return column(name, Float64, get) }
func boolean[R any](name string, get func(*R) bool) Column { return column(name, Bool, get) }
func ts[R any](name string, get func(*R) int64) Column     { return column(name, Time, get).as(name) }
func derived(name string, t Type, expr string) Column {
	return Column{Name: name, Type: t, Expr: expr, View: Field{Name: name, Type: t}}
}
func object[R any](name string, get func(*R) map[string]any, shred []string) Column {
	c := column(name, Variant, get)
	c.Shred = shred
	return c
}

func (c Column) dict() Column    { c.Dict = true; return c }
func (c Column) private() Column { c.Private, c.View = true, Field{}; return c }

func (c Column) view(f Field) Column { c.View = f; return c }

// as names a time column in the clean view, which reads it as an instant.
func (c Column) as(name string) Column {
	return c.view(Field{Name: name, Type: Time, Expr: c.Name + "::TIMESTAMPTZ_NS"})
}

func init() {
	for _, s := range Signals {
		s.schema = buildSchema(s)
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
		switch c.Type {
		case String:
			node = parquet.String()
		case Int64:
			node = parquet.Int(64)
		case Uint64:
			node = parquet.Uint(64)
		case Float64:
			node = parquet.Leaf(parquet.DoubleType)
		case Bool:
			node = parquet.Leaf(parquet.BooleanType)
		case Time:
			node = parquet.Timestamp(parquet.Nanosecond)
		case Variant:
			shred := parquet.Group{}
			for _, key := range c.Shred {
				shred[key] = parquet.Optional(parquet.String())
			}
			variant, err := parquet.ShreddedVariant(shred)
			if err != nil {
				panic(fmt.Sprintf("%s.%s: %v", s.Name, c.Name, err))
			} // The fixed schema is a programmer-owned constant.
			node = parquet.Optional(dictMetadata{variant})
		}
		if c.Dict {
			node = parquet.Encoded(node, &parquet.RLEDictionary)
		}
		fields[c.Name] = node
	}
	return parquet.NewSchema(s.Name, fields)
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
