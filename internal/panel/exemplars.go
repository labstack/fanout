package panel

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type SelectionBucket struct {
	Lower float64  `json:"lower"`
	Upper *float64 `json:"upper,omitempty"`
}
type ExemplarRequest struct {
	Dashboard  Dashboard         `json:"dashboard"`
	PanelID    string            `json:"panel_id"`
	Time       *Time             `json:"time,omitempty"`
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Bucket     *SelectionBucket  `json:"bucket,omitempty"`
	Vars       map[string]Value  `json:"vars,omitempty"`
}
type Exemplar struct {
	TraceID    string    `json:"trace_id"`
	Namespace  string    `json:"namespace"`
	Service    string    `json:"service"`
	Operation  string    `json:"operation"`
	DurationMS float64   `json:"duration_ms"`
	Status     string    `json:"status"`
	Start      time.Time `json:"start"`
}
type ExemplarResponse struct {
	Traces    []Exemplar `json:"traces"`
	Truncated bool       `json:"truncated,omitempty"`
}

func selectionWhere(p *Panel, filters []Filter, scope Scope, dimensions map[string]string, bucket *SelectionBucket) (string, []any, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return "", nil, err
	}
	if len(dimensions) > 3 {
		return "", nil, Problems{{Path: "dimensions", Message: "at most 3 dimensions", Hint: "select at most three panel grouping fields"}}
	}
	names := make([]string, 0, len(dimensions))
	for name := range dimensions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !slices.Contains(p.Query.By, name) || len(dimensions[name]) > 500 {
			return "", nil, Problems{{Path: "dimensions", Message: "dimensions must name query.by fields and values at most 500 characters", Hint: "select one of the panel grouping fields"}}
		}
		if dimensions[name] == "Other" && p.Viz == "timeseries" && len(p.Query.By) == 1 && p.Top() > 0 {
			return "", nil, Problems{{Path: "dimensions", Message: "Other groups several values; pick a named series"}}
		}
		ref, err := sig.field(name)
		if err != nil {
			return "", nil, err
		}
		where += " AND coalesce(" + ref.stringSQL() + ",'')=?"
		args = append(args, dimensions[name])
	}
	if bucket != nil {
		if p.Query.From != "spans" || p.Query.Histogram == nil || p.Query.Histogram.Field != "duration_ms" || math.IsNaN(bucket.Lower) || math.IsInf(bucket.Lower, 0) || bucket.Lower < 0 {
			return "", nil, Problems{{Path: "bucket", Message: "bucket needs a spans duration_ms histogram and a finite nonnegative lower bound", Hint: "select a finite duration bucket"}}
		}
		where += " AND duration_ms>=?"
		args = append(args, bucket.Lower)
		if bucket.Upper != nil {
			if math.IsNaN(*bucket.Upper) || math.IsInf(*bucket.Upper, 0) || *bucket.Upper <= bucket.Lower {
				return "", nil, Problems{{Path: "bucket.upper", Message: "upper must be finite and greater than lower", Hint: "select a larger upper bound or omit it for overflow"}}
			}
			where += " AND duration_ms<?"
			args = append(args, *bucket.Upper)
		}
	}
	return where, args, nil
}

func (e *Executor) Exemplars(ctx context.Context, req ExemplarRequest) (ExemplarResponse, error) {
	out := ExemplarResponse{Traces: []Exemplar{}}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	d := req.Dashboard
	Normalize(&d)
	checked, err := e.check(ctx, &d)
	if err != nil {
		return out, err
	}
	index := slices.IndexFunc(d.Panels, func(p Panel) bool { return p.ID == req.PanelID })
	if index < 0 {
		return out, Problems{{Path: "panel_id", Message: "panel does not exist in dashboard"}}
	}
	p := &d.Panels[index]
	if p.Query == nil || p.Query.From == "metrics" {
		return out, Problems{{Path: "panel_id", Message: "trace lineage needs a structured spans or logs panel"}}
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
		if t.Refresh == "" {
			t.Refresh = d.Time.Refresh
		}
		var problems Problems
		validateTime(t, &problems)
		if len(problems) > 0 {
			return out, problems
		}
	}
	override := p.Time
	if req.Time != nil && req.Time.From != nil && req.Time.To != nil {
		override = nil
	} // The browser captured the effective window.
	start, end, err := resolveWindow(t, override, e.now(), e.maxWindow)
	if err != nil {
		return out, Problems{{Path: "time", Message: err.Error()}}
	}
	if req.From.IsZero() || req.To.IsZero() || !req.From.Before(req.To) {
		return out, Problems{{Path: "from", Message: "selection needs a positive absolute range"}}
	}
	if !inTimestampNSRange(req.From) || !inTimestampNSRange(req.To) {
		return out, Problems{{Path: "from", Message: "selection times must be within the TIMESTAMP_NS range (1677–2262)"}}
	}
	lo, hi := req.From.UTC(), req.To.UTC()
	if lo.Before(start) {
		lo = start
	}
	if hi.After(end) {
		hi = end
	}
	if !lo.Before(hi) {
		return out, outsideSelection()
	}
	vars, err := e.values(ctx, &d, checked, start, end, req.Vars)
	if err != nil {
		return out, err
	}
	where, args, err := selectionWhere(p, checked.Filters[p.ID], Scope{Start: lo, End: hi, Vars: vars}, req.Dimensions, req.Bucket)
	if err != nil {
		return out, err
	}
	if p.Query.From == "logs" && p.Options != nil && p.Options.Highlight != "" {
		where += " AND contains(lower(body),lower(?))"
		args = append(args, p.Options.Highlight)
	}
	// Candidate membership is filtered; root metadata is selected within the full panel window.
	candidateSource := structuredSource(p.Query.From)
	text := checkedTraceSQL(candidateSource, where, 21, "duration_ms DESC,trace_id,namespace", "start_time::TIMESTAMP_NS")
	args = append(args, start, end)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end})
	rows, err := e.engine.QueryContext(ctx, text, args...)
	if err != nil {
		return out, exemplarError(ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		var trace Exemplar
		var capped bool
		if err := rows.Scan(&trace.TraceID, &trace.Namespace, &trace.Service, &trace.Operation, &trace.DurationMS, &trace.Status, &trace.Start, &capped); err != nil {
			return out, exemplarError(ctx, err)
		}
		out.Truncated = out.Truncated || capped
		if trace.TraceID == "" {
			continue
		} // The cap summary survives an empty roots result.
		trace.Start = trace.Start.UTC()
		out.Traces = append(out.Traces, trace)
	}
	if err := rows.Err(); err != nil {
		return out, exemplarError(ctx, err)
	}
	if len(out.Traces) > 20 {
		out.Traces = out.Traces[:20]
		out.Truncated = true
	}
	// A 21st root or a 1,001st candidate proves truncation; exactly 20 does not.
	return out, nil
}
func traceRootsSQL() string {
	return `SELECT s.trace_id,s.namespace,s.service,s.operation,s.duration_ms,CASE WHEN max(CASE WHEN s.status IN ('STATUS_CODE_ERROR','ERROR') THEN 1 ELSE 0 END) OVER(PARTITION BY s.namespace,s.trace_id)>0 THEN 'STATUS_CODE_ERROR' ELSE coalesce(s.status,'') END AS status,s.start_time,row_number() OVER(PARTITION BY s.namespace,s.trace_id ORDER BY (coalesce(s.parent_span_id,'')='') DESC,s.start_time,s.span_id) AS n
FROM spans s JOIN candidates c ON s.namespace=c.namespace AND s.trace_id=c.trace_id
WHERE s.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`
}

func checkedTraceSQL(source, where string, limit int, order, startProjection string) string {
	// Span candidates prioritize the slowest matching span per trace. Logs are a
	// deterministic sample by namespace and trace ID, not a global latency ranking.
	candidateOrder := "namespace,trace_id"
	if source == "spans" {
		candidateOrder = "max(duration_ms) DESC,namespace,trace_id"
	}
	return fmt.Sprintf(`WITH candidate_probe AS (SELECT namespace,trace_id,row_number() OVER (ORDER BY %s) AS candidate_rank FROM %s WHERE %s AND trace_id<>'' GROUP BY namespace,trace_id ORDER BY candidate_rank LIMIT 1001),
candidates AS (SELECT namespace,trace_id FROM candidate_probe WHERE candidate_rank<=1000), roots AS (%s)
SELECT coalesce(trace_id,''),coalesce(namespace,''),coalesce(service,''),coalesce(operation,''),coalesce(duration_ms,0),coalesce(status,''),coalesce(start_time,TIMESTAMP_NS '1970-01-01'),capped
FROM (SELECT count(*)>1000 AS capped FROM candidate_probe) cap LEFT JOIN
(SELECT trace_id,namespace,service,operation,duration_ms,status,%s AS start_time FROM roots WHERE n=1 ORDER BY %s LIMIT %d) selected ON TRUE
ORDER BY %s`, candidateOrder, source, where, traceRootsSQL(), startProjection, order, limit, order)
}

// Bound matching trace candidates before the root window functions. Like
// exemplars, ranking is approximate when filters exclude the root span.
func checkedTraceRowsSQL(where string, limit int, order string) string {
	limit = min(limit, 1001)
	candidateOrder := "max(duration_ms) DESC,namespace,trace_id"
	if strings.HasPrefix(order, "start_time ASC") {
		candidateOrder = "min(start_time) ASC,namespace,trace_id"
	}
	return fmt.Sprintf(`WITH candidates AS (SELECT namespace,trace_id FROM spans WHERE %s AND trace_id<>'' GROUP BY namespace,trace_id ORDER BY %s LIMIT %d), roots AS (%s)
SELECT coalesce(trace_id,''),coalesce(namespace,''),coalesce(service,''),coalesce(operation,''),coalesce(duration_ms,0),coalesce(status,''),epoch_ms(start_time::TIMESTAMP_NS)::BIGINT AS start
FROM roots WHERE n=1 ORDER BY %s LIMIT %d`, where, candidateOrder, limit, traceRootsSQL(), order, limit)
}

// The safe message preserves errors.Is without exposing paths in Error().
type exemplarReadError struct {
	message string
	cause   error
}

func (e exemplarReadError) Error() string { return e.message }
func (e exemplarReadError) Unwrap() error { return e.cause }
func exemplarError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isOperational(err) {
		return err
	}
	return fmt.Errorf("exemplars: %w", exemplarReadError{message: SafeError(err), cause: err})
}
func outsideSelection() error {
	return Problems{{Path: "from", Message: "selection does not overlap the panel window"}}
}
