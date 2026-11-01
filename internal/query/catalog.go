package query

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/labstack/fanout/internal/suggest"
	"github.com/labstack/fanout/internal/telemetry"
	"github.com/labstack/fanout/internal/telemetry/semconv"
)

type FieldType string

const (
	TypeString FieldType = "string"
	TypeNumber FieldType = "number"
	TypeTime   FieldType = "time"
	TypeMap    FieldType = "map"
	TypeBool   FieldType = "bool"
)

type Field struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Unit        string    `json:"unit,omitempty"`
	Description string    `json:"description,omitempty"`
}

type signal struct {
	source  *telemetry.Signal
	name    string
	time    string
	fields  []Field
	index   map[string]Field
	lowCard []string // columns whose common values the schema lists
}

// column presents one clean-view column of the telemetry registry, which
// owns its name and type; the catalog adds display order and documentation.
type column struct{ name, unit, description string }

func col(name, description string) column { return column{name: name, description: description} }

func (c column) in(unit string) column { c.unit = unit; return c }

var fieldTypes = map[telemetry.Type]FieldType{
	telemetry.String: TypeString, telemetry.Int64: TypeNumber, telemetry.Uint64: TypeNumber,
	telemetry.Float64: TypeNumber, telemetry.Bool: TypeBool, telemetry.Time: TypeTime, telemetry.Variant: TypeMap,
}

func newSignal(source *telemetry.Signal, lowCard []string, columns ...column) *signal {
	s := &signal{source: source, name: source.Name, time: source.Clock(), index: map[string]Field{}, lowCard: lowCard}
	for _, c := range columns {
		f, ok := source.Field(c.name)
		typ, known := fieldTypes[f.Type]
		if !ok || !known {
			panic(fmt.Sprintf("%s has no queryable column %s", source.Name, c.name))
		}
		field := Field{Name: c.name, Type: typ, Unit: c.unit, Description: c.description}
		s.fields = append(s.fields, field)
		s.index[field.Name] = field
	}
	return s
}

// The catalog's columns of each signal's clean view, in display order.
var signals = map[string]*signal{
	"spans": newSignal(telemetry.Spans,
		[]string{"service", "kind", "status", "messaging_system"},
		col("namespace", "OpenTelemetry service namespace"),
		col("service", "Service name"),
		col("operation", "Span name"),
		col("kind", vocabularyDescription("kind")),
		col("status", vocabularyDescription("status")),
		col("status_message", "Status description"),
		col("trace_id", "Trace identifier"),
		col("span_id", "Span identifier"),
		col("parent_span_id", "Parent span identifier; empty for roots"),
		col("start_time", "Span start"),
		col("end_time", "Span end"),
		col("duration_ms", "Span duration in milliseconds").in("ms"),
		col("rpc_service", "RPC service"),
		col("peer_service", "Remote service named by the caller"),
		col("exception_type", "Exception type from the span's exception event"),
		col("exception_message", "Exception message"),
		col("messaging_system", "messaging.system attribute"),
		col("messaging_destination", "messaging.destination.name attribute"),
		col("scope_name", "Instrumentation scope"),
		col("attributes", "Span attributes; use attributes['key']"),
		col("resource", "Resource attributes; use resource['key']"),
	),
	"logs": newSignal(telemetry.Logs,
		[]string{"service", "severity"},
		col("namespace", "OpenTelemetry service namespace"),
		col("service", "Service name"),
		col("time", "Log time"),
		col("severity", "Severity text, e.g. ERROR, WARN, INFO"),
		col("severity_number", "OpenTelemetry severity number; 17 and above is an error").in("none"),
		col("body", "Redacted log body"),
		col("body_template", "Body with variable parts replaced by <*>; groups similar messages"),
		col("trace_id", "Trace identifier"),
		col("span_id", "Span identifier"),
		col("scope_name", "Instrumentation scope"),
		col("attributes", "Log attributes; use attributes['key']"),
		col("resource", "Resource attributes; use resource['key']"),
	),
	"metrics": newSignal(telemetry.Metrics,
		[]string{"service", "type", "temporality"},
		col("namespace", "OpenTelemetry service namespace"),
		col("service", "Service name"),
		col("time", "Data point time"),
		col("name", "Metric name"),
		col("unit", "Metric unit as reported"),
		col("type", vocabularyDescription("type")),
		col("temporality", "cumulative or delta; NULL for gauge and summary"),
		col("start_time", "Data point start time; NULL when absent"),
		col("value", "Gauge or sum value; NULL for histogram and summary").in("none"),
		col("hist_count", "Histogram point count").in("count"),
		col("hist_sum", "Histogram point sum; NULL when absent").in("none"),
		col("attributes", "Data point attributes; use attributes['key']"),
		col("resource", "Resource attributes; use resource['key']"),
	),
}

// SignalNames lists the queryable signals in display order.
func SignalNames() []string {
	names := make([]string, len(telemetry.Signals))
	for i, s := range telemetry.Signals {
		names[i] = s.Name
	}
	return names
}

func lookupSignal(name string) (*signal, bool) {
	s, ok := signals[name]
	return s, ok
}

func (s *signal) names() []string {
	out := make([]string, 0, len(s.fields))
	for _, f := range s.fields {
		if f.Type != TypeMap {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// FieldRef is a validated reference to a column or an attribute lookup.
type FieldRef struct {
	Text   string
	Column string
	Key    string
	Type   FieldType
	Unit   string
}

var attributePattern = regexp.MustCompile(`(?s)^(attributes|resource)\['((?:[^']|'')+)'\]$`)

func (s *signal) field(text string) (FieldRef, error) {
	text = strings.TrimSpace(text)
	if m := attributePattern.FindStringSubmatch(text); m != nil {
		key := strings.ReplaceAll(m[2], "''", "'")
		return FieldRef{Text: text, Column: m[1], Key: key, Type: s.keyType(m[1], key), Unit: "none"}, nil
	}
	f, ok := s.index[text]
	if !ok {
		if k, internal := internalKey(text); internal {
			return FieldRef{}, fmt.Errorf("%s is internal; write %s", text, k)
		}
		message := fmt.Sprintf("%q is not a %s field", text, s.name)
		if hint := suggest.Spelling(text, s.names()); hint != "" {
			return FieldRef{}, errors.New(message + "; " + hint)
		}
		return FieldRef{}, errors.New(message + "; use a column or attributes['key']")
	}
	if f.Type == TypeMap {
		return FieldRef{}, fmt.Errorf("%s is a map; use %s['key']", text, text)
	}
	unit := f.Unit
	if unit == "" {
		unit = "none"
	}
	return FieldRef{Text: text, Column: f.Name, Type: f.Type, Unit: unit}, nil
}

// keyType is the type of a map lookup: a standard key with a dedicated
// column reads as the column's type (proposal 0005), any other key as text.
func (s *signal) keyType(column, key string) FieldType {
	if k, ok := s.source.Dedicated(column, key); ok && k.Type == semconv.Int {
		return TypeNumber
	}
	return TypeString
}

// internalKey returns the lookup to write instead of a storage column.
func internalKey(name string) (string, bool) {
	for _, k := range semconv.Keys {
		if strings.EqualFold(k.Column, name) {
			column := "attributes"
			if k.Scope == semconv.Resource {
				column = "resource"
			}
			return column + "[" + sqlString(k.Name) + "]", true
		}
	}
	return "", false
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func sqlString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

// stringSQL renders the field as text, for grouping and option lists.
func (f FieldRef) stringSQL() string {
	if f.Key != "" {
		return fmt.Sprintf("TRY_CAST(%s[%s] AS VARCHAR)", f.Column, sqlString(f.Key))
	}
	if f.Type == TypeString {
		return quoteIdent(f.Column)
	}
	return fmt.Sprintf("CAST(%s AS VARCHAR)", quoteIdent(f.Column))
}

// numberSQL renders the field as a number, for aggregation.
func (f FieldRef) numberSQL() string {
	if f.Key != "" {
		return fmt.Sprintf("TRY_CAST(%s[%s] AS DOUBLE)", f.Column, sqlString(f.Key))
	}
	if f.Type == TypeNumber {
		return quoteIdent(f.Column)
	}
	return fmt.Sprintf("TRY_CAST(%s AS DOUBLE)", quoteIdent(f.Column))
}

// alias is the frame column name for a grouping field.
func (f FieldRef) alias() string {
	if f.Key != "" {
		return f.Key
	}
	return f.Column
}

var unitFamilies = map[string]string{
	"ms": "duration", "s": "duration", "ns": "duration",
	"percent": "percent", "ratio": "ratio", "count": "count",
	"per_second": "rate", "per_minute": "rate", "bytes": "bytes", "none": "none",
}

func unitNames() []string {
	out := make([]string, 0, len(unitFamilies))
	for name := range unitFamilies {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func vocabularyDescription(field string) string {
	var values []string
	for _, stored := range fieldVocabularies[field].Aliases {
		values = append(values, stored)
	}
	slices.Sort(values)
	return strings.Join(values, ", ")
}
