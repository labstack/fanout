package query

import (
	"context"
	"errors"
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
	Dashboard  Dashboard         `json:"dashboard" jsonschema:"Complete v1 dashboard specification; every panel is executed"`
	PanelID    string            `json:"panel_id" jsonschema:"ID of the selected panel in dashboard"`
	Kind       string            `json:"kind,omitempty" jsonschema:"Selection signal: traces or logs; defaults from the selected panel"`
	Time       *Time             `json:"time,omitempty" jsonschema:"Optional dashboard time override: exact from/to or a relative range"`
	From       time.Time         `json:"from" jsonschema:"Selection start as an RFC3339Nano UTC timestamp"`
	To         time.Time         `json:"to" jsonschema:"Selection end as an RFC3339Nano UTC timestamp, after from"`
	Dimensions map[string]string `json:"dimensions,omitempty" jsonschema:"Exact dimension values of the selected series or row"`
	Bucket     *SelectionBucket  `json:"bucket,omitempty" jsonschema:"Optional selected bucket with its exact bounds"`
	Vars       map[string]Value  `json:"vars,omitempty" jsonschema:"Resolved variable values: strings or lists; preserves $__all and empty lists"`
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
	Logs      *Frame     `json:"logs,omitempty"`
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
		if (!slices.Contains(p.Query.By, name) && !((p.Viz == "health" || p.Viz == "service_map") && name == "service")) || len(dimensions[name]) > 500 {
			return "", nil, Problems{{Path: "dimensions", Message: "dimensions must name query.by fields and values at most 500 characters", Hint: "select one of the panel grouping fields"}}
		}
		if (dimensions[name] == "Other" || strings.HasPrefix(dimensions[name], "Other (") && strings.HasSuffix(dimensions[name], ")")) && p.Viz == "timeseries" && len(p.Query.By) == 1 && p.Top() > 0 {
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

// Exemplars answers a selection's sample traces or logs through the result
// cache, keyed by the canonical request, so a burst of identical drill-downs
// runs once. Relative windows end now, the stamp of a cached answer.
func (e *Executor) Exemplars(ctx context.Context, req ExemplarRequest) (ExemplarResponse, error) {
	now := e.now()
	key, err := keyOf(struct {
		Kind    string          `json:"kind"`
		Request ExemplarRequest `json:"request"`
	}{"exemplars", req})
	if err != nil {
		return e.exemplars(ctx, req, now)
	}
	out, _, err := loadCached(e, ctx, key, now, func(run context.Context) (ExemplarResponse, int64, bool, error) {
		// Leave room for execution and the response inside the 15s client bound.
		wait, cancel := context.WithTimeout(run, 2*time.Second)
		release, err := e.admit(wait)
		cancel()
		if err != nil {
			if run.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				err = ErrBusy
			}
			return ExemplarResponse{Traces: []Exemplar{}}, 0, false, err
		}
		defer release()
		out, err := e.exemplars(run, req, now)
		size := int64(256+len(out.Traces)*256) + out.Logs.size()
		return out, size, err == nil, err
	})
	if err != nil {
		return ExemplarResponse{Traces: []Exemplar{}}, err
	}
	out.Logs = out.Logs.clone()
	out.Traces = slices.Clone(out.Traces)
	return out, nil
}

func (e *Executor) exemplars(ctx context.Context, req ExemplarRequest, now time.Time) (ExemplarResponse, error) {
	out := ExemplarResponse{Traces: []Exemplar{}}
	if req.Kind != "" && req.Kind != "traces" && req.Kind != "logs" {
		return out, Problems{{Path: "kind", Message: "kind must be traces or logs", Hint: "choose traces or logs"}}
	}
	ctx, cancel := context.WithTimeout(ctx, min(e.timeout, 10*time.Second))
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
	start, end, err := resolveWindow(t, override, now, e.maxWindow)
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
	if req.Kind == "logs" {
		if p.Query.From != "logs" {
			return out, Problems{{Path: "kind", Message: "log selections need a logs-source panel"}}
		}
		copyPanel := *p
		copyQuery := *p.Query
		copyQuery.Limit = 200
		copyQuery.Bucket = ""
		copyQuery.Sort = "time"
		copyPanel.Query = &copyQuery
		copyPanel.Viz = "logs"
		scope := Scope{Start: lo, End: hi, Vars: vars}
		compiled, err := compileRowsWhere(&copyPanel, where, args, scope)
		if err != nil {
			return out, err
		}
		logCtx := queryrows.WithWindow(ctx, queryrows.Window{Start: lo, End: hi})
		rows, err := e.engine.QueryContext(logCtx, compiled.SQL, compiled.Args...)
		if err != nil {
			return out, err
		}
		out.Logs, err = scanFrame(rows, compiled.Columns, 200)
		return out, err
	}

	if p.Query.From == "logs" && p.Options != nil && p.Options.Highlight != "" {
		where += " AND contains(lower(body),lower(?))"
		args = append(args, p.Options.Highlight)
	}
	// Rank only the narrow span projection in the selected bucket. DuckDB uses
	// a bounded TOP_N heap; the extra row detects truncation without feeding
	// more than 64 candidates to root lookup. Logs retain their sampling order.
	// Root metadata still uses the full panel window.
	candidateCtx := queryrows.WithWindow(ctx, queryrows.Window{Start: lo, End: hi})
	candidateSQL := "SELECT namespace,trace_id FROM " + structuredSource(p.Query.From) + " WHERE " + where + " AND trace_id<>'' LIMIT 65"
	if p.Query.From == "spans" {
		candidateSQL = "SELECT namespace,trace_id FROM (SELECT namespace,trace_id,duration_ms FROM spans WHERE " + where + " AND trace_id<>'' ORDER BY duration_ms DESC LIMIT 65)"
	}
	probe, err := e.engine.QueryContext(candidateCtx, candidateSQL, args...)
	if err != nil {
		return out, exemplarError(ctx, err)
	}
	type identity struct{ namespace, trace string }
	seen := make(map[identity]bool)
	var candidates []string
	var rootArgs []any
	count := 0
	for probe.Next() {
		var id identity
		if err := probe.Scan(&id.namespace, &id.trace); err != nil {
			probe.Close()
			return out, exemplarError(ctx, err)
		}
		count++
		if count > 64 {
			out.Truncated = true
			break
		}
		if !seen[id] {
			seen[id] = true
			candidates = append(candidates, "struct_pack(namespace := ?::VARCHAR, trace_id := ?::VARCHAR)")
			rootArgs = append(rootArgs, id.namespace, id.trace)
		}
	}
	err = probe.Err()
	probe.Close()
	if err != nil {
		return out, exemplarError(ctx, err)
	}
	if len(candidates) == 0 {
		return out, nil
	}
	// Keep the bounded keys as constants in a predicate. Joining a VALUES
	// relation lets zero-cardinality Parquet estimates reverse the hash build
	// into every retained span, consuming memory and CPU before returning roots.
	// One aggregate state per candidate trace also bounds root selection memory
	// when a candidate trace has many spans. Prefer a root, then time and span ID,
	// exactly as the trace-row path does, and retain errors from all its spans.
	text := fmt.Sprintf(`WITH roots AS (
SELECT s.namespace,s.trace_id,
arg_min(struct_pack(service := s.service, operation := s.operation, duration_ms := s.duration_ms, status := s.status, start_time := s.start_time),
struct_pack(child := coalesce(s.parent_span_id,'')<>'', start_time := s.start_time, span_id := s.span_id)) AS root,
bool_or(s.status='STATUS_CODE_ERROR') AS has_error
FROM spans s WHERE struct_pack(namespace := s.namespace, trace_id := s.trace_id) IN (%s)
AND s.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
GROUP BY s.namespace,s.trace_id)
SELECT coalesce(trace_id,''),coalesce(namespace,''),coalesce(root.service,''),coalesce(root.operation,''),coalesce(root.duration_ms,0),
CASE WHEN has_error THEN 'STATUS_CODE_ERROR' ELSE coalesce(root.status,'') END,root.start_time::TIMESTAMP_NS,false
FROM roots ORDER BY root.duration_ms DESC,trace_id,namespace LIMIT 21`, strings.Join(candidates, ","))

	rootArgs = append(rootArgs, start, end)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end})
	rows, err := e.engine.QueryContext(ctx, text, rootArgs...)
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
	// A 21st root or a 65th matching row proves truncation; exactly 20 does not.
	return out, nil
}
func traceRootsSQL() string {
	return `SELECT s.trace_id,s.namespace,s.service,s.operation,s.duration_ms,CASE WHEN max(CASE WHEN s.status = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) OVER(PARTITION BY s.namespace,s.trace_id)>0 THEN 'STATUS_CODE_ERROR' ELSE coalesce(s.status,'') END AS status,s.start_time,row_number() OVER(PARTITION BY s.namespace,s.trace_id ORDER BY (coalesce(s.parent_span_id,'')='') DESC,s.start_time,s.span_id) AS n
FROM spans s JOIN candidates c ON s.namespace=c.namespace AND s.trace_id=c.trace_id
WHERE s.start_time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS`
}

// Bound matching trace candidates before the root window functions. Like
// exemplars, ranking is approximate when filters exclude the root span.
// checkedTraceRowsSQL returns the root of each candidate trace, at most limit.
func checkedTraceRowsSQL(candidates, order string, limit int) string {
	return fmt.Sprintf(`WITH candidates AS (%s), roots AS (%s)
SELECT coalesce(trace_id,''),coalesce(namespace,''),coalesce(service,''),coalesce(operation,''),coalesce(duration_ms,0),coalesce(status,''),epoch_ms(start_time::TIMESTAMP_NS)::BIGINT AS start
FROM roots WHERE n=1 ORDER BY %s LIMIT %d`, candidates, traceRootsSQL(), order, limit)
}

func exemplarError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func outsideSelection() error {
	return Problems{{Path: "from", Message: "selection does not overlap the panel window"}}
}
