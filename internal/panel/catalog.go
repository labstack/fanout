package panel

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type FieldType string

const (
	TypeString FieldType = "string"
	TypeNumber FieldType = "number"
	TypeTime   FieldType = "time"
	TypeMap    FieldType = "map"
)

type Field struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Unit        string    `json:"unit,omitempty"`
	Description string    `json:"description,omitempty"`
}

type signal struct {
	name    string
	time    string
	fields  []Field
	index   map[string]Field
	lowCard []string // columns whose common values the schema lists
}

func newSignal(name, timeColumn string, lowCard []string, fields ...Field) *signal {
	s := &signal{name: name, time: timeColumn, fields: fields, index: map[string]Field{}, lowCard: lowCard}
	for _, f := range fields {
		s.index[f.Name] = f
	}
	return s
}

func str(name, description string) Field {
	return Field{Name: name, Type: TypeString, Description: description}
}
func num(name, unit, description string) Field {
	return Field{Name: name, Type: TypeNumber, Unit: unit, Description: description}
}
func ts(name, description string) Field {
	return Field{Name: name, Type: TypeTime, Description: description}
}
func dict(name, description string) Field {
	return Field{Name: name, Type: TypeMap, Description: description}
}

// The columns of the spans, logs and metrics views in internal/query/views.go.
var signals = map[string]*signal{
	"spans": newSignal("spans", "start_time",
		[]string{"service", "kind", "status", "http_method", "db_system", "messaging_system", "deployment_env", "service_version"},
		str("namespace", "OpenTelemetry service namespace"),
		str("service", "Service name"),
		str("operation", "Span name"),
		str("kind", "SPAN_KIND_SERVER, SPAN_KIND_CLIENT, SPAN_KIND_INTERNAL, SPAN_KIND_PRODUCER or SPAN_KIND_CONSUMER"),
		str("status", "STATUS_CODE_OK, STATUS_CODE_ERROR or STATUS_CODE_UNSET"),
		str("status_message", "Status description"),
		str("trace_id", "Trace identifier"),
		str("span_id", "Span identifier"),
		str("parent_span_id", "Parent span identifier; empty for roots"),
		ts("start_time", "Span start"),
		ts("end_time", "Span end"),
		num("duration_ms", "ms", "Span duration in milliseconds"),
		str("http_method", "HTTP method"),
		str("http_status_code", "HTTP response status code as text"),
		str("http_route", "HTTP route template"),
		str("db_system", "Database system, e.g. postgresql, redis"),
		str("rpc_method", "RPC method"),
		str("rpc_service", "RPC service"),
		str("peer_service", "Remote service named by the caller"),
		str("service_version", "service.version resource attribute"),
		str("deployment_env", "deployment.environment resource attribute"),
		str("exception_type", "Exception type from the span's exception event"),
		str("exception_message", "Exception message"),
		str("messaging_system", "messaging.system attribute"),
		str("messaging_destination", "messaging.destination.name attribute"),
		str("scope_name", "Instrumentation scope"),
		dict("attributes", "Span attributes; use attributes['key']"),
		dict("resource", "Resource attributes; use resource['key']"),
	),
	"logs": newSignal("logs", "time",
		[]string{"service", "severity"},
		str("namespace", "OpenTelemetry service namespace"),
		str("service", "Service name"),
		ts("time", "Log time"),
		str("severity", "Severity text, e.g. ERROR, WARN, INFO"),
		num("severity_number", "none", "OpenTelemetry severity number; 17 and above is an error"),
		str("body", "Redacted log body"),
		str("body_template", "Body with variable parts replaced by <*>; groups similar messages"),
		str("trace_id", "Trace identifier"),
		str("span_id", "Span identifier"),
		str("scope_name", "Instrumentation scope"),
		dict("attributes", "Log attributes; use attributes['key']"),
		dict("resource", "Resource attributes; use resource['key']"),
	),
	"metrics": newSignal("metrics", "time",
		[]string{"service", "type"},
		str("namespace", "OpenTelemetry service namespace"),
		str("service", "Service name"),
		ts("time", "Data point time"),
		str("name", "Metric name"),
		str("unit", "Metric unit as reported"),
		str("type", "gauge, sum or histogram"),
		num("value", "none", "Gauge or sum value"),
		num("hist_count", "count", "Histogram point count"),
		num("hist_sum", "none", "Histogram point sum"),
		dict("attributes", "Data point attributes; use attributes['key']"),
		dict("resource", "Resource attributes; use resource['key']"),
	),
}

// SignalNames lists the queryable signals in display order.
func SignalNames() []string { return []string{"spans", "logs", "metrics"} }

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

var attributePattern = regexp.MustCompile(`^(attributes|resource)\['([A-Za-z0-9_.\-/:@]{1,128})'\]$`)

func (s *signal) field(text string) (FieldRef, error) {
	text = strings.TrimSpace(text)
	if m := attributePattern.FindStringSubmatch(text); m != nil {
		return FieldRef{Text: text, Column: m[1], Key: m[2], Type: TypeString, Unit: "none"}, nil
	}
	f, ok := s.index[text]
	if !ok {
		message := fmt.Sprintf("%q is not a %s field", text, s.name)
		if hint := suggest(text, s.names()); hint != "" {
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
